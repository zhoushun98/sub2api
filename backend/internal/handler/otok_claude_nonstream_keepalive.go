package handler

import (
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

// Claude /v1/messages 非流式请求保活。
//
// otokapi.com 前面的 Cloudflare 在源站约 100～125 秒没有任何响应时会断开连接（客户端收到 524），
// 而非流式请求要等上游生成完毕才有响应，长输出（例如 16k token 的 Opus）经常超过这个时间。
// 断开后网关不计费，但上游照样跑完并向我们扣费。
//
// 做法与上游生图的 JSON 保活相同：等待超过 interval 后先提交 200，再定期写出空白（JSON 允许前导空白），
// 最终响应仍是一份完整 JSON。区别在于：
//   - 心跳字节不计入 Size() / Written()，handler 里「已写出内容则不再 failover」等上游判断无需改动；
//   - 提交 200 之后再写出 4xx/5xx 错误时，按非流式带内错误标记给 ops，错误日志和错误率不受影响。
const (
	otokClaudeNonstreamKeepaliveEnv     = "OTOK_CLAUDE_NONSTREAM_KEEPALIVE_SECONDS"
	otokClaudeNonstreamKeepaliveDefault = 45 * time.Second
	otokClaudeNonstreamKeepaliveMin     = 5 * time.Second
	otokClaudeNonstreamKeepaliveMax     = 90 * time.Second
)

var otokJSONKeepalivePadding = []byte(" \n")

// otokClaudeNonstreamKeepaliveIntervalFunc 是保活间隔的来源，测试里替换成毫秒级。
var otokClaudeNonstreamKeepaliveIntervalFunc = otokClaudeNonstreamKeepaliveInterval

// otokClaudeNonstreamKeepaliveInterval 读取保活间隔：未设置用默认值，0 或负数关闭，其余限制在 5～90 秒。
func otokClaudeNonstreamKeepaliveInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv(otokClaudeNonstreamKeepaliveEnv))
	if v == "" {
		return otokClaudeNonstreamKeepaliveDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return otokClaudeNonstreamKeepaliveDefault
	}
	if n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d < otokClaudeNonstreamKeepaliveMin {
		return otokClaudeNonstreamKeepaliveMin
	}
	if d > otokClaudeNonstreamKeepaliveMax {
		return otokClaudeNonstreamKeepaliveMax
	}
	return d
}

// otokStartClaudeNonstreamKeepalive 为非流式请求启动保活，返回的函数停止心跳并还原 c.Writer；
// 流式请求或已关闭时什么也不做。
func otokStartClaudeNonstreamKeepalive(c *gin.Context, stream bool) func() {
	if stream {
		return func() {}
	}
	return otokStartJSONKeepalive(c, otokClaudeNonstreamKeepaliveIntervalFunc())
}

func otokStartJSONKeepalive(c *gin.Context, interval time.Duration) func() {
	if c == nil || c.Writer == nil || interval <= 0 {
		return func() {}
	}
	w := &otokJSONKeepaliveWriter{ResponseWriter: c.Writer, ctx: c, done: make(chan struct{})}
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
		// 外层 ops 中间件的 writer 来自对象池、请求结束后归还，必须还原，不能让外层拿到包着它的 wrapper。
		if c.Writer == w {
			c.Writer = w.ResponseWriter
		}
	}
}

type otokJSONKeepaliveWriter struct {
	gin.ResponseWriter

	ctx       *gin.Context
	mu        sync.Mutex
	done      chan struct{}
	halted    bool
	committed bool // 心跳已提交 200 响应头
	padding   int  // 心跳写出的字节数
	status    int  // 提交后 handler 请求的状态码
	marked    bool // 已向 ops 标记带内错误
}

func (w *otokJSONKeepaliveWriter) beat() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.halted {
		return false
	}
	if !w.committed {
		h := w.ResponseWriter.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		w.ResponseWriter.WriteHeader(http.StatusOK)
		w.committed = true
	}
	n, err := w.ResponseWriter.Write(otokJSONKeepalivePadding)
	w.padding += n
	if err != nil {
		w.haltLocked()
		return false
	}
	w.ResponseWriter.Flush()
	return true
}

func (w *otokJSONKeepaliveWriter) halt() {
	w.mu.Lock()
	w.haltLocked()
	w.mu.Unlock()
}

func (w *otokJSONKeepaliveWriter) haltLocked() {
	if !w.halted {
		w.halted = true
		close(w.done)
	}
}

// markInBandError 在 200 已提交、handler 想返回 4xx/5xx 时，把错误按非流式带内错误标记给 ops。
func (w *otokJSONKeepaliveWriter) markInBandError(body []byte) {
	w.mu.Lock()
	status := w.status
	if !w.committed || status < http.StatusBadRequest || w.marked {
		w.mu.Unlock()
		return
	}
	w.marked = true
	w.mu.Unlock()

	errType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	if errType == "" {
		errType = "upstream_error"
	}
	service.MarkOpsStreamErrorValue(w.ctx, service.OpsStreamError{
		ErrType:         errType,
		Code:            gjson.GetBytes(body, "error.code").String(),
		Message:         gjson.GetBytes(body, "error.message").String(),
		IntendedStatus:  status,
		CountTowardsSLA: true,
		NonStream:       true,
	})
}

// 以下方法在 handler 开始组织响应时先停掉心跳；beat 全程持锁，所以停下后不会再并发访问响应头。

func (w *otokJSONKeepaliveWriter) Header() http.Header {
	w.halt()
	return w.ResponseWriter.Header()
}

func (w *otokJSONKeepaliveWriter) WriteHeader(code int) {
	w.mu.Lock()
	w.haltLocked()
	if w.committed && code > 0 {
		w.status = code
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *otokJSONKeepaliveWriter) WriteHeaderNow() {
	w.halt()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *otokJSONKeepaliveWriter) Write(data []byte) (int, error) {
	w.halt()
	w.markInBandError(data)
	return w.ResponseWriter.Write(data)
}

func (w *otokJSONKeepaliveWriter) WriteString(s string) (int, error) {
	w.halt()
	w.markInBandError([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *otokJSONKeepaliveWriter) Flush() {
	w.halt()
	w.ResponseWriter.Flush()
}

func (w *otokJSONKeepaliveWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed && w.status > 0 {
		return w.status
	}
	return w.ResponseWriter.Status()
}

// Size 不计心跳字节；只写过心跳时视为尚未写出（-1）。
func (w *otokJSONKeepaliveWriter) Size() int {
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

func (w *otokJSONKeepaliveWriter) Written() bool {
	return w.Size() != -1
}
