package cli

import (
	"fmt"
	"io"
	"maps"
	"strconv"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// muxLsRow is the data model for both the built-in mux ls table and a user
// --format template. It embeds the original OwnedWindow row (so its fields
// and {{json .}} keep working) and adds the precomputed column widths via
// tableMeta (.W, .Win), which carries json:"-" tags and is therefore excluded
// from {{json .}} output.
type muxLsRow struct {
	mux.OwnedWindow
	tableMeta
}

// DefaultMuxLsRowFormat renders one mux ls row: SESSION, WINDOW, ID, IDENTITY
// each laid out with "cell" against the widths the model already measured;
// LAYOUT is the trailing column and runs through "fit" so it is truncated to
// the terminal width remaining after the fixed columns. One column per source
// line keeps the template readable; {{- -}} trims the joins to a single line
// (renderTemplate appends the newline).
const DefaultMuxLsRowFormat = `{{- cell .SessionName .W.Session -}}
{{- cell .WindowName .W.Window -}}
{{- cell .WindowID .W.ID -}}
{{- cell .Identity .W.Identity -}}
{{- fit (muxMarker .Marker) .Win .W.Used -}}`

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

// RenderMuxWindows renders []mux.OwnedWindow as a tabular listing to out.
// format selects the output style:
//   - "" or "table": the default width-aware aligned table with a header row.
//   - anything else: a Go text/template applied per row (same enriched muxLsRow
//     model as the built-in table; no header is printed).
//
// The columns are SESSION, WINDOW, ID, IDENTITY, LAYOUT. A Marker of -1 is
// displayed as "-" (no layout applied yet). The extra template function
// "muxMarker" is available in user --format strings in addition to the standard
// helpers.
func RenderMuxWindows(out io.Writer, windows []mux.OwnedWindow, format string) error {
	w := measureMuxLs(windows)
	meta := tableMeta{W: w, Win: terminalWidth(out)}
	rows := make([]muxLsRow, len(windows))
	for i, win := range windows {
		rows[i] = muxLsRow{OwnedWindow: win, tableMeta: meta}
	}

	if format == "" || format == "table" {
		fmt.Fprintln(out,
			cell("SESSION", w["Session"])+
				cell("WINDOW", w["Window"])+
				cell("ID", w["ID"])+
				cell("IDENTITY", w["Identity"])+
				"LAYOUT",
		)
		format = DefaultMuxLsRowFormat
	}
	return renderMuxTemplate(out, rows, format)
}

// measureMuxLs computes the longest display width of each column (header
// included). The "Used" key records the total width the four fixed columns and
// their gaps consume before the trailing LAYOUT column.
func measureMuxLs(windows []mux.OwnedWindow) map[string]int {
	w := map[string]int{
		"Session":  width("SESSION"),
		"Window":   width("WINDOW"),
		"ID":       width("ID"),
		"Identity": width("IDENTITY"),
	}
	for _, win := range windows {
		w["Session"] = max(w["Session"], width(win.SessionName))
		w["Window"] = max(w["Window"], width(win.WindowName))
		w["ID"] = max(w["ID"], width(win.WindowID))
		w["Identity"] = max(w["Identity"], width(win.Identity))
	}
	w["Used"] = w["Session"] + w["Window"] + w["ID"] + w["Identity"] + 4*len(columnGap)
	return w
}

// MuxLsFormatUsage returns the --format usage string for mux ls / compose mux ls.
func MuxLsFormatUsage() string {
	return `Output format: "table" (default) or a Go text/template string.
Template fields:
  .SessionName (string), .WindowName (string), .WindowID (string),
  .Identity (string), .Marker (int, -1 = no layout applied)
Extra template function: muxMarker (renders Marker as "-" when -1)
Template functions: ` + templateFuncList()
}
