package compose

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// stopTimeoutRecorder records the calls a compose operation makes to cmdman,
// and the timeout of every stop among them.
type stopTimeoutRecorder struct {
	mu       sync.Mutex
	lists    int
	timeouts []*time.Duration
}

// svc answers a List of replicas with entries, a List of resource holders or
// hook commands with none, and every Stop with success.
func (r *stopTimeoutRecorder) svc(entries []store.CommandEntry) testCmdmanSvc {
	return testCmdmanSvc{
		list: func(_ context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lists++
			if _, ok := req.Labels[LabelIntermediate]; ok {
				return nil, nil
			}
			return entries, nil
		},
		stop: func(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.timeouts = append(r.timeouts, req.Timeout)
			results := make([]cmdman.StopResult, len(req.Targets))
			for i, id := range req.Targets {
				results[i] = cmdman.StopResult{ID: id}
			}
			return results, nil
		},
	}
}

func (r *stopTimeoutRecorder) recorded() (lists int, timeouts []*time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lists, append([]*time.Duration(nil), r.timeouts...)
}

// stopTimeoutOps runs compose stop, down and restart on the project of
// storedGraphEntry with the given stop timeout.
var stopTimeoutOps = []struct {
	name string
	run  func(ctx context.Context, s *Service, timeout *time.Duration) error
}{
	{name: "stop", run: func(ctx context.Context, s *Service, timeout *time.Duration) error {
		_, err := s.Stop(ctx, ProjectSelection{WorkDir: "/wd", Project: "proj"},
			StopOption{Timeout: timeout})
		return err
	}},
	{name: "down", run: func(ctx context.Context, s *Service, timeout *time.Duration) error {
		_, err := s.Down(ctx, ProjectSelection{WorkDir: "/wd", Project: "proj"},
			DownOption{Timeout: timeout})
		return err
	}},
	{name: "restart", run: func(ctx context.Context, s *Service, timeout *time.Duration) error {
		spec := reconcileSpec(reconcileCmd("api"))
		_, err := s.Restart(ctx, ProjectSelection{WorkDir: "/wd", Project: "proj", Spec: &spec},
			RestartOption{Timeout: timeout})
		return err
	}},
}

// The timeout of a compose stop, down or restart reaches the stop of every
// replica, and an unset one leaves each replica its stored stop timeout.
func TestStopTimeoutReachesEveryStop(t *testing.T) {
	for _, op := range stopTimeoutOps {
		for _, timeout := range []*time.Duration{nil, new(3 * time.Second)} {
			name := op.name + "/unset"
			if timeout != nil {
				name = op.name + "/" + timeout.String()
			}
			t.Run(name, func(t *testing.T) {
				rec := &stopTimeoutRecorder{}
				s := &Service{svc: rec.svc(
					[]store.CommandEntry{storedGraphEntry("api", model.EventTypeRunning)},
				)}

				if err := op.run(context.Background(), s, timeout); err != nil {
					t.Fatalf("%s: %v", op.name, err)
				}

				_, got := rec.recorded()
				if len(got) != 1 {
					t.Fatalf("expected one stop, got %d", len(got))
				}
				switch {
				case timeout == nil && got[0] != nil:
					t.Fatalf("stop timeout = %s, want unset", *got[0])
				case timeout != nil && (got[0] == nil || *got[0] != *timeout):
					t.Fatalf("stop timeout = %v, want %s", got[0], *timeout)
				}
			})
		}
	}
}

// A compose stop, down or restart refuses a timeout that is not positive before
// it looks at the project, so no stop hook runs for a stop cmdman would refuse.
func TestStopTimeoutRejectsNonPositive(t *testing.T) {
	for _, op := range stopTimeoutOps {
		for _, timeout := range []time.Duration{0, -3 * time.Second} {
			t.Run(op.name+"/"+timeout.String(), func(t *testing.T) {
				rec := &stopTimeoutRecorder{}
				s := &Service{svc: rec.svc(
					[]store.CommandEntry{storedGraphEntry("api", model.EventTypeRunning)},
				)}

				err := op.run(context.Background(), s, &timeout)
				if err == nil || !strings.Contains(err.Error(), "stop timeout must be positive") {
					t.Fatalf("%s: want a non-positive timeout error, got %v", op.name, err)
				}
				if lists, stops := rec.recorded(); lists != 0 || len(stops) != 0 {
					t.Fatalf("expected no List and no Stop, got %d and %d", lists, len(stops))
				}
			})
		}
	}
}

// The stop before a recreate waits the stored stop timeout of the replica.
func TestRecreateStopLeavesStoredTimeout(t *testing.T) {
	rec := &stopTimeoutRecorder{}
	svc := rec.svc(nil)
	svc.remove = func(_ context.Context, req cmdman.RemoveRequest) ([]cmdman.RemoveResult, error) {
		return []cmdman.RemoveResult{{ID: req.Targets[0]}}, nil
	}
	s := &Service{svc: svc}

	outcome, err := s.executeAction(
		context.Background(),
		reconcileSpec(reconcileCmd("alpha")),
		CommandAction{
			Kind:    ActionRecreate,
			Desired: reconcileCmd("alpha"),
			Existing: &store.CommandEntry{
				ID:         "id-alpha",
				State:      model.EventTypeRunning,
				ConfigJSON: &model.CommandConfig{},
			},
			DesiredHash: "h2",
		},
	)
	if err != nil || outcome.Err != nil {
		t.Fatalf("recreate: %v / %v", err, outcome.Err)
	}
	_, got := rec.recorded()
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("expected one stop with the timeout unset, got %v", got)
	}
}
