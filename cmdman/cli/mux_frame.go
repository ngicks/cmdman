package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman/mux"
)

// frameLsRow is one line of the mux frame ls table: a def and the windows it is
// shown around.
type frameLsRow struct {
	Def string
	// Windows is the WINDOWS column: the driver-native ids the def is shown
	// around, space separated, or "-" when it is shown nowhere.
	Windows string
}

// RenderFrameList writes [mux.FrameListResult] as the mux frame ls table: one
// row per def, with the windows it is shown around.
//
// The table is def-centric where mux ls is window-centric (its FRAME column
// answers "what is on this window"), so the two listings complement each other
// rather than repeat: this one answers "what can I show, and where is it up".
//
// A def is listed when it is discoverable on disk or shown around a window, so
// a def shown by path — or one deleted since it was shown — still appears
// instead of vanishing from a listing while standing on screen.
func RenderFrameList(out io.Writer, res mux.FrameListResult) error {
	rows := frameLsRows(res)

	w := width("DEF")
	for _, r := range rows {
		w = max(w, width(r.Def))
	}

	if _, err := fmt.Fprintln(out, cell("DEF", w)+"WINDOWS"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintln(out, cell(r.Def, w)+r.Windows); err != nil {
			return err
		}
	}
	return nil
}

// frameLsRows turns the two halves of a [mux.FrameListResult] into the table's
// rows: the union of the defs on disk and the defs shown somewhere, sorted, each
// carrying the window ids it is shown around.
//
// Both orderings are lexical, which for the ids is determinism rather than
// meaning: a window id is the driver's own opaque handle (tmux "@10" sorts
// before "@2"), so the column promises a stable listing, not a window order.
func frameLsRows(res mux.FrameListResult) []frameLsRow {
	windows := make(map[string][]string, len(res.Shown))
	for windowID, def := range res.Shown {
		windows[def] = append(windows[def], windowID)
	}

	names := slices.Sorted(maps.Keys(windows))
	names = append(names, res.Defs...)
	slices.Sort(names)
	names = slices.Compact(names)

	rows := make([]frameLsRow, len(names))
	for i, name := range names {
		shown := "-"
		if ids := windows[name]; len(ids) > 0 {
			slices.Sort(ids)
			shown = strings.Join(ids, " ")
		}
		rows[i] = frameLsRow{Def: name, Windows: shown}
	}
	return rows
}
