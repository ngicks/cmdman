// Package tmux implements [muxctl.Session] for tmux by issuing CLI commands
// against a tmux server.
//
// One [Session] owns exactly one window inside one tmux session — the
// "cmdman-owned window," named via [muxctl.Config.WindowName]. [Session.ApplyLayout]
// resets the panes inside that window and rebuilds them from a
// [muxctl.PaneSpec] tree; the tmux session and any other windows are left
// untouched. This window-by-name ownership is what keeps re-runs safe and
// portable to multiplexers that lack tmux's per-pane @-options.
//
// The driver hosts no tty/pty of its own; it shells out to tmux and exits. The
// tmux binary and server are selected through [muxctl.Config.DriverOpt]: "path"
// overrides the tmux binary (default "tmux") and "socket" selects the server
// (-L socket). When called from inside an existing tmux client ($TMUX), leaving
// "socket" empty makes tmux reuse that current server; from outside, an empty
// "socket" selects tmux's default socket. A non-empty "socket" selects a
// dedicated server, the opt-in isolation mode.
//
// Recognized per-pane CmdOpt keys (others are ignored):
//
//   - "title": overrides the tmux pane-border title; defaults to the pane
//     name.
package tmux
