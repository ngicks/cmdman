package tmux_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// dockFramePane splits a 3-row pane off the top of windowID and stamps it as a
// frame pane. It stands in for a ShowFrame these cases do not need: what they
// want is a window whose panes carry both roles, and the stamp is the whole of
// what makes a pane a frame pane — no def, no entry argv, no geometry to agree
// with. Cases about the show itself call [muxctl.Session.ShowFrame].
func dockFramePane(t *testing.T, socket, windowID string) string {
	t.Helper()
	id := run(
		t, socket, "split-window", "-v", "-b", "-d", "-l", "3",
		"-t", windowID, "-P", "-F", "#{pane_id}",
	)
	run(t, socket, "set-option", "-p", "-t", id, "@cmdman_frame", "1")
	return id
}

// paneFormat expands a tmux format string against one pane; an unset user
// option expands to "".
func paneFormat(t *testing.T, socket, paneID, format string) string {
	t.Helper()
	return run(t, socket, "display-message", "-p", "-t", paneID, format)
}

// TestApplyLayout_SparesFramePanes pins the frame-aware reset: a project apply
// rebuilds only the region the frame leaves over. The frame pane keeps its id
// (it is never killed and re-created), keeps its stamp and its height, and does
// not become one of the layout's panes — which is what a whole-window reset
// would have made of it, since it sits first in the window.
func TestApplyLayout_SparesFramePanes(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	frameID := dockFramePane(t, socket, sess.WindowID())
	frameHeight := paneFormat(t, socket, frameID, "#{pane_height}")

	if _, err := sess.ApplyLayout(
		context.Background(), loadLayout(t, "horizontal-two.yaml", ""), 1,
	); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	ids := listPaneIDs(t, socket, sess.WindowID())
	if !slices.Contains(ids, frameID) {
		t.Fatalf("frame pane %s did not survive the apply: %v", frameID, ids)
	}
	if len(ids) != 3 {
		t.Errorf("panes after apply = %v, want the frame pane plus the layout's two", ids)
	}
	if got := paneFormat(t, socket, frameID, "#{@cmdman_frame}"); got == "" {
		t.Errorf("frame stamp on %s was cleared by the apply", frameID)
	}
	if got := paneFormat(t, socket, frameID, "#{@cmdman_marker}"); got != "" {
		t.Errorf("frame pane %s picked up marker %q; it is not part of the layout", frameID, got)
	}
	if got := paneFormat(t, socket, frameID, "#{pane_height}"); got != frameHeight {
		t.Errorf(
			"frame pane height = %s, want %s (rebuild left the frame region)",
			got,
			frameHeight,
		)
	}

	titles := listPaneTitles(t, socket, sess.WindowID())
	for _, name := range []string{"a", "b"} {
		if !slices.Contains(titles, name) {
			t.Errorf("layout pane %q missing after apply: titles = %v", name, titles)
		}
	}
}

// recordingTmux writes a wrapper around the real tmux binary that appends each
// invocation's argv (tab separated, one line per call) to a log before exec'ing
// it. Unlike apply_order_test.go's fake, it drives a real tmux server: these
// assertions are about which pane a command targeted, so the panes have to
// exist. The returned func reads the log back as one []string per call.
func recordingTmux(t *testing.T) (path string, recorded func() [][]string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "tmux")
	logPath := filepath.Join(dir, "argv.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatalf("create argv log: %v", err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\t' \"$@\" >> '" + logPath + "'\n" +
		"printf '\\n' >> '" + logPath + "'\n" +
		"exec '" + requireTmux(t) + "' \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording tmux: %v", err)
	}
	return path, func() [][]string {
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read argv log: %v", err)
		}
		var calls [][]string
		for line := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
			if line == "" {
				continue
			}
			calls = append(calls, strings.Split(strings.TrimSuffix(line, "\t"), "\t"))
		}
		return calls
	}
}

// sendKeysTarget reports the pane a recorded invocation typed into, if it was a
// send-keys at all. Matching whole argv fields keeps %1 from matching %10.
func sendKeysTarget(call []string) (string, bool) {
	verb := slices.Index(call, "send-keys")
	if verb < 0 {
		return "", false
	}
	for i := verb + 1; i+1 < len(call); i++ {
		if call[i] == "-t" {
			return call[i+1], true
		}
	}
	return "", false
}

// TestApplyLayout_NeverSendsDetachKeysToFramePanes pins the scoped quiesce: the
// viewer sweep that precedes a project rebuild must not type the detach
// sequence into a frame pane's widget. The frame pane is given a stale marker
// on purpose — the sweep selects panes by marker, so without the frame stamp
// being consulted first this pane is exactly the kind it would sweep.
func TestApplyLayout_NeverSendsDetachKeysToFramePanes(t *testing.T) {
	requireTmux(t)
	tmuxPath, recorded := recordingTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	ctx := context.Background()
	srv, err := tmuxctl.Driver{}.Connect(ctx, muxctl.ServerConfig{
		Executable: tmuxPath,
		Socket:     socket,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := srv.New(ctx, muxctl.Config{
		SessionName:      "cmdman-test",
		WindowName:       "cmdman",
		ViewerDetachKeys: []string{"C-p", "C-q"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := loadLayout(t, "single-leaf.yaml", "")
	if _, err := sess.ApplyLayout(ctx, root, 0); err != nil {
		t.Fatalf("first ApplyLayout: %v", err)
	}
	waitForMarker(t, sess, 0)

	ids := listPaneIDs(t, socket, sess.WindowID())
	if len(ids) != 1 {
		t.Fatalf("want a single project pane before docking the frame, got %v", ids)
	}
	projectID := ids[0]
	frameID := dockFramePane(t, socket, sess.WindowID())
	run(t, socket, "set-option", "-p", "-t", frameID, "@cmdman_marker", "0")

	before := len(recorded())
	if _, err := sess.ApplyLayout(ctx, root, 1); err != nil {
		t.Fatalf("second ApplyLayout: %v", err)
	}

	sweptProject := false
	for _, call := range recorded()[before:] {
		target, ok := sendKeysTarget(call)
		if !ok {
			continue
		}
		if target == frameID {
			t.Errorf("detach keys sent into frame pane %s: %v", frameID, call)
		}
		if target == projectID {
			sweptProject = true
		}
	}
	if !sweptProject {
		t.Fatal("no detach keys reached the project pane: the sweep never ran")
	}
}

// mainPaneName is the placeholder leaf the frame verbs carve around; it names
// the region ShowFrame must resize rather than build.
const mainPaneName = "main"

// sleepArgv is a viewer stand-in: it stays alive so "still running" assertions
// mean something.
var sleepArgv = []string{"/bin/sh", "-c", "sleep 60"}

// carveFrame builds the tree a frame verb hands to ShowFrame, through the real
// consumer path: frame.Spec.Carve around a placeholder leaf that carries no
// argv, since the panes it stands for already exist.
func carveFrame(t *testing.T, entries ...frame.Entry) muxctl.PaneSpec {
	t.Helper()
	root, err := frame.Spec{Entries: entries}.Carve(
		muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: mainPaneName}},
		func(string) ([]string, error) { return sleepArgv, nil },
	)
	if err != nil {
		t.Fatalf("Carve: %v", err)
	}
	return root
}

// paneInt reads a numeric tmux format for one pane.
func paneInt(t *testing.T, socket, paneID, format string) int {
	t.Helper()
	raw := paneFormat(t, socket, paneID, format)
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("pane %s %s = %q: %v", paneID, format, raw, err)
	}
	return n
}

// panePIDs reads each pane's process id. A pane that was respawned reports a
// new one, so comparing these is what distinguishes "resized" from "rebuilt".
func panePIDs(t *testing.T, socket string, ids []string) []string {
	t.Helper()
	pids := make([]string, len(ids))
	for i, id := range ids {
		pids[i] = paneFormat(t, socket, id, "#{pane_pid}")
	}
	return pids
}

// framePaneIDs returns the ids of the window's frame-stamped panes.
func framePaneIDs(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{@cmdman_frame}")
	var ids []string
	for line := range strings.SplitSeq(out, "\n") {
		id, stamp, _ := strings.Cut(line, "\t")
		if id != "" && stamp != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// projectPaneIDs is framePaneIDs' complement: the panes carrying no frame
// stamp, i.e. the project region.
func projectPaneIDs(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{@cmdman_frame}")
	var ids []string
	for line := range strings.SplitSeq(out, "\n") {
		id, stamp, _ := strings.Cut(line, "\t")
		if id != "" && stamp == "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// paneIDByTitle finds the pane whose border title is title. Frame leaves are
// named by frame.EntryPaneName, and stampLeaf writes the leaf name as the title.
func paneIDByTitle(t *testing.T, socket, windowID, title string) string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{pane_title}")
	for line := range strings.SplitSeq(out, "\n") {
		id, got, _ := strings.Cut(line, "\t")
		if got == title {
			return id
		}
	}
	t.Fatalf("no pane titled %q in %s: %s", title, windowID, out)
	return ""
}

// activePaneID returns the window's active pane.
func activePaneID(t *testing.T, socket, windowID string) string {
	t.Helper()
	return run(t, socket, "display-message", "-p", "-t", windowID, "#{pane_id}")
}

// assertPanesAlive fails when any pane is dead or was respawned since its pid
// was captured.
func assertPanesAlive(t *testing.T, socket string, ids, wantPIDs []string, when string) {
	t.Helper()
	for i, id := range ids {
		if got := paneFormat(t, socket, id, "#{pane_dead}"); got != "0" {
			t.Errorf("pane %s is dead %s", id, when)
		}
		if got := paneFormat(t, socket, id, "#{pane_pid}"); got != wantPIDs[i] {
			t.Errorf(
				"pane %s pid = %s, want %s: it was respawned %s",
				id, got, wantPIDs[i], when,
			)
		}
	}
}

// TestShowFrame_AroundRunningProject pins what show must leave alone: the
// project's panes keep their ids and their processes — they are resized into the
// region the frame leaves over, never rebuilt — while the frame's own panes are
// created, stamped, and recorded in the window's frame-def state. The tree comes
// from frame.Spec.Carve so the driver is exercised on the shape its real
// consumer produces, including the carve's nesting: the top entry divides the
// window and the left entry divides only what the top one left.
func TestShowFrame_AroundRunningProject(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	projectIDs := listPaneIDs(t, socket, sess.WindowID())
	if len(projectIDs) != 2 {
		t.Fatalf("panes before show = %v, want the layout's two", projectIDs)
	}
	pids := panePIDs(t, socket, projectIDs)
	heightBefore := paneInt(t, socket, projectIDs[0], "#{pane_height}")

	root := carveFrame(t,
		frame.Entry{
			Edge:    frame.EdgeTop,
			Size:    frame.Size{N: 3}.MuxSize(),
			Command: sleepArgv,
		},
		frame.Entry{
			Edge:      frame.EdgeLeft,
			Size:      frame.Size{N: 20}.MuxSize(),
			Component: frame.ComponentSwitcher,
		},
	)
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}

	after := listPaneIDs(t, socket, sess.WindowID())
	for _, id := range projectIDs {
		if !slices.Contains(after, id) {
			t.Fatalf("project pane %s did not survive the show: %v", id, after)
		}
	}
	assertPanesAlive(t, socket, projectIDs, pids, "by the show")

	framed := framePaneIDs(t, socket, sess.WindowID())
	if len(framed) != 2 {
		t.Fatalf("frame panes = %v, want one per entry", framed)
	}
	for _, id := range framed {
		if slices.Contains(projectIDs, id) {
			t.Errorf("project pane %s was stamped as a frame pane", id)
		}
		for _, opt := range []string{"#{@cmdman_marker}", "#{@cmdman_leaf}"} {
			if got := paneFormat(t, socket, id, opt); got != "" {
				t.Errorf("frame pane %s carries %s = %q, want unset", id, opt, got)
			}
		}
	}

	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q, want %q", got, "dev")
	}
	// Read back through the documented accessor too: the option ShowFrame writes
	// and the one muxctl.StateKeyFrameDef maps to must stay the same option, which
	// is what makes a framed window enumerable.
	state, err := newServer(t, socket).ReadWindowState(
		context.Background(), sess.WindowID(), muxctl.StateKeyFrameDef,
	)
	if err != nil {
		t.Fatalf("ReadWindowState: %v", err)
	}
	if state != "dev" {
		t.Errorf("StateKeyFrameDef = %q, want %q", state, "dev")
	}

	if active := activePaneID(t, socket, sess.WindowID()); !slices.Contains(projectIDs, active) {
		t.Errorf("active pane %s is not in the main region %v", active, projectIDs)
	}

	if heightAfter := paneInt(
		t,
		socket,
		projectIDs[0],
		"#{pane_height}",
	); heightAfter >= heightBefore {
		t.Errorf(
			"project pane height = %d, want less than %d: the frame took no room",
			heightAfter, heightBefore,
		)
	}

	// The carve's nesting, realized: entry 0 spans the window, entry 1 starts
	// below it and spans only the remainder.
	topID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(0))
	leftID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(1))
	windowWidth, err := strconv.Atoi(
		run(t, socket, "display-message", "-p", "-t", sess.WindowID(), "#{window_width}"),
	)
	if err != nil {
		t.Fatalf("window width: %v", err)
	}
	if got := paneInt(t, socket, topID, "#{pane_width}"); got != windowWidth {
		t.Errorf("top entry width = %d, want the window's %d", got, windowWidth)
	}
	topBottom := paneInt(
		t,
		socket,
		topID,
		"#{pane_top}",
	) + paneInt(
		t,
		socket,
		topID,
		"#{pane_height}",
	)
	if got := paneInt(t, socket, leftID, "#{pane_top}"); got < topBottom {
		t.Errorf(
			"left entry starts at row %d, want at or below the top entry's %d",
			got, topBottom,
		)
	}
}

// TestHideFrame_ReturnsTheWindowToTheProject pins the other half: hide removes
// the frame panes and the frame-def state, the project region grows back into
// the whole window, and nothing it was running was disturbed on the way.
func TestHideFrame_ReturnsTheWindowToTheProject(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	projectIDs := listPaneIDs(t, socket, sess.WindowID())
	pids := panePIDs(t, socket, projectIDs)
	heightBefore := paneInt(t, socket, projectIDs[0], "#{pane_height}")

	root := carveFrame(t, frame.Entry{
		Edge:    frame.EdgeTop,
		Size:    frame.Size{N: 3}.MuxSize(),
		Command: sleepArgv,
	})
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame: %v", err)
	}

	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, projectIDs) {
		t.Errorf("panes after hide = %v, want the project's %v", got, projectIDs)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 0 {
		t.Errorf("frame panes after hide = %v, want none", got)
	}
	assertPanesAlive(t, socket, projectIDs, pids, "by the show/hide round trip")
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "" {
		t.Errorf("@cmdman_frame_def = %q, want it cleared", got)
	}
	if got := paneInt(t, socket, projectIDs[0], "#{pane_height}"); got != heightBefore {
		t.Errorf(
			"project pane height = %d, want %d: the region did not expand back",
			got, heightBefore,
		)
	}
}

// TestShowFrame_NoProjectFramesTheDefaultPane pins the show-before-launch case:
// on a window that has had no layout applied, the main region is the pane the
// driver put there by default — it is resized into place and left holding the
// focus, and the first project apply is what replaces it.
func TestShowFrame_NoProjectFramesTheDefaultPane(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	before := listPaneIDs(t, socket, sess.WindowID())
	if len(before) != 1 {
		t.Fatalf("panes on a fresh window = %v, want the default pane alone", before)
	}
	defaultPane := before[0]
	pid := paneFormat(t, socket, defaultPane, "#{pane_pid}")
	widthBefore := paneInt(t, socket, defaultPane, "#{pane_width}")

	root := carveFrame(t, frame.Entry{
		Edge:      frame.EdgeLeft,
		Size:      frame.Size{N: 20}.MuxSize(),
		Component: frame.ComponentSwitcher,
	})
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}

	assertPanesAlive(t, socket, []string{defaultPane}, []string{pid}, "by the show")
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 1 {
		t.Fatalf("frame panes = %v, want the def's one entry", got)
	}
	if got := paneFormat(t, socket, defaultPane, "#{@cmdman_frame}"); got != "" {
		t.Errorf("the default pane was stamped as a frame pane (%q)", got)
	}
	if active := activePaneID(t, socket, sess.WindowID()); active != defaultPane {
		t.Errorf("active pane = %s, want the default pane %s", active, defaultPane)
	}
	if got := paneInt(t, socket, defaultPane, "#{pane_width}"); got >= widthBefore {
		t.Errorf(
			"default pane width = %d, want less than %d: the frame took no room",
			got, widthBefore,
		)
	}
}

// TestShowFrame_TakesFocusOutOfAFramePane covers the branch the ordinary path
// never needs: docking with -d leaves the active pane alone, so focus is already
// in the main region unless a frame pane held it when the show began. Whichever
// way it started, focus must end up in the main region.
func TestShowFrame_TakesFocusOutOfAFramePane(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	stale := dockFramePane(t, socket, sess.WindowID())
	run(t, socket, "select-pane", "-t", stale)
	if got := activePaneID(t, socket, sess.WindowID()); got != stale {
		t.Fatalf("active pane = %s, want the frame pane %s before the show", got, stale)
	}

	root := carveFrame(t, frame.Entry{
		Edge:    frame.EdgeBottom,
		Size:    frame.Size{N: 3}.MuxSize(),
		Command: sleepArgv,
	})
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}

	active := activePaneID(t, socket, sess.WindowID())
	if slices.Contains(framePaneIDs(t, socket, sess.WindowID()), active) {
		t.Errorf("active pane %s is a frame pane after the show", active)
	}
}

// parkFocusOnFramePaneFallback arranges the state tmux falls back from: the
// frame pane is made active first and a doomed project pane second, so killing
// that project pane in a reset lands the active pane on the frame. It returns
// the frame pane's id.
//
// The doomed pane must not be the region's first: a reset keeps that one as the
// anchor, so parking the focus there would prove nothing.
func parkFocusOnFramePaneFallback(t *testing.T, socket, windowID string) string {
	t.Helper()
	frameID := dockFramePane(t, socket, windowID)
	project := projectPaneIDs(t, socket, windowID)
	if len(project) < 2 {
		t.Fatalf("need a project pane the reset will kill; have %v", project)
	}
	run(t, socket, "select-pane", "-t", frameID)
	run(t, socket, "select-pane", "-t", project[len(project)-1])
	return frameID
}

// TestApplyLayout_KeepsFocusOutOfFramePanes pins the focus policy on the path
// that has nothing of its own to select: the layout's focus leaf does not fit,
// so it is skipped and never becomes a pane to focus. The reset that began the
// apply killed the pane the user was in, and tmux falls back to the last-active
// pane — the frame pane — which is the landing the driver must not allow.
func TestApplyLayout_KeepsFocusOutOfFramePanes(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 0); err != nil {
		t.Fatalf("first ApplyLayout: %v", err)
	}
	frameID := parkFocusOnFramePaneFallback(t, socket, sess.WindowID())

	// "focus" leads the tree, so PickFocus names it, and the 200-cell sibling
	// leaves it no room in an 80-column test window: the apply realizes only
	// "rest" and selects nothing.
	root := muxctl.PaneSpec{
		Container: muxctl.Container{
			Dir:    muxctl.DirHorizontal,
			Splits: []muxctl.Size{{N: 1}, {N: 200, Absolute: true}},
			Panes: []muxctl.PaneSpec{
				{Leaf: muxctl.Leaf{Name: "focus", Cmd: sleepArgv}},
				{Leaf: muxctl.Leaf{Name: "rest", Cmd: sleepArgv}},
			},
		},
	}
	panes, err := sess.ApplyLayout(ctx, root, 1)
	if err != nil {
		t.Fatalf("second ApplyLayout: %v", err)
	}
	if _, ok := panes["focus"]; ok {
		t.Fatalf("the focus leaf was realized; the case under test needs it skipped")
	}

	active := activePaneID(t, socket, sess.WindowID())
	if active == frameID {
		t.Errorf("active pane is the frame pane %s after the apply", frameID)
	}
	if !slices.Contains(projectPaneIDs(t, socket, sess.WindowID()), active) {
		t.Errorf(
			"active pane %s is not one of the project panes %v",
			active, projectPaneIDs(t, socket, sess.WindowID()),
		)
	}
}

// TestDetach_KeepsFocusOutOfFramePanes is the same guarantee on the teardown
// side, where there is no layout to name a focus at all: collapsing the project
// region kills the pane the focus was in, and the frame the project leaves
// standing must not inherit it. What the user is handed back is the restored
// project pane.
func TestDetach_KeepsFocusOutOfFramePanes(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 0); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	frameID := parkFocusOnFramePaneFallback(t, socket, sess.WindowID())

	if err := sess.Detach(ctx); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	active := activePaneID(t, socket, sess.WindowID())
	if active == frameID {
		t.Errorf("active pane is the frame pane %s after the project teardown", frameID)
	}
	if !slices.Contains(projectPaneIDs(t, socket, sess.WindowID()), active) {
		t.Errorf(
			"active pane %s is not the collapsed project pane %v",
			active, projectPaneIDs(t, socket, sess.WindowID()),
		)
	}
}

// tmuxFailingOn writes a wrapper around the real tmux that fails every
// invocation of one subcommand and execs tmux for the rest. It stands in for
// the ways a show dies partway through — a tmux error, a cancelled context — at
// a point the test can name. The verb is read past the socket flag the executor
// prefixes to every invocation.
func tmuxFailingOn(t *testing.T, verb string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\n" +
		"v=\"$1\"\n" +
		"case \"$v\" in -L|-S) v=\"$3\" ;; esac\n" +
		"if [ \"$v\" = '" + verb + "' ]; then\n" +
		"  echo 'injected failure' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"exec '" + requireTmux(t) + "' \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing tmux: %v", err)
	}
	return path
}

// TestShowFrame_InterruptedShowStrandsNoPanes pins what a half-finished show
// must leave: panes HideFrame can still find. The respawn that ends the show
// fails, so the second entry never reaches the stamping loop at all — and an
// unstamped bar is a project pane to every scan that reads the window, which
// means HideFrame walks past it and the next reset adopts it as the anchor and
// kills the real project panes it sorts ahead of.
func TestShowFrame_InterruptedShowStrandsNoPanes(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()

	srv, err := tmuxctl.Driver{}.Connect(ctx, muxctl.ServerConfig{
		Executable: tmuxFailingOn(t, "respawn-pane"),
		Socket:     socket,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := srv.New(ctx, muxctl.Config{
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := listPaneIDs(t, socket, sess.WindowID())
	if len(before) != 1 {
		t.Fatalf("panes on a fresh window = %v, want the default pane alone", before)
	}

	root := carveFrame(t,
		frame.Entry{
			Edge:    frame.EdgeTop,
			Size:    frame.Size{N: 3}.MuxSize(),
			Command: sleepArgv,
		},
		frame.Entry{
			Edge:      frame.EdgeLeft,
			Size:      frame.Size{N: 20}.MuxSize(),
			Component: frame.ComponentSwitcher,
		},
	)
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err == nil {
		t.Fatal("ShowFrame succeeded; the case under test needs it to die partway")
	}
	// The show got as far as docking both bars before it died: without them the
	// assertion below would pass for the wrong reason.
	if got := listPaneIDs(t, socket, sess.WindowID()); len(got) != 3 {
		t.Fatalf("panes after the failed show = %v, want the main region plus both bars", got)
	}

	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame after the failed show: %v", err)
	}
	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, before) {
		t.Errorf("panes after hide = %v, want the window's original %v", got, before)
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "" {
		t.Errorf("@cmdman_frame_def = %q, want it cleared", got)
	}
}

// TestHideFrame_LeavesAWindowCarryingNoStateAlone pins the no-op the contract
// claims: hiding a frame on a window cmdman never touched must stop before the
// window restore, which turns off a pane-border-status row the user set for
// themselves. [Server.Open] is how a teardown caller reaches such a window
// without mutating it on the way in.
func TestHideFrame_LeavesAWindowCarryingNoStateAlone(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()

	run(t, socket, "new-session", "-d", "-s", "mine", "-n", "shell")
	windowID := run(t, socket, "display-message", "-p", "-t", "mine:shell", "#{window_id}")
	run(t, socket, "set-option", "-w", "-t", windowID, "pane-border-status", "bottom")
	before := listPaneIDs(t, socket, windowID)

	sess, ok, err := newServer(t, socket).Open(ctx, muxctl.Config{WindowID: windowID})
	if err != nil || !ok {
		t.Fatalf("Open: %v (ok=%v)", err, ok)
	}
	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame: %v", err)
	}

	if got := windowOption(t, socket, windowID, "pane-border-status"); got != "bottom" {
		t.Errorf(
			"pane-border-status = %q after hiding a frame that was never shown, want the user's %q",
			got, "bottom",
		)
	}
	if got := listPaneIDs(t, socket, windowID); !slices.Equal(got, before) {
		t.Errorf("panes = %v, want the window's original %v", got, before)
	}
}

// TestHideFrame_RemovesStampedPanesWithNoDefRecorded pins what makes hide a
// recovery and not just a teardown: the panes it removes are the stamped ones,
// so a window left holding a frame it no longer names — the state a teardown
// that died after clearing the def leaves — is cleaned up by hiding again. And
// a show straight after builds its frame around the window that came back,
// rather than stacking one on the leftovers.
func TestHideFrame_RemovesStampedPanesWithNoDefRecorded(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	before := listPaneIDs(t, socket, sess.WindowID())
	showOneBarFrame(t, sess, "dev")
	run(t, socket, "set-option", "-w", "-u", "-t", sess.WindowID(), "@cmdman_frame_def")
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 1 {
		t.Fatalf("frame panes = %v, want the def's one entry standing", got)
	}

	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame on a window recording no def: %v", err)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 0 {
		t.Errorf("frame panes after hide = %v, want none", got)
	}
	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, before) {
		t.Errorf("panes after hide = %v, want the window's original %v", got, before)
	}

	showOneBarFrame(t, sess, "dev")
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 1 {
		t.Errorf("frame panes after the show = %v, want the def's one entry", got)
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q, want %q", got, "dev")
	}
}

// TestHideFrame_ClearsTheDefBeforeKillingPanes pins the order the recovery
// above rests on. A hide that dies mid-teardown must leave stamped panes on a
// window naming no def, which hiding again removes; the opposite order would
// leave a window naming a frame whose panes are gone, and the next show would
// read that as "already shown" and never come back for them.
func TestHideFrame_ClearsTheDefBeforeKillingPanes(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()

	srv, err := tmuxctl.Driver{}.Connect(ctx, muxctl.ServerConfig{
		Executable: tmuxFailingOn(t, "kill-pane"),
		Socket:     socket,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := srv.New(ctx, muxctl.Config{
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := listPaneIDs(t, socket, sess.WindowID())
	showOneBarFrame(t, sess, "dev")

	if err := sess.HideFrame(ctx); err == nil {
		t.Fatal("HideFrame succeeded; the case under test needs it to die partway")
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "" {
		t.Errorf("@cmdman_frame_def = %q after the failed hide, want it already cleared", got)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 1 {
		t.Fatalf("frame panes after the failed hide = %v, want the one that outlived it", got)
	}

	// The recovery, through a tmux that works: [muxctl.Server.Open] is how a
	// teardown caller reaches a window it must not stamp on the way in.
	retry, ok, err := newServer(t, socket).Open(ctx, muxctl.Config{WindowID: sess.WindowID()})
	if err != nil || !ok {
		t.Fatalf("Open: %v (ok=%v)", err, ok)
	}
	if err := retry.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame after the failed one: %v", err)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 0 {
		t.Errorf("frame panes after the second hide = %v, want none", got)
	}
	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, before) {
		t.Errorf("panes after the second hide = %v, want the window's original %v", got, before)
	}
}

// killMainRegion kills every project pane, leaving a window that holds nothing
// but its frame — what a framed window becomes when the viewers it was built
// around exit. Returns the frame pane ids that were left standing.
func killMainRegion(t *testing.T, socket, windowID string) []string {
	t.Helper()
	for _, id := range projectPaneIDs(t, socket, windowID) {
		run(t, socket, "kill-pane", "-t", id)
	}
	if got := projectPaneIDs(t, socket, windowID); len(got) != 0 {
		t.Fatalf("project panes after killing the main region = %v, want none", got)
	}
	return framePaneIDs(t, socket, windowID)
}

// showOneBarFrame shows a one-entry frame around whatever the window holds.
func showOneBarFrame(t *testing.T, sess muxctl.Session, defName string) {
	t.Helper()
	root := carveFrame(t, frame.Entry{
		Edge:    frame.EdgeTop,
		Size:    frame.Size{N: 3}.MuxSize(),
		Command: sleepArgv,
	})
	if err := sess.ShowFrame(context.Background(), root, mainPaneName, defName); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
}

// TestApplyLayout_AfterTheMainRegionExited pins the other half of the
// show-before-launch seam: a frame outlives the project it surrounded, so the
// window it keeps open holds frame panes and nothing else. The next apply has
// no project pane to anchor on and must make one rather than refuse — chrome is
// the fixture, projects arrive and leave inside it.
func TestApplyLayout_AfterTheMainRegionExited(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	showOneBarFrame(t, sess, "dev")
	framed := killMainRegion(t, socket, sess.WindowID())
	if len(framed) != 1 {
		t.Fatalf("frame panes = %v, want the def's one entry", framed)
	}
	pids := panePIDs(t, socket, framed)

	panes, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 2)
	if err != nil {
		t.Fatalf("ApplyLayout on a window holding only its frame: %v", err)
	}
	if got := sortedKeys(panes); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("panes = %v, want the layout's [a b]", got)
	}

	// The frame is untouched by the apply that had to build a region for itself.
	if got := framePaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, framed) {
		t.Errorf("frame panes after the apply = %v, want the standing %v", got, framed)
	}
	assertPanesAlive(t, socket, framed, pids, "by the apply")
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q, want dev", got)
	}

	// And the layout landed in the region the apply spawned, not on the frame.
	project := projectPaneIDs(t, socket, sess.WindowID())
	for _, name := range []string{"a", "b"} {
		id := panes[name].PaneId()
		if !slices.Contains(project, id) {
			t.Errorf("layout pane %q (%s) is not a project pane: %v", name, id, project)
		}
	}
	if active := activePaneID(t, socket, sess.WindowID()); slices.Contains(framed, active) {
		t.Errorf("active pane %s is a frame pane after the apply", active)
	}
	if got := markerValue(t, sess); got != 2 {
		t.Errorf("Marker = %d, want the applied 2", got)
	}
}

// markerValue reads the window's marker. The apply it follows has already
// returned, so unlike waitForMarker there is nothing left to poll for.
func markerValue(t *testing.T, sess muxctl.Session) int {
	t.Helper()
	stat, err := sess.StatWindow(context.Background(), sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	return stat.Marker
}

// TestShowFrame_AfterTheMainRegionExited is the same state reached from the
// frame side: selecting or cycling a frame is hide-then-show, and the show half
// runs against a window whose main region has gone. It gets the driver's
// default pane, exactly as a show on a window that never had a layout does.
func TestShowFrame_AfterTheMainRegionExited(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	showOneBarFrame(t, sess, "dev")
	killMainRegion(t, socket, sess.WindowID())

	showOneBarFrame(t, sess, "ops")

	project := projectPaneIDs(t, socket, sess.WindowID())
	if len(project) != 1 {
		t.Fatalf("project panes after the second show = %v, want the spawned main region", project)
	}
	if got := paneFormat(t, socket, project[0], "#{pane_dead}"); got != "0" {
		t.Errorf("the spawned main region %s is dead", project[0])
	}
	if active := activePaneID(t, socket, sess.WindowID()); active != project[0] {
		t.Errorf("active pane = %s, want the main region %s", active, project[0])
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "ops" {
		t.Errorf("@cmdman_frame_def = %q, want ops", got)
	}
}

// TestShowFrame_DocksTrailingEntriesAtTheirEdges covers the placement the
// leading entries never exercise: a bottom or right entry trails the main
// region in document order, so it is docked without -b and must land against
// the far edge rather than the near one. The nesting still holds — the bottom
// entry divides the window and the right entry divides only what is left above
// it.
func TestShowFrame_DocksTrailingEntriesAtTheirEdges(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	root := carveFrame(t,
		frame.Entry{
			Edge:    frame.EdgeBottom,
			Size:    frame.Size{N: 3}.MuxSize(),
			Command: sleepArgv,
		},
		frame.Entry{
			Edge:      frame.EdgeRight,
			Size:      frame.Size{N: 20}.MuxSize(),
			Component: frame.ComponentSwitcher,
		},
	)
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}

	windowW := paneInt(t, socket, sess.WindowID(), "#{window_width}")
	windowH := paneInt(t, socket, sess.WindowID(), "#{window_height}")
	bottomID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(0))
	rightID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(1))

	if got := paneInt(t, socket, bottomID, "#{pane_bottom}"); got != windowH-1 {
		t.Errorf("bottom entry ends at row %d, want the window's last row %d", got, windowH-1)
	}
	if got := paneInt(t, socket, bottomID, "#{pane_left}"); got != 0 {
		t.Errorf("bottom entry starts at column %d, want 0: it spans the window", got)
	}
	if got := paneInt(t, socket, bottomID, "#{pane_right}"); got != windowW-1 {
		t.Errorf("bottom entry ends at column %d, want the window's last %d", got, windowW-1)
	}
	if got := paneInt(t, socket, bottomID, "#{pane_height}"); got != 3 {
		t.Errorf("bottom entry height = %d, want the entry's 3", got)
	}

	if got := paneInt(t, socket, rightID, "#{pane_right}"); got != windowW-1 {
		t.Errorf("right entry ends at column %d, want the window's last %d", got, windowW-1)
	}
	if got := paneInt(t, socket, rightID, "#{pane_left}"); got == 0 {
		t.Error("right entry starts at column 0: it was docked at the left edge")
	}
	if got := paneInt(t, socket, rightID, "#{pane_width}"); got != 20 {
		t.Errorf("right entry width = %d, want the entry's 20", got)
	}
	// The right entry divides the rectangle the bottom entry left over, so it
	// stops above it rather than running to the window's floor.
	bottomTop := paneInt(t, socket, bottomID, "#{pane_top}")
	if got := paneInt(t, socket, rightID, "#{pane_bottom}"); got >= bottomTop {
		t.Errorf(
			"right entry reaches row %d, want it above the bottom entry's %d",
			got, bottomTop,
		)
	}

	project := projectPaneIDs(t, socket, sess.WindowID())
	if len(project) != 1 {
		t.Fatalf("project panes = %v, want the default pane alone", project)
	}
	if got := paneInt(t, socket, project[0], "#{pane_right}"); got >= paneInt(
		t, socket, rightID, "#{pane_left}",
	) {
		t.Error("the main region runs into the right entry")
	}
}

// TestShowFrame_PercentEntryResolvesAgainstTheRemainder pins D30 where it is
// realized rather than planned: a percentage entry is a percentage of the
// rectangle its predecessors left over, not of the window. The 12-cell entry
// takes half the window first, so 50% of the remainder is visibly smaller than
// the 50% of the window the same def would mean under the other reading.
func TestShowFrame_PercentEntryResolvesAgainstTheRemainder(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	const topCells = 12
	root := carveFrame(t,
		frame.Entry{
			Edge:    frame.EdgeTop,
			Size:    frame.Size{N: topCells}.MuxSize(),
			Command: sleepArgv,
		},
		frame.Entry{
			Edge:    frame.EdgeBottom,
			Size:    frame.Size{N: 50, Percent: true}.MuxSize(),
			Command: sleepArgv,
		},
	)
	if err := sess.ShowFrame(ctx, root, mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}

	windowH := paneInt(t, socket, sess.WindowID(), "#{window_height}")
	topID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(0))
	percentID := paneIDByTitle(t, socket, sess.WindowID(), frame.EntryPaneName(1))

	// The entry's cells are what the split reserved; the pane reports one row
	// less when the border row it was given lands inside it (the pre-existing
	// pane-border-status sizing quirk, not this case's subject).
	topHeight := paneInt(t, socket, topID, "#{pane_height}")
	if topHeight != topCells && topHeight != topCells-1 {
		t.Fatalf("top entry height = %d, want the entry's %d", topHeight, topCells)
	}
	got := paneInt(t, socket, percentID, "#{pane_height}")
	if got < 1 {
		t.Fatalf("percent entry height = %d: it was never realized", got)
	}
	// The reading this rules out: 50% of the window is at least what the
	// 12-cell entry ahead of it took, so a percent entry that is not smaller
	// than its predecessor resolved against the wrong rectangle. The realized
	// height is smaller still than the planned 50% of the remainder, because
	// the outer entry is docked last and compresses what it encloses; the exact
	// arithmetic is pinned on the planner in frame_internal_test.go.
	if got >= topHeight {
		t.Errorf(
			"percent entry height = %d, not smaller than the %d-row entry ahead of it: "+
				"it resolved against the whole %d-row window",
			got, topHeight, windowH,
		)
	}
	// The main region survives between them: under percent-of-window the two
	// entries alone would claim the whole window.
	project := projectPaneIDs(t, socket, sess.WindowID())
	if len(project) != 1 {
		t.Fatalf("project panes = %v, want the default pane alone", project)
	}
	if got := paneInt(t, socket, project[0], "#{pane_height}"); got < 1 {
		t.Errorf("main region height = %d: the frame left it no room", got)
	}
}

// TestStatWindow_FramePanesDoNotVoteOnMarker pins the marker semantics that
// layout cycling reads: only project panes vote, so a framed window still
// reports the layout it is showing instead of the -1 that snaps cycling back to
// the first layout. The frame pane is docked last so tmux lists it after the
// marked panes, which is the order that used to break consistency.
func TestStatWindow_FramePanesDoNotVoteOnMarker(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	ctx := context.Background()
	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 3); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	ids := listPaneIDs(t, socket, sess.WindowID())
	frameID := run(
		t, socket, "split-window", "-v", "-d", "-l", "3",
		"-t", ids[len(ids)-1], "-P", "-F", "#{pane_id}",
	)
	run(t, socket, "set-option", "-p", "-t", frameID, "@cmdman_frame", "1")

	stat, err := sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != 3 {
		t.Errorf("Marker = %d, want 3 (unmarked frame pane must not break the vote)", stat.Marker)
	}
	// PaneNames stays a report of what the window holds, frame panes included.
	if len(stat.PaneNames) != 3 {
		t.Errorf("PaneNames = %v, want one entry per pane in the window", stat.PaneNames)
	}

	// Exclusion keys on the frame stamp, not on the frame pane happening to
	// carry no marker of its own.
	run(t, socket, "set-option", "-p", "-t", frameID, "@cmdman_marker", "9")
	stat, err = sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow after stale marker: %v", err)
	}
	if stat.Marker != 3 {
		t.Errorf("Marker = %d, want 3 (a frame pane's marker must not vote)", stat.Marker)
	}
}

// TestStatWindow_ForeignPanesDoNotVoteOnMarker pins that a pane cmdman never
// stamped — a shell the user split off, a floating pane a plugin joined into
// the dashboard window — neither supplies a marker nor breaks agreement, while
// a project pane (leaf-stamped) that lost its marker still does.
func TestStatWindow_ForeignPanesDoNotVoteOnMarker(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	ctx := context.Background()
	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 3); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	ids := listPaneIDs(t, socket, sess.WindowID())
	foreignID := run(
		t, socket, "split-window", "-v", "-d", "-l", "3",
		"-t", ids[len(ids)-1], "-P", "-F", "#{pane_id}",
	)

	stat, err := sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != 3 {
		t.Errorf(
			"Marker = %d, want 3 (an unstamped foreign pane must not break the vote)",
			stat.Marker,
		)
	}
	if len(stat.PaneNames) != 3 {
		t.Errorf("PaneNames = %v, want one entry per pane in the window", stat.PaneNames)
	}

	// A leaf stamp makes the pane a project pane again; its missing marker
	// now disagrees with the others.
	run(t, socket, "set-option", "-p", "-t", foreignID, "@cmdman_leaf", "web")
	stat, err = sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow after leaf stamp: %v", err)
	}
	if stat.Marker != -1 {
		t.Errorf(
			"Marker = %d, want -1 (a leaf-stamped pane without a marker breaks the vote)",
			stat.Marker,
		)
	}

	// Order must not matter: the same unmarked project pane listed before
	// every marked one breaks the vote just the same.
	run(t, socket, "kill-pane", "-t", foreignID)
	firstID := run(
		t, socket, "split-window", "-v", "-d", "-b", "-l", "3",
		"-t", ids[0], "-P", "-F", "#{pane_id}",
	)
	run(t, socket, "set-option", "-p", "-t", firstID, "@cmdman_leaf", "web")
	stat, err = sess.StatWindow(ctx, sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow with leading unmarked pane: %v", err)
	}
	if stat.Marker != -1 {
		t.Errorf(
			"Marker = %d, want -1 (an unmarked project pane listed first breaks the vote)",
			stat.Marker,
		)
	}
}
