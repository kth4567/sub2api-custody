# 账号托管市场（Account Custody Marketplace）落地手册

本功能在 sub2api 基础上新增「**非管理员用户托管自己的订阅账号 → 被调用时按分成获取收益 → 提现**」的双边市场（对标 cf.ai-pixel 的「号主分成」）。基础版 sub2api 只有管理员全局账号池，本功能补齐了 **归属 / 用量归属计费 / 收益结算 / 提现 / 托管入口** 五层。

> ⚠️ **本手册对应的代码未在开发机编译验证**（开发机无 Go 工具链）。所有 Go/SQL 均按仓库既有约定编写，但**首次编译大概率仍有少量需现场修正的错误**。请严格按「构建步骤」跑一遍 `go generate` + `go build`，并对照「必须现场核对项」自查。

---

## 1. 模块与文件清单

| 模块 | 说明 | 新增/改动文件 |
|------|------|--------------|
| A 账号归属 | accounts 加 `owner_user_id` | `ent/schema/account.go`（改）、`ent/schema/user.go`（改）、`migrations/182_account_ownership.sql` |
| C 收益结算 | 用量→号主收益，水位线幂等 | `migrations/183_owner_earnings.sql`、`internal/service/owner_earning_service.go`、`internal/service/owner_earning_settings.go`、`internal/service/owner_earning_worker.go`、`internal/repository/owner_earning_repo.go` |
| D 提现 | 申请/审核/退额/限流 | `migrations/184_owner_withdrawals.sql`、`internal/service/owner_withdrawal_service.go`、`internal/repository/owner_withdrawal_repo.go` |
| B 托管入口 | 用户自助加号/退管 | `internal/service/owner_account_service.go`、`internal/repository/owner_account_repo.go` |
| HTTP | 用户端 + 管理端接口 | `internal/handler/owner_handler.go`、`internal/server/routes/user.go`（改）、`internal/server/routes/admin.go`（改） |
| 接线 | Wire/Handlers | `internal/repository/wire.go`（改）、`internal/service/wire.go`（改）、`internal/handler/wire.go`（改）、`internal/handler/handler.go`（改） |
| 前端 | 号主中心页 | `frontend/src/api/owner.ts`、`frontend/src/views/user/OwnerCustodyView.vue`、路由与菜单（改，见前端章节） |

数据流：请求命中托管号 → `usage_logs` 记录 `account_id` 与成本 → 后台 worker 周期扫描 `usage_logs`（水位线增量）→ 关联 `accounts.owner_user_id` → 按分成比例入号主**冻结**收益 → 过释放期转**可提现** → 用户提现 → 管理员审核打款。

---

## 2. 构建步骤（必须按序执行）

```bash
cd backend

# 1) 模块 A 改了 Ent schema，必须重新生成 Ent 代码（否则没有 owner_user_id 访问器）
go generate ./ent

# 2) 改了 Wire provider（repository/service/handler 三处 wire.go + ProvideHandlers 签名），
#    必须重新生成 wire_gen.go
go generate ./cmd/server

# 3) 编译（首次可能报错，按报错逐一修，见第 6 节「必须现场核对项」）
go build -tags embed ./...

# 4) 前端（改了 view/api/router）
cd ../frontend && pnpm install && pnpm run build
```

迁移 `182/183/184` 会在服务启动时由 `repository.ApplyMigrations` 自动执行（`migrations/*.sql` 通过 `//go:embed` 内嵌），**无需手动跑 SQL**。

---

## 3. 后台 worker 接线（需手动补一处）

`NewOwnerEarningWorker` 已加入 `internal/service/wire.go` 的 provider set，但 Wire 只会实例化「被依赖」的对象，且 worker 的 `Start()/Stop()` 需要挂到 App 生命周期。参照 `IdempotencyCleanupService` 的做法，在 `cmd/server/wire.go` 里：

1. 给 App 的清理/生命周期入参加上 `ownerEarningWorker *service.OwnerEarningWorker`（这样 Wire 才会构造它）。
2. 启动阶段调用 `ownerEarningWorker.Start()`（与其它 `*.Start()` 放一起）。
3. 关闭阶段在 Stop 注册块里加：
   ```go
   {"OwnerEarningWorker", func() error {
       if ownerEarningWorker != nil {
           ownerEarningWorker.Stop()
       }
       return nil
   }},
   ```
4. 重新 `go generate ./cmd/server`。

> 若暂不接 worker，收益不会自动结算；可临时手动调用 `OwnerEarningService.SettleOnce/ReleaseMaturedFrozen` 验证逻辑。

---

## 4. 需要配置的设置项（在管理后台 setting 里加，或 settings 表插入）

| key | 默认 | 说明 |
|-----|------|------|
| `owner_earning_enabled` | `false` | **总开关**。不开则不结算、不产生收益、禁止托管/提现 |
| `owner_earning_share_rate` | `0.7` | 号主分成比例 (0,1]，平台抽 1-rate |
| `owner_earning_freeze_hours` | `72` | 收益冻结小时数，过后可提现 |
| `owner_earning_basis` | `total_cost` | 分成基数列：`total_cost`（稳定）或 `actual_cost`（随消费端计费） |
| `owner_hosted_group_id` | `0` | **托管号自动加入的分组 id**。为 0 则托管被拒（须先建一个「托管共享池」分组） |
| `owner_max_hosted_per_user` | `10` | 每用户托管上限 |

**上线前置**：先在管理后台建一个分组作为「托管共享池」（把消费用户的 API Key 指向它），拿到它的 id 填进 `owner_hosted_group_id`，再打开 `owner_earning_enabled`。

---

## 5. API 一览

用户端（需登录，`/api/v1` 前缀）：
- `GET  /user/owner/earnings` 收益汇总（可提现/冻结/累计/托管数）
- `GET  /user/owner/accounts` 我的托管账号
- `POST /user/owner/accounts` 托管账号 `{name, platform, type, credentials(JSON字符串)}`
- `DELETE /user/owner/accounts/:id` 退管
- `POST /user/owner/withdrawals` 提现 `{amount, method, account_info}`
- `GET  /user/owner/withdrawals` 我的提现单

管理端（需管理员）：
- `POST /admin/owner/withdrawals/:id/review` 审核 `{approve, note}`

---

## 6. 必须现场核对项（本机无法编译，逐项确认）

1. **Ent 生成**：`go generate ./ent` 后确认生成了 `Account.owner_user_id`、`User.QueryHostedAccounts` 等；`account.go` 的 owner 边为可空（nullable FK）。
2. **`*dbent.Client` 裸 SQL**：本功能仓储用 `client.QueryContext/ExecContext/Tx`（照 `affiliate_repo.go`）。确认该 Client 确实暴露这些方法（仓库启用了 Ent `sql/execquery` 特性——affiliate 已在用，应无问题）。
3. **DECIMAL → float64 扫描**：收益/金额列是 `DECIMAL(20,x)`，`rows.Scan(&float64)` 能否直接工作取决于 PG 驱动。affiliate_repo 同样这么扫，若它能编过运行则本功能一致；如遇 `sql: Scan error`，改用与 affiliate 相同的数值扫描方式。
4. **`response` 包助手**：owner_handler 用了 `response.Success/Created/Unauthorized/ErrorFrom/BadRequest`。前四个已确认存在；**`response.BadRequest` 需确认**，若无请改用现有的参数错误助手。
5. **`logger.LegacyPrintf`**：service/worker 用它打日志，确认签名 `(tag, format, args...)` 一致。
6. **Wire 签名变更**：`ProvideHandlers` 加了 `ownerHandler *OwnerHandler` 参数——**必须** `go generate ./cmd/server` 重新生成 `wire_gen.go`，否则调用处参数对不上。
7. **提现审核幂等**：`ReviewWithdrawal` 用 `FOR UPDATE` 锁单 + 状态判 pending，避免重复退额；确认事务隔离级别下行为符合预期。

---

## 7. 模块 E（风控）—— 当前状态与待补

已内置：
- **归属校验**：退管/审核只允许操作本人/合法单据（`UnhostAccount` 带 `owner_user_id` 条件、`ReviewWithdrawal` 校验 pending）。
- **原子扣额防超提**：提现 `WHERE earning_quota >= amount`。
- **提现限流**：7 天最多 3 次。
- **收益冻结期**：对冲退款/刷量/封号回收。

**建议补齐（尚未实现）**：
- **凭证加密**：`owner_account_repo.HostAccount` 目前把 `credentials` 原样写入 JSONB。生产必须加密存储（参照上游对 OAuth/API Key 的加密方式），避免号主凭证泄露。
- **托管号去重**：防止同一订阅号被多个用户重复上传。可复用仓库已有的 `sub2api:admin:account-duplicate:*` 去重缓存，或对 `credentials` 做指纹哈希唯一约束。
- **有效性校验**：托管时调用 `account_test_service` 验证账号真实可用，无效则拒绝入池。
- **封号联动**：托管号被上游封禁（`accounts.status=error`）时，暂停给该号主结算/打钱并通知。
- **反刷量**：号主自调自己托管号刷收益——**已实现**：结算 SQL 里已加 `AND ul.user_id <> a.owner_user_id`，排除号主本人对自己托管号的用量。
- **管理端提现列表**：当前只提供审核单个单据，建议补 `GET /admin/owner/withdrawals`（分页 + 状态筛选）。

---

## 8. 合规与风险（务必知晓）

- 诱导第三方上传订阅凭证并付费，等于运营**账号池市场**，违反 Anthropic/OpenAI 等上游 ToS，封号是常态。
- 涉及给号主打钱：需防欺诈、退款、洗钱/资金合规。
- 本项目基于 **sub2api（LGPL-3.0）** 二次开发，分发需保留原许可与署名。

本功能仅供技术学习研究，使用产生的一切风险自负。
