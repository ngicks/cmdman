package muxctl_test

import (
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

func TestShouldReuseUnmarkedWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		curName   string
		ownedName string
		panes     int
		want      bool
	}{
		{"single pane is reused", "work", "cmdman", 1, true},
		{"zero panes is reused", "work", "cmdman", 0, true},
		{"name match is reused even when multi-pane", "cmdman", "cmdman", 5, true},
		{"multi-pane unowned is not reused", "work", "cmdman", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := muxctl.ShouldReuseUnmarkedWindow(tc.curName, tc.ownedName, tc.panes)
			if got != tc.want {
				t.Fatalf(
					"ShouldReuseUnmarkedWindow(%q, %q, %d) = %v, want %v",
					tc.curName, tc.ownedName, tc.panes, got, tc.want,
				)
			}
		})
	}
}
