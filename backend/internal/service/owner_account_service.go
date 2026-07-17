package service

// owner_account_service.go —— 账号托管市场 模块 B：用户自助托管账号（service 层）。
// 方法挂在 OwnerEarningService 上；相关设置项 getter 也放在这里（自包含）。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const defaultOwnerMaxHostedPerUser = 10 // 每用户默认托管上限

// 设置项 key。
const (
	SettingKeyOwnerHostedGroupID    = "owner_hosted_group_id"     // 托管号自动加入的“托管池”分组 id
	SettingKeyOwnerMaxHostedPerUser = "owner_max_hosted_per_user" // 每用户托管上限
)

// 托管相关错误。
var (
	ErrHostedGroupNotConfigured = errors.New("hosted pool group not configured")
	ErrHostedLimitReached       = errors.New("hosted account limit reached")
	ErrAccountNotOwned          = errors.New("account not found or not owned by user")
	ErrHostMissingFields        = errors.New("missing required fields")
)

// OwnerHostedAccount 托管账号视图（不含凭证）。
type OwnerHostedAccount struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// GetOwnerHostedGroupID 托管号自动加入的分组 id（<=0 表示未配置，禁止托管）。
func (s *SettingService) GetOwnerHostedGroupID(ctx context.Context) int64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerHostedGroupID)
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// GetOwnerMaxHostedPerUser 每用户托管上限（<=0 回退默认）。
func (s *SettingService) GetOwnerMaxHostedPerUser(ctx context.Context) int {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerMaxHostedPerUser)
	if err != nil {
		return defaultOwnerMaxHostedPerUser
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultOwnerMaxHostedPerUser
	}
	return n
}

// HostAccount 用户托管一个自己的订阅账号。
func (s *OwnerEarningService) HostAccount(ctx context.Context, userID int64, name, platform, accType, credentialsJSON string) (int64, error) {
	if !s.enabled(ctx) {
		return 0, ErrOwnerEarningDisabled
	}
	if s.settingService == nil {
		return 0, ErrHostedGroupNotConfigured
	}
	groupID := s.settingService.GetOwnerHostedGroupID(ctx)
	if groupID <= 0 {
		return 0, ErrHostedGroupNotConfigured
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(platform) == "" ||
		strings.TrimSpace(accType) == "" || strings.TrimSpace(credentialsJSON) == "" {
		return 0, ErrHostMissingFields
	}

	maxHosted := s.settingService.GetOwnerMaxHostedPerUser(ctx)
	cnt, err := s.repo.CountHostedAccounts(ctx, userID)
	if err != nil {
		return 0, err
	}
	if cnt >= maxHosted {
		return 0, fmt.Errorf("%w: %d/%d", ErrHostedLimitReached, cnt, maxHosted)
	}

	return s.repo.HostAccount(ctx, userID, name, platform, accType, credentialsJSON, groupID)
}

// ListMyHostedAccounts 列出当前用户的托管账号。
func (s *OwnerEarningService) ListMyHostedAccounts(ctx context.Context, userID int64) ([]OwnerHostedAccount, error) {
	return s.repo.ListHostedAccounts(ctx, userID)
}

// UnhostAccount 用户退管自己的账号。
func (s *OwnerEarningService) UnhostAccount(ctx context.Context, userID, accountID int64) error {
	return s.repo.UnhostAccount(ctx, userID, accountID)
}
