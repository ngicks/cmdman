package cmdman

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// A monitor the stop cannot reach may have ended its run cleanly since the
// stop last read the state. A terminal state or a command gone from the store
// is a stop that succeeded, and the stored state stays as the monitor left it.
// A command the store still reports as live had its monitor die on the way:
// the death is recorded on top of the stored state, and the caller's error
// comes back, nil included. A death stale cleanup already recorded is no end of
// the run either, so the caller's error comes back and the state stays.
func TestSettleUnreachableMonitor(t *testing.T) {
	const startedAt = "2026-01-02T03:04:05Z"
	for _, tc := range []struct {
		name string
		// state is what the store holds for the command. Empty leaves the
		// command out of the store.
		state model.EventType
		// stateError is the Error the stored state carries.
		stateError string
		monitorDie bool
	}{
		{name: "removed"},
		{name: "exited", state: model.EventTypeExited},
		{name: "failed", state: model.EventTypeFailed},
		{name: "running", state: model.EventTypeRunning, monitorDie: true},
		{
			name:       "died",
			state:      model.EventTypeFailed,
			stateError: "monitor died unexpectedly",
			monitorDie: true,
		},
	} {
		for _, rc := range []struct {
			name   string
			retErr error
		}{
			{name: "nil error", retErr: nil},
			{name: "kill error", retErr: errors.New("SIGKILL failed")},
		} {
			t.Run(tc.name+"/"+rc.name, func(t *testing.T) {
				appCfg := testConfig(t, t.TempDir())
				st := openStopTestStore(t, appCfg)
				svc := NewService(appCfg)
				defer svc.Close()

				id := "test-settle-unreachable-monitor"
				cfg := &model.CommandConfig{}
				if tc.state != "" {
					assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
					assert.NilError(t, st.InsertCommandState(id, tc.state, &model.CommandState{
						StartedAt: startedAt,
						Error:     tc.stateError,
					}))
				}

				err := svc.settleUnreachableMonitor(t.Context(), st, id, cfg, rc.retErr)

				if tc.monitorDie && rc.retErr != nil {
					assert.ErrorIs(t, err, rc.retErr)
				} else {
					assert.NilError(t, err)
				}
				if tc.state == "" {
					return
				}

				state, _, stateJSON, err := st.GetCommandState(id)
				assert.NilError(t, err)
				assert.Equal(t, stateJSON.StartedAt, startedAt)
				if tc.monitorDie {
					assert.Equal(t, state, model.EventTypeFailed)
					assert.Equal(t, stateJSON.Error, "monitor died unexpectedly")
				} else {
					assert.Equal(t, state, tc.state)
					assert.Equal(t, stateJSON.Error, tc.stateError)
				}
			})
		}
	}
}

// A monitor that is gone before the stop reaches it leaves nothing to stop. The
// stop succeeds and records the monitor's death.
func TestServiceStopWithMonitorGoneRecordsDeath(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	st := openStopTestStore(t, appCfg)

	id := "test-stop-monitor-gone"
	assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{}))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
		SocketPath: filepath.Join(dir, "gone.sock"),
	}))

	svc := NewService(appCfg)
	defer svc.Close()
	results, err := svc.Stop(t.Context(), StopRequest{
		Targets: []string{id},
		Timeout: time.Second,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(results), 1)
	assert.NilError(t, results[0].Err)

	state, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
	assert.Equal(t, stateJSON.Error, "monitor died unexpectedly")
}

func openStopTestStore(t *testing.T, appCfg CmdmanConfig) *store.Store {
	t.Helper()
	st, err := store.OpenStore(t.Context(), mustDBPath(t, appCfg), true)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}
