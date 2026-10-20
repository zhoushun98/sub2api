//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type otokContactHandlerRepoStub struct{ values map[string]string }

func (r *otokContactHandlerRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}
func (r *otokContactHandlerRepoStub) Get(context.Context, string) (*service.Setting, error) {
	return nil, errors.New("unused")
}
func (r *otokContactHandlerRepoStub) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *otokContactHandlerRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *otokContactHandlerRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *otokContactHandlerRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *otokContactHandlerRepoStub) Delete(context.Context, string) error { return nil }

func otokContactHandlerRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	repo := &otokContactHandlerRepoStub{values: map[string]string{}}
	h := NewOtokContactHandler(service.NewOtokContactService(service.NewSettingService(repo, nil)))
	r := gin.New()
	r.GET("/contact", h.GetSummary)
	r.GET("/contact/qrcodes", h.GetQRCodes)
	r.PUT("/admin/contact", h.AdminUpdate)
	return r
}

func otokContactDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestOtokContactHandlerFlow(t *testing.T) {
	r := otokContactHandlerRouter()

	rec := otokContactDo(r, http.MethodGet, "/contact", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"enabled":false`)

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	qr := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	body, _ := json.Marshal(map[string]string{"email": "admin@otokapi.com", "qq_group": "123456", "qq_group_qr": qr})

	rec = otokContactDo(r, http.MethodPut, "/admin/contact", string(body))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = otokContactDo(r, http.MethodGet, "/contact", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"enabled":true`)
	require.Contains(t, rec.Body.String(), `"has_qq_group_qr":true`)
	require.NotContains(t, rec.Body.String(), "data:image", "摘要不应带图片")

	rec = otokContactDo(r, http.MethodGet, "/contact/qrcodes", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), qr)
}

func TestOtokContactHandlerRejectsInvalid(t *testing.T) {
	r := otokContactHandlerRouter()

	rec := otokContactDo(r, http.MethodPut, "/admin/contact", `{"qq_group":"abc"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "OTOK_CONTACT_INVALID_QQ_GROUP")

	rec = otokContactDo(r, http.MethodPut, "/admin/contact", `not json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = otokContactDo(r, http.MethodGet, "/contact", "")
	require.Contains(t, rec.Body.String(), `"enabled":false`)
}
