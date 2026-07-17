package service

// owner_earning_service.go —— 账号托管市场 模块 C：用量归属与收益结算。
//
// 职责：把「托管账号（accounts.owner_user_id 非空）被调用产生的用量」按分成比例
// 结算成号主收益。结算走水位线保证幂等；收益先进冻结额度，过释放期后转为可提现额度
// （可提现额度由模块 D 提现流程消费）。
//
// 与 AffiliateService 同构：Repository 接口在本包定义、由 repository 包裸 SQL 实现；
// 金额四舍五入复用本包已有的 roundTo()。

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// ── 配置默认值（可被 SettingService 覆盖，见 settingService 相关 getter）──────────
const (
	// 号主分成比例：号主拿 rate，平台抽 (1-rate)。取值区间 (0,1]。
	defaultOwnerEarningShareRate = 0.7
	// 收益冻结小时数：入账后冻结这么久才可提现，用于对冲退款/刷量/封号回收。
	defaultOwnerEarningFreezeHours = 72
	// 分成基数列：total_cost（该请求的计算成本，稳定、与消费端计费模式无关）
	// 或 actual_cost（消费端实际被扣，订阅覆盖时可能为 0）。
	defaultOwnerEarningBasis = "total_cost"
	// 结算滞后：只结算 created_at <= now-lag 的日志，避开在途写入造成的漏结算。
	ownerEarningSettleSafetyLag = 60 * time.Second
)

// OwnerUsageAggregate 是结算扫描的中间结果：某托管账号在本批区间的被调用成本合计。
type OwnerUsageAggregate struct {
	OwnerUserID int64
	AccountID   int64
	BasisCost   float64 // 按 basis 列（total_cost/actual_cost）聚合的成本
}

// OwnerEarningAccrual 是一次入账项：某号主某账号本批应得的分成收益。
type OwnerEarningAccrual struct {
	OwnerUserID int64
	AccountID   int64
	BasisCost   float64
	Amount      float64 // roundTo(BasisCost * Rate, 8)
	Rate        float64
}

// OwnerEarningSummary 是号主收益汇总视图（用户/管理端展示）。
type OwnerEarningSummary struct {
	UserID             int64   `json:"user_id"`
	EarningQuota       float64 `json:"earning_quota"`        // 可提现
	FrozenQuota        float64 `json:"frozen_quota"`         // 冻结中
	HistoryQuota       float64 `json:"history_quota"`        // 累计历史
	HostedAccountCount int     `json:"hosted_account_count"` // 当前托管账号数
}

// OwnerEarningRepository 由 repository 包裸 SQL 实现（参照 AffiliateRepository）。
type OwnerEarningRepository interface {
	// GetSettlementWatermark 返回已结算到的 usage_logs.id 上界。
	GetSettlementWatermark(ctx context.Context) (int64, error)
	// MaxUsageLogIDBefore 返回 created_at <= before 的最大 usage_logs.id，作为本批 ceiling。
	MaxUsageLogIDBefore(ctx context.Context, before time.Time) (int64, error)
	// AggregateUnsettledUsage 聚合 (watermark, ceiling] 内、托管账号（owner 非空）按号主+账号的成本。
	// basisColumn 只接受 "total_cost" 或 "actual_cost"（调用方已校验）。
	AggregateUnsettledUsage(ctx context.Context, watermark, ceiling int64, basisColumn string) ([]OwnerUsageAggregate, error)
	// SettleBatch 在单事务内：把各号主的分成计入冻结额度、写 accrue 流水（带 mature_at）、
	// 并把水位线推进到 ceiling。accruals 为空时也要推进水位线，避免重复扫描同一区间。
	SettleBatch(ctx context.Context, accruals []OwnerEarningAccrual, ceiling int64, matureAt time.Time) error
	// ReleaseMatured 把已成熟(mature_at<=now)且未释放的冻结收益转为可提现，返回释放总额与笔数。
	ReleaseMatured(ctx context.Context, now time.Time) (amount float64, count int64, err error)
	// GetSummary 返回号主收益汇总（记录不存在时返回零值汇总，不报错）。
	GetSummary(ctx context.Context, userID int64) (*OwnerEarningSummary, error)

	// —— 提现（模块 D）——
	// CountWithdrawalsSince 统计 since 之后该用户的提现单数（用于限流）。
	CountWithdrawalsSince(ctx context.Context, userID int64, since time.Time) (int, error)
	// CreateWithdrawal 在事务内原子扣减可提现额度并生成 pending 提现单 + withdraw 流水；
	// 余额不足返回 ErrInsufficientEarning。
	CreateWithdrawal(ctx context.Context, userID int64, amount float64, method, accountInfo string) (int64, error)
	// ListWithdrawals 列出某用户的提现单（倒序）。
	ListWithdrawals(ctx context.Context, userID int64, limit, offset int) ([]OwnerWithdrawal, error)
	// ReviewWithdrawal 审核：approve=true 置 paid；false 驳回并退回额度 + reverse 流水。
	// 单据非 pending 时返回 ErrWithdrawalNotPending。
	ReviewWithdrawal(ctx context.Context, id int64, approve bool, reviewerID int64, note string) error

	// —— 托管（模块 B）——
	// CountHostedAccounts 统计某用户当前有效（未软删）的托管账号数。
	CountHostedAccounts(ctx context.Context, userID int64) (int, error)
	// HostAccount 在事务内创建一个归属该用户的账号、加入托管池分组、并把 hosted_account_count+1。
	HostAccount(ctx context.Context, userID int64, name, platform, accType, credentialsJSON string, groupID int64) (int64, error)
	// ListHostedAccounts 列出某用户的托管账号（不含凭证）。
	ListHostedAccounts(ctx context.Context, userID int64) ([]OwnerHostedAccount, error)
	// UnhostAccount 退管：软删账号 + 移出分组 + 计数-1；仅限本人的号，否则 ErrAccountNotOwned。
	UnhostAccount(ctx context.Context, userID, accountID int64) error
}

// OwnerEarningService 负责「托管账号用量 → 号主收益」的结算与释放。
type OwnerEarningService struct {
	repo           OwnerEarningRepository
	settingService *SettingService
}

// NewOwnerEarningService 构造收益结算服务（Wire 注入 repo 与 settingService）。
func NewOwnerEarningService(repo OwnerEarningRepository, settingService *SettingService) *OwnerEarningService {
	return &OwnerEarningService{repo: repo, settingService: settingService}
}

// ── 配置读取：优先 SettingService，回退默认值 ─────────────────────────────────
// 说明：以下 getter 需在 SettingService 上补齐（见 runbook「settings 补丁」）：
//   IsOwnerEarningEnabled(ctx) bool
//   GetOwnerEarningShareRate(ctx) float64
//   GetOwnerEarningFreezeHours(ctx) int
//   GetOwnerEarningBasis(ctx) string

func (s *OwnerEarningService) enabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false
	}
	return s.settingService.IsOwnerEarningEnabled(ctx)
}

func (s *OwnerEarningService) shareRate(ctx context.Context) float64 {
	if s.settingService == nil {
		return defaultOwnerEarningShareRate
	}
	r := s.settingService.GetOwnerEarningShareRate(ctx)
	if r <= 0 || r > 1 {
		return defaultOwnerEarningShareRate
	}
	return r
}

func (s *OwnerEarningService) freezeHours(ctx context.Context) int {
	if s.settingService == nil {
		return defaultOwnerEarningFreezeHours
	}
	h := s.settingService.GetOwnerEarningFreezeHours(ctx)
	if h < 0 {
		return defaultOwnerEarningFreezeHours
	}
	return h
}

func (s *OwnerEarningService) basisColumn(ctx context.Context) string {
	basis := defaultOwnerEarningBasis
	if s.settingService != nil {
		if b := s.settingService.GetOwnerEarningBasis(ctx); b == "total_cost" || b == "actual_cost" {
			basis = b
		}
	}
	return basis
}

// SettleOnce 执行一次增量结算。由后台 worker 周期调用（见 owner_earning_worker.go）。
// 幂等：以 usage_logs.id 水位线推进，绝不重复计费；崩溃重启后从水位线续跑。
func (s *OwnerEarningService) SettleOnce(ctx context.Context) error {
	if !s.enabled(ctx) {
		return nil
	}

	watermark, err := s.repo.GetSettlementWatermark(ctx)
	if err != nil {
		return err
	}
	ceiling, err := s.repo.MaxUsageLogIDBefore(ctx, time.Now().Add(-ownerEarningSettleSafetyLag))
	if err != nil {
		return err
	}
	if ceiling <= watermark {
		return nil // 没有新的可结算日志
	}

	basis := s.basisColumn(ctx)
	aggs, err := s.repo.AggregateUnsettledUsage(ctx, watermark, ceiling, basis)
	if err != nil {
		return err
	}

	rate := s.shareRate(ctx)
	accruals := make([]OwnerEarningAccrual, 0, len(aggs))
	for _, a := range aggs {
		amt := roundTo(a.BasisCost*rate, 8)
		if amt <= 0 {
			continue
		}
		accruals = append(accruals, OwnerEarningAccrual{
			OwnerUserID: a.OwnerUserID,
			AccountID:   a.AccountID,
			BasisCost:   a.BasisCost,
			Amount:      amt,
			Rate:        rate,
		})
	}

	matureAt := time.Now().Add(time.Duration(s.freezeHours(ctx)) * time.Hour)
	if err := s.repo.SettleBatch(ctx, accruals, ceiling, matureAt); err != nil {
		return err
	}
	logger.LegacyPrintf("service.owner_earning",
		"[OwnerEarning] settled usage (%d, %d] owners=%d rate=%.4f basis=%s",
		watermark, ceiling, len(accruals), rate, basis)
	return nil
}

// ReleaseMaturedFrozen 把到期的冻结收益转为可提现。由后台 worker 周期调用。
func (s *OwnerEarningService) ReleaseMaturedFrozen(ctx context.Context) error {
	if !s.enabled(ctx) {
		return nil
	}
	amount, count, err := s.repo.ReleaseMatured(ctx, time.Now())
	if err != nil {
		return err
	}
	if count > 0 {
		logger.LegacyPrintf("service.owner_earning",
			"[OwnerEarning] released matured frozen: entries=%d amount=%.8f", count, amount)
	}
	return nil
}

// GetSummary 返回某号主的收益汇总（供用户端「我的收益」与管理端展示）。
func (s *OwnerEarningService) GetSummary(ctx context.Context, userID int64) (*OwnerEarningSummary, error) {
	return s.repo.GetSummary(ctx, userID)
}
