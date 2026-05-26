# cmdman mux plan (mux-00)

This is a **discussion snapshot**, not an implementation commitment. It was
captured mid-interview while finding the UX and the public API shape. The
multiplexer layout DSL has mostly converged; the `pkg/mux` public API shape is
still open. Resume from "Where we left off" at the bottom.

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

## Where the design currently stands

### Decided / converged

- **Pane content = interactive `attach` by default**, per-pane overridable to
  read-only `logs -f` via `mode:`. (User chose attach-everywhere; `main-*`-style
  layouts and per-pane sizing keep it readable. Read-only remains available as a
  per-pane mode, not the default.)
- **Layout is a declarative, nestable tree** (zellij/i3 family), authored in
  YAML in the house style, rooted under a single `mux:` key.
- **`mux:` is the spec root** so the whole thing can either live in its own file
  or be embedded into the compose file later as one `mux:` section, with no
  restructuring.
- **Realize each leaf by spawning the attach process directly in the pane**
  (tmux `split-window … -- <argv>`), NOT by spawning a login shell and
  `send-keys`-ing a command line. This avoids rc-file latency, prompt spam,
  alias shadowing, and shell-quoting/injection on command names, and lets a dead
  command leave a closed/idle pane rather than a live shell.
- **`CRAB_*` must be renamed `CMDMAN_*`.** The `#{INJECT_META}` shell-export hack
  (a leftover from the donor "crabswarm" project) is replaced by passing env on
  the pane spawn (tmux `-e CMDMAN_COMMAND=… -e CMDMAN_DATA_DIR=…`, tmux ≥3.0).
- **Binary identity:** panes must invoke `os.Executable()` and inherit the root
  persistent flags (`--data-dir`, `--runtime-dir`), never bare `cmdman` from
  `$PATH`.
- **One shared Go struct** (`MuxSpec`) backs both `cmdman mux` and `cmdman
  compose mux`. The only per-command difference is leaf-name resolution: plain
  `mux` resolves to a cmdman command (name/ID); `compose mux` resolves to a
  compose service name.
- **The spec is supplied as a file path or via stdin** to both commands. The
  nested tree has no efficient CLI-flag form, so there is no attempt to express
  layout via flags. The `panes` list **is** the selection (the commands that get
  panes); no separate selection flags in the explicit case.
- **`cmdman compose mux` is a confirmed subcommand** (not just a candidate). It
  reuses the compose flags for project resolution and `resolveCommandID` /
  `filterByCommandNames` to map a leaf's service name → backing command ID.

### Open (the part still "up")

- The `pkg/mux` public API shape (imperative vs declarative layout). See
  "Public API shape" below.
- Driver autodetect fallback, "from current session" meaning, multiplexer
  session lifecycle/socket, layout input flag + stdin/tty handling, default
  layout when none is authored. See "Open questions."

## Layout spec (UX surface — mostly settled)

Single root `mux:` key. Geometry (`dir` + `splits`) is separated from content
(`panes`).

```yaml
mux:
  driver: tmux            # tmux | zellij | "" (autodetect; see below)
  windows:
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

Grammar:

- A **pane** is either a **leaf** or a **container**.
  - Leaf: a bare string (the command/service name), or a map when options are
    needed: `{ cmd: api, mode: logs, focus: true }`. (`cmd` as the leaf-options
    key is proposed, not confirmed.)
  - Container: `{ dir, splits, panes }`.
- **`dir`**: `h` (panes side by side) or `v` (panes stacked). Single-letter, tmux
  convention. (Note the tmux/zellij terminology inversion: tmux `-h` = zellij
  "vertical" = side by side. We use tmux's `h`/`v` because tmux is primary.)
- **`splits`**: sizing array, **index-parallel to `panes`** (`len(splits) ==
  len(panes)`). `splits[i]` sizes `panes[i]`.
- **Size grammar**:
  - `Nc` — absolute, `N` character cells (`c` = character). Columns when `dir:h`,
    rows when `dir:v`.
  - bare `N` — proportional weight. Absolutes are reserved first; the leftover
    space is divided among the bare weights by ratio (e.g. `[90c, 1, 2]` → first
    pane 90 cells, the rest split 1:2).
  - `%` — NOT included for now (proposed; open).
- Only the window **root** pane is unsized (fills the window). Every nested
  pane's size comes from its parent's `splits[i]`.

Conceptual Go shape (lives in its own package; note `Pane` name collides with
the runtime `mux.Pane` interface, so the spec type belongs outside `pkg/mux` —
e.g. `pkg/cmdman/mux` or `pkg/muxlayout`):

```go
type MuxSpec struct {
	Driver  string // "tmux" | "zellij" | "" (autodetect)
	Windows []Window
}

type Window struct {
	Name string
	Root Pane // a container or a single leaf
}

// Pane is a container (Dir+Splits+Panes) XOR a leaf (Cmd).
type Pane struct {
	// container
	Dir    string // "h" | "v"
	Splits []Size // index-parallel to Panes
	Panes  []Pane

	// leaf
	Cmd   string // referenced command/service name
	Mode  string // "attach" (default) | "logs"
	Focus bool
}

type Size struct {
	N   int
	Abs bool // true => absolute cells (Nc); false => proportional weight
}
```

## CLI input (file or stdin)

Both `cmdman mux` and `cmdman compose mux` take the spec as a file path or on
stdin; neither encodes the nested tree as flags. Two tensions to resolve:

- **`-f` is already the compose file.** `compose mux` inherits `-f/--file` =
  compose file from the shared compose flags, so `-f` cannot also mean "layout
  file." Proposed: a dedicated `--layout <path>` (with `-` or omitted ⇒ stdin) on
  both commands, so `-f` keeps one meaning and the layout input is uniform across
  the two. (Open alternatives: read layout only from stdin for `compose mux`; or
  let `compose mux` read an embedded `mux:` section from the compose file — see
  Compose integration.)
- **stdin-for-spec vs tty-for-attach.** If the spec arrives on stdin (a pipe),
  stdin is not the controlling terminal, so a foreground `tmux attach` fails
  ("not a terminal"). Either re-open `/dev/tty` for the foreground attach, or
  default to the detached + print-hint lifecycle when stdin is a pipe. Ties
  directly into the session-lifecycle question.

## Driver selection

`mux.driver`: `tmux` | `zellij` | empty.

- Empty → autodetect: `$TMUX` set → `tmux`; else `$ZELLIJ` set → `zellij`.
- **Open hole 1:** neither env var set is the *common* case (you usually run
  `cmdman mux` from a plain shell to *build* a dashboard). Proposed fallback:
  default `tmux` (also the only implemented backend), not a hard error.
- **Open hole 2:** "from current session" is ambiguous between *picking the
  backend* vs *injecting windows into the session you are already in*. Proposed:
  pick-driver-only + create a dedicated cmdman session (safe); injection into the
  user's current session is slick but collides with their window names. Unconfirmed.
- **Open hole 3:** both env vars set → precedence (`$TMUX` wins?). Unconfirmed.
- `driver: zellij` must fail fast with "not implemented yet" — there is no zellij
  backend (only `pkg/mux/tmux` exists).

## Public API shape (STILL OPEN — the main thing to resolve next)

`pkg/mux` today exposes `Session` / `Window` / `Pane` interfaces and a complete
tmux backend, but **nothing imports it**, and `Window.Split(n)` only fans out N
equal panes with a rebalance hook hardcoded to `select-layout … tiled`. A
sized/nested tree cannot be expressed by `Split(n)`, and the `tiled` hook would
*flatten a custom layout on every client attach*. Two candidate shapes:

- **A. Imperative.** Add `Window.SplitAt(ctx, target Pane, dir Direction, size
  Size, argv []string) (Pane, error)`; a builder walks the tree. Maps directly
  to tmux `split-window -h/-v -l <size> -- <argv>`.
- **B. Declarative (leaning).** Make the layout tree a first-class `mux.Layout`
  type and add `Session.NewWindowWithLayout(ctx, name string, l Layout)`. Each
  backend realizes it natively (tmux: recursive `split-window`; zellij: generated
  KDL via `zellij action new-tab --layout`). Matches how zellij actually works
  (layout-file-driven, not imperative-split-driven) and keeps the tree
  backend-agnostic.

Either way: the existing `Split(n)` + `tiled` rebalance hook becomes only the
**auto-generated default path** (no layout authored → synthesize a balanced
tree), and the `tiled` hook must be scoped so it never runs over a custom layout.

**Leaf resolver seam.** The layout/builder package stays agnostic to how a leaf
name becomes a process: it takes a resolver
`func(ctx, name) (argv []string, env map[string]string, err error)`. `cmdman mux`
injects a resolver backed by the cmdman service (name/ID → `cmdman attach <id>`);
`cmdman compose mux` injects one backed by `ProjectSelection` (service name →
backing command ID). This is the seam that lets one `MuxSpec` + one builder serve
both commands, and it reinforces option B.

## tmux realization notes

- Container `dir:h` → `split-window -h`; `dir:v` → `split-window -v`. Build the
  tree by recursive splits in pane order; pass each leaf's argv as the pane
  command so the pane *is* the attach process.
- Sizes: `-l <cells>` (tmux ≥3.1 also accepts `-l N%`). Absolute `Nc` → `-l N`;
  proportional weights → compute cell sizes from leftover after absolutes.
- Per-pane env: `split-window -e CMDMAN_COMMAND=<name> -e CMDMAN_DATA_DIR=… …`.
- Identity/flags: `os.Executable()` + propagate `--data-dir` / `--runtime-dir`.
- Drop / rework the `select-layout … tiled` rebalance hook so custom layouts
  survive client attach (proportional scaling, or re-realize). The hook exists
  because splits on a detached 80×24 session distort on first real attach.

## Lifecycle (proposed, follows the guiding principle)

- `cmdman mux` snapshots the selected command set, builds the session/windows,
  attaches in the foreground. Detach (e.g. tmux `Ctrl-b d`) leaves the daemon and
  all commands running; the session and its idle attaches persist for re-entry.
- Re-running `cmdman mux` reuses the existing session if present (the tmux
  backend already supports reuse-or-attach).
- **Open:** dedicated socket (`-L cmdman`, isolated) vs default socket (shows in
  the user's `tmux ls`) vs create-detached-and-print-hint. Session naming
  (`cmdman` / `cmdman-<project>`?). Behavior when already inside a multiplexer
  (nesting / switch-client) — tied to "from current session" above.

## Compose integration

`cmdman compose mux` is **confirmed**. It shares the compose flags (`-f/--file`,
`-p/--project-name`, `--workdir`) for project resolution and takes the layout
spec via `--layout`/stdin (see CLI input). Leaf names resolve to compose service
names through `ProjectSelection` + `filterByCommandNames` → backing command ID.
DAG order can drive default pane/window order when the spec leaves it
unspecified.

Still open:

- Whether `cmdman compose up --attach` also exists (bring everything up, then
  drop into the dashboard in one step; `compose up` is detached today).
- Whether `compose mux` may ALSO read an embedded `mux:` section from the compose
  file as an alternative to `--layout`/stdin, and the precedence if so
  (`--layout` > stdin > embedded `mux:` > …). The single-root `mux:` spec makes
  embedding clean, but the user emphasized external file/stdin input.

**Naming collision still noted:** `dir` already means *working directory* in the
compose command model. If a layout is embedded in the compose file, `dir` (split
direction) and `dir` (workdir) read confusingly even though they sit in different
subtrees. `split:`/`flow:` were offered as unambiguous alternatives; user kept
`dir`.

## Relevant existing architecture (context for whoever resumes)

- **`pkg/mux`**: `Session`/`Window`/`Pane` interfaces; `pkg/mux/tmux` is a
  complete backend (new/attach session, NewWindow, `Split(n)` + tiled rebalance
  hook, `SendKeys` with `#{SESSION_ID|WINDOW_ID|PANE_ID|INJECT_META}`
  interpolation, `Capture`, `Close`). Lifted from "crabswarm" (hence `CRAB_*`).
  **No non-test importer outside `pkg/mux`** — `mux` is greenfield wiring on
  finished plumbing.
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

## Two-layer detach UX (needs a decision, not yet discussed in depth)

Inside a mux pane there are two detach mechanisms: the multiplexer's own (tmux
`Ctrl-b d` leaves the whole dashboard) and `cmdman attach`'s detach keys
(`ctrl-p,ctrl-q` drops one pane's attach back to… nothing, since the pane IS the
attach process). Also `ctrl-c`/SIGWINCH routing across N attaches. Decide whether
mux panes disable cmdman attach detach-keys (multiplexer owns navigation) and how
a pane behaves when its attach exits (command died vs user detached).

## Open questions (next session)

1. **Public API shape: A (imperative `SplitAt`) or B (declarative `Layout` +
   per-backend realize)?** (Leaning B.)
2. Driver autodetect fallback when neither `$TMUX` nor `$ZELLIJ`: default `tmux`?
3. "From current session" = pick backend only (dedicated session) or inject into
   the user's current session?
4. Multiplexer session: dedicated `-L cmdman` socket + foreground attach, default
   socket, or detached + print hint? Session naming? Behavior when already inside
   a multiplexer?
5. Compose surface: `compose mux` is confirmed — does `compose up --attach` also
   exist? May `compose mux` read an embedded `mux:` section from the compose
   file, and with what precedence vs `--layout`/stdin?
6. The `panes` list is the selection for the explicit case. Remaining: how does a
   layout-less invocation pick commands (all running? by label?), and binding
   edges — unknown leaf name (error?), a running command with no leaf (silently
   omit, or warn/spillover window?).
7. No layout authored → synthesized default (tiled / main-vertical /
   window-per-command)?
8. Naming nits: leaf-options key `cmd:` vs `command:`; `%` sizes; `windows` vs
   `tabs`; keep `dir` despite the compose `dir`=workdir collision.
9. Does `cmdman attach` replay the ring buffer (pane shows scrollback) or only
   live tail? (`broadcaster` + `ringBuffer` exist — confirm subscribe-time
   replay.)
10. Two-layer detach/signal UX (see above).
11. Layout input mechanics: dedicated `--layout <path>` with `-`/omitted ⇒ stdin
    (vs overloading `-f`)? Confirm the stdin sentinel.
12. stdin (spec) vs controlling tty (foreground attach): re-open `/dev/tty`, or
    default to detached + print-hint when stdin is a pipe?

## Where we left off

UX/layout DSL is largely settled (the `mux:` + `windows`/`dir`/`splits`/`panes`
shape above). The **public API shape of `pkg/mux` is the open item** the user
flagged — resolve question 1 first, since it dictates how the layout tree is
realized and how much of the existing `Split(n)`/tiled machinery is reused vs
replaced. Then walk questions 2–12.
