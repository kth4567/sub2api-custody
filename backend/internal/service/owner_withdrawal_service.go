package service

// owner_withdrawal_service.go —— 账号托管市场 模块 D：号主提现（service 层）。
// 方法挂在 OwnerEarningService 上，复用其 repo/settingService，避免新增 Wire provider。

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ── 提现策略（可后续接入 SettingService）──────────────────────────────
const (
	ownerWithdrawalMinAmount    = 1.0            // 最小提现额（USD/额度单位）
	ownerWithdrawalWindow       = 7 * 24 * time.Hour
	ownerWithdrawalMaxPerWindow = 3 // 每 7 天最多 3 次
)

// 提现相关错误（handler 层据此映射 HTTP 状态码）。
var (
	ErrOwnerEarningDisabled  = errors.New("owner earning is disabled")
	ErrInsufficientEarning   = errors.New("insufficient withdrawable earning")
	ErrWithdrawalTooSmall    = errors.New("withdrawal amount below minimum")
	ErrWithdrawalRateLimited = errors.New("withdrawal rate limited")
	ErrWithdrawalNotPending  = errors.New("withdrawal is not pending")
)

// OwnerWithdrawal 提现单视图。
type OwnerWithdrawal struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Amount      float64   `json:"amount"`
	Status      string    `json:"status"`
	Method      string    `json:"method"`
	AccountInfo string    `json:"account_info"`
	ReviewNote  string    `json:"review_note"`
	CreatedAt   time.Time `json:"created_at"`
}

// RequestWithdrawal 号主发起提现：校验开关/最小额/限流后，由 repo 在事务内原子扣额建单。
func (s *OwnerEarningService) RequestWithdrawal(ctx context.Context, userID int64, amount float64, method, accountInfo string) (int64, error) {
	if !s.enabled(ctx) {
		return 0, ErrOwnerEarningDisabled
	}
	if amount < ownerWithdrawalMinAmount {
		return 0, fmt.Errorf("%w: min=%.2f", ErrWithdrawalTooSmall, ownerWithdrawalMinAmount)
	}
	cnt, err := s.repo.CountWithdrawalsSince(ctx, userID, time.Now().Add(-ownerWithdrawalWindow))
	if err != nil {
		return 0, err
	}
	if cnt >= ownerWithdrawalMaxPerWindow {
		return 0, fmt.Errorf("%w: %d/%d in %s", ErrWithdrawalRateLimited, cnt, ownerWithdrawalMaxPerWindow, ownerWithdrawalWindow)
	}
	return s.repo.CreateWithdrawal(ctx, userID, roundTo(amount, 8), method, accountInfo)
}

// ListMyWithdrawals 列出当前号主的提现单。
func (s *OwnerEarningService) ListMyWithdrawals(ctx context.Context, userID int64, limit, offset int) ([]OwnerWithdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListWithdrawals(ctx, userID, limit, offset)
}

// AdminReviewWithdrawal 管理员审核提现单。
func (s *OwnerEarningService) AdminReviewWithdrawal(ctx context.Context, id int64, approve bool, reviewerID int64, note string) error {
	return s.repo.ReviewWithdrawal(ctx, id, approve, reviewerID, note)
}
