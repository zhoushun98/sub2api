package otokchain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 币安只读 API：读取站长币安账户的 USDT 充值记录（含链上充值与币安用户间的链下转账）。

// BinanceDefaultBaseURL 是币安现货 / 钱包接口的默认地址。
const BinanceDefaultBaseURL = "https://api.binance.com"

const (
	binanceDepositPath      = "/sapi/v1/capital/deposit/hisrec"
	binanceDepositPageLimit = 1000
	binanceDepositMaxPages  = 3
	binanceRecvWindowMs     = 10000
	// binanceMaxRange：接口要求 startTime 与 endTime 相差不足 90 天。
	binanceMaxRange = 89 * 24 * time.Hour
	// binanceTxIDMaxLen 与 otok_usdt_transfers.tx_hash 的列宽一致。
	binanceTxIDMaxLen = 80
)

// 币安充值状态（/sapi/v1/capital/deposit/hisrec 的 status 字段）。
const (
	BinanceDepositPending        = 0 // 处理中
	BinanceDepositSuccess        = 1 // 成功
	BinanceDepositRejected       = 2 // 拒绝
	BinanceDepositCreditedLocked = 6 // 已入账，暂不能提现
	BinanceDepositWrong          = 7 // 充错
	BinanceDepositWaitingConfirm = 8 // 等待用户确认（travel rule）
)

// binanceTransferInternal 是链下（币安用户之间的内部）转账的 transferType。
const binanceTransferInternal = 1

// BinanceDeposit 是一条币安充值记录。
type BinanceDeposit struct {
	ID               string `json:"id"`
	Amount           string `json:"amount"`
	Coin             string `json:"coin"`
	Network          string `json:"network"`
	Status           int    `json:"status"`
	Address          string `json:"address"`
	AddressTag       string `json:"addressTag"`
	TxID             string `json:"txId"`
	SourceAddress    string `json:"sourceAddress"`
	InsertTime       int64  `json:"insertTime"`
	CompleteTime     int64  `json:"completeTime"`
	TransferType     int    `json:"transferType"`
	ConfirmTimes     string `json:"confirmTimes"`
	TravelRuleStatus int    `json:"travelRuleStatus"`
}

// OffChain 报告是否为币安用户之间的链下转账。
func (d BinanceDeposit) OffChain() bool { return d.TransferType == binanceTransferInternal }

// Credited 报告这笔钱是否已进入账户余额（成功，或已入账但暂不能提现）。
func (d BinanceDeposit) Credited() bool {
	return (d.Status == BinanceDepositSuccess || d.Status == BinanceDepositCreditedLocked) && d.TravelRuleStatus != 1
}

// Rejected 报告这笔充值是否已被币安拒绝或判定为充错，不会再到账。
func (d BinanceDeposit) Rejected() bool {
	return d.Status == BinanceDepositRejected || d.Status == BinanceDepositWrong
}

// NeedsUserAction 报告这笔充值是否卡在需要账户持有人处理的步骤（travel rule 确认）。
func (d BinanceDeposit) NeedsUserAction() bool {
	return d.Status == BinanceDepositWaitingConfirm || d.TravelRuleStatus == 1
}

// binanceNetworks 是币安网络代码到 otok 网络的映射，写死以免误认其他链。
var binanceNetworks = map[string]Network{
	"BSC":   NetworkBEP20,
	"ETH":   NetworkERC20,
	"MATIC": NetworkPolygon,
	"TRX":   NetworkTRC20,
}

// BinanceNetwork 把币安网络代码映射为 otok 网络；不支持的网络返回 false。
func BinanceNetwork(code string) (Network, bool) {
	n, ok := binanceNetworks[strings.ToUpper(strings.TrimSpace(code))]
	return n, ok
}

var offChainIDPattern = regexp.MustCompile(`^(?i:(?:off-chain transfer|internal transfer|链下转账|内部转账)\s*[:：]?\s*)?(\d{6,24})$`)

// ParseOffChainID 从用户输入或币安 txId 中识别链下转账编号，返回纯数字编号。
// 接受「Off-chain transfer 123」「链下转账 123」「123」等写法（忽略全角空格和首尾空白）。
func ParseOffChainID(input string) (string, bool) {
	s := strings.TrimSpace(strings.ReplaceAll(input, "　", " "))
	s = strings.Join(strings.Fields(s), " ")
	m := offChainIDPattern.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// LookupKey 返回按 txId 查询这笔充值时要传的值：链下转账只认纯数字编号。
func (d BinanceDeposit) LookupKey() string {
	if d.OffChain() {
		if id, ok := ParseOffChainID(d.TxID); ok {
			return id
		}
	}
	return strings.TrimSpace(d.TxID)
}

// Transfer 把充值记录转换为统一的 Transfer。n 为收款网络，logIndex 由调用方分配（同一 txId 下多笔充值时区分）。
// 链上充值的 TxHash 为规范化后的交易哈希；链下转账保留币安 txId 原文（如 "Off-chain transfer 123"）。
func (d BinanceDeposit) Transfer(n Network, logIndex int64) (Transfer, error) {
	spec, err := SpecFor(n)
	if err != nil {
		return Transfer{}, err
	}
	addr, err := ParseAddress(n, d.Address)
	if err != nil {
		return Transfer{}, fmt.Errorf("deposit %s address: %w", d.ID, err)
	}
	amount, err := ParseDecimalRaw(d.Amount, spec.Decimals)
	if err != nil {
		return Transfer{}, fmt.Errorf("deposit %s amount: %w", d.ID, err)
	}
	txHash := strings.TrimSpace(d.TxID)
	if !d.OffChain() {
		if txHash, err = NormalizeTxHash(n, txHash); err != nil {
			return Transfer{}, fmt.Errorf("deposit %s txId: %w", d.ID, err)
		}
	}
	if txHash == "" || len(txHash) > binanceTxIDMaxLen {
		return Transfer{}, fmt.Errorf("deposit %s has an invalid txId", d.ID)
	}
	if d.InsertTime <= 0 {
		return Transfer{}, fmt.Errorf("deposit %s has no insertTime", d.ID)
	}
	return Transfer{
		Network:   n,
		TxHash:    txHash,
		LogIndex:  logIndex,
		BlockTime: time.UnixMilli(d.InsertTime),
		From:      strings.TrimSpace(d.SourceAddress),
		ToNorm:    addr.Norm,
		Amount:    amount,
	}, nil
}

// BinanceClient 读取币安账户的 USDT 充值记录（只用只读权限）。
type BinanceClient struct {
	base      string
	apiKey    string
	apiSecret string
	hc        *http.Client
	now       func() time.Time
}

// NewBinanceClient 创建客户端；baseURL 为空时使用 BinanceDefaultBaseURL。
func NewBinanceClient(baseURL, apiKey, apiSecret string, hc *http.Client) (*BinanceClient, error) {
	apiKey, apiSecret = strings.TrimSpace(apiKey), strings.TrimSpace(apiSecret)
	if apiKey == "" || apiSecret == "" {
		return nil, errors.New("binance api key and secret are required")
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = BinanceDefaultBaseURL
	}
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid binance api url")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &BinanceClient{base: base, apiKey: apiKey, apiSecret: apiSecret, hc: hc, now: time.Now}, nil
}

// Deposits 返回 [start, end] 内的 USDT 充值记录（含链下转账），超过一页时翻页，最多 binanceDepositMaxPages 页。
func (c *BinanceClient) Deposits(ctx context.Context, start, end time.Time) ([]BinanceDeposit, error) {
	if end.Sub(start) > binanceMaxRange {
		start = end.Add(-binanceMaxRange)
	}
	var out []BinanceDeposit
	for page := 0; page < binanceDepositMaxPages; page++ {
		var list []BinanceDeposit
		err := c.signedGet(ctx, binanceDepositPath, [][2]string{
			{"coin", "USDT"},
			{"includeSource", "true"},
			{"startTime", strconv.FormatInt(start.UnixMilli(), 10)},
			{"endTime", strconv.FormatInt(end.UnixMilli(), 10)},
			{"offset", strconv.Itoa(page * binanceDepositPageLimit)},
			{"limit", strconv.Itoa(binanceDepositPageLimit)},
		}, &list)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
		if len(list) < binanceDepositPageLimit {
			break
		}
	}
	return out, nil
}

// DepositsByTxID 按 txId 查询 USDT 充值记录（最近 90 天）。链上充值传交易哈希，链下转账传纯数字编号
// （币安按「Off-chain transfer 123」原文查询返回空）。
func (c *BinanceClient) DepositsByTxID(ctx context.Context, txID string) ([]BinanceDeposit, error) {
	txID = strings.TrimSpace(txID)
	if txID == "" {
		return nil, errors.New("txId is required")
	}
	var list []BinanceDeposit
	if err := c.signedGet(ctx, binanceDepositPath, [][2]string{
		{"coin", "USDT"},
		{"includeSource", "true"},
		{"txId", txID},
	}, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *BinanceClient) signedGet(ctx context.Context, path string, params [][2]string, out any) error {
	params = append(params,
		[2]string{"recvWindow", strconv.Itoa(binanceRecvWindowMs)},
		[2]string{"timestamp", strconv.FormatInt(c.now().UnixMilli(), 10)},
	)
	query := binanceEncode(params)
	endpoint := c.base + path + "?" + query + "&signature=" + binanceSign(c.apiSecret, query)
	header := http.Header{}
	header.Set("X-MBX-APIKEY", c.apiKey)
	if err := doJSON(ctx, c.hc, http.MethodGet, endpoint, header, nil, out); err != nil {
		return fmt.Errorf("binance %s: %w", path, err)
	}
	return nil
}

// binanceEncode 按给定顺序编码参数。空格必须编码为 %20：币安会把 + 当成字面量参与验签，签名对不上。
func binanceEncode(params [][2]string) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, url.QueryEscape(p[0])+"="+strings.ReplaceAll(url.QueryEscape(p[1]), "+", "%20"))
	}
	return strings.Join(parts, "&")
}

// binanceSign 返回 query 的 HMAC-SHA256 签名（小写 hex）。
func binanceSign(secret, query string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(query))
	return hex.EncodeToString(m.Sum(nil))
}
