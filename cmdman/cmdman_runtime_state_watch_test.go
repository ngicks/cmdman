package cmdman

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// recvRuntimeState takes the next pushed record, failing the test on a timeout,
// an early close, or a terminal error.
func recvRuntimeState(t *testing.T, ch <-chan RuntimeStateRecord) RuntimeState {
	t.Helper()
	select {
	case rec, ok := <-ch:
		assert.Assert(t, ok, "runtime-state channel closed before the expected record")
		assert.NilError(t, rec.Err)
		return rec.State
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a runtime-state record")
		return RuntimeState{}
	}
}

// drainClosed reads until the channel closes, failing on a terminal error or a
// stream that never ends.
func drainClosed(t *testing.T, ch <-chan RuntimeStateRecord) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case rec, ok := <-ch:
			if !ok {
				return
			}
			assert.NilError(t, rec.Err)
		case <-deadline:
			t.Fatal("runtime-state channel never closed")
		}
	}
}

func TestServiceWatchRuntimeState(t *testing.T) {
	// Every stage waits for a marker file, so the test - not a sleep - decides
	// when the command changes its state, and the subscription is open before
	// the first change happens. Marker files rather than the WriteStdin or Stop
	// RPCs, and no Status polling: Monitor.QueueStdin, SignalProcess and
	// GetState read m.stdin / m.cmd with no happens-before edge against
	// runOnce's teardown of those same fields, so driving a run to its end that
	// way trips -race on a monitor bug this plan does not own.
	stage := t.TempDir()
	appCfg, id, _ := startTestMonitor(t, true, "/bin/sh", "-c", fmt.Sprintf(`
until [ -e "%[1]s/title" ]; do sleep 0.05; done
printf '\033]0;first\007'
until [ -e "%[1]s/retitle" ]; do sleep 0.05; done
printf '\033]0;second\007'
until [ -e "%[1]s/bell" ]; do sleep 0.05; done
printf '\a'
until [ -e "%[1]s/exit" ]; do sleep 0.05; done
`, stage))
	svc := NewService(appCfg)
	defer svc.Close()

	advance := func(marker string) {
		t.Helper()
		assert.NilError(t, os.WriteFile(filepath.Join(stage, marker), nil, 0o600))
	}

	sub, err := svc.WatchRuntimeState(t.Context(), id)
	assert.NilError(t, err)
	defer sub.Close()

	// The command has written nothing yet, so the opening snapshot is the empty
	// state - and it arrives without any change having happened at all.
	assert.DeepEqual(t, recvRuntimeState(t, sub.Records()), RuntimeState{})

	advance("title")
	assert.Equal(t, recvRuntimeState(t, sub.Records()).Title, "first")

	advance("retitle")
	retitled := recvRuntimeState(t, sub.Records())
	assert.Equal(t, retitled.Title, "second")
	assert.Equal(t, retitled.BellUnread, false)

	// Only once the debounced title has landed: a bell arriving inside that
	// window would be flushed carrying the new title, coalescing the two.
	advance("bell")
	belled := recvRuntimeState(t, sub.Records())
	assert.Equal(t, belled.BellUnread, true)
	assert.Equal(t, belled.Title, "second")

	// A subscription opened now opens with what the monitor already holds,
	// rather than waiting for the next change.
	late, err := svc.WatchRuntimeState(t.Context(), id)
	assert.NilError(t, err)
	assert.DeepEqual(t, recvRuntimeState(t, late.Records()), RuntimeState{
		Title:      "second",
		BellUnread: true,
	})
	assert.NilError(t, late.Close())
	drainClosed(t, late.Records())

	// The command stopping ends the stream. Whatever the run pushes on its way
	// out (the reset that clears the run's state) is fine; what matters is that
	// the channel closes and carries no error.
	advance("exit")
	drainClosed(t, sub.Records())
}

func TestServiceWatchRuntimeStateClose(t *testing.T) {
	appCfg, id, _ := startTestMonitor(t, false, "/bin/sh", "-c", "sleep 30")
	svc := NewService(appCfg)
	defer svc.Close()

	sub, err := svc.WatchRuntimeState(t.Context(), id)
	assert.NilError(t, err)

	// Taking the snapshot leaves the pump parked in Recv; Close must unblock it
	// and close the channel without reporting the cancellation as an error.
	assert.DeepEqual(t, recvRuntimeState(t, sub.Records()), RuntimeState{})
	assert.NilError(t, sub.Close())
	drainClosed(t, sub.Records())
}
