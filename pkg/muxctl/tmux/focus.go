package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// FocusWindow implements [muxctl.Server.FocusWindow] as tmux's two-step focus:
// `select-window` makes the window current inside its own session (which needs
// no attached client at all), and `switch-client` moves a client that is sitting
// in another session.
//
// The client is always addressed explicitly with -c. tmux resolves an omitted
// -c to "the current client", which it derives from the caller's terminal —
// something a cmdman process summoned in a `display-popup -E` does not have.
// Measured on a scratch server with two attached clients: a bare
// `switch-client -t <session>` run inside a popup moved the *other* client,
// while `-c <client of the popup's session>` moved the right one. The same
// experiment confirmed the popup's child does get $TMUX (so callers can tell
// they are inside tmux) but no $TMUX_PANE, and that switching sessions leaves
// the popup up — it closes with its process, revealing the landed-on window.
func (srv *Server) FocusWindow(
	ctx context.Context,
	windowID string,
	opts muxctl.FocusOptions,
) error {
	e := srv.exec
	if _, err := e.run(ctx, "select-window", "-t", windowID); err != nil {
		return fmt.Errorf("tmux: select window %s: %w", windowID, err)
	}
	if !opts.SwitchClient {
		return nil
	}
	target, err := e.run(ctx, "display-message", "-p", "-t", windowID, "#{session_name}")
	if err != nil {
		return fmt.Errorf("tmux: resolve session of window %s: %w", windowID, err)
	}
	client, ok := clientToSwitch(ctx, e, opts.ClientSession)
	if !ok {
		// Nobody to move: the window is selected, which is everything focus can
		// mean without a client. Not an error — the caller decides whether to
		// attach a terminal instead.
		return nil
	}
	if _, err := e.run(ctx, "switch-client", "-c", client, "-t", "="+target); err != nil {
		return fmt.Errorf("tmux: switch client %s to session %s: %w", client, target, err)
	}
	return nil
}

// AttachCommand implements [muxctl.Server.AttachCommand]: the same binary and
// socket flags the executor runs everything else with, so a dashboard built on a
// dedicated socket is attached on that socket too. The session is targeted in
// the exact-match `=name` form used elsewhere in this driver.
func (srv *Server) AttachCommand(sessionName string) []string {
	args := srv.exec.buildArgs([]string{"attach-session", "-t", "=" + sessionName})
	return append([]string{srv.exec.path}, args...)
}

// clientToSwitch picks the client [Server.FocusWindow] moves. A caller that
// knows the session it is sitting in gets that session's client — the most
// recently active one when several are attached to it, since that is the
// terminal the user was last typing in and therefore the one that just asked to
// land. Without a caller session a single attached client is unambiguous and is
// taken; several are not, and guessing there is how the wrong terminal gets
// yanked, so nothing is moved.
func clientToSwitch(ctx context.Context, e *executor, callerSession string) (string, bool) {
	args := []string{"list-clients", "-F", "#{client_activity}\t#{client_name}"}
	if callerSession != "" {
		args = append(args, "-t", "="+callerSession)
	}
	out, err := e.run(ctx, args...)
	if err != nil || out == "" {
		return "", false
	}
	clients := strings.Split(out, "\n")
	if callerSession == "" && len(clients) > 1 {
		return "", false
	}
	client := newestClient(clients)
	return client, client != ""
}

// newestClient is the client with the latest activity stamp among
// "#{client_activity}\t#{client_name}" lines. A line without a stamp that
// parses (an older tmux wording the variable differently, say) is not dropped:
// it keeps its list position, so the worst case is the arbitrary-but-attached
// client the driver picked before it asked for the stamp at all.
func newestClient(lines []string) string {
	var (
		name   string
		newest int64
	)
	for _, line := range lines {
		stamp, client, ok := strings.Cut(line, "\t")
		if !ok {
			stamp, client = "", line
		}
		if client == "" {
			continue
		}
		at, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			at = -1
		}
		if name == "" || at > newest {
			name, newest = client, at
		}
	}
	return name
}
