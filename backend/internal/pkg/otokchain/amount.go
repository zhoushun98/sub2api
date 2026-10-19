package otokchain

import (
	"fmt"
	"math/big"
	"strings"
)

// CentsDecimals 是订单金额的精度（USDT 保留两位小数）。
const CentsDecimals = 2

func pow10(n int32) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// CentsToRaw 把「分」换算为链上最小单位；decimals 必须 ≥ 2。
func CentsToRaw(cents int64, decimals int32) *big.Int {
	return new(big.Int).Mul(big.NewInt(cents), pow10(decimals-CentsDecimals))
}

// RawToCents 把链上最小单位换算为「分」；exact=false 表示存在小于 0.01 的零头。
func RawToCents(raw *big.Int, decimals int32) (cents int64, exact bool) {
	if raw == nil || raw.Sign() < 0 {
		return 0, false
	}
	q, r := new(big.Int).QuoRem(raw, pow10(decimals-CentsDecimals), new(big.Int))
	if !q.IsInt64() {
		return 0, false
	}
	return q.Int64(), r.Sign() == 0
}

// FormatRaw 以十进制字符串展示链上金额：去掉末尾多余的 0，但至少保留两位小数。
func FormatRaw(raw *big.Int, decimals int32) string {
	if raw == nil {
		return "0"
	}
	neg := raw.Sign() < 0
	abs := new(big.Int).Abs(raw)
	q, r := new(big.Int).QuoRem(abs, pow10(decimals), new(big.Int))
	s := q.String()
	// 至少保留两位小数，与订单金额的展示保持一致；更多位只在非零时展示。
	frac := "00"
	if r.Sign() != 0 {
		frac = strings.TrimRight(fmt.Sprintf("%0*s", int(decimals), r.String()), "0")
		for len(frac) < 2 {
			frac += "0"
		}
	}
	s += "." + frac
	if neg {
		s = "-" + s
	}
	return s
}

// FormatCents 把「分」格式化为两位小数字符串。
func FormatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// ParseUint256 解析十进制或 0x 十六进制的非负整数。
func ParseUint256(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
		base = 16
		if s == "" {
			return new(big.Int), nil
		}
	}
	v, ok := new(big.Int).SetString(s, base)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid uint256: %q", s)
	}
	return v, nil
}
