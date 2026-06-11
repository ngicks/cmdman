# mux-01 — Fix fragile dashboard window detection; `mux up` / `mux down` / `mux ls`

Status: approved, not yet implemented.
Follows: mux-00 (initial mux implementation).

## Problem

`cmdman compose mux` (and standalone `mux`) only behaves correctly when invoked
from a shell pane that was attached when the dashboard was built. Invocations
from a newly created pane, from the tmux command prompt (`prefix :` →
`run-shell`), or from outside the session fail with messages like:

    No cmdman dashboard window found to detach in session "win1"

or silently create a second dashboard window.

### Root cause

The dashboard window has **no durable identity**. The only ownership signals
today are:

- the per-pane `@cmdman_marker` user option — but it stores the applied
  *layout index* for cycling, and ownership checks require **every** pane to
  carry it (`windowIsMarked`, `pkg/muxctl/tmux/reuse.go`). Any pane the user
  opens by hand breaks the invariant; panes are also churned by every layout
  apply.
- the window *name* `cmdman-<project>` — but when `Run` takes over the
  caller's current window (the common in-tmux path, `pkg/muxctl/tmux/tmux.go`
  `New` → `currentWindowToReuse`), the window is **never renamed**. The name
  flows one-way, CLI → tmux lookup; nothing in tmux records which window
  belongs to which project.

Teardown (`mux.Detach` → `tmux.OpenExisting`) therefore finds the window only
via (a) "current window with all panes marked" — broken from a fresh pane or
any no-client context — or (b) by-name lookup in the client-resolved session —
never matches a takeover window. Cycling has the mirror problem: from a fresh
pane, `currentWindowToReuse` rejects the dashboard window (not all-marked,
name mismatch, >1 pane) and find-or-create spawns a duplicate window.

Additionally, session resolution depends on `$TMUX` in the calling process
env (`resolveSessionName`, `pkg/cmdman/mux/run.go`), which is absent in
`run-shell` / command-prompt contexts (only `TMUX_PANE` is set there).

## Design

### Core: window-level identity stamp

Stamp a **window-level** tmux user option at build time:

    @cmdman_window = <identity>

- Window-level (`set-option -w`), not per-pane: survives pane churn, splits,
  manual panes, and window renames. Exactly one stamp per window.
- Written by `tmux.New` on whichever window the dashboard actually lands in —
  both the takeover path and the find-or-create path.
- Cleared by `Session.Detach`, so a restored window is no longer owned.
- Pane titles are explicitly NOT used as identity: titles are presentation
  surface (resettable by programs/shells/users). Same lesson as mux-00's
  marker-vs-title split in `stat.go`.

### Identity schema

- **compose mux**: `<workdir-hash>-<escaped-project>` — the existing command
  naming prefix, reusing `workdirHash` and `escapeName` from
  `pkg/cmdman/compose/hash.go` (`GenerateName` minus the command segment).
  Disambiguates same-named projects in different directories.
- **standalone mux**: the resolved owned-window name (today: `WindowName`,
  defaulting to the session name).
- The identity is an **opaque string** below `pkg/cmdman/mux`: drivers store
  and return it, never interpret it.

### Discovery

New driver capability: enumerate owned windows **server-wide**, with no
dependence on `$TMUX`, the current client, or the current window:

    tmux list-windows -a -F '#{window_id}\t#{session_name}\t#{window_name}\t#{@cmdman_window}\t...'

filtered by exact identity match. An explicit `--session` narrows the scan
(`list-windows -t <session>` instead of `-a`).

### Teardown semantics (`down`)

Tearing down a *specific* dashboard when several match is ambiguous in
principle, but multiple dashboards for one project is not an envisioned
state. So: `down` restores **every** matching window, printing one line per
window restored. Zero matches prints a friendly note and exits 0 (today's
behavior). `--session` remains as a narrowing filter. Teardown stays
non-destructive: viewers detach gracefully, supervised commands keep running.

### CLI restructure (breaking; no `--detach` alias — app never deployed)

| old | new |
|---|---|
| `cmdman mux [path]` | `cmdman mux up [path]` (root `mux` = alias of `up`) |
| `cmdman mux --detach [path]` | `cmdman mux down` |
| — | `cmdman mux ls` |
| `cmdman compose mux [layout]` | `cmdman compose mux up [layout]` (root alias) |
| `cmdman compose mux --detach` | `cmdman compose mux down` |
| — | `cmdman compose mux ls` (filtered to the project) |

- `up`/`down` chosen over `attach` to avoid colliding with `cmdman attach`
  (command stdio) and to reuse the compose up/down vocabulary; `up` also
  creates, which "attach" would misname.
- Cobra: parent command keeps `RunE`; an unmatched first arg falls through to
  the parent, so `cmdman mux dashboard.yaml` / `compose mux 2` keep working.
  Documented edge: a layout literally named `up`/`down`/`ls` is shadowed at
  the root alias and needs the explicit `mux up <name>` form.
- `down` needs no layout/leaf resolution — compose: only the project identity;
  standalone: only driver/driver_opt from the spec (skip spec read on the
  stdin default, as today).

### Driver portability (zellij / WezTerm — vision, not planned)

The driver contract is two **semantic** capabilities: *stamp an identity on
the window-equivalent at build* and *enumerate windows by identity*. Where
the stamp lives is private to each driver:

- tmux: native window user option (this plan).
- WezTerm: `wezterm cli list` gives solid enumeration; per-pane user vars are
  emitted via escape sequence from inside panes (our viewers run there), or
  tab title / sidecar.
- zellij: no arbitrary metadata today; tab/pane titles are as fragile as
  tmux titles (we overwrite titles ourselves). Long-term shape is a zellij
  plugin storing per-pane/per-window data. Vision only; no plan.
- Universal fallback (deliberately **deferred**): a registry file under
  cmdman's runtime dir mapping identity → driver locator. Rejected for now
  because a sidecar cannot be the source of truth, only a cache: every read
  must re-validate liveness *and ownership* against the live multiplexer
  (manual window closes, server restarts reusing ids), which requires an
  in-driver mark anyway — degenerating into "native stamp + cache + locking
  + crash cleanup". For tmux the native option is strictly better. Decide
  per-driver when a driver that truly lacks storage materializes.

Record this contract expectation in `pkg/muxctl/doc.go` so the next driver
author inherits the reasoning.

## Changes by package

### 1. `pkg/muxctl/tmux`

- `tmux.go` (`New`): new `Config` field for the identity (e.g.
  `OwnedIdentity string`); after resolving `wid` (both paths), stamp
  `set-option -w -t <wid> @cmdman_window <identity>`, next to the existing
  `pane-border-status` set. Constant `ownerOption = "@cmdman_window"`.
- `detach.go` (`Session.Detach`): unset `@cmdman_window` alongside
  `pane-border-status`.
- `reuse.go`: ownership recognition (`currentWindowToReuse` /
  `currentWindowIfMarked`) switches from "every pane marked" to "window
  carries `@cmdman_window`" — fixes cycling from a fresh pane in the
  dashboard window. `windowIsMarked` remains only where the layout marker
  itself is the question.
- New `ListOwnedWindows(ctx, cfg, identity)` (and an unfiltered variant for
  `ls`): server-wide `list-windows -a`, returning per window: session name,
  window id, window name, identity, layout marker. Optional session filter.
- `OpenExisting`: the by-name find path loses its only caller; simplify to
  the `WindowID` (+ marked-current-window, if still needed) paths or fold
  into the new flow.

### 2. `pkg/cmdman/mux`

- `detach.go` → `down.go`: `Down(ctx, DownOptions)` — resolve driver,
  enumerate by identity (server-wide; session filter when `--session` set),
  `Session.Detach` each match, print each restored window, friendly no-op on
  zero. Drops `resolveSessionName` / `$TMUX` dependence from teardown
  entirely.
- New `List(ctx, ListOptions)` returning owned-window rows for `ls`.
- `run.go` (`Run`): pass the identity through to `tmux.Config`; reuse logic
  otherwise unchanged.

### 3. `pkg/cmdman/compose`

- Expose the project identity (`<wdhash>-<escaped-project>`) from the
  selection/load layer for the mux subcommands (reuse `workdirHash` /
  `escapeName`; likely a small exported helper next to `GenerateName`).

### 4. `cmd/cmdman/commands` (use the `go-edit-cobra` skill)

- `mux.go`: restructure into `mux` root (alias of `up`) + `up`/`down`/`ls`
  subcommands; delete `--detach`; help text: "tear down the dashboard
  (supervised commands keep running)".
- `compose_mux.go`: same mirror under `compose mux`; `ls` filters to the
  resolved project; layout-name completion moves to `up` (and the root
  alias).

### 5. `pkg/cmdman/cli`

- Table rendering for `ls` (session, window, project/identity, layout) —
  presentation lives here per the layering rule, not under `./cmd`.
- `tui_backend.go` (`CycleMux`): re-derives the window name and calls
  `mux.Run` itself; it must pass the same project identity so TUI-built
  dashboards are stamped identically. Share one identity/window-name helper
  between `cmd/cmdman/commands` and this path instead of duplicating the
  derivation.

### 6. Docs

Primary — rewrite for the subcommand structure:

- `doc/man/cmdman-mux.1.md`, `doc/man/cmdman-compose-mux.1.md`: `up`/`down`/
  `ls`, root-as-`up` alias, `--detach` removal, server-wide discovery
  ("works from any pane, run-shell, or outside tmux"), the
  shadowed-layout-name edge.

Secondary — sweep every `cmdman mux` / `compose mux` invocation mention:

- `doc/man/cmdman-mux.5.md`: wording about how `cmdman mux` /
  `cmdman compose mux` apply layouts and resolve leaves.
- `doc/man/cmdman.1.md`, `doc/man/cmdman-compose.1.md`,
  `doc/man/cmdman-compose.5.md`, `doc/man/cmdman-tui.1.md`: cross-reference
  links and one-line descriptions. Verify completeness with
  `grep -rn 'mux\|--detach' doc/man/` at the end of the docs pass.

Code-level:

- `pkg/muxctl/doc.go`: driver contract note (stamp + enumerate; storage is
  driver-private).

### 7. Tests

- `pkg/muxctl/tmux` (real-tmux tests, per-test socket as today):
  - `New` stamps the option on both takeover and create paths.
  - `Detach` clears it.
  - `ListOwnedWindows` finds a renamed / takeover window across sessions.
  - Reuse/ownership still recognized with an extra unmarked pane present.
- `e2e/cmdman/mux_test.go`:
  - Update existing detach tests to `mux down` / `compose mux down`.
  - New capability: build dashboard in session A, run `compose mux down`
    with **no** `--session` from outside tmux → window in A found and
    restored.
  - `mux ls` lists the dashboard (session, identity).
  - Root-alias behavior: `cmdman mux <path>` ≡ `cmdman mux up <path>`.

## Known limitation (documented, not solved)

Standalone `mux down` with no `--session` and a *default* window name derives
the identity from context (window name defaults to session name), so a
dashboard built with default naming in another session resolves a different
identity. Compose is unaffected — its identity comes from workdir+project.
Possible later fix: constant default identity for standalone mux.

## Execution & status tracking

Implementation is dispatched as workstream tasks to opus subagents. Live
status lives in `STATUS.md` next to this plan
(`doc/plan/mux-01-fix-fragile-window-detection/STATUS.md`): one section per
workstream with state (`todo` / `in-progress` / `done` / `blocked`), a short
summary of what was changed, and the exact verification commands run (tests,
lint). Agents update only their own workstream section. Read STATUS.md first
when reviewing or resuming this plan.

Workstreams, in dependency order:

1. **driver** — `pkg/muxctl/tmux` stamp / `ListOwnedWindows` / `Detach`
   clear / reuse switch + `muxctl/doc.go` contract note + driver tests
   (real-tmux, per-test socket).
2. **mux-layer** — `pkg/cmdman/mux` `Down` / `List`; compose identity helper
   in `pkg/cmdman/compose`. Depends on 1.
3. **cli** — `cmd/cmdman/commands` subcommand restructure (`go-edit-cobra`
   skill); `pkg/cmdman/cli` `ls` rendering + `CycleMux` identity. Depends
   on 2.
4. **docs** — man-page rewrite + sweep. Depends on 3 for final flag/command
   wording.
5. **e2e** — `e2e/cmdman/mux_test.go` updates + new capability tests.
   Depends on 3.

## Decision log

- **2026-06-12 — `ls` must honor spec driver options (user-approved: "fix both").**
  Found during ws5 e2e: `runComposeMuxLs` dropped `spec.Driver`/`spec.DriverOpt`,
  so `compose mux ls` could not see dashboards on a non-default tmux socket
  (`compose mux down` passed them correctly). Standalone `mux ls` had the same
  blindness structurally: it accepted no spec path, while `mux down [path]`
  reads one purely for driver/driver_opt extraction. Decision: fix both —
  `compose mux ls` passes the spec's driver options to `mux.List`, and
  standalone `mux ls` gains an optional `[path]` argument with the exact
  `mux down [path]` semantics (read only for driver/driver_opt; stdin default
  skips the read). Docs and e2e updated to match.

## Out of scope (candidate follow-up)

The *up/cycle* path from `run-shell` / command prompt still misresolves the
session because `$TMUX` is absent there (only `TMUX_PANE` is set):
`resolveSessionName` falls back to `"cmdman"`. One-line candidate fix: treat
`TMUX_PANE` as "inside tmux" in `resolveDriver` / `resolveSessionName`
(`pkg/cmdman/mux/run.go`) — the `tmux display-message` subprocess inherits
`TMUX_PANE` and resolves the correct session.
