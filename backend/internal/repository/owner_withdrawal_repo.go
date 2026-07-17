package repository

// owner_withdrawal_repo.go —— 账号托管市场 模块 D：号主提现的裸 SQL 实现。
// 方法挂在 *ownerEarningRepository 上（与收益共用一个仓储）。

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *ownerEarningRepository) CountWithdrawalsSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	rows, err := r.client.QueryContext(ctx,
		`SELECT COUNT(*) FROM owner_withdrawals WHERE user_id = $1 AND created_at >= $2`, userID, since)
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

// CreateWithdrawal 原子扣减可提现额度、建单、写 withdraw 负额流水。余额不足返回 ErrInsufficientEarning。
func (r *ownerEarningRepository) CreateWithdrawal(ctx context.Context, userID int64, amount float64, method, accountInfo string) (int64, error) {
	var newID int64
	err := r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		// 原子扣减：仅当 earning_quota >= amount 时才成功，杜绝并发超额。
		res, err := tx.ExecContext(txCtx, `
			UPDATE owner_earnings
			SET earning_quota = earning_quota - $2, updated_at = NOW()
			WHERE user_id = $1 AND earning_quota >= $2`, userID, amount)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrInsufficientEarning
		}

		// 读回快照。
		var earningAfter, frozenAfter, historyAfter float64
		sr, err := tx.QueryContext(txCtx,
			`SELECT earning_quota, frozen_quota, history_quota FROM owner_earnings WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		if sr.Next() {
			if err := sr.Scan(&earningAfter, &frozenAfter, &historyAfter); err != nil {
				sr.Close()
				return err
			}
		}
		sr.Close()

		// 建提现单。
		wr, err := tx.QueryContext(txCtx, `
			INSERT INTO owner_withdrawals(user_id, amount, status, method, account_info, created_at, updated_at)
			VALUES ($1, $2, 'pending', $3, $4, NOW(), NOW())
			RETURNING id`, userID, amount, ownerNullIfEmpty(method), ownerNullIfEmpty(accountInfo))
		if err != nil {
			return err
		}
		if wr.Next() {
			if err := wr.Scan(&newID); err != nil {
				wr.Close()
				return err
			}
		}
		wr.Close()

		// withdraw 负额流水，ref_id 用单号保证幂等（唯一索引 action+ref_id）。
		_, err = tx.ExecContext(txCtx, `
			INSERT INTO owner_earning_ledger(
				user_id, action, amount, ref_id,
				earning_quota_after, frozen_quota_after, history_quota_after, created_at)
			VALUES ($1, 'withdraw', $2, $3, $4, $5, $6, NOW())`,
			userID, -amount, fmt.Sprintf("wd:%d", newID), earningAfter, frozenAfter, historyAfter)
		return err
	})
	return newID, err
}

func (r *ownerEarningRepository) ListWithdrawals(ctx context.Context, userID int64, limit, offset int) ([]service.OwnerWithdrawal, error) {
	rows, err := r.client.QueryContext(ctx, `
		SELECT id, user_id, amount, status,
		       COALESCE(method, ''), COALESCE(account_info, ''), COALESCE(review_note, ''), created_at
		FROM owner_withdrawals
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.OwnerWithdrawal
	for rows.Next() {
		var w service.OwnerWithdrawal
		if err := rows.Scan(&w.ID, &w.UserID, &w.Amount, &w.Status,
			&w.Method, &w.AccountInfo, &w.ReviewNote, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ReviewWithdrawal 审核：approve=true 置 paid（申请时已扣额，不再动余额）；
// false 驳回并把额度退回 earning_quota + 写 reverse 冲正流水。
func (r *ownerEarningRepository) ReviewWithdrawal(ctx context.Context, id int64, approve bool, reviewerID int64, note string) error {
	return r.withTx(ctx, func(txCtx context.Context, tx *dbent.Client) error {
		// 锁定单据。
		var userID int64
		var amount float64
		var status string
		lr, err := tx.QueryContext(txCtx,
			`SELECT user_id, amount, status FROM owner_withdrawals WHERE id = $1 FOR UPDATE`, id)
		if err != nil {
			return err
		}
		found := false
		if lr.Next() {
			found = true
			if err := lr.Scan(&userID, &amount, &status); err != nil {
				lr.Close()
				return err
			}
		}
		lr.Close()
		if !found {
			return service.ErrWithdrawalNotPending
		}
		if status != "pending" {
			return service.ErrWithdrawalNotPending
		}

		if approve {
			_, err = tx.ExecContext(txCtx, `
				UPDATE owner_withdrawals
				SET status = 'paid', reviewed_by = $2, reviewed_at = NOW(), review_note = $3, updated_at = NOW()
				WHERE id = $1`, id, reviewerID, note)
			return err
		}

		// 驳回：退回可提现额度。
		var earningAfter, frozenAfter, historyAfter float64
		ur, err := tx.QueryContext(txCtx, `
			UPDATE owner_earnings
			SET earning_quota = earning_quota + $2, updated_at = NOW()
			WHERE user_id = $1
			RETURNING earning_quota, frozen_quota, history_quota`, userID, amount)
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

		if _, err = tx.ExecContext(txCtx, `
			INSERT INTO owner_earning_ledger(
				user_id, action, amount, ref_id,
				earning_quota_after, frozen_quota_after, history_quota_after, created_at)
			VALUES ($1, 'reverse', $2, $3, $4, $5, $6, NOW())`,
			userID, amount, fmt.Sprintf("wd-reverse:%d", id), earningAfter, frozenAfter, historyAfter); err != nil {
			return err
		}

		_, err = tx.ExecContext(txCtx, `
			UPDATE owner_withdrawals
			SET status = 'rejected', reviewed_by = $2, reviewed_at = NOW(), review_note = $3, updated_at = NOW()
			WHERE id = $1`, id, reviewerID, note)
		return err
	})
}

// ownerNullIfEmpty 把空串转成 SQL NULL，便于可空列存储。
func ownerNullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
