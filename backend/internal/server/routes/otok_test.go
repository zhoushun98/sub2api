//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOtokUserRateLimitIsPerUserAcrossIPs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	limiter := middleware.NewRateLimiter(redis.NewClient(&redis.Options{Addr: mr.Addr()}))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if uid := c.GetHeader("X-Test-User"); uid == "7" {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7})
		} else if uid == "8" {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 8})
		}
		c.Next()
	})
	router.POST("/tx", otokUserRateLimit(limiter, "otok-test", 1, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(user, ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/tx", nil)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("X-Test-User", user)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	require.Equal(t, http.StatusOK, do("7", "10.0.0.1"))
	// 同一用户换 IP 仍被限流
	require.Equal(t, http.StatusTooManyRequests, do("7", "10.0.0.2"))
	// 其他用户不受影响
	require.Equal(t, http.StatusOK, do("8", "10.0.0.2"))
}
