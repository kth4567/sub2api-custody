-- 账号托管市场（Account Custody Marketplace）— 模块 C：用量归属与收益结算
--
-- 参照现有 affiliate（邀请返利）体系的钱模型：
--   可提现额度(earning_quota) / 冻结额度(frozen_quota) / 累计历史(history_quota) + 明细流水(ledger)。
-- 号主（托管订阅号的普通用户）名下账号被调用时，按分成比例产生收益：先进冻结额度，
-- 过“释放期”后转为可提现额度；可提现额度再走模块 D 的提现流程。
--
-- 这些表刻意用裸 SQL + 手写 repository（与 user_affiliates / user_affiliate_ledger 一致），
-- 不接入 Ent，避免 go generate 依赖。

-- ── 号主收益汇总（每个号主一行）───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS owner_earnings (
    user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    earning_quota        DECIMAL(20,8) NOT NULL DEFAULT 0,   -- 当前可提现收益
    frozen_quota         DECIMAL(20,8) NOT NULL DEFAULT 0,   -- 冻结中（未过释放期）
    history_quota        DECIMAL(20,8) NOT NULL DEFAULT 0,   -- 累计历史总收益（只增）
    hosted_account_count INTEGER       NOT NULL DEFAULT 0,   -- 当前有效托管账号数（模块 B 维护）
    created_at           TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE  owner_earnings                      IS '号主（托管账号者）收益汇总';
COMMENT ON COLUMN owner_earnings.earning_quota        IS '当前可提现收益';
COMMENT ON COLUMN owner_earnings.frozen_quota         IS '冻结中的收益（未过释放期）';
COMMENT ON COLUMN owner_earnings.history_quota        IS '累计历史总收益（只增）';
COMMENT ON COLUMN owner_earnings.hosted_account_count IS '当前有效托管账号数';

-- ── 号主收益流水（每次结算/释放/提现/冲正一行，可审计）──────────────────────────
CREATE TABLE IF NOT EXISTS owner_earning_ledger (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,       -- 号主
    action              VARCHAR(20)  NOT NULL,   -- accrue（结算入冻结）| release（冻结转可提现）| withdraw（提现扣减）| reverse（冲正）
    amount              DECIMAL(20,8) NOT NULL,  -- 本次金额（正=入账，负=扣减）
    source_account_id   BIGINT       NULL REFERENCES accounts(id) ON DELETE SET NULL,       -- 产生该收益的托管账号
    basis_cost          DECIMAL(20,10) NULL,     -- 计费基数（该周期该账号被调用成本合计）
    rate                DECIMAL(10,6) NULL,      -- 分成比例快照
    period_start        TIMESTAMPTZ  NULL,       -- 结算周期起（含）
    period_end          TIMESTAMPTZ  NULL,       -- 结算周期止（含）
    earning_quota_after DECIMAL(20,8) NULL,      -- 操作后可提现额度快照
    frozen_quota_after  DECIMAL(20,8) NULL,      -- 操作后冻结额度快照
    history_quota_after DECIMAL(20,8) NULL,      -- 操作后累计历史快照
    ref_id              VARCHAR(64)  NULL,       -- 外部引用/幂等键（如提现单号）
    -- 冻结释放：accrue 行记录成熟时间与是否已释放；release 任务据此把冻结转为可提现。
    mature_at           TIMESTAMPTZ  NULL,       -- 该笔冻结收益的可释放时间（仅 accrue 行有值）
    released            BOOLEAN      NOT NULL DEFAULT false,  -- 该笔冻结是否已释放（仅 accrue 行）
    note                TEXT         NULL,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_owner_earning_ledger_user_created
    ON owner_earning_ledger(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_owner_earning_ledger_action
    ON owner_earning_ledger(action);
CREATE INDEX IF NOT EXISTS idx_owner_earning_ledger_source_account
    ON owner_earning_ledger(source_account_id)
    WHERE source_account_id IS NOT NULL;
-- 幂等：同一 action + ref_id 只允许一条（提现扣减/冲正等带 ref_id 的操作防重放）
CREATE UNIQUE INDEX IF NOT EXISTS uq_owner_earning_ledger_action_ref
    ON owner_earning_ledger(action, ref_id)
    WHERE ref_id IS NOT NULL;
-- 冻结释放扫描：未释放且已成熟的 accrue 行
CREATE INDEX IF NOT EXISTS idx_owner_earning_ledger_release_scan
    ON owner_earning_ledger(mature_at)
    WHERE action = 'accrue' AND released = false;

COMMENT ON TABLE owner_earning_ledger IS '号主收益明细流水（结算/释放/提现/冲正）';

-- ── 结算水位线（单行）：记录已结算到的 usage_logs.id，保证幂等、不重复计费 ──────────
-- usage_logs 是只追加不可变表，无法给行打“已结算”标记，因此用全局水位线：
-- 每次结算处理 last_settled_usage_log_id < id <= ceiling 的日志，然后推进水位线。
CREATE TABLE IF NOT EXISTS owner_earning_settlement_state (
    id                        SMALLINT PRIMARY KEY DEFAULT 1,
    last_settled_usage_log_id BIGINT      NOT NULL DEFAULT 0,
    last_run_at               TIMESTAMPTZ NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT owner_earning_settlement_singleton CHECK (id = 1)
);
INSERT INTO owner_earning_settlement_state(id, last_settled_usage_log_id)
    VALUES (1, 0)
    ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE owner_earning_settlement_state IS '号主收益结算水位线（单行，保证结算幂等）';

-- ── 结算扫描加速：按账号 + 主键范围聚合 usage_logs 的复合索引 ────────────────────
-- 结算查询形如：SELECT account_id, SUM(cost) FROM usage_logs
--   WHERE account_id = ANY(<托管账号集>) AND id > :watermark AND id <= :ceiling GROUP BY account_id;
CREATE INDEX IF NOT EXISTS idx_usage_logs_account_id_id
    ON usage_logs(account_id, id);
