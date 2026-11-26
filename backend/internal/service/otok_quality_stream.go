package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// otok: 降智检测三种协议的 SSE 解析。

const otokQualityHeadBytes = 512

type otokQualityStreamParser struct {
	protocol string
	start    time.Time
	res      *otokQualityResult
	text     strings.Builder
	event    string // 当前 SSE 事件名，空行后清空
	sawData  bool
	ended    bool // 读到了终止帧（成功或失败）
	// chat：finish_reason 之后 usage 还会单独来一块，要继续读到 [DONE] 或 EOF。
	finished     bool
	finishReason string
	stopReason   string
}

// otokQualityHeadReader 记下响应体开头，2xx 却不是 SSE 时用来写错误原文。
type otokQualityHeadReader struct {
	r    io.Reader
	head []byte
}

func (h *otokQualityHeadReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if room := otokQualityHeadBytes - len(h.head); room > 0 && n > 0 {
		h.head = append(h.head, p[:min(room, n)]...)
	}
	return n, err
}

// parseOtokQualityStream 读完 SSE 流并把结果写进 res。
func parseOtokQualityStream(ctx context.Context, protocol, contentType string, body io.Reader, start time.Time, res *otokQualityResult) {
	head := &otokQualityHeadReader{r: body}
	reader := bufio.NewReaderSize(head, 64<<10)
	p := &otokQualityStreamParser{protocol: protocol, start: start, res: res}
	var readErr error
	for !p.ended {
		line, err := otokQualityReadLine(reader)
		p.handleLine(line)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	if res.Text == "" {
		res.Text = p.text.String()
	}
	if !res.ok() {
		return
	}

	switch {
	case p.ended && p.protocol != OtokQualityProtocolChat:
		if p.protocol == OtokQualityProtocolMessages && p.stopReason == "max_tokens" {
			res.ErrCode, res.ErrMessage = "response_incomplete", "response incomplete: max_tokens"
			return
		}
	case p.ended || p.finished:
		if p.finishReason == "length" {
			res.ErrCode, res.ErrMessage = "response_incomplete", "response incomplete: length"
			return
		}
	case ctx.Err() != nil:
		setOtokQualityTransportError(ctx, res, ctx.Err())
		return
	case readErr != nil:
		res.ErrCode, res.ErrMessage, res.Retryable = "stream_interrupted", readErr.Error(), true
		return
	case !p.sawData:
		res.ErrCode = "unexpected_response"
		res.ErrMessage = truncateOtokQualityRunes(fmt.Sprintf("response is not an SSE stream (content-type: %q): %s",
			contentType, strings.TrimSpace(strings.ToValidUTF8(string(head.head), ""))), otokQualityMaxErrorRunes)
		return
	default:
		res.ErrCode, res.ErrMessage, res.Retryable = "stream_interrupted", "stream ended before the final event", true
		return
	}
	if strings.TrimSpace(res.Text) == "" {
		res.ErrCode, res.ErrMessage = "empty_output", "model returned no text"
	}
}

func otokQualityReadLine(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(buf)+len(chunk) <= otokQualityMaxLineBytes {
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

func (p *otokQualityStreamParser) handleLine(line string) {
	if line == "" {
		p.event = ""
		return
	}
	if strings.HasPrefix(line, "event:") {
		p.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		return
	}
	if !strings.HasPrefix(line, "data:") {
		return // id: / retry: / 注释行
	}
	p.sawData = true
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" {
		return
	}
	if data == "[DONE]" {
		if p.protocol == OtokQualityProtocolChat {
			p.ended = true
		}
		return
	}
	if p.handleErrorFrame(data) {
		p.ended = true
		return
	}
	switch p.protocol {
	case OtokQualityProtocolResponses:
		p.ended = p.handleResponses(data)
	case OtokQualityProtocolChat:
		p.handleChat(data)
	case OtokQualityProtocolMessages:
		p.ended = p.handleMessages(data)
	}
}

func (p *otokQualityStreamParser) appendText(s string) {
	if s == "" {
		return
	}
	if p.res.FirstTokenMs == nil {
		ms := int(time.Since(p.start).Milliseconds())
		p.res.FirstTokenMs = &ms
	}
	if p.text.Len() < otokQualityMaxTextBytes {
		_, _ = p.text.WriteString(s)
	}
}

// handleErrorFrame 识别三种协议通用的流内错误帧：事件名是 error、data 的 type 是 error、或 data 顶层有 error 字段。
func (p *otokQualityStreamParser) handleErrorFrame(data string) bool {
	var frame struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
		Code  any             `json:"code"`
	}
	parsed := json.Unmarshal([]byte(data), &frame) == nil
	hasErrField := false
	var hint strings.Builder
	if parsed {
		raw := strings.TrimSpace(string(frame.Error))
		if strings.HasPrefix(raw, "{") {
			hasErrField = true
			var nested struct {
				Type string `json:"type"`
				Code any    `json:"code"`
			}
			if json.Unmarshal(frame.Error, &nested) == nil {
				_, _ = hint.WriteString(nested.Type + " ")
				if nested.Code != nil {
					_, _ = hint.WriteString(fmt.Sprint(nested.Code) + " ")
				}
			}
		} else if strings.HasPrefix(raw, `"`) {
			var s string
			hasErrField = json.Unmarshal(frame.Error, &s) == nil && s != ""
		}
		if frame.Code != nil {
			_, _ = hint.WriteString(fmt.Sprint(frame.Code) + " ")
		}
	}
	isErrType := parsed && frame.Type == "error"
	if p.event != "error" && !isErrType && !hasErrField {
		return false
	}
	p.res.ErrCode = "stream_error"
	p.res.ErrMessage = otokQualityErrorBodyMessage([]byte(data))
	p.res.Retryable = otokQualityRetryableCode(hint.String()) || otokQualityRetryableCode(p.res.ErrMessage)
	p.res.Text = p.text.String()
	return true
}

// ---------- Responses ----------

type otokQualityResponsesEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Response json.RawMessage `json:"response"`
}

type otokQualityResponseObj struct {
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

// handleResponses 处理一条 Responses 事件；返回 true 表示流已结束（成功或失败）。
func (p *otokQualityStreamParser) handleResponses(data string) bool {
	var ev otokQualityResponsesEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false
	}
	res := p.res
	switch ev.Type {
	case "response.output_text.delta":
		p.appendText(ev.Delta)
	case "response.completed":
		obj := p.parseResponseObj(ev.Response)
		// 本站 /v1/responses 转 Chat 上游、finish_reason=length 时，用 completed 事件带 incomplete 状态返回。
		if obj != nil && (obj.Status == "incomplete" || (obj.IncompleteDetails != nil && obj.IncompleteDetails.Reason != "")) {
			p.setResponseIncomplete(obj)
			return true
		}
		if p.text.Len() == 0 && obj != nil {
			res.Text = otokQualityOutputText(obj)
		}
		return true
	case "response.failed":
		obj := p.parseResponseObj(ev.Response)
		res.ErrCode, res.ErrMessage = "response_failed", "response failed"
		if obj != nil && obj.Error != nil {
			code := ""
			if obj.Error.Code != nil {
				code = fmt.Sprint(obj.Error.Code)
			}
			if obj.Error.Message != "" {
				res.ErrMessage = obj.Error.Message
			}
			if code != "" {
				res.ErrMessage = code + ": " + res.ErrMessage
			}
			res.Retryable = otokQualityRetryableCode(code)
		}
		res.Text = p.text.String()
		return true
	case "response.incomplete":
		p.setResponseIncomplete(p.parseResponseObj(ev.Response))
		return true
	}
	return false
}

// setResponseIncomplete 记为 response_incomplete（不重试），已有正文保留：优先累计的 delta，没有就取 output。
func (p *otokQualityStreamParser) setResponseIncomplete(obj *otokQualityResponseObj) {
	res := p.res
	res.ErrCode, res.ErrMessage = "response_incomplete", "response incomplete"
	if obj != nil && obj.IncompleteDetails != nil && obj.IncompleteDetails.Reason != "" {
		res.ErrMessage = "response incomplete: " + obj.IncompleteDetails.Reason
	}
	res.Text = p.text.String()
	if res.Text == "" && obj != nil {
		res.Text = otokQualityOutputText(obj)
	}
}

func (p *otokQualityStreamParser) parseResponseObj(raw json.RawMessage) *otokQualityResponseObj {
	if len(raw) == 0 {
		return nil
	}
	var obj otokQualityResponseObj
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if obj.Model != "" {
		p.res.ResponseModel = obj.Model
	}
	if obj.Usage != nil {
		p.res.InputTokens = obj.Usage.InputTokens
		p.res.OutputTokens = obj.Usage.OutputTokens
		if obj.Usage.OutputTokensDetails != nil {
			p.res.ReasoningTokens = obj.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
	return &obj
}

func otokQualityOutputText(obj *otokQualityResponseObj) string {
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

// ---------- Chat Completions ----------

type otokQualityChatChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content any `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens            *int `json:"prompt_tokens"`
		CompletionTokens        *int `json:"completion_tokens"`
		CompletionTokensDetails *struct {
			ReasoningTokens *int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

func (p *otokQualityStreamParser) handleChat(data string) {
	var ch otokQualityChatChunk
	if err := json.Unmarshal([]byte(data), &ch); err != nil {
		return
	}
	if ch.Model != "" {
		p.res.ResponseModel = ch.Model
	}
	if len(ch.Choices) > 0 {
		c := ch.Choices[0]
		if s, ok := c.Delta.Content.(string); ok {
			p.appendText(s)
		}
		if c.FinishReason != nil && *c.FinishReason != "" {
			p.finished = true
			p.finishReason = *c.FinishReason
		}
	}
	if u := ch.Usage; u != nil {
		p.res.InputTokens = u.PromptTokens
		p.res.OutputTokens = u.CompletionTokens
		p.res.ReasoningTokens = nil
		if u.CompletionTokensDetails != nil {
			p.res.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
		}
	}
}

// ---------- Messages ----------

type otokQualityMessagesEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens *int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta *struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	} `json:"usage"`
}

// handleMessages 处理一条 Anthropic Messages 事件；返回 true 表示读到 message_stop。
func (p *otokQualityStreamParser) handleMessages(data string) bool {
	var ev otokQualityMessagesEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			if ev.Message.Model != "" {
				p.res.ResponseModel = ev.Message.Model
			}
			if ev.Message.Usage != nil && ev.Message.Usage.InputTokens != nil {
				p.res.InputTokens = ev.Message.Usage.InputTokens
			}
		}
	case "content_block_delta":
		if ev.Delta != nil && ev.Delta.Type == "text_delta" {
			p.appendText(ev.Delta.Text)
		}
	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			p.stopReason = ev.Delta.StopReason
		}
		if ev.Usage != nil {
			if ev.Usage.OutputTokens != nil {
				p.res.OutputTokens = ev.Usage.OutputTokens
			}
			// 本站把 OpenAI 分组转成 Messages 流时，输入 token 只在这里给。
			if ev.Usage.InputTokens != nil && *ev.Usage.InputTokens > 0 {
				p.res.InputTokens = ev.Usage.InputTokens
			}
		}
	case "message_stop":
		return true
	}
	return false
}
