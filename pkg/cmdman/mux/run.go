package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// RunOptions configures [Run].
type RunOptions struct {
	// SessionName names the multiplexer session this invocation targets.
	// When empty and the driver is tmux, the current tmux session is
	// detected via `tmux display-message -p '#{session_name}'` (when $TMUX
	// is set). If detection fails or we are not inside tmux, falls back to
	// "cmdman". A non-empty value is used verbatim as an explicit override.
	SessionName string
	// WindowName names the cmdman-owned window within SessionName. Empty
	// defaults to SessionName. Plan/mux-00: standalone mux uses "cmdman";
	// compose mux passes "cmdman-<project>".
	WindowName string
	// Layout selects a specific layout to apply instead of cycling. It accepts
	// a layout name or a 0-based index (e.g. "2"). A name is matched first, so
	// a layout literally named "2" wins over index 2. Empty (the default)
	// cycles to the next layout after the one currently applied.
	Layout string
	// Env is the process env consulted for driver autodetection ($TMUX /
	// $ZELLIJ). Empty defaults to os.Environ().
	Env []string
	// Stdout is where the attach hint is printed when running outside a
	// multiplexer. Empty defaults to os.Stdout.
	Stdout io.Writer
}

// Run applies one layout from spec to the configured driver's cmdman-owned
// window. When opts.Layout is set, that named/indexed layout is applied
// directly; otherwise the applied layout index is `(previousMarker+1) mod
// len(Layouts)`, read back from the existing window via
// [muxctl.Session.StatWindow] (a fresh window starts at index 0). Either way
// the applied index is persisted as the window marker, so a subsequent cycling
// Run continues from the layout just shown.
//
// When invoked outside a multiplexer (no $TMUX / $ZELLIJ in env), Run builds
// the window detached and prints an attach hint
// (`tmux attach -t <session>`) to opts.Stdout.
//
// v1 supports the tmux driver only; spec.Driver == "zellij" or any future
// driver returns a "not implemented" error. Autodetect may still select
// "zellij" from $ZELLIJ — the error message points the user at the open
// driver work.
func Run(ctx context.Context, spec muxctl.MuxSpec, opts RunOptions) error {
	if len(spec.Layouts) == 0 {
		return errors.New("mux: spec has no layouts")
	}

	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	driver := resolveDriver(spec.Driver, env)
	if driver != "tmux" {
		return fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	path, socket := spec.DriverOpt["path"], spec.DriverOpt["socket"]
	explicitSession := opts.SessionName != ""
	sessionName := resolveSessionName(
		opts.SessionName,
		env,
		func() (string, error) { return currentTmuxSession(ctx, path, socket) },
	)
	windowName := opts.WindowName
	if windowName == "" {
		windowName = sessionName
	}

	// With no explicit --session, take over the caller's current window when it
	// is safe to repurpose (single-pane or already ours) instead of spawning a
	// separate window. An explicit session means "target that session", so the
	// current-window takeover is disabled.
	reuseCurrent := !explicitSession && envOf(env, "TMUX") != ""

	// Resolve an explicit layout selector up-front so a bad name/index fails
	// before we touch the multiplexer. -1 means "cycle".
	explicitIdx := -1
	if opts.Layout != "" {
		idx, err := resolveLayoutIndex(opts.Layout, spec.Layouts)
		if err != nil {
			return err
		}
		explicitIdx = idx
	}

	sess, err := tmux.New(ctx, tmux.Config{
		Path:               spec.DriverOpt["path"],
		Socket:             spec.DriverOpt["socket"],
		SessionName:        sessionName,
		WindowName:         windowName,
		ReuseCurrentWindow: reuseCurrent,
	})
	if err != nil {
		return err
	}

	nextIdx := explicitIdx
	if nextIdx < 0 {
		stat, err := sess.StatWindow(ctx, sess.WindowID())
		if err != nil {
			return err
		}
		nextIdx = 0
		if stat.Marker >= 0 {
			nextIdx = (stat.Marker + 1) % len(spec.Layouts)
		}
	}
	if _, err := sess.ApplyLayout(ctx, spec.Layouts[nextIdx].Root, nextIdx); err != nil {
		return err
	}

	if envOf(env, "TMUX") == "" && envOf(env, "ZELLIJ") == "" {
		fmt.Fprintf(stdout, "Attach: tmux attach -t %s\n", sessionName)
	}
	return nil
}

// resolveSessionName determines the tmux session name to target.
// When override is non-empty it is returned verbatim.
// When inside tmux ($TMUX is set) queryCurrent is called; on success its
// result is returned. On failure or when not inside tmux, falls back to
// "cmdman".
func resolveSessionName(
	override string,
	env []string,
	queryCurrent func() (string, error),
) string {
	if override != "" {
		return override
	}
	if envOf(env, "TMUX") != "" {
		if name, err := queryCurrent(); err == nil {
			return name
		}
	}
	return "cmdman"
}

// currentTmuxSession queries the name of the currently-active tmux session by
// running `tmux display-message -p '#{session_name}'`. tmuxPath is the tmux
// binary path (empty → "tmux"). socket, when non-empty, is passed as -L
// <socket> before the subcommand (mirroring tmux.Config.Socket).
func currentTmuxSession(ctx context.Context, tmuxPath, socket string) (string, error) {
	bin := tmuxPath
	if bin == "" {
		bin = "tmux"
	}
	args := []string{}
	if socket != "" {
		args = append(args, "-L", socket)
	}
	args = append(args, "display-message", "-p", "#{session_name}")
	var buf strings.Builder
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// resolveLayoutIndex resolves a layout selector to an index into layouts. A
// name is matched first (so a layout literally named "2" wins over index 2),
// then the selector is parsed as a 0-based index. It errors on an unknown name
// or out-of-range index.
func resolveLayoutIndex(selector string, layouts []muxctl.Layout) (int, error) {
	for i, l := range layouts {
		if l.Name == selector {
			return i, nil
		}
	}
	if n, err := strconv.Atoi(selector); err == nil {
		if n < 0 || n >= len(layouts) {
			return 0, fmt.Errorf(
				"mux: layout index %d out of range [0,%d)", n, len(layouts))
		}
		return n, nil
	}
	return 0, fmt.Errorf("mux: no layout %q; available: %s",
		selector, strings.Join(layoutNames(layouts), ", "))
}

// layoutNames returns the layout names for error/diagnostic messages.
func layoutNames(layouts []muxctl.Layout) []string {
	names := make([]string, len(layouts))
	for i, l := range layouts {
		names[i] = l.Name
	}
	return names
}

// resolveDriver picks the driver from spec.Driver / autodetect. Empty
// spec.Driver triggers autodetect: $TMUX > $ZELLIJ > fallback "tmux".
func resolveDriver(declared string, env []string) string {
	if declared != "" {
		return declared
	}
	if envOf(env, "TMUX") != "" {
		return "tmux"
	}
	if envOf(env, "ZELLIJ") != "" {
		return "zellij"
	}
	return "tmux"
}

// envOf returns the value of key in env, or "" when absent. env is a slice of
// "KEY=VALUE" entries as produced by os.Environ.
func envOf(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}
