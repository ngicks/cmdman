package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/internal/flock"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/store"
)

// composeJobLockPoll is how often a launch waiting on another launch of the same
// job tries the lock again. flock(2) blocks with no regard for a context, so the
// wait polls instead, at the interval starting a command polls its state at.
const composeJobLockPoll = 50 * time.Millisecond

// ComposeDownJobOptions names the project a compose down job tears down.
type ComposeDownJobOptions struct {
	// WorkDir is the project's work directory. A relative one is resolved
	// against the current directory.
	WorkDir string
	// Project is the project name. Empty passes no -p, and the down then
	// selects every command in WorkDir.
	Project string
	// File is passed to -f as given: a compose file path, or a project name
	// resolved under the compose dir. A relative path resolves against WorkDir,
	// where the job runs. Empty passes no -f.
	File string
	// Env is the job's whole environment, normally the caller's os.Environ().
	// It is taken as it is rather than rebuilt: $TMUX and $TMUX_PANE tell the
	// down which multiplexer holds the project's windows and which pane asked
	// for it, and the window of that pane is restored rather than closed.
	Env []string
}

// ComposeDownJob is the cmdman command that runs, or ran, a project's down.
type ComposeDownJob struct {
	ID   string
	Name string
	// State is the job's state as the launch or the lookup last read it.
	State model.EventType
	// NeverRan marks a record the lookup found created with nothing bringing it
	// up (see composeDownJobNeverRan). Its down never ran and never will, so the
	// record is over rather than pending, and the next launch replaces it.
	NeverRan bool
}

// LaunchComposeDownJob starts `cmdman compose down --progress json
// --close-windows` for one project as a cmdman command of its own, so the
// teardown survives the caller going away, or finds the job that already
// answers this request. launched reports whether this call started the job.
//
// A project has one job, named after the project's identity, and launches of it
// are serialized by a lock of their own. Under that lock, the record found under
// the job's name decides:
//
//   - starting or running: a down is under way, and it is the answer;
//   - exited or failed, with a run that began no earlier than this call: a down
//     that finished while this call waited for the lock answers it too;
//   - anything else, a down that finished earlier or a record that never got
//     going: a monitor holding the record's PID lock means a launch is in
//     flight, and that is the answer; otherwise the record is removed and a new
//     job takes its place.
//
// The job is not auto-removed, so its record keeps the down's result for
// whoever asks next, and its output is kept in a k8s-file log in its command
// dir.
func LaunchComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	opts ComposeDownJobOptions,
) (job ComposeDownJob, launched bool, err error) {
	// Read before waiting for the lock: a down that ran while this call waited
	// is one this call asked for.
	requested := time.Now()
	req, err := composeDownJobRequest(svc.Config(), opts)
	if err != nil {
		return ComposeDownJob{}, false, err
	}
	return launchComposeDownJob(ctx, svc, req, requested, svc.Start)
}

// composeDownJobRequest describes the job as a command record.
//
// The job runs in the project's work directory under the caller's environment,
// is never restarted, and is never auto-removed. The log driver is named rather
// than left to the configured default, which may be none: the job's output is
// where the down's progress is read back from.
func composeDownJobRequest(
	cfg cmdman.CmdmanConfig,
	opts ComposeDownJobOptions,
) (cmdman.CreateRequest, error) {
	selection, err := composeDownJobSelection(opts)
	if err != nil {
		return cmdman.CreateRequest{}, err
	}
	workDir := selection.WorkDir

	verbArgv := []string{"compose", "--workdir", workDir}
	if opts.Project != "" {
		verbArgv = append(verbArgv, "-p", opts.Project)
	}
	if opts.File != "" {
		verbArgv = append(verbArgv, "-f", opts.File)
	}
	verbArgv = append(verbArgv, "down", "--progress", "json", "--close-windows")
	argv, err := selfArgv(cfg, verbArgv)
	if err != nil {
		return cmdman.CreateRequest{}, fmt.Errorf("compose down job: %w", err)
	}

	return cmdman.CreateRequest{
		Name:          composeDownJobName(selection),
		Dir:           workDir,
		Env:           slices.Clone(opts.Env),
		ImportHostEnv: new(false),
		RestartPolicy: model.RestartPolicyNo,
		LogDriver:     logdriver.DriverK8sFile,
		Labels: map[string]string{
			compose.LabelJob:        compose.JobDown,
			compose.LabelJobWorkdir: workDir,
			compose.LabelJobProject: opts.Project,
		},
		Argv: argv,
	}, nil
}

// composeDownJobSelection is the project opts names, with its work directory
// made absolute.
func composeDownJobSelection(opts ComposeDownJobOptions) (compose.ProjectSelection, error) {
	workDir, err := filepath.Abs(opts.WorkDir)
	if err != nil {
		return compose.ProjectSelection{}, fmt.Errorf(
			"compose down job: resolve work dir: %w", err,
		)
	}
	return compose.ProjectSelection{WorkDir: workDir, Project: opts.Project}, nil
}

// composeDownJobName names the down job of selection's project. The project
// identity tells projects apart by work directory as well as by name, and the
// ".down" suffix cannot end a replica's name, which ends in the replica's index.
func composeDownJobName(selection compose.ProjectSelection) string {
	return selection.ProjectIdentity() + ".down"
}

// findComposeDownJob finds the down job of the project opts names: the one
// running, or the last one to have run, since a job is not removed when it
// ends. A record left created is either a launch still on its way or one that
// never ran, which the lookup tells apart the way a launch does (see
// [ComposeDownJob.NeverRan]). ok is false when the project has none. opts.File
// and opts.Env are not read.
func findComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	opts ComposeDownJobOptions,
) (job ComposeDownJob, ok bool, err error) {
	selection, err := composeDownJobSelection(opts)
	if err != nil {
		return ComposeDownJob{}, false, err
	}
	name := composeDownJobName(selection)
	entry, err := findCommandByName(ctx, svc, name)
	if err != nil {
		return ComposeDownJob{}, false, fmt.Errorf("look up compose down job %q: %w", name, err)
	}
	if entry == nil {
		return ComposeDownJob{}, false, nil
	}
	job = composeDownJobOf(*entry)
	if entry.State == model.EventTypeCreated {
		if job.NeverRan, err = composeDownJobNeverRan(ctx, svc, *entry); err != nil {
			return ComposeDownJob{}, false, err
		}
	}
	return job, true, nil
}

// composeDownJobNeverRan reports whether entry, a record of the job found
// created, never ran and never will. The rule is the one a launch applies
// before it replaces such a record (see reuseOrRemoveComposeDownJob): no launch
// holds the job's lock, and no monitor holds the record's PID lock.
//
// The record is read again under the job's lock, so the answer is not about a
// record a launch has since got going or replaced. The PID lock is tried under
// the job's lock too: a launch holds that lock until the monitor it spawned has
// reported in, and trying the PID lock while that monitor comes up would keep it
// out.
func composeDownJobNeverRan(
	ctx context.Context,
	svc *cmdman.Service,
	entry store.CommandEntry,
) (bool, error) {
	unlock, ok, err := tryLockComposeJob(svc.Config(), entry.Name)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer unlock()

	current, err := findCommandEntryByID(ctx, svc, entry.ID)
	if err != nil {
		return false, fmt.Errorf("look up compose down job %q: %w", entry.Name, err)
	}
	if current == nil || current.State != model.EventTypeCreated {
		return false, nil
	}
	release, held, err := monitor.HoldPIDLock(svc.Config(), entry.ID)
	if err != nil {
		return false, fmt.Errorf("check compose down job %q for a monitor: %w", entry.Name, err)
	}
	if !held {
		return false, nil
	}
	release()
	return true, nil
}

// launchComposeDownJob is [LaunchComposeDownJob] for the job req describes, as
// asked for at requested.
//
// start is a parameter rather than a straight call so a test can put a monitor
// it drives itself in place of the detached one.
func launchComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	req cmdman.CreateRequest,
	requested time.Time,
	start func(ctx context.Context, idOrName string) error,
) (ComposeDownJob, bool, error) {
	unlock, err := lockComposeJob(ctx, svc.Config(), req.Name)
	if err != nil {
		return ComposeDownJob{}, false, err
	}
	defer unlock()

	existing, err := findCommandByName(ctx, svc, req.Name)
	if err != nil {
		return ComposeDownJob{}, false, fmt.Errorf(
			"look up compose down job %q: %w", req.Name, err,
		)
	}
	if existing != nil {
		reuse, err := reuseOrRemoveComposeDownJob(ctx, svc, *existing, requested)
		if err != nil {
			return ComposeDownJob{}, false, err
		}
		if reuse {
			return composeDownJobOf(*existing), false, nil
		}
	}

	id, err := createComposeDownJob(ctx, svc, req)
	if err != nil {
		return ComposeDownJob{}, false, err
	}
	// Starting returns once the job is running, or once it has already exited,
	// so the next launch to take the lock finds a record that answers it.
	if err := start(ctx, id); err != nil {
		startErr := fmt.Errorf("start compose down job %q: %w", req.Name, err)
		// A record the failed start left created would tell whoever looks next of
		// a down that never ran. A monitor that came up after all keeps it. The
		// removal runs past a done ctx, which may be what failed the start.
		if _, err := removeUnmonitoredComposeDownJob(
			context.WithoutCancel(ctx), svc, id, req.Name,
		); err != nil {
			return ComposeDownJob{}, false, errors.Join(startErr, err)
		}
		return ComposeDownJob{}, false, startErr
	}

	started, err := findCommandByName(ctx, svc, req.Name)
	if err != nil {
		return ComposeDownJob{}, false, fmt.Errorf(
			"look up compose down job %q: %w", req.Name, err,
		)
	}
	if started == nil || started.ID != id {
		return ComposeDownJob{}, false, fmt.Errorf(
			"compose down job %q is gone after starting", req.Name,
		)
	}
	return composeDownJobOf(*started), true, nil
}

// reuseOrRemoveComposeDownJob decides what the record found under the job's
// name means for a launch asked for at requested. reuse reports a record that
// answers the launch; otherwise the record has been removed to make way for a
// new job.
func reuseOrRemoveComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	entry store.CommandEntry,
	requested time.Time,
) (reuse bool, err error) {
	switch entry.State {
	case model.EventTypeStarting, model.EventTypeRunning:
		// The listing that read the record has checked that a monitor is behind
		// it.
		return true, nil
	case model.EventTypeExited, model.EventTypeFailed:
		if composeDownJobRanSince(entry, requested) {
			return true, nil
		}
	}

	// What is left has no run to show for this launch, but a monitor may still be
	// coming up for it: one spawned by a launch that died before the monitor
	// reported in, or by a start from elsewhere.
	removed, err := removeUnmonitoredComposeDownJob(ctx, svc, entry.ID, entry.Name)
	if err != nil {
		return false, err
	}
	return !removed, nil
}

// removeUnmonitoredComposeDownJob removes the job's record id unless a monitor
// holds its PID lock, and reports whether it did. The PID lock is held across
// the removal, so a monitor coming up for the record either holds the lock
// already, and the record stays, or takes it only once the record is gone,
// finds nothing to run, and exits.
func removeUnmonitoredComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	id, name string,
) (removed bool, err error) {
	release, held, err := monitor.HoldPIDLock(svc.Config(), id)
	if err != nil {
		return false, fmt.Errorf("check compose down job %q for a monitor: %w", name, err)
	}
	if !held {
		return false, nil
	}
	defer release()

	results, err := svc.Remove(
		ctx,
		cmdman.RemoveRequest{Targets: []string{id}, Force: true},
	)
	if err == nil {
		for _, r := range results {
			if r.Err != nil {
				err = r.Err
				break
			}
		}
	}
	if err != nil {
		return false, fmt.Errorf("remove compose down job %q: %w", name, err)
	}
	return true, nil
}

// composeDownJobRanSince reports whether entry's run began no earlier than
// requested.
//
// The stamps are stored to the second, so requested is compared at that
// resolution too; otherwise a run that began a fraction of a second after the
// request would read as older than it. A run that failed before it got going
// has no start stamp, and the record's creation stands in: the launch that made
// the record started it right after.
func composeDownJobRanSince(entry store.CommandEntry, requested time.Time) bool {
	stamp := entry.CreatedAt
	if entry.StateJSON != nil && entry.StateJSON.StartedAt != "" {
		stamp = entry.StateJSON.StartedAt
	}
	started, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return false
	}
	return !started.Before(requested.Truncate(time.Second))
}

// createComposeDownJob registers the job and returns its command id.
//
// The listing found nothing under the job's name, or the record it found is
// gone, so a conflict over the name comes from a record the listing cannot see:
// one whose create died between writing its config and its state. No monitor can
// be behind such a record, since starting a command reads its state first, so it
// is removed and the registration tried once more.
func createComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	req cmdman.CreateRequest,
) (string, error) {
	res, err := svc.Create(ctx, req)
	if err == nil {
		return res.ID, nil
	}
	createErr := fmt.Errorf("register compose down job %q: %w", req.Name, err)

	results, err := svc.Remove(
		ctx,
		cmdman.RemoveRequest{Targets: []string{req.Name}, Force: true},
	)
	if err != nil {
		// Nothing goes by the name, so the create failed for a reason of its own.
		return "", createErr
	}
	for _, r := range results {
		if r.Err != nil {
			return "", errors.Join(
				createErr,
				fmt.Errorf("remove leftover %q: %w", req.Name, r.Err),
			)
		}
	}

	res, err = svc.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("register compose down job %q: %w", req.Name, err)
	}
	return res.ID, nil
}

// lockComposeJob takes the lock that serializes launches of the compose job
// named name, waiting on a launch that holds it until ctx is done. unlock
// releases it, and so does the process exiting, so a launch that dies holding
// the lock does not keep the next one out.
//
// The lock file stays behind after unlock. Removing it would let a launch still
// waiting on the old file and one that opens a new file at the same path both
// hold "the" lock.
func lockComposeJob(
	ctx context.Context,
	cfg cmdman.CmdmanConfig,
	name string,
) (unlock func(), err error) {
	f, err := openComposeJobLock(cfg, name)
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(composeJobLockPoll)
	defer ticker.Stop()
	for {
		acquired, err := flock.TryLockExclusive(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("compose job: lock %q: %w", f.Name(), err)
		}
		if acquired {
			return func() { _ = f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("compose job: wait for lock %q: %w", f.Name(), ctx.Err())
		case <-ticker.C:
		}
	}
}

// tryLockComposeJob is [lockComposeJob] without the wait: ok is false, and
// unlock nil, when a launch holds the lock.
func tryLockComposeJob(
	cfg cmdman.CmdmanConfig,
	name string,
) (unlock func(), ok bool, err error) {
	f, err := openComposeJobLock(cfg, name)
	if err != nil {
		return nil, false, err
	}
	acquired, err := flock.TryLockExclusive(f)
	if err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("compose job: lock %q: %w", f.Name(), err)
	}
	if !acquired {
		_ = f.Close()
		return nil, false, nil
	}
	return func() { _ = f.Close() }, true, nil
}

// openComposeJobLock opens the lock file of the compose job named name,
// creating it when there is none.
func openComposeJobLock(cfg cmdman.CmdmanConfig, name string) (*os.File, error) {
	if cfg.RuntimeDir == "" {
		return nil, errors.New("compose job: runtime dir is empty")
	}
	dir := filepath.Join(cfg.RuntimeDir, "compose-jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("compose job: create lock dir: %w", err)
	}
	path := filepath.Join(dir, name+".lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("compose job: open lock %q: %w", path, err)
	}
	return f, nil
}

func composeDownJobOf(entry store.CommandEntry) ComposeDownJob {
	return ComposeDownJob{ID: entry.ID, Name: entry.Name, State: entry.State}
}
