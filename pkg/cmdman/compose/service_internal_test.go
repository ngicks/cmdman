package compose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

type testCmdmanSvc struct {
	logs func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error)
}

func (s testCmdmanSvc) Start(context.Context, string) error {
	return nil
}

func (s testCmdmanSvc) Wait(context.Context, cmdman.WaitRequest) ([]cmdman.WaitResult, error) {
	return nil, nil
}

func (s testCmdmanSvc) List(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
	return nil, nil
}

func (s testCmdmanSvc) Create(context.Context, cmdman.CreateRequest) (*cmdman.CreateResult, error) {
	return nil, nil
}

func (s testCmdmanSvc) Remove(
	context.Context,
	cmdman.RemoveRequest,
) ([]cmdman.RemoveResult, error) {
	return nil, nil
}

func (s testCmdmanSvc) Stop(context.Context, cmdman.StopRequest) ([]cmdman.StopResult, error) {
	return nil, nil
}

func (s testCmdmanSvc) Signal(context.Context, string, int32) error {
	return nil
}

func (s testCmdmanSvc) Logs(
	ctx context.Context,
	req cmdman.LogsRequest,
) (logdriver.Reader, error) {
	return s.logs(ctx, req)
}

type testLogReader struct {
	records  chan logdriver.Record
	closeErr error
}

func (r testLogReader) Records() <-chan logdriver.Record {
	return r.records
}

func (r testLogReader) Close() error {
	return r.closeErr
}

func TestWaitForConditionStartedDoesNotPassOnPreExistingExit(t *testing.T) {
	events := make(chan depEvent)
	close(events)

	err := waitForCondition(
		context.Background(),
		testCmdmanSvc{},
		map[string]*dagCommand{
			"api": {
				genName: "generated-api",
				events:  events,
			},
		},
		map[string]string{"api": store.StateExited},
		AfterSpec{Name: "api", Condition: ConditionStarted},
		"worker",
	)
	if err == nil {
		t.Fatal("expected started condition to fail for a pre-existing exited dependency")
	}
}

func TestLogsMergedReturnsOpenReaderErrors(t *testing.T) {
	want := errors.New("no retained logs")
	svc := &Service{svc: testCmdmanSvc{
		logs: func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error) {
			return nil, want
		},
	}}

	err := svc.logsMerged(context.Background(), "project", []cmdmanEntry{
		buildTestEntry("id-alpha", "alpha"),
	}, LogsOption{}, make(chan LogMessage, 1))
	if !errors.Is(err, want) {
		t.Fatalf("expected logs error %v, got %v", want, err)
	}
}

func TestLogsMergedReturnsRecordErrors(t *testing.T) {
	want := errors.New("bad record")
	records := make(chan logdriver.Record, 1)
	records <- logdriver.Record{Err: want}
	close(records)

	svc := &Service{svc: testCmdmanSvc{
		logs: func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error) {
			return testLogReader{records: records}, nil
		},
	}}

	err := svc.logsMerged(context.Background(), "project", []cmdmanEntry{
		buildTestEntry("id-alpha", "alpha"),
	}, LogsOption{}, make(chan LogMessage, 1))
	if !errors.Is(err, want) {
		t.Fatalf("expected record error %v, got %v", want, err)
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("expected command name in error, got: %v", err)
	}
}

func buildTestEntry(id, command string) store.CommandEntry {
	return store.CommandEntry{
		ID: id,
		ConfigJSON: &store.CommandConfigJSON{
			Labels: map[string]string{LabelCommand: command},
		},
	}
}
