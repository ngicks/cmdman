package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gotest.tools/v3/assert"
)

// stopMonitor answers Stop like a monitor would: it records the signal and the
// timeout and flips the command to exited, or fails the stop when stopErr is
// set.
type stopMonitor struct {
	cmdmanv1pb.UnimplementedCommandMonitorServiceServer
	st      *store.Store
	id      string
	stopErr error

	mu       sync.Mutex
	signals  []int32
	timeouts []time.Duration
}

func (f *stopMonitor) Stop(
	_ context.Context,
	req *cmdmanv1pb.StopRequest,
) (*cmdmanv1pb.StopResponse, error) {
	f.mu.Lock()
	f.signals = append(f.signals, req.Signal)
	f.timeouts = append(f.timeouts, req.GetTimeout().AsDuration())
	f.mu.Unlock()
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	exitCode := 143
	err := f.st.UpdateCommandState(f.id, model.EventTypeExited, &exitCode, &model.CommandState{})
	if err != nil {
		return nil, err
	}
	return &cmdmanv1pb.StopResponse{}, nil
}

func (f *stopMonitor) received() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int32(nil), f.signals...)
}

func (f *stopMonitor) receivedTimeouts() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.timeouts...)
}

// startMonitorStandIn starts a process that stands in for the monitor rm may
// kill. It returns the pid and a channel that delivers the process's exit
// status.
func startMonitorStandIn(t *testing.T) (int, <-chan *os.ProcessState) {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	assert.NilError(t, cmd.Start())
	exited := make(chan *os.ProcessState, 1)
	go func() {
		_ = cmd.Wait()
		exited <- cmd.ProcessState
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd.Process.Pid, exited
}

// A create that died between its config row and its state row leaves a record
// with no state. It still holds its name, so it has to be removable.
func TestServiceRemoveRecordWithoutState(t *testing.T) {
	const id = "config-only"

	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st.Close() })

	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"true"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}
	assert.NilError(t, st.InsertCommandConfig(id, id, cfg))
	assert.NilError(t, store.WriteCommandConfig(commandDir, cfg))

	svc := NewService(appCfg)
	defer svc.Close()

	// No monitor ever ran for the record, so no force is needed to remove it.
	results, err := svc.Remove(t.Context(), RemoveRequest{Targets: []string{id}})
	assert.NilError(t, err)
	assert.Equal(t, len(results), 1)
	assert.NilError(t, results[0].Err)

	_, err = st.ResolveIDByName(id)
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "name still held: %v", err)
	_, err = os.Stat(commandDir)
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "command dir still present: %v", err)
}

func TestServiceRemoveForce(t *testing.T) {
	const id = "force-rm"

	cases := []struct {
		name string
		// stopErr makes the monitor fail the stop.
		stopErr error
		// unreachable points the state at a socket nothing listens on.
		unreachable bool
		wantKilled  bool
		wantSignals []int32
	}{
		{
			name:        "stops the command through its monitor and leaves the monitor alone",
			wantSignals: []int32{int32(syscall.SIGTERM)},
		},
		{
			name:        "kills a monitor that fails the stop",
			stopErr:     status.Error(codes.Internal, "stop failed"),
			wantKilled:  true,
			wantSignals: []int32{int32(syscall.SIGTERM)},
		},
		{
			name:        "kills a monitor it cannot reach",
			unreachable: true,
			wantKilled:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			appCfg := testConfig(t, dir)

			dbPath, err := appCfg.DBPath()
			assert.NilError(t, err)
			st, err := store.OpenStore(t.Context(), dbPath, true)
			assert.NilError(t, err)
			t.Cleanup(func() { st.Close() })

			fake := &stopMonitor{st: st, id: id, stopErr: tc.stopErr}
			sockPath := filepath.Join(t.TempDir(), "gone.sock")
			if !tc.unreachable {
				sockPath = serveFakeMonitor(t, fake)
			}
			monitorPID, monitorExited := startMonitorStandIn(t)

			commandDir, err := appCfg.CommandDir(id)
			assert.NilError(t, err)
			cfg := &model.CommandConfig{
				Argv:            []string{"sleep", "300"},
				Dir:             dir,
				Env:             testEnv(),
				RestartPolicy:   model.RestartPolicyNo,
				ScrollbackBytes: 4096,
				LogDriver:       model.DefaultLogDriver,
				CommandDir:      commandDir,
			}
			assert.NilError(t, st.InsertCommandConfig(id, id, cfg))
			assert.NilError(t, store.WriteCommandConfig(commandDir, cfg))
			assert.NilError(
				t,
				st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
					MonitorPID: monitorPID,
					SocketPath: sockPath,
				}),
			)

			svc := NewService(appCfg)
			defer svc.Close()

			ctx := contextkey.WithSlogLogger(t.Context(), slog.New(slog.DiscardHandler))
			results, err := svc.Remove(ctx, RemoveRequest{Targets: []string{id}, Force: true})
			assert.NilError(t, err)
			assert.Equal(t, len(results), 1)
			assert.NilError(t, results[0].Err)

			assert.DeepEqual(t, fake.received(), tc.wantSignals)

			_, _, _, err = st.GetCommandState(id)
			assert.Assert(t, errors.Is(err, sql.ErrNoRows), "record still present: %v", err)
			_, err = os.Stat(commandDir)
			assert.Assert(t, errors.Is(err, fs.ErrNotExist), "command dir still present: %v", err)

			if tc.wantKilled {
				select {
				case ps := <-monitorExited:
					ws, _ := ps.Sys().(syscall.WaitStatus)
					assert.Assert(t, ws.Signaled() && ws.Signal() == syscall.SIGKILL,
						"monitor exited with %v, want SIGKILL", ps)
				case <-time.After(5 * time.Second):
					t.Fatal("rm left the monitor running")
				}
				return
			}
			select {
			case ps := <-monitorExited:
				t.Fatalf("rm killed a monitor that stopped the command: %v", ps)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}
