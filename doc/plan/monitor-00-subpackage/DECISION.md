# DECISION — monitor-00-subpackage

Decision log. One entry per material decision: choice, rationale, rejected
alternatives.

## D1: Import-cycle breaker — RESOLVED 2026-07-10 (maintainer)

- Choice: hoist `CmdmanConfig` (config*.go) and the shared env helpers
  (env.go) into a new leaf package `pkg/cmdman/config`; `pkg/cmdman`
  keeps the API surface via `type CmdmanConfig = config.CmdmanConfig` and
  `const ENV_CMDMAN_* = config.ENV_CMDMAN_*` aliases, so all consumers
  compile unchanged. Resulting import shape:
  monitor → config ← cmdman → monitor (acyclic).
- Rationale: config.go already depends only on leaf packages; the move is
  mechanical and verbatim; C4's "pkg/cmdman keeps Config" goal is met at
  the API-surface level.
- Rejected: consumer-side interface in `monitor` (monitor code reads
  config struct fields directly — the interface would need getters and
  the move stops being verbatim); hoist without aliases (import churn
  across cli/compose/tui/cmd and ~10 e2e files for no behavioral gain).

## D2: Shared test helpers (`testStore`/`testEnv`) — RESOLVED 2026-07-10 (Stage B)

- Choice: duplicate. Added `pkg/cmdman/monitor/test_helpers_test.go`
  (package monitor) with its own `testStore` + `testEnv`. The staying
  `pkg/cmdman/test_helpers_test.go` was trimmed to `testEnv` only.
- Rationale: after the move the two sides need disjoint subsets —
  `testStore` was consumed only by the moved `mon_test.go`, so keeping it
  in the staying helper would leave an unused unexported func (staticcheck
  U1000). The staying tests (`cmdman_logs_test.go`,
  `cmdman_send-keys_test.go`, `cmdman_mux_test.go`) use only `testEnv`;
  the monitor tests use both. Duplication is two ~5-line helpers, adds no
  package, and follows existing precedent: `pkg/cmdman/store/store_test.go`
  already keeps its own private copies of `testStore`/`testEnv`.
- Rejected: a shared internal test-util package would pull `testing` into a
  non-test production package and mint a whole package for two functions
  used by two callers — heavier and against "one responsibility per
  package".
- Note: the monitor-side helper file also carries a blank import
  `_ "…/logdriver/k8sfile"` so the default log driver is registered for the
  monitor package's isolated test binary (pre-move this came transitively
  from package cmdman; production is unchanged — the cmdman binary still
  links k8sfile via pkg/cmdman).
