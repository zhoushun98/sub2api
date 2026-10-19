//go:build unit

package handler

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestOtokUpdateGuardBlocksInPlaceUpdates(t *testing.T) {
	g := newOtokUpdateGuard(nil)
	for _, err := range []error{
		g.PerformUpdate(context.Background()),
		g.Rollback(),
		g.RollbackToVersion(context.Background(), "0.2.16"),
	} {
		require.Error(t, err)
		require.Equal(t, "OTOK_ONLINE_UPDATE_DISABLED", infraerrors.Reason(err))
	}
	versions, err := g.ListRollbackVersions(context.Background())
	require.NoError(t, err)
	require.Empty(t, versions)
}
