//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestOtokKeepLiveGroupIDs(t *testing.T) {
	live := map[int64]struct{}{3: {}, 9: {}}
	require.Equal(t, []int64{3, 9}, otokKeepLiveGroupIDs([]int64{3, 5, 9, 271}, live))
	require.Equal(t, []int64{3, 9}, otokKeepLiveGroupIDs([]int64{3, 9}, live))
	// 全部被删时保留原值：空列表在上游表示「全部分组」。
	require.Equal(t, []int64{5, 271}, otokKeepLiveGroupIDs([]int64{5, 271}, live))
}

func TestOtokLiveChannelMonitorV2GroupIDsDropsDeleted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`SELECT id FROM groups WHERE id = ANY\(\$1\) AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(266)).AddRow(int64(127)))

	got := otokLiveChannelMonitorV2GroupIDs(context.Background(), db, []int64{127, 179, 266, 271})
	require.Equal(t, []int64{127, 266}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOtokLiveChannelMonitorV2GroupIDsKeepsOnQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`SELECT id FROM groups`).WillReturnError(errors.New("boom"))

	ids := []int64{127, 271}
	require.Equal(t, ids, otokLiveChannelMonitorV2GroupIDs(context.Background(), db, ids))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOtokLiveChannelMonitorV2GroupIDsSkipsQueryWhenEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Empty(t, otokLiveChannelMonitorV2GroupIDs(context.Background(), db, nil))
	require.Empty(t, otokLiveChannelMonitorV2GroupIDs(context.Background(), db, []int64{}))
	require.NoError(t, mock.ExpectationsWereMet())
}
