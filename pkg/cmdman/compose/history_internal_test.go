package compose

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// historyRecorder captures the history requests a Service issues.
type historyRecorder struct {
	mu   sync.Mutex
	reqs []cmdman.ComposeHistoryRequest
	err  error
}

func (r *historyRecorder) record(_ context.Context, req cmdman.ComposeHistoryRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return r.err
}

func (r *historyRecorder) calls() []cmdman.ComposeHistoryRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cmdman.ComposeHistoryRequest(nil), r.reqs...)
}

// TestCreateRecordsHistory pins the writer site: Create — which every entry path
// including Up goes through — records the project with the compose file it was
// brought up from.
func TestCreateRecordsHistory(t *testing.T) {
	rec := &historyRecorder{}
	svc := &Service{svc: testCmdmanSvc{history: rec.record}}

	spec := reconcileSpec(reconcileCmd("api"))
	spec.ComposeFile = "/wd/cmd-compose.yaml"
	if _, err := svc.Create(context.Background(), spec, CreateOption{}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one history write, got %#v", calls)
	}
	want := cmdman.ComposeHistoryRequest{
		WorkDir: "/wd",
		Project: "proj",
		File:    "/wd/cmd-compose.yaml",
	}
	if calls[0] != want {
		t.Fatalf("history request: want %#v, got %#v", want, calls[0])
	}
}

// TestCreateSurvivesHistoryFailure pins history as auxiliary state: a failed
// write warns and leaves the create result intact.
func TestCreateSurvivesHistoryFailure(t *testing.T) {
	rec := &historyRecorder{err: errors.New("db is gone")}
	svc := &Service{svc: testCmdmanSvc{history: rec.record}}

	spec := reconcileSpec(reconcileCmd("api"))
	spec.ComposeFile = "/wd/cmd-compose.yaml"
	buf, ctx := warnLogger()
	result, err := svc.Create(ctx, spec, CreateOption{})
	if err != nil {
		t.Fatalf("Create must not fail on a history write error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected the create action to be reported, got %#v", result.Actions)
	}
	if log := buf.String(); !strings.Contains(log, "compose: record project history") {
		t.Fatalf("expected a history warning; got:\n%s", log)
	}
}

// TestStartBumpsRecencyOnly covers the path that bypasses Create. Neither start
// variant may send a File: rows exist because a create made them, and the file a
// start was pointed at is not necessarily the one the running commands came
// from, so an empty File keeps the request a recency bump.
func TestStartBumpsRecencyOnly(t *testing.T) {
	want := cmdman.ComposeHistoryRequest{WorkDir: "/wd", Project: "proj"}

	for _, tc := range []struct {
		name      string
		selection ProjectSelection
	}{
		{
			// Resolved from stored labels: the project is known, the file is not.
			name:      "without spec",
			selection: ProjectSelection{WorkDir: "/wd", Project: "proj"},
		},
		{
			// Resolved from a compose file, which must still not overwrite File.
			name: "with spec",
			selection: func() ProjectSelection {
				spec := reconcileSpec(reconcileCmd("api"))
				spec.ComposeFile = "/wd/other-compose.yaml"
				return ProjectSelection{Spec: &spec, WorkDir: "/wd", Project: "proj"}
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &historyRecorder{}
			svc := &Service{svc: testCmdmanSvc{
				history: rec.record,
				list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
					return []store.CommandEntry{
						storedGraphEntry("api", model.EventTypeRunning),
					}, nil
				},
			}}

			if _, err := svc.Start(context.Background(), tc.selection, StartOption{}); err != nil {
				t.Fatalf("Start: %v", err)
			}

			calls := rec.calls()
			if len(calls) != 1 {
				t.Fatalf("expected one history write, got %#v", calls)
			}
			if calls[0] != want {
				t.Fatalf("history request: want %#v, got %#v", want, calls[0])
			}
		})
	}
}
