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
	if h == nil {
		return
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
