package mux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

func TestResolveSessionName(t *testing.T) {
	okQuery := func() (string, bool, error) { return "work", true, nil }
	errQuery := func() (string, bool, error) { return "", false, errors.New("not in tmux") }
	notAttachedQuery := func() (string, bool, error) { return "", false, nil }
	inTmux := []string{"TMUX=/tmp/tmux-1000/default,123,0"}
	noTmux := []string{"PATH=/usr/bin"}

	t.Run("explicit override wins", func(t *testing.T) {
		// Override is used verbatim, without querying tmux.
		if got := resolveSessionName("custom", inTmux, func() (string, bool, error) {
			t.Fatal("queryCurrent must not be called when override is set")
			return "", false, nil
		}); got != "custom" {
			t.Fatalf("got %q, want %q", got, "custom")
		}
	})

	t.Run("outside tmux falls back to cmdman", func(t *testing.T) {
		if got := resolveSessionName("", noTmux, func() (string, bool, error) {
			t.Fatal("queryCurrent must not be called outside tmux")
			return "", false, nil
		}); got != "cmdman" {
			t.Fatalf("got %q, want %q", got, "cmdman")
		}
	})

	t.Run("inside tmux uses current session", func(t *testing.T) {
		if got := resolveSessionName("", inTmux, okQuery); got != "work" {
			t.Fatalf("got %q, want %q", got, "work")
		}
	})

	t.Run("inside tmux but query fails falls back to cmdman", func(t *testing.T) {
		if got := resolveSessionName("", inTmux, errQuery); got != "cmdman" {
			t.Fatalf("got %q, want %q", got, "cmdman")
		}
	})

	t.Run("inside tmux but not attached falls back to cmdman", func(t *testing.T) {
		if got := resolveSessionName("", inTmux, notAttachedQuery); got != "cmdman" {
			t.Fatalf("got %q, want %q", got, "cmdman")
		}
	})
}

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

// TestResolveServer covers server resolution: the tmux driver (linked into this
// test binary via scale_rmw_test.go's import) connects — Connect is a pure
// binding, so no tmux server is needed — while the known-but-unimplemented names
// get the friendly "not implemented yet" wording, and any other unregistered
// name surfaces LookupDriver's naming error. The error branches need no tmux
// server.
func TestResolveServer(t *testing.T) {
	ctx := context.Background()
	noTmux := []string{"PATH=/usr/bin"}

	t.Run("tmux resolves", func(t *testing.T) {
		server, err := resolveServer(ctx, muxctl.DriverSpec{Name: "tmux"}, noTmux)
		if err != nil {
			t.Fatalf("resolveServer(tmux) = %v, want nil error", err)
		}
		if server == nil {
			t.Fatal("resolveServer(tmux) returned nil server")
		}
	})

	t.Run("zellij and wezterm are not-implemented-yet", func(t *testing.T) {
		for _, name := range []string{"zellij", "wezterm"} {
			server, err := resolveServer(ctx, muxctl.DriverSpec{Name: name}, noTmux)
			if err == nil {
				t.Fatalf("resolveServer(%q) = %v, want error", name, server)
			}
			if server != nil {
				t.Errorf("resolveServer(%q) server = %v, want nil", name, server)
			}
			if !strings.Contains(err.Error(), "not implemented yet") {
				t.Errorf("resolveServer(%q) error = %q, want \"not implemented yet\"", name, err)
			}
		}
	})

	t.Run("unregistered name surfaces lookup error", func(t *testing.T) {
		const name = "nonexistent-driver-xyz"
		server, err := resolveServer(ctx, muxctl.DriverSpec{Name: name}, noTmux)
		if err == nil {
			t.Fatalf("resolveServer(%q) = %v, want error", name, server)
		}
		if server != nil {
			t.Errorf("resolveServer(%q) server = %v, want nil", name, server)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("resolveServer(%q) error = %q, want it to name the driver", name, err)
		}
		if strings.Contains(err.Error(), "not implemented yet") {
			t.Errorf("resolveServer(%q) should not use the not-implemented wording: %q", name, err)
		}
	})
}
