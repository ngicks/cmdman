package cli

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// lsRow is the data model for both the built-in ls table and a user --format.
// It embeds the original entry (so .ID, .Name, … and {{json .}} keep working)
// and adds the table layout via tableMeta (.W, .Win), which is json:"-".
type lsRow struct {
	store.CommandEntry
	tableMeta
}

// DefaultLsRowFormat renders one ls row from an lsRow: each column is laid out
// with the shared "cell" helper (pad to the width the model already measured +
// gap), and the trailing COMMAND column runs through "fit" so it is truncated
// to whatever terminal width is left. One column per line keeps the template
// readable; {{- -}} trims the joins so the row prints on a single line
// (renderTemplate adds the newline).
const DefaultLsRowFormat = `{{- cell (shortID .ID) .W.ID -}}
{{- cell .Name .W.Name -}}
{{- cell (printf "%v" .State) .W.State -}}
{{- cell (exitCode .ExitCode) .W.Code -}}
{{- fit (command .ConfigJSON) .Win .W.Used -}}`

// RenderEntries renders the command entries either as ID-only lines (quiet
// mode) or as a tabular view. Both the built-in table (format == "") and a
// user-supplied format receive the same []lsRow model; the built-in path also
// prints a header first.
func RenderEntries(out io.Writer, entries []store.CommandEntry, quiet bool, format string) error {
	if quiet {
		for _, e := range entries {
			fmt.Fprintln(out, e.ID)
		}
		return nil
	}

	w := measureLs(entries)
	meta := tableMeta{W: w, Win: terminalWidth(out)}
	rows := make([]lsRow, len(entries))
	for i, e := range entries {
		rows[i] = lsRow{CommandEntry: e, tableMeta: meta}
	}

	if format == "" {
		fmt.Fprintln(out, cell("ID", w["ID"])+cell("NAME", w["Name"])+
			cell("STATE", w["State"])+cell("EXIT CODE", w["Code"])+"COMMAND")
		format = DefaultLsRowFormat
	}
	return renderTemplate(out, rows, format)
}

// measureLs computes the longest line length of every ls column (header
// included) plus, under "Used", the width the four fixed columns and their gaps
// consume before the trailing COMMAND column.
func measureLs(entries []store.CommandEntry) map[string]int {
	w := map[string]int{
		"ID":    width("ID"),
		"Name":  width("NAME"),
		"State": width("STATE"),
		"Code":  width("EXIT CODE"),
	}
	for _, e := range entries {
		w["ID"] = max(w["ID"], width(shortID(e.ID)))
		w["Name"] = max(w["Name"], width(e.Name))
		w["State"] = max(w["State"], width(fmt.Sprintf("%v", e.State)))
		w["Code"] = max(w["Code"], width(exitCode(e.ExitCode)))
	}
	w["Used"] = w["ID"] + w["Name"] + w["State"] + w["Code"] + 4*len(columnGap)
	return w
}

// FormatUsage returns a usage string describing the available fields and
// helper functions for the --format flag.
func FormatUsage() string {
	t := reflect.TypeFor[store.CommandEntry]()
	var fields []string
	for f := range t.Fields() {
		fields = append(fields, fmt.Sprintf(".%s (%s)", f.Name, f.Type))
	}
	return fmt.Sprintf(
		"Go text/template string. Available fields:\n  %s\nTemplate functions: %s",
		strings.Join(fields, ", "),
		templateFuncList(),
	)
}
