package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/muesli/cancelreader"
	"github.com/ngicks/go-common/iopipe"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// ListCommands lists every command. Compose-managed commands (carrying the
// project and workdir labels) are grouped by project; standalone commands keep
// an empty project and group under their working directory.
//
// Each entry's runtime state is dialed from its monitor as the listing is
// built. This is the same one-shot fan-out `ls` does, repeated on the TUI's
// refresh cadence: a live subscription to the monitors' WatchRuntimeState
// stream is a later phase, and a poll per reload is what the model's
// load-and-rebuild shape already supports.
func (b *serviceBackend) ListCommands(ctx context.Context) ([]tui.CommandInfo, error) {
	entries, err := b.svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	// A refresh that stalls is worse than one showing last cycle's titles, so
	// the whole fan-out is bounded, not only each dial inside it.
	return commandInfos(entries, RuntimeStates(ctx, b.svc, entries)), nil
}

// commandInfos projects store entries to command rows, merging in the runtime
// state keyed by command ID. A compose-managed command (carrying both the
// project and workdir labels) reports its compose project name and the labelled
// workdir; a standalone command reports an empty project and falls back to its
// configured working directory, so it still appears in the TUI rather than
// being dropped. A command whose monitor did not answer keeps the zero runtime
// fields.
func commandInfos(
	entries []store.CommandEntry,
	runtime map[string]cmdman.RuntimeState,
) []tui.CommandInfo {
	var out []tui.CommandInfo
	for _, e := range entries {
		var labels map[string]string
		var driver logdriver.LogDriver
		var dir string
		var tty bool
		if e.ConfigJSON != nil {
			labels = e.ConfigJSON.Labels
			driver = e.ConfigJSON.LogDriver
			dir = e.ConfigJSON.Dir
			tty = e.ConfigJSON.Tty
		}
		project := labels[compose.LabelProject]
		workdir, hasWorkdir := labels[compose.LabelWorkdir]
		if !hasWorkdir {
			workdir = dir
		}
		name := e.Name
		if cmd := labels[compose.LabelCommand]; cmd != "" {
			name = cmd
		}
		rs := runtime[e.ID]
		out = append(out, tui.CommandInfo{
			ID:         e.ID,
			Name:       name,
			Project:    project,
			Workdir:    normalizePath(workdir),
			State:      e.State,
			ExitCode:   e.ExitCode,
			LogDriver:  driver,
			Tty:        tty,
			Title:      rs.Title,
			Status:     rs.Status,
			Detail:     rs.Detail,
			BellUnread: rs.BellUnread,
		})
	}
	return out
}

func (b *serviceBackend) Start(ctx context.Context, id string) error {
	return b.svc.Start(ctx, id)
}

func (b *serviceBackend) Stop(ctx context.Context, id string) error {
	res, err := b.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
	if err != nil {
		return err
	}
	return firstStopErr(res)
}

func (b *serviceBackend) Restart(ctx context.Context, id string) error {
	res, err := b.svc.Restart(ctx, cmdman.RestartRequest{Targets: []string{id}})
	if err != nil {
		return err
	}
	return firstRestartErr(res)
}

func (b *serviceBackend) Remove(ctx context.Context, id string, force bool) error {
	res, err := b.svc.Remove(ctx, cmdman.RemoveRequest{Targets: []string{id}, Force: force})
	if err != nil {
		return err
	}
	return firstRemoveErr(res)
}

// Events subscribes to the local event-log tail and emits a coalesced change
// signal per record.
func (b *serviceBackend) Events(ctx context.Context) (tui.EventStream, error) {
	sub, err := b.svc.Events(ctx, cmdman.EventsRequest{})
	if err != nil {
		return nil, err
	}
	es := &eventStream{sub: sub, ch: make(chan tui.EventSignal, 1)}
	go es.pump()
	return es, nil
}

type eventStream struct {
	sub *cmdman.EventsSubscription
	ch  chan tui.EventSignal
}

func (e *eventStream) pump() {
	defer close(e.ch)
	for rec := range e.sub.Records() {
		sig := tui.EventSignal{}
		if rec.Err != nil {
			sig.Err = rec.Err
		}
		select {
		case e.ch <- sig:
		default:
		}
	}
}

func (e *eventStream) Signals() <-chan tui.EventSignal { return e.ch }
func (e *eventStream) Close() error                    { return e.sub.Close() }

// Logs opens a sticky Tail+Follow reader and streams its lines. Sticky keeps
// the preview live across command restarts: when the running instance exits, a
// meta line records it and the reader resumes on the next start — so the
// preview keeps showing live output without the user re-selecting the command
// (a plain Follow reader would stop at the first exit and never reconnect).
func (b *serviceBackend) Logs(ctx context.Context, id string, tail int) (tui.LogStream, error) {
	rdr, err := b.svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id, Tail: tail, Sticky: true})
	if err != nil {
		return nil, err
	}
	ls := &logStream{rdr: rdr, ch: make(chan tui.LogLine, 64), done: make(chan struct{})}
	go ls.pump()
	return ls, nil
}

type logStream struct {
	rdr       logdriver.Reader
	ch        chan tui.LogLine
	done      chan struct{}
	closeOnce sync.Once
}

func (l *logStream) pump() {
	defer close(l.ch)
	for rec := range l.rdr.Records() {
		line := tui.LogLine{}
		if rec.Err != nil {
			line.Err = rec.Err
		} else {
			line.Text = string(rec.Line.Line)
		}
		select {
		case l.ch <- line:
		case <-l.done:
			return
		}
	}
}

func (l *logStream) Lines() <-chan tui.LogLine { return l.ch }

func (l *logStream) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.rdr.Close()
}

// RawView opens a read-only attach session and streams its raw stdout (the
// monitor replays scrollback then follows live). It only ever calls Recv, never
// SendStdin/Resize, so the previewed command and any concurrent interactive
// attach are unaffected. Close releases the session, unblocking the pump.
func (b *serviceBackend) RawView(ctx context.Context, id string) (tui.RawStream, error) {
	session, err := b.svc.OpenAttachSession(ctx, id)
	if err != nil {
		return nil, err
	}
	rs := &rawStream{
		session: session,
		ch:      make(chan tui.RawChunk, 64),
		done:    make(chan struct{}),
	}
	go rs.pump()
	return rs, nil
}

type rawStream struct {
	session   *cmdman.Session
	ch        chan tui.RawChunk
	done      chan struct{}
	closeOnce sync.Once
}

func (r *rawStream) pump() {
	defer close(r.ch)
	for {
		m, err := r.session.RecvMessage()
		if err != nil {
			// io.EOF (the command exited or the stream ended) is a clean close:
			// the consumer observes the channel closing, keeping the last frame.
			if errors.Is(err, io.EOF) {
				return
			}
			select {
			case r.ch <- tui.RawChunk{Err: err}:
			case <-r.done:
			}
			return
		}
		chunk := tui.RawChunk{Bytes: m.Stdout}
		if m.Resize {
			chunk = tui.RawChunk{Resize: &tui.RawSize{Rows: m.Rows, Cols: m.Cols}}
		}
		select {
		case r.ch <- chunk:
		case <-r.done:
			return
		}
	}
}

func (r *rawStream) Chunks() <-chan tui.RawChunk { return r.ch }

func (r *rawStream) Close() error {
	r.closeOnce.Do(func() { close(r.done) })
	return r.session.Close()
}

// Attach opens an attach session and hands the terminal to cli.Attach directly
// (not the sticky Cobra path), so detach returns control to the TUI.
func (b *serviceBackend) Attach(ctx context.Context, id string) (string, error) {
	session, err := b.svc.OpenAttachSession(ctx, id)
	if err != nil {
		return "", err
	}

	// Registered before the cancels below so it runs after them: defers are
	// LIFO, and the forwarding goroutines only exit once attachCtx is done and
	// cancelreader has released a Read in flight on os.Stdin.
	var wg sync.WaitGroup
	defer wg.Wait()

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The stdin controller reads os.Stdin through a cancelable reader:
	// canceling it on return stops reading os.Stdin entirely, leaving no
	// goroutine racing the TUI's input reader after detach. The stdout
	// controller writes os.Stdout directly and never closes it (the TUI
	// keeps using it after attach returns).
	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return "", err
	}
	defer cr.Cancel()

	stdinPipe := iopipe.NewReader(cr)
	stdoutPipe := iopipe.NewWriter(os.Stdout)
	wg.Go(func() { stdinPipe.Run(attachCtx) })
	wg.Go(func() { stdoutPipe.Run(attachCtx) })

	opts := AttachOptions{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		StdinPipe:  stdinPipe,
		StdoutPipe: stdoutPipe,
		DetachKeys: DefaultDetachKeys,
	}
	err = Attach(attachCtx, session, opts)
	switch {
	case err == nil:
		return tui.AttachDetached, nil
	case errors.Is(err, ErrRemoteEOF):
		return tui.AttachExited, nil
	default:
		return "", err
	}
}

func firstStopErr(res []cmdman.StopResult) error {
	for _, r := range res {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

func firstRestartErr(res []cmdman.RestartResult) error {
	for _, r := range res {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

func firstRemoveErr(res []cmdman.RemoveResult) error {
	for _, r := range res {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}
