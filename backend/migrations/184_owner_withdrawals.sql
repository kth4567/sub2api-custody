-- 账号托管市场（Account Custody Marketplace）— 模块 D：号主提现
--
-- 号主把「可提现收益（owner_earnings.earning_quota）」发起提现：
--   申请时原子扣减 earning_quota（防超额并发），生成 pending 提现单 + withdraw 负额流水；
--   管理员审核：通过 -> paid；驳回 -> 退回 earning_quota + reverse 冲正流水。
-- 限流（7 天最多 3 次）在 service 层用 COUNT 实现。

CREATE TABLE IF NOT EXISTS owner_withdrawals (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount       DECIMAL(20,8) NOT NULL CHECK (amount > 0),
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending',  -- pending | paid | rejected
    method       VARCHAR(30)  NULL,      -- 提现方式：alipay / wechat / usdt / bank ...
    account_info VARCHAR(255) NULL,      -- 收款账号（建议加密或脱敏；勿明文存敏感信息）
    review_note  TEXT         NULL,      -- 审核备注
    reviewed_by  BIGINT       NULL,      -- 审核管理员 user_id
    reviewed_at  TIMESTAMPTZ  NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_owner_withdrawals_user_created
    ON owner_withdrawals(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_owner_withdrawals_status
    ON owner_withdrawals(status);

COMMENT ON TABLE  owner_withdrawals              IS '号主提现申请单';
COMMENT ON COLUMN owner_withdrawals.status       IS 'pending 待审 / paid 已打款 / rejected 已驳回';
COMMENT ON COLUMN owner_withdrawals.account_info IS '收款账号（建议加密存储，勿明文敏感信息）';
