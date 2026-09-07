package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// stopResultErrSvc answers List with entries and every Stop with a per-target
// result error, the way cmdman.Service reports a target whose monitor refused
// the stop: the call itself succeeds and the failure rides in the result.
func stopResultErrSvc(entries []store.CommandEntry, stopErr error) testCmdmanSvc {
	return testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return entries, nil
		},
		stop: func(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
			results := make([]cmdman.StopResult, len(req.Targets))
			for i, id := range req.Targets {
				results[i] = cmdman.StopResult{ID: id, Err: stopErr}
			}
			return results, nil
		},
	}
}

func restartOutcomeByCommand(outcomes []RestartOutcome, name string) (RestartOutcome, bool) {
	for _, o := range outcomes {
		if o.Command == name {
			return o, true
		}
	}
	return RestartOutcome{}, false
}

// TestStopReportsPerTargetResultError verifies a stop failure carried by the
// per-target result becomes the command's outcome error, so the CLI exits
// non-zero instead of reporting the command as stopped.
func TestStopReportsPerTargetResultError(t *testing.T) {
	want := errors.New("monitor refused stop")
	svc := &Service{svc: stopResultErrSvc(
		[]store.CommandEntry{storedGraphEntry("api", model.EventTypeRunning)},
		want,
	)}

	result, err := svc.Stop(
		context.Background(),
		ProjectSelection{WorkDir: "/wd", Project: "proj"},
		StopOption{},
	)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	o, ok := stopOutcomeByCommand(result.Stops, "api")
	if !ok {
		t.Fatalf("expected an api outcome, got %#v", result.Stops)
	}
	if !errors.Is(o.Err, want) {
		t.Fatalf("api outcome should carry the per-target stop error, got %v", o.Err)
	}
}

// TestRestartReportsPerTargetStopResultError verifies the restart stop phase
// records a per-target result error as the command's StopErr.
func TestRestartReportsPerTargetStopResultError(t *testing.T) {
	want := errors.New("monitor refused stop")
	spec := reconcileSpec(reconcileCmd("api"))
	svc := &Service{svc: stopResultErrSvc(
		[]store.CommandEntry{storedGraphEntry("api", model.EventTypeRunning)},
		want,
	)}

	result, err := svc.Restart(
		context.Background(),
		ProjectSelection{WorkDir: "/wd", Project: "proj", Spec: &spec},
		RestartOption{},
	)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	o, ok := restartOutcomeByCommand(result.Restarts, "api")
	if !ok {
		t.Fatalf("expected an api outcome, got %#v", result.Restarts)
	}
	if !errors.Is(o.StopErr, want) {
		t.Fatalf("api outcome should carry the per-target stop error, got %v", o.StopErr)
	}
	if o.StartErr != nil {
		t.Fatalf("api start should succeed, got %v", o.StartErr)
	}
}

// TestStopAllConcurrentReportsPerTargetResultError covers the fileless teardown
// path down uses when no dependency graph is reconstructable.
func TestStopAllConcurrentReportsPerTargetResultError(t *testing.T) {
	want := errors.New("monitor refused stop")
	entries := []store.CommandEntry{storedGraphEntry("api", model.EventTypeRunning)}
	svc := &Service{svc: stopResultErrSvc(entries, want)}

	outcomes := stopAllConcurrent(context.Background(), svc, entries, "proj")
	o, ok := stopOutcomeByCommand(outcomes, "api")
	if !ok {
		t.Fatalf("expected an api outcome, got %#v", outcomes)
	}
	if !errors.Is(o.Err, want) {
		t.Fatalf("api outcome should carry the per-target stop error, got %v", o.Err)
	}
}
