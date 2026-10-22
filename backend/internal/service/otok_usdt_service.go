package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/google/uuid"
)

// otok: USDT 链上充值服务——后台监听、统一入账、用户提交 TxHash、管理员补单与查询。

const (
	otokUSDTMaxCreditAttempts = 5
	// otokUSDTDropAfter：已绑定的 EVM 交易在链上消失（重组）超过该时长后解除绑定。
	otokUSDTDropAfter = 15 * time.Minute
	// otokUSDTMinPayDelay：转账区块时间至少晚于下单时间这么久，才会匹配到该订单。
	// 正常用户不可能在下单后几秒内完成转账并上链；更早的转账多半是别人的迟到付款。
	otokUSDTMinPayDelay = 10 * time.Second
	// otokUSDTExpiredRecoverAge：上游只接受 5 分钟内过期的订单入账，超过时先刷新 updated_at。
	otokUSDTExpiredRecoverAge = 4 * time.Minute
	otokUSDTHTTPTimeout       = 15 * time.Second
)

// OtokUSDTService 负责 USDT 渠道的链上监听与入账。
type OtokUSDTService struct {
	paymentSvc *PaymentService
	entClient  *dbent.Client
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	interval   time.Duration

	clientsMu   sync.Mutex
	evmClients  map[string]*otokchain.EVMClient
	tronClients map[string]*otokchain.TronClient
	evmChunks   map[otokchain.Network]int64

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewOtokUSDTService 创建服务；interval ≤ 0 时不启动后台监听。
func NewOtokUSDTService(paymentSvc *PaymentService, entClient *dbent.Client, interval time.Duration) *OtokUSDTService {
	return &OtokUSDTService{
		paymentSvc:  paymentSvc,
		entClient:   entClient,
		instanceID:  uuid.NewString(),
		interval:    interval,
		evmClients:  map[string]*otokchain.EVMClient{},
		tronClients: map[string]*otokchain.TronClient{},
		evmChunks:   map[otokchain.Network]int64{},
		stopCh:      make(chan struct{}),
	}
}

// SetLeaderLock 注入多实例选主所需的锁；都为 nil 时不加锁运行。
func (s *OtokUSDTService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// otokHTTPClientOptions 默认拒绝解析到内网地址的 RPC（防 SSRF）；自建内网节点或本地联调时
// 设置 OTOK_USDT_ALLOW_PRIVATE_RPC=1 放开。
func otokHTTPClientOptions() httpclient.Options {
	v := strings.TrimSpace(os.Getenv("OTOK_USDT_ALLOW_PRIVATE_RPC"))
	allowPrivate := v == "1" || strings.EqualFold(v, "true")
	return httpclient.Options{Timeout: otokUSDTHTTPTimeout, ValidateResolvedIP: !allowPrivate}
}

// otokHTTPClient 返回不跟随重定向的客户端：RPC / TronGrid 不需要重定向，
// 跟随重定向会把 TRON-PRO-API-KEY 等自定义请求头带给第三方主机。
// httpclient.GetClient 返回的是共享实例，这里复制一份再改。
func otokHTTPClient() (*http.Client, error) {
	shared, err := httpclient.GetClient(otokHTTPClientOptions())
	if err != nil {
		return nil, err
	}
	hc := *shared
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc, nil
}

func (s *OtokUSDTService) evmClient(spec otokchain.Spec, endpoint string) (*otokchain.EVMClient, error) {
	key := string(spec.Network) + "|" + endpoint
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if c, ok := s.evmClients[key]; ok {
		return c, nil
	}
	hc, err := otokHTTPClient()
	if err != nil {
		return nil, err
	}
	c, err := otokchain.NewEVMClient(spec, endpoint, hc)
	if err != nil {
		return nil, err
	}
	s.evmClients[key] = c
	return c, nil
}

func (s *OtokUSDTService) tronClient(spec otokchain.Spec, apiKey string) (*otokchain.TronClient, error) {
	key := string(spec.Network) + "|" + apiKey
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if c, ok := s.tronClients[key]; ok {
		return c, nil
	}
	hc, err := otokHTTPClient()
	if err != nil {
		return nil, err
	}
	// OTOK_USDT_TRON_API_URL 可指向自建的 TronGrid 兼容网关或联调用的模拟服务；留空使用默认端点。
	c, err := otokchain.NewTronClient(spec, os.Getenv("OTOK_USDT_TRON_API_URL"), apiKey, hc)
	if err != nil {
		return nil, err
	}
	s.tronClients[key] = c
	return c, nil
}

// instanceConfig 读取预留所属渠道实例的配置；实例被删除或配置不可读时退回到默认端点。
func (s *OtokUSDTService) instanceConfig(ctx context.Context, instanceID string) *provider.OtokUSDTConfig {
	empty := &provider.OtokUSDTConfig{RPCURLs: map[otokchain.Network]string{}}
	id, err := strconv.ParseInt(strings.TrimSpace(instanceID), 10, 64)
	if err != nil || s.paymentSvc == nil || s.paymentSvc.loadBalancer == nil {
		return empty
	}
	raw, err := s.paymentSvc.loadBalancer.GetInstanceConfig(ctx, id)
	if err != nil {
		return empty
	}
	cfg, err := provider.ParseOtokUSDTConfig(raw)
	if err != nil {
		// 地址被清空等情况下仍尽量沿用 API key / RPC 配置。
		return &provider.OtokUSDTConfig{
			TronGridAPIKey: strings.TrimSpace(raw[provider.OtokUSDTConfigTronGridAPIKey]),
			RPCURLs: map[otokchain.Network]string{
				otokchain.NetworkERC20:   strings.TrimSpace(raw[provider.OtokUSDTConfigETHRPCURL]),
				otokchain.NetworkBEP20:   strings.TrimSpace(raw[provider.OtokUSDTConfigBSCRPCURL]),
				otokchain.NetworkPolygon: strings.TrimSpace(raw[provider.OtokUSDTConfigPolygonRPCURL]),
			},
		}
	}
	return cfg
}

// enabledInstance 返回当前启用的 USDT 渠道实例及其配置。
func (s *OtokUSDTService) enabledInstance(ctx context.Context) (*dbent.PaymentProviderInstance, *provider.OtokUSDTConfig, error) {
	inst, err := s.entClient.PaymentProviderInstance.Query().
		Where(paymentproviderinstance.ProviderKeyEQ(payment.TypeOtokUSDT), paymentproviderinstance.Enabled(true)).
		Order(paymentproviderinstance.BySortOrder(), paymentproviderinstance.ByID()).
		First(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	raw, err := s.paymentSvc.configService.decryptConfig(inst.Config)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := provider.ParseOtokUSDTConfig(raw)
	if err != nil {
		return inst, nil, err
	}
	return inst, cfg, nil
}

func otokOrderPaidLike(status string) bool {
	switch status {
	case OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted,
		payment.OrderStatusRefundRequested, payment.OrderStatusRefunding, payment.OrderStatusRefundPending,
		payment.OrderStatusPartiallyRefunded, payment.OrderStatusRefunded, payment.OrderStatusRefundFailed:
		return true
	}
	return false
}

// otokOrderMatchable 报告订单是否还能自动匹配新的转账。已取消的订单不再匹配：取消后才到的
// 转账留给管理员补单，避免被取消（含管理员取消）的订单自动复活。
func otokOrderMatchable(status string) bool {
	return status == OrderStatusPending || status == OrderStatusExpired
}

// otokChainCheck 是对一笔已绑定转账的链上复核结果。
type otokChainCheck struct {
	confirmed bool
	missing   bool // 链上找不到（可能被重组）
	invalid   string
	transfer  *otokchain.Transfer
	current   int64 // 当前确认数（EVM）
	required  int64
}

// verifyOnChain 复核转账仍在链上且已达确认要求。
func (s *OtokUSDTService) verifyOnChain(ctx context.Context, t *otokUSDTTransfer, ucfg *provider.OtokUSDTConfig) (*otokChainCheck, error) {
	spec, err := otokchain.SpecFor(t.Network)
	if err != nil {
		return nil, err
	}
	check := &otokChainCheck{required: spec.Confirmations}
	matchTransfer := func(list []otokchain.Transfer) *otokchain.Transfer {
		for i := range list {
			if list[i].ToNorm == t.ToAddressNorm && list[i].LogIndex == t.LogIndex && list[i].Amount.Cmp(t.AmountRaw) == 0 {
				return &list[i]
			}
		}
		return nil
	}
	switch spec.Kind {
	case otokchain.KindTron:
		c, err := s.tronClient(spec, ucfg.TronGridAPIKey)
		if err != nil {
			return nil, err
		}
		res, err := c.Transaction(ctx, t.TxHash, t.ToAddressNorm)
		if errors.Is(err, otokchain.ErrNotFound) {
			check.missing = true
			return check, nil
		}
		if err != nil {
			return nil, err
		}
		if !res.Success {
			check.invalid = "transaction failed on chain"
			return check, nil
		}
		check.transfer = matchTransfer(res.Transfers)
		if check.transfer == nil {
			check.invalid = "transfer not found in transaction logs"
			return check, nil
		}
		check.confirmed = res.Confirmed
		if res.Confirmed {
			check.current = spec.Confirmations
		}
		return check, nil
	case otokchain.KindEVM:
		c, err := s.evmClient(spec, ucfg.RPCURL(t.Network))
		if err != nil {
			return nil, err
		}
		if err := c.EnsureChainID(ctx); err != nil {
			return nil, err
		}
		rc, err := c.Receipt(ctx, t.TxHash)
		if errors.Is(err, otokchain.ErrNotFound) {
			check.missing = true
			return check, nil
		}
		if err != nil {
			return nil, err
		}
		if !rc.Success {
			check.invalid = "transaction reverted"
			return check, nil
		}
		check.transfer = matchTransfer(rc.Transfers)
		if check.transfer == nil {
			check.invalid = "transfer not found in receipt logs"
			return check, nil
		}
		latest, err := c.BlockNumber(ctx)
		if err != nil {
			return nil, err
		}
		check.current = latest - rc.BlockNumber + 1
		if check.current < 0 {
			check.current = 0
		}
		check.confirmed = check.current >= spec.Confirmations
		if check.transfer.BlockTime.IsZero() {
			if _, ts, err := c.Block(ctx, rc.BlockNumber); err == nil {
				check.transfer.BlockTime = ts
			}
		}
		return check, nil
	default:
		return nil, fmt.Errorf("unsupported network %s", t.Network)
	}
}

// creditBoundTransfer 对已绑定订单的转账做链上复核并入账。三条入口（监听、用户、管理员）共用。
// 返回 credited=true 表示订单已处于已支付状态。
// 出错（多为链上 RPC 故障）时转账保持原状态等下一轮重试，错误记到 last_error 供后台查看。
func (s *OtokUSDTService) creditBoundTransfer(ctx context.Context, t *otokUSDTTransfer, operator string) (credited bool, err error) {
	defer func() {
		if err != nil || t.LastError != "" {
			if saveErr := otokSaveTransferError(ctx, s.entClient, t.ID, err); saveErr != nil {
				slog.Warn("[OtokUSDT] save transfer credit error failed", "tx", t.TxHash, "error", saveErr)
			}
		}
	}()
	if !t.OrderID.Valid {
		return false, nil
	}
	r, err := otokGetReservation(ctx, s.entClient, t.OrderID.Int64)
	if err != nil {
		return false, err
	}
	if r == nil {
		return false, otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferReview, "reservation missing", false)
	}
	order, err := s.entClient.PaymentOrder.Get(ctx, r.OrderID)
	if err != nil {
		return false, fmt.Errorf("load order %d: %w", r.OrderID, err)
	}
	if otokOrderPaidLike(order.Status) {
		return true, s.markCredited(ctx, t, r)
	}
	if order.Status == OrderStatusFailed && order.PaidAt == nil {
		return false, otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferReview, "order failed before payment; handle manually", false)
	}
	if note := otokBoundTransferMismatch(t, r); note != "" {
		// 兜底：任何入口绑定的转账，只要网络、地址或金额与预留不一致（管理员补单除外）都不入账。
		slog.Warn("[OtokUSDT] bound transfer does not match reservation, sending to review", "tx", t.TxHash, "order", r.OrderID, "reason", note)
		if err := otokUnbindTransfer(ctx, s.entClient, t.ID, note); err != nil {
			return false, err
		}
		return false, otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferReview, note, false)
	}

	check, err := s.verifyOnChain(ctx, t, s.instanceConfig(ctx, r.InstanceID))
	if err != nil {
		return false, err
	}
	switch {
	case check.missing:
		if time.Since(t.UpdatedAt) > otokUSDTDropAfter {
			slog.Warn("[OtokUSDT] bound transfer disappeared from chain, unbinding", "tx", t.TxHash, "order", r.OrderID)
			return false, otokUnbindTransfer(ctx, s.entClient, t.ID, "transaction not found on chain (reorg?)")
		}
		return false, nil
	case check.invalid != "":
		if err := otokUnbindTransfer(ctx, s.entClient, t.ID, check.invalid); err != nil {
			return false, err
		}
		return false, otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferReview, check.invalid, false)
	case !check.confirmed:
		if check.transfer != nil && (check.transfer.BlockNumber != t.BlockNumber || check.transfer.BlockHash != t.BlockHash) {
			_, _ = otokUpsertTransfer(ctx, s.entClient, *check.transfer, t.TokenContract, t.Decimals, t.Source)
		}
		return false, nil
	}

	if order.Status == OrderStatusExpired && time.Since(order.UpdatedAt) > otokUSDTExpiredRecoverAge {
		// 上游 toPaid 只接受 5 分钟宽限内的过期订单；USDT 预留期内到账的订单先刷新时间再入账。
		if _, err := s.entClient.PaymentOrder.Update().
			Where(paymentorder.IDEQ(order.ID), paymentorder.StatusEQ(OrderStatusExpired)).
			SetUpdatedAt(time.Now()).Save(ctx); err != nil {
			return false, fmt.Errorf("refresh expired order: %w", err)
		}
	}
	notifyErr := s.paymentSvc.HandlePaymentNotification(ctx, &payment.PaymentNotification{
		TradeNo: t.TxHash,
		OrderID: r.OutTradeNo,
		Amount:  order.PayAmount,
		Status:  payment.NotificationStatusSuccess,
		RawData: fmt.Sprintf("otok_usdt network=%s tx=%s log=%d amount=%s", t.Network, t.TxHash, t.LogIndex, otokchain.FormatRaw(t.AmountRaw, t.Decimals)),
	}, payment.TypeOtokUSDT)

	refreshed, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return false, fmt.Errorf("reload order %d: %w", order.ID, err)
	}
	if otokOrderPaidLike(refreshed.Status) {
		s.paymentSvc.writeAuditLog(ctx, order.ID, "OTOK_USDT_CREDITED", operator, map[string]any{
			"network": string(t.Network), "txHash": t.TxHash, "logIndex": t.LogIndex, "from": t.FromAddress,
			"onChainAmount": otokchain.FormatRaw(t.AmountRaw, t.Decimals), "source": t.Source,
		})
		return true, s.markCredited(ctx, t, r)
	}
	note := "order not credited"
	if notifyErr != nil {
		note = notifyErr.Error()
	}
	if t.CreditAttempts+1 >= otokUSDTMaxCreditAttempts {
		note = "credit retries exhausted: " + note
	}
	if err := otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferCreditFailed, note, true); err != nil {
		return false, err
	}
	return false, notifyErr
}

// otokBoundTransferMismatch 校验已绑定转账与预留是否一致，不一致时返回原因。
func otokBoundTransferMismatch(t *otokUSDTTransfer, r *otokUSDTReservation) string {
	if t.Network != r.Network || t.ToAddressNorm != r.ToAddressNorm {
		return fmt.Sprintf("transfer %s/%s does not match order %s/%s", t.Network, t.ToAddressNorm, r.Network, r.ToAddressNorm)
	}
	if t.AmountOverride {
		return ""
	}
	if cents, exact := t.AmountCents(); !exact || cents != r.AmountCents {
		return fmt.Sprintf("amount %s does not match order amount %s", otokchain.FormatRaw(t.AmountRaw, t.Decimals), otokchain.FormatCents(r.AmountCents))
	}
	return ""
}

func (s *OtokUSDTService) markCredited(ctx context.Context, t *otokUSDTTransfer, r *otokUSDTReservation) error {
	if err := otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferCredited, "", false); err != nil {
		return err
	}
	return otokMarkReservationCredited(ctx, s.entClient, r.OrderID)
}

// bindTransfer 在事务内把转账绑定到订单；admin 的含义见 otokBindTransfer。
func (s *OtokUSDTService) bindTransfer(ctx context.Context, transferID, orderID int64, note string, admin bool) (bool, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := otokBindTransfer(ctx, tx, transferID, orderID, note, admin)
	if err != nil || !ok {
		return false, err
	}
	return true, tx.Commit()
}

// fetchTxTransfers 按交易哈希读取转入 recipient 的 USDT 转账（用户提交与管理员补单使用）。
func (s *OtokUSDTService) fetchTxTransfers(ctx context.Context, r *otokUSDTReservation, txHash string) ([]otokchain.Transfer, *otokChainCheck, error) {
	spec, err := otokchain.SpecFor(r.Network)
	if err != nil {
		return nil, nil, err
	}
	ucfg := s.instanceConfig(ctx, r.InstanceID)
	check := &otokChainCheck{required: spec.Confirmations}
	switch spec.Kind {
	case otokchain.KindTron:
		c, err := s.tronClient(spec, ucfg.TronGridAPIKey)
		if err != nil {
			return nil, nil, err
		}
		res, err := c.Transaction(ctx, txHash, r.ToAddressNorm)
		if err != nil {
			return nil, nil, err
		}
		if !res.Success {
			check.invalid = "transaction failed on chain"
		}
		check.confirmed = res.Confirmed
		return res.Transfers, check, nil
	case otokchain.KindEVM:
		c, err := s.evmClient(spec, ucfg.RPCURL(r.Network))
		if err != nil {
			return nil, nil, err
		}
		if err := c.EnsureChainID(ctx); err != nil {
			return nil, nil, err
		}
		rc, err := c.Receipt(ctx, txHash)
		if err != nil {
			return nil, nil, err
		}
		if !rc.Success {
			check.invalid = "transaction reverted"
		}
		latest, err := c.BlockNumber(ctx)
		if err != nil {
			return nil, nil, err
		}
		check.current = latest - rc.BlockNumber + 1
		check.confirmed = check.current >= spec.Confirmations
		var out []otokchain.Transfer
		blockTime := time.Time{}
		for _, tr := range rc.Transfers {
			if tr.ToNorm != r.ToAddressNorm {
				continue
			}
			if tr.BlockTime.IsZero() {
				if blockTime.IsZero() {
					if _, ts, err := c.Block(ctx, rc.BlockNumber); err == nil {
						blockTime = ts
					}
				}
				tr.BlockTime = blockTime
			}
			out = append(out, tr)
		}
		return out, check, nil
	default:
		return nil, nil, fmt.Errorf("unsupported network %s", r.Network)
	}
}

// otokTransferInWindow 报告转账的区块时间是否落在订单可匹配的窗口
// [下单时间 + otokUSDTMinPayDelay, 保留期结束] 内。
func otokTransferInWindow(t otokchain.Transfer, r *otokUSDTReservation) bool {
	if t.BlockTime.IsZero() {
		return false
	}
	return !t.BlockTime.Before(r.CreatedAt.Add(otokUSDTMinPayDelay)) && !t.BlockTime.After(r.ReservedUntil)
}

// otokTransferAutoMatchable 报告一条已记录的转账能否自动（监听 / 用户提交）绑定到预留：
// 金额精确相等、区块时间在窗口内，且在下单之后才首次被记录。
func otokTransferAutoMatchable(row *otokUSDTTransfer, r *otokUSDTReservation) bool {
	if row.OrderID.Valid || row.Status != otokTransferSeen || !row.BlockTime.Valid {
		return false
	}
	if row.Network != r.Network || row.ToAddressNorm != r.ToAddressNorm || row.CreatedAt.Before(r.CreatedAt) {
		return false
	}
	if cents, exact := row.AmountCents(); !exact || cents != r.AmountCents {
		return false
	}
	return otokTransferInWindow(otokchain.Transfer{BlockTime: row.BlockTime.Time}, r)
}

func otokChainLookupError(err error) error {
	if errors.Is(err, otokchain.ErrNotFound) {
		return infraerrors.BadRequest("USDT_TX_NOT_FOUND", "transaction not found on this network yet; check the network and hash, then retry later")
	}
	if strings.Contains(err.Error(), "invalid transaction hash") {
		return infraerrors.BadRequest("USDT_TX_INVALID", err.Error())
	}
	return infraerrors.ServiceUnavailable("USDT_CHAIN_UNAVAILABLE", "blockchain query failed, please retry later")
}

// SubmitUserTx 用户提交交易哈希加速确认：只接受金额精确等于本单、且在订单时间窗口内的转账。
// 不符合条件时只记审计日志，不改动转账状态——用户可以提交任意交易哈希，
// 不能借此把别人的待匹配转账改成待审核来阻止其自动到账。
func (s *OtokUSDTService) SubmitUserTx(ctx context.Context, userID, orderID int64, txHash string) (*OtokUSDTOrderDetail, error) {
	order, err := s.paymentSvc.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	r, err := otokGetReservation(ctx, s.entClient, order.ID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "not a USDT order")
	}
	if otokOrderPaidLike(order.Status) {
		return s.orderDetail(ctx, order, r)
	}
	norm, err := otokchain.NormalizeTxHash(r.Network, txHash)
	if err != nil {
		return nil, infraerrors.BadRequest("USDT_TX_INVALID", err.Error())
	}
	operator := fmt.Sprintf("user:%d", userID)
	if existing, err := otokGetTransferByOrder(ctx, s.entClient, order.ID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.TxHash != norm {
			return nil, infraerrors.Conflict("USDT_ORDER_ALREADY_MATCHED", "this order is already matched with another transaction")
		}
		_, _ = s.creditBoundTransfer(ctx, existing, operator)
		return s.orderDetail(ctx, order, r)
	}
	if !otokOrderMatchable(order.Status) {
		return nil, infraerrors.BadRequest("USDT_ORDER_NOT_PAYABLE", "order is not awaiting payment")
	}
	if time.Now().After(r.ReservedUntil.Add(otokUSDTReleaseMargin)) {
		return nil, infraerrors.BadRequest("USDT_ORDER_WINDOW_CLOSED", "the automatic confirmation window has closed; please contact support")
	}
	transfers, check, err := s.fetchTxTransfers(ctx, r, norm)
	if err != nil {
		return nil, otokChainLookupError(err)
	}
	if check.invalid != "" {
		return nil, infraerrors.BadRequest("USDT_TX_FAILED", check.invalid)
	}
	if len(transfers) == 0 {
		return nil, infraerrors.BadRequest("USDT_TX_WRONG_RECIPIENT", "this transaction is not a USDT transfer to the order's receiving address")
	}
	var matched *otokUSDTTransfer
	var rejected []string
	usedByOther := false
	for _, tr := range transfers {
		row, err := otokUpsertTransfer(ctx, s.entClient, tr, r.TokenContract, r.Decimals, otokSourceUser)
		if err != nil {
			return nil, err
		}
		if row.OrderID.Valid {
			usedByOther = true
			continue
		}
		if otokTransferAutoMatchable(row, r) {
			if matched == nil {
				matched = row
			}
			continue
		}
		rejected = append(rejected, fmt.Sprintf("log %d: amount %s (expected %s), status %s, block time %s",
			row.LogIndex, otokchain.FormatRaw(row.AmountRaw, row.Decimals), otokchain.FormatCents(r.AmountCents),
			row.Status, tr.BlockTime.Format(time.RFC3339)))
	}
	if matched == nil {
		slog.Info("[OtokUSDT] user submitted tx did not match order", "order", order.ID, "tx", norm, "rejected", rejected)
		s.paymentSvc.writeAuditLog(ctx, order.ID, "OTOK_USDT_TX_REJECTED", operator, map[string]any{
			"network": string(r.Network), "txHash": norm, "usedByOtherOrder": usedByOther, "rejected": rejected,
		})
		if usedByOther && len(rejected) == 0 {
			return nil, infraerrors.Conflict("USDT_TX_ALREADY_USED", "this transaction has already been used by another order")
		}
		return nil, infraerrors.BadRequest("USDT_TX_AMOUNT_MISMATCH", "the transferred amount or time does not match this order; please contact support")
	}
	ok, err := s.bindTransfer(ctx, matched.ID, order.ID, "submitted by user "+strconv.FormatInt(userID, 10), false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, infraerrors.Conflict("USDT_TX_ALREADY_USED", "this transaction has already been used by another order")
	}
	if bound, err := otokGetTransfer(ctx, s.entClient, matched.ID); err == nil && bound != nil {
		if _, err := s.creditBoundTransfer(ctx, bound, operator); err != nil {
			slog.Warn("[OtokUSDT] credit after user submission failed", "order", order.ID, "error", err)
		}
	}
	return s.orderDetail(ctx, order, r)
}

// AdminBindTx 管理员补单：校验链上交易后绑定到订单并入账，允许金额不一致（需填写原因）。
func (s *OtokUSDTService) AdminBindTx(ctx context.Context, orderID int64, txHash string, logIndex *int64, reason, operator string) (*OtokUSDTOrderDetail, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, infraerrors.BadRequest("VALIDATION_ERROR", "reason is required")
	}
	order, err := s.paymentSvc.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	r, err := otokGetReservation(ctx, s.entClient, order.ID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "not a USDT order")
	}
	if otokOrderPaidLike(order.Status) {
		return nil, infraerrors.Conflict("ORDER_ALREADY_PAID", "order is already paid")
	}
	if order.Status == OrderStatusFailed && order.PaidAt == nil {
		return nil, infraerrors.BadRequest("USDT_ORDER_NOT_PAYABLE", "order failed before payment; create a new order or adjust balance manually")
	}
	norm, err := otokchain.NormalizeTxHash(r.Network, txHash)
	if err != nil {
		return nil, infraerrors.BadRequest("USDT_TX_INVALID", err.Error())
	}
	existing, err := otokGetTransferByOrder(ctx, s.entClient, order.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.TxHash != norm {
		return nil, infraerrors.Conflict("USDT_ORDER_ALREADY_MATCHED", "this order is already matched with another transaction")
	}
	transfers, check, err := s.fetchTxTransfers(ctx, r, norm)
	if err != nil {
		return nil, otokChainLookupError(err)
	}
	if check.invalid != "" {
		return nil, infraerrors.BadRequest("USDT_TX_FAILED", check.invalid)
	}
	if !check.confirmed {
		return nil, infraerrors.BadRequest("USDT_TX_UNCONFIRMED", fmt.Sprintf("transaction is not confirmed yet (%d/%d)", check.current, check.required))
	}
	var pick *otokchain.Transfer
	for i := range transfers {
		tr := &transfers[i]
		if logIndex != nil {
			if tr.LogIndex == *logIndex {
				pick = tr
			}
			continue
		}
		if cents, exact := otokchain.RawToCents(tr.Amount, r.Decimals); exact && cents == r.AmountCents {
			pick = tr
			break
		}
	}
	if pick == nil && logIndex == nil && len(transfers) == 1 {
		pick = &transfers[0]
	}
	if pick == nil {
		if len(transfers) == 0 {
			return nil, infraerrors.BadRequest("USDT_TX_WRONG_RECIPIENT", "this transaction is not a USDT transfer to the order's receiving address")
		}
		return nil, infraerrors.BadRequest("USDT_TX_AMBIGUOUS", "multiple transfers in this transaction; specify log_index")
	}
	row, err := otokUpsertTransfer(ctx, s.entClient, *pick, r.TokenContract, r.Decimals, otokSourceAdmin)
	if err != nil {
		return nil, err
	}
	if row.OrderID.Valid && row.OrderID.Int64 != order.ID {
		return nil, infraerrors.Conflict("USDT_TX_ALREADY_USED", fmt.Sprintf("this transaction is already used by order %d", row.OrderID.Int64))
	}
	if !row.OrderID.Valid {
		if row.Status != otokTransferSeen && row.Status != otokTransferReview && row.Status != otokTransferDropped {
			if err := otokSetTransferStatus(ctx, s.entClient, row.ID, otokTransferReview, "", false); err != nil {
				return nil, err
			}
		}
		ok, err := s.bindTransfer(ctx, row.ID, order.ID, "admin: "+reason, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, infraerrors.Conflict("USDT_TX_ALREADY_USED", "this transaction has already been used")
		}
	}
	s.paymentSvc.writeAuditLog(ctx, order.ID, "OTOK_USDT_ADMIN_BIND", operator, map[string]any{
		"network": string(r.Network), "txHash": norm, "logIndex": pick.LogIndex,
		"onChainAmount": otokchain.FormatRaw(pick.Amount, r.Decimals), "expectedAmount": otokchain.FormatCents(r.AmountCents),
		"reason": reason,
	})
	bound, err := otokGetTransfer(ctx, s.entClient, row.ID)
	if err != nil {
		return nil, err
	}
	if credited, err := s.creditBoundTransfer(ctx, bound, operator); err != nil {
		return nil, infraerrors.ServiceUnavailable("USDT_CREDIT_FAILED", err.Error())
	} else if !credited {
		return nil, infraerrors.ServiceUnavailable("USDT_CREDIT_FAILED", "transaction bound but the order was not credited; check the audit log")
	}
	refreshed, err := s.paymentSvc.GetOrderByID(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	return s.orderDetail(ctx, refreshed, r)
}

// AdminIgnoreTransfer 把未匹配 / 待审核的转账标记为已忽略。
func (s *OtokUSDTService) AdminIgnoreTransfer(ctx context.Context, transferID int64, note string) error {
	t, err := otokGetTransfer(ctx, s.entClient, transferID)
	if err != nil {
		return err
	}
	if t == nil {
		return infraerrors.NotFound("NOT_FOUND", "transfer not found")
	}
	if t.OrderID.Valid {
		return infraerrors.Conflict("USDT_TRANSFER_BOUND", "transfer is bound to an order")
	}
	return otokSetTransferStatus(ctx, s.entClient, t.ID, otokTransferIgnored, strings.TrimSpace(note), false)
}

// OtokUSDTTransferView 是转账的对外展示结构。
type OtokUSDTTransferView struct {
	ID             int64      `json:"id"`
	Network        string     `json:"network"`
	TxHash         string     `json:"tx_hash"`
	LogIndex       int64      `json:"log_index"`
	ExplorerURL    string     `json:"explorer_url"`
	BlockNumber    int64      `json:"block_number"`
	BlockTime      *time.Time `json:"block_time,omitempty"`
	FromAddress    string     `json:"from_address"`
	ToAddress      string     `json:"to_address"`
	Amount         string     `json:"amount"`
	OrderID        *int64     `json:"order_id,omitempty"`
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	Note           string     `json:"note,omitempty"`
	CreditAttempts int        `json:"credit_attempts"`
	LastError      string     `json:"last_error,omitempty"`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty"`
	Confirmations  *int64     `json:"confirmations,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *OtokUSDTService) transferView(ctx context.Context, t *otokUSDTTransfer, admin bool) *OtokUSDTTransferView {
	if t == nil {
		return nil
	}
	v := &OtokUSDTTransferView{
		ID: t.ID, Network: string(t.Network), TxHash: t.TxHash, LogIndex: t.LogIndex, BlockNumber: t.BlockNumber,
		FromAddress: t.FromAddress, ToAddress: t.ToAddressNorm, Amount: otokchain.FormatRaw(t.AmountRaw, t.Decimals),
		Source: t.Source, Status: t.Status, CreditAttempts: t.CreditAttempts, CreatedAt: t.CreatedAt,
	}
	if spec, err := otokchain.SpecFor(t.Network); err == nil {
		v.ExplorerURL = spec.ExplorerTxLink(t.TxHash)
		if spec.Kind == otokchain.KindEVM {
			if addr, err := otokchain.ParseAddress(t.Network, t.ToAddressNorm); err == nil {
				v.ToAddress = addr.Display
			}
			if cur, err := otokGetCursor(ctx, s.entClient, t.Network, "*"); err == nil && cur != nil && cur.LastBlock >= t.BlockNumber && t.BlockNumber > 0 {
				n := cur.LastBlock - t.BlockNumber + 1
				v.Confirmations = &n
			}
		}
	}
	if t.BlockTime.Valid {
		bt := t.BlockTime.Time
		v.BlockTime = &bt
	}
	if t.OrderID.Valid {
		id := t.OrderID.Int64
		v.OrderID = &id
	}
	if admin {
		v.Note = t.Note
		v.LastError = t.LastError
		if t.LastErrorAt.Valid {
			at := t.LastErrorAt.Time
			v.LastErrorAt = &at
		}
	}
	return v
}

// OtokUSDTOrderDetail 是 USDT 订单的付款信息。
type OtokUSDTOrderDetail struct {
	OrderID               int64                 `json:"order_id"`
	OutTradeNo            string                `json:"out_trade_no"`
	PaymentType           string                `json:"payment_type"`
	Network               string                `json:"network"`
	NetworkName           string                `json:"network_name"`
	Address               string                `json:"address"`
	TokenContract         string                `json:"token_contract"`
	Amount                string                `json:"amount"`
	StationAmount         float64               `json:"station_amount"`
	ExchangeRate          string                `json:"exchange_rate"`
	FeeRate               string                `json:"fee_rate"`
	RequiredConfirmations int64                 `json:"required_confirmations"`
	OrderStatus           string                `json:"order_status"`
	ReservationStatus     string                `json:"reservation_status"`
	ExpiresAt             time.Time             `json:"expires_at"`
	ReservedUntil         time.Time             `json:"reserved_until"`
	CreatedAt             time.Time             `json:"created_at"`
	CanSubmitTx           bool                  `json:"can_submit_tx"`
	Transfer              *OtokUSDTTransferView `json:"transfer,omitempty"`
}

func (s *OtokUSDTService) orderDetail(ctx context.Context, order *dbent.PaymentOrder, r *otokUSDTReservation) (*OtokUSDTOrderDetail, error) {
	return s.buildOrderDetail(ctx, order, r, false)
}

func (s *OtokUSDTService) buildOrderDetail(ctx context.Context, order *dbent.PaymentOrder, r *otokUSDTReservation, admin bool) (*OtokUSDTOrderDetail, error) {
	if fresh, err := s.entClient.PaymentOrder.Get(ctx, order.ID); err == nil {
		order = fresh
	}
	if fresh, err := otokGetReservation(ctx, s.entClient, r.OrderID); err == nil && fresh != nil {
		r = fresh
	}
	spec, err := otokchain.SpecFor(r.Network)
	if err != nil {
		return nil, err
	}
	d := &OtokUSDTOrderDetail{
		OrderID: order.ID, OutTradeNo: order.OutTradeNo, PaymentType: order.PaymentType,
		Network: string(r.Network), NetworkName: spec.DisplayName, Address: r.ToAddress, TokenContract: spec.TokenContract,
		Amount: otokchain.FormatCents(r.AmountCents), StationAmount: r.StationAmount,
		ExchangeRate: otokTrimDecimal(r.ExchangeRate), FeeRate: otokTrimDecimal(r.FeeRate), RequiredConfirmations: spec.Confirmations,
		OrderStatus: order.Status, ReservationStatus: r.Status, ExpiresAt: order.ExpiresAt,
		ReservedUntil: r.ReservedUntil, CreatedAt: order.CreatedAt,
	}
	if t, err := otokGetTransferByOrder(ctx, s.entClient, order.ID); err == nil && t != nil {
		d.Transfer = s.transferView(ctx, t, admin)
	}
	d.CanSubmitTx = d.Transfer == nil && otokOrderMatchable(order.Status) && time.Now().Before(r.ReservedUntil)
	return d, nil
}

// GetUserOrderDetail 返回用户自己订单的付款信息。
func (s *OtokUSDTService) GetUserOrderDetail(ctx context.Context, userID, orderID int64) (*OtokUSDTOrderDetail, error) {
	order, err := s.paymentSvc.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	r, err := otokGetReservation(ctx, s.entClient, order.ID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "not a USDT order")
	}
	return s.buildOrderDetail(ctx, order, r, false)
}

// GetAdminOrderDetail 返回订单的链上信息（管理端）。非 USDT 订单返回 nil。
func (s *OtokUSDTService) GetAdminOrderDetail(ctx context.Context, orderID int64) (*OtokUSDTOrderDetail, error) {
	order, err := s.paymentSvc.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	r, err := otokGetReservation(ctx, s.entClient, order.ID)
	if err != nil || r == nil {
		return nil, err
	}
	return s.buildOrderDetail(ctx, order, r, true)
}

// Quote 返回当前启用的 USDT 渠道报价参数；amount > 0 时附带预估应付金额。
func (s *OtokUSDTService) Quote(ctx context.Context, amount float64) (*OtokUSDTQuote, error) {
	inst, ucfg, err := s.enabledInstance(ctx)
	if err != nil {
		slog.Warn("[OtokUSDT] enabled instance config unreadable", "error", err)
		return &OtokUSDTQuote{Enabled: false}, nil
	}
	if inst == nil || ucfg == nil {
		return &OtokUSDTQuote{Enabled: false}, nil
	}
	enabled := map[string]bool{}
	for _, t := range splitTypes(inst.SupportedTypes) {
		enabled[t] = true
	}
	q := &OtokUSDTQuote{
		Enabled: true, ExchangeRate: ucfg.ExchangeRate.String(), FeeRate: ucfg.FeeRate.String(),
		ExpireMinutes: ucfg.ExpireMinutes, Networks: otokUSDTNetworkInfos(ucfg, enabled),
	}
	if amount > 0 {
		if cents, err := otokUSDTQuoteCents(amount, ucfg.FeeRate, ucfg.ExchangeRate); err == nil {
			q.EstimatedAmount = otokchain.FormatCents(cents)
		}
	}
	return q, nil
}

// ListTransfers 管理端转账列表。
func (s *OtokUSDTService) ListTransfers(ctx context.Context, f OtokUSDTTransferFilter) ([]*OtokUSDTTransferView, int64, error) {
	list, total, err := otokListTransfers(ctx, s.entClient, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*OtokUSDTTransferView, 0, len(list))
	for _, t := range list {
		out = append(out, s.transferView(ctx, t, true))
	}
	return out, total, nil
}

// OtokUSDTCursorView 是一条扫描游标的健康状态。
type OtokUSDTCursorView struct {
	Network       string     `json:"network"`
	Address       string     `json:"address"`
	LastBlock     int64      `json:"last_block"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
}

// OtokUSDTHealth 是监听服务健康状态。
type OtokUSDTHealth struct {
	Testnet             bool                 `json:"testnet"`
	ActiveReservations  int64                `json:"active_reservations"`
	UnmatchedTransfers  int64                `json:"unmatched_transfers"`
	ReviewTransfers     int64                `json:"review_transfers"`
	CreditFailed        int64                `json:"credit_failed_transfers"`
	AwaitingConfirmTxns int64                `json:"awaiting_confirmation_transfers"`
	CreditBlocked       int64                `json:"credit_blocked_transfers"`
	Cursors             []OtokUSDTCursorView `json:"cursors"`
}

func otokCount(ctx context.Context, ex otokExecutor, query string) int64 {
	rows, err := ex.QueryContext(ctx, query)
	if err != nil {
		return 0
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	return n
}

// Health 返回监听健康状态。
func (s *OtokUSDTService) Health(ctx context.Context) (*OtokUSDTHealth, error) {
	cursors, err := otokListCursors(ctx, s.entClient)
	if err != nil {
		return nil, err
	}
	h := &OtokUSDTHealth{
		Testnet:             otokchain.IsTestnet(),
		ActiveReservations:  otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_orders WHERE active`),
		UnmatchedTransfers:  otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_transfers WHERE status = 'seen' AND order_id IS NULL`),
		ReviewTransfers:     otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_transfers WHERE status = 'review'`),
		CreditFailed:        otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_transfers WHERE status = 'credit_failed'`),
		AwaitingConfirmTxns: otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_transfers WHERE status = 'bound'`),
		CreditBlocked:       otokCount(ctx, s.entClient, `SELECT COUNT(*) FROM otok_usdt_transfers WHERE status = 'bound' AND last_error <> ''`),
	}
	for _, c := range cursors {
		v := OtokUSDTCursorView{Network: string(c.Network), Address: c.AddressNorm, LastBlock: c.LastBlock, LastError: c.LastError}
		if c.LastSuccessAt.Valid {
			t := c.LastSuccessAt.Time
			v.LastSuccessAt = &t
		}
		if c.LastErrorAt.Valid {
			t := c.LastErrorAt.Time
			v.LastErrorAt = &t
		}
		h.Cursors = append(h.Cursors, v)
	}
	return h, nil
}
