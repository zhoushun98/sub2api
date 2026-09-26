//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2MaxChunkForDepth(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	// Within last day → tightest ceiling (2h).
	require.Equal(t, channelMonitorV2MaxChunkNear1d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*time.Hour)))
	// Between 1d and 7d → 4h.
	require.Equal(t, channelMonitorV2MaxChunkNear7d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*24*time.Hour)))
	// Older than 7d → 6h (never 24h default).
	require.Equal(t, channelMonitorV2MaxChunkFar, channelMonitorV2MaxChunkForDepth(now, now.Add(-10*24*time.Hour)))
	require.Less(t, channelMonitorV2MaxChunkFar, 24*time.Hour)
	require.Equal(t, time.Hour, channelMonitorV2BackfillChunkInit)
	require.Equal(t, 15*time.Minute, channelMonitorV2MinBackfillChunk)
}

func TestChannelMonitorV2AggregatorAdaptiveChunk(t *testing.T) {
	s := NewChannelMonitorV2Aggregator(nil, nil, nil)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-3 * time.Hour)

	// Failure shrinks chunk and sets backoff floor.
	s.backfillChunk = 2 * time.Hour
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, time.Hour, s.backfillChunk)
	require.Equal(t, time.Minute, s.nextWaitFloor)
	require.Equal(t, 1, s.backfillFailures)

	// Repeated failure halves again and raises floor.
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, 30*time.Minute, s.backfillChunk)
	require.Equal(t, 2*time.Minute, s.nextWaitFloor)

	// Fast success grows within depth ceiling and clears backoff.
	s.recordBackfillSuccess(cursor.Add(-30*time.Minute), 5*time.Second, now)
	require.Equal(t, 0, s.backfillFailures)
	require.Equal(t, time.Duration(0), s.nextWaitFloor)
	require.Greater(t, s.backfillChunk, 30*time.Minute)
	require.LessOrEqual(t, s.backfillChunk, channelMonitorV2MaxChunkForDepth(now, cursor.Add(-30*time.Minute)))
}

func TestChannelMonitorV2RecentWindowCatchesUpAfterStall(t *testing.T) {
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

	// 正常运行：水位紧跟当前时间，只刷新末尾 overlap。
	start, end := channelMonitorV2RecentWindow(now, now.Add(-time.Minute))
	require.Equal(t, now.Add(-channelMonitorV2RecentOverlap), start)
	require.Equal(t, now, end)

	// 从未聚合过：同样只刷新 overlap，历史交给回补流程。
	start, end = channelMonitorV2RecentWindow(now, time.Time{})
	require.Equal(t, now.Add(-channelMonitorV2RecentOverlap), start)
	require.Equal(t, now, end)

	// 停摆 4.5 小时：从水位（再往前留 overlap）一直补到现在，不留空洞。
	through := now.Add(-270 * time.Minute)
	start, end = channelMonitorV2RecentWindow(now, through)
	require.Equal(t, through.Add(-channelMonitorV2RecentOverlap), start)
	require.Equal(t, now, end)

	// 停摆超过单轮上限：本轮只推进一个追赶块，下轮从新的水位继续。
	through = now.Add(-20 * time.Hour)
	start, end = channelMonitorV2RecentWindow(now, through)
	require.Equal(t, through.Add(-channelMonitorV2RecentOverlap), start)
	require.Equal(t, start.Add(channelMonitorV2CatchUpChunk), end)
}
