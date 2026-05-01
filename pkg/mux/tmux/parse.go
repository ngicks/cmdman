package tmux

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ngicks/crabswarm/pkg/mux"
)

// parseWindows parses tmux list-windows output formatted as "#{window_id}\t#{window_index}\t#{window_name}".
// sessionStartupKeys and windowKeys are injected into each constructed window.
func parseWindows(out string, sessionName string, exec *executor, sessionStartupKeys []string, windowKeys map[string][]string, sessionID string) []mux.Window {
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	windows := make([]mux.Window, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		wid := parts[0]
		w := &window{
			id:                 wid,
			sessionID:          sessionID,
			sessionName:        sessionName,
			exec:               exec,
			sessionStartupKeys: sessionStartupKeys,
		}
		if wk, ok := windowKeys[wid]; ok {
			w.startupKeys = slices.Clone(wk)
		}
		windows = append(windows, w)
	}
	return windows
}

// parsePanes parses tmux list-panes output formatted as "#{pane_id}\t#{pane_index}".
func parsePanes(out string, sessionID, windowID string, exec *executor) []mux.Pane {
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	panes := make([]mux.Pane, 0, len(lines))
	for _, line := range lines {
		id, _, err := parsePaneInfo(line)
		if err != nil {
			continue
		}
		panes = append(panes, &pane{
			id:        id,
			sessionID: sessionID,
			windowID:  windowID,
			exec:      exec,
		})
	}
	return panes
}

// parsePaneInfo parses a single line of "#{pane_id}\t#{pane_index}" format.
func parsePaneInfo(line string) (id string, index int, err error) {
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) < 2 {
		return "", 0, fmt.Errorf("invalid pane info: %q", line)
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid pane index %q: %w", parts[1], err)
	}
	return parts[0], idx, nil
}

// parsePaneIDs parses tmux list-panes output formatted as "#{pane_id}" (one per line).
func parsePaneIDs(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
