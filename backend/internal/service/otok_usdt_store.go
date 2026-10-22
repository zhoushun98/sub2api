package service

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
)

// otok: USDT 渠道的原生 SQL 存储（表见 migrations/otok_0001_usdt.sql）。
// 不使用 Ent schema，避免同步上游时与 Ent 生成代码冲突。

// otokExecutor 由 *ent.Client、*ent.Tx 与 *sql.DB 共同满足。
type otokExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const (
	// otokUSDTAmountSlots 是同一基础金额可用的识别尾差个数（+0.01 ~ +0.99）。
	// 不分配 +0.00：报价展示的预估金额就是基础金额，没按尾差付款的转账最容易撞上它，
	// 不能让这类转账自动匹配到任何订单。
	otokUSDTAmountSlots = 99
	// otokUSDTMaxActivePerUser 是单个用户同时占用的待付款金额上限，防止批量下单占满尾差。
	otokUSDTMaxActivePerUser = 5
	// otokUSDTReleaseMargin：保留期结束后金额再多占用一段时间，给监听器发现临界到账留出余量。
	otokUSDTReleaseMargin = 5 * time.Minute
	// otokUSDTCancelledHold：订单取消后金额只再占用这么久，之后才能分配给新订单。
	otokUSDTCancelledHold = 10 * time.Minute
)

// 预留状态（pending / bound / credited / released）直接写在 SQL 里，见 otok_0001_usdt.sql。

// 转账状态。
const (
	otokTransferSeen         = "seen"
	otokTransferBound        = "bound"
	otokTransferCredited     = "credited"
	otokTransferCreditFailed = "credit_failed"
	otokTransferReview       = "review"
	otokTransferIgnored      = "ignored"
	otokTransferDropped      = "dropped"
)

// 转账来源。
const (
	otokSourceWatcher = "watcher"
	otokSourceUser    = "user"
	otokSourceAdmin   = "admin"
)

type otokUSDTReservation struct {
	OrderID           int64
	OutTradeNo        string
	InstanceID        string
	Network           otokchain.Network
	TokenContract     string
	Decimals          int32
	ToAddress         string
	ToAddressNorm     string
	BaseAmountCents   int64
	AmountCents       int64
	StationAmount     float64
	ExchangeRate      string
	FeeRate           string
	Status            string
	Active            bool
	ExpiresAt         time.Time
	ReservedUntil     time.Time
	MatchedTransferID sql.NullInt64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

const otokReservationColumns = `order_id, out_trade_no, instance_id, network, token_contract, decimals, to_address,
	to_address_norm, base_amount_cents, amount_cents, station_amount::float8, exchange_rate::text, fee_rate::text,
	status, active, expires_at, reserved_until, matched_transfer_id, created_at, updated_at`

func scanOtokReservation(rows *sql.Rows) (*otokUSDTReservation, error) {
	var r otokUSDTReservation
	var network string
	if err := rows.Scan(&r.OrderID, &r.OutTradeNo, &r.InstanceID, &network, &r.TokenContract, &r.Decimals, &r.ToAddress,
		&r.ToAddressNorm, &r.BaseAmountCents, &r.AmountCents, &r.StationAmount, &r.ExchangeRate, &r.FeeRate,
		&r.Status, &r.Active, &r.ExpiresAt, &r.ReservedUntil, &r.MatchedTransferID, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Network = otokchain.Network(network)
	return &r, nil
}

type otokUSDTTransfer struct {
	ID             int64
	Network        otokchain.Network
	TxHash         string
	LogIndex       int64
	BlockNumber    int64
	BlockHash      string
	BlockTime      sql.NullTime
	FromAddress    string
	ToAddressNorm  string
	TokenContract  string
	AmountRaw      *big.Int
	Decimals       int32
	OrderID        sql.NullInt64
	Source         string
	Status         string
	Note           string
	CreditAttempts int
	// AmountOverride 为 true 表示由管理员补单绑定，入账时不要求金额与订单一致。
	AmountOverride bool
	// LastError 是最近一次入账复核失败的原因，复核成功后清空。
	LastError   string
	LastErrorAt sql.NullTime
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const otokTransferColumns = `id, network, tx_hash, log_index, block_number, block_hash, block_time, from_address,
	to_address_norm, token_contract, amount_raw::text, decimals, order_id, source, status, note, credit_attempts,
	amount_override, last_error, last_error_at, created_at, updated_at`

func scanOtokTransfer(rows *sql.Rows) (*otokUSDTTransfer, error) {
	var t otokUSDTTransfer
	var network, amount string
	if err := rows.Scan(&t.ID, &network, &t.TxHash, &t.LogIndex, &t.BlockNumber, &t.BlockHash, &t.BlockTime, &t.FromAddress,
		&t.ToAddressNorm, &t.TokenContract, &amount, &t.Decimals, &t.OrderID, &t.Source, &t.Status, &t.Note, &t.CreditAttempts,
		&t.AmountOverride, &t.LastError, &t.LastErrorAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Network = otokchain.Network(network)
	raw, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount_raw %q", amount)
	}
	t.AmountRaw = raw
	return &t, nil
}

// AmountCents 返回转账金额（分）与是否恰好为两位小数。
func (t *otokUSDTTransfer) AmountCents() (int64, bool) {
	return otokchain.RawToCents(t.AmountRaw, t.Decimals)
}

func otokQueryRows[T any](ctx context.Context, ex otokExecutor, scan func(*sql.Rows) (*T, error), query string, args ...any) ([]*T, error) {
	rows, err := ex.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func otokQueryInt64(ctx context.Context, ex otokExecutor, query string, args ...any) (int64, error) {
	rows, err := ex.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

func otokQueryOne[T any](ctx context.Context, ex otokExecutor, scan func(*sql.Rows) (*T, error), query string, args ...any) (*T, error) {
	list, err := otokQueryRows(ctx, ex, scan, query, args...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list[0], nil
}

// otokPickFreeAmountCents 在 (base, base+slots] 中挑选最小的未占用金额。
func otokPickFreeAmountCents(base int64, taken map[int64]bool) (int64, bool) {
	for i := int64(1); i <= otokUSDTAmountSlots; i++ {
		if !taken[base+i] {
			return base + i, true
		}
	}
	return 0, false
}

// otokReserveAmount 在调用方事务内为订单分配唯一金额并写入预留记录。
//   - 先按用户加事务级 advisory lock，限制同一用户同时占用的金额数；
//   - 再按收款地址加锁：同一地址在所有网络间金额唯一（EVM 网络通常共用一个地址，
//     付错网络的转账不能匹配到别人在另一网络上的同金额订单），并顺带释放该地址已过期的预留。
func otokReserveAmount(ctx context.Context, ex otokExecutor, userID int64, r *otokUSDTReservation) error {
	if _, err := ex.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("otok_usdt:user:%d", userID)); err != nil {
		return fmt.Errorf("lock usdt user: %w", err)
	}
	active, err := otokQueryInt64(ctx, ex, `SELECT COUNT(*) FROM otok_usdt_orders r JOIN payment_orders o ON o.id = r.order_id
		WHERE o.user_id = $1 AND r.active AND r.status = 'pending'`, userID)
	if err != nil {
		return fmt.Errorf("count active usdt reservations: %w", err)
	}
	if active >= otokUSDTMaxActivePerUser {
		return infraerrors.TooManyRequests("USDT_TOO_MANY_ACTIVE_ORDERS", "too many unpaid USDT orders; pay or wait for earlier orders to expire").
			WithMetadata(map[string]string{"max": fmt.Sprint(otokUSDTMaxActivePerUser)})
	}
	if _, err := ex.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		"otok_usdt:addr:"+r.ToAddressNorm); err != nil {
		return fmt.Errorf("lock usdt amount slot: %w", err)
	}
	if _, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders
		SET active = FALSE, status = CASE WHEN status = 'pending' THEN 'released' ELSE status END, updated_at = NOW()
		WHERE to_address_norm = $1 AND active AND reserved_until < NOW() - make_interval(secs => $2)`,
		r.ToAddressNorm, otokUSDTReleaseMargin.Seconds()); err != nil {
		return fmt.Errorf("release expired usdt reservations: %w", err)
	}
	rows, err := ex.QueryContext(ctx, `SELECT amount_cents FROM otok_usdt_orders
		WHERE to_address_norm = $1 AND active AND amount_cents > $2 AND amount_cents <= $3`,
		r.ToAddressNorm, r.BaseAmountCents, r.BaseAmountCents+otokUSDTAmountSlots)
	if err != nil {
		return fmt.Errorf("query usdt amount slots: %w", err)
	}
	taken := map[int64]bool{}
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			_ = rows.Close()
			return err
		}
		taken[c] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	amount, ok := otokPickFreeAmountCents(r.BaseAmountCents, taken)
	if !ok {
		return infraerrors.TooManyRequests("USDT_AMOUNT_SLOTS_EXHAUSTED", "too many pending USDT orders for this amount, please retry later or change the amount")
	}
	r.AmountCents = amount
	_, err = ex.ExecContext(ctx, `INSERT INTO otok_usdt_orders (order_id, out_trade_no, instance_id, network, token_contract,
		decimals, to_address, to_address_norm, base_amount_cents, amount_cents, station_amount, exchange_rate, fee_rate,
		status, active, expires_at, reserved_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'pending', TRUE, $14, $15)`,
		r.OrderID, r.OutTradeNo, r.InstanceID, string(r.Network), r.TokenContract, r.Decimals, r.ToAddress, r.ToAddressNorm,
		r.BaseAmountCents, r.AmountCents, r.StationAmount, r.ExchangeRate, r.FeeRate, r.ExpiresAt, r.ReservedUntil)
	if err != nil {
		return fmt.Errorf("insert usdt reservation: %w", err)
	}
	return nil
}

func otokGetReservation(ctx context.Context, ex otokExecutor, orderID int64) (*otokUSDTReservation, error) {
	return otokQueryOne(ctx, ex, scanOtokReservation,
		`SELECT `+otokReservationColumns+` FROM otok_usdt_orders WHERE order_id = $1`, orderID)
}

// otokListActiveReservations 返回仍在等待转账的预留（未绑定转账）。
func otokListActiveReservations(ctx context.Context, ex otokExecutor) ([]*otokUSDTReservation, error) {
	return otokQueryRows(ctx, ex, scanOtokReservation,
		`SELECT `+otokReservationColumns+` FROM otok_usdt_orders WHERE active AND status = 'pending' ORDER BY created_at`)
}

// otokReleaseReservations 释放不再需要占用金额的预留：已过保留期（另加 otokUSDTReleaseMargin）、
// 订单已入账，以及从未付款就失败的订单（上游会直接履约 FAILED 订单，必须阻止其被匹配）。
// 已取消订单的保留期先缩短到取消后 otokUSDTCancelledHold。
func otokReleaseReservations(ctx context.Context, ex otokExecutor) (int64, error) {
	if _, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders r
		SET reserved_until = o.updated_at + make_interval(secs => $1), updated_at = NOW()
		FROM payment_orders o
		WHERE r.order_id = o.id AND r.active AND r.status = 'pending' AND o.status = 'CANCELLED'
			AND r.reserved_until > o.updated_at + make_interval(secs => $1)`, otokUSDTCancelledHold.Seconds()); err != nil {
		return 0, err
	}
	res, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders r
		SET active = FALSE, status = CASE WHEN r.status = 'pending' THEN 'released' ELSE r.status END, updated_at = NOW()
		FROM payment_orders o
		WHERE r.order_id = o.id AND r.active AND (
			r.reserved_until < NOW() - make_interval(secs => $1)
			OR o.status IN ('PAID', 'RECHARGING', 'COMPLETED', 'REFUND_REQUESTED', 'REFUNDING', 'REFUND_PENDING',
				'PARTIALLY_REFUNDED', 'REFUNDED', 'REFUND_FAILED')
			OR (o.status = 'FAILED' AND o.paid_at IS NULL)
		)`, otokUSDTReleaseMargin.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// otokUpsertTransfer 记录一笔链上转账；已存在时只在未入账前刷新区块信息。
func otokUpsertTransfer(ctx context.Context, ex otokExecutor, t otokchain.Transfer, tokenContract string, decimals int32, source string) (*otokUSDTTransfer, error) {
	var blockTime any
	if !t.BlockTime.IsZero() {
		blockTime = t.BlockTime
	}
	return otokQueryOne(ctx, ex, scanOtokTransfer, `INSERT INTO otok_usdt_transfers (network, tx_hash, log_index,
		block_number, block_hash, block_time, from_address, to_address_norm, token_contract, amount_raw, decimals, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::numeric, $11, $12)
		ON CONFLICT (network, tx_hash, log_index) DO UPDATE SET
			block_number = CASE WHEN otok_usdt_transfers.status IN ('seen', 'bound', 'review', 'dropped') AND EXCLUDED.block_number > 0
				THEN EXCLUDED.block_number ELSE otok_usdt_transfers.block_number END,
			block_hash = CASE WHEN otok_usdt_transfers.status IN ('seen', 'bound', 'review', 'dropped') AND EXCLUDED.block_hash <> ''
				THEN EXCLUDED.block_hash ELSE otok_usdt_transfers.block_hash END,
			block_time = COALESCE(otok_usdt_transfers.block_time, EXCLUDED.block_time),
			updated_at = NOW()
		RETURNING `+otokTransferColumns,
		string(t.Network), t.TxHash, t.LogIndex, t.BlockNumber, t.BlockHash, blockTime, t.From, t.ToNorm,
		tokenContract, t.Amount.String(), decimals, source)
}

func otokGetTransfer(ctx context.Context, ex otokExecutor, id int64) (*otokUSDTTransfer, error) {
	return otokQueryOne(ctx, ex, scanOtokTransfer, `SELECT `+otokTransferColumns+` FROM otok_usdt_transfers WHERE id = $1`, id)
}

func otokGetTransferByOrder(ctx context.Context, ex otokExecutor, orderID int64) (*otokUSDTTransfer, error) {
	return otokQueryOne(ctx, ex, scanOtokTransfer, `SELECT `+otokTransferColumns+` FROM otok_usdt_transfers WHERE order_id = $1`, orderID)
}

// otokListPendingCreditTransfers 返回已绑定订单、等待确认或重试入账的转账。
func otokListPendingCreditTransfers(ctx context.Context, ex otokExecutor, maxAttempts int) ([]*otokUSDTTransfer, error) {
	return otokQueryRows(ctx, ex, scanOtokTransfer, `SELECT `+otokTransferColumns+` FROM otok_usdt_transfers
		WHERE order_id IS NOT NULL AND (status = 'bound' OR (status = 'credit_failed' AND credit_attempts < $1
			AND updated_at < NOW() - credit_attempts * INTERVAL '1 minute'))
		ORDER BY created_at LIMIT 200`, maxAttempts)
}

// otokBindTransfer 在事务内把转账绑定到订单，以下情况返回 false：
//   - 转账已被占用，或订单已绑定其他转账；
//   - 同一笔链上付款的另一条记录（链重组后日志序号变化产生的副本）已绑定过订单；
//   - 非管理员绑定时，转账不是 seen 状态（待审核 / 已解绑的转账只能由管理员处理）。
//
// admin=true 时同时记 amount_override，入账时不再要求金额与订单一致。
func otokBindTransfer(ctx context.Context, ex otokExecutor, transferID, orderID int64, note string, admin bool) (bool, error) {
	// 同一交易的绑定串行化，保证下面的副本检查看得到并发事务的结果。
	if _, err := ex.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('otok_usdt:tx:' || network || ':' || tx_hash))
		FROM otok_usdt_transfers WHERE id = $1`, transferID); err != nil {
		return false, err
	}
	res, err := ex.ExecContext(ctx, `UPDATE otok_usdt_transfers t
		SET order_id = $2, status = 'bound', note = $3, amount_override = $4, updated_at = NOW()
		WHERE t.id = $1 AND t.order_id IS NULL
		AND (t.status = 'seen' OR ($4 AND t.status IN ('review', 'dropped')))
		AND NOT EXISTS (SELECT 1 FROM otok_usdt_transfers x WHERE x.order_id = $2)
		AND NOT EXISTS (SELECT 1 FROM otok_usdt_transfers y WHERE y.id <> t.id AND y.order_id IS NOT NULL
			AND y.network = t.network AND y.tx_hash = t.tx_hash AND y.to_address_norm = t.to_address_norm
			AND y.amount_raw = t.amount_raw)`, transferID, orderID, note, admin)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders SET status = 'bound', matched_transfer_id = $1, updated_at = NOW()
		WHERE order_id = $2 AND status IN ('pending', 'released')`, transferID, orderID); err != nil {
		return false, err
	}
	return true, nil
}

// otokUnbindTransfer 解除绑定（链重组、链上校验失败、金额不符时使用），订单恢复为等待转账。
func otokUnbindTransfer(ctx context.Context, ex otokExecutor, transferID int64, note string) error {
	if _, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders SET status = 'pending', matched_transfer_id = NULL, updated_at = NOW()
		WHERE matched_transfer_id = $1 AND status = 'bound'`, transferID); err != nil {
		return err
	}
	_, err := ex.ExecContext(ctx, `UPDATE otok_usdt_transfers SET order_id = NULL, status = 'dropped', amount_override = FALSE,
		note = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('bound', 'credit_failed')`, transferID, note)
	return err
}

func otokSetTransferStatus(ctx context.Context, ex otokExecutor, id int64, status, note string, incAttempts bool) error {
	inc := 0
	if incAttempts {
		inc = 1
	}
	_, err := ex.ExecContext(ctx, `UPDATE otok_usdt_transfers SET status = $2,
		note = CASE WHEN $3 = '' THEN note ELSE $3 END, credit_attempts = credit_attempts + $4, updated_at = NOW()
		WHERE id = $1`, id, status, note, inc)
	return err
}

// otokSaveTransferError 记录入账复核失败原因；传 nil 清空。不改 updated_at，
// 以免影响 credit_failed 的退避和「链上消失」的判定。
func otokSaveTransferError(ctx context.Context, ex otokExecutor, id int64, creditErr error) error {
	if creditErr == nil {
		_, err := ex.ExecContext(ctx, `UPDATE otok_usdt_transfers SET last_error = '', last_error_at = NULL
			WHERE id = $1 AND last_error <> ''`, id)
		return err
	}
	msg := creditErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := ex.ExecContext(ctx, `UPDATE otok_usdt_transfers SET last_error = $2, last_error_at = NOW() WHERE id = $1`, id, msg)
	return err
}

func otokMarkReservationCredited(ctx context.Context, ex otokExecutor, orderID int64) error {
	_, err := ex.ExecContext(ctx, `UPDATE otok_usdt_orders SET status = 'credited', active = FALSE, updated_at = NOW()
		WHERE order_id = $1`, orderID)
	return err
}

type otokScanCursor struct {
	Network       otokchain.Network
	AddressNorm   string
	LastBlock     int64
	LastTsMs      int64
	LastSuccessAt sql.NullTime
	LastError     string
	LastErrorAt   sql.NullTime
}

func scanOtokCursor(rows *sql.Rows) (*otokScanCursor, error) {
	var c otokScanCursor
	var network string
	if err := rows.Scan(&network, &c.AddressNorm, &c.LastBlock, &c.LastTsMs, &c.LastSuccessAt, &c.LastError, &c.LastErrorAt); err != nil {
		return nil, err
	}
	c.Network = otokchain.Network(network)
	return &c, nil
}

const otokCursorColumns = `network, address_norm, last_block, last_ts_ms, last_success_at, last_error, last_error_at`

func otokGetCursor(ctx context.Context, ex otokExecutor, network otokchain.Network, addressNorm string) (*otokScanCursor, error) {
	return otokQueryOne(ctx, ex, scanOtokCursor, `SELECT `+otokCursorColumns+` FROM otok_usdt_scan_cursors
		WHERE network = $1 AND address_norm = $2`, string(network), addressNorm)
}

func otokListCursors(ctx context.Context, ex otokExecutor) ([]*otokScanCursor, error) {
	return otokQueryRows(ctx, ex, scanOtokCursor, `SELECT `+otokCursorColumns+` FROM otok_usdt_scan_cursors ORDER BY network, address_norm`)
}

func otokSaveCursorSuccess(ctx context.Context, ex otokExecutor, network otokchain.Network, addressNorm string, lastBlock, lastTsMs int64) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO otok_usdt_scan_cursors (network, address_norm, last_block, last_ts_ms, last_success_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (network, address_norm) DO UPDATE SET
			last_block = GREATEST(otok_usdt_scan_cursors.last_block, EXCLUDED.last_block),
			last_ts_ms = GREATEST(otok_usdt_scan_cursors.last_ts_ms, EXCLUDED.last_ts_ms),
			last_success_at = NOW(), last_error = '', updated_at = NOW()`,
		string(network), addressNorm, lastBlock, lastTsMs)
	return err
}

func otokSaveCursorError(ctx context.Context, ex otokExecutor, network otokchain.Network, addressNorm string, scanErr error) error {
	msg := scanErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO otok_usdt_scan_cursors (network, address_norm, last_error, last_error_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (network, address_norm) DO UPDATE SET last_error = EXCLUDED.last_error, last_error_at = NOW(), updated_at = NOW()`,
		string(network), addressNorm, msg)
	return err
}

// OtokUSDTTransferFilter 是管理端转账列表的筛选条件。
type OtokUSDTTransferFilter struct {
	Status   string
	Network  string
	Keyword  string // 交易哈希 / 付款地址 / 订单号
	Page     int
	PageSize int
}

func otokListTransfers(ctx context.Context, ex otokExecutor, f OtokUSDTTransferFilter) ([]*otokUSDTTransfer, int64, error) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.Network != "" {
		add("network = ?", f.Network)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		args = append(args, "%"+strings.TrimPrefix(strings.ToLower(kw), "0x")+"%", kw)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(tx_hash ILIKE $%d OR from_address ILIKE $%d OR CAST(order_id AS TEXT) = $%d)", n-1, n-1, n))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int64
	rows, err := ex.QueryContext(ctx, `SELECT COUNT(*) FROM otok_usdt_transfers`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	if rows.Next() {
		if err := rows.Scan(&total); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	args = append(args, size, (page-1)*size)
	list, err := otokQueryRows(ctx, ex, scanOtokTransfer, `SELECT `+otokTransferColumns+` FROM otok_usdt_transfers`+where+
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
