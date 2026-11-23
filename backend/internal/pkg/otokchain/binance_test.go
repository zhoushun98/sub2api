//go:build unit

package otokchain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testBinanceEVMAddr  = "0xbd7c05c59bf79dc572a3b80fda9f798355bcfa2d"
	testBinanceTronAddr = "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"
)

// 币安官方文档的 HMAC 签名示例。
func TestBinanceSignVector(t *testing.T) {
	sig := binanceSign("NhqPtmdSJYdKjVHjA7PZj4Mge3R5YNiP1e3UZjInClVN65XAbvqqM6A7H5fATj0j",
		"symbol=LTCBTC&side=BUY&type=LIMIT&timeInForce=GTC&quantity=1&price=0.1&recvWindow=5000&timestamp=1499827319559")
	require.Equal(t, "c8db56825ae71d6d79447849e617115f4a920fa2acdcab2b053c4b2838bd6b71", sig)
}

func TestBinanceEncodeKeepsOrderAndEncodesSpaceAsPercent20(t *testing.T) {
	q := binanceEncode([][2]string{{"txId", "Off-chain transfer 1"}, {"coin", "USDT"}, {"x", "a+b&c"}})
	require.Equal(t, "txId=Off-chain%20transfer%201&coin=USDT&x=a%2Bb%26c", q)
}

func TestBinanceNetwork(t *testing.T) {
	for code, want := range map[string]Network{"BSC": NetworkBEP20, "eth": NetworkERC20, "MATIC": NetworkPolygon, " TRX ": NetworkTRC20} {
		n, ok := BinanceNetwork(code)
		require.True(t, ok, code)
		require.Equal(t, want, n, code)
	}
	for _, code := range []string{"POL", "PLASMA", "SOL", ""} {
		_, ok := BinanceNetwork(code)
		require.False(t, ok, code)
	}
}

func TestParseOffChainID(t *testing.T) {
	for in, want := range map[string]string{
		"434226888519":                      "434226888519",
		"Off-chain transfer 434226888519":   "434226888519",
		"off-chain transfer  434226888519 ": "434226888519",
		"链下转账 434226888519":                 "434226888519",
		"链下转账434226888519":                  "434226888519",
		"链下转账：434226888519":                 "434226888519",
		"　链下转账　434226888519　":               "434226888519",
		"Internal transfer 434226888519":    "434226888519",
	} {
		got, ok := ParseOffChainID(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{
		"", "12345", "0x7288040170cf17b5b8a1cf0c6c5c27e246a741adfef9e822382bc446137636ea",
		"Off-chain transfer", "transfer 434226888519", "4342268885 19", "434226888519; drop table",
	} {
		_, ok := ParseOffChainID(in)
		require.False(t, ok, in)
	}
}

func TestParseDecimalRaw(t *testing.T) {
	v, err := ParseDecimalRaw("7.52", 18)
	require.NoError(t, err)
	require.Equal(t, "7520000000000000000", v.String())
	cents, exact := RawToCents(v, 18)
	require.True(t, exact)
	require.EqualValues(t, 752, cents)

	v, err = ParseDecimalRaw("9.935984", 6)
	require.NoError(t, err)
	_, exact = RawToCents(v, 6)
	require.False(t, exact)

	v, err = ParseDecimalRaw("210", 6)
	require.NoError(t, err)
	require.Equal(t, "210000000", v.String())

	for _, bad := range []string{"", ".5", "-1", "1e3", "1.1234567", "abc", "0x10", "1.2.3"} {
		_, err := ParseDecimalRaw(bad, 6)
		require.Error(t, err, bad)
	}
}

func TestBinanceDepositTransfer(t *testing.T) {
	off := BinanceDeposit{
		ID: "5326511839180757249", Amount: "7.52", Coin: "USDT", Network: "BSC", Status: 1,
		Address: testBinanceEVMAddr, TxID: "Off-chain transfer 434226888519", SourceAddress: "434226888519",
		InsertTime: 1795443246000, TransferType: 1,
	}
	require.True(t, off.OffChain())
	require.True(t, off.Credited())
	require.Equal(t, "434226888519", off.LookupKey())
	tr, err := off.Transfer(NetworkBEP20, -1)
	require.NoError(t, err)
	require.Equal(t, "Off-chain transfer 434226888519", tr.TxHash)
	require.EqualValues(t, -1, tr.LogIndex)
	require.Equal(t, testBinanceEVMAddr, tr.ToNorm)
	require.Equal(t, "434226888519", tr.From)
	require.Equal(t, int64(1795443246000), tr.BlockTime.UnixMilli())
	cents, exact := RawToCents(tr.Amount, 18)
	require.True(t, exact)
	require.EqualValues(t, 752, cents)

	on := BinanceDeposit{
		ID: "1", Amount: "270.33", Network: "TRX", Status: 6, Address: testBinanceTronAddr,
		TxID: "B902C82D6E2EF4F6F7637612A0F2CE3DF85141BD0446B80034C7D1B47DD3D79C", InsertTime: 1, TransferType: 0,
	}
	require.False(t, on.OffChain())
	require.True(t, on.Credited())
	tr, err = on.Transfer(NetworkTRC20, -1)
	require.NoError(t, err)
	require.Equal(t, "b902c82d6e2ef4f6f7637612a0f2ce3df85141bd0446b80034c7d1b47dd3d79c", tr.TxHash)
	require.Equal(t, on.TxID, on.LookupKey())

	// 不能入账 / 不完整的记录。
	require.False(t, BinanceDeposit{Status: 0}.Credited())
	require.False(t, BinanceDeposit{Status: 1, TravelRuleStatus: 1}.Credited())
	require.True(t, BinanceDeposit{Status: 1, TravelRuleStatus: 1}.NeedsUserAction())
	require.True(t, BinanceDeposit{Status: 8}.NeedsUserAction())
	require.True(t, BinanceDeposit{Status: 2}.Rejected())
	require.True(t, BinanceDeposit{Status: 7}.Rejected())
	bad := off
	bad.Amount = "7.5x"
	_, err = bad.Transfer(NetworkBEP20, -1)
	require.Error(t, err)
	bad = off
	bad.Address = "not-an-address"
	_, err = bad.Transfer(NetworkBEP20, -1)
	require.Error(t, err)
	bad = on
	bad.TxID = "zz"
	_, err = bad.Transfer(NetworkTRC20, -1)
	require.Error(t, err)
	bad = off
	bad.InsertTime = 0
	_, err = bad.Transfer(NetworkBEP20, -1)
	require.Error(t, err)
}

func TestBinanceClientSignsAndPaginates(t *testing.T) {
	const key, secret = "test-key", "test-secret"
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, binanceDepositPath, r.URL.Path)
		require.Equal(t, key, r.Header.Get("X-MBX-APIKEY"))
		raw := r.URL.RawQuery
		idx := strings.LastIndex(raw, "&signature=")
		require.Positive(t, idx)
		require.Equal(t, binanceSign(secret, raw[:idx]), raw[idx+len("&signature="):], "signature must cover the exact query sent")
		q := r.URL.Query()
		require.Equal(t, "USDT", q.Get("coin"))
		require.Equal(t, "true", q.Get("includeSource"))
		require.Equal(t, "10000", q.Get("recvWindow"))
		calls = append(calls, q.Get("offset")+"|"+q.Get("txId"))
		n := 2
		if q.Get("offset") == "0" && q.Get("txId") == "" {
			n = binanceDepositPageLimit
		}
		out := make([]BinanceDeposit, n)
		for i := range out {
			out[i] = BinanceDeposit{ID: fmt.Sprint(i), Amount: "1.00", Network: "BSC", Status: 1}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c, err := NewBinanceClient(srv.URL, key, secret, srv.Client())
	require.NoError(t, err)
	end := time.Now()
	list, err := c.Deposits(context.Background(), end.Add(-200*24*time.Hour), end)
	require.NoError(t, err)
	require.Len(t, list, binanceDepositPageLimit+2)
	require.Equal(t, []string{"0|", "1000|"}, calls)

	calls = nil
	list, err = c.DepositsByTxID(context.Background(), "Off-chain transfer 1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, []string{"|Off-chain transfer 1"}, calls)
}

func TestBinanceClientErrorsDoNotLeakSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`))
	}))
	defer srv.Close()
	c, err := NewBinanceClient(srv.URL, "visible-key", "hidden-secret", srv.Client())
	require.NoError(t, err)
	_, err = c.Deposits(context.Background(), time.Now().Add(-time.Hour), time.Now())
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "-2015")
	require.Contains(t, msg, "HTTP 401")
	for _, leak := range []string{"signature", "hidden-secret", "visible-key", "timestamp="} {
		require.NotContains(t, msg, leak)
	}

	_, err = NewBinanceClient("", "", "x", nil)
	require.Error(t, err)
	_, err = NewBinanceClient("ftp://x", "k", "s", nil)
	require.Error(t, err)
}
