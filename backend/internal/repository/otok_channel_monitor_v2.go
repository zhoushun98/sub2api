package repository

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// otokLiveChannelMonitorV2GroupIDs 从渠道监控 V2 配置的分组勾选（group_ids）里剔除已删除的分组。
//
// 上游删除分组（软删除）时不会同步清理 group_ids；设置面板只列出现存分组，
// 已删除的 id 既看不到也取消不了，而 matrix 查询 LEFT JOIN groups 不过滤 deleted_at，
// 管理员视角又不按可用分组收窄，于是已删除分组的卡片会一直留在渠道状态页。
// 读、写配置时都过一遍，库里和接口返回的勾选列表就只剩现存分组。
//
// 停用（status != active）的分组保留，管理员可能只是临时停用。
// 查询失败时原样返回，不影响监控页可用性。
func otokLiveChannelMonitorV2GroupIDs(ctx context.Context, db *sql.DB, ids []int64) []int64 {
	if len(ids) == 0 || db == nil {
		return ids
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM groups WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(ids))
	if err != nil {
		return ids
	}
	defer func() { _ = rows.Close() }()
	live := make(map[int64]struct{}, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return ids
		}
		live[id] = struct{}{}
	}
	if rows.Err() != nil {
		return ids
	}
	return otokKeepLiveGroupIDs(ids, live)
}

// otokKeepLiveGroupIDs 按原顺序保留 live 中存在的 id。
// group_ids 为空在上游表示「监控全部分组」，所以勾选的分组全被删光时保留原值，
// 避免监控范围突然从几个分组放大到全部分组。
func otokKeepLiveGroupIDs(ids []int64, live map[int64]struct{}) []int64 {
	kept := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := live[id]; ok {
			kept = append(kept, id)
		}
	}
	if len(kept) == 0 {
		return ids
	}
	return kept
}
