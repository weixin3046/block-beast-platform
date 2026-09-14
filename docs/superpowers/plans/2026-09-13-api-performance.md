# API Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish and improve a measurable P95 200 ms latency SLO for all platform-owned HTTP route families without weakening correctness or security.

**Architecture:** Measure first at the handler and PostgreSQL boundaries, then remove only verified duplicate work, N+1 reads, and unindexed query plans. Keep transactional writes authoritative and provider routes separately budgeted.

**Tech Stack:** Go 1.26, net/http, pgx/PostgreSQL 17, Go benchmarks and tests.

**Spec:** `docs/superpowers/specs/2026-09-13-api-performance-design.md`

## Global Constraints

- Work locally only; no deployment or remote profiling.
- Preserve HTTP fields, status codes, authentication, money transactions, idempotency, and audit behavior.
- Measure each route family independently; do not hide provider latency with fabricated success responses.
- Use new migrations only; add an index only after an integration query-plan test justifies it.
- Run `gofmt`, targeted tests, `go test ./...`, `go vet ./...`, race tests for modified packages, and `git diff --check` before completion.

---

### Task 1: Build a route-family benchmark and request timing contract

**Files:**
- Create: `internal/platform/httpapi/performance_test.go`
- Modify: `internal/platform/httpapi/server.go`
- Test: `internal/platform/httpapi/performance_test.go`

**Interfaces:**
- Produces benchmark names grouped by public, authenticated player, admin, transactional, and provider route families.
- Preserves `withRequestLog(http.Handler) http.Handler` and adds only structured latency fields needed to calculate route percentiles.

- [ ] Write handler benchmarks with stub services for `/healthz`, `/v1/platform`, authenticated menu/state reads, an admin list, and a transactional request.
- [ ] Run each benchmark with `-benchmem` and record baseline allocations/op and ns/op in the test output.
- [ ] Add request log fields for HTTP status and duration bucket without logging credentials, request bodies, or tokens.
- [ ] Add HTTP unit tests asserting the logger wrapper preserves status/body behavior.
- [ ] Re-run benchmarks and HTTP API tests.

### Task 2: Eliminate verified N+1 reads in player game and lobby endpoints

**Files:**
- Modify: `internal/application/operations/lulu_menus.go`
- Create: `internal/application/operations/lulu_menus_integration_test.go`
- Test: `internal/application/operations/lulu_menus_integration_test.go`

**Interfaces:**
- Preserves `GetLuluMenus(context.Context) (LuluMenus, error)` and its JSON response shape.
- Produces one ordered query that assembles games, rooms, plays, and currency configs in memory.

- [ ] Add a PostgreSQL integration fixture with three games, six rooms, multiple plays, and currencies.
- [ ] Assert one data query produces the existing nested response ordering and omits disabled data.
- [ ] Replace hierarchical game/room/play query fan-out with one joined ordered query.
- [ ] Run the new integration test with `POSTGRES_TEST_DSN`, then the operations and HTTP API packages.

### Task 3: Audit and batch remaining list/detail route families

**Files:**
- Modify only services proven by query-count fixtures: `operations`, `agent`, `chat`, `credit`, `leaderboard`, `betting`, and `game` packages.
- Create one focused integration test per changed service.

**Interfaces:**
- Existing public methods and pagination/cursor contracts remain unchanged.

- [ ] For each route family in the spec, enumerate its service queries and compare list-plus-count or parent-plus-child reads against a query-count fixture.
- [ ] Add a failing regression test for each confirmed duplicated query.
- [ ] Replace only the confirmed duplicate query with a join, batch query, or read-only transaction snapshot.
- [ ] Run the changed package tests before proceeding to the next family.

### Task 4: Add evidence-backed database indexes

**Files:**
- Create: `migrations/0092_api_query_performance.sql` only if a query-plan fixture proves an uncovered filter/order access pattern.
- Create: `internal/.../*_integration_test.go` for each indexed query.

**Interfaces:**
- Existing SQL result ordering and filter semantics remain unchanged.

- [ ] Capture `EXPLAIN (ANALYZE, BUFFERS)` from each candidate query using the integration fixture.
- [ ] Add a new migration containing only indexes whose plan has an uncovered scan/sort at representative cardinality.
- [ ] Add test data large enough to exercise the candidate order/filter.
- [ ] Verify the expected index is selected or document why PostgreSQL correctly chooses a sequential scan at fixture size.

### Task 5: Set connection and provider latency budgets

**Files:**
- Modify: `internal/config/config.go`, `.env.example`, and the pgx pool construction in each executable if the baseline shows pool contention.
- Modify provider clients only where a missing context deadline or transport reuse is demonstrated.
- Test: corresponding config and client tests.

**Interfaces:**
- Existing environment variables keep their defaults unless new optional `API_*` pool/timeout settings are documented.
- Provider error and timeout semantics remain unchanged.

- [ ] Test default and invalid pool/timeout configuration parsing.
- [ ] Add the minimal bounded pool/acquire configuration only when a benchmark or integration test identifies queueing.
- [ ] Test provider timeout propagation and reuse without making a real network request.
- [ ] Run the affected command and client package tests.

### Task 6: Final verification and report

**Files:**
- Modify: `README.md` or `docs/frontend-api.md` only if configuration or observable response behavior changes.

- [ ] Run `gofmt` on every modified Go file.
- [ ] Run `go test ./...`, `go vet ./...`, and `go test -race` for every modified package.
- [ ] Run the benchmark matrix with `-benchmem` and compare to its recorded baseline.
- [ ] Run `git diff --check` and inspect the final diff for API/transaction changes.
- [ ] Report per-family findings, concrete improvements, measured local results, and remaining provider limitations.
