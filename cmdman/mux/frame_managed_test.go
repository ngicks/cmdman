package mux

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// managedDefContent is a def whose single entry is supervised (D19): the same
// two-row top bar the ephemeral tests use, opted into cmdman supervision and
// carrying the hook override a managed entry may set (D17/V5).
const managedDefContent = "frame:\n" +
	"  - edge: top\n" +
	"    size: 2\n" +
	"    command: [\"/bin/sh\", \"-c\", \"sleep 60\"]\n" +
	"    managed: true\n" +
	"    hooks:\n" +
	"      bell:\n" +
	"        action: block\n"

// managedHooks is what managedDefContent's hooks: block decodes to.
var managedHooks = model.HookSet{
	model.HookEventBell: {Action: model.HookActionBlock},
}

// fakeFrameSvc stands in for cmdman.Service. A real one is out of reach here —
// the cmdman package imports this one — and out of place anyway: starting a
// supervised command re-execs the cmdman binary, which a unit test does not
// have. What it models is the only thing the verbs may assume of the service:
// a command named once keeps its identity, and nothing the frame verbs do can
// end it (the seam has no call that could).
type fakeFrameSvc struct {
	cfg config.Config
	// commands maps a command name to what a listing reports for it, standing
	// for the service's store.
	commands map[string]CommandStatus
	// listed counts the [FrameSvc.ListCommands] calls; created and started
	// record the calls that made and started a command.
	listed  int
	created []CommandSpec
	started []string
	// onList runs inside the listing, which is how the ordering tests observe
	// what the window looks like at that moment. The listing rather than create
	// or start: it is the one call every ensure makes, whichever branch it takes.
	onList func()
}

func newFakeFrameSvc(t *testing.T) *fakeFrameSvc {
	t.Helper()
	dir := t.TempDir()
	return &fakeFrameSvc{
		cfg:      config.Config{DataDir: dir, RuntimeDir: dir},
		commands: map[string]CommandStatus{},
	}
}

func (f *fakeFrameSvc) Config() config.Config { return f.cfg }

// ListCommands answers with every command it holds, in map order: which one
// answers to a name is the caller's to work out, and an answer that arrives
// unordered is what keeps the test honest about that.
func (f *fakeFrameSvc) ListCommands(_ context.Context) ([]CommandStatus, error) {
	f.listed++
	if f.onList != nil {
		f.onList()
	}
	return slices.Collect(maps.Values(f.commands)), nil
}

func (f *fakeFrameSvc) CreateCommand(_ context.Context, spec CommandSpec) (string, error) {
	id := fmt.Sprintf("cmd%d", len(f.created))
	f.created = append(f.created, spec)
	f.commands[spec.Name] = CommandStatus{
		ID:    id,
		Name:  spec.Name,
		State: model.EventTypeCreated,
	}
	return id, nil
}

func (f *fakeFrameSvc) Start(_ context.Context, idOrName string) error {
	f.started = append(f.started, idOrName)
	for name, status := range f.commands {
		if status.ID == idOrName {
			status.State = model.EventTypeRunning
			f.commands[name] = status
		}
	}
	return nil
}

// seed puts a command into the fake's store, as one that was created and left
// in the given state.
func (f *fakeFrameSvc) seed(id, name string, state model.EventType) {
	f.commands[name] = CommandStatus{ID: id, Name: name, State: state}
}

// TestEnsure walks the three branches [ensureFrameCommand] chooses between for
// the command behind a managed entry (V7), and the identity question underneath
// all three: the service answers with everything it holds, so which of those
// commands is the entry's is decided here, by name and nothing else.
//
// Every case seeds a decoy the entry must not take. The verb-level tests below
// enter the machine through the create branch only; the fake service is what
// makes the other two reachable, since putting a real one into either state
// means actually starting a supervised command.
func TestEnsure(t *testing.T) {
	req := frameCommand{
		Name:  "frame-dev-0",
		Argv:  []string{"/bin/sh", "-c", "sleep 60"},
		Hooks: managedHooks,
	}

	t.Run("create", func(t *testing.T) {
		svc := newFakeFrameSvc(t)
		// A running command of another def: nothing answers to this entry's
		// name, so the entry gets one of its own rather than the nearest thing.
		svc.seed("other", "frame-alt-0", model.EventTypeRunning)

		id, err := ensureFrameCommand(context.Background(), svc, req)
		if err != nil {
			t.Fatalf("ensureFrameCommand: %v", err)
		}
		if len(svc.created) != 1 {
			t.Fatalf("commands created = %v, want the entry's one", svc.created)
		}
		got := svc.created[0]
		if got.Name != req.Name {
			t.Errorf("created name = %q, want the entry's %q", got.Name, req.Name)
		}
		if !slices.Equal(got.Argv, req.Argv) {
			t.Errorf("created argv = %v, want the entry's %v", got.Argv, req.Argv)
		}
		// The entry is watched through `cmdman attach` in a pane, so it gets a
		// terminal rather than plain pipes.
		if !got.Tty {
			t.Error("created command has no tty, want one")
		}
		if len(got.Hooks) != 1 ||
			got.Hooks[model.HookEventBell].Action != model.HookActionBlock {
			t.Errorf("created hooks = %v, want the entry's %v", got.Hooks, managedHooks)
		}
		if !slices.Equal(svc.started, []string{id}) {
			t.Errorf("started = %v, want the created %q", svc.started, id)
		}
	})

	t.Run("restart", func(t *testing.T) {
		svc := newFakeFrameSvc(t)
		svc.seed("other", "frame-alt-0", model.EventTypeExited)
		svc.seed("cmd7", req.Name, model.EventTypeExited)

		id, err := ensureFrameCommand(context.Background(), svc, req)
		if err != nil {
			t.Fatalf("ensureFrameCommand: %v", err)
		}
		// The record is the identity: an entry that outlived its process comes
		// back under the same command rather than as a replacement for it.
		if id != "cmd7" {
			t.Errorf("id = %q, want the existing cmd7", id)
		}
		if len(svc.created) != 0 {
			t.Errorf("commands created = %v, want the exited one started again", svc.created)
		}
		if !slices.Equal(svc.started, []string{"cmd7"}) {
			t.Errorf("started = %v, want the existing cmd7", svc.started)
		}
	})

	t.Run("adopt", func(t *testing.T) {
		for _, state := range []model.EventType{
			model.EventTypeRunning,
			model.EventTypeStarting,
		} {
			svc := newFakeFrameSvc(t)
			svc.seed("other", "frame-alt-0", state)
			svc.seed("cmd7", req.Name, state)

			id, err := ensureFrameCommand(context.Background(), svc, req)
			if err != nil {
				t.Fatalf("ensureFrameCommand on a %s command: %v", state, err)
			}
			if id != "cmd7" {
				t.Errorf("id = %q for a %s command, want the running cmd7", id, state)
			}
			// Starting a live command again would error; adopting it means
			// touching nothing but the listing.
			if len(svc.created) != 0 || len(svc.started) != 0 {
				t.Errorf("a %s command was created=%v / started=%v, want it taken as it stands",
					state, svc.created, svc.started)
			}
		}
	})
}

// managedSpec is managedDefContent as a normalized spec, without the file
// detour: the pure tests below are about what the verb layer does with a
// managed entry, not about decoding one.
func managedSpec() frame.Spec {
	return frame.Spec{Entries: []frame.Entry{{
		Edge:    frame.EdgeTop,
		Size:    muxctl.Size{N: 2, Absolute: true},
		Command: []string{"/bin/sh", "-c", "sleep 60"},
		Managed: true,
		Hooks:   managedHooks,
	}}}
}

func carveManaged(t *testing.T, spec frame.Spec) muxctl.PaneSpec {
	t.Helper()
	root, err := spec.Carve(
		muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: frameMainPane}},
		frameComponentArgv("", ""),
	)
	if err != nil {
		t.Fatalf("Carve: %v", err)
	}
	return root
}

// fakeViewer writes a stand-in for the cmdman binary a managed entry's pane
// runs: it ignores the viewer argv and stays alive, which is what an attach
// client does. The real one cannot stand in for itself here — the test binary
// is what os.Executable() resolves to, and it exits the moment tmux runs it
// with a viewer's flags, taking the pane with it.
func fakeViewer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdman")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// leafCmd returns the argv carried by the leaf named name.
func leafCmd(t *testing.T, node muxctl.PaneSpec, name string) []string {
	t.Helper()
	if node.Name == name {
		return node.Cmd
	}
	for _, child := range node.Panes {
		if got := leafCmd(t, child, name); got != nil {
			return got
		}
	}
	return nil
}

// TestManaged_Adopt pins what a managed entry costs the pane it was written
// for: the pane stops running the entry's own command and runs a viewer of the
// supervised command instead, under the identity V7 names it by, with the
// entry's hooks: carried along for the command's own config (D17/V5).
func TestManaged_Adopt(t *testing.T) {
	spec := managedSpec()
	root := carveManaged(t, spec)
	svc := newFakeFrameSvc(t)
	opts := FrameOptions{Svc: svc}

	if err := adoptManagedEntries(context.Background(), opts, "dev", spec, &root); err != nil {
		t.Fatalf("adoptManagedEntries: %v", err)
	}

	if svc.listed != 1 {
		t.Fatalf("ListCommands calls = %d, want the def's one managed entry", svc.listed)
	}
	if len(svc.created) != 1 {
		t.Fatalf("commands created = %v, want the def's one managed entry", svc.created)
	}
	got := svc.created[0]
	if got.Name != "frame-dev-0" {
		t.Errorf("command name = %q, want frame-dev-0 (V7: def name + entry index)", got.Name)
	}
	if !slices.Equal(got.Argv, []string{"/bin/sh", "-c", "sleep 60"}) {
		t.Errorf("argv = %v, want the entry's command", got.Argv)
	}
	if len(got.Hooks) != 1 ||
		got.Hooks[model.HookEventBell].Action != model.HookActionBlock {
		t.Errorf("hooks = %v, want the entry's %v", got.Hooks, managedHooks)
	}

	// The pane views the command; it does not run it a second time.
	pane := leafCmd(t, root, frame.EntryPaneName(0))
	want := []string{
		frameExecutable(""),
		"--data-dir", svc.cfg.DataDir,
		"--runtime-dir", svc.cfg.RuntimeDir,
		"attach", "cmd0",
	}
	if !slices.Equal(pane, want) {
		t.Errorf("frame-0 argv = %v, want the viewer %v", pane, want)
	}
}

// TestManaged_Untouched: an entry that never asked for supervision keeps its
// pane and never reaches the service, so a def of components and ephemeral
// commands works with no cmdman behind it at all — and one that does ask says
// so by name rather than quietly running unsupervised.
func TestManaged_Untouched(t *testing.T) {
	t.Run("ephemeral", func(t *testing.T) {
		spec := managedSpec()
		spec.Entries[0].Managed = false
		root := carveManaged(t, spec)
		before := slices.Clone(leafCmd(t, root, frame.EntryPaneName(0)))

		err := adoptManagedEntries(context.Background(), FrameOptions{}, "dev", spec, &root)
		if err != nil {
			t.Fatalf("adoptManagedEntries with no service: %v", err)
		}
		if after := leafCmd(t, root, frame.EntryPaneName(0)); !slices.Equal(after, before) {
			t.Errorf("frame-0 argv = %v, want the entry's own %v", after, before)
		}
	})

	t.Run("no service", func(t *testing.T) {
		spec := managedSpec()
		root := carveManaged(t, spec)

		err := adoptManagedEntries(context.Background(), FrameOptions{}, "dev", spec, &root)
		if err == nil {
			t.Fatal("adoptManagedEntries with a managed entry and no service returned nil")
		}
		for _, want := range []string{`"dev"`, "entry 0", "managed"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

// managedFrame writes a def per name (all managed) and returns the socket and
// window id of a project window to show them around.
func managedFrame(t *testing.T, names ...string) (socket, windowID string) {
	t.Helper()
	dir := frameDefs(t)
	for _, name := range names {
		writeFrameDef(t, dir, name, managedDefContent)
	}
	return projectWindow(t, "abc123-myproject")
}

// managedOpts is [frameOpts] with the two halves a managed entry needs: the
// service its command comes from, and a viewer binary that survives being run
// (see [fakeViewer]).
func managedOpts(t *testing.T, socket string, svc FrameSvc) FrameOptions {
	t.Helper()
	opts := frameOpts(socket)
	opts.Svc = svc
	opts.Executable = fakeViewer(t)
	return opts
}

// paneStartCommands returns the commands the window's frame panes were started
// with, which is where the viewer substitution shows up in the running window.
func paneStartCommands(t *testing.T, bin, socket, windowID string) []string {
	t.Helper()
	out := tmuxRun(
		t, bin, socket, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{@cmdman_frame}\t#{pane_start_command}",
	)
	var cmds []string
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) == 3 && parts[1] != "" {
			cmds = append(cmds, parts[2])
		}
	}
	return cmds
}

// TestManaged_Hide is D19's whole point: the frame comes down and the command
// behind its managed entry does not. Hiding takes nothing to the service —
// there is no call on the seam that could stop a command — so the supervised
// side of the window is exactly as the show left it.
func TestManaged_Hide(t *testing.T) {
	socket, windowID := managedFrame(t, "dev")
	bin := tmuxOrSkip(t)
	svc := newFakeFrameSvc(t)

	ctx := context.Background()
	opts := managedOpts(t, socket, svc)
	opts.Def = "dev"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow: %v", err)
	}
	if got := framePaneCount(t, bin, socket, windowID); got != 1 {
		t.Fatalf("frame panes after show = %d, want the def's one entry", got)
	}
	// The docked pane views the command rather than running it: what tmux was
	// asked to start is the viewer, not the entry's own sleep.
	if got := paneStartCommands(t, bin, socket, windowID); len(got) != 1 ||
		!strings.Contains(got[0], "attach cmd0") {
		t.Errorf("frame pane commands = %v, want the viewer attaching to cmd0", got)
	}

	hide := managedOpts(t, socket, svc)
	if err := FrameHide(ctx, hide); err != nil {
		t.Fatalf("FrameHide: %v", err)
	}

	if got := framePaneCount(t, bin, socket, windowID); got != 0 {
		t.Errorf("frame panes after hide = %d, want none", got)
	}
	if got := shownDef(t, bin, socket, windowID); got != "" {
		t.Errorf("@cmdman_frame_def = %q after hide, want it cleared", got)
	}
	if status, ok := svc.commands["frame-dev-0"]; !ok || status.State != model.EventTypeRunning {
		t.Errorf("frame-dev-0 is %+v/%v to the service after hide, want it still running",
			status, ok)
	}
	if svc.listed != 1 {
		t.Errorf("ListCommands calls = %d, want only the show's one", svc.listed)
	}
}

// TestManaged_Reshow is V7's adopt: the command a show created is the one the
// next show views. The frame's panes are rebuilt from nothing every time — the
// command underneath them is not.
func TestManaged_Reshow(t *testing.T) {
	socket, windowID := managedFrame(t, "dev")
	bin := tmuxOrSkip(t)
	svc := newFakeFrameSvc(t)

	ctx := context.Background()
	opts := managedOpts(t, socket, svc)
	opts.Def = "dev"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow: %v", err)
	}
	if err := FrameHide(ctx, managedOpts(t, socket, svc)); err != nil {
		t.Fatalf("FrameHide: %v", err)
	}
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("second FrameShow: %v", err)
	}

	if got := shownDef(t, bin, socket, windowID); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q after the re-show, want dev", got)
	}
	if got := framePaneCount(t, bin, socket, windowID); got != 1 {
		t.Errorf("frame panes after the re-show = %d, want the def's one entry", got)
	}
	if svc.listed != 2 {
		t.Fatalf("ListCommands calls = %d, want one per show", svc.listed)
	}
	if len(svc.created) != 1 {
		t.Errorf("commands created = %v, want the one the first show made", svc.created)
	}
}

// TestManaged_Replace pins F7's order on the replace path: the def coming up
// has its supervised commands in hand before the driver is asked to remove the
// panes of the def going down. The probe is the window's own record of which
// def is shown — HideFrame clears it — so the assertion is about what the
// driver has been told, not about how fast a pane dies.
func TestManaged_Replace(t *testing.T) {
	socket, windowID := managedFrame(t, "alt", "dev")
	bin := tmuxOrSkip(t)
	svc := newFakeFrameSvc(t)

	ctx := context.Background()
	opts := managedOpts(t, socket, svc)
	opts.Def = "dev"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow dev: %v", err)
	}

	var whileEnsuring string
	svc.onList = func() { whileEnsuring = shownDef(t, bin, socket, windowID) }
	opts.Def = "alt"
	if err := FrameShow(ctx, opts); err != nil {
		t.Fatalf("FrameShow alt: %v", err)
	}

	if whileEnsuring != "dev" {
		t.Errorf("@cmdman_frame_def = %q while adopting alt's commands, want dev still up",
			whileEnsuring)
	}
	if got := shownDef(t, bin, socket, windowID); got != "alt" {
		t.Errorf("@cmdman_frame_def = %q after the replace, want alt", got)
	}
	if _, ok := svc.commands["frame-dev-0"]; !ok {
		t.Error("frame-dev-0 is gone after the replace, want the hidden def's command running")
	}
	if len(svc.created) != 2 {
		t.Errorf("commands created = %v, want one per def", svc.created)
	}
}
