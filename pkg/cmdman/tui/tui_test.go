package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func kr(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	kTab   = tea.KeyMsg{Type: tea.KeyTab}
	kEnter = tea.KeyMsg{Type: tea.KeyEnter}
	kEsc   = tea.KeyMsg{Type: tea.KeyEsc}
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
				state:   model.EventTypeStarted,
			},
		}},
		{name: "local-dev", workdir: "/work/local-dev", commands: []commandRow{
			{
				id:      "1",
				name:    "watcher",
				project: "local-dev",
				workdir: "/work/local-dev",
				state:   model.EventTypeStarted,
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
		{model.EventTypeStarted, nil, "running"},
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
	// "running" is the display label for started commands: watcher and web.
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
			{id: "9", name: "loose", workdir: "/work/loose", state: model.EventTypeStarted},
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
			State:   model.EventTypeStarted,
		},
		{
			ID:      "3",
			Name:    "web",
			Project: "api-stack",
			Workdir: "/work/api",
			State:   model.EventTypeStarted,
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
	if m.active != tabCompose {
		t.Fatalf("tab should switch to Compose")
	}
	m, _ = upd(m, kTab)
	if m.active != tabCommands {
		t.Fatalf("tab should switch back to Commands")
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
	if c.state != model.EventTypeStarted {
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
	m, _ = upd(m, tea.KeyMsg{Type: tea.KeyLeft}) // toggle to <yes>
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
	m.active = tabCompose
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
