-- 账号托管市场（Account Custody Marketplace）— 模块 A：账号归属
--
-- 为 accounts 增加 owner_user_id：记录"托管账号"的号主（贡献该订阅号的普通用户）。
--   - NULL     = 平台/管理员账号（现有行为完全不变）
--   - 非 NULL  = 某个用户托管进共享池的订阅号
-- 调度器不读取此列，它只按分组/优先级/健康/并发选号；owner_user_id 仅用于
-- 后续的“用量归属 → 收益结算 → 提现”。
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT;

-- 按号主筛选托管账号的部分索引：只索引真正的托管号（owner 非空且未软删除），
-- 避免给海量平台账号（owner_user_id IS NULL）建无用索引项。
CREATE INDEX IF NOT EXISTS idx_accounts_owner_user_id
    ON accounts (owner_user_id)
    WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL;
