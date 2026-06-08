package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func TestEventSignalSchedulesDebouncedRelist(t *testing.T) {
	m := seed()
	m.events = &fakeEventStream{ch: make(chan EventSignal, 1)}
	beforeGen := m.reloadGen
	m, cmd := m2tuple(m.onEventSignal(eventSignalMsg{}))
	if m.reloadGen != beforeGen+1 {
		t.Fatalf("event should bump the debounce generation")
	}
	if cmd == nil {
		t.Fatalf("event should schedule a debounce tick and keep listening")
	}
}

func TestReloadTickStaleGenerationIgnored(t *testing.T) {
	m := seed()
	m.reloadGen = 5
	// A tick from an older generation must not re-list (a newer event arrived).
	_, cmd := m2tuple(m.onReloadTick(reloadTickMsg{gen: 4}))
	if cmd != nil {
		t.Fatalf("stale debounce tick should not trigger a re-list")
	}
	// The latest tick triggers the re-list.
	_, cmd = m2tuple(m.onReloadTick(reloadTickMsg{gen: 5}))
	if cmd == nil {
		t.Fatalf("matching debounce tick should trigger a re-list")
	}
}

func TestEventTailErrorReportedWithoutClosing(t *testing.T) {
	m := seed()
	m.events = &fakeEventStream{ch: make(chan EventSignal, 1)}
	m, cmd := m2tuple(m.onEventSignal(eventSignalMsg{err: errors.New("tail broke")}))
	if !strings.Contains(m.status, "tail broke") {
		t.Fatalf("event-tail error should be surfaced in the footer, got %q", m.status)
	}
	if cmd == nil {
		t.Fatalf("the TUI should keep listening after a tail error")
	}
}

func TestEventStreamClosedStopsListening(t *testing.T) {
	m := seed()
	m.events = &fakeEventStream{ch: make(chan EventSignal)}
	m, cmd := m2tuple(m.onEventSignal(eventSignalMsg{closed: true}))
	if cmd != nil {
		t.Fatalf("a closed event stream should stop the listen loop")
	}
	if m.events != nil {
		t.Fatalf("closed stream should be cleared")
	}
}

func TestRefreshPreservesFoldFilterAndTab(t *testing.T) {
	m := seed()
	m.commands.setFolded(0, true) // fold local-dev
	m.commands.filter = "web"
	m.active = tabCommands

	infos := []CommandInfo{
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
		{
			ID:      "3",
			Name:    "web",
			Project: "api-stack",
			Workdir: "/work/api",
			State:   model.EventTypeRunning,
		},
	}
	m, _ = m.onCommandsLoaded(commandsLoadedMsg{infos: infos})

	if !m.commands.folded(0) {
		t.Fatalf("fold state should survive a refresh")
	}
	if m.commands.filter != "web" {
		t.Fatalf("filter should survive a refresh, got %q", m.commands.filter)
	}
	if m.active != tabCommands {
		t.Fatalf("active tab should survive a refresh")
	}
}

func TestPreviewStartsAndCancelsPreviousReader(t *testing.T) {
	m := seed()
	fb := m.backend.(*fakeBackend)

	// Select watcher (idx 1) and open its preview.
	m.commands.selected = 1
	cmd := (&m).reconcilePreview()
	if cmd == nil {
		t.Fatalf("selecting a command should open a preview reader")
	}
	opened, ok := cmd().(previewOpenedMsg)
	if !ok {
		t.Fatalf("expected previewOpenedMsg, got %#v", cmd())
	}
	m, _ = m2tuple(m.onPreviewOpened(opened))
	if m.commands.preview.stream == nil {
		t.Fatalf("preview stream should be established")
	}
	if len(fb.logStreams) != 1 {
		t.Fatalf("expected exactly one Logs reader, got %d", len(fb.logStreams))
	}
	first := fb.logStreams[0]

	// Move to seed-db (idx 2): the previous follow reader must be cancelled.
	m.commands.selected = 2
	cmd = (&m).reconcilePreview()
	if !first.closed {
		t.Fatalf("selection change should cancel the previous follow reader")
	}
	if cmd == nil {
		t.Fatalf("the new selection should open its own preview reader")
	}
}

func TestPreviewLineAppendsAndStaleIgnored(t *testing.T) {
	m := seed()
	stream := &fakeLogStream{ch: make(chan LogLine, 4)}
	m.commands.preview = previewState{cmdID: "1", status: previewLoading, stream: stream}

	m, _ = m2tuple(m.onPreviewLine(previewLineMsg{cmdID: "1", line: "first"}))
	if m.commands.preview.status != previewOK {
		t.Fatalf("preview should become OK after a line")
	}
	if len(m.commands.preview.lines) != 1 || m.commands.preview.lines[0] != "first" {
		t.Fatalf("line should be appended, got %v", m.commands.preview.lines)
	}

	// A line for a different (previously selected) command is ignored.
	m, _ = m2tuple(m.onPreviewLine(previewLineMsg{cmdID: "999", line: "stale"}))
	if len(m.commands.preview.lines) != 1 {
		t.Fatalf("stale reader lines must be ignored, got %v", m.commands.preview.lines)
	}
}

func TestPreviewReadErrorRendersErrorState(t *testing.T) {
	m := seed()
	m.commands.preview = previewState{cmdID: "1", status: previewLoading}
	m, _ = m2tuple(
		m.onPreviewLine(previewLineMsg{cmdID: "1", err: errors.New("permission denied")}),
	)
	if m.commands.preview.status != previewError {
		t.Fatalf("a read error should set the preview error state")
	}
	if !strings.Contains(m.commands.preview.errMsg, "permission denied") {
		t.Fatalf("error message should be captured, got %q", m.commands.preview.errMsg)
	}
}

func TestPreviewNoneDriverShowsNoStorage(t *testing.T) {
	m := New(Options{Backend: &fakeBackend{}})
	m.cwd = "/w"
	m.setGroups([]projectGroup{
		{name: "p", workdir: "/w", commands: []commandRow{
			{
				id:        "n1",
				name:      "quiet",
				project:   "p",
				workdir:   "/w",
				state:     model.EventTypeRunning,
				logDriver: logdriver.DriverNone,
			},
		}},
	})
	m.commands.selected = 1 // the command row
	cmd := (&m).reconcilePreview()
	if cmd != nil {
		t.Fatalf("none-driver command should not open a log reader")
	}
	if m.commands.preview.status != previewNoStorage {
		t.Fatalf("none-driver command should show the no-storage state")
	}
}

func TestPreviewClearedWhenProjectRowSelected(t *testing.T) {
	m := seed()
	m.commands.selected = 1
	_ = (&m).reconcilePreview() // establish preview for a command
	m.commands.preview.cmdID = "1"

	m.commands.selected = 0 // project header row
	cmd := (&m).reconcilePreview()
	if cmd != nil {
		t.Fatalf("a project row has no preview reader")
	}
	if m.commands.preview.status != previewEmpty {
		t.Fatalf("preview should reset to empty on a project row")
	}
}

func TestAttachDetachKeepsSelectionAndReports(t *testing.T) {
	m := seed()
	m.commands.selected = 1
	c, _ := m.commands.selectedCommand()
	m, cmd := m2tuple(m.onAttachDone(attachDoneMsg{name: c.name, outcome: AttachDetached}))
	if !strings.Contains(m.status, "detached") {
		t.Fatalf("detach should report a status, got %q", m.status)
	}
	got, ok := m.commands.selectedCommand()
	if !ok || got.id != c.id {
		t.Fatalf("selection should be preserved across attach handoff")
	}
	if cmd == nil {
		t.Fatalf("attach return should redraw and refresh")
	}
}

func TestAttachExitedReportsExit(t *testing.T) {
	m := seed()
	m, _ = m2tuple(m.onAttachDone(attachDoneMsg{name: "web", outcome: AttachExited}))
	if !strings.Contains(m.status, "exited") {
		t.Fatalf("command exit during attach should report exited, got %q", m.status)
	}
}

func TestAttachErrorReported(t *testing.T) {
	m := seed()
	m, _ = m2tuple(m.onAttachDone(attachDoneMsg{name: "web", err: errors.New("session gone")}))
	if !strings.Contains(m.status, "session gone") {
		t.Fatalf("attach error should be surfaced, got %q", m.status)
	}
}

func TestAttachConfirmStartsHandoff(t *testing.T) {
	m := seed()
	m.commands.selected = 1
	m, _ = upd(m, kr("a")) // open attach popup (defaults to yes)
	if m.popup.kind != popupAttach {
		t.Fatalf("a should open the attach popup")
	}
	m, cmd := upd(m, kEnter) // confirm
	if cmd == nil {
		t.Fatalf("confirming attach should start the terminal handoff")
	}
	if m.popup.open() {
		t.Fatalf("popup should close once confirmed")
	}
}

func TestInitSubscribesToEvents(t *testing.T) {
	m := seed()
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("Init should issue startup commands")
	}
	// The batch should include the event subscription; execute it and look for
	// the subscribed message among the batch results.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init should batch its startup commands, got %#v", msg)
	}
	foundSubscribe := false
	for _, c := range batch {
		if _, isSub := c().(eventsSubscribedMsg); isSub {
			foundSubscribe = true
		}
	}
	if !foundSubscribe {
		t.Fatalf("Init should subscribe to events")
	}
}
