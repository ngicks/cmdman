package mux

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// tmuxOrSkip returns the tmux binary path or skips the test when tmux is not
// installed. The muxctl/tmux package treats a missing tmux as a hard failure
// (tmux is its reason to exist); at the mux layer a missing tmux is a skip so
// the pure unit tests still run in a tmux-less lane.
func tmuxOrSkip(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not in PATH: %v", err)
	}
	return p
}

// tmuxRun shells out to tmux on socket and returns trimmed stdout, failing the
// test on error.
func tmuxRun(t *testing.T, bin, socket string, args ...string) string {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	out, err := exec.Command(bin, full...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// TestWriteScalePosition_MergeAndDelete exercises the read-modify-write glue
// that moved up from the driver (writeScalePosition): a second command's write
// must merge into the window's @cmdman_scale option rather than clobber the
// first, and pos<=0 must delete only that command's entry. It drives a real
// tmux window (mirroring the muxctl/tmux harness) and reads the stored option
// back through the driver's raw surface, decoding it the way the mux consumers
// do. This is the end-to-end merge/delete coverage the old tmux-layer
// TestScaleState_ReadWriteMerge provided before the RMW relocated here.
func TestWriteScalePosition_MergeAndDelete(t *testing.T) {
	bin := tmuxOrSkip(t)
	socket := "cmdman-mux-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() })

	tmuxRun(t, bin, socket, "new-session", "-d", "-s", "scale-rmw", "-n", "dash")
	wid := tmuxRun(t, bin, socket, "list-windows", "-t", "scale-rmw", "-F", "#{window_id}")

	ctx := context.Background()
	// The tmux driver is used directly here (test files are exempt from the
	// mux → muxctl-only invariant); it also links the driver's init so the
	// window-state KV is reachable without a blank import elsewhere. Connect is a
	// pure binding, so no real tmux server is required to obtain the Server.
	server, err := tmux.Driver{}.Connect(ctx, muxctl.ServerConfig{Socket: socket})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// read decodes the stored raw state the way the mux consumers do.
	read := func() map[string]int {
		raw, err := server.ReadWindowState(ctx, wid, muxctl.StateKeyScale)
		if err != nil {
			t.Fatalf("ReadWindowState: %v", err)
		}
		return decodeScalePositions(raw)
	}

	// Write web=2, then worker=1: the second write must not clobber the first.
	if err := writeScalePosition(ctx, server, wid, "web", 2); err != nil {
		t.Fatalf("writeScalePosition web=2: %v", err)
	}
	if err := writeScalePosition(ctx, server, wid, "worker", 1); err != nil {
		t.Fatalf("writeScalePosition worker=1: %v", err)
	}
	got := read()
	if got["web"] != 2 {
		t.Errorf("after merge web = %d, want 2 (clobbered?)", got["web"])
	}
	if got["worker"] != 1 {
		t.Errorf("after merge worker = %d, want 1", got["worker"])
	}

	// Delete worker via pos=0: web must survive the removal.
	if err := writeScalePosition(ctx, server, wid, "worker", 0); err != nil {
		t.Fatalf("writeScalePosition worker=0: %v", err)
	}
	got = read()
	if _, ok := got["worker"]; ok {
		t.Errorf("worker still present after delete: %v", got)
	}
	if got["web"] != 2 {
		t.Errorf("web changed after worker delete: got %d, want 2", got["web"])
	}
}
