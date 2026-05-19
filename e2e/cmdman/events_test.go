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
// `cmdman events` (no --follow) replays the create/start/running/exit
// events from the on-disk event log.
func TestEvents_ReplayHistorical(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 0")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout := env.run(ctx, "events", "--id", id)
	if stdout == "" {
		t.Fatalf("events output empty for id %q", id)
	}
	gotTypes := collectEventTypes(t, stdout)
	wantPresent := []string{"create", "start", "running", "exit"}
	for _, w := range wantPresent {
		if _, ok := gotTypes[w]; !ok {
			t.Errorf("expected event type %q in output, got types %v\nraw:\n%s",
				w, sortedKeys(gotTypes), stdout)
		}
	}
}

// TestEvents_FollowLivePublishes a streamed --follow subscription receives
// new events as they happen.
func TestEvents_FollowLivePublishes(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(streamCtx, cmdmanBin,
		"events", "-f", "--from-end")
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
		// "create" is racy under --from-end because it can be written
		// between file creation and our seek-to-end. The terminal "exit"
		// is published after the subscription is established, so it's
		// the deterministic signal that live delivery works.
		if _, ok := gotTypes["exit"]; ok {
			return
		}
		if err != nil {
			continue
		}
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
