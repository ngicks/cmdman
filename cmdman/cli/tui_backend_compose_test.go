package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func TestMergeProjectInfosAddsZeroCommandNamedProjects(t *testing.T) {
	summaries := []compose.ProjectSummary{
		{Project: "api-stack", Commands: 3, Running: 1, WorkDir: "/work/api"},
	}
	named := []string{"api-stack", "tools"} // api-stack already known; tools is new
	got := mergeProjectInfos(summaries, named)
	if len(got) != 2 {
		t.Fatalf("expected 2 merged projects, got %d", len(got))
	}
	byName := map[string]int{}
	for _, p := range got {
		byName[p.Name] = p.Commands
	}
	if byName["api-stack"] != 3 {
		t.Fatalf("api-stack should keep its store count, got %d", byName["api-stack"])
	}
	count, ok := byName["tools"]
	if !ok {
		t.Fatalf("never-run named project tools should appear")
	}
	if count != 0 {
		t.Fatalf("never-run project should have zero commands, got %d", count)
	}
}

// TestMergeProjectInfosStampsIdentity pins which spelling of the work directory
// the switcher's identity is hashed from: the canonical one compose computes and
// mux stamps its windows with, not ProjectInfo.Workdir, which is symlink-
// resolved for cwd comparison and would address a window that does not exist.
func TestMergeProjectInfosStampsIdentity(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	got := mergeProjectInfos(
		[]compose.ProjectSummary{{Project: "api-stack", WorkDir: link}},
		[]string{"tools"},
	)
	if len(got) != 2 {
		t.Fatalf("want the summary and the named project, got %d", len(got))
	}
	want := compose.ProjectSelection{WorkDir: link, Project: "api-stack"}.ProjectIdentity()
	if got[0].Identity != want {
		t.Errorf("identity = %q, want %q", got[0].Identity, want)
	}
	resolved := compose.ProjectSelection{
		WorkDir: normalizePath(link), Project: "api-stack",
	}.ProjectIdentity()
	if got[0].Identity == resolved {
		t.Errorf("identity must not be hashed from the symlink-resolved work directory")
	}
	// A never-run named project has no directory to hash, so it gets no identity
	// rather than one that would match some other project's window.
	if got[1].Identity != "" {
		t.Errorf("named project identity = %q, want none", got[1].Identity)
	}
}

func TestComposeUpStreamDropsHookEvents(t *testing.T) {
	stream := newComposeUpStream(t.Context())
	hookPhases := []compose.Phase{
		compose.PhaseHookRunning,
		compose.PhaseHookOutput,
		compose.PhaseHookSucceeded,
		compose.PhaseHookFailed,
		compose.PhaseHookWarning,
		compose.PhaseHookIgnored,
	}

	stream.Report(compose.Event{Command: "web", Phase: compose.PhaseCreating})
	for _, phase := range hookPhases {
		stream.Report(compose.Event{
			Command:    "web",
			Phase:      phase,
			Err:        errors.New("hook command exited with code 1"),
			ScaleIndex: 1,
			Hook:       "scratch",
			Lifecycle:  compose.LifecycleCreatePre,
			Exec:       "abc-proj-web-1.hook.scratch.create_pre",
		})
	}
	stream.Report(compose.Event{
		Command: "web",
		Phase:   compose.PhaseHookWarning,
		Err:     errors.New("remove without the stored hooks"),
	})
	stream.Report(compose.Event{Command: "web", Phase: compose.PhaseCreated})
	stream.finish(nil)

	var got []tui.ComposeUpEvent
	for ev := range stream.Events() {
		got = append(got, ev)
	}
	want := []tui.ComposeUpEvent{
		{Command: "web", Phase: string(compose.PhaseCreating)},
		{Command: "web", Phase: string(compose.PhaseCreated), Terminal: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hook events must not reach the replica's mark:\ngot  %+v\nwant %+v", got, want)
	}
}

const cwdComposeYAML = "name: cwdproj\ncommands:\n  a:\n    args: [echo, a]\n"

func TestAppendCwdProjectAddsUnregisteredProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(cwdComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	got := appendCwdProject(nil, "")
	if len(got) != 1 {
		t.Fatalf("want 1 cwd project, got %d", len(got))
	}
	if got[0].Name != "cwdproj" {
		t.Fatalf("name = %q, want cwdproj", got[0].Name)
	}
	if got[0].Workdir != normalizePath(dir) {
		t.Fatalf("workdir = %q, want %q", got[0].Workdir, normalizePath(dir))
	}
	if got[0].Path == "" {
		t.Fatal("path should be the discovered compose file")
	}
}

func TestProjectDefinitionReadsRawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	content := "name: tools\ncommands:\n  a:\n    args: [echo, a]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &serviceBackend{}
	got, err := b.ProjectDefinition(context.Background(), "tools", path)
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Fatalf("ProjectDefinition should return the raw file text, got %q", got)
	}
}

func TestComposeFilePathReturnsExplicitPath(t *testing.T) {
	b := &serviceBackend{}
	got, err := b.ComposeFilePath(context.Background(), "tools", "/etc/compose/tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/compose/tools.yaml" {
		t.Fatalf("an explicit composeFile should pass through, got %q", got)
	}
}

const downComposeYAML = `name: downproj
commands:
  a:
    args: [echo, a]
`

// downProjectDir writes the project the down tests work on into a temp
// directory and makes it the working directory. The compose config dir is
// derived from $CMDMAN_CONF; without the override the resolution would reach
// the projects the developer keeps in their own.
func downProjectDir(t *testing.T) (dir, path string) {
	t.Helper()
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	dir = t.TempDir()
	path = filepath.Join(dir, "cmd-compose.yaml")
	if err := os.WriteFile(path, []byte(downComposeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir, path
}

// The job is told the project as compose resolves it, whatever directory the
// widget stands in: the target's own work directory, and the compose file as an
// absolute path, which a target naming the project by its name key alone gets
// from the compose config dir.
func TestComposeDownJobOptionsResolveTheProject(t *testing.T) {
	dir, path := downProjectDir(t)
	named := filepath.Join(filepath.Dir(os.Getenv("CMDMAN_CONF")), "compose", "tools.yaml")
	if err := os.MkdirAll(filepath.Dir(named), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(named, []byte(muxComposeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	b := &serviceBackend{}

	for _, tc := range []struct {
		name   string
		target tui.DownTarget
		want   ComposeDownJobOptions
	}{
		{
			name:   "compose file",
			target: tui.DownTarget{Project: "downproj", Path: path, WorkDir: dir},
			want:   ComposeDownJobOptions{WorkDir: dir, Project: "downproj", File: path},
		},
		{
			name:   "name key",
			target: tui.DownTarget{Project: "tools", WorkDir: dir},
			want:   ComposeDownJobOptions{WorkDir: dir, Project: "tools", File: named},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.composeDownJobOptions(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("options = %+v, want %+v", got, tc.want)
			}
		})
	}

	if _, err := b.composeDownJobOptions(tui.DownTarget{
		Project: "downproj", Path: filepath.Join(dir, "gone.yaml"), WorkDir: dir,
	}); err == nil {
		t.Error("a compose file that is gone should fail the resolution")
	}
}

// A project's latest down job is found by the project alone, and says whether
// its run was over when it was found. A record left created is over unless a
// launch or a monitor is still bringing it up.
func TestFindComposeDownFindsTheProjectsJob(t *testing.T) {
	dir, path := downProjectDir(t)
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()
	b := &serviceBackend{svc: svc}
	target := tui.DownTarget{Project: "downproj", Path: path, WorkDir: dir}

	if _, ok, err := b.FindComposeDown(t.Context(), target); err != nil || ok {
		t.Fatalf("a project that never went down has a job: ok = %v, err = %v", ok, err)
	}

	req := downJobTestRequest(t, svc, dir, "downproj")
	res, err := svc.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	find := func(t *testing.T, want tui.DownJob) {
		t.Helper()
		job, ok, err := b.FindComposeDown(t.Context(), target)
		if err != nil || !ok {
			t.Fatalf("the job was not found: ok = %v, err = %v", ok, err)
		}
		if job != want {
			t.Errorf("job = %+v, want %+v", job, want)
		}
	}

	t.Run("left created with nothing bringing it up", func(t *testing.T) {
		find(t, tui.DownJob{ID: res.ID, Finished: true})
	})

	t.Run("left created while a launch holds the job's lock", func(t *testing.T) {
		unlock, err := lockComposeJob(t.Context(), cfg, req.Name)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		find(t, tui.DownJob{ID: res.ID})
	})

	t.Run("left created while a monitor holds its PID lock", func(t *testing.T) {
		release, held, err := monitor.HoldPIDLock(cfg, res.ID)
		if err != nil || !held {
			t.Fatalf("hold the PID lock: held = %v, err = %v", held, err)
		}
		defer release()
		find(t, tui.DownJob{ID: res.ID})
	})

	t.Run("exited", func(t *testing.T) {
		setJobState(t, cfg, res.ID, model.EventTypeExited, &model.CommandState{})
		find(t, tui.DownJob{ID: res.ID, Finished: true})
	})

	// The same file under another name is another project, with no job.
	other := tui.DownTarget{Project: "other", Path: path, WorkDir: dir}
	if _, ok, err := b.FindComposeDown(t.Context(), other); err != nil || ok {
		t.Errorf("another project found a job: ok = %v, err = %v", ok, err)
	}
}

func TestAppendCwdProjectFillsPathWhenAlreadyListed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(cwdComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	// Already listed (e.g. from the store) but with no compose-file path.
	got := appendCwdProject([]tui.ProjectInfo{{Name: "cwdproj"}}, "")
	if len(got) != 1 {
		t.Fatalf("must not duplicate an already-listed project, got %d", len(got))
	}
	if got[0].Path == "" {
		t.Fatal("discovered path should be filled into the existing row")
	}
}
