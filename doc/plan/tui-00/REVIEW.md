# tui-00 plan review

Review of `PLAN.md`, `TUI_CORE.md`, `TUI_RUNTIME.md`, `TUI_MUX.md` against the current
codebase, with author decisions folded in.

Each item below records the gap, the decision, and the concrete change the plan docs
still need. Items marked **OPEN** have no decision yet.

---

## Tier 1 — foundational

### 1. TUI library — DECIDED: bubbletea

The plan never names a rendering library. Decision: **use `github.com/charmbracelet/bubbletea`**
(`lipgloss` is already a direct dep; bubbletea is new).

Caveat to document, not just adopt: `pkg/cmdman/cli/progress_tty.go:19` deliberately avoids a
TUI framework, with the comment that "a full framework (bubbletea) queries the terminal at
process startup for the whole binary, which corrupts the PTY of sibling subcommands such as
`compose attach`." The `tui` subcommand is its own process and does not spawn sibling
subcommands that share the PTY, so this concern does not apply here — but the plan should say
that explicitly so the decision is not silently reversed later.

Doc change: add a short "Library & process model" note to `TUI_CORE.md` stating bubbletea is
the renderer and why the `progress_tty.go` PTY concern does not apply to a standalone `tui`
process.

### 2. Scope / CLI-parity claim — DECIDED: narrow the goal

Standalone (non-compose) commands are **out of scope**. Support for bare `cmdman run` /
`cmdman create` flows is **dropped**, and editing `compose.yml` is **not supported**. With that
scope, the compose-grouped command tree is correct as drawn.

But `PLAN.md` currently overclaims. The goal says the first version "should cover the same core
command lifecycle operations that are available from the CLI" — that reads as full CLI parity.

Doc change (`PLAN.md` "Goal"): reword to scope the TUI to lifecycle operations
(list/start/stop/restart/attach/remove) **on compose-managed commands only**, and state
explicitly that it does **not** create commands (`run`/`create`) or edit compose files. Drop
any wording implying all CLI subcommands are covered.

### 3. Attach terminal handoff — DECIDED: elaborate, or drop for the first cut

`cli.Attach` (`pkg/cmdman/cli/attach.go`) requires real `*os.File` for `Stdin`/`Stdout`
(raw-mode toggling, SIGWINCH ioctl). With bubbletea owning the terminal, "suspend TUI → call
`cli.Attach` → redraw" needs concrete release/reclaim plumbing. `TUI_RUNTIME.md` describes the
flow but not the mechanics.

Pick one for v1:

- **Keep it:** specify the bubbletea handoff explicitly — `tea.ExecProcess` (or
  `ReleaseTerminal`/`RestoreTerminal`), passing the real `os.Stdin`/`os.Stdout` fds to
  `cli.Attach`, restoring raw mode and re-issuing a resize on return, and which goroutine owns
  the program while attach runs. Confirm `Service.OpenAttachSession` + `cli.Attach` (non-sticky)
  is the path, per the existing note.
- **Drop it:** remove the attach sections from `TUI_CORE.md`/`TUI_RUNTIME.md`, drop the `a`
  key and the attach confirmation popup, and list attach as future work.

This is where most attach bugs will originate, so the doc should not leave it at flow level.

### 4. Popup / zellij / pane-count are net-new primitives — DECIDED: zellij is a TODO stub, v1 tmux-only

- **zellij:** there is no zellij driver. `pkg/cmdman/mux/run.go` detects `$ZELLIJ` but errors
  `"driver \"zellij\" is not implemented yet (v1 ships tmux only)"`. Decision: zellij is an
  explicit **TODO stub** (author has not used it recently). Doc change: mark `--popup=zellij`
  and zellij floating-pane support as a TODO stub in `TUI_CORE.md`/`TUI_MUX.md`, and scope v1
  popup to **tmux only**, consistent with mux.
- **popup:** no popup / `display-popup` / floating-pane code exists anywhere in `pkg/mux` or
  `pkg/muxctl`. `--popup` is real new work, not a flag over existing behavior — the plan should
  acknowledge this rather than describing it as thin.
- **pane-count detection** ("reject if the window already has multiple panes") has no
  production API. The only `#{window_panes}` logic lives in the internal dev tool
  (`pkg/muxctl/internal/cmd/muxctltester/main.go:353`), not on `muxctl.Session`. Note this as
  new work (and see item 8 for how it interacts with popup).

---

## Tier 2 — correctness / consistency

### 5. Status vocabulary mismatch — DECIDED: agreed

Mockups show `running` / `stopped` / `exited(0)`. The real persisted states
(`pkg/cmdman/model/state.go`) are `created`, `starting`, `started`, `exited`, `failed`. There is
no persisted `running` or `stopped` state (`stopped` is a transient event; a stopped process
ends `exited`/`failed`).

Doc change: add an explicit state→display-label mapping (e.g. `started`→`running`) and use the
real state set in the mockups, so the TUI does not render statuses that don't exist.

### 6. Mux layout state ownership — DECIDED: the TUI is a thin wrapper

Original intent: the TUI is a wrapper over the existing layout-selection + layout-cycling
command, not an owner of layout state. The current docs contradict this — `PLAN.md` lists
"selected mux layout per compose project" in view/project state, and `TUI_MUX.md` describes a
selector picking a specific layout, while `mux.Run` (`pkg/cmdman/mux/run.go`) already cycles
layouts from a persisted tmux window marker (`(marker+1) % len(layouts)`).

Doc change: rewrite `TUI_MUX.md` (and trim `PLAN.md` State Model) so the TUI does **not** track
its own "selected layout per project." `c`/cycle and the `l` selector invoke the existing
mux command and let mux's window-marker state be the single source of truth. If the selector
must show a *specific* layout (not just "next"), call out that `mux.Run` currently only
auto-cycles and a "show layout N" entry point would be new work — otherwise drop the
specific-layout selector and keep cycle-only for v1.

### 7. Events / refresh framing — DECIDED: simplify

`Service.Events` (`pkg/cmdman/cmdman_events.go`) is a **local JSONL file tail** over
`<data-dir>/events.log`, not a network stream, and there is **no central daemon** (each command
has its own monitor process/socket). `TUI_RUNTIME.md`'s "event stream unavailable / reconnect /
polling fallback" language describes a network-stream model that doesn't apply.

Doc change: replace that framing with the intended statement — "the TUI backend subscribes to
`cmdman.Service.Events` and reflects state changes to the screen." Drop reconnect/stream-failure
wording for lifecycle events. (If polling is still wanted as a backstop for things `Events`
doesn't cover, scope it to that explicitly rather than framing it as stream reconnection.)

### 8. TUI-in-tmux vs. applying a layout to the current window — DECIDED: mux uses --popup; warn otherwise

Applying a mux layout rearranges the *current window*; in non-popup mode the TUI occupies that
window, so a layout would clobber the TUI. Decision: **mux layout changes go through the
`--popup` path.** If `--popup` is not specified, **warn the user before invoking mux**, because
the layout will rearrange/replace the current window (the TUI).

Doc change (`TUI_MUX.md` "Mux Display"): state that mux display runs via popup; if `--popup`
is not set, show a confirmation/warning before calling mux. Reconcile this with the existing
"reject if the window already has multiple panes" guard (pane-count detection from item 4) —
clarify whether the warning replaces or complements that rejection.

---

## Tier 3 — smaller follow-ups (no decision yet) — OPEN

- **Remove of a running command** needs `--force` (SIGKILL); the remove popup doesn't address
  running commands.
- **Stop/restart** can't specify `--signal`/`--timeout` from the TUI (defaults only). State as
  a deliberate v1 limitation.
- **No multi-select / bulk actions**, though CLI `stop`/`restart`/`rm` accept multiple targets.
  (`Service.Start` is single-target; the others take a `Targets` slice.)
- **`none` log driver** → preview is always empty; the "No output yet" empty state should
  distinguish "no output" from "no log storage configured."
- **`?` help screen** is in the keymap but never specified.
- **Service concurrency**: events subscription + log streaming + async actions run together;
  state whether one `Service` is safe under concurrent use.
- **"Active project"** is not first-class — compute by comparing `ProjectSummary.WorkDir`
  (`pkg/cmdman/compose/service_list.go`) to `os.Getwd()`.
- **Footer `v0.1.0`** is a placeholder; actual is `v0.0.7-devel` (`pkg/cmdman/version.go`) via
  `versioninfo.ReadVersionInfo`.

---

## Round 2 — review of the revised docs (decisions folded in)

The first revision addressed Tier 1/2 well. Verified one new factual claim: the
"Service Concurrency" assertion is accurate — `Service` guards `store`/`evtLog` with
`sync.Mutex` (`pkg/cmdman/cmdman.go:25-34`) and `store.Store` wraps a WAL-mode
`*sql.DB` on `modernc.org/sqlite` (`pkg/cmdman/store/store.go:27-29`), safe for
concurrent use. Remaining items below, with decisions.

### A. Preview source — DECIDED: it's the logs API, read concurrently with a buffer

The preview is fed by `Service.Logs`, **not** the `Service.Events` subscription.
Lifecycle events only signal *when* to (re)load; they carry no output. Doc change to
`TUI_RUNTIME.md`:

- name `Service.Logs` as the preview source (`Tail:N` for the snapshot; `Follow` for
  the live tail of the selected command)
- read logs in a goroutine and deliver lines back as a Bubble Tea message — never
  block the update loop / navigation while logs load
- cache at least ~2× the current preview viewport height in lines, so a resize or
  small scroll survives without an immediate re-read (size `Tail:N` accordingly)
- keep only one live `Follow` at a time (the selected command); cancel it on
  selection change so per-monitor gRPC `Subscribe` streams don't leak
- stop describing preview updates as arriving via "output/log events"

### B. Attach handoff primitive — DECIDED (confirmed via godoc)

Replace `tea.ExecProcess` with `tea.Exec` + a custom `tea.ExecCommand` (or
`Program.ReleaseTerminal`/`RestoreTerminal`). `tea.ExecProcess` takes `*exec.Cmd` and
can only run an external process, which conflicts with the constraint to call
in-process `cli.Attach` directly. Fix both mentions in `TUI_RUNTIME.md` (flow diagram
and Terminal Handoff section).

### C. Popup child process context — DECIDED: forward cwd + dirs

The launcher must pass its working directory and the data/runtime/config locations to
`cmdman tui __child`, otherwise "active project" (`WorkDir` vs `os.Getwd()`) and the
store target break in popup mode. cwd propagation needs no scripting on any backend:

- tmux: `display-popup -d <cwd>`
- zellij: inherits cwd already
- wezterm (future, alongside zellij): `wezterm cli spawn --cwd <cwd> -- …`

Also forward `--data-dir`/`--runtime-dir`/`$CMDMAN_CONF` to the child (the monitor
spawn path already does this). Non-popup direct mode is unaffected (no child process).

### D. Stale footer — DECIDED: fix

The Compose-tab footer still advertises `l layouts` while the body demotes `l` to
future work. Update the footer hint (PLAN.md and `TUI_CORE.md:185`) to match.

### E. Force-remove popup wording — DECIDED: fix

The force popup uses `<force remove>`/`<cancel>`, but shared "Popup behavior" says
selection moves between `<yes>` and `<cancel>` (`TUI_CORE.md:349`). Reconcile so the
behavior text covers the force popup's button labels.

### F. Lifecycle keys + quit — DECIDED: `s` start, `S` stop, `q` quit

`q`=start was a quit-habit footgun. New mapping:

- **`s` starts, `S` stops** (frees up `q` and `e`)
- **`q` quits the TUI** — this is now the documented exit key
- `Ctrl+C` / `Ctrl+D` still tear down the display, but are **demoted to undocumented
  behavior**: drop "Ctrl+c or Ctrl+d to exit" from both tab footers and advertise
  `q quit` instead

Update the keymap and both tab footers in PLAN.md and `TUI_CORE.md`. Note the
interaction with attach: `Ctrl+C` during an active attach is still forwarded to the
remote command (per `TUI_RUNTIME.md`), so the "undocumented" demotion applies only to
the normal TUI screen, not the attach handoff.

### G. Preview mockup addressing — DECIDED: fix

The mockup header `$ cmdman logs local-dev/watcher` implies a `project/command` address
form that doesn't exist. Use an id/`GeneratedName` form or the `cmdman compose logs`
path so the mockup doesn't imply a non-existent CLI surface (`TUI_CORE.md:146`).

### H. `--popup` flag mechanism — RESOLVED (no concern)

Use `pflag.BoolFunc`, as already done for `--log`/`--log-level` in
`internal/loggerfactory/loggerfactory.go:44,62`: bare `--popup` → fn called with
`"true"` (infer driver); `--popup=tmux` / `--popup=zellij` → fn called with the value.
This is exactly the "bool-style optional-value flag" the docs describe; no
`NoOptDefVal` hand-wiring is needed. My earlier "no precedent" note was wrong.

### Blocking vs polish

A, B, C are worth blocking on. D, E, F, G are cleanup. H needs no change.

---

## Round 3 — review of the Round 2 application

Verified Round 2 landed correctly in PLAN.md, TUI_CORE.md, TUI_RUNTIME.md (A–H + the
`q`-quit / Ctrl-C-demotion decision). The help-overlay `q` ambiguity resolved cleanly
(`TUI_CORE.md:303` "`q` quits the TUI, even while help is open"). TUI_MUX.md is
consistent with the keymap change. One new substantive gap, two minor.

### I. List data sources are unspecified — and the global event stream must be filtered (BLOCKING)

Round 2 named `Service.Logs` as the preview source. The parallel question for the two
**list** views was never answered: where does the data come from, and how is the global
event stream reconciled with the compose-only scope? Right now PLAN's Refresh Model and
TUI_RUNTIME only say "subscribe to `Service.Events` … update command list state," but
events are deltas with no baseline, and the list-building primitives are never named.

Concrete data flow the docs should state:

- **Commands tab tree** = project enumeration + per-project command rows:
  - enumerate projects from the **filesystem** (`compose.ListNamedProjects()`) so
    never-run projects still appear (the mockup's `tools  0 commands`), unioned with
    store-known projects
  - load each project's command rows via `Service.List(ListRequest{AllStates:true,
    Labels:{LabelWorkdir, LabelProject}})` (the `compose ps` path)
  - seed this at startup and on refresh; lifecycle events only mutate the already-loaded
    set
- **Compose tab list** = `compose.Service.ListProjects()` for counts
  (Commands/Running/Exited/Failed), **merged** with `compose.ListNamedProjects()` so
  filesystem-only/never-run projects show with `0 commands`. Note `ProjectSummary` does
  **not** carry mux presence — the `mux` badge requires parsing each project's compose
  file (`Spec.Mux != nil`), which is extra work beyond `ListProjects`.
- **Event filtering for scope:** `events.log` is global and includes standalone
  `cmdman run` commands that are out of v1 scope. The subscription must drop non-compose
  events. Compose membership is by label and new IDs appear at runtime, so a static
  `IDFilter` can't express "all compose commands" — a `created` event for an unknown ID
  needs a follow-up `Inspect`/`List` to learn its labels/project before the row is
  placed or discarded.

This is the same class of omission Round 2 fixed for preview, and it directly affects
scope correctness (the Round 1 #2 decision). Worth naming the sources explicitly in
PLAN "Refresh Model" / TUI_RUNTIME, the way `Service.Logs` is now named for preview.

### J. Filter focus vs single-key bindings (minor, but sharpened by `q`=quit)

No doc states that single-key bindings (`s`/`S`/`r`/`a`/`x`/`c`/`q`/`?`) are inert while
the filter input is focused. This matters now that `q` quits: a user typing `q` (or
`s`) into the filter must not quit/start. Add a one-liner: while the filter input has
focus, character keys edit the filter; `esc` leaves filter focus first; lifecycle/quit
bindings apply only outside filter focus.

### K. Attach popup default selection unspecified (minor)

"Popup behavior" sets the default to `<cancel>` for remove but says nothing about the
attach popup. Decide whether attach also defaults to `<cancel>` (safer, consistent) or
to `<yes>` (faster, and attach is reversible via detach). Either is fine — just state it.

### Blocking vs polish (Round 3)

I is worth blocking on (it's a real data-flow/scope gap). J and K are cleanup.

### Round 3 decisions

**I — DECIDED: `Service.List` + events, readdir for default-dir projects, no cwd scan.**

- Command list and the running-project set come from `cmdman.Service.List` (filtered to
  compose commands by label); group rows client-side by the compose project/workdir
  labels.
- Use `Service.Events` as a **change signal that triggers a (debounced) re-`List`**, not
  a hand-applied per-event delta. This sidesteps the unknown-ID problem from finding I:
  no need to `Inspect` a freshly-`created` ID to learn its labels — the next `List`
  already reflects it, filtered to compose scope. (If true incremental deltas are ever
  wanted instead, the unknown-ID `Inspect` step comes back; re-list is the simpler v1.)
- Discover never-run / default-location projects (e.g. `tools  0 commands`) by `readdir`
  of the default compose dir.
- Determine the cwd-tied **active** project by comparing the workdir label from
  `Service.List` to `os.Getwd()`. Do **not** scan cwd for compose files; the
  readdir + `cmdman compose config` verification path is explicitly **not** done in v1.
- Remaining sub-note: the Compose tab `mux` badge is not present in `Service.List` or a
  plain `readdir` — it still requires opening each project's compose file
  (`Spec.Mux != nil`). Either parse-on-list or defer the badge.

**J — DECIDED (accepted as proposed):** while the filter input has focus, character keys
edit the filter and lifecycle/quit single-key bindings are inert; `esc` leaves filter
focus first.

**K — DECIDED:** the attach popup defaults to `<yes>`.

---

## Round 4 — review of the Round 3 application

I/J/K all landed correctly and thoroughly: PLAN.md "List Data Sources" + TUI_RUNTIME.md
"List Loading" name the real primitives (`Service.List` + compose labels,
`ListNamedProjects()` for never-run projects, `ListProjects()` for counts, `Spec.Mux`
parse for the badge, no cwd scan), events are debounced re-list triggers filtered to
compose scope, filter-focus modality is in CORE + PLAN, attach defaults to `<yes>`.

The blocking gaps (rounds 1–3) are resolved. The plan is implementable as written.
Remaining items are minor — listed for completeness, not as blockers.

### L. Project merge key is ambiguous (minor, only real-correctness item left)

`compose.ListNamedProjects()` returns project **names** only, but store identity is
`(WorkDir, Project)`. A named file under the default dir has no intrinsic workdir, so
merging filesystem names with `ListProjects()`/`Service.List` rows by **name alone** can
collide when the same project name was run from two different workdirs (one row, or a
mismatched merge). The docs should state the merge key explicitly — e.g. match by
project name scoped to the default-dir workdir, and treat same-named projects from other
workdirs as distinct rows. Decide this rather than leave it to implementation.

### M. Lifecycle keys on a project row are unspecified (trivial)

Actions target the selected command, but a project (group) row can be selected for
folding. State that `s`/`S`/`r`/`a`/`x` on a project row are a no-op with a status
message (or define project-level semantics) so the behavior isn't accidental.

### N. Active-project path comparison needs normalization (trivial)

Comparing the stored workdir label to `os.Getwd()` should normalize both (absolute,
symlink-resolved, trailing slash) or a symlinked launch dir silently fails to match.

### Trivial / implementation-level (no doc change needed)

- the TUI needs a `compose.Service` instance (for `ListProjects`) alongside
  `cmdman.Service`; construction is an impl detail
- Commands tab could use an explicit empty state when zero compose projects exist
  (the Compose tab already has one)
- the debounced re-list must leave the active preview/`Follow` reader and selection/scroll
  untouched (already implied by "selection/fold preserved across refresh"; worth a
  one-liner only if codex wants it explicit)
- TUI_MUX.md `c` (cycle) is also a mux invocation, so the non-popup warning in
  "Mux Display" applies to it too — a cross-reference would prevent misreading

### Assessment

Rounds 1–3 closed every substantive gap. Round 4 found nothing blocking. L is the only
item with correctness implications; M/N are quick clarifications; the rest are
implementation notes. The plan is ready to implement.
