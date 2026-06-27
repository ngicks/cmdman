package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

func TestResolvePopupDriverInfersTmux(t *testing.T) {
	// Bare --popup (value "true") with a tmux server in the environment.
	got, err := resolvePopupDriver("true", []string{"TMUX=/tmp/tmux-1000/default,123,0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tmux" {
		t.Fatalf("inferred driver = %q, want tmux", got)
	}
}

func TestResolvePopupDriverFallsBackToTmux(t *testing.T) {
	got, err := resolvePopupDriver("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tmux" {
		t.Fatalf("fallback driver = %q, want tmux", got)
	}
}

func TestResolvePopupDriverExplicitTmux(t *testing.T) {
	got, err := resolvePopupDriver("tmux", nil)
	if err != nil || got != "tmux" {
		t.Fatalf("resolvePopupDriver(tmux) = %q, %v", got, err)
	}
}

func TestResolvePopupDriverZellijNotImplemented(t *testing.T) {
	_, err := resolvePopupDriver("zellij", nil)
	if err == nil {
		t.Fatalf("expected zellij to report not implemented")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("error should mention not implemented, got %v", err)
	}
}

func TestResolvePopupDriverInfersZellijReportsNotImplemented(t *testing.T) {
	// Bare --popup that infers zellij from the environment must still report
	// not implemented for v1.
	_, err := resolvePopupDriver("true", []string{"ZELLIJ=0"})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("inferred zellij should report not implemented, got %v", err)
	}
}

func TestChildCommandForwardsDirs(t *testing.T) {
	cfg := PopupConfig{
		Executable: "/usr/bin/cmdman",
		DataDir:    "/data",
		RuntimeDir: "/run",
	}
	args := cfg.childCommand("/tmp/ipc.sock")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"/usr/bin/cmdman tui __child",
		"--ipc /tmp/ipc.sock",
		"--data-dir /data",
		"--runtime-dir /run",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("child command %q should contain %q", joined, want)
		}
	}
}

func TestChildCommandForwardsTab(t *testing.T) {
	// No Tab set: the popup child argv must not carry a --tab flag.
	plain := PopupConfig{Executable: "/usr/bin/cmdman"}.childCommand("/tmp/ipc.sock")
	if strings.Contains(strings.Join(plain, " "), "--tab") {
		t.Errorf("childCommand without Tab should not forward --tab, got %v", plain)
	}

	// Tab set: it is forwarded verbatim.
	withTab := PopupConfig{Executable: "/usr/bin/cmdman", Tab: "layout"}.
		childCommand("/tmp/ipc.sock")
	if !strings.Contains(strings.Join(withTab, " "), "--tab layout") {
		t.Errorf("childCommand should forward --tab layout, got %v", withTab)
	}
}

func TestTmuxPopupArgsNoGeometry(t *testing.T) {
	// No cwd and no geometry: only -E and the command, leaving tmux's defaults.
	args := tmuxPopupArgs(PopupConfig{}, "child")
	if want := []string{"display-popup", "-E", "child"}; !slices.Equal(args, want) {
		t.Fatalf("tmuxPopupArgs = %v, want %v", args, want)
	}
}

func TestTmuxPopupArgsWithGeometry(t *testing.T) {
	cfg := PopupConfig{
		Cwd:    "/work",
		Width:  "80%",
		Height: "70%",
		X:      "10%",
		Y:      "5%",
	}
	args := tmuxPopupArgs(cfg, "child")
	want := []string{
		"display-popup", "-E", "-d", "/work",
		"-w", "80%", "-h", "70%", "-x", "10%", "-y", "5%",
		"child",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("tmuxPopupArgs = %v, want %v", args, want)
	}
}

func TestTmuxPopupArgsPartialGeometry(t *testing.T) {
	// Only width set: the other dimensions are omitted so tmux defaults them.
	args := tmuxPopupArgs(PopupConfig{Width: "50%"}, "child")
	if want := []string{"display-popup", "-E", "-w", "50%", "child"}; !slices.Equal(args, want) {
		t.Fatalf("tmuxPopupArgs = %v, want %v", args, want)
	}
}

func TestPopupGeometryValidate(t *testing.T) {
	valid := []PopupGeometry{
		{}, // all empty: tmux defaults every dimension
		{Width: "80%"},
		{Width: "80%", Height: "70%", X: "10%", Y: "5%"},
		{Height: "0%"},
		{X: "100%"},
	}
	for _, g := range valid {
		if err := g.Validate(); err != nil {
			t.Errorf("Validate(%+v) unexpected error: %v", g, err)
		}
	}

	invalid := []PopupGeometry{
		{Width: "80"},    // bare number, no percent
		{Height: "C"},    // tmux position token
		{X: "150x"},      // wrong suffix
		{Y: "1234%"},     // too many digits
		{Width: "%"},     // no digits
		{Width: "8 0%"},  // embedded space
		{Width: "-5%"},   // negative
		{Height: " 80%"}, // leading space
	}
	for _, g := range invalid {
		if err := g.Validate(); err == nil {
			t.Errorf("Validate(%+v) expected error, got nil", g)
		}
	}
}

func TestTabToken(t *testing.T) {
	cases := map[tui.Tab]string{
		tui.TabCommands: "commands",
		tui.TabCompose:  "compose",
		tui.TabLayout:   "layout",
	}
	for tab, want := range cases {
		if got := tabToken(tab); got != want {
			t.Errorf("tabToken(%d) = %q, want %q", tab, got, want)
		}
	}
	if got := tabToken(tui.Tab(99)); got != "" {
		t.Errorf("tabToken(out-of-range) = %q, want empty", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"":            "''",
		"with space":  "'with space'",
		"a'b":         `'a'\''b'`,
		"/tmp/x.sock": "/tmp/x.sock",
		"semi;colon":  "'semi;colon'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
