package handler

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// otok: 定制版禁用后台「在线更新 / 回滚」。上游的在线更新会从 Wei-Shaw/sub2api 下载
// 官方二进制覆盖容器内的定制版；定制版统一通过拉取新镜像升级。版本检查保留，
// 以便后台继续提示上游发布了新版本（同步提醒）。

func otokOnlineUpdateDisabledError() error {
	return infraerrors.Forbidden("OTOK_ONLINE_UPDATE_DISABLED",
		"online update is disabled for this custom build; upgrade by pulling the new image")
}

type otokUpdateGuard struct {
	inner *service.UpdateService
}

func newOtokUpdateGuard(inner *service.UpdateService) *otokUpdateGuard {
	return &otokUpdateGuard{inner: inner}
}

func (g *otokUpdateGuard) CheckUpdate(ctx context.Context, force bool) (*service.UpdateInfo, error) {
	return g.inner.CheckUpdate(ctx, force)
}

func (g *otokUpdateGuard) PerformUpdate(context.Context) error {
	return otokOnlineUpdateDisabledError()
}

func (g *otokUpdateGuard) Rollback() error { return otokOnlineUpdateDisabledError() }

func (g *otokUpdateGuard) ListRollbackVersions(context.Context) ([]service.RollbackVersion, error) {
	return []service.RollbackVersion{}, nil
}

func (g *otokUpdateGuard) RollbackToVersion(context.Context, string) error {
	return otokOnlineUpdateDisabledError()
}
