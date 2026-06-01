package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/muesli/cancelreader"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

// serviceBackend is the production tui.Backend, adapting *cmdman.Service and
// *compose.Service to the data the TUI model renders and the actions it runs.
// It lives in the cli package (not tui) so it can call cli.Attach without an
// import cycle.
type serviceBackend struct {
	svc     *cmdman.Service
	compose *compose.Service
	cwd     string
}

// newServiceBackend builds a tui.Backend over the given cmdman service.
func newServiceBackend(svc *cmdman.Service) tui.Backend {
	return &serviceBackend{
		svc:     svc,
		compose: compose.NewService(svc),
		cwd:     currentDir(),
	}
}

func (b *serviceBackend) Cwd() string { return b.cwd }

// ListCommands lists compose-scoped commands by requiring the compose project
// and workdir labels; standalone commands (without those labels) are dropped.
func (b *serviceBackend) ListCommands(ctx context.Context) ([]tui.CommandInfo, error) {
	entries, err := b.svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	return composeCommandInfos(entries), nil
}

// composeCommandInfos projects store entries to command rows, keeping only
// compose-managed commands (those carrying both the project and workdir
// labels) and dropping standalone commands.
func composeCommandInfos(entries []store.CommandEntry) []tui.CommandInfo {
	var out []tui.CommandInfo
	for _, e := range entries {
		var labels map[string]string
		var driver logdriver.LogDriver
		if e.ConfigJSON != nil {
			labels = e.ConfigJSON.Labels
			driver = e.ConfigJSON.LogDriver
		}
		project, hasProject := labels[compose.LabelProject]
		workdir, hasWorkdir := labels[compose.LabelWorkdir]
		if !hasProject || !hasWorkdir {
			continue // standalone command, out of v1 scope
		}
		name := e.Name
		if cmd := labels[compose.LabelCommand]; cmd != "" {
			name = cmd
		}
		out = append(out, tui.CommandInfo{
			ID:        e.ID,
			Name:      name,
			Project:   project,
			Workdir:   normalizePath(workdir),
			State:     e.State,
			ExitCode:  e.ExitCode,
			LogDriver: driver,
		})
	}
	return out
}

// ListProjects merges store-known project counts with never-run projects found
// under the default compose directory. The mux badge is populated by the mux
// layer; here HasMux is left false.
func (b *serviceBackend) ListProjects(ctx context.Context) ([]tui.ProjectInfo, error) {
	summaries, err := b.compose.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	named, _ := compose.ListNamedProjects()
	return mergeProjectInfos(summaries, named), nil
}

// mergeProjectInfos merges store-known project summaries with never-run named
// projects (which appear with zero commands). Named projects already present in
// the summaries are not duplicated; the merge key is the project name.
func mergeProjectInfos(summaries []compose.ProjectSummary, named []string) []tui.ProjectInfo {
	seen := map[string]bool{}
	var out []tui.ProjectInfo
	for _, s := range summaries {
		seen[s.Project] = true
		out = append(out, tui.ProjectInfo{
			Name:     s.Project,
			Path:     s.ComposeFile,
			Workdir:  normalizePath(s.WorkDir),
			Commands: s.Commands,
			Running:  s.Running,
			Exited:   s.Exited,
			Failed:   s.Failed,
			Modified: modifiedLabel(s.ComposeFile),
		})
	}
	for _, n := range named {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, tui.ProjectInfo{Name: n})
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
		// Coalesce bursts: drop a new signal when one is already pending.
		select {
		case e.ch <- sig:
		default:
		}
	}
}

func (e *eventStream) Signals() <-chan tui.EventSignal { return e.ch }
func (e *eventStream) Close() error                    { return e.sub.Close() }

// Logs opens a Tail+Follow reader and streams its lines.
func (b *serviceBackend) Logs(ctx context.Context, id string, tail int) (tui.LogStream, error) {
	rdr, err := b.svc.Logs(ctx, cmdman.LogsRequest{IDOrName: id, Tail: tail, Follow: true})
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

// Attach opens an attach session and hands the terminal to cli.Attach directly
// (not the sticky Cobra path), so detach returns control to the TUI.
func (b *serviceBackend) Attach(ctx context.Context, id string) (string, error) {
	session, err := b.svc.OpenAttachSession(ctx, id)
	if err != nil {
		return "", err
	}

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdinPipe, err := newCancelStdin()
	if err != nil {
		return "", err
	}

	opts := AttachOptions{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		StdinPipe:  stdinPipe,
		StdoutPipe: nopWriteCloser{os.Stdout},
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

// newCancelStdin wraps os.Stdin in a cancelable reader so that Attach's
// StdinPipe.Close() stops reading os.Stdin entirely, leaving no goroutine
// racing the TUI's input reader after detach.
func newCancelStdin() (io.ReadCloser, error) {
	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return nil, err
	}
	return &cancelStdin{cr: cr}, nil
}

type cancelStdin struct {
	cr cancelreader.CancelReader
}

func (c *cancelStdin) Read(p []byte) (int, error) { return c.cr.Read(p) }
func (c *cancelStdin) Close() error {
	c.cr.Cancel()
	return nil
}

// nopWriteCloser writes to the terminal directly; Close must not close
// os.Stdout (the TUI keeps using it after attach returns).
type nopWriteCloser struct{ w io.Writer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n nopWriteCloser) Close() error                { return nil }

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

// currentDir returns the normalized working directory, or "" if unavailable.
func currentDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return normalizePath(wd)
}

// normalizePath returns an absolute, symlink-resolved, clean path so that
// workdir labels and os.Getwd() compare equal even through symlinks.
func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// modifiedLabel renders a compact "modified <date>" metadata string from a
// compose file's mtime, or "" when unavailable.
func modifiedLabel(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return "modified " + fi.ModTime().Format(time.DateOnly)
}
