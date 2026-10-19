//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// otok: USDT 存储层的 PostgreSQL 集成测试（advisory lock、部分唯一索引等 PG 专有语义）。
// service 包不能依赖 repository，这里自行按需启动 PG 容器，并用 Ent schema 建上游表。

var (
	otokPGOnce   sync.Once
	otokPGClient *dbent.Client
	otokPGErr    error
	otokOrderSeq atomic.Int64
)

func otokIntegrationClient(t *testing.T) *dbent.Client {
	t.Helper()
	otokPGOnce.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
			tcpostgres.WithDatabase("otok_test"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("postgres"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			otokPGErr = err
			return
		}
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			otokPGErr = err
			return
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			otokPGErr = err
			return
		}
		// 与生产一样限制连接池，避免并发用例超过 PostgreSQL 默认的 100 连接上限。
		db.SetMaxOpenConns(20)
		otokPGClient = enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.Postgres, db))))
		otokPGErr = otokApplyMigration(ctx, otokPGClient)
	})
	if otokPGErr != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("postgres unavailable: %v", otokPGErr)
		}
		t.Skipf("postgres (docker) unavailable: %v", otokPGErr)
	}
	ctx := context.Background()
	_, err := otokPGClient.ExecContext(ctx, `TRUNCATE otok_usdt_transfers, otok_usdt_orders, otok_usdt_scan_cursors RESTART IDENTITY`)
	require.NoError(t, err)
	return otokPGClient
}

func otokApplyMigration(ctx context.Context, client *dbent.Client) error {
	for _, name := range []string{"otok_0001_usdt.sql", "otok_0002_usdt_hardening.sql"} {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := client.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func otokCreateTestUser(t *testing.T, ctx context.Context, client *dbent.Client) *dbent.User {
	t.Helper()
	n := otokOrderSeq.Add(1)
	user, err := client.User.Create().
		SetEmail(fmt.Sprintf("otok-%d-%d@example.com", n, time.Now().UnixNano())).
		SetPasswordHash("hash").SetUsername("otok-user").Save(ctx)
	require.NoError(t, err)
	return user
}

// otokCreateTestOrder 为一个新用户创建订单。
func otokCreateTestOrder(t *testing.T, ctx context.Context, client *dbent.Client, status string) *dbent.PaymentOrder {
	t.Helper()
	return otokCreateTestOrderFor(t, ctx, client, otokCreateTestUser(t, ctx, client), status)
}

func otokCreateTestOrderFor(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, status string) *dbent.PaymentOrder {
	t.Helper()
	n := otokOrderSeq.Add(1)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(100).SetPayAmount(101.5).SetFeeRate(0).
		SetRechargeCode("PAY-OTOK-" + strconv.FormatInt(n, 10)).
		SetOutTradeNo(fmt.Sprintf("sub2_otok_%d_%d", n, time.Now().UnixNano())).
		SetPaymentType(payment.TypeOtokUSDTTRC20).SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).SetStatus(status).
		SetExpiresAt(time.Now().Add(30 * time.Minute)).
		SetClientIP("127.0.0.1").SetSrcHost("test").Save(ctx)
	require.NoError(t, err)
	return order
}

func otokTestReservation(order *dbent.PaymentOrder, base int64) *otokUSDTReservation {
	now := time.Now()
	return &otokUSDTReservation{
		OrderID: order.ID, OutTradeNo: order.OutTradeNo, InstanceID: "1",
		Network: otokchain.NetworkTRC20, TokenContract: "a614f803b6fd780986a42c78ec9c7f77e6ded13c", Decimals: 6,
		ToAddress: otokTestTronAddress, ToAddressNorm: otokTestTronAddress,
		BaseAmountCents: base, StationAmount: 100, ExchangeRate: "7.1", FeeRate: "0",
		ExpiresAt: now.Add(30 * time.Minute), ReservedUntil: now.Add(90 * time.Minute),
	}
}

func otokReserveInTx(ctx context.Context, client *dbent.Client, r *otokUSDTReservation) error {
	order, err := client.PaymentOrder.Get(ctx, r.OrderID)
	if err != nil {
		return err
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := otokReserveAmount(ctx, tx, order.UserID, r); err != nil {
		return err
	}
	return tx.Commit()
}

func TestOtokUSDTMigrationIdempotent(t *testing.T) {
	client := otokIntegrationClient(t)
	require.NoError(t, otokApplyMigration(context.Background(), client))
}

func TestOtokUSDTConcurrentReservationsAreUnique(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	const workers = otokUSDTAmountSlots + 1
	orders := make([]*dbent.PaymentOrder, workers)
	for i := range orders {
		orders[i] = otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	}
	var wg sync.WaitGroup
	results := make([]*otokUSDTReservation, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := otokTestReservation(orders[i], 1409)
			errs[i] = otokReserveInTx(ctx, client, r)
			results[i] = r
		}(i)
	}
	wg.Wait()

	seen := map[int64]bool{}
	failures := 0
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			failures++
			require.Equal(t, "USDT_AMOUNT_SLOTS_EXHAUSTED", infraerrors.Reason(errs[i]), "err=%v", errs[i])
			continue
		}
		amt := results[i].AmountCents
		require.False(t, seen[amt], "duplicate amount %d", amt)
		require.Greater(t, amt, int64(1409), "the base amount (+0.00) is never assigned")
		require.LessOrEqual(t, amt, int64(1409+otokUSDTAmountSlots))
		seen[amt] = true
	}
	require.Equal(t, 1, failures, "exactly one order should overflow the 99 slots")
	require.Len(t, seen, otokUSDTAmountSlots)
}

func TestOtokUSDTAmountUniqueAcrossNetworks(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	evm := func(network otokchain.Network) *otokUSDTReservation {
		r := otokTestReservation(otokCreateTestOrder(t, ctx, client, OrderStatusPending), 1409)
		r.Network = network
		r.ToAddress = otokTestEVMAddress
		r.ToAddressNorm = strings.ToLower(otokTestEVMAddress)
		return r
	}
	bsc, polygon := evm(otokchain.NetworkBEP20), evm(otokchain.NetworkPolygon)
	require.NoError(t, otokReserveInTx(ctx, client, bsc))
	require.NoError(t, otokReserveInTx(ctx, client, polygon))
	// 同一 EVM 地址在不同网络上也不会分到同一金额：付错网络的转账不会匹配到别人的订单。
	require.NotEqual(t, bsc.AmountCents, polygon.AmountCents)

	// 唯一索引本身也按地址跨网络生效。
	dup := otokTestReservation(otokCreateTestOrder(t, ctx, client, OrderStatusPending), 1409)
	_, err := client.ExecContext(ctx, `INSERT INTO otok_usdt_orders (order_id, out_trade_no, instance_id, network, token_contract,
		decimals, to_address, to_address_norm, base_amount_cents, amount_cents, station_amount, exchange_rate, fee_rate,
		status, active, expires_at, reserved_until)
		VALUES ($1, $2, '1', 'erc20', 'x', 6, $3, $4, 1409, $5, 100, 7.1, 0, 'pending', TRUE, NOW(), NOW())`,
		dup.OrderID, dup.OutTradeNo, otokTestEVMAddress, strings.ToLower(otokTestEVMAddress), bsc.AmountCents)
	require.Error(t, err)
}

func TestOtokUSDTPerUserActiveLimit(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	user := otokCreateTestUser(t, ctx, client)
	var first *dbent.PaymentOrder
	for i := 0; i < otokUSDTMaxActivePerUser; i++ {
		o := otokCreateTestOrderFor(t, ctx, client, user, OrderStatusPending)
		if first == nil {
			first = o
		}
		require.NoError(t, otokReserveInTx(ctx, client, otokTestReservation(o, int64(1000+i*100))))
	}
	extra := otokCreateTestOrderFor(t, ctx, client, user, OrderStatusPending)
	err := otokReserveInTx(ctx, client, otokTestReservation(extra, 9000))
	require.Error(t, err)
	require.Equal(t, "USDT_TOO_MANY_ACTIVE_ORDERS", infraerrors.Reason(err))

	// 其他用户不受影响
	other := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	require.NoError(t, otokReserveInTx(ctx, client, otokTestReservation(other, 9000)))

	// 旧订单入账（预留释放）后可以继续下单
	_, err = client.PaymentOrder.UpdateOneID(first.ID).SetStatus(OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)
	_, err = otokReleaseReservations(ctx, client)
	require.NoError(t, err)
	require.NoError(t, otokReserveInTx(ctx, client, otokTestReservation(extra, 9000)))
}

func TestOtokUSDTReleaseRules(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()

	expired := otokCreateTestOrder(t, ctx, client, OrderStatusExpired)
	rExpired := otokTestReservation(expired, 2000)
	rExpired.ReservedUntil = time.Now().Add(-otokUSDTReleaseMargin - time.Minute)
	require.NoError(t, otokReserveInTx(ctx, client, rExpired))

	// 刚过保留期、还在释放余量内的预留继续占用金额。
	inMargin := otokCreateTestOrder(t, ctx, client, OrderStatusExpired)
	rInMargin := otokTestReservation(inMargin, 2500)
	rInMargin.ReservedUntil = time.Now().Add(-time.Minute)
	require.NoError(t, otokReserveInTx(ctx, client, rInMargin))

	failed := otokCreateTestOrder(t, ctx, client, OrderStatusFailed)
	rFailed := otokTestReservation(failed, 3000)
	require.NoError(t, otokReserveInTx(ctx, client, rFailed))

	cancelled := otokCreateTestOrder(t, ctx, client, OrderStatusCancelled)
	rCancelled := otokTestReservation(cancelled, 4000)
	require.NoError(t, otokReserveInTx(ctx, client, rCancelled))

	// 同地址下一次预留时已顺带释放过期预留（自愈）；watcher 释放剩下的 FAILED 订单。
	selfHealed, err := otokGetReservation(ctx, client, expired.ID)
	require.NoError(t, err)
	require.False(t, selfHealed.Active)
	require.Equal(t, otokReservationReleased, selfHealed.Status)

	n, err := otokReleaseReservations(ctx, client)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "FAILED-without-payment is released by the watcher")

	active, err := otokListActiveReservations(ctx, client)
	require.NoError(t, err)
	require.Len(t, active, 2)
	activeIDs := map[int64]*otokUSDTReservation{}
	for _, r := range active {
		activeIDs[r.OrderID] = r
	}
	require.Contains(t, activeIDs, inMargin.ID)
	require.Contains(t, activeIDs, cancelled.ID)
	// 已取消订单的保留期缩短到取消后 10 分钟。
	require.WithinDuration(t, time.Now().Add(otokUSDTCancelledHold), activeIDs[cancelled.ID].ReservedUntil, time.Minute)

	// 取消 20 分钟后，金额被释放。
	_, err = client.ExecContext(ctx, `UPDATE payment_orders SET updated_at = NOW() - INTERVAL '20 minutes' WHERE id = $1`, cancelled.ID)
	require.NoError(t, err)
	n, err = otokReleaseReservations(ctx, client)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	gone, err := otokGetReservation(ctx, client, cancelled.ID)
	require.NoError(t, err)
	require.False(t, gone.Active)

	// 释放后的金额可以被新订单复用
	reuse := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	rReuse := otokTestReservation(reuse, 3000)
	require.NoError(t, otokReserveInTx(ctx, client, rReuse))
	require.EqualValues(t, rFailed.AmountCents, rReuse.AmountCents)
}

func TestOtokUSDTBindTransferOnlyOnce(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	svc := NewOtokUSDTService(&PaymentService{entClient: client}, client, 0)

	ordersA := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	ordersB := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	ordersC := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	for i, o := range []*dbent.PaymentOrder{ordersA, ordersB, ordersC} {
		require.NoError(t, otokReserveInTx(ctx, client, otokTestReservation(o, int64(5000+i*10))))
	}
	tr := otokchain.Transfer{
		Network: otokchain.NetworkTRC20, TxHash: "ab" + fmt.Sprintf("%062d", 1), BlockTime: time.Now(),
		From: otokTestTronAddress, ToNorm: otokTestTronAddress, Amount: big.NewInt(50_000_000),
	}
	row, err := otokUpsertTransfer(ctx, client, tr, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", 6, otokSourceWatcher)
	require.NoError(t, err)
	again, err := otokUpsertTransfer(ctx, client, tr, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", 6, otokSourceUser)
	require.NoError(t, err)
	require.Equal(t, row.ID, again.ID, "same (network, tx, log) is recorded once")

	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, o := range []*dbent.PaymentOrder{ordersA, ordersB, ordersC} {
		wg.Add(1)
		go func(orderID int64) {
			defer wg.Done()
			if ok, err := svc.bindTransfer(ctx, row.ID, orderID, "test", false); err == nil && ok {
				wins.Add(1)
			}
		}(o.ID)
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())

	// 已绑定交易的订单不能再绑定第二笔交易
	bound, err := otokGetTransfer(ctx, client, row.ID)
	require.NoError(t, err)
	require.True(t, bound.OrderID.Valid)
	tr2 := tr
	tr2.TxHash = "cd" + fmt.Sprintf("%062d", 2)
	row2, err := otokUpsertTransfer(ctx, client, tr2, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", 6, otokSourceWatcher)
	require.NoError(t, err)
	ok, err := svc.bindTransfer(ctx, row2.ID, bound.OrderID.Int64, "test", false)
	require.NoError(t, err)
	require.False(t, ok)

	// 链重组后同一笔付款换了日志序号：副本不能再绑定到其他订单（防一笔付款入账两次）。
	var other int64
	for _, o := range []*dbent.PaymentOrder{ordersA, ordersB, ordersC} {
		if o.ID != bound.OrderID.Int64 {
			other = o.ID
		}
	}
	copyTr := tr
	copyTr.LogIndex = 3
	copyRow, err := otokUpsertTransfer(ctx, client, copyTr, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", 6, otokSourceWatcher)
	require.NoError(t, err)
	require.NotEqual(t, row.ID, copyRow.ID)
	ok, err = svc.bindTransfer(ctx, copyRow.ID, other, "test", true)
	require.NoError(t, err)
	require.False(t, ok)

	// 待审核的转账只能由管理员绑定，并会记 amount_override。
	require.NoError(t, otokSetTransferStatus(ctx, client, row2.ID, otokTransferReview, "manual", false))
	ok, err = svc.bindTransfer(ctx, row2.ID, other, "test", false)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = svc.bindTransfer(ctx, row2.ID, other, "admin", true)
	require.NoError(t, err)
	require.True(t, ok)
	adminBound, err := otokGetTransfer(ctx, client, row2.ID)
	require.NoError(t, err)
	require.True(t, adminBound.AmountOverride)

	// 解绑后订单恢复为等待转账，amount_override 清除
	require.NoError(t, otokUnbindTransfer(ctx, client, row.ID, "reorg"))
	r, err := otokGetReservation(ctx, client, bound.OrderID.Int64)
	require.NoError(t, err)
	require.Equal(t, otokReservationPending, r.Status)
	require.NoError(t, otokUnbindTransfer(ctx, client, row2.ID, "test"))
	unbound, err := otokGetTransfer(ctx, client, row2.ID)
	require.NoError(t, err)
	require.False(t, unbound.AmountOverride)
	require.False(t, unbound.OrderID.Valid)
}

func TestOtokUSDTListTransfersFilter(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := otokUpsertTransfer(ctx, client, otokchain.Transfer{
			Network: otokchain.NetworkTRC20, TxHash: fmt.Sprintf("%064d", 100+i), BlockTime: time.Now(),
			From: "TFrom", ToNorm: otokTestTronAddress, Amount: big.NewInt(int64(1_000_000 * (i + 1))),
		}, "a614f803b6fd780986a42c78ec9c7f77e6ded13c", 6, otokSourceWatcher)
		require.NoError(t, err)
	}
	list, total, err := otokListTransfers(ctx, client, OtokUSDTTransferFilter{Status: otokTransferSeen, Keyword: fmt.Sprintf("%064d", 101), Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, list, 1)
	cents, exact := list[0].AmountCents()
	require.True(t, exact)
	require.EqualValues(t, 200, cents)
}

type otokTestTronTx struct {
	amount    int64 // 原始值（6 位小数）
	blockTime time.Time
}

// otokTronTxServer 模拟 TronGrid 按哈希查询交易的接口：每个哈希对应一笔转入 recipient 的 USDT 转账。
func otokTronTxServer(t *testing.T, recipient string, txs map[string]otokTestTronTx) string {
	t.Helper()
	spec, err := otokchain.SpecFor(otokchain.NetworkTRC20)
	require.NoError(t, err)
	contractHex, err := otokchain.NormalizeTokenContract(spec)
	require.NoError(t, err)
	recipientHex, err := otokchain.TronAddressToHex(recipient)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		tx, ok := txs[body.Value]
		if !ok {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": body.Value, "blockNumber": 100, "blockTimeStamp": tx.blockTime.UnixMilli(),
			"receipt": map[string]any{"result": "SUCCESS"},
			"log": []any{map[string]any{
				"address": contractHex,
				"topics": []string{
					"ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
					strings.Repeat("0", 24) + strings.Repeat("1", 40),
					strings.Repeat("0", 24) + recipientHex,
				},
				"data": fmt.Sprintf("%064x", tx.amount),
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// otokBackdateReservation 把预留的创建时间往前挪，模拟「下单若干分钟后才付款」。
func otokBackdateReservation(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64, ago time.Duration) *otokUSDTReservation {
	t.Helper()
	_, err := client.ExecContext(ctx, `UPDATE otok_usdt_orders SET created_at = NOW() - make_interval(secs => $2) WHERE order_id = $1`,
		orderID, ago.Seconds())
	require.NoError(t, err)
	r, err := otokGetReservation(ctx, client, orderID)
	require.NoError(t, err)
	return r
}

func TestOtokUSDTMatchingRules(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	svc := NewOtokUSDTService(&PaymentService{entClient: client}, client, 0)
	const token = "a614f803b6fd780986a42c78ec9c7f77e6ded13c"

	reserve := func(status string, base int64) (*dbent.PaymentOrder, *otokUSDTReservation) {
		o := otokCreateTestOrder(t, ctx, client, status)
		require.NoError(t, otokReserveInTx(ctx, client, otokTestReservation(o, base)))
		return o, otokBackdateReservation(t, ctx, client, o.ID, 5*time.Minute)
	}
	orderA, rA := reserve(OrderStatusPending, 1409) // 14.10
	orderB, rB := reserve(OrderStatusPending, 2000) // 20.01
	require.EqualValues(t, 1410, rA.AmountCents)
	require.EqualValues(t, 2001, rB.AmountCents)

	txA := "a1" + fmt.Sprintf("%062d", 1)
	t.Setenv("OTOK_USDT_TRON_API_URL", otokTronTxServer(t, otokTestTronAddress, map[string]otokTestTronTx{
		txA: {amount: 14_100_000, blockTime: time.Now().Add(-time.Minute)},
	}))
	t.Setenv("OTOK_USDT_ALLOW_PRIVATE_RPC", "1")

	// 用户 B 拿用户 A 的交易哈希提交到自己的订单：被拒，且不能改动这笔转账的状态。
	_, err := svc.SubmitUserTx(ctx, orderB.UserID, orderB.ID, txA)
	require.Error(t, err)
	require.Equal(t, "USDT_TX_AMOUNT_MISMATCH", infraerrors.Reason(err), "err=%v", err)
	list, _, err := otokListTransfers(ctx, client, OtokUSDTTransferFilter{Keyword: txA, Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, otokTransferSeen, list[0].Status)
	require.False(t, list[0].OrderID.Valid)
	logs, err := svc.paymentSvc.GetOrderAuditLogs(ctx, orderB.ID)
	require.NoError(t, err)
	require.NotEmpty(t, logs)
	require.Equal(t, "OTOK_USDT_TX_REJECTED", logs[len(logs)-1].Action)

	// 监听器照常把它匹配给真正的订单 A。
	svc.matchTransfers(ctx, list, []*otokUSDTReservation{rA, rB})
	boundA, err := otokGetTransferByOrder(ctx, client, orderA.ID)
	require.NoError(t, err)
	require.NotNil(t, boundA)
	require.Equal(t, txA, boundA.TxHash)

	record := func(hash string, cents int64, blockTime time.Time) *otokUSDTTransfer {
		row, err := otokUpsertTransfer(ctx, client, otokchain.Transfer{
			Network: otokchain.NetworkTRC20, TxHash: hash, BlockTime: blockTime,
			From: otokTestTronAddress, ToNorm: otokTestTronAddress, Amount: otokchain.CentsToRaw(cents, 6),
		}, token, 6, otokSourceWatcher)
		require.NoError(t, err)
		return row
	}

	// 已取消的订单不再自动匹配。
	orderC, rC := reserve(OrderStatusCancelled, 3000)
	rowC := record("c1"+fmt.Sprintf("%062d", 3), rC.AmountCents, time.Now().Add(-time.Minute))
	svc.matchTransfers(ctx, []*otokUSDTTransfer{rowC}, []*otokUSDTReservation{rC})
	notBound, err := otokGetTransferByOrder(ctx, client, orderC.ID)
	require.NoError(t, err)
	require.Nil(t, notBound)

	// 区块时间早于下单的转账不匹配。
	orderD, rD := reserve(OrderStatusPending, 4000)
	rowD := record("d1"+fmt.Sprintf("%062d", 4), rD.AmountCents, rD.CreatedAt.Add(-time.Minute))
	svc.matchTransfers(ctx, []*otokUSDTTransfer{rowD}, []*otokUSDTReservation{rD})
	notBound, err = otokGetTransferByOrder(ctx, client, orderD.ID)
	require.NoError(t, err)
	require.Nil(t, notBound)

	// 下单之前就已记录的转账（即使区块时间在窗口内）也不匹配。
	orderE, rE := reserve(OrderStatusPending, 5000)
	rowE := record("e1"+fmt.Sprintf("%062d", 5), rE.AmountCents, time.Now().Add(-time.Minute))
	_, err = client.ExecContext(ctx, `UPDATE otok_usdt_transfers SET created_at = $2 WHERE id = $1`, rowE.ID, rE.CreatedAt.Add(-time.Second))
	require.NoError(t, err)
	rowE, err = otokGetTransfer(ctx, client, rowE.ID)
	require.NoError(t, err)
	svc.matchTransfers(ctx, []*otokUSDTTransfer{rowE}, []*otokUSDTReservation{rE})
	notBound, err = otokGetTransferByOrder(ctx, client, orderE.ID)
	require.NoError(t, err)
	require.Nil(t, notBound)
}
