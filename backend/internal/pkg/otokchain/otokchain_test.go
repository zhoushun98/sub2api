//go:build unit

package otokchain

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTronAddressRoundTrip(t *testing.T) {
	h, err := TronAddressToHex("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")
	require.NoError(t, err)
	require.Equal(t, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", h)

	back, err := TronAddressFromHex("41" + h)
	require.NoError(t, err)
	require.Equal(t, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", back)

	back, err = TronAddressFromHex("0x" + h)
	require.NoError(t, err)
	require.Equal(t, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", back)
}

func TestTronAddressRejectsInvalid(t *testing.T) {
	for _, addr := range []string{
		"",
		"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6u", // checksum 错误
		"tR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", // 首字母大小写错误
		"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6",  // 长度不足
		"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj0t", // 含非法字符 0
		"0xdAC17F958D2ee523a2206206994597C13D831ec7",
	} {
		_, err := TronAddressToHex(addr)
		require.Error(t, err, addr)
	}
}

func TestNormalizeEVMAddress(t *testing.T) {
	norm, err := NormalizeEVMAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	require.NoError(t, err)
	require.Equal(t, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", norm)
	require.Equal(t, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", ChecksumEVMAddress(norm))
	require.Equal(t, "0xdAC17F958D2ee523a2206206994597C13D831ec7", ChecksumEVMAddress("0xdac17f958d2ee523a2206206994597c13d831ec7"))

	// 全小写 / 全大写不做校验和检查
	_, err = NormalizeEVMAddress("0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")
	require.NoError(t, err)
	_, err = NormalizeEVMAddress("0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED")
	require.NoError(t, err)

	for _, bad := range []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD", // 校验和错误
		"5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe",
		"0xZZAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
	} {
		_, err := NormalizeEVMAddress(bad)
		require.Error(t, err, bad)
	}
}

func TestParseAddressRejectsZeroAndContract(t *testing.T) {
	t.Setenv(envTestnet, "")
	_, err := ParseAddress(NetworkERC20, "0x0000000000000000000000000000000000000000")
	require.Error(t, err)
	_, err = ParseAddress(NetworkERC20, "0xdAC17F958D2ee523a2206206994597C13D831ec7")
	require.Error(t, err)
	_, err = ParseAddress(NetworkTRC20, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")
	require.Error(t, err)

	addr, err := ParseAddress(NetworkBEP20, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")
	require.NoError(t, err)
	require.Equal(t, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", addr.Display)
	require.Equal(t, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", addr.Norm)
}

func TestAmountConversions(t *testing.T) {
	raw := CentsToRaw(1403, 18)
	require.Equal(t, "14030000000000000000", raw.String()) // 超出 int64
	cents, exact := RawToCents(raw, 18)
	require.True(t, exact)
	require.EqualValues(t, 1403, cents)

	cents, exact = RawToCents(big.NewInt(14_031_000), 6)
	require.False(t, exact)
	require.EqualValues(t, 1403, cents)

	require.Equal(t, "14.031", FormatRaw(big.NewInt(14_031_000), 6))
	require.Equal(t, "14.00", FormatRaw(big.NewInt(14_000_000), 6))
	require.Equal(t, "14.10", FormatRaw(big.NewInt(14_100_000), 6))
	require.Equal(t, "0.00", FormatRaw(big.NewInt(0), 6))
	require.Equal(t, "0.000001", FormatRaw(big.NewInt(1), 6))
	require.Equal(t, "14.03", FormatCents(1403))
	require.Equal(t, "0.05", FormatCents(5))

	v, err := ParseUint256("0x00000000000000000000000000000000000000000000000000000000000f4240")
	require.NoError(t, err)
	require.EqualValues(t, 1_000_000, v.Int64())
	_, err = ParseUint256("-1")
	require.Error(t, err)
}

func TestNormalizeTxHash(t *testing.T) {
	h := strings.Repeat("Ab", 32)
	evm, err := NormalizeTxHash(NetworkERC20, h)
	require.NoError(t, err)
	require.Equal(t, "0x"+strings.ToLower(h), evm)
	tron, err := NormalizeTxHash(NetworkTRC20, "0x"+h)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(h), tron)
	_, err = NormalizeTxHash(NetworkERC20, "0x1234")
	require.Error(t, err)
}

type rpcCall struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func newRPCServer(t *testing.T, handle func(call rpcCall) (any, *rpcError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call rpcCall
		require.NoError(t, json.Unmarshal(body, &call))
		result, rerr := handle(call)
		resp := map[string]any{"jsonrpc": "2.0", "id": 1}
		if rerr != nil {
			resp["error"] = rerr
		} else {
			resp["result"] = result
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

const (
	testRecipient = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	testSender    = "0x1111111111111111111111111111111111111111"
)

func usdtLog(spec Spec, contract string, to string, value int64, block int64, logIdx int64) map[string]any {
	return map[string]any{
		"address":         contract,
		"topics":          []string{transferEventTopic, addressToTopic(testSender), addressToTopic(to)},
		"data":            "0x" + strings.Repeat("0", 48) + big.NewInt(value).Text(16),
		"blockNumber":     toHexQuantity(block),
		"blockHash":       "0x" + strings.Repeat("b", 64),
		"transactionHash": "0x" + strings.Repeat("c", 63) + "1",
		"logIndex":        toHexQuantity(logIdx),
		"removed":         false,
	}
}

func TestEVMTransferLogsFiltersFakeTokensAndRemoved(t *testing.T) {
	spec, err := SpecFor(NetworkPolygon)
	require.NoError(t, err)
	contract := strings.ToLower(spec.TokenContract)
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) {
		require.Equal(t, "eth_getLogs", call.Method)
		var filter struct {
			Address string `json:"address"`
			Topics  []any  `json:"topics"`
		}
		require.NoError(t, json.Unmarshal(call.Params[0], &filter))
		require.Equal(t, contract, filter.Address)
		good := usdtLog(spec, contract, testRecipient, 14_030_000, 100, 1)
		fake := usdtLog(spec, "0x2222222222222222222222222222222222222222", testRecipient, 14_030_000, 100, 2)
		removed := usdtLog(spec, contract, testRecipient, 14_030_000, 100, 3)
		removed["removed"] = true
		other := usdtLog(spec, contract, testSender, 14_030_000, 100, 4)
		return []any{good, fake, removed, other}, nil
	})
	defer srv.Close()

	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	transfers, err := c.TransferLogs(context.Background(), 1, 200, []string{testRecipient})
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	require.Equal(t, testRecipient, transfers[0].ToNorm)
	require.EqualValues(t, 14_030_000, transfers[0].Amount.Int64())
	require.EqualValues(t, 100, transfers[0].BlockNumber)
	require.EqualValues(t, 1, transfers[0].LogIndex)
}

func TestEVMScanTransfersHalvesChunkOnError(t *testing.T) {
	spec, err := SpecFor(NetworkBEP20)
	require.NoError(t, err)
	var calls atomic.Int32
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) {
		calls.Add(1)
		var filter struct {
			FromBlock string `json:"fromBlock"`
			ToBlock   string `json:"toBlock"`
		}
		require.NoError(t, json.Unmarshal(call.Params[0], &filter))
		from, _ := parseHexInt(filter.FromBlock)
		to, _ := parseHexInt(filter.ToBlock)
		if to-from+1 > 500 {
			return nil, &rpcError{Code: -32005, Message: "block range too large"}
		}
		return []any{}, nil
	})
	defer srv.Close()

	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	res, err := c.ScanTransfers(context.Background(), 1, 1000, []string{testRecipient}, 2000, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1000, res.ScannedTo)
	require.LessOrEqual(t, res.NextChunk, int64(751))
	require.Greater(t, calls.Load(), int32(2))
}

func TestEVMScanTransfersShrinksToSingleBlock(t *testing.T) {
	spec, err := SpecFor(NetworkPolygon)
	require.NoError(t, err)
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) {
		var filter struct {
			FromBlock string `json:"fromBlock"`
			ToBlock   string `json:"toBlock"`
		}
		require.NoError(t, json.Unmarshal(call.Params[0], &filter))
		from, _ := parseHexInt(filter.FromBlock)
		to, _ := parseHexInt(filter.ToBlock)
		if to > from {
			return nil, &rpcError{Code: -32005, Message: "query returned more than 10000 results"}
		}
		return []any{}, nil
	})
	defer srv.Close()

	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	res, err := c.ScanTransfers(context.Background(), 1, 3, []string{testRecipient}, 8, 20)
	require.NoError(t, err)
	require.EqualValues(t, 3, res.ScannedTo)
}

func TestEVMEnsureChainIDMismatch(t *testing.T) {
	spec, err := SpecFor(NetworkERC20)
	require.NoError(t, err)
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) {
		return "0x38", nil // 56 = BSC
	})
	defer srv.Close()
	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	require.ErrorContains(t, c.EnsureChainID(context.Background()), "does not match")
}

func TestEVMReceipt(t *testing.T) {
	spec, err := SpecFor(NetworkERC20)
	require.NoError(t, err)
	contract := strings.ToLower(spec.TokenContract)
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) {
		require.Equal(t, "eth_getTransactionReceipt", call.Method)
		return map[string]any{
			"status":      "0x1",
			"blockNumber": "0x64",
			"blockHash":   "0x" + strings.Repeat("B", 64),
			"logs":        []any{usdtLog(spec, contract, testRecipient, 5_000_000, 100, 7)},
		}, nil
	})
	defer srv.Close()
	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	rc, err := c.Receipt(context.Background(), "0x"+strings.Repeat("c", 63)+"1")
	require.NoError(t, err)
	require.True(t, rc.Success)
	require.EqualValues(t, 100, rc.BlockNumber)
	require.Equal(t, "0x"+strings.Repeat("b", 64), rc.BlockHash)
	require.Len(t, rc.Transfers, 1)
	require.EqualValues(t, 7, rc.Transfers[0].LogIndex)
}

func TestEVMReceiptNotFound(t *testing.T) {
	spec, err := SpecFor(NetworkERC20)
	require.NoError(t, err)
	srv := newRPCServer(t, func(call rpcCall) (any, *rpcError) { return nil, nil })
	defer srv.Close()
	c, err := NewEVMClient(spec, srv.URL, srv.Client())
	require.NoError(t, err)
	_, err = c.Receipt(context.Background(), "0x"+strings.Repeat("c", 64))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestTronIncomingTransfers(t *testing.T) {
	spec, err := SpecFor(NetworkTRC20)
	require.NoError(t, err)
	recipient, err := TronAddressFromHex(strings.Repeat("2", 40))
	require.NoError(t, err)
	tx := strings.Repeat("a", 64)
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("TRON-PRO-API-KEY")
		require.Equal(t, "/v1/accounts/"+recipient+"/transactions/trc20", r.URL.Path)
		require.Equal(t, "true", r.URL.Query().Get("only_confirmed"))
		require.Equal(t, spec.TokenContract, r.URL.Query().Get("contract_address"))
		item := func(value string, contract string, to string) map[string]any {
			return map[string]any{
				"transaction_id":  tx,
				"token_info":      map[string]any{"address": contract, "decimals": 6},
				"block_timestamp": 1_700_000_000_000,
				"from":            "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf",
				"to":              to,
				"type":            "Transfer",
				"value":           value,
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": []any{
				item("14030000", spec.TokenContract, recipient),
				item("2000000", spec.TokenContract, recipient),
				item("14030000", "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf", recipient), // 假币
			},
			"meta": map[string]any{},
		})
	}))
	defer srv.Close()

	c, err := NewTronClient(spec, srv.URL, "secret-key", srv.Client())
	require.NoError(t, err)
	transfers, maxTs, more, err := c.IncomingTransfers(context.Background(), recipient, 1, 3)
	require.NoError(t, err)
	require.False(t, more)
	require.Equal(t, "secret-key", gotKey)
	require.Len(t, transfers, 2)
	require.EqualValues(t, 1_700_000_000_000, maxTs)
	// 同一交易内按金额升序编号
	for _, tr := range transfers {
		if tr.Amount.Int64() == 2_000_000 {
			require.EqualValues(t, 0, tr.LogIndex)
		} else {
			require.EqualValues(t, 1, tr.LogIndex)
		}
	}
	require.Equal(t, time.UnixMilli(1_700_000_000_000).UTC(), transfers[0].BlockTime)
}

func TestTronIncomingTransfersReportsTruncation(t *testing.T) {
	spec, err := SpecFor(NetworkTRC20)
	require.NoError(t, err)
	recipient, err := TronAddressFromHex(strings.Repeat("2", 40))
	require.NoError(t, err)
	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := pages.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": []any{map[string]any{
				"transaction_id":  strings.Repeat(string(rune('a'+n)), 64),
				"token_info":      map[string]any{"address": spec.TokenContract, "decimals": 6},
				"block_timestamp": 1_700_000_000_000 + int64(n),
				"from":            "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf",
				"to":              recipient,
				"type":            "Transfer",
				"value":           "1000000",
			}},
			"meta": map[string]any{"fingerprint": "next"},
		})
	}))
	defer srv.Close()

	c, err := NewTronClient(spec, srv.URL, "", srv.Client())
	require.NoError(t, err)
	transfers, maxTs, more, err := c.IncomingTransfers(context.Background(), recipient, 1, 2)
	require.NoError(t, err)
	require.True(t, more)
	require.Len(t, transfers, 2)
	require.EqualValues(t, 1_700_000_000_002, maxTs)
	require.EqualValues(t, 2, pages.Load())
}

func TestTronTransactionParsesLogs(t *testing.T) {
	spec, err := SpecFor(NetworkTRC20)
	require.NoError(t, err)
	recipient, err := TronAddressFromHex(strings.Repeat("3", 40))
	require.NoError(t, err)
	recipientHex, err := TronAddressToHex(recipient)
	require.NoError(t, err)
	contractHex, err := TronAddressToHex(spec.TokenContract)
	require.NoError(t, err)
	tx := strings.Repeat("d", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/walletsolidity/gettransactioninfobyid" {
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		require.Equal(t, "/wallet/gettransactioninfobyid", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             tx,
			"blockNumber":    123,
			"blockTimeStamp": 1_700_000_000_000,
			"receipt":        map[string]any{"result": "SUCCESS"},
			"log": []any{map[string]any{
				"address": contractHex,
				"topics":  []string{strings.TrimPrefix(transferEventTopic, "0x"), strings.Repeat("0", 24) + strings.Repeat("1", 40), strings.Repeat("0", 24) + recipientHex},
				"data":    strings.Repeat("0", 58) + big.NewInt(14_030_000).Text(16),
			}},
		})
	}))
	defer srv.Close()

	c, err := NewTronClient(spec, srv.URL, "", srv.Client())
	require.NoError(t, err)
	res, err := c.Transaction(context.Background(), tx, recipient)
	require.NoError(t, err)
	require.False(t, res.Confirmed)
	require.True(t, res.Success)
	require.Len(t, res.Transfers, 1)
	require.EqualValues(t, 14_030_000, res.Transfers[0].Amount.Int64())
	require.Equal(t, recipient, res.Transfers[0].ToNorm)
}

func TestTestnetRequiresContract(t *testing.T) {
	t.Setenv(envTestnet, "1")
	t.Setenv("OTOK_USDT_TESTNET_ERC20_CONTRACT", "")
	_, err := SpecFor(NetworkERC20)
	require.Error(t, err)
	t.Setenv("OTOK_USDT_TESTNET_ERC20_CONTRACT", "0x1111111111111111111111111111111111111111")
	spec, err := SpecFor(NetworkERC20)
	require.NoError(t, err)
	require.EqualValues(t, 11155111, spec.ChainID)
	tron, err := SpecFor(NetworkTRC20)
	require.NoError(t, err)
	require.Equal(t, "https://nile.trongrid.io", tron.DefaultEndpoint)
}
