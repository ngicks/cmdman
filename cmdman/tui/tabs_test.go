package tui

import (
	"testing"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// TestTabDefsInSync guards the invariant that TabNames, TabKeys, NumTabs, and
// ParseTab all derive from core.TabDefs in the same order as the Tab constants, so
// the tab bar and the --tab flag can never drift.
func TestTabDefsInSync(t *testing.T) {
	names := TabNames()
	keys := TabKeys()
	if len(names) != len(core.TabDefs) {
		t.Fatalf("TabNames() len = %d, want %d", len(names), len(core.TabDefs))
	}
	if len(keys) != len(core.TabDefs) {
		t.Fatalf("TabKeys() len = %d, want %d", len(keys), len(core.TabDefs))
	}
	if NumTabs() != len(core.TabDefs) {
		t.Fatalf("NumTabs() = %d, want %d", NumTabs(), len(core.TabDefs))
	}

	for i, d := range core.TabDefs {
		if d.Tab != Tab(i) {
			t.Errorf("core.TabDefs[%d].Tab = %d, want %d (constants must match order)", i, d.Tab, i)
		}
		if names[i] != d.Name {
			t.Errorf("TabNames()[%d] = %q, want %q", i, names[i], d.Name)
		}
		if keys[i] != d.Key {
			t.Errorf("TabKeys()[%d] = %q, want %q", i, keys[i], d.Key)
		}
		got, err := ParseTab(d.Key)
		if err != nil {
			t.Errorf("ParseTab(%q) unexpected error: %v", d.Key, err)
		}
		if got != d.Tab {
			t.Errorf("ParseTab(%q) = %d, want %d", d.Key, got, d.Tab)
		}
	}

	if _, err := ParseTab("nope"); err == nil {
		t.Errorf("ParseTab(%q) should return an error", "nope")
	}
}
