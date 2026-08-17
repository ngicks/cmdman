package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/go-common/contextkey"
)

// RunOptions configures [Run].
type RunOptions struct {
	// SessionName names the multiplexer session this invocation targets.
	// When empty and the driver is tmux, the current tmux session is
	// detected via `tmux display-message -p '#{session_name}'` (when $TMUX
	// is set). If detection fails or we are not inside tmux, falls back to
	// "cmdman". A non-empty value is used verbatim as an explicit override.
	SessionName string
	// WindowName is the label a window created by this call wears (standalone
	// mux uses "cmdman"; compose mux passes "cmdman-<project>"). Empty defaults
	// to SessionName. It is display-only: which window this project already
	// holds is answered by Identity, so a window renamed since it was built is
	// still found, and a same-named window belonging to somebody else is never
	// mistaken for it.
	WindowName string
	// Identity is the opaque ownership string stamped on the window as the
	// @cmdman_window tmux user option. It is what says which window is this
	// project's: [Run] reuses the window carrying it, and server-wide discovery
	// and teardown via [Down] go by the same stamp. Callers set this to a
	// stable, context-independent value:
	//
	//   - compose mux:    <wdhash>-<escaped-project>  (see compose.GenerateProjectIdentity)
	//   - standalone mux: empty here — defaults to the resolved window name.
	//     A spec is not tied to a directory the way a compose project is, so
	//     there is nothing context-independent to hash; the resulting identity
	//     is session-local, and [Down] with the same spec finds it within the
	//     same session.
	//
	// The identity is separate from the window name: a takeover window keeps
	// its original name but is stamped with the identity, so find-by-identity
	// works even after the window is renamed.
	Identity string
	// KeepCurrentWindow forbids the current-window takeover: the dashboard is
	// always built in a window of its own, even when the caller's window would
	// be safe to repurpose. Callers that are not the user's shell set it — a
	// launcher summoned as a popup runs in whatever window the user happens to
	// be looking at, and "start this project" must not repurpose that window
	// (D4's background start explicitly stays put).
	KeepCurrentWindow bool
	// Layout selects a specific layout to apply instead of cycling. It accepts
	// a layout name or a 0-based index (e.g. "2"). A name is matched first, so
	// a layout literally named "2" wins over index 2. Empty (the default)
	// cycles to the next layout after the one currently applied.
	Layout string
	// Config is the resolved cmdman configuration. Only DefaultFrame is read —
	// it names the frame [Run] docks around the window it just brought up (V9),
	// exactly as [FrameOptions.Config] names the one the frame verbs fall back
	// to. The zero value shows no frame, which is what a caller with no
	// configuration to offer wants.
	Config config.Config
	// Svc is the seam [FrameOptions.Svc] is, and is needed for the same reason:
	// a default_frame holding a managed: true entry (D19/V7) has a supervised
	// command to adopt, restart or create before a pane can view it. A def with no
	// managed entry never touches it, and an auto-show that needs it without it
	// warns like any other def that could not be shown.
	Svc FrameSvc
	// Env is the process env consulted for driver autodetection ($TMUX /
	// $ZELLIJ). Empty defaults to os.Environ().
	Env []string
	// Stdout is where the attach hint is printed when running outside a
	// multiplexer. Empty defaults to os.Stdout.
	Stdout io.Writer
}

// viewerDetachKeys is the tmux send-keys sequence that makes the in-pane
// cmdman viewers detach gracefully before the mux driver rebuilds the window
// (instead of being SIGKILLed mid-frame). [build.go] spawns the attach / logs
// viewers without overriding their --detach-keys, so they honor cmdman's
// default (ctrl-p,ctrl-q); this is that same sequence in tmux send-keys
// syntax. It is the mux layer's job to know what its viewers respond to — the
// muxctl driver is told via [muxctl.Config.ViewerDetachKeys].
var viewerDetachKeys = []string{"C-p", "C-q"}

// Run applies one layout from spec to the configured driver's cmdman-owned
// window: the window already stamped with opts.Identity when the project has
// one, else a window built for it. Reuse goes by the stamp alone — a window
// renamed since it was built is still the project's, and a same-named window
// belonging to another project (two checkouts of one repo, brought up from
// different directories) is not.
//
// When opts.Layout is set, that named/indexed layout is applied
// directly; otherwise the applied layout index is `(previousMarker+1) mod
// len(Layouts)`, read back from the existing window via
// [muxctl.Session.StatWindow] (a fresh window starts at index 0). Either way
// the applied index is persisted as the window marker, so a subsequent cycling
// Run continues from the layout just shown.
//
// A configured opts.Config.DefaultFrame is then shown around the window (V9),
// so a dashboard arrives inside its chrome without the user placing a frame per
// window; see [showDefaultFrame] for what is left alone and why nothing there
// can fail the up.
//
// When invoked outside a multiplexer (no $TMUX / $ZELLIJ in env), Run builds
// the window detached and prints an attach hint
// (`tmux attach -t <session>`) to opts.Stdout.
//
// v1 supports the tmux driver only; spec.Driver.Name == "zellij" or any future
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

	server, err := resolveServer(ctx, spec.Driver, env)
	if err != nil {
		return err
	}

	explicitSession := opts.SessionName != ""
	sessionName := resolveSessionName(
		opts.SessionName,
		env,
		func() (string, bool, error) { return server.CurrentSessionName(ctx) },
	)
	windowName := opts.WindowName
	if windowName == "" {
		windowName = sessionName
	}
	// Default the identity to the resolved window name when the caller did not
	// supply one. This is the standalone-mux default: the identity is
	// session-local (it equals the window name), so [Down] with the same spec
	// finds it within the same session. Compose callers always pass an explicit
	// Identity (compose.GenerateProjectIdentity), which is context-independent.
	identity := deriveIdentity(opts.Identity, opts.WindowName, sessionName)

	// With no explicit --session, take over the caller's current window when it
	// is safe to repurpose (single-pane or already ours) instead of spawning a
	// separate window. An explicit session means "target that session", so the
	// current-window takeover is disabled — as does a caller that is not the
	// user's own shell (see KeepCurrentWindow).
	reuseCurrent := !explicitSession && !opts.KeepCurrentWindow && envOf(env, "TMUX") != ""

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

	// Find the project's own window before building one, exactly as [Land] and
	// [Down] do: the identity stamp is what says which window is this project's,
	// and it is the cmdman side that knows it. The driver is only ever told
	// "this window" or "make one".
	rows, err := server.ListWindows(ctx, muxctl.ListOptions{
		Session:  opts.SessionName,
		Identity: identity,
	})
	if err != nil {
		return fmt.Errorf("mux: enumerate owned windows: %w", err)
	}

	cfg := muxctl.Config{
		SessionName:        sessionName,
		WindowName:         windowName,
		OwnedIdentity:      identity,
		ReuseCurrentWindow: reuseCurrent,
		ViewerDetachKeys:   viewerDetachKeys,
	}
	if row, ok := pickOwnedWindow(rows, sessionName); ok {
		// Target the window by id and leave the rest of the config out: the
		// window is already ours, so neither the name it currently wears nor the
		// current-window takeover has anything left to decide.
		cfg = muxctl.Config{
			WindowID:         row.WindowID,
			OwnedIdentity:    identity,
			ViewerDetachKeys: viewerDetachKeys,
		}
	}

	sess, err := server.New(ctx, cfg)
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

	if err := showDefaultFrame(
		ctx, server, sess.WindowID(), opts.Config.DefaultFrame, opts.Svc,
	); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"mux: up: default_frame not shown",
			"frame", opts.Config.DefaultFrame,
			"error", err,
		)
	}

	if envOf(env, "TMUX") == "" && envOf(env, "ZELLIJ") == "" {
		fmt.Fprintf(stdout, "Attach: tmux attach -t %s\n", sessionName)
	}
	return nil
}

// showDefaultFrame docks the configured default_frame around the window an up
// has just built (V9): the key both names the default def and applies it, which
// is what keeps the frame present across the per-window jumps D6's switching
// makes. An unset key shows nothing, and a window that already carries a frame
// is left as it is — auto-show never replaces a def the user selected
// deliberately.
//
// It targets the window by id rather than resolving the current one the frame
// verbs' way: `mux up` may well have built a window nobody is looking at, and
// the frame belongs around the dashboard it just made.
//
// Every failure here is the caller's to warn about and none of them is fatal:
// putting the dashboard up is what `mux up` is for, so a default def that is
// missing or broken costs the user a warning, never their windows.
func showDefaultFrame(
	ctx context.Context,
	server muxctl.Server,
	windowID string,
	defName string,
	svc FrameSvc,
) error {
	if defName == "" {
		return nil
	}
	shown, err := server.ReadWindowState(ctx, windowID, muxctl.StateKeyFrameDef)
	if err != nil {
		return fmt.Errorf("read the def shown on window %s: %w", windowID, err)
	}
	if shown != "" {
		return nil
	}
	spec, err := loadFrameSpec(ctx, defName)
	if err != nil {
		return err
	}
	target := frameTarget{server: server, windowID: windowID, shown: shown}
	return target.show(ctx, FrameOptions{Svc: svc}, defName, spec)
}

// resolveSessionName determines the session name to target.
// When override is non-empty it is returned verbatim.
// When inside tmux ($TMUX is set) queryCurrent is called; its result is
// returned only when it reports ok with a nil error. When not attached, on
// failure, or when not inside tmux, falls back to "cmdman".
func resolveSessionName(
	override string,
	env []string,
	queryCurrent func() (string, bool, error),
) string {
	if override != "" {
		return override
	}
	if envOf(env, "TMUX") != "" {
		if name, ok, err := queryCurrent(); ok && err == nil {
			return name
		}
	}
	return "cmdman"
}

// deriveIdentity returns the ownership identity [Run] stamps on the window and
// [Down]/[List] search for: the explicit identity when non-empty, else the
// window name, else the session name. Run and Down must share this derivation
// so a default-named `mux down` finds exactly what `mux up` stamped.
func deriveIdentity(identity, windowName, sessionName string) string {
	switch {
	case identity != "":
		return identity
	case windowName != "":
		return windowName
	default:
		return sessionName
	}
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

// resolveDriverName picks the driver name from the declared name (spec.Driver.Name)
// or autodetect. Empty declared triggers autodetect: $TMUX > $ZELLIJ > fallback "tmux".
func resolveDriverName(declared string, env []string) string {
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

// resolveServer resolves the driver name from spec.Name (see [resolveDriverName]),
// looks up the registered [muxctl.Driver], and [muxctl.Driver.Connect]s to the
// [muxctl.Server] the rest of spec describes (Path/Socket/Opts →
// [muxctl.ServerConfig] via [muxctl.DriverSpec.ServerConfig]). The
// known-but-unimplemented names "zellij"/"wezterm" keep the friendly "not
// implemented yet" wording; any other unregistered name surfaces
// [muxctl.LookupDriver]'s naming error (the usual cause is a missing blank import
// of the driver package at the composition root — cmd/cmdman/main.go).
func resolveServer(
	ctx context.Context,
	spec muxctl.DriverSpec,
	env []string,
) (muxctl.Server, error) {
	name := resolveDriverName(spec.Name, env)
	driver, err := muxctl.LookupDriver(name)
	if err != nil {
		switch name {
		case "zellij", "wezterm":
			return nil, fmt.Errorf(
				"mux: driver %q is not implemented yet (v1 ships tmux only)", name,
			)
		}
		return nil, err
	}
	return driver.Connect(ctx, spec.ServerConfig())
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
