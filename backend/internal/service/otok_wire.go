package service

import (
	"database/sql"
	"os"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/wire"
)

// otok: fork 定制服务的 Wire 注册，在 cmd/server/wire.go 中引用。
var OtokProviderSet = wire.NewSet(
	ProvideOtokUSDTService,
	ProvideOtokPelicanService,
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

// otokPelicanDisabled 允许通过 OTOK_PELICAN_DISABLED=1 关闭鹈鹕测试调度（例如只读副本）。
func otokPelicanDisabled() bool {
	v := strings.TrimSpace(os.Getenv("OTOK_PELICAN_DISABLED"))
	return v == "1" || strings.EqualFold(v, "true")
}

// ProvideOtokPelicanService 创建「降智检测」（鹈鹕测试）服务并启动调度。
func ProvideOtokPelicanService(
	settings *SettingService,
	db *sql.DB,
	cfg *config.Config,
	userRepo UserRepository,
	groupRepo GroupRepository,
	rateRepo UserGroupRateRepository,
	apiKeys *APIKeyService,
	billingCache *BillingCacheService,
	lockCache LeaderLockCache,
) *OtokPelicanService {
	svc := NewOtokPelicanService(settings, db, cfg, userRepo, groupRepo, rateRepo, apiKeys, billingCache, otokPelicanDisabled())
	svc.SetLeaderLock(lockCache)
	svc.Start()
	return svc
}
