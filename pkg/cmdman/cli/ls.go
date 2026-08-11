package cli

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// runtimeStater is the one thing a listing needs from the service: the
// monitor-held state of the entries it is about to render.
type runtimeStater interface {
	RuntimeStates(
		ctx context.Context,
		entries []store.CommandEntry,
		opt cmdman.RuntimeStatesOption,
	) map[string]cmdman.RuntimeState
}

// RuntimeStates fills the runtime columns of a listing, under the overall
// budget every glance-style listing shares ([cmdman.RuntimeStatesBudget]).
// Failure is an empty column, never an error, so the map is simply missing the
// commands whose monitors did not answer in time.
func RuntimeStates(
	ctx context.Context,
	svc runtimeStater,
	entries []store.CommandEntry,
) map[string]cmdman.RuntimeState {
	ctx, cancel := context.WithTimeout(ctx, cmdman.RuntimeStatesBudget)
	defer cancel()
	return svc.RuntimeStates(ctx, entries, cmdman.RuntimeStatesOption{})
}

// lsRow is the data model for both the built-in ls table and a user --format.
// It embeds the original entry (so .ID, .Name, … and {{json .}} keep working),
// the runtime state its monitor holds for the current run (.Title, .Status,
// .Detail, .BellUnread — all zero for a command with no live monitor), and the
// table layout via tableMeta (.W, .Win), which is json:"-".
type lsRow struct {
	store.CommandEntry
	cmdman.RuntimeState
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
{{- cell (or .Status "-") .W.Status -}}
{{- cell .Bell .W.Bell -}}
{{- cell (or .Detail "-") .W.Detail -}}
{{- cell (title .Title) .W.Title -}}
{{- fit (command .ConfigJSON) .Win .W.Used -}}`

// Bell renders the bell column; see composeStatusRow.Bell.
func (r lsRow) Bell() string { return bellMark(r.BellUnread) }

// RenderEntries renders the command entries either as ID-only lines (quiet
// mode) or as a tabular view. runtime carries the monitor-held state keyed by
// command ID (see [cmdman.Service.RuntimeStates]); an entry with no live
// monitor is simply absent from it and renders empty runtime columns. Both the
// built-in table (format == "") and a user-supplied format receive the same
// []lsRow model; the built-in path also prints a header first.
func RenderEntries(
	out io.Writer,
	entries []store.CommandEntry,
	runtime map[string]cmdman.RuntimeState,
	quiet bool,
	format string,
) error {
	if quiet {
		for _, e := range entries {
			fmt.Fprintln(out, e.ID)
		}
		return nil
	}

	w := measureLs(entries, runtime)
	meta := tableMeta{W: w, Win: terminalWidth(out)}
	rows := make([]lsRow, len(entries))
	for i, e := range entries {
		rows[i] = lsRow{CommandEntry: e, RuntimeState: runtime[e.ID], tableMeta: meta}
	}

	if format == "" {
		fmt.Fprintln(out, cell("ID", w["ID"])+cell("NAME", w["Name"])+
			cell("STATE", w["State"])+cell("EXIT CODE", w["Code"])+
			cell("STATUS", w["Status"])+cell("BELL", w["Bell"])+
			cell("DETAIL", w["Detail"])+cell("TITLE", w["Title"])+"COMMAND")
		format = DefaultLsRowFormat
	}
	return renderTemplate(out, rows, format)
}

// measureLs computes the longest line length of every ls column (header
// included) plus, under "Used", the width the fixed columns and their gaps
// consume before the trailing COMMAND column. The runtime columns are measured
// through the same helpers that render them, so a truncated title cannot
// measure wider than it draws.
func measureLs(
	entries []store.CommandEntry,
	runtime map[string]cmdman.RuntimeState,
) map[string]int {
	w := map[string]int{
		"ID":     width("ID"),
		"Name":   width("NAME"),
		"State":  width("STATE"),
		"Code":   width("EXIT CODE"),
		"Status": width("STATUS"),
		"Detail": width("DETAIL"),
		"Bell":   width("BELL"),
		"Title":  width("TITLE"),
	}
	for _, e := range entries {
		w["ID"] = max(w["ID"], width(shortID(e.ID)))
		w["Name"] = max(w["Name"], width(e.Name))
		w["State"] = max(w["State"], width(fmt.Sprintf("%v", e.State)))
		w["Code"] = max(w["Code"], width(exitCode(e.ExitCode)))
		rs := runtime[e.ID]
		w["Status"] = max(w["Status"], width(rs.Status))
		w["Detail"] = max(w["Detail"], width(rs.Detail))
		w["Title"] = max(w["Title"], width(titleText(rs.Title)))
	}
	w["Used"] = w["ID"] + w["Name"] + w["State"] + w["Code"] +
		w["Status"] + w["Detail"] + w["Bell"] + w["Title"] + 8*len(columnGap)
	return w
}

// FormatUsage returns a usage string describing the available fields and
// helper functions for the --format flag.
func FormatUsage() string {
	return fmt.Sprintf(
		"Go text/template string. Available fields:\n  %s\nTemplate functions: %s",
		strings.Join(
			append(
				templateFields[store.CommandEntry](),
				templateFields[cmdman.RuntimeState]()...,
			),
			", ",
		),
		templateFuncList(),
	)
}

// templateFields lists T's exported fields the way --format usage spells them.
func templateFields[T any]() []string {
	t := reflect.TypeFor[T]()
	var fields []string
	for f := range t.Fields() {
		fields = append(fields, fmt.Sprintf(".%s (%s)", f.Name, f.Type))
	}
	return fields
}
