package handler

// owner_handler.go —— 账号托管市场：号主托管/收益/提现的 HTTP 接口。
// 用户端挂在 /user/owner 下；管理端审核挂在 /admin/owner 下。

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OwnerHandler 账号托管市场接口处理器。
type OwnerHandler struct {
	svc *service.OwnerEarningService
}

// NewOwnerHandler 构造 OwnerHandler（Wire provider）。
func NewOwnerHandler(svc *service.OwnerEarningService) *OwnerHandler {
	return &OwnerHandler{svc: svc}
}

// ── 请求体 ──────────────────────────────────────────────────────────
type hostAccountRequest struct {
	Name        string `json:"name" binding:"required"`
	Platform    string `json:"platform" binding:"required"`
	Type        string `json:"type" binding:"required"`
	Credentials string `json:"credentials" binding:"required"` // 凭证 JSON 字符串
}

type withdrawRequest struct {
	Amount      float64 `json:"amount" binding:"required"`
	Method      string  `json:"method"`
	AccountInfo string  `json:"account_info"`
}

type reviewWithdrawalRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}

// ── 用户端 ──────────────────────────────────────────────────────────

// GetMyEarnings GET /user/owner/earnings —— 号主收益汇总。
func (h *OwnerHandler) GetMyEarnings(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	summary, err := h.svc.GetSummary(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

// ListHostedAccounts GET /user/owner/accounts —— 我的托管账号（不含凭证）。
func (h *OwnerHandler) ListHostedAccounts(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	list, err := h.svc.ListMyHostedAccounts(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, list)
}

// HostAccount POST /user/owner/accounts —— 托管一个自己的订阅账号。
func (h *OwnerHandler) HostAccount(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req hostAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	id, err := h.svc.HostAccount(c.Request.Context(), subject.UserID, req.Name, req.Platform, req.Type, req.Credentials)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"id": id})
}

// UnhostAccount DELETE /user/owner/accounts/:id —— 退管自己的账号。
func (h *OwnerHandler) UnhostAccount(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}
	if err := h.svc.UnhostAccount(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// RequestWithdrawal POST /user/owner/withdrawals —— 发起提现。
func (h *OwnerHandler) RequestWithdrawal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req withdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	id, err := h.svc.RequestWithdrawal(c.Request.Context(), subject.UserID, req.Amount, req.Method, req.AccountInfo)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, gin.H{"id": id})
}

// ListMyWithdrawals GET /user/owner/withdrawals —— 我的提现单。
func (h *OwnerHandler) ListMyWithdrawals(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	list, err := h.svc.ListMyWithdrawals(c.Request.Context(), subject.UserID, limit, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, list)
}

// ── 管理端 ──────────────────────────────────────────────────────────

// AdminReviewWithdrawal POST /admin/owner/withdrawals/:id/review —— 审核提现单。
func (h *OwnerHandler) AdminReviewWithdrawal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid withdrawal id")
		return
	}
	var req reviewWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.svc.AdminReviewWithdrawal(c.Request.Context(), id, req.Approve, subject.UserID, req.Note); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}
