package compose

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

type testCmdmanSvc struct {
	logs func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error)
	list func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error)
}

func (s testCmdmanSvc) Start(context.Context, string) error {
	return nil
}

func (s testCmdmanSvc) Wait(context.Context, cmdman.WaitRequest) ([]cmdman.WaitResult, error) {
	return nil, nil
}

func (s testCmdmanSvc) List(
	ctx context.Context,
	req cmdman.ListRequest,
) ([]store.CommandEntry, error) {
	if s.list != nil {
		return s.list(ctx, req)
	}
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

func TestLogsStockReturnsOpenReaderErrors(t *testing.T) {
	want := errors.New("no retained logs")
	svc := &Service{svc: testCmdmanSvc{
		logs: func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error) {
			return nil, want
		},
	}}

	err := svc.logsStock(context.Background(), "project", []cmdmanEntry{
		buildTestEntry("id-alpha", "alpha"),
	}, LogsOption{}, time.Time{}, make(chan LogMessage, 1))
	if !errors.Is(err, want) {
		t.Fatalf("expected logs error %v, got %v", want, err)
	}
}

func TestLogsStockReturnsRecordErrors(t *testing.T) {
	want := errors.New("bad record")
	records := make(chan logdriver.Record, 1)
	records <- logdriver.Record{Err: want}
	close(records)

	svc := &Service{svc: testCmdmanSvc{
		logs: func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error) {
			return testLogReader{records: records}, nil
		},
	}}

	err := svc.logsStock(context.Background(), "project", []cmdmanEntry{
		buildTestEntry("id-alpha", "alpha"),
	}, LogsOption{}, time.Time{}, make(chan LogMessage, 1))
	if !errors.Is(err, want) {
		t.Fatalf("expected record error %v, got %v", want, err)
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("expected command name in error, got: %v", err)
	}
}

// TestLogsStockMergesByTimestamp verifies the stock phase reorders records from
// multiple commands into a single timestamp-ordered stream using a streaming
// k-way merge over each command's already-sorted records.
func TestLogsStockMergesByTimestamp(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mk := func(offset time.Duration, line string) logdriver.Record {
		return logdriver.Record{
			Line: logdriver.LogLine{Time: base.Add(offset), Line: []byte(line)},
		}
	}

	// alpha and beta each emit ascending-by-time records that interleave.
	alpha := makeRecordReader(
		mk(0*time.Second, "a0"),
		mk(2*time.Second, "a2"),
		mk(4*time.Second, "a4"),
	)
	beta := makeRecordReader(
		mk(1*time.Second, "b1"),
		mk(3*time.Second, "b3"),
		mk(5*time.Second, "b5"),
	)

	readers := map[string]logdriver.Reader{"id-alpha": alpha, "id-beta": beta}
	svc := &Service{svc: testCmdmanSvc{
		logs: func(_ context.Context, req cmdman.LogsRequest) (logdriver.Reader, error) {
			return readers[req.IDOrName], nil
		},
	}}

	out := make(chan LogMessage, 16)
	err := svc.logsStock(context.Background(), "project", []cmdmanEntry{
		buildTestEntry("id-alpha", "alpha"),
		buildTestEntry("id-beta", "beta"),
	}, LogsOption{}, time.Time{}, out)
	if err != nil {
		t.Fatalf("logsStock failed: %v", err)
	}
	close(out)

	var got []string
	for msg := range out {
		got = append(got, string(msg.Record.Line.Line))
	}
	want := []string{"a0", "b1", "a2", "b3", "a4", "b5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected timestamp-ordered merge %v, got %v", want, got)
	}
}

func makeRecordReader(recs ...logdriver.Record) testLogReader {
	ch := make(chan logdriver.Record, len(recs))
	for _, rec := range recs {
		ch <- rec
	}
	close(ch)
	return testLogReader{records: ch}
}

func TestListProjectsGroupsComposeCommands(t *testing.T) {
	svc := &Service{svc: testCmdmanSvc{
		list: func(_ context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error) {
			if !req.AllStates {
				t.Fatal("expected all states")
			}
			if req.Labels[LabelVersion] != LabelVersionValue {
				t.Fatalf("expected compose label filter, got %#v", req.Labels)
			}
			return []store.CommandEntry{
				buildTestProjectEntry("id-1", "api", "project-a", "/tmp/a", "/tmp/a/cmd-compose.yaml", store.StateRunning),
				buildTestProjectEntry("id-2", "worker", "project-a", "/tmp/a", "/tmp/a/cmd-compose.yaml", store.StateExited),
				buildTestProjectEntry("id-3", "api", "project-b", "/tmp/b", "/tmp/b/cmd-compose.yaml", store.StateFailed),
			}, nil
		},
	}}

	summaries, err := svc.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 projects, got %#v", summaries)
	}
	if summaries[0].Project != "project-a" ||
		summaries[0].Commands != 2 ||
		summaries[0].Running != 1 ||
		summaries[0].Exited != 1 {
		t.Fatalf("unexpected first summary: %#v", summaries[0])
	}
	if summaries[1].Project != "project-b" ||
		summaries[1].Commands != 1 ||
		summaries[1].Failed != 1 {
		t.Fatalf("unexpected second summary: %#v", summaries[1])
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

func buildTestProjectEntry(
	id, command, project, workDir, file, state string,
) store.CommandEntry {
	return store.CommandEntry{
		ID:    id,
		State: state,
		ConfigJSON: &store.CommandConfigJSON{
			Labels: map[string]string{
				LabelCommand: command,
				LabelProject: project,
				LabelWorkdir: workDir,
				LabelFile:    file,
				LabelVersion: LabelVersionValue,
			},
		},
	}
}

func TestProjectLabelsOmitsEmptyProject(t *testing.T) {
	// Empty project: filter by workdir only. Since FindByLabels ANDs the given
	// labels, this matches every command in the workdir across all projects.
	got := projectLabels("/wd", "")
	if got[LabelWorkdir] != "/wd" {
		t.Fatalf("expected workdir label, got %v", got)
	}
	if _, ok := got[LabelProject]; ok {
		t.Fatalf("empty project must not add a project label, got %v", got)
	}

	// Known project: narrow to workdir + project.
	got = projectLabels("/wd", "proj")
	if got[LabelWorkdir] != "/wd" || got[LabelProject] != "proj" {
		t.Fatalf("expected workdir+project labels, got %v", got)
	}
}
