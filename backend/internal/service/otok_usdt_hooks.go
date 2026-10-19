package service

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/shopspring/decimal"
)

// otok: 挂在上游下单 / 渠道配置流程上的钩子。上游文件只调用这里的函数，
// 每处调用点都标注了 `// otok:`，并登记在 docs/otok/UPSTREAM_PATCHES.md。

// otokForcedProviderKey 让 USDT 支付类型只在 otok_usdt 实例中选择。上游选实例时
// providerKey 为空会跨渠道选择，而 supported_types 为空的实例会匹配任意类型。
func otokForcedProviderKey(paymentType string) string {
	if payment.IsOtokUSDTPaymentType(paymentType) {
		return payment.TypeOtokUSDT
	}
	return ""
}

// otokPrepareUSDTOrderInTx 在下单事务内完成 USDT 报价、唯一金额预留，并把应付金额、
// 渠道手续费、有效期与网络信息写回订单。非 USDT 订单原样返回。
func (s *PaymentService) otokPrepareUSDTOrderInTx(ctx context.Context, tx *dbent.Tx, order *dbent.PaymentOrder, req CreateOrderRequest, plan *dbent.SubscriptionPlan, cfg *PaymentConfig, limitAmount float64, sel *payment.InstanceSelection) (*dbent.PaymentOrder, error) {
	isUSDTType := payment.IsOtokUSDTPaymentType(req.PaymentType)
	isUSDTInstance := sel != nil && sel.ProviderKey == payment.TypeOtokUSDT
	if !isUSDTType && !isUSDTInstance {
		return order, nil
	}
	if isUSDTType != isUSDTInstance {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "usdt payment type does not match the selected provider instance")
	}
	ucfg, err := provider.ParseOtokUSDTConfig(sel.Config)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", "provider_misconfigured").
			WithMetadata(map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID})
	}
	network, _ := otokchain.ParseNetwork(payment.OtokUSDTNetworkOf(req.PaymentType))
	addr, ok := ucfg.Addresses[network]
	if !ok {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "method_not_configured").
			WithMetadata(map[string]string{"payment_type": req.PaymentType})
	}
	spec, err := otokchain.SpecFor(network)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", err.Error())
	}
	tokenNorm, err := otokchain.NormalizeTokenContract(spec)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", err.Error())
	}
	station := otokUSDTStationAmount(cfg, plan, limitAmount)
	baseCents, err := otokUSDTQuoteCents(station, ucfg.FeeRate, ucfg.ExchangeRate)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", err.Error())
	}
	expiresAt := time.Now().Add(time.Duration(ucfg.ExpireMinutes) * time.Minute)
	r := &otokUSDTReservation{
		OrderID:         order.ID,
		OutTradeNo:      order.OutTradeNo,
		InstanceID:      sel.InstanceID,
		Network:         network,
		TokenContract:   tokenNorm,
		Decimals:        spec.Decimals,
		ToAddress:       addr.Display,
		ToAddressNorm:   addr.Norm,
		BaseAmountCents: baseCents,
		StationAmount:   station,
		ExchangeRate:    ucfg.ExchangeRate.String(),
		FeeRate:         ucfg.FeeRate.String(),
		ExpiresAt:       expiresAt,
		ReservedUntil:   expiresAt.Add(time.Duration(ucfg.GraceMinutes) * time.Minute),
	}
	if err := otokReserveAmount(ctx, tx, r); err != nil {
		return nil, err
	}
	snapshot := maps.Clone(order.ProviderSnapshot)
	if snapshot == nil {
		snapshot = map[string]any{"schema_version": 2}
	}
	snapshot["currency"] = "USD"
	snapshot["otok_network"] = string(network)
	snapshot["otok_to_address"] = addr.Display
	snapshot["otok_token_contract"] = spec.TokenContract
	return tx.PaymentOrder.UpdateOneID(order.ID).
		SetPayAmount(decimal.New(r.AmountCents, -2).InexactFloat64()).
		SetFeeRate(ucfg.FeeRate.InexactFloat64()).
		SetExpiresAt(expiresAt).
		SetProviderSnapshot(snapshot).
		Save(ctx)
}

// otokCreateOrderPayAmount 让 CreateOrder 后续流程（调用渠道、组装响应）使用
// 事务内写回的 USDT 唯一金额，而不是按站内币种算出的金额。
func otokCreateOrderPayAmount(order *dbent.PaymentOrder, sel *payment.InstanceSelection, payAmountStr string, payAmount float64) (string, float64) {
	if order == nil || sel == nil || sel.ProviderKey != payment.TypeOtokUSDT {
		return payAmountStr, payAmount
	}
	return decimal.NewFromFloat(order.PayAmount).StringFixed(2), order.PayAmount
}

// otokValidateProviderInstance 在保存渠道实例时校验 USDT 相关约束：
//   - 非 otok 实例不得声明 otok_usdt_* 类型；
//   - otok 实例必须显式声明且只能声明 otok_usdt_* 类型（空类型会被上游当作「支持全部类型」）；
//   - 启用时，每个声明的网络都要有收款地址，且同时只允许一个启用的 otok 实例（保证汇率唯一）。
func (s *PaymentConfigService) otokValidateProviderInstance(ctx context.Context, excludeID int64, providerKey, supportedTypes string, config map[string]string, enabled bool) error {
	types := splitTypes(supportedTypes)
	if providerKey != payment.TypeOtokUSDT {
		for _, t := range types {
			if payment.IsOtokUSDTPaymentType(t) || strings.HasPrefix(t, payment.TypeOtokUSDT) {
				return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("payment type %s is reserved for the USDT provider", t))
			}
		}
		return nil
	}
	if len(types) == 0 {
		return infraerrors.BadRequest("VALIDATION_ERROR", "USDT provider must select at least one network")
	}
	for _, t := range types {
		if !payment.IsOtokUSDTPaymentType(t) {
			return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("USDT provider does not support payment type %s", t))
		}
	}
	if !enabled {
		return nil
	}
	ucfg, err := provider.ParseOtokUSDTConfig(config)
	if err != nil {
		return infraerrors.BadRequest("VALIDATION_ERROR", err.Error())
	}
	for _, t := range types {
		network, _ := otokchain.ParseNetwork(payment.OtokUSDTNetworkOf(t))
		if _, ok := ucfg.Addresses[network]; !ok {
			return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("network %s is selected but has no receiving address", network))
		}
	}
	if s == nil || s.entClient == nil {
		return nil
	}
	q := s.entClient.PaymentProviderInstance.Query().Where(
		paymentproviderinstance.ProviderKeyEQ(payment.TypeOtokUSDT),
		paymentproviderinstance.Enabled(true),
	)
	if excludeID > 0 {
		q = q.Where(paymentproviderinstance.IDNEQ(excludeID))
	}
	n, err := q.Count(ctx)
	if err != nil {
		return fmt.Errorf("count enabled usdt instances: %w", err)
	}
	if n > 0 {
		return infraerrors.Conflict("USDT_PROVIDER_EXISTS", "only one enabled USDT provider instance is allowed")
	}
	return nil
}
