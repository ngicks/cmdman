package monitor

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
)

const (
	// hookExecTimeout bounds one hook run. Without it a hook that never exits
	// would hold the queue for the rest of the monitor's life, silently
	// dropping every later event.
	hookExecTimeout = 30 * time.Second
	// hookWaitDelay is how long a hook has to exit after its process group is
	// signaled before it is killed outright.
	hookWaitDelay = 5 * time.Second
	// hookQueueDepth is the number of events that may wait behind the running
	// hook. Deeper queues only postpone the drop while making the hook lag
	// further behind what the command is doing now.
	hookQueueDepth = 1
)

// hookEvent is one captured sequence handed to the dispatcher. Title carries a
// window title for a title event and the notification title for a notification
// event; Body is a notification's message.
type hookEvent struct {
	Kind  model.HookEvent
	Title string
	Body  string
}

// hookInvocation is an event bound to the configuration it was captured under.
// Resolving at enqueue rather than at dequeue is what keeps an event waiting
// behind a slow hook from running with the next run's argv, directory or
// environment: Monitor.runOnce reconfigures the dispatcher while the queue may
// still hold the previous run's event.
type hookInvocation struct {
	event hookEvent
	argv  []string
	dir   string
	env   []string
}

// hookExecFunc runs one hook argv to completion. It is a field on the
// dispatcher so tests can observe dispatch without spawning processes.
type hookExecFunc func(ctx context.Context, argv []string, dir string, env []string) error

// hookDispatcher runs the configured argv hooks for captured sequences. Events
// arrive from the vt callbacks, which run under Monitor.outputMu, so dispatch
// only ever enqueues: a single worker goroutine serializes the runs, and an
// event that finds the queue full is dropped rather than made to wait.
type hookDispatcher struct {
	logger *slog.Logger
	execFn hookExecFunc

	// mu guards the configuration a run installs (Monitor.runOnce re-reads the
	// command config on every restart). It is a leaf lock, taken for copies
	// only - never while a hook runs.
	mu     sync.Mutex
	layers model.HookLayers
	dir    string
	env    []string

	events  chan hookInvocation
	group   errgroup.Group
	cancel  context.CancelFunc
	dropped atomic.Uint64
}

func newHookDispatcher(logger *slog.Logger) *hookDispatcher {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &hookDispatcher{
		logger: logger,
		execFn: execHook,
		events: make(chan hookInvocation, hookQueueDepth),
	}
}

// configure installs the hook configuration for the run that is about to start.
// env is the environment the supervised command itself gets, so a hook sees the
// same context (including the CMDMAN_CMD_ID family).
func (d *hookDispatcher) configure(layers model.HookLayers, dir string, env []string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.layers, d.dir, d.env = layers, dir, env
}

// blocks reports which event kinds must be stripped from the bytes a viewer
// receives.
func (d *hookDispatcher) blocks() hookBlocks {
	if d == nil {
		return hookBlocks{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return hookBlocks{
		Bell:         d.layers.Resolve(model.HookEventBell).Blocks(),
		Title:        d.layers.Resolve(model.HookEventTitle).Blocks(),
		Notification: d.layers.Resolve(model.HookEventNotification).Blocks(),
	}
}

// start launches the worker. The hooks of a run die with the monitor: ctx
// cancellation signals a running hook's process group and close waits for it.
func (d *hookDispatcher) start(ctx context.Context) {
	if d == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.group.Go(func() error {
		d.run(ctx)
		return nil
	})
}

// close stops accepting events and waits for the running hook to be gone. It is
// bounded: the run is cancelled first, and exec.Cmd.WaitDelay kills a hook that
// ignores the signal.
func (d *hookDispatcher) close() {
	if d == nil || d.cancel == nil {
		return
	}
	d.cancel()
	_ = d.group.Wait()
	// Reporting the total once keeps a flood that outran its hook visible
	// without logging from the latch path itself.
	if dropped := d.dropped.Load(); dropped > 0 {
		d.logger.Warn("hook events dropped while a hook was running",
			slog.Uint64("count", dropped),
		)
	}
}

func (d *hookDispatcher) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case inv := <-d.events:
			d.runHook(ctx, inv)
		}
	}
}

// dispatch resolves ev against the current configuration and queues the two
// together. It never blocks and never waits on anything that could: it runs
// inside a vt callback while outputMu is held, so a busy queue costs the event,
// not the command's output path.
func (d *hookDispatcher) dispatch(ev hookEvent) {
	if d == nil {
		return
	}
	argv, dir, env := d.resolve(ev.Kind)
	if len(argv) == 0 {
		return
	}
	select {
	case d.events <- hookInvocation{event: ev, argv: argv, dir: dir, env: env}:
	default:
		d.dropped.Add(1)
	}
}

func (d *hookDispatcher) resolve(kind model.HookEvent) (argv []string, dir string, env []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.layers.Resolve(kind).Exec, d.dir, d.env
}

func (d *hookDispatcher) runHook(ctx context.Context, inv hookInvocation) {
	ctx, cancel := context.WithTimeout(ctx, hookExecTimeout)
	defer cancel()
	env := config.WithHookEventEnv(inv.env, config.HookEventEnv{
		Event: inv.event.Kind,
		Title: inv.event.Title,
		Body:  inv.event.Body,
	})
	if err := d.execFn(ctx, inv.argv, inv.dir, env); err != nil {
		d.logger.WarnContext(ctx, "hook failed",
			slog.String("event", string(inv.event.Kind)),
			slog.String("program", inv.argv[0]),
			slog.String("error", err.Error()),
		)
	}
}

// execHook runs a hook argv with its output discarded. The hook gets its own
// process group and the same cancellation contract as a supervised command, so
// a hook that spawned children takes them down with it on shutdown.
func execHook(ctx context.Context, argv []string, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	prepProcessAttrs(cmd, false)
	cmd.WaitDelay = hookWaitDelay
	return cmd.Run()
}
