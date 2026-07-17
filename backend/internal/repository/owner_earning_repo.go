package repository

// owner_earning_repo.go —— 账号托管市场 模块 C：号主收益结算的裸 SQL 仓储实现。
// 参照 affiliate_repo.go：持有 *dbent.Client，通过 ExecContext/QueryContext 跑裸 SQL，
// 事务用 withTx 包裹。对应接口 service.OwnerEarningRepository 定义在 service 包。

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type ownerEarningRepository struct {
	client *dbent.Client
}

// NewOwnerEarningRepository 构造号主收益仓储（Wire 绑定到 service.OwnerEarningRepository）。
func NewOwnerEarningRepository(client *dbent.Client) service.OwnerEarningRepository {
	return &ownerEarningRepository{client: client}
}

// withTx 在事务内执行 fn。参照 affiliateRepository.withTx。
func (r *ownerEarningRepository) withTx(ctx context.Context, fn func(ctx context.Context, tx *dbent.Client) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback()
			panic(v)
		}
	}()
	if err := fn(ctx, tx.Client()); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", err, rerr)
		}
		return err
	}
	return tx.Commit()
}

func (r *ownerEarningRepository) GetSettlementWatermark(ctx context.Context) (int64, error) {
	rows, err := r.client.QueryContext(ctx,
		`SELECT last_settled_usage_log_id FROM owner_earning_settlement_state WHERE id = 1`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var watermark int64
	if rows.Next() {
		if err := rows.Scan(&watermark); err != nil {
			return 0, err
		}
	}
	return watermark, rows.Err()
}

func (r *ownerEarningRepository) MaxUsageLogIDBefore(ctx context.Context, before time.Time) (int64, error) {
	rows, err := r.client.QueryContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM usage_logs WHERE created_at <= $1`, before)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var maxID int64
	if rows.Next() {
		if err := rows.Scan(&maxID); err != nil {
			return 0, err
		}
	}
	return maxID, rows.Err()
}

func (r *ownerEarningRepository) AggregateUnsettledUsage(ctx context.Context, watermark, ceiling int64, basisColumn string) ([]service.OwnerUsageAggregate, error) {
	// basisColumn 已由 service 层白名单校验（total_cost / actual_cost），可安全拼接列名。
	// 反刷量：排除「调用方就是号主本人」的用量，防止自调自己的托管号白嫖分成。
	query := fmt.Sprintf(`
		SELECT a.owner_user_id, ul.account_id, COALESCE(SUM(ul.%s), 0) AS basis_cost
		FROM usage_logs ul
		JOIN accounts a ON a.id = ul.account_id
		WHERE a.owner_user_id IS NOT NULL
		  AND ul.user_id <> a.owner_user_id
		  AND ul.id > $1 AND ul.id <= $2
		GROUP BY a.owner_user_id, ul.account_id
		HAVING COALESCE(SUM(ul.%s), 0) > 0`, basisColumn, basisColumn)

	rows, err := r.client.QueryContext(ctx, query, watermark, ceiling)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.OwnerUsageAggregate
	for rows.Next() {
		var agg service.OwnerUsageAggregate
		if err := rows.Scan(&agg.OwnerUserID, &agg.AccountID, &agg.BasisCost); err != nil {
			return nil, err
		}
		out = append(out, agg)
	}
	return out, rows.Err()
}

func (r *ownerEarningRepository) SettleBatch(ctx context.Context, accruals []service.OwnerEarningAccrual, ceiling int64, matureAt time.Time) error {
	return r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		for _, a := range accruals {
			// upsert 汇总：冻结额度与历史累计各 += amount，读回快照。
			var earningAfter, frozenAfter, historyAfter float64
			rows, err := tx.QueryContext(txCtx, `
				INSERT INTO owner_earnings(user_id, frozen_quota, history_quota, updated_at)
				VALUES ($1, $2, $2, NOW())
				ON CONFLICT (user_id) DO UPDATE SET
					frozen_quota  = owner_earnings.frozen_quota  + EXCLUDED.frozen_quota,
					history_quota = owner_earnings.history_quota + EXCLUDED.history_quota,
					updated_at    = NOW()
				RETURNING earning_quota, frozen_quota, history_quota`, a.OwnerUserID, a.Amount)
			if err != nil {
				return err
			}
			if rows.Next() {
				if err := rows.Scan(&earningAfter, &frozenAfter, &historyAfter); err != nil {
					rows.Close()
					return err
				}
			}
			rows.Close()

			// 写 accrue 流水（带 mature_at 与快照）。
			if _, err := tx.ExecContext(txCtx, `
				INSERT INTO owner_earning_ledger(
					user_id, action, amount, source_account_id, basis_cost, rate,
					mature_at, earning_quota_after, frozen_quota_after, history_quota_after, created_at)
				VALUES ($1, 'accrue', $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
				a.OwnerUserID, a.Amount, a.AccountID, a.BasisCost, a.Rate,
				matureAt, earningAfter, frozenAfter, historyAfter); err != nil {
				return err
			}
		}

		// 推进水位线（单调，防回退）。即使 accruals 为空也要推进，避免重复扫描同一区间。
		if _, err := tx.ExecContext(txCtx, `
			UPDATE owner_earning_settlement_state
			SET last_settled_usage_log_id = $1, last_run_at = NOW(), updated_at = NOW()
			WHERE id = 1 AND last_settled_usage_log_id < $1`, ceiling); err != nil {
			return err
		}
		return nil
	})
}

func (r *ownerEarningRepository) ReleaseMatured(ctx context.Context, now time.Time) (float64, int64, error) {
	var totalReleased float64
	var releasedCount int64

	err := r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		// 汇总已成熟且未释放的冻结收益（按号主）。
		rows, err := tx.QueryContext(txCtx, `
			SELECT user_id, COALESCE(SUM(amount), 0) AS amt
			FROM owner_earning_ledger
			WHERE action = 'accrue' AND released = false
			  AND mature_at IS NOT NULL AND mature_at <= $1
			GROUP BY user_id`, now)
		if err != nil {
			return err
		}
		type matured struct {
			userID int64
			amount float64
		}
		var batch []matured
		for rows.Next() {
			var m matured
			if err := rows.Scan(&m.userID, &m.amount); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, m := range batch {
			if m.amount <= 0 {
				continue
			}
			var earningAfter, frozenAfter, historyAfter float64
			ur, err := tx.QueryContext(txCtx, `
				UPDATE owner_earnings
				SET frozen_quota  = GREATEST(frozen_quota - $2, 0),
				    earning_quota = earning_quota + $2,
				    updated_at    = NOW()
				WHERE user_id = $1
				RETURNING earning_quota, frozen_quota, history_quota`, m.userID, m.amount)
			if err != nil {
				return err
			}
			if ur.Next() {
				if err := ur.Scan(&earningAfter, &frozenAfter, &historyAfter); err != nil {
					ur.Close()
					return err
				}
			}
			ur.Close()

			if _, err := tx.ExecContext(txCtx, `
				INSERT INTO owner_earning_ledger(
					user_id, action, amount, earning_quota_after, frozen_quota_after, history_quota_after, created_at)
				VALUES ($1, 'release', $2, $3, $4, $5, NOW())`,
				m.userID, m.amount, earningAfter, frozenAfter, historyAfter); err != nil {
				return err
			}
			totalReleased += m.amount
			releasedCount++
		}

		// 标记这批 accrue 行为已释放（谓词与上面聚合一致，事务内一致，防重复释放）。
		if _, err := tx.ExecContext(txCtx, `
			UPDATE owner_earning_ledger
			SET released = true
			WHERE action = 'accrue' AND released = false
			  AND mature_at IS NOT NULL AND mature_at <= $1`, now); err != nil {
			return err
		}
		return nil
	})
	return totalReleased, releasedCount, err
}

func (r *ownerEarningRepository) GetSummary(ctx context.Context, userID int64) (*service.OwnerEarningSummary, error) {
	rows, err := r.client.QueryContext(ctx, `
		SELECT user_id, earning_quota, frozen_quota, history_quota, hosted_account_count
		FROM owner_earnings WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := &service.OwnerEarningSummary{UserID: userID}
	if rows.Next() {
		if err := rows.Scan(
			&summary.UserID, &summary.EarningQuota, &summary.FrozenQuota,
			&summary.HistoryQuota, &summary.HostedAccountCount); err != nil {
			return nil, err
		}
	}
	return summary, rows.Err()
}
