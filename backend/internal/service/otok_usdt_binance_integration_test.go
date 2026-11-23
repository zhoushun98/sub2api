//go:build integration

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/stretchr/testify/require"
)

// otok: 币安数据源的集成测试（PostgreSQL + 模拟币安充值接口）。上游支付回调需要完整的 PaymentService，
// 这里只覆盖币安特有的部分：发现付款、匹配、各状态的复核、按编号查询与管理员选取。

const otokTestEVMNormIT = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"

type otokStubLB struct {
	mu  sync.Mutex
	cfg map[string]string
}

func (l *otokStubLB) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.cfg {
		out[k] = v
	}
	return out, nil
}

func (l *otokStubLB) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return nil, nil
}

func (l *otokStubLB) set(k, v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfg[k] = v
}

// otokFakeBinance 模拟 /sapi/v1/capital/deposit/hisrec：按 txId 过滤时与真实接口一致，链下转账只认纯数字编号。
type otokFakeBinance struct {
	mu       sync.Mutex
	deposits []otokchain.BinanceDeposit
	// exactTxID 为 true 时按 txId 原文过滤，模拟「按编号查不到」以覆盖兜底查找。
	exactTxID bool
}

func (f *otokFakeBinance) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		txID := r.URL.Query().Get("txId")
		out := []otokchain.BinanceDeposit{}
		for _, d := range f.deposits {
			if txID == "" || (!f.exactTxID && d.LookupKey() == txID) || (f.exactTxID && d.TxID == txID) {
				out = append(out, d)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *otokFakeBinance) setStatus(id string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.deposits {
		if f.deposits[i].ID == id {
			f.deposits[i].Status = status
		}
	}
}

func (f *otokFakeBinance) add(d otokchain.BinanceDeposit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deposits = append(f.deposits, d)
}

func otokBinanceTestSetup(t *testing.T) (*dbent.Client, *OtokUSDTService, *otokStubLB, *otokFakeBinance) {
	t.Helper()
	client := otokIntegrationClient(t)
	fake := &otokFakeBinance{}
	t.Setenv("OTOK_USDT_BINANCE_API_URL", fake.serve(t))
	t.Setenv("OTOK_USDT_ALLOW_PRIVATE_RPC", "1")
	lb := &otokStubLB{cfg: map[string]string{
		provider.OtokUSDTConfigExchangeRate:     "6.66",
		provider.OtokUSDTConfigTRC20Address:     otokTestTronAddress,
		provider.OtokUSDTConfigBEP20Address:     otokTestEVMAddress,
		provider.OtokUSDTConfigDepositSource:    provider.OtokUSDTSourceBinance,
		provider.OtokUSDTConfigBinanceAPIKey:    "k",
		provider.OtokUSDTConfigBinanceAPISecret: "s",
	}}
	svc := NewOtokUSDTService(&PaymentService{entClient: client, loadBalancer: lb}, client, 0)
	return client, svc, lb, fake
}

// otokReserveBEP20 为一个新订单在 BEP20 地址上预留金额，并把下单时间挪到 5 分钟前。
func otokReserveBEP20(t *testing.T, ctx context.Context, client *dbent.Client, status string, base int64) (*dbent.PaymentOrder, *otokUSDTReservation) {
	t.Helper()
	order := otokCreateTestOrder(t, ctx, client, status)
	spec, err := otokchain.SpecFor(otokchain.NetworkBEP20)
	require.NoError(t, err)
	token, err := otokchain.NormalizeTokenContract(spec)
	require.NoError(t, err)
	r := otokTestReservation(order, base)
	r.Network, r.TokenContract, r.Decimals = otokchain.NetworkBEP20, token, spec.Decimals
	r.ToAddress, r.ToAddressNorm = otokTestEVMAddress, otokTestEVMNormIT
	require.NoError(t, otokReserveInTx(ctx, client, r))
	return order, otokBackdateReservation(t, ctx, client, order.ID, 5*time.Minute)
}

func otokOffChainDeposit(id, number, network string, cents int64, status int) otokchain.BinanceDeposit {
	return otokchain.BinanceDeposit{
		ID: id, Coin: "USDT", Amount: otokchain.FormatCents(cents), Network: network, Status: status,
		Address: otokTestEVMNormIT, TxID: "Off-chain transfer " + number, SourceAddress: number,
		InsertTime: time.Now().Add(-time.Minute).UnixMilli(), TransferType: 1,
	}
}

func TestOtokBinanceUpsertIdempotent(t *testing.T) {
	client := otokIntegrationClient(t)
	ctx := context.Background()
	tr := otokchain.Transfer{
		Network: otokchain.NetworkBEP20, TxHash: "Off-chain transfer 434226888519", LogIndex: -1,
		BlockTime: time.Now().Add(-time.Minute), From: "434226888519", ToNorm: otokTestEVMNormIT,
		Amount: otokchain.CentsToRaw(752, 18),
	}
	first, err := otokUpsertBinanceTransfer(ctx, client, tr, "5326511839180757249", "token", 18)
	require.NoError(t, err)
	require.Equal(t, otokSourceBinance, first.Source)
	require.Equal(t, "5326511839180757249", first.ExternalID)
	time.Sleep(20 * time.Millisecond)
	again, err := otokUpsertBinanceTransfer(ctx, client, tr, "5326511839180757249", "token", 18)
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	require.True(t, again.UpdatedAt.Equal(first.UpdatedAt), "repeated polling must not touch updated_at")

	// 同一 txId 下的另一笔充值（不同 id、不同序号）各自成行。
	tr.LogIndex = -2
	other, err := otokUpsertBinanceTransfer(ctx, client, tr, "5326511839180757250", "token", 18)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, other.ID)
	_, err = otokUpsertBinanceTransfer(ctx, client, tr, "", "token", 18)
	require.Error(t, err)
}

func TestOtokBinanceScanMatchAndVerify(t *testing.T) {
	client, svc, lb, fake := otokBinanceTestSetup(t)
	ctx := context.Background()
	order, r := otokReserveBEP20(t, ctx, client, OrderStatusPending, 751)
	require.EqualValues(t, 752, r.AmountCents)

	// 用户在币安选了 Polygon（MATIC）网络转到同一地址，走链下转账；币安还在处理中。
	fake.add(otokOffChainDeposit("5326511839180757249", "434226888519", "MATIC", r.AmountCents, otokchain.BinanceDepositPending))
	ucfg := svc.instanceConfig(ctx, r.InstanceID)
	require.True(t, ucfg.UsesBinance())
	svc.scanBinance(ctx, ucfg, []*otokUSDTReservation{r})

	bound, err := otokGetTransferByOrder(ctx, client, order.ID)
	require.NoError(t, err)
	require.NotNil(t, bound, "off-chain transfer must match the BEP20 order across networks")
	require.Equal(t, otokSourceBinance, bound.Source)
	require.Equal(t, otokchain.NetworkPolygon, bound.Network)
	require.Equal(t, "Off-chain transfer 434226888519", bound.TxHash)
	require.Equal(t, otokTransferBound, bound.Status)
	cur, err := otokGetCursor(ctx, client, otokUSDTBinanceCursor, otokUSDTEVMCursorAddress)
	require.NoError(t, err)
	require.True(t, cur.LastSuccessAt.Valid)
	view := svc.transferView(ctx, bound, true)
	require.True(t, view.OffChain)
	require.Empty(t, view.ExplorerURL)

	// 处理中：不入账、不算受阻。
	credited, err := svc.creditBoundTransfer(ctx, bound, otokUSDTWatcherOperator)
	require.NoError(t, err)
	require.False(t, credited)
	after, err := otokGetTransfer(ctx, client, bound.ID)
	require.NoError(t, err)
	require.Equal(t, otokTransferBound, after.Status)
	require.Empty(t, after.LastError)

	// 等待账户持有人确认（travel rule）：受阻，错误写进 last_error。
	fake.setStatus("5326511839180757249", otokchain.BinanceDepositWaitingConfirm)
	_, err = svc.creditBoundTransfer(ctx, after, otokUSDTWatcherOperator)
	require.Error(t, err)
	after, err = otokGetTransfer(ctx, client, bound.ID)
	require.NoError(t, err)
	require.Equal(t, otokTransferBound, after.Status)
	require.Contains(t, after.LastError, "travel rule")
	h, err := svc.Health(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, h.CreditBlocked)

	// 已入账（6）：复核通过。切回链上数据源后，币安来源的记录仍向币安复核。
	fake.setStatus("5326511839180757249", otokchain.BinanceDepositCreditedLocked)
	lb.set(provider.OtokUSDTConfigDepositSource, provider.OtokUSDTSourceChain)
	check, err := svc.verifyTransfer(ctx, after, svc.instanceConfig(ctx, r.InstanceID))
	require.NoError(t, err)
	require.True(t, check.confirmed)

	// 被拒绝：解绑并转待审核，订单恢复为等待转账。
	fake.setStatus("5326511839180757249", otokchain.BinanceDepositRejected)
	_, err = svc.creditBoundTransfer(ctx, after, otokUSDTWatcherOperator)
	require.NoError(t, err)
	after, err = otokGetTransfer(ctx, client, bound.ID)
	require.NoError(t, err)
	require.Equal(t, otokTransferReview, after.Status)
	require.False(t, after.OrderID.Valid)
	res, err := otokGetReservation(ctx, client, order.ID)
	require.NoError(t, err)
	require.Equal(t, otokReservationPending, res.Status)
}

func TestOtokBinanceUserSubmitAndAdminPick(t *testing.T) {
	client, svc, _, fake := otokBinanceTestSetup(t)
	ctx := context.Background()
	orderA, rA := otokReserveBEP20(t, ctx, client, OrderStatusPending, 1409) // 14.10
	orderB, _ := otokReserveBEP20(t, ctx, client, OrderStatusPending, 2000)  // 20.01
	fake.add(otokOffChainDeposit("900", "434226888519", "BSC", rA.AmountCents, otokchain.BinanceDepositPending))

	// 用户 B 拿 A 的链下编号提交：被拒，且不改动这笔转账。
	_, err := svc.SubmitUserTx(ctx, orderB.UserID, orderB.ID, "434226888519")
	require.Equal(t, "USDT_TX_AMOUNT_MISMATCH", infraerrors.Reason(err), "err=%v", err)
	list, _, err := otokListTransfers(ctx, client, OtokUSDTTransferFilter{Keyword: "434226888519", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, otokTransferSeen, list[0].Status)
	require.False(t, list[0].OrderID.Valid)

	// 不存在的编号 / 链上模式不支持的写法。
	_, err = svc.SubmitUserTx(ctx, orderA.UserID, orderA.ID, "链下转账 111111111")
	require.Equal(t, "USDT_TX_NOT_FOUND", infraerrors.Reason(err))

	// 用户 A 用中文写法提交：绑定到 A（处理中，暂不入账）。
	d, err := svc.SubmitUserTx(ctx, orderA.UserID, orderA.ID, " 链下转账 434226888519 ")
	require.NoError(t, err)
	require.NotNil(t, d.Transfer)
	require.True(t, d.Transfer.OffChain)
	require.Equal(t, provider.OtokUSDTSourceBinance, d.DepositSource)
	// 已绑定后再提交同一编号（换一种写法）不报冲突。
	_, err = svc.SubmitUserTx(ctx, orderA.UserID, orderA.ID, "Off-chain transfer 434226888519")
	require.NoError(t, err)

	// 管理员补单：已取消订单 + 金额不一致 + 按编号查不到（币安改了前缀）时兜底查找。
	orderC, rC := otokReserveBEP20(t, ctx, client, OrderStatusCancelled, 3000)
	fake.add(otokOffChainDeposit("901", "555555555555", "BSC", 1234, otokchain.BinanceDepositPending))
	fake.mu.Lock()
	fake.exactTxID = true
	fake.mu.Unlock()
	ucfg := svc.instanceConfig(ctx, rC.InstanceID)
	in, err := otokParseTxInput(rC.Network, "555555555555", true)
	require.NoError(t, err)
	_, err = svc.adminPickBinance(ctx, rC, ucfg, in, nil)
	require.Equal(t, "USDT_TX_UNCONFIRMED", infraerrors.Reason(err), "err=%v", err)
	fake.setStatus("901", otokchain.BinanceDepositSuccess)
	row, err := svc.adminPickBinance(ctx, rC, ucfg, in, nil)
	require.NoError(t, err)
	ok, err := otokBindTransfer(ctx, client, row.ID, orderC.ID, "admin: late off-chain payment", true)
	require.NoError(t, err)
	require.True(t, ok)
	boundC, err := otokGetTransfer(ctx, client, row.ID)
	require.NoError(t, err)
	require.True(t, boundC.AmountOverride)
	require.Empty(t, otokBoundTransferMismatch(boundC, rC))
	check, err := svc.verifyTransfer(ctx, boundC, ucfg)
	require.NoError(t, err)
	require.True(t, check.confirmed, "verification falls back to the time window when the id lookup misses")
}
