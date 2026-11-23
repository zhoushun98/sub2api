//go:build unit

package service

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
	"github.com/stretchr/testify/require"
)

const otokTestEVMNorm = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"

func otokTestBinanceConfig(t *testing.T) *provider.OtokUSDTConfig {
	t.Helper()
	cfg, err := provider.ParseOtokUSDTConfig(map[string]string{
		provider.OtokUSDTConfigExchangeRate:     "7.1",
		provider.OtokUSDTConfigTRC20Address:     otokTestTronAddress,
		provider.OtokUSDTConfigBEP20Address:     otokTestEVMAddress,
		provider.OtokUSDTConfigDepositSource:    provider.OtokUSDTSourceBinance,
		provider.OtokUSDTConfigBinanceAPIKey:    "k",
		provider.OtokUSDTConfigBinanceAPISecret: "s",
	})
	require.NoError(t, err)
	return cfg
}

func TestOtokBinanceRecordsFilterAndOrdinal(t *testing.T) {
	cfg := otokTestBinanceConfig(t)
	const hash = "0x7288040170cf17b5b8a1cf0c6c5c27e246a741adfef9e822382bc446137636ea"
	deposits := []otokchain.BinanceDeposit{
		// 同一笔链上交易里转入两次：按币安 id 排序依次为 -1、-2（id 位数不同也要按数值排）。
		{ID: "100", Coin: "USDT", Amount: "2.00", Network: "BSC", Status: 1, Address: otokTestEVMNorm, TxID: hash, InsertTime: 1},
		{ID: "99", Coin: "USDT", Amount: "1.00", Network: "BSC", Status: 1, Address: otokTestEVMNorm, TxID: hash, InsertTime: 1},
		// 链下转账，Polygon 网络转入同一个 EVM 地址（Polygon 未配置地址也接收：钱进的是同一个账户）。
		{ID: "5", Coin: "USDT", Amount: "7.52", Network: "MATIC", Status: 0, Address: otokTestEVMNorm, TxID: "Off-chain transfer 434226888519", TransferType: 1, InsertTime: 1},
		// 以下都应被过滤。
		{ID: "6", Coin: "USDT", Amount: "1.00", Network: "BSC", Status: 2, Address: otokTestEVMNorm, TxID: "Off-chain transfer 1234567", TransferType: 1, InsertTime: 1},
		{ID: "7", Coin: "USDT", Amount: "1.00", Network: "BSC", Status: 7, Address: otokTestEVMNorm, TxID: "Off-chain transfer 1234568", TransferType: 1, InsertTime: 1},
		{ID: "8", Coin: "USDC", Amount: "1.00", Network: "BSC", Status: 1, Address: otokTestEVMNorm, TxID: "Off-chain transfer 1234569", TransferType: 1, InsertTime: 1},
		{ID: "9", Coin: "USDT", Amount: "1.00", Network: "PLASMA", Status: 1, Address: otokTestEVMNorm, TxID: "Off-chain transfer 1234570", TransferType: 1, InsertTime: 1},
		{ID: "10", Coin: "USDT", Amount: "1.00", Network: "BSC", Status: 1, Address: "0x1111111111111111111111111111111111111111", TxID: "Off-chain transfer 1234571", TransferType: 1, InsertTime: 1},
		{ID: "", Coin: "USDT", Amount: "1.00", Network: "BSC", Status: 1, Address: otokTestEVMNorm, TxID: "Off-chain transfer 1234572", TransferType: 1, InsertTime: 1},
	}
	recs := otokBinanceRecords(cfg, deposits)
	require.Len(t, recs, 3)

	// 同一 txId 下的另一笔从处理中变成被拒绝：其余记录的序号不变（序号按全部记录分配）。
	logIndexOf99 := func(status98 int) int64 {
		list := append([]otokchain.BinanceDeposit{
			{ID: "98", Coin: "USDT", Amount: "3.00", Network: "BSC", Status: status98, Address: otokTestEVMNorm, TxID: hash, InsertTime: 1},
		}, deposits[:2]...)
		for _, r := range otokBinanceRecords(cfg, list) {
			if r.deposit.ID == "99" {
				return r.transfer.LogIndex
			}
		}
		t.Fatal("deposit 99 missing")
		return 0
	}
	require.Equal(t, logIndexOf99(otokchain.BinanceDepositPending), logIndexOf99(otokchain.BinanceDepositRejected))
	got := map[string]otokchain.Transfer{}
	for _, r := range recs {
		got[r.deposit.ID] = r.transfer
	}
	require.EqualValues(t, -1, got["99"].LogIndex)
	require.EqualValues(t, -2, got["100"].LogIndex)
	require.Equal(t, hash, got["99"].TxHash)
	require.Equal(t, otokchain.NetworkPolygon, got["5"].Network)
	require.Equal(t, "Off-chain transfer 434226888519", got["5"].TxHash)
	require.Equal(t, otokTestEVMNorm, got["5"].ToNorm)
}

func TestOtokParseTxInput(t *testing.T) {
	in, err := otokParseTxInput(otokchain.NetworkBEP20, " 链下转账 434226888519 ", true)
	require.NoError(t, err)
	require.Equal(t, otokTxInput{Key: "434226888519", OffChain: true}, in)
	require.True(t, in.matches(&otokUSDTTransfer{TxHash: "Off-chain transfer 434226888519", Source: otokSourceBinance}))
	require.False(t, in.matches(&otokUSDTTransfer{TxHash: "Off-chain transfer 434226888518", Source: otokSourceBinance}))

	_, err = otokParseTxInput(otokchain.NetworkBEP20, "434226888519", false)
	require.Equal(t, "USDT_OFFCHAIN_UNSUPPORTED", infraerrors.Reason(err))

	const hash = "0x7288040170CF17B5B8A1CF0C6C5C27E246A741ADFEF9E822382BC446137636EA"
	for _, binance := range []bool{true, false} {
		in, err = otokParseTxInput(otokchain.NetworkBEP20, hash, binance)
		require.NoError(t, err)
		require.Equal(t, otokTxInput{Key: "0x7288040170cf17b5b8a1cf0c6c5c27e246a741adfef9e822382bc446137636ea"}, in)
		require.True(t, in.matches(&otokUSDTTransfer{TxHash: in.Key}))
	}
	for _, bad := range []string{"", "hello", "12345", "0x1234"} {
		_, err = otokParseTxInput(otokchain.NetworkBEP20, bad, true)
		require.Equal(t, "USDT_TX_INVALID", infraerrors.Reason(err), bad)
	}
}

func TestOtokBinanceTransferMatchesAcrossNetworks(t *testing.T) {
	created := time.Date(2026, 11, 23, 22, 13, 6, 0, time.UTC)
	r := &otokUSDTReservation{
		Network: otokchain.NetworkBEP20, ToAddressNorm: otokTestEVMNorm, AmountCents: 752,
		CreatedAt: created, ReservedUntil: created.Add(90 * time.Minute),
	}
	row := &otokUSDTTransfer{
		Network: otokchain.NetworkPolygon, ToAddressNorm: otokTestEVMNorm, Status: otokTransferSeen,
		AmountRaw: otokchain.CentsToRaw(752, 6), Decimals: 6, Source: otokSourceBinance,
		TxHash:    "Off-chain transfer 434226888519",
		BlockTime: sql.NullTime{Time: created.Add(time.Minute), Valid: true},
		CreatedAt: created.Add(70 * time.Second),
	}
	require.True(t, otokTransferAutoMatchable(row, r), "币安来源的转账不要求网络一致")
	require.Empty(t, otokBoundTransferMismatch(row, r))
	require.True(t, otokIsOffChainTransfer(row))

	// 链上来源仍然要求网络一致；地址不同的币安来源也不匹配。
	row.Source = otokSourceWatcher
	require.False(t, otokTransferAutoMatchable(row, r))
	require.NotEmpty(t, otokBoundTransferMismatch(row, r))
	require.False(t, otokIsOffChainTransfer(row))
	row.Source = otokSourceBinance
	row.ToAddressNorm = "0x1111111111111111111111111111111111111111"
	require.False(t, otokTransferAutoMatchable(row, r))
}

func TestOtokBinanceLookupKey(t *testing.T) {
	require.Equal(t, "434226888519", otokBinanceLookupKey("Off-chain transfer 434226888519"))
	h := "0x7288040170cf17b5b8a1cf0c6c5c27e246a741adfef9e822382bc446137636ea"
	require.Equal(t, h, otokBinanceLookupKey(h))
	require.True(t, otokNumericLess("99", "100"))
	require.False(t, otokNumericLess("100", "99"))
	require.True(t, otokNumericLess("5326511839180757249", "5326511839180757250"))
}
