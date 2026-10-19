package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/Wei-Shaw/sub2api/internal/pkg/otokchain"
)

// otok: USDT 链上监听。每轮：释放过期预留 → 扫描有活跃预留的地址 → 精确匹配绑定 → 复核确认并入账。

const (
	otokUSDTWatchInterval     = 15 * time.Second
	otokUSDTWatcherLeaderKey  = "otok:usdt:watcher:leader"
	otokUSDTWatcherLeaderTTL  = 3 * time.Minute
	otokUSDTWatcherRunTimeout = 2 * time.Minute
	otokUSDTTronOverlap       = 60 * time.Second
	otokUSDTScanLookback      = 2 * time.Minute
	otokUSDTEVMMaxCalls       = 8
	// otokUSDTEVMHeadLag：EVM 扫描终点比最新块少几个块。公共 RPC 背后各节点高度不一，
	// 处理 eth_getLogs 的节点可能还没同步到 eth_blockNumber 返回的高度
	// （"block range extends beyond current head block"）。
	otokUSDTEVMHeadLag       = 3
	otokUSDTTronMaxPages     = 5
	otokUSDTWatcherOperator  = "otok-usdt-watcher"
	otokUSDTEVMCursorAddress = "*"
)

// Start 启动后台监听。
func (s *OtokUSDTService) Start() {
	if s == nil || s.paymentSvc == nil || s.entClient == nil || s.interval <= 0 {
		return
	}
	if otokchain.IsTestnet() {
		slog.Warn("[OtokUSDT] OTOK_USDT_TESTNET is enabled: watching TESTNET contracts, do not use in production")
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.runOnce()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止后台监听并等待当前轮次结束。
func (s *OtokUSDTService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *OtokUSDTService) runOnce() {
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 2*time.Second)
	release, ok := tryAcquireSingletonLeaderLock(lockCtx, s.lockCache, s.db, otokUSDTWatcherLeaderKey, s.instanceID, otokUSDTWatcherLeaderTTL)
	lockCancel()
	if !ok {
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), otokUSDTWatcherRunTimeout)
	defer cancel()
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := s.RunOnce(ctx); err != nil {
		slog.Warn("[OtokUSDT] watcher round failed", "error", err)
	}
}

// RunOnce 执行一轮监听。
func (s *OtokUSDTService) RunOnce(ctx context.Context) error {
	if n, err := otokReleaseReservations(ctx, s.entClient); err != nil {
		return fmt.Errorf("release reservations: %w", err)
	} else if n > 0 {
		slog.Info("[OtokUSDT] released usdt amount reservations", "count", n)
	}
	reservations, err := otokListActiveReservations(ctx, s.entClient)
	if err != nil {
		return fmt.Errorf("list reservations: %w", err)
	}
	if len(reservations) > 0 {
		s.scanAndMatch(ctx, reservations)
	}
	s.processPendingCredits(ctx)
	return nil
}

func (s *OtokUSDTService) scanAndMatch(ctx context.Context, reservations []*otokUSDTReservation) {
	byNetwork := map[otokchain.Network][]*otokUSDTReservation{}
	for _, r := range reservations {
		byNetwork[r.Network] = append(byNetwork[r.Network], r)
	}
	for network, list := range byNetwork {
		if ctx.Err() != nil {
			return
		}
		spec, err := otokchain.SpecFor(network)
		if err != nil {
			slog.Warn("[OtokUSDT] unknown network in reservations", "network", network, "error", err)
			continue
		}
		// 同一时间只允许一个启用实例；取最新预留所属实例的端点配置。
		ucfg := s.instanceConfig(ctx, list[len(list)-1].InstanceID)
		switch spec.Kind {
		case otokchain.KindTron:
			byAddr := map[string][]*otokUSDTReservation{}
			for _, r := range list {
				byAddr[r.ToAddressNorm] = append(byAddr[r.ToAddressNorm], r)
			}
			for addr, rs := range byAddr {
				s.scanTron(ctx, spec, ucfg, addr, rs)
			}
		case otokchain.KindEVM:
			s.scanEVM(ctx, spec, ucfg, list)
		}
	}
}

func otokEarliestCreated(rs []*otokUSDTReservation) time.Time {
	earliest := rs[0].CreatedAt
	for _, r := range rs[1:] {
		if r.CreatedAt.Before(earliest) {
			earliest = r.CreatedAt
		}
	}
	return earliest.Add(-otokUSDTScanLookback)
}

func (s *OtokUSDTService) scanTron(ctx context.Context, spec otokchain.Spec, ucfg *provider.OtokUSDTConfig, addr string, rs []*otokUSDTReservation) {
	c, err := s.tronClient(spec, ucfg.TronGridAPIKey)
	if err != nil {
		_ = otokSaveCursorError(ctx, s.entClient, spec.Network, addr, err)
		return
	}
	since := otokEarliestCreated(rs).UnixMilli()
	if cur, err := otokGetCursor(ctx, s.entClient, spec.Network, addr); err == nil && cur != nil {
		if resume := cur.LastTsMs - otokUSDTTronOverlap.Milliseconds(); resume > since {
			since = resume
		}
	}
	transfers, maxTs, more, scanErr := c.IncomingTransfers(ctx, addr, since, otokUSDTTronMaxPages)
	rows := s.recordTransfers(ctx, spec, rs[0], transfers)
	if scanErr != nil {
		slog.Warn("[OtokUSDT] tron scan failed", "address", addr, "error", scanErr)
		_ = otokSaveCursorError(ctx, s.entClient, spec.Network, addr, scanErr)
	} else {
		cursorTs := maxTs
		if more {
			// 翻页到上限仍有数据（如被大量小额转账刷屏）：下一轮直接从 maxTs 继续，
			// 不再回退重叠窗口，避免在同一段时间里原地打转。
			cursorTs = maxTs + otokUSDTTronOverlap.Milliseconds()
		}
		_ = otokSaveCursorSuccess(ctx, s.entClient, spec.Network, addr, 0, cursorTs)
	}
	s.matchTransfers(ctx, rows, rs)
}

func (s *OtokUSDTService) scanEVM(ctx context.Context, spec otokchain.Spec, ucfg *provider.OtokUSDTConfig, rs []*otokUSDTReservation) {
	c, err := s.evmClient(spec, ucfg.RPCURL(spec.Network))
	if err == nil {
		err = c.EnsureChainID(ctx)
	}
	var latest int64
	if err == nil {
		latest, err = c.BlockNumber(ctx)
	}
	if err != nil {
		slog.Warn("[OtokUSDT] evm rpc unavailable", "network", spec.Network, "error", err)
		_ = otokSaveCursorError(ctx, s.entClient, spec.Network, otokUSDTEVMCursorAddress, err)
		return
	}
	latest -= otokUSDTEVMHeadLag
	windowBlocks := int64(math.Ceil(time.Since(otokEarliestCreated(rs)).Seconds()/spec.AvgBlockSeconds)) + 20
	start := latest - windowBlocks
	if cur, err := otokGetCursor(ctx, s.entClient, spec.Network, otokUSDTEVMCursorAddress); err == nil && cur != nil && cur.LastBlock > 0 {
		resume := cur.LastBlock + 1
		// 未达确认数的尾部区块每轮重扫，链重组后的新位置也能被发现。
		if tail := latest - spec.Confirmations - 5; resume > tail {
			resume = tail
		}
		if resume > start {
			start = resume
		}
	}
	if start < 1 {
		start = 1
	}
	if start > latest {
		return
	}
	seen := map[string]bool{}
	var addrs []string
	for _, r := range rs {
		if !seen[r.ToAddressNorm] {
			seen[r.ToAddressNorm] = true
			addrs = append(addrs, r.ToAddressNorm)
		}
	}
	s.clientsMu.Lock()
	chunk := s.evmChunks[spec.Network]
	s.clientsMu.Unlock()
	res, scanErr := c.ScanTransfers(ctx, start, latest, addrs, chunk, otokUSDTEVMMaxCalls)
	s.clientsMu.Lock()
	s.evmChunks[spec.Network] = res.NextChunk
	s.clientsMu.Unlock()

	blockTimes := map[int64]time.Time{}
	for i := range res.Transfers {
		t := &res.Transfers[i]
		if !t.BlockTime.IsZero() {
			continue
		}
		if ts, ok := blockTimes[t.BlockNumber]; ok {
			t.BlockTime = ts
			continue
		}
		if _, ts, err := c.Block(ctx, t.BlockNumber); err == nil {
			blockTimes[t.BlockNumber] = ts
			t.BlockTime = ts
		}
	}
	rows := s.recordTransfers(ctx, spec, rs[0], res.Transfers)
	if res.ScannedTo >= start {
		_ = otokSaveCursorSuccess(ctx, s.entClient, spec.Network, otokUSDTEVMCursorAddress, res.ScannedTo, 0)
	}
	if scanErr != nil {
		slog.Warn("[OtokUSDT] evm scan failed", "network", spec.Network, "from", start, "to", latest, "error", scanErr)
		_ = otokSaveCursorError(ctx, s.entClient, spec.Network, otokUSDTEVMCursorAddress, scanErr)
	}
	s.matchTransfers(ctx, rows, rs)
}

// recordTransfers 记录扫描到的转账；不足 0.01 USDT 的转账（地址投毒等垃圾转账）直接忽略。
func (s *OtokUSDTService) recordTransfers(ctx context.Context, spec otokchain.Spec, ref *otokUSDTReservation, transfers []otokchain.Transfer) []*otokUSDTTransfer {
	rows := make([]*otokUSDTTransfer, 0, len(transfers))
	minRaw := otokchain.CentsToRaw(1, spec.Decimals)
	for _, t := range transfers {
		if t.Amount == nil || t.Amount.Cmp(minRaw) < 0 {
			continue
		}
		row, err := otokUpsertTransfer(ctx, s.entClient, t, ref.TokenContract, spec.Decimals, otokSourceWatcher)
		if err != nil {
			slog.Warn("[OtokUSDT] record transfer failed", "tx", t.TxHash, "error", err)
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

// matchTransfers 把未绑定的转账按（地址, 精确金额, 时间窗口）匹配到等待中的预留，规则见 otokTransferAutoMatchable。
func (s *OtokUSDTService) matchTransfers(ctx context.Context, rows []*otokUSDTTransfer, rs []*otokUSDTReservation) {
	index := make(map[string]*otokUSDTReservation, len(rs))
	for _, r := range rs {
		index[fmt.Sprintf("%s|%d", r.ToAddressNorm, r.AmountCents)] = r
	}
	for _, row := range rows {
		cents, exact := row.AmountCents()
		if !exact {
			continue
		}
		key := fmt.Sprintf("%s|%d", row.ToAddressNorm, cents)
		r := index[key]
		if r == nil || !otokTransferAutoMatchable(row, r) {
			continue
		}
		order, err := s.entClient.PaymentOrder.Get(ctx, r.OrderID)
		if err != nil || !otokOrderMatchable(order.Status) {
			continue
		}
		ok, err := s.bindTransfer(ctx, row.ID, r.OrderID, "matched by watcher", false)
		if err != nil {
			slog.Warn("[OtokUSDT] bind transfer failed", "tx", row.TxHash, "order", r.OrderID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		delete(index, key)
		slog.Info("[OtokUSDT] matched usdt transfer", "order", r.OrderID, "network", row.Network, "tx", row.TxHash)
		s.paymentSvc.writeAuditLog(ctx, r.OrderID, "OTOK_USDT_MATCHED", otokUSDTWatcherOperator, map[string]any{
			"network": string(row.Network), "txHash": row.TxHash, "logIndex": row.LogIndex,
			"amount": otokchain.FormatRaw(row.AmountRaw, row.Decimals), "from": row.FromAddress,
		})
	}
}

func (s *OtokUSDTService) processPendingCredits(ctx context.Context) {
	list, err := otokListPendingCreditTransfers(ctx, s.entClient, otokUSDTMaxCreditAttempts)
	if err != nil {
		slog.Warn("[OtokUSDT] list pending credits failed", "error", err)
		return
	}
	for _, t := range list {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.creditBoundTransfer(ctx, t, otokUSDTWatcherOperator); err != nil {
			slog.Warn("[OtokUSDT] credit usdt transfer failed", "tx", t.TxHash, "order", t.OrderID.Int64, "error", err)
		}
	}
}
