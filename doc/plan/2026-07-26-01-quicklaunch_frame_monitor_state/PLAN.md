# Plan: quick-launch, frame, monitor runtime-state

Ship [IDEA.md](./IDEA.md)'s three tracks as ordered increments: one-shot
compose bring-up and a fuzzy launcher with history (A), trapped + reported
per-command runtime state (C), and the frame with its switcher widget (B) —
with B's muxctl contract revision split into its own follow-up plan.

Status: **finalized** (2026-08-10). All 36 open questions (usability
1–10/21–34, implementation 11–20/35–36) are resolved — see
[DECISION.md](./DECISION.md) D1–D41. Contracts below are binding;
implementation may begin at phase 0. Codebase grounding lives in
[NOTES.md](./NOTES.md); this file cites it rather than repeating it.

## Goal / success criteria

From IDEA.md's pain points, in observable terms:

1. **One-shot bring-up (A).** `cmdman compose up --mux` (spelling: Q3)
   brings the environment up and shows its mux layout in one command.
2. **Launch from anywhere (A).** One gesture summons a launcher popup; a
   few letters + Enter on a merged list (history, discovered projects,
   running windows) ends with the environment up and focus inside the
   project. Re-launching a running project is idempotent — it just takes
   you there. Background-launch variant on an alternate select.
3. **Which agent needs me (C).** A command's BEL/title is captured, and a
   hook can run a report verb; `cmdman ls`, TUI rows, and (later) the
   switcher show one state word / title / unread badge per command.
4. **Persistent chrome (B).** A user-level `frame.yaml` renders docked
   components around a main region; the switcher strip lists projects with
   attention state; selecting one switches without rebuilding the chrome.

## Scope

- Track A in full (phase 0 + phase 1).
- Track C in full (phase 2).
- Track B through the frame spec, widget entrypoint, and switcher widget
  (phase 3) — **except** the muxctl ownership/contract revision
  (subtree-scoped apply, identity coexistence), which is a separate plan
  this one only writes requirements for (NOTES.md "first-class frame").

## Non-goals

- Multi-`-f` compose file sets — `-f` stays a single string (NOTES.md
  "Corrections").
- Desktop-notification relay of OSC 9/777 (Q8 default: badges only; relay
  is a later opt-in).
- Zellij or any non-tmux driver work.
- Backward compatibility of DB schema or CLI surface (never deployed).

## Context

See [NOTES.md](./NOTES.md) for the full grounding. Load-bearing facts:

- TUI already does the whole cold-launch flow with two strings:
  `backend.ComposeUp(ctx, project, composeFile)` + `backend.CycleMux`
  (`pkg/cmdman/tui/composeup.go`, `pkg/cmdman/tui/mux.go`,
  `pkg/cmdman/cli/tui_backend_compose.go`). `--popup` mode exists.
- `runComposeUp` (`cmd/cmdman/commands/compose_up.go`) already holds the
  parsed `ComposeSpec` with its `Mux *mux.Spec`; `specSelection`
  (`pkg/cmdman/compose/selection.go`) builds what `Service.MuxUp` needs —
  the `--mux` flag is a short tail, no re-load.
- Project identity is the `(WorkDir, Project)` pair everywhere; history is
  genuinely new state (nothing survives teardown today).
- Monitor output funnels through `pkg/cmdman/monitor/mon_run.go` into a
  `vt.Emulator` (`terminal_screen.go`); `Emulator.SetCallbacks` exists and
  is unregistered — capture is "register callbacks and latch", TTY-gated.
- Store migrations: `pkg/cmdman/store/migration/0001_init.sql`,
  `0002_created_at.sql`; `store/schema/schema.sql` is hand-synced (drift
  test `TestSchemaSQLMatchesMigrationChain`); queries under
  `store/schema/query/` regenerate via `go generate ./pkg/cmdman/store`.
- The frame violates two muxctl invariants (one identity per window;
  `ApplyLayout`/`Detach` reset the whole window) — the reason phase 3's
  core is a separate plan.

## Approach

Four phases, ordered by pain-relief per unit of risk. A and C are
independent; B integrates both and is last. Each phase lands usable on its
own.

- **Phase 0 — one-shot CLI** (near-free, kills daily tedium immediately).
- **Phase 1 — history + launcher** (new table, launcher surface, landing).
- **Phase 2 — runtime state** (vt callbacks, report verb, storage,
  `ls`/TUI surfacing).
- **Phase 3 — frame** (spec + widget entrypoint + switcher; muxctl
  contract revision spun off as its own plan, then consumed here).

Rejected alternatives:

- *Launcher outside the TUI* (standalone fzf-style binary): the TUI popup
  already exists, shares the backend, and inherits filtering; a second
  UI stack buys nothing.
- *Labels as state storage* (Track C): rejected in NOTES.md — labels are
  config, load-bearing for lookup and hashing.
- *Compile-in frame as the destination*: rejected as final shape (dies
  with `mux down`, killed on layout cycle); acceptable only as a
  disposable spike if Q4 lands on per-window semantics.

## Contracts

The expensive-to-change surfaces, pinned before implementation. Each is
gated by the question(s) listed.

### ComposeHistory table (gates: Q11, Q12; sketch settled in NOTES.md)

`pkg/cmdman/store/migration/0003_compose_history.sql`, hand-synced into
`store/schema/schema.sql`, new `store/schema/query/compose_history.sql`:

```sql
CREATE TABLE ComposeHistory (
  WorkDir  TEXT NOT NULL,  -- canonicalized as compose/hash.go does (no symlink resolution)
  Project  TEXT NOT NULL,  -- '' for unnamed
  File     TEXT NOT NULL,  -- the -f string as given (path or bare name)
  LastUsed TEXT NOT NULL,  -- RFC3339 UTC
  PRIMARY KEY (WorkDir, Project)
);
```

Upsert on every `up`; `File` is last-used, not part of the key. Wrapper
methods on `*store.Store` take `ctx` (do not copy the
`context.Background()` pattern of older wrappers).

### Runtime state: monitor-held, streamed (decided D32; was CommandState composite)

Title, reported status (enum `working`/`waiting`/`done` + detail, D12),
and bell-unread live **in-memory in the Monitor** — no store writes, no
`model.CommandState` additions. Served over the monitor socket:
one-shot via an extended `Status`, live via a new **server-streaming
RPC** (initial snapshot, then push on change; titles debounced
monitor-side before broadcast). Death-with-run (D13) is automatic.
Consumers: TUI/switcher/launcher subscribe; `ls`/`ps` fill their
columns with a bounded parallel one-shot dial across commands that have
a `SocketPath` (short timeout, missing socket → column empty). Bell
clear (D11) is a monitor-side transition visible on the stream.

### OSC hook config (decided D17/D40)

A `Hooks` field on `model.CommandConfig` plus a defaults map in the
global config; precedence frame-def override > per-command > global >
built-in. Built-ins are only **`passthrough`** (default) and
**`block`** — state capture (badge/bell/title) is the monitor's
unconditional internal latching served via D32's RPC, not a hook.
Configured argv hooks are exec'd fire-and-forget from a separate
goroutine with event data in env vars (`CMDMAN_HOOK_EVENT`, …);
per-command serialization, drop-if-busy; never blocks the emulator-lock
callback path.

### Proto (decided D32/D33)

`pkg/api/schema/proto/cmdman/v1/cmdman.proto`: extend `StatusResponse`
(currently `{state, exit_code, pid}`) with title / reported status /
detail / bell-unread; add a server-streaming watch RPC (snapshot +
push-on-change) and the set/get/delete RPCs backing
`cmdman status …` (D33). Regenerate with `buf generate` from `pkg/api`.

### CLI surface (gates: Q1, Q2, Q3, Q20, Q21)

- `cmdman compose up --mux` (or the spelling Q3 picks).
- Launcher entrypoint: the popup binding target (`cmdman tui --popup
  --tab launcher`-ish) and/or a shell verb; pinned with Q1.
- Report verb spelling (`cmdman report --status …`?) pinned with Q20;
  identity via `CMDMAN_CMD_ID` env (already injected).

### Frame spec file (gates: Q15, Q16; Q5/Q6/Q14/Q24/Q27/Q32/Q34 decided)

Named user-level YAML defs under `~/.config/cmdman/frame/<name>.yaml`,
discovered like compose defs (D15): a flat array of
`{edge, size, component|command}` entries, sequential carving,
percent-of-remainder (Q27 default). The frame is shown / hidden /
selected / cycled explicitly; the def to use comes from config or a
command flag (D15). No default frame; `switcher` and `statusbar` are
built-in components (D16). `component:` resolves to the widget
entrypoint (Q15). Parsing likely lives beside
`pkg/cmdman/compose/discover.go`'s conventions; package home decided in
phase 3.

**Mock previews** (D16; disposable, graduate only as reference for the
real widgets):

- [`frame_mock/`](./frame_mock/) — the framed window: switcher column
  (grouped bucket-sorted list D20, marker vocabulary D21–D24, scrolling
  D25, weak app rows D26), bottom-row form, statusbar built-in, def
  cycling and shown/hidden toggling, mouse selection. Reviews Q27
  (percent feel) and Q31 (interaction). Run:
  `go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/frame_mock`.
- [`launcher_mock/`](./launcher_mock/) — the full-sized popup selector
  (Track A): search-as-you-type over the merged recency list (D7),
  git-aware `repo(branch) (project)` rows matched over path / repo uri /
  branch / project name (D18), Enter = launch+focus vs ctrl+s =
  start-only (D4/D10), stale-entry error kept inline with removal
  (D10/Q12), mux-less landing warning (D9), mouse click. Run:
  `go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/launcher_mock`.

## Implementation steps

Rough until the gating questions resolve; each step independently
verifiable.

### Phase 0 — one-shot CLI

1. Add `--mux` to `compose up`: flag in
   `cmd/cmdman/commands/compose_up.go`, tail in `runComposeUp` calling
   `specSelection` + `Service.MuxUp` after `Up` succeeds. Focus behavior
   per Q2/Q3/Q21. Verify: e2e in `e2e/cmdman` (up --mux creates the
   window; re-run is idempotent).

### Phase 1 — history + launcher (Track A)

2. `ComposeHistory` migration + queries + `*store.Store` wrapper; bump
   schema version. Verify: store unit tests + drift test.
3. Record history on `up` (writer site per Q11). Verify: e2e — `up` then
   inspect DB row; re-`up` with different `-f` overwrites `File`.
4. Launcher view in the TUI (tab vs overlay per Q1's gesture answer;
   NOTES.md: a tab inherits filtering by one case in each of the three
   `tui/keys.go` switches; an overlay needs its own key handling). Data:
   history rows + existing `ListProjects` merge + `muxctl.Server.
   ListWindows` mapped to projects via the history table, plus per-entry
   git info (repo name, branch, uri — source per Q36). Rows read
   `path (project)`, git-aware; match over full path, repo uri, branch,
   project name (D18). Verify: TUI unit tests on the merged/sorted list
   (D7) and the match fields.
5. Landing: cold entry → `ComposeUp` + `CycleMux` + focus switch as
   soon as the window exists (launcher dismisses on Enter, D10);
   running entry → focus switch only; `s` = start-only background
   launch (D4); outside tmux → auto-create-or-attach (D8); mux-less
   project → synthesized shell window + warning (D9). Launch failures
   raise the project's attention state once phase 2's surface exists;
   until then in-place error output only (D10). Verify: e2e inside a
   scripted tmux server.
6. Failure experience: stale history entry surfaces resolution error and
   offers removal (Q12). Verify: e2e with a moved compose file.

### Phase 2 — runtime state (Track C)

7. Register `vt.Callbacks{Bell, Title}` in
   `pkg/cmdman/monitor/terminal_screen.go` / `mon_run.go`; latch only
   (callbacks fire under the emulator lock — never re-enter or block).
   Verify: unit test feeding OSC/BEL bytes.
8. Monitor-held state + stream (D32): latch title/bell/status
   in-memory, extend `Status`, add the server-streaming watch RPC
   (debounced title broadcast); `ls`/`ps` bounded parallel dial.
   Verify: unit tests on debounce/stream; e2e — command emits title,
   `ls` shows it.
9. OSC hook dispatch (D17/D40): `Hooks` on CommandConfig + global
   defaults; built-ins passthrough/block; async argv exec with
   `CMDMAN_HOOK_*` env, fed by step 7's capture; never blocks the
   callback path; frame-def override comes with phase 3. Verify: unit
   tests on hook resolution/precedence; e2e — a configured hook command
   fires on bell.
10. Status verbs (D33): `cmdman status set|get|delete` (+ compose
    mirror), identity from `CMDMAN_CMD_ID`, transport = monitor socket.
    Verify: e2e — hooked command reports `waiting`, `ls` shows it;
    status dies with the run (D13).
11. Surfacing: `ls`/`ps` columns and `--format` fields
    (`pkg/cmdman/cli/`), TUI rows, `Inspect`. Aggregation rule D14 and
    grouped title ordering D20 live here for the TUI. Verify: golden-ish
    CLI output tests + TUI unit tests (incl. bucket sort).

### Phase 3 — frame (Track B)

12. Frame spec: YAML type + load/validate; named-def discovery under
    `<config-dir>/frame/` mirroring `compose/discover.go` (D15);
    `managed:` flag on `command:` entries (D19); carving mapped onto the
    existing spec model (entry i → two-child container, NOTES.md
    "Carving"). Verify: unit tests incl. order-dependence and
    percent-of-remainder (Q27).
13. Widget entrypoint (D37): `cmdman tui widget <name>` command group,
    each widget a subcommand; restricted single-view TUI mode
    (`tui.Options` today has no widget mode). Verify: run
    `cmdman tui widget switcher` standalone.
14. **Scope (and if needed spawn) the muxctl sub-plan** — sized by Q13
    now that D15 defines the feature: what the driver must support to
    show / hide / cycle a frame around a project window (subtree-scoped
    apply, `@cmdman_frame` pane stamps, identity coexistence,
    `resetWindow`/`Detach` sparing frame panes — NOTES.md "The hard
    part"). This plan blocks on the outcome only from step 15 on.
15. Frame verbs: show / hide / select / cycle, def via config or flag
    (D15); switcher widget consuming phase-1 enumeration + phase-2
    state, grouped-list column form + aggregated row form (D20/D14),
    project dot markers and whole-group selection highlight (D21);
    selection navigates per-project windows (D6), interaction per Q31;
    frame-def hook override (D17). Verify: e2e in scripted tmux; chrome
    survives project switch/stop/relaunch.

## Testing / verification

- Unit tests beside code as usual; e2e in `e2e/cmdman` (TestMain-built
  binary — required anyway since the monitor re-execs `os.Executable()`).
- tmux-dependent e2e runs against a scratch tmux server (socket in temp
  dir), as existing mux tests do.
- Store: drift test + migration chain test must stay green.
- Manual passes for feel: popup gesture latency, launcher few-letter
  matching, frame collapse.

## Risks

- **Phase 3 rests on a muxctl contract revision** — mitigated by
  splitting it into its own plan and keeping phases 0–2 independent.
- **Title write churn**: shells retitle per prompt → debounce in the
  monitor before any store write (step 8).
- **vt callback reentrancy**: callbacks run under the emulator's lock —
  latch-only bodies, asserted in review.
- **`tui/keys.go` tab-identity switches**: launcher-as-tab touches three
  hard-switched sites; overlay avoids them but re-implements filtering.
- **Phase 3's size hangs on Q13**: showing/hiding a frame around a
  project window still touches muxctl's ownership invariants; how much
  is decided in step 14, after the mock validates the feature's feel.

## Open questions

Usability questions (numbered 1–10, 21–33) are stated in full in
[IDEA.md](./IDEA.md); implementation questions (11–20) in
[NOTES.md](./NOTES.md). They resolve in that order — usability first,
since 11–20 are downstream of those answers. One line each here for
reference; ✅ marks resolved (see [DECISION.md](./DECISION.md)).

### Usability (resolve with the user, before implementation detail is final)

1. ✅ (A) Launcher gesture — popup key binding primary (D3).
2. ✅ (A) Landing — Enter = launch + focus, idempotent; `s` = start-only background launch (D4).
3. ✅ (A) One-shot spelling — `compose up --mux` (D5).
4. ✅ (B) Switching semantics — per-project windows (D6); opens Q34.
5. ✅ (B) Frame appearance — explicit shown/hidden/selected/cycled control; no lazy appearance (D15).
6. ✅ (B) Default frame — none; `switcher`/`statusbar` are built-ins; show/hide is the collapse gesture (D16).
7. ✅ (C) Bell read/clear — attach/preview clears; per-project aggregation (D11).
8. ✅ (C) OSC handling — reframed: per-command OSC hook system, passthrough default, built-in hooks, frame-def override (D17); schema is Q35.
9. ✅ (A) Jump-list presentation — one recency-sorted list, badges not grouping; docked forms running-only (D7).
10. ✅ (C) Reported-status vocabulary — enum (`working`/`waiting`/`done`) + optional detail; ls, TUI rows, switcher (D12).
21. ✅ (A) Launch from outside tmux — auto-create-or-attach (D8).
22. ✅ (A×B) Landing under in-place swap — moot: per-window semantics everywhere (D6).
23. ✅ (A) Entry display & match — git-aware `path (project)` rows (repo+branch, else basename); match over full path, repo uri, branch, project name (D18); git-info source is Q36.
24. ✅ (B) Frame scope — dissolved by the reframe; per explicit invocation, global project list (D15).
25. ✅ (C) Reported-status lifecycle — dies with the run; exit state shown after (D13).
26. ✅ (C) Per-project aggregation — unread bell outranks; waiting > working > done (D14).
27. ✅ (B) Percent base — remaining rectangle, confirmed on the mock (D30).
28. ✅ (A) Mux-less projects — synthesized one-pane shell window + warning (D9).
29. ✅ (A) Launch feedback — land immediately; failures raise Track C attention; landing-blocking errors stay in the launcher (D10).
30. ✅ (B) Swap-out experience — moot: per-project windows persist (D6).
31. ✅ (B) Docked switcher interaction — mouse + D24/D28/D29 key/zone model (D31); keyboard strip-reach in tmux deferred to phase 3 planning.
32. ✅ (C,B) `command:` frame-entry lifecycle — ephemeral by default; `managed: true` makes it cmdman-managed (D19).
33. ✅ (C) Title representation — grouped list: command titles under project-path groups, ~5 s bucket sort then name-or-id (D20).
34. ✅ (B) Chrome placement — reframed: the frame is a standalone shown/hidden/cycled feature with file-based defs; pane-ownership mechanics move to Q13, validated via the mock (D15).

### Implementation (decided during planning, after the above)

11. ✅ (A) History writer — `compose.Service.Create` via new `cmdmanSvc` method; `LastUsed` bump on up to confirm in implementation (D34).
12. ✅ (A) Stale history rows — keep + surface at launch, removal offered (D10/D28; mock-validated).
13. ✅ (B) Frame build shape — straight to first-class; muxctl sub-plan, no spike (D36).
14. ✅ (B) Frame spec location — `<config-dir>/frame/<name>.yaml`, like compose defs (D15).
15. ✅ (B) Widget entrypoint — `cmdman tui widget <name>`, each widget a subcommand (D37).
16. ✅ (B) By-name frame entries — no; `command: ["cmdman","attach",…]` covers it (D38).
17. ✅ (C) Storage — monitor-held, server-stream RPC; ls/ps bounded dial (D32).
18. ✅ (C) TTY-only capture — accepted (D35).
19. ✅ (C) OSC capture scope — BEL + title + 9/777 first pass; 133/7/9;4/99 later (D39).
20. ✅ (C) Report verb — `cmdman [compose] status set|get|delete`, monitor socket (D33).
35. ✅ (C) OSC hook config — CommandConfig `Hooks` + global defaults; async exec + env; built-ins passthrough/block only (D40).
36. ✅ (A) Git info — exec git per entry at launcher open (D41).
