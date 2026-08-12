package cli

import (
	"bytes"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/mux"
)

func TestBuildScaleColumn(t *testing.T) {
	win := mux.OwnedWindow{
		ScalePositions: map[string]int{"web": 2},
	}

	tests := []struct {
		name    string
		win     mux.OwnedWindow
		counts  map[string]int
		targets []string
		want    string
	}{
		{
			name:    "no targets falls back to stored positions",
			win:     win,
			counts:  nil,
			targets: nil,
			want:    "web=2",
		},
		{
			name:    "no targets and no stored positions",
			win:     mux.OwnedWindow{},
			counts:  nil,
			targets: nil,
			want:    "-",
		},
		{
			name:    "nil counts renders positions only",
			win:     win,
			counts:  nil,
			targets: []string{"web", "worker"},
			want:    "web=2 worker=1",
		},
		{
			name:    "resolved counts",
			win:     win,
			counts:  map[string]int{"web": 3, "worker": 2},
			targets: []string{"web", "worker"},
			want:    "web=2/3 worker=1/2",
		},
		{
			name:    "unresolvable count renders question mark",
			win:     win,
			counts:  map[string]int{"web": 3},
			targets: []string{"web", "worker"},
			want:    "web=2/3 worker=1/?",
		},
		{
			name:    "no stored positions default to 1",
			win:     mux.OwnedWindow{},
			counts:  map[string]int{"web": 3},
			targets: []string{"web"},
			want:    "web=1/3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, buildScaleColumn(tt.win, tt.counts, tt.targets), tt.want)
		})
	}
}

func TestRenderMuxWindowsScaleColumn(t *testing.T) {
	windows := []mux.OwnedWindow{{
		SessionName:    "main",
		WindowName:     "cmdman",
		WindowID:       "@3",
		Identity:       "aabb-myapp",
		Frame:          "dev",
		Marker:         1,
		ScalePositions: map[string]int{"web": 2},
	}}

	var out bytes.Buffer
	// Unsorted, duplicated targets: RenderMuxWindows sorts and dedups.
	err := RenderMuxWindows(
		&out, windows, map[string]int{"web": 3, "worker": 2},
		[]string{"worker", "web", "web"}, "",
	)
	assert.NilError(t, err)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 2, "output = %q", out.String())

	header := strings.Fields(lines[0])
	assert.DeepEqual(t, header, []string{
		"SESSION", "WINDOW", "ID", "IDENTITY", "FRAME", "LAYOUT", "SCALE",
	})

	// SCALE pairs contain internal spaces, so compare the leading fixed
	// columns and the trailing SCALE pairs separately.
	fields := strings.Fields(lines[1])
	want := []string{"main", "cmdman", "@3", "aabb-myapp", "dev", "1", "web=2/3", "worker=1/2"}
	assert.DeepEqual(t, fields, want)
}

// TestRenderMuxWindowsFrameColumn pins the FRAME column on the window the
// column exists for and on the one it does not: a frame shown around a window
// is named, and an unframed window reads "-" rather than a blank cell that
// would collapse into its neighbour under strings.Fields.
func TestRenderMuxWindowsFrameColumn(t *testing.T) {
	windows := []mux.OwnedWindow{
		{SessionName: "main", WindowName: "cmdman", WindowID: "@3", Identity: "p", Marker: -1},
		{SessionName: "main", WindowName: "chrome", WindowID: "@4", Frame: "dev", Marker: -1},
	}

	var out bytes.Buffer
	err := RenderMuxWindows(&out, windows, nil, nil, "")
	assert.NilError(t, err)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())

	assert.DeepEqual(
		t,
		strings.Fields(lines[1]),
		[]string{"main", "cmdman", "@3", "p", "-", "-", "-"},
	)
	// A frame-only window carries no identity, so its IDENTITY cell is blank
	// and only the frame name distinguishes the row.
	assert.DeepEqual(t, strings.Fields(lines[2]), []string{"main", "chrome", "@4", "dev", "-", "-"})
}
