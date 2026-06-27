package tui

import "testing"

// TestTabDefsInSync guards the invariant that TabNames, TabKeys, NumTabs, and
// ParseTab all derive from tabDefs in the same order as the Tab constants, so
// the tab bar and the --tab flag can never drift.
func TestTabDefsInSync(t *testing.T) {
	names := TabNames()
	keys := TabKeys()
	if len(names) != len(tabDefs) {
		t.Fatalf("TabNames() len = %d, want %d", len(names), len(tabDefs))
	}
	if len(keys) != len(tabDefs) {
		t.Fatalf("TabKeys() len = %d, want %d", len(keys), len(tabDefs))
	}
	if NumTabs() != len(tabDefs) {
		t.Fatalf("NumTabs() = %d, want %d", NumTabs(), len(tabDefs))
	}

	for i, d := range tabDefs {
		if d.tab != Tab(i) {
			t.Errorf("tabDefs[%d].tab = %d, want %d (constants must match order)", i, d.tab, i)
		}
		if names[i] != d.name {
			t.Errorf("TabNames()[%d] = %q, want %q", i, names[i], d.name)
		}
		if keys[i] != d.key {
			t.Errorf("TabKeys()[%d] = %q, want %q", i, keys[i], d.key)
		}
		got, err := ParseTab(d.key)
		if err != nil {
			t.Errorf("ParseTab(%q) unexpected error: %v", d.key, err)
		}
		if got != d.tab {
			t.Errorf("ParseTab(%q) = %d, want %d", d.key, got, d.tab)
		}
	}

	if _, err := ParseTab("nope"); err == nil {
		t.Errorf("ParseTab(%q) should return an error", "nope")
	}
}
