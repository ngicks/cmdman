package core

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

// TabDef is one row of TabDefs.
type TabDef struct {
	Tab  Tab
	Name string
	Key  string
}

// TabDefs is the single source of truth for the top-level tabs: their order,
// display name (the tab bar), and CLI token (the --tab flag). Every consumer —
// the tab bar, the --tab flag usage/validation/completion, and tab cycling —
// derives from this table so the names never drift.
var TabDefs = []TabDef{
	{TabCommands, "Commands", "commands"},
	{TabCompose, "Compose", "compose"},
	{TabLayout, "Layout", "layout"},
}

// TabNames returns the tab display names in tab order (used by the tab bar).
func TabNames() []string {
	names := make([]string, len(TabDefs))
	for i, d := range TabDefs {
		names[i] = d.Name
	}
	return names
}

// TabKeys returns the --tab CLI tokens in tab order.
func TabKeys() []string {
	keys := make([]string, len(TabDefs))
	for i, d := range TabDefs {
		keys[i] = d.Key
	}
	return keys
}

// ParseTab maps a --tab CLI token to its Tab, validating against TabDefs. It is
// the inverse of the TabDefs key column.
func ParseTab(s string) (Tab, error) {
	for _, d := range TabDefs {
		if d.Key == s {
			return d.Tab, nil
		}
	}
	return 0, fmt.Errorf("invalid tab %q: want one of %s", s, strings.Join(TabKeys(), ", "))
}

// NumTabs returns the number of top-level tabs.
func NumTabs() int { return len(TabDefs) }
