package tmux

import (
	"context"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ListWindows enumerates tmux windows that carry cmdman state — the
// @cmdman_window ownership stamp, the @cmdman_frame_def frame record, or both
// — server-wide (or restricted to one session via opts.Session), and
// optionally filtered to an exact identity value. It implements
// [muxctl.Server.ListWindows] for tmux.
//
// Both slots are read in the same round-trip and reported on the row, so a
// window a project teardown left framed (its ownership stamp cleared, its
// frame still shown) stays enumerable, while an identity filter keeps matching
// the ownership slot alone.
//
// When no tmux server is running on the target socket, list-windows exits
// non-zero with a "no server" message; this is treated as zero results rather
// than an error — the caller asked "what dashboards are up?" and the answer is
// simply "none, the server is not running".
//
// Each returned row includes the per-window layout marker from StatWindow so
// callers can display the active layout index alongside the identity, plus the
// inline per-window state values for the keys named in opts.StateKeys (each key
// mapped to the window option @cmdman_<key>, fetched in the same list-windows
// round-trip). A requested key whose option is unset maps to "".
func (srv *Server) ListWindows(
	ctx context.Context,
	opts muxctl.ListOptions,
) ([]muxctl.Window, error) {
	e := srv.exec

	// Build the list-windows invocation. -a scans all sessions; -t <session>
	// restricts to one. The format delivers all per-window fields we need in
	// one round-trip; we read the marker per-window below.
	//
	// The base format string fetches six tab-separated fields per window, then
	// one field per requested state key. #{@cmdman_<key>} expands as a window
	// option directly in list-windows -F, so no extra tmux call is needed to
	// read the requested state (e.g. the cycle-scale positions). The frame def
	// and the created marker ride along as base fields rather than as state
	// keys: they are part of every row, so no caller has to ask for them. A
	// caller that names one in opts.StateKeys simply gets the same option twice.
	var fmtBuf strings.Builder
	fmtBuf.WriteString(
		"#{window_id}\t#{session_name}\t#{window_name}\t#{" + ownerOption + "}" +
			"\t#{" + frameDefOption + "}\t#{" + createdOption + "}",
	)
	for _, key := range opts.StateKeys {
		fmtBuf.WriteString("\t#{" + stateOption(key) + "}")
	}
	fmtStr := fmtBuf.String()
	var args []string
	if opts.Session != "" {
		args = []string{"list-windows", "-t", opts.Session, "-F", fmtStr}
	} else {
		args = []string{"list-windows", "-a", "-F", fmtStr}
	}

	out, err := e.run(ctx, args...)
	if err != nil {
		// Three classes of "benign" errors are treated as zero results:
		//
		//   1. "no server running" / "error connecting to" / "No such file or
		//      directory" — the tmux server is simply not up on this socket.
		//      The caller asked "what dashboards are up?" and the answer is "none".
		//
		//   2. "can't find session" — opts.Session named a session that does not
		//      exist. Listing dashboards in an absent session means "none" for the
		//      same reason: it is a valid query with a well-defined empty answer,
		//      not a programming error. (Distinct from a genuinely bad invocation
		//      like a malformed flag, which surfaces below as a real error.)
		msg := err.Error()
		if strings.Contains(msg, "no server running") ||
			strings.Contains(msg, "error connecting") ||
			strings.Contains(msg, "No such file or directory") ||
			strings.Contains(msg, "can't find session") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	// nFields is the number of tab-separated fields the format string emits:
	// six base fields plus one per state key.
	nFields := 6 + len(opts.StateKeys)

	var rows []muxctl.Window
	for line := range strings.SplitSeq(out, "\n") {
		// tmux strips trailing empty fields from -F output: a borrowed unframed
		// window with no requested state option set yields fewer fields. SplitN
		// keeps internal empty fields, and the guards below default any missing
		// trailing field (the frame slot, the created marker, a state value, the
		// whole state map) to empty.
		parts := strings.SplitN(line, "\t", nFields)
		if len(parts) < 4 {
			continue
		}
		wid, session, wname, identity := parts[0], parts[1], parts[2], parts[3]
		frame := ""
		if len(parts) > 4 {
			frame = parts[4]
		}
		created := false
		if len(parts) > 5 {
			created = parts[5] == createdStamp
		}
		if identity == "" && frame == "" {
			// Window carries neither identity — not ours.
			continue
		}
		if opts.Identity != "" && identity != opts.Identity {
			// Caller requested a specific project; the frame slot never
			// answers for it, so a framed window of another project (or of
			// none) is skipped like any other.
			continue
		}

		var state map[muxctl.StateKey]string
		if len(opts.StateKeys) > 0 {
			state = make(map[muxctl.StateKey]string, len(opts.StateKeys))
			for i, key := range opts.StateKeys {
				v := ""
				if 6+i < len(parts) {
					v = parts[6+i]
				}
				state[key] = v
			}
		}

		// Read the layout marker for this window. We construct a throwaway
		// Session purely to call StatWindow — it needs no state beyond the
		// executor (the window id is passed as the argument).
		tmp := &Session{exec: e}
		stat, err := tmp.StatWindow(ctx, wid)
		marker := -1
		if err == nil {
			marker = stat.Marker
		}

		rows = append(rows, muxctl.Window{
			SessionName: session,
			WindowID:    wid,
			WindowName:  wname,
			Identity:    identity,
			Frame:       frame,
			Created:     created,
			Marker:      marker,
			State:       state,
		})
	}
	return rows, nil
}
