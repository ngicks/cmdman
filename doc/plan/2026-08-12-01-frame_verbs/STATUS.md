# Status

**Current state: implemented — all nine steps done, final review
passed (approve-with-nits; nits fixed or recorded below).**
(2026-08-14)

Idea gate passed (IDEA.md confirmed as written), all nine questions
resolved (DECISION.md V1–V9), traceability gate passed below.

Standalone plan delivering the quicklaunch parent plan's step 15 (the
parent predates sub-plan management and is not restructured — user's
call). Everything below the verbs exists: `cmdman/frame` (parent steps
11–12), the widgets (step 13), the driver contract
([muxctl plan](../2026-08-10-01-muxctl_first_class_frame/STATUS.md),
implemented 2026-08-12), and the `default_frame` config key (added
2026-08-12 after review caught it unowned).

## Question resolution

All resolved 2026-08-13.

- [x] Q9 frame presence across window switches → V9 (`mux up`
      auto-shows `default_frame`)
- [x] Q1 verb mount → V1 (`cmdman mux frame …`)
- [x] Q2 `select` verb → V2 (folded into `show DEF`)
- [x] Q3 cycle order & start → V3 (sorted `ListNamedDefs`; routine
      call, noted)
- [x] Q4 `frame ls` → V4 (ships now)
- [x] Q5 D17 hook-override owner → V5 (owned here, step 5)
- [x] Q6 switcher interaction set → V6 (navigate-only + `--no-quit`;
      no `q` binding when docked — user's refinement)
- [x] Q7 managed entry identity → V7 (`frame-<def>-<i>` + adopt)
- [x] Q8 in-widget collapse gesture → V8 (`z` runs hide)

## Implementation checklist (mirrors PLAN.md steps)

- [x] 1. Def resolution + FrameShow/FrameHide service layer (2026-08-13)
- [x] 2. `mux up` auto-show of `default_frame` (V9) (2026-08-13)
- [x] 3. Cycle composition (V3) (2026-08-13)
- [x] 4. Managed entry lifecycle (D19/F7/V7) (2026-08-13; see the
      F7 note under "Follow-ups")
- [x] 5. Frame def `hooks:` field (D17/V5) (2026-08-13)
- [x] 6. CLI wiring incl. `ls` + man pages (V1/V2/V4) (2026-08-13)
- [x] 7. Switcher selection actions + `--no-quit` (D6/D22/V6)
      (2026-08-13)
- [x] 8. Collapse gesture `z` (D16/V8) (2026-08-13)
- [x] 9. Lifecycle e2e (parent step-15 verify) (2026-08-14;
      `e2e/cmdman/mux_frame_lifecycle_test.go`, see the two notes under
      "Follow-ups")

## Follow-ups (recorded during implementation, 2026-08-13)

- **No viewer quiesce on hide/cycle — by design, resolved as V10.**
  The viewer pane dies with the frame; the supervised command's
  survival is the invariant and holds (see DECISION.md V10 for the
  full grounds and the rejected driver-quiesce alternative).
- ~~`show <unresolvable-name>` reports the path tried, not the
  candidate list; the decode error is double-wrapped~~ — fixed
  2026-08-14: a bare name that resolves to nothing appends
  `; available defs: …` (or names the empty frame dir), an explicit
  path or a parse error stays bare, and `DecodeFile` no longer
  re-prefixes the `*os.PathError`. Residue: IDEA.md's "paths tried"
  (plural — enumerating the `.yaml`/`.yml` candidates probed) would
  need a `DiscoverFile` signature change; and
  `cmdman/compose/discover.go:220-232` carries the same doubling,
  untouched (compose is out of this plan's scope).
- `mux frame ls` outside tmux succeeds (defs with `-`), by design of
  `FrameList`'s session filter — deviates from the step-6 verify
  line's "outside-tmux error", which the man page documents.
- ~~`compose mux` passes no `Config` to `mux.Run`, so V9 auto-show
  does not apply there~~ — extended 2026-08-14 (user's call, V9
  amendment): `Config`/`Svc` are threaded through compose's
  `cmdmanSvc` seam, so every `Service.MuxUp` caller — `compose mux
  up`, `compose up --mux`, the TUI mux and launcher paths — gets V9.
  e2e: `TestComposeMuxFrame` (shown with a managed def / unset stays
  bare); the already-framed and broken-def branches are shared code
  covered by `TestUpAutoFrame`.
- `show` of the def already up is a strict no-op (V2) and so cannot
  revive a managed command that died while shown; recovery is
  hide+show or `cmdman start frame-<def>-<i>`.
- **Driver defect the step-9 e2e turned up — belongs to the muxctl
  plan, not fixed here.** `findOrCreateWindow` builds
  `new-window -d -t <sessionName>` (`pkg/muxctl/tmux/tmux.go:300-304`).
  When a window in that session is named exactly like the session,
  tmux resolves the target to *that window* and refuses. Standalone
  mux's own default is that shape — `WindowName` defaults to
  `SessionName` (`cmdman/mux/run.go:130-133`) — so a second dashboard
  in the session cannot be built. Reproduced on tmux 3.7b with the
  built binary, `mux up -s work` then `compose mux up -s work`:

  ```
  error: tmux: find-or-create window "cmdman-proj" in session "work":
  tmux new-window -d -t work -n cmdman-proj -P -F #{window_id}:
  exit status 1: create window failed: index 1 in use
  ```

  At the tmux level, `-t life` fails against a session `life` whose
  window is named `life` while `-t "=life:"` succeeds — and that
  `=<session>:` form is the one the same file already uses for exactly
  this ambiguity (`tmux.go:59-67,78`). A fix wants its own driver test
  for a second window in a session whose window is named like it.
- The step-9 e2e's switch leg is therefore cross-session (the project
  dashboard goes up in the default `cmdman` session), and a scripted
  server has no client attached, so it proves `select-window` — the
  `switch-client` half of `FocusWindow` has nobody to move
  (`pkg/muxctl/tmux/focus.go:42-48`). Pointing the frame verbs with
  `-s` likewise means the driver's "a window holding a frame but no
  project is taken over in place" branch
  (`pkg/muxctl/tmux/reuse.go:98`) is not what puts the project under
  the frame there; it stays covered by the muxctl unit tests.

- **Component panes get no root flags (final review, 2026-08-14).**
  `frame.WidgetArgv` emits `<exe> tui widget <name> --no-quit` with no
  `--data-dir`/`--runtime-dir`/`--config`, while the managed viewer
  pane beside it forwards the dirs (`cmdman/mux/build.go` paneArgv).
  Under `cmdman --data-dir X mux frame show dev` a docked widget reads
  the default dirs, not X; env-supplied dirs are unaffected (children
  inherit the environment — the lifecycle e2e leans on exactly that).
  Fix means a `WidgetArgv` signature change — deferred as a design
  call.
- Test-coverage gaps noted by the final review, deferred: no renderer
  test for `cmdman/cli/mux_frame.go` (`frameLsRows`' union of on-disk
  and shown-by-path defs); standalone `mux up` auto-show untested
  with a `managed:`/`component:` def (the compose side covers a
  managed def end to end via `TestComposeMuxFrame/shown`,
  2026-08-14). ~~The ensure restart branch~~ closed 2026-08-14 by
  `TestEnsure/restart` during the FrameSvc refactor.
- **FrameSvc refactor (2026-08-14, user's call in three rulings):**
  frame semantics belong to `cmdman/mux`, and `cmdman.Service`
  carries no mux-typed method at all. Final shape: the ensure state
  machine (adopt/restart/create, find-by-name) lives in
  `cmdman/mux/frame_command.go`; `mux.FrameSvc` asks only
  `Config`/`ListCommands`/`CreateCommand`/`Start` in mux-owned
  types; the one implementation is the `cmdman/cli.FrameSvc` adapter
  (`cli.NewFrameSvc`), the layer that may import both sides; compose
  receives it via `compose.WithFrameSvc(...)` injection.
  Known consequences, deliberate: (a) the adapter in `cli` is wiring
  in the presentation package — the only existing package importing
  both; a dedicated wiring package is the alternative if it grates.
  (b) `WithFrameSvc` is a silent default — a compose Service built
  without it still shows defs, but `managed:` entries warn instead
  of starting; wired at all three `MuxUp`-reachable construction
  sites, e2e-covered only on `compose mux up` (`compose up --mux`
  and the TUI paths are wired but not e2e-tested with a managed
  def).

## Traceability gate — passed 2026-08-13

Every operative clause, inherited and local, mapped to an owning step:

- D15 "via config or a command flag" → steps 1, 2
- D15 "shown / hidden / selected / cycled" → steps 1, 3, 6 ("selected"
  = `show DEF` replace, V2)
- D16 "collapse gesture" → step 8 (V8)
- D17 "override the default hooks" → step 5 (V5)
- D19/F7 managed viewer detach-before-kill → step 4 (V7)
- D6 switcher navigation → step 7
- D37 component → widget entrypoint → done upstream (parent step 13);
  step 7 extends the widget, not the resolution
- V1 verb mount / V4 `ls` → step 6
- V3 cycle order → step 3
- V6 `--no-quit` + navigate-only boundary → step 7
- V9 auto-show on `mux up` → step 2
- Parent step-15 verify line → step 9

IDEA.md use cases replayed against the steps: (1) put up the frame →
steps 1, 2, 6; (2) take it down → steps 1, 6; (3) walk the defs →
steps 3, 6; (4) frame first, projects later → driver F6 (done) +
step 9 verifies; (5) switching from the switcher → steps 7, 8;
(6) managed entries → steps 4, 5; (7) knowing what's available/up →
step 6 (`ls`). No unowned use case.

## Next action

None — implementation complete and verified (build/vet/lint/full test
suite green, 2026-08-14). Remaining work lives in "Follow-ups": the
muxctl-plan driver defect, the `WidgetArgv` root-flag design call,
and the deferred test-coverage gaps.
