# Decisions — remove statusline widget

Decisions made during the autonomous run that removed the statusbar widget.
Entries tagged `[automatic]` were decided without the user (user marked
themselves away). The prior switcher/statusbar decisions (D44 and friends)
live in `doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md`;
this run keeps its own directory rather than appending there.

## "Move logic" = collapse panel into switcher [automatic]

`widget/internal/panel` existed only because the statusbar and switcher
facades shared one model. With the statusbar widget gone, the panel package
has exactly one consumer, so its code moves into `cmdman/tui/widget/switcher`
proper and the `internal/panel` package is deleted. The statusbar rendering
code (`panel/statusbar.go`) is deleted outright rather than relocated: after
the widget's removal it has no consumer — the full TUI's bottom line is
separate code (`Model.renderFooter` in `cmdman/tui/view.go`) that never called
into panel.

## Frame `component: statusbar` becomes a plain validation error [automatic]

`ComponentStatusbar` is removed from `frame`'s built-in component table, so a
frame spec naming `component: statusbar` now fails normalization with the
standard unknown-component error (whose message lists the remaining builtins,
now just `switcher`). No silent ignore and no bespoke "statusbar was removed"
error text.

## AltScreen becomes unconditionally true [automatic]

`cmdman/cli/tui.go` special-cased the statusbar as the only widget running
outside the alternate screen. With it gone every widget takes the alt screen,
so the conditional collapses to `AltScreen: true`. If a future one-line widget
appears, the conditional can be reintroduced then.

## Unrelated `"statusbar"` string fixtures stay [automatic]

`pkg/muxctl/tmux/frame_internal_test.go` uses the bare string `"statusbar"`
as an arbitrary command name in muxctl fixtures with no dependency on the
widget; it is left unchanged.
