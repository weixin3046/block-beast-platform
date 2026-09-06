# Core review scope

Read-only review of local uncommitted changes for docs/reference-alignment-design.md tasks 1–2 plus dormant whitelist.

Review application/rebate (all new files), betting/service.go snapshot + cancel lock order, settlement/settle.go new rebate path and rebate_integration_test.go, leaderboard/service.go and self_test.go, httpapi/rebates.go and leaderboard.go/self tests, operations/login_whitelist.go/test and httpapi/login_whitelist.go, migrations0060/0061/0063. Parent integrating other admin controls separately; those are not ready for review.

Spec: config room×game×currency six nondecreasing permille grades; snapshot per new hash bet, legacy retained; road/guess stake and dodge positive profit base; no simulated or virtual payouts; sorted wallet locking; idempotency and money overflow; atomic balance/ledger/outbox; self authenticated out of limit, same snapshot, other users balance hidden. Whitelist per user explicit decision: no third party, return risk_check_enabled=false and whitelist_effective=false; configuration must not alter auth.

Evidence: all core tests passed prior config/snapshot stage. Current leaderboard DB self tests and HTTP privacy tests pass. New rebate record DB assertions pass including pagination total, decimal amounts and beneficiary scope. Whitelist DB permission/normalization/deletion test passed on fresh bb_alignment_final_20260906. Final combined suite not yet run; no need repeat tests during read-only review.

Do not commit/edit code, do not spawn agents, no remote calls. Report concrete critical/important bugs with file/line, spec compliance and quality verdict, to docs/superpowers/plans/core-review-report.md (report file allowed). Existing unrelated users.go edits are not this review scope. User requested no commits, so HEAD range is empty: inspect specified working files and git diff directly for modifications; all new code is untracked.
