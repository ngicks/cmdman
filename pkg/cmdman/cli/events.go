package cli

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
)

// DefaultEventsFormat renders one event per line in a compact JSON form.
const DefaultEventsFormat = `{{json .}}`

// RenderEvents consumes Records from a Service.Events subscription and
// writes each event to out using format (a Go text/template fragment).
// When format is empty, DefaultEventsFormat is used.
func RenderEvents(out io.Writer, records <-chan eventlog.Record, format string) error {
	if out == nil {
		return fmt.Errorf("eventlog: stdout writer is nil")
	}
	if records == nil {
		return fmt.Errorf("eventlog: records channel is nil")
	}
	if format == "" {
		format = DefaultEventsFormat
	}
	tmpl, err := template.New("events").Funcs(templateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	for rec := range records {
		if rec.Err != nil {
			return rec.Err
		}
		if err := tmpl.Execute(out, rec.Event); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	return nil
}

// EventsFormatUsage returns a usage string describing fields and helper
// functions available to the --format flag of the events subcommand.
func EventsFormatUsage() string {
	t := reflect.TypeFor[eventlog.Event]()
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
