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
	"github.com/moby/term"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

const (
	// commandMaxLen bounds the display width of a rendered command line.
	commandMaxLen = 40
	// idShortLen is the display width of an abbreviated command/container ID.
	idShortLen = 12
	// columnGap is the run of spaces written after each column except the last.
	columnGap = "   "
)

// tableMeta is the precomputed layout a default table template needs to align
// columns without re-scanning the whole result set. It is embedded into each
// per-table row model (lsRow, composeLsRow, composePsRow) alongside the
// original input row, so the same enriched value is handed to both the builtin
// table template and a user-supplied --format.
//
//   - W maps a column key to its longest line length (header included), and the
//     key "Used" to the total width consumed by the fixed columns and gaps.
//   - Win is the current terminal width in cells, or 0 when output is not a
//     terminal (the "fit" helper then leaves the final column untruncated).
//
// Both fields are json:"-" so the "json" helper marshals only the embedded
// input row, never these template-internal fields.
type tableMeta struct {
	W   map[string]int `json:"-"`
	Win int            `json:"-"`
}

// templateFuncMap is the shared FuncMap used by every --format template across
// the direct subcommands (ls, inspect, events) and the compose subcommands
// (compose ls/ps/inspect). A single map is what lets both halves — and any
// user-supplied --format — format and align columns the same way.
//
// The width helpers measure with go-runewidth, so East Asian wide and combining
// runes contribute their true display width and truncation never splits a
// multi-byte rune.
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
	"shortID":  shortID,
	"exitCode": exitCode,
	"join": func(sep string, elems []string) string {
		return strings.Join(elems, sep)
	},
	// width returns the display width of s in terminal cells.
	"width": runewidth.StringWidth,
	// pad left-aligns s in a field of w cells, padding the right with spaces.
	"pad": runewidth.FillRight,
	// cell is pad plus the inter-column gap, so a table template can place one
	// column per source line (joined with {{- -}}) instead of one long line.
	"cell": cell,
	// trunc shortens s to at most w cells, appending an ellipsis when cut.
	"trunc": func(s string, w int) string {
		return runewidth.Truncate(s, w, "…")
	},
	// fit truncates the final column to the room left in a win-cell line after
	// the fixed columns consumed "used" cells (see .Win and .W.Used).
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
	ws, err := term.GetWinsize(f.Fd())
	if err != nil || ws == nil {
		return 0
	}
	return int(ws.Width)
}

// renderTemplate applies format to each item in turn, newline-terminated — the
// same machinery a user --format runs through. Both the built-in tables and a
// user --format receive the same enriched row models (lsRow, composeLsRow, …),
// so the row template can pad against the precomputed widths in .W / .Win.
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
