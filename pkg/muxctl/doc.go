// Package muxctl is a window-centric controller for terminal multiplexers
// (tmux first; zellij and wezterm are planned). It owns no multiplexer state
// of its own — it issues CLI commands to the underlying multiplexer and hosts
// no tty/pty — so it is safe to use from any non-interactive caller.
//
// # Layering
//
// Two layers live here:
//
//   - The runtime: [Session] and [Pane] interfaces. Drivers under
//     pkg/muxctl/<driver> implement these (only tmux is planned today). The
//     Session interface is intentionally minimal — ApplyLayout and Close —
//     so that driver-specific concerns (session reuse, sockets, dedicated
//     servers, ...) live in each driver's constructor, not on the interface.
//     A Session controls one cmdman-owned window; switching among named
//     layouts is repeated ApplyLayout calls on that window.
//
//   - The spec: [MuxSpec], [Layout], [PaneSpec], [Size], [Direction]. A
//     driver-agnostic description of the switchable layouts the user can
//     pick among, each composed of panes running a given argv. The spec
//     uses argv ([]string) and a per-pane CmdOpt map for driver-specific
//     hints; it knows nothing about cmdman. Higher layers (e.g.
//     pkg/cmdman/...) parse their own user-facing YAML and emit a
//     muxctl.MuxSpec after their own leaf-name → argv resolution.
//
// # Guiding principle
//
// The multiplexer is a disposable viewer. Closing the multiplexer session,
// detaching, or closing a pane MUST NOT stop any process the pane was
// observing. Drivers and callers must preserve this invariant.
//
// # Ownership and re-runs
//
// Drivers own at the window/tab granularity by NAME (the cmdman-owned window
// name comes from the driver constructor) — portable to multiplexers that lack
// tmux-style per-pane @-options. [Session.ApplyLayout] is declarative and
// RESETS the window's panes: switching layouts (or re-running the same one)
// tears the panes down and rebuilds them. Pane reuse across re-applies is
// deliberately not attempted.
package muxctl
