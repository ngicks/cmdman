package cmdman

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
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

				died, err := svc.settleUnreachableMonitor(t.Context(), st, id, cfg, rc.retErr)

				assert.Equal(t, died, tc.monitorDie)
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
		Timeout: new(time.Second),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(results), 1)
	assert.NilError(t, results[0].Err)

	state, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
	assert.Equal(t, stateJSON.Error, "monitor died unexpectedly")
}

func TestStopTimeout(t *testing.T) {
	stored := model.Duration(2 * time.Second)
	for _, tc := range []struct {
		name     string
		override *time.Duration
		stored   *model.Duration
		want     time.Duration
	}{
		{
			name:     "explicit over stored",
			override: new(5 * time.Second),
			stored:   &stored,
			want:     5 * time.Second,
		},
		{name: "explicit without stored", override: new(time.Second), want: time.Second},
		{name: "stored", stored: &stored, want: 2 * time.Second},
		{name: "default", want: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &model.CommandConfig{StopTimeout: tc.stored}
			assert.Equal(t, stopTimeout(tc.override, cfg), tc.want)
		})
	}
}

// Stop and a forced rm hand the monitor the timeout the stop resolved for the
// command, so the monitor escalates to SIGKILL at the same deadline.
func TestServiceStopSendsResolvedTimeout(t *testing.T) {
	const id = "stop-timeout"
	stored := model.Duration(2 * time.Second)
	for _, tc := range []struct {
		name    string
		remove  bool
		stored  *model.Duration
		timeout *time.Duration
		want    time.Duration
	}{
		{name: "stop explicit", stored: &stored, timeout: new(5 * time.Second), want: 5 * time.Second},
		{name: "stop stored", stored: &stored, want: 2 * time.Second},
		{name: "stop default", want: 10 * time.Second},
		{name: "rm stored", remove: true, stored: &stored, want: 2 * time.Second},
		{name: "rm default", remove: true, want: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appCfg := testConfig(t, t.TempDir())
			st := openStopTestStore(t, appCfg)
			fake := &stopMonitor{st: st, id: id}
			assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{
				StopTimeout: tc.stored,
			}))
			assert.NilError(
				t,
				st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
					SocketPath: serveFakeMonitor(t, fake),
				}),
			)

			svc := NewService(appCfg)
			defer svc.Close()
			var targetErr error
			if tc.remove {
				results, err := svc.Remove(t.Context(), RemoveRequest{
					Targets: []string{id},
					Force:   true,
				})
				assert.NilError(t, err)
				assert.Equal(t, len(results), 1)
				targetErr = results[0].Err
			} else {
				results, err := svc.Stop(t.Context(), StopRequest{
					Targets: []string{id},
					Timeout: tc.timeout,
				})
				assert.NilError(t, err)
				assert.Equal(t, len(results), 1)
				targetErr = results[0].Err
			}
			assert.NilError(t, targetErr)
			assert.DeepEqual(t, fake.receivedTimeouts(), []time.Duration{tc.want})
		})
	}
}

func TestStopWait(t *testing.T) {
	const timeout = 3 * time.Second
	assert.Equal(t, stopWait(timeout, &model.CommandConfig{}), timeout)
	assert.Equal(
		t,
		stopWait(timeout, &model.CommandConfig{
			StopCommand: &model.StopCommand{Args: []string{"true"}},
		}),
		2*timeout,
	)
}

// sigkillOnlyMonitor answers Stop like a monitor whose command outlives every
// signal but SIGKILL: it records when each stop arrived and flips the command
// to exited on SIGKILL only. forceKilled is what the exited state says about
// the SIGKILL.
type sigkillOnlyMonitor struct {
	cmdmanv1pb.UnimplementedCommandMonitorServiceServer
	st          *store.Store
	id          string
	forceKilled bool

	mu    sync.Mutex
	stops []receivedStop
}

type receivedStop struct {
	at      time.Time
	signal  int32
	timeout time.Duration
}

func (f *sigkillOnlyMonitor) Stop(
	_ context.Context,
	req *cmdmanv1pb.StopRequest,
) (*cmdmanv1pb.StopResponse, error) {
	f.mu.Lock()
	f.stops = append(f.stops, receivedStop{
		at:      time.Now(),
		signal:  req.Signal,
		timeout: req.GetTimeout().AsDuration(),
	})
	f.mu.Unlock()
	if syscall.Signal(req.Signal) != syscall.SIGKILL {
		return &cmdmanv1pb.StopResponse{}, nil
	}
	exitCode := 137
	err := f.st.UpdateCommandState(f.id, model.EventTypeExited, &exitCode, &model.CommandState{
		ForceKilled: f.forceKilled,
	})
	if err != nil {
		return nil, err
	}
	return &cmdmanv1pb.StopResponse{}, nil
}

func (f *sigkillOnlyMonitor) received() []receivedStop {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stops)
}

// The monitor runs a stored stop command for up to the timeout before the stop
// signal goes out, so the client holds its own SIGKILL back for two timeouts.
// The monitor is still handed the timeout itself.
func TestServiceStopWaitsTwiceTheTimeoutForAStopCommand(t *testing.T) {
	const (
		id      = "stop-command-wait"
		timeout = 300 * time.Millisecond
	)
	appCfg := testConfig(t, t.TempDir())
	st := openStopTestStore(t, appCfg)
	fake := &sigkillOnlyMonitor{st: st, id: id}
	assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{
		StopCommand: &model.StopCommand{Args: []string{"true"}},
	}))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
		SocketPath: serveFakeMonitor(t, fake),
	}))

	svc := NewService(appCfg)
	defer svc.Close()
	results, err := svc.Stop(t.Context(), StopRequest{
		Targets: []string{id},
		Timeout: new(timeout),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(results), 1)
	assert.NilError(t, results[0].Err)

	stops := fake.received()
	assert.Equal(t, len(stops), 2, "stops received: %v", stops)
	assert.Equal(t, syscall.Signal(stops[0].signal), syscall.SIGTERM)
	assert.Equal(t, stops[0].timeout, timeout)
	assert.Equal(t, syscall.Signal(stops[1].signal), syscall.SIGKILL)
	gap := stops[1].at.Sub(stops[0].at)
	assert.Assert(t, gap >= 2*timeout, "the client sent SIGKILL %s after the stop", gap)
}

// A stop reports the forced kill the monitor recorded for the run it ended. A
// command that had stopped already reports nothing, whatever an earlier stop
// recorded for it.
func TestServiceStopReportsForcedKill(t *testing.T) {
	const id = "stop-force-killed"
	for _, tc := range []struct {
		name string
		// state is what the store holds for the command before the stop.
		state model.EventType
		// monitor serves the command when set, recording forceKilled for the run
		// its SIGKILL ends.
		monitor     bool
		forceKilled bool
		want        bool
	}{
		{name: "force-killed", state: model.EventTypeRunning, monitor: true, forceKilled: true,
			want: true},
		{name: "killed", state: model.EventTypeRunning, monitor: true},
		{name: "already exited", state: model.EventTypeExited, forceKilled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appCfg := testConfig(t, t.TempDir())
			st := openStopTestStore(t, appCfg)
			assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{}))
			stateJSON := &model.CommandState{ForceKilled: tc.forceKilled}
			if tc.monitor {
				stateJSON = &model.CommandState{SocketPath: serveFakeMonitor(t, &sigkillOnlyMonitor{
					st:          st,
					id:          id,
					forceKilled: tc.forceKilled,
				})}
			}
			assert.NilError(t, st.InsertCommandState(id, tc.state, stateJSON))

			svc := NewService(appCfg)
			defer svc.Close()
			results, err := svc.Stop(t.Context(), StopRequest{
				Targets: []string{id},
				Timeout: new(100 * time.Millisecond),
			})
			assert.NilError(t, err)
			assert.Equal(t, len(results), 1)
			assert.NilError(t, results[0].Err)
			assert.Equal(t, results[0].ForceKilled, tc.want)
		})
	}
}

// An explicit timeout that is not positive fails stop and restart before they
// touch any target.
func TestServiceStopRejectsNonPositiveTimeout(t *testing.T) {
	const id = "stop-bad-timeout"
	for _, timeout := range []time.Duration{0, -3 * time.Second} {
		for _, op := range []struct {
			name string
			call func(svc *Service, ctx context.Context) error
		}{
			{name: "stop", call: func(svc *Service, ctx context.Context) error {
				_, err := svc.Stop(ctx, StopRequest{Targets: []string{id}, Timeout: &timeout})
				return err
			}},
			{name: "restart", call: func(svc *Service, ctx context.Context) error {
				_, err := svc.Restart(ctx, RestartRequest{Targets: []string{id}, Timeout: &timeout})
				return err
			}},
		} {
			t.Run(op.name+"/"+timeout.String(), func(t *testing.T) {
				appCfg := testConfig(t, t.TempDir())
				st := openStopTestStore(t, appCfg)
				fake := &stopMonitor{st: st, id: id}
				assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{}))
				assert.NilError(
					t,
					st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
						SocketPath: serveFakeMonitor(t, fake),
					}),
				)

				svc := NewService(appCfg)
				defer svc.Close()
				err := op.call(svc, t.Context())
				assert.ErrorContains(t, err, "stop timeout must be positive")
				assert.Equal(t, len(fake.received()), 0)
				state, _, _, err := st.GetCommandState(id)
				assert.NilError(t, err)
				assert.Equal(t, state, model.EventTypeRunning)
			})
		}
	}
}

func openStopTestStore(t *testing.T, appCfg CmdmanConfig) *store.Store {
	t.Helper()
	st, err := store.OpenStore(t.Context(), mustDBPath(t, appCfg), true)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}
