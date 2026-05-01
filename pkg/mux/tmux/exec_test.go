package tmux

import (
	"slices"
	"testing"
)

func TestNewExecutor(t *testing.T) {
	t.Run("default path", func(t *testing.T) {
		e := newExecutor("", "")
		if e.tmuxPath != "tmux" {
			t.Errorf("tmuxPath = %q, want %q", e.tmuxPath, "tmux")
		}
	})

	t.Run("custom path", func(t *testing.T) {
		e := newExecutor("/usr/bin/tmux", "")
		if e.tmuxPath != "/usr/bin/tmux" {
			t.Errorf("tmuxPath = %q, want %q", e.tmuxPath, "/usr/bin/tmux")
		}
	})
}

func TestBuildArgs(t *testing.T) {
	t.Run("no socket", func(t *testing.T) {
		e := newExecutor("", "")
		got := e.buildArgs([]string{"list-panes"})
		want := []string{"list-panes"}
		if !slices.Equal(got, want) {
			t.Errorf("buildArgs = %v, want %v", got, want)
		}
	})

	t.Run("with socket", func(t *testing.T) {
		e := newExecutor("", "mysock")
		got := e.buildArgs([]string{"list-panes"})
		want := []string{"-L", "mysock", "list-panes"}
		if !slices.Equal(got, want) {
			t.Errorf("buildArgs = %v, want %v", got, want)
		}
	})
}
