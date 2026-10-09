package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/store"
)

// downJobTestRequest is the job request for project in workDir, against svc's
// config.
func downJobTestRequest(
	t *testing.T,
	svc *cmdman.Service,
	workDir, project string,
) cmdman.CreateRequest {
	t.Helper()
	req, err := composeDownJobRequest(svc.Config(), ComposeDownJobOptions{
		WorkDir: workDir,
		Project: project,
		Env:     []string{"PATH=" + os.Getenv("PATH")},
	})
	assert.NilError(t, err)
	return req
}

// startRecorder stands in for starting a job without running anything, and
// records which jobs it was asked to start.
type startRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *startRecorder) start(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
	return nil
}

func (r *startRecorder) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

// startDownJobInProcess stands in for the detached start the way
// startInProcessMonitor does, except that it returns when the real start does:
// once the job is running, or has already exited. A job's record outlives its
// run, so its being gone is no sign of the run being over.
//
// The monitors are stopped and waited for when the test ends, before the temp
// dir they write into is removed.
func startDownJobInProcess(
	t *testing.T,
	cfg cmdman.CmdmanConfig,
	svc *cmdman.Service,
) (start func(context.Context, string) error, starts func() int) {
	t.Helper()

	monitorCtx, stopMonitors := context.WithCancel(context.Background())
	var monitors errgroup.Group
	t.Cleanup(func() {
		stopMonitors()
		assert.NilError(t, monitors.Wait())
	})
	logger := slog.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
	)

	var count atomic.Int32
	return func(ctx context.Context, id string) error {
		count.Add(1)
		monitors.Go(func() error { return monitor.RunMonitor(monitorCtx, id, cfg, logger) })
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			entries, err := svc.List(ctx, cmdman.ListRequest{AllStates: true})
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.ID != id {
					continue
				}
				switch e.State {
				case model.EventTypeRunning, model.EventTypeExited:
					return nil
				case model.EventTypeFailed:
					return errors.New("the in-process monitor failed")
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("timed out waiting for the in-process monitor")
	}, func() int { return int(count.Load()) }
}

// setJobState writes state over id's record the way its monitor would.
func setJobState(
	t *testing.T,
	cfg cmdman.CmdmanConfig,
	id string,
	state model.EventType,
	stateJSON *model.CommandState,
) {
	t.Helper()

	dbPath, err := cfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, st.Close()) }()
	assert.NilError(t, st.UpdateCommandState(id, state, nil, stateJSON))
}

// holdMonitorLock takes id's PID lock the way a monitor that has come up but not
// yet reported in holds it.
func holdMonitorLock(t *testing.T, cfg cmdman.CmdmanConfig, id string) {
	t.Helper()

	release, held, err := monitor.HoldPIDLock(cfg, id)
	assert.NilError(t, err)
	assert.Assert(t, held)
	t.Cleanup(release)
}

// waitJobState waits for id's record to reach state.
func waitJobState(t *testing.T, svc *cmdman.Service, id string, state model.EventType) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := svc.List(t.Context(), cmdman.ListRequest{AllStates: true})
		assert.NilError(t, err)
		for _, e := range entries {
			if e.ID == id && e.State == state {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, state)
}

// The job re-runs this binary as `compose down` for exactly the project it was
// asked about, in that project's directory and under the caller's environment,
// and its record must not read as one of that project's commands.
func TestComposeDownJobRequest(t *testing.T) {
	cfg := frameSvcConfig(t)
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	exe, err := os.Executable()
	assert.NilError(t, err)

	workDir := t.TempDir()
	env := []string{"PATH=/usr/bin", "TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%3"}
	req, err := composeDownJobRequest(cfg, ComposeDownJobOptions{
		WorkDir: workDir,
		Project: "tools",
		File:    filepath.Join(workDir, "cmd-compose.yaml"),
		Env:     env,
	})
	assert.NilError(t, err)

	assert.DeepEqual(t, req.Argv, []string{
		exe,
		"--data-dir", cfg.DataDir,
		"--runtime-dir", cfg.RuntimeDir,
		"--config", cfg.ConfigPath,
		"compose",
		"--workdir", workDir,
		"-p", "tools",
		"-f", filepath.Join(workDir, "cmd-compose.yaml"),
		"down", "--progress", "json", "--close-windows",
	})
	assert.Equal(
		t,
		req.Name,
		compose.ProjectSelection{WorkDir: workDir, Project: "tools"}.ProjectIdentity()+".down",
	)
	assert.Equal(t, req.Dir, workDir)
	assert.DeepEqual(t, req.Env, env)
	assert.Assert(t, req.ImportHostEnv != nil && !*req.ImportHostEnv)
	assert.Equal(t, req.RestartPolicy, model.RestartPolicyNo)
	assert.Equal(t, req.AutoRemove, false)
	assert.Equal(t, req.LogDriver, logdriver.DriverK8sFile)
	assert.DeepEqual(t, req.Labels, map[string]string{
		compose.LabelJob:        compose.JobDown,
		compose.LabelJobWorkdir: workDir,
		compose.LabelJobProject: "tools",
	})

	t.Run("passes no -p or -f it was not given", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		req, err := composeDownJobRequest(cfg, ComposeDownJobOptions{WorkDir: workDir})
		assert.NilError(t, err)
		assert.DeepEqual(t, req.Argv, []string{
			exe,
			"--data-dir", cfg.DataDir,
			"--runtime-dir", cfg.RuntimeDir,
			"compose",
			"--workdir", workDir,
			"down", "--progress", "json", "--close-windows",
		})
	})

	t.Run("resolves a relative work dir against the current directory", func(t *testing.T) {
		req, err := composeDownJobRequest(cfg, ComposeDownJobOptions{WorkDir: "project"})
		assert.NilError(t, err)
		want, err := filepath.Abs("project")
		assert.NilError(t, err)
		assert.Equal(t, req.Dir, want)
		assert.Equal(t, req.Labels[compose.LabelJobWorkdir], want)
	})
}

// What a launch does with the record it finds under the job's name.
func TestLaunchComposeDownJob(t *testing.T) {
	type setup struct {
		cfg cmdman.CmdmanConfig
		svc *cmdman.Service
		req cmdman.CreateRequest
	}
	newSetup := func(t *testing.T) setup {
		t.Helper()
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		t.Cleanup(func() { _ = svc.Close() })
		return setup{cfg: cfg, svc: svc, req: downJobTestRequest(t, svc, t.TempDir(), "tools")}
	}
	create := func(t *testing.T, s setup) string {
		t.Helper()
		res, err := s.svc.Create(t.Context(), s.req)
		assert.NilError(t, err)
		return res.ID
	}
	// reused launches against s and checks that it answered with the job id
	// without starting another.
	reused := func(t *testing.T, s setup, requested time.Time, id string) ComposeDownJob {
		t.Helper()
		var starts startRecorder
		job, launched, err := launchComposeDownJob(
			t.Context(), s.svc, s.req, requested, starts.start,
		)
		assert.NilError(t, err)
		assert.Assert(t, !launched)
		assert.Equal(t, job.ID, id)
		assert.Equal(t, job.Name, s.req.Name)
		assert.Equal(t, len(starts.started()), 0, "a job was started anyway")
		return job
	}
	// replaced launches against s and checks that it started a new job in place
	// of old.
	replaced := func(t *testing.T, s setup, requested time.Time, old string) ComposeDownJob {
		t.Helper()
		var starts startRecorder
		job, launched, err := launchComposeDownJob(
			t.Context(), s.svc, s.req, requested, starts.start,
		)
		assert.NilError(t, err)
		assert.Assert(t, launched)
		assert.Assert(t, job.ID != old, "the old job was kept")
		assert.DeepEqual(t, starts.started(), []string{job.ID})

		entry, err := findCommandByName(t.Context(), s.svc, s.req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil)
		assert.Equal(t, entry.ID, job.ID)
		return job
	}

	t.Run("launches a job when there is none", func(t *testing.T) {
		s := newSetup(t)
		var starts startRecorder
		job, launched, err := launchComposeDownJob(
			t.Context(), s.svc, s.req, time.Now(), starts.start,
		)
		assert.NilError(t, err)
		assert.Assert(t, launched)
		assert.Equal(t, job.Name, s.req.Name)
		assert.DeepEqual(t, starts.started(), []string{job.ID})
	})

	t.Run("returns a running job", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)
		markRunningMonitor(t, s.cfg, id)

		job := reused(t, s, time.Now(), id)
		assert.Equal(t, job.State, model.EventTypeRunning)
	})

	t.Run("returns a starting job", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)
		holdMonitorLock(t, s.cfg, id)
		setJobState(t, s.cfg, id, model.EventTypeStarting, &model.CommandState{})

		job := reused(t, s, time.Now(), id)
		assert.Equal(t, job.State, model.EventTypeStarting)
	})

	t.Run("returns a job that ran since the request", func(t *testing.T) {
		s := newSetup(t)
		requested := time.Now()
		id := create(t, s)
		setJobState(t, s.cfg, id, model.EventTypeExited, &model.CommandState{
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		})

		job := reused(t, s, requested, id)
		assert.Equal(t, job.State, model.EventTypeExited)
	})

	// A job that failed before it got going has no start stamp; it was created
	// since the request, which is what counts then.
	t.Run("returns a job that failed to start since the request", func(t *testing.T) {
		s := newSetup(t)
		requested := time.Now()
		id := create(t, s)
		setJobState(t, s.cfg, id, model.EventTypeFailed, &model.CommandState{})

		job := reused(t, s, requested, id)
		assert.Equal(t, job.State, model.EventTypeFailed)
	})

	t.Run("replaces a job that ran before the request", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)
		setJobState(t, s.cfg, id, model.EventTypeExited, &model.CommandState{
			StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})

		replaced(t, s, time.Now(), id)
	})

	t.Run("replaces a job left created with no monitor", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)

		replaced(t, s, time.Now(), id)
	})

	// A launch that died after spawning the monitor leaves a record in the
	// created state, and a monitor that has taken its PID lock but not yet said
	// so. That launch is still in flight.
	t.Run("returns a job whose monitor has not reported in yet", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)
		holdMonitorLock(t, s.cfg, id)

		job := reused(t, s, time.Now(), id)
		assert.Equal(t, job.State, model.EventTypeCreated)
	})

	// The same goes for a finished job a monitor has come up for again.
	t.Run("returns a job that ran before the request but has a monitor", func(t *testing.T) {
		s := newSetup(t)
		id := create(t, s)
		setJobState(t, s.cfg, id, model.EventTypeExited, &model.CommandState{
			StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})
		holdMonitorLock(t, s.cfg, id)

		reused(t, s, time.Now(), id)
	})

	// A create that died between its config row and its state row holds the
	// job's name in a record the listing does not show.
	t.Run("replaces a record that has no state", func(t *testing.T) {
		s := newSetup(t)

		dbPath, err := s.cfg.DBPath()
		assert.NilError(t, err)
		st, err := store.OpenStore(t.Context(), dbPath, true)
		assert.NilError(t, err)
		const id = "0123456789abcdef0123456789abcdef"
		commandDir, err := s.cfg.CommandDir(id)
		assert.NilError(t, err)
		assert.NilError(t, st.InsertCommandConfig(id, s.req.Name, &model.CommandConfig{
			Argv:       []string{"true"},
			CommandDir: commandDir,
		}))
		assert.NilError(t, st.Close())

		replaced(t, s, time.Now(), id)
	})

	// Left created, the record of a start that failed would read as a down that
	// never ran to whoever looks next.
	t.Run("removes the job when its start fails", func(t *testing.T) {
		s := newSetup(t)
		failing := func(context.Context, string) error { return errors.New("spawn failed") }
		_, launched, err := launchComposeDownJob(t.Context(), s.svc, s.req, time.Now(), failing)
		assert.ErrorContains(t, err, "spawn failed")
		assert.Assert(t, !launched)

		entry, err := findCommandByName(t.Context(), s.svc, s.req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry == nil, "the record outlived its failed start: %+v", entry)
	})

	// A monitor that came up after all keeps the record its start gave up on.
	t.Run("keeps the job when its start fails but a monitor came up", func(t *testing.T) {
		s := newSetup(t)
		var id string
		late := func(_ context.Context, started string) error {
			id = started
			holdMonitorLock(t, s.cfg, started)
			return errors.New("timed out waiting for the monitor")
		}
		_, _, err := launchComposeDownJob(t.Context(), s.svc, s.req, time.Now(), late)
		assert.ErrorContains(t, err, "timed out waiting for the monitor")

		entry, err := findCommandByName(t.Context(), s.svc, s.req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil && entry.ID == id, "the record went: %+v", entry)
	})

	t.Run("stops waiting for another launch when ctx is done", func(t *testing.T) {
		s := newSetup(t)
		unlock, err := lockComposeJob(t.Context(), s.cfg, s.req.Name)
		assert.NilError(t, err)
		defer unlock()

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		var starts startRecorder
		_, _, err = launchComposeDownJob(ctx, s.svc, s.req, time.Now(), starts.start)
		assert.Assert(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
		assert.Equal(t, len(starts.started()), 0)
	})
}

// Two launches for one project that race each other, as two widgets asking at
// once do, end up with one job between them.
func TestLaunchComposeDownJobConcurrently(t *testing.T) {
	cfg := frameSvcConfig(t)
	// One service each, as two processes would have.
	svcs := []*cmdman.Service{cmdman.NewService(cfg), cmdman.NewService(cfg)}
	for _, svc := range svcs {
		t.Cleanup(func() { _ = svc.Close() })
	}

	req := downJobTestRequest(t, svcs[0], t.TempDir(), "tools")
	req.Argv = []string{"/bin/sh", "-c", "sleep 0.5"}
	start, starts := startDownJobInProcess(t, cfg, svcs[0])

	type outcome struct {
		job      ComposeDownJob
		launched bool
	}
	outcomes := make([]outcome, len(svcs))
	requested := time.Now()
	var launches errgroup.Group
	for i, svc := range svcs {
		launches.Go(func() error {
			job, launched, err := launchComposeDownJob(t.Context(), svc, req, requested, start)
			outcomes[i] = outcome{job: job, launched: launched}
			return err
		})
	}
	assert.NilError(t, launches.Wait())

	assert.Equal(t, starts(), 1)
	assert.Equal(t, outcomes[0].job.ID, outcomes[1].job.ID)
	assert.Assert(t, outcomes[0].launched != outcomes[1].launched,
		"launched: %v and %v", outcomes[0].launched, outcomes[1].launched)

	entries, err := svcs[0].List(t.Context(), cmdman.ListRequest{
		AllStates: true,
		Labels:    map[string]string{compose.LabelJob: compose.JobDown},
	})
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
}

// A project is its work directory as much as its name, so one name in two
// directories is two projects, and two jobs.
func TestLaunchComposeDownJobPerWorkdir(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	var starts startRecorder
	dirs := []string{t.TempDir(), t.TempDir()}
	jobs := make([]ComposeDownJob, len(dirs))
	for i, dir := range dirs {
		req := downJobTestRequest(t, svc, dir, "tools")
		job, launched, err := launchComposeDownJob(
			t.Context(), svc, req, time.Now(), starts.start,
		)
		assert.NilError(t, err)
		assert.Assert(t, launched)
		jobs[i] = job
	}

	assert.Assert(t, jobs[0].ID != jobs[1].ID)
	assert.Assert(t, jobs[0].Name != jobs[1].Name)
	for i, dir := range dirs {
		entries, err := svc.List(t.Context(), cmdman.ListRequest{
			AllStates: true,
			Labels: map[string]string{
				compose.LabelJob:        compose.JobDown,
				compose.LabelJobWorkdir: dir,
				compose.LabelJobProject: "tools",
			},
		})
		assert.NilError(t, err)
		assert.Equal(t, len(entries), 1)
		assert.Equal(t, entries[0].ID, jobs[i].ID)
	}
}

// A down can finish before a second request racing it gets the lock. The
// finished job answers that request too, rather than a second down running for
// a project the first one already took down.
func TestLaunchComposeDownJobAfterFastDown(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	req := downJobTestRequest(t, svc, t.TempDir(), "tools")
	req.Argv = []string{"/bin/sh", "-c", "exit 0"}
	start, starts := startDownJobInProcess(t, cfg, svc)

	// Both requests are made now; the second gets the lock only once the first
	// job has finished.
	requested := time.Now()
	first, launched, err := launchComposeDownJob(t.Context(), svc, req, requested, start)
	assert.NilError(t, err)
	assert.Assert(t, launched)
	waitJobState(t, svc, first.ID, model.EventTypeExited)

	second, launched, err := launchComposeDownJob(t.Context(), svc, req, requested, start)
	assert.NilError(t, err)
	assert.Assert(t, !launched)
	assert.Equal(t, second.ID, first.ID)
	assert.Equal(t, second.State, model.EventTypeExited)
	assert.Equal(t, starts(), 1)
}
