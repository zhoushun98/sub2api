//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type otokPelicanSvcStub struct {
	enabled []int64
	runs    map[int64]*service.OtokPelicanRunDetail
	listed  []int64
	saved   *service.OtokPelicanConfig
}

func (s *otokPelicanSvcStub) EnabledGroupIDs(context.Context) ([]int64, service.OtokPelicanConfig, error) {
	cfg := service.DefaultOtokPelicanConfig()
	cfg.Enabled = true
	for _, id := range s.enabled {
		cfg.Groups = append(cfg.Groups, service.OtokPelicanGroupConfig{GroupID: id, Enabled: true})
	}
	return s.enabled, cfg, nil
}
func (s *otokPelicanSvcStub) ListRounds(_ context.Context, groupID int64) ([]service.OtokPelicanRound, error) {
	s.listed = append(s.listed, groupID)
	return []service.OtokPelicanRound{{ID: 1, GroupID: groupID}}, nil
}
func (s *otokPelicanSvcStub) GetRun(_ context.Context, runID int64) (*service.OtokPelicanRunDetail, error) {
	if r, ok := s.runs[runID]; ok {
		return r, nil
	}
	return nil, service.ErrOtokPelicanRunNotFound
}
func (s *otokPelicanSvcStub) GetConfig(context.Context) (service.OtokPelicanConfig, error) {
	return service.DefaultOtokPelicanConfig(), nil
}
func (s *otokPelicanSvcStub) SaveConfig(_ context.Context, in service.OtokPelicanConfig) (service.OtokPelicanConfig, error) {
	s.saved = &in
	return in, nil
}
func (s *otokPelicanSvcStub) ProbeStatus(context.Context) (service.OtokPelicanProbeStatus, error) {
	return service.OtokPelicanProbeStatus{}, nil
}
func (s *otokPelicanSvcStub) RunNow(context.Context, int64) (int64, error) { return 9, nil }

type otokAvailableGroupsStub struct{ ids []int64 }

func (s *otokAvailableGroupsStub) GetAvailableGroups(context.Context, int64) ([]service.Group, error) {
	out := make([]service.Group, 0, len(s.ids))
	for _, id := range s.ids {
		out = append(out, service.Group{ID: id})
	}
	return out, nil
}

func otokPelicanRouter(svc *otokPelicanSvcStub, available []int64, authed bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &OtokPelicanHandler{svc: svc, groups: &otokAvailableGroupsStub{ids: available}}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authed {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		}
		c.Next()
	})
	r.GET("/groups", h.Groups)
	r.GET("/groups/:id/rounds", h.Rounds)
	r.GET("/runs/:id", h.Run)
	r.PUT("/admin/config", h.AdminUpdateConfig)
	return r
}

func otokPelicanDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestOtokPelicanHandler_GroupsIntersectsAvailable(t *testing.T) {
	svc := &otokPelicanSvcStub{enabled: []int64{127, 273, 240}}
	w := otokPelicanDo(otokPelicanRouter(svc, []int64{127, 240, 999}, true), http.MethodGet, "/groups", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data struct {
			GroupIDs        []int64 `json:"group_ids"`
			IntervalMinutes int     `json:"interval_minutes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []int64{127, 240}, resp.Data.GroupIDs)
	require.Equal(t, 10, resp.Data.IntervalMinutes)
}

func TestOtokPelicanHandler_RequiresAuth(t *testing.T) {
	w := otokPelicanDo(otokPelicanRouter(&otokPelicanSvcStub{}, nil, false), http.MethodGet, "/groups", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOtokPelicanHandler_HiddenGroupsAre404(t *testing.T) {
	svc := &otokPelicanSvcStub{
		enabled: []int64{127, 273},
		runs: map[int64]*service.OtokPelicanRunDetail{
			1: {GroupID: 127, OutputHTML: "<html>ok</html>"},
			2: {GroupID: 273, OutputHTML: "<html>secret</html>"},
			3: {GroupID: 500, OutputHTML: "<html>disabled group</html>"},
		},
	}
	r := otokPelicanRouter(svc, []int64{127, 500}, true)

	require.Equal(t, http.StatusOK, otokPelicanDo(r, http.MethodGet, "/groups/127/rounds", "").Code)
	require.Equal(t, http.StatusNotFound, otokPelicanDo(r, http.MethodGet, "/groups/273/rounds", "").Code, "启用但用户不可用")
	require.Equal(t, http.StatusNotFound, otokPelicanDo(r, http.MethodGet, "/groups/500/rounds", "").Code, "可用但未启用")
	require.Equal(t, http.StatusBadRequest, otokPelicanDo(r, http.MethodGet, "/groups/abc/rounds", "").Code)
	require.Equal(t, []int64{127}, svc.listed)

	w := otokPelicanDo(r, http.MethodGet, "/runs/1", "")
	require.Equal(t, http.StatusOK, w.Code)
	var run struct {
		Data service.OtokPelicanRunDetail `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &run))
	require.Equal(t, "<html>ok</html>", run.Data.OutputHTML)
	for _, path := range []string{"/runs/2", "/runs/3", "/runs/404"} {
		w := otokPelicanDo(r, http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, w.Code, path)
		require.NotContains(t, w.Body.String(), "secret", path)
	}
}

func TestOtokPelicanHandler_AdminUpdateConfigBindsBody(t *testing.T) {
	svc := &otokPelicanSvcStub{}
	r := otokPelicanRouter(svc, nil, true)
	require.Equal(t, http.StatusBadRequest, otokPelicanDo(r, http.MethodPut, "/admin/config", "{bad").Code)
	w := otokPelicanDo(r, http.MethodPut, "/admin/config", `{"enabled":true,"interval_minutes":15,"groups":[{"group_id":127,"enabled":true}]}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.saved)
	require.True(t, svc.saved.Enabled)
	require.Equal(t, 15, svc.saved.IntervalMinutes)
	require.Equal(t, int64(127), svc.saved.Groups[0].GroupID)
}
