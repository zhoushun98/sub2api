package otokchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// Transfer 是一笔转入收款地址的 USDT 转账。
type Transfer struct {
	Network     Network
	TxHash      string // NormalizeTxHash 形式
	LogIndex    int64
	BlockNumber int64
	BlockHash   string
	// BlockTime 零值表示未知（部分 EVM 节点的日志不带时间戳）。
	BlockTime time.Time
	From      string
	ToNorm    string
	Amount    *big.Int
}

// ErrNotFound 表示链上查不到对应交易或区块。
var ErrNotFound = errors.New("not found on chain")

const maxResponseBytes = 8 << 20

// doJSON 发送请求并把 JSON 响应解码到 out。错误信息不包含请求 URL，
// 避免把嵌在 RPC 地址里的 API key 写进日志。
func doJSON(ctx context.Context, hc *http.Client, method, endpoint string, header http.Header, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return errors.New("invalid endpoint url")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return fmt.Errorf("request to %s failed: %w", req.URL.Host, uerr.Err)
		}
		return fmt.Errorf("request to %s failed: %w", req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response from %s: %w", req.URL.Host, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := raw
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("%s responded HTTP %d: %s", req.URL.Host, resp.StatusCode, bytes.TrimSpace(snippet))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", req.URL.Host, err)
	}
	return nil
}

// assignTronLogIndexes 为 TRON 转账分配稳定的同交易序号：同一交易内转入同一地址的多笔
// 转账按 (金额, 付款方) 排序编号。watcher 与 TxHash 校验两条路径用同一规则，保证去重键一致。
func assignTronLogIndexes(transfers []Transfer) {
	groups := map[string][]int{}
	for i := range transfers {
		key := transfers[i].TxHash + "|" + transfers[i].ToNorm
		groups[key] = append(groups[key], i)
	}
	for _, idxs := range groups {
		sort.SliceStable(idxs, func(a, b int) bool {
			ta, tb := transfers[idxs[a]], transfers[idxs[b]]
			if c := ta.Amount.Cmp(tb.Amount); c != 0 {
				return c < 0
			}
			return ta.From < tb.From
		})
		for ord, i := range idxs {
			transfers[i].LogIndex = int64(ord)
		}
	}
}
