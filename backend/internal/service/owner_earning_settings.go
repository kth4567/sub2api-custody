package service

// owner_earning_settings.go —— 账号托管市场 模块 C：收益结算相关的 SettingService 读取项。
// 读不到 key 时回退到 owner_earning_service.go 中定义的 default* 常量。
// 自包含一个文件，避免改动庞大的 setting_features.go。

import (
	"context"
	"math"
	"strconv"
	"strings"
)

// 设置项 key。未在设置表中显式配置时，各 getter 自动回退默认值，因此无需强制 seed。
const (
	SettingKeyOwnerEarningEnabled     = "owner_earning_enabled"
	SettingKeyOwnerEarningShareRate   = "owner_earning_share_rate"
	SettingKeyOwnerEarningFreezeHours = "owner_earning_freeze_hours"
	SettingKeyOwnerEarningBasis       = "owner_earning_basis"
)

// IsOwnerEarningEnabled 号主收益总开关（默认关闭：不结算、不产生收益）。
func (s *SettingService) IsOwnerEarningEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerEarningEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// GetOwnerEarningShareRate 号主分成比例，区间 (0,1]；非法值回退默认。
func (s *SettingService) GetOwnerEarningShareRate(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerEarningShareRate)
	if err != nil {
		return defaultOwnerEarningShareRate
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || rate > 1 {
		return defaultOwnerEarningShareRate
	}
	return rate
}

// GetOwnerEarningFreezeHours 收益冻结小时数（>=0）；非法值回退默认。
func (s *SettingService) GetOwnerEarningFreezeHours(ctx context.Context) int {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerEarningFreezeHours)
	if err != nil {
		return defaultOwnerEarningFreezeHours
	}
	hours, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || hours < 0 {
		return defaultOwnerEarningFreezeHours
	}
	return hours
}

// GetOwnerEarningBasis 分成基数列：total_cost 或 actual_cost；其它值回退默认。
func (s *SettingService) GetOwnerEarningBasis(ctx context.Context) string {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOwnerEarningBasis)
	if err != nil {
		return defaultOwnerEarningBasis
	}
	if basis := strings.TrimSpace(raw); basis == "total_cost" || basis == "actual_cost" {
		return basis
	}
	return defaultOwnerEarningBasis
}
