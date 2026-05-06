package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// executor wraps os/exec for running tmux commands.
type executor struct {
	tmuxPath   string
	socketName string
}

func newExecutor(tmuxPath, socketName string) *executor {
	if tmuxPath == "" {
		tmuxPath = "tmux"
	}
	return &executor{
		tmuxPath:   tmuxPath,
		socketName: socketName,
	}
}

// run executes a tmux command and returns the trimmed stdout.
// If the command fails, the error includes stderr.
func (e *executor) run(ctx context.Context, args ...string) (string, error) {
	fullArgs := e.buildArgs(args)
	cmd := exec.CommandContext(ctx, e.tmuxPath, fullArgs...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"tmux %s: %w: %s",
			strings.Join(fullArgs, " "),
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (e *executor) buildArgs(args []string) []string {
	if e.socketName != "" {
		return append([]string{"-L", e.socketName}, args...)
	}
	return args
}

// socketFlag returns the "-L <name> " flag string for use in shell commands,
// or "" if no socket is configured.
func (e *executor) socketFlag() string {
	if e.socketName != "" {
		return "-L " + shellQuote(e.socketName) + " "
	}
	return ""
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
// This is safe for embedding in sh(1) commands.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
