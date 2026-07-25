# DECISION LOG — muxctl Server split

## D0 — Three-tier contract (decided by plan mandate)

**Choice:** `Driver --Connect--> Server --New/Open--> Session`. Server owns the
session-less operations; Driver is only a binder from driver-specific options
to a Server; Session unchanged.

**Rationale:** `FindPane`/`ReadWindowState`/`WriteWindowState` consumed only
`ListOptions.DriverOpt` — server addressing masquerading as list options.
Capturing the server once as a value removes per-call `DriverOpt` threading
and the two duplicate `DriverOpt` fields (`Config`, `ListOptions`).

**Rejected:** narrow `ServerOptions` per call (still threads addressing);
folding Server into Session (breaks session-less enumeration/teardown).

## D1 — Driver.Connect(ctx, map[string]string) (resolved 2026-07-24)

**Choice:** the binder method is named **Connect** and takes the raw
`map[string]string` driver-option bag.

**Rationale:** the bag is exactly what `MuxSpec.DriverOpt` already carries —
no new type needed for a single field. The user renamed Open → Connect so
"Open" keeps one meaning in the package (`Server.Open` = locate an existing
owned window).

**Rejected:** a `ServerConfig` wrapper struct (speculative room to grow;
nothing to put in it today); naming the method Open (collides with
`Server.Open`'s different semantics).

## D2 — Connect is a pure binding, no I/O (resolved 2026-07-24)

**Choice:** Connect captures addressing only; it never probes or spawns a
server.

**Rationale:** preserves today's tolerated-absence semantics — a missing tmux
server yields zero rows from `ListWindows` and ok=false from `Server.Open`,
which the down/list no-op paths rely on.

**Rejected:** probe-and-fail-fast (earlier diagnostics, but breaks the
"no server ⇒ nothing to do" flows and adds a startup cost per call chain).

## D3 — Server.CurrentSession is in scope (resolved 2026-07-24)

**Choice:** add `CurrentSession(ctx) (name string, ok bool, err error)` to
`Server`; delete `mux/run.go currentTmuxSession` and the
`DriverOpt["path"/"socket"]` reads in run.go/down.go.

**Rationale:** same leak class this plan exists to fix — tmux-specific exec
and option-key knowledge sitting in the driver-agnostic mux layer.

**Rejected:** deferring to a follow-up plan (would leave the leak plus a
now-orphaned reason for mux to inspect the option bag).

## D4 — tmux exports methods on *tmux.Server only (resolved 2026-07-24)

**Choice:** convert the package-level `New`/`Open`/`ListWindows`/`FindPane`/
`ReadWindowState`/`WriteWindowState` functions into methods on an exported
`*tmux.Server`; keep no package-level wrappers.

**Rationale:** clean break; back-compat is a stated non-goal and wrappers
would preserve two ways to do the same thing.

**Rejected:** retaining package-level thin wrappers (less test churn, but
permanent dual surface).

## D5 — Session-name probe named CurrentSessionName (resolved 2026-07-24)

**Choice:** the D3 method is `CurrentSessionName(ctx) (name string, ok bool,
err error)`.

**Rationale:** the method returns the *name* of the attached multiplexer
session and performs no open; the name says exactly that and cannot be
misread as returning a `muxctl.Session`.

**Rejected:** `CurrentSession` (collides with the `muxctl.Session` type — it
reads as returning one); `OpenCurrent` (user's earlier candidate — "Open" in
muxctl means "locate a window and return a Session", which this method does
not do).

## D6 — Connect takes ServerConfig; executable/socket are first-class (resolved 2026-07-24, revises D1)

**Choice:** `Driver.Connect(ctx, cfg ServerConfig)` with
`ServerConfig{Executable string; Socket string; DriverOpt map[string]string}`.
Executable and Socket are common to every multiplexer, so they live at the
muxctl layer; only genuinely driver-specific keys stay in the DriverOpt bag.

**Rationale:** D1 picked the bare map before this requirement existed; "which
binary" and "which server" are not driver-specific concepts, and a struct
gives them names and docs.

**Rejected:** positional params `Connect(ctx, executable, socket, opt)`
(unlabeled call sites); keeping them as well-known map keys (stringly-typed
contract for universal concepts).

## D7 — tmux Socket resolution is path-aware (resolved 2026-07-24)

**Choice:** the tmux driver maps `Socket` containing a path separator to
`tmux -S <path>` (socket file path) and a bare name to `tmux -L <name>`
(name in tmux's default socket dir). Empty means the default server.

**Rationale:** supports true socket paths (the field's muxctl-level meaning)
while keeping every existing -L-name user (e2e, current specs) working.

**Rejected:** -L-only (cannot express a socket path); -S-only (breaks all
current bare-name users).

## D8 — YAML `driver` becomes an object (resolved 2026-07-24)

**Choice:** the mux spec's `driver` YAML field changes from a string (plus
sibling `driver_opt` map) to an object:

```yaml
driver:
  name: tmux        # empty → autodetect, as before
  path: /usr/bin/tmux
  socket: cmdman    # name, or a socket file path (D7)
  opts:             # driver-specific only
    aaa: bbb
```

Go-side: a `DriverSpec{Name, Path, Socket string; Opts map[string]string}`
(YAML-facing) replacing `MuxSpec.Driver`/`MuxSpec.DriverOpt` and
`mux.Spec.Driver`/`.DriverOpt`; consumers map it onto `ServerConfig`
(Path→Executable, Opts→DriverOpt) plus the name for driver lookup.

**Rationale:** user decision — the whole semantic changes so the YAML mirrors
the new API instead of hiding universal fields inside `driver_opt`. Back-compat
is a non-goal.

**Rejected:** hoisting to sibling top-level YAML fields (`executable:`,
`socket:` next to `driver:` — scatters one concept); keeping `driver_opt`
extraction at the consumer layer (YAML disagrees with the Go API).

**Implementation note:** `DriverSpec` additionally carries the repo-standard
inline `Unknown map[string]any` catch-all (as MuxSpec/Layout/PaneSpec do), so
stray keys under `driver:` are captured for warning rather than silently
dropped — a convention-consistent addition beyond D8's literal field list.
