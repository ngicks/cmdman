// Package templateutil centralizes the [text/template] helpers shared by every
// template-rendering call site — the `config` subcommand's --format, the ls /
// inspect / events / status renderers in cmdman/cli, and any renderer added
// later. One func map means every template sees the same functions; one
// [FuncDocs] means the help text cannot drift from them.
//
// Only project-agnostic helpers belong here: value formatting (json, deref,
// join) and terminal-cell string metrics (width, pad, trunc). A helper that
// needs a cmdman domain type or a caller's column layout stays with its
// renderer, which copies this map and adds its own entries on top.
package templateutil

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/mattn/go-runewidth"
)

// FuncMap returns the generic template function map shared across the project's
// template renderers. A fresh map is returned on each call so callers may mutate
// it without affecting one another.
//
// Every entry must be documented in [FuncDocs]; a test keeps the two in
// lockstep.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"json":  JSON,
		"deref": Deref,
		"join":  Join,
		"width": Width,
		"pad":   Pad,
		"trunc": Trunc,
	}
}

// FuncDoc documents a single helper exposed by [FuncMap].
type FuncDoc struct {
	Name  string // bare function name as registered in FuncMap
	Usage string // name plus argument placeholders, e.g. "json VALUE"
	Desc  string // one-line human description
}

// FuncDocs returns documentation for every helper in [FuncMap], in a stable
// display order. It is the single source of truth behind [FuncHelp] and the
// command help text; keep it in sync with FuncMap (guarded by a test).
func FuncDocs() []FuncDoc {
	return []FuncDoc{
		{Name: "json", Usage: "json VALUE", Desc: "VALUE marshaled as compact one-line JSON"},
		{
			Name:  "deref",
			Usage: "deref VALUE",
			Desc:  "VALUE with pointers followed; nil if any is nil",
		},
		{Name: "join", Usage: "join SEP LIST", Desc: "LIST elements joined with SEP"},
		{Name: "width", Usage: "width STR", Desc: "display width of STR in terminal cells"},
		{Name: "pad", Usage: "pad STR N", Desc: "STR left-aligned in an N-cell field"},
		{Name: "trunc", Usage: "trunc STR N", Desc: "STR cut to N cells, marked with an ellipsis"},
	}
}

// FuncHelp renders [FuncDocs] as an aligned, indented block for embedding in
// command help text. Each line is "  <usage>  <desc>" with the usage column
// padded to a common width; the block ends with a trailing newline.
func FuncHelp() string {
	docs := FuncDocs()
	width := 0
	for _, d := range docs {
		width = max(width, len(d.Usage))
	}
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, d.Usage, d.Desc)
	}
	return b.String()
}

// JSON marshals v as compact, single-line JSON, reporting a marshal failure
// inline as "ERR: ..." rather than aborting the whole template.
//
// Compact rather than indented: {{json .}} is how callers ask for one
// machine-readable record per output line (`cmdman ls --format '{{json .}}'`),
// which an indented encoding would split across lines. A renderer that wants an
// indented document — the `config` subcommand's default output — indents it
// itself instead of going through this helper.
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("ERR: %v", err)
	}
	return string(b)
}

// Deref follows v through any number of pointers and returns the pointed-to
// value, or nil when v or any link along the way is nil. It lets a template
// print a *T field without a nil check.
func Deref(v any) any {
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

// Join concatenates elems with sep. The separator comes first so a template
// reads in the order it is written: {{join " " .Argv}}.
func Join(sep string, elems []string) string {
	return strings.Join(elems, sep)
}

// Width is the display width of s in terminal cells — a double-width rune
// counts as two, so column arithmetic survives CJK text.
func Width(s string) int { return runewidth.StringWidth(s) }

// Pad left-aligns s in a w-cell field, padding with spaces.
func Pad(s string, w int) string { return runewidth.FillRight(s, w) }

// Trunc shortens s to at most w display cells, marking the cut with an
// ellipsis.
func Trunc(s string, w int) string { return runewidth.Truncate(s, w, "…") }
