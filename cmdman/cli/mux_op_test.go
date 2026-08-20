package cli

import (
	"context"
	"errors"
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

// The worker marker decides which half of the split a process is, so both
// states are pinned: with it set the operation must happen right here, and
// without it the operation must still happen and still report what it did —
// the follower side is not in place yet, and until it is no invocation may
// quietly do nothing.
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
			err := RunMuxOp(ctx, func(opCtx context.Context) error {
				calls++
				assert.Equal(t, opCtx.Value(ctxKey{}), "carried")
				return want
			})

			assert.Equal(t, calls, 1)
			assert.Assert(t, errors.Is(err, want))
		})
	}
}
