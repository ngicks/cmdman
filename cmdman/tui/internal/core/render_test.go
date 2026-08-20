package core_test

import (
	"image/color"
	"testing"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// TestScaleCellIsAlwaysTwoCells pins the column's whole point: every row spends
// exactly two cells on the replica index, whether it has one or not, so the
// names after it line up down the list.
func TestScaleCellIsAlwaysTwoCells(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   core.CommandRow
		want  string
		cells int
	}{
		{"unscaled", core.CommandRow{}, "  ", 2},
		{"scaled but index zero", core.CommandRow{ScaleCount: 3}, "  ", 2},
		{"index 1", core.CommandRow{ScaleIndex: 1, ScaleCount: 3}, " 1", 2},
		{"index 2", core.CommandRow{ScaleIndex: 2, ScaleCount: 3}, " 2", 2},
		{"index 10", core.CommandRow{ScaleIndex: 10, ScaleCount: 12}, "10", 2},
		{"index 99", core.CommandRow{ScaleIndex: 99, ScaleCount: 100}, "99", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := core.ScaleCell(tc.row)
			if got != tc.want {
				t.Errorf("ScaleCell() = %q, want %q", got, tc.want)
			}
			if w := core.GlyphWidth(got); w != tc.cells {
				t.Errorf("ScaleCell() = %q is %d cells, want %d", got, w, tc.cells)
			}
		})
	}
}

// TestClampCells covers the cut and the two ways it can go wrong: a string that
// fits must not pay for an ellipsis, and a cut that lands on a wide glyph must
// drop it whole rather than hand back a column one cell too wide.
func TestClampCells(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    string
		w    int
		want string
	}{
		{"exact fit", "abc", 3, "abc"},
		{"one cell over", "abcd", 3, "ab…"},
		{"wide glyph at the cut", "漢字漢", 4, "漢…"},
		{"only the ellipsis fits", "abc", 1, "…"},
		{"no room at all", "abc", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := core.ClampCells(tc.s, tc.w)
			if got != tc.want {
				t.Errorf("ClampCells(%q, %d) = %q, want %q", tc.s, tc.w, got, tc.want)
			}
			if w := core.GlyphWidth(got); w > tc.w {
				t.Errorf("ClampCells(%q, %d) = %q is %d cells, over the limit",
					tc.s, tc.w, got, w)
			}
		})
	}
}

// TestRowNameStyle walks the state map the row name now carries, since it is
// the only place a row says what state it is in.
func TestRowNameStyle(t *testing.T) {
	weak := color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
	live := func(status string) core.CommandRow {
		return core.CommandRow{State: model.EventTypeRunning, Status: status}
	}
	for _, tc := range []struct {
		name  string
		row   core.CommandRow
		fg    color.Color
		bold  bool
		faint bool
	}{
		{"waiting", live(core.StatusWaiting),
			core.StyleMarkerBlocked.GetForeground(), true, false},
		{"working", live(core.StatusWorking),
			core.StyleMarkerWork.GetForeground(), false, false},
		{"idle", live("idle"), core.StyleMarkerIdle.GetForeground(), false, false},
		{"done", live(core.StatusDone), core.StyleMarkerIdle.GetForeground(), false, false},
		{"live but unreported", live(""), core.StyleMarkerIdle.GetForeground(), false, true},
		{"not live", core.CommandRow{State: model.EventTypeExited}, weak, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := core.RowNameStyle(tc.row, weak)
			if fg := got.GetForeground(); fg != tc.fg {
				t.Errorf("foreground = %v, want %v", fg, tc.fg)
			}
			if got.GetBold() != tc.bold {
				t.Errorf("bold = %v, want %v", got.GetBold(), tc.bold)
			}
			if got.GetFaint() != tc.faint {
				t.Errorf("faint = %v, want %v", got.GetFaint(), tc.faint)
			}
		})
	}
}

// TestRowNameStyleLeavesMarkerStylesAlone guards the bold and faint shades: they
// are derived from package-level styles the markers also render with, so a
// derivation that mutated its source would repaint every project dot.
func TestRowNameStyleLeavesMarkerStylesAlone(t *testing.T) {
	waiting := core.CommandRow{State: model.EventTypeRunning, Status: core.StatusWaiting}
	unreported := core.CommandRow{State: model.EventTypeRunning}
	_ = core.RowNameStyle(waiting, nil)
	_ = core.RowNameStyle(unreported, nil)
	if core.StyleMarkerBlocked.GetBold() {
		t.Error("StyleMarkerBlocked went bold")
	}
	if core.StyleMarkerIdle.GetFaint() {
		t.Error("StyleMarkerIdle went faint")
	}
}

// TestRowPayload pins what fills the row's title slot: the command's own words
// only while it is running, its lifecycle word otherwise.
func TestRowPayload(t *testing.T) {
	two := 2
	for _, tc := range []struct {
		name     string
		row      core.CommandRow
		wantText string
		wantLive bool
	}{
		{
			"running row shows its title",
			core.CommandRow{State: model.EventTypeRunning, Title: "building"},
			"building", true,
		},
		{
			"exited row shows its exit code, not its last report",
			core.CommandRow{State: model.EventTypeExited, ExitCode: &two, Title: "building"},
			"exited(2)", false,
		},
		{
			"pending row shows the action in flight",
			core.CommandRow{State: model.EventTypeRunning, Pending: "stopping", Title: "x"},
			"stopping…", false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, live := core.RowPayload(tc.row)
			if text != tc.wantText || live != tc.wantLive {
				t.Errorf("RowPayload() = (%q, %v), want (%q, %v)",
					text, live, tc.wantText, tc.wantLive)
			}
		})
	}
}
