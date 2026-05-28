# cmdman mux plan (mux-00)

This is a **discussion snapshot**, not an implementation commitment. It was
captured mid-interview while finding the UX and the public API shape. The
layout DSL and the `pkg/muxctl` controller API have converged; what remains is the
layout-tree builder + a few smaller open questions. Resume from "Where we left
off" at the bottom.

## Goal

Add a `cmdman mux` command family that drives a terminal multiplexer (tmux
first) as a **bulk viewer** over several cmdman-managed commands at once: one
pane per command, arranged by a declarative, nestable layout, each pane bound to
a command via `cmdman attach` (or `logs -f`). Plus a compose integration so the
same dashboard can be opened for a compose project.

The feature should let users:

- open a multiplexer session that shows many commands side by side;
- describe the pane arrangement declaratively (nested splits, sizes, per-pane
  command) instead of hand-driving tmux;
- reuse the existing per-command attach/logs plumbing unchanged;
- open the same dashboard for a compose project.

## Guiding principle (load-bearing)

**The multiplexer is a disposable viewer, never the source of truth.** The
cmdman daemon owns process lifecycle; mux only *observes/attaches*. Killing the
mux session, detaching, or closing a pane MUST NOT stop any command. This
resolves most lifecycle questions by construction and is what keeps `mux` from
re-becoming "tmux" (cmdman bills itself as "the tmux without terminals" —
`mux` adds terminals back strictly as a view).

## Non-goals (current thinking)

- Not a process supervisor inside tmux (the daemon already supervises).
- Not a live reconciler in v1: snapshot the command set at invocation; re-run to
  refresh. (Open: may revisit.)
- Not a Docker/zellij/tmux config importer; the layout format is cmdman-native.
- **View-only:** mux never starts/creates commands (use `start` / `compose up`);
  not-running panes rely on the sticky-attach `r`-to-start prompt. A `--start`
  convenience may come later.

## Where the design currently stands

### Decided / converged

- **Pane content = interactive `attach` by default**, per-pane overridable to
  read-only logs via `mode: logs` (which runs `cmdman logs --sticky`; see Attach &
  logs stickiness). Read-only remains a per-pane mode, not the default.
- **Layout is a declarative, nestable tree** (zellij/i3 family), authored in
  YAML in the house style, rooted under a single `mux:` key.
- **`mux:` is the spec root** so the whole thing either lives in its own file
  (standalone `cmdman mux`) or is the compose file's `mux:` section (`compose
  mux`), with no restructuring.
- **Realize each leaf by spawning the attach process directly in the pane**
  (tmux `split-window … -- <argv>`), NOT by spawning a login shell and
  `send-keys`-ing a command line. This avoids rc-file latency, prompt spam,
  alias shadowing, and shell-quoting/injection on command names, and lets a dead
  command leave a closed/idle pane rather than a live shell.
- **Per-pane env injection is retired entirely.** The donor "crabswarm"
  `#{INJECT_META}` / `CRAB_*` shell-export hack is dropped, not renamed: panes run
  `cmdman attach <id>` directly with `--data-dir`/`--runtime-dir` as flags, so
  nothing reads per-pane env. (send-keys and key interpolation are also unused —
  we spawn argv, never type into a shell.)
- **Binary identity:** panes must invoke `os.Executable()` and inherit the root
  persistent flags (`--data-dir`, `--runtime-dir`), never bare `cmdman` from
  `$PATH`.
- **One shared Go struct** (`MuxSpec`) backs both `cmdman mux` and `cmdman
  compose mux`. The only per-command difference is leaf-name resolution: plain
  `mux` resolves to a cmdman command (name/ID); `compose mux` resolves to a
  compose service name.
- **Spec input differs by command.** `cmdman mux` reads a standalone layout file
  (path or stdin). `cmdman compose mux` reads the **`mux:` section of the compose
  file** — no extra flag. The nested tree has no efficient CLI-flag form. Either
  way the `panes` list **is** the selection (the commands that get panes); no
  separate selection flags in the explicit case.
- **mux is a controller, not a terminal host.** It drives the multiplexer purely
  through its CLI (`tmux …`, `zellij …`, or any such command) to build the
  session/windows/panes, and needs **no tty/pty of its own** — the multiplexer
  owns the user's terminal. (So reading the spec from stdin is fine; nothing in
  cmdman competes for the controlling tty.)
- **`cmdman compose mux` is a confirmed subcommand** (not just a candidate). It
  reuses the compose flags for project resolution and `resolveCommandID` /
  `filterByCommandNames` to map a leaf's service name → backing command ID.
- **New controller package `pkg/muxctl`**: minimal single-window `Session`
  (`ApplyLayout`(resets, returns `map[pane-name]Pane`) / `Close`) + a tiny
  `Pane` interface (`PaneId`/`Name`). One invocation owns one window;
  switching among `MuxSpec.Layouts` is repeated `ApplyLayout` calls. The
  window name, session reuse/teardown, and socket choice are **driver config**
  (`pkg/muxctl/tmux.New`), not on the interface. **`MuxSpec` and its tree
  (`Layout`, `PaneSpec`, `Size`) also live here** — generic, with leaves
  carrying resolved `Cmd []string` argv + per-pane `CmdOpt`. The old `pkg/mux`
  tree is superseded. See Public API shape.
- **Standalone `cmdman mux` takes the layout path as a positional arg** (`-` or
  omitted ⇒ stdin).
- **`compose mux` with no `mux:` section is an error** — no synthesized default.
- **Attach is sticky by default** (a general `cmdman attach` change, not mux-only):
  attaching to a not-running command, or after the command exits, prints the state
  and waits with **press `r` to restart & attach**; `--auto-exit` restores the old
  exit-on-exit behavior. mux panes run plain `cmdman attach <id>` and get
  stay-open + restart-prompt for free. See "Attach stickiness."
- **No `compose up --attach` for now** — the dashboard is the separate `compose
  mux` step (may add the flag later).
- **`tmux.New` default specifier:** drive the user's current server/session when
  inside one (`$TMUX`/`$ZELLIJ`), else a session named `cmdman` on the default
  server. Safe because mux owns whole windows/tabs by name and only resets its
  own; a dedicated `-L cmdman` / `zellij --session cmdman` session is the opt-in
  for isolation.
- **Ownership keys on NAMES, not tmux `@`-options** (portable to zellij): pane
  name = command name (cmdman layer's convention; muxctl just sees `PaneSpec.Name`),
  the cmdman-owned window/tab name is fixed by the driver constructor
  (`cmdman` / `cmdman-<project>`). mux owns whole window/tab and never
  injects panes into a user's window.
- **Pane detach-keys are kept** (`ctrl-p,ctrl-q`); inside a pane they mean "close
  this pane" (end the attach / the stay-wait). tmux's `Ctrl-b` still owns
  navigation and whole-dashboard detach.
- **Duplicate command in a layout is rejected** (one pane per command; keeps the
  `map[command-name]Pane` key unambiguous).
- **Autodetect: default `tmux` when neither `$TMUX`/`$ZELLIJ` is set; `$TMUX` wins
  when both** — never errors on the plain-shell case.
- **mux is view-only** (never starts commands); a **running command with no leaf
  is silently omitted** (unknown leaf name = hard error); the leaf-options key
  at the cmdman-YAML layer is **`command:`** (which the cmdman layer resolves
  to argv before emitting `muxctl.PaneSpec.Cmd`); each **layout** `name` is
  required and unique (it is the user-facing switch handle; the cmdman-owned
  window itself is named by the driver constructor and is the re-run
  ownership key).
- **Per-pane driver hints via `cmd_opt`** (a map at the pane level, mirror of
  the top-level `driver_opt`): driver-specific options for a single pane
  (e.g. tmux pane title), surfaced on `muxctl.PaneSpec.CmdOpt`. Drivers
  pick keys they understand and ignore the rest. Replaces per-pane env
  injection (retired).
- **Pane titles = command name** (tmux pane-border title; driver equivalents
  later) so panes are identifiable in multi-pane windows.
- **Initial focus = first leaf** in document order; one `focus: true` overrides;
  more than one per window is an error.
- **v1 ships the tmux driver only.** zellij and wezterm are future TODO drivers;
  the `muxctl` interface is designed to accommodate them (see Cross-driver
  feasibility).
- **Leaf `command:` resolves by ID or NAME**, like every other cmdman subcommand
  (standalone `mux` against the cmdman service; `compose mux` against the project's
  services). When not driving the current session, the created session is named
  **`cmdman`** (fixed).
- **`logs --sticky` meta prefix** defaults to `#|`, configurable via
  `--meta-prefix`. **Too-small terminal** → best-effort: build what fits, skip the
  rest, warn on stderr.
- **Driver-specific options via `mux.driver_opt`** (a map) flow straight to the
  driver constructor (e.g. tmux socket / dedicated server). Isolation is opted
  into here, not via a CLI flag.
- **Outside a multiplexer:** build detached and print the attach hint (`tmux
  attach -t cmdman`), then exit — pure controller, no tty hosted. **Inside:** build
  the windows without stealing focus.

### Open (the part still "up")

- Implementation chunks remaining: (a) attach-stickiness internals (`r`→restart
  wiring, exit-code source, auto-reattach), and (b) the **cmdman-layer** YAML
  parser + leaf resolver above `pkg/muxctl` (turning `command:`-bearing leaves
  into `muxctl.PaneSpec.Cmd` argv via the cmdman / compose services), plus the
  `cmdman mux` / `cmdman compose mux` cobra wiring. The `pkg/muxctl` interface
  and the **tmux driver** under `pkg/muxctl/tmux` are now implemented (see
  "Where we left off"). Everything else (compose surface, tmux default,
  autodetect, detach UX, sizing, dup-command) is settled. See "Open questions."

## Layout spec (UX surface — mostly settled)

Single root `mux:` key. Geometry (`dir` + `splits`) is separated from content
(`panes`).

```yaml
mux:
  driver: tmux            # tmux | zellij | "" (autodetect; see below)
  driver_opt:             # optional, driver-specific (e.g. tmux: socket / dedicated)
    socket: cmdman
  layouts:                # one window per invocation; user switches among these
    - name: services
      dir: h              # h = side by side, v = stacked (tmux semantics)
      splits: [90c, 1, 2] # index-parallel to panes; len(splits) == len(panes)
      panes:
        - api             # leaf: bare string = command/service name
        - worker
        - dir: v          # a pane may itself be a split (nesting)
          splits: [1, 1]
          panes: [redis, db]
```

A **single command invocation creates exactly one window**; the named entries
under `layouts:` are switchable configurations applied to that window (the
cmdman mux family exposes the switch UX). This is NOT a multi-window dashboard
— that's what distinguishes mux from generic tmux/zellij usage.

Grammar:

- A **pane** is either a **leaf** or a **container**.
  - Leaf: a bare string (the command/service name), or a map when options are
    needed: `{ command: api, mode: logs, focus: true }`. (`command:` is the
    leaf-options key; the bare string stays the shorthand.)
  - Container: `{ dir, splits, panes }`.
- **`dir`**: `h` (panes side by side) or `v` (panes stacked). Single-letter, tmux
  convention. (Note the tmux/zellij terminology inversion: tmux `-h` = zellij
  "vertical" = side by side. We use tmux's `h`/`v` because tmux is primary.)
- **`splits`**: sizing array, **index-parallel to `panes`** (`len(splits) ==
  len(panes)`). `splits[i]` sizes `panes[i]`.
- **Size grammar**:
  - `Nc` — absolute, `N` character cells (`c` = character). Columns when `dir:h`,
    rows when `dir:v`.
  - `N%` — percent of parent dimension (1..100). Resolved as
    `floor(parent * N / 100)` cells; reserved-first alongside absolutes (so
    `[50%, 1, 1]` gives the first pane half the parent and the rest split
    1:1 over the remainder, minus separators).
  - bare `N` — proportional weight. Absolutes and percents are reserved first;
    the leftover space is divided among the bare weights by ratio (e.g.
    `[90c, 1, 2]` → first pane 90 cells, the rest split 1:2).
- Only the layout **root** pane is unsized (fills the window). Every nested
  pane's size comes from its parent's `splits[i]`.
- Each **layout** requires a unique `name` (error if missing or duplicated) — it
  is the user-facing switch handle. The cmdman-owned **window** itself is named
  by the driver constructor (a fixed `cmdman` / `cmdman-<project>` window) and
  is the ownership key drivers use to find and reset only their own window
  on re-run.
- Initial **focus** = the first leaf in document order, unless exactly one leaf
  sets `focus: true` (more than one per layout is an error).
- Per-pane driver hints flow via `cmd_opt:` (a map, mirroring the top-level
  `driver_opt:` but scoped to one pane). Drivers pick out keys they understand
  (e.g. tmux pane title) and ignore the rest. This is the post-snapshot
  replacement for per-pane env injection (which was retired).

Go shape (post-skeleton: `MuxSpec` lives in `pkg/muxctl` and is generic /
driver-agnostic — leaves carry already-resolved argv plus a per-pane
driver-options bag, NOT cmdman concepts):

```go
package muxctl

type MuxSpec struct {
	Driver    string            // "tmux" | "zellij" | "" (autodetect)
	DriverOpt map[string]string // driver-specific opts (YAML: driver_opt)
	Layouts   []Layout          // switchable layouts; one window per invocation
}

type Layout struct {
	Name string   // user-facing switch handle; unique within MuxSpec.Layouts
	Root PaneSpec // container or single leaf
}

// Container holds the fields that make a PaneSpec a container node.
type Container struct {
	Dir    Direction
	Splits []Size // index-parallel to Panes
	Panes  []PaneSpec
}

// Leaf holds the fields that make a PaneSpec a leaf node.
type Leaf struct {
	Name   string            // pane name; map key in Session.ApplyLayout's return
	Cmd    []string          // argv to spawn (already resolved)
	CmdOpt map[string]string // per-pane driver-specific opts (YAML: cmd_opt)
	Focus  bool
}

// PaneSpec is a layout-tree node: a Container XOR a Leaf. Both groups are
// embedded so each side can be constructed and described independently. The
// YAML wire form is unchanged (Container/Leaf are inlined). Distinct from
// the runtime muxctl.Pane (an interface for pane identity returned by
// Session.ApplyLayout).
type PaneSpec struct {
	Container // inline; container fields
	Leaf      // inline; leaf fields
}

type Size struct {
	N   int  // N>0; for Percent N is in 1..100
	Absolute bool // absolute cells (Nc)
	Percent bool // percent of parent dim (N%); mutually exclusive with Absolute
	// Both false => proportional weight
}
```

The cmdman-facing YAML (the example above, with bare-string named leaves
like `api`, and a `mode: attach|logs` keyword) is the **higher layer's UX
surface**, not muxctl's wire form. The cmdman layer (a future
`pkg/cmdman/...` package) parses that YAML, resolves named leaves to argv
(`./api` → `cmdman attach <id>` or `cmdman logs --sticky <id>`), and emits
a `muxctl.MuxSpec` with resolved `Cmd []string`.

## CLI input

- `cmdman mux` reads a standalone layout spec as a **file path or on stdin**
  (`-` for stdin); the nested tree is never encoded as flags.
- `cmdman compose mux` reads the **`mux:` section embedded in the compose file**
  (resolved via the usual compose `-f`/discovery). No separate layout flag and no
  `-f` overload — the compose file carries both the services and their `mux:`
  layout.

The earlier stdin-vs-tty worry is moot: mux drives the multiplexer through its
CLI (`tmux`/`zellij`/…) and hosts no interactive terminal of its own, so
consuming stdin for the spec costs nothing. (Standalone `cmdman mux` takes the
layout path as a positional arg; `-`/omitted ⇒ stdin.)

## Driver selection

`mux.driver`: `tmux` | `zellij` | empty.

- Empty → autodetect: `$TMUX` set → `tmux`; else `$ZELLIJ` set → `zellij`.
- **Resolved (was hole 1):** neither set → default `tmux` (the only backend);
  never a hard error on the plain-shell case.
- **Resolved (was hole 2):** autodetect only *picks the driver*. Whether to drive
  the current server, attach an existing session, or create a new one on a
  dedicated socket/server is **driver config** passed to `pkg/muxctl/tmux.New`
  (the "session specifier"). Default: current server if inside (`$TMUX`), else the
  default tmux socket.
- **Resolved (was hole 3):** both set → `$TMUX` wins.
- v1 implements only the tmux driver. `driver: zellij` (or a future `wezterm`)
  must fail fast with "not implemented yet" — no `pkg/muxctl/zellij` or
  `.../wezterm` backend exists yet. Autodetect may still *select* `zellij` from
  `$ZELLIJ`; that then errors cleanly until the backend lands.

## Public API shape — new package `pkg/muxctl` (decided)

The existing `pkg/mux` tree (`Split`/`SendKeys`/`Capture`) is **superseded —
ignore it.** `pkg/muxctl` defines a small, window-controlling controller:

```go
package muxctl

// Session controls the cmdman-owned window in one multiplexer session.
// Minimal by design: a single window, switchable layouts. Session reuse,
// socket choice, dedicated-server isolation, and the window name belong
// to each driver's constructor (e.g. tmux.New), not to this interface.
type Session interface {
	// ApplyLayout (re)builds the controlled window's pane tree to match
	// root. It RESETS the window's panes — switching among MuxSpec.Layouts
	// is repeated ApplyLayout calls. Returns panes keyed by PaneSpec.Name.
	ApplyLayout(ctx context.Context, root PaneSpec) (map[string]Pane, error)
	// Close closes the controlled window. MUST NOT stop the panes' processes.
	Close(ctx context.Context) error
}

// Pane is the runtime pane identity returned by ApplyLayout.
type Pane interface {
	PaneId() string
	Name() string // matches PaneSpec.Name and the map key from ApplyLayout
}
```

Decided properties:

- **Single window, two methods.** One command invocation owns one window;
  the user switches among `MuxSpec.Layouts` by repeated `ApplyLayout`. No
  incremental `Split`/`SendKeys`, no per-window create/close on the
  interface (the driver constructor takes the window name and finds-or-creates;
  `Close` tears it down).
- **`ApplyLayout` resets.** Re-running `cmdman mux` or switching layouts
  re-applies the pane tree; the window reconciles by teardown+rebuild
  (kills+respawns the panes' `cmdman attach` — fine, mux is only a viewer).
- **`map[string]Pane` keyed by pane name.** Order-independent correlation.
  In the cmdman layer above, the pane name is typically the command name
  (and the cmdman layer rejects two panes referencing the same command —
  the muxctl layer rejects two panes with the same Name in a layout).
- **`MuxSpec` lives in `pkg/muxctl`.** It is the generic, driver-agnostic
  wire form: leaves carry resolved `Cmd []string` and a `CmdOpt` per-pane
  driver-opts bag. Higher layers parse their own user-facing YAML and emit
  a `muxctl.MuxSpec` after leaf-name → argv resolution.
- **Targeting + socket/server are driver-specific, NOT on the interface.**
  `pkg/muxctl/tmux.New(...)` takes tmux-specific config (the "session specifier")
  that decides: attach an existing session vs create a new one, and dedicated
  socket/server vs the current one. The generic autodetect (`$TMUX`/`$ZELLIJ`)
  only picks the driver; the chosen driver's `New` resolves the session.

**Leaf resolver lives ABOVE muxctl.** A `muxctl.PaneSpec` leaf carries pure
argv plus `CmdOpt` driver hints; it knows nothing about cmdman. The
`cmdman mux` / `compose mux` layer resolves a leaf name → argv via a resolver
(`func(ctx, name) (argv []string, err error)`; cmdman service for `mux`,
`ProjectSelection` for `compose mux`), builds a `muxctl.MuxSpec` with those
argv, and calls `ApplyLayout()` once per switch. One cmdman MuxSpec → one
builder → both commands.

## tmux realization notes (the tmux `muxctl` driver)

Fresh implementation behind `muxctl.Session`; the old `pkg/mux/tmux` is not reused
(salvage ideas only — key interpolation, and the rebalance-hook lesson below).

- Container `dir:h` → `split-window -h`; `dir:v` → `split-window -v`. Build the
  tree by recursive splits in pane order; pass each leaf's argv as the pane
  command so the pane *is* the attach process.
- Sizes: `-l <cells>` (tmux ≥3.1 also accepts `-l N%`, but we always pass
  resolved cells — `N%` is converted to cells against the live parent
  dimension at apply time, so the on-the-wire form is identical to
  absolutes). Absolute `Nc` → `-l N`; percent `N%` → `-l floor(parent*N/100)`;
  proportional weights → compute cell sizes from leftover after absolutes and
  percents.
- No per-pane env injection (retired). Identity/config goes via argv flags:
  `os.Executable()` + propagate `--data-dir` / `--runtime-dir` into the pane's
  `cmdman attach`/`logs` command.
- Drop / rework the `select-layout … tiled` rebalance hook so custom layouts
  survive client attach (proportional scaling, or re-realize). The hook exists
  because splits on a detached 80×24 session distort on first real attach.
- Terminal too small for the layout (tmux refuses a split): best-effort — build
  what fits, skip the panes that don't, and warn on stderr listing them.

## Cross-driver feasibility (zellij / wezterm)

**v1 ships the tmux driver only; zellij and wezterm are future TODOs.** The zellij
check below (verified against its docs) confirms the `muxctl` interface is
portable, which de-risks adding those drivers later — zellij maps cleanly onto
`muxctl.Session`:

| muxctl op (post-skeleton) | tmux | zellij |
| --- | --- | --- |
| driver constructor: find-or-create the cmdman window | `new-window -n cmdman -P -F '#{window_id}'` | `action new-tab --name cmdman` (prints tab id) |
| run a leaf | `split-window -- argv` | `run -- argv` / `new-pane --name <cmd> -- argv` (prints pane id) |
| `ApplyLayout` builds from PaneSpec | recursive `split-window` | `action new-tab --layout x.kdl` (we emit KDL) |
| `ApplyLayout` returns panes | `list-panes -F` | `action list-panes --json` |
| target a pane/window | `-t %id` / `-t @id` | `--pane-id terminal_N` / `--tab-id N` |
| `Close` (tear down the cmdman window) | `kill-window -t` | `action close-tab --tab-id N` |
| focus/find by name | window names | `action go-to-tab-name --create` |

**The one real gap:** zellij has no equivalent of tmux `@`-user-options (arbitrary
per-pane/tab metadata). So **ownership + correlation must key on NAMES, not
tmux-specific metadata** — which the design already does: pane name = command name
(backs `Pane.Name()` and the `map[string]Pane` key), tab/window name = the spec
window key. tmux `@`-options are an optional tmux-only refinement, not required.

**Portable, never-savage ownership = a dedicated cmdman session.** Because zellij
can't tag foreign objects, the clean cross-driver "we own this" is "it's our
session": run on a dedicated session (tmux `-L cmdman` / a `cmdman` session;
`zellij --session cmdman`). Everything in it is ours, so reset/reconcile is never
savage and behaves identically on both. **mux owns whole windows/tabs — it never
injects a pane into a user's window** (that would be tmux-leaning, fragile, and
savage-prone).

Re-run = **reset & rebuild the owned window** (`Layout`'s defined semantic): kill
its panes down to one, re-split per the spec, keep the window in its place. Pane
*reuse* (rearranging surviving panes into a new geometry) is **deliberately not
attempted** — it would need `swap-pane`/`join-pane`/`break-pane` + a positional
`select-layout` layout-string + resize fixups (fragile on tmux, worse on zellij)
for little gain: attach replays scrollback and is sticky, so a rebuilt pane just
re-fills. Optional, non-v1 sweetener: skip the rebuild when the window's current
command set already matches the spec (a `list-panes`/`list-tabs` set comparison,
not geometry surgery) to avoid flicker on a no-op re-run.

**Resolved:** default to **driving the user's current server/session when inside
one** (`$TMUX`/`$ZELLIJ`), else a session on the default server; a dedicated
`-L cmdman` / `zellij --session cmdman` is the opt-in for isolation. This is safe
*because ownership is whole-window/tab by name*: re-run resets only our named
window, never the user's. It works in-place on zellij too — name the tab, find it
via `list-tabs --json`, reset/close by `--tab-id`. zellij's missing per-pane
metadata doesn't bite, since we own at window/tab level, not per pane.

## Lifecycle (proposed, follows the guiding principle)

- `cmdman mux` snapshots the selected command set and **builds the session,
  windows and panes by issuing multiplexer CLI commands**; it does not host a
  foreground attach. Entry: when run inside a session (`$TMUX`/`$ZELLIJ`) it adds
  the windows **without stealing focus**; otherwise it builds detached and **prints
  the attach hint** (`tmux attach -t cmdman`). Closing, detaching, or killing the
  session never stops a command.
- Re-running `cmdman mux` reuses the existing session if present (the tmux
  backend already supports reuse-or-attach).
- **Default chosen:** `pkg/muxctl/tmux.New` drives the current server when inside
  one (`$TMUX`), else the default tmux socket (mux windows show up in the user's
  `tmux ls`). A dedicated `-L cmdman` socket remains an option, not the default.
  Session naming (`cmdman` / `cmdman-<project>`) still TBD.

## Compose integration

`cmdman compose mux` is **confirmed**. It shares the compose flags (`-f/--file`,
`-p/--project-name`, `--workdir`) for project resolution and reads the **`mux:`
section of the compose file** — no separate layout flag. Leaf names resolve to
compose service names through `ProjectSelection` + `filterByCommandNames` →
backing command ID. DAG order can drive default pane/window order when the
`mux:` section leaves it unspecified (or is absent).

Still open:

- `cmdman compose up --attach` is **not added for now** (the dashboard stays the
  separate `compose mux` step; `compose up` remains detached). May add later.
- What `compose mux` does when the compose file has **no `mux:` section** — error,
  or synthesize a default layout from the project's services (ties into the
  "default layout" question).

**Naming collision still noted:** `dir` already means *working directory* in the
compose command model. If a layout is embedded in the compose file, `dir` (split
direction) and `dir` (workdir) read confusingly even though they sit in different
subtrees. `split:`/`flow:` were offered as unambiguous alternatives; user kept
`dir`.

## Relevant existing architecture (context for whoever resumes)

- **`pkg/mux`** (SUPERSEDED by `pkg/muxctl`): `Session`/`Window`/`Pane` interfaces;
  `pkg/mux/tmux` is a complete backend (new/attach session, NewWindow, `Split(n)`
  + tiled rebalance hook, `SendKeys` with `#{SESSION_ID|WINDOW_ID|PANE_ID|
  INJECT_META}` interpolation, `Capture`, `Close`). Lifted from "crabswarm"
  (hence `CRAB_*`). **No non-test importer** — mux-00 does not reuse it; only the
  rebalance-hook lesson carries over (send-keys, key interpolation, and
  `INJECT_META` are all unused now — panes spawn argv directly).
- **`cmdman attach`** (`cmd/.../attach.go` → `cli.Attach` over
  `cmdman.Session`): single-consumer gRPC bidi `Attach` stream (stdout down;
  stdin/resize/signal up), raw mode, sig-proxy, 3×SIGINT force-exit, detach keys
  `ctrl-p,ctrl-q`. A monitor daemon per command serves it.
- **compose**: flat commands + reserved labels (`cmdman.compose.project`,
  `.command`, `.workdir`, …); `compose attach` resolves one service → ID →
  `Service.OpenAttachSession`. `compose up` is **detached**.
- **Root persistent flags**: `--data-dir`, `--runtime-dir` (must be threaded into
  pane processes). Common compose flags: `-f/--file`, `-p/--project-name`,
  `--workdir`.
- **Placement rules** (AGENTS.md): `cmd/` thin (flags → service); presentation in
  `pkg/cmdman/cli`; logic in `pkg/cmdman/<feature>`. So likely: layout spec +
  builder in a new package; cobra wiring in `cmd/cmdman/commands/mux*.go`; reuse
  `cli.Attach` for any foreground attach.

## Attach & logs stickiness (`r` restart, `--auto-exit`, `logs --sticky`)

**Default attach behavior changes (general `cmdman attach`, not mux-only).** Attach
is interactive by nature, so it should not silently vanish:

- Attaching to a **not-running** command (created, or exited) no longer
  errors/exits. It prints the state (`not running` / `created` / `exited (code N)`)
  and prompts **press `r` to restart & attach**, then waits.
- When an attached command **exits**, attach stays, prints `exited (code N)`, and
  shows the same `r` prompt. If the command restarts on its own (restart policy),
  attach re-attaches automatically.
- **`r`** (in the waiting/exited state only) → routes through cmdman's existing
  `restart` (no retry-budget special-casing) and attaches to the new instance.
  **`ctrl-p,ctrl-q`** → detach/exit (closes the pane in mux). While the command
  runs, keystrokes go to the command; `r` is special only in the waiting state.
- **`--auto-exit`** opts back into the old behavior: exit immediately when the
  command exits or isn't running (no prompt, no wait). It is the inverse of the
  earlier `--stay-attached` idea, now folded into the default.

**Consequence for mux:** panes just run `cmdman attach <id>` — the sticky default
already gives stay-open-on-exit + the restart prompt, with no extra flag. The pane
is spawned directly as the attach process (`tmux split-window -- cmdman attach
<id>`); no login shell / send-keys, so it keeps the rc-file/quoting/identity
problems out. Because attach no longer exits on command-exit, tmux never reaps the
pane unless the user hits the detach-keys.

**Detach keys kept:** inside a pane `ctrl-p,ctrl-q` exits that pane's attach
(closing it). tmux's `Ctrl-b` owns navigation and `Ctrl-b d` leaves the whole
dashboard. `ctrl-c` / `SIGWINCH` route to the focused pane's attach.

**Implementation notes.** `cli.Attach`'s default path grows the not-running and
post-exit states: render the status line, read stdin for `r` / detach-keys (reuse
`term.NewEscapeProxy`), and on `r` call cmdman's existing `restart` for the id,
then reattach. Needs the exit code (monitor exit history / stream close) and
restart detection for auto-reattach (subscribe to the command's `eventlog`).
`--auto-exit` short-circuits to today's return-on-EOF. tmux `remain-on-exit` is
not used (crude, non-portable).

**Logs panes: a parallel `cmdman logs --sticky`.** `cmdman logs` gains `--sticky`:
follow the log and, when the command exits, print a meta line prefixed `#|`
(configurable via `--meta-prefix`; e.g. `#| 12:01:03 exited (code 1)`) — the marker
lets users distinguish injected meta from real log lines — then wait for the next
start and resume redirecting.
Read-only: no `r`/restart, just a passive wait. mux uses `--sticky` for `mode:
logs` panes by default. Shares the exit-code + restart-detection (`eventlog`)
machinery with sticky attach.

## Open questions (next session)

1. (Decided, revised post-snapshot) `pkg/muxctl`: `Session` = `ApplyLayout` /
   `Close` (single cmdman-owned window per invocation; switching among
   `MuxSpec.Layouts` is repeated `ApplyLayout`); returns
   `map[pane-name]Pane` (`Pane`: `PaneId`/`Name`); duplicate pane name in a
   layout is rejected; targeting + socket + window name are `tmux.New` config.
   `MuxSpec` lives in `pkg/muxctl` (generic — `Cmd []string` + `CmdOpt`).
2. (Decided) Autodetect: none set → `tmux`; both set → `$TMUX` wins.
3. (Decided) "From current session" is `tmux.New` config (drive current server /
   existing / new); only its default value is TBD (see Q4).
4. (Decided) Default: drive the current server/session when inside
   (`$TMUX`/`$ZELLIJ`), else a session named `cmdman` on the default server;
   dedicated `-L cmdman` / `zellij --session cmdman` is opt-in. Safe via
   whole-window/tab ownership by name. Window `name` required.
5. (Decided) Compose surface: `compose mux` reads the compose file's `mux:`
   section; a missing `mux:` section is an error; **no `compose up --attach` for
   now**.
6. (Decided) Binding: unknown leaf name = hard error; a running command with no
   leaf = silently omitted (the layout is the explicit truth). There is no
   "layout-less" invocation — standalone requires a positional spec, `compose mux`
   requires a `mux:` section.
7. (Decided) No synthesized default layout: `compose mux` errors on a missing
   `mux:` section and standalone `mux` requires a positional spec, so there is no
   "no layout" path.
8. (Decided, revised post-snapshot) Naming: cmdman-YAML leaf-options key is
   `command:` (resolves to `muxctl.PaneSpec.Cmd`); layout `name` required &
   unique (top-level key is `layouts:`, not `windows:` — one window per
   invocation, switchable among layouts); per-pane driver hints via
   `cmd_opt:` map (mirror of `driver_opt:`); `%` sizes now supported
   (resolved against parent dim, reserved alongside absolutes); keep `dir`.
9. (Confirmed) `cmdman attach` replays scrollback — the monitor sends
   `sub.Scrollback` to a new client before live streaming (`mon_server.go` Attach,
   lines ~61-65). Freshly-opened mux panes show recent history for free.
10. (Decided) Detach UX: pane keeps `ctrl-p,ctrl-q` as "close pane"; tmux `Ctrl-b`
    owns navigation; stay-attached governs exit/reattach. See Stay-attached mode.
11. (Decided) Standalone `cmdman mux` takes the layout path as a **positional
    arg**; stdin via `-`/omitted. Compose mux reads the embedded `mux:` section.
12. (Decided) Outside a mux: build detached + print the attach hint (`tmux attach
    -t cmdman`); pure controller, no tty. Inside: build without stealing focus.
    Session targeting/socket comes from `mux.driver_opt` → the driver constructor
    (`tmux.New`); isolation is opted into via `driver_opt`, not a CLI flag.
13. Attach/logs stickiness internals (impl): `r` routes through cmdman's existing
    `restart` (decided). Still to wire: exit-code source (monitor exit history vs
    stream close) and auto-reattach / resume on restart (subscribe `eventlog`),
    shared by sticky attach and `logs --sticky`. `--auto-exit` short-circuits to
    return-on-EOF.

## Where we left off

Design is essentially complete. Pinned (post-skeleton): the cmdman-facing
layout DSL (`command:` leaf = Id|NAME, per-pane `mode: attach|logs` with
attach default, `layouts:` top-level — single window per invocation,
switchable among named layouts — each layout `name` required, pane titles =
command name via `cmd_opt`, focus = first leaf unless one `focus: true`,
sizes: `Nc` absolute, `N%` percent (1..100), bare `N` weight — absolutes and
percents reserved first); `pkg/muxctl` (`Session` = `ApplyLayout` / `Close`,
`ApplyLayout` → `map[pane-name]Pane`, ownership whole-window/tab **by name**
(driver-constructor-set), **reset-and-rebuild** on each `ApplyLayout` — no
pane reuse; `MuxSpec` lives here generic with `Cmd []string` + `CmdOpt`);
**tmux driver only in v1**
(zellij + wezterm are TODO; interface verified portable against zellij); default =
drive the current server when inside (no focus-steal), else build detached + print
the attach hint (`tmux attach -t cmdman`); isolation via `mux.driver_opt`; attach
is **sticky by default** (`r` routes through existing `restart`;
`--auto-exit` opts out), and `cmdman logs` gains a parallel **`--sticky`**
(read-only, `#|`-prefixed meta lines; mux's `mode: logs` default); mux is
**view-only** (never starts commands); orphan running commands silently omitted,
duplicate/unknown leaves rejected; autodetect → tmux (`$TMUX` wins). `compose mux`
reads the file's `mux:` section (missing = error); no `compose up --attach` yet.
Per-pane env injection retired (flags only); `logs --sticky` meta prefix `#|`
(configurable via `--meta-prefix`); too-small terminal → best-effort + warn.

**Implemented so far:** `pkg/muxctl` spec types + validation + YAML decoder
(`MuxSpec`/`Layout`/`PaneSpec`/`Size`); the `pkg/muxctl/tmux` driver
(`Config`{Path,Socket,SessionName,WindowName}, `New` find-or-create window,
`ApplyLayout` reset-then-recursive-`split-window -b -l Ncells` with absolutes-
reserved-first cell math, `respawn-pane -k -- argv` at leaves, `select-pane -T`
for `CmdOpt["title"]`/name pane titles, `select-pane` for focus, `Close`
kills only the owned window; too-small-leftover children are skipped and
the dropped names go to `contextkey.ValueSlogLoggerDefault(ctx).WarnContext`).
Covered by unit tests for cell math + focus pick and tmux-required
integration tests for New / ApplyLayout (single, h-split, nested mixed,
reset-on-reapply, focus, title override, Close-doesn't-touch-siblings,
too-small-warns-via-logger).

Implementation chunks remaining: (a) attach/logs stickiness internals
(exit code, auto-reattach via `eventlog`; `r`→`restart`), and (b) the
cmdman-layer YAML parser + leaf resolver above muxctl (cmdman-YAML
`command:`/`mode:` → `muxctl.PaneSpec.Cmd` argv via the cmdman / compose
services), plus the `cmdman mux` / `cmdman compose mux` cobra wiring.
