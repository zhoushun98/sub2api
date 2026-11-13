package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Claude /v1/messages 流式请求「等上游响应头」阶段的保活。
//
// 上游自带的流式心跳只在两个阶段生效：等并发槽时（concurrency helper 写 ping），以及上游开始回流之后
// （gateway_upstream_response 的 stream_keepalive_interval）。从发出上游请求到收到上游响应头之间，
// 网关一个字节都不写；上游排队或卡住超过约 120 秒时，Cloudflare 直接给客户端回 524
// （nginx 记 499、rt≈125、resp_bytes=0）。2026-11-13 一天约 100 条，主要是 ZY-Claude满分。
//
// 做法：等待超过 interval 后先提交 200 + text/event-stream，再定期写出 Anthropic 标准的 ping 事件。
//   - 心跳字节不计入 Size() / Written()，handler 里「已写出内容则不再 failover」等上游判断无需改动；
//   - handler 或上游开始写真实内容时立即停止心跳，之后由上游流内 keepalive 接手，不会与事件交错；
//   - 提交 200 之后 handler 再写出 4xx/5xx JSON 错误时，就地改写为 `event: error` 帧，并标记给 ops。
const (
	otokClaudeStreamKeepaliveEnv     = "OTOK_CLAUDE_STREAM_KEEPALIVE_SECONDS"
	otokClaudeStreamKeepaliveDefault = 45 * time.Second
	otokClaudeStreamKeepaliveMin     = 5 * time.Second
	otokClaudeStreamKeepaliveMax     = 90 * time.Second

	otokSSEPingEvent = "event: ping\ndata: {\"type\": \"ping\"}\n\n"
)

// otokClaudeStreamKeepaliveIntervalFunc 是保活间隔的来源，测试里替换成毫秒级。
var otokClaudeStreamKeepaliveIntervalFunc = otokClaudeStreamKeepaliveInterval

// otokClaudeStreamKeepaliveInterval 读取保活间隔：未设置用默认值，0 或负数关闭，其余限制在 5～90 秒。
func otokClaudeStreamKeepaliveInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv(otokClaudeStreamKeepaliveEnv))
	if v == "" {
		return otokClaudeStreamKeepaliveDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return otokClaudeStreamKeepaliveDefault
	}
	if n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d < otokClaudeStreamKeepaliveMin {
		return otokClaudeStreamKeepaliveMin
	}
	if d > otokClaudeStreamKeepaliveMax {
		return otokClaudeStreamKeepaliveMax
	}
	return d
}

// otokStartClaudeKeepalive 按请求类型启动保活：非流式写空白，流式写 SSE ping。
// 返回的函数停止心跳并还原 c.Writer。
func otokStartClaudeKeepalive(c *gin.Context, stream bool) func() {
	if stream {
		return otokStartSSEKeepalive(c, otokClaudeStreamKeepaliveIntervalFunc())
	}
	return otokStartClaudeNonstreamKeepalive(c, false)
}

func otokStartSSEKeepalive(c *gin.Context, interval time.Duration) func() {
	if c == nil || c.Writer == nil || interval <= 0 {
		return func() {}
	}
	w := &otokSSEKeepaliveWriter{ResponseWriter: c.Writer, ctx: c, done: make(chan struct{})}
	c.Writer = w

	var reqDone <-chan struct{}
	if c.Request != nil {
		reqDone = c.Request.Context().Done()
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-reqDone:
				return
			case <-ticker.C:
			}
			if !w.beat() {
				return
			}
		}
	}()
	return func() {
		w.halt()
		// 外层 ops 中间件的 writer 来自对象池、请求结束后归还，必须还原。
		if c.Writer == w {
			c.Writer = w.ResponseWriter
		}
	}
}

// otokSSEKeepaliveWriter 包装 c.Writer。所有写入都在 mu 下串行，心跳不会插进别人的事件中间。
type otokSSEKeepaliveWriter struct {
	gin.ResponseWriter

	ctx       *gin.Context
	mu        sync.Mutex
	done      chan struct{}
	halted    bool
	committed bool // 心跳提交了 200 响应头
	foreign   bool // 心跳提交前已有人改响应头，心跳不再去碰响应头
	padding   int  // 心跳写出的字节数
	status    int  // 提交后 handler 请求的状态码
	marked    bool // 已向 ops 标记带内错误
}

func (w *otokSSEKeepaliveWriter) beat() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.halted {
		return false
	}
	if !w.ResponseWriter.Written() {
		if w.foreign {
			// handler 正在组织自己的响应头，等它写出后再继续发心跳。
			return true
		}
		h := w.ResponseWriter.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.ResponseWriter.WriteHeader(http.StatusOK)
		w.committed = true
	}
	n, err := w.ResponseWriter.WriteString(otokSSEPingEvent)
	w.padding += n
	if err != nil {
		w.haltLocked()
		return false
	}
	w.ResponseWriter.Flush()
	return true
}

func (w *otokSSEKeepaliveWriter) halt() {
	w.mu.Lock()
	w.haltLocked()
	w.mu.Unlock()
}

func (w *otokSSEKeepaliveWriter) haltLocked() {
	if !w.halted {
		w.halted = true
		close(w.done)
	}
}

func (w *otokSSEKeepaliveWriter) Header() http.Header {
	w.mu.Lock()
	if !w.ResponseWriter.Written() {
		w.foreign = true
	}
	w.mu.Unlock()
	return w.ResponseWriter.Header()
}

func (w *otokSSEKeepaliveWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.haltLocked()
	if w.committed && code > 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *otokSSEKeepaliveWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.haltLocked()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *otokSSEKeepaliveWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeLocked(data)
}

func (w *otokSSEKeepaliveWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeLocked([]byte(s))
}

func (w *otokSSEKeepaliveWriter) writeLocked(data []byte) (int, error) {
	// 等并发槽时 concurrency helper 写的 ping 是完整事件，放行且不停心跳：之后还要继续等上游响应头。
	if !w.halted && string(data) == string(SSEPingFormatClaude) {
		return w.ResponseWriter.Write(data)
	}
	w.haltLocked()
	if w.committed && w.status >= http.StatusBadRequest {
		if _, err := w.ResponseWriter.WriteString(w.inBandErrorEvent(data)); err != nil {
			return 0, err
		}
		w.ResponseWriter.Flush()
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

// inBandErrorEvent 把 handler 写出的 JSON 错误改写成 Anthropic 标准的 SSE error 事件，并标记给 ops。
func (w *otokSSEKeepaliveWriter) inBandErrorEvent(body []byte) string {
	payload := otokAnthropicErrorPayload(body)
	if !w.marked {
		w.marked = true
		errType := strings.TrimSpace(gjson.GetBytes(payload, "error.type").String())
		if errType == "" {
			errType = "upstream_error"
		}
		service.MarkOpsStreamErrorValue(w.ctx, service.OpsStreamError{
			ErrType:         errType,
			Code:            gjson.GetBytes(payload, "error.code").String(),
			Message:         gjson.GetBytes(payload, "error.message").String(),
			IntendedStatus:  w.status,
			CountTowardsSLA: true,
		})
	}
	return "event: error\ndata: " + string(payload) + "\n\n"
}

// otokAnthropicErrorPayload 把任意错误正文整理成单行的 {"type":"error","error":{...}}。
func otokAnthropicErrorPayload(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if json.Valid(trimmed) {
		if gjson.GetBytes(trimmed, "type").String() == "error" && gjson.GetBytes(trimmed, "error").IsObject() {
			var buf bytes.Buffer
			if json.Compact(&buf, trimmed) == nil {
				return buf.Bytes()
			}
		}
		if errObj := gjson.GetBytes(trimmed, "error"); errObj.IsObject() {
			var buf bytes.Buffer
			if json.Compact(&buf, []byte(errObj.Raw)) == nil {
				return []byte(`{"type":"error","error":` + buf.String() + `}`)
			}
		}
	}
	message := string(trimmed)
	if len(message) > 512 {
		message = message[:512]
	}
	if message == "" {
		message = "Upstream request failed"
	}
	out, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": "api_error", "message": message},
	})
	return out
}

func (w *otokSSEKeepaliveWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}

func (w *otokSSEKeepaliveWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed && w.status > 0 {
		return w.status
	}
	return w.ResponseWriter.Status()
}

// Size 不计心跳字节；只写过心跳时视为尚未写出（-1）。
func (w *otokSSEKeepaliveWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	size := w.ResponseWriter.Size()
	if size < 0 || w.padding == 0 {
		return size
	}
	if real := size - w.padding; real > 0 {
		return real
	}
	return -1
}

func (w *otokSSEKeepaliveWriter) Written() bool {
	return w.Size() != -1
}
