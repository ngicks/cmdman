# Issues

Known issues awaiting their own plan/commit, folded in from concluded
plans' HANDOFF files; each entry keeps its original discovery date and
names its source plan. First batch moved out of
`2026-08-15-01-project_manager_widget/HANDOFF.md` (2026-08-17); second
batch out of `2026-08-17-01-improve_widget_behavior/HANDOFF.md`
(2026-08-19).

## `compose up --mux` collides on window index in a shared session (out-of-scope discovery, 2026-08-16)

Bringing a *second* project up with `--mux` into a session that already
holds one fails: `tmux new-window -d -t <session> …: create window failed:
index 1 in use` — `-t <session>` resolves to the session's current window's
*index*, so the insert collides once the current window is not the last
(find-or-create path under `pkg/muxctl/tmux/`). Found while building the
project-manager plan's step-6 summon e2e (it forced the test's project B to
be `create`d instead of brought up). **Follow-up**: fix window creation to
append (`-a`) or pick an explicit free index; deserves its own small
plan/commit.

## Summon workdir is symlink-resolved; symlinked `work_dir:` still misses (out-of-scope discovery, 2026-08-16)

D20's summon passes `ProjectInfo.Workdir`, which is
`normalizePath`/`EvalSymlinks`-resolved (`cmdman/cli/tui_backend.go:82-96`),
while compose identity deliberately preserves symlinks
(`cmdman/compose/normalize.go:118-119`). Right whenever the stored label is
symlink-free (the normal case — labels come from `os.Getwd()` or an explicit
`--workdir`); a project whose `work_dir:` goes through a symlink would still
resolve wrong. **Follow-up**: carry a raw-workdir field through
`ProjectInfo`/`ProjectGroup` (design change; pre-existing hazard also noted
at `cmdman/cli/tui_backend_compose.go:70-76`).

## Full TUI Compose-tab Active mark is still cwd-only (out-of-scope discovery, 2026-08-16)

`cmdman/tui/state.go:371` marks the Compose tab's active project by cwd
match only — it is not among D3's enumerated consumers (switcher Active
mark, `resolveLayoutSelection`, project-manager), so the project-manager
plan's step 3 deliberately did not convert it. **Follow-up**: fold it onto
`ActiveIdentity` in a later change for full D3 consistency.

**User note (2026-08-17, undecided)**: leaning toward dropping the TUI
panel from the `tui` subcommand in favor of new widgets — which would make
this fix moot along with the panel it fixes. No decision yet; whichever
plan picks this issue up should settle that direction first.

## Broader muxctl cleanup — user-approved deferral (D7, 2026-08-18)

From `2026-08-17-01-improve_widget_behavior` (DECISION.md D7: "We'll later
revisit to muxctl clean up"). That plan's step 7 shipped the minimal fix
for the reported bug — driver `New` without `WindowID` always creates, the
by-name adoption formerly in `pkg/muxctl/tmux/tmux.go` is deleted,
`WindowName` is documented display-only, and `mux.Run` orchestrates
find-or-create by identity via `Server.ListWindows`. Everything wider is
deferred:

- Whether `Server.New`'s remaining mode switches (`WindowID` targeting,
  `ReuseCurrentWindow` takeover, create) should be split into separate,
  single-purpose interface methods (find / open / create) instead of one
  config-driven entry point.
- `currentWindowToReuse` / `ReuseCurrentWindow` semantics
  (`pkg/muxctl/reuse.go`, `pkg/muxctl/tmux/tmux.go:113-119`) — audit
  against the same "names and current-window are not identity" principle.
- `deriveIdentity`'s fallback for standalone (non-compose) specs
  (`cmdman/mux/run.go:134-139`), where the identity defaults to the
  window/session *name* — the name-as-identity assumption survives there
  in stamped form.
- A contract-documentation sweep of `pkg/muxctl` (`Config`, `Window`,
  `doc.go`) so every field states whether it is a key or display-only.
- Inherited, still-open item from
  `doc/plan/2026-08-15-01-switcher_creates_window/HANDOFF.md`: windows on
  a dedicated mux socket are invisible to autodetect-only `Land`.

**Follow-up**: a future plan dedicated to muxctl (suggested name:
`muxctl-NN-interface-cleanup`, joining the existing `muxctl-00`/`muxctl-01`
series), taking this list plus whatever step 7's implementation uncovered.

## `frame show` self-heal on desynced windows — undecided (D8, 2026-08-18)

From `2026-08-17-01-improve_widget_behavior` (DECISION.md D8, recorded by
the autonomous orchestrator). `frameTarget.show` skips its teardown when no
def is recorded, so a window carrying leftover frame-stamped panes with an
empty `frameDefOption` gets new docks stacked on the leftovers. `frame
hide` is the documented, now state-independent recovery path; whether
show's pre-hide should become unconditional (making show itself
self-healing) is undecided. **Follow-up**: decide with the user; if yes, a
small change in `cmdman/mux/frame.go` plus a test mirroring the hide
desync tests.

## Show-before-launch under a standing frame for outside-tmux callers (D9, 2026-08-18)

From `2026-08-17-01-improve_widget_behavior` (DECISION.md D9, recorded by
the autonomous orchestrator). Before the identity-keyed lookup change,
`mux up` from outside tmux could adopt a pre-existing frame-only window by
name and launch the project "under the chrome". Now the miss branch always
creates, so that path builds a fresh window beside the standing frame.
Inside tmux the current-window takeover still covers it. If the capability
should return, the find must key on the frame stamp (never the name) and
needs answers for: which frame-only window wins when several exist, and
whether adoption wants an opt-in/opt-out guard. Related: `Server.Open`'s
by-name fallback (`pkg/muxctl/tmux/tmux.go:211`) is now exercised only by
tests — a removal candidate for the same cleanup. **Follow-up**: decide
with the user; likely belongs to the deferred muxctl cleanup (previous
entry).

Also note, same change, accepted consequence (not open): `compose mux
down` then `up` now builds a fresh window — down clears the stamp, so the
next up has nothing to recognise; the restored window stays. The e2e
tests pin the behavior.

## Vendored x/ansi awaits the upstream fix release (2026-08-19)

From `2026-08-17-01-improve_widget_behavior` (D11 follow-up). The upstream
OSC parser bug — charmbracelet/x#848: `x/ansi`'s parser honors a raw C1
control byte even when it is a continuation byte inside a multi-byte UTF-8
rune, cutting an OSC 0/2 title mid-rune; its `handleTitle` also silently
drops titles containing ';' — is worked around by vendoring `x/ansi
v0.11.7` with fix PR charmbracelet/x#946 applied, at
`internal/third_party/charmbracelet-x-ansi` behind a `replace` directive.
**Follow-up**: track charmbracelet/x#946; when a release containing the
fix ships, delete the vendored copy plus the `replace` and bump the dep
(see `internal/third_party/README.md`). Also pending: a confirmation
comment for PR #946, drafted at
`doc/plan/2026-08-17-01-improve_widget_behavior/pr946-comment.md`, awaits
a manual post — posting from this environment failed (the gh token cannot
comment on foreign repos). The latch-side sanitize (D11) stays regardless,
as the guard against any future parser misbehavior.
