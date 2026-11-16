//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOtokChannelMonitorV2ConfigDropsDeletedGroups(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := &channelMonitorV2Repository{db: integrationDB}
	suffix := time.Now().UnixNano()

	live, err := client.Group.Create().SetName(fmt.Sprintf("otok-monitor-live-%d", suffix)).SetPlatform(service.PlatformGemini).Save(ctx)
	require.NoError(t, err)
	deleted, err := client.Group.Create().SetName(fmt.Sprintf("otok-monitor-deleted-%d", suffix)).SetPlatform(service.PlatformGemini).Save(ctx)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at = NOW() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)

	var originalIDs []int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT group_ids FROM channel_monitor_v2_config WHERE id = 1`).Scan(pq.Array(&originalIDs)))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v2_config SET group_ids = $1 WHERE id = 1`, pq.Array(originalIDs))
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id = ANY($1)`, pq.Array([]int64{live.ID, deleted.ID}))
	})

	// 读：库里残留的已删除分组不出现在配置里。
	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v2_config SET group_ids = $1 WHERE id = 1`, pq.Array([]int64{live.ID, deleted.ID}))
	require.NoError(t, err)
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{live.ID}, cfg.GroupIDs)

	// 写：旧表单带着已删除分组保存，也不会写回库里。
	cfg.GroupIDs = []int64{live.ID, deleted.ID}
	updated, err := repo.UpdateConfig(ctx, *cfg, cfg.Version)
	require.NoError(t, err)
	require.Equal(t, []int64{live.ID}, updated.GroupIDs)
	var stored []int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT group_ids FROM channel_monitor_v2_config WHERE id = 1`).Scan(pq.Array(&stored)))
	require.Equal(t, []int64{live.ID}, stored)

	// 只勾了已删除分组时保留原值，不能变成空列表（= 全部分组）。
	_, err = integrationDB.ExecContext(ctx, `UPDATE channel_monitor_v2_config SET group_ids = $1 WHERE id = 1`, pq.Array([]int64{deleted.ID}))
	require.NoError(t, err)
	cfg, err = repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{deleted.ID}, cfg.GroupIDs)
}
