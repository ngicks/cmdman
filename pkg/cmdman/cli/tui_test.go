package cli

import (
	"strings"
	"testing"
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
