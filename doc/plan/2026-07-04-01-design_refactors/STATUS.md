# STATUS — design_refactors

Current state: **finalized** — ten candidates (C1-C10) with evidence, agreed
ranking, and all nine decisions resolved (DECISION.md D1-D9). This plan is a
backlog document (per D1); execution happens via per-item plan dirs (or directly
for the small items).

## Checklist

- [x] Plan directory scaffolded
- [x] Scan: compose/mux command-layer push-down (C1)
- [x] Scan: SQL inventory for sqlc (C2)
- [x] Scan: muxctl/tmux genericity classification (C3)
- [x] Scan: general improvement sweep (C4-C10)
- [x] Candidates written up with evidence
- [x] Evaluation & ranking drafted and accepted (D2)
- [x] Label-query options drafted + sqlc json_each prototype gate passed
      (label-query-options.md)
- [x] Open questions resolved (9/9)
- [x] Plan finalized

## Execution backlog (agreed order, none started)

- [x] 1. C5 — flock-based monitor liveness probe (S; latent-bug fix) —
      implemented 2026-07-04 (working tree, awaiting maintainer review)
- [x] 2. C1 — compose mux push-down to compose.Service methods (M) —
      implemented 2026-07-04 via `doc/plan/compose-02-mux-pushdown/`
      (working tree, awaiting maintainer review)
- [x] 3. C3 — muxctl tier-1 pure-function hoist; scale codec → pkg/cmdman/mux
      (S) — implemented 2026-07-04 via `doc/plan/muxctl-00-tier1-hoist/`
      (working tree, awaiting maintainer review)
- [x] 4. C11 — muxctl tier-2 driver-contract extraction: tmux-free
      `pkg/cmdman/mux` (M; promoted by D10, reopening D7) — implemented
      2026-07-04 via `doc/plan/muxctl-01-driver-contract/` (working tree,
      awaiting maintainer review)
- [x] 5. C2 — sqlc adoption + .sql-file migrations + json_each label queries
      (M-L) — implemented 2026-07-05 via `doc/plan/store-00-sqlc/` (working
      tree, awaiting maintainer review). Note: one D9 deviation — sqlc cannot
      parse `ALTER TABLE ... ADD COLUMN`, so its schema input is a
      drift-test-guarded `schema.sql` squash (store-00-sqlc DECISION.md D5).
- [x] 6. C6 — broadcaster[T] -race unit test (S) — implemented 2026-07-05
      directly (per D1, no plan dir): `pkg/cmdman/broadcaster_test.go`, six
      tests incl. slow-consumer drop and concurrent stress; verified under
      `-race -count=5` (working tree, awaiting maintainer review)
- [x] 7. C10 — log cleanup errors in markMonitorDied (S) — implemented
      2026-07-05 directly (per D1): ctx threaded through both callers, both
      os.RemoveAll failures warn via the contextkey logger, mirroring
      maybeAutoRemove (working tree, awaiting maintainer review)
- [x] 8. C8 — extract detach-key lexer from cli/attach.go (S) — implemented
      2026-07-08 directly (per D1): lexer (`detachKeyReader`,
      `parseDetachKeys*`) moved verbatim to `cli/attach_detachkeys.go`, its
      tests to `cli/attach_detachkeys_test.go`; `errDetach` stays in attach.go
      (shared). Reviewed (approve) + full `go test ./...` incl. e2e green
      (working tree, awaiting maintainer review)
- [x] 9. C7 — split compose/load.go into discover.go/normalize.go (S-M) —
      implemented 2026-07-08 via `doc/plan/compose-03-load-split/`: load.go
      deleted, decls moved verbatim to discover.go/normalize.go (working
      tree, awaiting maintainer review)
- [x] 10. C4 — extract pkg/cmdman/monitor subpackage (L; after C5/C6/C10) —
      implemented 2026-07-10 via `doc/plan/monitor-00-subpackage/`: monitor
      machinery → `pkg/cmdman/monitor`; import cycle broken by hoisting
      config*.go + env.go → new leaf `pkg/cmdman/config` with aliases in
      `pkg/cmdman` (working tree, awaiting maintainer review)
- [x] 11. C9 — split cli/tui_backend.go by tab (S; after C1, if still needed) —
      re-checked 2026-07-12: still 718 lines post-C1, split warranted.
      Implemented directly (per D1): tui_backend.go kept as core
      (serviceBackend, ctor, Cwd, shared path helpers); decls moved verbatim
      to tui_backend_commands.go / _compose.go / _mux.go; tui_backend_test.go
      split to match (original deleted). Reviewed (approve, move-only
      verified) + `go build ./...` and full `go test ./...` green (one
      unrelated eventlog flake, passes 5/5 in isolation) (working tree,
      awaiting maintainer review)

Next action: maintainer reviews the C4 change (working tree). All eleven
backlog items are now implemented — this plan is complete pending that
review.
C5 + C1 + C3 + C11 + C2 + C6 + C10 + C8 + C7 + C9 are committed (through
c39a74a).
