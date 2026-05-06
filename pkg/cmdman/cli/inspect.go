package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

// RenderInspect prints the inspect output. When format is empty, it emits
// indented JSON. Otherwise format is parsed as a Go text/template applied to
// the inspect output, followed by a trailing newline.
func RenderInspect(out io.Writer, info *cmdman.InspectOutput, format string) error {
	if format == "" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	tmpl, err := template.New("format").Funcs(templateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	if err := tmpl.Execute(out, info); err != nil {
		return fmt.Errorf("execute format template: %w", err)
	}
	fmt.Fprintln(out)
	return nil
}

// InspectFormatUsage returns a usage string describing the available fields
// and helper functions for the inspect --format flag.
func InspectFormatUsage() string {
	t := reflect.TypeFor[cmdman.InspectOutput]()
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
