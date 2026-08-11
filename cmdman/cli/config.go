package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/template"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/internal/templateutil"
)

// TemplateFuncHelp returns the aligned help block describing the helper
// functions a `cmdman config --format` template may call — the generic set from
// templateutil, which is exactly what [RenderConfig] installs. The command's
// Long text embeds it so the documented helpers cannot drift from the ones the
// template actually sees.
//
// The listing renderers (ls, inspect, events, ...) expose these plus their own
// domain helpers; templateFuncList names that wider set in their --format usage.
func TemplateFuncHelp() string {
	return templateutil.FuncHelp()
}

// RenderConfig writes the resolved configuration to w.
//
// With format == "" it writes indented JSON, the same shape the config file
// takes, so the output can be saved as one. Otherwise format is parsed as a Go
// text/template and executed against cfg; field paths use the Go field names
// (e.g. {{.DataDir}}), not the lower-case JSON keys, and the shared
// templateutil helpers are available. Either form ends with a newline.
//
// A field tagged json:"-" (Config.DefaultEnvironment) is absent from the JSON
// output but still reachable through --format.
func RenderConfig(w io.Writer, cfg config.Config, format string) error {
	if format != "" {
		tmpl, err := template.New("config").Funcs(templateutil.FuncMap()).Parse(format)
		if err != nil {
			return fmt.Errorf("parse format template: %w", err)
		}
		if err := tmpl.Execute(w, cfg); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(w)
		return nil
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}
