package compose

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

type testCmdmanSvc struct {
	logs     func(context.Context, cmdman.LogsRequest) (logdriver.Reader, error)
	list     func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error)
	inspect  func(context.Context, string) (*cmdman.InspectOutput, error)
	events   func(context.Context, cmdman.EventsRequest) (*cmdman.EventsSubscription, error)
	sendKeys func(context.Context, string, cmdman.SendKeysRequest) error
	start    func(context.Context, string) error
	wait     func(context.Context, cmdman.WaitRequest) ([]cmdman.WaitResult, error)
}

func (s testCmdmanSvc) Start(ctx context.Context, idOrName string) error {
	if s.start != nil {
		return s.start(ctx, idOrName)
	}
	return nil
}

func (s testCmdmanSvc) Wait(
	ctx context.Context,
	req cmdman.WaitRequest,
) ([]cmdman.WaitResult, error) {
	if s.wait != nil {
		return s.wait(ctx, req)
	}
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

func (s testCmdmanSvc) Inspect(
	ctx context.Context,
	idOrName string,
) (*cmdman.InspectOutput, error) {
	if s.inspect != nil {
		return s.inspect(ctx, idOrName)
	}
	return nil, nil
}

func (s testCmdmanSvc) Events(
	ctx context.Context,
	req cmdman.EventsRequest,
) (*cmdman.EventsSubscription, error) {
	if s.events != nil {
		return s.events(ctx, req)
	}
	return nil, nil
}

func (s testCmdmanSvc) OpenAttachSession(context.Context, string) (*cmdman.Session, error) {
	return nil, nil
}

func (s testCmdmanSvc) SendKeys(
	ctx context.Context,
	idOrName string,
	req cmdman.SendKeysRequest,
) error {
	if s.sendKeys != nil {
		return s.sendKeys(ctx, idOrName, req)
	}
	return nil
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

// reconcileTestEnv wires a Service over a fake cmdmanSvc that records Start
// calls (in order) and lets a test drive Wait results per command.
type reconcileTestEnv struct {
	mu         sync.Mutex
	startOrder []string
	startCalls map[string]int

	// startHook, when set for a generated name, runs before Start returns. Use
	// it to block, signal barriers, or return an error.
	startHook map[string]func(context.Context) error
	// waitResult, when set for a generated name, is returned from Wait. Absent
	// means a clean exit with code 0.
	waitResult map[string]cmdman.WaitResult
	// waitHook, when set for a generated name, runs before Wait returns.
	waitHook map[string]func(context.Context) error
}

func newReconcileTestEnv() *reconcileTestEnv {
	return &reconcileTestEnv{
		startCalls: map[string]int{},
		startHook:  map[string]func(context.Context) error{},
		waitResult: map[string]cmdman.WaitResult{},
		waitHook:   map[string]func(context.Context) error{},
	}
}

// svc builds a Service whose List returns entries derived from states, and
// whose Start/Wait route through the recorder.
func (e *reconcileTestEnv) svc(states map[string]model.EventType) *Service {
	return &Service{svc: testCmdmanSvc{
		list: func(_ context.Context, _ cmdman.ListRequest) ([]store.CommandEntry, error) {
			var entries []store.CommandEntry
			for name, st := range states {
				entries = append(entries, store.CommandEntry{
					ID:    "id-" + name,
					Name:  "gen-" + name,
					State: st,
					ConfigJSON: &model.CommandConfig{
						Labels: map[string]string{LabelCommand: name},
					},
				})
			}
			return entries, nil
		},
		start: func(ctx context.Context, genName string) error {
			e.mu.Lock()
			e.startOrder = append(e.startOrder, genName)
			e.startCalls[genName]++
			hook := e.startHook[genName]
			e.mu.Unlock()
			if hook != nil {
				return hook(ctx)
			}
			return nil
		},
		wait: func(ctx context.Context, req cmdman.WaitRequest) ([]cmdman.WaitResult, error) {
			genName := req.Targets[0]
			e.mu.Lock()
			hook := e.waitHook[genName]
			res, ok := e.waitResult[genName]
			e.mu.Unlock()
			if hook != nil {
				if err := hook(ctx); err != nil {
					return nil, err
				}
			}
			if !ok {
				zero := 0
				res = cmdman.WaitResult{ID: genName, ExitCode: &zero}
			}
			return []cmdman.WaitResult{res}, nil
		},
	}}
}

func (e *reconcileTestEnv) started(genName string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startCalls[genName] > 0
}

func (e *reconcileTestEnv) order() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.startOrder)
}

// cmd builds a normalized Command with a GeneratedName and after deps.
func reconcileCmd(name string, after ...AfterSpec) Command {
	return Command{Name: name, GeneratedName: "gen-" + name, After: after}
}

func reconcileSpec(cmds ...Command) ComposeSpec {
	return ComposeSpec{Project: "proj", WorkDir: "/wd", Commands: cmds}
}

func outcomeByCommand(outcomes []StartOutcome, name string) (StartOutcome, bool) {
	for _, o := range outcomes {
		if o.Command == name {
			return o, true
		}
	}
	return StartOutcome{}, false
}

// TestReconcileStartedConditionStartsDependentWithoutWaitingForExit verifies a
// "started" dependency releases its dependent as soon as the dependency starts,
// without waiting for it to terminate, and without Wait being called for it.
func TestReconcileStartedConditionStartsDependentWithoutWaitingForExit(t *testing.T) {
	env := newReconcileTestEnv()
	apiStarted := make(chan struct{})
	releaseAPI := make(chan struct{})
	env.startHook["gen-api"] = func(context.Context) error {
		close(apiStarted)
		<-releaseAPI // api stays "starting" until the test releases it
		return nil
	}

	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionStarted}),
	)
	states := map[string]model.EventType{
		"api":    model.EventTypeCreated,
		"worker": model.EventTypeCreated,
	}

	// api blocks in Start; worker must not start until api's Start returns.
	go func() {
		<-apiStarted
		if env.started("gen-worker") {
			t.Errorf("worker started before api finished starting")
		}
		close(releaseAPI)
	}()

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if !env.started("gen-worker") {
		t.Fatal("worker should have started after api started")
	}
	for _, name := range []string{"api", "worker"} {
		if o, _ := outcomeByCommand(outcomes, name); o.Err != nil {
			t.Fatalf("%s outcome error: %v", name, o.Err)
		}
	}
}

// TestReconcileCompletedConditionWaitsForTerminal verifies a "completed"
// dependency blocks its dependent until the dependency reaches a terminal state.
func TestReconcileCompletedConditionWaitsForTerminal(t *testing.T) {
	env := newReconcileTestEnv()
	releaseWait := make(chan struct{})
	waitEntered := make(chan struct{})
	env.waitHook["gen-api"] = func(context.Context) error {
		close(waitEntered)
		<-releaseWait
		return nil
	}

	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionCompleted}),
	)
	states := map[string]model.EventType{
		"api":    model.EventTypeCreated,
		"worker": model.EventTypeCreated,
	}

	go func() {
		<-waitEntered
		if env.started("gen-worker") {
			t.Errorf("worker started before api completed")
		}
		close(releaseWait)
	}()

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if !env.started("gen-worker") {
		t.Fatal("worker should have started after api completed")
	}
	if o, _ := outcomeByCommand(outcomes, "worker"); o.Err != nil {
		t.Fatalf("worker outcome error: %v", o.Err)
	}
}

// TestReconcileCompletedSuccessfullyBlocksOnNonZeroExit verifies that a
// completed_successfully dependency with a non-zero exit blocks only the
// dependent branch.
func TestReconcileCompletedSuccessfullyBlocksOnNonZeroExit(t *testing.T) {
	env := newReconcileTestEnv()
	two := 2
	env.waitResult["gen-api"] = cmdman.WaitResult{ID: "gen-api", ExitCode: &two}

	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionCompletedSuccessfully}),
	)
	states := map[string]model.EventType{
		"api":    model.EventTypeCreated,
		"worker": model.EventTypeCreated,
	}

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if env.started("gen-worker") {
		t.Fatal("worker must not start when api exits non-zero")
	}
	if o, _ := outcomeByCommand(outcomes, "api"); o.Err != nil {
		t.Fatalf("api outcome should succeed, got %v", o.Err)
	}
	if o, ok := outcomeByCommand(outcomes, "worker"); !ok || o.Err == nil {
		t.Fatalf("worker outcome should record a dependency error, got %#v", o)
	}
}

// TestReconcileStaleTerminalStateDoesNotSatisfyCompleted is the core fix: when
// the dependency is being restarted, a completed dependent must wait for the
// new run, not proceed from the dependency's stale terminal state.
func TestReconcileStaleTerminalStateDoesNotSatisfyCompleted(t *testing.T) {
	env := newReconcileTestEnv()
	releaseWait := make(chan struct{})
	waitEntered := make(chan struct{})
	env.waitHook["gen-api"] = func(context.Context) error {
		close(waitEntered)
		<-releaseWait
		return nil
	}

	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionCompleted}),
	)
	// Both already exited from a previous run: the stale state must not release
	// worker before api is started and waited again.
	states := map[string]model.EventType{
		"api":    model.EventTypeExited,
		"worker": model.EventTypeExited,
	}

	go func() {
		<-waitEntered
		if env.started("gen-worker") {
			t.Errorf("worker started from api's stale terminal state")
		}
		close(releaseWait)
	}()

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if !env.started("gen-api") {
		t.Fatal("api should be restarted from its exited state")
	}
	if !env.started("gen-worker") {
		t.Fatal("worker should start after api's new completion")
	}
	if o, _ := outcomeByCommand(outcomes, "worker"); o.Err != nil {
		t.Fatalf("worker outcome error: %v", o.Err)
	}
}

// TestReconcileRestartsExitedAndFailed verifies selected commands in previous
// exited and failed states are started again, matching low-level cmdman start.
func TestReconcileRestartsExitedAndFailed(t *testing.T) {
	env := newReconcileTestEnv()
	spec := reconcileSpec(reconcileCmd("alpha"), reconcileCmd("beta"))
	states := map[string]model.EventType{
		"alpha": model.EventTypeExited,
		"beta":  model.EventTypeFailed,
	}

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if !env.started("gen-alpha") || !env.started("gen-beta") {
		t.Fatalf("both exited/failed commands should be started; order=%v", env.order())
	}
	for _, name := range []string{"alpha", "beta"} {
		if o, _ := outcomeByCommand(outcomes, name); o.Err != nil {
			t.Fatalf("%s outcome error: %v", name, o.Err)
		}
	}
}

// TestReconcileSkipsActiveCommands verifies starting/started commands are not
// re-started.
func TestReconcileSkipsActiveCommands(t *testing.T) {
	env := newReconcileTestEnv()
	spec := reconcileSpec(reconcileCmd("alpha"), reconcileCmd("beta"))
	states := map[string]model.EventType{
		"alpha": model.EventTypeStarted,
		"beta":  model.EventTypeStarting,
	}

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if env.started("gen-alpha") || env.started("gen-beta") {
		t.Fatalf("active commands must not be re-started; order=%v", env.order())
	}
	for _, name := range []string{"alpha", "beta"} {
		if o, _ := outcomeByCommand(outcomes, name); o.Err != nil {
			t.Fatalf("%s outcome error: %v", name, o.Err)
		}
	}
}

// TestReconcileIndependentCommandsStartConcurrently verifies independent
// commands start at the same time, asserted via a barrier rather than timing.
func TestReconcileIndependentCommandsStartConcurrently(t *testing.T) {
	env := newReconcileTestEnv()
	bothEntered := make(chan struct{})
	var once sync.Once
	entered := make(chan string, 2)
	barrier := func(context.Context) error {
		entered <- "in"
		if len(entered) == 2 {
			once.Do(func() { close(bothEntered) })
		}
		// Block until both commands have entered Start concurrently.
		select {
		case <-bothEntered:
		case <-time.After(5 * time.Second):
			return errors.New("timeout waiting for concurrent start")
		}
		return nil
	}
	env.startHook["gen-alpha"] = barrier
	env.startHook["gen-beta"] = barrier

	spec := reconcileSpec(reconcileCmd("alpha"), reconcileCmd("beta"))
	states := map[string]model.EventType{
		"alpha": model.EventTypeCreated,
		"beta":  model.EventTypeCreated,
	}

	outcomes, err := env.svc(states).reconcileStart(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if o, _ := outcomeByCommand(outcomes, name); o.Err != nil {
			t.Fatalf("%s outcome error: %v", name, o.Err)
		}
	}
}

// TestReconcileFailedBranchDoesNotBlockSibling verifies a failed start on one
// branch does not prevent an independent branch from starting.
func TestReconcileFailedBranchDoesNotBlockSibling(t *testing.T) {
	env := newReconcileTestEnv()
	env.startHook["gen-api"] = func(context.Context) error {
		return errors.New("boom")
	}

	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionStarted}),
		reconcileCmd("solo"),
	)
	states := map[string]model.EventType{
		"api":    model.EventTypeCreated,
		"worker": model.EventTypeCreated,
		"solo":   model.EventTypeCreated,
	}

	// Inject a logger via context to prove the service-layer warning wiring works.
	buf, ctx := warnLogger()
	outcomes, err := env.svc(states).reconcileStart(ctx, spec, nil)
	if err != nil {
		t.Fatalf("reconcileStart: %v", err)
	}
	if log := buf.String(); !strings.Contains(log, "compose: start failed") ||
		!strings.Contains(log, "api") {
		t.Fatalf("expected a start-failure warning mentioning api; got:\n%s", log)
	}
	if !env.started("gen-solo") {
		t.Fatal("independent command should start despite api failure")
	}
	if env.started("gen-worker") {
		t.Fatal("worker depends on a failed api and must not start")
	}
	if o, _ := outcomeByCommand(outcomes, "api"); o.Err == nil {
		t.Fatal("api outcome should record the start error")
	}
	if o, _ := outcomeByCommand(outcomes, "worker"); o.Err == nil {
		t.Fatal("worker outcome should record a dependency error")
	}
	if o, _ := outcomeByCommand(outcomes, "solo"); o.Err != nil {
		t.Fatalf("solo outcome error: %v", o.Err)
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
				buildTestProjectEntry(
					"id-1",
					"api",
					"project-a",
					"/tmp/a",
					"/tmp/a/cmd-compose.yaml",
					model.EventTypeStarted,
				),
				buildTestProjectEntry(
					"id-2",
					"worker",
					"project-a",
					"/tmp/a",
					"/tmp/a/cmd-compose.yaml",
					model.EventTypeExited,
				),
				buildTestProjectEntry(
					"id-3",
					"api",
					"project-b",
					"/tmp/b",
					"/tmp/b/cmd-compose.yaml",
					model.EventTypeFailed,
				),
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
		ConfigJSON: &model.CommandConfig{
			Labels: map[string]string{LabelCommand: command},
		},
	}
}

func buildTestProjectEntry(
	id, command, project, workDir, file string,
	state model.EventType,
) store.CommandEntry {
	return store.CommandEntry{
		ID:    id,
		State: state,
		ConfigJSON: &model.CommandConfig{
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
