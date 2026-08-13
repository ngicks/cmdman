package mux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// sleepLeaf is a viewer stand-in that stays alive, so "still running"
// assertions mean something.
func sleepLeaf(name string) muxctl.PaneSpec {
	return muxctl.PaneSpec{Leaf: muxctl.Leaf{
		Name: name,
		Cmd:  []string{"/bin/sh", "-c", "sleep 60"},
	}}
}

// frameTestSession names the session every frame test builds its window in; the
// frame verbs address that window by being the session's current one.
const frameTestSession = "framed"

// projectWindow builds a cmdman-owned window holding a two-pane project layout
// and returns the socket plus the window id. It drives the driver directly
// (test files are exempt from the mux → muxctl-only invariant).
//
// The window is left as its session's current one: the frame verbs target the
// window the user is sitting in, and `new-window -d` (which the driver builds
// with) does not select what it creates.
func projectWindow(t *testing.T, identity string) (socket, windowID string) {
	t.Helper()
	bin := tmuxOrSkip(t)
	socket = "cmdman-mux-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() })

	ctx := context.Background()
	server, err := tmux.Driver{}.Connect(ctx, muxctl.ServerConfig{Socket: socket})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := server.New(ctx, muxctl.Config{
		SessionName:      frameTestSession,
		WindowName:       "dash",
		OwnedIdentity:    identity,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	project := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirHorizontal,
		Splits: []muxctl.Size{{N: 1}, {N: 1}},
		Panes:  []muxctl.PaneSpec{sleepLeaf("web"), sleepLeaf("worker")},
	}}
	if _, err := sess.ApplyLayout(ctx, project, 0); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	if err := server.FocusWindow(ctx, sess.WindowID(), muxctl.FocusOptions{}); err != nil {
		t.Fatalf("FocusWindow: %v", err)
	}
	return socket, sess.WindowID()
}

// framedWindow is [projectWindow] with a one-entry frame already docked around
// it, driven through the driver rather than the verbs so the read-back tests
// below pin what a frame looks like independently of who put it up.
func framedWindow(t *testing.T, identity, defName string) (socket, windowID string) {
	t.Helper()
	socket, windowID = projectWindow(t, identity)

	ctx := context.Background()
	server, err := tmux.Driver{}.Connect(ctx, muxctl.ServerConfig{Socket: socket})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := server.New(ctx, muxctl.Config{
		WindowID:         windowID,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The shape frame.Spec.Carve produces: the entry leads, the main
	// placeholder stands for the panes that already exist.
	frameTree := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirVertical,
		Splits: []muxctl.Size{{N: 3, Absolute: true}, {N: 1}},
		Panes: []muxctl.PaneSpec{
			sleepLeaf("frame-0"),
			{Leaf: muxctl.Leaf{Name: "main"}},
		},
	}}
	if err := sess.ShowFrame(ctx, frameTree, "main", defName); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
	return socket, windowID
}

// windowFormat expands a tmux format against the window, which yields "" for an
// option that was never set instead of failing.
func windowFormat(t *testing.T, bin, socket, windowID, format string) string {
	t.Helper()
	return tmuxRun(t, bin, socket, "display-message", "-p", "-t", windowID, format)
}

// framePaneCount counts the window's frame-stamped panes.
func framePaneCount(t *testing.T, bin, socket, windowID string) int {
	t.Helper()
	return len(framePaneIDs(t, bin, socket, windowID))
}

// framePaneIDs returns the ids of the window's frame-stamped panes. The ids
// tell a frame that was left alone from one that was torn down and rebuilt —
// a killed pane never comes back under its old id.
func framePaneIDs(t *testing.T, bin, socket, windowID string) []string {
	t.Helper()
	out := tmuxRun(
		t, bin, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{@cmdman_frame}",
	)
	var ids []string
	for line := range strings.SplitSeq(out, "\n") {
		if id, stamp, _ := strings.Cut(line, "\t"); stamp != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestList_ReportsTheFrameOnAFramedWindow pins what `mux ls` gains: the row for
// a framed window answers for its project exactly as before and carries the
// name of the frame around it, with no extra query per window.
func TestList_ReportsTheFrameOnAFramedWindow(t *testing.T) {
	const identity = "abc123-myproject"
	socket, windowID := framedWindow(t, identity, "dev")

	rows, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %v, want the one framed window", rows)
	}
	if rows[0].WindowID != windowID {
		t.Errorf("WindowID = %q, want %q", rows[0].WindowID, windowID)
	}
	if rows[0].Identity != identity {
		t.Errorf("Identity = %q, want %q", rows[0].Identity, identity)
	}
	if rows[0].Frame != "dev" {
		t.Errorf("Frame = %q, want dev", rows[0].Frame)
	}
	if rows[0].Marker != 0 {
		t.Errorf("Marker = %d, want the applied 0: a frame pane must not break the vote",
			rows[0].Marker)
	}
}

// TestDown_OnAFramedWindowLeavesTheFrame is the mux-level reading of per-side
// teardown: `mux down` finds a framed window by the project identity it has
// always used, tears the project down, and leaves the frame standing and
// discoverable — chrome outlives the projects that arrive inside it.
func TestDown_OnAFramedWindowLeavesTheFrame(t *testing.T) {
	const identity = "abc123-myproject"
	socket, windowID := framedWindow(t, identity, "dev")
	bin := tmuxOrSkip(t)

	var out bytes.Buffer
	if err := Down(context.Background(), DownOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
		Stdout:   &out,
	}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !strings.Contains(out.String(), "Restored window") {
		t.Errorf("Down printed %q, want a restored-window line", out.String())
	}

	if got := framePaneCount(t, bin, socket, windowID); got != 1 {
		t.Errorf("frame panes after down = %d, want the frame's one still up", got)
	}
	if got := windowFormat(t, bin, socket, windowID, "#{@cmdman_frame_def}"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q after down, want dev", got)
	}
	if got := windowFormat(t, bin, socket, windowID, "#{@cmdman_window}"); got != "" {
		t.Errorf("@cmdman_window = %q after down, want it cleared", got)
	}

	// The window the project left behind is still cmdman's: the frame answers
	// for it, so the frame verbs can find it with no project to name it by.
	rows, err := List(context.Background(), ListOptions{
		Driver: muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Env:    []string{},
	})
	if err != nil {
		t.Fatalf("List after down: %v", err)
	}
	if len(rows) != 1 || rows[0].WindowID != windowID {
		t.Fatalf("List after down returned %v, want the framed window %s", rows, windowID)
	}
	if rows[0].Identity != "" || rows[0].Frame != "dev" {
		t.Errorf("row after down = {Identity:%q Frame:%q}, want {\"\" \"dev\"}",
			rows[0].Identity, rows[0].Frame)
	}

	// And the project's own query no longer matches it.
	mine, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List by identity after down: %v", err)
	}
	if len(mine) != 0 {
		t.Errorf("identity %q still matches %v after down", identity, mine)
	}
}

// ---- frame verbs ------------------------------------------------------------

// frameDefContent is the def the verb tests show: a two-row top bar running a
// long sleep. Two rows rather than one because a 1-row bar realizes at 0 usable
// rows under pane-border-status top (the driver's known off-by-one).
const frameDefContent = "frame:\n" +
	"  - edge: top\n" +
	"    size: 2\n" +
	"    command: [\"/bin/sh\", \"-c\", \"sleep 60\"]\n"

// frameDefs points $CMDMAN_CONF at a temp config file — so the frame dir is its
// sibling, exactly as config.FrameConfigDir resolves it — and writes one def
// per name there. It returns the frame dir.
func frameDefs(t *testing.T, names ...string) string {
	t.Helper()
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	dir := filepath.Join(conf, "frame")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range names {
		writeFrameDef(t, dir, name, frameDefContent)
	}
	return dir
}

// writeFrameDef writes one def file and returns its path.
func writeFrameDef(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// frameOpts is what every verb test drives: the per-test socket and the session
// holding the window. The tests run outside tmux, so the session is how the
// verbs find the window the user would otherwise be sitting in.
func frameOpts(socket string) FrameOptions {
	return FrameOptions{
		Driver:  muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Session: frameTestSession,
		Env:     []string{},
	}
}

// shownDef reads back the def name stamped on the window.
func shownDef(t *testing.T, bin, socket, windowID string) string {
	t.Helper()
	return windowFormat(t, bin, socket, windowID, "#{@cmdman_frame_def}")
}

// projectPaneTitles returns the titles of the window's unstamped panes — the
// project region, which no frame verb may disturb.
//
// The pane id leads the format although nothing reads it: the runner trims the
// whole output, so a format starting with the (empty) frame stamp would lose
// the first pane's leading tab and read its title as the stamp.
func projectPaneTitles(t *testing.T, bin, socket, windowID string) []string {
	t.Helper()
	out := tmuxRun(
		t, bin, socket, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{@cmdman_frame}\t#{pane_title}",
	)
	var titles []string
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) == 3 && parts[1] == "" {
			titles = append(titles, parts[2])
		}
	}
	slices.Sort(titles)
	return titles
}

// TestFrameShow_DefResolution pins the resolution order D15 asks for: a named
// def wins, config default_frame answers when nothing is named, and with
// neither set the error names the defs the user could have meant.
//
// The subtest names stay short: each one builds a tmux server on a socket named
// after the test, and the socket path has to fit in sockaddr_un.
func TestFrameShow_DefResolution(t *testing.T) {
	t.Run("named def wins", func(t *testing.T) {
		frameDefs(t, "alt", "dev")
		socket, windowID := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		opts := frameOpts(socket)
		opts.Def = "alt"
		opts.Config.DefaultFrame = "dev"
		if err := FrameShow(context.Background(), opts); err != nil {
			t.Fatalf("FrameShow: %v", err)
		}
		if got := shownDef(t, bin, socket, windowID); got != "alt" {
			t.Errorf("@cmdman_frame_def = %q, want the named alt", got)
		}
	})

	t.Run("config default answers", func(t *testing.T) {
		frameDefs(t, "alt", "dev")
		socket, windowID := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		opts := frameOpts(socket)
		opts.Config.DefaultFrame = "dev"
		if err := FrameShow(context.Background(), opts); err != nil {
			t.Fatalf("FrameShow: %v", err)
		}
		if got := shownDef(t, bin, socket, windowID); got != "dev" {
			t.Errorf("@cmdman_frame_def = %q, want the configured dev", got)
		}
		if got := framePaneCount(t, bin, socket, windowID); got != 1 {
			t.Errorf("frame panes = %d, want the def's one entry docked", got)
		}
	})

	t.Run("neither lists candidates", func(t *testing.T) {
		frameDefs(t, "alt", "dev")

		err := FrameShow(context.Background(), FrameOptions{Env: []string{}})
		if err == nil {
			t.Fatal("FrameShow with no def and no default returned nil")
		}
		for _, want := range []string{"default_frame", "alt", "dev"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

// TestFrameShow_UnresolvableDef pins the failure experience IDEA.md's
// resolution flowchart asks for: a def that resolves to no file is reported
// with what was tried, and — when it was a bare name, the form discovery
// answers for — with the defs the user could have meant.
//
// None of these subtests reaches a multiplexer: resolution fails before a
// window is opened, which is also why a broken def never disturbs a frame
// already standing.
func TestFrameShow_UnresolvableDef(t *testing.T) {
	showErr := func(t *testing.T, def string) error {
		t.Helper()
		opts := FrameOptions{Env: []string{}}
		opts.Def = def
		err := FrameShow(context.Background(), opts)
		if err == nil {
			t.Fatalf("FrameShow with the unresolvable def %q returned nil", def)
		}
		return err
	}

	t.Run("bare name lists candidates", func(t *testing.T) {
		frameDefs(t, "alt", "dev")
		cwd, wderr := os.Getwd()
		if wderr != nil {
			t.Fatalf("Getwd: %v", wderr)
		}

		err := showErr(t, "nope")
		wants := []string{`"nope"`, filepath.Join(cwd, "nope"), "available defs: alt, dev"}
		for _, want := range wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		// The open failure names its own path; a wrapper repeating it read as
		// "open <path>: open <path>: …".
		if got := strings.Count(err.Error(), "open "); got != 1 {
			t.Errorf("error %q names the open %d times, want once", err, got)
		}
	})

	t.Run("bare name in an empty dir names the dir", func(t *testing.T) {
		dir := frameDefs(t)

		err := showErr(t, "nope")
		for _, want := range []string{"no defs found", dir} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	// An explicit path is the user's own pointer: the defs on disk are not what
	// they meant, so listing them would be noise.
	t.Run("path names only the path", func(t *testing.T) {
		frameDefs(t, "alt", "dev")
		path := filepath.Join(t.TempDir(), "missing.yaml")

		err := showErr(t, path)
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name the path tried", err)
		}
		if strings.Contains(err.Error(), "available defs") {
			t.Errorf("error %q lists candidates for an explicit path", err)
		}
	})

	// A def that exists and fails to parse is not a discovery failure: the
	// candidate list would read as "no such def" and bury the parse error.
	t.Run("broken def is not a discovery failure", func(t *testing.T) {
		dir := frameDefs(t, "alt")
		writeFrameDef(t, dir, "bad", "frame:\n  - edge: [\n")

		err := showErr(t, "bad")
		if !strings.Contains(err.Error(), "yaml:") {
			t.Errorf("error %q does not report the parse failure", err)
		}
		if strings.Contains(err.Error(), "available defs") {
			t.Errorf("error %q lists candidates for a def that exists", err)
		}
	})
}

// TestFrameShow_SameDefIsANoOp: the driver stacks a second dock around the
// first on a repeated ShowFrame, so recognizing the def already up is the
// verb's job — a re-run of the same command must leave one frame, not two.
func TestFrameShow_SameDefIsANoOp(t *testing.T) {
	frameDefs(t, "dev")
	socket, windowID := projectWindow(t, "abc123-myproject")
	bin := tmuxOrSkip(t)

	ctx := context.Background()
	opts := frameOpts(socket)
	opts.Def = "dev"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow: %v", err)
	}
	before := framePaneIDs(t, bin, socket, windowID)
	if len(before) != 1 {
		t.Fatalf("frame panes after the first show = %v, want the def's one entry", before)
	}
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("second FrameShow: %v", err)
	}

	// Same panes, not merely the same count: a repeat that hid and re-showed
	// would pass a count check while flickering the frame and killing whatever
	// its panes were running.
	if after := framePaneIDs(t, bin, socket, windowID); !slices.Equal(after, before) {
		t.Errorf("frame panes = %v after re-showing dev, want the untouched %v", after, before)
	}
	if got := shownDef(t, bin, socket, windowID); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q, want dev", got)
	}
}

// TestFrameShow_OtherDefReplacesInPlace is V2: showing a def while another is
// up replaces it — one frame at a time — and the project region underneath is
// none of the verb's business.
func TestFrameShow_OtherDefReplacesInPlace(t *testing.T) {
	frameDefs(t, "alt", "dev")
	socket, windowID := projectWindow(t, "abc123-myproject")
	bin := tmuxOrSkip(t)

	ctx := context.Background()
	opts := frameOpts(socket)
	opts.Def = "dev"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow dev: %v", err)
	}
	opts.Def = "alt"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow alt: %v", err)
	}

	if got := shownDef(t, bin, socket, windowID); got != "alt" {
		t.Errorf("@cmdman_frame_def = %q, want the replacing alt", got)
	}
	if got := framePaneCount(t, bin, socket, windowID); got != 1 {
		t.Errorf("frame panes after the replace = %d, want one: show must never stack", got)
	}
	if got := projectPaneTitles(t, bin, socket, windowID); !slices.Equal(
		got, []string{"web", "worker"},
	) {
		t.Errorf("project panes = %v, want [web worker] untouched by the replace", got)
	}
}

// TestFrameHide covers both halves of hide's contract: it takes down the frame
// that is up, and it is a quiet no-op on a window that carries none.
func TestFrameHide(t *testing.T) {
	t.Run("unframed window is left alone", func(t *testing.T) {
		socket, windowID := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		if err := FrameHide(context.Background(), frameOpts(socket)); err != nil {
			t.Fatalf("FrameHide on an unframed window: %v", err)
		}
		if got := framePaneCount(t, bin, socket, windowID); got != 0 {
			t.Errorf("frame panes = %d, want none", got)
		}
		if got := windowFormat(t, bin, socket, windowID, "#{@cmdman_window}"); got !=
			"abc123-myproject" {
			t.Errorf("@cmdman_window = %q, want the project's stamp left alone", got)
		}
		if got := projectPaneTitles(t, bin, socket, windowID); !slices.Equal(
			got, []string{"web", "worker"},
		) {
			t.Errorf("project panes = %v, want [web worker] untouched", got)
		}
	})

	t.Run("a shown frame comes down", func(t *testing.T) {
		frameDefs(t, "dev")
		socket, windowID := projectWindow(t, "abc123-myproject")
		bin := tmuxOrSkip(t)

		ctx := context.Background()
		opts := frameOpts(socket)
		opts.Def = "dev"
		if err := FrameShow(ctx, opts); err != nil {
			t.Fatalf("FrameShow: %v", err)
		}
		if err := FrameHide(ctx, frameOpts(socket)); err != nil {
			t.Fatalf("FrameHide: %v", err)
		}

		if got := framePaneCount(t, bin, socket, windowID); got != 0 {
			t.Errorf("frame panes after hide = %d, want none", got)
		}
		if got := shownDef(t, bin, socket, windowID); got != "" {
			t.Errorf("@cmdman_frame_def = %q after hide, want it cleared", got)
		}
		if got := projectPaneTitles(t, bin, socket, windowID); !slices.Equal(
			got, []string{"web", "worker"},
		) {
			t.Errorf("project panes = %v, want [web worker] untouched by hide", got)
		}
	})
}

// TestFrameCycle_WalksSortedDefsAndWraps is V3: the rotation is the sorted def
// list regardless of the order the files were written in, it starts at the
// first def from an unframed window, and it wraps.
func TestFrameCycle_WalksSortedDefsAndWraps(t *testing.T) {
	frameDefs(t, "dev", "alt")
	socket, windowID := projectWindow(t, "abc123-myproject")
	bin := tmuxOrSkip(t)

	ctx := context.Background()
	opts := frameOpts(socket)
	for i, want := range []string{"alt", "dev", "alt"} {
		if err := FrameCycle(ctx, opts); err != nil {
			t.Fatalf("FrameCycle #%d: %v", i+1, err)
		}
		if got := shownDef(t, bin, socket, windowID); got != want {
			t.Errorf("cycle #%d shows %q, want %q", i+1, got, want)
		}
		if got := framePaneCount(t, bin, socket, windowID); got != 1 {
			t.Errorf("cycle #%d left %d frame panes, want the one dock", i+1, got)
		}
	}
}

// TestFrameCycle_BrokenDefIsReported: a def that does not parse is named — with
// the file that failed — rather than skipped, and the frame already standing is
// left alone because the def is read before the window is touched.
func TestFrameCycle_BrokenDefIsReported(t *testing.T) {
	dir := frameDefs(t, "alt")
	badPath := writeFrameDef(
		t, dir, "bad",
		"frame:\n  - edge: sideways\n    size: 2\n    command: [\"true\"]\n",
	)
	socket, windowID := projectWindow(t, "abc123-myproject")
	bin := tmuxOrSkip(t)

	ctx := context.Background()
	opts := frameOpts(socket)
	if err := FrameCycle(ctx, opts); err != nil {
		t.Fatalf("FrameCycle onto alt: %v", err)
	}

	err := FrameCycle(ctx, opts)
	if err == nil {
		t.Fatal("FrameCycle onto a broken def returned nil")
	}
	if !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("error %q does not name the def", err)
	}
	if !strings.Contains(err.Error(), badPath) {
		t.Errorf("error %q does not name the file %s", err, badPath)
	}
	if got := shownDef(t, bin, socket, windowID); got != "alt" {
		t.Errorf("@cmdman_frame_def = %q after the failed cycle, want alt still standing", got)
	}
}

// TestFrameList_ReportsDefsAndShownWindows is V4: the defs on disk and which
// def is up on which window, the two halves of "what can I show" and "what is
// showing".
func TestFrameList_ReportsDefsAndShownWindows(t *testing.T) {
	frameDefs(t, "alt", "dev")
	socket, windowID := framedWindow(t, "abc123-myproject", "dev")

	res, err := FrameList(context.Background(), frameOpts(socket))
	if err != nil {
		t.Fatalf("FrameList: %v", err)
	}
	if !slices.Equal(res.Defs, []string{"alt", "dev"}) {
		t.Errorf("Defs = %v, want the sorted [alt dev]", res.Defs)
	}
	if len(res.Shown) != 1 || res.Shown[windowID] != "dev" {
		t.Errorf("Shown = %v, want the framed window %s showing dev", res.Shown, windowID)
	}
}
