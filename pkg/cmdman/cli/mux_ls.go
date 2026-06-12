package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// muxLsRow is the data model for both the built-in mux ls table and a user
// --format template. It embeds the original OwnedWindow row (so its fields
// and {{json .}} keep working) and adds the precomputed column widths via
// tableMeta (.W, .Win), which carries json:"-" tags and is therefore excluded
// from {{json .}} output. Scale holds the precomputed SCALE column value.
type muxLsRow struct {
	mux.OwnedWindow
	tableMeta
	// Scale is the precomputed SCALE column value for this window.
	// Format: space-separated "cmd=pos/count" (or "cmd=pos" when counts are
	// unavailable), sorted by command name. "-" when the window has no cycle
	// targets in the spec.
	Scale string
}

// DefaultMuxLsRowFormat renders one mux ls row: SESSION, WINDOW, ID, IDENTITY,
// LAYOUT each laid out with "cell" against the widths the model already
// measured; SCALE is the trailing column and runs through "fit" so it is
// truncated to the terminal width remaining after the fixed columns. One column
// per source line keeps the template readable; {{- -}} trims the joins to a
// single line (renderTemplate appends the newline).
const DefaultMuxLsRowFormat = `{{- cell .SessionName .W.Session -}}
{{- cell .WindowName .W.Window -}}
{{- cell .WindowID .W.ID -}}
{{- cell .Identity .W.Identity -}}
{{- cell (muxMarker .Marker) .W.Layout -}}
{{- fit .Scale .Win .W.Used -}}`

// muxTemplateFuncMap extends templateFuncMap with the muxMarker helper so that
// both the built-in DefaultMuxLsRowFormat and any user-supplied --format can
// call {{muxMarker .Marker}}.
var muxTemplateFuncMap = func() template.FuncMap {
	m := make(template.FuncMap, len(templateFuncMap)+1)
	maps.Copy(m, templateFuncMap)
	m["muxMarker"] = func(marker int) string {
		if marker < 0 {
			return "-"
		}
		return strconv.Itoa(marker)
	}
	return m
}()

// renderMuxTemplate applies format to each muxLsRow using muxTemplateFuncMap,
// which extends the shared template helpers with muxMarker. Both the built-in
// table and user --format strings can call {{muxMarker .Marker}}.
func renderMuxTemplate(out io.Writer, rows []muxLsRow, format string) error {
	tmpl, err := template.New("format").Funcs(muxTemplateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	for _, row := range rows {
		if err := tmpl.Execute(out, row); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(out)
	}
	return nil
}

// buildScaleColumn computes the SCALE column value for a single window.
//
//   - cycleTargets is the sorted, deduplicated list of unpinned command names
//     from the spec (commands that participate in cycle-scale). When empty
//     (standalone mux ls, which has no spec-derived targets), the window's
//     stored positions are rendered instead; "-" when none are stored.
//   - replicaCounts maps command name to its live replica count. Nil means
//     counts are not available (standalone mux ls); entries render as
//     "cmd=pos" without a "/count" suffix. Non-nil with a command absent means
//     the count could not be resolved; the entry renders as "cmd=pos/?".
//   - positions are read from win.ScalePositions; absent commands default to 1.
func buildScaleColumn(
	win mux.OwnedWindow,
	replicaCounts map[string]int,
	cycleTargets []string,
) string {
	if len(cycleTargets) == 0 {
		// No spec-derived targets: fall back to the positions stored on the
		// window itself so standalone mux ls still surfaces them.
		cycleTargets = slices.Sorted(maps.Keys(win.ScalePositions))
		if len(cycleTargets) == 0 {
			return "-"
		}
	}

	parts := make([]string, 0, len(cycleTargets))
	for _, cmd := range cycleTargets {
		pos := 1
		if win.ScalePositions != nil {
			if sp, ok := win.ScalePositions[cmd]; ok {
				pos = sp
			}
		}
		switch count, ok := replicaCounts[cmd]; {
		case replicaCounts == nil:
			parts = append(parts, fmt.Sprintf("%s=%d", cmd, pos))
		case ok:
			parts = append(parts, fmt.Sprintf("%s=%d/%d", cmd, pos, count))
		default:
			parts = append(parts, fmt.Sprintf("%s=%d/?", cmd, pos))
		}
	}
	return strings.Join(parts, " ")
}

// RenderMuxWindows renders []mux.OwnedWindow as a tabular listing to out.
// format selects the output style:
//   - "" or "table": the default width-aware aligned table with a header row.
//   - anything else: a Go text/template applied per row (same enriched muxLsRow
//     model as the built-in table; no header is printed).
//
// The columns are SESSION, WINDOW, ID, IDENTITY, LAYOUT, SCALE.
// A Marker of -1 is displayed as "-" (no layout applied yet).
//
// replicaCounts maps command name to its live replica count. When nil, the
// SCALE column shows positions without counts ("cmd=pos"). When the caller
// resolves counts eagerly (compose mux ls), pass the resolved map.
//
// cycleTargets is the sorted list of unpinned command names from the spec (the
// commands that participate in cycle-scale). When empty or nil (standalone mux
// ls), each window falls back to its stored positions; windows with none show
// "-". The caller derives this from the spec's unpinned leaves.
//
// The extra template function "muxMarker" is available in user --format strings
// in addition to the standard helpers.
func RenderMuxWindows(
	out io.Writer,
	windows []mux.OwnedWindow,
	replicaCounts map[string]int,
	cycleTargets []string,
	format string,
) error {
	// Ensure cycleTargets is sorted and deduplicated for deterministic output.
	targets := slices.Clone(cycleTargets)
	slices.Sort(targets)
	targets = slices.Compact(targets)

	w := measureMuxLs(windows)
	meta := tableMeta{W: w, Win: terminalWidth(out)}
	rows := make([]muxLsRow, len(windows))
	for i, win := range windows {
		rows[i] = muxLsRow{
			OwnedWindow: win,
			tableMeta:   meta,
			Scale:       buildScaleColumn(win, replicaCounts, targets),
		}
	}

	if format == "" || format == "table" {
		fmt.Fprintln(out,
			cell("SESSION", w["Session"])+
				cell("WINDOW", w["Window"])+
				cell("ID", w["ID"])+
				cell("IDENTITY", w["Identity"])+
				cell("LAYOUT", w["Layout"])+
				"SCALE",
		)
		format = DefaultMuxLsRowFormat
	}
	return renderMuxTemplate(out, rows, format)
}

// measureMuxLs computes the longest display width of each column (header
// included). The "Used" key records the total width the five fixed columns and
// their gaps consume before the trailing SCALE column.
func measureMuxLs(windows []mux.OwnedWindow) map[string]int {
	w := map[string]int{
		"Session":  width("SESSION"),
		"Window":   width("WINDOW"),
		"ID":       width("ID"),
		"Identity": width("IDENTITY"),
		"Layout":   width("LAYOUT"),
	}
	for _, win := range windows {
		w["Session"] = max(w["Session"], width(win.SessionName))
		w["Window"] = max(w["Window"], width(win.WindowName))
		w["ID"] = max(w["ID"], width(win.WindowID))
		w["Identity"] = max(w["Identity"], width(win.Identity))
		// Marker is rendered through muxMarker: "-" or strconv.Itoa(marker).
		var markerStr string
		if win.Marker < 0 {
			markerStr = "-"
		} else {
			markerStr = strconv.Itoa(win.Marker)
		}
		w["Layout"] = max(w["Layout"], width(markerStr))
	}
	w["Used"] = w["Session"] + w["Window"] + w["ID"] + w["Identity"] + w["Layout"] +
		5*len(columnGap)
	return w
}

// MuxLsFormatUsage returns the --format usage string for mux ls / compose mux ls.
func MuxLsFormatUsage() string {
	return `Output format: "table" (default) or a Go text/template string.
Template fields:
  .SessionName (string), .WindowName (string), .WindowID (string),
  .Identity (string), .Marker (int, -1 = no layout applied),
  .Scale (string, precomputed SCALE column: "cmd=pos/count" pairs or "-")
Extra template function: muxMarker (renders Marker as "-" when -1)
Note: compose mux ls resolves replica counts; standalone mux ls shows pos only.
Template functions: ` + templateFuncList()
}
