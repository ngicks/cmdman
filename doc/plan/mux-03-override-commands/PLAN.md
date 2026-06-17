# mux-03 — Override in-pane signal behavior (signal → action map)

Status: draft, not yet implemented.
Follows: mux-01 (window identity stamp; `mux up` / `mux down` / `mux ls`),
mux-02 (per-pane `@cmdman_leaf` stamp; `cycle-scale`).

## Problem

The in-pane viewers a dashboard spawns (`cmdman attach <id>` and
`cmdman logs --sticky <id>`, built by `pkg/cmdman/mux/build.go:paneArgv`) hand
all signal behavior to the OS defaults plus attach's sig-proxy. Two concrete
gaps fall out of that:

- **Ctrl-Z is a dead end in a pane.** Panes are spawned with
  `respawn-pane -k -- <argv>` (`pkg/muxctl/tmux/apply.go:217`), i.e. the viewer
  is the pane's foreground process with **no interactive job-control shell**.
  In the cooked `logs` viewer, Ctrl-Z raises `SIGTSTP`, the viewer stops, and
  there is no `fg` to resume it — the pane freezes. The user must `kill -CONT`
  from elsewhere or kill the pane.
- **No way to bind a pane key/signal to a dashboard action.** There is no
  mechanism to say "tear this dashboard down", "detach just this viewer", or
  "cycle the replica in this pane" from inside a pane without typing a full
  `cmdman mux …` command.

The keyboard can only natively raise three signals on Linux — `SIGINT`
(Ctrl-C), `SIGQUIT` (Ctrl-\\), `SIGTSTP` (Ctrl-Z) — and only in cooked mode
(`ISIG`). But a **delivered** signal (`kill(2)`) is caught regardless of
`ISIG`, so a single signal handler in the viewer behaves identically in the
raw `attach` viewer and the cooked `logs` viewer. That makes a custom signal
(e.g. `SIGUSR1`) a portable, driver-independent trigger: the viewer owns the
*action*, and any front-end — a `tmux`/`zellij`/`wezterm` keybinding that runs
`kill -USR1 <pane_pid>`, another `cmdman` command, or a script — owns the
*trigger*.

This plan adds a **signal → action map** to the mux spec and a matching
`--on-signal` CLI flag, so users can override what a delivered signal does in a
pane viewer. It is the portable back-end half; per-driver keybinding front-ends
(`display-menu`, wezterm `InputSelector`, zellij swap-layouts) are out of scope
here (see Non-goals).

## Design

### Model

A pane viewer holds a map `signal → action`. On a **delivered** signal in that
map, the viewer runs the action instead of its default behavior. Keying is on
delivery, not on the keystroke, so:

- The same handler fires in `attach` (raw, `ISIG` off) and `logs` (cooked).
  Raw mode only disables the tty's *keystroke → signal translation*; it does
  not block delivered signals.
- The keyboard is just one possible sender, and only for the ≤3 char-signals.
  The general front-end is `kill`, which any mux keybinding can issue against
  `#{pane_pid}` (tmux), `wezterm cli list --format json`, or
  `zellij action list-panes --json`.

A signal **absent** from the map keeps today's behavior (attach: forward via
sig-proxy; logs: OS default). The map only changes the signals it lists.

### Actions

| Action keyword     | Behavior in the viewer                                                                 |
| ------------------ | -------------------------------------------------------------------------------------- |
| `forward`          | Forward the signal to the supervised command (attach sig-proxy). No-op for `logs`.     |
| `ignore`           | Catch and swallow — the signal's default action (e.g. suspend) does not happen.        |
| `mux-down`         | Tear down *this pane's* dashboard window via `mux.Down` (resolve identity, see below). |
| `detach`           | Detach/exit just this viewer (attach: as if detach-keys; logs: exit). Pane survives.   |
| `exec: <command>`  | Run `<command>` via the user's shell with injected env (below), then exit the viewer.  |

`exec` is the "override to a command" escape hatch the title names: richer
actions cmdman does not special-case (cycle layout, `cycle-scale`, pop a
picker) are expressed as `exec: cmdman compose mux <project> cycle-scale web`,
etc. The viewer does not need the spec for these — the command re-enters cmdman
with the context it needs from env.

`exec` environment (set on the spawned command, in addition to the inherited
pane env which already carries `--data-dir`/`--runtime-dir` resolvability):

- `CMDMAN_MUX_IDENTITY` — the window's `@cmdman_window` value
- `CMDMAN_MUX_SESSION`  — the resolved session name
- `CMDMAN_MUX_LEAF`     — this pane's `@cmdman_leaf` (the command, when stamped)
- `CMDMAN_MUX_PANE`     — `$TMUX_PANE`
- `CMDMAN_BIN`          — `PaneArgvOpts.Executable`

### Config schema change

New optional `signals:` map at two scopes in the `mux:` section. Per-leaf
entries are merged **over** the spec-level map, per signal key (a leaf may set
`SIGUSR1: detach` while the spec default is `SIGUSR1: mux-down`).

```yaml
mux:
  driver: tmux
  # Pane-wide default, applied to every attach/logs viewer in this dashboard.
  signals:
    SIGUSR1: mux-down          # kill -USR1 <pane> tears the whole dashboard down
    SIGTSTP: ignore            # Ctrl-Z in a cooked logs pane becomes a no-op
    SIGUSR2:
      exec: "cmdman compose mux ${CMDMAN_MUX_PROJECT} cycle-scale ${CMDMAN_MUX_LEAF}"
  layouts:
    - name: main
      root:
        dir: h
        panes:
          - command: web
            signals:
              SIGUSR1: detach  # this pane only: detach the viewer, keep the dashboard
          - command: api
            mode: logs
```

Schema additions (both fields optional; omitted = today's behavior):

| Location               | Field                            | Meaning                                  |
| ---------------------- | -------------------------------- | ---------------------------------------- |
| `mux:` (spec)          | `signals: map[string]SignalAction` | pane-wide default signal→action map      |
| `mux: … panes[*]` leaf | `signals: map[string]SignalAction` | per-leaf override, merged over the spec  |

A `SignalAction` value is written as either a bare keyword scalar
(`forward` / `ignore` / `mux-down` / `detach`) or a mapping `{exec: "<cmd>"}`.

### Public API change

`pkg/cmdman/mux/spec.go` — new type + two fields:

```go
// SignalActionKind enumerates what a pane viewer does on a delivered signal.
type SignalActionKind string

const (
	SignalForward SignalActionKind = "forward"
	SignalIgnore  SignalActionKind = "ignore"
	SignalMuxDown SignalActionKind = "mux-down"
	SignalDetach  SignalActionKind = "detach"
	SignalExec    SignalActionKind = "exec"
)

// SignalAction is the action bound to one signal. In YAML it is either a bare
// keyword scalar or a mapping {exec: "<command>"}; see UnmarshalYAML.
type SignalAction struct {
	Kind SignalActionKind
	Exec string // shell command; required iff Kind == SignalExec
}

func (a *SignalAction) UnmarshalYAML(node *yaml.Node) error // scalar keyword | {exec: ...}

type Spec struct {
	Driver    string                    `yaml:"driver,omitempty"`
	DriverOpt map[string]string         `yaml:"driver_opt,omitempty"`
	Signals   map[string]SignalAction   `yaml:"signals,omitempty"` // NEW: pane default
	Layouts   []Layout                  `yaml:"layouts"`
	Unknown   map[string]any            `yaml:",inline"`
}

type PaneSpec struct {
	// … existing container + leaf fields …
	Signals map[string]SignalAction `yaml:"signals,omitempty"` // NEW: per-leaf override
}

// ResolveSignals returns the spec-level map merged with a leaf's override
// (leaf wins per key). Exported so build.go and validation share one merge.
func (s Spec) ResolveSignals(leaf PaneSpec) map[string]SignalAction
```

`pkg/cmdman/cli/attach.go` — `cli` must stay free of the `mux` package, so the
action is injected as a handler map built by the command layer:

```go
type AttachOptions struct {
	// … existing fields …

	// SignalActions maps a delivered signal to a handler that runs instead of
	// the default forward behavior. Absent signal = today's behavior. A
	// handler returning a non-nil error ends the attach loop with that error;
	// return ErrViewerExit for a clean teardown/detach exit.
	SignalActions map[os.Signal]func(ctx context.Context) error
}

// ErrViewerExit is the sentinel a SignalActions handler returns to end the
// viewer cleanly (mux-down / detach / exec). Attach maps it to a nil return.
var ErrViewerExit = errors.New("attach: viewer exit requested")
```

`handleAttachSignals` (`attach.go:218`) gains a first branch: if
`opts.SignalActions[sig]` exists, call it and route `ErrViewerExit`/errors into
the existing `forceExitCh`/`errCh` machinery; otherwise fall through to today's
forward/force-exit logic. `forward`-keyword signals are simply **not** put in
the map (default already forwards), so the map only carries overrides.

`pkg/muxctl/tmux` — new helper for identity-from-inside-a-pane:

```go
// CurrentWindowIdentity reads @cmdman_window for the window the calling
// process's pane belongs to (show-options -wqv @cmdman_window). ok=false when
// unset (pane is not a cmdman dashboard). Used by the mux-down/exec handlers.
func CurrentWindowIdentity(ctx context.Context, opts ListOwnedWindowsOptions) (id string, ok bool, err error)
```

`pkg/cmdman/mux/build.go` — `paneArgv` stamps the merged map as repeatable
flags. For each `sig → action` in `Spec.ResolveSignals(leaf)`:

```
--on-signal SIGUSR1=mux-down
--on-signal 'SIGUSR2=exec:cmdman compose mux web cycle-scale api'
```

### CLI flag

`--on-signal SIG=ACTION` (repeatable; `pflag.StringArray`) on both `attachCmd`
(`cmd/cmdman/commands/attach.go`) and `logsCmd`
(`cmd/cmdman/commands/logs.go`). `SIG` is parsed with the existing
`hrstr.ParseSignal` (`pkg/hrstr/signal.go:13`); `ACTION` is a keyword or
`exec:<command>`. The standalone flag is the lowering target of the spec map —
mux panes get it stamped by `paneArgv`, and a user can also pass it by hand to
a bare `cmdman attach`/`logs`. Absent flag = today's behavior, so standalone
viewers keep normal Unix Ctrl-Z.

Command-layer wiring (`runAttach`, `runLogs`) parses the flags into
`map[os.Signal]func(ctx) error`:

- `forward` → omit (keep default).
- `ignore` → no-op handler returning `nil`.
- `mux-down` → resolve identity via `tmux.CurrentWindowIdentity`, call
  `mux.Down(ctx, mux.DownOptions{Identity: id})`, return `cli.ErrViewerExit`.
- `detach` → return `cli.ErrViewerExit` (attach already restores the terminal
  on exit; logs just stops following).
- `exec:<cmd>` → run `<cmd>` via shell with the injected env above, return
  `cli.ErrViewerExit`.

For the `logs` viewer (`runLogs`), install a `signal.Notify` goroutine over the
mapped signals around the `cli.RenderLogs(...)` follow loop and dispatch to the
same handler map; cancel the logs context on a handler that exits.

### Validation

In `Spec.Validate` (and surfaced for compose via `validateMux` /
`validateMuxPane`, `pkg/cmdman/compose/load.go:720`):

- Every `signals` key parses via `hrstr.ParseSignal`; unknown name → error.
- Reject `SIGKILL` (9) and `SIGSTOP` (19): uncatchable, so an override silently
  would not fire — error rather than mislead.
- `SignalExec` with empty `Exec` → error.
- Unknown `SignalActionKind` (a keyword that is not one of the five) → error.
- `SIGUSR1`/`SIGUSR2` are in attach's `forwardedSignals` (`cli/attach.go:46`):
  overriding them replaces forwarding *in mux panes only* (opt-in via the
  stamped flag), which is the intended trade for a dashboard. No error; noted
  in the man page.

### File map (what changes where)

| File                                      | Change                                                                 |
| ----------------------------------------- | ---------------------------------------------------------------------- |
| `pkg/cmdman/mux/spec.go`                  | `SignalAction(Kind)` types; `Spec.Signals`, `PaneSpec.Signals`; `ResolveSignals`; `UnmarshalYAML`; validation in `Validate`. |
| `pkg/cmdman/mux/build.go`                 | `paneArgv` emits `--on-signal` flags from `ResolveSignals(leaf)`.       |
| `pkg/cmdman/cli/attach.go`                | `AttachOptions.SignalActions`; `ErrViewerExit`; dispatch in `handleAttachSignals`. |
| `pkg/muxctl/tmux/*.go`                     | `CurrentWindowIdentity` (reads `ownerOption`, `tmux.go:19`).            |
| `cmd/cmdman/commands/attach.go`           | `--on-signal` flag; parse → handler map; inject into `AttachOptions`.   |
| `cmd/cmdman/commands/logs.go`             | `--on-signal` flag; `signal.Notify` dispatch around `RenderLogs`.       |
| `cmd/cmdman/commands/{mux,compose_mux}.go`| no behavior change (flags flow through the spec → `paneArgv`).          |
| `pkg/cmdman/compose/load.go`              | `validateMux`/`validateMuxPane` validate the `signals` maps.            |
| `doc/man/cmdman-mux.5.md`, `cmdman-compose.5.md`, `cmdman-mux.1.md`, `cmdman-compose-mux.1.md` | document `signals:` and `--on-signal`. |

### Compose integration

The `mux:` section already embeds in compose files (`raw.Mux`,
`load.go:491`), so `Spec.Signals` and per-leaf `signals` flow through unchanged
and are statically validated by the extended `validateMux`. `${CMDMAN_MUX_*}`
env in `exec` actions lets a compose dashboard wire `cycle-scale` to a signal
without the pane knowing the project name at build time.

## Non-goals

- **Keybinding front-ends.** Per-driver bindings that issue `kill -USR1
  <pane_pid>` (tmux `bind` + `run-shell`, wezterm `key_tables` + Lua, zellij
  modes/plugin) and overlay pickers (`display-menu`, wezterm `InputSelector`,
  zellij floating plugin) are a separate plan. This plan ships the portable
  action back-end they target.
- **The `0x1a` byte intercept** in raw-mode `attach` (catching bare Ctrl-Z as
  an input byte) is intentionally dropped: with delivered-signal handling plus
  a keybinding→`kill` front-end, the byte path adds a second code path for no
  extra reach. Bare Ctrl-Z in a cooked `logs` pane is covered by a `SIGTSTP`
  override.
- **zellij/wezterm drivers** generally (v1 is tmux-only); the spec fields and
  the viewer handler are driver-agnostic, but `CurrentWindowIdentity` and the
  injected env are tmux-specific until those drivers land.

## Open questions

1. Flag spelling: `--on-signal SIG=ACTION` (repeatable) vs
   `--signal-action SIG=ACTION`. Leaning `--on-signal` for brevity.
2. Should `exec` run via `$SHELL -c` or split argv? `$SHELL -c` is more
   flexible (env expansion, pipelines) and matches the `${CMDMAN_MUX_*}` design;
   argv is safer. Leaning `$SHELL -c`.
3. Whether to also expose a top-level `signals:` default on the **compose**
   document (outside `mux:`) so non-mux attach/logs inherit it. Out of scope
   for the first cut; revisit if asked.
