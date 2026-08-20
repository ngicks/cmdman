package mux

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// scratchServer starts a tmux server of this test's own and returns the socket
// name to reach it by. The session it opens is what a current-session query has
// to answer with.
func scratchServer(t *testing.T, session string) string {
	t.Helper()
	bin := tmuxOrSkip(t)
	socket := "cmdman-mux-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() })
	tmuxRun(t, bin, socket, "new-session", "-d", "-s", session)
	return socket
}

// A name for the window an operation is about to act on is only worth anything
// if the operation looks for the same one, so what ResolveIdentity answers is
// pinned against what a teardown of the same options searches for — which Down
// names in the note it prints when it finds nothing.
func TestResolveIdentityIsWhatDownSearchesFor(t *testing.T) {
	socket := scratchServer(t, "current")
	driver := muxctl.DriverSpec{Name: "tmux", Socket: socket}

	tests := []struct {
		name    string
		session string
		window  string
		env     []string
		want    string
	}{
		{
			name: "outside a multiplexer nothing is asked",
			env:  []string{},
			want: "cmdman",
		},
		{
			name:    "a named session is the answer as typed",
			session: "elsewhere",
			env:     []string{},
			want:    "elsewhere",
		},
		{
			name:   "a window name outranks the session",
			window: "dashboard",
			env:    []string{},
			want:   "dashboard",
		},
		{
			name: "inside a multiplexer it is the session we are in",
			env:  []string{"TMUX=/dev/null,0,0"},
			want: "current",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveIdentity(context.Background(), IdentityOptions{
				Driver:      driver,
				SessionName: tt.session,
				WindowName:  tt.window,
				Env:         tt.env,
			})
			if err != nil {
				t.Fatalf("ResolveIdentity: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveIdentity() = %q, want %q", got, tt.want)
			}

			var note strings.Builder
			err = Down(context.Background(), DownOptions{
				Driver:      driver,
				SessionName: tt.session,
				WindowName:  tt.window,
				Env:         tt.env,
				Stdout:      &note,
			})
			if err != nil {
				t.Fatalf("Down: %v", err)
			}
			if !strings.Contains(note.String(), strconv.Quote(got)) {
				t.Fatalf("Down looked for something else: %q does not name %q", note.String(), got)
			}
		})
	}
}

// The frame verbs act on the window the caller is pointing at, and a caller
// naming that window ahead of a verb must be told the same window — or nothing
// at all, when there is nothing to point at.
func TestFrameTargetWindow(t *testing.T) {
	socket := scratchServer(t, "framed")
	driver := muxctl.DriverSpec{Name: "tmux", Socket: socket}

	t.Run("the named session's current window", func(t *testing.T) {
		got, ok, err := FrameTargetWindow(context.Background(), FrameOptions{
			Driver:  driver,
			Session: "framed",
			Env:     []string{},
		})
		if err != nil || !ok {
			t.Fatalf("FrameTargetWindow() = %q, %v, %v; want a window", got, ok, err)
		}
		if !strings.HasPrefix(got, "@") {
			t.Fatalf("FrameTargetWindow() = %q, want a tmux window id", got)
		}

		// The verbs resolve the same window through their own path.
		server, err := resolveServer(context.Background(), driver, []string{})
		if err != nil {
			t.Fatal(err)
		}
		want, err := frameWindowID(context.Background(), server, "framed", []string{})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("FrameTargetWindow() = %q, but the frame verbs act on %q", got, want)
		}
	})

	t.Run("nothing pointed at", func(t *testing.T) {
		got, ok, err := FrameTargetWindow(context.Background(), FrameOptions{
			Driver: driver,
			Env:    []string{},
		})
		if err != nil {
			t.Fatalf("FrameTargetWindow: %v", err)
		}
		if ok || got != "" {
			t.Fatalf("FrameTargetWindow() = %q, %v; want no window outside a multiplexer",
				got, ok)
		}
	})
}
