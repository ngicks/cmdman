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
//     Session interface is intentionally minimal — ApplyLayout, Close, and
//     StatWindow — so that driver-specific concerns (session reuse, sockets,
//     dedicated servers, teardown/detach, ...) live in each driver's
//     constructor or driver-specific methods, not on the interface.
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
//
// # Driver contract: identity stamp and enumeration
//
// Every driver MUST support two semantic capabilities:
//
//  1. Stamp an opaque, caller-supplied identity on the window-equivalent at
//     build time (when the driver constructor resolves the window). The identity
//     is an arbitrary string chosen by the caller; the driver stores and returns
//     it verbatim without interpretation.
//
//  2. Enumerate windows that carry the stamp, server-wide or filtered to one
//     session, by exact identity. This enumeration must not depend on $TMUX,
//     an attached client, or the current window — it must work from any calling
//     context including run-shell, command-prompt, and outside the multiplexer
//     entirely.
//
// WHERE the stamp lives is private to each driver:
//
//   - tmux: a window-level user option (@cmdman_window), set with
//     "set-option -w" and enumerated with "list-windows -a -F #{@cmdman_window}".
//   - WezTerm (future): tab title or per-pane user vars emitted via escape
//     sequence from inside panes; "wezterm cli list" gives solid enumeration.
//   - zellij (future): no arbitrary metadata today; likely a plugin storing
//     per-pane/per-window data. Vision only.
//
// Titles (window/pane/tab) MUST NOT be used as identity storage. Titles are
// presentation surface: programs, shells, and users routinely overwrite them,
// so a title-based identity is silently lost during normal use. This is the
// same lesson that motivated the @cmdman_marker option (storing the layout
// index in the pane title suffix was fragile for the same reason).
//
// A sidecar registry file (e.g. a per-socket JSON mapping identity → window id)
// is deliberately deferred rather than used as a fallback. A sidecar cannot be
// the source of truth — only a cache. Every read would need to re-validate
// liveness AND ownership against the live multiplexer (manual window closes,
// server restarts that reuse ids), which itself requires an in-driver mark
// anyway, degenerating into "native stamp + sidecar + locking + crash cleanup".
// For drivers that have native per-window storage (tmux), the native option is
// strictly better. For a driver that truly lacks any per-window storage, a
// sidecar should be designed at that time with the full liveness-validation
// cost accounted for.
package muxctl
