package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// executor wraps os/exec.CommandContext for running tmux CLI invocations
// with the configured binary path and socket flag.
type executor struct {
	path   string
	socket string
}

func newExecutor(path, socket string) *executor {
	if path == "" {
		path = "tmux"
	}
	return &executor{path: path, socket: socket}
}

// run invokes tmux with the configured socket prefix (see buildArgs) plus args
// and returns trimmed stdout. The error wraps stderr to surface tmux's own
// diagnostics.
func (e *executor) run(ctx context.Context, args ...string) (string, error) {
	full := e.buildArgs(args)
	cmd := exec.CommandContext(ctx, e.path, full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"tmux %s: %w: %s",
			strings.Join(full, " "), err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// buildArgs prefixes the socket-selecting flag. A socket value containing a
// path separator is a socket file path (tmux -S <path>); a bare name selects a
// named server in tmux's default socket dir (tmux -L <name>); empty selects the
// default server (no flag).
func (e *executor) buildArgs(args []string) []string {
	switch {
	case e.socket == "":
		return args
	case strings.ContainsRune(e.socket, os.PathSeparator):
		return append([]string{"-S", e.socket}, args...)
	default:
		return append([]string{"-L", e.socket}, args...)
	}
}
