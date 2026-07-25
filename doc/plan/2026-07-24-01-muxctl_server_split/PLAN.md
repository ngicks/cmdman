# muxctl Server split — Driver → Server → Session

Reshape the muxctl contract from a flat `Driver` interface into a three-tier
chain: `Driver --Connect--> Server --New/Open--> Session`.

## Goal / success criteria

- `muxctl.Driver` shrinks to a single constructor: `Connect` binds to a
  concrete multiplexer server from driver-specific options.
- A new `muxctl.Server` interface owns everything that today needs a
  `DriverOpt` bag threaded per call: `New`, `Open`, `ListWindows`, `FindPane`,
  `ReadWindowState`, `WriteWindowState` — plus `CurrentSessionName` (absorbing the
  tmux shell-out in the mux layer).
- `DriverOpt` disappears from `muxctl.Config` and `muxctl.ListOptions`; the
  per-window state functions stop taking `ListOptions` at all (its only
  consumed field was `DriverOpt`).
- `muxctl.Session` is unchanged.
- No tmux-specific code remains in `pkg/cmdman/mux` (no
  `DriverOpt["path"/"socket"]` reads, no direct tmux exec).
- All of `pkg/cmdman/mux`, `pkg/cmdman/cli`, `cmd/cmdman` compile against the
  new shape; `go test ./...` and e2e pass.

## Scope / non-goals

- In scope: `pkg/muxctl` (driver.go, Config), `pkg/muxctl/tmux`
  (driver.go, exec.go, tmux.go, list.go, leaf.go, scale_state.go),
  `pkg/cmdman/mux` (run.go, down.go, list.go, cycle_scale.go), tests of all of
  the above.
- Non-goals: no behavior change to window resolution, ownership stamping,
  layout application, or scale cycling. No zellij/wezterm implementation. The
  YAML spec (`muxctl.MuxSpec.Driver` / `.DriverOpt`, `mux.Spec.DriverOpt`)
  keeps its shape — it is how callers *describe* which server to connect to.

## Context

Current shape (`pkg/muxctl/driver.go`):

- `Driver` has 6 methods: `New(ctx, Config)`, `Open(ctx, Config)`,
  `ListWindows(ctx, ListOptions)`, `FindPane(ctx, ListOptions, windowID, key)`,
  `ReadWindowState(ctx, ListOptions, windowID, key)`,
  `WriteWindowState(ctx, ListOptions, windowID, key, value)`.
- Server addressing (tmux binary `"path"`, socket `"socket"`) travels as
  `map[string]string` in **two** places — `Config.DriverOpt` and
  `ListOptions.DriverOpt` — and every tmux entry point rebuilds an executor
  from it (`newExecutorFor`, `pkg/muxctl/tmux/exec.go:27`).
- `FindPane`/`ReadWindowState`/`WriteWindowState` take a whole `ListOptions`
  but consume only `.DriverOpt` (tmux/scale_state.go:49,72, tmux/leaf.go:112) —
  the asymmetry that motivated this plan.
- Registry: `RegisterDriver`/`LookupDriver` (database/sql idiom), tmux
  self-registers in `pkg/muxctl/tmux/driver.go:18`.
- Consumers (`pkg/cmdman/mux`) resolve the driver via `resolveDriver`
  (run.go:278) and then thread `spec.DriverOpt` into every `Config` /
  `ListOptions` they build (run.go:135, down.go:108,137, list.go:82,
  cycle_scale.go:93,113,204,313).
- `mux/run.go:208 currentTmuxSession` and `down.go:93` reach into
  `DriverOpt["path"]/["socket"]` directly to shell out to tmux for
  current-session detection — a tmux-specific leak in the driver-agnostic
  layer, absorbed by `Server.CurrentSessionName` in this plan (D3).

## Approach

New contract in `pkg/muxctl/driver.go`:

```go
// Driver binds to one multiplexer server.
type Driver interface {
    // Connect binds to the server selected by opt (driver-specific keys; the
    // tmux driver honors "path" and "socket"). Pure binding — no I/O, no
    // server spawn; a missing server surfaces later as zero rows / ok=false.
    Connect(ctx context.Context, opt map[string]string) (Server, error)
}

// Server is one multiplexer server: it builds, enumerates, and stores
// per-window state for cmdman-owned windows on that server.
type Server interface {
    New(ctx context.Context, cfg Config) (Session, error)
    Open(ctx context.Context, cfg Config) (Session, bool, error)
    ListWindows(ctx context.Context, opts ListOptions) ([]Window, error)
    FindPane(ctx context.Context, windowID, key string) (paneID string, ok bool, err error)
    ReadWindowState(ctx context.Context, windowID string, key StateKey) (string, error)
    WriteWindowState(ctx context.Context, windowID string, key StateKey, value string) error
    // CurrentSessionName reports the name of the multiplexer session the
    // calling terminal is attached to ("inside the multiplexer"), ok=false
    // when not attached or undetectable. Absorbs mux/run.go
    // currentTmuxSession (D3); named for what it returns — a session name,
    // not a [Session] (D5).
    CurrentSessionName(ctx context.Context) (name string, ok bool, err error)
}
```

- `Config` loses `DriverOpt`; `ListOptions` loses `DriverOpt` (keeps
  `Session`, `Identity`, `StateKeys`).
- `RegisterDriver`/`LookupDriver` stay as-is (still keyed by driver name).
- tmux: `Driver.Connect` builds the `*executor` once and returns an exported
  `*tmux.Server{exec}`; today's package-level funcs (`New`, `Open`,
  `ListWindows`, `FindPane`, `ReadWindowState`, `WriteWindowState`) become
  methods on `*Server` — no package-level wrappers remain (D4).
  `tmux.Server.CurrentSessionName` runs `display-message -p '#{session_name}'`
  through the shared executor. `tmux.Session` keeps holding the executor it
  gets from its parent `Server`.
- `pkg/cmdman/mux`: `resolveDriver(declared, env)` grows into
  `resolveServer(ctx, declared, driverOpt, env) (muxctl.Server, error)`
  (lookup + `driver.Connect`); every call site then drops its `DriverOpt`
  threading. `run.go`/`down.go` call `server.CurrentSessionName` instead of
  `currentTmuxSession` (delete it and the `path`/`socket` reads).
  `cycle_scale.go` loses `listOpts` entirely; `writeScalePosition` takes a
  `muxctl.Server` and no options.

Decided (see DECISION.md):

- D1 — `Connect(ctx, map[string]string)`: keep the opaque bag; method named
  Connect, not Open, to avoid colliding with `Server.Open`'s find-session
  meaning.
- D2 — Connect is a pure binding, no I/O; missing-server tolerance preserved.
- D3 — `Server.CurrentSessionName` is in scope.
- D4 — tmux exports methods on `*tmux.Server` only; package-level functions
  are removed, tests migrate to the Server type.
- D5 — the session-name probe is named `CurrentSessionName` (not
  `CurrentSession`/`OpenCurrent`): it returns a name and opens nothing.

Rejected alternatives:

- **Narrow options type** (`ServerOptions{DriverOpt}`) passed per call — keeps
  the flat Driver but still threads addressing through every call; doesn't
  model "same server" as a value.
- **Sessionful-only design** (fold Server into Session) — breaks the
  session-less operations (`ListWindows` from outside tmux, teardown without
  creating windows), which are load-bearing (down.go, cycle_scale.go).

## Implementation steps

1. **muxctl contract** — rewrite `pkg/muxctl/driver.go`: add `Server`
   interface (incl. `CurrentSessionName`), shrink `Driver` to `Connect`, delete
   `Config.DriverOpt` and `ListOptions.DriverOpt`, update doc comments (incl.
   the Driver rationale block at driver.go:93-102). Adjust `pkg/muxctl/doc.go`
   if it names the old shape.
2. **tmux driver** — add `Server` struct in `pkg/muxctl/tmux/` holding
   `*executor`; convert package-level `New`/`Open` (tmux.go),
   `ListWindows` (list.go), `FindPane` (leaf.go),
   `ReadWindowState`/`WriteWindowState` (scale_state.go) into methods; add
   `CurrentSessionName`; `Driver` in driver.go becomes the one-method `Connect`
   adapter; delete `newExecutorFor`. Update tmux tests (helpers_test.go,
   ownership_test.go, cycle_scale_test.go, tmux_test.go, …).
3. **mux consumer** — `run.go`: `resolveDriver` → `resolveServer`; `Run`,
   `Down`, `List`, `CycleScale`, `ReadScaleState` call `server.*` and stop
   populating `DriverOpt` in `Config`/`ListOptions`; replace
   `currentTmuxSession` with `server.CurrentSessionName` (in `resolveSessionName`'s
   queryCurrent hook and down.go's identity derivation); `writeScalePosition`
   and `cycleScaleWindow` signatures drop `driver`+`listOpts` for `server`.
   Consumer option structs (`DownOptions.DriverOpt`, `ListOptions.DriverOpt`,
   `ScaleStateOptions.DriverOpt`) stay — they feed `Driver.Connect` now.
4. **Fakes/tests in mux** — update `run_internal_test.go`,
   `down_internal_test.go`, `cycle_scale_test.go`, `scale_rmw_test.go`,
   `build_test.go` fakes to implement `muxctl.Server` (+ tiny fake Driver).
5. **Docs sweep** — `mux/doc.go`, comments referencing
   `[muxctl.Driver.ListWindows]` etc. across mux files; project-overview
   rules file is generated context, leave it.
6. **Verify** — `go test ./...`, `golangci-lint run`, e2e (`e2e/cmdman`) if a
   mux e2e exists.

## Testing and verification

- Existing unit tests updated in steps 2/4 double as the regression suite; no
  behavior change expected, so failures indicate contract drift.
- tmux integration tests (testdata-driven, ownership/cycle-scale) run
  unchanged in behavior.
- `CurrentSessionName` gets a unit test in tmux (attached vs not attached — the
  latter via a bogus socket) and the mux-layer fakes stub it.
- `go build ./... && go test ./... && golangci-lint run`.

## Risks

- Wide mechanical blast radius (every mux call site + all fakes); mitigated by
  compiler-driven refactor and no-behavior-change goal.
- `Connect` taking `ctx` while doing no I/O may look odd; kept for
  forward-compat with drivers that must probe (D2).
- `CurrentSessionName` semantics ("what session is the caller in") depend on the
  calling terminal's environment ($TMUX), which the executor does not model —
  the tmux implementation relies on `display-message` resolving the attached
  client, same as today's `currentTmuxSession`; behavior is intentionally
  identical.

## Addendum (2026-07-24): ServerConfig + driver YAML object (D6–D8)

Follow-up decided after the base split landed: executable/socket are promoted
to the muxctl layer; the YAML `driver` field becomes an object.

Target shapes: see DECISION.md D6 (ServerConfig), D7 (tmux path-aware Socket),
D8 (YAML `driver: {name, path, socket, opts}` and Go `DriverSpec`).

Steps:

7. **muxctl** — add `ServerConfig` (driver.go); `Driver.Connect(ctx,
   ServerConfig)`; replace `MuxSpec.Driver string` + `MuxSpec.DriverOpt` with
   `MuxSpec.Driver DriverSpec` (spec.go, yaml tags name/path/socket/opts);
   update validate.go/spec tests/driver_test.go as needed.
8. **tmux** — `Connect` builds the executor from
   `cfg.Executable`/`cfg.Socket`/`cfg.DriverOpt`; executor grows path-aware
   socket handling per D7 (`-S` when the value contains a path separator,
   else `-L`); tests for both forms; doc.go updated (no more "path"/"socket"
   DriverOpt keys — the opts bag is now genuinely driver-specific and tmux
   currently defines no keys).
9. **mux consumer + upper layers** — `mux.Spec.Driver`/`.DriverOpt` →
   `Driver muxctl.DriverSpec` (spec.go yaml); `resolveServer` takes the
   DriverSpec (name → lookup/autodetect as before, rest → ServerConfig);
   option structs `DownOptions`/`ListOptions`/`ScaleStateOptions` replace
   `Driver string`+`DriverOpt map` with `Driver muxctl.DriverSpec`; update
   `pkg/cmdman/cli/tui_backend_mux.go`, `pkg/cmdman/compose` mux glue,
   `cmd/cmdman/commands/{zz_mux_helpers,mux_ls,mux_down}.go`; update every
   YAML fixture (mux tests, e2e/cmdman mux specs) to the driver-object form.
10. **Verify** — full build/test/lint + review, as before.

## Open questions

(none — all resolved; see DECISION.md D1–D8)
