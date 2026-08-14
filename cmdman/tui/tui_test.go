package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"

	tea "charm.land/bubbletea/v2"

	"github.com/mattn/go-runewidth"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// --- helpers ---------------------------------------------------------------

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
		m.commands.preview.cmdID = c.ID
	}
}

var (
	kTab = tea.KeyPressMsg{Code: tea.KeyTab}
	kEsc = tea.KeyPressMsg{Code: tea.KeyEscape}
)

// seed builds a model with two projects; local-dev is the cwd-tied project.
func seed() Model {
	m := New(core.Options{Backend: &coretest.FakeBackend{Dir: "/work/local-dev"}})
	m.cwd = "/work/local-dev"
	m.setGroups([]core.ProjectGroup{
		{Name: "api-stack", Workdir: "/work/api", Commands: []core.CommandRow{
			{
				ID:      "3",
				Name:    "web",
				Project: "api-stack",
				Workdir: "/work/api",
				State:   model.EventTypeRunning,
			},
		}},
		{Name: "local-dev", Workdir: "/work/local-dev", Commands: []core.CommandRow{
			{
				ID:      "1",
				Name:    "watcher",
				Project: "local-dev",
				Workdir: "/work/local-dev",
				State:   model.EventTypeRunning,
			},
			{
				ID:      "2",
				Name:    "seed-db",
				Project: "local-dev",
				Workdir: "/work/local-dev",
				State:   model.EventTypeExited,
			},
		}},
	})
	return m
}

// --- tests -----------------------------------------------------------------

func TestActiveProjectSortsFirst(t *testing.T) {
	m := seed()
	if got := m.commands.groups[0].Name; got != "local-dev" {
		t.Fatalf("active project should sort first, got %q", got)
	}
	if !m.commands.groups[0].Active {
		t.Fatalf("local-dev should be marked active")
	}
	if m.commands.groups[1].Active {
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
		if got := core.DisplayLabel(c.state, c.code); got != c.want {
			t.Errorf("core.DisplayLabel(%s) = %q, want %q", c.state, got, c.want)
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
	if rows[0].kind != visProject || m.commands.groups[rows[0].group].Name != "local-dev" {
		t.Fatalf("first row should be local-dev project header")
	}
	if rows[1].kind != visCommand ||
		m.commands.groups[rows[1].group].Commands[rows[1].cmd].Name != "watcher" {
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
	m.setGroups(append(m.commands.groups, core.ProjectGroup{
		Name:    "",
		Workdir: "/work/loose",
		Commands: []core.CommandRow{
			{ID: "9", Name: "loose", Workdir: "/work/loose", State: model.EventTypeRunning},
		},
	}))
	rows := m.commands.visibleRows()
	var standaloneCmds, standaloneHeaders int
	for _, r := range rows {
		g := m.commands.groups[r.group]
		if g.Name != "" {
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
	if !ok || sel.ID != "3" {
		t.Fatalf("precondition: web not selected")
	}
	// Reload with the same data in a different order.
	infos := []core.CommandInfo{
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
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: infos})
	got, ok := m.commands.selectedCommand()
	if !ok || got.ID != "3" {
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
	fb := m.backend.(*coretest.FakeBackend)
	m, _ = upd(m, coretest.Kr("/")) // focus filter
	if !m.commands.filtering {
		t.Fatalf("filter should be focused after /")
	}
	// Typing 's' and 'q' must edit the filter, not start a command or quit.
	m, _ = upd(m, coretest.Kr("s"))
	m, cmd := upd(m, coretest.Kr("q"))
	if m.quitting {
		t.Fatalf("q must not quit while filter is focused")
	}
	if cmd != nil {
		// q while filtering should not return tea.Quit
		if msg := cmd(); coretest.MsgIsQuit(msg) {
			t.Fatalf("q while filtering should not produce Quit")
		}
	}
	if m.commands.filter != "sq" {
		t.Fatalf("filter should be 'sq', got %q", m.commands.filter)
	}
	if len(fb.Started) != 0 {
		t.Fatalf("no start action should have dispatched while filtering")
	}
	// esc leaves filter focus first.
	m, _ = upd(m, kEsc)
	if m.commands.filtering {
		t.Fatalf("esc should leave filter focus")
	}
}

func TestEnterDoesNotToggleLifecycle(t *testing.T) {
	m := seed()
	fb := m.backend.(*coretest.FakeBackend)
	// Select a command row (watcher under local-dev: rows[1]).
	selectCmd(&m, 1)
	if _, ok := m.commands.selectedCommand(); !ok {
		t.Fatalf("precondition: a command row should be selected")
	}
	m, cmd := upd(m, coretest.KEnter)
	if cmd != nil {
		t.Fatalf("enter on a command row should not dispatch an action")
	}
	if m.popup.open() {
		t.Fatalf("enter on a command row should not open a popup")
	}
	if len(fb.Started)+len(fb.Stopped)+len(fb.Restarted) != 0 {
		t.Fatalf("enter must not perform lifecycle actions")
	}
}

func TestEnterTogglesFoldOnProjectRow(t *testing.T) {
	m := seed()
	m.commands.selected = 0 // local-dev project header
	m, _ = upd(m, coretest.KEnter)
	if !m.commands.folded(0) {
		t.Fatalf("enter on a project row should fold it")
	}
	m, _ = upd(m, coretest.KEnter)
	if m.commands.folded(0) {
		t.Fatalf("enter on a folded project row should unfold it")
	}
}

func TestAttachConfirmationDefaultsYes(t *testing.T) {
	m := seed()
	m.commands.selected = 1 // a command row
	m, _ = upd(m, coretest.Kr("a"))
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
	if !ok || c.ID != "2" {
		t.Fatalf("precondition: seed-db should be selected, got %+v", c)
	}
	m, _ = upd(m, coretest.Kr("x"))
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
	if c.State != model.EventTypeRunning {
		t.Fatalf("precondition: watcher should be running")
	}
	m, _ = upd(m, coretest.Kr("x"))
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
	m, _ = upd(m, coretest.Kr("x"))
	// Default is cancel; enter cancels without removing.
	m, cmd := upd(m, coretest.KEnter)
	if cmd != nil {
		t.Fatalf("confirming the default <cancel> should not dispatch a remove")
	}
	if m.popup.open() {
		t.Fatalf("popup should close after a choice")
	}
	// Reopen, move to the action button, confirm.
	m.commands.selected = 2
	m, _ = upd(m, coretest.Kr("x"))
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to <yes>
	m, cmd = upd(m, coretest.KEnter)
	if cmd == nil {
		t.Fatalf("confirming <yes> should dispatch a remove command")
	}
	done, ok := firstActionDone(drain(cmd))
	if !ok || done.verb != "remove" {
		t.Fatalf("expected a remove actionDoneMsg")
	}
	fb := m.backend.(*coretest.FakeBackend)
	if len(fb.Removed) != 1 || fb.Removed[0] != "2" {
		t.Fatalf("remove should target seed-db (id 2), got %v", fb.Removed)
	}
}

func TestStartDispatchesForStoppedCommand(t *testing.T) {
	m := seed()
	selectCmd(&m, 2) // seed-db, exited
	m, cmd := upd(m, coretest.Kr("s"))
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
	fb := m.backend.(*coretest.FakeBackend)
	if len(fb.Started) != 1 || fb.Started[0] != "2" {
		t.Fatalf("start should target seed-db (id 2), got %v", fb.Started)
	}
}

func TestStartIgnoredForRunningCommand(t *testing.T) {
	m := seed()
	selectCmd(&m, 1) // watcher, running
	m, cmd := upd(m, coretest.Kr("s"))
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
	m, cmd := upd(m, coretest.Kr("S"))
	if cmd != nil {
		t.Fatalf("S on a stopped command should not dispatch stop")
	}
	selectCmd(&m, 1) // watcher, running
	m, cmd = upd(m, coretest.Kr("S"))
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
	m, _ = upd(m, coretest.Kr("?"))
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
	m, _ = upd(m, coretest.Kr("?"))
	composeHelp := m.renderHelp()
	if !strings.Contains(composeHelp, "cycle mux") {
		t.Fatalf("Compose-tab help should mention mux cycling")
	}
	// ? closes help.
	m, _ = upd(m, coretest.Kr("?"))
	if m.helpOpen {
		t.Fatalf("? should close help")
	}
}

func TestComposeEnterOpensDefinitionViewer(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}
	fb := m.backend.(*coretest.FakeBackend)
	fb.Definition = "name: tools\ncommands:\n  a:\n    args: [echo, a]\n"

	m, cmd := upd(m, coretest.KEnter)
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
	if len(fb.DefRequested) != 1 || fb.DefRequested[0] != "tools" {
		t.Fatalf("ProjectDefinition should be requested for tools, got %v", fb.DefRequested)
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

	m, _ = upd(m, coretest.Kr("j"))
	if m.defViewer.scroll != 1 {
		t.Fatalf("j should scroll down by one, got %d", m.defViewer.scroll)
	}
	m, _ = upd(m, coretest.Kr("k"))
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

	m, _ = upd(m, coretest.Kr("q"))
	if m.defViewer.open {
		t.Fatalf("q should close the definition viewer")
	}
}

func TestComposeEditResolvesPathAndHandsOff(t *testing.T) {
	m := seed()
	m.active = TabCompose
	m.compose.rows = []composeRow{{name: "tools", path: "/etc/compose/tools.yaml"}}
	fb := m.backend.(*coretest.FakeBackend)
	fb.ComposePath = "/etc/compose/tools.yaml"

	m, cmd := upd(m, coretest.Kr("e"))
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
	if len(fb.PathRequested) != 1 || fb.PathRequested[0] != "tools" {
		t.Fatalf("e should resolve the compose path for tools, got %v", fb.PathRequested)
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

	m, cmd := upd(m, coretest.Kr("a"))
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
	fb := m.backend.(*coretest.FakeBackend)
	zero := 0
	fb.ComposeUpEvents = []ComposeUpEvent{
		{Command: "web", Phase: "creating"},
		{Command: "web", Phase: "running", Terminal: true},
		{Command: "db", Phase: "exited", Terminal: true, ExitCode: &zero},
	}

	// a → confirm popup; enter confirms (default is the action button).
	m, _ = upd(m, coretest.Kr("a"))
	m, cmd := upd(m, coretest.KEnter)

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
	if len(fb.ComposeUpCalled) != 1 || fb.ComposeUpCalled[0] != "tools" {
		t.Fatalf("ComposeUp should target tools, got %v", fb.ComposeUpCalled)
	}

	// Opening the stream activates the live overlay.
	m, _ = upd(m, opened)
	if !m.composeUp.active {
		t.Fatalf("a successful ComposeUp open should activate the progress overlay")
	}

	// Drive the live stream: each buffered event updates the per-service marks.
	for range fb.ComposeUpEvents {
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

func TestTopBarShowsCwd(t *testing.T) {
	m := seed() // cwd "/work/local-dev"
	out := core.StripANSI(m.renderTopBar(80))
	if !strings.Contains(out, "cmdman tui") {
		t.Fatalf("top bar should keep the title, got %q", out)
	}
	if !strings.Contains(out, "/work/local-dev") {
		t.Fatalf("top bar should show the cwd, got %q", out)
	}
	if w := runewidth.StringWidth(out); w > 80 {
		t.Fatalf("top bar must not exceed the width, got %d cells: %q", w, out)
	}
}

func TestTopBarOmitsCwdWhenUnknown(t *testing.T) {
	m := seed()
	m.cwd = ""
	out := core.StripANSI(m.renderTopBar(80))
	if strings.Contains(out, "cwd:") {
		t.Fatalf("top bar should omit the cwd label when the cwd is unknown, got %q", out)
	}
	if !strings.Contains(out, "cmdman tui") {
		t.Fatalf("top bar should still render the title, got %q", out)
	}
}

func TestTopBarLeftTruncatesLongCwdKeepingLeaf(t *testing.T) {
	m := seed()
	m.cwd = "/very/long/path/that/does/not/fit/into/a/narrow/terminal/leafdir"
	out := core.StripANSI(m.renderTopBar(40))
	if !strings.Contains(out, "leafdir") {
		t.Fatalf("a truncated cwd should keep its leaf visible, got %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("a truncated cwd should be marked with an ellipsis, got %q", out)
	}
	if w := runewidth.StringWidth(out); w > 40 {
		t.Fatalf("truncated top bar must fit the width, got %d cells: %q", w, out)
	}
}

func TestProjectHeaderShowsWorkdir(t *testing.T) {
	m := seed()
	out := core.StripANSI(m.renderCommandList("Commands", 60, 12))
	if !strings.Contains(out, "/work/api") {
		t.Fatalf("a compose project header should show the project workdir, got:\n%s", out)
	}
}

// TestCommandsTabMarkerSlotIsFixedWidth pins the Commands tab to the same marker
// slot the switcher and the statusbar use: 🔔 is two cells and ●/○ one, so the
// project names have to start at the same column whichever marker their row
// carries.
func TestCommandsTabMarkerSlotIsFixedWidth(t *testing.T) {
	m := seed()
	groups := m.commands.groups
	// One project with an unread bell, one without: the two marker widths.
	groups[0].Commands[0].Bell = true
	m.setGroups(groups)

	out := core.StripANSI(m.renderCommandList("Commands", 70, 12))
	nameColumn := func(name string) int {
		for line := range strings.SplitSeq(out, "\n") {
			if before, _, ok := strings.Cut(line, name); ok {
				return core.Cells.StringWidth(before)
			}
		}
		t.Fatalf("no row names %q:\n%s", name, out)
		return -1
	}
	belled, plain := nameColumn("local-dev"), nameColumn("api-stack")
	if belled != plain {
		t.Errorf("a belled project starts its name at column %d and a plain one at %d:\n%s",
			belled, plain, out)
	}
	if !strings.Contains(out, core.GlyphBell) {
		t.Fatalf("the belled project should carry the bell; the test proves nothing:\n%s", out)
	}
}

// TestCommandRowShowsScaleBadge covers D44 on the Commands tab: a command that
// is one replica among several says which one, next to its state where the
// pane's own truncation reaches last, and an unscaled row is left as it was.
func TestCommandRowShowsScaleBadge(t *testing.T) {
	m := seed()
	groups := m.commands.groups
	groups[0].Commands[0].ScaleIndex, groups[0].Commands[0].ScaleCount = 2, 3
	m.setGroups(groups)

	out := core.StripANSI(m.renderCommandList("Commands", 80, 12))
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(line, "watcher"):
			if !strings.Contains(line, "[2]") {
				t.Errorf("a replica's row should say which one it is: %q", line)
			}
		case strings.Contains(line, "seed-db"):
			if strings.Contains(line, "[") {
				t.Errorf("an unscaled command's row should carry no badge: %q", line)
			}
		}
	}
}

func TestStandaloneCommandShowsWorkdir(t *testing.T) {
	m := seed()
	// A free-floating command carries no project name and no group header, so its
	// workdir must appear on the command row itself.
	m.setGroups(append(m.commands.groups, core.ProjectGroup{
		Name:    "",
		Workdir: "/work/loose",
		Commands: []core.CommandRow{
			{ID: "9", Name: "loose", Workdir: "/work/loose", State: model.EventTypeRunning},
		},
	}))
	out := core.StripANSI(m.renderCommandList("Commands", 60, 16))
	if !strings.Contains(out, "/work/loose") {
		t.Fatalf("a free-floating command row should show its workdir, got:\n%s", out)
	}
}

func TestComposeRowShowsWorkdir(t *testing.T) {
	m := composeSeed(false)
	out := core.StripANSI(m.renderComposeBody(90, 8))
	if !strings.Contains(out, "/work/local-dev") {
		t.Fatalf("a compose project row should show its workdir, got:\n%s", out)
	}
	if !strings.Contains(out, "/other") {
		t.Fatalf("each compose project row should show its own workdir, got:\n%s", out)
	}
}

func TestLayoutTabDefaultSelectionIsMarker(t *testing.T) {
	m := New(core.Options{Backend: &coretest.FakeBackend{}, PopupMode: true})
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
	m := New(core.Options{Backend: &coretest.FakeBackend{}, PopupMode: true})
	m.active = TabLayout
	// No running dashboard: Current == -1 should land the selection on the first.
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{Names: []string{"a", "b"}, Current: -1}})
	if m.layout.selected != 0 {
		t.Fatalf("with no marker the selection should default to 0, got %d", m.layout.selected)
	}
}

func TestLayoutTabNavigation(t *testing.T) {
	m := New(core.Options{Backend: &coretest.FakeBackend{}, PopupMode: true})
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{Names: []string{"a", "b", "c"}, Current: 0}})
	m, _ = upd(m, coretest.Kr("j"))
	if m.layout.selected != 1 {
		t.Fatalf("j should move the selection down, got %d", m.layout.selected)
	}
	m, _ = upd(m, coretest.Kr("k"))
	if m.layout.selected != 0 {
		t.Fatalf("k should move the selection up, got %d", m.layout.selected)
	}
	// Clamp at the top.
	m, _ = upd(m, coretest.Kr("k"))
	if m.layout.selected != 0 {
		t.Fatalf("k at the top should clamp to 0, got %d", m.layout.selected)
	}
}

func TestLayoutTabEnterAppliesInPopupMode(t *testing.T) {
	m := New(core.Options{Backend: &coretest.FakeBackend{}, PopupMode: true})
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{
		Project: "tools", Path: "/c.yaml", Names: []string{"dev", "ops"}, Current: 0,
	}})
	m, _ = upd(m, coretest.Kr("j")) // select "ops"

	m, cmd := upd(m, coretest.KEnter)
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
	fb := m.backend.(*coretest.FakeBackend)
	if len(fb.AppliedLayouts) != 1 || fb.AppliedLayouts[0] != "ops" {
		t.Fatalf("ApplyLayout should target the selected layout, got %v", fb.AppliedLayouts)
	}
}

func TestLayoutTabEnterWarnsInDirectMode(t *testing.T) {
	m := New(core.Options{Backend: &coretest.FakeBackend{}}) // direct mode: PopupMode false
	m.active = TabLayout
	m, _ = upd(m, layoutsLoadedMsg{info: LayoutsInfo{
		Project: "tools", Path: "/c.yaml", Names: []string{"dev", "ops"}, Current: 0,
	}})

	m, _ = upd(m, coretest.KEnter)
	if m.popup.kind != popupMuxWarn {
		t.Fatalf("direct mode should open the mux warning popup, got %v", m.popup.kind)
	}
	if m.popup.layout != "dev" {
		t.Fatalf("the warning popup should carry the selected layout, got %q", m.popup.layout)
	}
	if m.popup.confirmed() {
		t.Fatalf("the warning popup should default to <cancel>")
	}
	fb := m.backend.(*coretest.FakeBackend)
	if len(fb.AppliedLayouts) != 0 {
		t.Fatalf("apply must wait for confirmation, got %v", fb.AppliedLayouts)
	}

	// Toggle to the action button and confirm.
	m, _ = upd(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m, cmd := upd(m, coretest.KEnter)
	if _, ok := firstMsg[layoutDoneMsg](drain(cmd)); !ok {
		t.Fatalf("confirming the warning should dispatch ApplyLayout")
	}
	if len(fb.AppliedLayouts) != 1 || fb.AppliedLayouts[0] != "dev" {
		t.Fatalf("ApplyLayout should target the selected layout after confirm, got %v",
			fb.AppliedLayouts)
	}
}

func TestLayoutTabEntryLoadsLayouts(t *testing.T) {
	m := seed()
	fb := m.backend.(*coretest.FakeBackend)
	fb.LayoutsInfo = LayoutsInfo{Project: "local-dev", Names: []string{"solo"}, Current: 0}
	// Switch Commands -> Compose -> Layout; entering Layout dispatches a load.
	m, _ = upd(m, kTab) // Compose
	m, cmd := upd(m, kTab)
	if m.active != TabLayout {
		t.Fatalf("precondition: should be on the Layout tab")
	}
	if _, ok := firstMsg[layoutsLoadedMsg](drain(cmd)); !ok {
		t.Fatalf("entering the Layout tab should dispatch a ListLayouts load")
	}
	if len(fb.LayoutsReq) == 0 {
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
// and a coretest.FakeBackend, sized to a usable preview pane.
func termModel(rows ...core.CommandRow) Model {
	m := New(core.Options{Backend: &coretest.FakeBackend{}})
	m.width, m.height = 80, 24
	m.active = TabCommands
	m.setGroups([]core.ProjectGroup{{Name: "", Workdir: "/w", Commands: rows}})
	return m
}

func TestPreviewTerminalViewRendersRawStream(t *testing.T) {
	m := termModel(core.CommandRow{
		ID: "1", Name: "shell", Workdir: "/w", State: model.EventTypeRunning, Tty: true,
	})
	m.commands.selected = 0
	fb := m.backend.(*coretest.FakeBackend)
	fb.RawChunks = [][]byte{[]byte("hello-term")}

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
	if len(fb.RawIDs) != 1 || fb.RawIDs[0] != "1" {
		t.Fatalf("RawView should target the running tty command, got %v", fb.RawIDs)
	}
	if len(fb.LogStreams) != 0 {
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
	stream := fb.RawStreams[0]
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
		row  core.CommandRow
	}{
		{"running non-tty", core.CommandRow{
			ID: "1", Name: "svc", Workdir: "/w",
			State: model.EventTypeRunning, LogDriver: logdriver.DriverK8sFile,
		}},
		{"exited tty", core.CommandRow{
			ID: "1", Name: "job", Workdir: "/w",
			State: model.EventTypeExited, LogDriver: logdriver.DriverK8sFile, Tty: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := termModel(c.row)
			m.commands.selected = 0
			fb := m.backend.(*coretest.FakeBackend)

			cmd := (&m).reconcilePreview()
			if m.commands.preview.terminal {
				t.Fatalf("%s must not use terminal-view mode", c.name)
			}
			if _, ok := firstMsg[previewOpenedMsg](drain(cmd)); !ok {
				t.Fatalf("%s should open the sanitized log reader", c.name)
			}
			if len(fb.RawIDs) != 0 {
				t.Fatalf("%s must not open a raw stream, got %v", c.name, fb.RawIDs)
			}
		})
	}
}

func TestPreviewTerminalStreamClosesOnSelectionChange(t *testing.T) {
	m := termModel(
		core.CommandRow{
			ID:      "1",
			Name:    "a",
			Workdir: "/w",
			State:   model.EventTypeRunning,
			Tty:     true,
		},
		core.CommandRow{
			ID:      "2",
			Name:    "b",
			Workdir: "/w",
			State:   model.EventTypeRunning,
			Tty:     true,
		},
	)
	fb := m.backend.(*coretest.FakeBackend)
	m.commands.selected = 0

	opened, ok := firstMsg[rawOpenedMsg](drain((&m).reconcilePreview()))
	if !ok {
		t.Fatalf("the first selection should open a raw stream")
	}
	m, _ = upd(m, opened)
	if m.commands.preview.raw == nil {
		t.Fatalf("the first selection should hold a live raw stream")
	}
	if len(fb.RawStreams) != 1 {
		t.Fatalf("expected one raw stream opened, got %d", len(fb.RawStreams))
	}

	// Moving the selection must close the previous raw stream. stopPreview closes
	// it off the update loop, so wait briefly for the async close.
	m.commands.selected = 1
	_ = (&m).reconcilePreview()
	fb.RawStreams[0].WaitClosed(t)
}

func TestPreviewTerminalEmulatorSizedToPTYNotPane(t *testing.T) {
	m := termModel(core.CommandRow{
		ID: "1", Name: "shell", Workdir: "/w", State: model.EventTypeRunning, Tty: true,
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
