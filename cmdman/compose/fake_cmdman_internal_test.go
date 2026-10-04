package compose

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// fakeRun is what a started command of fakeCmdman does.
type fakeRun struct {
	// exit ends the run with this exit code; nil ends it failed, without one.
	exit   *int
	stdout []string
	stderr []string
	// block keeps the command running until it is stopped.
	block bool
}

// fakeCommand is one command fakeCmdman holds.
type fakeCommand struct {
	entry  store.CommandEntry
	req    cmdman.CreateRequest
	output []logdriver.LogLine
	// done is closed when the current run ends.
	done chan struct{}
}

// fakeCmdman is an in-memory stand-in for the cmdman service that keeps
// commands, their states and their output, so hook runs can be followed from
// create to remove.
type fakeCmdman struct {
	mu       sync.Mutex
	cfg      cmdman.CmdmanConfig
	nextID   int
	commands map[string]*fakeCommand
	calls    []string

	// run decides what a started command does. nil exits 0 with no output.
	run func(name string, req cmdman.CreateRequest) fakeRun
	// createErr fails a Create when it returns non-nil.
	createErr func(req cmdman.CreateRequest) error
	// started is called after a command has started.
	started func(name string)
}

func newFakeCmdman() *fakeCmdman {
	return &fakeCmdman{
		cfg: cmdman.CmdmanConfig{
			DataDir:            "/data",
			RuntimeDir:         "/run",
			DefaultWorkingDir:  "/default-wd",
			DefaultEnvironment: []string{"HOST_VAR=host"},
		},
		commands: map[string]*fakeCommand{},
	}
}

func (f *fakeCmdman) service(r Reporter) *Service {
	return &Service{
		svc: testCmdmanSvc{
			config: f.cfg,
			list:   f.list,
			create: f.create,
			start:  f.start,
			wait:   f.wait,
			stop:   f.stop,
			remove: f.remove,
			logs:   f.logs,
		},
		reporter: r,
	}
}

// lookup finds a command by id or name. The caller holds f.mu.
func (f *fakeCmdman) lookup(idOrName string) *fakeCommand {
	if c, ok := f.commands[idOrName]; ok {
		return c
	}
	for _, c := range f.commands {
		if c.entry.Name == idOrName {
			return c
		}
	}
	return nil
}

// get returns a snapshot of the command named name.
func (f *fakeCmdman) get(name string) (store.CommandEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.lookup(name)
	if c == nil {
		return store.CommandEntry{}, false
	}
	return c.entry, true
}

// request returns the create request of the command named name.
func (f *fakeCmdman) request(name string) cmdman.CreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.lookup(name); c != nil {
		return c.req
	}
	return cmdman.CreateRequest{}
}

func (f *fakeCmdman) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// put stores a command as if it had been created and left in state.
func (f *fakeCmdman) put(req cmdman.CreateRequest, state model.EventType) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.insert(req)
	f.commands[id].entry.State = state
	if state == model.EventTypeRunning {
		f.commands[id].done = make(chan struct{})
	}
	return id
}

// insert adds a created command. The caller holds f.mu.
func (f *fakeCmdman) insert(req cmdman.CreateRequest) string {
	f.nextID++
	id := fmt.Sprintf("id%d", f.nextID)
	f.commands[id] = &fakeCommand{
		entry: store.CommandEntry{
			ID:    id,
			Name:  req.Name,
			State: model.EventTypeCreated,
			ConfigJSON: &model.CommandConfig{
				Argv:          slices.Clone(req.Argv),
				Dir:           req.Dir,
				Env:           slices.Clone(req.Env),
				Labels:        maps.Clone(req.Labels),
				LogDriver:     req.LogDriver,
				RestartPolicy: req.RestartPolicy,
			},
		},
		req: req,
	}
	return id
}

func (f *fakeCmdman) list(_ context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.CommandEntry
	for _, c := range f.commands {
		matches := true
		for k, v := range req.Labels {
			if got, ok := c.entry.ConfigJSON.Labels[k]; !ok || got != v {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, c.entry)
		}
	}
	slices.SortFunc(
		out,
		func(a, b store.CommandEntry) int { return strings.Compare(a.Name, b.Name) },
	)
	return out, nil
}

func (f *fakeCmdman) create(
	_ context.Context,
	req cmdman.CreateRequest,
) (*cmdman.CreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create "+req.Name)
	if f.createErr != nil {
		if err := f.createErr(req); err != nil {
			return nil, err
		}
	}
	var replaced string
	if old := f.lookup(req.Name); old != nil {
		if !req.Replace {
			return nil, fmt.Errorf("name %q is in use", req.Name)
		}
		if old.entry.State == model.EventTypeRunning {
			return nil, fmt.Errorf("command %q is running", req.Name)
		}
		replaced = old.entry.ID
	}
	id := f.insert(req)
	if replaced != "" {
		delete(f.commands, replaced)
	}
	return &cmdman.CreateResult{ID: id, Name: req.Name}, nil
}

func (f *fakeCmdman) start(_ context.Context, idOrName string) error {
	f.mu.Lock()
	c := f.lookup(idOrName)
	if c == nil {
		f.mu.Unlock()
		return fmt.Errorf("no command %q", idOrName)
	}
	f.calls = append(f.calls, "start "+c.entry.Name)
	run := fakeRun{exit: new(0)}
	if f.run != nil {
		run = f.run(c.entry.Name, c.req)
	}
	c.output = nil
	for _, l := range run.stdout {
		c.output = append(c.output, logdriver.LogLine{
			Stream: logdriver.StreamStdout, Line: []byte(l + "\n"),
		})
	}
	for _, l := range run.stderr {
		c.output = append(c.output, logdriver.LogLine{
			Stream: logdriver.StreamStderr, Line: []byte(l + "\n"),
		})
	}
	c.done = make(chan struct{})
	if run.block {
		c.entry.State = model.EventTypeRunning
	} else {
		finishFake(c, run.exit)
	}
	name, started := c.entry.Name, f.started
	f.mu.Unlock()
	if started != nil {
		started(name)
	}
	return nil
}

// finishFake ends the current run of c. The caller holds the lock.
func finishFake(c *fakeCommand, exit *int) {
	if exit == nil {
		c.entry.State = model.EventTypeFailed
		c.entry.ExitCode = nil
	} else {
		c.entry.State = model.EventTypeExited
		c.entry.ExitCode = new(*exit)
	}
	close(c.done)
}

func (f *fakeCmdman) wait(
	ctx context.Context,
	req cmdman.WaitRequest,
) ([]cmdman.WaitResult, error) {
	target := req.Targets[0]
	f.mu.Lock()
	c := f.lookup(target)
	if c == nil {
		f.mu.Unlock()
		return []cmdman.WaitResult{{ID: target}}, nil
	}
	done := c.done
	f.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return []cmdman.WaitResult{{ID: target, Err: ctx.Err()}}, nil
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []cmdman.WaitResult{{ID: target, ExitCode: c.entry.ExitCode}}, nil
}

func (f *fakeCmdman) stop(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cmdman.StopResult
	for _, t := range req.Targets {
		c := f.lookup(t)
		if c == nil {
			out = append(out, cmdman.StopResult{ID: t})
			continue
		}
		f.calls = append(f.calls, "stop "+c.entry.Name)
		if c.entry.State == model.EventTypeRunning {
			finishFake(c, new(143))
		}
		out = append(out, cmdman.StopResult{ID: c.entry.ID})
	}
	return out, nil
}

func (f *fakeCmdman) remove(
	_ context.Context,
	req cmdman.RemoveRequest,
) ([]cmdman.RemoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cmdman.RemoveResult
	for _, t := range req.Targets {
		c := f.lookup(t)
		if c == nil {
			out = append(out, cmdman.RemoveResult{ID: t, Err: fmt.Errorf("no command %q", t)})
			continue
		}
		f.calls = append(f.calls, "remove "+c.entry.Name)
		if c.entry.State == model.EventTypeRunning && !req.Force {
			out = append(out, cmdman.RemoveResult{ID: c.entry.ID, Err: fmt.Errorf("running")})
			continue
		}
		delete(f.commands, c.entry.ID)
		out = append(out, cmdman.RemoveResult{ID: c.entry.ID})
	}
	return out, nil
}

func (f *fakeCmdman) logs(_ context.Context, req cmdman.LogsRequest) (logdriver.Reader, error) {
	f.mu.Lock()
	c := f.lookup(req.IDOrName)
	if c == nil {
		f.mu.Unlock()
		return nil, fmt.Errorf("no command %q", req.IDOrName)
	}
	lines := slices.Clone(c.output)
	f.mu.Unlock()
	ch := make(chan logdriver.Record, len(lines))
	for _, l := range lines {
		ch <- logdriver.Record{Line: l}
	}
	close(ch)
	return testLogReader{records: ch}, nil
}
