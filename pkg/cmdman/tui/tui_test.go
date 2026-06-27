package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// --- helpers ---------------------------------------------------------------

type fakeBackend struct {
	cmds  []CommandInfo
	projs []ProjectInfo
	cwd   string

	started     []string
	stopped     []string
	restarted   []string
	removed     []string
	removeForce map[string]bool

	logStreams  []*fakeLogStream // one per Logs call
	eventStream *fakeEventStream
	attachIDs   []string
	attachOut   string
	attachErr   error

	muxCycled []string // project names passed to CycleMux
	muxErr    error

	layoutsInfo    LayoutsInfo // info returned by ListLayouts
	layoutsErr     error       // error returned by ListLayouts
	layoutsReq     []string    // project names passed to ListLayouts
	appliedLayouts []string    // layout names passed to ApplyLayout
	applyLayoutErr error       // error returned by ApplyLayout

	definition     string   // text returned by ProjectDefinition
	definitionErr  error    // error returned by ProjectDefinition
	defRequested   []string // project names passed to ProjectDefinition
	composePath    string   // path returned by ComposeFilePath
	composePathErr error    // error returned by ComposeFilePath
	pathRequested  []string // project names passed to ComposeFilePath

	composeUpCalled []string         // project names passed to ComposeUp
	composeUpEvents []ComposeUpEvent // events pre-loaded into the stream
	composeUpErr    error            // error returned by ComposeUp (open failure)
	composeUpStream *fakeComposeUpStream

	rawIDs     []string         // ids passed to RawView
	rawChunks  [][]byte         // chunks pre-loaded into each RawView stream
	rawErr     error            // error returned by RawView (open failure)
	rawStreams []*fakeRawStream // one per RawView call
}

func (f *fakeBackend) ListCommands(context.Context) ([]CommandInfo, error) { return f.cmds, nil }
func (f *fakeBackend) ListProjects(context.Context) ([]ProjectInfo, error) { return f.projs, nil }
func (f *fakeBackend) Cwd() string                                         { return f.cwd }
func (f *fakeBackend) Start(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}
func (f *fakeBackend) Stop(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeBackend) Restart(_ context.Context, id string) error {
	f.restarted = append(f.restarted, id)
	return nil
}
func (f *fakeBackend) Remove(_ context.Context, id string, force bool) error {
	f.removed = append(f.removed, id)
	if f.removeForce == nil {
		f.removeForce = map[string]bool{}
	}
	f.removeForce[id] = force
	return nil
}

func (f *fakeBackend) Events(context.Context) (EventStream, error) {
	if f.eventStream == nil {
		f.eventStream = &fakeEventStream{ch: make(chan EventSignal, 1)}
	}
	return f.eventStream, nil
}

func (f *fakeBackend) Logs(_ context.Context, _ string, _ int) (LogStream, error) {
	ls := &fakeLogStream{ch: make(chan LogLine, 16)}
	f.logStreams = append(f.logStreams, ls)
	return ls, nil
}

func (f *fakeBackend) Attach(_ context.Context, id string) (string, error) {
	f.attachIDs = append(f.attachIDs, id)
	return f.attachOut, f.attachErr
}

func (f *fakeBackend) CycleMux(_ context.Context, projectName, _ string) error {
	f.muxCycled = append(f.muxCycled, projectName)
	return f.muxErr
}

func (f *fakeBackend) ListLayouts(_ context.Context, projectName, _ string) (LayoutsInfo, error) {
	f.layoutsReq = append(f.layoutsReq, projectName)
	return f.layoutsInfo, f.layoutsErr
}

func (f *fakeBackend) ApplyLayout(_ context.Context, _, _, layoutName string) error {
	f.appliedLayouts = append(f.appliedLayouts, layoutName)
	return f.applyLayoutErr
}

func (f *fakeBackend) ProjectDefinition(_ context.Context, projectName, _ string) (string, error) {
	f.defRequested = append(f.defRequested, projectName)
	return f.definition, f.definitionErr
}

func (f *fakeBackend) ComposeFilePath(_ context.Context, projectName, _ string) (string, error) {
	f.pathRequested = append(f.pathRequested, projectName)
	return f.composePath, f.composePathErr
}

func (f *fakeBackend) ComposeUp(_ context.Context, projectName, _ string) (ComposeUpStream, error) {
	f.composeUpCalled = append(f.composeUpCalled, projectName)
	if f.composeUpErr != nil {
		return nil, f.composeUpErr
	}
	s := &fakeComposeUpStream{ch: make(chan ComposeUpEvent, len(f.composeUpEvents))}
	for _, ev := range f.composeUpEvents {
		s.ch <- ev
	}
	f.composeUpStream = s
	return s, nil
}

func (f *fakeBackend) RawView(_ context.Context, id string) (RawStream, error) {
	f.rawIDs = append(f.rawIDs, id)
	if f.rawErr != nil {
		return nil, f.rawErr
	}
	s := newFakeRawStream(len(f.rawChunks) + 1)
	for _, c := range f.rawChunks {
		s.ch <- RawChunk{Bytes: c}
	}
	f.rawStreams = append(f.rawStreams, s)
	return s, nil
}

// fakeRawStream is closed off the update loop (see closeRawAsync), so its closed
// state is mutex-guarded and a closedCh lets a test wait for an async close
// without racing the goroutine.
type fakeRawStream struct {
	ch        chan RawChunk
	closedCh  chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	closed bool
}

func newFakeRawStream(buf int) *fakeRawStream {
	return &fakeRawStream{ch: make(chan RawChunk, buf), closedCh: make(chan struct{})}
}

func (s *fakeRawStream) Chunks() <-chan RawChunk { return s.ch }
func (s *fakeRawStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.ch)
		close(s.closedCh)
	})
	return nil
}

// isClosed reports the close state without racing an async Close.
func (s *fakeRawStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// waitClosed blocks briefly for an async Close, failing the test if it never
// happens (closeRawAsync runs Close in a goroutine).
func (s *fakeRawStream) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closedCh:
	case <-time.After(time.Second):
		t.Fatalf("raw stream was not closed")
	}
}

type fakeComposeUpStream struct {
	ch     chan ComposeUpEvent
	err    error
	closed bool
}

func (s *fakeComposeUpStream) Events() <-chan ComposeUpEvent { return s.ch }
func (s *fakeComposeUpStream) Err() error                    { return s.err }
func (s *fakeComposeUpStream) Close() error {
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	return nil
}

type fakeLogStream struct {
	ch     chan LogLine
	closed bool
}

func (s *fakeLogStream) Lines() <-chan LogLine { return s.ch }
func (s *fakeLogStream) Close() error {
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	return nil
}

type fakeEventStream struct {
	ch     chan EventSignal
	closed bool
}

func (s *fakeEventStream) Signals() <-chan EventSignal { return s.ch }
func (s *fakeEventStream) Close() error {
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	return nil
}

func upd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// drain executes a command, recursively flattening tea.Batch results into the
// leaf messages it produces.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, drain(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func firstActionDone(msgs []tea.Msg) (actionDoneMsg, bool) {
	for _, m := range msgs {
		if d, ok := m.(actionDoneMsg); ok {
			return d, true
		}
	}
	return actionDoneMsg{}, false
}

// selectCmd selects the visible row at idx and marks its command's preview as
// already established, so reconcilePreview is a no-op and the command returned
// by a subsequent key press is the lifecycle action alone (not batched with a
// preview-open command).
func selectCmd(m *Model, idx int) {
	m.commands.selected = idx
	if c, ok := m.commands.selectedCommand(); ok {
		m.commands.preview.cmdID = c.id
	}
}

func kr(s string) tea.KeyMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

var (
	kTab   = tea.KeyPressMsg{Code: tea.KeyTab}
	kEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	kEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

// seed builds a model with two projects; local-dev is the cwd-tied project.
func seed() Model {
	m := New(Options{Backend: &fakeBackend{cwd: "/work/local-dev"}})
	m.cwd = "/work/local-dev"
	m.setGroups([]projectGroup{
		{name: "api-stack", workdir: "/work/api", commands: []commandRow{
			{
				id:      "3",
				name:    "web",
				project: "api-stack",
				workdir: "/work/api",
				state:   model.EventTypeRunning,
			},
		}},
		{name: "local-dev", workdir: "/work/local-dev", commands: []commandRow{
			{
				id:      "1",
				name:    "watcher",
				project: "local-dev",
				workdir: "/work/local-dev",
				state:   model.EventTypeRunning,
			},
			{
				id:      "2",
				name:    "seed-db",
				project: "local-dev",
				workdir: "/work/local-dev",
				state:   model.EventTypeExited,
			},
		}},
	})
	return m
}

// --- tests -----------------------------------------------------------------

func TestActiveProjectSortsFirst(t *testing.T) {
	m := seed()
	if got := m.commands.groups[0].name; got != "local-dev" {
		t.Fatalf("active project should sort first, got %q", got)
	}
	if !m.commands.groups[0].active {
		t.Fatalf("local-dev should be marked active")
	}
	if m.commands.groups[1].active {
		t.Fatalf("api-stack should not be active")
	}
}

func TestDisplayLabels(t *testing.T) {
	zero := 0
	cases := []struct {
		state model.EventType
		code  *int
		want  string
	}{
		{model.EventTypeRunning, nil, "running"},
		{model.EventTypeStarting, nil, "starting"},
		{model.EventTypeCreated, nil, "created"},
		{model.EventTypeExited, nil, "exited"},
		{model.EventTypeExited, &zero, "exited(0)"},
		{model.EventTypeFailed, nil, "failed"},
	}
	for _, c := range cases {
		if got := displayLabel(c.state, c.code); got != c.want {
			t.Errorf("displayLabel(%s) = %q, want %q", c.state, got, c.want)
		}
	}
}

func TestFilteringMatchesAndKeepsGrouping(t *testing.T) {
	m := seed()
	m.commands.filter = "watcher"
	rows := m.commands.visibleRows()
	if len(rows) != 2 {
		t.Fatalf("want 2 visible rows (project + command), got %d", len(rows))
	}
	if rows[0].kind != visProject || m.commands.groups[rows[0].group].name != "local-dev" {
		t.Fatalf("first row should be local-dev project header")
	}
	if rows[1].kind != visCommand ||
		m.commands.groups[rows[1].group].commands[rows[1].cmd].name != "watcher" {
		t.Fatalf("second row should be the watcher command")
	}
}

func TestFilteringMatchesProjectShowsAllChildren(t *testing.T) {
	m := seed()
	m.commands.filter = "local-dev"
	rows := m.commands.visibleRows()
	// project header + its two commands
	if len(rows) != 3 {
		t.Fatalf("project-name match should show all children, got %d rows", len(rows))
	}
}

func TestFilteringMatchesStatusLabel(t *testing.T) {
	m := seed()
	m.commands.filter = "running"
	rows := m.commands.visibleRows()
	// "running" is the display label for running commands: watcher and web.
	cmds := 0
	for _, r := range rows {
		if r.kind == visCommand {
			cmds++
		}
	}
	if cmds != 2 {
		t.Fatalf("status-label filter should match 2 running commands, got %d", cmds)
	}
}

func TestFoldHidesAndRevealsRows(t *testing.T) {
	m := seed()
	// local-dev is groups[0]; fold it.
	m.commands.setFolded(0, true)
	rows := m.commands.visibleRows()
	for _, r := range rows {
		if r.kind == visCommand && r.group == 0 {
			t.Fatalf("folded project should hide its commands")
		}
	}
	m.commands.setFolded(0, false)
	revealed := false
	for _, r := range m.commands.visibleRows() {
		if r.kind == visCommand && r.group == 0 {
			revealed = true
		}
	}
	if !revealed {
		t.Fatalf("unfolded project should reveal its commands")
	}
}

func TestStandaloneCommandsHaveNoGroupHeader(t *testing.T) {
	m := seed()
	// A standalone command carries no compose project name (empty name group).
	m.setGroups(append(m.commands.groups, projectGroup{
		name:    "",
		workdir: "/work/loose",
		commands: []commandRow{
			{id: "9", name: "loose", workdir: "/work/loose", state: model.EventTypeRunning},
		},
	}))
	rows := m.commands.visibleRows()
	var standaloneCmds, standaloneHeaders int
	for _, r := range rows {
		g := m.commands.groups[r.group]
		if g.name != "" {
			continue
		}
		switch r.kind {
		case visProject:
			standaloneHeaders++
		case visCommand:
			standaloneCmds++
		}
	}
	if standaloneHeaders != 0 {
		t.Fatalf("standalone group should not emit a header row, got %d", standaloneHeaders)
	}
	if standaloneCmds != 1 {
		t.Fatalf("standalone command should still be listed, got %d", standaloneCmds)
	}
}

func TestSelectionMovesOnlyAcrossVisibleRows(t *testing.T) {
	m := seed()
	m.commands.setFolded(0, true) // hide local-dev's commands
	// Visible rows: [local-dev(proj), api-stack(proj), web(cmd)] = 3
	m.commands.selected = 0
	for range 10 {
		m.commands.moveSelection(1)
	}
	if m.commands.selected != len(m.commands.visibleRows())-1 {
		t.Fatalf("selection should clamp to last visible row")
	}
}

func TestSelectionPreservedAcrossRefresh(t *testing.T) {
	m := seed()
	// Select the web command (id 3).
	m.selectCommandByID("3")
	sel, ok := m.commands.selectedCommand()
	if !ok || sel.id != "3" {
		t.Fatalf("precondition: web not selected")
	}
	// Reload with the same data in a different order.
	infos := []CommandInfo{
		{
			ID:      "1",
			Name:    "watcher",
			Project: "local-dev",
			Workdir: "/work/local-dev",
			State:   model.EventTypeRunning,
		},
		{
			ID:      "3",
			Name:    "web",
			Project: "api-stack",
			Workdir: "/work/api",
			State:   model.EventTypeRunning,
		},
		{
			ID:      "2",
			Name:    "seed-db",
			Project: "local-dev",
			Workdir: "/work/local-dev",
			State:   model.EventTypeExited,
		},
	}
	m, _ = m.onCommandsLoaded(commandsLoadedMsg{infos: infos})
	got, ok := m.commands.selectedCommand()
	if !ok || got.id != "3" {
		t.Fatalf(
			"selection should be preserved on web (id 3) after refresh, got %+v ok=%v",
			got,
			ok,
		)
	}
}

func TestTabSwitchPreservesTabLocalState(t *testing.T) {
	m := seed()
	m.compose.rows = []composeRow{{name: "tools"}}
	m.commands.filter = "abc"
	m.compose.filter = "xyz"
	m.commands.selected = 1

	m, _ = upd(m, kTab)
	if m.active != TabCompose {
		t.Fatalf("tab should switch to Compose")
	}
	m, _ = upd(m, kTab)
	if m.active != TabLayout {
		t.Fatalf("tab should switch to Layout")
	}
	m, _ = upd(m, kTab)
	if m.active != TabCommands {
		t.Fatalf("tab should wrap back to Commands")
	}
	if m.commands.filter != "abc" || m.compose.filter != "xyz" {
		t.Fatalf("tab-local filters not preserved: %q / %q", m.commands.filter, m.compose.filter)
	}
	if m.commands.selected != 1 {
		t.Fatalf("commands selection not preserved: %d", m.commands.selected)
	}
}

func TestFilterFocusMakesSingleKeysInert(t *testing.T) {
	m := seed()
	fb := m.backend.(*fakeBackend)
	m, _ = upd(m, kr("/")) // focus filter
	if !m.commands.filtering {
		t.Fatalf("filter should be focused after /")
	}
	// Typing 's' and 'q' must edit the filter, not start a command or quit.
	m, _ = upd(m, kr("s"))
	m, cmd := upd(m, kr("q"))
	if m.quitting {
		t.Fatalf("q must not quit while filter is focused")
	}
	if cmd != nil {
		// q while filtering should not return tea.Quit
		if msg := cmd(); msgIsQuit(msg) {
			t.Fatalf("q while filtering should not produce Quit")
		}
	}
	if m.commands.filter != "sq" {
		t.Fatalf("filter should be 'sq', got %q", m.commands.filter)
	}
	if len(fb.started) != 0 {
		t.Fatalf("no start action should have dispatched while filtering")
	}
	// esc leaves filter focus first.
	m, _ = upd(m, kEsc)
	if m.commands.filtering {
		t.Fatalf("esc should leave filter focus")
	}
}

func msgIsQuit(msg tea.Msg) bool {
	// tea.Quit returns a tea.QuitMsg.
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func TestEnterDoesNotToggleLifecycle(t *testing.T) {
	m := seed()
	fb := m.backend.(*fakeBackend)
	// Select a command row (watcher under local-dev: rows[1]).
	selectCmd(&m, 1)
	if _, ok := m.commands.selectedCommand(); !ok {
		t.Fatalf("precondition: a command row should be selected")
	}
	m, cmd := upd(m, kEnter)
	if cmd != nil {
		t.Fatalf("enter on a command row should not dispatch an action")
	}
	if m.popup.open() {
		t.Fatalf("enter on a command row should not open a popup")
	}
	if len(fb.started)+len(fb.stopped)+len(fb.restarted) != 0 {
		t.Fatalf("enter must not perform lifecycle actions")
	}
}

func TestEnterTogglesFoldOnProjectRow(t *testing.T) {
	m := seed()
	m.commands.selected = 0 // local-dev project header
	m, _ = upd(m, kEnter)
	if !m.commands.folded(0) {
		t.Fatalf("enter on a project row should fold it")
	}
	m, _ = upd(m, kEnter)
	if m.commands.folded(0) {
		t.Fatalf("enter on a folded project row should unfold it")
	}
}

func TestAttachConfirmationDefaultsYes(t *testing.T) {
	m := seed()
	m.commands.selected = 1 // a command row
	m, _ = upd(m, kr("a"))
	if m.popup.kind != popupAttach {
		t.Fatalf("a should open the attach popup")
	}
	if !m.popup.confirmed() {
		t.Fatalf("attach popup should default to <yes>")
	}
}

func TestRemoveConfirmationDefaultsCancel(t *testing.T) {
	m := seed()
	// Select seed-db (exited, id 2): rows = [local-dev, watcher, seed-db, api-stack, web]
	m.commands.selected = 2
	c, ok := m.commands.selectedCommand()
	if !ok || c.id != "2" {
		t.Fatalf("precondition: seed-db should be selected, got %+v", c)
	}
	m, _ = upd(m, kr("x"))
	if m.popup.kind != popupRemove {
		t.Fatalf("x on an exited command should open the plain remove popup")
	}
	if m.popup.confirmed() {
		t.Fatalf("remove popup should default to <cancel>")
	}
}

func TestRunningRemoveShowsForceConfirmation(t *testing.T) {
	m := seed()
	m.commands.selected = 1 // watcher, running
	c, _ := m.commands.selectedCommand()
	if c.state != model.EventTypeRunning {
		t.Fatalf("precondition: watcher should be running")
	}
	m, _ = upd(m, kr("x"))
	if m.popup.kind != popupForceRemove {
		t.Fatalf("x on a running command should open the force-remove popup")
	}
	if !strings.Contains(m.popup.title(), "Force remove") {
		t.Fatalf("force popup title should mention force, got %q", m.popup.title())
	}
	if m.popup.actionLabel() != "<force remove>" {
		t.Fatalf("force popup action label should be <force remove>, got %q", m.popup.actionLabel())
	}
}

func TestRemoveRequiresExplicitConfirmation(t *testing.T) {
	m := seed()
	selectCmd(&m, 2) // seed-db, exited
	m, _ = upd(m, kr("x"))
	// Default is cancel; enter cancels without removing.
	m, cmd := upd(m, kEnter)
	if cmd != nil {
		t.Fatalf("confirming the default <cancel> should not dispatch a remove")
	}
	if m.popup.open() {
		t.Fatalf("popup should close after a choice")
	}
	// Reopen, move to the action button, confirm.
	m.commands.selected = 2
	m, _ = upd(m, kr("x"))
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to <yes>
	m, cmd = upd(m, kEnter)
	if cmd == nil {
		t.Fatalf("confirming <yes> should dispatch a remove command")
	}
	done, ok := firstActionDone(drain(cmd))
	if !ok || done.verb != "remove" {
		t.Fatalf("expected a remove actionDoneMsg")
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.removed) != 1 || fb.removed[0] != "2" {
		t.Fatalf("remove should target seed-db (id 2), got %v", fb.removed)
	}
}

func TestStartDispatchesForStoppedCommand(t *testing.T) {
	m := seed()
	selectCmd(&m, 2) // seed-db, exited
	m, cmd := upd(m, kr("s"))
	if cmd == nil {
		t.Fatalf("s on a stopped command should dispatch start")
	}
	if got := m.pendingOf("2"); got != "starting" {
		t.Fatalf("start should set pending marker, got %q", got)
	}
	done, ok := firstActionDone(drain(cmd))
	if !ok || done.verb != "start" {
		t.Fatalf("expected start actionDoneMsg")
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.started) != 1 || fb.started[0] != "2" {
		t.Fatalf("start should target seed-db (id 2), got %v", fb.started)
	}
}

func TestStartIgnoredForRunningCommand(t *testing.T) {
	m := seed()
	selectCmd(&m, 1) // watcher, running
	m, cmd := upd(m, kr("s"))
	if cmd != nil {
		t.Fatalf("s on a running command should not dispatch start")
	}
	if !strings.Contains(m.status, "already running") {
		t.Fatalf("status should explain it is already running, got %q", m.status)
	}
}

func TestStopOnlyForRunningCommand(t *testing.T) {
	m := seed()
	selectCmd(&m, 2) // seed-db, exited
	m, cmd := upd(m, kr("S"))
	if cmd != nil {
		t.Fatalf("S on a stopped command should not dispatch stop")
	}
	selectCmd(&m, 1) // watcher, running
	m, cmd = upd(m, kr("S"))
	if cmd == nil {
		t.Fatalf("S on a running command should dispatch stop")
	}
}

func TestActionDoneClearsPendingAndRefreshes(t *testing.T) {
	m := seed()
	m.setPending("2", "starting")
	m, cmd := m2tuple(m.onActionDone(actionDoneMsg{verb: "start", name: "seed-db", id: "2"}))
	if m.pendingOf("2") != "" {
		t.Fatalf("pending should be cleared after action completion")
	}
	if cmd == nil {
		t.Fatalf("action completion should trigger a refresh")
	}
}

func m2tuple(model tea.Model, cmd tea.Cmd) (Model, tea.Cmd) {
	return model.(Model), cmd
}

func TestHelpOverlayOpensWithTabBindings(t *testing.T) {
	m := seed()
	m, _ = upd(m, kr("?"))
	if !m.helpOpen {
		t.Fatalf("? should open help")
	}
	help := m.renderHelp()
	for _, want := range []string{"start", "stop", "restart", "attach", "remove"} {
		if !strings.Contains(help, want) {
			t.Fatalf("Commands-tab help should list %q binding", want)
		}
	}
	// Switch to compose tab help.
	m.helpOpen = false
	m.active = TabCompose
	m, _ = upd(m, kr("?"))
	composeHelp := m.renderHelp()
	if !strings.Contains(composeHelp, "cycle mux") {
		t.Fatalf("Compose-tab help should mention mux cycling")
	}
	// ? closes help.
	m, _ = upd(m, kr("?"))
	if m.helpOpen {
		t.Fatalf("? should close help")
	}
}

func TestComposeEnterOpensDefinitionViewer(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}
	fb := m.backend.(*fakeBackend)
	fb.definition = "name: tools\ncommands:\n  a:\n    args: [echo, a]\n"

	m, cmd := upd(m, kEnter)
	if !m.defViewer.open {
		t.Fatalf("enter on the Compose tab should open the definition viewer")
	}
	if m.defViewer.project != "tools" {
		t.Fatalf("viewer should target the selected project, got %q", m.defViewer.project)
	}

	var loaded defLoadedMsg
	found := false
	for _, mm := range drain(cmd) {
		if d, ok := mm.(defLoadedMsg); ok {
			loaded, found = d, true
		}
	}
	if !found {
		t.Fatalf("enter should dispatch a definition-load command")
	}
	if len(fb.defRequested) != 1 || fb.defRequested[0] != "tools" {
		t.Fatalf("ProjectDefinition should be requested for tools, got %v", fb.defRequested)
	}
	m, _ = upd(m, loaded)
	if m.defViewer.loading {
		t.Fatalf("viewer should stop loading once the definition arrives")
	}
	if len(m.defViewer.lines) == 0 {
		t.Fatalf("viewer should hold the loaded definition lines")
	}
	out := m.renderDefViewer()
	if !strings.Contains(out, "name: tools") {
		t.Fatalf("rendered viewer should show the raw YAML, got:\n%s", out)
	}

	m, _ = upd(m, kEsc)
	if m.defViewer.open {
		t.Fatalf("esc should close the definition viewer")
	}
}

func TestDefViewerScrollAndClose(t *testing.T) {
	m := seed()
	m.width, m.height = 80, 24
	m.active = TabCompose
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	m.defViewer = defViewerState{open: true, project: "tools", lines: lines}

	m, _ = upd(m, kr("j"))
	if m.defViewer.scroll != 1 {
		t.Fatalf("j should scroll down by one, got %d", m.defViewer.scroll)
	}
	m, _ = upd(m, kr("k"))
	if m.defViewer.scroll != 0 {
		t.Fatalf("k should scroll back to the top, got %d", m.defViewer.scroll)
	}
	page := m.defViewerPage()
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.defViewer.scroll != page {
		t.Fatalf("pgdown should scroll one page (%d), got %d", page, m.defViewer.scroll)
	}
	// Scrolling cannot run past the final screenful.
	for range 10 {
		m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if want := len(lines) - page; m.defViewer.scroll != want {
		t.Fatalf("scroll should clamp to %d, got %d", want, m.defViewer.scroll)
	}

	m, _ = upd(m, kr("q"))
	if m.defViewer.open {
		t.Fatalf("q should close the definition viewer")
	}
}

func TestComposeEditResolvesPathAndHandsOff(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}
	fb := m.backend.(*fakeBackend)
	fb.composePath = "/etc/compose/tools.yaml"

	m, cmd := upd(m, kr("e"))
	var pathMsg editPathMsg
	found := false
	for _, mm := range drain(cmd) {
		if p, ok := mm.(editPathMsg); ok {
			pathMsg, found = p, true
		}
	}
	if !found {
		t.Fatalf("e should dispatch a path-resolve command")
	}
	if len(fb.pathRequested) != 1 || fb.pathRequested[0] != "tools" {
		t.Fatalf("e should resolve the compose path for tools, got %v", fb.pathRequested)
	}
	if pathMsg.path != "/etc/compose/tools.yaml" {
		t.Fatalf("resolved edit path = %q, want the compose file", pathMsg.path)
	}
	// onEditPath builds the editor handoff; assert it produces a command without
	// running a real editor.
	_, execCmd := upd(m, pathMsg)
	if execCmd == nil {
		t.Fatalf("a resolved edit path should produce an editor handoff command")
	}
	// A finished editor handoff reloads projects.
	_, doneCmd := upd(m, editDoneMsg{project: "tools"})
	if doneCmd == nil {
		t.Fatalf("editDoneMsg should trigger a refresh")
	}
}

func TestComposeEditPathErrorSurfacesStatus(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools"}}
	m, execCmd := upd(m, editPathMsg{project: "tools", err: fmt.Errorf("boom")})
	if execCmd != nil {
		t.Fatalf("an unresolved edit path should not hand off to an editor")
	}
	if !strings.Contains(m.status, "boom") {
		t.Fatalf("path-resolve error should surface in the status, got %q", m.status)
	}
}

func TestComposeUpOpensConfirmation(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}

	m, cmd := upd(m, kr("a"))
	if cmd != nil {
		t.Fatalf("a should only open a popup, not dispatch a command")
	}
	if m.popup.kind != popupComposeUp {
		t.Fatalf("a on the Compose tab should open the compose-up popup, got %v", m.popup.kind)
	}
	if !m.popup.confirmed() {
		t.Fatalf("compose-up popup should default to the action button (<up>)")
	}
	if m.composeUp.active {
		t.Fatalf("the overlay should not be active until the run is confirmed")
	}
}

func TestComposeUpConfirmRunsAndOverlayCollapses(t *testing.T) {
	m := seed()
	m.width, m.height = 80, 24
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}
	fb := m.backend.(*fakeBackend)
	zero := 0
	fb.composeUpEvents = []ComposeUpEvent{
		{Command: "web", Phase: "creating"},
		{Command: "web", Phase: "running", Terminal: true},
		{Command: "db", Phase: "exited", Terminal: true, ExitCode: &zero},
	}

	// a → confirm popup; enter confirms (default is the action button).
	m, _ = upd(m, kr("a"))
	m, cmd := upd(m, kEnter)

	var opened composeUpOpenedMsg
	found := false
	for _, mm := range drain(cmd) {
		if o, ok := mm.(composeUpOpenedMsg); ok {
			opened, found = o, true
		}
	}
	if !found {
		t.Fatalf("confirming compose up should dispatch a ComposeUp command")
	}
	if len(fb.composeUpCalled) != 1 || fb.composeUpCalled[0] != "tools" {
		t.Fatalf("ComposeUp should target tools, got %v", fb.composeUpCalled)
	}

	// Opening the stream activates the live overlay.
	m, _ = upd(m, opened)
	if !m.composeUp.active {
		t.Fatalf("a successful ComposeUp open should activate the progress overlay")
	}

	// Drive the live stream: each buffered event updates the per-service marks.
	for range fb.composeUpEvents {
		ev, ok := waitComposeUpCmd(m.composeUp.stream, "tools")().(composeUpEventMsg)
		if !ok {
			t.Fatalf("expected a composeUpEventMsg from the stream")
		}
		m, _ = upd(m, ev)
	}
	if len(m.composeUp.order) != 2 {
		t.Fatalf("overlay should track 2 services (web, db), got %v", m.composeUp.order)
	}
	if mk := m.composeUp.marks["web"]; mk.phase != "running" || !mk.terminal {
		t.Fatalf("web should be marked terminal/running, got %+v", mk)
	}
	out := m.renderComposeUp()
	if !strings.Contains(out, "web") || !strings.Contains(out, "running") {
		t.Fatalf("overlay should render the web/running mark, got:\n%s", out)
	}

	// The operation's terminal phase (channel close) collapses to a footer summary.
	_ = m.composeUp.stream.Close()
	done, ok := waitComposeUpCmd(m.composeUp.stream, "tools")().(composeUpDoneMsg)
	if !ok {
		t.Fatalf("a closed stream should yield a composeUpDoneMsg")
	}
	m, _ = upd(m, done)
	if m.composeUp.active {
		t.Fatalf("the overlay should collapse on the terminal phase")
	}
	if !strings.Contains(m.status, "compose up tools") {
		t.Fatalf("the footer summary should mention the project, got %q", m.status)
	}
}

func TestComposeUpOpenErrorSurfacesStatus(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools"}}

	m, _ = upd(m, composeUpOpenedMsg{project: "tools", err: fmt.Errorf("boom")})
	if m.composeUp.active {
		t.Fatalf("a failed open should not activate the overlay")
	}
	if !strings.Contains(m.status, "boom") {
		t.Fatalf("compose-up open error should surface in the status, got %q", m.status)
	}
}

func TestTabBarRendersThreeTabs(t *testing.T) {
	m := seed()
	bar := m.renderTabBar()
	for _, name := range []string{"Commands", "Compose", "Layout"} {
		if !strings.Contains(bar, name) {
			t.Fatalf("tab bar should render the %q tab, got %q", name, bar)
		}
	}
}

func TestLayoutTabDefaultSelectionIsMarker(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}, PopupMode: true})
	m.active = TabLayout
	info := LayoutsInfo{
		Project: "tools", Path: "/c.yaml",
		Names: []string{"dev", "ops", "full"}, Current: 1,
	}
	m, _ = upd(m, layoutsLoadedMsg{info: info})
	if len(m.layout.rows) != 3 {
		t.Fatalf("want 3 layout rows, got %d", len(m.layout.rows))
	}
	if m.layout.selected != 1 {
		t.Fatalf("default selection should be the current marker (1), got %d", m.layout.selected)
	}
	if m.layout.current != 1 {
		t.Fatalf("current marker should be recorded as 1, got %d", m.layout.current)
	}
	out := m.renderLayoutBody(40, 10)
	for _, name := range []string{"dev", "ops", "full"} {
		if !strings.Contains(out, name) {
			t.Fatalf("layout body should list %q, got:\n%s", name, out)
		}
	}
}

func TestLayoutTabNoMarkerSelectsFirst(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}, PopupMode: true})
	m.active = TabLayout
	// No running dashboard: Current == -1 should land the selection on the first.
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{Names: []string{"a", "b"}, Current: -1}})
	if m.layout.selected != 0 {
		t.Fatalf("with no marker the selection should default to 0, got %d", m.layout.selected)
	}
}

func TestLayoutTabNavigation(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}, PopupMode: true})
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{Names: []string{"a", "b", "c"}, Current: 0}})
	m, _ = upd(m, kr("j"))
	if m.layout.selected != 1 {
		t.Fatalf("j should move the selection down, got %d", m.layout.selected)
	}
	m, _ = upd(m, kr("k"))
	if m.layout.selected != 0 {
		t.Fatalf("k should move the selection up, got %d", m.layout.selected)
	}
	// Clamp at the top.
	m, _ = upd(m, kr("k"))
	if m.layout.selected != 0 {
		t.Fatalf("k at the top should clamp to 0, got %d", m.layout.selected)
	}
}

func TestLayoutTabEnterAppliesInPopupMode(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}, PopupMode: true})
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{
		Project: "tools", Path: "/c.yaml", Names: []string{"dev", "ops"}, Current: 0,
	}})
	m, _ = upd(m, kr("j")) // select "ops"

	m, cmd := upd(m, kEnter)
	if m.popup.open() {
		t.Fatalf("popup mode should apply immediately, not open a warning popup")
	}
	done, ok := firstMsg[layoutDoneMsg](drain(cmd))
	if !ok {
		t.Fatalf("enter should dispatch an ApplyLayout command")
	}
	if done.layout != "ops" {
		t.Fatalf("layoutDoneMsg should report the applied layout, got %q", done.layout)
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.appliedLayouts) != 1 || fb.appliedLayouts[0] != "ops" {
		t.Fatalf("ApplyLayout should target the selected layout, got %v", fb.appliedLayouts)
	}
}

func TestLayoutTabEnterWarnsInDirectMode(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}}) // direct mode: PopupMode false
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{
		Project: "tools", Path: "/c.yaml", Names: []string{"dev", "ops"}, Current: 0,
	}})

	m, _ = upd(m, kEnter)
	if m.popup.kind != popupMuxWarn {
		t.Fatalf("direct mode should open the mux warning popup, got %v", m.popup.kind)
	}
	if m.popup.layout != "dev" {
		t.Fatalf("the warning popup should carry the selected layout, got %q", m.popup.layout)
	}
	if m.popup.confirmed() {
		t.Fatalf("the warning popup should default to <cancel>")
	}
	fb := m.backend.(*fakeBackend)
	if len(fb.appliedLayouts) != 0 {
		t.Fatalf("apply must wait for confirmation, got %v", fb.appliedLayouts)
	}

	// Toggle to the action button and confirm.
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m, cmd := upd(m, kEnter)
	if _, ok := firstMsg[layoutDoneMsg](drain(cmd)); !ok {
		t.Fatalf("confirming the warning should dispatch ApplyLayout")
	}
	if len(fb.appliedLayouts) != 1 || fb.appliedLayouts[0] != "dev" {
		t.Fatalf("ApplyLayout should target the selected layout after confirm, got %v",
			fb.appliedLayouts)
	}
}

func TestLayoutTabEntryLoadsLayouts(t *testing.T) {
	m := seed()
	fb := m.backend.(*fakeBackend)
	fb.layoutsInfo = LayoutsInfo{Project: "local-dev", Names: []string{"solo"}, Current: 0}
	// Switch Commands -> Compose -> Layout; entering Layout dispatches a load.
	m, _ = upd(m, kTab) // Compose
	m, cmd := upd(m, kTab)
	if m.active != TabLayout {
		t.Fatalf("precondition: should be on the Layout tab")
	}
	if _, ok := firstMsg[layoutsLoadedMsg](drain(cmd)); !ok {
		t.Fatalf("entering the Layout tab should dispatch a ListLayouts load")
	}
	if len(fb.layoutsReq) == 0 {
		t.Fatalf("ListLayouts should have been requested on tab entry")
	}
}

func TestResolveEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := resolveEditor(); got != "vim" {
		t.Fatalf("resolveEditor fallback should be vim, got %q", got)
	}
	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); got != "nano" {
		t.Fatalf("$EDITOR should win over the fallback, got %q", got)
	}
	t.Setenv("VISUAL", "emacs")
	if got := resolveEditor(); got != "emacs" {
		t.Fatalf("$VISUAL should win over $EDITOR, got %q", got)
	}
}

func TestComposeFilterMatchesNameAndPath(t *testing.T) {
	r := composeRow{
		name:     "tools",
		path:     "/etc/cmdman/compose/tools.yaml",
		modified: "modified 2026-05-20",
	}
	if !composeRowMatches("tools", r) {
		t.Errorf("should match by name")
	}
	if !composeRowMatches("compose/tools", r) {
		t.Errorf("should match by path")
	}
	if !composeRowMatches("2026", r) {
		t.Errorf("should match by metadata")
	}
	if composeRowMatches("zzz", r) {
		t.Errorf("should not match unrelated text")
	}
}

func TestNoneLogDriverPreviewState(t *testing.T) {
	// The preview no-storage state is selected for the none log driver. This
	// verifies the render path produces the documented message.
	m := seed()
	m.commands.preview = previewState{status: previewNoStorage}
	out := m.renderPreview(40, 6)
	if !strings.Contains(out, "No log storage configured") {
		t.Fatalf("none-driver preview should show the no-storage state, got:\n%s", out)
	}
	_ = logdriver.DriverNone
}

// firstMsg returns the first message of type T produced by a drained command.
func firstMsg[T tea.Msg](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if t, ok := m.(T); ok {
			return t, true
		}
	}
	var zero T
	return zero, false
}

// termModel builds a single-group model (no project header) with the given rows
// and a fakeBackend, sized to a usable preview pane.
func termModel(rows ...commandRow) Model {
	m := New(Options{Backend: &fakeBackend{}})
	m.width, m.height = 80, 24
	m.active = TabCommands
	m.setGroups([]projectGroup{{name: "", workdir: "/w", commands: rows}})
	return m
}

func TestPreviewTerminalViewRendersRawStream(t *testing.T) {
	m := termModel(commandRow{
		id: "1", name: "shell", workdir: "/w", state: model.EventTypeRunning, tty: true,
	})
	m.commands.selected = 0
	fb := m.backend.(*fakeBackend)
	fb.rawChunks = [][]byte{[]byte("hello-term")}

	openCmd := (&m).reconcilePreview()
	if openCmd == nil {
		t.Fatalf("selecting a running tty command should open a raw stream")
	}
	if !m.commands.preview.terminal {
		t.Fatalf("a running tty command should select terminal-view mode")
	}
	opened, ok := firstMsg[rawOpenedMsg](drain(openCmd))
	if !ok {
		t.Fatalf("reconcile should dispatch a RawView open")
	}
	if len(fb.rawIDs) != 1 || fb.rawIDs[0] != "1" {
		t.Fatalf("RawView should target the running tty command, got %v", fb.rawIDs)
	}
	if len(fb.logStreams) != 0 {
		t.Fatalf("terminal-view must not fall back to the log reader")
	}

	m, _ = upd(m, opened)
	if m.commands.preview.term == nil {
		t.Fatalf("opening the raw stream should create the emulator")
	}
	if !m.commands.preview.streaming {
		t.Fatalf("opening the raw stream should mark the preview as streaming")
	}

	// The background drain writes chunk bytes straight into the shared emulator
	// (they never travel through the message loop). Close the stream so the drain
	// loop finishes after consuming the buffered chunk.
	stream := fb.rawStreams[0]
	_ = stream.Close()
	closed, ok := drainRawCmd(
		m.commands.preview.term, stream, "1", m.commands.preview.gen,
	)().(rawClosedMsg)
	if !ok {
		t.Fatalf("the drain should report a rawClosedMsg when the stream ends")
	}
	if closed.cmdID != "1" || closed.err != nil {
		t.Fatalf("rawClosedMsg should carry the cmdID and no error, got %+v", closed)
	}

	out := m.renderPreview(40, 12)
	if !strings.Contains(out, "hello-term") {
		t.Fatalf("emulator frame should render the raw bytes, got:\n%s", out)
	}
}

func TestPreviewPredicateSelectsFallback(t *testing.T) {
	cases := []struct {
		name string
		row  commandRow
	}{
		{"running non-tty", commandRow{
			id: "1", name: "svc", workdir: "/w",
			state: model.EventTypeRunning, logDriver: logdriver.DriverK8sFile,
		}},
		{"exited tty", commandRow{
			id: "1", name: "job", workdir: "/w",
			state: model.EventTypeExited, logDriver: logdriver.DriverK8sFile, tty: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := termModel(c.row)
			m.commands.selected = 0
			fb := m.backend.(*fakeBackend)

			cmd := (&m).reconcilePreview()
			if m.commands.preview.terminal {
				t.Fatalf("%s must not use terminal-view mode", c.name)
			}
			if _, ok := firstMsg[previewOpenedMsg](drain(cmd)); !ok {
				t.Fatalf("%s should open the sanitized log reader", c.name)
			}
			if len(fb.rawIDs) != 0 {
				t.Fatalf("%s must not open a raw stream, got %v", c.name, fb.rawIDs)
			}
		})
	}
}

func TestPreviewTerminalStreamClosesOnSelectionChange(t *testing.T) {
	m := termModel(
		commandRow{id: "1", name: "a", workdir: "/w", state: model.EventTypeRunning, tty: true},
		commandRow{id: "2", name: "b", workdir: "/w", state: model.EventTypeRunning, tty: true},
	)
	fb := m.backend.(*fakeBackend)
	m.commands.selected = 0

	opened, ok := firstMsg[rawOpenedMsg](drain((&m).reconcilePreview()))
	if !ok {
		t.Fatalf("the first selection should open a raw stream")
	}
	m, _ = upd(m, opened)
	if m.commands.preview.raw == nil {
		t.Fatalf("the first selection should hold a live raw stream")
	}
	if len(fb.rawStreams) != 1 {
		t.Fatalf("expected one raw stream opened, got %d", len(fb.rawStreams))
	}

	// Moving the selection must close the previous raw stream. stopPreview closes
	// it off the update loop, so wait briefly for the async close.
	m.commands.selected = 1
	_ = (&m).reconcilePreview()
	fb.rawStreams[0].waitClosed(t)
}

func TestPreviewTerminalEmulatorSizedToPTYNotPane(t *testing.T) {
	m := termModel(commandRow{
		id: "1", name: "shell", workdir: "/w", state: model.EventTypeRunning, tty: true,
	})
	m.commands.selected = 0

	opened, ok := firstMsg[rawOpenedMsg](drain((&m).reconcilePreview()))
	if !ok {
		t.Fatalf("selecting a running tty command should open a raw stream")
	}
	m, _ = upd(m, opened)
	term := m.commands.preview.term
	if term == nil {
		t.Fatalf("opening the raw stream should create the emulator")
	}
	// The emulator opens at the default size; the command's real PTY size arrives
	// as a resize chunk over the raw stream (D9: the remote PTY is never touched).
	if term.Width() != defaultPreviewCols || term.Height() != defaultPreviewRows {
		t.Fatalf("emulator should open at the default size %dx%d, got %dx%d",
			defaultPreviewCols, defaultPreviewRows, term.Width(), term.Height())
	}

	// A window resize must not touch the emulator: it is sized to the PTY, not the
	// pane, and the preview crops it on render.
	m, _ = upd(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if term.Width() != defaultPreviewCols || term.Height() != defaultPreviewRows {
		t.Fatalf("a window resize must not resize the PTY-sized emulator, got %dx%d",
			term.Width(), term.Height())
	}
}
