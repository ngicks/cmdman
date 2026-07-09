# STATUS — compose-03-load-split

Current state: **done** — implemented 2026-07-08 (working tree, awaiting
maintainer review). `load.go` deleted; `discover.go` (237 lines) +
`normalize.go` (617 lines), verbatim move, placements recorded in
DECISION.md D1.

## Checklist

- [x] Plan directory scaffolded
- [x] Decl-by-decl assignment (discover vs normalize) done and recorded
- [x] Split implemented (verbatim move, imports adjusted)
- [x] Build + full tests + e2e green (`go test -count=1 ./e2e/...` 152.6s;
      one flaky failure `TestLogs_FollowPreservesStreamSplit` observed once,
      then passed 5/5 reruns and a full uncached rerun — unrelated to this
      change)
- [x] Reviewer pass (approve; verbatim/complete/behavior-preserving
      confirmed; no blocking or minor findings in scope)
- [x] Parent backlog (design_refactors STATUS.md item 9) updated

## Notes

- Known stale references left as-is per review: the draft plan
  `doc/plan/mux-03-override-commands/PLAN.md:237,260,266` cites
  `compose/load.go` line numbers; fix when that plan is picked up
  (`validateMux*` now live in `compose/normalize.go`).
