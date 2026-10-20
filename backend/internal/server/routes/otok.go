package routes

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	ratelimit "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// otok: fork 定制接口路由。中间件与上游支付路由（routes/payment.go）保持一致。

const (
	otokUSDTSubmitTxRateLimit       = 10
	otokUSDTSubmitTxRateLimitWindow = time.Minute
)

// otokUserRateLimit 按登录用户限流（IP 限流之外再加一层，换 IP 也绕不过去）。
// 每次提交都会查询链上节点 / TronGrid，消耗的是站点自己的 API 配额。Redis 异常时放行。
func otokUserRateLimit(limiter *ratelimit.RateLimiter, key string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			c.Next()
			return
		}
		res, err := limiter.Allow(c.Request.Context(), key+":user:"+strconv.FormatInt(subject.UserID, 10), limit, window)
		if err != nil {
			slog.Warn("[OtokUSDT] user rate limit check failed, allowing request", "key", key, "error", err)
			c.Next()
			return
		}
		if !res.Allowed {
			if res.RetryAfter > 0 {
				c.Header("Retry-After", strconv.FormatInt(int64((res.RetryAfter+time.Second-1)/time.Second), 10))
			}
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "Too many requests, please try again later",
			})
			return
		}
		c.Next()
	}
}

// RegisterOtokRoutes 注册 fork 定制的用户端与管理端接口。
func RegisterOtokRoutes(
	v1 *gin.RouterGroup,
	h *handler.OtokHandlers,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
	redisClient *redis.Client,
) {
	registerOtokContactRoutes(v1, jwtAuth, adminAuth, auditLog, settingService, panelRateLimiter)
	if h == nil {
		return
	}
	if h.Pelican != nil {
		registerOtokPelicanRoutes(v1, h.Pelican, jwtAuth, adminAuth, auditLog, settingService, panelRateLimiter)
	}
	if h.USDT != nil {
		submitLimiter := ratelimit.NewRateLimiter(redisClient)
		user := v1.Group("/payment/otok/usdt")
		user.Use(gin.HandlerFunc(jwtAuth))
		user.Use(middleware.BackendModeUserGuard(settingService))
		user.Use(panelRateLimiter.Global())
		{
			user.GET("/quote", h.USDT.GetQuote)
			user.GET("/orders/:id", h.USDT.GetOrder)
			user.POST("/orders/:id/tx",
				submitLimiter.Limit("otok-usdt-submit-tx", otokUSDTSubmitTxRateLimit, otokUSDTSubmitTxRateLimitWindow),
				otokUserRateLimit(submitLimiter, "otok-usdt-submit-tx", otokUSDTSubmitTxRateLimit, otokUSDTSubmitTxRateLimitWindow),
				h.USDT.SubmitTx)
		}
	}
	if h.AdminUSDT != nil {
		adminGroup := v1.Group("/admin/payment/otok/usdt")
		adminGroup.Use(gin.HandlerFunc(adminAuth))
		adminGroup.Use(gin.HandlerFunc(auditLog))
		adminGroup.Use(middleware.AdminComplianceGuard(settingService))
		{
			adminGroup.GET("/health", h.AdminUSDT.Health)
			adminGroup.GET("/transfers", h.AdminUSDT.ListTransfers)
			adminGroup.POST("/transfers/:id/ignore", h.AdminUSDT.IgnoreTransfer)
			adminGroup.GET("/orders/:id", h.AdminUSDT.GetOrder)
			adminGroup.POST("/orders/:id/bind-tx", h.AdminUSDT.BindTx)
		}
	}
}

// otokContactMaxBodyBytes 容纳两张 256KB 二维码的 base64（约 700KB）再留余量。
const otokContactMaxBodyBytes = 2 << 20

// registerOtokContactRoutes 注册「联系我们」接口。只依赖 settings 表，就地构造，不经 wire。
func registerOtokContactRoutes(
	v1 *gin.RouterGroup,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if settingService == nil {
		return
	}
	contact := handler.NewOtokContactHandler(service.NewOtokContactService(settingService))

	user := v1.Group("/otok/contact")
	user.Use(gin.HandlerFunc(jwtAuth))
	user.Use(middleware.BackendModeUserGuard(settingService))
	user.Use(panelRateLimiter.Global())
	{
		user.GET("", contact.GetSummary)
		user.GET("/qrcodes", contact.GetQRCodes)
	}

	adminGroup := v1.Group("/admin/otok/contact")
	adminGroup.Use(gin.HandlerFunc(adminAuth))
	adminGroup.Use(gin.HandlerFunc(auditLog))
	adminGroup.Use(middleware.AdminComplianceGuard(settingService))
	{
		adminGroup.GET("", contact.AdminGet)
		adminGroup.PUT("", middleware.RequestBodyLimit(otokContactMaxBodyBytes), contact.AdminUpdate)
	}
}

// otokPelicanMaxBodyBytes 容纳 8000 字 prompt 与分组列表。
const otokPelicanMaxBodyBytes = 128 << 10

// registerOtokPelicanRoutes 注册「降智检测」（鹈鹕测试）接口，中间件与「联系我们」一致。
func registerOtokPelicanRoutes(
	v1 *gin.RouterGroup,
	h *handler.OtokPelicanHandler,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	user := v1.Group("/otok/pelican")
	user.Use(gin.HandlerFunc(jwtAuth))
	user.Use(middleware.BackendModeUserGuard(settingService))
	user.Use(panelRateLimiter.Global())
	{
		user.GET("/groups", h.Groups)
		user.GET("/groups/:id/rounds", h.Rounds)
		user.GET("/runs/:id", h.Run)
	}

	adminGroup := v1.Group("/admin/otok/pelican")
	adminGroup.Use(gin.HandlerFunc(adminAuth))
	adminGroup.Use(gin.HandlerFunc(auditLog))
	adminGroup.Use(middleware.AdminComplianceGuard(settingService))
	{
		adminGroup.GET("/config", h.AdminGetConfig)
		adminGroup.PUT("/config", middleware.RequestBodyLimit(otokPelicanMaxBodyBytes), h.AdminUpdateConfig)
		adminGroup.GET("/status", h.AdminStatus)
		adminGroup.POST("/run-now", middleware.RequestBodyLimit(otokPelicanMaxBodyBytes), h.AdminRunNow)
		adminGroup.GET("/groups/:id/rounds", h.AdminRounds)
		adminGroup.GET("/runs/:id", h.AdminRun)
	}
}
