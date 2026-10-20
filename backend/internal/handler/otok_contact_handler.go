package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// otok: 侧边栏「联系我们」的用户端与管理端接口。

// OtokContactHandler 处理联系方式的读取与配置。
type OtokContactHandler struct {
	svc *service.OtokContactService
}

// NewOtokContactHandler 创建联系方式 handler。
func NewOtokContactHandler(svc *service.OtokContactService) *OtokContactHandler {
	return &OtokContactHandler{svc: svc}
}

// GetSummary 返回不含二维码的摘要，侧边栏据此决定是否显示入口。
// GET /api/v1/otok/contact
func (h *OtokContactHandler) GetSummary(c *gin.Context) {
	s, err := h.svc.Summary(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, s)
}

// GetQRCodes 返回二维码图片（data URL），打开弹窗时才请求。
// GET /api/v1/otok/contact/qrcodes
func (h *OtokContactHandler) GetQRCodes(c *gin.Context) {
	q, err := h.svc.QRCodes(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, q)
}

// AdminGet 返回完整配置。
// GET /api/v1/admin/otok/contact
func (h *OtokContactHandler) AdminGet(c *gin.Context) {
	cfg, err := h.svc.Get(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// AdminUpdate 整组替换配置。
// PUT /api/v1/admin/otok/contact
func (h *OtokContactHandler) AdminUpdate(c *gin.Context) {
	var req service.OtokContactConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	cfg, summary, err := h.svc.Save(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"config": cfg, "summary": summary})
}
