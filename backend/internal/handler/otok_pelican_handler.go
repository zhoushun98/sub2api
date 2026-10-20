package handler

import (
	"context"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// otok: 「降智检测」（鹈鹕测试）的用户端与管理端接口。
// 用户只能看「已启用 ∩ 自己可用」的分组，与渠道状态卡片页（V2 scopeFilter）一致；不可见一律 404。

type otokPelicanService interface {
	EnabledGroupIDs(ctx context.Context) ([]int64, service.OtokPelicanConfig, error)
	ListRounds(ctx context.Context, groupID int64) ([]service.OtokPelicanRound, error)
	GetRun(ctx context.Context, runID int64) (*service.OtokPelicanRunDetail, error)
	GetConfig(ctx context.Context) (service.OtokPelicanConfig, error)
	SaveConfig(ctx context.Context, in service.OtokPelicanConfig) (service.OtokPelicanConfig, error)
	ProbeStatus(ctx context.Context) (service.OtokPelicanProbeStatus, error)
	RunNow(ctx context.Context, groupID int64) (int64, error)
}

type otokAvailableGroupsLister interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]service.Group, error)
}

// OtokPelicanHandler 处理鹈鹕测试的查询与配置。
type OtokPelicanHandler struct {
	svc    otokPelicanService
	groups otokAvailableGroupsLister
}

// NewOtokPelicanHandler 创建鹈鹕测试 handler。
func NewOtokPelicanHandler(svc *service.OtokPelicanService, apiKeys *service.APIKeyService) *OtokPelicanHandler {
	h := &OtokPelicanHandler{}
	if svc != nil {
		h.svc = svc
	}
	if apiKeys != nil {
		h.groups = apiKeys
	}
	return h
}

// visibleGroupIDs 返回当前用户可见、且启用了鹈鹕测试的分组。
func (h *OtokPelicanHandler) visibleGroupIDs(c *gin.Context) (map[int64]bool, service.OtokPelicanConfig, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return nil, service.OtokPelicanConfig{}, false
	}
	if h.svc == nil || h.groups == nil {
		response.InternalError(c, "pelican probe unavailable")
		return nil, service.OtokPelicanConfig{}, false
	}
	enabled, cfg, err := h.svc.EnabledGroupIDs(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, service.OtokPelicanConfig{}, false
	}
	visible := map[int64]bool{}
	if len(enabled) == 0 {
		return visible, cfg, true
	}
	groups, err := h.groups.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, service.OtokPelicanConfig{}, false
	}
	allowed := make(map[int64]bool, len(groups))
	for i := range groups {
		allowed[groups[i].ID] = true
	}
	for _, id := range enabled {
		if allowed[id] {
			visible[id] = true
		}
	}
	return visible, cfg, true
}

// Groups 返回当前用户可见的分组 ID，卡片据此决定是否显示「降智检测」按钮。
// GET /api/v1/otok/pelican/groups
func (h *OtokPelicanHandler) Groups(c *gin.Context) {
	visible, cfg, ok := h.visibleGroupIDs(c)
	if !ok {
		return
	}
	ids := make([]int64, 0, len(visible))
	for _, g := range cfg.EnabledGroups() {
		if visible[g.GroupID] {
			ids = append(ids, g.GroupID)
		}
	}
	response.Success(c, gin.H{
		"group_ids":        ids,
		"interval_minutes": cfg.IntervalMinutes,
		"keep_rounds":      cfg.KeepRounds,
		"reasoning_effort": cfg.ReasoningEffort,
	})
}

// Rounds 返回分组最近的轮次（不含生成内容）。
// GET /api/v1/otok/pelican/groups/:id/rounds
func (h *OtokPelicanHandler) Rounds(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	visible, _, ok := h.visibleGroupIDs(c)
	if !ok {
		return
	}
	if !visible[groupID] {
		response.NotFound(c, "Group not found")
		return
	}
	rounds, err := h.svc.ListRounds(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"rounds": rounds})
}

// Run 返回单条记录（含生成的 HTML 与原文）。
// GET /api/v1/otok/pelican/runs/:id
func (h *OtokPelicanHandler) Run(c *gin.Context) {
	runID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || runID <= 0 {
		response.BadRequest(c, "Invalid run ID")
		return
	}
	visible, _, ok := h.visibleGroupIDs(c)
	if !ok {
		return
	}
	run, err := h.svc.GetRun(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, service.ErrOtokPelicanRunNotFound) {
			response.NotFound(c, "Run not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if !visible[run.GroupID] {
		response.NotFound(c, "Run not found")
		return
	}
	response.Success(c, run)
}

// AdminGetConfig 返回完整配置。
// GET /api/v1/admin/otok/pelican/config
func (h *OtokPelicanHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := h.svc.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// AdminUpdateConfig 整组替换配置。
// PUT /api/v1/admin/otok/pelican/config
func (h *OtokPelicanHandler) AdminUpdateConfig(c *gin.Context) {
	var req service.OtokPelicanConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	cfg, err := h.svc.SaveConfig(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// AdminStatus 返回内部账号与各分组 key 的状态。
// GET /api/v1/admin/otok/pelican/status
func (h *OtokPelicanHandler) AdminStatus(c *gin.Context) {
	st, err := h.svc.ProbeStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, st)
}

type otokPelicanRunNowRequest struct {
	GroupID int64 `json:"group_id" binding:"required"`
}

// AdminRunNow 立即为分组跑一轮。
// POST /api/v1/admin/otok/pelican/run-now
func (h *OtokPelicanHandler) AdminRunNow(c *gin.Context) {
	var req otokPelicanRunNowRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID <= 0 {
		response.BadRequest(c, "Invalid request body")
		return
	}
	roundID, err := h.svc.RunNow(c.Request.Context(), req.GroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"round_id": roundID})
}

// AdminRounds 管理员查看任意分组（含未启用分组的手动运行结果）。
// GET /api/v1/admin/otok/pelican/groups/:id/rounds
func (h *OtokPelicanHandler) AdminRounds(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	rounds, err := h.svc.ListRounds(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"rounds": rounds})
}

// AdminRun 管理员查看任意记录。
// GET /api/v1/admin/otok/pelican/runs/:id
func (h *OtokPelicanHandler) AdminRun(c *gin.Context) {
	runID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || runID <= 0 {
		response.BadRequest(c, "Invalid run ID")
		return
	}
	run, err := h.svc.GetRun(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, service.ErrOtokPelicanRunNotFound) {
			response.NotFound(c, "Run not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, run)
}
