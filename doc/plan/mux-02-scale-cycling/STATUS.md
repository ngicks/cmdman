# mux-02 — implementation status

Per-workstream live status for `PLAN.md`. States: `todo` / `in-progress` /
`done` / `blocked`. Each implementing agent updates only its own section:
state, a short summary of what changed, and the exact verification commands
run (tests, lint).

## 1. compose-validate

- State: done
- Summary: Added `validateMux` + `validateMuxPane` helpers to `pkg/cmdman/compose/load.go`; called from `Normalize` after `ValidateDAG` when `raw.Mux != nil`. Four unit tests in `mux_validate_test.go` cover: unknown leaf command, pinned scale > command scale (error), pinned scale == command scale (ok), absent scale (cycle target, never errors).
- Verification: `go build ./...` (clean); `go test ./pkg/cmdman/compose/...` (ok, 4 new tests pass); `golangci-lint run ./pkg/cmdman/compose/...` (0 issues).

## 2. driver

- State: done
- Summary: Added `Leaf.CycleKey string` to `pkg/muxctl/spec.go`. In `pkg/muxctl/tmux`: `leafOption`
  constant + stamp/clear in `realizeLeafAt` (via shared `stampLeaf` helper in `leaf.go`);
  `scaleOption` + `decodeScalePositions`/`encodeScalePositions`/`ReadScalePositions`/
  `WriteScalePosition` in new `scale_state.go`; `FindLeafPane` + `RespawnLeaf` +
  `quiesceSinglePane` in new `leaf.go`; `Detach` clears `@cmdman_leaf` and `@cmdman_scale`;
  `OwnedWindow.ScalePositions` decoded from `#{@cmdman_scale}` in `list.go` (5-field format
  with trailing-empty-field fallback). New real-tmux tests in `cycle_scale_test.go`.
- Verification: `go test ./pkg/muxctl/... -count=1` (all pass); `go build ./...` (clean);
  `golangci-lint run ./pkg/muxctl/...` (0 issues).

## 3. mux-layer

- State: done
- Summary: Reworked `pkg/cmdman/mux/build.go` to use `BuildOptions` struct (carries `ScalePositions
  map[string]int`); deleted pseudo-layout expansion — `Build` now emits exactly one `muxctl.Layout`
  per spec layout. Unpinned cycling leaves (Scale==0, non-nil ReplicaCounter) resolve at their
  command's wrapped position and always get `CycleKey` set — including the single-replica case, so
  `@cmdman_leaf` is always stamped for locatability. Fixed a bug in the prior agent's code: the
  previous implementation did not set `CycleKey` when n==1, making single-replica cycle-scale
  targets unlocatable by `FindLeafPane`.
  Added `pkg/cmdman/mux/cycle_scale.go`: `CycleScale`, `CycleScaleOptions`, `CycleScaleResult`,
  `CycleScaleWindowResult`, `ReadScaleState`, `ScaleStateOptions`, and pure helpers
  `computeTargetPosition`, `isCycleScaleTarget`, `pinnedScaleIndices`, `findUnpinnedLeaf`.
  Updated `pkg/cmdman/mux/list.go` (`OwnedWindow`): added `ScalePositions map[string]int` field
  and propagated it from `tmux.OwnedWindow` in `List`.
  Updated `pkg/cmdman/mux/spec.go` (`PaneSpec.Scale` doc): rewritten — Zero marks the leaf as a
  cycle-scale target; positive Scale is pinned and never advanced.
  `pkg/cmdman/mux/run.go`: no change needed (doc already did not mention replica rotation).
  The three `Build` call sites (`cmd/cmdman/commands/mux.go`,
  `cmd/cmdman/commands/compose_mux.go`, `pkg/cmdman/cli/tui_backend.go`) were mechanically adapted
  by the prior agent. New unit tests in `pkg/cmdman/mux/build_test.go` and
  `pkg/cmdman/mux/cycle_scale_test.go` cover: one-layout-per-spec-layout, missing position key →
  replica 1, stored pos > live count wraps, CycleKey set only on unpinned cycling leaves, nil
  ReplicaCounter resolves at index 0, advance wrap, pinned-index skip, all-pinned error, explicit
  out-of-range error, explicit-pinned error, not-a-target error.
- Verification: `go build ./...` (clean); `go test ./pkg/cmdman/mux/... -count=1` (all pass);
  `golangci-lint run ./pkg/cmdman/mux/...` (0 issues).

## 4. cli

- State: done
- Summary: New `compose mux cycle-scale <command>[=N]` subcommand in
  `cmd/cmdman/commands/compose_mux.go` (`cobra.ExactArgs(1)`, `--session` flag wired like
  `down`'s, completion over the spec's unpinned leaf commands via `collectCycleTargets`, layout
  shadowing note extended to cycle-scale). `runComposeMuxUp` and TUI `CycleMux`
  (`pkg/cmdman/cli/tui_backend.go`) read positions via `mux.ReadScaleState` before `mux.Build`
  and pass them through `BuildOptions`. `runComposeMuxLs` opens the cmdman service to resolve
  live replica counts (unresolvable counts render `pos/?`; listing still works without store
  entries). Standalone `mux ls`/`mux up` call sites updated (nil counter, no positions).
  `pkg/cmdman/cli/mux_ls.go`: new SCALE column (`cmd=pos/count` sorted, `cmd=pos` without
  counts, `-` when no cycle targets), LAYOUT became a fixed-width column, `.Scale` template
  field, `MuxLsFormatUsage` updated. New `pkg/cmdman/cli/mux_cycle_scale.go`
  (`RenderCycleScaleResult`, presentation only). Supervisor review fixes: removed a bogus
  `var _ = context.Background` guard; removed the redundant `CycleScaleResult.Err` field
  (the same joined error was returned twice, double-printing failures at the call site —
  renderer is now presentation-only and the command returns `CycleScale`'s error); missing
  replica counts now render `?` instead of `0` per the plan. Added unit tests in
  `pkg/cmdman/cli/mux_ls_test.go` (`buildScaleColumn` edge cases: `-`, positions-only,
  `pos/count`, `pos/?`, default position 1; `RenderMuxWindows` header/row with
  unsorted-duplicated targets).
- Verification: `go build ./...` (clean); `go test ./pkg/cmdman/... ./cmd/... -count=1` (all
  pass); `golangci-lint run ./cmd/... ./pkg/cmdman/cli/... ./pkg/cmdman/mux/...` (0 issues);
  `go build -o /tmp/cmdman ./cmd/cmdman && /tmp/cmdman compose mux cycle-scale --help` (help
  text renders, flags present).

## 5. docs

- State: done
- Summary: Rewrote `doc/man/cmdman-compose-mux.1.md`: added `cycle-scale`
  subcommand section (synopsis, behavior, =N form, visible/not-visible output
  format, five error messages matched to code); rewrote Description to explain
  pinned vs cycle-scale-target leaves, independence of layout/replica cycling,
  persist-across-layouts, reset-on-down; added SCALE column to `ls` section
  (`cmd=pos/count`, `pos/?`, `-`); updated `--format` docs with `.Scale`
  field; extended subcommand shadow note to include `cycle-scale`. Rewrote
  `doc/man/cmdman-mux.5.md`: new `Replica Pinning and Cycle-Scale` section;
  noted one-layout-per-spec-layout (no expansion); documented compose
  static-validation constraint; standalone `scale:` semantics. Updated
  `doc/man/cmdman-compose.5.md`: added `Mux Section` with both static
  validation rules and exact error shapes; updated `mux:` field with
  cross-reference. Updated `doc/man/cmdman-mux.1.md`: fixed cycling wording
  to "layout cycle position" only; added standalone `scale:` paragraph; updated
  SCALE column description and `--format` docs. Updated
  `doc/man/cmdman-compose.1.md`: added `cycle-scale` to mux subcommand listing.
  Fixed stale comment in `example.compose.yaml` (per-invocation rotation
  wording replaced with cycle-scale-target wording).
  Plan/code discrepancy found by the docs pass: the plan states standalone
  `mux ls` shows "stored positions only (`web=2`)" but the code always passed
  nil cycleTargets, so SCALE always rendered `-`. Supervisor resolved it in
  favor of the plan: `buildScaleColumn` now falls back to the window's stored
  positions when no cycle targets are supplied (`cmd=pos`, `-` when none);
  `cmdman-mux.1.md` and the standalone `mux ls` help text were re-aligned.
- Verification: `grep -rn 'cycle\|#2\|scale' doc/man/cmdman-*mux*` (no stale
  per-invocation rotation or pseudo-layout `#2` names; all cycle/scale mentions
  are correct). `grep -n 'cycle\|rotation\|scale' example.compose.yaml` (stale
  comment replaced). No Makefile, CI workflows, or man-page lint step found.

## 6. e2e

- State: done
- Summary: Added `e2e/cmdman/mux_cycle_scale_test.go` with six test functions covering all
  plan scenarios: `TestComposeMuxCycleScale_HappyPath` (up → web-1; cycle-scale → web-2;
  =3 jump → web-3; wrap → web-1), `TestComposeMuxCycleScale_PersistsAcrossLayoutCycle`
  (replica position survives a layout switch), `TestComposeMuxCycleScale_NoWindowError`
  (no dashboard window → error mentioning "compose mux up"),
  `TestComposeMuxLs_ShowsScaleColumn` (SCALE column shows web=1/3 after up, web=2/3 after
  cycle-scale), `TestComposeMuxCycleScale_DownResetsPosition` (down clears @cmdman_scale;
  next up resets to replica 1), `TestComposeMux_MuxValidationScaleExceedsCommand` (static
  validation: mux: leaf scale 4 > commands.web.scale 2 fails to load with "exceeds" error).
  Pre-existing failures in `TestComposeMux_BuildsPanesForServices` and
  `TestComposeMux_NoFileAutoSelectsCwdFile` (pane names now "alpha-1"/"beta-1" instead of
  "alpha"/"beta") were caused by the intended workstream-3 behavior change (unpinned compose
  leaves resolve at replica position 1 and are titled `<command>-1`, consistent with
  cycle-scale's retitling). Supervisor updated both tests to expect the new titles.
- Verification: `go build ./...` (clean); `golangci-lint run ./e2e/...` (0 issues);
  `go test ./e2e/cmdman -run 'Mux' -count=1` (full Mux suite passes, including the 6 new
  tests and the 2 updated pre-existing tests).
