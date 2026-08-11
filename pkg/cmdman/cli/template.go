package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

const (
	commandMaxLen = 40
	idShortLen    = 12
	// titleMaxLen bounds the TITLE column. Titles are whatever the command
	// chose to say — a shell puts its whole working directory in one — and this
	// column is not the trailing one, so an unbounded title would push the
	// command itself off the line.
	titleMaxLen = 30
	columnGap   = "   "
)

// tableMeta carries precomputed column widths and the terminal width. A zero
// terminal width leaves the final column untruncated.
type tableMeta struct {
	W   map[string]int `json:"-"`
	Win int            `json:"-"`
}

// templateFuncMap is shared by all --format templates. Width helpers use
// terminal cell widths and preserve multi-byte runes.
var templateFuncMap = template.FuncMap{
	"json": func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("ERR: %v", err)
		}
		return string(b)
	},
	"deref":    deref,
	"command":  commandLine,
	"title":    titleText,
	"bell":     bellMark,
	"shortID":  shortID,
	"exitCode": exitCode,
	"join": func(sep string, elems []string) string {
		return strings.Join(elems, sep)
	},
	"width": runewidth.StringWidth,
	"pad":   runewidth.FillRight,
	"cell":  cell,
	"trunc": func(s string, w int) string {
		return runewidth.Truncate(s, w, "…")
	},
	"fit": fitColumn,
}

// width is the display width of s in terminal cells.
func width(s string) int { return runewidth.StringWidth(s) }

// cell left-aligns s in a w-cell field and appends the inter-column gap.
func cell(s string, w int) string { return runewidth.FillRight(s, w) + columnGap }

// shortID abbreviates a command/container ID to idShortLen display cells.
func shortID(id string) string { return runewidth.Truncate(id, idShortLen, "") }

// exitCode renders an optional exit code, using "-" when it is unset.
func exitCode(code *int) string {
	if code == nil {
		return "-"
	}
	return strconv.Itoa(*code)
}

// titleText renders the window title a command last set for a fixed-width
// column, using "-" when it set none (or when no monitor answered for it).
func titleText(s string) string {
	if s == "" {
		return "-"
	}
	return runewidth.Truncate(s, titleMaxLen, "…")
}

// bellMark renders the bell column: a command nobody has looked at since it
// rang is the only interesting case, so the quiet one gets the same "-" the
// other tables use for "nothing here". A table stays a table - the 🔔 the TUI
// shows belongs where a column width is not being measured in cells.
func bellMark(unread bool) string {
	if unread {
		return "*"
	}
	return "-"
}

// commandLine renders a command's argv as a single space-joined, width-bounded
// line, using "-" when there is no command.
func commandLine(cfg *model.CommandConfig) string {
	if cfg == nil || len(cfg.Argv) == 0 {
		return "-"
	}
	return runewidth.Truncate(strings.Join(cfg.Argv, " "), commandMaxLen, "...")
}

// fitColumn truncates s to the space left in a win-cell line after used cells
// have been consumed by the preceding columns and gaps. A win of 0 (unknown /
// not a terminal) leaves s untouched so redirected output keeps full values.
func fitColumn(s string, win, used int) string {
	if avail := win - used; win > 0 && avail > 0 {
		return runewidth.Truncate(s, avail, "…")
	}
	return s
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

// terminalWidth returns the column count of the terminal backing out, or 0 when
// out is not a terminal (a bytes.Buffer in tests, a pipe, a redirected file). A
// zero result means "unlimited width": the "fit" helper then leaves the final
// column untruncated, which keeps output deterministic for non-interactive
// consumers.
func terminalWidth(out io.Writer) int {
	f, ok := out.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}

// renderTemplate applies a newline-terminated template to each item.
func renderTemplate[T any](out io.Writer, items []T, format string) error {
	tmpl, err := template.New("format").Funcs(templateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	for _, item := range items {
		if err := tmpl.Execute(out, item); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(out)
	}
	return nil
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
