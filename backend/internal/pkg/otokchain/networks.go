// Package otokchain 是 otok fork 的 USDT 链上查询工具包：网络常量、地址校验、
// 金额换算，以及 TronGrid / EVM JSON-RPC 客户端。只做链上数据读取，不依赖业务层。
package otokchain

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Network 标识一条 USDT 收款网络。
type Network string

const (
	NetworkTRC20   Network = "trc20"
	NetworkBEP20   Network = "bep20"
	NetworkPolygon Network = "polygon"
	NetworkERC20   Network = "erc20"
)

// AllNetworks 按前端展示顺序列出全部网络。
var AllNetworks = []Network{NetworkTRC20, NetworkBEP20, NetworkPolygon, NetworkERC20}

// Kind 区分链的查询方式。
type Kind int

const (
	KindTron Kind = iota + 1
	KindEVM
)

// Spec 描述一条网络上 USDT 的链上参数。
type Spec struct {
	Network     Network
	Kind        Kind
	DisplayName string
	ChainID     int64 // 仅 EVM
	// TokenContract 为展示形式：TRON 为 base58，EVM 为 EIP-55 校验和地址。
	TokenContract string
	Decimals      int32
	// Confirmations 为 EVM 入账所需确认块数；TRON 使用固化块（only_confirmed），此值仅用于展示。
	Confirmations   int64
	DefaultEndpoint string
	// AvgBlockSeconds 用于估算扫描起点。
	AvgBlockSeconds float64
	ExplorerTxURL   string // 含一个 %s 占位符
}

// Testnet 时 TokenContract 必须通过环境变量指定，避免误用非官方测试币。
const (
	envTestnet         = "OTOK_USDT_TESTNET"
	envTestnetContract = "OTOK_USDT_TESTNET_%s_CONTRACT"
	envTestnetDecimals = "OTOK_USDT_TESTNET_%s_DECIMALS"
)

var mainnetSpecs = map[Network]Spec{
	NetworkTRC20: {
		Network: NetworkTRC20, Kind: KindTron, DisplayName: "TRON (TRC20)",
		TokenContract: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6, Confirmations: 19,
		DefaultEndpoint: "https://api.trongrid.io", AvgBlockSeconds: 3,
		ExplorerTxURL: "https://tronscan.org/#/transaction/%s",
	},
	NetworkBEP20: {
		Network: NetworkBEP20, Kind: KindEVM, DisplayName: "BNB Smart Chain (BEP20)", ChainID: 56,
		TokenContract: "0x55d398326f99059fF775485246999027B3197955", Decimals: 18, Confirmations: 15,
		DefaultEndpoint: "https://bsc-rpc.publicnode.com", AvgBlockSeconds: 0.75,
		ExplorerTxURL: "https://bscscan.com/tx/%s",
	},
	NetworkPolygon: {
		Network: NetworkPolygon, Kind: KindEVM, DisplayName: "Polygon PoS", ChainID: 137,
		TokenContract: "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", Decimals: 6, Confirmations: 64,
		DefaultEndpoint: "https://polygon-bor-rpc.publicnode.com", AvgBlockSeconds: 2,
		ExplorerTxURL: "https://polygonscan.com/tx/%s",
	},
	NetworkERC20: {
		Network: NetworkERC20, Kind: KindEVM, DisplayName: "Ethereum (ERC20)", ChainID: 1,
		TokenContract: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, Confirmations: 12,
		DefaultEndpoint: "https://ethereum-rpc.publicnode.com", AvgBlockSeconds: 12,
		ExplorerTxURL: "https://etherscan.io/tx/%s",
	},
}

var testnetSpecs = map[Network]Spec{
	NetworkTRC20: {
		Network: NetworkTRC20, Kind: KindTron, DisplayName: "TRON Nile (TRC20)",
		TokenContract: "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf", Decimals: 6, Confirmations: 19,
		DefaultEndpoint: "https://nile.trongrid.io", AvgBlockSeconds: 3,
		ExplorerTxURL: "https://nile.tronscan.org/#/transaction/%s",
	},
	NetworkBEP20: {
		Network: NetworkBEP20, Kind: KindEVM, DisplayName: "BSC Testnet", ChainID: 97,
		Decimals: 18, Confirmations: 3,
		DefaultEndpoint: "https://bsc-testnet-rpc.publicnode.com", AvgBlockSeconds: 0.75,
		ExplorerTxURL: "https://testnet.bscscan.com/tx/%s",
	},
	NetworkPolygon: {
		Network: NetworkPolygon, Kind: KindEVM, DisplayName: "Polygon Amoy", ChainID: 80002,
		Decimals: 6, Confirmations: 3,
		DefaultEndpoint: "https://polygon-amoy-bor-rpc.publicnode.com", AvgBlockSeconds: 2,
		ExplorerTxURL: "https://amoy.polygonscan.com/tx/%s",
	},
	NetworkERC20: {
		Network: NetworkERC20, Kind: KindEVM, DisplayName: "Ethereum Sepolia", ChainID: 11155111,
		Decimals: 6, Confirmations: 3,
		DefaultEndpoint: "https://ethereum-sepolia-rpc.publicnode.com", AvgBlockSeconds: 12,
		ExplorerTxURL: "https://sepolia.etherscan.io/tx/%s",
	},
}

// IsTestnet 报告是否通过 OTOK_USDT_TESTNET 切换到测试网（仅用于联调）。
func IsTestnet() bool {
	v := strings.TrimSpace(os.Getenv(envTestnet))
	return v == "1" || strings.EqualFold(v, "true")
}

// ParseNetwork 解析网络标识。
func ParseNetwork(s string) (Network, bool) {
	n := Network(strings.ToLower(strings.TrimSpace(s)))
	_, ok := mainnetSpecs[n]
	return n, ok
}

// SpecFor 返回网络参数；测试网模式下合约与精度可由环境变量覆盖。
func SpecFor(n Network) (Spec, error) {
	if !IsTestnet() {
		spec, ok := mainnetSpecs[n]
		if !ok {
			return Spec{}, fmt.Errorf("unknown network: %s", n)
		}
		return spec, nil
	}
	spec, ok := testnetSpecs[n]
	if !ok {
		return Spec{}, fmt.Errorf("unknown network: %s", n)
	}
	key := strings.ToUpper(string(n))
	if v := strings.TrimSpace(os.Getenv(fmt.Sprintf(envTestnetContract, key))); v != "" {
		spec.TokenContract = v
	}
	if v := strings.TrimSpace(os.Getenv(fmt.Sprintf(envTestnetDecimals, key))); v != "" {
		d, err := strconv.ParseInt(v, 10, 32)
		if err != nil || d < 2 || d > 36 {
			return Spec{}, fmt.Errorf("invalid %s: %q", fmt.Sprintf(envTestnetDecimals, key), v)
		}
		spec.Decimals = int32(d)
	}
	if spec.TokenContract == "" {
		return Spec{}, fmt.Errorf("testnet %s requires %s", n, fmt.Sprintf(envTestnetContract, key))
	}
	return spec, nil
}

// ExplorerTxLink 返回区块浏览器交易链接。
func (s Spec) ExplorerTxLink(txHash string) string {
	if s.ExplorerTxURL == "" || txHash == "" {
		return ""
	}
	if s.Kind == KindEVM && !strings.HasPrefix(txHash, "0x") {
		txHash = "0x" + txHash
	}
	return fmt.Sprintf(s.ExplorerTxURL, txHash)
}
