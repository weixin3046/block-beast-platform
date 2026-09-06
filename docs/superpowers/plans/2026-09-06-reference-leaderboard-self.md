# 排行榜本人排名实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 无论本人是否处于返回榜单的前N名，都返回同一周期、同一币种下的本人排名。

**Architecture:** leaderboard 应用服务从相同数据库快照读取列表和本人；HTTP 使用已认证身份，不接收任意 self_user_id。

**Tech Stack:** Go 1.26.5、PostgreSQL、现有金额输出层。

**Spec:** docs/reference-alignment-design.md 本人排名章节。

## Global Constraints

仅本地功能分支，不提交、推送、部署、不新建工作区。只用 apply_patch 修改；遵守 AGENTS.md；金额使用实际数量字符串，保持其他玩家余额裁剪。

### Task 1: 查询和输出本人完整排名

**Files:**
- Modify: internal/application/leaderboard/service.go
- Create: internal/application/leaderboard/self_test.go
- Modify: internal/platform/httpapi/leaderboard.go
- Create: internal/platform/httpapi/leaderboard_self_test.go
- Modify: docs/openapi.yaml、docs/frontend-api.md

**Interfaces:**
- Board 增加 `Self *Entry`，JSON `self` 不省略；没有有效投注排名为null。
- 提供接收登录用户内部UUID的应用查询（如 ListForUser），同时保留现有 List 调用者。HTTP从claims.Subject传入用户，不允许查询参数改变 self 身份。

- [ ] **Step 1: 先写回归测试并确认失败**

```text
three users ranked 1/2/3, viewer third, limit=1 => items.length=1, self.rank=3
viewer absent => self=null (not rank0 fabricated entry)
two currencies => self uses requested currency only
today/yesterday/this_week/last_week => self shares response period
ordinary viewer => other items have no available fields, self may contain own balance
query self_user_id=someone_else does not impersonate that user
anonymous =>401
```

- [ ] **Step 2: 应用查询**

抽取 Entry 扫描/格式化复用函数，避免列表和self金额格式漂移。用 repeatable-read只读事务保证period、items、self在同一快照；self单独按period_id、currency、user_id查询，不受LIMIT影响。无period返回items空/self空。

- [ ] **Step 3: HTTP和文档**

接入新查询，保留原有参数校验和错误语义。不可返回用户内部UUID、账户名或其他玩家钱包余额。增加实际JSON示例，包含列表外本人和未上榜null。

- [ ] **Step 4: 验证并报告**

```sh
go test ./internal/application/leaderboard ./internal/platform/httpapi -count=1
go vet ./...
git diff --check
```

必须运行带POSTGRES_TEST_DSN的集成测试。提供RED/GREEN证据，不提交。主代理进行独立只读审查。
