//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ---- writer ----

func newOtokKeepaliveTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, rec
}

func waitOtokKeepaliveCommitted(t *testing.T, c *gin.Context) *otokJSONKeepaliveWriter {
	t.Helper()
	w, ok := c.Writer.(*otokJSONKeepaliveWriter)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.committed
	}, time.Second, time.Millisecond)
	return w
}

func TestOtokJSONKeepalive_SlowSuccessStaysValidJSON(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	original := c.Writer

	stop := otokStartJSONKeepalive(c, 5*time.Millisecond)
	waitOtokKeepaliveCommitted(t, c)
	// 只写过心跳时，handler 看到的仍是「尚未写出」，failover 判断不受影响。
	require.Equal(t, -1, c.Writer.Size())
	require.False(t, c.Writer.Written())

	c.Data(http.StatusOK, "application/json", []byte(`{"type":"message","content":[{"type":"text","text":"hi"}]}`))
	require.Greater(t, c.Writer.Size(), 0)
	stop()

	require.Same(t, original, c.Writer)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	require.True(t, rec.Flushed)
	require.True(t, strings.HasPrefix(rec.Body.String(), " \n"))
	require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
	require.Equal(t, "hi", gjson.Get(rec.Body.String(), "content.0.text").String())
}

func TestOtokJSONKeepalive_FastResponsesUntouched(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		c, rec := newOtokKeepaliveTestContext()
		stop := otokStartJSONKeepalive(c, time.Second)
		c.JSON(status, gin.H{"ok": status})
		stop()

		require.Equal(t, status, rec.Code)
		require.False(t, strings.HasPrefix(rec.Body.String(), " "))
		_, marked := c.Get(service.OpsStreamErrorKey)
		require.False(t, marked)
	}
}

func TestOtokJSONKeepalive_LateErrorMarksOpsInBandError(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	stop := otokStartJSONKeepalive(c, 5*time.Millisecond)
	waitOtokKeepaliveCommitted(t, c)

	c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "upstream_error", "message": "Upstream request failed"}})
	require.Equal(t, http.StatusBadGateway, c.Writer.Status())
	stop()

	// 线上状态码已固化为 200，正文仍是合法的错误 JSON。
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, json.Valid(rec.Body.Bytes()), rec.Body.String())
	require.Equal(t, "Upstream request failed", gjson.Get(rec.Body.String(), "error.message").String())

	v, ok := c.Get(service.OpsStreamErrorKey)
	require.True(t, ok)
	streamErr, ok := v.(service.OpsStreamError)
	require.True(t, ok)
	require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
	require.Equal(t, "upstream_error", streamErr.ErrType)
	require.Equal(t, "Upstream request failed", streamErr.Message)
	require.True(t, streamErr.NonStream)
	require.True(t, streamErr.CountTowardsSLA)
}

func TestOtokJSONKeepalive_HeaderAccessStopsHeartbeat(t *testing.T) {
	c, _ := newOtokKeepaliveTestContext()
	stop := otokStartJSONKeepalive(c, 2*time.Millisecond)
	defer stop()
	w := waitOtokKeepaliveCommitted(t, c)

	c.Writer.Header().Set("X-Test", "1")
	w.mu.Lock()
	padding := w.padding
	w.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Equal(t, padding, w.padding)
}

func TestOtokClaudeNonstreamKeepalive_StreamIsNoop(t *testing.T) {
	c, _ := newOtokKeepaliveTestContext()
	original := c.Writer
	stop := otokStartClaudeNonstreamKeepalive(c, true)
	require.Same(t, original, c.Writer)
	stop()
}

func TestOtokClaudeNonstreamKeepaliveInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":    otokClaudeNonstreamKeepaliveDefault,
		"abc": otokClaudeNonstreamKeepaliveDefault,
		"0":   0,
		"-1":  0,
		"1":   otokClaudeNonstreamKeepaliveMin,
		"30":  30 * time.Second,
		"600": otokClaudeNonstreamKeepaliveMax,
	}
	for v, want := range cases {
		t.Setenv(otokClaudeNonstreamKeepaliveEnv, v)
		require.Equal(t, want, otokClaudeNonstreamKeepaliveInterval(), "env=%q", v)
	}
}

// ---- 经真实 Messages handler 与 HTTP 连接的端到端验证 ----

type otokSlowUpstreamReply struct {
	delay  time.Duration
	status int
	body   string
}

// otokSlowAnthropicUpstream 按账号返回延迟响应，模拟生成很久的非流式上游。
type otokSlowAnthropicUpstream struct {
	replies map[int64]otokSlowUpstreamReply
	mu      sync.Mutex
	calls   map[int64]int
}

func (u *otokSlowAnthropicUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.calls[accountID]++
	reply := u.replies[accountID]
	u.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	select {
	case <-time.After(reply.delay):
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: reply.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(reply.body)),
		Request:    req,
	}, nil
}

func (u *otokSlowAnthropicUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *otokSlowAnthropicUpstream) callCount(accountID int64) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[accountID]
}

type otokKeepaliveAccountRepoStub struct {
	service.AccountRepository
}

func (otokKeepaliveAccountRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}
func (otokKeepaliveAccountRepoStub) SetOverloaded(context.Context, int64, time.Time) error { return nil }
func (otokKeepaliveAccountRepoStub) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

const otokKeepaliveSuccessBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`

type otokKeepaliveFixture struct {
	server   *httptest.Server
	upstream *otokSlowAnthropicUpstream
}

func newOtokKeepaliveFixture(t *testing.T, interval time.Duration, replies map[int64]otokSlowUpstreamReply) *otokKeepaliveFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prev := otokClaudeNonstreamKeepaliveIntervalFunc
	otokClaudeNonstreamKeepaliveIntervalFunc = func() time.Duration { return interval }
	t.Cleanup(func() { otokClaudeNonstreamKeepaliveIntervalFunc = prev })

	groupID := int64(9600)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive}
	var accounts []*service.Account
	for id := range replies {
		accounts = append(accounts, &service.Account{
			ID:            id,
			Name:          "otok-keepalive",
			Platform:      service.PlatformAnthropic,
			Type:          service.AccountTypeAPIKey,
			Credentials:   map[string]any{"api_key": "test-key"},
			Status:        service.StatusActive,
			Schedulable:   true,
			Concurrency:   1,
			Priority:      int(id),
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		})
	}
	upstream := &otokSlowAnthropicUpstream{replies: replies, calls: map[int64]int{}}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	schedulerSnapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil)
	rateLimitSvc := service.NewRateLimitService(otokKeepaliveAccountRepoStub{}, nil, cfg, nil, nil)
	gwSvc := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil,
		cfg, schedulerSnapshot, nil, nil, rateLimitSvc, nil, nil,
		upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	h := &GatewayHandler{
		gatewayService:      gwSvc,
		billingCacheService: billingCacheSvc,
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
		maxAccountSwitches:  3,
	}

	setupOpsErrorLogTestQueue(t, 8)
	apiKey := &service.APIKey{
		ID: 9602, UserID: 9603, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 9603, Concurrency: 10, Balance: 100},
	}
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)))
	router.POST("/v1/messages", func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
		h.Messages(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &otokKeepaliveFixture{server: server, upstream: upstream}
}

// post 发出非流式请求，返回收到响应头的耗时、状态码和完整正文。
func (f *otokKeepaliveFixture) post(t *testing.T, stream bool) (time.Duration, int, []byte) {
	t.Helper()
	body := `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	if stream {
		body = `{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/messages", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := f.server.Client().Do(req)
	require.NoError(t, err)
	headerAt := time.Since(start)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return headerAt, resp.StatusCode, data
}

func opsErrorLogQueueEntries(t *testing.T) []*service.OpsInsertErrorLogInput {
	t.Helper()
	var entries []*service.OpsInsertErrorLogInput
	for {
		select {
		case job := <-opsErrorLogQueue:
			entries = append(entries, job.entry)
		default:
			return entries
		}
	}
}

// 上游比保活间隔慢得多：响应头和心跳先到（Cloudflare 不会因无响应断开），最终正文仍是可解析的完整 JSON。
func TestOtokMessagesNonstreamKeepalive_SlowUpstreamGetsEarlyBytes(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 40*time.Millisecond, map[int64]otokSlowUpstreamReply{
		9601: {delay: 400 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveSuccessBody},
	})

	start := time.Now()
	headerAt, status, body := f.post(t, false)

	require.Less(t, headerAt, 300*time.Millisecond, "响应头应在上游完成前由心跳提交")
	require.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond)
	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(string(body), " \n"), "应带心跳空白")
	require.True(t, json.Valid(body), string(body))
	require.Equal(t, "done", gjson.GetBytes(body, "content.0.text").String())
	require.Equal(t, 1, f.upstream.callCount(9601))
	require.Empty(t, opsErrorLogQueueEntries(t))
}

// 上游在保活间隔内完成：行为与原来完全一致，没有心跳空白。
func TestOtokMessagesNonstreamKeepalive_FastUpstreamUnchanged(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 300*time.Millisecond, map[int64]otokSlowUpstreamReply{
		9601: {delay: 10 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveSuccessBody},
	})

	_, status, body := f.post(t, false)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, byte('{'), body[0])
	require.Equal(t, "done", gjson.GetBytes(body, "content.0.text").String())
}

// 心跳已发出后第一个账号返回 5xx：仍按原逻辑切换到下一个账号，客户端拿到成功结果。
func TestOtokMessagesNonstreamKeepalive_FailoverAfterHeartbeat(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 30*time.Millisecond, map[int64]otokSlowUpstreamReply{
		9601: {delay: 150 * time.Millisecond, status: http.StatusServiceUnavailable, body: `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`},
		9611: {delay: 150 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveSuccessBody},
	})

	headerAt, status, body := f.post(t, false)

	require.Less(t, headerAt, 150*time.Millisecond)
	require.Equal(t, http.StatusOK, status)
	require.True(t, json.Valid(body), string(body))
	require.Equal(t, "done", gjson.GetBytes(body, "content.0.text").String())
	require.Equal(t, 1, f.upstream.callCount(9601))
	require.Equal(t, 1, f.upstream.callCount(9611))
}

var otokKeepaliveLateErrorReply = map[int64]otokSlowUpstreamReply{
	9601: {delay: 150 * time.Millisecond, status: http.StatusNotFound, body: `{"type":"error","error":{"type":"not_found_error","message":"model not found"}}`},
}

// 心跳已发出后上游返回不可切换的错误：线上状态码只能是 200，但正文和 ops 错误日志与关闭保活时一致。
func TestOtokMessagesNonstreamKeepalive_LateUpstreamErrorMatchesBaseline(t *testing.T) {
	baseline := newOtokKeepaliveFixture(t, 0, otokKeepaliveLateErrorReply)
	_, baseStatus, baseBody := baseline.post(t, false)
	baseEntries := opsErrorLogQueueEntries(t)
	require.GreaterOrEqual(t, baseStatus, http.StatusBadRequest)
	require.Len(t, baseEntries, 1)

	f := newOtokKeepaliveFixture(t, 30*time.Millisecond, otokKeepaliveLateErrorReply)
	_, status, body := f.post(t, false)
	entries := opsErrorLogQueueEntries(t)

	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(string(body), " \n"))
	require.True(t, json.Valid(body), string(body))
	require.Equal(t, string(baseBody), strings.TrimLeft(string(body), " \n"))

	require.Len(t, entries, 1)
	require.Equal(t, baseStatus, entries[0].StatusCode)
	require.Equal(t, baseEntries[0].StatusCode, entries[0].StatusCode)
	require.Equal(t, baseEntries[0].ErrorType, entries[0].ErrorType)
	require.Equal(t, baseEntries[0].ErrorMessage, entries[0].ErrorMessage)
	require.False(t, entries[0].Stream)
}

// 流式请求不走这里的保活：同样的上游错误照常以原状态码返回。
func TestOtokMessagesNonstreamKeepalive_StreamRequestUntouched(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 20*time.Millisecond, otokKeepaliveLateErrorReply)

	_, status, body := f.post(t, true)

	require.GreaterOrEqual(t, status, http.StatusBadRequest)
	require.False(t, strings.HasPrefix(string(body), " "))
}
