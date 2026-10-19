package service

import (
	"database/sql"
	"os"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/google/wire"
)

// otok: fork 定制服务的 Wire 注册，在 cmd/server/wire.go 中引用。
var OtokProviderSet = wire.NewSet(
	ProvideOtokUSDTService,
)

// otokUSDTWatcherDisabled 允许通过 OTOK_USDT_WATCHER_DISABLED=1 关闭后台监听（例如只读副本）。
func otokUSDTWatcherDisabled() bool {
	v := strings.TrimSpace(os.Getenv("OTOK_USDT_WATCHER_DISABLED"))
	return v == "1" || strings.EqualFold(v, "true")
}

// ProvideOtokUSDTService 创建 USDT 服务并启动链上监听。
func ProvideOtokUSDTService(paymentSvc *PaymentService, entClient *dbent.Client, lockCache LeaderLockCache, db *sql.DB) *OtokUSDTService {
	interval := otokUSDTWatchInterval
	if otokUSDTWatcherDisabled() {
		interval = 0
	}
	svc := NewOtokUSDTService(paymentSvc, entClient, interval)
	svc.SetLeaderLock(lockCache, db)
	svc.Start()
	return svc
}
