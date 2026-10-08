package cmdman_test

import (
	"slices"
	"testing"
)

func TestStopTimeoutFlag_Stored(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	for _, tc := range []struct {
		name string
		args []string
		want any
	}{
		{name: "create-duration", args: []string{"create", "--stop-timeout", "30s"}, want: "30s"},
		{name: "create-seconds", args: []string{"create", "--stop-timeout", "45"}, want: "45s"},
		{name: "run-duration", args: []string{"run", "--stop-timeout", "1m30s"}, want: "1m30s"},
		{name: "create-unset", args: []string{"create"}, want: nil},
	} {
		args := slices.Concat(tc.args, []string{"-n", tc.name, "--", "/bin/sh", "-c", "true"})
		env.Cmd(args...).Run(ctx, t)
		t.Cleanup(func() { env.cleanupCommand(ctx, tc.name) })

		cfg, _ := env.inspectJSON(ctx, tc.name)["Config"].(map[string]any)
		if got := cfg["stop_timeout"]; got != tc.want {
			t.Errorf("%s: stop_timeout = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestStopTimeoutFlag_ZeroRejected(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Cmd("create", "-n", "zero-timeout", "--stop-timeout", "0", "--", "/bin/sh", "-c", "true").
		ExpectFail(ctx, t, "--stop-timeout", "timeout must be positive")

	if entries := env.lsJSON(ctx); len(entries) != 0 {
		t.Fatalf("a rejected create must store nothing, got %v", entries)
	}
}
