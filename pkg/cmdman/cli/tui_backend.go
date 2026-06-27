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
			Tty:       tty,
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
		data, err := r.session.Recv()
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
		select {
		case r.ch <- tui.RawChunk{Bytes: data}:
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
	selection, err := resolveMuxSelection(projectName, composeFile)
	if err != nil {
		return err
	}
	return b.muxRun(ctx, selection, "")
}

// muxRun rebuilds the project's mux dashboard and applies a layout. An empty
// layout cycles to the next layout; a non-empty one applies that named/indexed
// layout (and starts a dashboard at it when none is running). Shared by CycleMux
// and ApplyLayout.
func (b *serviceBackend) muxRun(
	ctx context.Context, selection compose.ProjectSelection, layout string,
) error {
	spec := *selection.Spec.Mux

	scalePositions, err := mux.ReadScaleState(ctx, mux.ScaleStateOptions{
		Driver:    spec.Driver,
		DriverOpt: spec.DriverOpt,
		Identity:  selection.ProjectIdentity(),
	})
	if err != nil {
		return fmt.Errorf("mux: read scale state: %w", err)
	}

	cfg := b.svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("mux: locate cmdman binary: %w", err)
	}
	resolver, replicas, err := b.compose.MuxLeafResolver(ctx, selection)
	if err != nil {
		return err
	}
	built, err := mux.Build(ctx, mux.BuildOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: replicas,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
		ScalePositions: scalePositions,
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
		Layout:   layout,
		Stdout:   io.Discard,
	})
}

// resolveMuxSelection loads the compose project for a mux operation and verifies
// it declares a mux: section. composeFile is used directly when set; otherwise it
// is resolved on demand from the project name.
func resolveMuxSelection(projectName, composeFile string) (compose.ProjectSelection, error) {
	opts := compose.NormalizeOpts{File: composeFile}
	if composeFile == "" {
		opts.File = projectName
	}
	selection, err := compose.LoadOrProject(opts)
	if err != nil {
		return compose.ProjectSelection{}, err
	}
	if selection.Spec == nil {
		return compose.ProjectSelection{}, fmt.Errorf(
			"mux: no compose file found for project %q", projectName,
		)
	}
	if selection.Spec.Mux == nil {
		return compose.ProjectSelection{}, fmt.Errorf(
			"mux: project %q has no mux section", projectName,
		)
	}
	return selection, nil
}

// resolveLayoutSelection resolves the "current" mux project for the Layout tab
// (D5): the cwd-active mux project, falling back to the Compose-tab selection
// identified by projectName/composeFile. The resolved project must declare a
// mux: section.
func resolveLayoutSelection(projectName, composeFile string) (compose.ProjectSelection, error) {
	// Prefer the cwd-active mux project. SelectMuxProject errors when no (or an
	// ambiguous set of) mux compose is associated with the cwd; in that case fall
	// back to the explicit Compose-tab selection.
	if sel, err := compose.SelectMuxProject(compose.NormalizeOpts{}); err == nil {
		return sel, nil
	}
	return resolveMuxSelection(projectName, composeFile)
}

// ListLayouts returns the current project's mux layouts in definition order plus
// the running dashboard's current layout marker. The project is resolved per D5
// (cwd-active mux project, falling back to the Compose-tab selection).
func (b *serviceBackend) ListLayouts(
	ctx context.Context, projectName, composeFile string,
) (tui.LayoutsInfo, error) {
	selection, err := resolveLayoutSelection(projectName, composeFile)
	if err != nil {
		return tui.LayoutsInfo{}, err
	}
	spec := *selection.Spec.Mux

	names := make([]string, len(spec.Layouts))
	for i, l := range spec.Layouts {
		names[i] = l.Name
	}
	info := tui.LayoutsInfo{
		Project: selection.Project,
		Path:    selection.Spec.ComposeFile,
		Names:   names,
		Current: -1,
	}

	// Read the running dashboard's current layout marker, best-effort: a missing
	// tmux server or no dashboard yields no rows (Current stays -1). A genuine
	// listing failure is not fatal here — the layouts list itself is still valid,
	// and -1 already encodes "current layout unknown".
	windows, listErr := mux.List(ctx, mux.ListOptions{
		Driver:    spec.Driver,
		DriverOpt: spec.DriverOpt,
		Identity:  selection.ProjectIdentity(),
	})
	if listErr == nil && len(windows) > 0 {
		info.Current = windows[0].Marker
	}
	return info, nil
}

// ApplyLayout applies the named layout to the project's running dashboard,
// starting one at that layout when none is running (D6). It reuses CycleMux's
// build path with an explicit layout selector.
func (b *serviceBackend) ApplyLayout(
	ctx context.Context, projectName, composeFile, layoutName string,
) error {
	selection, err := resolveMuxSelection(projectName, composeFile)
	if err != nil {
		return err
	}
	return b.muxRun(ctx, selection, layoutName)
}

// ProjectDefinition returns the raw compose YAML file text for the project. It
// returns the file exactly as written on disk (not the normalized spec), so the
// definition viewer matches what the `e` editor opens.
func (b *serviceBackend) ProjectDefinition(
	_ context.Context, projectName, composeFile string,
) (string, error) {
	path, err := resolveComposePath(projectName, composeFile)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read compose file %s: %w", path, err)
	}
	return string(data), nil
}

// ComposeFilePath resolves the compose file path for the project so the TUI can
// hand it to the editor.
func (b *serviceBackend) ComposeFilePath(
	_ context.Context, projectName, composeFile string,
) (string, error) {
	return resolveComposePath(projectName, composeFile)
}

// ComposeUp runs "compose up" for a project, forwarding compose progress events
// to the TUI through a stream. The reporter is installed on a per-operation
// compose.Service so events flow only for this run; Up runs in a goroutine and
// the stream's channel closes when it returns, signaling the terminal phase.
func (b *serviceBackend) ComposeUp(
	ctx context.Context, projectName, composeFile string,
) (tui.ComposeUpStream, error) {
	opts := compose.NormalizeOpts{File: composeFile}
	if composeFile == "" {
		opts.File = projectName
	}
	spec, err := compose.LoadAndNormalize(opts)
	if err != nil {
		return nil, fmt.Errorf("compose up %q: %w", projectName, err)
	}
	stream := newComposeUpStream(ctx)
	svc := compose.NewService(b.svc, compose.WithReporter(stream))
	go func() {
		_, upErr := svc.Up(ctx, spec, compose.UpOption{})
		stream.finish(upErr)
	}()
	return stream, nil
}

// composeUpStream adapts a compose progress Reporter to the tui.ComposeUpStream
// contract: Report (called concurrently by the reconcile walk) projects each
// event onto a channel the TUI drains; finish records the operation-level error
// and closes the channel. Up joins its goroutines before returning, so no Report
// call races finish's close.
type composeUpStream struct {
	// ctxDone is the operation context's cancellation signal (ctx.Done()), kept
	// so a blocked Report unblocks if the consumer abandons the stream while Up
	// is still running. We keep the channel, not the context itself, per the
	// "no context in a struct" convention.
	ctxDone   <-chan struct{}
	ch        chan tui.ComposeUpEvent
	done      chan struct{}
	closeOnce sync.Once

	mu    sync.Mutex
	upErr error
}

func newComposeUpStream(ctx context.Context) *composeUpStream {
	return &composeUpStream{
		ctxDone: ctx.Done(),
		ch:      make(chan tui.ComposeUpEvent, 64),
		done:    make(chan struct{}),
	}
}

// Report implements compose.Reporter.
func (s *composeUpStream) Report(ev compose.Event) {
	out := tui.ComposeUpEvent{
		Command:  ev.Command,
		Phase:    string(ev.Phase),
		Terminal: ev.Phase.Terminal(),
		Failed:   ev.Phase.Failed(),
		ExitCode: ev.ExitCode,
		Err:      ev.Err,
	}
	select {
	case s.ch <- out:
	case <-s.done:
	case <-s.ctxDone:
	}
}

// finish records the operation-level error and closes the event channel.
func (s *composeUpStream) finish(err error) {
	s.mu.Lock()
	s.upErr = err
	s.mu.Unlock()
	close(s.ch)
}

func (s *composeUpStream) Events() <-chan tui.ComposeUpEvent { return s.ch }

func (s *composeUpStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upErr
}

func (s *composeUpStream) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}

// resolveComposePath returns the compose file path for a project. composeFile is
// used directly when set; otherwise it is resolved on demand via
// compose.LoadOrProject, so never-run named projects (which carry an empty path)
// still resolve to their compose file under the default compose dir.
func resolveComposePath(projectName, composeFile string) (string, error) {
	if composeFile != "" {
		return composeFile, nil
	}
	sel, err := compose.LoadOrProject(compose.NormalizeOpts{File: projectName})
	if err != nil {
		return "", err
	}
	if sel.Spec == nil {
		return "", fmt.Errorf("no compose file found for project %q", projectName)
	}
	return sel.Spec.ComposeFile, nil
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
