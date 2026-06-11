package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/muesli/cancelreader"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
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

// ListCommands lists every command. Compose-managed commands (carrying the
// project and workdir labels) are grouped by project; standalone commands keep
// an empty project and group under their working directory.
func (b *serviceBackend) ListCommands(ctx context.Context) ([]tui.CommandInfo, error) {
	entries, err := b.svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	return commandInfos(entries), nil
}

// commandInfos projects store entries to command rows. A compose-managed
// command (carrying both the project and workdir labels) reports its compose
// project name and the labelled workdir; a standalone command reports an empty
// project and falls back to its configured working directory, so it still
// appears in the TUI rather than being dropped.
func commandInfos(entries []store.CommandEntry) []tui.CommandInfo {
	var out []tui.CommandInfo
	for _, e := range entries {
		var labels map[string]string
		var driver logdriver.LogDriver
		var dir string
		if e.ConfigJSON != nil {
			labels = e.ConfigJSON.Labels
			driver = e.ConfigJSON.LogDriver
			dir = e.ConfigJSON.Dir
		}
		project := labels[compose.LabelProject]
		workdir, hasWorkdir := labels[compose.LabelWorkdir]
		if !hasWorkdir {
			// Standalone (or partially-labelled) command: use its own working
			// directory so cwd-active grouping still works.
			workdir = dir
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
// under the default compose directory and a compose file discovered in the
// current working directory.
func (b *serviceBackend) ListProjects(ctx context.Context) ([]tui.ProjectInfo, error) {
	summaries, err := b.compose.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	named, _ := compose.ListNamedProjects()
	infos := mergeProjectInfos(summaries, named)
	infos = appendCwdProject(infos)
	// Enrich with the mux badge by parsing each project's compose file.
	for i := range infos {
		infos[i].HasMux = projectHasMux(infos[i].Name, infos[i].Path)
	}
	return infos, nil
}

// appendCwdProject ensures a compose project discoverable in the current
// working directory shows up in the Compose tab even when it has never been
// run and is not a named project under the compose config dir — so it can be
// opened and its mux cycled straight from the directory it lives in. When the
// project is already listed (by name) but lacks a compose-file path, the
// discovered path and workdir are filled in so the mux badge, modified time,
// and cwd-active marker resolve.
func appendCwdProject(infos []tui.ProjectInfo) []tui.ProjectInfo {
	sel, err := compose.LoadOrProject(compose.NormalizeOpts{})
	if err != nil || sel.Spec == nil {
		return infos
	}
	path := sel.Spec.ComposeFile
	workdir := normalizePath(sel.WorkDir)
	for i := range infos {
		if infos[i].Name != sel.Project {
			continue
		}
		if infos[i].Path == "" {
			infos[i].Path = path
		}
		if infos[i].Workdir == "" {
			infos[i].Workdir = workdir
		}
		return infos
	}
	return append(infos, tui.ProjectInfo{
		Name:     sel.Project,
		Path:     path,
		Workdir:  workdir,
		Modified: modifiedLabel(path),
	})
}

// projectHasMux reports whether a compose project declares a mux: section. It
// loads the project's compose file; failures and never-loadable projects
// report false (no badge).
func projectHasMux(name, composeFile string) bool {
	opts := compose.NormalizeOpts{File: composeFile}
	if composeFile == "" {
		opts.File = name
	}
	sel, err := compose.LoadOrProject(opts)
	if err != nil || sel.Spec == nil {
		return false
	}
	return sel.Spec.Mux != nil
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

// CycleMux cycles the mux layout for a compose project via the existing compose
// mux path (compose.LoadOrProject + mux.Build + mux.Run). mux owns its layout
// state through a persisted tmux window marker; the TUI keeps none.
func (b *serviceBackend) CycleMux(ctx context.Context, projectName, composeFile string) error {
	opts := compose.NormalizeOpts{File: composeFile}
	if composeFile == "" {
		opts.File = projectName
	}
	selection, err := compose.LoadOrProject(opts)
	if err != nil {
		return err
	}
	if selection.Spec == nil {
		return fmt.Errorf("mux: no compose file found for project %q", projectName)
	}
	if selection.Spec.Mux == nil {
		return fmt.Errorf("mux: project %q has no mux section", projectName)
	}
	spec := *selection.Spec.Mux

	cfg := b.svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("mux: locate cmdman binary: %w", err)
	}
	resolver, replicas, err := b.compose.MuxLeafResolver(ctx, selection)
	if err != nil {
		return err
	}
	built, err := mux.Build(ctx, spec, resolver, replicas, mux.PaneArgvOpts{
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
	})
	if err != nil {
		return err
	}
	windowName := "cmdman"
	if selection.Project != "" {
		windowName = "cmdman-" + selection.Project
	}
	// Discard mux's stdout hint so it never bleeds into the TUI surface; the
	// TUI runs inside tmux so mux prints nothing anyway.
	return mux.Run(ctx, built, mux.RunOptions{
		WindowName: windowName,
		// Pass the compose project identity so TUI-built dashboards are stamped
		// identically to CLI-built ones: `mux down` can find them regardless of
		// whether they were opened from the TUI or the command line.
		Identity: selection.ProjectIdentity(),
		Stdout:   io.Discard,
	})
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
