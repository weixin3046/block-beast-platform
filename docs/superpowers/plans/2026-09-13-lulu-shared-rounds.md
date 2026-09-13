# Lulu Shared Settlement Rounds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each Lulu external game create and settle one shared platform round per upstream issue while retaining the game-specific betting plays, limits, and odds.

**Architecture:** Store each external game as one `game_types` row and its selectable plays in `lulu_play_configs`. Resolve play-level validation, payout snapshots, and win conditions from that table inside the existing betting and settlement transactions. Leave `external_draw_rounds` as the upstream event idempotency source and migrate unfinished legacy play rounds to refunded cancellations.

**Tech Stack:** Go 1.26, PostgreSQL migrations, pgx, existing game/betting/settlement services, OpenAPI YAML.

**Spec:** `docs/superpowers/specs/2026-09-13-lulu-shared-rounds-design.md`

## Global Constraints

- Do not connect to or deploy on any remote server; the user performs deployment.
- Preserve settled and cancelled historical bets and rounds exactly as stored.
- New migration files only; never alter released migrations.
- All money and payout arithmetic remains integer minor units.
- Reuse the existing wallet, ledger, outbox, cancellation, and idempotency transactions.
- Run `gofmt` on modified Go files and `go test ./...` before completion.

---

### Task 1: Define and migrate shared Lulu game and play data

**Files:**
- Create: `migrations/0087_lulu_shared_rounds.sql`
- Create: `internal/domain/game/lulu.go`
- Test: `internal/domain/game/lulu_test.go`

**Interfaces:**
- Produces `game.LuluPlay` with `Code`, `Outcomes`, `ResultMap`, `DodgeMode`, `PayoutMultiplier`, `PayoutDivisor`, and per-currency `BetLimits`.
- Produces `game.ParseLuluPlay(row)` and `game.LuluPlay.SelectionAllowed(selection)` for Tasks 2 and 3.

- [ ] **Step 1: Write the failing domain tests**

```go
func TestLuluPlaySelectionAllowedUsesConfiguredOutcomes(t *testing.T) {
    play := LuluPlay{Code: "up_down", Outcomes: []string{"up", "down"}}
    if !play.SelectionAllowed(json.RawMessage(`{"pick":"up"}`)) {
        t.Fatal("configured outcome should be accepted")
    }
    if play.SelectionAllowed(json.RawMessage(`{"pick":"left"}`)) {
        t.Fatal("foreign outcome must be rejected")
    }
}

func TestLuluPlaySelectionWinsHonorsDodgeMode(t *testing.T) {
    play := LuluPlay{Code: "dodge", Outcomes: []string{"1","2"}, DodgeMode: true}
    if !play.SelectionWins(json.RawMessage(`{"pick":"1"}`), []string{"1"}) {
        t.Fatal("dodge pick that is absent from a drawn room should win")
    }
    if play.SelectionWins(json.RawMessage(`{"pick":"1"}`), []string{"1","2"}) {
        t.Fatal("dodge pick that is present in every drawn room must lose")
    }
}
```

- [ ] **Step 2: Run the tests to verify RED**

Run: `go test ./internal/domain/game -run 'TestLuluPlay' -count=1`

Expected: FAIL because `LuluPlay` and its methods do not yet exist.

- [ ] **Step 3: Add the minimal domain model**

Implement `LuluPlay` and pure JSON-selection helpers in `internal/domain/game/lulu.go`. Validate non-empty unique outcomes, positive payout terms, non-empty result mappings that only map to configured outcomes, and valid positive currency limits. Implement `SelectionAllowed` and `SelectionWins` without database access.

- [ ] **Step 4: Run the domain tests to verify GREEN**

Run: `go test ./internal/domain/game -run 'TestLuluPlay' -count=1`

Expected: PASS.

- [ ] **Step 5: Add migration `0087_lulu_shared_rounds.sql`**

Create `lulu_play_configs` keyed by `(game_type_id, code)` with `name`, `sort_order`, `enabled`, `outcomes`, `result_map`, `dodge_mode`, payout fields, and `bet_limits`. Insert one shared game type for each external game (`lulu-xdy`, `lulu-lh`, `lulu-race`) and its default plays. Disable legacy `lulu-xdy-*`, `lulu-lh-winner`, and `lulu-race-*` types. In one transaction, cancel/refund only their unfinished accepted bets using the same monetary invariants as `settlement.RefundRound`, then mark affected rounds cancelled. Do not modify historical settled/cancelled data.

- [ ] **Step 6: Add a migration integration test**

Extend the existing PostgreSQL migration test harness to assert that the migration creates exactly three enabled shared Lulu types, their expected play config counts, disabled legacy types, and leaves an already-settled legacy round unchanged.

- [ ] **Step 7: Run Task 1 tests**

Run: `go test ./internal/domain/game ./internal/application/settlement -run 'TestLulu|TestPostgres' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Task 1**

```bash
git add migrations/0087_lulu_shared_rounds.sql internal/domain/game/lulu.go internal/domain/game/lulu_test.go internal/domain/game/postgres_test.go
git commit -m "feat: add shared Lulu play configuration"
```

### Task 2: Use one shared round for each external issue

**Files:**
- Modify: `internal/application/externaldraw/service.go:69-105`
- Modify: `internal/application/externaldraw/service_test.go`
- Modify: `internal/application/settlement/lulu.go`
- Modify: `internal/application/settlement/lulu_test.go`

**Interfaces:**
- Consumes `lulu_play_configs` and shared `game_types` from Task 1.
- Produces one `rounds` row for each `(external_game, external_round)` and shared game type.

- [ ] **Step 1: Write the failing external-draw test**

Create an integration test that seeds the shared `lulu-xdy` type and its five play configs, calls `Handle` with an `xdy` close event for issue `7095`, and asserts `rounds` contains exactly one open row with sequence `7095` and game type `lulu-xdy`.

- [ ] **Step 2: Run the test to verify RED**

Run: `go test ./internal/application/externaldraw -run TestHandleCreatesOneSharedRoundForExternalIssue -count=1`

Expected: FAIL because `createRounds` currently selects every legacy `game_type` and will not select the new shared representation.

- [ ] **Step 3: Update external-draw round creation**

Change the query in `createRounds` to select the one enabled `game_types` row whose rules source is `lulu_ws` and whose `extras.external_game` matches the event. Keep `ON CONFLICT(game_type_id, sequence) DO NOTHING` and the close-time guard.

- [ ] **Step 4: Write the failing result-source test**

```go
func TestLuluOutcomeReturnsConfirmedRawRoomsForSharedGame(t *testing.T) {
    // Seed confirmed external_draw_rounds xdy issue 7095 with ["2","7"].
    // Resolve lulu-xdy round 7095 and expect []string{"2","7"}.
}
```

- [ ] **Step 5: Run the test to verify RED**

Run: `go test ./internal/application/settlement -run TestLuluOutcomeReturnsConfirmedRawRoomsForSharedGame -count=1`

Expected: FAIL because the source currently maps results through one legacy play's `result_map`.

- [ ] **Step 6: Return raw confirmed external outcomes**

Update `LuluResultSource.Outcome` to read and validate the confirmed raw room values for the matching external game and return them unchanged for the shared round. Retain `ErrBlockNotFound` for a missing or unconfirmed result.

- [ ] **Step 7: Run Task 2 tests**

Run: `go test ./internal/application/externaldraw ./internal/application/settlement -run 'TestHandle|TestLulu' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

```bash
git add internal/application/externaldraw/service.go internal/application/externaldraw/service_test.go internal/application/settlement/lulu.go internal/application/settlement/lulu_test.go
git commit -m "feat: create one shared round per Lulu issue"
```

### Task 3: Resolve shared Lulu play rules during betting and settlement

**Files:**
- Modify: `internal/application/betting/service.go:431-760`
- Modify: `internal/application/betting/service_test.go`
- Modify: `internal/application/settlement/settle.go:28-300`
- Modify: `internal/application/settlement/settle_test.go`
- Create: `internal/application/settlement/lulu_play.go`
- Test: `internal/application/settlement/lulu_play_test.go`

**Interfaces:**
- Consumes `game.LuluPlay` and `lulu_play_configs` from Task 1.
- Produces correctly snapshotted bets and per-play win decisions for a shared Lulu round.

- [ ] **Step 1: Write the failing betting test**

Create an integration test that opens a `lulu-xdy` shared round, places one `up_down` bet and one `dodge` bet with the same round ID, then asserts both are accepted, each has its own payout snapshot, and an unknown `play_mode` is rejected with `ErrSelectionOutsidePlay`.

- [ ] **Step 2: Run the test to verify RED**

Run: `go test ./internal/application/betting -run TestPlaceBetUsesSharedLuluPlayConfig -count=1`

Expected: FAIL because current non-hash placement ignores `play_mode` and validates selections only against the shared game rule pool.

- [ ] **Step 3: Add shared Lulu play lookup to betting**

Detect `rules.Source == "lulu_ws"` in `placeBetTx`. Require `request.PlayMode`, load the enabled play config for the locked round's game type, validate `selection.pick` and the selected currency limit, and use the play's payout terms for `payout_multiplier_snapshot` and `payout_divisor_snapshot`. Keep `game_room_id` empty, preserve standard request idempotency, and do not enable hash order merging or rebate snapshots.

- [ ] **Step 4: Write the failing settlement test**

Create a shared `lulu-xdy` round with accepted `up_down` and `dodge` bets. Settle raw result `["2", "7"]`; assert each bet is evaluated with its own result map and dodge semantics rather than `hashSelectionWins`.

- [ ] **Step 5: Run the test to verify RED**

Run: `go test ./internal/application/settlement -run TestSettleSharedLuluRoundByPlayMode -count=1`

Expected: FAIL because `SettleRound` currently routes every non-empty `play_mode` through `hashSelectionWins`.

- [ ] **Step 6: Add per-play settlement resolution**

Create `lulu_play.go` with a transaction-scoped loader for all play configs for a shared Lulu round. In `SettleRound`, use that lookup for each non-empty shared Lulu `play_mode`, map raw room values via that play's `result_map`, and call `LuluPlay.SelectionWins`. Continue using the existing payout snapshot and all existing wallet, ledger, rebate, task, and outbox operations.

- [ ] **Step 7: Run Task 3 tests**

Run: `go test ./internal/application/betting ./internal/application/settlement -run 'TestPlaceBetUsesSharedLulu|TestSettleSharedLulu' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

```bash
git add internal/application/betting/service.go internal/application/betting/service_test.go internal/application/settlement/settle.go internal/application/settlement/settle_test.go internal/application/settlement/lulu_play.go internal/application/settlement/lulu_play_test.go
git commit -m "feat: settle shared Lulu plays by configuration"
```

### Task 4: Expose shared plays and revise API documentation

**Files:**
- Modify: `internal/application/operations/games.go`
- Modify: `internal/application/operations/games_test.go`
- Modify: `docs/openapi.yaml`
- Modify: `docs/frontend-api.md`
- Modify: `README.md`

**Interfaces:**
- Produces `lulu_plays` in the game-type response for shared Lulu types.
- Documents `game_type` values `lulu-xdy`, `lulu-lh`, and `lulu-race`, plus required `play_mode` for their bets.

- [ ] **Step 1: Write the failing operations test**

Seed a `lulu-xdy` shared type with two enabled play configs, call `ListGameTypes`, and assert that its response includes the two plays in `sort_order` with selection options, odds, and limits but does not expose disabled plays.

- [ ] **Step 2: Run the test to verify RED**

Run: `go test ./internal/application/operations -run TestListGameTypesIncludesEnabledLuluPlays -count=1`

Expected: FAIL because `operations.GameType` currently exposes only the raw `rules` object.

- [ ] **Step 3: Expose safe play configuration**

Add a typed `LuluPlays` field to `operations.GameType`; load it only for `lulu_ws` shared types. Include code, display name, sort order, outcomes, dodge flag, payout terms, and limits. Do not expose raw upstream credentials or unneeded external event metadata.

- [ ] **Step 4: Update OpenAPI and frontend guidance**

Replace descriptions that direct clients to legacy `lulu-xdy-direct`-style game types. Document that clients query a shared game type, display `lulu_plays`, and submit `round_id`, `play_mode`, and `{ "pick": "..." }` without a room ID.

- [ ] **Step 5: Run API contract tests**

Run: `go test ./internal/application/operations ./internal/platform/httpapi -run 'TestListGameTypesIncludesEnabledLuluPlays|TestOpenAPI' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add internal/application/operations/games.go internal/application/operations/games_test.go docs/openapi.yaml docs/frontend-api.md README.md
git commit -m "docs: expose shared Lulu play contracts"
```

### Task 5: Final integration verification

**Files:**
- Modify: files only if validation reveals a defect directly related to Tasks 1–4.

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w internal/domain/game/lulu.go internal/application/externaldraw/service.go internal/application/externaldraw/service_test.go internal/application/settlement/lulu.go internal/application/settlement/lulu_test.go internal/application/settlement/lulu_play.go internal/application/settlement/lulu_play_test.go internal/application/settlement/settle.go internal/application/settlement/settle_test.go internal/application/betting/service.go internal/application/betting/service_test.go internal/application/operations/games.go internal/application/operations/games_test.go`

- [ ] **Step 2: Run targeted migration, Worker, and external-draw tests**

Run: `go test ./internal/domain/game ./internal/application/externaldraw ./internal/application/betting ./internal/application/settlement ./internal/application/operations ./cmd/worker -count=1`

Expected: PASS.

- [ ] **Step 3: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Review the change set**

Run: `git diff --check HEAD^` and `git status --short`

Expected: no whitespace errors and no unrelated modifications.

- [ ] **Step 5: Commit any validation-only correction**

```bash
git add <only-files-corrected-during-validation>
git commit -m "fix: verify shared Lulu round integration"
```
