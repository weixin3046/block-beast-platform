# API Performance Design

## Goal

Keep platform-owned HTTP endpoints at a local, database-backed P95 of 200 ms
or less under representative concurrent load. Provider-backed calls (Lulu,
chain, object storage) retain explicit timeouts and are measured separately;
their remote latency is not hidden by returning fabricated successful results.

## Scope

Every HTTP route registered by `internal/platform/httpapi/server.go` is in
scope. The work is grouped so no route family is silently excluded:

| Route family | Examples | Performance review |
| --- | --- | --- |
| Platform, identity and sessions | platform, login, refresh, profile | password-cost boundaries, session queries, response serialization |
| Player game and lobby reads | rooms, menus, rounds, trends, histories | query count, current-state indexes, bounded result sets |
| Wallet, bets and money writes | wallets, ledger, bets, deposits, withdrawals | lock order, transaction round trips, idempotent replay paths; never weaken consistency |
| Community and activity | chat, uploads, leaderboard, red packets, spins, tasks | pagination, ownership checks, object-storage boundaries |
| Agent and operations reads | teams, commissions, reports, audit, dashboards, monitor | filter/order indexes, count/list snapshots, N+1 joins |
| Admin configuration and commands | game/room config, users, roles, security | small writes, optimistic-lock retry behavior, validation overhead |
| Provider and webhook routes | Lulu, PQPA, chain webhooks, file content | timeout budget, connection reuse, asynchronous boundaries, accurate errors |

For every family, capture a representative request matrix, database query
count, allocation profile, and PostgreSQL plan before changing code. Add only
evidence-backed PostgreSQL indexes for filter and ordering patterns, add
repeatable local benchmarks and query-count regression tests, and preserve
authentication, money transactions, idempotency, API shapes, and existing error
semantics.

## Baseline and Measurement

The API request logger already records method, path, and total duration. The
performance suite will add focused Go benchmarks for serialization and service
work that can run without an external dependency. Database integration tests
will use `POSTGRES_TEST_DSN` when supplied and will assert query shape/counts
rather than unreliable wall-clock thresholds.

For deployment validation, use the request-duration logs to calculate per-route
P50/P95/P99 over a fixed sample, and use `EXPLAIN (ANALYZE, BUFFERS)` against
the representative database before and after each added index. A local Docker
database was unavailable during design, so no production-like latency number is
claimed before that validation.

## Design

### 1. Profile every route family before optimization

Add a benchmark harness that executes the authenticated handler stack with
representative request bodies and bounded response payloads. For database-backed
services, integration fixtures must record the number of SQL round trips and
capture `EXPLAIN (ANALYZE, BUFFERS)` for the list and state queries. The harness
reports each route family separately; a single average across fast health checks
and slow reports is not accepted as an SLO.

The full 200 ms objective is a P95 deployment SLO, not a claim that every
endpoint has equal work: uploads, external providers, password hashing, and
large administrative exports have their own published budgets and must not be
made faster by removing required checks.

### 2. Remove N+1 and duplicate work in every read family

For each list/detail endpoint, first replace repeated per-item or per-parent
queries with an ordered join, batched lookup, or request-local map. Reuse one
read-only snapshot when an endpoint needs a list plus a total/count. Keep strict
page limits and cursor semantics. The implementation must cover player,
operations, agent, community, and Lulu reads; the Lulu menu query below is the
first confirmed instance, not the only target.

### 3. Collapse Lulu menu reads into one query

`operations.GetLuluMenus` currently queries games, then rooms for each game,
then plays for each room. A six-room three-game menu therefore requires one
game query, three room queries, and up to eighteen play queries. Replace that
fan-out with one ordered join of shared Lulu game types, enabled rooms, enabled
plays, and currency configurations. Assemble the existing `LuluMenus` response
in memory while preserving ordering and omitting rooms/plays without an enabled
currency configuration.

The public response remains byte-compatible in shape: games contain rooms,
rooms contain plays, and plays contain currency configurations. The endpoint
continues to use the current server time and does not add caching that could
show disabled or changed betting configuration.

### 4. Index verified list and current-state access patterns

Inspect each candidate with an integration test and `EXPLAIN`. The initial
candidates are:

- the latest shared-Lulu round lookup: `(game_type_id, sequence DESC)`;
- the player-visible external-result history lookup: `(source, game,
  external_round DESC)` for confirmed draws;
- operations current accepted-bet listing: an index beginning with `status` and
  the existing descending creation order only if the existing index does not
  cover the actual filtered plan.

Each index must be added in a new migration using `CREATE INDEX CONCURRENTLY`
when its deployment environment requires it, with the migration runner adjusted
only if transactional migration execution would otherwise reject that statement.
No speculative index is added just to satisfy a checklist.

### 5. Connection, serialization, and external-call budgets

Configure the PostgreSQL pool with explicit bounded connection counts and
acquisition timeouts appropriate to the API process. Reuse HTTP transports for
provider clients and assign each provider route a context deadline that leaves
enough time to persist a correct timeout/error state. Keep large JSON responses
bounded by existing pagination and avoid repeated amount conversion or
authorization lookups within one request.

### 6. Bound work, not correctness

Keep existing endpoint limit validation and pagination. Where an endpoint
performs count-plus-list work, use a single read-only snapshot or avoid a count
when the response contract does not need it. Do not add a blanket 200 ms
request timeout to money, betting, authentication, or administrative write
paths: those paths must finish their database transaction or return an accurate
failure.

Provider calls keep their existing finite timeout and return their current
timeout/unavailable error; they are excluded from the platform-owned latency
SLO.

## Testing

- Add a route-family benchmark table covering public, authenticated, admin,
  transactional, and provider route shapes.
- Add query-count regression tests to every modified list/detail service,
  including player, operations, agent, community, and Lulu reads.
- Add a menu assembly test using the same joined row shape, covering three
  games, six rooms, multiple plays, and multiple currencies.
- Add PostgreSQL integration tests for each changed query and index access
  pattern when `POSTGRES_TEST_DSN` is available.
- Add Go benchmarks for handler serialization and service assembly hot paths.
- Run `go test ./...`, `go test -race` for modified packages, `go vet ./...`,
  and `git diff --check` before completion.

## Non-goals

- No production deployment, database operation, or remote profiling.
- No response caching of balances, bets, game state, or administrative data.
- No API field, status-code, authorization, wallet, or settlement semantic
  change.
