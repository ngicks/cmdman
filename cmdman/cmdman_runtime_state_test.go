package cmdman

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gotest.tools/v3/assert"
)

// fakeMonitor answers Status like a monitor would, counting how many calls it
// serves at once so a test can watch the dial fan-out.
type fakeMonitor struct {
	cmdmanv1pb.UnimplementedCommandMonitorServiceServer
	state *cmdmanv1pb.RuntimeState
	delay time.Duration

	mu       sync.Mutex
	inFlight int
	peak     int
}

func (f *fakeMonitor) Status(
	ctx context.Context,
	_ *cmdmanv1pb.StatusRequest,
) (*cmdmanv1pb.StatusResponse, error) {
	f.enter()
	defer f.leave()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &cmdmanv1pb.StatusResponse{State: "running", RuntimeState: f.state}, nil
}

func (f *fakeMonitor) enter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
}

func (f *fakeMonitor) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
}

func (f *fakeMonitor) peakInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

// serveFakeMonitor listens on a Unix socket under the test's temp dir and
// returns its path.
func serveFakeMonitor(t *testing.T, srv *fakeMonitor) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "monitor.sock")
	lis, err := net.Listen("unix", sockPath)
	assert.NilError(t, err)

	grpcServer := grpc.NewServer()
	cmdmanv1pb.RegisterCommandMonitorServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)
	return sockPath
}

func socketEntries(sockPath string, n int) []store.CommandEntry {
	entries := make([]store.CommandEntry, n)
	for i := range entries {
		entries[i] = store.CommandEntry{
			ID:        fmt.Sprintf("cmd-%d", i),
			StateJSON: &model.CommandState{SocketPath: sockPath},
		}
	}
	return entries
}

func TestServiceRuntimeStates(t *testing.T) {
	t.Run("collects state from every reachable monitor", func(t *testing.T) {
		sockPath := serveFakeMonitor(t, &fakeMonitor{
			state: &cmdmanv1pb.RuntimeState{
				Title:      "vim",
				Status:     cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING,
				Detail:     "review me",
				BellUnread: true,
			},
		})
		svc := NewService(CmdmanConfig{})
		defer svc.Close()

		got := svc.RuntimeStates(t.Context(), socketEntries(sockPath, 3), RuntimeStatesOption{})

		assert.Equal(t, len(got), 3)
		assert.DeepEqual(t, got["cmd-0"], RuntimeState{
			Title:      "vim",
			Status:     ReportedStatusWaiting,
			Detail:     "review me",
			BellUnread: true,
		})
	})

	t.Run("respects the parallelism bound", func(t *testing.T) {
		for _, bound := range []int{1, 2} {
			t.Run(fmt.Sprintf("bound %d", bound), func(t *testing.T) {
				srv := &fakeMonitor{
					state: &cmdmanv1pb.RuntimeState{Title: "busy"},
					delay: 20 * time.Millisecond,
				}
				sockPath := serveFakeMonitor(t, srv)
				svc := NewService(CmdmanConfig{})
				defer svc.Close()

				got := svc.RuntimeStates(
					t.Context(),
					socketEntries(sockPath, 8),
					RuntimeStatesOption{Parallelism: bound, Timeout: 5 * time.Second},
				)

				assert.Equal(t, len(got), 8)
				assert.Assert(t, srv.peakInFlight() <= bound,
					"peak in-flight %d exceeds bound %d", srv.peakInFlight(), bound)
			})
		}
	})

	t.Run("a slow or missing monitor costs an entry, not the listing", func(t *testing.T) {
		slow := serveFakeMonitor(t, &fakeMonitor{
			state: &cmdmanv1pb.RuntimeState{Title: "wedged"},
			delay: time.Minute,
		})
		quick := serveFakeMonitor(t, &fakeMonitor{state: &cmdmanv1pb.RuntimeState{Title: "alive"}})

		entries := []store.CommandEntry{
			{ID: "slow", StateJSON: &model.CommandState{SocketPath: slow}},
			{ID: "dead", StateJSON: &model.CommandState{
				SocketPath: filepath.Join(t.TempDir(), "gone.sock"),
			}},
			{ID: "never-started", StateJSON: &model.CommandState{}},
			{ID: "no-state"},
			{ID: "quick", StateJSON: &model.CommandState{SocketPath: quick}},
		}

		svc := NewService(CmdmanConfig{})
		defer svc.Close()

		got := svc.RuntimeStates(t.Context(), entries, RuntimeStatesOption{
			Timeout: 100 * time.Millisecond,
		})

		assert.DeepEqual(t, got, map[string]RuntimeState{"quick": {Title: "alive"}})
	})
}

// startTestMonitor supervises argv in-process and returns the command id and
// the state JSON holding its socket path, once the command is running.
func startTestMonitor(
	t *testing.T,
	tty bool,
	argv ...string,
) (CmdmanConfig, string, *model.CommandState) {
	t.Helper()
	dir := t.TempDir()
	appCfg := testConfig(t, dir)

	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st.Close() })

	id := "runtime-state"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            argv,
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		Tty:             tty,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}
	assert.NilError(t, st.InsertCommandConfig(id, "runtime-state", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- monitor.RunMonitor(ctx, id, appCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		assert.NilError(t, <-runErr)
	})

	var stateJSON *model.CommandState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _, sj, err := st.GetCommandState(id)
		assert.NilError(t, err)
		if state == model.EventTypeRunning && sj.SocketPath != "" {
			stateJSON = sj
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Assert(t, stateJSON != nil, "monitor never reported a running command")
	return appCfg, id, stateJSON
}

func monitorClient(t *testing.T, svc *Service, id string) cmdmanv1pb.CommandMonitorServiceClient {
	t.Helper()
	conn, err := svc.connectMonitorByName(t.Context(), id)
	assert.NilError(t, err)
	t.Cleanup(func() { conn.Close() })
	return cmdmanv1pb.NewCommandMonitorServiceClient(conn)
}

func TestServiceRuntimeStateOverMonitorSocket(t *testing.T) {
	appCfg, id, stateJSON := startTestMonitor(
		t, true,
		"/bin/sh", "-c", `printf '\033]0;my title\007'; sleep 30`,
	)
	svc := NewService(appCfg)
	defer svc.Close()
	entries := []store.CommandEntry{{ID: id, StateJSON: stateJSON}}

	// The title arrives once the command has written it and the emulator has
	// consumed it, so give the dial a few tries.
	var got RuntimeState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got = svc.RuntimeStates(t.Context(), entries, RuntimeStatesOption{
			Timeout: time.Second,
		})[id]
		if got.Title != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Equal(t, got.Title, "my title")

	client := monitorClient(t, svc, id)

	watch, err := client.WatchRuntimeState(t.Context(), &cmdmanv1pb.WatchRuntimeStateRequest{})
	assert.NilError(t, err)
	snap, err := watch.Recv()
	assert.NilError(t, err)
	assert.Equal(t, snap.State.Title, "my title")

	_, err = client.SetReportedStatus(t.Context(), &cmdmanv1pb.SetReportedStatusRequest{
		Status: cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING,
		Detail: "needs input",
	})
	assert.NilError(t, err)

	pushed, err := watch.Recv()
	assert.NilError(t, err)
	assert.Equal(t, pushed.State.Status, cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING)
	assert.Equal(t, pushed.State.Detail, "needs input")
	assert.Equal(t, pushed.State.Title, "my title")

	reported, err := client.GetReportedStatus(t.Context(), &cmdmanv1pb.GetReportedStatusRequest{})
	assert.NilError(t, err)
	assert.Equal(t, reported.Status, cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING)
	assert.Equal(t, reported.Detail, "needs input")

	live := svc.RuntimeStates(t.Context(), entries, RuntimeStatesOption{Timeout: time.Second})[id]
	assert.Equal(t, live.Status, ReportedStatusWaiting)
	assert.Equal(t, live.Detail, "needs input")

	_, err = client.DeleteReportedStatus(t.Context(), &cmdmanv1pb.DeleteReportedStatusRequest{})
	assert.NilError(t, err)
	reported, err = client.GetReportedStatus(t.Context(), &cmdmanv1pb.GetReportedStatusRequest{})
	assert.NilError(t, err)
	assert.Equal(t, reported.Status, cmdmanv1pb.ReportedStatus_REPORTED_STATUS_UNSPECIFIED)
}

func TestServiceReportedStatusWithoutTty(t *testing.T) {
	// Bell and title capture are TTY-only (D35); reporting a status is not.
	appCfg, id, _ := startTestMonitor(t, false, "/bin/sh", "-c", "sleep 30")
	svc := NewService(appCfg)
	defer svc.Close()
	client := monitorClient(t, svc, id)

	_, err := client.SetReportedStatus(t.Context(), &cmdmanv1pb.SetReportedStatusRequest{
		Status: cmdmanv1pb.ReportedStatus_REPORTED_STATUS_DONE,
	})
	assert.NilError(t, err)

	resp, err := client.Status(t.Context(), &cmdmanv1pb.StatusRequest{})
	assert.NilError(t, err)
	assert.Equal(t, resp.RuntimeState.Status, cmdmanv1pb.ReportedStatus_REPORTED_STATUS_DONE)

	_, err = client.SetReportedStatus(t.Context(), &cmdmanv1pb.SetReportedStatusRequest{
		Status: cmdmanv1pb.ReportedStatus_REPORTED_STATUS_UNSPECIFIED,
	})
	assert.Equal(t, status.Code(err), codes.InvalidArgument)
}
