# monitor-00 — extract Monitor into `pkg/cmdman/monitor`

One-line summary: move the Monitor machinery (mon*.go, terminal emulation,
ring buffer, broadcaster, posix process prep) out of the flat `pkg/cmdman`
into `pkg/cmdman/monitor`, breaking the resulting import cycle by hoisting
`CmdmanConfig` (+ env helpers) into a new leaf package `pkg/cmdman/config`
with type/const aliases kept in `pkg/cmdman`.

Executes item **C4** of `doc/plan/2026-07-04-01-design_refactors/PLAN.md`
(rank 9 / backlog item 10). Prerequisites C5/C6/C10 (same mon_* files) are
all landed.

## Goal / success criteria

- `pkg/cmdman/monitor` owns: `mon.go`, `mon_run.go`, `mon_server.go`,
  `mon_spawn.go`, `mon_spawn_posix.go`, `mon_clean.go`,
  `prep_process_posix.go`, `terminal_screen.go`, `terminal_state.go`,
  `ringbuffer.go`, `broadcaster.go` + their tests (`mon_test.go`,
  `restart_test.go`, `terminal_*_test.go`, `ringbuffer_test.go`,
  `broadcaster_test.go`).
- `pkg/cmdman/config` (new leaf) owns: `config.go`, `config_linux.go`,
  `config_other.go`, `config_test.go`, `env.go` (shared by both sides;
  missed by C4's file list). Depends only on leaf packages (`model`,
  `store`, `eventlog`, `logdriver`).
- `pkg/cmdman` keeps Service (`cmdman.go`, `cmdman_*.go`), Session
  (`attach_session.go`), and the Config API surface via aliases
  (`type CmdmanConfig = config.CmdmanConfig`, `const ENV_CMDMAN_* =
  config.ENV_CMDMAN_*`), so cli/compose/tui/cmd/e2e consumers compile
  unchanged.
- No import cycle: monitor → config ← cmdman → monitor.
- Behavior-preserving: verbatim moves except the minimal exports/rewires
  listed below; `go build ./...`, `go test ./...` (uncached), e2e green.

## Scope / non-goals

- Non-goals: any behavior change; renaming `CmdmanConfig`; restructuring
  Service; touching `pkg/cmdman/cli|compose|tui|store|...` beyond what
  aliases make unnecessary.

## Context — boundary map (explorer scan 2026-07-10)

- staying → moving crossings: `SpawnMonitor` (mon_spawn_posix.go:36 ←
  cmdman_start.go:42), `WaitForState` (mon_spawn.go:51 ←
  cmdman_start.go:45), `CleanStaleEntries` (mon_clean.go:18 ←
  cmdman_list.go:21), `markMonitorDied` (mon_clean.go:70 ←
  cmdman_stop.go:98, needs exporting), `DaemonizeMonitor`
  (mon_spawn_posix.go:62 ← cmd/cmdman/commands/monitor.go:36).
- moving → staying crossings: `CmdmanConfig` + path-helper methods
  (pervasive), `ENV_CMDMAN_*` consts, `withCommandContextEnv`/
  `hasAnyPrefix` (env.go:5,29 — also used by staying cmdman_create.go:63).
- Clean no-crossing moves: `ringBuffer`, `broadcaster[T]`,
  `screenTracker`, `terminalPaneState` (referenced only by mon_* files
  and own tests).
- e2e references `cmdman.ENV_CMDMAN_*` in ~10 places — aliases keep these
  compiling.
- Straddling tests staying in `pkg/cmdman`: `cmdman_logs_test.go:72` and
  `cmdman_send-keys_test.go:139` call `RunMonitor` → import
  `pkg/cmdman/monitor` from the external test package.
- Shared test helpers `testStore`/`testEnv` (test_helpers_test.go) are
  used by tests on both sides — see DECISION.md D2.

## Implementation steps

1. Stage A — config hoist: move `config*.go` + `env.go` to
   `pkg/cmdman/config` (export `WithCommandContextEnv`); add alias file in
   `pkg/cmdman`; build + tests green.
2. Stage B — monitor move: move the mon/terminal/ringbuffer/broadcaster
   files + tests to `pkg/cmdman/monitor`; export `MarkMonitorDied`; rewire
   `cmdman_start.go`/`cmdman_list.go`/`cmdman_stop.go` and
   `cmd/cmdman/commands/monitor.go`; resolve test-helper sharing; build +
   tests green.
3. Reviewer pass + full uncached `go test -count=1` incl. e2e.

## Testing / verification

- `go build ./...`, `go test -count=1 ./...` (e2e included; note
  `TestLogs_FollowPreservesStreamSplit` is a known flake — rerun on
  failure), golangci-lint via hooks, ng-reviewer pass.
