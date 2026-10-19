package service

import (
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/shopspring/decimal"
)

// otok: USDT 报价。用户按站内金额（元）下单，应付 USDT 按渠道汇率与渠道手续费换算。

var (
	decimalOne     = decimal.NewFromInt(1)
	decimalHundred = decimal.NewFromInt(100)
)

// otokUSDTQuoteCents 计算应付 USDT（分）：ceil(站内金额 × (1 + 手续费%/100) ÷ 汇率，两位小数)。
func otokUSDTQuoteCents(stationAmount float64, feeRate, exchangeRate decimal.Decimal) (int64, error) {
	if !exchangeRate.IsPositive() {
		return 0, fmt.Errorf("exchange rate must be positive")
	}
	station := decimal.NewFromFloat(stationAmount).Round(2)
	if !station.IsPositive() {
		return 0, fmt.Errorf("amount must be positive")
	}
	gross := station.Mul(decimalOne.Add(feeRate.Div(decimalHundred)))
	cents := gross.Mul(decimalHundred).Div(exchangeRate).Ceil()
	if cents.LessThan(decimalOne) {
		cents = decimalOne
	}
	if !cents.IsInteger() || cents.GreaterThan(decimal.NewFromInt(1_000_000_000_00)) {
		return 0, fmt.Errorf("usdt amount out of range")
	}
	return cents.IntPart(), nil
}

// otokUSDTStationAmount 返回订单的站内计价金额（元）：余额订单为实付基数，
// 订阅订单为按 SUBSCRIPTION_USD_TO_CNY_RATE 换算后的人民币价格。
func otokUSDTStationAmount(cfg *PaymentConfig, plan *dbent.SubscriptionPlan, limitAmount float64) float64 {
	if plan == nil || cfg == nil {
		return limitAmount
	}
	return calculateSubscriptionGatewayBaseAmount(limitAmount, cfg.SubscriptionUSDToCNYRate, payment.DefaultPaymentCurrency)
}

// OtokUSDTNetworkInfo 是前端展示所需的网络信息。
type OtokUSDTNetworkInfo struct {
	PaymentType   string `json:"payment_type"`
	Network       string `json:"network"`
	DisplayName   string `json:"display_name"`
	TokenContract string `json:"token_contract"`
	Confirmations int64  `json:"confirmations"`
}

// OtokUSDTQuote 是 USDT 渠道的公开报价参数；前端用同一公式估算应付金额。
type OtokUSDTQuote struct {
	Enabled       bool                  `json:"enabled"`
	ExchangeRate  string                `json:"exchange_rate"`
	FeeRate       string                `json:"fee_rate"`
	ExpireMinutes int                   `json:"expire_minutes"`
	Networks      []OtokUSDTNetworkInfo `json:"networks"`
	// EstimatedAmount 为传入 amount 时的预估应付 USDT（不含 0.00~0.99 的识别尾差）。
	EstimatedAmount string `json:"estimated_amount,omitempty"`
}

func otokUSDTNetworkInfos(ucfg *provider.OtokUSDTConfig, enabledTypes map[string]bool) []OtokUSDTNetworkInfo {
	var out []OtokUSDTNetworkInfo
	for _, n := range otokchain.AllNetworks {
		if _, ok := ucfg.Addresses[n]; !ok {
			continue
		}
		pt := payment.OtokUSDTPaymentTypeOf(string(n))
		if enabledTypes != nil && !enabledTypes[pt] {
			continue
		}
		spec, err := otokchain.SpecFor(n)
		if err != nil {
			continue
		}
		out = append(out, OtokUSDTNetworkInfo{
			PaymentType:   pt,
			Network:       string(n),
			DisplayName:   spec.DisplayName,
			TokenContract: spec.TokenContract,
			Confirmations: spec.Confirmations,
		})
	}
	return out
}

// otokTrimDecimal 去掉 NUMERIC 列读出的尾随零（7.10000000 → 7.1），解析失败时原样返回。
func otokTrimDecimal(s string) string {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return s
	}
	return d.String()
}
