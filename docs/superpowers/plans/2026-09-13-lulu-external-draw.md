# Lulu 外部开奖直连实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 安全接入 Lulu 三游戏外部期号、封盘与开奖结果，并继续由本平台完成下注和结算。

**Architecture:** Worker 中的 `luludraw` 协议适配器输出无敏感信息的标准事件；`externaldraw` 事务性同步器创建和关闭外部轮次。`settlement` 按玩法的 `result_map` 从已确认外部结果生成 outcome，复用既有资金和 outbox 事务。

**Tech Stack:** Go 1.26、PostgreSQL/pgx、标准库 TLS/JSON/HMAC/AES、现有 Worker/NATS。

**Spec:** `docs/superpowers/specs/2026-09-13-lulu-external-draw-design.md`

## Global Constraints

- 仅在启用时连接 `lh.lululu.com.cn`、`xdy.lululu.com.cn`、`race.lululu.com.cn`。
- 令牌、UID 与协议密钥复用已有 `lulu_config` 的加密存储，只能在 Worker 内存中解密，不能出现在日志、文档示例或测试快照中。
- TLS 必须验证证书；禁止 `InsecureSkipVerify`。不发送上游投注或资金指令。
- 钱包、账本和 outbox 只经既有结算事务写入；重复来源事件不得重复派奖。
- 每个任务遵循 TDD：先写失败测试、确认 RED、最小实现、确认 GREEN、提交。

---

### Task 1: 规则扩展、外部轮次表和默认玩法

**Files:**
- Create: `migrations/0086_lulu_external_draws.sql`
- Modify: `internal/domain/game/rules.go`
- Test: `internal/domain/game/rules_test.go`

**Interfaces:** 产生 `source:"lulu_ws"` 规则，其中 `extras.external_game` 为 `lh|xdy|race`，`extras.result_map` 为外部结果到玩法 outcome 的映射。

- [ ] **Step 1: Write the failing test**

    `TestParseRulesRejectsLuluRulesWithoutExternalGameOrResultMap` 使用 `source=lulu_ws` 与空 extras，断言 `ErrInvalidRules`；`TestParseRulesAcceptsLuluResultMap` 使用 `external_game=xdy`、`result_map:{"1":["up"]}`，断言成功。

- [ ] **Step 2: Run RED**

    Run: `go test ./internal/domain/game -run 'TestParseRules.*Lulu' -count=1`
    Expected: FAIL，原因是尚未校验 `lulu_ws` extras。

- [ ] **Step 3: Write minimal implementation**

    仅在 `Rules.Source == "lulu_ws"` 时反序列化 extras；拒绝未知游戏、空键、空 outcome 与不属于 `Rules.Outcomes` 的映射值；其他来源保持原语义。

- [ ] **Step 4: Add migration defaults**

    建立 `external_draw_rounds`，唯一键 `(source, game, external_round)`，包含 close/result/conflict/status 与时间戳。种子创建十个 `lulu_ws` game type：星海五玩法、怒翎胜方、绿茵四玩法。每个玩法独立 `result_map`、赔率及限额；用全部启用币种及各自 decimals 动态生成实际最小额 1 和已确认实际金额上限对应的最小单位限额。

- [ ] **Step 5: Run GREEN and commit**

    Run: `go test ./internal/domain/game -count=1`
    Expected: PASS.
    Commit: `git commit -m "feat: 增加噜噜游戏默认玩法规则"`

### Task 2: 安全 WebSocket 协议适配器

**Files:**
- Create: `internal/platform/luludraw/client.go`
- Test: `internal/platform/luludraw/client_test.go`

**Interfaces:** `Event{Game, Kind, Round string; CloseAt *time.Time; Result []string}`；`Client.Run(context.Context, func(Event)) error`。调用者从不获得 token 或 UID。

- [ ] **Step 1: Write the failing tests**

    `TestParseAngryFeatherResult` 输入 `{"e":"3005","d":{"round_id":42,"win_item_id":2}}`，期望 `Round:"42", Result:["2"]`。`TestParseStarSeaFailedRoom` 输入 `{"e":"3004","d":{"roundId":8,"failedRoomId":6}}`，期望 `Result:["6"]`。同时覆盖 race 的 `rank=1`、非法 MAC、未知 host、错误消息不包含令牌。

- [ ] **Step 2: Run RED**

    Run: `go test ./internal/platform/luludraw -run TestParse -count=1`
    Expected: FAIL，包不存在。

- [ ] **Step 3: Write minimal implementation**

    实现有大小上限的 `&` 分段 base64 解码、认证解密、`3002/3006` 当前期与封盘时间、`lh 3005`、`xdy 3004/3005`、`race 3005` 解析。连接仅允许三个固定 host，TLS 最低 1.2 并使用系统证书验证，退避上限 20 秒。

- [ ] **Step 4: Run GREEN and commit**

    Run: `go test ./internal/platform/luludraw -count=1`
    Expected: PASS.
    Commit: `git commit -m "feat: 增加噜噜开奖安全直连适配器"`

### Task 3: 外部轮次同步及冲突保护

**Files:**
- Create: `internal/application/externaldraw/service.go`
- Test: `internal/application/externaldraw/service_test.go`
- Modify: `internal/domain/game/postgres.go`
- Test: `internal/domain/game/postgres_test.go`

**Interfaces:** `externaldraw.Service.Handle(context.Context, luludraw.Event) error`；对同一外部期号创建相应全部玩法的平台轮次。

- [ ] **Step 1: Write the failing tests**

    `TestHandleCreatesAllExternalPlayRoundsForSourceRound` 为两种 xdy 玩法与来源期号 42 断言各有一个 open round。`TestHandleRepeatedResultDoesNotChangeStoredOutcome` 断言相同结果只存一次。`TestHandleConflictingResultDoesNotSettle` 断言冲突记录不覆盖既有结果。

- [ ] **Step 2: Run RED**

    Run: `go test ./internal/application/externaldraw -run TestHandle -count=1`
    Expected: FAIL，包不存在。

- [ ] **Step 3: Write minimal implementation**

    在单事务中锁定/创建外部记录，按 `external_game` 找到已启用玩法并以外部期号作为 sequence 创建 round。封盘使用来源结束时间减安全提前量；结果冲突转为 conflict，不改已确认结果。让 `EnsureScheduledRounds` 排除 `rules.source='lulu_ws'`，避免区块排程生成虚假轮次。

- [ ] **Step 4: Run GREEN and commit**

    Run: `go test ./internal/application/externaldraw ./internal/domain/game -count=1`
    Expected: PASS.
    Commit: `git commit -m "feat: 同步噜噜外部轮次并防止结果冲突"`

### Task 4: 结算结果源与 Worker 生命周期

**Files:**
- Create: `internal/application/settlement/lulu.go`
- Test: `internal/application/settlement/lulu_test.go`
- Modify: `internal/application/settlement/composite.go`, `cmd/worker/main.go`, `cmd/worker/main_test.go`, `internal/config/config.go`, `internal/config/config_test.go`, `.env.example`

**Interfaces:** `LuluResultSource.Outcome(context.Context, game.Round, game.Rules) ([]string,error)`；Worker 仅在配置已启用且完整时运行采集器。

- [ ] **Step 1: Write the failing tests**

    `TestLuluOutcomeMapsRawResultForThePlay` 将已确认 xdy 原始 `1` 映射为 `up`。`TestLuluOutcomeRejectsMissingOrConflictedDraw` 断言返回可重试的未就绪错误。`TestWorkerDoesNotStartLuluClientWhenDisabled` 断言禁用配置不创建连接。

- [ ] **Step 2: Run RED**

    Run: `go test ./internal/application/settlement ./cmd/worker -run 'TestLulu|TestWorker.*Lulu' -count=1`
    Expected: FAIL，结果源和生命周期尚不存在。

- [ ] **Step 3: Write minimal implementation**

    结果源按 `(external_game, round.Sequence)` 查询 confirmed 外部结果，经 `result_map` 生成 outcome；缺失或 conflict 结果返回可重试错误。Composite 在 Tron/OKX 前路由 `lulu_ws`。新增 `LULU_DRAW_ENABLED`、`LULU_DRAW_GAMES`、`LULU_DRAW_CLOSE_BEFORE_SECONDS`；Worker 通过现有 `lulu.Service.RuntimeConfig` 解封已有上下分凭据后启动可取消 goroutine，停机等待退出，仅日志 game/event/round。

- [ ] **Step 4: Run GREEN and commit**

    Run: `go test ./internal/application/settlement ./internal/config ./cmd/worker -count=1`
    Expected: PASS.
    Commit: `git commit -m "feat: Worker 接入噜噜开奖与幂等结算"`

### Task 5: 后台配置契约和全量验证

**Files:**
- Modify: `internal/application/operations/games.go`, `internal/application/operations/games_test.go`, `docs/openapi.yaml`, `docs/frontend-api.md`, `README.md`
- Test: `internal/platform/httpapi/openapi_contract_test.go`

**Interfaces:** 复用现有管理员 game-type 创建/编辑接口维护 `lulu_ws` 玩法；投注继续保存既有赔率快照。

- [ ] **Step 1: Write the failing test**

    `TestCreateGameTypeAcceptsLuluRulesWithoutHashOrKlineRoom` 提交有效 `lulu_ws` 规则，断言后台服务可以保存；无效 `result_map` 返回 `ErrInvalidGameType`。

- [ ] **Step 2: Run RED**

    Run: `go test ./internal/application/operations -run TestCreateGameTypeAcceptsLulu -count=1`
    Expected: FAIL（若现有 room/source 校验仍拒绝外部来源）。

- [ ] **Step 3: Write minimal implementation and docs**

    允许无房间的有效 `lulu_ws` 规则；在 OpenAPI、前端接口文档和 README 明确 `external_game`、`result_map`、赔率、限额及“修改仅影响后续投注”的快照语义。

- [ ] **Step 4: Verify and commit**

    Run: `gofmt -w internal/domain/game/rules.go internal/platform/luludraw/*.go internal/application/externaldraw/*.go internal/application/settlement/lulu.go internal/domain/game/postgres.go internal/application/operations/games.go internal/config/config.go cmd/worker/main.go && go test ./...`
    Expected: all packages PASS.
    Run: `git diff --check && rg -n -i 'InsecureSkipVerify|LULU_DRAW_TOKEN=.+[^[:space:]]|token.*(Print|Info|Error)' . --glob '!docs/superpowers/**'`
    Expected: no TLS bypass and no credential exposure.
    Commit: `git commit -m "docs: 补充噜噜游戏后台配置说明"`
