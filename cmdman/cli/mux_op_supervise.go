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
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

const (
	// muxOpLogMaxSize and muxOpLogMaxFile keep the diagnostic record of one
	// operation small and un-rotated: an operation writes a page or two, and
	// what matters is the last run, not a history of them.
	muxOpLogMaxSize = "1MiB"
	muxOpLogMaxFile = "1"

	// muxOpPreRunGrace is how long a record that has not reached running yet is
	// still taken for a live operation.
	//
	// A record begins life in the created state, and nothing about it says
	// whether a worker is behind it: no monitor has started, so there is no PID
	// to check and nothing for the stale sweep to reconcile. Removing it on that
	// basis would pull the record out from under a worker that is merely still
	// spawning, and the invocation that created it would then find its worker
	// gone. Age decides instead. Starting a command waits up to five seconds for
	// the monitor to report itself running — a hundred polls, fifty milliseconds
	// apart — so anything younger than that may well still be on its way up.
	// Ten seconds covers that whole wait plus the second of slack the stored
	// timestamp can add, being written to the resolution of a second.
	muxOpPreRunGrace = 10 * time.Second
)

// superviseMuxOp performs op in a detached worker and follows it.
//
// The worker is registered as an ordinary cmdman command, so it gets the whole
// of cmdman's spawning, supervision and log plumbing for free: a monitor
// detached from this process's pane runs it, its output is written where any
// command's output is written, and its exit is recorded the way any command's
// exit is. What is left here is the following: printing that output as it
// arrives and reporting the status the worker exited with, so a verb typed
// outside tmux — or in another window, where nothing is destroyed — still reads
// as the synchronous command it always was.
func superviseMuxOp(
	ctx context.Context,
	opts MuxOpOptions,
	op func(context.Context) error,
) error {
	if !opts.supervised() {
		return op(ctx)
	}
	opts, err := opts.resolve()
	if err != nil {
		return err
	}

	req, err := muxOpCreateRequest(opts)
	if err != nil {
		return err
	}
	return runMuxOpWorker(ctx, opts, req, opts.Svc.Start)
}

// runMuxOpWorker registers the worker described by req, hands it to start, and
// follows it to its exit.
//
// start is a parameter rather than a straight call so a test can put a monitor
// it drives itself in place of the detached one.
func runMuxOpWorker(
	ctx context.Context,
	opts MuxOpOptions,
	req cmdman.CreateRequest,
	start func(ctx context.Context, idOrName string) error,
) error {
	svc := opts.Svc
	logPath := req.LogOpts[logdriver.LogOptPath]

	// Read before anything is registered, so the boundary is ahead of the first
	// byte this run can write: everything past it belongs to this run, and
	// everything before it to the runs that used the window before.
	from := muxOpLogEnd(ctx, logPath)

	// The exit status has to come off the event log rather than the command
	// record, because the record does not survive the exit: the worker is
	// auto-removed, and removal takes its state, its exit code and its exit
	// history with it. The monitor appends the exit event before it removes
	// anything, and the event log is append-only, so the event outlives the
	// record.
	id, err := createMuxOpCommand(ctx, svc, req)
	if err != nil {
		return err
	}
	sub, err := subscribeMuxOpExit(ctx, svc, id)
	if err != nil {
		return err
	}
	defer sub.Close()

	if err := start(ctx, id); err != nil {
		return fmt.Errorf("start mux op worker: %w", err)
	}

	followMuxOpOutput(ctx, opts, id, logPath, from)
	return waitMuxOpExit(ctx, svc, sub, id)
}

// subscribeMuxOpExit watches the event log for the end of the worker's run.
//
// The log is read from its start rather than tailed, so an exit that landed
// before the subscription opened is still delivered — however fast the
// operation turns out to be, its exit cannot be missed.
//
// No time bound goes with that, deliberately. The worker's id was minted a
// moment ago and belongs to nothing else that was ever written to the log, so
// selecting on it already leaves out every older entry, and a lower bound on
// time would only add a way to go wrong: a host clock can step backwards
// between subscribing and the worker exiting, and an exit event stamped before
// the subscription would then be dropped as history — a finished operation
// left looking like a worker that died without saying anything.
func subscribeMuxOpExit(
	ctx context.Context,
	svc *cmdman.Service,
	id string,
) (*cmdman.EventsSubscription, error) {
	sub, err := svc.Events(ctx, cmdman.EventsRequest{
		FromStart:  true,
		IDFilter:   []string{id},
		TypeFilter: []model.EventType{model.EventTypeExited, model.EventTypeFailed},
	})
	if err != nil {
		return nil, fmt.Errorf("watch mux op worker: %w", err)
	}
	return sub, nil
}

// muxOpCreateRequest describes the worker as a command record.
//
// It is a normal command in every respect that matters, with three settings
// that are not the usual defaults:
//
//   - auto-remove, because an applied dashboard should not leave a row behind
//     in `cmdman ls` for the user to clean up;
//   - no restart policy, because a failed apply that respawns would keep
//     rebuilding the window under the user instead of stopping and saying so;
//   - a log file of its own under the runtime dir, because auto-remove deletes
//     the command directory the output would otherwise live in. The runtime dir
//     is deliberately ephemeral — cleared on reboot, like the event log — which
//     is the right lifetime for a record kept only to explain what an operation
//     did.
func muxOpCreateRequest(opts MuxOpOptions) (cmdman.CreateRequest, error) {
	cfg := opts.Svc.Config()
	logPath, err := muxOpLogPath(cfg, opts.LogName)
	if err != nil {
		return cmdman.CreateRequest{}, err
	}
	argv, err := muxOpArgv(cfg, opts.Argv)
	if err != nil {
		return cmdman.CreateRequest{}, err
	}
	// The worker has to find the multiplexer the invoker was looking at, and
	// $TMUX (the socket it names, and the pane it was typed in) is the only
	// thing that says which one that is, so the invoker's environment is passed
	// through as-is rather than rebuilt from this process's own.
	env := append(slices.Clone(opts.Env), envMuxOp+"=1")

	return cmdman.CreateRequest{
		Name:          muxOpCommandName(opts.LogName),
		Dir:           opts.Dir,
		Env:           env,
		ImportHostEnv: new(false),
		RestartPolicy: model.RestartPolicyNo,
		AutoRemove:    true,
		LogDriver:     logdriver.DriverK8sFile,
		LogOpts: map[string]string{
			logdriver.LogOptPath:    logPath,
			logdriver.LogOptMaxSize: muxOpLogMaxSize,
			logdriver.LogOptMaxFile: muxOpLogMaxFile,
		},
		Argv: argv,
	}, nil
}

// muxOpLogPath returns where a mux operation's output is kept, creating the
// directory the first time one runs.
func muxOpLogPath(cfg cmdman.CmdmanConfig, logName string) (string, error) {
	if cfg.RuntimeDir == "" {
		return "", errors.New("mux op: runtime dir is empty")
	}
	dir := filepath.Join(cfg.RuntimeDir, "mux")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mux op: create log dir: %w", err)
	}
	return filepath.Join(dir, logName+".log"), nil
}

// muxOpArgv builds the worker's command line: this binary, re-run with the
// arguments the user typed.
//
// The resolved data and runtime dirs — and the config file, when one was named
// — are passed ahead of them the same way the monitor passes them to its own
// re-exec: a child inherits the environment but not the flags, so without them
// the worker would resolve its own store and its own file-only settings. The
// user's own copies of those flags come after and win, which resolves to the
// same values, since that is where the config being forwarded came from.
func muxOpArgv(cfg cmdman.CmdmanConfig, verbArgv []string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("mux op: locate cmdman binary: %w", err)
	}
	argv := []string{
		exe,
		"--data-dir", cfg.DataDir,
		"--runtime-dir", cfg.RuntimeDir,
	}
	if cfg.ConfigPath != "" {
		argv = append(argv, "--config", cfg.ConfigPath)
	}
	return append(argv, verbArgv...), nil
}

// createMuxOpCommand registers the worker and returns its command id.
//
// The name is derived from the window the operation acts on, so registering it
// is also how two operations are kept off that window: the store refuses a
// second command by the same name.
//
// A conflict does not by itself mean an operation is running, though. A worker
// that was killed outright leaves its record behind, and a record like that
// must not lock the window out for good. So a conflict is looked at once:
// listing is what notices a monitor that died without cleaning up — it flips
// the record, or removes it when the command was auto-removed — and what is
// left is read by [muxOpRecordIsLive]. A leftover is removed, and the
// registration is tried one more time.
func createMuxOpCommand(
	ctx context.Context,
	svc *cmdman.Service,
	req cmdman.CreateRequest,
) (string, error) {
	res, err := svc.Create(ctx, req)
	if err == nil {
		return res.ID, nil
	}
	createErr := err

	existing, err := findCommandByName(ctx, svc, req.Name)
	if err != nil {
		return "", errors.Join(
			fmt.Errorf("register mux op worker: %w", createErr),
			fmt.Errorf("inspect existing %q: %w", req.Name, err),
		)
	}
	if existing != nil {
		if muxOpRecordIsLive(*existing, time.Now()) {
			return "", fmt.Errorf(
				"mux op already running for this window (command %s, state %s)",
				existing.ID,
				existing.State,
			)
		}
		if err := removeMuxOpLeftover(ctx, svc, existing.ID); err != nil {
			return "", fmt.Errorf("remove leftover mux op %q: %w", req.Name, err)
		}
	}

	res, err = svc.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("register mux op worker: %w", err)
	}
	return res.ID, nil
}

// removeMuxOpLeftover takes the finished worker's record off the books.
//
// Two invocations aimed at one window can read the same leftover and both go to
// remove it; the one that gets there second is told there is no such command,
// because the id it names has already been deleted. That is the outcome asked
// for, not a failure — the record is gone either way — so the removal counts as
// done whenever the record is no longer there, and only a record that outlived
// the attempt is reported.
func removeMuxOpLeftover(ctx context.Context, svc *cmdman.Service, id string) error {
	results, err := svc.Remove(ctx, cmdman.RemoveRequest{Targets: []string{id}})
	if err == nil {
		for _, r := range results {
			if r.Err != nil {
				err = r.Err
				break
			}
		}
	}
	if err == nil {
		return nil
	}
	if present, findErr := findCommandByID(ctx, svc, id); findErr == nil && !present {
		return nil
	}
	return err
}

// muxOpRecordIsLive reports whether the record found under the worker's name
// belongs to an operation still under way, as opposed to a leftover free to be
// removed.
//
// A running record answers for itself: the listing that produced it has already
// checked that a monitor is behind it. A record that has not got that far
// cannot be checked that way — there is no monitor yet — so its age answers
// instead, within [muxOpPreRunGrace]. Anything else has finished, one way or
// another.
func muxOpRecordIsLive(entry store.CommandEntry, now time.Time) bool {
	switch entry.State {
	case model.EventTypeRunning:
		return true
	case model.EventTypeCreated, model.EventTypeStarting:
		created, err := time.Parse(time.RFC3339, entry.CreatedAt)
		if err != nil {
			// Registering a command always stamps it, so a stamp that cannot be
			// read did not come from a registration a moment ago — it comes from
			// a store old enough to predate the column. Reading it as live would
			// lock the window out for good.
			return false
		}
		return now.Sub(created) < muxOpPreRunGrace
	default:
		return false
	}
}

// findCommandByName returns the command registered under name, or nil when
// there is none. Listing is deliberate: it is the path that reconciles records
// whose monitor died, which is exactly the state a leftover is in.
func findCommandByName(
	ctx context.Context,
	svc *cmdman.Service,
	name string,
) (*store.CommandEntry, error) {
	entries, err := svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i], nil
		}
	}
	return nil, nil
}
