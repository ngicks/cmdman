package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// The worker marker decides which half of the split a process is, so both
// states are pinned: with it set the operation must happen right here, and
// without a worker asked for the operation must still happen and still report
// what it did — a verb that has not been given its identity yet may not quietly
// do nothing.
func TestRunMuxOp(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{name: "worker", marker: "1"},
		{name: "marker set to something else", marker: "0"},
		{name: "marker absent", marker: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv registers the restore either way; unsetting after it
			// is how the absent case is reached without leaking the change.
			t.Setenv(envMuxOp, tt.marker)
			if tt.marker == "" {
				assert.NilError(t, os.Unsetenv(envMuxOp))
			}

			type ctxKey struct{}
			ctx := context.WithValue(context.Background(), ctxKey{}, "carried")
			want := errors.New("what the operation reported")

			calls := 0
			err := RunMuxOp(ctx, MuxOpOptions{}, func(opCtx context.Context) error {
				calls++
				assert.Equal(t, opCtx.Value(ctxKey{}), "carried")
				// Whatever the operation goes on to create must not inherit the
				// marker: it describes this process's role, not theirs.
				assert.Assert(t, os.Getenv(envMuxOp) != "1")
				return want
			})

			assert.Equal(t, calls, 1)
			assert.Assert(t, errors.Is(err, want))
		})
	}
}

// The name is the lock and the log file at once, so it has to come out the same
// for the same window every time, and different for different ones — including
// windows whose names only differ in characters a file name cannot hold.
func TestMuxOpLogName(t *testing.T) {
	tests := []struct {
		name     string
		server   string
		identity string
		want     string
	}{
		{name: "plain", identity: "dev", want: "dev"},
		{name: "separator is doubled", identity: "dev-box", want: "dev--box"},
		{
			name:     "cannot be mistaken for the compose namespace",
			identity: "compose-abc",
			want:     "compose--abc",
		},
		{
			name:     "a named server is part of which window this is",
			server:   "work",
			identity: "dev",
			want:     "work-_dev",
		},
		{
			name:     "both halves are escaped",
			server:   "work-sock",
			identity: "dev-box",
			want:     "work--sock-_dev--box",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, MuxOpLogName(tt.server, tt.identity), tt.want)
		})
	}
}

// The join has to say which dashes came from which half. A dash at the edge of
// one is where doubling alone stops answering that: the run it leaves runs
// straight into the separator, and one name would then stand for two windows —
// one lock and one log file between them.
func TestMuxOpLogNameJoinsUnambiguously(t *testing.T) {
	pairs := [][2]string{
		{"", "dev"},
		{"", "-dev"},
		{"", "dev-"},
		{"work", "dev"},
		{"work-", "dev"},
		{"work", "-dev"},
		{"work--", "dev"},
		{"work", "--dev"},
		{"work-", "-dev"},
		{"work_", "dev"},
		{"work", "_dev"},
		{"work-", "_dev"},
		{"work", "-_dev"},
	}

	seen := map[string][2]string{}
	for _, pair := range pairs {
		got := MuxOpLogName(pair[0], pair[1])
		if prev, dup := seen[got]; dup {
			t.Errorf("server/identity %q and %q share the name %q", prev, pair, got)
		}
		seen[got] = pair
	}

	// The compose namespace is the other side of the same question: a server
	// named like the namespace must not name a compose project's window.
	assert.Assert(
		t,
		MuxOpLogName("compose", "0123456789ab-app") !=
			ComposeMuxOpLogName("0123456789ab-app"),
	)
}

// Two multiplexer servers can each hold a session of the same name, and they are
// two windows: an operation on one must not be taken for an operation on the
// other, which is what sharing a name would do.
func TestMuxOpLogNameSeparatesServers(t *testing.T) {
	assert.Assert(t, MuxOpLogName("", "cmdman") != MuxOpLogName("scratch", "cmdman"))
	assert.Assert(t, MuxOpLogName("a", "cmdman") != MuxOpLogName("b", "cmdman"))
}

func TestComposeMuxOpLogName(t *testing.T) {
	// The compose identity is already a workdir hash plus an escaped project
	// name, so it passes through untouched under its namespace.
	assert.Equal(
		t,
		ComposeMuxOpLogName("0123456789ab-my--app"),
		"compose-0123456789ab-my--app",
	)
}

func TestMuxOpLogNameSanitizes(t *testing.T) {
	// Window names are arbitrary strings; these are the shapes that cannot be
	// used as a file name as typed.
	identities := []string{
		"dev/box",
		"dev box",
		"dev:box",
		"../escape",
		"",
		strings.Repeat("w", 400),
		"日本語",
	}

	seen := map[string]string{}
	for _, identity := range identities {
		got := MuxOpLogName("", identity)

		assert.Assert(t, got != "", "identity %q produced no name", identity)
		assert.Assert(
			t,
			len(got) <= muxOpNameMaxLen,
			"identity %q produced %d bytes", identity, len(got),
		)
		for _, r := range got {
			ok := r >= 'a' && r <= 'z' ||
				r >= 'A' && r <= 'Z' ||
				r >= '0' && r <= '9' ||
				r == '-' || r == '_' || r == '.'
			assert.Assert(t, ok, "identity %q produced %q", identity, got)
		}
		assert.Equal(
			t, got, MuxOpLogName("", identity), "identity %q is not deterministic", identity,
		)

		if prev, dup := seen[got]; dup {
			t.Fatalf("identities %q and %q share the name %q", prev, identity, got)
		}
		seen[got] = identity
	}
}

func TestMuxOpCommandName(t *testing.T) {
	assert.Equal(t, muxOpCommandName("dev"), "muxop-dev")
}

func TestExitCodeError(t *testing.T) {
	tests := []struct {
		name string
		code int
		want int
	}{
		{name: "ordinary failure", code: 2, want: 2},
		{name: "cut short", code: -1, want: 1},
		{name: "out of range", code: 300, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ExitCodeError{Code: tt.code}
			assert.Equal(t, err.ExitStatus(), tt.want)

			var target *ExitCodeError
			assert.Assert(t, errors.As(error(err), &target))
		})
	}
}
