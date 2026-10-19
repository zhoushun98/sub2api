//go:build unit

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newOtokTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestOtokUSDTQuoteCents(t *testing.T) {
	rate := decimal.RequireFromString("7.10")
	cases := []struct {
		station float64
		fee     string
		want    int64
	}{
		{100, "0", 1409},   // 14.0845… → 向上取整 14.09
		{71, "0", 1000},    // 恰好整除，不额外进位
		{100, "1.5", 1430}, // 101.5 / 7.1 = 14.2957… → 14.30
		{0.01, "0", 1},     // 最小 0.01 USDT
	}
	for _, c := range cases {
		got, err := otokUSDTQuoteCents(c.station, decimal.RequireFromString(c.fee), rate)
		require.NoError(t, err)
		require.Equal(t, c.want, got, "station=%v fee=%s", c.station, c.fee)
	}
	_, err := otokUSDTQuoteCents(100, decimal.Zero, decimal.Zero)
	require.Error(t, err)
	_, err = otokUSDTQuoteCents(0, decimal.Zero, rate)
	require.Error(t, err)
	_, err = otokUSDTQuoteCents(math.NaN(), decimal.Zero, rate)
	require.Error(t, err)
	_, err = otokUSDTQuoteCents(math.Inf(1), decimal.Zero, rate)
	require.Error(t, err)
}

func TestOtokUSDTStationAmountForSubscription(t *testing.T) {
	cfg := &PaymentConfig{SubscriptionUSDToCNYRate: 7.2}
	require.Equal(t, 100.0, otokUSDTStationAmount(cfg, nil, 100))
	require.Equal(t, 72.0, otokUSDTStationAmount(cfg, &dbent.SubscriptionPlan{}, 10))
	// 未配置订阅汇率时保持 price 直付
	require.Equal(t, 10.0, otokUSDTStationAmount(&PaymentConfig{}, &dbent.SubscriptionPlan{}, 10))
}

func TestOtokPickFreeAmountCents(t *testing.T) {
	// 基础金额本身（+0.00）永远不分配。
	got, ok := otokPickFreeAmountCents(1409, nil)
	require.True(t, ok)
	require.EqualValues(t, 1410, got)

	taken := map[int64]bool{1410: true, 1411: true, 1413: true}
	got, ok = otokPickFreeAmountCents(1409, taken)
	require.True(t, ok)
	require.EqualValues(t, 1412, got)

	full := map[int64]bool{}
	for i := int64(1); i <= otokUSDTAmountSlots; i++ {
		full[1409+i] = true
	}
	_, ok = otokPickFreeAmountCents(1409, full)
	require.False(t, ok)
	require.EqualValues(t, 99, otokUSDTAmountSlots)
}

func TestOtokForcedProviderKey(t *testing.T) {
	require.Equal(t, payment.TypeOtokUSDT, otokForcedProviderKey(payment.TypeOtokUSDTTRC20))
	require.Equal(t, "", otokForcedProviderKey(payment.TypeAlipay))
	require.Equal(t, "", otokForcedProviderKey("usdt_trc20"))
}

func TestOtokCreateOrderPayAmount(t *testing.T) {
	order := &dbent.PaymentOrder{PayAmount: 14.1}
	s, v := otokCreateOrderPayAmount(order, &payment.InstanceSelection{ProviderKey: payment.TypeOtokUSDT}, "101.50", 101.5)
	require.Equal(t, "14.10", s)
	require.Equal(t, 14.1, v)
	s, v = otokCreateOrderPayAmount(order, &payment.InstanceSelection{ProviderKey: payment.TypeAlipay}, "101.50", 101.5)
	require.Equal(t, "101.50", s)
	require.Equal(t, 101.5, v)
}

func TestOtokUSDTSensitiveFieldsCoveredByAudit(t *testing.T) {
	for field := range providerSensitiveConfigFields[payment.TypeOtokUSDT] {
		require.True(t, isAuditSensitiveBodyKey(field), field)
	}
	require.True(t, isAuditSensitiveBodyKey(provider.OtokUSDTConfigTronGridAPIKey))
	require.True(t, validProviderKeys[payment.TypeOtokUSDT])
	require.True(t, isSensitiveProviderConfigField(payment.TypeOtokUSDT, provider.OtokUSDTConfigTronGridAPIKey))
	// RPC 地址可能在路径里带 key，审计日志要脱敏。
	for _, k := range []string{provider.OtokUSDTConfigETHRPCURL, provider.OtokUSDTConfigBSCRPCURL, provider.OtokUSDTConfigPolygonRPCURL} {
		require.True(t, isAuditSensitiveBodyKey(k), k)
	}
	require.False(t, isAuditSensitiveBodyKey(provider.OtokUSDTConfigTRC20Address))
}

func TestOtokTransferInWindow(t *testing.T) {
	created := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	r := &otokUSDTReservation{CreatedAt: created, ReservedUntil: created.Add(90 * time.Minute)}
	require.True(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(5 * time.Minute)}, r))
	require.True(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(otokUSDTMinPayDelay)}, r))
	require.True(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(90 * time.Minute)}, r))
	// 早于下单（或下单后几秒内）的转账不属于这张订单。
	require.False(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(5 * time.Second)}, r))
	require.False(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(-30 * time.Second)}, r))
	require.False(t, otokTransferInWindow(otokchain.Transfer{BlockTime: created.Add(91 * time.Minute)}, r))
	require.False(t, otokTransferInWindow(otokchain.Transfer{}, r))
}

func TestOtokTransferAutoMatchable(t *testing.T) {
	created := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	r := &otokUSDTReservation{
		Network: otokchain.NetworkBEP20, ToAddressNorm: "0xabc", AmountCents: 1410,
		CreatedAt: created, ReservedUntil: created.Add(90 * time.Minute),
	}
	good := func() *otokUSDTTransfer {
		return &otokUSDTTransfer{
			Network: otokchain.NetworkBEP20, ToAddressNorm: "0xabc", Status: otokTransferSeen,
			AmountRaw: otokchain.CentsToRaw(1410, 18), Decimals: 18,
			BlockTime: sql.NullTime{Time: created.Add(3 * time.Minute), Valid: true},
			CreatedAt: created.Add(4 * time.Minute),
		}
	}
	require.True(t, otokTransferAutoMatchable(good(), r))

	cases := map[string]func(*otokUSDTTransfer){
		"already bound":  func(t *otokUSDTTransfer) { t.OrderID = sql.NullInt64{Int64: 9, Valid: true} },
		"in review":      func(t *otokUSDTTransfer) { t.Status = otokTransferReview },
		"dropped":        func(t *otokUSDTTransfer) { t.Status = otokTransferDropped },
		"other network":  func(t *otokUSDTTransfer) { t.Network = otokchain.NetworkPolygon },
		"other address":  func(t *otokUSDTTransfer) { t.ToAddressNorm = "0xdef" },
		"amount differs": func(t *otokUSDTTransfer) { t.AmountRaw = otokchain.CentsToRaw(1409, 18) },
		"sub-cent amount": func(t *otokUSDTTransfer) {
			t.AmountRaw = new(big.Int).Add(otokchain.CentsToRaw(1410, 18), big.NewInt(1))
		},
		"no block time":      func(t *otokUSDTTransfer) { t.BlockTime = sql.NullTime{} },
		"paid before order":  func(t *otokUSDTTransfer) { t.BlockTime.Time = created.Add(-time.Minute) },
		"seen before order":  func(t *otokUSDTTransfer) { t.CreatedAt = created.Add(-time.Second) },
		"paid after closing": func(t *otokUSDTTransfer) { t.BlockTime.Time = created.Add(91 * time.Minute) },
	}
	for name, mutate := range cases {
		row := good()
		mutate(row)
		require.False(t, otokTransferAutoMatchable(row, r), name)
	}
}

func TestOtokBoundTransferMismatch(t *testing.T) {
	r := &otokUSDTReservation{Network: otokchain.NetworkTRC20, ToAddressNorm: otokTestTronAddress, AmountCents: 1410}
	row := &otokUSDTTransfer{
		Network: otokchain.NetworkTRC20, ToAddressNorm: otokTestTronAddress,
		AmountRaw: otokchain.CentsToRaw(1410, 6), Decimals: 6,
	}
	require.Empty(t, otokBoundTransferMismatch(row, r))

	row.AmountRaw = otokchain.CentsToRaw(1000, 6)
	require.Contains(t, otokBoundTransferMismatch(row, r), "does not match order amount")
	// 管理员补单允许金额不一致，但网络 / 地址必须一致。
	row.AmountOverride = true
	require.Empty(t, otokBoundTransferMismatch(row, r))
	row.Network = otokchain.NetworkBEP20
	require.NotEmpty(t, otokBoundTransferMismatch(row, r))
}

func TestOtokOrderMatchable(t *testing.T) {
	require.True(t, otokOrderMatchable(OrderStatusPending))
	require.True(t, otokOrderMatchable(OrderStatusExpired))
	require.False(t, otokOrderMatchable(OrderStatusCancelled))
	require.False(t, otokOrderMatchable(OrderStatusFailed))
	require.False(t, otokOrderMatchable(OrderStatusPaid))
}

func otokTestConfigJSON(t *testing.T, extra map[string]string) string {
	cfg := map[string]string{
		provider.OtokUSDTConfigExchangeRate: "7.10",
		provider.OtokUSDTConfigTRC20Address: otokTestTronAddress,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return string(raw)
}

func TestOtokUSDTSelectionIgnoresEmptyTypeInstances(t *testing.T) {
	client := newOtokTestClient(t)
	ctx := context.Background()
	// supported_types 为空的支付宝实例：上游会把它当作支持全部类型。
	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).SetName("alipay").SetConfig(`{"appId":"x"}`).
		SetSupportedTypes("").SetEnabled(true).SetSortOrder(0).Save(ctx)
	require.NoError(t, err)
	usdt, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeOtokUSDT).SetName("usdt").SetConfig(otokTestConfigJSON(t, nil)).
		SetSupportedTypes(payment.TypeOtokUSDTTRC20).SetEnabled(true).SetSortOrder(1).Save(ctx)
	require.NoError(t, err)

	lb := payment.NewDefaultLoadBalancer(client, nil)
	for i := 0; i < 5; i++ {
		sel, err := lb.SelectInstance(ctx, otokForcedProviderKey(payment.TypeOtokUSDTTRC20), payment.TypeOtokUSDTTRC20, payment.Strategy(payment.DefaultLoadBalanceStrategy), 100)
		require.NoError(t, err)
		require.Equal(t, payment.TypeOtokUSDT, sel.ProviderKey)
		require.Equal(t, usdt.ID, mustParseInt64(t, sel.InstanceID))
	}
}

func mustParseInt64(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err)
	return v
}

func TestOtokValidateProviderInstance(t *testing.T) {
	client := newOtokTestClient(t)
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: client}
	validCfg := map[string]string{
		provider.OtokUSDTConfigExchangeRate: "7.10",
		provider.OtokUSDTConfigTRC20Address: otokTestTronAddress,
		provider.OtokUSDTConfigERC20Address: otokTestEVMAddress,
	}

	requireReason := func(err error, reason string) {
		t.Helper()
		require.Error(t, err)
		require.Equal(t, reason, infraerrors.Reason(err))
	}

	requireReason(svc.otokValidateProviderInstance(ctx, 0, payment.TypeEasyPay, "alipay,otok_usdt_trc20", nil, true), "VALIDATION_ERROR")
	require.NoError(t, svc.otokValidateProviderInstance(ctx, 0, payment.TypeEasyPay, "alipay,usdt_trc20", nil, true))

	requireReason(svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "", validCfg, true), "VALIDATION_ERROR")
	requireReason(svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "otok_usdt_trc20,alipay", validCfg, true), "VALIDATION_ERROR")
	requireReason(svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "otok_usdt_trc20,otok_usdt_bep20", validCfg, true), "VALIDATION_ERROR")
	// 未启用时允许暂存不完整配置
	require.NoError(t, svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "otok_usdt_bep20", map[string]string{}, false))
	require.NoError(t, svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "otok_usdt_trc20,otok_usdt_erc20", validCfg, true))

	existing, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeOtokUSDT).SetName("usdt").SetConfig(otokTestConfigJSON(t, nil)).
		SetSupportedTypes(payment.TypeOtokUSDTTRC20).SetEnabled(true).Save(ctx)
	require.NoError(t, err)
	requireReason(svc.otokValidateProviderInstance(ctx, 0, payment.TypeOtokUSDT, "otok_usdt_trc20", validCfg, true), "USDT_PROVIDER_EXISTS")
	require.NoError(t, svc.otokValidateProviderInstance(ctx, existing.ID, payment.TypeOtokUSDT, "otok_usdt_trc20", validCfg, true))
}
