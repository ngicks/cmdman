# muxctl-01 — reify the driver contract; make `pkg/cmdman/mux` tmux-free

One-line summary: turn muxctl/doc.go's prose "driver contract" into Go
interfaces + vocabulary types in `pkg/muxctl`, implement them in the tmux
driver, and rewire `pkg/cmdman/mux` so it imports only `pkg/muxctl`.

Executes item **C11** of `doc/plan/2026-07-04-01-design_refactors/PLAN.md`,
promoted by that plan's **D10** (reopening D7). ApplyLayout-core extraction
stays deferred (driver-internal, does not leak into `pkg/cmdman/mux`).

## Goal / success criteria

- `pkg/cmdman/mux` (non-test files) has zero imports of `pkg/muxctl/tmux`.
- The driver contract is Go types: a `muxctl.Driver` interface, extended
  `muxctl.Session`, and muxctl-owned vocabulary types (`Config`,
  `ListOptions`, `OwnedWindow`).
- The tmux driver self-registers; the binary links it via blank import at the
  composition root.
- Behavior-preserving: `go build ./...`, `go test ./...` (incl. real-tmux
  integration + e2e) green; user-visible error wording for
  unimplemented/unknown drivers stays equivalent.

## Scope / non-goals

- Non-goals: ApplyLayout materialize/split core extraction (still deferred
  per D10); any zellij/wezterm implementation; TUI/compose changes (they go
  through `pkg/cmdman/mux`'s public API, which keeps its shape).

## Context (from the 2026-07-04 scan; all cites verified)

What `pkg/cmdman/mux` consumes from `pkg/muxctl/tmux` today:

- Constructors: `tmux.New` (`tmux/tmux.go:100`, called `run.go:137`),
  `tmux.OpenExisting` (`tmux.go:179`, called `down.go:139`,
  `cycle_scale.go:216`), with `tmux.Config` (`tmux.go:22-85`; generic fields
  SessionName/WindowName/WindowID/OwnedIdentity/ReuseCurrentWindow/
  ViewerDetachKeys + tmux-specific Path/Socket).
- Enumeration: `tmux.ListOwnedWindows`/`ListOwnedWindowsOptions`
  (`Path, Socket, Session, Identity` — `tmux/list.go:36-69`) /
  `tmux.OwnedWindow` (`list.go:10-33`), called from `list.go:81`,
  `down.go:109`, `cycle_scale.go:98,326`.
- Cycle-scale primitives: `tmux.FindLeafPane` (`tmux/leaf.go:105-109`),
  `tmux.RespawnLeaf` (`leaf.go:137-142` — takes concrete `*tmux.Session`
  because it uses unexported session internals), `tmux.ReadScaleRaw`/
  `WriteScaleRaw` (`tmux/scale_state.go:26-51`).
- Two `*tmux.Session` methods missing from `muxctl.Session`
  (`muxctl/session.go:19-50` has only ApplyLayout/Close/StatWindow):
  `WindowID()` (`tmux/tmux.go:220-222`, used `run.go:152`) and
  `Detach(ctx)` (`tmux/detach.go:21`, used `down.go:157`).
- Driver selection: `resolveDriver` (`pkg/cmdman/mux/run.go:260-273`) —
  `spec.Driver` wins, else `$TMUX`→tmux, `$ZELLIJ`→zellij, else tmux; every
  non-tmux resolution errors "not implemented yet" at five call sites
  (`run.go:96-100`, `down.go:82-87`, `list.go:75-79`,
  `cycle_scale.go:92-96,319-324`). `DriverOpt` honors exactly `"path"` and
  `"socket"` for tmux.
- `pkg/cmdman/mux/list.go:11-38` already defines a mux-layer `OwnedWindow`
  mirror "so future non-tmux drivers fit"; but `down.go`/`cycle_scale.go`
  still operate on raw `tmux.OwnedWindow` internally.
- `pkg/muxctl/internal/cmd/muxctltester` imports tmux directly by design
  (driver test harness) — out of scope.

## Approach (decisions D-M1-1..4 in DECISION.md)

New in `pkg/muxctl` (e.g. `driver.go`):

```go
// Vocabulary types (muxctl-owned, driver-agnostic).
type Config struct {          // from tmux.Config's generic fields
    SessionName, WindowName, WindowID, OwnedIdentity string
    ReuseCurrentWindow bool
    ViewerDetachKeys []string
    DriverOpt map[string]string // driver-specific: tmux honors "path","socket"
}
type ListOptions struct {
    Session, Identity string
    StateKeys []string          // per-window state keys to fetch inline
    DriverOpt map[string]string
}
type OwnedWindow struct {
    SessionName, WindowID, WindowName, Identity string
    Marker int
    State map[string]string     // filled for requested StateKeys
}

type Driver interface {
    New(ctx context.Context, cfg Config) (Session, error)
    OpenExisting(ctx context.Context, cfg Config) (Session, bool, error)
    ListOwnedWindows(ctx context.Context, opts ListOptions) ([]OwnedWindow, error)
    FindLeafPane(ctx context.Context, opts ListOptions, windowID, cycleKey string) (string, bool, error)
    ReadWindowState(ctx context.Context, opts ListOptions, windowID, key string) (string, error)
    WriteWindowState(ctx context.Context, opts ListOptions, windowID, key, value string) error
}

func RegisterDriver(name string, d Driver) // panics on duplicate
func LookupDriver(name string) (Driver, error)
```

- `muxctl.Session` gains `WindowID() string`, `Detach(ctx) error`, and
  `RespawnLeaf(ctx, paneID string, leaf Leaf) error` (the "respawn one leaf"
  method the parent tier-2 called for; tmux's package-level `RespawnLeaf`
  becomes this method).
- Per-window state is a generic opaque KV: cmdman passes key `"scale"`; the
  tmux driver maps key → `@cmdman_<key>` (keeps D-M0-2's constant placement
  and D6's "scale semantics out of muxctl"). Write of empty value unsets.
  Absent key reads as `""`, no error (preserves current semantics).
- Registration: the tmux package registers itself in `init()` as `"tmux"`
  (database/sql driver idiom); `cmd/cmdman/main.go` gains
  `_ ".../pkg/muxctl/tmux"`. `resolveDriver` keeps the env autodetect and
  now ends in `muxctl.LookupDriver`; unknown/unregistered drivers produce an
  error naming the driver (keep "not implemented yet" wording for the known
  names zellij/wezterm).
- `muxctl/doc.go:42-55`'s contract section is updated to reference the
  reified `Driver`/`Session` types.

Rejected alternatives: threading a `Driver` value through every
`RunOptions`/`DownOptions`/... (spreads driver construction to compose/cli/
cmd callers); a name→impl mapping file inside `pkg/cmdman/mux` (keeps the
tmux import, defeating the invariant); muxctl importing tmux for a factory
(import cycle).

## Implementation steps (each independently verifiable)

1. **muxctl contract**: add `driver.go` (types + `Driver` + registry +
   tests for the registry), extend `Session` (+ doc-contract comments),
   update `doc.go`. Build green (tmux does not yet satisfy — keep new
   Session methods unwired until step 2 lands in the same PR-sized change;
   steps 1+2 may land as one commit-equivalent unit if the interface split
   breaks the build).
2. **tmux driver**: satisfy the contract — adapt `tmux.Config` sites to
   consume `muxctl.Config` (Path/Socket from `DriverOpt`), implement
   `Driver` (thin wrappers over the existing package funcs),
   `Session.RespawnLeaf` method (package func removed or delegating),
   `ListOwnedWindows` fills `State` for `StateKeys`, generic
   `Read/WriteWindowState` replace `ReadScaleRaw`/`WriteScaleRaw`,
   `init()` registration. Adapt tmux tests. Build + tmux tests green.
3. **cmdman/mux rewire**: all four files switch to `muxctl.Driver` via
   `resolveDriver` → `LookupDriver`; internal currency becomes
   `muxctl.OwnedWindow` (public `mux.OwnedWindow` keeps its decoded
   `ScalePositions map[string]int` shape); scale RMW calls the KV methods
   with key `"scale"`; delete every `pkg/muxctl/tmux` import from non-test
   files. Blank import added to `cmd/cmdman/main.go`. Build + tests green.
4. **Tests**: registry unit test; keep/adapt the real-tmux tests
   (`pkg/cmdman/mux/scale_rmw_test.go` may keep its direct tmux import —
   test files are exempt from the invariant, or switch to the driver);
   verify `go vet` + full `go test ./pkg/...`.
5. **Full verification**: `go build ./...`, `go test ./...` (incl. e2e),
   review pass. Grep-gate: `grep -rn 'muxctl/tmux' pkg/cmdman/mux/*.go`
   (non-test) must be empty.

## Testing / verification

- Behavior-preserving refactor; e2e mux tests (`e2e/cmdman/mux*_test.go`)
  and the real-tmux integration tests are the safety net.
- New: registry tests (register/lookup/unknown), and the adapted tmux
  Driver-method tests must preserve the coverage of the funcs they wrap.

## Risks

- Widest API churn so far in this series (muxctl, tmux, cmdman/mux all
  change surface at once); mitigated by the e2e suite and by keeping the
  `pkg/cmdman/mux` public API unchanged.
- Forgetting the blank import would compile fine and fail at runtime with
  "unknown driver tmux" — cover with an e2e-reachable path (any existing
  mux e2e test exercises it) and a unit lookup test.
- The generic KV inline-fetch (`StateKeys`/`State`) must preserve tmux's
  single-format-call efficiency and its absent→empty semantics.

## Open questions

None — design decisions recorded as D-M1-1..4.
