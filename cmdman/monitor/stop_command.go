package monitor

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// stopCommandReapWait bounds how long a stop sequence waits for the members of
// its stop command's process group to be gone once it has killed the group.
const stopCommandReapWait = 2 * time.Second

// stopSequence is a graceful stop of a run whose command has a stop command,
// carried out the way systemd carries out ExecStop=: the stop command first,
// bounded by the stop's grace period, then the stop signal to whatever the run
// still has, with the stop's deadline armed for one more grace period. The
// monitor owns every step, so a client that goes away mid-stop changes
// nothing.
//
// runOnce makes one per run and publishes it under procMu next to the run's
// handles. The first graceful stop begins it, and a later one leaves it be. A
// SIGKILL stop kills a stop command that is still running.
type stopSequence struct {
	argv []string
	dir  string
	// env is the run's hook environment. CMDMAN_MAIN_PID joins it once the stop
	// that begins the sequence names the process.
	env []string

	// begin hands the stop that begins the sequence to its goroutine. It has
	// room for that one stop, so the stop never waits on it.
	begin chan stopBegin
	// cancel ends the sequence early: a SIGKILL stop calls it to kill a stop
	// command that is still running, and the run end calls it to retire a
	// sequence no stop began.
	cancel context.CancelFunc
	// done is closed once the goroutine is over. It is a channel and not an
	// errgroup because the run end waits on it with a bound instead of joining
	// it.
	done chan struct{}

	// begun and until are guarded by Monitor.procMu. begun is set by the stop
	// that sends on begin, and until is when the run end gives up waiting for
	// the sequence.
	begun bool
	until time.Time
}

// stopBegin is what the stop that begins a sequence hands over.
type stopBegin struct {
	sig syscall.Signal
	// grace is the stop's timeout, which the deadline armed with the stop
	// signal waits out before SIGKILL. Zero arms no deadline.
	grace time.Duration
	// bound is how long the stop command may run.
	bound time.Duration
	// mainPID is the run's own process, 0 once it has been reaped.
	mainPID int
}

// startStopSequence makes the stop sequence of the run about to be published
// and starts the goroutine that carries it out once a stop begins it. ctx is
// the run's, so the monitor shutting down kills a stop command still running.
// It returns nil for a command without a stop command.
func (m *Monitor) startStopSequence(
	ctx context.Context,
	stop *model.StopCommand,
	dir string,
	env []string,
) *stopSequence {
	if stop == nil || len(stop.Args) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	seq := &stopSequence{
		argv:   slices.Clone(stop.Args),
		dir:    dir,
		env:    env,
		begin:  make(chan stopBegin, 1),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(seq.done)
		select {
		case b := <-seq.begin:
			m.runStopSequence(ctx, seq, b)
		case <-ctx.Done():
		}
	}()
	return seq
}

// beginStopSequence begins seq for a graceful stop with sig and timeout. A
// sequence a stop has begun already is left as it is: a stop in progress is
// never started over, and the stop command runs once. The caller holds
// procMu.
func (m *Monitor) beginStopSequence(
	seq *stopSequence,
	sig syscall.Signal,
	timeout time.Duration,
) {
	if seq.begun {
		return
	}
	seq.begun = true
	// A stop without a timeout leaves the SIGKILL to its client, but the stop
	// command still gets a bound: the run end waits for it, and nothing it
	// waits on there goes unbounded.
	bound := timeout
	if bound <= 0 {
		bound = sweepBound
	}
	// Past the stop command's own bound, sweepBound covers killing and reaping
	// its group. Only a stop command that SIGKILL cannot end takes that long.
	seq.until = time.Now().Add(bound + sweepBound)
	mainPID := 0
	if m.cmd != nil && m.cmd.Process != nil {
		mainPID = m.cmd.Process.Pid
	}
	seq.begin <- stopBegin{sig: sig, grace: timeout, bound: bound, mainPID: mainPID}
}

// runStopSequence carries out a sequence a stop has begun.
func (m *Monitor) runStopSequence(ctx context.Context, seq *stopSequence, b stopBegin) {
	runStopCommand(
		ctx,
		seq.argv,
		seq.dir,
		config.WithStopCommandEnv(seq.env, b.mainPID),
		b.bound,
	)
	// A SIGKILL stop or the monitor's shutdown cut the stop command short, and a
	// graceful signal after either would only get in their way.
	if ctx.Err() != nil {
		return
	}
	m.sendStopSignal(ctx, seq, b.sig, b.grace)
}

// runStopCommand runs a stop command with its output discarded and gives it
// bound to finish. Like a hook it leads a process group of its own in the
// monitor's session, so the survivor sweep never takes it for something the
// command left behind. Once it is over - exited, out of time or cancelled
// through ctx - its whole group is killed and reaped, so nothing it started
// outlives the stop. How it ended is only logged: the stop goes on regardless.
func runStopCommand(
	ctx context.Context,
	argv []string,
	dir string,
	env []string,
	bound time.Duration,
) {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	cmdCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	prepHookAttrs(cmd)
	// The bound is all a stop command gets, so running out of it kills the group
	// instead of asking it to stop.
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() == nil {
			logger.WarnContext(ctx, "start stop command",
				slog.String("program", argv[0]),
				slog.String("error", err.Error()),
			)
		}
		return
	}
	err := cmd.Wait()

	pgid := cmd.Process.Pid
	_ = signalProcessGroup(pgid, syscall.SIGKILL)
	// The reap has a bound of its own and ignores ctx's cancellation: a stop
	// command cut short by a SIGKILL stop or by the shutdown still leaves
	// members to reap.
	reapCtx, cancelReap := context.WithTimeout(context.WithoutCancel(ctx), stopCommandReapWait)
	reapProcessGroup(reapCtx, pgid)
	cancelReap()

	switch {
	case err == nil, ctx.Err() != nil:
		// Exited cleanly, or cut short by a SIGKILL stop or the shutdown, which
		// is no failure of the stop command's own.
	case cmdCtx.Err() != nil:
		logger.WarnContext(ctx, "stop command did not finish within the grace period; killed it",
			slog.String("program", argv[0]),
			slog.Duration("grace", bound),
		)
	default:
		logger.WarnContext(ctx, "stop command failed",
			slog.String("program", argv[0]),
			slog.String("error", err.Error()),
		)
	}
}

// sendStopSignal is the step of a sequence that follows its stop command: the
// stop signal to whatever is left in the run's process group, and the deadline
// armed for one more grace period. A group with nobody left in it gets no
// signal.
func (m *Monitor) sendStopSignal(
	ctx context.Context,
	seq *stopSequence,
	sig syscall.Signal,
	grace time.Duration,
) {
	m.procMu.Lock()
	// A SIGKILL stop went out while the stop command ran, or the run end gave up
	// waiting for this sequence. Either way escalating is no longer this
	// sequence's business.
	if m.stopKilled.Load() || m.stopSeq != seq {
		m.procMu.Unlock()
		return
	}
	// The child leads the run's process group, so the group SignalProcess
	// reaches through a live child and the one the run keeps for its leftovers
	// are one and the same.
	pgid := m.runPgid
	// The deadline is armed even when nobody is left in the group. A survivor
	// that leads a process group of its own inside the command's session is out
	// of the stop signal's reach, and the deadline's SIGKILL is what lets the
	// run end kill it by pid. A run with nothing left at all ends right after
	// this sequence, and the run end disarms the deadline.
	if grace > 0 {
		if m.stopDeadline != nil {
			m.stopDeadline.Stop()
		}
		m.stopDeadline = time.AfterFunc(grace, m.escalateStop)
	}
	m.procMu.Unlock()

	if errors.Is(signalProcessGroup(pgid, 0), syscall.ESRCH) {
		return
	}
	send := m.stopSignalFn
	if send == nil {
		send = signalProcessGroup
	}
	if err := send(pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx, "send stop signal",
			slog.String("id", m.ID),
			slog.String("error", err.Error()),
		)
	}
}

// finishStopSequence lets the run's stop sequence finish before the run gives
// its process group up: a stop command still running is let finish or run out
// its bound, and the stop signal that follows it still has the group to aim
// at. A sequence no stop has begun is retired instead, and a stop that lands
// from then on takes the plain path, whose deadline the run end disarms like
// any other.
func (m *Monitor) finishStopSequence(ctx context.Context, seq *stopSequence) {
	if seq == nil {
		return
	}
	defer seq.cancel()

	m.procMu.Lock()
	begun, until := seq.begun, seq.until
	if !begun {
		m.stopSeq = nil
	}
	m.procMu.Unlock()
	if !begun {
		seq.cancel()
		<-seq.done
		return
	}

	// The run can reach its end long after the sequence is over: a survivor may
	// have held it until the deadline the sequence armed. until may well have
	// passed by then, and a select that found both ready could pick either.
	select {
	case <-seq.done:
		return
	default:
	}
	bound := time.NewTimer(time.Until(until))
	defer bound.Stop()
	select {
	case <-seq.done:
	case <-bound.C:
		// Only a stop command that SIGKILL cannot end gets here. It is left
		// behind, and the sequence finds itself retired if it ever gets past it.
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"stop command still running past its bound; leaving it behind",
			slog.String("id", m.ID),
		)
	}
}
