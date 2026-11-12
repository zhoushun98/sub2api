package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 线上中转上游（麻豆）实际返回的两种写法
const (
	otokEncryptedContextInvalidBody = `{"error":{"code":"encrypted_context_invalid","message":"The upstream could not validate encrypted continuation context. Rebuild the conversation context in the client.","param":"input","request_id":"f966673c","type":"invalid_request_error"},"request_id":"f966673c"}`
	otokGenericInvalidRequestBody   = `{"error":{"code":"invalid_request","message":"The request could not be processed. Please check the request parameters.","request_id":"79cc240f","type":"invalid_request_error"},"request_id":"79cc240f"}`
)

func TestOtokIsInvalidEncryptedContextError(t *testing.T) {
	for _, tc := range []struct {
		name, code, msg string
		want            bool
	}{
		{"encrypted_context_invalid", "encrypted_context_invalid", "The upstream could not validate encrypted continuation context.", true},
		{"code 大小写与空白", " Encrypted_Context_Invalid ", "", true},
		{"笼统 invalid_request", "invalid_request", "The request could not be processed. Please check the request parameters.", true},
		{"invalid_request 但是别的文案", "invalid_request", "Missing required parameter: 'input'.", false},
		{"笼统文案但 code 不同", "rate_limit_exceeded", "The request could not be processed. Please check the request parameters.", false},
		{"空", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, otokIsInvalidEncryptedContextError(tc.code, tc.msg))
		})
	}
}

func otokNewEncryptedContextTestService(responses ...string) (*OpenAIGatewayService, *httpUpstreamRecorder, *Account) {
	upstream := &httpUpstreamRecorder{}
	for _, body := range responses {
		status := http.StatusOK
		if strings.Contains(body, `"error"`) {
			status = http.StatusBadRequest
		}
		upstream.responses = append(upstream.responses, &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		})
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{ID: 186761, Name: "relay-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
		Extra: map[string]any{"use_responses_api": true}}
	return svc, upstream, account
}

func otokNewEncryptedContextTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c
}

func TestOtokForward_RelayEncryptedContextErrorsRetryWithoutCiphertext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, errBody string }{
		{"encrypted_context_invalid", otokEncryptedContextInvalidBody},
		{"generic invalid_request", otokGenericInvalidRequestBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, upstream, account := otokNewEncryptedContextTestService(
				tc.errBody,
				`{"id":"resp_recovered","usage":{"input_tokens":1,"output_tokens":2}}`,
				`{"id":"resp_next","usage":{"input_tokens":1,"output_tokens":2}}`,
			)
			input := `{"model":"gpt-5.6-sol","stream":false,"prompt_cache_key":"otok-` + tc.name + `","input":[{"type":"reasoning","encrypted_content":"from-other-vendor","summary":[{"type":"summary_text","text":"keep"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`

			first, err := svc.Forward(context.Background(), otokNewEncryptedContextTestContext(), account, []byte(input))
			require.NoError(t, err)
			require.NotNil(t, first)
			require.Len(t, upstream.bodies, 2)
			require.Equal(t, "from-other-vendor", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
			require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
			require.Equal(t, "keep", gjson.GetBytes(upstream.bodies[1], "input.0.summary.0.text").String())
			require.Equal(t, "hello", gjson.GetBytes(upstream.bodies[1], "input.1.content.0.text").String())

			// 同一会话下一轮：失效密文已记入 lineage，进场即剥离，不再先吃一次 400
			second, err := svc.Forward(context.Background(), otokNewEncryptedContextTestContext(), account, []byte(input))
			require.NoError(t, err)
			require.NotNil(t, second)
			require.Len(t, upstream.bodies, 3)
			require.False(t, gjson.GetBytes(upstream.bodies[2], "input.0.encrypted_content").Exists())
		})
	}
}

func TestOtokForward_GenericInvalidRequestWithoutCiphertextPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusBadRequest,
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(otokGenericInvalidRequestBody))}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{ID: 186761, Name: "relay-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
		Extra: map[string]any{"use_responses_api": true}}

	_, err := svc.Forward(context.Background(), otokNewEncryptedContextTestContext(), account,
		[]byte(`{"model":"gpt-5.6-sol","stream":false,"input":[{"type":"message","role":"user","content":"hello"}]}`))
	require.Error(t, err)
	require.Len(t, upstream.bodies, 1, "请求里没有加密推理项时不应重试")
}
