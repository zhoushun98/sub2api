package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// otok: 鹈鹕测试的单次请求。走本站网关环回（POST /v1/responses，stream），和真实用户经过同样的调度与模型映射。

const (
	otokPelicanUserAgent     = "otok-pelican-probe/1.0"
	otokPelicanMaxLineBytes  = 8 << 20
	otokPelicanMaxTextBytes  = 2 << 20
	otokPelicanMaxStoreBytes = 512 << 10
	otokPelicanMaxErrorBody  = 64 << 10
	otokPelicanMaxErrorRunes = 2000
)

type otokPelicanProbeRequest struct {
	Model           string
	Prompt          string
	ReasoningEffort string
	RequestID       string
}

type otokPelicanProbeResult struct {
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

func (r otokPelicanProbeResult) ok() bool { return r.ErrCode == "" }

// otokPelicanBaseURL 返回本进程网关地址；监听 0.0.0.0 / :: 时改用 127.0.0.1。
func otokPelicanBaseURL(cfg *config.Config) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("OTOK_PELICAN_BASE_URL")), "/"); v != "" {
		return v
	}
	host, port := "127.0.0.1", 8080
	if cfg != nil {
		if h := strings.TrimSpace(cfg.Server.Host); h != "" && h != "0.0.0.0" && h != "::" && h != "[::]" {
			host = h
		}
		if cfg.Server.Port > 0 {
			port = cfg.Server.Port
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// newOtokPelicanHTTPClient 不走环境代理（HTTP_PROXY 会把环回请求转走），超时交给 ctx。
func newOtokPelicanHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 0,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
	}}
}

func buildOtokPelicanBody(req otokPelicanProbeRequest) ([]byte, error) {
	body := map[string]any{
		"model":  req.Model,
		"input":  req.Prompt,
		"stream": true,
		"store":  false,
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	return json.Marshal(body)
}

// doOtokPelicanProbe 发送一次请求并读完整个 SSE 流。
func doOtokPelicanProbe(ctx context.Context, client *http.Client, baseURL, apiKey string, req otokPelicanProbeRequest) otokPelicanProbeResult {
	start := time.Now()
	res := otokPelicanProbeResult{}
	finish := func() otokPelicanProbeResult {
		res.DurationMs = int(time.Since(start).Milliseconds())
		return res
	}
	body, err := buildOtokPelicanBody(req)
	if err != nil {
		res.ErrCode, res.ErrMessage = "build_request", err.Error()
		return finish()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		res.ErrCode, res.ErrMessage = "build_request", err.Error()
		return finish()
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", otokPelicanUserAgent)
	if req.RequestID != "" {
		httpReq.Header.Set("X-Client-Request-ID", req.RequestID)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		setOtokPelicanTransportError(ctx, &res, err)
		return finish()
	}
	defer func() { _ = resp.Body.Close() }()
	res.HTTPStatus = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, otokPelicanMaxErrorBody))
		res.ErrCode = "http_" + strconv.Itoa(resp.StatusCode)
		res.ErrMessage = otokPelicanErrorBodyMessage(raw)
		res.Retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return finish()
	}

	parseOtokPelicanStream(ctx, resp.Body, start, &res)
	return finish()
}

func setOtokPelicanTransportError(ctx context.Context, res *otokPelicanProbeResult, err error) {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.ErrCode, res.ErrMessage, res.Retryable = "timeout", "request timed out", true
	case errors.Is(ctx.Err(), context.Canceled):
		res.ErrCode, res.ErrMessage = "canceled", "request canceled"
	case errors.Is(err, syscall.ECONNREFUSED):
		res.ErrCode, res.ErrMessage, res.Retryable = "connection_refused", err.Error(), true
	default:
		res.ErrCode, res.ErrMessage, res.Retryable = "network_error", err.Error(), true
	}
}

type otokPelicanSSEEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Response json.RawMessage `json:"response"`
	Error    json.RawMessage `json:"error"`
	Code     any             `json:"code"`
	Message  string          `json:"message"`
}

type otokPelicanResponseObj struct {
	Model  string `json:"model"`
	Status string `json:"status"`
	Usage  *struct {
		InputTokens         *int `json:"input_tokens"`
		OutputTokens        *int `json:"output_tokens"`
		OutputTokensDetails *struct {
			ReasoningTokens *int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

// parseOtokPelicanStream 解析 Responses SSE。completed 之前流就结束算 stream_interrupted。
func parseOtokPelicanStream(ctx context.Context, body io.Reader, start time.Time, res *otokPelicanProbeResult) {
	reader := bufio.NewReaderSize(body, 64<<10)
	var text strings.Builder
	ended := false
	var readErr error
	for {
		line, err := otokPelicanReadLine(reader)
		if line != "" && handleOtokPelicanLine(line, start, &text, res) {
			ended = true
			break
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	if res.Text == "" {
		res.Text = text.String()
	}
	if !ended && res.ok() {
		switch {
		case ctx.Err() != nil:
			setOtokPelicanTransportError(ctx, res, ctx.Err())
		case readErr != nil:
			res.ErrCode, res.ErrMessage, res.Retryable = "stream_interrupted", readErr.Error(), true
		default:
			res.ErrCode, res.ErrMessage, res.Retryable = "stream_interrupted", "stream ended before response.completed", true
		}
	}
	if res.ok() && strings.TrimSpace(res.Text) == "" {
		res.ErrCode, res.ErrMessage = "empty_output", "model returned no text"
	}
}

func otokPelicanReadLine(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(buf)+len(chunk) <= otokPelicanMaxLineBytes {
			buf = append(buf, chunk...)
		}
		if err != nil {
			return string(buf), err
		}
		if !isPrefix {
			return string(buf), nil
		}
	}
}

// handleOtokPelicanLine 处理一行 SSE；返回 true 表示流已结束（成功或失败）。
func handleOtokPelicanLine(line string, start time.Time, text *strings.Builder, res *otokPelicanProbeResult) bool {
	if !strings.HasPrefix(line, "data:") {
		return false // event: / id: / 注释行，类型以 data 里的 type 为准
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return false
	}
	var ev otokPelicanSSEEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta == "" {
			return false
		}
		if res.FirstTokenMs == nil {
			ms := int(time.Since(start).Milliseconds())
			res.FirstTokenMs = &ms
		}
		if text.Len() < otokPelicanMaxTextBytes {
			_, _ = text.WriteString(ev.Delta)
		}
	case "response.completed":
		obj := parseOtokPelicanResponseObj(ev.Response, res)
		if text.Len() == 0 && obj != nil {
			res.Text = otokPelicanOutputText(obj)
		}
		return true
	case "response.failed":
		obj := parseOtokPelicanResponseObj(ev.Response, res)
		res.ErrCode, res.ErrMessage = "response_failed", "response failed"
		if obj != nil && obj.Error != nil {
			code := fmt.Sprint(obj.Error.Code)
			if obj.Error.Message != "" {
				res.ErrMessage = obj.Error.Message
			}
			if code != "" && code != "<nil>" {
				res.ErrMessage = code + ": " + res.ErrMessage
			}
			res.Retryable = otokPelicanRetryableCode(code)
		}
		res.Text = text.String()
		return true
	case "response.incomplete":
		obj := parseOtokPelicanResponseObj(ev.Response, res)
		res.ErrCode, res.ErrMessage = "response_incomplete", "response incomplete"
		if obj != nil && obj.IncompleteDetails != nil && obj.IncompleteDetails.Reason != "" {
			res.ErrMessage = "response incomplete: " + obj.IncompleteDetails.Reason
		}
		res.Text = text.String()
		return true
	case "error":
		res.ErrCode = "stream_error"
		res.ErrMessage = otokPelicanErrorBodyMessage([]byte(data))
		code := fmt.Sprint(ev.Code)
		res.Retryable = otokPelicanRetryableCode(code) || strings.Contains(strings.ToLower(res.ErrMessage), "server_error")
		res.Text = text.String()
		return true
	}
	return false
}

func parseOtokPelicanResponseObj(raw json.RawMessage, res *otokPelicanProbeResult) *otokPelicanResponseObj {
	if len(raw) == 0 {
		return nil
	}
	var obj otokPelicanResponseObj
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if obj.Model != "" {
		res.ResponseModel = obj.Model
	}
	if obj.Usage != nil {
		res.InputTokens = obj.Usage.InputTokens
		res.OutputTokens = obj.Usage.OutputTokens
		if obj.Usage.OutputTokensDetails != nil {
			res.ReasoningTokens = obj.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
	return &obj
}

func otokPelicanOutputText(obj *otokPelicanResponseObj) string {
	var b strings.Builder
	for _, item := range obj.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" {
				_, _ = b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

func otokPelicanRetryableCode(code string) bool {
	code = strings.ToLower(code)
	return strings.Contains(code, "server_error") || strings.Contains(code, "rate_limit") ||
		strings.Contains(code, "overloaded") || strings.Contains(code, "timeout") || strings.Contains(code, "unavailable")
}

// otokPelicanErrorBodyMessage 从 OpenAI 风格的错误体里取 message，取不到就返回截断后的原文。
func otokPelicanErrorBodyMessage(raw []byte) string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &body) == nil {
		var nested struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		}
		if len(body.Error) > 0 && json.Unmarshal(body.Error, &nested) == nil && nested.Message != "" {
			msg = nested.Message
			if c := fmt.Sprint(nested.Code); nested.Code != nil && c != "" {
				msg = c + ": " + msg
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
	return truncateOtokPelicanRunes(msg, otokPelicanMaxErrorRunes)
}

func truncateOtokPelicanRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// truncateOtokPelicanBytes 按字节截断，并退到合法的 UTF-8 边界。
func truncateOtokPelicanBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

var (
	otokPelicanFenceRe   = regexp.MustCompile("(?s)```([A-Za-z0-9_+-]*)[^\\n]*\\n(.*?)(?:```|$)")
	otokPelicanDocRe     = regexp.MustCompile(`(?is)(<!doctype\s+html.*?</html\s*>|<html[\s>].*?</html\s*>)`)
	otokPelicanSVGRe     = regexp.MustCompile(`(?is)<svg[\s>].*?</svg\s*>`)
	otokPelicanHTMLHint  = regexp.MustCompile(`(?i)<!doctype\s+html|<html[\s>]|<svg[\s>]`)
	otokPelicanOpenDocRe = regexp.MustCompile(`(?is)(<!doctype\s+html|<html[\s>]).*`)
)

// extractOtokPelicanHTML 从模型输出里取出 HTML：代码围栏 → 完整文档 → 单独的 SVG；都没有返回空串。
func extractOtokPelicanHTML(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if m := otokPelicanFenceRe.FindAllStringSubmatch(text, -1); len(m) > 0 {
		var pick string
		for i := len(m) - 1; i >= 0; i-- {
			lang := strings.ToLower(m[i][1])
			body := strings.TrimSpace(m[i][2])
			if (lang == "html" || lang == "svg" || lang == "xml") && body != "" {
				pick = body
				break
			}
			if pick == "" && otokPelicanHTMLHint.MatchString(body) {
				pick = body
			}
		}
		if pick != "" {
			return wrapOtokPelicanSVG(pick)
		}
	}
	if m := otokPelicanDocRe.FindString(text); m != "" {
		return m
	}
	if m := otokPelicanSVGRe.FindString(text); m != "" {
		return wrapOtokPelicanSVG(m)
	}
	// 输出被截断、缺少 </html> 时尽量保留已有部分，浏览器能容错渲染。
	if m := otokPelicanOpenDocRe.FindString(text); m != "" {
		return m
	}
	return ""
}

func wrapOtokPelicanSVG(s string) string {
	t := strings.TrimSpace(s)
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "<svg") || strings.HasPrefix(lower, "<?xml") {
		return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><style>html,body{margin:0;height:100%;display:flex;align-items:center;justify-content:center;background:#fff}svg{max-width:100%;max-height:100%}</style></head><body>\n" + t + "\n</body></html>"
	}
	return t
}
