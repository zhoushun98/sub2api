package otokchain

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TronClient 通过 TronGrid 读取 TRC20 USDT 转账。
type TronClient struct {
	spec        Spec
	base        string
	apiKey      string
	hc          *http.Client
	contractB58 string
	contractHex string // 20 字节小写 hex
}

// NewTronClient 创建 TronGrid 客户端；endpoint 为空时使用网络默认地址。
func NewTronClient(spec Spec, endpoint, apiKey string, hc *http.Client) (*TronClient, error) {
	if spec.Kind != KindTron {
		return nil, fmt.Errorf("network %s is not TRON", spec.Network)
	}
	contractHex, err := NormalizeTokenContract(spec)
	if err != nil {
		return nil, fmt.Errorf("token contract: %w", err)
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		endpoint = spec.DefaultEndpoint
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &TronClient{
		spec: spec, base: endpoint, apiKey: strings.TrimSpace(apiKey), hc: hc,
		contractB58: spec.TokenContract, contractHex: contractHex,
	}, nil
}

// Spec 返回客户端对应的网络参数。
func (c *TronClient) Spec() Spec { return c.spec }

func (c *TronClient) header() http.Header {
	h := http.Header{}
	if c.apiKey != "" {
		h.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	return h
}

type tronTRC20Item struct {
	TransactionID string `json:"transaction_id"`
	TokenInfo     struct {
		Address  string `json:"address"`
		Decimals int    `json:"decimals"`
	} `json:"token_info"`
	BlockTimestamp int64  `json:"block_timestamp"`
	From           string `json:"from"`
	To             string `json:"to"`
	Type           string `json:"type"`
	Value          string `json:"value"`
}

type tronTRC20Page struct {
	Data    []tronTRC20Item `json:"data"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Meta    struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"meta"`
}

// IncomingTransfers 拉取 minTimestampMs 之后已固化（only_confirmed）的转入 USDT 转账，
// 按区块时间升序，最多翻 maxPages 页。返回转账以及本次见到的最大区块时间（毫秒）。
func (c *TronClient) IncomingTransfers(ctx context.Context, toAddr string, minTimestampMs int64, maxPages int) ([]Transfer, int64, error) {
	if maxPages <= 0 {
		maxPages = 1
	}
	var (
		out         []Transfer
		maxTs       = minTimestampMs
		fingerprint string
	)
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("only_to", "true")
		q.Set("only_confirmed", "true")
		q.Set("limit", "200")
		q.Set("contract_address", c.contractB58)
		q.Set("order_by", "block_timestamp,asc")
		if minTimestampMs > 0 {
			q.Set("min_timestamp", strconv.FormatInt(minTimestampMs, 10))
		}
		if fingerprint != "" {
			q.Set("fingerprint", fingerprint)
		}
		endpoint := fmt.Sprintf("%s/v1/accounts/%s/transactions/trc20?%s", c.base, url.PathEscape(toAddr), q.Encode())
		var resp tronTRC20Page
		if err := doJSON(ctx, c.hc, http.MethodGet, endpoint, c.header(), nil, &resp); err != nil {
			return out, maxTs, fmt.Errorf("trongrid trc20 list: %w", err)
		}
		if !resp.Success {
			return out, maxTs, fmt.Errorf("trongrid trc20 list: %s", strings.TrimSpace(resp.Error))
		}
		for _, item := range resp.Data {
			if item.Type != "Transfer" || item.TokenInfo.Address != c.contractB58 || item.To != toAddr {
				continue
			}
			amount, err := ParseUint256(item.Value)
			if err != nil {
				continue
			}
			txHash, err := NormalizeTxHash(c.spec.Network, item.TransactionID)
			if err != nil {
				continue
			}
			out = append(out, Transfer{
				Network:   c.spec.Network,
				TxHash:    txHash,
				BlockTime: time.UnixMilli(item.BlockTimestamp).UTC(),
				From:      item.From,
				ToNorm:    item.To,
				Amount:    amount,
			})
			if item.BlockTimestamp > maxTs {
				maxTs = item.BlockTimestamp
			}
		}
		fingerprint = resp.Meta.Fingerprint
		if fingerprint == "" || len(resp.Data) == 0 {
			break
		}
	}
	assignTronLogIndexes(out)
	return out, maxTs, nil
}

type tronTxInfo struct {
	ID             string `json:"id"`
	BlockNumber    int64  `json:"blockNumber"`
	BlockTimeStamp int64  `json:"blockTimeStamp"`
	Receipt        struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Result string `json:"result"`
	Log    []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"log"`
}

// TronTxResult 是按交易哈希查询的结果。
type TronTxResult struct {
	// Confirmed=false 表示交易已上链但尚未固化。
	Confirmed   bool
	Success     bool
	BlockNumber int64
	BlockTime   time.Time
	Transfers   []Transfer
}

func (c *TronClient) txInfo(ctx context.Context, path, txHash string) (*tronTxInfo, error) {
	var info tronTxInfo
	if err := doJSON(ctx, c.hc, http.MethodPost, c.base+path, c.header(), map[string]any{"value": txHash}, &info); err != nil {
		return nil, err
	}
	if info.ID == "" {
		return nil, ErrNotFound
	}
	return &info, nil
}

// Transaction 按哈希查询交易，只解析转入 recipient（base58）的 USDT Transfer 日志。
// 先查固化节点，查不到再查全节点判断是否仍在确认中；都查不到返回 ErrNotFound。
func (c *TronClient) Transaction(ctx context.Context, txHash, recipient string) (*TronTxResult, error) {
	norm, err := NormalizeTxHash(c.spec.Network, txHash)
	if err != nil {
		return nil, err
	}
	confirmed := true
	info, err := c.txInfo(ctx, "/walletsolidity/gettransactioninfobyid", norm)
	if errors.Is(err, ErrNotFound) {
		confirmed = false
		info, err = c.txInfo(ctx, "/wallet/gettransactioninfobyid", norm)
	}
	if err != nil {
		return nil, err
	}
	res := &TronTxResult{
		Confirmed:   confirmed,
		Success:     info.Receipt.Result == "SUCCESS" && info.Result != "FAILED",
		BlockNumber: info.BlockNumber,
		BlockTime:   time.UnixMilli(info.BlockTimeStamp).UTC(),
	}
	for _, l := range info.Log {
		if strings.ToLower(strings.TrimPrefix(l.Address, "41")) != c.contractHex && strings.ToLower(l.Address) != c.contractHex {
			continue
		}
		if len(l.Topics) != 3 || "0x"+strings.TrimPrefix(strings.ToLower(l.Topics[0]), "0x") != transferEventTopic {
			continue
		}
		fromHex, ok1 := topicToAddress(l.Topics[1])
		toHex, ok2 := topicToAddress(l.Topics[2])
		if !ok1 || !ok2 {
			continue
		}
		to, err := TronAddressFromHex(toHex)
		if err != nil || to != recipient {
			continue
		}
		from, _ := TronAddressFromHex(fromHex)
		amount, err := ParseUint256("0x" + strings.TrimPrefix(l.Data, "0x"))
		if err != nil {
			continue
		}
		res.Transfers = append(res.Transfers, Transfer{
			Network:     c.spec.Network,
			TxHash:      norm,
			BlockNumber: info.BlockNumber,
			BlockTime:   res.BlockTime,
			From:        from,
			ToNorm:      to,
			Amount:      amount,
		})
	}
	assignTronLogIndexes(res.Transfers)
	return res, nil
}
