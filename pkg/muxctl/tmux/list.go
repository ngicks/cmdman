package tmux

import (
	"context"
	"strings"
)

// OwnedWindow is a row returned by [ListOwnedWindows]: it describes a single
// tmux window that carries the @cmdman_window ownership stamp.
type OwnedWindow struct {
	// SessionName is the tmux session the window belongs to.
	SessionName string
	// WindowID is the tmux window @id (e.g. "@3").
	WindowID string
	// WindowName is the human-visible tmux window name (may differ from the
	// identity — takeover windows keep their original name).
	WindowName string
	// Identity is the value of @cmdman_window — the opaque string the caller
	// supplied as [Config.OwnedIdentity] when building this dashboard. The
	// driver stores and returns it verbatim; upper layers interpret it.
	Identity string
	// Marker is the layout index last applied to this window (see
	// [Session.StatWindow]), or -1 when no layout has been applied yet or the
	// panes carry inconsistent markers. Reading the marker requires an extra
	// list-panes call per window; it is -1 when the server returns no panes.
	Marker int
	// ScalePositions holds the per-command cycle-scale positions decoded from
	// the window's @cmdman_scale option (command name → 1-based replica
	// position). Absent commands default to position 1 at consumption time.
	// Nil when the option is unset or empty.
	ScalePositions map[string]int
}

// ListOwnedWindowsOptions controls which windows [ListOwnedWindows] returns.
type ListOwnedWindowsOptions struct {
	// Path overrides the tmux binary path. Defaults to "tmux".
	Path string

	// Socket selects the tmux server by socket name (-L). Empty uses the
	// default socket (or the server inherited from $TMUX).
	Socket string

	// Session, when non-empty, restricts the scan to that session only
	// (list-windows -t <session> instead of list-windows -a). Empty scans
	// all sessions on the server.
	Session string

	// Identity, when non-empty, filters the results to windows whose
	// @cmdman_window option equals this string exactly. Empty returns all
	// stamped windows regardless of identity.
	Identity string
}

// ListOwnedWindows enumerates tmux windows that carry the @cmdman_window
// ownership stamp, server-wide (or restricted to one session via
// opts.Session), and optionally filtered to an exact identity value.
//
// When no tmux server is running on the target socket, list-windows exits
// non-zero with a "no server" message; this is treated as zero results rather
// than an error — the caller asked "what dashboards are up?" and the answer is
// simply "none, the server is not running".
//
// Each returned row includes the per-window layout marker from StatWindow so
// callers can display the active layout index alongside the identity.
func ListOwnedWindows(
	ctx context.Context,
	opts ListOwnedWindowsOptions,
) ([]OwnedWindow, error) {
	e := newExecutor(opts.Path, opts.Socket)

	// Build the list-windows invocation. -a scans all sessions; -t <session>
	// restricts to one. The format delivers all per-window fields we need in
	// one round-trip; we read the marker per-window below.
	// The format string fetches five tab-separated fields per window.
	// #{@cmdman_scale} expands as a window option directly in list-windows -F,
	// so no extra tmux call is needed to read the scale positions.
	const fmtStr = "#{window_id}\t#{session_name}\t#{window_name}\t#{" +
		ownerOption + "}\t#{" + scaleOption + "}"
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

	var rows []OwnedWindow
	for line := range strings.SplitSeq(out, "\n") {
		// tmux strips trailing empty fields from -F output: a window without
		// @cmdman_scale set yields 4 fields instead of 5. Accept both.
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		wid, session, wname, identity := parts[0], parts[1], parts[2], parts[3]
		scaleRaw := ""
		if len(parts) == 5 {
			scaleRaw = parts[4]
		}
		if identity == "" {
			// Window has no ownership stamp — not ours.
			continue
		}
		if opts.Identity != "" && identity != opts.Identity {
			// Caller requested a specific identity; skip non-matching rows.
			continue
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

		rows = append(rows, OwnedWindow{
			SessionName:    session,
			WindowID:       wid,
			WindowName:     wname,
			Identity:       identity,
			Marker:         marker,
			ScalePositions: decodeScalePositions(scaleRaw),
		})
	}
	return rows, nil
}
