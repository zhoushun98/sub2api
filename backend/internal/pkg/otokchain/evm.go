package otokchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// transferEventTopic = keccak256("Transfer(address,address,uint256)")。
const transferEventTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

const (
	// DefaultEVMChunk 是 eth_getLogs 单次扫描的初始区块跨度。
	DefaultEVMChunk int64 = 2000
	// minEVMChunk 允许降到单个区块：收款地址被大量垃圾转账刷屏时，节点可能连很小的跨度都返回超限。
	minEVMChunk int64 = 1
	maxEVMChunk int64 = 5000
)

// EVMClient 通过标准 JSON-RPC 读取 ERC20 USDT 转账。
type EVMClient struct {
	spec     Spec
	endpoint string
	hc       *http.Client
	contract string // 小写 0x 地址

	seq         atomic.Int64
	chainMu     sync.Mutex
	chainVerify bool
}

// NewEVMClient 创建 EVM 客户端；endpoint 为空时使用网络默认 RPC。
func NewEVMClient(spec Spec, endpoint string, hc *http.Client) (*EVMClient, error) {
	if spec.Kind != KindEVM {
		return nil, fmt.Errorf("network %s is not EVM", spec.Network)
	}
	contract, err := NormalizeTokenContract(spec)
	if err != nil {
		return nil, fmt.Errorf("token contract: %w", err)
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = spec.DefaultEndpoint
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &EVMClient{spec: spec, endpoint: endpoint, hc: hc, contract: contract}, nil
}

// Spec 返回客户端对应的网络参数。
func (c *EVMClient) Spec() Spec { return c.spec }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

func (c *EVMClient) call(ctx context.Context, method string, params []any, out any) error {
	req := map[string]any{"jsonrpc": "2.0", "id": c.seq.Add(1), "method": method, "params": params}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := doJSON(ctx, c.hc, http.MethodPost, c.endpoint, nil, req, &resp); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %w", method, resp.Error)
	}
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return ErrNotFound
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

func parseHexInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return 0, fmt.Errorf("invalid hex quantity: %q", s)
	}
	if len(s) == 2 {
		return 0, nil
	}
	return strconv.ParseInt(s[2:], 16, 64)
}

func toHexQuantity(n int64) string { return "0x" + strconv.FormatInt(n, 16) }

// EnsureChainID 首次调用时校验 RPC 的 chainId，防止把 BSC 节点填到以太坊等配置错误。
func (c *EVMClient) EnsureChainID(ctx context.Context) error {
	c.chainMu.Lock()
	defer c.chainMu.Unlock()
	if c.chainVerify {
		return nil
	}
	var hexID string
	if err := c.call(ctx, "eth_chainId", []any{}, &hexID); err != nil {
		return err
	}
	id, err := parseHexInt(hexID)
	if err != nil {
		return fmt.Errorf("eth_chainId: %w", err)
	}
	if id != c.spec.ChainID {
		return fmt.Errorf("rpc chainId %d does not match %s (expected %d)", id, c.spec.DisplayName, c.spec.ChainID)
	}
	c.chainVerify = true
	return nil
}

// BlockNumber 返回最新区块高度。
func (c *EVMClient) BlockNumber(ctx context.Context) (int64, error) {
	var hexNum string
	if err := c.call(ctx, "eth_blockNumber", []any{}, &hexNum); err != nil {
		return 0, err
	}
	return parseHexInt(hexNum)
}

// Block 返回区块哈希与时间。
func (c *EVMClient) Block(ctx context.Context, number int64) (string, time.Time, error) {
	var blk struct {
		Hash      string `json:"hash"`
		Timestamp string `json:"timestamp"`
	}
	if err := c.call(ctx, "eth_getBlockByNumber", []any{toHexQuantity(number), false}, &blk); err != nil {
		return "", time.Time{}, err
	}
	ts, err := parseHexInt(blk.Timestamp)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("block timestamp: %w", err)
	}
	return strings.ToLower(blk.Hash), time.Unix(ts, 0).UTC(), nil
}

type evmLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	BlockHash       string   `json:"blockHash"`
	BlockTimestamp  string   `json:"blockTimestamp"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
	Removed         bool     `json:"removed"`
}

func topicToAddress(topic string) (string, bool) {
	t := strings.ToLower(strings.TrimPrefix(topic, "0x"))
	if len(t) != 64 || strings.Trim(t[:24], "0") != "" {
		return "", false
	}
	return "0x" + t[24:], true
}

func addressToTopic(norm string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(norm, "0x")
}

// decodeTransferLog 解析 USDT Transfer 日志；非 USDT 合约、非 Transfer 事件或格式异常时返回 false。
func (c *EVMClient) decodeTransferLog(l evmLog) (Transfer, bool) {
	if l.Removed || strings.ToLower(l.Address) != c.contract || len(l.Topics) != 3 ||
		strings.ToLower(l.Topics[0]) != transferEventTopic {
		return Transfer{}, false
	}
	from, ok1 := topicToAddress(l.Topics[1])
	to, ok2 := topicToAddress(l.Topics[2])
	if !ok1 || !ok2 {
		return Transfer{}, false
	}
	amount, err := ParseUint256(l.Data)
	if err != nil {
		return Transfer{}, false
	}
	blockNum, err1 := parseHexInt(l.BlockNumber)
	logIdx, err2 := parseHexInt(l.LogIndex)
	txHash, err3 := NormalizeTxHash(c.spec.Network, l.TransactionHash)
	if err1 != nil || err2 != nil || err3 != nil {
		return Transfer{}, false
	}
	t := Transfer{
		Network:     c.spec.Network,
		TxHash:      txHash,
		LogIndex:    logIdx,
		BlockNumber: blockNum,
		BlockHash:   strings.ToLower(l.BlockHash),
		From:        ChecksumEVMAddress(from),
		ToNorm:      to,
		Amount:      amount,
	}
	if l.BlockTimestamp != "" {
		if ts, err := parseHexInt(l.BlockTimestamp); err == nil && ts > 0 {
			t.BlockTime = time.Unix(ts, 0).UTC()
		}
	}
	return t, true
}

// TransferLogs 查询 [from, to] 区块内转入任一 recipients（小写地址）的 USDT 转账。
func (c *EVMClient) TransferLogs(ctx context.Context, from, to int64, recipients []string) ([]Transfer, error) {
	if len(recipients) == 0 || from > to {
		return nil, nil
	}
	recipientTopics := make([]string, 0, len(recipients))
	wanted := make(map[string]bool, len(recipients))
	for _, r := range recipients {
		recipientTopics = append(recipientTopics, addressToTopic(r))
		wanted[r] = true
	}
	filter := map[string]any{
		"fromBlock": toHexQuantity(from),
		"toBlock":   toHexQuantity(to),
		"address":   c.contract,
		"topics":    []any{transferEventTopic, nil, recipientTopics},
	}
	var logs []evmLog
	if err := c.call(ctx, "eth_getLogs", []any{filter}, &logs); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Transfer, 0, len(logs))
	for _, l := range logs {
		t, ok := c.decodeTransferLog(l)
		if ok && wanted[t.ToNorm] {
			out = append(out, t)
		}
	}
	return out, nil
}

// ScanResult 是一次分段扫描的结果。
type ScanResult struct {
	Transfers []Transfer
	// ScannedTo 为已完整扫描的最高区块；小于起点表示本轮没有推进。
	ScannedTo int64
	// NextChunk 为下次建议使用的分段跨度。
	NextChunk int64
}

// ScanTransfers 从 fromBlock 扫描到 toBlock（含），单次调用最多 maxCalls 段。
// RPC 报错（多为跨度或结果数超限）时跨度减半重试；连续成功则逐步放大。
func (c *EVMClient) ScanTransfers(ctx context.Context, fromBlock, toBlock int64, recipients []string, chunk int64, maxCalls int) (ScanResult, error) {
	if chunk <= 0 {
		chunk = DefaultEVMChunk
	}
	res := ScanResult{ScannedTo: fromBlock - 1, NextChunk: chunk}
	cur := fromBlock
	for calls := 0; cur <= toBlock && calls < maxCalls; calls++ {
		end := cur + chunk - 1
		if end > toBlock {
			end = toBlock
		}
		logs, err := c.TransferLogs(ctx, cur, end, recipients)
		if err != nil {
			if ctx.Err() != nil || chunk <= minEVMChunk {
				res.NextChunk = chunk
				return res, err
			}
			chunk /= 2
			if chunk < minEVMChunk {
				chunk = minEVMChunk
			}
			continue
		}
		res.Transfers = append(res.Transfers, logs...)
		res.ScannedTo = end
		fullChunk := end-cur+1 == chunk
		cur = end + 1
		if fullChunk && chunk < maxEVMChunk {
			chunk = chunk*3/2 + 1 // +1 保证跨度降到 1 后仍能逐步放大
			if chunk > maxEVMChunk {
				chunk = maxEVMChunk
			}
		}
	}
	res.NextChunk = chunk
	return res, nil
}

// EVMReceipt 是交易回执中与 USDT 相关的信息。
type EVMReceipt struct {
	Success     bool
	BlockNumber int64
	BlockHash   string
	Transfers   []Transfer
}

// Receipt 查询交易回执；交易未上链或不存在时返回 ErrNotFound。
func (c *EVMClient) Receipt(ctx context.Context, txHash string) (*EVMReceipt, error) {
	norm, err := NormalizeTxHash(c.spec.Network, txHash)
	if err != nil {
		return nil, err
	}
	var rc struct {
		Status      string   `json:"status"`
		BlockNumber string   `json:"blockNumber"`
		BlockHash   string   `json:"blockHash"`
		Logs        []evmLog `json:"logs"`
	}
	if err := c.call(ctx, "eth_getTransactionReceipt", []any{norm}, &rc); err != nil {
		return nil, err
	}
	if rc.BlockNumber == "" {
		return nil, ErrNotFound
	}
	blockNum, err := parseHexInt(rc.BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("receipt blockNumber: %w", err)
	}
	status, _ := parseHexInt(rc.Status)
	out := &EVMReceipt{Success: status == 1, BlockNumber: blockNum, BlockHash: strings.ToLower(rc.BlockHash)}
	for _, l := range rc.Logs {
		if l.BlockNumber == "" {
			l.BlockNumber = rc.BlockNumber
		}
		if l.BlockHash == "" {
			l.BlockHash = rc.BlockHash
		}
		if l.TransactionHash == "" {
			l.TransactionHash = norm
		}
		if t, ok := c.decodeTransferLog(l); ok {
			out.Transfers = append(out.Transfers, t)
		}
	}
	return out, nil
}
