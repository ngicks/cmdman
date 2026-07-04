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

- [ ] 1. C5 — flock-based monitor liveness probe (S; latent-bug fix)
- [ ] 2. C1 — compose mux push-down to compose.Service methods (M)
- [ ] 3. C3 — muxctl tier-1 pure-function hoist; scale codec → pkg/cmdman/mux (S)
- [ ] 4. C2 — sqlc adoption + .sql-file migrations + json_each label queries (M-L)
- [ ] 5. C6 — broadcaster[T] -race unit test (S)
- [ ] 6. C10 — log cleanup errors in markMonitorDied (S)
- [ ] 7. C8 — extract detach-key lexer from cli/attach.go (S)
- [ ] 8. C7 — split compose/load.go into discover.go/normalize.go (S-M)
- [ ] 9. C4 — extract pkg/cmdman/monitor subpackage (L; after C5/C6/C10)
- [ ] 10. C9 — split cli/tui_backend.go by tab (S; after C1, if still needed)

Next action: pick up item 1 (C5) — small enough to execute directly per D1.
