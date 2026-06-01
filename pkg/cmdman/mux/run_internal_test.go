package mux

import (
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

func TestResolveLayoutIndex(t *testing.T) {
	layouts := []muxctl.Layout{
		{Name: "all"},
		{Name: "claude"},
		{Name: "codex"},
	}

	t.Run("by name", func(t *testing.T) {
		for want, name := range map[int]string{0: "all", 1: "claude", 2: "codex"} {
			got, err := resolveLayoutIndex(name, layouts)
			if err != nil || got != want {
				t.Fatalf("resolveLayoutIndex(%q) = %d, %v; want %d", name, got, err, want)
			}
		}
	})

	t.Run("by index", func(t *testing.T) {
		got, err := resolveLayoutIndex("2", layouts)
		if err != nil || got != 2 {
			t.Fatalf("resolveLayoutIndex(\"2\") = %d, %v; want 2", got, err)
		}
	})

	t.Run("name wins over index", func(t *testing.T) {
		// A layout literally named "2" must resolve to its position, not index 2.
		named := []muxctl.Layout{{Name: "a"}, {Name: "2"}, {Name: "c"}}
		got, err := resolveLayoutIndex("2", named)
		if err != nil || got != 1 {
			t.Fatalf("a layout named \"2\" should win; got %d, %v", got, err)
		}
	})

	t.Run("index out of range", func(t *testing.T) {
		if _, err := resolveLayoutIndex("5", layouts); err == nil {
			t.Fatalf("out-of-range index should error")
		}
		if _, err := resolveLayoutIndex("-1", layouts); err == nil {
			t.Fatalf("negative index should error")
		}
	})

	t.Run("unknown name", func(t *testing.T) {
		_, err := resolveLayoutIndex("nope", layouts)
		if err == nil {
			t.Fatalf("unknown layout name should error")
		}
	})
}
