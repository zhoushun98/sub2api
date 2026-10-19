package otokchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// tronAddressPrefix 是 TRON 主网地址的版本字节。
const tronAddressPrefix = 0x41

var (
	errInvalidAddress = errors.New("invalid address")
	base58Index       = func() [256]int {
		var idx [256]int
		for i := range idx {
			idx[i] = -1
		}
		for i := 0; i < len(base58Alphabet); i++ {
			idx[base58Alphabet[i]] = i
		}
		return idx
	}()
)

func base58Decode(s string) ([]byte, error) {
	if s == "" {
		return nil, errInvalidAddress
	}
	n := new(big.Int)
	radix := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		v := base58Index[s[i]]
		if v < 0 {
			return nil, errInvalidAddress
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(v)))
	}
	out := n.Bytes()
	leading := 0
	for leading < len(s) && s[leading] == base58Alphabet[0] {
		leading++
	}
	return append(make([]byte, leading), out...), nil
}

func base58Encode(b []byte) string {
	n := new(big.Int).SetBytes(b)
	radix := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, radix, mod)
		out = append(out, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < len(b) && b[i] == 0; i++ {
		out = append(out, base58Alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func doubleSHA256(b []byte) []byte {
	first := sha256.Sum256(b)
	second := sha256.Sum256(first[:])
	return second[:]
}

// TronAddressToHex 校验 base58check 形式的 TRON 地址，返回 20 字节账户（不含 0x41 前缀）的小写 hex。
func TronAddressToHex(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if len(addr) != 34 || addr[0] != 'T' {
		return "", fmt.Errorf("%w: tron address must be 34 chars starting with T", errInvalidAddress)
	}
	raw, err := base58Decode(addr)
	if err != nil || len(raw) != 25 {
		return "", fmt.Errorf("%w: malformed tron address", errInvalidAddress)
	}
	payload, checksum := raw[:21], raw[21:]
	if payload[0] != tronAddressPrefix {
		return "", fmt.Errorf("%w: unexpected tron address prefix", errInvalidAddress)
	}
	if hex.EncodeToString(doubleSHA256(payload)[:4]) != hex.EncodeToString(checksum) {
		return "", fmt.Errorf("%w: tron address checksum mismatch", errInvalidAddress)
	}
	return hex.EncodeToString(payload[1:]), nil
}

// TronAddressFromHex 把 20 字节账户 hex（可带 0x / 41 前缀）编码为 base58check 地址。
func TronAddressFromHex(h string) (string, error) {
	h = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "0x"))
	if len(h) == 42 && strings.HasPrefix(h, "41") {
		h = h[2:]
	}
	if len(h) != 40 {
		return "", fmt.Errorf("%w: tron hex address must be 20 bytes", errInvalidAddress)
	}
	body, err := hex.DecodeString(h)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidAddress, err)
	}
	payload := append([]byte{tronAddressPrefix}, body...)
	return base58Encode(append(payload, doubleSHA256(payload)[:4]...)), nil
}

func keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(b)
	return h.Sum(nil)
}

// NormalizeEVMAddress 校验 EVM 地址并返回小写形式；混合大小写输入必须满足 EIP-55 校验和。
func NormalizeEVMAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if len(addr) != 42 || (!strings.HasPrefix(addr, "0x") && !strings.HasPrefix(addr, "0X")) {
		return "", fmt.Errorf("%w: evm address must be 0x + 40 hex chars", errInvalidAddress)
	}
	body := addr[2:]
	if _, err := hex.DecodeString(body); err != nil {
		return "", fmt.Errorf("%w: evm address is not hex", errInvalidAddress)
	}
	lower := strings.ToLower(body)
	if body != lower && body != strings.ToUpper(body) {
		if ChecksumEVMAddress("0x"+lower) != "0x"+body {
			return "", fmt.Errorf("%w: evm address checksum mismatch", errInvalidAddress)
		}
	}
	return "0x" + lower, nil
}

// ChecksumEVMAddress 返回 EIP-55 校验和形式；输入需为合法的 0x 地址。
func ChecksumEVMAddress(addr string) string {
	lower := strings.ToLower(strings.TrimPrefix(addr, "0x"))
	hash := hex.EncodeToString(keccak256([]byte(lower)))
	out := make([]byte, len(lower))
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c >= 'a' && c <= 'f' && hash[i] >= '8' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return "0x" + string(out)
}

// Address 是校验后的收款地址。
type Address struct {
	// Display 用于展示与生成二维码：TRON 为 base58，EVM 为 EIP-55。
	Display string
	// Norm 用于匹配与唯一索引：TRON 保持 base58 原样（区分大小写），EVM 为小写。
	Norm string
}

// ParseAddress 按网络校验收款地址，并拒绝零地址与 USDT 合约地址本身。
func ParseAddress(n Network, addr string) (Address, error) {
	spec, err := SpecFor(n)
	if err != nil {
		return Address{}, err
	}
	switch spec.Kind {
	case KindTron:
		h, err := TronAddressToHex(addr)
		if err != nil {
			return Address{}, err
		}
		if strings.Trim(h, "0") == "" {
			return Address{}, fmt.Errorf("%w: zero address", errInvalidAddress)
		}
		norm := strings.TrimSpace(addr)
		if norm == spec.TokenContract {
			return Address{}, fmt.Errorf("%w: address is the USDT contract itself", errInvalidAddress)
		}
		return Address{Display: norm, Norm: norm}, nil
	case KindEVM:
		norm, err := NormalizeEVMAddress(addr)
		if err != nil {
			return Address{}, err
		}
		if strings.Trim(norm[2:], "0") == "" {
			return Address{}, fmt.Errorf("%w: zero address", errInvalidAddress)
		}
		if contract, cerr := NormalizeEVMAddress(spec.TokenContract); cerr == nil && norm == contract {
			return Address{}, fmt.Errorf("%w: address is the USDT contract itself", errInvalidAddress)
		}
		return Address{Display: ChecksumEVMAddress(norm), Norm: norm}, nil
	default:
		return Address{}, fmt.Errorf("unsupported network kind for %s", n)
	}
}

// NormalizeTokenContract 返回合约地址的匹配形式（TRON：20 字节 hex；EVM：小写 0x 地址）。
func NormalizeTokenContract(spec Spec) (string, error) {
	if spec.Kind == KindTron {
		return TronAddressToHex(spec.TokenContract)
	}
	return NormalizeEVMAddress(spec.TokenContract)
}

// NormalizeTxHash 统一交易哈希格式：EVM 为小写 0x 前缀，TRON 为小写无前缀。
func NormalizeTxHash(n Network, txHash string) (string, error) {
	spec, err := SpecFor(n)
	if err != nil {
		return "", err
	}
	h := strings.ToLower(strings.TrimSpace(txHash))
	h = strings.TrimPrefix(h, "0x")
	if len(h) != 64 {
		return "", errors.New("invalid transaction hash: must be 32 bytes hex")
	}
	if _, err := hex.DecodeString(h); err != nil {
		return "", errors.New("invalid transaction hash: not hex")
	}
	if spec.Kind == KindEVM {
		return "0x" + h, nil
	}
	return h, nil
}
