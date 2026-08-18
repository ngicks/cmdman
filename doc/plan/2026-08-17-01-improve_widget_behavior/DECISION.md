# DECISION — improve TUI widget behavior

## D1 — Idea gate skipped by user direction

**Decision**: The ngplan idea phase was skipped; IDEA.md is a brief derived
statement of the user's directives, not an explored idea document.
**Rationale**: The user explicitly instructed "No idea phase; skip it" and
supplied the target behaviors directly (2026-08-17).
**Rejected**: Running the idea gate anyway (would re-litigate decisions the
user already made).

## D2 — Config-known projects: visible on the empty filter, disabled by default

**Decision** (user, 2026-08-17): Config-only projects (discovered via
`compose.ListNamedProjects()`) appear in the launcher's default (empty
filter) view, ordered after history rows (history by recency, config-only
rows name-sorted), and start **disabled** — `space` enables them before `s`
can start them. Store-derived projects and the cwd project stay filter-only.
**Rationale**: `enabled` gates what `s` mass-starts; a never-run project
must not join a bulk start the user aimed at their active projects.
Widening beyond "under config" is scope the user did not ask for.
**Rejected**: enabled-by-default; admitting store/cwd sources on the empty
filter; overloading `FromHistory=true` for config rows (lies about
provenance and breaks `ctrl+d` forget semantics).

## D3 — Down actions on `d` / `D`, confirm on `D`

**Decision** (user, 2026-08-17): In all three widgets, `d` = mux down
(dashboard windows only; immediate; status-line result) and `D` = compose
down (stop + remove commands) behind a status-line `y/n` confirm — `D`
shows "compose down <project>? y/n", `y` proceeds, anything else cancels.
`d` on a project whose spec has no `mux:` section shows an error in the
status line (the action stays visible). Launcher's `ctrl+d` (forget history
row) is unchanged.
**Rejected**: `x`/`X` keys; no-confirm compose down; modal confirm overlay;
hiding `d` when no `mux:` section exists.

## D4 — Window uniqueness via identity-keyed lookup; unnamed projects get a synthesized identity

**Decision** (user, 2026-08-17): The tmux driver's find-or-create keys on
the project identity stamp when `Config.OwnedIdentity` is set (matching
`mux.Land`'s find-by-identity), falling back to name-keyed behavior for
identity-less callers. Unnamed projects get a synthesized identity from the
workdir hash alone so identity is never empty — absorbing the
`2026-08-15-01-switcher_creates_window` HANDOFF item "identity-less
projects dead-end". This deliberately changes reuse semantics for **every**
`mux up` caller (CLI included), fixing the same-name-different-workdir
collision globally.
**Rejected**: identity lookup without the unnamed-project fix (collision
remains reachable); workdir-hash-suffixed window names (ugly names,
diverges from `Land`'s identity keying).

**Amendment** (user, 2026-08-18) — supersedes the mechanism above, keeps
the goal: the fix is not an identity-first lookup *inside* the driver's
find-or-create, because the layering itself was wrong — "finding the
pre-existing window is the cmdman-side job", and the muxctl contract never
promised unique window names (names are mutable via user rename, OSC 2
titles from in-pane programs, tmux automatic-rename — so name-lookup has
false positives *and* false negatives). Amended mechanism:
- `muxctl.Server.New` without `WindowID` always **creates**; the by-name
  adoption in `findOrCreateWindow` is deleted; `WindowName` is documented
  display-only, never a key.
- `mux.Run` orchestrates find-or-create cmdman-side via
  `Server.ListWindows` filtered by identity → reuse the found `WindowID`
  or create — the pattern `mux.Land`/`mux.Down` already use.
The synthesized identity for unnamed projects and the every-caller blast
radius from the original entry stand unchanged.
**Rejected by the amendment**: driver-side identity-first find-or-create
(wrong layer; keeps a name-matching code path alive in the driver).

## D5 — Harden frame hide in this plan (not HANDOFF)

**Decision** (user, 2026-08-17): Beyond removing `z`, make `hide`
crash-safe: `frameTarget.hide` no longer no-ops on an empty
`frameDefOption` state key — frame-stamped panes are removed by their stamp
regardless of recorded state, so panes and state can never desync no matter
where a caller dies. With hide state-independent, the driver may clear
state before killing panes.
**Rationale**: The kill-before-clear ordering in `Session.HideFrame`
(`pkg/muxctl/tmux/frame.go:298-307`) plus the empty-state no-op
(`cmdman/mux/frame.go:241-243`) is the mechanism behind the reported
`z` → `frame show` no-op bug; the user chose fixing it here over deferring.
**Rejected**: HANDOFF-note-only deferral; naive reorder (clear state first)
without the state-independent hide — a crash after the clear would leave
live panes the CLI could no longer remove.

## D6 — Widget bring-up keeps the current layout (`KeepLayout` mode)

**Decision** (user, 2026-08-17): Add a `KeepLayout` mode threaded
`mux.RunOptions` → `compose.MuxUpOption` → the launcher's `bringUp`: an
existing window re-applies the layout its marker records (a manually cycled
layout survives a re-up); a fresh window gets layout 0. An explicit
`Layout` wins over `KeepLayout`. CLI cycle semantics (empty `--layout`
cycles) are unchanged.
**Rationale**: Grounded in the 2026-08-17 user-reported bug — starting a
second project flipped the (shared) dashboard window to the second layout,
because empty `Layout` means "cycle" (`cmdman/mux/run.go:170-180`).
Cycling must be an explicit gesture, never a bring-up side effect.
**Clarification** (re-confirmed with the user 2026-08-18): the cross-project
half of the bug — B reading A's window marker — is fixed by D4 alone
(identity-keyed windows mean the marker read is always the project's own;
a fresh window has no marker and gets layout 0, so no state clearing is
needed). D6 covers only the residual same-project case: a re-up of an
already-running project applies `marker` (the layout the window already
shows) instead of `marker+1`. "Re-applies its own marker" = keep the shown
layout, nothing more.
**Rejected**: widgets pinning `Layout: "0"` (snaps back user-chosen
layouts); making non-cycling the global default with a new CLI `--cycle`
flag (changes documented CLI behavior).

## D7 — Broader muxctl cleanup deferred to a later plan

**Decision** (user, 2026-08-18): "We'll later revisit to muxctl clean up."
This plan ships only step 7's targeted fix (D4 as amended: `New` always
creates, name is display-only, `mux.Run` finds by identity); the wider
`pkg/muxctl` interface/layering cleanup is deferred to a future dedicated
plan. The deferred items are enumerated in HANDOFF.md entry 1.
**Rationale**: The five widget behaviors are this plan's scope; a muxctl
redesign (interface split, reuse semantics, name-as-identity fallbacks,
contract docs, the inherited mux-socket item) is its own effort.
**Rejected**: folding the full cleanup into this plan (scope creep);
leaving the follow-up unrecorded.

## D8 — Show does not self-heal desynced windows; recovery stays `frame hide` [automatic]

**Decision** (orchestrator, autonomous run 2026-08-18): The hide-hardening
step surfaced that `frameTarget.show` (`cmdman/mux/frame.go:212-215`) runs
its teardown only when a def is recorded, so a window with an empty def but
leftover frame-stamped panes gets docks stacked on the leftovers. This plan
does not make show's pre-hide unconditional; the documented recovery is
`cmdman mux frame hide` (now state-independent). Deferred to HANDOFF
entry 2.
**Rationale**: The user-approved hardening covered hide only; changing
show's teardown semantics is new scope and touches the show/cycle gestures'
behavior on healthy windows too.
**Rejected**: silently widening scope mid-run.

## D9 — No frame-only window adoption in Run's miss branch [automatic]

**Decision** (orchestrator, autonomous run 2026-08-18): With by-name
adoption gone from the driver, a `mux up` from outside tmux (or with an
explicit `-s` session) no longer lands a project inside a pre-existing
standing frame window — the miss branch plainly creates, per the approved
design. The current-window takeover inside tmux is unchanged and covered
by tests. Frame-only adoption was NOT re-added cmdman-side: it contradicts
the approved "miss: plain create" and raises unresolved questions (which
frame-only window wins; whether it needs a KeepCurrentWindow-style guard).
Deferred to HANDOFF entry 3.
**Rationale**: the approved mechanism is explicit; re-adding adoption on a
different key mid-run would be new design without the user.
**Rejected**: silently restoring the capability via frame-stamp-keyed
adoption.

## D10 — Floating config projects: offered at the selected/typed dir, not pinned to cwd

**Decision** (user, 2026-08-18, post-implementation): A named config project
whose spec declares no `work_dir:` (e.g. a dev-environment project meant to
be started wherever the user is working) is no longer pinned to the
launcher process's cwd. Instead it is offered at whatever directory the
user selects or types: the projects pane of the selected location includes
it, launching with that location's directory as the work dir. Projects
WITH `work_dir:` keep their single pinned row. Floating rows keep the
config provenance (disabled by default, `space` enables).
**Rationale**: The user's real use case is starting a config-known project
(devenv: nvim/shell/claude) in an arbitrary, possibly never-launched
directory. Pinning to the launcher's cwd made the project invisible at any
other selected dir — reported as "does not show project under
~/.config/cmdman/compose/ when selecting dir not listed in history".
**Rejected**: keeping cwd pinning with typed-dir merge only; offering
floating projects at every location row while also keeping a cwd-pinned
row (duplication).
