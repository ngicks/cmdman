# Notes: codebase grounding for quick-launch / frame / monitor-state

Companion to [IDEA.md](./IDEA.md), elaborated against the codebase
(2026-08-05). These are constraints and cost notes — they inform sequencing
and PLAN.md's context and approach, never the target experience stated in
IDEA.md. Implementation-side open questions 11–20 live at the bottom;
numbering continues from IDEA.md's usability questions 1–10 so
cross-references stay stable.

---

## Track A — Quick-launch

### What exists already

- The TUI Compose tab runs `compose up` via
  `backend.ComposeUp(ctx, project, composeFile)`
  (`pkg/cmdman/tui/composeup.go`, `pkg/cmdman/cli/tui_backend_compose.go`).
  The whole flow needs exactly **two strings** — project name and compose file,
  either possibly empty; `-f` also accepts a bare project name resolved under
  `~/.config/cmdman/compose/` (`compose/discover.go`). Work dir is derived
  inside `Normalize`, never passed by the TUI.
- Mux integration: `cycleMux` → `backend.CycleMux` →
  `compose.ResolveMuxSelectionByName` + `Service.MuxUp`
  (`pkg/cmdman/tui/mux.go`, `pkg/cmdman/cli/tui_backend_mux.go`).
- The TUI's `--popup` mode already exists — the popup-summoned launcher
  gesture (IDEA question 1's default) has its delivery vehicle.
- Filtering: the matcher `matchesFilter` (`pkg/cmdman/tui/filter.go`) is pure
  and data-source-agnostic (contains + in-order fuzzy). The _input plumbing_
  is not: keystroke routing is hard-switched on tab identity in three switches
  (`activeFiltering`, `setFiltering`, `editFilter` in `tui/keys.go`). A
  launcher built as a **new tab** inherits filtering by adding one case to
  each; an **overlay** (like `defViewer`) bypasses the filter path entirely
  and needs its own key handling.
- `workDir` override flows `--workdir` → `RunTUI` → `serviceBackend` →
  `NormalizeOpts.WorkDir`; it never changes the process cwd.
- Project enumeration: `compose ls` is **store-only** — it lists commands
  labeled `cmdman.compose.*` and groups by `(LabelWorkdir, LabelProject)`
  (`compose/service_list.go`); a project with no stored commands does not
  appear. The TUI already compensates: `ListProjects` merges store summaries +
  `compose.ListNamedProjects()` + cwd discovery
  (`cli/tui_backend_compose.go`). The launcher's data source is _this merge
  plus a history table_, not `compose ls` alone.
- The store migration recipe is known and cheap (see the table sketch below),
  and a version bump forces `cmdman migrate` on existing DBs — acceptable,
  back-compat is a non-goal.

### Corrections to the original sketch

- **There is no `-f` file _set_.** `-f` is a single string end-to-end
  (`compose.NormalizeOpts.File`, `LabelFile`, single `StringVarP` in
  `cmd/cmdman/commands/compose.go`). History records one file string;
  multi-`-f` would be prerequisite work, not part of this track. The old
  "what about the file set" keying question dissolves.
- **Nothing survives project teardown today.** `LabelFile`/`LabelWorkdir`
  live on per-command labels; remove the commands and the record is gone. No
  timestamp anywhere says "project X was last up'd at T" (the only timestamps
  are `CommandConfig.CreatedAt` and `CommandExitCode.Timestamp`). The history
  table is genuinely new state, not a projection of existing state.
- The compose config hash cannot substitute as a key: `hashCanonical`
  deliberately excludes compose file path and project name
  (`compose/hash.go`).

### History table sketch

Project identity everywhere in compose is already the `(WorkDir, Project)`
pair — the label filter (`projectLabels`), the `service_list` grouping key,
and the mux identity `GenerateProjectIdentity(workdirHash(WorkDir), Project)`
all agree. History keys on the same pair:

```sql
CREATE TABLE ComposeHistory (
  WorkDir  TEXT NOT NULL,  -- canonicalized project dir
  Project  TEXT NOT NULL,  -- '' for unnamed
  File     TEXT NOT NULL,  -- the -f string as given (path or bare name)
  LastUsed TEXT NOT NULL,  -- RFC3339 UTC
  PRIMARY KEY (WorkDir, Project)
);
```

- Upsert on every `up`. `File` is "last-used file for this project", not part
  of the key — re-upping with a different `-f` overwrites.
- Canonicalization wart: mux identity canonicalizes with
  `filepath.Clean(filepath.Abs(p))` **without** symlink resolution
  (`compose/hash.go`), while the TUI's `normalizePath` resolves symlinks
  (`cli/tui_backend.go`). History should use the `hash.go` form so history
  keys and mux identities agree.
- Moved/deleted files: keep rows; a launch that fails to resolve surfaces the
  error and offers removal (or a `--prune`). Don't validate at write time.
  (Matches IDEA.md's failure-experience requirement; open question 12.)
- Mechanics: new `store/migration/0003_*.sql`, hand-sync
  `store/schema/schema.sql` (drift test enforces), new
  `store/schema/query/*.sql`, `go generate ./pkg/cmdman/store`, hand-written
  wrapper on `*Store`. (Existing wrappers pass `context.Background()`
  internally — don't copy that; take ctx.)
- **The plumbing is the real cost, not the table.** `cmdman.Service.store` is
  unexported, and `compose.Service` reaches cmdman only through the
  `cmdmanSvc` consumer interface (`compose/service.go`), which exposes no
  store access. Writing history from `compose.Service.Up` needs a new
  exported method on `*cmdman.Service` plus a `cmdmanSvc` addition — the
  alternative is recording at the CLI/TUI call sites around `Up`. (Open
  question 11.)

### The launcher flow

- Search-as-you-type over merged history + discovered projects + active
  windows; selecting a cold entry runs the same two backend calls the
  Compose tab already makes: `ComposeUp(project, file)` then
  `CycleMux(project, file)` — plus the focus switch that makes the landing
  complete. Selecting a running entry is focus-switch only. No new machinery
  below the TUI.
- Active windows are already enumerable: `muxctl.Server.ListWindows` with no
  identity filter returns every cmdman-owned window (what `cmdman mux ls`
  does), with `WindowName` and `Identity`. Mapping an identity back to a
  readable project needs either the history table (compute
  `GenerateProjectIdentity` per row and match) or parsing the
  `cmdman-<project>` window-name convention — the history-table route is the
  honest one.
- The gesture and the landing are the real decisions (open questions 1–2);
  whether the view is a tab or overlay inside the TUI is downstream of the
  gesture (a popup-summoned launcher is effectively its own surface either
  way).

### CLI shortcut — cheap, and worth doing first

`runComposeUp` (`cmd/cmdman/commands/compose_up.go`) already holds the loaded
`ComposeSpec`, which carries the parsed `Mux *mux.Spec`; `specSelection`
(`compose/selection.go`) builds the `ProjectSelection` that `MuxUp` needs from
exactly that spec. So `cmdman compose up --mux` is one flag plus a short tail
after the `Up` call — **no second load, no re-resolution**. It kills most of
the daily tedium regardless of the TUI launcher. (Open question 3.)

---

## Track B — Frame

### Where user-level things live today

The global config is **JSON** (`~/.config/cmdman/config.json`,
`pkg/cmdman/config/config.go`), and the established user-level **YAML**
convention is standalone files under the config dir —
`~/.config/cmdman/compose/<name>.yaml` (`ComposeConfigDir`,
`compose/discover.go`). A frame spec fits that pattern
(`~/.config/cmdman/frame.yaml`, possibly `frame/<name>.yaml` variants later),
not a new field inside config.json. (Open question 14.)

### Carving maps onto the existing spec model

The sequential-carving semantics in IDEA.md map mechanically onto the
existing spec model: entry i becomes a two-child container
`[entry-pane (fixed Size), rest]`, recursing, with the project layout at the
innermost `rest`. `muxctl.Size` already speaks cells (`Nc`) and
percent-of-parent (`N%`), and the tmux driver's
`materialize`/`ComputeChildCells` (`muxctl/tmux/apply.go`,
`muxctl/layout.go`) already do exactly this sequential carve, resolved to
cells (`split-window -l N`) before splitting. **No new size grammar or tree
machinery is needed at the spec level.**

### The hard part the original sketch understated: window ownership

The current driver model rests on two facts a frame violates:

1. **One identity slot per window.** Ownership is a single window-level tmux
   user option `@cmdman_window`, set at `Server.New`, matched by exact
   equality in `ListWindows`, cleared by `Detach` (`muxctl/tmux/tmux.go`,
   `list.go`, `detach.go`). A frame identity and a project identity cannot
   coexist on one window today.
2. **`ApplyLayout` resets the whole window.** `resetWindow`
   (`muxctl/tmux/apply.go`) kills every pane but the anchor before splitting,
   and `Detach` does the same. Any frame pane sharing the project's window is
   destroyed by the next `mux up`, layout cycle, or `mux down`.

Also, muxctl's documented contract is "a single command invocation owns
exactly one window" (`muxctl/session.go`); a frame breaks that deliberately.

Two implementation shapes fall out (open question 13) — note the in-place
switching experience (IDEA question 4's default) effectively forces (ii):

- **(i) Compile-in.** Frame + project layout compile into one combined
  `PaneSpec` tree at `mux up` time (the mapping above). Zero muxctl changes.
  But frame panes are killed and respawned on every layout cycle, the frame
  shares the project's identity, and it dies with `mux down` — contradicting
  the lifecycle stated in IDEA.md. A prototype shape to validate the spec and
  carving UX, not the destination.
- **(ii) First-class frame.** The frame owns the window
  (`@cmdman_window` = frame identity); the project layout applies _into the
  leftover pane_. Requires:
  - Subtree-scoped apply: `ApplyLayout` (or a sibling) anchoring on a given
    pane instead of resetting the window; `resetWindow` and `Detach` must
    spare frame panes. Pane-level stamps are established precedent
    (`@cmdman_marker`, `@cmdman_leaf`) — a `@cmdman_frame` pane option is the
    same trick.
  - Identity coexistence: window identity = frame; project identity needs a
    second home (a second window option, or a pane-level stamp on the main
    region) and `mux down`'s enumeration must learn it.
  - This is a real muxctl contract revision — likely its own plan, in the
    style of the existing muxctl plan series.

### Implementation notes

- **Units:** confirmed against the driver — everything resolves to cells
  before `split-window -l`; percent resolves against the remaining rectangle
  by construction. Nothing new needed.
- **Entries as leaves:** `paneArgv` (`mux/build.go`) is the single argv
  writer; `command:` passes argv through verbatim, `component:` resolves to a
  cmdman invocation — both collapse to pane-with-argv below the spec layer.
- **But no widget mode exists.** `tui.Options` has no restricted/single-view
  mode; `--tab` only selects the startup tab. `component: switcher` implies a
  new entrypoint (`cmdman tui --widget switcher`, or a hidden subcommand) —
  small but real TUI-side work, and the spelling is part of the frame spec's
  contract. (Open question 15.)

---

## Track C — Runtime state

### What exists — corrected

The monitor does **not** parse output with `charmbracelet/x/ansi` directly;
it feeds a `charmbracelet/x/vt` emulator
(`pkg/cmdman/monitor/terminal_screen.go`, `vt.NewEmulator`) whose snapshot
serves attach scrollback. All output funnels through
`Monitor.logCommandOutput` (`monitor/mon_run.go`): CSI-only
`terminalState.Observe` → ring buffer → per-line log write + broadcast →
`screen.feed`.

Three load-bearing facts:

1. **The hook already exists, unregistered.** `vt` exposes
   `Callbacks{Bell, Title, IconName, …}` via `Emulator.SetCallbacks`; cmdman
   never calls it, so BEL and OSC 0/1/2 are parsed and swallowed today.
   Capture = register callbacks and latch state in the monitor — no new
   parsing infrastructure. There is **no `Title()` getter** on the emulator;
   callbacks are the only route.
2. **Both parsers are TTY-gated.** `terminalState.Observe` and `screen.feed`
   run only when `cfg.Tty` — a piped command's BEL/OSC never reach any
   parser. Either accept capture as TTY-only (where interactive tools live
   anyway) or pay for parsing non-TTY output too. (Open question 18.)
3. **The screen is recreated per run**, so title/bell state naturally resets
   on restart — matching "runtime state" semantics. vt's title parse is
   strict (`OSC 0;title` with exactly one `;`). OSC 9/777 are absent from
   vt's default handler table but `RegisterOscHandler` is public — desktop
   notifications are addable later without forking the dep. (Open
   question 19.)

Caution: vt callbacks fire from inside `Write` while the emulator's lock is
held (also true of the TUI's `SafeEmulator`) — callback bodies must only
latch state, never re-enter the emulator or block.

### Active status reporting — grounding

- **Self-identification is already solved.** Every supervised child gets
  `CMDMAN_CMD_ID` (plus `CMDMAN_DATA_DIR` / `CMDMAN_RUNTIME_DIR` /
  `CMDMAN_CMD_DATA_DIR`) injected into its environment
  (`config.WithCommandContextEnv`, `pkg/cmdman/config/env.go`, applied in
  `monitor/mon_run.go`). A hook inside the command can call
  `cmdman <report-verb> --status waiting` with no arguments about _which_
  command — resolution comes free from the env.
- The verb's transport can be either a store write (the CLI already opens
  the store) or the monitor's socket (`SocketPath` is in the state JSON) —
  same trade-off space as trapped state, decided together with it. (Open
  question 20.)

### Storage — do not reuse labels

`Labels` lives on `CommandConfig` (`pkg/cmdman/model/command_config.go`) and
is user-supplied _configuration_ — also load-bearing for lookup
(`FindByLabels`) and the compose config hash; titles/bells are _runtime
state_. Mixing them means config writes on every title change, hash churn,
and namespace collisions.

New fact that reshapes the options: **`ls` and `compose ps` are store-only —
they never dial monitor sockets** (`cmdman_list.go`, `service_list.go`). The
only live merge today is `Inspect`, which dials a single socket when
`SocketPath` is set. Options, updated:

1. **In-memory in the monitor + RPC**: extend `StatusResponse` (currently
   `{state, exit_code, pid}`, `pkg/api/schema/proto/cmdman/v1/cmdman.proto`)
   or add an RPC. Truthful and ephemeral — but `ls` would grow a
   dial-every-socket loop it doesn't have. Fits the TUI/switcher (live
   subscribers), heavy for `ls`.
2. **`CommandState` JSON blob** — an option the original list missed.
   `CommandState` is already the monitor-owned runtime blob (`MonitorPID`,
   `SocketPath`, `StartedAt`, …), written exclusively by the monitor and
   surfaced to `ls`/`ps`/TUI through `StateJSON` with **zero plumbing and no
   schema migration** (JSON column). Cost: a store write per title change —
   needs debouncing in the monitor, since shells retitle on every prompt.
3. **eventlog** for the notification-shaped part: `model.Event.Attrs` is a
   free-form map, and the TUI already tails the event log, re-listing on
   every event — a `bell` event reaches the switcher through existing
   wiring. Note `EventType` doubles as persisted state; `bell`/`title` would
   be Type-without-State events (precedent: `stopped`, `signaled`).

A plausible composite: title + reported status → CommandState blob
(debounced for titles; reported status changes are rare enough to write
directly); bell → eventlog event (drives badges); all mirrored on
`StatusResponse` for live queries. That's several write paths, though —
decide in planning, driven by which consumers the UX answers imply. (Open
question 17.)

### Consumer costs

- Titles and reported status in `ls` / `ps` / TUI rows are free if stored in
  CommandState (storage option 2).
- Bell reaches the Track B switcher naturally via eventlog (storage
  option 3).
- The TUI preview already runs its own `vt.SafeEmulator` (also without
  callbacks) — it could latch the focused command's title locally, but list
  rows need the monitor-side capture regardless.

---

## Sequencing / dependencies

- B's switcher widget consumes A's "active projects" enumeration and C's
  bell/title/reported-status state — both already have (or can cheaply grow)
  APIs independent of B, so build order stays free.
- A is standalone and hits the daily pain directly (its `--mux` flag is
  near-free); C is standalone; B is the integrator and the only track that
  forces a muxctl contract change.

## Open questions — implementation

Decided in planning, after IDEA.md's usability questions 1–10 are pinned.
Numbering continues from IDEA.md.

11. **(A) Who writes history rows** — `compose.Service.Up` via a new
    exported method on `*cmdman.Service` + `cmdmanSvc` addition, vs
    recording at CLI/TUI call sites around `Up`.
12. **(A) Stale history rows** — keep and surface resolution failure at
    launch (with removal offered), vs validate/prune eagerly.
13. **(B) Frame build shape** — compile-in spike (i) vs straight to
    first-class ownership (ii). Largely forced by question 4: in-place swap
    requires the first-class frame.
14. **(B) Frame spec location** — ✅ **Resolved (2026-08-09)** by the Q34
    reframe: named defs under `<config-dir>/frame/<name>.yaml`, like
    compose defs; the def to use is passed via config or command flag.
    (DECISION.md D15.)
15. **(B) Widget entrypoint spelling** — `cmdman tui --widget <name>` vs a
    hidden subcommand; part of the frame spec's contract since `component:`
    resolves to it.
16. **(B) Third union variant** — frame entries referencing a cmdman-managed
    command by name (resolved like mux leaves), or spell out
    `command: ["cmdman", "attach", …]`. Partially shaped by usability
    Q32 (D19): a raw `command:` entry gains `managed: true` to become
    cmdman-supervised; remaining here is whether referencing an
    *existing* named command is also supported.
17. **(C) Storage** — in-memory + RPC, CommandState blob, eventlog, or the
    composite; driven by which consumers the answers to 7–10 imply.
18. **(C) TTY-only capture** — accept that non-TTY commands get no
    title/bell, or extend parsing to the pipe path.
19. **(C) OSC 9 / OSC 777 capture scope** — first pass or later (cheap to
    add via `RegisterOscHandler`). Other candidate sequences, same
    mechanism: OSC 133 (semantic prompt marks — passive working/waiting +
    exit codes for shell-hosted work), OSC 7 (cwd → live context), OSC 9;4
    (progress percent), OSC 99 (structured notifications).
20. **(C) Report verb spelling & transport** — the CLI verb a command's
    hook calls (`cmdman report`? `cmdman status set`?) and whether it
    writes via the store or the monitor socket; identity resolution is free
    via `CMDMAN_CMD_ID`.

Questions 35–36 were added while resolving usability questions
(2026-08-09); numbering continues after IDEA.md's 21–34.

35. **(C) OSC hook config schema** — usability Q8 (D17) decided a
    per-command OSC hook system: hooked OSC dispatches a configured
    command, passthrough by default, built-in hooks (badge, notify, …),
    frame-def override. To pin: the persistent config shape (where in
    `model.CommandConfig` / global config it lives), the built-in hook
    vocabulary and their names, the dispatch execution model (argv, env
    passed, blocking vs fire-and-forget from the monitor's callback
    path — must not block; vt callbacks run under the emulator lock),
    and how a frame def's override composes with per-command config.
36. **(A) Git info source for launcher rows** — usability Q23 (D18)
    makes display/match git-aware (repo name + branch, repo uri).
    To pin: read `.git` directly (HEAD, config) vs exec `git`;
    per-entry cost at launcher-open time; caching/refresh (branch
    changes while the launcher is open are acceptable to miss).

Implementation fact learned from the mocks (2026-08-10): glyph width
math must use `go-runewidth` through an explicit
`runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}`,
not the package default. ● / ○ are East-Asian *ambiguous* — under a CJK
locale the runewidth default counts them 2 while the renderer
(uniseg/lipgloss) draws 1, tearing column alignment. Rendered
(ANSI-carrying) strings still measure via `lipgloss.Width`. The real
switcher/statusbar/launcher widgets inherit this; both mocks
demonstrate the pattern.
