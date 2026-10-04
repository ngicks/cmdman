package monitor

import (
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

func insertWaitTestCommand(
	t *testing.T,
	st *store.Store,
	id string,
	state model.EventType,
	monitorPID int,
) {
	t.Helper()
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: store.DefaultScrollbackBytes,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      "/tmp/cmd/" + id,
	}
	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, st.InsertCommandState(id, state, &model.CommandState{
		MonitorPID: monitorPID,
	}))
}

func TestWaitForStateReturnsWhenRunEndsBeforeRunningIsSeen(t *testing.T) {
	st := testStore(t)
	const id = "quick-exit"
	insertWaitTestCommand(t, st, id, model.EventTypeCreated, 0)
	run := &model.CommandState{MonitorPID: 42}

	var g errgroup.Group
	g.Go(func() error {
		time.Sleep(20 * time.Millisecond)
		if err := st.UpdateCommandState(id, model.EventTypeStarting, nil, run); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
		code := 0
		return st.UpdateCommandState(id, model.EventTypeExited, &code, run)
	})

	start := time.Now()
	state, err := WaitForState(
		st, id, model.EventTypeRunning, StateMark{State: model.EventTypeCreated}, 100)
	elapsed := time.Since(start)
	assert.NilError(t, g.Wait())

	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	assert.Assert(t, elapsed < 2*time.Second, "WaitForState took %s", elapsed)
}

func TestWaitForStateReturnsWhenRunEndedBeforeFirstPoll(t *testing.T) {
	cases := map[string]struct {
		since StateMark
		state model.EventType
	}{
		"fresh command": {StateMark{State: model.EventTypeCreated}, model.EventTypeExited},
		"exited again": {
			StateMark{State: model.EventTypeExited, MonitorPID: 41},
			model.EventTypeExited,
		},
		"failed run": {StateMark{State: model.EventTypeCreated}, model.EventTypeFailed},
		"failed restart": {
			StateMark{State: model.EventTypeFailed, MonitorPID: 41},
			model.EventTypeFailed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := testStore(t)
			const id = "ended"
			insertWaitTestCommand(t, st, id, tc.state, 42)

			state, err := WaitForState(st, id, model.EventTypeRunning, tc.since, 100)

			assert.Equal(t, state, tc.state)
			if tc.state == model.EventTypeFailed {
				assert.ErrorContains(t, err, "monitor entered failed state")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestWaitForStateIgnoresLeftoverTerminalState(t *testing.T) {
	for _, state := range []model.EventType{model.EventTypeExited, model.EventTypeFailed} {
		t.Run(string(state), func(t *testing.T) {
			st := testStore(t)
			const id = "leftover"
			insertWaitTestCommand(t, st, id, state, 41)

			got, err := WaitForState(
				st, id, model.EventTypeRunning, StateMark{State: state, MonitorPID: 41}, 4)

			assert.ErrorContains(t, err, "timeout waiting for state")
			assert.Equal(t, got, state)
		})
	}
}
