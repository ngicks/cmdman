// Command muxctltester is a developer harness for the pkg/muxctl tmux
// driver. It loads a layout YAML file (the muxctl wire form: a MuxSpec
// document, not the higher-level cmdman-layer DSL) and applies the first
// layout to a tmux window.
//
// Usage:
//
//	muxctltester [flags] <layout-file>
//
// Window targeting:
//
//   - When invoked from inside a tmux client whose currently-focused window
//     name matches -window (the muxctl-owned name, default "cmdman"), the
//     layout is applied IN PLACE to the current window — i.e. the window
//     is reset and rebuilt from the spec. Useful for iterating: keep your
//     focus in the muxctl window and re-run the tester to see changes.
//
//   - Otherwise (current window is not muxctl-owned, or we are outside
//     tmux entirely), muxctltester CREATES A NEW window — it does not
//     reuse any other window that happens to share the muxctl-owned name.
//     Outside tmux it creates the window in a freshly-detached session
//     and prints an attach hint.
//
// muxctltester writes a one-line summary to stderr describing which path
// was taken and the resulting tmux window id.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "muxctltester:", err)
		os.Exit(1)
	}
}

func run() error {
	sessionFlag := flag.String(
		"session",
		"",
		"tmux session name (default: current session via $TMUX, else \"cmdman\")",
	)
	windowFlag := flag.String(
		"window",
		"cmdman",
		"muxctl-owned window name; matched against the current window to decide in-place vs new",
	)
	socketFlag := flag.String(
		"socket",
		"",
		"tmux -L socket name (default: empty, server-default / inherited via $TMUX)",
	)
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(),
			"usage: muxctltester [flags] <layout-file>")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		return errors.New("expected exactly one positional argument: path to layout YAML")
	}
	path := flag.Arg(0)

	spec, err := decodeSpecFile(path)
	if err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	if len(spec.Layouts) == 0 {
		return fmt.Errorf("%s has no layouts", path)
	}

	ctx := contextkey.WithSlogLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(os.Stderr, nil)),
	)

	target, err := resolveTarget(ctx, *socketFlag, *sessionFlag, *windowFlag)
	if err != nil {
		return err
	}

	sess, err := tmux.New(ctx, tmux.Config{
		Socket:   *socketFlag,
		WindowID: target.windowID,
	})
	if err != nil {
		return fmt.Errorf("tmux.New: %w", err)
	}

	// Cycle: read the previous layout marker off the target window and
	// advance to the next layout. First run on an unmarked window starts
	// at layout 0.
	prev, err := sess.StatWindow(ctx, target.windowID)
	if err != nil {
		return fmt.Errorf("StatWindow: %w", err)
	}
	idx := nextLayout(prev.Marker, len(spec.Layouts))
	layout := spec.Layouts[idx]

	panes, err := sess.ApplyLayout(ctx, layout.Root, idx)
	if err != nil {
		return fmt.Errorf("ApplyLayout: %w", err)
	}

	fmt.Fprintf(
		os.Stderr,
		"muxctltester: applied layout %q (index %d) to %s; windowId=%s, panes=%d (prev marker=%d)\n",
		layout.Name,
		idx,
		target.summary,
		sess.WindowID(),
		len(panes),
		prev.Marker,
	)
	if target.attachHint != "" {
		fmt.Fprintln(os.Stderr, "attach:", target.attachHint)
	}
	return nil
}

// nextLayout picks the next layout index given the previously-embedded
// marker and the total number of layouts. An unset/out-of-range marker
// (e.g. -1 or >= n) starts at index 0.
func nextLayout(prevMarker, n int) int {
	if prevMarker < 0 || prevMarker >= n {
		return 0
	}
	return (prevMarker + 1) % n
}

// targetWindow is the resolved tmux window the tester will hand to
// muxctl/tmux. summary describes which path was taken (for the report
// line); attachHint, when non-empty, is the tmux command the user should
// run to reach the window (only set when we built outside any tmux client).
type targetWindow struct {
	windowID   string
	summary    string
	attachHint string
}

// resolveTarget picks the tmux window the layout should be applied to:
//
//   - Inside tmux, when the current window is recognized as muxctl-owned —
//     either its name matches ownedName, or its panes carry a muxctl
//     marker suffix — returns the current window's id (apply in place).
//
//   - Inside tmux, when the current window has only a single pane: takes
//     it over as an "empty" window safe to repurpose.
//
//   - Inside tmux otherwise (multi-pane, unowned current window): creates
//     a fresh window in the current session and returns its id.
//
//   - Outside tmux: ensures the named session exists and creates a fresh
//     window in it, returning its id and an attach hint.
//
// resolveTarget never reuses an existing non-current window — the
// reuse decision is strictly about the current window.
func resolveTarget(
	ctx context.Context,
	socket, sessionFlag, ownedName string,
) (targetWindow, error) {
	exe := tmuxExec{socket: socket}

	if os.Getenv("TMUX") != "" {
		curName, curID, curPanes, err := exe.currentWindow(ctx)
		if err != nil {
			return targetWindow{}, fmt.Errorf("detect current window: %w", err)
		}
		if curName == ownedName {
			return targetWindow{
				windowID: curID,
				summary:  fmt.Sprintf("current window (muxctl-owned: %q)", curName),
			}, nil
		}
		// Probe for an embedded muxctl marker — a marker-bearing window
		// is one of ours, regardless of its tmux name.
		if marker, ok := exe.windowMarker(ctx, curID); ok {
			return targetWindow{
				windowID: curID,
				summary: fmt.Sprintf(
					"current window %q (muxctl-marked, marker=%d)", curName, marker,
				),
			}, nil
		}
		if curPanes <= 1 {
			return targetWindow{
				windowID: curID,
				summary: fmt.Sprintf(
					"current window %q (single-pane, taking over)", curName,
				),
			}, nil
		}
		sessionName, err := resolveSession(ctx, exe, sessionFlag)
		if err != nil {
			return targetWindow{}, err
		}
		newID, err := exe.newWindow(ctx, sessionName, ownedName)
		if err != nil {
			return targetWindow{}, fmt.Errorf("create new window: %w", err)
		}
		return targetWindow{
			windowID: newID,
			summary: fmt.Sprintf(
				"new window %q in session %q (current window %q has %d panes and is not muxctl-owned)",
				ownedName,
				sessionName,
				curName,
				curPanes,
			),
		}, nil
	}

	// Outside tmux: ensure a session exists, then always create a new window.
	sessionName := sessionFlag
	if sessionName == "" {
		sessionName = "cmdman"
	}
	if err := exe.ensureSession(ctx, sessionName); err != nil {
		return targetWindow{}, fmt.Errorf("ensure session %q: %w", sessionName, err)
	}
	newID, err := exe.newWindow(ctx, sessionName, ownedName)
	if err != nil {
		return targetWindow{}, fmt.Errorf("create new window: %w", err)
	}
	return targetWindow{
		windowID: newID,
		summary: fmt.Sprintf(
			"new window %q in session %q (detached, not inside tmux)",
			ownedName,
			sessionName,
		),
		attachHint: fmt.Sprintf("tmux %sattach -t %s", attachSocketArg(socket), sessionName),
	}, nil
}

func decodeSpecFile(path string) (muxctl.MuxSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return muxctl.MuxSpec{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	spec, err := muxctl.Decode(f)
	if err != nil {
		return muxctl.MuxSpec{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return spec, nil
}

func resolveSession(ctx context.Context, exe tmuxExec, flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if os.Getenv("TMUX") == "" {
		return "cmdman", nil
	}
	out, err := exe.run(ctx, "display-message", "-p", "#{session_name}")
	if err != nil {
		return "", fmt.Errorf("detect current tmux session: %w", err)
	}
	return out, nil
}

func attachSocketArg(socket string) string {
	if socket == "" {
		return ""
	}
	return "-L " + socket + " "
}

// tmuxExec is a thin wrapper around `tmux ...` invocations that the
// tester needs to perform itself (window probing, fresh-window creation).
// muxctl/tmux has its own equivalent, but it is package-internal.
type tmuxExec struct {
	socket string
}

func (t tmuxExec) run(ctx context.Context, args ...string) (string, error) {
	if t.socket != "" {
		args = append([]string{"-L", t.socket}, args...)
	}
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// windowMarker reports the marker recorded on windowID's panes via the
// @cmdman_marker per-pane option. Mirrors the read in pkg/muxctl/tmux/stat.go;
// duplicated here so the tester can probe "is this window muxctl-owned"
// without first having to construct a muxctl.Session (which would side-effect
// the window by turning on pane-border-status).
//
// Returns (marker, true) only when every pane carries the SAME marker;
// returns (-1, false) when no marker is present, panes disagree, or the
// window has no panes / fails to list.
func (t tmuxExec) windowMarker(ctx context.Context, windowID string) (int, bool) {
	out, err := t.run(ctx, "list-panes", "-t", windowID, "-F", "#{@cmdman_marker}")
	if err != nil || out == "" {
		return -1, false
	}
	marker := -1
	for line := range strings.SplitSeq(out, "\n") {
		n, err := strconv.Atoi(line)
		if err != nil {
			return -1, false
		}
		if marker == -1 {
			marker = n
			continue
		}
		if n != marker {
			return -1, false
		}
	}
	if marker < 0 {
		return -1, false
	}
	return marker, true
}

func (t tmuxExec) currentWindow(ctx context.Context) (name, id string, panes int, err error) {
	out, err := t.run(ctx, "display-message", "-p",
		"#{window_name}\t#{window_id}\t#{window_panes}")
	if err != nil {
		return "", "", 0, err
	}
	parts := strings.SplitN(out, "\t", 3)
	if len(parts) != 3 {
		return "", "", 0, fmt.Errorf("unexpected display-message output: %q", out)
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", "", 0, fmt.Errorf("parse window_panes %q: %w", parts[2], err)
	}
	return parts[0], parts[1], n, nil
}

func (t tmuxExec) ensureSession(ctx context.Context, name string) error {
	if _, err := t.run(ctx, "has-session", "-t", "="+name); err == nil {
		return nil
	}
	_, err := t.run(ctx, "new-session", "-d", "-s", name)
	if err != nil && strings.Contains(err.Error(), "duplicate session") {
		return nil
	}
	return err
}

// newWindow creates a new window in sessionName and returns its window id.
// tmux allows duplicate window names within a session, so this is always
// a fresh window even if another window with the same name already exists.
func (t tmuxExec) newWindow(ctx context.Context, sessionName, windowName string) (string, error) {
	return t.run(ctx,
		"new-window", "-d", "-t", sessionName,
		"-n", windowName, "-P", "-F", "#{window_id}",
	)
}
