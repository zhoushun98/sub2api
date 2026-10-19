package payment

import "strings"

// otok: USDT 链上充值（fork 定制，见 docs/otok/USDT_PAYMENT.md）。
// 渠道 key 与支付类型统一带 otok_ 前缀，避免与上游易支付自定义通道（如 usdt_trc20）重名。
const (
	TypeOtokUSDT        PaymentType = "otok_usdt"
	TypeOtokUSDTTRC20   PaymentType = "otok_usdt_trc20"
	TypeOtokUSDTBEP20   PaymentType = "otok_usdt_bep20"
	TypeOtokUSDTPolygon PaymentType = "otok_usdt_polygon"
	TypeOtokUSDTERC20   PaymentType = "otok_usdt_erc20"
)

const otokUSDTTypePrefix = TypeOtokUSDT + "_"

// OtokUSDTPaymentTypes 按前端展示顺序列出全部 USDT 支付类型。
var OtokUSDTPaymentTypes = []PaymentType{TypeOtokUSDTTRC20, TypeOtokUSDTBEP20, TypeOtokUSDTPolygon, TypeOtokUSDTERC20}

// IsOtokUSDTPaymentType 报告支付类型是否属于 USDT 渠道。
func IsOtokUSDTPaymentType(t string) bool {
	t = strings.TrimSpace(t)
	for _, v := range OtokUSDTPaymentTypes {
		if t == v {
			return true
		}
	}
	return false
}

// OtokUSDTNetworkOf 返回支付类型对应的网络标识（trc20 / bep20 / polygon / erc20）。
func OtokUSDTNetworkOf(t string) string {
	if !IsOtokUSDTPaymentType(t) {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(t), otokUSDTTypePrefix)
}

// OtokUSDTPaymentTypeOf 返回网络标识对应的支付类型。
func OtokUSDTPaymentTypeOf(network string) PaymentType {
	t := otokUSDTTypePrefix + strings.ToLower(strings.TrimSpace(network))
	if IsOtokUSDTPaymentType(t) {
		return t
	}
	return ""
}
