// Package tmux implements the [muxctl] driver contract for tmux — [Driver],
// [Server], and [Session] — by issuing CLI commands against a tmux server.
//
// One [Session] controls one window inside one tmux session — the
// "cmdman-owned window," named via [muxctl.Config.WindowName].
// [Session.ApplyLayout] resets the project's panes inside that window and
// rebuilds them from a [muxctl.PaneSpec] tree; the tmux session, any other
// window, and the window's own frame panes are left untouched. This
// window-by-name ownership is what keeps re-runs safe and portable to
// multiplexers that lack tmux's per-pane @-options.
//
// The two identities muxctl gives a window are tmux user options here: the
// project's is the window option @cmdman_window, the shown frame's is the
// window option @cmdman_frame_def, and a pane belonging to the frame rather
// than to the project carries the pane option @cmdman_frame — which is what
// keeps it out of the project's rebuild, its viewer sweep, its layout marker,
// and the focus. User options are tmux's own: zellij and wezterm have no
// equivalent, so a driver for them implements or rejects that contract on its
// own terms (scale_state.go records the same caveat for per-window state).
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
