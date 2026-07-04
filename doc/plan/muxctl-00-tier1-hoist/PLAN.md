# muxctl-00 — tier-1 pure-function hoist from `pkg/muxctl/tmux/` to `pkg/muxctl/`

One-line summary: move the backend-agnostic pure functions out of the tmux
driver up to `pkg/muxctl/`, and move the scale-position codec (a cmdman
concept) to `pkg/cmdman/mux`.

Executes item **C3** of `doc/plan/2026-07-04-01-design_refactors/PLAN.md`
(rank 3). Scope fixed by that plan's DECISION.md **D6** (scale codec →
`pkg/cmdman/mux`) and **D7** (tier 1 only; all interface extraction deferred
until a second driver is real).

## Goal / success criteria

- The tier-1 pure functions live in `pkg/muxctl/` (exported), tests moved with
  them; `pkg/muxctl/tmux/` calls them qualified.
- The scale-position codec lives in `pkg/cmdman/mux`; `pkg/muxctl/tmux` deals
  only in raw option strings (no decode/encode, no map[string]int scale state).
- No import cycle: `muxctl/tmux` must NOT import `pkg/cmdman/mux`.
- Behavior-preserving: `go build ./...`, `go test ./...`, e2e green.

## Scope / non-goals

- Non-goals (tier 2, deferred per D7): window-enumeration interface,
  cycle-scale driver primitives, ApplyLayout core extraction. No new
  interfaces anywhere.

## Context (verified against working tree 2026-07-04)

Tier-1 hoist candidates (all operate on `muxctl` types already):

- `computeChildCells` — `tmux/sizing.go:23-81`, pure geometry on
  `[]muxctl.Size`; called from `tmux/apply.go:121`. Has its own test file.
- `pickFocus` (`tmux/apply.go:285-317`), `parentDim` (`apply.go:267-274`),
  `childDims` (`apply.go:276-283`) — pure `muxctl.PaneSpec`/`Direction`
  logic; called from `apply.go:89,121,125`.
- `recordSkipped` (`tmux/apply.go:203-212`) — **a method on `applyState`**,
  not a free function: appends every leaf name under a node to `st.skipped`.
  Hoist the pure part as a free leaf-name walk in `muxctl`; the tmux side
  keeps a thin append wrapper (or appends the returned slice inline).
- `shouldReuseUnmarkedWindow` (`tmux/reuse.go:61-66`) — pure decision fn,
  called from `reuse.go:55`; tested standalone.

Scale codec (moves to `pkg/cmdman/mux` per D6 — "scale" is not muxctl
vocabulary):

- `decodeScalePositions` / `encodeScalePositions`
  (`tmux/scale_state.go:29-50, 56-81`) — pure codec for the space-joined
  `"name=pos"` wire format stored in the `@cmdman_scale` window option.
- **Cycle hazard**: the codec is consumed inside tmux by
  `ReadScalePositions`/`WriteScalePosition` (`scale_state.go:87-142`, RMW
  decodes + re-encodes) and by `list.go:150` (populates
  `OwnedWindow.ScalePositions map[string]int`). `cmdman/mux` imports
  `muxctl/tmux`, so tmux cannot import the codec back. The tmux layer must
  be reworked to deal in raw strings:
  - `OwnedWindow.ScalePositions` becomes a raw string field (e.g.
    `ScaleRaw string`); decoding moves to the `pkg/cmdman/mux` consumers
    (`list.go`, `cycle_scale.go`, `down.go` — wherever `.ScalePositions` is
    read).
  - `ReadScalePositions` → raw read (returns the option string; absent →
    empty, no error).
  - `WriteScalePosition`'s read-modify-write moves up to `pkg/cmdman/mux`
    (`cycle_scale.go:268` is the sole caller): read raw → decode → modify →
    encode → write. tmux keeps a raw setter that unsets the option when the
    encoded string is empty (preserving `scale_state.go:130-135` behavior).
  - The `@cmdman_scale` option name stays in tmux beside `markerOption`/
    `leafOption` (option storage is driver mechanics; the *meaning* moves).
- Codec + its tests land in `pkg/cmdman/mux` (unexported is fine — only that
  package uses them).

## Approach

Pure moves + exports; no signature redesign beyond what the codec relocation
forces on the tmux scale helpers. Rejected: reifying driver interfaces for
any of this (parent D7); keeping the codec in `pkg/muxctl` (parent D6).

## Implementation steps (each independently verifiable)

1. **muxctl hoist**: new `pkg/muxctl/layout.go` (+ `layout_test.go`) with
   exported `ComputeChildCells`, `PickFocus`, `ParentDim`, `ChildDims`, and a
   free leaf-name walk replacing `recordSkipped`'s traversal;
   `shouldReuseUnmarkedWindow` → exported decision fn (same file or a small
   `reuse.go` — implementer's call, keep it one coherent unit). Rewire
   `tmux/apply.go`, `tmux/sizing.go` (file may disappear), `tmux/reuse.go`;
   move/adapt their tests. Build + test green.
2. **Scale codec relocation**: codec → `pkg/cmdman/mux` (e.g.
   `scale_codec.go` + test, moved from `tmux/scale_state_test.go` if
   present); rework tmux scale helpers + `OwnedWindow` to raw strings per the
   hazard notes; rewire all `pkg/cmdman/mux` consumers. Build + test green.
3. **Full verification**: `go build ./...`, `go test ./...` (incl. e2e),
   review pass.

## Testing / verification

- Pure-move refactor: existing unit tests move with the functions; e2e
  (`e2e/cmdman`, incl. tmux-backed mux tests) is the behavior safety net.
- Codec relocation is the only part with real rewiring — cover the
  raw-string round-trip (decode→modify→encode→write) with a unit test at the
  `pkg/cmdman/mux` layer if a seam exists without tmux.

## Risks

- `OwnedWindow` field change ripples through `pkg/cmdman/mux` consumers —
  mechanical but easy to miss one (`ls` SCALE column rendering path).
- Behavior parity of absent-option handling (`show-options` non-zero exit →
  treated as empty) must survive the raw-string rework.

## Open questions

None — scope fixed by parent D6/D7.
