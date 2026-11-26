//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// ---------- 配置 ----------

func newOtokQualityConfigService(t *testing.T, cfg *config.Config, enc SecretEncryptor) (*OtokQualityService, *otokQualitySettingRepoStub) {
	t.Helper()
	repo := &otokQualitySettingRepoStub{values: map[string]string{}}
	return NewOtokQualityService(NewSettingService(repo, nil), nil, cfg, enc, true), repo
}

func otokQualityValidItem() OtokQualityItemInput {
	return OtokQualityItemInput{
		Name:            "OpenAI 分组",
		Enabled:         true,
		URL:             "http://127.0.0.1:8080/v1/responses",
		APIKey:          "sk-test-1234567890",
		Models:          []string{"gpt-6-astra"},
		ReasoningEffort: "medium",
		Prompt:          "画一只鹈鹕",
	}
}

func requireOtokQualityReason(t *testing.T, err error, reason string, status int) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, "OTOK_QUALITY_"+reason, infraerrors.Reason(err), err.Error())
	require.Equal(t, status, infraerrors.Code(err))
}

func TestNormalizeOtokQualityURL(t *testing.T) {
	ok := []struct{ in, want, proto string }{
		{"http://127.0.0.1:8080/v1/responses", "http://127.0.0.1:8080/v1/responses", OtokQualityProtocolResponses},
		{"https://api.example.com/v1/responses/", "https://api.example.com/v1/responses", OtokQualityProtocolResponses},
		{"HTTPS://api.example.com/v1/chat/completions//", "https://api.example.com/v1/chat/completions", OtokQualityProtocolChat},
		{"http://localhost/v1/messages?beta=true", "http://localhost/v1/messages?beta=true", OtokQualityProtocolMessages},
		{"  http://10.0.0.5:9000/proxy/messages  ", "http://10.0.0.5:9000/proxy/messages", OtokQualityProtocolMessages},
	}
	for _, tc := range ok {
		got, proto, err := normalizeOtokQualityURL(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
		require.Equal(t, tc.proto, proto, tc.in)
		require.Equal(t, tc.proto, otokQualityProtocolOf(got))
	}
	bad := []string{
		"",
		"http://127.0.0.1:8080/v1/messages/count_tokens",
		"http://127.0.0.1:8080/v1/models",
		"http://127.0.0.1:8080/v1/responses/compact",
		"ftp://example.com/v1/responses",
		"/v1/responses",
		"http:///v1/responses",
		"http://user:pass@example.com/v1/responses",
		"http://example.com/v1/responses#frag",
		"http://example.com/v1/chat/completionsx",
		"http://example.com/" + strings.Repeat("a", 2100) + "/responses",
	}
	for _, in := range bad {
		_, _, err := normalizeOtokQualityURL(in)
		requireOtokQualityReason(t, err, "INVALID_URL", http.StatusBadRequest)
	}
}

func TestOtokQualitySaveConfig_Validation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *OtokQualityConfigInput)
		reason string
	}{
		{"interval", func(in *OtokQualityConfigInput) { in.IntervalMinutes = 4 }, "INVALID_INTERVAL"},
		{"timeout", func(in *OtokQualityConfigInput) { in.TimeoutSeconds = 901 }, "INVALID_TIMEOUT"},
		{"concurrency", func(in *OtokQualityConfigInput) { in.Concurrency = 17 }, "INVALID_CONCURRENCY"},
		{"keep rounds", func(in *OtokQualityConfigInput) { in.KeepRounds = 51 }, "INVALID_KEEP_ROUNDS"},
		{"too many items", func(in *OtokQualityConfigInput) {
			for len(in.Items) <= otokQualityMaxItems {
				in.Items = append(in.Items, otokQualityValidItem())
			}
		}, "TOO_MANY_ITEMS"},
		{"empty name", func(in *OtokQualityConfigInput) { in.Items[0].Name = "  " }, "INVALID_NAME"},
		{"long name", func(in *OtokQualityConfigInput) { in.Items[0].Name = strings.Repeat("名", 65) }, "INVALID_NAME"},
		{"bad url", func(in *OtokQualityConfigInput) { in.Items[0].URL = "http://x/v1/messages/count_tokens" }, "INVALID_URL"},
		{"no key", func(in *OtokQualityConfigInput) { in.Items[0].APIKey = "" }, "API_KEY_REQUIRED"},
		{"key with space", func(in *OtokQualityConfigInput) { in.Items[0].APIKey = "sk test" }, "INVALID_API_KEY"},
		{"key with control", func(in *OtokQualityConfigInput) { in.Items[0].APIKey = "sk\x01test" }, "INVALID_API_KEY"},
		{"key too long", func(in *OtokQualityConfigInput) { in.Items[0].APIKey = strings.Repeat("k", 513) }, "INVALID_API_KEY"},
		{"no models", func(in *OtokQualityConfigInput) { in.Items[0].Models = []string{" "} }, "NO_MODELS"},
		{"bad model", func(in *OtokQualityConfigInput) { in.Items[0].Models = []string{"gpt 6"} }, "INVALID_MODEL"},
		{"too many models", func(in *OtokQualityConfigInput) { in.Items[0].Models = []string{"a", "b", "c", "d", "e", "f"} }, "TOO_MANY_MODELS"},
		{"bad effort", func(in *OtokQualityConfigInput) { in.Items[0].ReasoningEffort = "ultra" }, "INVALID_EFFORT"},
		{"no prompt", func(in *OtokQualityConfigInput) { in.Items[0].Prompt = " " }, "PROMPT_REQUIRED"},
		{"long prompt", func(in *OtokQualityConfigInput) { in.Items[0].Prompt = strings.Repeat("鹈", 8001) }, "PROMPT_TOO_LONG"},
		{"extra array", func(in *OtokQualityConfigInput) { in.Items[0].ExtraBody = `[1,2]` }, "INVALID_EXTRA_BODY"},
		{"extra broken", func(in *OtokQualityConfigInput) { in.Items[0].ExtraBody = `{"a":` }, "INVALID_EXTRA_BODY"},
		{"extra null", func(in *OtokQualityConfigInput) { in.Items[0].ExtraBody = `null` }, "INVALID_EXTRA_BODY"},
		{"extra too long", func(in *OtokQualityConfigInput) {
			in.Items[0].ExtraBody = `{"a":"` + strings.Repeat("x", 4000) + `"}`
		}, "INVALID_EXTRA_BODY"},
		{"unknown id", func(in *OtokQualityConfigInput) { in.Items[0].ID = 99 }, "ITEM_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newOtokQualityConfigService(t, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k1"})
			in := OtokQualityConfigInput{Items: []OtokQualityItemInput{otokQualityValidItem()}}
			tc.mutate(&in)
			_, err := svc.SaveConfig(context.Background(), in)
			requireOtokQualityReason(t, err, tc.reason, http.StatusBadRequest)
		})
	}
}

func TestOtokQualitySaveConfig_AllocatesIDsAndKeepsKey(t *testing.T) {
	enc := otokQualityEncryptorStub{keyID: "k1"}
	svc, repo := newOtokQualityConfigService(t, otokQualityTestConfig(), enc)
	ctx := context.Background()

	first := otokQualityValidItem()
	second := otokQualityValidItem()
	second.Name, second.URL, second.APIKey, second.ExtraBody = "Claude", "http://127.0.0.1:8080/v1/messages/", "sk-claude-abcdefgh", ` {"max_tokens": 4000} `
	second.ReasoningEffort = ""
	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Enabled: true, Items: []OtokQualityItemInput{first, second}})
	require.NoError(t, err)
	require.Equal(t, int64(1), out.Version)
	require.False(t, out.Invalid)
	require.Equal(t, 60, out.IntervalMinutes)
	require.Equal(t, 480, out.TimeoutSeconds)
	require.Equal(t, 2, out.Concurrency)
	require.Equal(t, 10, out.KeepRounds)
	require.Len(t, out.Items, 2)
	require.Equal(t, int64(1), out.Items[0].ID)
	require.Equal(t, int64(2), out.Items[1].ID)
	require.Equal(t, OtokQualityProtocolResponses, out.Items[0].Protocol)
	require.Equal(t, OtokQualityProtocolMessages, out.Items[1].Protocol)
	require.Equal(t, "http://127.0.0.1:8080/v1/messages", out.Items[1].URL)
	require.Equal(t, `{"max_tokens": 4000}`, out.Items[1].ExtraBody)
	require.True(t, out.Items[0].HasAPIKey)
	require.Equal(t, "sk-t***", out.Items[0].APIKeyMasked)
	require.False(t, out.Items[0].APIKeyDecryptFailed)

	raw := repo.values[SettingKeyOtokQuality]
	require.NotContains(t, raw, "sk-test-1234567890", "库里不存明文")
	var stored OtokQualityConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, int64(3), stored.NextItemID)
	firstEnc := stored.Items[0].APIKeyEnc
	require.NotEmpty(t, firstEnc)

	// 留空沿用旧密文；删掉第 1 条、新增一条，新条目 id 不复用。
	keep := second
	keep.ID, keep.APIKey = 2, ""
	third := otokQualityValidItem()
	third.Name = "新条目"
	out, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 1, Enabled: true, Items: []OtokQualityItemInput{third, keep}})
	require.NoError(t, err)
	require.Equal(t, int64(2), out.Version)
	require.Equal(t, int64(3), out.Items[0].ID)
	require.Equal(t, int64(2), out.Items[1].ID)
	require.Equal(t, "sk-c***", out.Items[1].APIKeyMasked)
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyOtokQuality]), &stored))
	require.Equal(t, int64(4), stored.NextItemID)
	plain, err := enc.Decrypt(stored.Items[1].APIKeyEnc)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-abcdefgh", plain)

	// 已删除的 id 不能再提交
	gone := otokQualityValidItem()
	gone.ID = 1
	_, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 2, Items: []OtokQualityItemInput{gone}})
	requireOtokQualityReason(t, err, "ITEM_NOT_FOUND", http.StatusBadRequest)

	// 同一 id 出现两次
	dup := keep
	_, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 2, Items: []OtokQualityItemInput{keep, dup}})
	requireOtokQualityReason(t, err, "ITEM_NOT_FOUND", http.StatusBadRequest)
}

func TestOtokQualitySaveConfig_VersionConflict(t *testing.T) {
	svc, _ := newOtokQualityConfigService(t, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k1"})
	ctx := context.Background()
	_, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Items: []OtokQualityItemInput{otokQualityValidItem()}})
	require.NoError(t, err)
	_, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 0})
	requireOtokQualityReason(t, err, "CONFIG_CONFLICT", http.StatusConflict)
	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 1})
	require.NoError(t, err)
	require.Equal(t, int64(2), out.Version)
	require.Empty(t, out.Items)
}

func TestOtokQualitySaveConfig_RequiresExplicitEncryptionKey(t *testing.T) {
	for name, cfg := range map[string]*config.Config{"nil config": nil, "auto generated": {}} {
		t.Run(name, func(t *testing.T) {
			svc, repo := newOtokQualityConfigService(t, cfg, otokQualityEncryptorStub{keyID: "k1"})
			_, err := svc.SaveConfig(context.Background(), OtokQualityConfigInput{Items: []OtokQualityItemInput{otokQualityValidItem()}})
			requireOtokQualityReason(t, err, "ENCRYPTION_KEY_REQUIRED", http.StatusBadRequest)
			require.Empty(t, repo.values)
		})
	}
}

func TestOtokQualityAdminConfig_DecryptFailureAndInvalidStored(t *testing.T) {
	svc, repo := newOtokQualityConfigService(t, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k1"})
	_, err := svc.SaveConfig(context.Background(), OtokQualityConfigInput{Items: []OtokQualityItemInput{otokQualityValidItem()}})
	require.NoError(t, err)

	// 换一把加密密钥
	other := NewOtokQualityService(NewSettingService(repo, nil), nil, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k2"}, true)
	out, err := other.AdminConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	require.True(t, out.Items[0].HasAPIKey)
	require.True(t, out.Items[0].APIKeyDecryptFailed)
	require.Empty(t, out.Items[0].APIKeyMasked)
	b, _ := json.Marshal(out)
	require.NotContains(t, string(b), "enc:k1")
	require.NotContains(t, string(b), "api_key_enc")
	require.NotContains(t, string(b), "next_item_id")

	// 已存内容读不出来：回退默认值并标记 invalid
	for _, raw := range []string{"{not json", `{"interval_minutes":3}`, `{"items":[{"id":0,"name":"x"}]}`} {
		repo.values[SettingKeyOtokQuality] = raw
		out, err = svc.AdminConfig(context.Background())
		require.NoError(t, err)
		require.True(t, out.Invalid, raw)
		require.False(t, out.Enabled)
		require.Empty(t, out.Items)
		require.Equal(t, int64(0), out.Version)
	}
	delete(repo.values, SettingKeyOtokQuality)
	out, err = svc.AdminConfig(context.Background())
	require.NoError(t, err)
	require.False(t, out.Invalid)

	repo.getErr = errors.New("db down")
	_, err = svc.AdminConfig(context.Background())
	require.Error(t, err)
}

func TestOtokQualityConfig_EnabledItems(t *testing.T) {
	cfg := DefaultOtokQualityConfig()
	cfg.Items = []OtokQualityItem{{ID: 1, Enabled: true}, {ID: 2}, {ID: 3, Enabled: true}}
	require.Empty(t, cfg.EnabledItems())
	cfg.Enabled = true
	got := cfg.EnabledItems()
	require.Len(t, got, 2)
	require.Equal(t, int64(3), got[1].ID)
	_, ok := cfg.ItemByID(2)
	require.True(t, ok)
	_, ok = cfg.ItemByID(9)
	require.False(t, ok)
}

func TestOtokQualityMaskKey(t *testing.T) {
	require.Equal(t, "***", otokQualityMaskKey("abcd"))
	require.Equal(t, "abcd***", otokQualityMaskKey("abcdefgh"))
}

// ---------- 请求体与请求头 ----------

func otokQualityBody(t *testing.T, req otokQualityRequest) map[string]any {
	t.Helper()
	raw, err := buildOtokQualityBody(req)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func TestBuildOtokQualityBody_Protocols(t *testing.T) {
	base := otokQualityRequest{Model: "m1", Prompt: "画鹈鹕", ReasoningEffort: "high"}

	r := base
	r.Protocol = OtokQualityProtocolResponses
	require.Equal(t, map[string]any{
		"model": "m1", "input": "画鹈鹕", "stream": true, "store": false,
		"reasoning": map[string]any{"effort": "high"},
	}, otokQualityBody(t, r))

	r.Protocol = OtokQualityProtocolChat
	require.Equal(t, map[string]any{
		"model": "m1", "stream": true, "reasoning_effort": "high",
		"messages":       []any{map[string]any{"role": "user", "content": "画鹈鹕"}},
		"stream_options": map[string]any{"include_usage": true},
	}, otokQualityBody(t, r))

	r.Protocol = OtokQualityProtocolMessages
	require.Equal(t, map[string]any{
		"model": "m1", "stream": true, "max_tokens": float64(16000),
		"messages": []any{map[string]any{"role": "user", "content": "画鹈鹕"}},
	}, otokQualityBody(t, r), "messages 忽略思考强度")

	r.Protocol, r.ReasoningEffort = OtokQualityProtocolResponses, ""
	_, has := otokQualityBody(t, r)["reasoning"]
	require.False(t, has, "不发送思考强度")

	r.Protocol = "unknown"
	_, err := buildOtokQualityBody(r)
	require.Error(t, err)
}

func TestBuildOtokQualityBody_MergeOrder(t *testing.T) {
	extra := `{"model":"evil","stream":false,"input":"x","messages":[],"max_output_tokens":100,"reasoning":{"effort":"low","summary":"auto"},"max_tokens":4000,"thinking":{"type":"enabled","budget_tokens":2000},"stream_options":{"include_usage":false,"foo":1},"temperature":0.2}`

	resp := otokQualityBody(t, otokQualityRequest{Protocol: OtokQualityProtocolResponses, Model: "m", Prompt: "p", ReasoningEffort: "high", ExtraBody: extra})
	require.Equal(t, "m", resp["model"])
	require.Equal(t, true, resp["stream"])
	require.Equal(t, "p", resp["input"])
	require.Equal(t, map[string]any{"effort": "low", "summary": "auto"}, resp["reasoning"], "extra_body 覆盖思考强度")
	require.Equal(t, float64(100), resp["max_output_tokens"])

	chat := otokQualityBody(t, otokQualityRequest{Protocol: OtokQualityProtocolChat, Model: "m", Prompt: "p", ExtraBody: extra})
	require.Equal(t, map[string]any{"include_usage": true, "foo": float64(1)}, chat["stream_options"])
	require.Equal(t, []any{map[string]any{"role": "user", "content": "p"}}, chat["messages"])
	require.Equal(t, 0.2, chat["temperature"])

	msg := otokQualityBody(t, otokQualityRequest{Protocol: OtokQualityProtocolMessages, Model: "m", Prompt: "p", ExtraBody: extra})
	require.Equal(t, float64(4000), msg["max_tokens"])
	require.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(2000)}, msg["thinking"])
	require.Equal(t, true, msg["stream"])
	require.Equal(t, "m", msg["model"])
}

func TestNewOtokQualityHTTPRequest_Headers(t *testing.T) {
	ctx := context.Background()
	req, err := newOtokQualityHTTPRequest(ctx, otokQualityRequest{URL: "http://x/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "sk-a", Model: "m", Prompt: "p"})
	require.NoError(t, err)
	require.Equal(t, "Bearer sk-a", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
	require.Empty(t, req.Header.Get("User-Agent"))
	require.Empty(t, req.Header.Get("X-Client-Request-ID"))
	require.Empty(t, req.Header.Get("x-api-key"))

	req, err = newOtokQualityHTTPRequest(ctx, otokQualityRequest{URL: "http://x/v1/messages", Protocol: OtokQualityProtocolMessages, APIKey: "sk-b", Model: "m", Prompt: "p"})
	require.NoError(t, err)
	require.Equal(t, "sk-b", req.Header.Get("x-api-key"))
	require.Equal(t, "2023-06-01", req.Header.Get("anthropic-version"))
	require.Empty(t, req.Header.Get("Authorization"))
}

// ---------- httptest 端到端 ----------

func otokQualitySSE(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: ")
		b.WriteString(e)
		b.WriteString("\n\n")
	}
	return b.String()
}

func otokQualityServer(t *testing.T, status int, contentType, body string, check func(r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if check != nil {
			check(r, m)
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func otokQualityDo(t *testing.T, srvURL, path, protocol, body string, status int) otokQualityResult {
	t.Helper()
	srv := otokQualityServer(t, status, "text/event-stream", body, nil)
	return doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + path, Protocol: protocol, APIKey: "k", Model: "m", Prompt: "p",
	})
}

func TestDoOtokQualityRequest_ResponsesSuccess(t *testing.T) {
	stream := ": keepalive\n\nevent: response.created\n" + otokQualitySSE(
		`{"type":"response.created","response":{"model":"gpt-6-astra"}}`,
		`{"type":"response.output_text.delta","delta":"`+"```html\\n<html><body>鹈鹕"+`"}`,
		`{"type":"response.output_text.delta","delta":"</body></html>\n`+"```"+`"}`,
		`{"type":"response.completed","response":{"model":"gpt-6-astra-2026","status":"completed","usage":{"input_tokens":120,"output_tokens":3000,"output_tokens_details":{"reasoning_tokens":1800}}}}`,
	)
	srv := otokQualityServer(t, http.StatusOK, "text/event-stream", stream, func(r *http.Request, body map[string]any) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		require.True(t, strings.HasPrefix(r.Header.Get("User-Agent"), "Go-http-client/"), "用 Go 默认 UA")
		require.Empty(t, r.Header.Get("X-Client-Request-ID"))
		require.Equal(t, "gpt-6-astra", body["model"])
		require.Equal(t, "画鹈鹕", body["input"])
	})
	res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + "/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "sk-test",
		Model: "gpt-6-astra", Prompt: "画鹈鹕", ReasoningEffort: "medium",
	})
	require.True(t, res.ok(), res.ErrMessage)
	require.Equal(t, 200, res.HTTPStatus)
	require.Equal(t, "```html\n<html><body>鹈鹕</body></html>\n```", res.Text)
	require.Equal(t, "gpt-6-astra-2026", res.ResponseModel)
	require.Equal(t, 120, *res.InputTokens)
	require.Equal(t, 3000, *res.OutputTokens)
	require.Equal(t, 1800, *res.ReasoningTokens)
	require.NotNil(t, res.FirstTokenMs)
	require.Equal(t, "<html><body>鹈鹕</body></html>", extractOtokQualityHTML(res.Text))
}

func TestDoOtokQualityRequest_ResponsesCompletedFallbackAndLongLine(t *testing.T) {
	long := strings.Repeat("a", 200<<10)
	completed := `{"type":"response.completed","response":{"model":"m","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"<svg>` + long + `</svg>"}]}]}}`
	res := otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(completed), 200)
	require.True(t, res.ok(), res.ErrMessage)
	require.Len(t, res.Text, len(long)+len("<svg></svg>"))
	require.Nil(t, res.FirstTokenMs)
}

type otokQualityCase struct {
	name      string
	status    int
	body      string
	code      string
	retryable bool
	msg       string
}

func runOtokQualityCases(t *testing.T, path, protocol string, cases []otokQualityCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := otokQualityDo(t, "", path, protocol, tc.body, tc.status)
			require.Equal(t, tc.code, res.ErrCode, res.ErrMessage)
			require.Equal(t, tc.retryable, res.Retryable)
			if tc.msg != "" {
				require.Equal(t, tc.msg, res.ErrMessage)
			}
		})
	}
}

func TestDoOtokQualityRequest_ResponsesFailures(t *testing.T) {
	runOtokQualityCases(t, "/v1/responses", OtokQualityProtocolResponses, []otokQualityCase{
		{"rate limited", 429, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, "http_429", true, "rate_limit_error: slow down"},
		{"bad request", 400, `{"error":{"message":"model not allowed","code":"model_not_allowed"}}`, "http_4xx", false, "model_not_allowed: model not allowed"},
		{"bad gateway", 502, `upstream exploded`, "http_5xx", true, "upstream exploded"},
		{"insufficient balance", 403, `{"error":{"message":"Insufficient account balance"}}`, "http_4xx", false, "Insufficient account balance"},
		{"response failed", 200, otokQualitySSE(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`), "response_failed", true, "server_error: boom"},
		{"response incomplete", 200, otokQualitySSE(`{"type":"response.output_text.delta","delta":"<html>"}`, `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`), "response_incomplete", false, "response incomplete: max_output_tokens"},
		{"error event", 200, otokQualitySSE(`{"type":"error","code":"invalid_prompt","message":"nope"}`), "stream_error", false, "nope"},
		{"stream interrupted", 200, otokQualitySSE(`{"type":"response.output_text.delta","delta":"<html>"}`), "stream_interrupted", true, ""},
		{"empty output", 200, otokQualitySSE(`{"type":"response.completed","response":{"output":[]}}`), "empty_output", false, "model returned no text"},
	})
}

func TestDoOtokQualityRequest_Chat(t *testing.T) {
	finish := `{"id":"c1","model":"gpt-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	usage := `{"id":"c1","model":"gpt-x","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":22,"completion_tokens_details":{"reasoning_tokens":7}}}`
	delta := func(s string) string {
		b, _ := json.Marshal(map[string]any{"model": "gpt-x", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": s}, "finish_reason": nil}}})
		return string(b)
	}

	t.Run("finish then usage then done", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/chat/completions", OtokQualityProtocolChat,
			otokQualitySSE(`{"choices":[{"delta":{"role":"assistant","content":null}}]}`, delta("<svg>"), delta("</svg>"), finish, usage, "[DONE]"), 200)
		require.True(t, res.ok(), res.ErrMessage)
		require.Equal(t, "<svg></svg>", res.Text)
		require.Equal(t, "gpt-x", res.ResponseModel)
		require.Equal(t, 11, *res.InputTokens)
		require.Equal(t, 22, *res.OutputTokens)
		require.Equal(t, 7, *res.ReasoningTokens)
		require.NotNil(t, res.FirstTokenMs)
	})
	t.Run("finish then eof", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/chat/completions", OtokQualityProtocolChat, otokQualitySSE(delta("hello"), finish), 200)
		require.True(t, res.ok(), res.ErrMessage)
		require.Equal(t, "hello", res.Text)
		require.Nil(t, res.InputTokens)
	})
	t.Run("done without finish", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/chat/completions", OtokQualityProtocolChat, otokQualitySSE(delta("hello"), "[DONE]"), 200)
		require.True(t, res.ok(), res.ErrMessage)
	})
	runOtokQualityCases(t, "/v1/chat/completions", OtokQualityProtocolChat, []otokQualityCase{
		{"length", 200, otokQualitySSE(delta("<html>"), `{"choices":[{"delta":{},"finish_reason":"length"}]}`, usage, "[DONE]"), "response_incomplete", false, "response incomplete: length"},
		{"interrupted", 200, otokQualitySSE(delta("<html>")), "stream_interrupted", true, ""},
		{"empty", 200, otokQualitySSE(finish, "[DONE]"), "empty_output", false, ""},
		{"error frame", 200, otokQualitySSE(delta("x"), `{"error":{"message":"upstream overloaded","type":"upstream_error"}}`), "stream_error", true, "upstream_error: upstream overloaded"},
	})
}

func TestDoOtokQualityRequest_Messages(t *testing.T) {
	start := `{"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":0}}}`
	text := func(s string) string {
		b, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": s}})
		return string(b)
	}
	thinking := `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`
	msgDelta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":321,"output_tokens":45}}`
	stop := `{"type":"message_stop"}`

	t.Run("success", func(t *testing.T) {
		body := "event: message_start\n" + otokQualitySSE(start) + "event: ping\n" + otokQualitySSE(`{"type":"ping"}`) +
			otokQualitySSE(thinking, text("<svg>"), text("</svg>"), msgDelta, stop)
		res := otokQualityDo(t, "", "/v1/messages", OtokQualityProtocolMessages, body, 200)
		require.True(t, res.ok(), res.ErrMessage)
		require.Equal(t, "<svg></svg>", res.Text, "thinking_delta 不计入正文")
		require.Equal(t, "claude-x", res.ResponseModel)
		require.Equal(t, 321, *res.InputTokens, "message_delta 的输入 token 覆盖 message_start")
		require.Equal(t, 45, *res.OutputTokens)
		require.Nil(t, res.ReasoningTokens)
	})
	t.Run("input tokens kept when delta has zero", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/messages", OtokQualityProtocolMessages, otokQualitySSE(
			`{"type":"message_start","message":{"model":"c","usage":{"input_tokens":50}}}`, text("hi"),
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":3}}`, stop), 200)
		require.True(t, res.ok(), res.ErrMessage)
		require.Equal(t, 50, *res.InputTokens)
	})
	runOtokQualityCases(t, "/v1/messages", OtokQualityProtocolMessages, []otokQualityCase{
		{"max tokens", 200, otokQualitySSE(start, text("<html>"), `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":16000}}`, stop), "response_incomplete", false, "response incomplete: max_tokens"},
		{"interrupted", 200, otokQualitySSE(start, text("<html>")), "stream_interrupted", true, ""},
		{"empty", 200, otokQualitySSE(start, msgDelta, stop), "empty_output", false, ""},
		{"event error", 200, otokQualitySSE(start) + "event: error\ndata: {\"message\":\"stream_read_error: upstream closed\"}\n\n", "stream_error", true, "stream_read_error: upstream closed"},
		{"type error", 200, otokQualitySSE(start, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`), "stream_error", true, "overloaded_error: Overloaded"},
		{"top-level error", 200, otokQualitySSE(start, `{"error":{"type":"invalid_request_error","message":"bad"}}`), "stream_error", false, "invalid_request_error: bad"},
		{"top-level string error", 200, otokQualitySSE(start, `{"error":"api_error happened"}`), "stream_error", true, "api_error happened"},
	})
}

func TestDoOtokQualityRequest_HTTPErrorsAreGrouped(t *testing.T) {
	re := regexp.MustCompile(`^http_\d{3}$`)
	for _, status := range []int{301, 302, 307, 400, 401, 403, 404, 413, 429, 500, 502, 503, 504} {
		srv := otokQualityServer(t, status, "application/json", `{"error":{"message":"x"}}`, nil)
		res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
			URL: srv.URL + "/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "k", Model: "m", Prompt: "p",
		})
		require.Equal(t, status, res.HTTPStatus)
		if status != 429 {
			require.False(t, re.MatchString(res.ErrCode), "%d -> %s", status, res.ErrCode)
		}
	}
}

func TestDoOtokQualityRequest_DoesNotFollowRedirect(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/responses", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + "/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "k", Model: "m", Prompt: "p",
	})
	require.Equal(t, "http_3xx", res.ErrCode)
	require.False(t, res.Retryable)
	require.Equal(t, http.StatusTemporaryRedirect, res.HTTPStatus)
	require.Contains(t, res.ErrMessage, target.URL)
	require.Zero(t, hits)
}

func TestDoOtokQualityRequest_UnexpectedResponse(t *testing.T) {
	srv := otokQualityServer(t, 200, "text/html; charset=utf-8", "<!DOCTYPE html><html><head><title>Sub2API</title></head></html>", nil)
	res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + "/responses", Protocol: OtokQualityProtocolResponses, APIKey: "k", Model: "m", Prompt: "p",
	})
	require.Equal(t, "unexpected_response", res.ErrCode)
	require.False(t, res.Retryable)
	require.Contains(t, res.ErrMessage, "text/html")
	require.Contains(t, res.ErrMessage, "<title>Sub2API</title>")
}

func TestDoOtokQualityRequest_RedactsKeyInErrors(t *testing.T) {
	key := "sk-very-secret-123456"
	srv := otokQualityServer(t, 401, "application/json", `{"error":{"message":"Invalid API key: `+key+`"}}`, nil)
	res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + "/v1/chat/completions", Protocol: OtokQualityProtocolChat, APIKey: key, Model: "m", Prompt: "p",
	})
	require.Equal(t, "http_4xx", res.ErrCode)
	require.Equal(t, 401, res.HTTPStatus)
	require.NotContains(t, res.ErrMessage, key)
	require.Contains(t, res.ErrMessage, "Invalid API key: ***")

	require.Equal(t, "short abc", otokQualityRedactKey("short abc", "abc"), "短 key 不打码")
}

func TestDoOtokQualityRequest_TimeoutAndRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, otokQualitySSE(`{"type":"response.output_text.delta","delta":"<html>"}`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res := doOtokQualityRequest(ctx, newOtokQualityHTTPClient(), otokQualityRequest{
		URL: srv.URL + "/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "k", Model: "m", Prompt: "p",
	})
	require.Equal(t, "timeout", res.ErrCode)
	require.False(t, res.Retryable, "超时不重试")
	require.Equal(t, "<html>", res.Text)

	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	res = doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
		URL: url + "/v1/responses", Protocol: OtokQualityProtocolResponses, APIKey: "k", Model: "m", Prompt: "p",
	})
	require.Contains(t, []string{"connection_refused", "network_error"}, res.ErrCode)
	require.True(t, res.Retryable)
}

func TestNewOtokQualityHTTPClient_NoProxy(t *testing.T) {
	c := newOtokQualityHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, tr.Proxy)
	require.NotNil(t, c.CheckRedirect)
}

// ---------- HTML 提取 ----------

func TestExtractOtokQualityHTML(t *testing.T) {
	doc := "<!DOCTYPE html>\n<html><body><svg></svg></body></html>"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "  ", ""},
		{"html fence", "说明\n```html\n" + doc + "\n```\n结束", doc},
		{"last html fence wins", "```html\n<html>old</html>\n```\n```html\n" + doc + "\n```", doc},
		{"unlabeled fence with html", "```\n" + doc + "\n```", doc},
		{"css fence ignored", "```css\nbody{}\n```\n" + doc, doc},
		{"bare document", "Here you go:\n" + doc + "\nEnjoy", doc},
		{"html tag without doctype", "x <html lang=\"zh\"><body>hi</body></html> y", "<html lang=\"zh\"><body>hi</body></html>"},
		{"unterminated fence", "```html\n" + doc, doc},
		{"truncated document", "<!DOCTYPE html><html><body><svg>", "<!DOCTYPE html><html><body><svg>"},
		{"plain text", "抱歉，我无法完成。", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, extractOtokQualityHTML(tc.in))
		})
	}
	got := extractOtokQualityHTML("看这个：<svg viewBox=\"0 0 10 10\"><circle r=\"5\"/></svg>")
	require.True(t, strings.HasPrefix(got, "<!DOCTYPE html>"))
	require.Contains(t, got, "<svg viewBox=\"0 0 10 10\"><circle r=\"5\"/></svg>")
}

func TestTruncateOtokQualityBytes(t *testing.T) {
	require.Equal(t, "abc", truncateOtokQualityBytes("abc", 10))
	got := truncateOtokQualityBytes("鹈鹕骑车", 7)
	require.Equal(t, "鹈鹕", got)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, "ab…", truncateOtokQualityRunes("abcd", 2))
}

// ---------- 调度辅助 ----------

func TestOtokQualityDispatchRespectsConcurrency(t *testing.T) {
	svc, _ := newOtokQualityConfigService(t, nil, nil)
	svc.concurrency.Store(2)
	svc.queue = make([]otokQualityJob, 5)
	svc.active = 2
	svc.dispatch()
	require.Len(t, svc.queue, 5, "已满并发时不再出队")
	require.Equal(t, 2, svc.active)
}

func TestOtokQualityRunNow_DisabledInstance(t *testing.T) {
	svc, _ := newOtokQualityConfigService(t, nil, nil)
	_, err := svc.RunNow(context.Background(), 1)
	requireOtokQualityReason(t, err, "DISABLED", http.StatusServiceUnavailable)
}

func TestOtokQualitySaveConfig_DropsQueuedJobsOfRemovedItems(t *testing.T) {
	svc, _ := newOtokQualityConfigService(t, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k1"})
	ctx := context.Background()
	two := []OtokQualityItemInput{otokQualityValidItem(), otokQualityValidItem()}
	_, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Items: two})
	require.NoError(t, err)
	svc.queue = []otokQualityJob{{ItemID: 1}, {ItemID: 2}, {ItemID: 1}}
	keep := otokQualityValidItem()
	keep.ID, keep.APIKey = 2, ""
	_, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: 1, Items: []OtokQualityItemInput{keep}})
	require.NoError(t, err)
	require.Equal(t, []otokQualityJob{{ItemID: 2}}, svc.queue)
}

// ---------- 脏字节、打码边界、响应体上限 ----------

func requireOtokQualityClean(t *testing.T, res otokQualityResult) {
	t.Helper()
	for name, s := range map[string]string{"ErrMessage": res.ErrMessage, "Text": res.Text, "ResponseModel": res.ResponseModel} {
		require.True(t, utf8.ValidString(s), "%s 不是合法 UTF-8: %q", name, s)
		require.NotContains(t, s, "\x00", name)
	}
}

func TestDoOtokQualityRequest_SanitizesDirtyBytes(t *testing.T) {
	gbk := "\xc4\xe3\xba\xc3\xa3\xac\xb7\xfe\xce\xf1\xc6\xf7\xb4\xed\xce\xf3" // GBK「你好，服务器错误」
	cases := []struct {
		name, path, protocol, contentType, body string
		status                                  int
		code                                    string
	}{
		{"gbk error page", "/v1/responses", OtokQualityProtocolResponses, "text/html; charset=gbk", "<html>" + gbk + "</html>", 502, "http_5xx"},
		{"json message with nul", "/v1/responses", OtokQualityProtocolResponses, "application/json", `{"error":{"message":"bad\u0000input"}}`, 400, "http_4xx"},
		{"binary 2xx body", "/v1/responses", OtokQualityProtocolResponses, "application/octet-stream", "\x00\x01\x02\xff\xfePK\x03\x04", 200, "unexpected_response"},
		{"non-utf8 error frame", "/v1/messages", OtokQualityProtocolMessages, "text/event-stream", "event: error\ndata: \xff\xfe broken " + gbk + "\n\n", 200, "stream_error"},
		{"nul in delta and model", "/v1/responses", OtokQualityProtocolResponses, "text/event-stream", otokQualitySSE(
			`{"type":"response.output_text.delta","delta":"<html>a\u0000b</html>"}`,
			`{"type":"response.completed","response":{"model":"m\u0000x","status":"completed"}}`), 200, ""},
		{"nul in chat delta", "/v1/chat/completions", OtokQualityProtocolChat, "text/event-stream", otokQualitySSE(
			`{"model":"g\u0000","choices":[{"delta":{"content":"x\u0000y"},"finish_reason":"stop"}]}`, "[DONE]"), 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := otokQualityServer(t, tc.status, tc.contentType, tc.body, nil)
			res := doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
				URL: srv.URL + tc.path, Protocol: tc.protocol, APIKey: "sk-dirty-bytes-key", Model: "m", Prompt: "p",
			})
			require.Equal(t, tc.code, res.ErrCode, res.ErrMessage)
			requireOtokQualityClean(t, res)
			if tc.code == "" {
				require.NotEmpty(t, res.Text)
				require.NotEmpty(t, res.ResponseModel)
			}
		})
	}
}

func TestDoOtokQualityRequest_ResponsesCompletedButIncomplete(t *testing.T) {
	runOtokQualityCases(t, "/v1/responses", OtokQualityProtocolResponses, []otokQualityCase{
		{"status incomplete", 200, otokQualitySSE(`{"type":"response.output_text.delta","delta":"<html>"}`,
			`{"type":"response.completed","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`),
			"response_incomplete", false, "response incomplete: max_output_tokens"},
		{"reason only", 200, otokQualitySSE(`{"type":"response.output_text.delta","delta":"<html>"}`,
			`{"type":"response.completed","response":{"status":"completed","incomplete_details":{"reason":"content_filter"}}}`),
			"response_incomplete", false, "response incomplete: content_filter"},
	})
	t.Run("keeps text", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(
			`{"type":"response.output_text.delta","delta":"<html>"}`,
			`{"type":"response.completed","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`), 200)
		require.Equal(t, "<html>", res.Text)
	})
	t.Run("falls back to output text", func(t *testing.T) {
		res := otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(
			`{"type":"response.completed","response":{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"<svg>"}]}]}}`), 200)
		require.Equal(t, "response_incomplete", res.ErrCode)
		require.False(t, res.Retryable)
		require.Equal(t, "<svg>", res.Text)
	})
}

func TestDoOtokQualityRequest_RedactsKeyAcrossTruncation(t *testing.T) {
	const key = "sk-ABCDEFGH-secret-0123456789"
	pad := strings.Repeat("x", 1989) // key 从第 1990 个字符开始，截断到 2000 字符时只剩前 11 位
	do := func(t *testing.T, status int, contentType, path, protocol, body string) otokQualityResult {
		t.Helper()
		srv := otokQualityServer(t, status, contentType, body, nil)
		return doOtokQualityRequest(context.Background(), newOtokQualityHTTPClient(), otokQualityRequest{
			URL: srv.URL + path, Protocol: protocol, APIKey: key, Model: "m", Prompt: "p",
		})
	}
	cases := []struct {
		name string
		res  func(t *testing.T) otokQualityResult
	}{
		{"plain error body", func(t *testing.T) otokQualityResult {
			return do(t, 401, "text/plain", "/v1/responses", OtokQualityProtocolResponses, pad+key)
		}},
		{"json error body", func(t *testing.T) otokQualityResult {
			return do(t, 401, "application/json", "/v1/responses", OtokQualityProtocolResponses, `{"error":{"message":"`+pad+key+`"}}`)
		}},
		{"unexpected html head", func(t *testing.T) otokQualityResult {
			return do(t, 200, "text/html", "/v1/responses", OtokQualityProtocolResponses, strings.Repeat("y", 500)+key+strings.Repeat("z", 100))
		}},
		{"stream error frame", func(t *testing.T) otokQualityResult {
			return do(t, 200, "text/event-stream", "/v1/messages", OtokQualityProtocolMessages, "event: error\ndata: "+pad+key+"\n\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.res(t)
			require.NotEmpty(t, res.ErrCode)
			require.NotContains(t, res.ErrMessage, key[:8], res.ErrMessage[max(0, len(res.ErrMessage)-40):])
			require.Contains(t, res.ErrMessage, "***")
		})
	}

	require.Equal(t, "invalid *** here", otokQualityRedactKey("invalid "+key+" here", key))
	require.Equal(t, "abc ***…", otokQualityRedactKey("abc "+key[:6]+"…", key))
	require.Equal(t, "abc sk-A…", otokQualityRedactKey("abc sk-A…", key), "4 位及以下的前缀不处理")
}

func TestDoOtokQualityRequest_ResponseBodyLimit(t *testing.T) {
	old := otokQualityMaxResponseBytes
	otokQualityMaxResponseBytes = 4096
	t.Cleanup(func() { otokQualityMaxResponseBytes = old })

	var events []string
	for i := 0; i < 100; i++ {
		events = append(events, `{"type":"response.output_text.delta","delta":"`+strings.Repeat("a", 100)+`"}`)
	}
	events = append(events, `{"type":"response.completed","response":{"status":"completed"}}`)
	res := otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(events...), 200)
	require.Equal(t, "response_incomplete", res.ErrCode)
	require.Equal(t, "response body exceeded 32MB", res.ErrMessage)
	require.False(t, res.Retryable)

	res = otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(events[0], events[len(events)-1]), 200)
	require.True(t, res.ok(), res.ErrMessage)

	res = otokQualityDo(t, "", "/v1/responses", OtokQualityProtocolResponses, otokQualitySSE(events[0]), 200)
	require.Equal(t, "stream_interrupted", res.ErrCode, "没撞上限的中断照旧")
	require.True(t, res.Retryable)
}
