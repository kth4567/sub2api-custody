package repository

// owner_account_repo.go —— 账号托管市场 模块 B：用户自助托管账号的裸 SQL 实现。
// 方法挂在 *ownerEarningRepository 上。
//
// 安全提示：credentialsJSON 目前按上游 accounts.credentials(JSONB) 原样写入。
// 生产环境应在写入前对敏感凭证做加密（见 runbook「模块 E / 安全」）。

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *ownerEarningRepository) CountHostedAccounts(ctx context.Context, userID int64) (int, error) {
	rows, err := r.client.QueryContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE owner_user_id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

// HostAccount 事务内：建号（owner=userID）→ 入托管池分组 → hosted_account_count+1。
func (r *ownerEarningRepository) HostAccount(ctx context.Context, userID int64, name, platform, accType, credentialsJSON string, groupID int64) (int64, error) {
	var accountID int64
	err := r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		// 建号。裸 SQL 需显式给出 Ent 默认值（concurrency/priority/status/schedulable）。
		ar, err := tx.QueryContext(txCtx, `
			INSERT INTO accounts(
				name, platform, type, credentials, owner_user_id,
				concurrency, priority, status, schedulable, created_at, updated_at)
			VALUES ($1, $2, $3, $4::jsonb, $5, 3, 50, 'active', true, NOW(), NOW())
			RETURNING id`, name, platform, accType, credentialsJSON, userID)
		if err != nil {
			return err
		}
		if ar.Next() {
			if err := ar.Scan(&accountID); err != nil {
				ar.Close()
				return err
			}
		}
		ar.Close()

		// 加入托管池分组。
		if _, err := tx.ExecContext(txCtx, `
			INSERT INTO account_groups(account_id, group_id, priority, created_at)
			VALUES ($1, $2, 50, NOW())
			ON CONFLICT (account_id, group_id) DO NOTHING`, accountID, groupID); err != nil {
			return err
		}

		// 托管计数 +1。
		if _, err := tx.ExecContext(txCtx, `
			INSERT INTO owner_earnings(user_id, hosted_account_count, updated_at)
			VALUES ($1, 1, NOW())
			ON CONFLICT (user_id) DO UPDATE SET
				hosted_account_count = owner_earnings.hosted_account_count + 1,
				updated_at = NOW()`, userID); err != nil {
			return err
		}
		return nil
	})
	return accountID, err
}

func (r *ownerEarningRepository) ListHostedAccounts(ctx context.Context, userID int64) ([]service.OwnerHostedAccount, error) {
	rows, err := r.client.QueryContext(ctx, `
		SELECT id, name, platform, type, status, created_at
		FROM accounts
		WHERE owner_user_id = $1 AND deleted_at IS NULL
		ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.OwnerHostedAccount
	for rows.Next() {
		var a service.OwnerHostedAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Type, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UnhostAccount 退管：软删账号（校验归属）→ 移出所有分组 → hosted_account_count-1。
func (r *ownerEarningRepository) UnhostAccount(ctx context.Context, userID, accountID int64) error {
	return r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		// 只允许软删本人的、未删除的托管号。
		res, err := tx.ExecContext(txCtx, `
			UPDATE accounts
			SET deleted_at = NOW(), schedulable = false, status = 'disabled', updated_at = NOW()
			WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NULL`, accountID, userID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrAccountNotOwned
		}

		// 移出分组，停止被调度。
		if _, err := tx.ExecContext(txCtx,
			`DELETE FROM account_groups WHERE account_id = $1`, accountID); err != nil {
			return err
		}

		// 计数 -1（不低于 0）。
		if _, err := tx.ExecContext(txCtx, `
			UPDATE owner_earnings
			SET hosted_account_count = GREATEST(hosted_account_count - 1, 0), updated_at = NOW()
			WHERE user_id = $1`, userID); err != nil {
			return err
		}
		return nil
	})
}
