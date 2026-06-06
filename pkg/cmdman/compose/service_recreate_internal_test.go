package compose

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// captureReporter records the phase of every event it receives, in order, so a
// test can assert the exact lifecycle trace an operation produced.
type captureReporter struct {
	mu     sync.Mutex
	phases []Phase
}

func (r *captureReporter) Report(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, ev.Phase)
}

func (r *captureReporter) snapshot() []Phase {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.phases)
}

// TestExecuteActionRecreateStopsRunningCommand verifies that recreating a command
// that is currently running stops it first (surfaced as stopping → stopped),
// then removes and recreates it, succeeding overall.
func TestExecuteActionRecreateStopsRunningCommand(t *testing.T) {
	var stopCalls, removeCalls, createCalls int
	rep := &captureReporter{}
	svc := &Service{
		reporter: rep,
		svc: testCmdmanSvc{
			stop: func(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
				stopCalls++
				return []cmdman.StopResult{{ID: req.Targets[0]}}, nil
			},
			remove: func(_ context.Context, req cmdman.RemoveRequest) ([]cmdman.RemoveResult, error) {
				removeCalls++
				return []cmdman.RemoveResult{{ID: req.Targets[0]}}, nil
			},
			create: func(context.Context, cmdman.CreateRequest) (*cmdman.CreateResult, error) {
				createCalls++
				return nil, nil
			},
		},
	}

	action := CommandAction{
		Kind:        ActionRecreate,
		Desired:     reconcileCmd("alpha"),
		Existing:    &store.CommandEntry{ID: "id-alpha", State: model.EventTypeStarted},
		DesiredHash: "h2",
	}
	outcome, err := svc.executeAction(
		context.Background(),
		reconcileSpec(reconcileCmd("alpha")),
		action,
	)
	if err != nil {
		t.Fatalf("executeAction returned internal error: %v", err)
	}
	if outcome.Err != nil {
		t.Fatalf("recreate of a running command should succeed, got err: %v", outcome.Err)
	}
	if outcome.Action != "recreate" {
		t.Fatalf("expected action %q, got %q", "recreate", outcome.Action)
	}
	if stopCalls != 1 || removeCalls != 1 || createCalls != 1 {
		t.Fatalf("expected stop/remove/create each once; got stop=%d remove=%d create=%d",
			stopCalls, removeCalls, createCalls)
	}
	want := []Phase{PhaseStopping, PhaseStopped, PhaseRecreating, PhaseRecreated}
	if got := rep.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("phase trace = %v, want %v", got, want)
	}
}

// TestExecuteActionRecreateStopFailureAbortsRecreate verifies the safety
// invariant: when stopping a running command fails, the recreate is aborted
// before the remove so the still-running command is never removed out from
// under its live monitor.
func TestExecuteActionRecreateStopFailureAbortsRecreate(t *testing.T) {
	stopErr := errors.New("monitor unreachable")
	var removeCalled bool
	rep := &captureReporter{}
	svc := &Service{
		reporter: rep,
		svc: testCmdmanSvc{
			stop: func(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
				return []cmdman.StopResult{{ID: req.Targets[0], Err: stopErr}}, nil
			},
			remove: func(context.Context, cmdman.RemoveRequest) ([]cmdman.RemoveResult, error) {
				removeCalled = true
				return nil, nil
			},
		},
	}

	action := CommandAction{
		Kind:     ActionRecreate,
		Desired:  reconcileCmd("alpha"),
		Existing: &store.CommandEntry{ID: "id-alpha", State: model.EventTypeStarted},
	}
	outcome, err := svc.executeAction(
		context.Background(),
		reconcileSpec(reconcileCmd("alpha")),
		action,
	)
	if err != nil {
		t.Fatalf("executeAction returned internal error: %v", err)
	}
	if outcome.Err == nil {
		t.Fatalf("expected recreate to fail when the stop fails")
	}
	if !errors.Is(outcome.Err, stopErr) {
		t.Fatalf("expected the stop error to be wrapped, got: %v", outcome.Err)
	}
	if outcome.Action != "recreate" {
		t.Fatalf("expected action %q, got %q", "recreate", outcome.Action)
	}
	if removeCalled {
		t.Fatalf("a still-running command must not be removed after a failed stop")
	}
	want := []Phase{PhaseStopping, PhaseError}
	if got := rep.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("phase trace = %v, want %v", got, want)
	}
}
