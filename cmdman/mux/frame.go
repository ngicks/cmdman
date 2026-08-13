package mux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// frameMainPane names the placeholder leaf [frame.Spec.Carve] carves around and
// [muxctl.Session.ShowFrame] resizes. It stands for the panes the window already
// holds, so it carries no command and nothing is ever spawned for it.
const frameMainPane = "main"

// FrameOptions configures the frame verbs [FrameShow], [FrameHide],
// [FrameCycle] and [FrameList].
type FrameOptions struct {
	// Def names the frame def to show: a bare name resolved under the frame
	// config dir (see [frame.DiscoverFile]) or a path. Empty falls back to
	// Config.DefaultFrame; with neither set, [FrameShow] errors and names the
	// defs it found on disk. [FrameHide] and [FrameCycle] ignore it — hide
	// removes whatever is shown, and cycle takes the next def off the sorted
	// def list.
	Def string
	// Session names the multiplexer session whose current window the verb acts
	// on. Empty targets the window the caller is sitting in, which requires
	// being inside the multiplexer: a frame is a per-window fixture pointed at
	// by being there, so outside it with no session named the verbs error
	// rather than guess a window.
	//
	// [FrameList] reads it as a pure narrowing filter instead, exactly as
	// [ListOptions.SessionName] does: empty lists every framed window on the
	// server.
	Session string
	// Config is the resolved cmdman configuration. Only DefaultFrame is read —
	// the second step of def resolution.
	Config config.Config
	// Svc is the seam to cmdman's supervised commands, through which a
	// managed: true entry (D19/V7) is adopted, restarted or created. A def with no managed
	// entry never touches it, so it may be nil for defs built of components and
	// ephemeral commands; a managed entry with no service behind it is an error
	// naming the entry rather than a pane quietly running the raw command.
	Svc FrameSvc
	// Executable is the cmdman binary a frame pane runs — a component's widget
	// and a managed entry's viewer alike, the same value [PaneArgvOpts] carries
	// for a dashboard's panes. Empty resolves the running binary, which is what
	// a caller that is the cmdman binary wants.
	Executable string
	// Driver selects and configures the multiplexer server, exactly as in
	// [ListOptions]: an empty Driver.Name autodetects from Env the same way
	// [Run] does, and Socket/Path/Opts must match those the window was built
	// with.
	Driver muxctl.DriverSpec
	// Env is the process env consulted for driver autodetection and for the
	// inside-the-multiplexer check. Empty defaults to os.Environ().
	Env []string
}

// FrameListResult is what [FrameList] reports: the defs available and the
// frames currently up. Presentation is cmdman/cli's job.
type FrameListResult struct {
	// Defs are the discoverable def names in sorted order (see
	// [frame.ListNamedDefs]). Nil when the frame dir holds none.
	Defs []string
	// Shown maps a driver-native window id (e.g. tmux "@3") to the name of the
	// def shown around that window. Unframed windows are absent, so an empty
	// map means no frame is up anywhere in scope.
	Shown map[string]string
}

// FrameShow shows a frame around the target window (see [FrameOptions.Session]
// for which window that is), with the def resolved flag > config
// default_frame > an error naming the candidates.
//
// Showing the def already up is a no-op, and showing a different one replaces
// it in place (V2) — which is what makes this verb D15's "selected" as well.
// The def is read and carved before the multiplexer is touched, so a typo or a
// broken def never disturbs the frame already standing.
func FrameShow(ctx context.Context, opts FrameOptions) error {
	defName, err := resolveFrameDef(opts.Def, opts.Config.DefaultFrame)
	if err != nil {
		return err
	}
	spec, err := loadFrameSpec(ctx, defName)
	if err != nil {
		return err
	}
	target, err := openFrameTarget(ctx, opts)
	if err != nil {
		return err
	}
	return target.show(ctx, opts, defName, spec)
}

// FrameHide removes the frame shown around the target window, leaving the
// project region to expand into it. A window carrying no frame is a quiet
// no-op.
func FrameHide(ctx context.Context, opts FrameOptions) error {
	target, err := openFrameTarget(ctx, opts)
	if err != nil {
		return err
	}
	return target.hide(ctx)
}

// FrameCycle shows the next def in the sorted [frame.ListNamedDefs] order,
// starting after the one currently shown and wrapping; from an unframed window
// it shows the first def (V3). A def that fails to load is reported with its
// name and path rather than skipped, so a broken def is visible instead of
// silently absent from the rotation.
func FrameCycle(ctx context.Context, opts FrameOptions) error {
	names, err := frame.ListNamedDefs()
	if err != nil {
		return fmt.Errorf("mux: frame: list defs: %w", err)
	}
	if len(names) == 0 {
		return fmt.Errorf("mux: frame: no defs to cycle through%s", frameDirHint())
	}

	target, err := openFrameTarget(ctx, opts)
	if err != nil {
		return err
	}
	next := nextFrameDef(names, target.shown)
	spec, err := loadFrameSpec(ctx, next)
	if err != nil {
		return err
	}
	return target.show(ctx, opts, next, spec)
}

// FrameList reports the defs on disk and which def is shown on which window
// (V4), the two halves of "what can I show" and "what is up". opts.Session
// narrows the window scan to one session; empty scans the server.
func FrameList(ctx context.Context, opts FrameOptions) (FrameListResult, error) {
	names, err := frame.ListNamedDefs()
	if err != nil {
		return FrameListResult{}, fmt.Errorf("mux: frame: list defs: %w", err)
	}

	server, err := resolveServer(ctx, opts.Driver, frameEnv(opts))
	if err != nil {
		return FrameListResult{}, err
	}
	rows, err := server.ListWindows(ctx, muxctl.ListOptions{Session: opts.Session})
	if err != nil {
		return FrameListResult{}, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}

	shown := make(map[string]string)
	for _, r := range rows {
		if r.Frame != "" {
			shown[r.WindowID] = r.Frame
		}
	}
	return FrameListResult{Defs: names, Shown: shown}, nil
}

// frameTarget is the window a frame verb acts on, together with the def shown
// around it when the target was resolved ("" when unframed). The verbs read
// that state once and branch on it: it decides no-op vs replace for show, and
// no-op vs teardown for hide.
type frameTarget struct {
	server   muxctl.Server
	windowID string
	shown    string
}

func openFrameTarget(ctx context.Context, opts FrameOptions) (frameTarget, error) {
	env := frameEnv(opts)

	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return frameTarget{}, err
	}
	windowID, err := frameWindowID(ctx, server, opts.Session, env)
	if err != nil {
		return frameTarget{}, err
	}
	shown, err := server.ReadWindowState(ctx, windowID, muxctl.StateKeyFrameDef)
	if err != nil {
		return frameTarget{}, fmt.Errorf(
			"mux: frame: read the def shown on window %s: %w", windowID, err)
	}
	return frameTarget{server: server, windowID: windowID, shown: shown}, nil
}

// show docks spec around the target window as defName.
func (t frameTarget) show(
	ctx context.Context,
	opts FrameOptions,
	defName string,
	spec frame.Spec,
) error {
	if t.shown == defName {
		return nil
	}

	root, err := spec.Carve(
		muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: frameMainPane}},
		frameComponentArgv(opts.Executable),
	)
	if err != nil {
		return fmt.Errorf("mux: frame def %q: %w", defName, err)
	}

	// Managed entries (D19/V7) are adopted or created here, before the panes
	// exist, so the pane argv can attach to the running command.
	if err := adoptManagedEntries(ctx, opts, defName, spec, &root); err != nil {
		return err
	}

	sess, err := t.server.New(ctx, muxctl.Config{
		WindowID:         t.windowID,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if err != nil {
		return err
	}
	if t.shown != "" {
		// Repeated ShowFrame stacks docks on the driver, so replacing a def is
		// always hide-then-show — never a second frame carved around the first.
		if err := hideFrameOf(ctx, sess, t.shown); err != nil {
			return err
		}
	}
	if err := sess.ShowFrame(ctx, root, frameMainPane, defName); err != nil {
		return fmt.Errorf("mux: frame: show %q: %w", defName, err)
	}
	return nil
}

// hide removes the frame the target carries, if any.
func (t frameTarget) hide(ctx context.Context) error {
	if t.shown == "" {
		return nil
	}

	// Open rather than New: hiding must not stamp anything onto the window it
	// is handing back.
	sess, ok, err := t.server.Open(ctx, muxctl.Config{
		WindowID:         t.windowID,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if err != nil {
		return fmt.Errorf("mux: frame: open window %s: %w", t.windowID, err)
	}
	if !ok {
		// The window went away between the state read and the open; nothing to
		// hide, which is what hide means on an unframed window anyway.
		return nil
	}
	return hideFrameOf(ctx, sess, t.shown)
}

// hideFrameOf tears the frame named defName off sess. It is the one place the
// hide half lives, shared by [frameTarget.hide] and the replace path of
// [frameTarget.show], because F7 pins the order both must follow: a managed
// entry's viewer is detached before the driver removes its pane.
func hideFrameOf(ctx context.Context, sess muxctl.Session, defName string) error {
	// Managed entries (D19/V7) are detached here — their supervised commands
	// keep running while the panes go away — and detaching one is precisely NOT
	// stopping it: what the pane holds is a `cmdman attach` client, disposable
	// by construction (see muxctl/doc.go), and the command it views belongs to
	// its own monitor process. So the detach is the pane teardown below, and the
	// preservation is the absence of any call that would end the command: the
	// verbs reach cmdman through [FrameSvc], which cannot stop anything.
	//
	// Nothing is typed at the viewer first. A graceful key-sequence quiesce
	// would need a driver primitive addressing frame panes by name, and muxctl
	// deliberately exposes none (F7 keeps frame-pane policy above the driver
	// seam), while the ungraceful path is already harmless: the SIGHUP the pane
	// teardown delivers is not among cmdman's forwarded signals
	// (cmdman/cli.forwardedSignals, pinned by
	// TestForwardedSignals_DoesNotIncludeSIGHUP), so it reaches the viewer and
	// stops there.
	if err := sess.HideFrame(ctx); err != nil {
		return fmt.Errorf("mux: frame: hide %q: %w", defName, err)
	}
	return nil
}

// adoptManagedEntries gives every managed: true entry of spec the supervised
// command V7 names it by, and points that entry's pane at a viewer for the
// command instead of at the entry's own argv. That substitution is what makes a
// managed entry survive the frame: the pane runs a disposable attach client,
// and what it shows runs under cmdman's supervision, out of reach of anything
// the multiplexer does to the pane.
//
// It rewrites the carved tree rather than carving a doctored spec so that the
// def the user wrote stays the thing that was validated, and so a def that does
// not carve never reaches the service.
//
// A def with no managed entry never consults svc — a frame of components and
// ephemeral commands is fully usable with no cmdman service behind it.
func adoptManagedEntries(
	ctx context.Context,
	opts FrameOptions,
	defName string,
	spec frame.Spec,
	root *muxctl.PaneSpec,
) error {
	managed := managedEntryIndexes(spec)
	if len(managed) == 0 {
		return nil
	}
	svc := opts.Svc
	if svc == nil {
		return fmt.Errorf(
			"mux: frame def %q: entry %d is managed: true, which needs a cmdman service",
			defName, managed[0],
		)
	}

	cfg := svc.Config()
	argvOpts := PaneArgvOpts{
		Executable: frameExecutable(opts.Executable),
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
	}
	for _, i := range managed {
		entry := spec.Entries[i]
		id, err := ensureFrameCommand(ctx, svc, frameCommand{
			Name:  FrameCommandName(defName, i),
			Argv:  entry.Command,
			Hooks: entry.Hooks,
		})
		if err != nil {
			return fmt.Errorf("mux: frame def %q: entry %d: %w", defName, i, err)
		}
		if !setLeafCmd(root, frame.EntryPaneName(i), paneArgv(argvOpts, ModeAttach, id)) {
			return fmt.Errorf(
				"mux: frame def %q: entry %d has no pane in the carved frame", defName, i)
		}
	}
	return nil
}

// managedEntryIndexes returns the positions of spec's managed entries, which
// are also the indexes their identities (see [FrameCommandName]) and their pane
// names (see [frame.EntryPaneName]) are formed from.
func managedEntryIndexes(spec frame.Spec) []int {
	var out []int
	for i, e := range spec.Entries {
		if e.Managed {
			out = append(out, i)
		}
	}
	return out
}

// setLeafCmd points the leaf named name at argv, reporting whether the tree held
// one.
func setLeafCmd(node *muxctl.PaneSpec, name string, argv []string) bool {
	if node.Name == name {
		node.Cmd = argv
		return true
	}
	for i := range node.Panes {
		if setLeafCmd(&node.Panes[i], name, argv) {
			return true
		}
	}
	return false
}

// frameWindowID resolves the window a frame verb acts on: the current window of
// the named session, or the one the caller is sitting in when no session is
// named. There is no window selector — a frame is a per-window fixture the user
// points at by being there — so outside the multiplexer with no session named
// there is nothing to point at, and that is an error rather than a guess.
func frameWindowID(
	ctx context.Context,
	server muxctl.Server,
	session string,
	env []string,
) (string, error) {
	if session == "" && envOf(env, "TMUX") == "" && envOf(env, "ZELLIJ") == "" {
		return "", errors.New(
			"mux: frame: not inside a multiplexer; run this inside tmux or name a session",
		)
	}

	windowID, ok, err := server.CurrentWindowID(ctx, session)
	if err != nil {
		return "", fmt.Errorf("mux: frame: resolve the current window: %w", err)
	}
	if !ok {
		if session != "" {
			return "", fmt.Errorf("mux: frame: session %q has no window to frame", session)
		}
		return "", errors.New("mux: frame: cannot tell which window you are in")
	}
	return windowID, nil
}

// resolveFrameDef picks the def a verb acts on: the explicit name, else the
// configured default_frame, else an error naming what is on disk (D15 — the def
// comes from config or a flag, never from discovery).
func resolveFrameDef(def, configured string) (string, error) {
	if def != "" {
		return def, nil
	}
	if configured != "" {
		return configured, nil
	}

	names, err := frame.ListNamedDefs()
	if err != nil {
		return "", fmt.Errorf("mux: frame: list defs: %w", err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf(
			"mux: frame: no def named and default_frame is unset; no defs found%s",
			frameDirHint(),
		)
	}
	return "", fmt.Errorf(
		"mux: frame: no def named and default_frame is unset; available defs: %s",
		strings.Join(names, ", "),
	)
}

// loadFrameSpec loads and normalizes the named def. The def name is added to
// the loader's message, which already names the path it tried, so a failure
// says both which def was asked for and which file answered for it — plus, for
// a bare name nothing answered for, the defs the user could have meant.
func loadFrameSpec(ctx context.Context, defName string) (frame.Spec, error) {
	spec, err := frame.LoadAndNormalize(ctx, defName)
	if err != nil {
		return frame.Spec{}, fmt.Errorf(
			"mux: frame def %q: %w%s", defName, err, frameCandidateHint(defName, err))
	}
	return spec, nil
}

// frameCandidateHint returns the tail naming the defs defName could have meant,
// for the one failure that is a discovery failure: a bare name (see
// [frame.IsBareName]) that no file answered for. Its shape matches the errors
// [resolveFrameDef] raises with no def named at all, which answer the same
// question.
//
// Two failures are deliberately left un-hinted. An explicit path is the user's
// own pointer, so the path in the error is the whole answer. And a def that
// exists but fails to parse or cannot be read is not missing: offering
// candidates there would read as "no such def" and bury what actually went
// wrong.
func frameCandidateHint(defName string, err error) string {
	if !errors.Is(err, os.ErrNotExist) || !frame.IsBareName(defName) {
		return ""
	}
	names, listErr := frame.ListNamedDefs()
	if listErr != nil {
		return ""
	}
	if len(names) == 0 {
		return "; no defs found" + frameDirHint()
	}
	return "; available defs: " + strings.Join(names, ", ")
}

// nextFrameDef returns the def cycle advances to: the one after shown in the
// sorted def list, wrapping. An unframed window (and one showing a def that is
// not among the named ones, e.g. shown by path or since deleted) starts the
// rotation at the first def (V3).
func nextFrameDef(names []string, shown string) string {
	i := slices.Index(names, shown)
	if i < 0 {
		return names[0]
	}
	return names[(i+1)%len(names)]
}

// frameComponentArgv resolves a component: entry to the widget invocation that
// runs it (D37).
func frameComponentArgv(exe string) frame.ComponentArgv {
	return frame.WidgetArgv(frameExecutable(exe))
}

// frameExecutable is the cmdman binary a frame pane runs — for a component's
// widget and for a managed entry's viewer alike. An unset one defaults to the
// binary that carved the frame, so a build docks its own widgets and attaches
// with its own viewer rather than with whatever cmdman is on PATH; an
// unresolvable executable falls back to that PATH lookup, which is the best a
// pane can do with no path to the running binary.
func frameExecutable(exe string) string {
	if exe != "" {
		return exe
	}
	resolved, err := os.Executable()
	if err != nil {
		return "cmdman"
	}
	return resolved
}

// frameDirHint names the directory defs are discovered under, for the errors
// that found none: with no defs to list, where they are expected to live is the
// only useful thing left to say.
func frameDirHint() string {
	dir, err := config.FrameConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return " under " + dir
}

func frameEnv(opts FrameOptions) []string {
	if opts.Env == nil {
		return os.Environ()
	}
	return opts.Env
}
