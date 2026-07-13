# STATUS — monitor-00-subpackage

Current state: **done** — implemented 2026-07-10 (working tree, awaiting
maintainer review). `pkg/cmdman/monitor` (11 production files + 7 test
files) and `pkg/cmdman/config` (5 files) extracted from the flat
`pkg/cmdman`; API surface preserved via aliases (D1).

## Checklist

- [x] Plan directory scaffolded
- [x] Boundary/dependency map (explorer scan; see PLAN.md context)
- [x] Cycle-breaker decided with maintainer (D1: config hoist + aliases)
- [x] Stage A: pkg/cmdman/config hoist + aliases, green
- [x] Stage B: pkg/cmdman/monitor move + rewire, green
- [x] Reviewer pass (approve-with-nits; both nits fixed: stale
      `Service.Events` doc reference in config/config.go, missing doc
      comment on config.WithCommandContextEnv)
- [x] Full uncached tests + e2e green (`go test -count=1 ./...`, e2e
      149.9s, no flakes)
- [x] Parent backlog (design_refactors STATUS.md item 10) updated

## Notes

- Test-only: `pkg/cmdman/monitor/test_helpers_test.go` blank-imports
  `logdriver/k8sfile` to register the default log driver for the isolated
  monitor test binary; production registration is unchanged (via
  `pkg/cmdman/cmdman_logs.go`).
- Exports introduced: `config.WithCommandContextEnv` (was
  `withCommandContextEnv`), `monitor.MarkMonitorDied` (was
  `markMonitorDied`).
