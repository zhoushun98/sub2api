//go:build unit

package provider

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/stretchr/testify/require"
)

const (
	otokTestTronAddress = "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"
	otokTestEVMAddress  = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
)

func otokValidConfig() map[string]string {
	return map[string]string{
		OtokUSDTConfigExchangeRate:   "7.10",
		OtokUSDTConfigTRC20Address:   otokTestTronAddress,
		OtokUSDTConfigPolygonAddress: otokTestEVMAddress,
	}
}

func TestParseOtokUSDTConfigDefaults(t *testing.T) {
	cfg, err := ParseOtokUSDTConfig(otokValidConfig())
	require.NoError(t, err)
	require.Equal(t, "7.1", cfg.ExchangeRate.String())
	require.True(t, cfg.FeeRate.IsZero())
	require.Equal(t, 30, cfg.ExpireMinutes)
	require.Equal(t, 60, cfg.GraceMinutes)
	require.Equal(t, []payment.PaymentType{payment.TypeOtokUSDTTRC20, payment.TypeOtokUSDTPolygon}, cfg.PaymentTypes())
	require.Equal(t, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", cfg.Addresses[otokchain.NetworkPolygon].Norm)
	require.Equal(t, otokTestEVMAddress, cfg.Addresses[otokchain.NetworkPolygon].Display)
}

func TestParseOtokUSDTConfigRejectsInvalid(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing rate": func(c map[string]string) { delete(c, OtokUSDTConfigExchangeRate) },
		"zero rate":    func(c map[string]string) { c[OtokUSDTConfigExchangeRate] = "0" },
		"negative fee": func(c map[string]string) { c[OtokUSDTConfigFeeRate] = "-1" },
		"fee 100":      func(c map[string]string) { c[OtokUSDTConfigFeeRate] = "100" },
		"bad tron":     func(c map[string]string) { c[OtokUSDTConfigTRC20Address] = "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBX" },
		"bad evm checksum": func(c map[string]string) {
			c[OtokUSDTConfigPolygonAddress] = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"
		},
		"no address": func(c map[string]string) {
			delete(c, OtokUSDTConfigTRC20Address)
			delete(c, OtokUSDTConfigPolygonAddress)
		},
		"rpc not url":       func(c map[string]string) { c[OtokUSDTConfigPolygonRPCURL] = "polygon-rpc" },
		"expire too short":  func(c map[string]string) { c[OtokUSDTConfigExpireMinutes] = "1" },
		"grace not integer": func(c map[string]string) { c[OtokUSDTConfigGraceMinutes] = "abc" },
		"usdt contract addr": func(c map[string]string) {
			c[OtokUSDTConfigERC20Address] = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := otokValidConfig()
			mutate(cfg)
			_, err := ParseOtokUSDTConfig(cfg)
			require.Error(t, err)
		})
	}
}

func TestOtokUSDTProviderCreatePayment(t *testing.T) {
	p, err := CreateProvider(payment.TypeOtokUSDT, "1", otokValidConfig())
	require.NoError(t, err)
	require.Equal(t, payment.TypeOtokUSDT, p.ProviderKey())

	resp, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{PaymentType: payment.TypeOtokUSDTPolygon, Amount: "14.09"})
	require.NoError(t, err)
	require.Equal(t, otokTestEVMAddress, resp.QRCode)
	require.Equal(t, "USD", resp.Currency)

	_, err = p.CreatePayment(context.Background(), payment.CreatePaymentRequest{PaymentType: payment.TypeOtokUSDTERC20})
	require.Error(t, err, "network without address must be rejected")

	q, err := p.QueryOrder(context.Background(), "sub2_x")
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPending, q.Status)

	_, err = p.Refund(context.Background(), payment.RefundRequest{})
	require.Error(t, err)
}

func TestOtokUSDTPaymentTypeHelpers(t *testing.T) {
	require.True(t, payment.IsOtokUSDTPaymentType("otok_usdt_trc20"))
	require.False(t, payment.IsOtokUSDTPaymentType("usdt_trc20"))
	require.False(t, payment.IsOtokUSDTPaymentType("otok_usdt"))
	require.Equal(t, "bep20", payment.OtokUSDTNetworkOf(payment.TypeOtokUSDTBEP20))
	require.Equal(t, payment.TypeOtokUSDTERC20, payment.OtokUSDTPaymentTypeOf("erc20"))
	require.Equal(t, "", payment.OtokUSDTPaymentTypeOf("solana"))
	// 上游 GetBasePaymentType 不得改写 otok 类型
	require.Equal(t, payment.TypeOtokUSDTTRC20, payment.GetBasePaymentType(payment.TypeOtokUSDTTRC20))
}
