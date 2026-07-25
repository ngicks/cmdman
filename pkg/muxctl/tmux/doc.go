// Package tmux implements the [muxctl] driver contract for tmux — [Driver],
// [Server], and [Session] — by issuing CLI commands against a tmux server.
//
// One [Session] owns exactly one window inside one tmux session — the
// "cmdman-owned window," named via [muxctl.Config.WindowName]. [Session.ApplyLayout]
// resets the panes inside that window and rebuilds them from a
// [muxctl.PaneSpec] tree; the tmux session and any other windows are left
// untouched. This window-by-name ownership is what keeps re-runs safe and
// portable to multiplexers that lack tmux's per-pane @-options.
//
// The driver hosts no tty/pty of its own; it shells out to tmux and exits. The
// tmux binary and server are selected once by [Driver.Connect] from a
// [muxctl.ServerConfig]: Executable overrides the tmux binary (default "tmux")
// and Socket selects the server — a value containing a path separator is a
// socket file path (tmux -S <path>), a bare name is a named socket in tmux's
// default dir (tmux -L <name>), and empty selects the default server. When
// Connect runs from inside an existing tmux client ($TMUX), leaving Socket empty
// makes tmux reuse that current server; from outside, an empty Socket selects
// tmux's default socket. A non-empty Socket selects a dedicated server, the
// opt-in isolation mode. [muxctl.ServerConfig.DriverOpt] carries no tmux-defined
// keys today and is ignored.
//
// Recognized per-pane CmdOpt keys (others are ignored):
//
//   - "title": overrides the tmux pane-border title; defaults to the pane
//     name.
package tmux
