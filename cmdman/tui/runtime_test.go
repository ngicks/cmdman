package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/vt"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

func TestEventSignalSchedulesDebouncedRelist(t *testing.T) {
	m := seed()
	m.events = &coretest.FakeEventStream{Ch: make(chan EventSignal, 1)}
	beforeGen := m.reloadGen
	m, cmd := m2tuple(m.onEventSignal(core.EventSignalMsg{}))
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
	_, cmd := m2tuple(m.onReloadTick(core.ReloadTickMsg{Gen: 4}))
	if cmd != nil {
		t.Fatalf("stale debounce tick should not trigger a re-list")
	}
	// The latest tick triggers the re-list.
	_, cmd = m2tuple(m.onReloadTick(core.ReloadTickMsg{Gen: 5}))
	if cmd == nil {
		t.Fatalf("matching debounce tick should trigger a re-list")
	}
}

func TestEventTailErrorReportedWithoutClosing(t *testing.T) {
	m := seed()
	m.events = &coretest.FakeEventStream{Ch: make(chan EventSignal, 1)}
	m, cmd := m2tuple(m.onEventSignal(core.EventSignalMsg{Err: errors.New("tail broke")}))
	if !strings.Contains(m.status, "tail broke") {
		t.Fatalf("event-tail error should be surfaced in the footer, got %q", m.status)
	}
	if cmd == nil {
		t.Fatalf("the TUI should keep listening after a tail error")
	}
}

func TestEventStreamClosedStopsListening(t *testing.T) {
	m := seed()
	m.events = &coretest.FakeEventStream{Ch: make(chan EventSignal)}
	m, cmd := m2tuple(m.onEventSignal(core.EventSignalMsg{Closed: true}))
	if cmd != nil {
		t.Fatalf("a closed event stream should stop the listen loop")
	}
	if m.events != nil {
		t.Fatalf("closed stream should be cleared")
	}
}

// --- live runtime state -----------------------------------------------------

// listedInfos is what a list load reports for the seeded local-dev project:
// one running command and one exited one, with no runtime state on either —
// the TUI's listing stopped carrying it once the streams took over (L3).
func listedInfos() []core.CommandInfo {
	return []core.CommandInfo{
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
	}
}

// watchingModel is a model that has loaded listedInfos once, so the watcher
// holds a stream for the running command. The watcher is closed with the test.
func watchingModel(t *testing.T) (Model, *coretest.FakeBackend) {
	t.Helper()
	m := seed()
	m.ctx = t.Context()
	fb, ok := m.backend.(*coretest.FakeBackend)
	if !ok {
		t.Fatalf("seed should carry the fake backend, got %T", m.backend)
	}
	t.Cleanup(func() { _ = m.watcher.Close() })
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: listedInfos()})
	return m, fb
}

// recvRuntimeUpdate runs the merged receive off the test goroutine, so a push
// that never arrives fails the test instead of hanging it.
func recvRuntimeUpdate(t *testing.T, w *core.RuntimeWatcher) core.RuntimeUpdateMsg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() { ch <- core.WaitRuntimeUpdateCmd(w)() }()
	select {
	case msg := <-ch:
		u, ok := msg.(core.RuntimeUpdateMsg)
		if !ok {
			t.Fatalf("WaitRuntimeUpdateCmd delivered %T, want core.RuntimeUpdateMsg", msg)
		}
		return u
	case <-time.After(time.Second):
		t.Fatalf("no runtime update arrived")
		return core.RuntimeUpdateMsg{}
	}
}

// commandRow returns the row for a command id from the Commands tab.
func commandRow(t *testing.T, m Model, id string) core.CommandRow {
	t.Helper()
	for _, g := range m.commands.groups {
		for _, c := range g.Commands {
			if c.ID == id {
				return c
			}
		}
	}
	t.Fatalf("no row for command %q", id)
	return core.CommandRow{}
}

func TestInitArmsRuntimeWatch(t *testing.T) {
	m := seed()
	armed := false
	for _, msg := range drain(m.Init()) {
		if _, ok := msg.(runtimeWatchReadyMsg); ok {
			armed = true
		}
	}
	if !armed {
		t.Fatalf("Init should arm the runtime-state receive")
	}
	// The arming message is what starts the (blocking) merged receive.
	if _, cmd := upd(m, runtimeWatchReadyMsg{}); cmd == nil {
		t.Fatalf("the arming message should start the merged receive")
	}
}

func TestRuntimePushPatchesRowWithoutRelist(t *testing.T) {
	m, fb := watchingModel(t)
	stream := fb.WatchStreams["1"]
	if stream == nil {
		t.Fatalf("the running command should be watched, subscribed %v", fb.WatchIDs)
	}
	stream.PushState(core.RuntimeStateView{
		Title:      "make build",
		Status:     "working",
		Detail:     "step 2/3",
		BellUnread: true,
	})

	// No list load happens between the push and the row: the update travels the
	// watcher's merged channel straight into Update.
	m, cmd := upd(m, recvRuntimeUpdate(t, m.watcher))
	if cmd == nil {
		t.Fatalf("a runtime update should rearm the receive")
	}
	row := commandRow(t, m, "1")
	if row.Title != "make build" || row.Status != "working" || row.Detail != "step 2/3" {
		t.Errorf("pushed state should patch the row, got %+v", row)
	}
	if !row.Bell {
		t.Errorf("a pushed unread bell should light the row's glyph")
	}
}

func TestRelistKeepsPushedRuntimeState(t *testing.T) {
	m, fb := watchingModel(t)
	fb.WatchStreams["1"].PushState(core.RuntimeStateView{Title: "make build", Status: "working"})
	m, _ = upd(m, recvRuntimeUpdate(t, m.watcher))

	// The re-list carries no runtime state; the cache is what keeps the row
	// from flashing back to an empty title.
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: listedInfos()})
	row := commandRow(t, m, "1")
	if row.Title != "make build" || row.Status != "working" {
		t.Errorf("a re-list should keep the pushed state, got %+v", row)
	}
	if len(fb.WatchIDs) != 1 {
		t.Errorf("a held stream should not be redialed, subscribed %v", fb.WatchIDs)
	}
}

func TestExitedCommandDropsCachedRuntimeState(t *testing.T) {
	m, fb := watchingModel(t)
	fb.WatchStreams["1"].PushState(core.RuntimeStateView{Title: "make build"})
	m, _ = upd(m, recvRuntimeUpdate(t, m.watcher))

	infos := listedInfos()
	infos[0].State = model.EventTypeExited
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: infos})
	if len(m.runtime) != 0 {
		t.Errorf("a dropped stream should evict its cached state, cached %v", m.runtime)
	}
	if row := commandRow(t, m, "1"); row.Title != "" {
		t.Errorf("an exited command should not keep its last title, got %q", row.Title)
	}
}

func TestSelfEndedStreamLeavesNothingForTheNextRun(t *testing.T) {
	m, fb := watchingModel(t)
	fb.WatchStreams["1"].PushState(core.RuntimeStateView{Title: "make build"})
	m, _ = upd(m, recvRuntimeUpdate(t, m.watcher))

	// The monitor left an active state, so the stream ended on its own: the
	// watcher forgets it without a reconcile ever naming it dropped.
	fb.WatchStreams["1"].EndStream()
	ended := recvRuntimeUpdate(t, m.watcher)
	if !ended.Closed || ended.ID != "1" {
		t.Fatalf("a stream that ends should report its own close, got %+v", ended)
	}
	m, _ = upd(m, ended)

	infos := listedInfos()
	infos[0].State = model.EventTypeExited
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: infos})
	if len(m.runtime) != 0 {
		t.Errorf("a command that left a live state should keep nothing cached, got %v", m.runtime)
	}

	// The command runs again: its row carries the new run's state only, and the
	// watcher redials now that the list names it live once more.
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: listedInfos()})
	if row := commandRow(t, m, "1"); row.Title != "" {
		t.Errorf("a run that came back should not wear the last run's title, got %q", row.Title)
	}
	if len(fb.WatchIDs) != 2 {
		t.Errorf("the returning command should be redialed, subscribed %v", fb.WatchIDs)
	}
}

func TestRuntimeUpdateForUnwatchedCommandIgnored(t *testing.T) {
	m, _ := watchingModel(t)
	// An id that is not listed at all, and one listed without a live monitor:
	// both are stragglers buffered before their stream was dropped.
	for _, id := range []string{"ghost", "2"} {
		nm, cmd := upd(m, core.RuntimeUpdateMsg{
			ID:    id,
			State: core.RuntimeStateView{Title: "stale"},
		})
		if cmd == nil {
			t.Fatalf("an ignored update should still rearm the receive")
		}
		m = nm
	}
	if len(m.runtime) != 0 {
		t.Errorf("an ignored update should not be cached, cached %v", m.runtime)
	}
	if row := commandRow(t, m, "2"); row.Title != "" {
		t.Errorf("an exited command's row should not take a straggler, got %q", row.Title)
	}
}

func TestRuntimeStreamEndAndErrorKeepListening(t *testing.T) {
	m, _ := watchingModel(t)
	for _, msg := range []core.RuntimeUpdateMsg{
		{ID: "1", Closed: true},
		{ID: "1", Err: errors.New("monitor went away")},
	} {
		nm, cmd := upd(m, msg)
		if cmd == nil {
			t.Fatalf("%+v should keep the merged receive armed", msg)
		}
		m = nm
		if m.status != "" {
			t.Errorf("a broken stream should not reach the footer, got %q", m.status)
		}
	}
}

func TestRuntimeWatcherClosedStopsListening(t *testing.T) {
	m := seed()
	// The watcher's own close carries no id: nothing is left to hear from.
	if _, cmd := upd(m, core.RuntimeUpdateMsg{Closed: true}); cmd != nil {
		t.Fatalf("a closed watcher should stop the receive loop")
	}
}

func TestQuitClosesRuntimeWatcher(t *testing.T) {
	m, fb := watchingModel(t)
	stream := fb.WatchStreams["1"]
	m, _ = upd(m, coretest.Kr("q"))
	if !m.quitting {
		t.Fatalf("q should quit")
	}
	stream.WaitClosed(t)
}

func TestRefreshPreservesFoldFilterAndTab(t *testing.T) {
	m := seed()
	m.commands.setFolded(0, true) // fold local-dev
	m.commands.filter = "web"
	m.active = TabCommands

	infos := []core.CommandInfo{
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
	m, _ = m.onCommandsLoaded(core.CommandsLoadedMsg{Infos: infos})

	if !m.commands.folded(0) {
		t.Fatalf("fold state should survive a refresh")
	}
	if m.commands.filter != "web" {
		t.Fatalf("filter should survive a refresh, got %q", m.commands.filter)
	}
	if m.active != TabCommands {
		t.Fatalf("active tab should survive a refresh")
	}
}

func TestPreviewStartsAndCancelsPreviousReader(t *testing.T) {
	m := seed()
	fb := m.backend.(*coretest.FakeBackend)

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
	if len(fb.LogStreams) != 1 {
		t.Fatalf("expected exactly one Logs reader, got %d", len(fb.LogStreams))
	}
	first := fb.LogStreams[0]

	// Move to seed-db (idx 2): the previous follow reader must be cancelled.
	m.commands.selected = 2
	cmd = (&m).reconcilePreview()
	if !first.Closed {
		t.Fatalf("selection change should cancel the previous follow reader")
	}
	if cmd == nil {
		t.Fatalf("the new selection should open its own preview reader")
	}
}

func TestPreviewDuplicateOpenClosesPreviousStream(t *testing.T) {
	m := seed()
	m.commands.preview = previewState{cmdID: "1", status: previewLoading}

	// First open for id "1" establishes a reader.
	first := &coretest.FakeLogStream{Ch: make(chan LogLine, 1)}
	m, _ = m2tuple(m.onPreviewOpened(previewOpenedMsg{cmdID: "1", stream: first}))
	if m.commands.preview.stream == nil {
		t.Fatalf("the first open should establish a preview stream")
	}

	// A second open for the same id (selection bounce A→B→A) must close the
	// first stream before overwriting it, releasing its pump goroutine.
	second := &coretest.FakeLogStream{Ch: make(chan LogLine, 1)}
	m, _ = m2tuple(m.onPreviewOpened(previewOpenedMsg{cmdID: "1", stream: second}))
	if !first.Closed {
		t.Fatalf("a duplicate open for the same id must close the previous stream")
	}
	if second.Closed {
		t.Fatalf("the surviving stream must stay open")
	}
}

func TestPreviewLineAppendsAndStaleIgnored(t *testing.T) {
	m := seed()
	stream := &coretest.FakeLogStream{Ch: make(chan LogLine, 4)}
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

func TestRawDuplicateOpenClosesPreviousStream(t *testing.T) {
	m := seed()
	m.width, m.height = 80, 24
	m.commands.preview = previewState{cmdID: "1", status: previewLoading, terminal: true}

	// First open for id "1" establishes a raw stream and emulator.
	first := coretest.NewFakeRawStream(1)
	m, _ = m2tuple(m.onRawOpened(rawOpenedMsg{cmdID: "1", stream: first}))
	if m.commands.preview.raw == nil {
		t.Fatalf("the first open should establish a raw stream")
	}

	// A second open for the same id (selection bounce A→B→A) must close the
	// first stream before overwriting it, releasing its drain goroutine. The
	// close runs off the update loop, so wait briefly for it.
	second := coretest.NewFakeRawStream(1)
	m, _ = m2tuple(m.onRawOpened(rawOpenedMsg{cmdID: "1", stream: second}))
	first.WaitClosed(t)
	if second.IsClosed() {
		t.Fatalf("the surviving raw stream must stay open")
	}
}

func TestRawTickStaleGenerationIgnored(t *testing.T) {
	m := seed()
	// An active terminal preview for cmd "1" at generation 5.
	m.commands.preview = previewState{
		cmdID: "1", status: previewOK, terminal: true, streaming: true, gen: 5,
	}

	// A tick that matches the active (cmdID, gen) keeps the repaint cadence alive.
	if _, cmd := m2tuple(m.onRawTick(rawTickMsg{cmdID: "1", gen: 5})); cmd == nil {
		t.Fatalf("a matching tick should reschedule the repaint")
	}
	// A tick from a superseded generation must not spawn a duplicate loop.
	if _, cmd := m2tuple(m.onRawTick(rawTickMsg{cmdID: "1", gen: 4})); cmd != nil {
		t.Fatalf("a stale-generation tick should stop, not reschedule")
	}
	// A tick for a previously selected command is ignored too.
	if _, cmd := m2tuple(m.onRawTick(rawTickMsg{cmdID: "9", gen: 5})); cmd != nil {
		t.Fatalf("a stale-cmdID tick should stop, not reschedule")
	}
}

func TestRawClosedStaleGenerationIgnored(t *testing.T) {
	m := seed()
	m.commands.preview = previewState{
		cmdID: "1", status: previewOK, terminal: true, streaming: true, gen: 5,
	}

	// A close for a superseded preview must not disturb the live one.
	stale, _ := m2tuple(m.onRawClosed(rawClosedMsg{cmdID: "1", gen: 4}))
	if !stale.commands.preview.streaming {
		t.Fatalf("a stale-generation close must not stop the current stream")
	}

	// A matching close stops the repaint tick while keeping the last frame.
	closed, _ := m2tuple(m.onRawClosed(rawClosedMsg{cmdID: "1", gen: 5}))
	if closed.commands.preview.streaming {
		t.Fatalf("a matching close should stop streaming")
	}
	if closed.commands.preview.status != previewOK {
		t.Fatalf("a clean close should keep the last frame, got status %v",
			closed.commands.preview.status)
	}
}

func TestRawClosedErrorRendersErrorState(t *testing.T) {
	m := seed()
	m.commands.preview = previewState{
		cmdID: "1", status: previewOK, terminal: true, streaming: true, gen: 5,
	}
	m, _ = m2tuple(m.onRawClosed(rawClosedMsg{cmdID: "1", gen: 5, err: errors.New("attach gone")}))
	if m.commands.preview.status != previewError {
		t.Fatalf("a drain error should set the preview error state")
	}
	if !strings.Contains(m.commands.preview.errMsg, "attach gone") {
		t.Fatalf("error message should be captured, got %q", m.commands.preview.errMsg)
	}
}

func TestPreviewNoneDriverShowsNoStorage(t *testing.T) {
	m := New(core.Options{Backend: &coretest.FakeBackend{}})
	m.cwd = "/w"
	m.setGroups([]core.ProjectGroup{
		{Name: "p", Workdir: "/w", Commands: []core.CommandRow{
			{
				ID:        "n1",
				Name:      "quiet",
				Project:   "p",
				Workdir:   "/w",
				State:     model.EventTypeRunning,
				LogDriver: logdriver.DriverNone,
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
	m, cmd := m2tuple(m.onAttachDone(attachDoneMsg{name: c.Name, outcome: AttachDetached}))
	if !strings.Contains(m.status, "detached") {
		t.Fatalf("detach should report a status, got %q", m.status)
	}
	got, ok := m.commands.selectedCommand()
	if !ok || got.ID != c.ID {
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
	m, _ = upd(m, coretest.Kr("a")) // open attach popup (defaults to yes)
	if m.popup.kind != popupAttach {
		t.Fatalf("a should open the attach popup")
	}
	m, cmd := upd(m, coretest.KEnter) // confirm
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
		if _, isSub := c().(core.EventsSubscribedMsg); isSub {
			foundSubscribe = true
		}
	}
	if !foundSubscribe {
		t.Fatalf("Init should subscribe to events")
	}
}

func TestDrainRawDoesNotDeadlockOnQueryResponses(t *testing.T) {
	// A full-screen program emits terminal queries (DA1 ESC[c, secondary DA
	// ESC[>c, cursor report ESC[6n, mode query ESC[?2026$p). The vt emulator
	// answers them into an unbuffered internal pipe; if drainRawCmd does not
	// drain that pipe, the first reply blocks term.Write under the write lock and
	// starves Render(), hanging the UI. This drives the real drainRawCmd with a
	// query-laden stream while a concurrent Render runs, and requires it to finish
	// quickly. Without the discard drain it deadlocks (times out).
	term := vt.NewSafeEmulator(40, 10)
	stream := coretest.NewFakeRawStream(256)
	query := []byte("x\x1b[c y\x1b[>c\x1b[6n\x1b[?2026$p z\r\n")
	for range 200 {
		stream.Ch <- RawChunk{Bytes: query}
	}
	_ = stream.Close() // closes the chunk channel; the 200 buffered chunks still drain

	stopRender := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopRender:
				return
			default:
				_ = term.Render()
			}
		}
	}()

	done := make(chan tea.Msg, 1)
	go func() { done <- drainRawCmd(term, stream, "1", 1)() }()

	select {
	case <-done:
		close(stopRender)
	case <-time.After(5 * time.Second):
		close(stopRender)
		t.Fatal(
			"drainRawCmd deadlocked draining query responses (emulator response pipe not drained)",
		)
	}
}

func TestDrainRawResizeSizesEmulatorToPTY(t *testing.T) {
	// A PTY size report must resize the local emulator to the command's actual
	// render dimensions (so the preview matches and does not reflow), without any
	// remote interaction.
	term := vt.NewSafeEmulator(defaultPreviewCols, defaultPreviewRows)
	stream := coretest.NewFakeRawStream(4)
	stream.Ch <- RawChunk{Resize: &RawSize{Rows: 40, Cols: 120}}
	stream.Ch <- RawChunk{Bytes: []byte("hello")}
	_ = stream.Close()

	_ = drainRawCmd(term, stream, "1", 1)()
	if w := term.Width(); w != 120 {
		t.Fatalf("emulator width should match reported PTY cols 120, got %d", w)
	}
	if h := term.Height(); h != 40 {
		t.Fatalf("emulator height should match reported PTY rows 40, got %d", h)
	}
}

func TestDrainRawRecoversEmulatorPanic(t *testing.T) {
	// The vt/ultraviolet emulator panics on some real control-sequence combos a
	// full-screen TUI (codex) emits: a scroll region + XTWINOPS resize + line
	// insert. drainRawCmd must recover and report a panicked close instead of
	// crashing the whole TUI. This sequence is a verified-deterministic trigger.
	term := vt.NewSafeEmulator(1, 1)
	stream := coretest.NewFakeRawStream(2)
	stream.Ch <- RawChunk{Bytes: []byte("\x1b[1;5r\x1b[8;24;80t\x1b[L")}
	_ = stream.Close()

	msg := drainRawCmd(term, stream, "1", 1)()
	rc, ok := msg.(rawClosedMsg)
	if !ok {
		t.Fatalf("expected rawClosedMsg, got %T", msg)
	}
	if !rc.panicked {
		t.Fatalf("a vt emulator panic must be reported as a panicked close, got %+v", rc)
	}
}

func TestRawClosedPanicDisablesTerminalPreviewAndFallsBack(t *testing.T) {
	m := seed()
	m.commands.preview = previewState{
		cmdID: "1", status: previewOK, terminal: true, streaming: true, gen: 7,
	}
	m2, _ := m2tuple(m.onRawClosed(rawClosedMsg{cmdID: "1", gen: 7, panicked: true}))
	if !m2.termPreviewDisabled {
		t.Fatalf("a panicked close must disable terminal-view for the session")
	}
	if m2.commands.preview.terminal {
		t.Fatalf("a panicked close must leave terminal-view mode")
	}

	// With terminal-view disabled, reconcile must never choose terminal-view again
	// for a running, TTY-backed command — it falls back to the log path.
	m2.setGroups([]core.ProjectGroup{
		{Name: "p", Workdir: "/w", Commands: []core.CommandRow{
			{ID: "tty1", Name: "codex", Project: "p", Workdir: "/w",
				State: model.EventTypeRunning, Tty: true, LogDriver: logdriver.DriverK8sFile},
		}},
	})
	m2.commands.selected = 1 // the command row
	m2.commands.preview = previewState{}
	_ = (&m2).reconcilePreview()
	if m2.commands.preview.terminal {
		t.Fatalf("with terminal-view disabled, a tty command must use the log preview")
	}
}

func TestRawOpenedStaleCmdIDClosesStream(t *testing.T) {
	// A rawOpenedMsg for a superseded selection must close the stale stream off
	// the update loop and not establish a preview.
	m := seed()
	m.commands.preview = previewState{cmdID: "current", status: previewLoading, terminal: true}
	stale := coretest.NewFakeRawStream(1)
	m2, _ := m2tuple(m.onRawOpened(rawOpenedMsg{cmdID: "old", stream: stale}))
	stale.WaitClosed(t)
	if m2.commands.preview.raw != nil {
		t.Fatalf("a stale-cmdID raw open must not establish a stream")
	}
}
