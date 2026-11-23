package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
)

// otok: USDT 到账数据源之一——币安充值记录（含币安用户之间的链下转账）。
// 渠道配置 depositSource = binance 时，监听器改为轮询币安充值记录来发现付款；
// 已记录转账的复核方式只看记录来源（source = binance 一律向币安复核），与当前开关无关。

const (
	// otokUSDTBinanceCursor 是游标表里记录币安轮询健康状态的 network 值。
	otokUSDTBinanceCursor = "binance"
	// otokUSDTBinanceVerifyWindow：按 txId 查不到时，在记录时间前后这么久的充值里按 id 兜底查找。
	otokUSDTBinanceVerifyWindow = 2 * time.Minute
	// OTOK_USDT_BINANCE_PROXY：访问币安用的代理（http / https / socks5，可带账号密码）。服务器在币安受限地区时必填。
	envOtokUSDTBinanceProxy = "OTOK_USDT_BINANCE_PROXY"
	// OTOK_USDT_BINANCE_API_URL：覆盖币安接口地址，仅供测试。
	envOtokUSDTBinanceAPIURL = "OTOK_USDT_BINANCE_API_URL"
)

// errOtokBinanceNotConfigured 表示渠道没有配置币安 API key / secret。
var errOtokBinanceNotConfigured = errors.New("binance api key / secret is not configured on the USDT provider")

// otokBinanceHTTPClient 返回经 OTOK_USDT_BINANCE_PROXY 访问币安、不跟随重定向的客户端。
// 代理地址里可能带密码，解析失败时不把原始错误（可能含 URL）带出去。
func otokBinanceHTTPClient() (*http.Client, error) {
	opts := otokHTTPClientOptions()
	opts.ProxyURL = strings.TrimSpace(os.Getenv(envOtokUSDTBinanceProxy))
	shared, err := httpclient.GetClient(opts)
	if err != nil {
		return nil, fmt.Errorf("invalid %s", envOtokUSDTBinanceProxy)
	}
	hc := *shared
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc, nil
}

func (s *OtokUSDTService) binanceClient(ucfg *provider.OtokUSDTConfig) (*otokchain.BinanceClient, error) {
	if ucfg == nil || ucfg.BinanceAPIKey == "" || ucfg.BinanceAPISecret == "" {
		return nil, errOtokBinanceNotConfigured
	}
	base := strings.TrimSpace(os.Getenv(envOtokUSDTBinanceAPIURL))
	key := strings.Join([]string{os.Getenv(envOtokUSDTBinanceProxy), base, ucfg.BinanceAPIKey, ucfg.BinanceAPISecret}, "|")
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if c, ok := s.binanceClients[key]; ok {
		return c, nil
	}
	hc, err := otokBinanceHTTPClient()
	if err != nil {
		return nil, err
	}
	c, err := otokchain.NewBinanceClient(base, ucfg.BinanceAPIKey, ucfg.BinanceAPISecret, hc)
	if err != nil {
		return nil, err
	}
	s.binanceClients[key] = c
	return c, nil
}

// scanBinance 拉取活跃预留时间窗内的币安充值记录，入库后按现有规则匹配。
func (s *OtokUSDTService) scanBinance(ctx context.Context, ucfg *provider.OtokUSDTConfig, rs []*otokUSDTReservation) {
	c, err := s.binanceClient(ucfg)
	if err != nil {
		_ = otokSaveCursorError(ctx, s.entClient, otokUSDTBinanceCursor, otokUSDTEVMCursorAddress, err)
		return
	}
	deposits, err := c.Deposits(ctx, otokEarliestCreated(rs), time.Now())
	if err != nil {
		slog.Warn("[OtokUSDT] binance deposit query failed", "error", err)
		_ = otokSaveCursorError(ctx, s.entClient, otokUSDTBinanceCursor, otokUSDTEVMCursorAddress, err)
		return
	}
	rows := s.recordBinanceDeposits(ctx, ucfg, deposits)
	_ = otokSaveCursorSuccess(ctx, s.entClient, otokUSDTBinanceCursor, otokUSDTEVMCursorAddress, 0, time.Now().UnixMilli())
	s.matchTransfers(ctx, rows, rs)
}

// otokBinanceRecord 是一条可入库的币安充值记录。
type otokBinanceRecord struct {
	deposit  otokchain.BinanceDeposit
	transfer otokchain.Transfer
}

// otokBinanceRecords 过滤并转换币安充值记录：只保留 USDT、支持的网络、收款地址是渠道配置的某个地址、
// 未被拒绝的记录。同一 txId 下有多笔时按币安 id 排序，log_index 依次为 -1、-2…（链上来源的序号 ≥ 0，不会冲突）。
func otokBinanceRecords(ucfg *provider.OtokUSDTConfig, deposits []otokchain.BinanceDeposit) []otokBinanceRecord {
	own := map[string]bool{}
	for _, a := range ucfg.Addresses {
		own[a.Norm] = true
	}
	var kept []otokchain.BinanceDeposit
	for _, d := range deposits {
		if !strings.EqualFold(strings.TrimSpace(d.Coin), "USDT") || d.Rejected() || strings.TrimSpace(d.ID) == "" {
			continue
		}
		kept = append(kept, d)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].TxID != kept[j].TxID {
			return kept[i].TxID < kept[j].TxID
		}
		return otokNumericLess(kept[i].ID, kept[j].ID)
	})
	var out []otokBinanceRecord
	ord := map[string]int64{}
	for _, d := range kept {
		ord[d.TxID]--
		n, ok := otokchain.BinanceNetwork(d.Network)
		if !ok {
			continue
		}
		tr, err := d.Transfer(n, ord[d.TxID])
		if err != nil {
			slog.Warn("[OtokUSDT] skip unparseable binance deposit", "id", d.ID, "error", err)
			continue
		}
		if !own[tr.ToNorm] {
			continue
		}
		out = append(out, otokBinanceRecord{deposit: d, transfer: tr})
	}
	return out
}

// otokNumericLess 比较两个十进制数字串（币安 id 超过 int64 时也能正确排序）。
func otokNumericLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// recordBinanceDeposits 把币安充值记录写入转账表；不足 0.01 USDT 的忽略。
func (s *OtokUSDTService) recordBinanceDeposits(ctx context.Context, ucfg *provider.OtokUSDTConfig, deposits []otokchain.BinanceDeposit) []*otokUSDTTransfer {
	records := otokBinanceRecords(ucfg, deposits)
	rows := make([]*otokUSDTTransfer, 0, len(records))
	for _, rec := range records {
		spec, err := otokchain.SpecFor(rec.transfer.Network)
		if err != nil {
			continue
		}
		if rec.transfer.Amount.Cmp(otokchain.CentsToRaw(1, spec.Decimals)) < 0 {
			continue
		}
		token, err := otokchain.NormalizeTokenContract(spec)
		if err != nil {
			continue
		}
		row, err := otokUpsertBinanceTransfer(ctx, s.entClient, rec.transfer, rec.deposit.ID, token, spec.Decimals)
		if err != nil {
			slog.Warn("[OtokUSDT] record binance deposit failed", "id", rec.deposit.ID, "tx", rec.transfer.TxHash, "error", err)
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// otokBinanceLookupKey 返回向币安按 txId 查询这条转账时要传的值：链下转账只认纯数字编号。
func otokBinanceLookupKey(txHash string) string {
	if id, ok := otokchain.ParseOffChainID(txHash); ok {
		return id
	}
	return txHash
}

// otokIsOffChainTransfer 报告转账是否为币安链下转账（没有链上交易）。
func otokIsOffChainTransfer(t *otokUSDTTransfer) bool {
	if t == nil || !t.FromBinance() {
		return false
	}
	_, ok := otokchain.ParseOffChainID(t.TxHash)
	return ok
}

// findBinanceDeposit 按 txId 查询币安充值记录并按 id 取出这一条；查不到时在记录时间前后兜底查找。
func (s *OtokUSDTService) findBinanceDeposit(ctx context.Context, c *otokchain.BinanceClient, t *otokUSDTTransfer) (*otokchain.BinanceDeposit, error) {
	list, err := c.DepositsByTxID(ctx, otokBinanceLookupKey(t.TxHash))
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == t.ExternalID {
			return &list[i], nil
		}
	}
	if !t.BlockTime.Valid {
		return nil, nil
	}
	bt := t.BlockTime.Time
	list, err = c.Deposits(ctx, bt.Add(-otokUSDTBinanceVerifyWindow), bt.Add(otokUSDTBinanceVerifyWindow))
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == t.ExternalID {
			return &list[i], nil
		}
	}
	return nil, nil
}

// verifyOnBinance 向币安复核一笔来自币安的转账：只有状态为成功 / 已入账（1 / 6）且地址、金额一致才算确认。
//   - 处理中（0）：未确认，等下一轮；
//   - 等待账户持有人确认（8 / travel rule）：返回错误，记入 last_error，后台显示为入账受阻；
//   - 被拒绝 / 充错（2 / 7）或记录与转账不一致：invalid，转待审核；
//   - 查不到：missing，沿用「链上消失」的处理。
func (s *OtokUSDTService) verifyOnBinance(ctx context.Context, t *otokUSDTTransfer, ucfg *provider.OtokUSDTConfig) (*otokChainCheck, error) {
	c, err := s.binanceClient(ucfg)
	if err != nil {
		return nil, err
	}
	d, err := s.findBinanceDeposit(ctx, c, t)
	if err != nil {
		return nil, err
	}
	check := &otokChainCheck{required: 1}
	if d == nil {
		check.missing = true
		return check, nil
	}
	switch {
	case d.Rejected():
		check.invalid = fmt.Sprintf("binance deposit %s was rejected (status %d)", d.ID, d.Status)
		return check, nil
	case d.NeedsUserAction():
		return nil, fmt.Errorf("binance deposit %s is waiting for the account holder to confirm (travel rule, status %d); confirm it in the Binance app", d.ID, d.Status)
	case !d.Credited():
		return check, nil
	}
	n, ok := otokchain.BinanceNetwork(d.Network)
	if !ok {
		check.invalid = fmt.Sprintf("binance deposit %s is on unsupported network %s", d.ID, d.Network)
		return check, nil
	}
	tr, err := d.Transfer(n, t.LogIndex)
	if err != nil {
		check.invalid = err.Error()
		return check, nil
	}
	if tr.ToNorm != t.ToAddressNorm || n != t.Network || tr.Amount.Cmp(t.AmountRaw) != 0 {
		check.invalid = fmt.Sprintf("binance deposit %s no longer matches the recorded transfer", d.ID)
		return check, nil
	}
	check.transfer = &tr
	check.current = 1
	check.confirmed = true
	return check, nil
}

// verifyTransfer 按转账来源选择复核方式。
func (s *OtokUSDTService) verifyTransfer(ctx context.Context, t *otokUSDTTransfer, ucfg *provider.OtokUSDTConfig) (*otokChainCheck, error) {
	if t.FromBinance() {
		return s.verifyOnBinance(ctx, t, ucfg)
	}
	return s.verifyOnChain(ctx, t, ucfg)
}

// otokTxInput 是用户 / 管理员输入的交易标识：链上为规范化后的交易哈希，链下为纯数字编号。
type otokTxInput struct {
	Key      string
	OffChain bool
}

// otokParseTxInput 解析用户 / 管理员输入。链下转账编号只在币安数据源下可用。
func otokParseTxInput(network otokchain.Network, input string, binance bool) (otokTxInput, error) {
	if id, ok := otokchain.ParseOffChainID(input); ok {
		if !binance {
			return otokTxInput{}, infraerrors.BadRequest("USDT_OFFCHAIN_UNSUPPORTED",
				"off-chain transfer ids are not supported by the current deposit source; please contact support")
		}
		return otokTxInput{Key: id, OffChain: true}, nil
	}
	h, err := otokchain.NormalizeTxHash(network, input)
	if err != nil {
		return otokTxInput{}, infraerrors.BadRequest("USDT_TX_INVALID", err.Error())
	}
	return otokTxInput{Key: h}, nil
}

// matches 报告已记录的转账是否就是这次输入的交易。
func (in otokTxInput) matches(t *otokUSDTTransfer) bool {
	return t != nil && otokBinanceLookupKey(t.TxHash) == in.Key
}

// otokBinanceFound 是按输入查到、已入库、转入本订单收款地址的一笔币安充值。
type otokBinanceFound struct {
	row     *otokUSDTTransfer
	deposit otokchain.BinanceDeposit
}

// lookupBinance 按用户 / 管理员输入向币安查询充值记录，入库后返回转入本订单收款地址的记录。
// 链下编号按 txId 查不到时，再在订单时间窗内的充值里按编号兜底查找。返回的错误已转换为对外错误码。
func (s *OtokUSDTService) lookupBinance(ctx context.Context, r *otokUSDTReservation, ucfg *provider.OtokUSDTConfig, in otokTxInput) ([]otokBinanceFound, error) {
	unavailable := func(err error) error {
		slog.Warn("[OtokUSDT] binance deposit lookup failed", "order", r.OrderID, "key", in.Key, "error", err)
		return infraerrors.ServiceUnavailable("USDT_BINANCE_UNAVAILABLE", "Binance deposit query failed, please retry later")
	}
	c, err := s.binanceClient(ucfg)
	if err != nil {
		return nil, unavailable(err)
	}
	list, err := c.DepositsByTxID(ctx, in.Key)
	if err != nil {
		return nil, unavailable(err)
	}
	if len(list) == 0 && in.OffChain {
		all, err := c.Deposits(ctx, r.CreatedAt.Add(-otokUSDTScanLookback), time.Now())
		if err != nil {
			return nil, unavailable(err)
		}
		for _, d := range all {
			if id, ok := otokchain.ParseOffChainID(d.TxID); ok && id == in.Key {
				list = append(list, d)
			}
		}
	}
	if len(list) == 0 {
		return nil, infraerrors.BadRequest("USDT_TX_NOT_FOUND",
			"no deposit with this transaction hash or off-chain transfer id was found yet; check it and retry later")
	}
	var out []otokBinanceFound
	for _, rec := range otokBinanceRecords(ucfg, list) {
		if rec.transfer.ToNorm != r.ToAddressNorm {
			continue
		}
		spec, err := otokchain.SpecFor(rec.transfer.Network)
		if err != nil {
			continue
		}
		token, err := otokchain.NormalizeTokenContract(spec)
		if err != nil {
			continue
		}
		row, err := otokUpsertBinanceTransfer(ctx, s.entClient, rec.transfer, rec.deposit.ID, token, spec.Decimals)
		if err != nil {
			return nil, err
		}
		out = append(out, otokBinanceFound{row: row, deposit: rec.deposit})
	}
	if len(out) == 0 {
		return nil, infraerrors.BadRequest("USDT_TX_WRONG_RECIPIENT", "this deposit is not a USDT transfer to the order's receiving address")
	}
	return out, nil
}
