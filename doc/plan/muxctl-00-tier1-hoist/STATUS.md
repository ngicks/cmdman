# STATUS — muxctl-00-tier1-hoist

Current state: **implemented, awaiting maintainer review** — all three steps
done 2026-07-04. Review verdict: approve-with-nits; all five nits applied
(RMW merge/delete test, stale doc, dead fallback, t.Parallel,
AppendLeafNames test). Full suite incl. e2e and `-race` green. Scaffolded
from parent backlog item C3 (`doc/plan/2026-07-04-01-design_refactors/`).

## Checklist (mirrors PLAN.md steps)

- [x] 1. Hoist pure layout/reuse functions to `pkg/muxctl/` (+ tests moved)
- [x] 2. Scale codec → `pkg/cmdman/mux`; tmux scale helpers + OwnedWindow
       reworked to raw strings; consumers rewired
- [x] 3. Full verification (build, `go test ./...` incl. e2e, review pass)

Next action: maintainer reviews the working-tree change.
