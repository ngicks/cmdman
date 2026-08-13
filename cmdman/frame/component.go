package frame

import (
	"fmt"
	"strings"
)

// WidgetArgv returns the canonical [ComponentArgv]: a built-in component name
// resolves to the widget entrypoint that runs it,
// `<exe> tui widget <name> --no-quit`. exe is the cmdman binary the frame pane
// should run — callers pass os.Executable() so a pane runs the same binary that
// carved the frame; an empty exe falls back to the bare name, resolved through
// PATH.
//
// --no-quit is not optional here: a widget docked in a frame pane must not exit
// from a keypress, or the fixture keeps a dead pane where the widget was (V6).
//
// Unknown component names are rejected here rather than reaching a pane as a
// command that fails at spawn time.
func WidgetArgv(exe string) ComponentArgv {
	if exe == "" {
		exe = "cmdman"
	}
	return func(component string) ([]string, error) {
		if !IsBuiltinComponent(component) {
			return nil, fmt.Errorf(
				"unknown component: want one of %s", strings.Join(BuiltinComponents(), ", "))
		}
		return []string{exe, "tui", "widget", component, "--no-quit"}, nil
	}
}
