# 外部开奖历史接口 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为星海逃杀和怒翎破阵提供受鉴权保护的、已解密的外部开奖历史读取接口。

**Architecture:** `internal/platform/lotterydraw` 封装受限的上游 HTTP 请求、XOR 解密和游戏房间校验；HTTP API 只校验本平台参数和映射错误。API 进程仅在配置了上游基础地址时装配该读取器，整个能力不写入数据库或影响投注、结算。

**Tech Stack:** Go 1.26、标准库 `net/http`/`net/url`/`encoding/json`、现有 `httpapi` 选项注入和 OpenAPI 3.1。

**Spec:** 对话中已确认的“外部开奖历史接口”设计（2026-09-13）。

## Global Constraints

- 仅支持 `star_sea` 与 `angry_feather`；未来游戏不预留可调用路由。
- 仅允许部署配置显式提供上游基础地址；不得在代码、文档或测试中写入目标上游地址。
- 上游请求禁用重定向、使用 5 秒超时、响应体最多 1 MiB，且不重试。
- API 路由必须使用现有 `server.protect` 鉴权包装；本地未配置鉴权时沿用当前兼容行为。
- 不新增数据库迁移、不修改钱包、账本、投注或结算流程。
- 保留已有未提交改动；仅提交本功能的文件，提交信息使用中文。

---

### Task 1: 上游开奖适配器

**Files:**
- Create: `internal/platform/lotterydraw/client.go`
- Create: `internal/platform/lotterydraw/client_test.go`

**Interfaces:**
- Produces: `type Reader interface { History(context.Context, string, int) ([]Record, error) }`
- Produces: `type Record struct { Issue string \`json:"issue"\`; Room []int \`json:"room"\` }`
- Produces: `New(baseURL string) (*Client, error)` 和可用于 HTTP 层错误映射的导出哨兵错误。

- [ ] **Step 1: 写适配器失败测试**

使用本地 `httptest.Server` 写入手工 XOR 密文夹具，断言 `star_sea` 请求 `/api/lottery/history?count=2` 后返回 `{issue:"6653",room:[4]}`；同时写入 `angry_feather` 路径、非法房间号、业务非零 code、无效密文、未知游戏与上游 HTTP 失败的测试。

- [ ] **Step 2: 运行测试，确认因缺少包而失败**

Run: `go test ./internal/platform/lotterydraw`

Expected: FAIL，原因是包或 `New`/`History` 尚未定义。

- [ ] **Step 3: 实现最小适配器**

在 `client.go` 中：解析基础地址，拒绝非 HTTP(S)、用户信息、查询串和片段；以标准库 HTTP 客户端禁用重定向、设置 5 秒超时；用 `url.Values` 编码 `count`。读取最多 1 MiB，校验 `{code:0,data:string}`，使用协议固定 XOR 密钥解密并解析记录，按游戏校验房间范围。

- [ ] **Step 4: 运行适配器测试，确认通过**

Run: `go test ./internal/platform/lotterydraw`

Expected: PASS。

### Task 2: 平台 HTTP 接口与装配

**Files:**
- Create: `internal/platform/httpapi/external_draw_history.go`
- Create: `internal/platform/httpapi/external_draw_history_test.go`
- Modify: `internal/platform/httpapi/server.go`
- Modify: `internal/config/config.go`
- Modify: `cmd/api/main.go`
- Modify: `.env.example`

**Interfaces:**
- Consumes: `lotterydraw.Reader.History(ctx, game, count)`。
- Produces: `GET /v1/external-draws/{game}/history?count=100`，成功体为 `{"game":"star_sea","items":[...]}`。

- [ ] **Step 1: 写 HTTP 路由失败测试**

用实现 `History` 的局部测试桩装配 Server，断言已认证请求的成功响应、缺少读取器返回 503、未知游戏和非法 count 返回 400、上游错误返回 502；同时断言路由由认证包装保护。

- [ ] **Step 2: 运行 HTTP 测试，确认因路由未注册而失败**

Run: `go test ./internal/platform/httpapi -run ExternalDrawHistory`

Expected: FAIL，原因是端点尚未注册或返回 404。

- [ ] **Step 3: 实现最小 HTTP 层和装配**

新增 `ExternalDrawHistoryReader` 与 `WithExternalDrawHistory`，注册 `GET /v1/external-draws/{game}/history` 并通过 `server.protect` 保护。`count` 默认 100、范围 1–100；将预期上游错误映射为 502。配置增加 `LotteryDrawUpstreamURL`，地址为空时不装配读取器；`.env.example` 仅保留空值说明。

- [ ] **Step 4: 运行 HTTP 测试，确认通过**

Run: `go test ./internal/platform/httpapi -run ExternalDrawHistory`

Expected: PASS。

### Task 3: 接口契约与后端文档

**Files:**
- Modify: `docs/openapi.yaml`
- Modify: `docs/frontend-api.md`
- Modify: `README.md`

- [ ] **Step 1: 补全同源 API 契约**

在 OpenAPI 添加认证的 `GET /v1/external-draws/{game}/history`，定义游戏枚举、`count` 范围、成功项和 400/401/502/503 响应。前端接入文档说明该接口已由平台后端解密且不向客户端暴露上游；README 的当前接口表加入端点说明。

- [ ] **Step 2: 运行 OpenAPI 路由契约测试**

Run: `go test ./internal/platform/httpapi -run TestEveryHTTPRouteIsDocumentedInOpenAPI`

Expected: PASS。

### Task 4: 格式化、全量验证和提交

**Files:**
- Modify: 本计划涉及的 Go 文件及文档。

- [ ] **Step 1: 格式化 Go 文件**

Run: `gofmt -w internal/platform/lotterydraw/client.go internal/platform/lotterydraw/client_test.go internal/platform/httpapi/external_draw_history.go internal/platform/httpapi/external_draw_history_test.go internal/platform/httpapi/server.go internal/config/config.go cmd/api/main.go`

- [ ] **Step 2: 运行全量测试**

Run: `go test ./...`

Expected: PASS。

- [ ] **Step 3: 审核改动范围并提交**

Run: `git diff --check`，随后只暂存本计划列出的新增/修改文件，确认未包含已有未提交文件，再以 `feat: 接入外部开奖历史查询` 提交。
