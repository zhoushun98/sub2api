//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"strconv"
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
	raw, err := migrations.FS.ReadFile("otok_0001_usdt.sql")
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, string(raw))
	return err
}

func otokCreateTestOrder(t *testing.T, ctx context.Context, client *dbent.Client, status string) *dbent.PaymentOrder {
	t.Helper()
	n := otokOrderSeq.Add(1)
	user, err := client.User.Create().
		SetEmail(fmt.Sprintf("otok-%d-%d@example.com", n, time.Now().UnixNano())).
		SetPasswordHash("hash").SetUsername("otok-user").Save(ctx)
	require.NoError(t, err)
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
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := otokReserveAmount(ctx, tx, r); err != nil {
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
		require.GreaterOrEqual(t, amt, int64(1409))
		require.Less(t, amt, int64(1409+otokUSDTAmountSlots))
		seen[amt] = true
	}
	require.Equal(t, 1, failures, "exactly one order should overflow the 100 slots")
	require.Len(t, seen, otokUSDTAmountSlots)
}

func TestOtokUSDTReleaseRules(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()

	expired := otokCreateTestOrder(t, ctx, client, OrderStatusExpired)
	rExpired := otokTestReservation(expired, 2000)
	rExpired.ReservedUntil = time.Now().Add(-time.Minute)
	require.NoError(t, otokReserveInTx(ctx, client, rExpired))

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
	require.Len(t, active, 1)
	require.Equal(t, cancelled.ID, active[0].OrderID, "cancelled orders keep their amount until the window closes")

	// 释放后的金额可以被新订单复用
	reuse := otokCreateTestOrder(t, ctx, client, OrderStatusPending)
	rReuse := otokTestReservation(reuse, 3000)
	require.NoError(t, otokReserveInTx(ctx, client, rReuse))
	require.EqualValues(t, 3000, rReuse.AmountCents)
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
			if ok, err := svc.bindTransfer(ctx, row.ID, orderID, "test"); err == nil && ok {
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
	ok, err := svc.bindTransfer(ctx, row2.ID, bound.OrderID.Int64, "test")
	require.False(t, ok)
	_ = err

	// 解绑后订单恢复为等待转账
	require.NoError(t, otokUnbindTransfer(ctx, client, row.ID, "reorg"))
	r, err := otokGetReservation(ctx, client, bound.OrderID.Int64)
	require.NoError(t, err)
	require.Equal(t, otokReservationPending, r.Status)
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
