//go:build unit

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------- 用户端（服务桩） ----------

type otokQualitySvcStub struct {
	cfg    service.OtokQualityConfig
	rounds map[int64][]service.OtokQualityAdminRound
	runs   map[int64]*service.OtokQualityAdminRunDetail
	listed []int64
}

func (s *otokQualitySvcStub) EnabledItems(context.Context) ([]service.OtokQualityItem, service.OtokQualityConfig, error) {
	return s.cfg.EnabledItems(), s.cfg, nil
}
func (s *otokQualitySvcStub) ListRounds(_ context.Context, itemID int64) ([]service.OtokQualityAdminRound, error) {
	s.listed = append(s.listed, itemID)
	return s.rounds[itemID], nil
}
func (s *otokQualitySvcStub) GetRun(_ context.Context, runID int64) (*service.OtokQualityAdminRunDetail, error) {
	if r, ok := s.runs[runID]; ok {
		return r, nil
	}
	return nil, service.ErrOtokQualityRunNotFound
}
func (s *otokQualitySvcStub) AdminConfig(context.Context) (service.OtokQualityAdminConfig, error) {
	return service.OtokQualityAdminConfig{}, nil
}
func (s *otokQualitySvcStub) SaveConfig(context.Context, service.OtokQualityConfigInput) (service.OtokQualityAdminConfig, error) {
	return service.OtokQualityAdminConfig{}, nil
}
func (s *otokQualitySvcStub) Status(context.Context) (service.OtokQualityStatus, error) {
	return service.OtokQualityStatus{}, nil
}
func (s *otokQualitySvcStub) RunNow(context.Context, int64) (int64, error) { return 9, nil }
func (s *otokQualitySvcStub) PreviewURL(runID int64) string {
	return "/api/v1/otok/quality/preview/" + strconv.FormatInt(runID, 10) + "?token=good"
}
func (s *otokQualitySvcStub) VerifyPreviewToken(_ int64, token string) bool { return token == "good" }

func otokQualityRouter(svc otokQualityService, authed bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &OtokQualityHandler{svc: svc}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authed {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		}
		c.Next()
	})
	r.GET("/items", h.Items)
	r.GET("/items/:id/rounds", h.Rounds)
	r.GET("/runs/:id", h.Run)
	r.GET("/preview/:id", h.Preview)
	r.GET("/admin/config", h.AdminGetConfig)
	r.PUT("/admin/config", h.AdminUpdateConfig)
	r.GET("/admin/items/:id/rounds", h.AdminRounds)
	r.GET("/admin/runs/:id", h.AdminRun)
	r.POST("/admin/run-now", h.AdminRunNow)
	return r
}

func otokQualityDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

const otokQualitySecretURL = "http://secret-upstream.example.com/v1/responses"

func otokQualityStubWithData() *otokQualitySvcStub {
	cfg := service.DefaultOtokQualityConfig()
	cfg.Enabled = true
	cfg.Items = []service.OtokQualityItem{
		{ID: 1, Name: "OpenAI", Enabled: true, URL: otokQualitySecretURL, APIKeyEnc: "enc-secret", Models: []string{"gpt-6-astra"}, ExtraBody: `{"secret_extra":1}`},
		{ID: 2, Name: "停用", Enabled: false, URL: otokQualitySecretURL, Models: []string{"m"}},
	}
	status := 401
	started := time.Now()
	summary := service.OtokQualityAdminRunSummary{
		OtokQualityRunSummary: service.OtokQualityRunSummary{ID: 10, Model: "gpt-6-astra", Status: "failed", ErrorCode: "http_4xx", StartedAt: &started},
		ErrorMessage:          "Invalid key at " + otokQualitySecretURL,
		HTTPStatus:            &status,
		ResponseModel:         "secret-response-model",
		Protocol:              service.OtokQualityProtocolResponses,
	}
	round := func(itemID int64) service.OtokQualityAdminRound {
		return service.OtokQualityAdminRound{
			OtokQualityRoundInfo: service.OtokQualityRoundInfo{ID: itemID * 100, ItemID: itemID, Trigger: "schedule", StartedAt: started},
			Prompt:               "画鹈鹕",
			Runs:                 []service.OtokQualityAdminRunSummary{summary},
		}
	}
	return otokQualityStubWithDataT{cfg: cfg, round: round, summary: summary}.build()
}

type otokQualityStubWithDataT struct {
	cfg     service.OtokQualityConfig
	round   func(int64) service.OtokQualityAdminRound
	summary service.OtokQualityAdminRunSummary
}

func (b otokQualityStubWithDataT) build() *otokQualitySvcStub {
	detail := func(id, itemID int64) *service.OtokQualityAdminRunDetail {
		s := b.summary
		s.ID = id
		return &service.OtokQualityAdminRunDetail{OtokQualityAdminRunSummary: s, RoundID: itemID * 100, ItemID: itemID, OutputText: "<svg/>", OutputHTML: "<svg/>"}
	}
	return &otokQualitySvcStub{
		cfg:    b.cfg,
		rounds: map[int64][]service.OtokQualityAdminRound{1: {b.round(1)}, 2: {b.round(2)}, 7: {b.round(7)}},
		runs:   map[int64]*service.OtokQualityAdminRunDetail{10: detail(10, 1), 20: detail(20, 2), 70: detail(70, 7)},
	}
}

var otokQualityExactHTTPCode = regexp.MustCompile(`"error_code":"http_\d{3}"`)

func requireOtokQualityUserSafe(t *testing.T, body string) {
	t.Helper()
	for _, banned := range []string{"error_message", "http_status", "response_model", `"protocol"`, "secret-upstream", "secret-response-model", "enc-secret", "secret_extra", `"url"`, "api_key", `"prompt"`, "画鹈鹕"} {
		require.NotContains(t, body, banned)
	}
	require.False(t, otokQualityExactHTTPCode.MatchString(body), body)
}

func TestOtokQualityHandler_ItemsOnlyVisible(t *testing.T) {
	svc := otokQualityStubWithData()
	w := otokQualityDo(otokQualityRouter(svc, true), http.MethodGet, "/items", "")
	require.Equal(t, http.StatusOK, w.Code)
	requireOtokQualityUserSafe(t, w.Body.String())
	var resp struct {
		Data struct {
			Items []struct {
				ID     int64    `json:"id"`
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"items"`
			IntervalMinutes int `json:"interval_minutes"`
			KeepRounds      int `json:"keep_rounds"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Items, 1)
	require.Equal(t, int64(1), resp.Data.Items[0].ID)
	require.Equal(t, "OpenAI", resp.Data.Items[0].Name)
	require.Equal(t, []string{"gpt-6-astra"}, resp.Data.Items[0].Models)
	require.Equal(t, 60, resp.Data.IntervalMinutes)
	require.Equal(t, 10, resp.Data.KeepRounds)

	svc.cfg.Enabled = false
	w = otokQualityDo(otokQualityRouter(svc, true), http.MethodGet, "/items", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"items":[]`)
}

func TestOtokQualityHandler_RequiresAuth(t *testing.T) {
	w := otokQualityDo(otokQualityRouter(otokQualityStubWithData(), false), http.MethodGet, "/items", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOtokQualityHandler_UserRoundsAndRun(t *testing.T) {
	svc := otokQualityStubWithData()
	r := otokQualityRouter(svc, true)

	w := otokQualityDo(r, http.MethodGet, "/items/1/rounds", "")
	require.Equal(t, http.StatusOK, w.Code)
	requireOtokQualityUserSafe(t, w.Body.String())
	require.Contains(t, w.Body.String(), `"error_code":"http_4xx"`)

	w = otokQualityDo(r, http.MethodGet, "/runs/10", "")
	require.Equal(t, http.StatusOK, w.Code)
	requireOtokQualityUserSafe(t, w.Body.String())
	require.Contains(t, w.Body.String(), `"output_html":"\u003csvg/\u003e"`)
	require.Contains(t, w.Body.String(), `"round_id":100`)
	require.Contains(t, w.Body.String(), `"preview_url":"/api/v1/otok/quality/preview/10?token=good"`)

	for _, path := range []string{"/items/2/rounds", "/items/7/rounds", "/runs/20", "/runs/70", "/runs/404"} {
		w := otokQualityDo(r, http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, w.Code, path)
		require.NotContains(t, w.Body.String(), "svg", path)
	}
	require.Equal(t, http.StatusBadRequest, otokQualityDo(r, http.MethodGet, "/items/abc/rounds", "").Code)
	require.Equal(t, []int64{1}, svc.listed)

	// 总开关关闭后全部 404
	svc.cfg.Enabled = false
	require.Equal(t, http.StatusNotFound, otokQualityDo(r, http.MethodGet, "/items/1/rounds", "").Code)
	require.Equal(t, http.StatusNotFound, otokQualityDo(r, http.MethodGet, "/runs/10", "").Code)
	// 条目停用后 404
	svc.cfg.Enabled = true
	svc.cfg.Items[0].Enabled = false
	require.Equal(t, http.StatusNotFound, otokQualityDo(r, http.MethodGet, "/items/1/rounds", "").Code)
	require.Equal(t, http.StatusNotFound, otokQualityDo(r, http.MethodGet, "/runs/10", "").Code)
}

func TestOtokQualityHandler_AdminSeesDetailsWithoutFiltering(t *testing.T) {
	svc := otokQualityStubWithData()
	r := otokQualityRouter(svc, true)
	w := otokQualityDo(r, http.MethodGet, "/admin/items/2/rounds", "")
	require.Equal(t, http.StatusOK, w.Code)
	for _, want := range []string{`"error_message"`, `"http_status":401`, `"response_model":"secret-response-model"`, `"protocol":"responses"`} {
		require.Contains(t, w.Body.String(), want)
	}
	require.Contains(t, w.Body.String(), `"prompt":"画鹈鹕"`)
	w = otokQualityDo(r, http.MethodGet, "/admin/runs/20", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"error_message"`)
	require.Contains(t, w.Body.String(), `"preview_url":"/api/v1/otok/quality/preview/20?token=good"`)
	require.Empty(t, svc.runs[20].PreviewURL, "不应改动服务返回的对象")
	require.Equal(t, http.StatusNotFound, otokQualityDo(r, http.MethodGet, "/admin/runs/404", "").Code)

	require.Equal(t, http.StatusBadRequest, otokQualityDo(r, http.MethodPost, "/admin/run-now", `{}`).Code)
	w = otokQualityDo(r, http.MethodPost, "/admin/run-now", `{"item_id":1}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"round_id":9`)
}

func TestOtokQualityHandler_Preview(t *testing.T) {
	svc := otokQualityStubWithData()
	svc.runs[11] = &service.OtokQualityAdminRunDetail{ItemID: 1, OutputText: "纯文本"}
	r := otokQualityRouter(svc, false) // 预览不走登录

	w := otokQualityDo(r, http.MethodGet, "/preview/10?token=good", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "<!DOCTYPE html>\n<svg/>", w.Body.String()) // 没有 doctype 时补上
	require.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	csp := w.Header().Get("Content-Security-Policy")
	require.Equal(t, service.OtokQualityPreviewCSP, csp)
	for _, want := range []string{"sandbox allow-scripts", "frame-ancestors 'self'", "connect-src 'none'", "default-src 'none'"} {
		require.Contains(t, csp, want)
	}
	require.NotContains(t, csp, "allow-same-origin")
	require.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Contains(t, w.Header().Get("Cache-Control"), "no-transform")

	// 套上站点的安全中间件：它先写全站 CSP 和 X-Frame-Options: DENY，预览接口要能覆盖掉。
	gin.SetMode(gin.TestMode)
	withHeaders := gin.New()
	withHeaders.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: config.DefaultCSPPolicy}, nil))
	withHeaders.GET("/preview/:id", (&OtokQualityHandler{svc: svc}).Preview)
	w = otokQualityDo(withHeaders, http.MethodGet, "/preview/10?token=good", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{service.OtokQualityPreviewCSP}, w.Header().Values("Content-Security-Policy"))
	require.Equal(t, []string{"SAMEORIGIN"}, w.Header().Values("X-Frame-Options"))

	for _, path := range []string{"/preview/10", "/preview/10?token=bad", "/preview/404?token=good", "/preview/11?token=good", "/preview/abc?token=good"} {
		w := otokQualityDo(r, http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, w.Code, path)
		require.Empty(t, w.Body.String(), path)
	}
}

// ---------- 管理端配置（真实服务） ----------

type otokQualityHandlerSettingRepo struct{ values map[string]string }

func (r *otokQualityHandlerSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, errors.New("unused")
}
func (r *otokQualityHandlerSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}
func (r *otokQualityHandlerSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *otokQualityHandlerSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *otokQualityHandlerSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *otokQualityHandlerSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *otokQualityHandlerSettingRepo) Delete(context.Context, string) error { return nil }

type otokQualityHandlerEncryptor struct{ keyID string }

func (e otokQualityHandlerEncryptor) Encrypt(plain string) (string, error) {
	return "enc:" + e.keyID + ":" + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}
func (e otokQualityHandlerEncryptor) Decrypt(c string) (string, error) {
	p := "enc:" + e.keyID + ":"
	if !strings.HasPrefix(c, p) {
		return "", errors.New("decrypt failed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c, p))
	return string(raw), err
}

func otokQualityRealService(repo *otokQualityHandlerSettingRepo, keyID string) *service.OtokQualityService {
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	return service.NewOtokQualityService(service.NewSettingService(repo, nil), nil, cfg, otokQualityHandlerEncryptor{keyID: keyID}, true)
}

const otokQualityPlainKey = "sk-plaintext-key-123456"

func TestOtokQualityHandler_AdminConfigNeverEchoesKey(t *testing.T) {
	repo := &otokQualityHandlerSettingRepo{values: map[string]string{}}
	r := otokQualityRouter(otokQualityRealService(repo, "k1"), true)

	require.Equal(t, http.StatusBadRequest, otokQualityDo(r, http.MethodPut, "/admin/config", "{bad").Code)
	put := `{"version":0,"enabled":true,"interval_minutes":30,"items":[{"id":0,"name":"本站 OpenAI","enabled":true,"url":"http://127.0.0.1:8080/v1/responses","api_key":"` + otokQualityPlainKey + `","models":["gpt-6-astra"],"reasoning_effort":"medium","prompt":"画鹈鹕","extra_body":""}]}`
	w := otokQualityDo(r, http.MethodPut, "/admin/config", put)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	putBody := w.Body.String()
	require.NotContains(t, putBody, otokQualityPlainKey)
	require.NotContains(t, putBody, "enc:k1")
	require.NotContains(t, putBody, "api_key_enc")
	require.NotContains(t, putBody, "next_item_id")
	require.Contains(t, repo.values[service.SettingKeyOtokQuality], "enc:k1", "库里存密文")

	w = otokQualityDo(r, http.MethodGet, "/admin/config", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, putBody, w.Body.String(), "PUT 返回体与 GET 相同")
	var resp struct {
		Data service.OtokQualityAdminConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, int64(1), resp.Data.Version)
	require.False(t, resp.Data.Invalid)
	require.Len(t, resp.Data.Items, 1)
	it := resp.Data.Items[0]
	require.Equal(t, int64(1), it.ID)
	require.Equal(t, "responses", it.Protocol)
	require.True(t, it.HasAPIKey)
	require.Equal(t, "sk-p***", it.APIKeyMasked)
	require.False(t, it.APIKeyDecryptFailed)

	// 版本过期返回 409
	w = otokQualityDo(r, http.MethodPut, "/admin/config", put)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "OTOK_QUALITY_CONFIG_CONFLICT")

	// 换一把加密密钥后仍能打开后台
	r2 := otokQualityRouter(otokQualityRealService(repo, "k2"), true)
	w = otokQualityDo(r2, http.MethodGet, "/admin/config", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Data.Items[0].HasAPIKey)
	require.True(t, resp.Data.Items[0].APIKeyDecryptFailed)
	require.Empty(t, resp.Data.Items[0].APIKeyMasked)
	require.NotContains(t, w.Body.String(), "enc:k1")
}

func TestOtokQualityAuditBodyRedactsItemKeys(t *testing.T) {
	body := `{"version":3,"items":[{"id":1,"name":"a","url":"http://127.0.0.1:8080/v1/responses","api_key":"` + otokQualityPlainKey + `","extra_body":"{\"max_output_tokens\":100}"}]}`
	out := service.RedactAuditBody([]byte(body), "application/json")
	require.NotContains(t, out, otokQualityPlainKey)
	require.Contains(t, out, "http://127.0.0.1:8080/v1/responses", "url 原样记录")
	require.Contains(t, out, "max_output_tokens", "extra_body 原样记录")
	var parsed struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	require.Equal(t, "***", parsed.Items[0]["api_key"])
}
