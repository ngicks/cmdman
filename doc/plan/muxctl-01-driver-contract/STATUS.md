# STATUS — muxctl-01-driver-contract

Current state: **implemented, awaiting maintainer review** — all five steps
done 2026-07-04. Review verdict: approve (after one round fixing two
blocking findings: registry-test `-count=2` panic, dangling tmux/doc.go
refs; plus four minors). Full suite incl. e2e and `-race` green; grep-gate
holds (`pkg/cmdman/mux` non-test files import no `muxctl/tmux`).

## Checklist (mirrors PLAN.md steps)

- [x] 1. muxctl contract: Driver interface + vocabulary types + registry
       (+ registry tests); Session extended; doc.go updated
- [x] 2. tmux driver satisfies the contract; init() registration; tests adapted
- [x] 3. cmdman/mux rewired to muxctl.Driver; tmux imports gone (non-test);
       blank import at cmd/cmdman/main.go
- [x] 4. Tests: registry unit tests; real-tmux tests kept/adapted
- [x] 5. Full verification (build, `go test ./...` incl. e2e, review pass;
       grep-gate on `muxctl/tmux` in pkg/cmdman/mux non-test files)

Next action: maintainer reviews the working-tree change.
