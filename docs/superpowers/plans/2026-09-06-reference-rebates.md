# 参考级差返水实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现参考哈希房间/币种/等级级差返水，替代新投注的旧直属佣金。

**Architecture:** 独立 rebate 应用模块持有配置、快照、计算与明细；betting 在创建投注事务中保存快照，settlement 在同事务内发放。保留旧单计算版本，不对历史重发。

**Tech Stack:** Go 1.26.5、PostgreSQL、现有 HTTP 和金额转换层。

**Spec:** docs/reference-alignment-design.md（第一批全部内容是本计划的权威规则）

## Global Constraints

- 只修改当前后端；用户选择当前本地功能分支，不新建 worktree。
- 已有改动已提交为 5cf7f44；后续实现不要自行提交、推送、部署或删除数据。
- 金额内部为最小单位整数，HTTP 为实际数量；保留权限、二级密码、同事务审计、幂等、虚拟隔离。
- 使用 apply_patch 编辑，gofmt 格式化；只新增迁移，0060 起。
- 无远程操作。测试使用本地 POSTGRES_TEST_DSN 环境；不得读取生产环境密钥。

### Task 1: 返水配置到结算完整闭环

**Files:**
- Create: migrations/0060_hash_rebates.sql
- Create: internal/application/rebate/{service.go,calculation.go,snapshot.go,records.go,service_test.go}
- Modify: internal/application/betting/service.go
- Modify: internal/application/settlement/settle.go、commission.go；必要时修正 refund.go 中与结算冲突的锁顺序
- Create: internal/platform/httpapi/rebates.go、rebates_test.go
- Modify: internal/platform/httpapi/server.go、cmd/api/main.go、internal/platform/usermessage/chinese.go
- Modify: docs/openapi.yaml、docs/frontend-api.md、docs/architecture.md

**Interfaces:**
- 新模块提供纯整数级差计算；参数为本金/派奖/玩法以及从近到远代理快照，输出每个收款人的基数、等级、级差和金额。
- 提供 `SnapshotTx(ctx context.Context, tx pgx.Tx, betID string) error`，下单后、事务提交前调用；旧记录默认版本1，新下单显式版本2。
- 提供事务内返水发放和预锁钱包辅助入口，结算负责先锁本期轮次/投注再按ID排序锁全体钱包，不得一边按玩家加锁一边补锁上级造成反序。
- 后台 `GET/PUT /v1/admin/rebate-configs[/{configID}]`；`GET /v1/admin/rebate-records`；本人 `GET /v1/agents/me/rebates`。

- [ ] **Step 1: 先写独立计算回归测试并看见失败**

测试手工期望（基数以最小单位表示）：

```text
base=1000000, levels rates=14/16/20, ancestry=1/2/3 => amounts 14000/2000/4000
stake=100000,payout=108000,mode=dodge => base=8000; level1(14) =>112
stake=100000,payout=0,mode=dodge => no entries
ancestry levels=2/2/1/3 => only first level2 then level3
virtual intermediary => no entry for virtual account, continue real ancestry
base=1,rate=1 => no positive entry
base=MaxInt64,rate=1000 => MaxInt64, no multiplication overflow
```

Run: `GOCACHE=/private/tmp/block-beast-money-cache.1v3zZV go test ./internal/application/rebate -count=1`。

- [ ] **Step 2: 实现计算、迁移与初始化**

采用分段整数乘除：`base/1000*rate + base%1000*rate/1000`。严格使用已批准设计中的六房间六等级千分比数组。配置包含 enabled、version、levels；校验完整1–6档和非递减0–1000整数比例。旧投注默认版本1，真实新单显式版本2并持有完整配置/祖先链快照。缺配置/关闭配置的新单同样保持版本2且发放为空，禁止退回旧佣金。

- [ ] **Step 3: 用数据库测试锁定配置、快照与明细行为，再实现**

测试：144条哈希初始化配置、错误角色拒绝写、版本冲突、非法档位拒绝、真实单快照不随配置/等级变更而变化、模拟单不发放、同级不发放、无上级不发放、余额不足最小单位不写零流水。测试修改配置后原单仍按14‰，新单按更新档位；同一单重复结算只写一份记录。

操作权限从当前数据库角色复核，管理写入口验证 second_password；修改和审计同事务。记录可关联来源投注、玩家、收款代理、房间/期号、基数、比例、金额、状态和日期；金额按币种转换，不混加币种。

- [ ] **Step 4: 接入下单与结算，覆盖并发及旧订单**

下单现有余额扣减、bets插入、快照、ledger和outbox同事务。结算先计算是否中奖和payout，再得出dodge基数。版本1继续旧佣金；版本2只走新返水。把所有玩家及收款人的钱包集合统一排序锁定，涵盖任务领取/转盘既有锁顺序；不要扩大全平台串行锁。付款检查int64余额加法溢出，出错回滚整个结算。

测试旧单仍仅一份旧佣金；退款和取消没有新返水；并发同轮次、多轮次共享上级、上级本人同时投注和抽奖；失败回滚不残留资金/记录。新返水可复用commission账本分类以保持现有看板和代理收入口径，但需有可识别来源，禁止同一金额同时写成两份收入。

- [ ] **Step 5: HTTP、金额字段、文档和旧入口标识**

分页 `{items,total}`；后台可按币种、房间、玩法、来源玩家、收款代理、起止时间查询；本人查询从令牌取身份，不接受任意收款人越权。版本冲突409、非法参数400、无权限403、不存在404。文档列出千分比示例、三类基数、历史切换、初始档位、权限、请求与响应参数。旧commission-rate写入口明确标为仅影响旧单，不静默冒充新配置。

- [ ] **Step 6: 全量验证与只读审查**

```sh
gofmt -w <本任务修改的Go文件>
go test -p 1 ./... -count=1
go test -race ./internal/application/rebate ./internal/application/settlement -count=1
go vet ./...
git diff --check
```

数据库测试必须使用已迁移的本地临时库，未运行或跳过必须在报告注明。最终报告包括修改文件、迁移编号、测试命令/结果、遗留风险；不要提交。由主代理安排只读复核，问题修正后才能认为本批完成。
