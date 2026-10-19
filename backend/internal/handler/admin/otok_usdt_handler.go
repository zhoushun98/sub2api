package admin

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// otok: USDT 链上充值的管理端接口。

// OtokUSDTHandler 处理管理端 USDT 请求。
type OtokUSDTHandler struct {
	svc *service.OtokUSDTService
}

// NewOtokUSDTHandler 创建管理端 USDT handler。
func NewOtokUSDTHandler(svc *service.OtokUSDTService) *OtokUSDTHandler {
	return &OtokUSDTHandler{svc: svc}
}

func otokOperator(c *gin.Context) string {
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		return fmt.Sprintf("admin:%d", subject.UserID)
	}
	return "admin"
}

// ListTransfers 列出观察到的 USDT 入账转账。
// GET /api/v1/admin/payment/otok/usdt/transfers
func (h *OtokUSDTHandler) ListTransfers(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.ListTransfers(c.Request.Context(), service.OtokUSDTTransferFilter{
		Status:   c.Query("status"),
		Network:  c.Query("network"),
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

// Health 返回链上监听健康状态。
// GET /api/v1/admin/payment/otok/usdt/health
func (h *OtokUSDTHandler) Health(c *gin.Context) {
	health, err := h.svc.Health(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, health)
}

// GetOrder 返回订单的链上信息；非 USDT 订单返回 null。
// GET /api/v1/admin/payment/otok/usdt/orders/:id
func (h *OtokUSDTHandler) GetOrder(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.GetAdminOrderDetail(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, d)
}

type otokBindTxRequest struct {
	TxHash   string `json:"tx_hash" binding:"required"`
	LogIndex *int64 `json:"log_index"`
	Reason   string `json:"reason" binding:"required"`
}

// BindTx 校验链上交易后补单入账。
// POST /api/v1/admin/payment/otok/usdt/orders/:id/bind-tx
func (h *OtokUSDTHandler) BindTx(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req otokBindTxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "tx_hash and reason are required")
		return
	}
	d, err := h.svc.AdminBindTx(c.Request.Context(), orderID, req.TxHash, req.LogIndex, req.Reason, otokOperator(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, d)
}

type otokIgnoreTransferRequest struct {
	Note string `json:"note"`
}

// IgnoreTransfer 把未匹配的转账标记为已忽略。
// POST /api/v1/admin/payment/otok/usdt/transfers/:id/ignore
func (h *OtokUSDTHandler) IgnoreTransfer(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req otokIgnoreTransferRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.AdminIgnoreTransfer(c.Request.Context(), id, req.Note); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "ignored"})
}
