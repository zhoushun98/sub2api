package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/shopspring/decimal"
)

// otok: USDT 链上收款渠道配置键。
const (
	OtokUSDTConfigExchangeRate   = "exchangeRate"
	OtokUSDTConfigFeeRate        = "feeRate"
	OtokUSDTConfigTRC20Address   = "trc20Address"
	OtokUSDTConfigBEP20Address   = "bep20Address"
	OtokUSDTConfigPolygonAddress = "polygonAddress"
	OtokUSDTConfigERC20Address   = "erc20Address"
	OtokUSDTConfigTronGridAPIKey = "tronGridApiKey"
	OtokUSDTConfigETHRPCURL      = "ethRpcUrl"
	OtokUSDTConfigBSCRPCURL      = "bscRpcUrl"
	OtokUSDTConfigPolygonRPCURL  = "polygonRpcUrl"
	OtokUSDTConfigExpireMinutes  = "expireMinutes"
	OtokUSDTConfigGraceMinutes   = "graceMinutes"
)

const (
	otokUSDTDefaultExpireMinutes = 30
	otokUSDTDefaultGraceMinutes  = 60
	otokUSDTMaxMinutes           = 24 * 60
)

// OtokUSDTAddressConfigKeys 网络 → 收款地址配置键。
var OtokUSDTAddressConfigKeys = map[otokchain.Network]string{
	otokchain.NetworkTRC20:   OtokUSDTConfigTRC20Address,
	otokchain.NetworkBEP20:   OtokUSDTConfigBEP20Address,
	otokchain.NetworkPolygon: OtokUSDTConfigPolygonAddress,
	otokchain.NetworkERC20:   OtokUSDTConfigERC20Address,
}

var otokUSDTRPCConfigKeys = map[otokchain.Network]string{
	otokchain.NetworkBEP20:   OtokUSDTConfigBSCRPCURL,
	otokchain.NetworkPolygon: OtokUSDTConfigPolygonRPCURL,
	otokchain.NetworkERC20:   OtokUSDTConfigETHRPCURL,
}

// OtokUSDTConfig 是解析校验后的 USDT 渠道配置。
type OtokUSDTConfig struct {
	// ExchangeRate 为 1 USDT 折合的站内金额（元）。
	ExchangeRate decimal.Decimal
	// FeeRate 为手续费百分数（1.5 表示 1.5%）。
	FeeRate        decimal.Decimal
	Addresses      map[otokchain.Network]otokchain.Address
	TronGridAPIKey string
	RPCURLs        map[otokchain.Network]string
	ExpireMinutes  int
	GraceMinutes   int
}

func parseOptionalMinutes(cfg map[string]string, key string, def, min int) (int, error) {
	raw := strings.TrimSpace(cfg[key])
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > otokUSDTMaxMinutes {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, min, otokUSDTMaxMinutes)
	}
	return v, nil
}

// ParseOtokUSDTConfig 解析并校验 USDT 渠道配置，不做任何网络请求。
func ParseOtokUSDTConfig(cfg map[string]string) (*OtokUSDTConfig, error) {
	out := &OtokUSDTConfig{
		Addresses:      map[otokchain.Network]otokchain.Address{},
		RPCURLs:        map[otokchain.Network]string{},
		TronGridAPIKey: strings.TrimSpace(cfg[OtokUSDTConfigTronGridAPIKey]),
	}
	rate, err := decimal.NewFromString(strings.TrimSpace(cfg[OtokUSDTConfigExchangeRate]))
	if err != nil || !rate.IsPositive() || rate.GreaterThan(decimal.NewFromInt(100000)) {
		return nil, fmt.Errorf("%s is required and must be a positive number (1 USDT = ? site currency)", OtokUSDTConfigExchangeRate)
	}
	out.ExchangeRate = rate
	out.FeeRate = decimal.Zero
	if raw := strings.TrimSpace(cfg[OtokUSDTConfigFeeRate]); raw != "" {
		fee, err := decimal.NewFromString(raw)
		if err != nil || fee.IsNegative() || fee.GreaterThanOrEqual(decimal.NewFromInt(100)) {
			return nil, fmt.Errorf("%s must be a percentage in [0, 100)", OtokUSDTConfigFeeRate)
		}
		out.FeeRate = fee
	}
	for _, n := range otokchain.AllNetworks {
		raw := strings.TrimSpace(cfg[OtokUSDTAddressConfigKeys[n]])
		if raw == "" {
			continue
		}
		addr, err := otokchain.ParseAddress(n, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", OtokUSDTAddressConfigKeys[n], err)
		}
		out.Addresses[n] = addr
	}
	if len(out.Addresses) == 0 {
		return nil, errors.New("at least one USDT receiving address is required")
	}
	for n, key := range otokUSDTRPCConfigKeys {
		raw := strings.TrimSpace(cfg[key])
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("%s must be an http(s) URL", key)
		}
		out.RPCURLs[n] = raw
	}
	if out.ExpireMinutes, err = parseOptionalMinutes(cfg, OtokUSDTConfigExpireMinutes, otokUSDTDefaultExpireMinutes, 5); err != nil {
		return nil, err
	}
	if out.GraceMinutes, err = parseOptionalMinutes(cfg, OtokUSDTConfigGraceMinutes, otokUSDTDefaultGraceMinutes, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// PaymentTypes 返回已配置收款地址的网络对应的支付类型。
func (c *OtokUSDTConfig) PaymentTypes() []payment.PaymentType {
	var out []payment.PaymentType
	for _, n := range otokchain.AllNetworks {
		if _, ok := c.Addresses[n]; ok {
			out = append(out, payment.OtokUSDTPaymentTypeOf(string(n)))
		}
	}
	return out
}

// RPCURL 返回网络的自定义 RPC 地址；空串表示使用默认公共节点。
func (c *OtokUSDTConfig) RPCURL(n otokchain.Network) string { return c.RPCURLs[n] }

// OtokUSDT 实现 payment.Provider。收款靠后台链上监听确认，QueryOrder 不访问链上。
type OtokUSDT struct {
	instanceID string
	cfg        *OtokUSDTConfig
}

// NewOtokUSDT 创建 USDT 渠道实例。
func NewOtokUSDT(instanceID string, config map[string]string) (*OtokUSDT, error) {
	cfg, err := ParseOtokUSDTConfig(config)
	if err != nil {
		return nil, fmt.Errorf("usdt config: %w", err)
	}
	return &OtokUSDT{instanceID: instanceID, cfg: cfg}, nil
}

func (p *OtokUSDT) Name() string        { return "USDT" }
func (p *OtokUSDT) ProviderKey() string { return payment.TypeOtokUSDT }
func (p *OtokUSDT) SupportedTypes() []payment.PaymentType {
	return p.cfg.PaymentTypes()
}

// CreatePayment 返回收款地址作为二维码内容；唯一金额与有效期已在下单事务中写入订单。
func (p *OtokUSDT) CreatePayment(_ context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	network, ok := otokchain.ParseNetwork(payment.OtokUSDTNetworkOf(req.PaymentType))
	if !ok {
		return nil, fmt.Errorf("unsupported usdt payment type: %s", req.PaymentType)
	}
	addr, ok := p.cfg.Addresses[network]
	if !ok {
		return nil, fmt.Errorf("usdt network %s has no receiving address", network)
	}
	return &payment.CreatePaymentResponse{
		QRCode:     addr.Display,
		Currency:   "USD",
		ResultType: payment.CreatePaymentResultOrderCreated,
	}, nil
}

// QueryOrder 始终返回 pending：链上到账由 otok USDT 监听服务确认后直接入账。
func (p *OtokUSDT) QueryOrder(_ context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	return &payment.QueryOrderResponse{TradeNo: tradeNo, Status: payment.ProviderStatusPending}, nil
}

func (p *OtokUSDT) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, errors.New("usdt provider does not accept webhooks")
}

func (p *OtokUSDT) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, errors.New("usdt orders cannot be refunded automatically; refund off-chain manually")
}

var _ payment.Provider = (*OtokUSDT)(nil)
