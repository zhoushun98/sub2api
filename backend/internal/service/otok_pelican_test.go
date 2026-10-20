//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// ---------- 配置 ----------

func TestNormalizeOtokPelicanConfig_Defaults(t *testing.T) {
	cfg, err := NormalizeOtokPelicanConfig(OtokPelicanConfig{DefaultModels: []string{" gpt-6-astra ", "gpt-6-astra", ""}})
	require.NoError(t, err)
	require.Equal(t, 10, cfg.IntervalMinutes)
	require.Equal(t, "medium", cfg.ReasoningEffort)
	require.Equal(t, OtokPelicanDefaultPrompt, cfg.Prompt)
	require.Equal(t, 480, cfg.TimeoutSeconds)
	require.Equal(t, 2, cfg.Concurrency)
	require.Equal(t, 10, cfg.KeepRounds)
	require.Equal(t, []string{"gpt-6-astra"}, cfg.DefaultModels)
	require.Empty(t, cfg.Groups)
}

func TestNormalizeOtokPelicanConfig_Validation(t *testing.T) {
	base := func() OtokPelicanConfig { return OtokPelicanConfig{DefaultModels: []string{"gpt-6-astra"}} }
	cases := map[string]func(c *OtokPelicanConfig){
		"interval too small": func(c *OtokPelicanConfig) { c.IntervalMinutes = 4 },
		"interval too large": func(c *OtokPelicanConfig) { c.IntervalMinutes = 1441 },
		"bad effort":         func(c *OtokPelicanConfig) { c.ReasoningEffort = "ultra" },
		"timeout too small":  func(c *OtokPelicanConfig) { c.TimeoutSeconds = 30 },
		"concurrency":        func(c *OtokPelicanConfig) { c.Concurrency = 5 },
		"keep rounds":        func(c *OtokPelicanConfig) { c.KeepRounds = 51 },
		"no models":          func(c *OtokPelicanConfig) { c.DefaultModels = nil },
		"bad model":          func(c *OtokPelicanConfig) { c.DefaultModels = []string{"gpt 6"} },
		"too many models":    func(c *OtokPelicanConfig) { c.DefaultModels = []string{"a", "b", "c", "d", "e", "f"} },
		"prompt too long":    func(c *OtokPelicanConfig) { c.Prompt = strings.Repeat("鹈", 8001) },
		"bad group id":       func(c *OtokPelicanConfig) { c.Groups = []OtokPelicanGroupConfig{{GroupID: 0}} },
		"bad group model": func(c *OtokPelicanConfig) {
			c.Groups = []OtokPelicanGroupConfig{{GroupID: 1, Models: []string{"bad model"}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			_, err := NormalizeOtokPelicanConfig(c)
			require.Error(t, err)
		})
	}
}

func TestOtokPelicanConfig_EnabledGroupsAndModels(t *testing.T) {
	cfg, err := NormalizeOtokPelicanConfig(OtokPelicanConfig{
		Enabled:       true,
		DefaultModels: []string{"gpt-6-astra"},
		Groups: []OtokPelicanGroupConfig{
			{GroupID: 127, Enabled: true},
			{GroupID: 127, Enabled: false}, // 重复的分组以第一条为准
			{GroupID: 147, Enabled: false},
			{GroupID: 273, Enabled: true, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}},
		},
	})
	require.NoError(t, err)
	enabled := cfg.EnabledGroups()
	require.Len(t, enabled, 2)
	require.Equal(t, []string{"gpt-6-astra"}, cfg.ModelsFor(enabled[0]))
	require.Equal(t, []string{"gpt-6-astra", "gpt-6.1-sol"}, cfg.ModelsFor(enabled[1]))

	cfg.Enabled = false
	require.Empty(t, cfg.EnabledGroups())
}

func TestLoadOtokPelicanConfig_FallsBackToDefaults(t *testing.T) {
	repo := &otokPelicanSettingRepoStub{values: map[string]string{SettingKeyOtokPelican: "{not json"}}
	cfg, err := loadOtokPelicanConfig(context.Background(), repo)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Equal(t, []string{otokPelicanDefaultModel}, cfg.DefaultModels)

	repo.getErr = errors.New("db down")
	_, err = loadOtokPelicanConfig(context.Background(), repo)
	require.Error(t, err)
}

// ---------- 请求与 SSE ----------

func otokPelicanSSE(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("event: x\ndata: ")
		b.WriteString(e)
		b.WriteString("\n\n")
	}
	return b.String()
}

func otokPelicanServer(t *testing.T, status int, body string, check func(r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if check != nil {
			check(r, m)
		}
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoOtokPelicanProbe_Success(t *testing.T) {
	stream := ": keepalive\n\n" + otokPelicanSSE(
		`{"type":"response.created","response":{"model":"gpt-6-astra"}}`,
		`{"type":"response.output_text.delta","delta":"`+"```html\\n<html><body>鹈鹕"+`"}`,
		`{"type":"response.output_text.delta","delta":"</body></html>\n`+"```"+`"}`,
		`{"type":"response.completed","response":{"model":"gpt-6-astra-2026","status":"completed","usage":{"input_tokens":120,"output_tokens":3000,"output_tokens_details":{"reasoning_tokens":1800}}}}`,
	)
	srv := otokPelicanServer(t, http.StatusOK, stream, func(r *http.Request, body map[string]any) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		require.Equal(t, otokPelicanUserAgent, r.Header.Get("User-Agent"))
		require.Equal(t, "otok-pelican-1-1", r.Header.Get("X-Client-Request-ID"))
		require.Equal(t, "gpt-6-astra", body["model"])
		require.Equal(t, true, body["stream"])
		require.Equal(t, false, body["store"])
		require.Equal(t, "画鹈鹕", body["input"])
		require.Equal(t, map[string]any{"effort": "medium"}, body["reasoning"])
	})
	res := doOtokPelicanProbe(context.Background(), newOtokPelicanHTTPClient(), srv.URL, "sk-test", otokPelicanProbeRequest{
		Model: "gpt-6-astra", Prompt: "画鹈鹕", ReasoningEffort: "medium", RequestID: "otok-pelican-1-1",
	})
	require.True(t, res.ok(), res.ErrMessage)
	require.Equal(t, 200, res.HTTPStatus)
	require.Equal(t, "```html\n<html><body>鹈鹕</body></html>\n```", res.Text)
	require.Equal(t, "gpt-6-astra-2026", res.ResponseModel)
	require.Equal(t, 120, *res.InputTokens)
	require.Equal(t, 3000, *res.OutputTokens)
	require.Equal(t, 1800, *res.ReasoningTokens)
	require.NotNil(t, res.FirstTokenMs)
	require.Equal(t, "<html><body>鹈鹕</body></html>", extractOtokPelicanHTML(res.Text))
}

func TestDoOtokPelicanProbe_CompletedFallbackAndLongLine(t *testing.T) {
	long := strings.Repeat("a", 200<<10) // 超过 bufio.Scanner 默认 64KB 的单行
	completed := `{"type":"response.completed","response":{"model":"m","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"<svg>` + long + `</svg>"}]}]}}`
	srv := otokPelicanServer(t, http.StatusOK, otokPelicanSSE(completed), nil)
	res := doOtokPelicanProbe(context.Background(), newOtokPelicanHTTPClient(), srv.URL, "k", otokPelicanProbeRequest{Model: "m", Prompt: "p"})
	require.True(t, res.ok(), res.ErrMessage)
	require.Len(t, res.Text, len(long)+len("<svg></svg>"))
	require.Nil(t, res.FirstTokenMs)
}

func TestDoOtokPelicanProbe_Failures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
		msg       string
	}{
		{"rate limited", 429, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, "http_429", true, "slow down"},
		{"bad request", 400, `{"error":{"message":"model not allowed","code":"model_not_allowed"}}`, "http_400", false, "model_not_allowed: model not allowed"},
		{"bad gateway", 502, `upstream exploded`, "http_502", true, "upstream exploded"},
		{"insufficient balance", 403, `{"error":{"message":"Insufficient account balance"}}`, "http_403", false, "Insufficient account balance"},
		{"response failed", 200, otokPelicanSSE(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`), "response_failed", true, "server_error: boom"},
		{"response incomplete", 200, otokPelicanSSE(`{"type":"response.output_text.delta","delta":"<html>"}`, `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`), "response_incomplete", false, "response incomplete: max_output_tokens"},
		{"error event", 200, otokPelicanSSE(`{"type":"error","code":"invalid_prompt","message":"nope"}`), "stream_error", false, "nope"},
		{"stream interrupted", 200, otokPelicanSSE(`{"type":"response.output_text.delta","delta":"<html>"}`), "stream_interrupted", true, "stream ended before response.completed"},
		{"empty output", 200, otokPelicanSSE(`{"type":"response.completed","response":{"output":[]}}`), "empty_output", false, "model returned no text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := otokPelicanServer(t, tc.status, tc.body, nil)
			res := doOtokPelicanProbe(context.Background(), newOtokPelicanHTTPClient(), srv.URL, "k", otokPelicanProbeRequest{Model: "m", Prompt: "p"})
			require.False(t, res.ok())
			require.Equal(t, tc.code, res.ErrCode)
			require.Equal(t, tc.retryable, res.Retryable)
			require.Equal(t, tc.msg, res.ErrMessage)
		})
	}
}

func TestDoOtokPelicanProbe_TimeoutAndRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, otokPelicanSSE(`{"type":"response.output_text.delta","delta":"<html>"}`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res := doOtokPelicanProbe(ctx, newOtokPelicanHTTPClient(), srv.URL, "k", otokPelicanProbeRequest{Model: "m", Prompt: "p"})
	require.Equal(t, "timeout", res.ErrCode)
	require.True(t, res.Retryable)
	require.Equal(t, "<html>", res.Text)

	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	res = doOtokPelicanProbe(context.Background(), newOtokPelicanHTTPClient(), url, "k", otokPelicanProbeRequest{Model: "m", Prompt: "p"})
	require.Contains(t, []string{"connection_refused", "network_error"}, res.ErrCode)
	require.True(t, res.Retryable)
}

func TestOtokPelicanBaseURL(t *testing.T) {
	require.Equal(t, "http://127.0.0.1:8080", otokPelicanBaseURL(nil))
	cfg := &config.Config{}
	cfg.Server.Host, cfg.Server.Port = "0.0.0.0", 9000
	require.Equal(t, "http://127.0.0.1:9000", otokPelicanBaseURL(cfg))
	cfg.Server.Host = "10.0.0.5"
	require.Equal(t, "http://10.0.0.5:9000", otokPelicanBaseURL(cfg))
	cfg.Server.Host = "::"
	require.Equal(t, "http://127.0.0.1:9000", otokPelicanBaseURL(cfg))
	t.Setenv("OTOK_PELICAN_BASE_URL", "http://gateway:8080/")
	require.Equal(t, "http://gateway:8080", otokPelicanBaseURL(cfg))
}

// ---------- HTML 提取 ----------

func TestExtractOtokPelicanHTML(t *testing.T) {
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
			require.Equal(t, tc.want, extractOtokPelicanHTML(tc.in))
		})
	}

	got := extractOtokPelicanHTML("看这个：<svg viewBox=\"0 0 10 10\"><circle r=\"5\"/></svg>")
	require.True(t, strings.HasPrefix(got, "<!DOCTYPE html>"))
	require.Contains(t, got, "<svg viewBox=\"0 0 10 10\"><circle r=\"5\"/></svg>")

	got = extractOtokPelicanHTML("```svg\n<svg><rect/></svg>\n```")
	require.Contains(t, got, "<body>\n<svg><rect/></svg>")
}

func TestTruncateOtokPelicanBytes(t *testing.T) {
	require.Equal(t, "abc", truncateOtokPelicanBytes("abc", 10))
	got := truncateOtokPelicanBytes("鹈鹕骑车", 7) // 每个汉字 3 字节，7 落在第 3 个字中间
	require.Equal(t, "鹈鹕", got)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, "ab…", truncateOtokPelicanRunes("abcd", 2))
}

// ---------- 内部账号 ----------

func newOtokPelicanProvisionService(t *testing.T) (*OtokPelicanService, *otokPelicanUserRepoStub, *otokPelicanSettingRepoStub) {
	t.Helper()
	users := &otokPelicanUserRepoStub{users: map[int64]*User{}, byEmail: map[string]*User{}}
	settings := &otokPelicanSettingRepoStub{values: map[string]string{}}
	svc := NewOtokPelicanService(NewSettingService(settings, nil), nil, nil, users, nil, nil, nil, nil, true)
	return svc, users, settings
}

func TestEnsureProbeUser_CreatesOnceAndRemembersID(t *testing.T) {
	svc, users, settings := newOtokPelicanProvisionService(t)
	u, err := svc.ensureProbeUser(context.Background())
	require.NoError(t, err)
	require.Len(t, users.created, 1)
	require.Equal(t, otokPelicanProbeEmail, u.Email)
	require.Equal(t, RoleUser, u.Role)
	require.Equal(t, StatusActive, u.Status)
	require.Equal(t, otokPelicanProbeBalance, u.Balance)
	require.NotEmpty(t, u.PasswordHash)
	require.Equal(t, "1000", settings.values[settingKeyOtokPelicanProbeUserID])

	again, err := svc.ensureProbeUser(context.Background())
	require.NoError(t, err)
	require.Equal(t, u.ID, again.ID)
	require.Len(t, users.created, 1)
}

func TestEnsureProbeUser_TopsUpAndRespectsDisabled(t *testing.T) {
	svc, users, _ := newOtokPelicanProvisionService(t)
	users.users[7] = &User{ID: 7, Email: otokPelicanProbeEmail, Status: StatusActive, Balance: 0.5}
	users.byEmail[otokPelicanProbeEmail] = users.users[7]
	u, err := svc.ensureProbeUser(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(7), u.ID)
	require.Equal(t, []float64{99.5}, users.adjusted)
	require.InDelta(t, 100, u.Balance, 1e-9)

	users.users[7].Status = StatusDisabled
	_, err = svc.ensureProbeUser(context.Background())
	require.ErrorContains(t, err, "disabled")
}

func TestEnsureProbeKey_GrantsExclusiveSetsRateAndReusesKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	svc, users, _ := newOtokPelicanProvisionService(t)
	rates := &otokPelicanRateRepoStub{rates: map[int64]float64{}}
	svc.db, svc.rateRepo = db, rates

	mock.ExpectQuery(`SELECT key FROM api_keys`).WithArgs(int64(5), int64(273)).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("sk-existing"))
	user := &User{ID: 5, Status: StatusActive}
	key, err := svc.ensureProbeKey(context.Background(), user, &Group{ID: 273, IsExclusive: true})
	require.NoError(t, err)
	require.Equal(t, "sk-existing", key)
	require.Equal(t, []int64{273}, users.allowed)
	require.Equal(t, otokPelicanProbeRate, rates.rates[273])
	require.Equal(t, 1, rates.syncs)

	// 已授权、倍率已对：不再重复写
	mock.ExpectQuery(`SELECT key FROM api_keys`).WithArgs(int64(5), int64(273)).
		WillReturnRows(sqlmock.NewRows([]string{"key"}).AddRow("sk-existing"))
	_, err = svc.ensureProbeKey(context.Background(), user, &Group{ID: 273, IsExclusive: true})
	require.NoError(t, err)
	require.Len(t, users.allowed, 1)
	require.Equal(t, 1, rates.syncs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------- 调度辅助 ----------

func TestOtokPelicanDispatchRespectsConcurrency(t *testing.T) {
	svc, _, _ := newOtokPelicanProvisionService(t)
	svc.concurrency.Store(2)
	svc.queue = make([]otokPelicanJob, 5)
	svc.active = 2
	svc.dispatch()
	require.Len(t, svc.queue, 5, "已满并发时不再出队")
	require.Equal(t, 2, svc.active)
}
