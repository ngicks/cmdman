package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
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
	"deref": deref,
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

func deref(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	return rv.Interface()
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
