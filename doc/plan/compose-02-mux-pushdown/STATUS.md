# STATUS — compose-02-mux-pushdown

Current state: **implemented, awaiting maintainer review** — all seven steps
done 2026-07-04. Review verdict: approve (after one round fixing two behavior
regressions in `mux down`/`mux ls`, see DECISION.md D-02-4). Full suite incl.
e2e and `-race` green.

## Checklist (mirrors PLAN.md steps)

- [x] 1. Move `collectCycleTargets` → `mux.CollectCycleTargets` (+ test)
- [x] 2. `compose.MuxWindowName` + selection resolvers into `pkg/cmdman/compose`
- [x] 3. `compose.Service.MuxUp/MuxDown/MuxLs/MuxCycleScale` + `cmdmanSvc.Config()`;
       thin `compose_mux.go` run-funcs
- [x] 4. Rewire `tui_backend.go` to the Service methods; delete `muxRun` pipeline
- [x] 5. `cmdman.Service.MuxResolver`; rewire `cmd/cmdman/commands/mux.go`
- [x] 6. Unit tests under `pkg/cmdman/compose/`
- [x] 7. Full verification (build, `go test ./...` incl. e2e, review pass)

Next action: maintainer reviews the working-tree change.
