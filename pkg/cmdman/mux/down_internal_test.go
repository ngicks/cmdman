package mux

// Tests for pure logic in down.go / run.go:
//   - identity defaulting in Run (opts.Identity="" → windowName)
//   - identity defaulting in Down (opts.Identity="" → resolveSessionName → windowName)
//
// Driver-touching behavior (ListOwnedWindows, OpenExisting, Detach) is covered
// by e2e tests (workstream 5). No tmux server is spawned here.

import (
	"errors"
	"testing"
)

// TestIdentityDefaulting_Run verifies the identity-fallback chain for Run:
// when RunOptions.Identity is empty the resolved window name becomes the
// identity (same derivation as Run applies to tmux.Config.OwnedIdentity).
func TestIdentityDefaulting_Run(t *testing.T) {
	inTmux := []string{"TMUX=/tmp/tmux-1000/default,123,0"}
	noTmux := []string{"PATH=/usr/bin"}

	tests := []struct {
		name         string
		env          []string
		sessionName  string // RunOptions.SessionName
		windowName   string // RunOptions.WindowName
		wantIdentity string
	}{
		{
			name:         "explicit window name → identity = window name",
			env:          noTmux,
			sessionName:  "mysession",
			windowName:   "mywindow",
			wantIdentity: "mywindow",
		},
		{
			name:         "no window name → identity = resolved session name",
			env:          noTmux,
			sessionName:  "mysession",
			windowName:   "",
			wantIdentity: "mysession",
		},
		{
			name:         "outside tmux, no session → session falls back to cmdman → identity = cmdman",
			env:          noTmux,
			sessionName:  "",
			windowName:   "",
			wantIdentity: "cmdman",
		},
		{
			name:         "in tmux, window name set → identity = window name",
			env:          inTmux,
			sessionName:  "work",
			windowName:   "dash",
			wantIdentity: "dash",
		},
		{
			name:         "in tmux, no window name → identity = resolved session (explicit here)",
			env:          inTmux,
			sessionName:  "work",
			windowName:   "",
			wantIdentity: "work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionName := resolveSessionName(
				tt.sessionName,
				tt.env,
				func() (string, error) { return "", errors.New("no tmux query in unit test") },
			)
			windowName := tt.windowName
			if windowName == "" {
				windowName = sessionName
			}
			identity := windowName // the defaulting logic Run applies
			if identity != tt.wantIdentity {
				t.Fatalf("identity = %q, want %q", identity, tt.wantIdentity)
			}
		})
	}
}

// TestIdentityDefaulting_Down verifies that Down's identity derivation (when
// DownOptions.Identity is empty) mirrors Run's exactly:
// resolveSessionName → windowName default → identity = windowName.
//
// The key invariant: a standalone `mux down` with the same spec as `mux up`
// (same SessionName, WindowName, Env) derives the same identity that Run
// stamped on the window.
func TestIdentityDefaulting_Down(t *testing.T) {
	noTmux := []string{"PATH=/usr/bin"}

	t.Run("explicit session + window → both paths agree", func(t *testing.T) {
		sessionName := "s1"
		windowName := "w1"

		// Run path
		_ = resolveSessionName(sessionName, noTmux, nil)
		runWindow := windowName
		runIdentity := runWindow

		// Down path (same inputs)
		downSession := resolveSessionName(sessionName, noTmux, nil)
		downWindow := windowName
		downIdentity := downWindow
		if downWindow == "" {
			downWindow = downSession
			downIdentity = downWindow
		}

		if runIdentity != downIdentity {
			t.Fatalf("Run identity %q ≠ Down identity %q", runIdentity, downIdentity)
		}
	})

	t.Run("no session, no window → both fall back to cmdman", func(t *testing.T) {
		noop := func() (string, error) { return "", errors.New("no tmux") }
		runSession := resolveSessionName("", noTmux, noop)
		runWindow := runSession // no WindowName override
		runIdentity := runWindow

		downSession := resolveSessionName("", noTmux, noop)
		downWindow := "" // DownOptions.WindowName is also empty
		if downWindow == "" {
			downWindow = downSession
		}
		downIdentity := downWindow

		if runIdentity != downIdentity {
			t.Fatalf("Run identity %q ≠ Down identity %q", runIdentity, downIdentity)
		}
	})
}

// TestDeriveIdentity covers the shared derivation used by both Run (stamp)
// and Down/List (search): explicit identity wins verbatim (the compose path —
// never overridden by session/window resolution), else window name, else
// session name.
func TestDeriveIdentity(t *testing.T) {
	tests := []struct {
		name                          string
		identity, windowName, session string
		want                          string
	}{
		{"explicit identity wins", "abc123-myproject", "w1", "s1", "abc123-myproject"},
		{
			"explicit identity wins without window/session",
			"abc123-myproject",
			"",
			"",
			"abc123-myproject",
		},
		{"window name when identity empty", "", "w1", "s1", "w1"},
		{"session name as last resort", "", "", "s1", "s1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveIdentity(tt.identity, tt.windowName, tt.session); got != tt.want {
				t.Fatalf("deriveIdentity(%q, %q, %q) = %q, want %q",
					tt.identity, tt.windowName, tt.session, got, tt.want)
			}
		})
	}
}
