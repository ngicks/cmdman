package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/cmdman/cmdman/tui"
	"github.com/ngicks/go-common/contextkey"
)

const (
	// composeDownExitPoll is how often a follower reads the job's record while it
	// waits for the job's exit. The exit event usually answers first; the record
	// is what answers for a monitor that died without appending one, which the
	// listing turns into a failed record.
	composeDownExitPoll = time.Second
	// composeDownGoneGrace is how long a follower keeps waiting for the exit event
	// once the job's record is gone. A job is never auto-removed, so its record
	// goes only when a later launch replaces a job that is over, and the exit
	// event of that job is in the log by then.
	composeDownGoneGrace = 5 * time.Second
	// composeDownStartWait is how long a follower waits for a launch still on its
	// way to get the job going. Starting a command gives its monitor about five
	// seconds to report in, counted from the spawn, while the follower counts from
	// its first look at the record, which can come before the spawn.
	composeDownStartWait = 10 * time.Second
)

// errComposeDownJobNotStarted is the end of a job whose record was left created
// and that nothing got going: its down never ran.
var errComposeDownJobNotStarted = errors.New("compose down job did not start")

// followComposeDownJob follows a compose down job (see tui.DownStream).
//
// Live summaries come off the job's output as it is written, and only for a job
// that was not over when it was found: the output of one that was is history,
// and the stream reports its end alone. The final summary is read again from the
// stored output once the job is over. The live read may have started too early
// or been cut off, while the stored output is complete.
func followComposeDownJob(
	ctx context.Context,
	svc *cmdman.Service,
	job tui.DownJob,
) (tui.DownStream, error) {
	return followComposeDownJobWithin(
		ctx, svc, job, composeDownExitPoll, composeDownGoneGrace, composeDownStartWait,
	)
}

// followComposeDownJobWithin is [followComposeDownJob] with its waits spelled
// out, so a test can drive them faster than a teardown would.
func followComposeDownJobWithin(
	ctx context.Context,
	svc *cmdman.Service,
	job tui.DownJob,
	interval, grace, startWait time.Duration,
) (tui.DownStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	// Subscribed before any output is read, and from the start of the log, so an
	// exit that lands at any point after this, or landed before it, is seen.
	sub, err := svc.Events(ctx, cmdman.EventsRequest{
		FromStart:  true,
		IDFilter:   []string{job.ID},
		TypeFilter: []model.EventType{model.EventTypeExited, model.EventTypeFailed},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("follow compose down job: %w", err)
	}
	s := &composeDownStream{ch: make(chan tui.DownSummary, 1), cancel: cancel}
	go s.run(ctx, svc, sub, job, interval, grace, startWait)
	return s, nil
}

// composeDownStream is the tui.DownStream of one followed job. The channel holds
// one summary, and publish replaces a summary nobody has read yet: every
// summary is the running total, so only the latest one is worth reading.
type composeDownStream struct {
	ch     chan tui.DownSummary
	cancel context.CancelFunc

	mu    sync.Mutex
	final tui.DownSummary
	err   error
}

func (s *composeDownStream) Summaries() <-chan tui.DownSummary { return s.ch }

func (s *composeDownStream) Result() (tui.DownSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.final, s.err
}

// Close stops following. The job itself goes on: it is a command of its own.
func (s *composeDownStream) Close() error {
	s.cancel()
	return nil
}

func (s *composeDownStream) publish(summary tui.DownSummary) {
	for {
		select {
		case s.ch <- summary:
			return
		default:
		}
		select {
		case <-s.ch:
		default:
		}
	}
}

func (s *composeDownStream) run(
	ctx context.Context,
	svc *cmdman.Service,
	sub *cmdman.EventsSubscription,
	job tui.DownJob,
	interval, grace, startWait time.Duration,
) {
	defer func() { _ = sub.Close() }()

	var live downProgress
	if !job.Finished {
		s.publish(tui.DownSummary{Running: true})
		live = readComposeDownOutput(ctx, svc, job.ID, true, s.publish)
	}
	code, exitErr := waitComposeDownJobExit(ctx, svc, sub, job.ID, interval, grace, startWait)

	final := live
	if ctx.Err() == nil {
		// Read back after a failure too, which the output of a failed down
		// explains. A record that is gone has no output left to read, and what the
		// live read got is all there is.
		if stored, ok := readStoredComposeDownOutput(ctx, svc, job.ID); ok {
			final = stored
		}
	}
	err := exitErr
	if err == nil && code != 0 {
		err = composeDownExitErr(code, final.lastErr)
	}

	s.mu.Lock()
	s.final, s.err = final.summary, err
	s.mu.Unlock()
	close(s.ch)
}

// composeDownExitErr is the failure a non-zero exit of the job stands for. The
// job's last error line, the one cmdman writes on its way out, says what
// failed; the status alone is what is left when it wrote none.
func composeDownExitErr(code int, lastErr string) error {
	if lastErr == "" {
		return &ExitCodeError{Code: code}
	}
	return fmt.Errorf("%w: %s", &ExitCodeError{Code: code}, lastErr)
}

// waitComposeDownJobExit waits for the job's run to be over and returns its exit
// status, or the failure that stands for a run that ended without one.
//
// The record answers first, which is what makes a job that was already over
// answer at once, and then answers again at every interval, beside the exit
// event. A record that is gone gives the exit event grace to turn up before the
// job is declared gone.
//
// A record left created is over at once when nothing is bringing it up (see
// composeDownJobNeverRan): no run will ever report an exit for it. One that a
// launch or a monitor is still bringing up gets startWait to reach starting.
func waitComposeDownJobExit(
	ctx context.Context,
	svc *cmdman.Service,
	sub *cmdman.EventsSubscription,
	id string,
	interval, grace, startWait time.Duration,
) (int, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var goneUntil, startBy time.Time
	for {
		entry, err := findCommandEntryByID(ctx, svc, id)
		switch {
		case err != nil:
			// A store that cannot be read right now says nothing about the job.
		case entry == nil:
			if goneUntil.IsZero() {
				goneUntil = time.Now().Add(grace)
			} else if time.Now().After(goneUntil) {
				return 0, fmt.Errorf("compose down job %s is gone without reporting an exit", id)
			}
		default:
			goneUntil = time.Time{}
			if code, over, err := composeDownJobEnd(
				entry.State, entry.ExitCode, stateError(entry.StateJSON),
			); over {
				return code, err
			}
			if entry.State != model.EventTypeCreated {
				break
			}
			if startBy.IsZero() {
				startBy = time.Now().Add(startWait)
			}
			neverRan, err := composeDownJobNeverRan(ctx, svc, *entry)
			if err != nil {
				// A probe that failed says nothing either way, and startWait still
				// bounds the wait.
				contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
					ctx, "compose down job: check for a launch", "id", id, "error", err,
				)
			}
			if neverRan || time.Now().After(startBy) {
				return 0, errComposeDownJobNotStarted
			}
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case rec, ok := <-sub.Records():
			if !ok {
				return 0, errors.New(
					"stopped watching before the compose down job reported an exit",
				)
			}
			if rec.Err != nil {
				continue
			}
			if code, over, err := composeDownJobEnd(
				rec.Event.Type, rec.Event.ExitCode, rec.Event.Error,
			); over {
				return code, err
			}
		case <-ticker.C:
		}
	}
}

// composeDownJobEnd reads a state, from the job's record or its exit event, as
// the end of its run. over is false for a run that is not over.
func composeDownJobEnd(
	state model.EventType,
	exitCode *int,
	failure string,
) (code int, over bool, err error) {
	switch state {
	case model.EventTypeExited:
		if exitCode != nil {
			code = *exitCode
		}
		return code, true, nil
	case model.EventTypeFailed:
		if failure != "" {
			return 0, true, fmt.Errorf("compose down job failed: %s", failure)
		}
		return 0, true, errors.New("compose down job failed")
	default:
		return 0, false, nil
	}
}

func stateError(state *model.CommandState) string {
	if state == nil {
		return ""
	}
	return state.Error
}

// findCommandEntryByID returns the record of id, nil when there is none.
func findCommandEntryByID(
	ctx context.Context,
	svc *cmdman.Service,
	id string,
) (*store.CommandEntry, error) {
	entries, err := svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(entries, func(e store.CommandEntry) bool { return e.ID == id })
	if i < 0 {
		return nil, nil
	}
	return &entries[i], nil
}
