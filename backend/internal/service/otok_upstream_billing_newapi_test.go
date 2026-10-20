package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// otokNewAPIProbeStub 按 URL path 返回响应，未登记的 path 一律 404。
type otokNewAPIProbeStub struct {
	mu       sync.Mutex
	routes   map[string]func() *http.Response
	requests []*http.Request
}

func (u *otokNewAPIProbeStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.requests = append(u.requests, req)
	route := u.routes[req.URL.Path]
	u.mu.Unlock()
	if route == nil {
		return otokNewAPITestResponse(http.StatusNotFound, nil, `{"error":{"message":"Invalid URL","type":"invalid_request_error"}}`), nil
	}
	return route(), nil
}

func (u *otokNewAPIProbeStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *otokNewAPIProbeStub) paths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	paths := make([]string, 0, len(u.requests))
	for _, req := range u.requests {
		paths = append(paths, req.URL.Path)
	}
	return paths
}

func otokNewAPITestResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

// otokNewAPITestLog 生成一条 new-api 日志，other 按 new-api 的习惯编码成 JSON 字符串。
func otokNewAPITestLog(t *testing.T, logType int, createdAt int64, group string, other map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(other)
	require.NoError(t, err)
	return map[string]any{
		"id":         createdAt,
		"type":       logType,
		"created_at": createdAt,
		"group":      group,
		"model_name": "gpt-6-astra",
		"content":    strings.Repeat("x", 64),
		"other":      string(encoded),
	}
}

func otokNewAPITestLogBody(t *testing.T, logs []map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": logs, "message": "", "success": true})
	require.NoError(t, err)
	return string(body)
}

func newOtokNewAPIProbeTest(t *testing.T, baseURL string, routes map[string]func() *http.Response) (*UpstreamBillingProbeService, *Account, *otokNewAPIProbeStub, time.Time) {
	t.Helper()
	account := &Account{
		ID:          101,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-newapi", "base_url": baseURL},
		Extra:       map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &otokNewAPIProbeStub{routes: routes}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	now := time.Date(2026, time.October, 20, 14, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return svc, account, upstream, now
}

func TestOtokNewAPIBillingProbeUsesLatestConsumeLog(t *testing.T) {
	logs := []map[string]any{
		otokNewAPITestLog(t, 5, 1792504100, "gpt混池", map[string]any{"status_code": 500}),
		otokNewAPITestLog(t, 2, 1792504062, "gpt混池", map[string]any{"group_ratio": 0.045, "user_group_ratio": -1, "billing_mode": "tiered_expr"}),
		otokNewAPITestLog(t, 2, 1792500000, "plus", map[string]any{"group_ratio": 0.18, "user_group_ratio": -1}),
	}
	svc, account, upstream, now := newOtokNewAPIProbeTest(t, "https://newapi.example/v1", map[string]func() *http.Response{
		"/api/log/token": func() *http.Response {
			return otokNewAPITestResponse(http.StatusOK, nil, otokNewAPITestLogBody(t, logs))
		},
	})
	account.Extra[UpstreamBillingRateSyncEnabledExtraKey] = true

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, http.StatusOK, snapshot.HTTPStatus)
	require.Equal(t, 0.045, snapshot.Data["group_rate_multiplier"])
	require.Equal(t, 0.045, snapshot.Data["resolved_rate_multiplier"])
	require.Equal(t, 0.045, snapshot.Data["effective_rate_multiplier"])
	require.Equal(t, false, snapshot.Data["peak_rate_enabled"])
	require.NotContains(t, snapshot.Data, "user_rate_multiplier")
	require.Equal(t, time.Unix(1792504062, 0).UTC().Format(time.RFC3339Nano), snapshot.Data["observed_at"])
	require.Equal(t, now, *snapshot.ReceivedAt)

	rate, ok := upstreamBillingRateAt(snapshot.Data, now)
	require.True(t, ok)
	require.Equal(t, 0.045, rate)
	require.NotNil(t, account.RateMultiplier)
	require.Equal(t, 0.045, *account.RateMultiplier)

	require.Equal(t, []string{"/v1/sub2api/billing", "/api/log/token"}, upstream.paths())
	logReq := upstream.requests[1]
	require.Equal(t, "newapi.example", logReq.URL.Host)
	require.Equal(t, "Bearer sk-newapi", logReq.Header.Get("Authorization"))
}

func TestOtokNewAPIBillingProbeSpecialGroupRatio(t *testing.T) {
	logs := []map[string]any{
		otokNewAPITestLog(t, 2, 1792504062, "plus", map[string]any{"group_ratio": 0.12, "user_group_ratio": 0.12}),
	}
	svc, account, _, _ := newOtokNewAPIProbeTest(t, "https://newapi.example", map[string]func() *http.Response{
		"/api/log/token": func() *http.Response {
			return otokNewAPITestResponse(http.StatusOK, nil, otokNewAPITestLogBody(t, logs))
		},
	})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.12, snapshot.Data["user_rate_multiplier"])
	require.Equal(t, 0.12, snapshot.Data["resolved_rate_multiplier"])
}

func TestOtokNewAPIBillingProbeStreamsLargeLogResponse(t *testing.T) {
	logs := make([]map[string]any, 0, 1000)
	for i := 0; i < 999; i++ {
		logs = append(logs, otokNewAPITestLog(t, 5, int64(1792504000-i), "gpt混池", map[string]any{"status_code": 502}))
	}
	logs = append(logs, otokNewAPITestLog(t, 2, 1792400000, "gpt混池", map[string]any{"group_ratio": 0.045, "user_group_ratio": -1}))
	body := otokNewAPITestLogBody(t, logs)
	require.Greater(t, len(body), upstreamBillingProbeMaxBodyBytes)
	svc, account, _, _ := newOtokNewAPIProbeTest(t, "https://newapi.example", map[string]func() *http.Response{
		"/api/log/token": func() *http.Response { return otokNewAPITestResponse(http.StatusOK, nil, body) },
	})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.045, snapshot.Data["resolved_rate_multiplier"])
}

func TestOtokNewAPIBillingProbeWithoutConsumeLogFails(t *testing.T) {
	for name, body := range map[string]string{
		"only_error_logs": otokNewAPITestLogBody(t, []map[string]any{
			otokNewAPITestLog(t, 5, 1792504100, "gpt混池", map[string]any{"status_code": 500}),
		}),
		"null_data": `{"data":null,"message":"","success":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc, account, _, now := newOtokNewAPIProbeTest(t, "https://newapi.example", map[string]func() *http.Response{
				"/api/log/token": func() *http.Response { return otokNewAPITestResponse(http.StatusOK, nil, body) },
			})

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
			require.Equal(t, otokNewAPIReasonNoUsageLog, snapshot.LastError)
			// 普通失败间隔，不是 unsupported 的 8 倍退避。
			require.False(t, snapshot.NextProbeAt.After(now.Add(36*time.Minute)))
		})
	}
}

func TestOtokNewAPIBillingProbeNonNewAPIStaysUnsupported(t *testing.T) {
	for name, route := range map[string]func() *http.Response{
		"log_404": nil,
		"spa_html": func() *http.Response {
			return otokNewAPITestResponse(http.StatusOK, nil, "<!doctype html><html></html>")
		},
		"other_json": func() *http.Response {
			return otokNewAPITestResponse(http.StatusOK, nil, `{"success":true,"items":[]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			routes := map[string]func() *http.Response{}
			if route != nil {
				routes["/api/log/token"] = route
			}
			svc, account, upstream, now := newOtokNewAPIProbeTest(t, "https://other.example/v1", routes)

			snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status)
			require.Equal(t, http.StatusNotFound, snapshot.HTTPStatus)
			require.False(t, snapshot.NextProbeAt.Before(now.Add(192*time.Minute)))
			require.Equal(t, []string{"/v1/sub2api/billing", "/api/log/token"}, upstream.paths())
		})
	}
}

func TestOtokNewAPIBillingProbeRateLimitedHonorsRetryAfter(t *testing.T) {
	svc, account, _, now := newOtokNewAPIProbeTest(t, "https://newapi.example", map[string]func() *http.Response{
		"/api/log/token": func() *http.Response {
			return otokNewAPITestResponse(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"7200"}}, "")
		},
	})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "http_error", snapshot.LastError)
	require.Equal(t, http.StatusTooManyRequests, snapshot.HTTPStatus)
	require.Equal(t, now.Add(2*time.Hour), snapshot.NextProbeAt)
}

func TestOtokNewAPIBillingProbeSkipsFallbackForSub2APIUpstream(t *testing.T) {
	svc, account, upstream, _ := newOtokNewAPIProbeTest(t, "https://sub2api.example", map[string]func() *http.Response{
		"/v1/sub2api/billing": func() *http.Response {
			return otokNewAPITestResponse(http.StatusOK, nil, `{
				"object":"sub2api.key_billing",
				"schema_version":1,
				"billing_scope":"token",
				"group_rate_multiplier":0.049,
				"resolved_rate_multiplier":0.049,
				"peak_rate_enabled":false,
				"effective_rate_multiplier":0.049,
				"observed_at":"2026-10-20T13:40:34Z"
			}`)
		},
	})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, 0.049, snapshot.Data["resolved_rate_multiplier"])
	require.Equal(t, []string{"/v1/sub2api/billing"}, upstream.paths())
}

func TestOtokNewAPILogTokenURL(t *testing.T) {
	for baseURL, want := range map[string]string{
		"https://newapi.example":              "https://newapi.example/api/log/token",
		"https://newapi.example/":             "https://newapi.example/api/log/token",
		"https://newapi.example/v1":           "https://newapi.example/api/log/token",
		"https://newapi.example/v1/":          "https://newapi.example/api/log/token",
		"https://newapi.example/v1beta":       "https://newapi.example/api/log/token",
		"https://newapi.example/relay/v1?x=1": "https://newapi.example/relay/api/log/token",
	} {
		got, err := otokNewAPILogTokenURL(baseURL)
		require.NoError(t, err, baseURL)
		require.Equal(t, want, got, baseURL)
	}
	_, err := otokNewAPILogTokenURL("not a url")
	require.Error(t, err)
}
