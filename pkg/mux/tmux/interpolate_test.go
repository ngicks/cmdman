package tmux

import (
	"slices"
	"testing"
)

func TestInterpolateKeyBasic(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		sessionID string
		windowID  string
		paneID    string
		want      string
	}{
		{
			name:      "session ID",
			key:       "echo #{SESSION_ID}",
			sessionID: "$0",
			want:      "echo $0",
		},
		{
			name:     "window ID",
			key:      "echo #{WINDOW_ID}",
			windowID: "@1",
			want:     "echo @1",
		},
		{
			name:   "pane ID",
			key:    "echo #{PANE_ID}",
			paneID: "%3",
			want:   "echo %3",
		},
		{
			name:      "inject meta",
			key:       "#{INJECT_META}",
			sessionID: "$0",
			windowID:  "@1",
			paneID:    "%3",
			want:      "export CRAB_SESSION_ID='$0' CRAB_WINDOW_ID='@1' CRAB_PANE_ID='%3'",
		},
		{
			name:      "multiple placeholders",
			key:       "sid=#{SESSION_ID} wid=#{WINDOW_ID} pid=#{PANE_ID}",
			sessionID: "$0",
			windowID:  "@1",
			paneID:    "%3",
			want:      "sid=$0 wid=@1 pid=%3",
		},
		{
			name:   "no placeholders unchanged",
			key:    "echo hello",
			paneID: "%0",
			want:   "echo hello",
		},
		{
			name:   "escape produces literal",
			key:    "##{PANE_ID}",
			paneID: "%3",
			want:   "#{PANE_ID}",
		},
		{
			name:   "escape mixed with real placeholder",
			key:    "##{PANE_ID} #{PANE_ID}",
			paneID: "%3",
			want:   "#{PANE_ID} %3",
		},
		{
			name: "empty key",
			key:  "",
			want: "",
		},
		{
			name:   "Enter key unchanged",
			key:    "Enter",
			paneID: "%0",
			want:   "Enter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolateKey(tt.key, tt.sessionID, tt.windowID, tt.paneID)
			if got != tt.want {
				t.Errorf("interpolateKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestInterpolateKeys(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		got := slices.Collect(interpolateKeys(nil, "$0", "@1", "%3"))
		if len(got) != 0 {
			t.Errorf("interpolateKeys(nil) = %v, want empty", got)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := slices.Collect(interpolateKeys([]string{}, "$0", "@1", "%3"))
		if len(got) != 0 {
			t.Errorf("interpolateKeys([]) len = %d, want 0", len(got))
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		input := []string{"echo #{PANE_ID}", "Enter"}
		orig := make([]string, len(input))
		copy(orig, input)

		got := slices.Collect(interpolateKeys(input, "$0", "@1", "%3"))

		// Verify input unchanged.
		for i := range input {
			if input[i] != orig[i] {
				t.Errorf("input[%d] mutated: got %q, was %q", i, input[i], orig[i])
			}
		}

		// Verify output is correct.
		if got[0] != "echo %3" {
			t.Errorf("got[0] = %q, want %q", got[0], "echo %3")
		}
		if got[1] != "Enter" {
			t.Errorf("got[1] = %q, want %q", got[1], "Enter")
		}
	})

	t.Run("multiple keys", func(t *testing.T) {
		input := []string{"#{INJECT_META}", "Enter", "echo #{PANE_ID}", "Enter"}
		got := slices.Collect(interpolateKeys(input, "$0", "@1", "%3"))
		if got[0] != "export CRAB_SESSION_ID='$0' CRAB_WINDOW_ID='@1' CRAB_PANE_ID='%3'" {
			t.Errorf("got[0] = %q", got[0])
		}
		if got[2] != "echo %3" {
			t.Errorf("got[2] = %q, want %q", got[2], "echo %3")
		}
	})
}
