package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// RunOptions configures [Run].
type RunOptions struct {
	// SessionName names the multiplexer session this invocation targets.
	// Empty defaults to "cmdman". Plan/mux-00: the cmdman-created session is
	// fixed to "cmdman"; when driving the user's current server (e.g. inside
	// $TMUX) the session is still found-or-created by this name.
	SessionName string
	// WindowName names the cmdman-owned window within SessionName. Empty
	// defaults to SessionName ("cmdman"). Plan/mux-00: standalone mux uses
	// "cmdman"; compose mux passes "cmdman-<project>".
	WindowName string
	// Env is the process env consulted for driver autodetection ($TMUX /
	// $ZELLIJ). Empty defaults to os.Environ().
	Env []string
	// Stdout is where the attach hint is printed when running outside a
	// multiplexer. Empty defaults to os.Stdout.
	Stdout io.Writer
}

// Run applies one layout from spec to the configured driver's cmdman-owned
// window. The applied layout index is `(previousMarker+1) mod len(Layouts)`,
// read back from the existing window via [muxctl.Session.StatWindow]; a fresh
// window starts at index 0.
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
	sessionName := opts.SessionName
	if sessionName == "" {
		sessionName = "cmdman"
	}
	windowName := opts.WindowName
	if windowName == "" {
		windowName = sessionName
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

	sess, err := tmux.New(ctx, tmux.Config{
		Path:        spec.DriverOpt["path"],
		Socket:      spec.DriverOpt["socket"],
		SessionName: sessionName,
		WindowName:  windowName,
	})
	if err != nil {
		return err
	}

	stat, err := sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		return err
	}
	nextIdx := 0
	if stat.Marker >= 0 {
		nextIdx = (stat.Marker + 1) % len(spec.Layouts)
	}
	if _, err := sess.ApplyLayout(ctx, spec.Layouts[nextIdx].Root, nextIdx); err != nil {
		return err
	}

	if envOf(env, "TMUX") == "" && envOf(env, "ZELLIJ") == "" {
		fmt.Fprintf(stdout, "Attach: tmux attach -t %s\n", sessionName)
	}
	return nil
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
