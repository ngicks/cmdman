package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

const commandMaxLen = 40

// templateFuncMap is the shared FuncMap used by both ls and inspect --format
// templates.
//
// The "command" helper takes a *store.CommandConfigJSON so it works against
// both CommandEntry.ConfigJSON (ls) and InspectOutput.Config (inspect).
var templateFuncMap = template.FuncMap{
	"json": func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("ERR: %v", err)
		}
		return string(b)
	},
	"command": func(cfg *store.CommandConfigJSON) string {
		if cfg == nil || len(cfg.Argv) == 0 {
			return "-"
		}
		s := strings.Join(cfg.Argv, " ")
		if len(s) > commandMaxLen {
			return s[:commandMaxLen-3] + "..."
		}
		return s
	},
}

// templateFuncList returns a comma-separated, sorted list of helper function
// names for inclusion in --format usage text.
func templateFuncList() string {
	names := make([]string, 0, len(templateFuncMap))
	for k := range templateFuncMap {
		names = append(names, k)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
