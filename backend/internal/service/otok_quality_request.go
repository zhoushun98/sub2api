package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// otok: 降智检测的单次请求。按条目 URL 的路径选协议（Responses / Chat Completions / Messages），一律流式。

const (
	otokQualityMaxLineBytes     = 8 << 20
	otokQualityMaxTextBytes     = 2 << 20
	otokQualityMaxStoreBytes    = 512 << 10
	otokQualityMaxErrorBody     = 64 << 10
	otokQualityMaxErrorRunes    = 2000
	otokQualityMessagesMaxToken = 16000
	otokQualityAnthropicVersion = "2023-06-01"
	otokQualityKeyRedactMinLen  = 8
)

// otokQualityMaxResponseBytes 是响应体读取上限；做成变量只为单测临时调小。
var otokQualityMaxResponseBytes int64 = 32 << 20

type otokQualityRequest struct {
	URL             string
	Protocol        string
	APIKey          string
	Model           string
	Prompt          string
	ReasoningEffort string
	ExtraBody       string
}

type otokQualityResult struct {
	HTTPStatus      int
	Text            string
	ResponseModel   string
	InputTokens     *int
	OutputTokens    *int
	ReasoningTokens *int
	FirstTokenMs    *int
	DurationMs      int
	ErrCode         string // 空表示成功
	ErrMessage      string
	Retryable       bool
}

func (r otokQualityResult) ok() bool { return r.ErrCode == "" }

// newOtokQualityHTTPClient 不走环境代理（HTTP_PROXY 会把环回请求转走），不跟随重定向，整体超时交给 ctx。
func newOtokQualityHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        8,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// buildOtokQualityBody 合并顺序：协议默认值 ← 思考强度 ← extra_body（顶层浅合并）← 强制字段。
func buildOtokQualityBody(req otokQualityRequest) ([]byte, error) {
	var body map[string]any
	switch req.Protocol {
	case OtokQualityProtocolResponses:
		body = map[string]any{"model": req.Model, "input": req.Prompt, "stream": true, "store": false}
		if req.ReasoningEffort != "" {
			body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
		}
	case OtokQualityProtocolChat:
		body = map[string]any{
			"model":          req.Model,
			"messages":       otokQualityUserMessages(req.Prompt),
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
		}
		if req.ReasoningEffort != "" {
			body["reasoning_effort"] = req.ReasoningEffort
		}
	case OtokQualityProtocolMessages:
		body = map[string]any{
			"model":      req.Model,
			"max_tokens": otokQualityMessagesMaxToken,
			"messages":   otokQualityUserMessages(req.Prompt),
			"stream":     true,
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", req.Protocol)
	}

	if extra := strings.TrimSpace(req.ExtraBody); extra != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(extra), &obj); err != nil || obj == nil {
			return nil, errors.New("extra_body is not a JSON object")
		}
		for k, v := range obj {
			body[k] = v
		}
	}

	body["model"] = req.Model
	body["stream"] = true
	switch req.Protocol {
	case OtokQualityProtocolResponses:
		body["input"] = req.Prompt
	case OtokQualityProtocolChat:
		body["messages"] = otokQualityUserMessages(req.Prompt)
		opts, _ := body["stream_options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		opts["include_usage"] = true
		body["stream_options"] = opts
	case OtokQualityProtocolMessages:
		body["messages"] = otokQualityUserMessages(req.Prompt)
	}
	return json.Marshal(body)
}

func otokQualityUserMessages(prompt string) []any {
	return []any{map[string]any{"role": "user", "content": prompt}}
}

// newOtokQualityHTTPRequest 只设置鉴权、Content-Type 和 Accept。不设 User-Agent、不发 X-Client-Request-ID：
// 本站网关会把这两个头原样转给上游。
func newOtokQualityHTTPRequest(ctx context.Context, req otokQualityRequest) (*http.Request, error) {
	body, err := buildOtokQualityBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if req.Protocol == OtokQualityProtocolMessages {
		httpReq.Header.Set("x-api-key", req.APIKey)
		httpReq.Header.Set("anthropic-version", otokQualityAnthropicVersion)
	} else {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	return httpReq, nil
}

// doOtokQualityRequest 发送一次请求并读完整个 SSE 流。错误原文里的 key 明文会被打码。
func doOtokQualityRequest(ctx context.Context, client *http.Client, req otokQualityRequest) otokQualityResult {
	start := time.Now()
	res := otokQualityResult{}
	finish := func() otokQualityResult {
		res.DurationMs = int(time.Since(start).Milliseconds())
		// PostgreSQL 的 TEXT 列拒收 NUL 和非法 UTF-8，写库失败会让记录卡在 running。
		res.Text = sanitizeOtokQualityText(res.Text)
		res.ResponseModel = sanitizeOtokQualityText(res.ResponseModel)
		// 顺序固定：清洗 → 打码 → 截断。
		res.ErrMessage = sanitizeOtokQualityText(res.ErrMessage)
		res.ErrMessage = otokQualityRedactKey(res.ErrMessage, req.APIKey)
		res.ErrMessage = truncateOtokQualityRunes(res.ErrMessage, otokQualityMaxErrorRunes)
		return res
	}
	httpReq, err := newOtokQualityHTTPRequest(ctx, req)
	if err != nil {
		res.ErrCode, res.ErrMessage = "build_request", err.Error()
		return finish()
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		setOtokQualityTransportError(ctx, &res, err)
		return finish()
	}
	defer func() { _ = resp.Body.Close() }()
	res.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, otokQualityMaxErrorBody))
		res.ErrCode = otokQualityHTTPErrorCode(resp.StatusCode)
		res.ErrMessage = otokQualityErrorBodyMessage(raw)
		if res.ErrCode == "http_3xx" {
			msg := fmt.Sprintf("HTTP %d redirect to %q (redirects are not followed)", resp.StatusCode, resp.Header.Get("Location"))
			if res.ErrMessage != "" {
				msg += ": " + res.ErrMessage
			}
			res.ErrMessage = truncateOtokQualityRunes(msg, otokQualityMaxErrorRunes)
		}
		res.Retryable = res.ErrCode == "http_429" || res.ErrCode == "http_5xx"
		return finish()
	}

	// 多读 1 字节，N 归零才说明确实超过了上限。
	body := &io.LimitedReader{R: resp.Body, N: otokQualityMaxResponseBytes + 1}
	parseOtokQualityStream(ctx, req.Protocol, resp.Header.Get("Content-Type"), body, start, &res)
	if body.N == 0 && res.ErrCode == "stream_interrupted" {
		// 撞上限不重试：重跑一遍多半还是这么长，白花一次钱。
		res.ErrCode, res.ErrMessage, res.Retryable = "response_incomplete", "response body exceeded 32MB", false
	}
	return finish()
}

// otokQualityHTTPErrorCode 在源头把状态码归并成几类，精确状态码只留在 http_status 列。
func otokQualityHTTPErrorCode(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "http_429"
	case status >= 300 && status < 400:
		return "http_3xx"
	case status >= 500:
		return "http_5xx"
	default:
		return "http_4xx"
	}
}

func setOtokQualityTransportError(ctx context.Context, res *otokQualityResult, err error) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// 超时不重试：我们断开后网关仍会把上游跑完并计费，重试等于花两次钱，而且多半还会超时。
		res.ErrCode, res.ErrMessage, res.Retryable = "timeout", "request timed out", false
	case errors.Is(ctx.Err(), context.Canceled):
		res.ErrCode, res.ErrMessage, res.Retryable = "canceled", "request canceled", false
	case errors.Is(err, syscall.ECONNREFUSED):
		res.ErrCode, res.ErrMessage, res.Retryable = "connection_refused", err.Error(), true
	default:
		res.ErrCode, res.ErrMessage, res.Retryable = "network_error", err.Error(), true
	}
}

func sanitizeOtokQualityText(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "\uFFFD")
}

// otokQualityRedactKey 把 key 明文替换成 ***；太短的 key 替换会误伤正文，不处理。
// 错误原文在上游就可能被截断，结尾残留的 key 前缀（长于 4 位）也换成 ***。
func otokQualityRedactKey(msg, key string) string {
	if len(key) < otokQualityKeyRedactMinLen || msg == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, key, "***")
	body, ellipsis := msg, ""
	if t, ok := strings.CutSuffix(msg, "…"); ok {
		body, ellipsis = t, "…"
	}
	for n := len(key) - 1; n > 4; n-- {
		if strings.HasSuffix(body, key[:n]) {
			return body[:len(body)-n] + "***" + ellipsis
		}
	}
	return msg
}

var otokQualityRetryableHints = []string{
	"server_error", "rate_limit", "overloaded", "timeout", "unavailable",
	"api_error", "upstream_error", "stream_read_error",
}

func otokQualityRetryableCode(code string) bool {
	code = strings.ToLower(code)
	for _, h := range otokQualityRetryableHints {
		if strings.Contains(code, h) {
			return true
		}
	}
	return false
}

// otokQualityErrorBodyMessage 从 OpenAI / Anthropic 风格的错误体里取 message，取不到就返回截断后的原文。
func otokQualityErrorBodyMessage(raw []byte) string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &body) == nil {
		var nested struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		}
		if len(body.Error) > 0 && json.Unmarshal(body.Error, &nested) == nil && nested.Message != "" {
			msg = nested.Message
			if nested.Code != nil && fmt.Sprint(nested.Code) != "" {
				msg = fmt.Sprint(nested.Code) + ": " + msg
			} else if nested.Type != "" {
				msg = nested.Type + ": " + msg
			}
		} else if len(body.Error) > 0 {
			var s string
			if json.Unmarshal(body.Error, &s) == nil {
				msg = s
			}
		}
		if msg == "" {
			msg = body.Message
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	return truncateOtokQualityRunes(msg, otokQualityMaxErrorRunes)
}

func truncateOtokQualityRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// truncateOtokQualityBytes 按字节截断，并退到合法的 UTF-8 边界。
func truncateOtokQualityBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var (
	otokQualityFenceRe   = regexp.MustCompile("(?s)```([A-Za-z0-9_+-]*)[^\\n]*\\n(.*?)(?:```|$)")
	otokQualityDocRe     = regexp.MustCompile(`(?is)(<!doctype\s+html.*?</html\s*>|<html[\s>].*?</html\s*>)`)
	otokQualitySVGRe     = regexp.MustCompile(`(?is)<svg[\s>].*?</svg\s*>`)
	otokQualityHTMLHint  = regexp.MustCompile(`(?i)<!doctype\s+html|<html[\s>]|<svg[\s>]`)
	otokQualityOpenDocRe = regexp.MustCompile(`(?is)(<!doctype\s+html|<html[\s>]).*`)
)

// extractOtokQualityHTML 从模型输出里取出 HTML：代码围栏 → 完整文档 → 单独的 SVG；都没有返回空串。
func extractOtokQualityHTML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if m := otokQualityFenceRe.FindAllStringSubmatch(text, -1); len(m) > 0 {
		var pick string
		for i := len(m) - 1; i >= 0; i-- {
			lang := strings.ToLower(m[i][1])
			body := strings.TrimSpace(m[i][2])
			if (lang == "html" || lang == "svg" || lang == "xml") && body != "" {
				pick = body
				break
			}
			if pick == "" && otokQualityHTMLHint.MatchString(body) {
				pick = body
			}
		}
		if pick != "" {
			return wrapOtokQualitySVG(pick)
		}
	}
	if m := otokQualityDocRe.FindString(text); m != "" {
		return m
	}
	if m := otokQualitySVGRe.FindString(text); m != "" {
		return wrapOtokQualitySVG(m)
	}
	// 输出被截断、缺少 </html> 时尽量保留已有部分，浏览器能容错渲染。
	if m := otokQualityOpenDocRe.FindString(text); m != "" {
		return m
	}
	return ""
}

func wrapOtokQualitySVG(s string) string {
	t := strings.TrimSpace(s)
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "<svg") || strings.HasPrefix(lower, "<?xml") {
		return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><style>html,body{margin:0;height:100%;display:flex;align-items:center;justify-content:center;background:#fff}svg{max-width:100%;max-height:100%}</style></head><body>\n" + t + "\n</body></html>"
	}
	return t
}
