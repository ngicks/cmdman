package tmux

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// markerSuffix matches a trailing "#<digits>" segment on a pane border
// title. The base-title portion (group 1) may contain '#' freely; the
// suffix is the stable parse anchor.
var markerSuffix = regexp.MustCompile(`^(.*)#(\d+)$`)

// StatWindow returns the muxctl-recognized embedded data parsed from the
// border titles of the panes in windowID. The window does not have to be
// the Session's own controlled window — callers probe other windows to
// decide "is this someone else's muxctl window".
//
// Parse rules (see also doc on [muxctl.WindowStat]):
//
//   - For each pane, the title is split into (base, marker) by stripping a
//     trailing "#<digits>" suffix; PaneNames receives the base.
//   - WindowStat.Marker is the marker shared by ALL panes that had one;
//     -1 when no pane carried a parseable suffix, or panes disagree, or
//     the window has zero panes.
func (s *Session) StatWindow(ctx context.Context, windowID string) (muxctl.WindowStat, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", windowID, "-F", "#{pane_title}",
	)
	if err != nil {
		return muxctl.WindowStat{}, fmt.Errorf(
			"tmux: list panes for window %s: %w", windowID, err,
		)
	}
	if out == "" {
		return muxctl.WindowStat{Marker: -1}, nil
	}
	titles := strings.Split(out, "\n")
	names := make([]string, 0, len(titles))
	marker := -1
	consistent := true
	sawAnyMarker := false
	for _, t := range titles {
		m := markerSuffix.FindStringSubmatch(t)
		if m == nil {
			names = append(names, t)
			// A pane without a marker breaks consistency with any
			// marker-bearing pane we have already seen.
			if sawAnyMarker {
				consistent = false
			}
			continue
		}
		base := m[1]
		n, err := strconv.Atoi(m[2])
		if err != nil {
			// Should be impossible given the regex, but be defensive.
			names = append(names, t)
			continue
		}
		names = append(names, base)
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
