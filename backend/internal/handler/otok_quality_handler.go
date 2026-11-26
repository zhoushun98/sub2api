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

// otok: 「降智检测」的用户端与管理端接口。
// 所有登录用户都能看到「总开关开 且 条目启用」的全部条目；不可见一律 404。
// 用户端不返回 URL、key、附加参数、错误原文、HTTP 状态码和上游返回的模型名。

type otokQualityService interface {
	EnabledItems(ctx context.Context) ([]service.OtokQualityItem, service.OtokQualityConfig, error)
	ListRounds(ctx context.Context, itemID int64) ([]service.OtokQualityAdminRound, error)
	GetRun(ctx context.Context, runID int64) (*service.OtokQualityAdminRunDetail, error)
	AdminConfig(ctx context.Context) (service.OtokQualityAdminConfig, error)
	SaveConfig(ctx context.Context, in service.OtokQualityConfigInput) (service.OtokQualityAdminConfig, error)
	Status(ctx context.Context) (service.OtokQualityStatus, error)
	RunNow(ctx context.Context, itemID int64) (int64, error)
}

// OtokQualityHandler 处理降智检测的查询与配置。
type OtokQualityHandler struct {
	svc otokQualityService
}

// NewOtokQualityHandler 创建降智检测 handler。
func NewOtokQualityHandler(svc *service.OtokQualityService) *OtokQualityHandler {
	h := &OtokQualityHandler{}
	if svc != nil {
		h.svc = svc
	}
	return h
}

type otokQualityPublicItem struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// visibleItems 返回当前用户可见的条目 id 集合。
func (h *OtokQualityHandler) visibleItems(c *gin.Context) (map[int64]bool, []service.OtokQualityItem, service.OtokQualityConfig, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return nil, nil, service.OtokQualityConfig{}, false
	}
	if h.svc == nil {
		response.InternalError(c, "quality check unavailable")
		return nil, nil, service.OtokQualityConfig{}, false
	}
	items, cfg, err := h.svc.EnabledItems(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, nil, service.OtokQualityConfig{}, false
	}
	visible := make(map[int64]bool, len(items))
	for _, it := range items {
		visible[it.ID] = true
	}
	return visible, items, cfg, true
}

// Items 返回用户可见的条目。
// GET /api/v1/otok/quality/items
func (h *OtokQualityHandler) Items(c *gin.Context) {
	_, items, cfg, ok := h.visibleItems(c)
	if !ok {
		return
	}
	out := make([]otokQualityPublicItem, 0, len(items))
	for _, it := range items {
		models := it.Models
		if models == nil {
			models = []string{}
		}
		out = append(out, otokQualityPublicItem{ID: it.ID, Name: it.Name, Models: models})
	}
	response.Success(c, gin.H{
		"items":            out,
		"interval_minutes": cfg.IntervalMinutes,
		"keep_rounds":      cfg.KeepRounds,
	})
}

// Rounds 返回条目最近的轮次（不含生成内容）。
// GET /api/v1/otok/quality/items/:id/rounds
func (h *OtokQualityHandler) Rounds(c *gin.Context) {
	itemID, ok := otokQualityParseID(c, "Invalid item ID")
	if !ok {
		return
	}
	visible, _, _, ok := h.visibleItems(c)
	if !ok {
		return
	}
	if !visible[itemID] {
		response.NotFound(c, "Item not found")
		return
	}
	rounds, err := h.svc.ListRounds(c.Request.Context(), itemID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]service.OtokQualityRound, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, r.UserView())
	}
	response.Success(c, gin.H{"rounds": out})
}

// Run 返回单条记录（含生成的 HTML 与原文）。
// GET /api/v1/otok/quality/runs/:id
func (h *OtokQualityHandler) Run(c *gin.Context) {
	runID, ok := otokQualityParseID(c, "Invalid run ID")
	if !ok {
		return
	}
	visible, _, _, ok := h.visibleItems(c)
	if !ok {
		return
	}
	run, err := h.svc.GetRun(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, service.ErrOtokQualityRunNotFound) {
			response.NotFound(c, "Run not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	if !visible[run.ItemID] {
		response.NotFound(c, "Run not found")
		return
	}
	response.Success(c, run.UserView())
}

// AdminGetConfig 返回完整配置（key 只给打码值）。
// GET /api/v1/admin/otok/quality/config
func (h *OtokQualityHandler) AdminGetConfig(c *gin.Context) {
	cfg, err := h.svc.AdminConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// AdminUpdateConfig 整组替换配置，返回体与 GET 相同。
// PUT /api/v1/admin/otok/quality/config
func (h *OtokQualityHandler) AdminUpdateConfig(c *gin.Context) {
	var req service.OtokQualityConfigInput
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

// AdminStatus 返回各条目最近一轮的情况。
// GET /api/v1/admin/otok/quality/status
func (h *OtokQualityHandler) AdminStatus(c *gin.Context) {
	st, err := h.svc.Status(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, st)
}

type otokQualityRunNowRequest struct {
	ItemID int64 `json:"item_id" binding:"required"`
}

// AdminRunNow 立即为条目跑一轮。
// POST /api/v1/admin/otok/quality/run-now
func (h *OtokQualityHandler) AdminRunNow(c *gin.Context) {
	var req otokQualityRunNowRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ItemID <= 0 {
		response.BadRequest(c, "Invalid request body")
		return
	}
	roundID, err := h.svc.RunNow(c.Request.Context(), req.ItemID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"round_id": roundID})
}

// AdminRounds 管理员查看任意条目（含未启用条目的手动运行结果）。
// GET /api/v1/admin/otok/quality/items/:id/rounds
func (h *OtokQualityHandler) AdminRounds(c *gin.Context) {
	itemID, ok := otokQualityParseID(c, "Invalid item ID")
	if !ok {
		return
	}
	rounds, err := h.svc.ListRounds(c.Request.Context(), itemID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"rounds": rounds})
}

// AdminRun 管理员查看任意记录。
// GET /api/v1/admin/otok/quality/runs/:id
func (h *OtokQualityHandler) AdminRun(c *gin.Context) {
	runID, ok := otokQualityParseID(c, "Invalid run ID")
	if !ok {
		return
	}
	run, err := h.svc.GetRun(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, service.ErrOtokQualityRunNotFound) {
			response.NotFound(c, "Run not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, run)
}

func otokQualityParseID(c *gin.Context, msg string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, msg)
		return 0, false
	}
	return id, true
}
