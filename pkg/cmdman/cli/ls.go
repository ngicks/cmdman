package cli

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

const (
	DefaultLsHeader    = "ID\tNAME\tSTATE\tEXIT CODE\tCOMMAND"
	DefaultLsRowFormat = "{{slice .ID 0 12}}\t{{.Name}}\t{{.State}}\t" +
		"{{if .ExitCode}}{{printf \"%d\" (deref .ExitCode)}}{{else}}-{{end}}\t" +
		"{{command .ConfigJSON}}"
)

// RenderEntries renders the command entries either as ID-only lines (quiet
// mode) or as a tabular view driven by a Go text/template format string.
// When format is empty, DefaultLsRowFormat preceded by DefaultLsHeader is
// used.
func RenderEntries(out io.Writer, entries []store.CommandEntry, quiet bool, format string) error {
	if quiet {
		for _, e := range entries {
			fmt.Fprintln(out, e.ID)
		}
		return nil
	}

	if format == "" {
		format = DefaultLsRowFormat
		fmt.Fprintln(out, DefaultLsHeader)
	}

	tmpl, err := template.New("format").Funcs(templateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	for _, e := range entries {
		if err := tmpl.Execute(out, e); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(out)
	}
	return nil
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
