package cmdman_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

// TestEvents_ReplayHistorical runs a short-lived command and verifies that
// `cmdman events --no-follow` replays the created/starting/running/exited
// events from the on-disk event log.
func TestEvents_ReplayHistorical(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 0")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout := env.run(ctx, "events", "--no-follow", "--id", id)
	if stdout == "" {
		t.Fatalf("events output empty for id %q", id)
	}
	gotTypes := collectEventTypes(t, stdout)
	wantPresent := []string{"created", "starting", "running", "exited"}
	for _, w := range wantPresent {
		if _, ok := gotTypes[w]; !ok {
			t.Errorf("expected event type %q in output, got types %v\nraw:\n%s",
				w, sortedKeys(gotTypes), stdout)
		}
	}
}

// TestEvents_FollowLivePublishes verifies that the default `cmdman events`
// subscription (tail new events, skip historical) receives new events as
// they happen.
func TestEvents_FollowLivePublishes(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(streamCtx, cmdmanBin, "events")
	cmd.Env = append(cmd.Env,
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		"PATH="+strings.Join([]string{"/usr/bin", "/bin"}, ":"),
	)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start events: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Give the watcher a moment to install before publishing.
	time.Sleep(200 * time.Millisecond)

	id := env.run(ctx, "run", "-n", "ev-target", "--", "/bin/sh", "-c", "exit 0")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	deadline := time.After(15 * time.Second)
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 1024)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events; got: %s", string(buf))
		default:
		}
		_ = stdoutPipe.(interface{ SetReadDeadline(time.Time) error }).
			SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := stdoutPipe.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		gotTypes := collectEventTypes(t, string(buf))
		// Wait for the terminal "exited" event. The reader observes
		// ENOENT at startup and suppresses the FromEnd seek for the
		// freshly-created log file, so by the time we see "exited" the
		// earlier events should also have been delivered — assert
		// that to guard against regressions in the fresh-log path.
		if _, ok := gotTypes["exited"]; ok {
			for _, w := range []string{"created", "starting", "running", "exited"} {
				if _, ok := gotTypes[w]; !ok {
					t.Fatalf("expected event type %q in live output; got %v\nraw:\n%s",
						w, sortedKeys(gotTypes), string(buf))
				}
			}
			return
		}
		if err != nil {
			continue
		}
	}
}

// TestEvents_RejectsInvertedWindow verifies that --since after --until is
// rejected at the boundary instead of silently producing no output or
// hanging on a future event in tail mode.
func TestEvents_RejectsInvertedWindow(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "events", "--no-follow",
		"--since", "2026-05-22T00:00:00Z",
		"--until", "2026-05-21T00:00:00Z",
	)
	if !strings.Contains(stderr, "since must not be after until") {
		t.Fatalf("expected since/until ordering error; got stderr:\n%s", stderr)
	}
}

func collectEventTypes(t *testing.T, raw string) map[string]struct{} {
	t.Helper()
	out := map[string]struct{}{}
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode event line %q: %v", line, err)
		}
		out[ev.Type] = struct{}{}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Tiny, no need to import sort.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
