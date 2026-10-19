package handler

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// otok: USDT 链上充值的用户端接口。

// OtokUSDTHandler 处理用户端 USDT 请求。
type OtokUSDTHandler struct {
	svc *service.OtokUSDTService
}

// NewOtokUSDTHandler 创建用户端 USDT handler。
func NewOtokUSDTHandler(svc *service.OtokUSDTService) *OtokUSDTHandler {
	return &OtokUSDTHandler{svc: svc}
}

// GetQuote 返回汇率、手续费与可用网络；带 amount 时附预估应付 USDT。
// GET /api/v1/payment/otok/usdt/quote
func (h *OtokUSDTHandler) GetQuote(c *gin.Context) {
	var amount float64
	if raw := strings.TrimSpace(c.Query("amount")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			response.BadRequest(c, "Invalid amount")
			return
		}
		amount = v
	}
	q, err := h.svc.Quote(c.Request.Context(), amount)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, q)
}

// GetOrder 返回订单的收款地址、精确金额与到账进度。
// GET /api/v1/payment/otok/usdt/orders/:id
func (h *OtokUSDTHandler) GetOrder(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}
	d, err := h.svc.GetUserOrderDetail(c.Request.Context(), subject.UserID, orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, d)
}

type otokSubmitTxRequest struct {
	TxHash string `json:"tx_hash" binding:"required"`
}

// SubmitTx 用户提交交易哈希加速确认。
// POST /api/v1/payment/otok/usdt/orders/:id/tx
func (h *OtokUSDTHandler) SubmitTx(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}
	var req otokSubmitTxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "tx_hash is required")
		return
	}
	d, err := h.svc.SubmitUserTx(c.Request.Context(), subject.UserID, orderID, req.TxHash)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, d)
}

// OtokHandlers 聚合 fork 定制的 handler，挂在 Handlers.Otok 上。
type OtokHandlers struct {
	USDT      *OtokUSDTHandler
	AdminUSDT *admin.OtokUSDTHandler
}

// ProvideOtokHandlers 创建 OtokHandlers。
func ProvideOtokHandlers(usdt *OtokUSDTHandler, adminUSDT *admin.OtokUSDTHandler) *OtokHandlers {
	return &OtokHandlers{USDT: usdt, AdminUSDT: adminUSDT}
}
