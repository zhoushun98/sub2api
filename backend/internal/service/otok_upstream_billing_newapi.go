package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// otok: 上游声明倍率探测适配 new-api 上游。
//
// new-api 没有 /v1/sub2api/billing，也没有任何接口能凭令牌直接返回分组倍率。
// 只凭令牌能拿到倍率的是 GET /api/log/token：返回该令牌最近最多 1000 条日志，
// 消费日志（type=2）带实际使用的分组（auto 已解析为具体分组），other.group_ratio
// 是最终扣费倍率（用户分组 × 使用分组的特殊倍率已替换进去）。
// 这里取最近一条消费日志的倍率，合成为 sub2api.key_billing 响应交回原探测流程，
// 校验、快照、写回与调度全部复用上游代码。

const (
	otokNewAPILogTypeConsume = 2
	// 1000 条日志实测约 850KB；只读到第一条消费日志就停，上限只是兜底。
	otokNewAPILogMaxBodyBytes = 8 << 20

	otokNewAPIReasonNoUsageLog = "newapi_no_usage_log"
)

type otokNewAPIBillingTarget struct {
	baseURL    string
	apiKey     string
	proxyURL   string
	profile    HTTPUpstreamProfile
	tlsProfile *tlsfingerprint.Profile
}

// otokNewAPIBillingResult：done 表示失败已落库、原流程直接返回 snapshot/err；
// body 非空表示识别成功，原流程把它当作 200 响应继续解析；两者都没有则按原逻辑记 unsupported。
type otokNewAPIBillingResult struct {
	body     []byte
	snapshot *UpstreamBillingProbeSnapshot
	err      error
	done     bool
}

type otokNewAPILogEntry struct {
	Type      int             `json:"type"`
	CreatedAt int64           `json:"created_at"`
	Group     string          `json:"group"`
	Other     json.RawMessage `json:"other"`
}

type otokNewAPILogRate struct {
	ratio      float64
	special    bool
	observedAt time.Time
}

var errOtokNewAPINotRecognized = errors.New("not a new-api log response")

func (s *UpstreamBillingProbeService) otokNewAPIBillingFallback(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	target otokNewAPIBillingTarget,
) otokNewAPIBillingResult {
	if statusCode != http.StatusNotFound && statusCode != http.StatusMethodNotAllowed {
		return otokNewAPIBillingResult{}
	}
	fail := func(status int, reason string, delay time.Duration) otokNewAPIBillingResult {
		snapshot, err := s.persistProbeFailure(ctx, account, intervalMinutes, now, status, reason, delay)
		return otokNewAPIBillingResult{snapshot: snapshot, err: err, done: true}
	}
	logURL, err := otokNewAPILogTokenURL(target.baseURL)
	if err != nil {
		return otokNewAPIBillingResult{}
	}
	reqCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, logURL, bytes.NewReader(nil))
	if err != nil {
		return fail(0, "request_build_failed", 0)
	}
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), target.profile)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.apiKey)
	account.ApplyHeaderOverrides(req.Header)
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, target.proxyURL, account.ID, account.Concurrency, target.tlsProfile)
	if err != nil {
		return fail(0, "request_failed", 0)
	}
	if resp == nil || resp.Body == nil {
		return fail(0, "empty_response", 0)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return otokNewAPIBillingResult{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(resp.StatusCode, "http_error", retryAfter(resp.Header, now))
	}
	rate, err := otokFindNewAPILogRate(io.LimitReader(resp.Body, otokNewAPILogMaxBodyBytes))
	switch {
	case errors.Is(err, errOtokNewAPINotRecognized):
		return otokNewAPIBillingResult{}
	case err != nil:
		return fail(resp.StatusCode, "invalid_response", retryAfter(resp.Header, now))
	case rate == nil:
		return fail(resp.StatusCode, otokNewAPIReasonNoUsageLog, retryAfter(resp.Header, now))
	}
	observedAt := rate.observedAt
	if observedAt.IsZero() {
		observedAt = now
	}
	payload := map[string]any{
		"object":                    "sub2api.key_billing",
		"schema_version":            1,
		"billing_scope":             "token",
		"group_rate_multiplier":     rate.ratio,
		"resolved_rate_multiplier":  rate.ratio,
		"effective_rate_multiplier": rate.ratio,
		"peak_rate_enabled":         false,
		"observed_at":               observedAt.UTC().Format(time.RFC3339Nano),
	}
	if rate.special {
		payload["user_rate_multiplier"] = rate.ratio
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fail(resp.StatusCode, "invalid_response", 0)
	}
	return otokNewAPIBillingResult{body: body}
}

// otokNewAPILogTokenURL 由账号 base_url 推出站点根：去掉末尾的 /v1、/v1beta 再拼 /api/log/token。
func otokNewAPILogTokenURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base url")
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/v1beta", "/v1"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	parsed.Path = path + "/api/log/token"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// otokFindNewAPILogRate 流式读取 {"data":[...], ...}，返回第一条（最新）带倍率的消费日志。
// 不是这个结构时返回 errOtokNewAPINotRecognized；结构对但没有消费日志时返回 nil, nil。
func otokFindNewAPILogRate(r io.Reader) (*otokNewAPILogRate, error) {
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errOtokNewAPINotRecognized
	}
	sawData := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, errOtokNewAPINotRecognized
		}
		if key, _ := keyTok.(string); key != "data" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, errOtokNewAPINotRecognized
			}
			continue
		}
		tok, err := dec.Token()
		if err != nil {
			return nil, errOtokNewAPINotRecognized
		}
		if tok == nil {
			// 令牌没有任何日志时 new-api 返回 "data": null。
			sawData = true
			continue
		}
		if tok != json.Delim('[') {
			return nil, errOtokNewAPINotRecognized
		}
		sawData = true
		for dec.More() {
			var entry otokNewAPILogEntry
			if err := dec.Decode(&entry); err != nil {
				return nil, err
			}
			if rate, ok := entry.consumeRate(); ok {
				return rate, nil
			}
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
	}
	if !sawData {
		return nil, errOtokNewAPINotRecognized
	}
	return nil, nil
}

func (e otokNewAPILogEntry) consumeRate() (*otokNewAPILogRate, bool) {
	if e.Type != otokNewAPILogTypeConsume || strings.TrimSpace(e.Group) == "" {
		return nil, false
	}
	// other 在 new-api 里是 JSON 字符串；也兼容直接下发对象的变体。
	raw := []byte(e.Other)
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = []byte(encoded)
	}
	var other struct {
		GroupRatio     *float64 `json:"group_ratio"`
		UserGroupRatio *float64 `json:"user_group_ratio"`
	}
	if err := json.Unmarshal(raw, &other); err != nil || other.GroupRatio == nil {
		return nil, false
	}
	ratio := *other.GroupRatio
	if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil, false
	}
	rate := &otokNewAPILogRate{
		ratio:   ratio,
		special: other.UserGroupRatio != nil && *other.UserGroupRatio >= 0,
	}
	if e.CreatedAt > 0 {
		rate.observedAt = time.Unix(e.CreatedAt, 0).UTC()
	}
	return rate, true
}
