# TUI mux plan

## Scope

This plan covers the mux integration exposed from `cmdman tui`.

It owns:

- compose `mux:` discovery
- cycle-layout behavior
- invoking existing compose mux behavior
- tmux popup interaction for mux display

Zellij support is a TODO stub for v1. The current mux implementation detects
zellij but returns "not implemented"; v1 TUI mux behavior should match that and
ship tmux-only.

## Compose Integration

The TUI does not own mux layout execution logic. It should stay a thin wrapper
around existing `cmdman compose mux` behavior.

The Compose tab should discover which projects have a `mux:` section and expose
that in the project list:

```text
> ⿻ local-dev        active           2 commands        mux
  ⿻ api-stack                         3 commands        mux
  ⿻ tools                             0 commands        modified 2026-05-20
```

Mux execution should use the same compose project selection as the highlighted
project row.

## Layout Selection

The existing mux command cycles layouts using persisted tmux window-marker
state. The TUI should not keep its own selected-layout state per project.

V1 should expose cycle-only behavior unless mux gains a new "show layout N"
entry point. Pressing `l` can show a status message such as:

```text
Specific layout selection is not available yet; use c to cycle layouts.
```

If a future `mux.Run` entry point can apply a specific layout by index or name,
`l` may open a selector that calls that entry point. Until then, a selector would
misrepresent the behavior because `mux.Run` currently auto-cycles.

## Cycle Layout

Pressing `c` on a compose project with a `mux:` section cycles to the next
available layout and shows it.

Behavior:

- call the existing compose mux path for the highlighted project
- let mux read and update its tmux window marker as the single source of truth
- do not mirror the selected layout in TUI state
- show a status message after the command returns
- projects without a `mux:` section show a status message

## Mux Display

Applying a mux layout rearranges the current tmux window. If the TUI is running
directly in that window, mux display can replace or rearrange the TUI itself.
For v1, mux layout display should run through the `cmdman tui --popup=tmux`
path so the TUI lives in a popup while mux changes the underlying window.

Expected behavior:

- show the selected compose mux layout using existing compose mux behavior
- prefer tmux popup mode for mux actions
- if `--popup` is not set, show a confirmation warning before invoking mux
- keep the multi-pane guard as a complementary safety check when pane-count
  detection is implemented
- pane-count detection is new production work; the only current
  `#{window_panes}` usage is in an internal dev tool, not on the mux session API
- do not unexpectedly rearrange existing panes without warning

Example non-popup warning:

```text
Showing a mux layout will rearrange the current tmux window, including this TUI.
Continue?
```

Example multi-pane rejection after pane-count detection exists:

```text
Cannot show mux layout: current window already has multiple panes.
```

## Popup Work

`cmdman tui --popup=tmux` and the mux display popup behavior require new tmux
popup code. There is no existing `display-popup` or floating-pane primitive in
the production mux packages. Treat popup support as new mux work, not a thin
flag over existing behavior.

`cmdman tui --popup=zellij` and zellij floating-pane support remain TODO stubs
for v1.

## Test Coverage

Mux tests should cover:

- Compose tab detects projects with a `mux:` section
- `c` invokes existing compose mux behavior for the highlighted project
- the TUI does not persist selected mux layout state
- non-popup mux display warns before invoking mux
- mux display rejects when the current window has multiple panes, once
  pane-count detection exists
- zellij popup/mux attempts report "not implemented"
