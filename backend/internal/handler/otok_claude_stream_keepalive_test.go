//go:build unit

package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ---- writer ----

func waitOtokSSEKeepaliveCommitted(t *testing.T, c *gin.Context) *otokSSEKeepaliveWriter {
	t.Helper()
	w, ok := c.Writer.(*otokSSEKeepaliveWriter)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.committed
	}, time.Second, time.Millisecond)
	return w
}

func otokSSEPadding(w *otokSSEKeepaliveWriter) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.padding
}

func TestOtokSSEKeepalive_SlowStreamGetsPingsThenEvents(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	original := c.Writer

	stop := otokStartSSEKeepalive(c, 5*time.Millisecond)
	w := waitOtokSSEKeepaliveCommitted(t, c)
	// 只写过心跳时，handler 看到的仍是「尚未写出」，failover 判断不受影响。
	require.Equal(t, -1, c.Writer.Size())
	require.False(t, c.Writer.Written())

	// 模拟 handleStreamingResponse：设响应头后逐行写事件。
	c.Header("Content-Type", "text/event-stream")
	_, _ = c.Writer.WriteString("event: message_start\n")
	padding := otokSSEPadding(w)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, padding, otokSSEPadding(w), "开始写真实事件后不应再插入心跳")
	_, _ = c.Writer.WriteString("data: {\"type\":\"message_start\"}\n\n")
	require.Greater(t, c.Writer.Size(), 0)
	stop()

	require.Same(t, original, c.Writer)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, otokSSEPingEvent), body)
	require.True(t, strings.HasSuffix(body, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"), body)
}

func TestOtokSSEKeepalive_FastResponsesUntouched(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		c, rec := newOtokKeepaliveTestContext()
		stop := otokStartSSEKeepalive(c, time.Second)
		c.JSON(status, gin.H{"ok": status})
		stop()

		require.Equal(t, status, rec.Code)
		require.Equal(t, byte('{'), rec.Body.Bytes()[0])
		_, marked := c.Get(service.OpsStreamErrorKey)
		require.False(t, marked)
	}
}

func TestOtokSSEKeepalive_LateErrorBecomesErrorEvent(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	stop := otokStartSSEKeepalive(c, 5*time.Millisecond)
	waitOtokSSEKeepaliveCommitted(t, c)

	c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "upstream_error", "message": "Upstream request failed"}})
	require.Equal(t, http.StatusBadGateway, c.Writer.Status())
	stop()

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, otokSSEPingEvent), body)
	idx := strings.Index(body, "event: error\ndata: ")
	require.GreaterOrEqual(t, idx, 0, body)
	data := strings.TrimSuffix(body[idx+len("event: error\ndata: "):], "\n\n")
	require.Equal(t, "error", gjson.Get(data, "type").String())
	require.Equal(t, "upstream_error", gjson.Get(data, "error.type").String())
	require.Equal(t, "Upstream request failed", gjson.Get(data, "error.message").String())

	v, ok := c.Get(service.OpsStreamErrorKey)
	require.True(t, ok)
	streamErr, ok := v.(service.OpsStreamError)
	require.True(t, ok)
	require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
	require.Equal(t, "upstream_error", streamErr.ErrType)
	require.Equal(t, "Upstream request failed", streamErr.Message)
	require.False(t, streamErr.NonStream)
	require.True(t, streamErr.CountTowardsSLA)
}

// 等账号并发槽时 concurrency helper 先写了 ping：不停心跳，之后继续保活等上游响应头。
func TestOtokSSEKeepalive_ConcurrencyPingKeepsHeartbeat(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	stop := otokStartSSEKeepalive(c, 3*time.Millisecond)
	defer stop()
	w := c.Writer.(*otokSSEKeepaliveWriter)

	c.Header("Content-Type", "text/event-stream")
	n, err := c.Writer.WriteString(string(SSEPingFormatClaude))
	require.NoError(t, err)
	require.Equal(t, len(SSEPingFormatClaude), n)
	require.Equal(t, n, c.Writer.Size(), "concurrency ping 照旧计入 Size")

	require.Eventually(t, func() bool { return otokSSEPadding(w) >= 2*len(otokSSEPingEvent) }, time.Second, time.Millisecond)
	require.Equal(t, n, c.Writer.Size())
	require.False(t, w.committed, "响应头是 concurrency helper 提交的")
	require.True(t, strings.HasPrefix(rec.Body.String(), string(SSEPingFormatClaude)+otokSSEPingEvent))
}

// handler 先动了响应头（例如上游很快回了流）：心跳不去碰响应头，也不提交。
func TestOtokSSEKeepalive_ForeignHeaderPreventsCommit(t *testing.T) {
	c, rec := newOtokKeepaliveTestContext()
	stop := otokStartSSEKeepalive(c, 2*time.Millisecond)
	defer stop()
	w := c.Writer.(*otokSSEKeepaliveWriter)

	c.Header("Content-Type", "text/event-stream")
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 0, otokSSEPadding(w))
	require.False(t, w.committed)
	require.Equal(t, 0, rec.Body.Len())
}

func TestOtokAnthropicErrorPayload(t *testing.T) {
	cases := []struct {
		body    string
		errType string
		message string
	}{
		{"{\n  \"type\": \"error\",\n  \"error\": {\"type\": \"not_found_error\", \"message\": \"x\"}\n}", "not_found_error", "x"},
		{`{"error":{"type":"invalid_request_error","message":"bad"}}`, "invalid_request_error", "bad"},
		{`<html>bad gateway</html>`, "api_error", "<html>bad gateway</html>"},
		{``, "api_error", "Upstream request failed"},
	}
	for _, tc := range cases {
		out := string(otokAnthropicErrorPayload([]byte(tc.body)))
		require.NotContains(t, out, "\n", tc.body)
		require.Equal(t, "error", gjson.Get(out, "type").String(), tc.body)
		require.Equal(t, tc.errType, gjson.Get(out, "error.type").String(), tc.body)
		require.Equal(t, tc.message, gjson.Get(out, "error.message").String(), tc.body)
	}
}

func TestOtokClaudeStreamKeepaliveInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":    otokClaudeStreamKeepaliveDefault,
		"abc": otokClaudeStreamKeepaliveDefault,
		"0":   0,
		"-1":  0,
		"1":   otokClaudeStreamKeepaliveMin,
		"30":  30 * time.Second,
		"600": otokClaudeStreamKeepaliveMax,
	}
	for v, want := range cases {
		t.Setenv(otokClaudeStreamKeepaliveEnv, v)
		require.Equal(t, want, otokClaudeStreamKeepaliveInterval(), "env=%q", v)
	}
}

// ---- 经真实 Messages handler 与 HTTP 连接的端到端验证 ----

const otokKeepaliveStreamBody = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func enableOtokStreamKeepalive(t *testing.T, interval time.Duration) {
	t.Helper()
	otokClaudeStreamKeepaliveIntervalFunc = func() time.Duration { return interval }
}

func requireOtokStreamDone(t *testing.T, body string) {
	t.Helper()
	require.Contains(t, body, `"text":"done"`, body)
	require.Contains(t, body, `"type":"message_stop"`, body)
}

// 上游迟迟不回响应头：200 和 ping 先到（Cloudflare 不会断开），随后是完整的事件流。
func TestOtokMessagesStreamKeepalive_SlowUpstreamGetsEarlyPing(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 0, map[int64]otokSlowUpstreamReply{
		9601: {delay: 400 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveStreamBody, contentType: "text/event-stream"},
	})
	enableOtokStreamKeepalive(t, 40*time.Millisecond)

	start := time.Now()
	headerAt, status, body := f.post(t, true)

	require.Less(t, headerAt, 300*time.Millisecond, "响应头应在上游回应前由心跳提交")
	require.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond)
	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(string(body), otokSSEPingEvent), string(body))
	requireOtokStreamDone(t, string(body))
	require.Equal(t, 1, f.upstream.callCount(9601))
	require.Empty(t, opsErrorLogQueueEntries(t))
}

// 上游在保活间隔内回应：与原来完全一致，没有心跳。
func TestOtokMessagesStreamKeepalive_FastUpstreamUnchanged(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 0, map[int64]otokSlowUpstreamReply{
		9601: {delay: 10 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveStreamBody, contentType: "text/event-stream"},
	})
	enableOtokStreamKeepalive(t, 300*time.Millisecond)

	_, status, body := f.post(t, true)

	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(body), otokSSEPingEvent)
	requireOtokStreamDone(t, string(body))
}

// 心跳已发出后第一个账号返回 5xx：仍切换到下一个账号，客户端拿到完整的流。
func TestOtokMessagesStreamKeepalive_FailoverAfterHeartbeat(t *testing.T) {
	f := newOtokKeepaliveFixture(t, 0, map[int64]otokSlowUpstreamReply{
		9601: {delay: 150 * time.Millisecond, status: http.StatusServiceUnavailable, body: `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`},
		9611: {delay: 150 * time.Millisecond, status: http.StatusOK, body: otokKeepaliveStreamBody, contentType: "text/event-stream"},
	})
	enableOtokStreamKeepalive(t, 30*time.Millisecond)

	headerAt, status, body := f.post(t, true)

	require.Less(t, headerAt, 150*time.Millisecond)
	require.Equal(t, http.StatusOK, status)
	require.True(t, strings.HasPrefix(string(body), otokSSEPingEvent), string(body))
	require.NotContains(t, string(body), "event: error")
	requireOtokStreamDone(t, string(body))
	require.Equal(t, 1, f.upstream.callCount(9601))
	require.Equal(t, 1, f.upstream.callCount(9611))
}

// 心跳已发出后上游返回不可切换的错误：线上状态码只能是 200，错误改为 SSE error 事件，ops 错误日志与关闭保活时一致。
func TestOtokMessagesStreamKeepalive_LateUpstreamErrorMatchesBaseline(t *testing.T) {
	baseline := newOtokKeepaliveFixture(t, 0, otokKeepaliveLateErrorReply)
	_, baseStatus, baseBody := baseline.post(t, true)
	baseEntries := opsErrorLogQueueEntries(t)
	require.GreaterOrEqual(t, baseStatus, http.StatusBadRequest)
	require.Len(t, baseEntries, 1)

	f := newOtokKeepaliveFixture(t, 0, otokKeepaliveLateErrorReply)
	enableOtokStreamKeepalive(t, 30*time.Millisecond)
	_, status, body := f.post(t, true)
	entries := opsErrorLogQueueEntries(t)

	require.Equal(t, http.StatusOK, status)
	text := string(body)
	require.True(t, strings.HasPrefix(text, otokSSEPingEvent), text)
	idx := strings.Index(text, "event: error\ndata: ")
	require.GreaterOrEqual(t, idx, 0, text)
	data := strings.TrimSuffix(text[idx+len("event: error\ndata: "):], "\n\n")
	require.Equal(t, gjson.GetBytes(baseBody, "error.type").String(), gjson.Get(data, "error.type").String())
	require.Equal(t, gjson.GetBytes(baseBody, "error.message").String(), gjson.Get(data, "error.message").String())

	require.Len(t, entries, 1)
	require.Equal(t, baseEntries[0].StatusCode, entries[0].StatusCode)
	require.Equal(t, baseEntries[0].ErrorType, entries[0].ErrorType)
	require.Equal(t, baseEntries[0].ErrorMessage, entries[0].ErrorMessage)
	require.True(t, entries[0].Stream)
}
