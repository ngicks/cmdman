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
//   - PaneNames receives each pane's border title verbatim.
//   - WindowStat.Marker is the marker shared by ALL panes that carry one; -1
//     when no pane carries a marker, panes disagree, or the window has zero
//     panes.
func (s *Session) StatWindow(ctx context.Context, windowID string) (muxctl.WindowStat, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", windowID,
		"-F", "#{"+markerOption+"}\t#{pane_title}",
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
	for _, line := range lines {
		// The format is "<marker>\t<title>"; an unset option expands to "".
		markerStr, title, _ := strings.Cut(line, "\t")
		names = append(names, title)

		n, err := strconv.Atoi(markerStr)
		if markerStr == "" || err != nil {
			// A pane without a (numeric) marker breaks consistency with any
			// marker-bearing pane we have already seen.
			if sawAnyMarker {
				consistent = false
			}
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
	if !sawAnyMarker || !consistent {
		marker = -1
	}
	return muxctl.WindowStat{Marker: marker, PaneNames: names}, nil
}
