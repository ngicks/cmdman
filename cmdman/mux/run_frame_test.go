package mux

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/go-common/contextkey"
)

// upSpec is the spec every auto-frame test brings up: one layout of two
// sleeping panes, the same project shape projectWindow builds by hand.
func upSpec(socket string) muxctl.MuxSpec {
	return muxctl.MuxSpec{
		Driver: muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Layouts: []muxctl.Layout{{
			Name: "dash",
			Root: muxctl.PaneSpec{Container: muxctl.Container{
				Dir:    muxctl.DirHorizontal,
				Splits: []muxctl.Size{{N: 1}, {N: 1}},
				Panes:  []muxctl.PaneSpec{sleepLeaf("web"), sleepLeaf("worker")},
			}},
		}},
	}
}

// upOpts brings the dashboard up in the session the frame tests build. The env
// is empty on purpose: outside tmux the current-window takeover is off, so the
// window under test is the one [Run] resolves by name rather than whichever
// window the session happens to be sitting on.
func upOpts(windowName, identity, defaultFrame string) RunOptions {
	return RunOptions{
		SessionName: frameTestSession,
		WindowName:  windowName,
		Identity:    identity,
		Config:      config.Config{DefaultFrame: defaultFrame},
		Env:         []string{},
		Stdout:      io.Discard,
	}
}

// upWindowID returns the id of the window stamped with identity — how these
// tests address a window [Run] created instead of one they built themselves.
func upWindowID(t *testing.T, socket, identity string) string {
	t.Helper()
	rows, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List by identity %q = %v, want the one window the up built", identity, rows)
	}
	return rows[0].WindowID
}

// frameLogCtx returns a context carrying a logger, plus the buffer it writes to,
// so the warn-and-continue paths can be read back.
func frameLogCtx() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	return contextkey.WithSlogLogger(context.Background(), logger), &buf
}

// assertBareUp checks that the up left a working dashboard with no frame around
// it — what every auto-show that does not happen must degrade to.
func assertBareUp(t *testing.T, bin, socket, identity string) {
	t.Helper()
	windowID := upWindowID(t, socket, identity)
	if got := framePaneCount(t, bin, socket, windowID); got != 0 {
		t.Errorf("frame panes = %d, want the window bare", got)
	}
	if got := shownDef(t, bin, socket, windowID); got != "" {
		t.Errorf("@cmdman_frame_def = %q, want it unset", got)
	}
	if got := projectPaneTitles(t, bin, socket, windowID); !slices.Equal(
		got, []string{"web", "worker"},
	) {
		t.Errorf("project panes = %v, want the layout's [web worker]", got)
	}
}

// TestUpAutoFrame is V9: `mux up` docks the configured default_frame around the
// window it brings up, so a dashboard arrives inside its chrome without the
// user placing a frame per window. The frame is the up's passenger and never
// its master — an unset key, a window already framed, and a def that cannot be
// shown each leave the dashboard standing.
//
// The subtest names stay short: each builds a tmux server on a socket named
// after the test, and the socket path has to fit in sockaddr_un.
func TestUpAutoFrame(t *testing.T) {
	t.Run("fresh window", func(t *testing.T) {
		frameDefs(t, "dev")
		socket, bare := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		err := Run(context.Background(), upSpec(socket), upOpts("up", "up", "dev"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		windowID := upWindowID(t, socket, "up")
		if got := shownDef(t, bin, socket, windowID); got != "dev" {
			t.Errorf("@cmdman_frame_def = %q, want the configured dev", got)
		}
		if got := framePaneCount(t, bin, socket, windowID); got != 1 {
			t.Errorf("frame panes = %d, want the def's one entry docked", got)
		}
		if got := projectPaneTitles(t, bin, socket, windowID); !slices.Equal(
			got, []string{"web", "worker"},
		) {
			t.Errorf("project panes = %v, want the layout's [web worker]", got)
		}

		// The frame goes around the window the up built. The session's current
		// window is a different one, and resolving the target the frame verbs'
		// way — the window the user is sitting in — would have framed that.
		if got := framePaneCount(t, bin, socket, bare); got != 0 {
			t.Errorf("frame panes on the session's current window = %d, want none", got)
		}
		if got := shownDef(t, bin, socket, bare); got != "" {
			t.Errorf("@cmdman_frame_def on the session's current window = %q, want unset", got)
		}
	})

	t.Run("key unset", func(t *testing.T) {
		// Defs exist on disk: it is the key, not their availability, that
		// decides whether a window comes up framed.
		frameDefs(t, "dev")
		socket, _ := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		if err := Run(context.Background(), upSpec(socket), upOpts("up", "up", "")); err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertBareUp(t, bin, socket, "up")
	})

	t.Run("already framed", func(t *testing.T) {
		frameDefs(t, "alt", "dev")
		socket, windowID := framedWindow(t, "abc123-myproject", "dev")
		bin := tmuxOrSkip(t)

		before := framePaneIDs(t, bin, socket, windowID)
		if len(before) != 1 {
			t.Fatalf("frame panes before the up = %v, want the docked one", before)
		}

		err := Run(
			context.Background(), upSpec(socket), upOpts("dash", "abc123-myproject", "alt"),
		)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		// Same panes, not merely the same count: a replace would pass a count
		// check while killing what the def the user chose was running.
		if got := shownDef(t, bin, socket, windowID); got != "dev" {
			t.Errorf("@cmdman_frame_def = %q, want the deliberately shown dev", got)
		}
		if after := framePaneIDs(t, bin, socket, windowID); !slices.Equal(after, before) {
			t.Errorf("frame panes = %v after the up, want the untouched %v", after, before)
		}
	})

	t.Run("missing def", func(t *testing.T) {
		frameDefs(t, "dev")
		socket, _ := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		ctx, logs := frameLogCtx()
		if err := Run(ctx, upSpec(socket), upOpts("up", "up", "nope")); err != nil {
			t.Fatalf("Run with an unresolvable default_frame: %v", err)
		}
		assertBareUp(t, bin, socket, "up")
		if got := logs.String(); !strings.Contains(got, "default_frame") ||
			!strings.Contains(got, "nope") {
			t.Errorf("log = %q, want a warning naming default_frame and the def", got)
		}
	})

	t.Run("broken def", func(t *testing.T) {
		dir := frameDefs(t)
		writeFrameDef(
			t, dir, "bad",
			"frame:\n  - edge: sideways\n    size: 2\n    command: [\"true\"]\n",
		)
		socket, _ := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		ctx, logs := frameLogCtx()
		if err := Run(ctx, upSpec(socket), upOpts("up", "up", "bad")); err != nil {
			t.Fatalf("Run with a broken default_frame: %v", err)
		}
		assertBareUp(t, bin, socket, "up")
		if got := logs.String(); !strings.Contains(got, "default_frame") ||
			!strings.Contains(got, "bad") {
			t.Errorf("log = %q, want a warning naming default_frame and the def", got)
		}
	})
}
