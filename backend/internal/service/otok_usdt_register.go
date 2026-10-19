package service

import "github.com/Wei-Shaw/sub2api/internal/payment"

// otok: 把 USDT 渠道注册进上游的渠道白名单与字段表，避免修改上游 map 字面量。
//
// 注意：审计脱敏表 auditBodySensitiveExactKeys 在 init() 之前就已构建，这里新增的
// 敏感字段不会进入它的精确匹配集合；字段名必须能被审计子串规则（如 "apikey"）命中，
// 由 TestOtokUSDTSensitiveFieldsCoveredByAudit 守护。
//
// RPC 地址（ethRpcUrl 等）不算敏感字段，后台需要回显；但 Infura / Alchemy 这类地址的路径里
// 带着 key，所以在审计日志里按子串 "rpcurl" 脱敏。
func init() {
	auditBodySensitiveSubstrings = append(auditBodySensitiveSubstrings, "rpcurl")
	validProviderKeys[payment.TypeOtokUSDT] = true
	providerSensitiveConfigFields[payment.TypeOtokUSDT] = map[string]struct{}{
		"trongridapikey": {},
	}
	providerPendingOrderProtectedConfigFields[payment.TypeOtokUSDT] = map[string]struct{}{
		"trc20address": {}, "bep20address": {}, "polygonaddress": {}, "erc20address": {},
	}
}
