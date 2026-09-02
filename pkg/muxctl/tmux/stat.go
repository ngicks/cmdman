package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// StatWindow returns the muxctl-recognized embedded data read from the panes
// in windowID. The window does not have to be the Session's own controlled
// window — callers probe other windows to decide "is this someone else's
// muxctl window".
//
// The layout marker lives in the per-pane user option markerOption
// ("@cmdman_marker"); the pane border title carries only the human-readable
// pane name (see [applyState.realizeLeafAt] for why the marker moved off the
// title).
//
// Parse rules (see also doc on [muxctl.WindowStat]):
//
//   - PaneNames receives each pane's border title verbatim, frame panes
//     included: it reports what the window holds, not who owns it.
//   - WindowStat.Marker is the marker shared by ALL project panes; -1 when no
//     project pane carries a marker, they disagree, or the window has zero
//     panes. A project pane is one cmdman stamped: it carries markerOption or
//     leafOption. Frame panes (frameOption) never vote: they are not part of
//     the layout the marker indexes, so an unmarked — or stale — frame pane
//     must not read as a project window mid-rebuild. A pane carrying none of
//     the three stamps is foreign — a shell the user split off, a floating
//     pane a plugin joined into the window — and neither votes nor breaks
//     agreement; a leaf-stamped pane that lost its marker still does.
func (s *Session) StatWindow(ctx context.Context, windowID string) (muxctl.WindowStat, error) {
	// pane_id leads only to keep the line from starting with the separator:
	// the executor trims its output, so an empty first field would be eaten
	// and shift every later field on the first pane.
	out, err := s.exec.run(
		ctx, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{"+frameOption+"}\t#{"+markerOption+"}\t#{"+leafOption+
			"}\t#{pane_title}",
	)
	if err != nil {
		return muxctl.WindowStat{}, fmt.Errorf(
			"tmux: list panes for window %s: %w", windowID, err,
		)
	}
	if out == "" {
		return muxctl.WindowStat{Marker: -1}, nil
	}
	lines := strings.Split(out, "\n")
	names := make([]string, 0, len(lines))
	marker := -1
	consistent := true
	sawAnyMarker := false
	sawUnmarkedProject := false
	for _, line := range lines {
		_, rest, _ := strings.Cut(line, "\t")
		frame, rest, _ := strings.Cut(rest, "\t")
		markerStr, rest, _ := strings.Cut(rest, "\t")
		leaf, title, _ := strings.Cut(rest, "\t")
		names = append(names, title)

		if frame != "" || (markerStr == "" && leaf == "") {
			continue
		}

		n, err := strconv.Atoi(markerStr)
		if markerStr == "" || err != nil {
			// A project pane without a (numeric) marker breaks consistency
			// with any marker-bearing pane, listed before or after it.
			sawUnmarkedProject = true
			continue
		}
		if !sawAnyMarker {
			marker = n
			sawAnyMarker = true
			continue
		}
		if n != marker {
			consistent = false
		}
	}
	if !sawAnyMarker || !consistent || sawUnmarkedProject {
		marker = -1
	}
	return muxctl.WindowStat{Marker: marker, PaneNames: names}, nil
}
