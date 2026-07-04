# compose-02 — push `[compose] mux` logic down into `compose.Service`

One-line summary: move the mux orchestration out of `cmd/cmdman/commands/`
(`compose_mux.go`, `mux.go`) and `pkg/cmdman/cli/tui_backend.go` into
`compose.Service` methods, killing the three-way duplication.

Executes item **C1** of `doc/plan/2026-07-04-01-design_refactors/PLAN.md`
(rank 2). API shape fixed by that plan's DECISION.md **D8**: uniform
`MuxUp`/`MuxDown`/`MuxLs`/`MuxCycleScale` methods on `compose.Service`.

## Goal / success criteria

- `cmd/cmdman/commands/compose_mux.go` run-funcs are thin: resolve selection →
  build service → one `compose.Service` method → `cli.Render*` (matching the
  `compose_up.go` / `compose_down.go` pattern).
- `serviceBackend.muxRun` and its duplicate resolver in
  `pkg/cmdman/cli/tui_backend.go` are deleted; the TUI backend calls the same
  `compose.Service` methods.
- Standalone `cmd/cmdman/commands/mux.go` uses a new `cmdman.Service.MuxResolver`
  instead of its inline resolver closure.
- New methods have unit tests under `pkg/cmdman/compose/` where testable without
  tmux; `go build ./...`, `go test ./...`, e2e all green; no user-visible
  behavior change.

## Scope / non-goals

- Non-goals: any muxctl/tmux-layer change (that is C3), TUI feature work,
  splitting tui_backend.go by tab (C9 — do after this, if still needed).

## Context (verified against working tree 2026-07-04)

Three copies of the same orchestration:

1. `runComposeMuxUp` — `cmd/cmdman/commands/compose_mux.go:233-301`
   (resolver + `mux.ReadScaleState` + `mux.Build` + `mux.Run`).
2. `serviceBackend.muxRun` — `pkg/cmdman/cli/tui_backend.go:422-474`
   (same pipeline; differences: no SessionName, `io.Discard` stdout, `"mux: "`
   error prefixes, inline window-name derivation `tui_backend.go:459-462`).
3. `runComposeMuxCycleScale` — `compose_mux.go:381-447` (same resolver +
   `PaneArgvOpts` assembly feeding `mux.CycleScale`).

Two near-identical selection resolvers with **deliberately different**
auto-select behavior (must stay distinct):

- CLI `resolveComposeMuxSelection` (`compose_mux.go:496-511`): no `-f` →
  cwd-based `compose.SelectMuxProject(opts)`.
- TUI `resolveMuxSelection` (`tui_backend.go:479-499`): empty composeFile →
  `opts.File = projectName`, then `compose.LoadOrProject`.

Other movables:

- `collectCycleTargets`/`collectUnpinnedLeafCommands`
  (`compose_mux.go:525-553`) — pure `mux.Spec`/`mux.PaneSpec` walk.
- `composeMuxWindowName` (`compose_mux.go:515-520`) — duplicated inline at
  `tui_backend.go:459-462`.
- Standalone-mux inline resolver (`cmd/cmdman/commands/mux.go:202-212`) —
  suffixes `<leaf>-<scaleIndex>` and resolves via `svc.Inspect`.

Hazards (from the parent scan, confirmed):

- `cmdmanSvc` consumer interface (`pkg/cmdman/compose/service.go:16-29`) lacks
  `Config()`, which `PaneArgvOpts` needs (`DataDir`/`RuntimeDir`).
- `os.Executable()` called at all three sites with different error wording
  (`"locate cmdman binary: %w"` vs `"mux: locate cmdman binary: %w"`).
- New methods need a pluggable `io.Writer` (TUI passes `io.Discard`, CLI passes
  `cmd.OutOrStdout()`); CLI passes `--session`, TUI passes none.

## Approach

Per D8: four methods on `compose.Service`, each taking an options struct that
carries the per-call-site variation (Selection, Layout, SessionName, Stdout):

- `Service.MuxUp(ctx, MuxUpOptions) error` ← `compose_mux.go:247-300` +
  `tui_backend.go:425-473`.
- `Service.MuxDown(ctx, MuxDownOptions) error` ← `compose_mux.go:306-326`
  (kept a method for the uniform surface even though it never touches `s.svc`).
- `Service.MuxLs(ctx, MuxLsOptions) (MuxLsResult, error)` ← non-render part of
  `compose_mux.go:328-375`; the result carries windows + replica counts +
  cycle targets; cmd keeps `cli.RenderMuxWindows`.
- `Service.MuxCycleScale(ctx, MuxCycleScaleOptions) (mux.CycleScaleResult, error)`
  ← `compose_mux.go:407-446`; `<cmd>[=N]` arg parsing stays in cmd; cmd keeps
  `cli.RenderCycleScaleResult`.

Supporting moves:

- `mux.CollectCycleTargets(spec)` ← `collectCycleTargets` +
  `collectUnpinnedLeafCommands` (pure walk, belongs beside `mux.PaneSpec`).
- `compose.MuxWindowName(selection)` (or a `ProjectSelection` method) ←
  `composeMuxWindowName`; TUI inline copy deleted.
- Selection resolvers move to `pkg/cmdman/compose` as two thin entry points
  sharing the mux-section validation (CLI cwd-auto-select vs TUI
  project-name-fallback behaviors preserved verbatim).
- `cmdman.Service.MuxResolver() mux.Resolver` (name per existing `mux` types) ←
  inline closure in `mux.go:202-212`; `compose.Service` methods keep using
  `MuxLeafResolver`.
- Extend `cmdmanSvc` with `Config() ...` (still satisfied by `*cmdman.Service`).

Rejected alternatives: package-level functions instead of methods (rejected in
parent D8); passing config values into each options struct instead of extending
`cmdmanSvc` (more churn at every call site for no testability gain).

## Implementation steps (each independently verifiable)

1. **mux.CollectCycleTargets**: move the pure walk to `pkg/cmdman/mux`
   (new file beside `PaneSpec`), export, add a unit test; rewire
   `compose_mux.go` (`runComposeMuxLs`,
   `completeComposeMuxCycleScaleTargets`). Build + test green.
2. **compose selection/window-name helpers**: add
   `compose.MuxWindowName` and the two selection entry points to
   `pkg/cmdman/compose`; rewire `compose_mux.go` and `tui_backend.go` to them;
   delete both local resolvers. Unit tests for window-name and the
   mux-section validation. Build + test green.
3. **compose.Service mux methods**: extend `cmdmanSvc` with `Config()`; add
   `MuxUp`/`MuxDown`/`MuxLs`/`MuxCycleScale` + options/result types in a new
   `pkg/cmdman/compose/mux.go` (declaration order per repo convention:
   `<Op>Option(s)` → `<Op>Result` → methods). Rewire the four
   `compose_mux.go` run-funcs into thin wrappers. Build + test green.
4. **TUI rewire**: replace `serviceBackend.muxRun` body (and `CycleMux`/
   `ApplyLayout` plumbing) with `compose.Service.MuxUp` calls
   (`Stdout: io.Discard`, no SessionName); delete the duplicated pipeline.
   Build + test green.
5. **Standalone mux resolver**: add `cmdman.Service.MuxResolver`; rewire
   `cmd/cmdman/commands/mux.go`. Build + test green.
6. **Unit tests** under `pkg/cmdman/compose/` for the new surface (options
   plumbing, selection resolution, window naming; tmux-dependent paths remain
   e2e territory).
7. **Full verification**: `go build ./...`, `go test ./...` (incl. e2e),
   review pass.

## Testing / verification

- Behavior-preserving refactor: e2e suite (`e2e/cmdman`) is the safety net for
  the tmux-touching paths.
- New unit tests: `mux.CollectCycleTargets`, selection resolvers,
  `MuxWindowName`, and whatever of the Service methods is exercisable without
  a tmux server.

## Risks

- Deliberate CLI-vs-TUI behavioral differences (auto-select, session, stdout,
  error prefixes) must survive the consolidation — parameterize, don't unify.
- Error-string wording changes where the three copies disagreed (see D-02-2).
- Import-cycle watch: `pkg/cmdman/compose` already imports `pkg/cmdman` and
  `pkg/cmdman/mux`; the moves add no new edges.

## Open questions

None — API shape fixed by parent D8; micro-decisions recorded in DECISION.md
(D-02-1..3).
