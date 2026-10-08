package compose

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// stopGauge answers cmdman stops and records the most stops in flight at once.
// A stop stays in flight until full stops are in flight together or hold
// passes. With full one above a bound, a bound that holds has every stop wait
// out hold and never reaches full, while a broken one reaches it.
type stopGauge struct {
	full int
	hold time.Duration

	mu       sync.Mutex
	inFlight int
	most     int
	calls    int
	isFull   bool
	filled   chan struct{}
}

func newStopGauge(full int, hold time.Duration) *stopGauge {
	return &stopGauge{full: full, hold: hold, filled: make(chan struct{})}
}

func (g *stopGauge) stop(
	_ context.Context,
	req cmdman.StopRequest,
) ([]cmdman.StopResult, error) {
	g.mu.Lock()
	g.calls++
	g.inFlight++
	g.most = max(g.most, g.inFlight)
	if g.inFlight >= g.full && !g.isFull {
		g.isFull = true
		close(g.filled)
	}
	g.mu.Unlock()

	select {
	case <-g.filled:
	case <-time.After(g.hold):
	}

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return []cmdman.StopResult{{ID: req.Targets[0]}}, nil
}

func (g *stopGauge) result() (most, calls int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.most, g.calls
}

// runningReplicas returns every stored replica of cmd in spec, all running.
func runningReplicas(spec ComposeSpec, cmd Command) []store.CommandEntry {
	out := make([]store.CommandEntry, 0, cmd.Scale)
	for idx := 1; idx <= cmd.Scale; idx++ {
		name := InstanceName(cmd.GeneratedName, idx)
		out = append(out, store.CommandEntry{
			ID:    "id-" + name,
			Name:  name,
			State: model.EventTypeRunning,
			ConfigJSON: &model.CommandConfig{
				Labels: BuildLabels(spec, cmd, "sha256:test", idx),
			},
		})
	}
	return out
}

// parallelProject returns a project of two independent commands of three
// replicas each, and its stored replicas, all running.
func parallelProject() (ComposeSpec, []store.CommandEntry) {
	api := reconcileCmd("api")
	api.Scale = 3
	worker := reconcileCmd("worker")
	worker.Scale = 3
	spec := reconcileSpec(api, worker)
	return spec, slices.Concat(runningReplicas(spec, api), runningReplicas(spec, worker))
}

// parallelService returns a Service bounded to limit whose cmdman lists entries
// as the project and stops through stop.
func parallelService(
	limit int,
	entries []store.CommandEntry,
	stop func(context.Context, cmdman.StopRequest) ([]cmdman.StopResult, error),
) *Service {
	s := NewService(nil, WithParallelLimit(limit))
	s.svc = testCmdmanSvc{
		list: func(_ context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error) {
			// Resource holders and hook commands are listed by this label; the
			// project has none.
			if req.Labels[LabelIntermediate] != "" {
				return nil, nil
			}
			return entries, nil
		},
		stop: stop,
	}
	return s
}

func TestParallelLimitBoundsReplicaStops(t *testing.T) {
	spec, entries := parallelProject()
	// orphaned declares none of the stored commands, so a whole-project down
	// stops all of them as orphans.
	orphaned := ComposeSpec{Project: spec.Project, WorkDir: spec.WorkDir}

	ops := []struct {
		name string
		run  func(ctx context.Context, s *Service) error
	}{
		{
			name: "stop",
			run: func(ctx context.Context, s *Service) error {
				_, err := s.Stop(ctx, ProjectSelection{
					WorkDir: spec.WorkDir, Project: spec.Project, Spec: &spec,
				}, StopOption{})
				return err
			},
		},
		{
			name: "down",
			run: func(ctx context.Context, s *Service) error {
				_, err := s.Down(ctx, ProjectSelection{
					WorkDir: spec.WorkDir, Project: spec.Project, Spec: &spec,
				}, DownOption{})
				return err
			},
		},
		{
			name: "down orphans",
			run: func(ctx context.Context, s *Service) error {
				_, err := s.Down(ctx, ProjectSelection{
					WorkDir: spec.WorkDir, Project: spec.Project, Spec: &orphaned,
				}, DownOption{})
				return err
			},
		},
		{
			name: "restart",
			run: func(ctx context.Context, s *Service) error {
				_, err := s.Restart(ctx, ProjectSelection{
					WorkDir: spec.WorkDir, Project: spec.Project, Spec: &spec,
				}, RestartOption{})
				return err
			},
		},
	}
	limits := []struct {
		name     string
		limit    int
		full     int
		hold     time.Duration
		wantMost int
	}{
		{name: "limit 2", limit: 2, full: 3, hold: 250 * time.Millisecond, wantMost: 2},
		{name: "unlimited", limit: -1, full: len(entries), hold: 10 * time.Second, wantMost: 6},
	}

	for _, op := range ops {
		for _, l := range limits {
			t.Run(op.name+"/"+l.name, func(t *testing.T) {
				g := newStopGauge(l.full, l.hold)
				s := parallelService(l.limit, entries, g.stop)
				if err := op.run(context.Background(), s); err != nil {
					t.Fatalf("%s: %v", op.name, err)
				}
				most, calls := g.result()
				if calls != len(entries) {
					t.Errorf("stops = %d, want %d", calls, len(entries))
				}
				if most != l.wantMost {
					t.Errorf("most stops in flight = %d, want %d", most, l.wantMost)
				}
			})
		}
	}
}

func TestParallelLimitCancelEndsWaitingStops(t *testing.T) {
	api := reconcileCmd("api")
	api.Scale = 2
	spec := reconcileSpec(api)
	entries := runningReplicas(spec, api)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	s := parallelService(1, entries, func(
		ctx context.Context,
		_ cmdman.StopRequest,
	) ([]cmdman.StopResult, error) {
		calls.Add(1)
		// Give the other replica time to block on the permit this stop holds.
		time.Sleep(50 * time.Millisecond)
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})

	outcomes := stopAllConcurrent(ctx, s, entries, spec.Project, s.newTeardown(false, nil))
	if got := calls.Load(); got != 1 {
		t.Errorf("stops = %d, want 1", got)
	}
	if len(outcomes) != len(entries) {
		t.Fatalf("outcomes = %#v, want one per replica", outcomes)
	}
	for _, o := range outcomes {
		if !errors.Is(o.Err, context.Canceled) {
			t.Errorf("%s outcome error = %v, want %v", o.Command, o.Err, context.Canceled)
		}
	}
}
