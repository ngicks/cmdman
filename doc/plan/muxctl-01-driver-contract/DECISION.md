# DECISION — muxctl-01-driver-contract

Inherited: promotion itself is parent D10 (reopening D7); scale semantics
stay out of muxctl per parent D6 and muxctl-00's D-M0-1/2. Entries below are
this plan's design decisions.

## D-M1-1: Contract shape — Driver interface + extended Session — RESOLVED 2026-07-04

- Choice: a `muxctl.Driver` interface carrying the session-less capabilities
  (constructors, enumeration, leaf find, per-window state KV) plus three new
  `muxctl.Session` methods (`WindowID`, `Detach`, `RespawnLeaf`).
- Rationale: matches how the code actually partitions today — `RespawnLeaf`
  needs session internals (tmux/leaf.go uses unexported quiesce/stamp), so it
  is a Session method; enumeration/find/KV work from any calling context with
  no session, so they sit on the driver.
- Rejected: everything on Session (enumeration explicitly must not require a
  session/$TMUX per doc.go); separate one-method interfaces per capability
  (interface soup for a contract that is mandatory-in-full per doc.go).

## D-M1-2: Driver registry + blank import at composition root — RESOLVED 2026-07-04

- Choice: `muxctl.RegisterDriver`/`LookupDriver` (database/sql idiom); the
  tmux package self-registers in `init()`; `cmd/cmdman/main.go` blank-imports
  the driver.
- Rationale: the only way `pkg/cmdman/mux` gets fully tmux-free without
  spreading driver construction to every caller. Registration is init-time
  write-once, so the repo's "DI over package globals" rule (aimed at mutable
  service state) is not meaningfully violated.
- Rejected: threading `muxctl.Driver` through every mux options struct
  (pushes driver resolution into compose/cli/cmd); mapping file inside
  `pkg/cmdman/mux` (keeps the import); factory inside muxctl (import cycle).

## D-M1-3: Generic per-window state KV, key "scale" — RESOLVED 2026-07-04

- Choice: `ReadWindowState`/`WriteWindowState(ctx, opts, windowID, key, value)`
  with opaque keys; tmux maps key → `@cmdman_<key>` option; empty value
  unsets; absent reads as `""` with nil error. Enumeration can fetch state
  inline via `ListOptions.StateKeys` → `OwnedWindow.State`.
- Rationale: parent tier-2 asked for "an abstract per-window key/value
  store"; opaque keys keep "scale" vocabulary out of muxctl (D6) while
  preserving tmux's one-call inline fetch during listing.
- Rejected: scale-named methods on the Driver (reimports cmdman vocabulary
  into muxctl); dropping inline fetch and reading state per window after
  listing (extra tmux round-trips, needless behavior risk).

## D-M1-4: muxctl.Config subsumes tmux.Config's generic fields — RESOLVED 2026-07-04

- Choice: `muxctl.Config` carries the driver-agnostic fields (session/window
  naming, identity stamp, reuse flag, viewer detach keys) plus
  `DriverOpt map[string]string`; tmux reads "path"/"socket" from DriverOpt
  (the exact keys `pkg/cmdman/mux` already threads from Spec.DriverOpt).
- Rationale: every field except Path/Socket is already conceptually generic;
  DriverOpt is the established pass-through for driver-specific knobs.
- Rejected: keeping tmux.Config public with a muxctl→tmux adapter in
  cmdman/mux (keeps the import); per-driver typed option structs in muxctl
  (muxctl would enumerate driver specifics — wrong direction).

## D-M1-5: driver-contract API polish (names + StateKey type) — RESOLVED 2026-07-05

Maintainer-approved naming/typing amendment after the contract landed. Purely
behavior-preserving; renames only, no logic change.

- Constructors: keep exactly two (`New` unchanged; `OpenExisting` → `Open`).
  `Open`'s contract is tightened in doc: it NEVER creates a session or window,
  NEVER mutates a window option, and NEVER takes over an unowned current window
  (that is `New`'s prerogative). The guarantees carried over verbatim.
- Enumeration renames: `Driver.ListOwnedWindows` → `ListWindows`, the row type
  `muxctl.OwnedWindow` → `muxctl.Window`, and `Driver.FindLeafPane` → `FindPane`
  (its param `cycleKey` → `key`). The mux-layer public `mux.OwnedWindow` type is
  deliberately NOT renamed — its API shape is frozen.
- `type StateKey string` plus a closed, code-declared const block
  (`StateKeyScale StateKey = "scale"`): the token constraint ([A-Za-z0-9_-],
  spliced into driver identifiers unescaped) moves onto the type;
  `ListOptions.StateKeys` becomes `[]StateKey`, `Window.State` becomes
  `map[StateKey]string`, and `Read/WriteWindowState`'s key param becomes
  `StateKey`. tmux's `stateOption` maps the key; `pkg/cmdman/mux` drops its local
  `scaleStateKey` const in favor of `muxctl.StateKeyScale`.
- Explicit amendment of parent D6 ("no scale vocabulary in muxctl"): only the
  slot NAME `"scale"` is now declared in muxctl (as `StateKeyScale`). The scale
  codec, wire format, and all semantics stay in `pkg/cmdman/mux` — drivers
  persist and return the slot value verbatim, never interpreting it. Naming the
  slot buys a typed, discoverable vocabulary without leaking meaning.
- Rejected: keeping the longer names (churn now vs. never — repo has no external
  consumers); leaving `StateKeys` as `[]string` (loses the type as a doc anchor
  for the token constraint and invites arbitrary external keys).
