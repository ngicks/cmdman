package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// TestModelJSONHidesLayoutFields verifies that the enriched row model handed to
// templates marshals via {{json .}} exactly like the bare embedded input — the
// layout fields (W, Win) are json:"-" and never leak — while still being
// reachable from a user --format through field promotion.
func TestModelJSONHidesLayoutFields(t *testing.T) {
	ec := 7
	entry := store.CommandEntry{
		ID:       "abc123",
		Name:     "svc",
		State:    model.EventTypeExited,
		ExitCode: &ec,
	}
	row := lsRow{
		CommandEntry: entry,
		tableMeta:    tableMeta{W: map[string]int{"ID": 9}, Win: 80},
	}

	var got bytes.Buffer
	err := renderTemplate(&got, []lsRow{row}, `{{json .}}`)
	assert.NilError(t, err)

	want, err := json.Marshal(entry)
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimRight(got.String(), "\n"), string(want))
	assert.Assert(t, !strings.Contains(got.String(), `"W"`), "W leaked: %s", got.String())
	assert.Assert(t, !strings.Contains(got.String(), `"Win"`), "Win leaked: %s", got.String())

	// The embedded fields and the layout are both reachable from a template.
	var promoted bytes.Buffer
	err = renderTemplate(&promoted, []lsRow{row}, `{{.Name}} win={{.Win}} idw={{.W.ID}}`)
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimRight(promoted.String(), "\n"), "svc win=80 idw=9")
}

// columnStarts returns the display-cell offset at which each column begins. A
// run of two or more spaces is treated as the inter-column gap (the table uses
// three), so a single space inside a cell — e.g. the "EXIT CODE" header — does
// not split it. Rows whose columns are aligned share these offsets.
func columnStarts(line string) []int {
	var starts []int
	col, spaceRun, atStart := 0, 0, true
	for _, r := range line {
		if r == ' ' {
			spaceRun++
		} else {
			if atStart || spaceRun >= 2 {
				starts = append(starts, col)
			}
			spaceRun, atStart = 0, false
		}
		col += runewidth.RuneWidth(r)
	}
	return starts
}

// TestDefaultLsFormatAligns renders entries whose cells vary in width (including
// a double-width CJK name) and asserts every row's columns begin at the same
// display offsets as the header's.
func TestDefaultLsFormatAligns(t *testing.T) {
	var out bytes.Buffer
	err := RenderEntries(&out, []store.CommandEntry{
		{
			ID:         "a1b2c3d4e5f6aaaa",
			Name:       "web",
			State:      model.EventTypeRunning,
			ConfigJSON: &model.CommandConfig{Argv: []string{"/usr/bin/node"}},
		},
		{
			ID:         "ff00",
			Name:       "日本語サーバ", // 6 runes, 12 display cells
			State:      model.EventTypeExited,
			ExitCode:   new(137),
			ConfigJSON: &model.CommandConfig{Argv: []string{"/bin/sh"}},
		},
	}, false, "")
	assert.NilError(t, err)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())

	want := columnStarts(lines[0]) // header offsets
	assert.Assert(t, len(want) == 5, "header columns = %v", want)
	for _, l := range lines[1:] {
		assert.DeepEqual(t, columnStarts(l), want)
	}
}

func TestFitColumn(t *testing.T) {
	const s = "/usr/bin/some-really-long-command --flag"

	// fitColumn(s, win, used): "used" cells are already consumed by the fixed
	// columns; the rest of the win-cell line is left for s.

	// Unknown width: unchanged.
	assert.Equal(t, fitColumn(s, 0, 10), s)
	// Plenty of room: unchanged.
	assert.Equal(t, fitColumn(s, 200, 10), s)

	// Tight: truncated to the remaining cells with an ellipsis.
	got := fitColumn(s, 20, 10)
	assert.Assert(t, runewidth.StringWidth(got) <= 10, "got %q", got)
	assert.Assert(t, strings.HasSuffix(got, "…"), "got %q", got)

	// No room left (fixed columns already overflow): left untouched rather than
	// cut to nothing.
	assert.Equal(t, fitColumn(s, 20, 25), s)
}

func TestTemplateWidthHelpers(t *testing.T) {
	fm := templateFuncMap
	assert.Equal(t, fm["width"].(func(string) int)("日本"), 4)
	assert.Equal(t, fm["pad"].(func(string, int) string)("hi", 5), "hi   ")
	assert.Equal(t, fm["cell"].(func(string, int) string)("hi", 4), "hi  "+columnGap)
	assert.Equal(t, fm["shortID"].(func(string) string)("0123456789abcdef"), "0123456789ab")
	assert.Equal(t, fm["exitCode"].(func(*int) string)(nil), "-")
	assert.Equal(t, fm["exitCode"].(func(*int) string)(new(0)), "0")
	assert.Equal(t, fm["join"].(func(string, []string) string)(" ", []string{"a", "b"}), "a b")
}
