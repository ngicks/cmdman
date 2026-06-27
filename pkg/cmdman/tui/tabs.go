package tui

import (
	"fmt"
	"strings"
)

// Tab identifies a top-level tab. It is exported so cmd/cli can name a tab for
// Options.InitialTab and the --tab flag.
type Tab int

const (
	TabCommands Tab = iota
	TabCompose
	TabLayout
)

// tabDefs is the single source of truth for the top-level tabs: their order,
// display name (the tab bar), and CLI token (the --tab flag). Every consumer —
// the tab bar, the --tab flag usage/validation/completion, and tab cycling —
// derives from this table so the names never drift.
var tabDefs = []struct {
	tab  Tab
	name string
	key  string
}{
	{TabCommands, "Commands", "commands"},
	{TabCompose, "Compose", "compose"},
	{TabLayout, "Layout", "layout"},
}

// TabNames returns the tab display names in tab order (used by the tab bar).
func TabNames() []string {
	names := make([]string, len(tabDefs))
	for i, d := range tabDefs {
		names[i] = d.name
	}
	return names
}

// TabKeys returns the --tab CLI tokens in tab order.
func TabKeys() []string {
	keys := make([]string, len(tabDefs))
	for i, d := range tabDefs {
		keys[i] = d.key
	}
	return keys
}

// ParseTab maps a --tab CLI token to its Tab, validating against tabDefs. It is
// the inverse of the tabDefs key column.
func ParseTab(s string) (Tab, error) {
	for _, d := range tabDefs {
		if d.key == s {
			return d.tab, nil
		}
	}
	return 0, fmt.Errorf("invalid tab %q: want one of %s", s, strings.Join(TabKeys(), ", "))
}

// NumTabs returns the number of top-level tabs.
func NumTabs() int { return len(tabDefs) }
