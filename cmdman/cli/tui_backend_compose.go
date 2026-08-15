package cli

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/tui"
)

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
	infos = appendCwdProject(infos, b.workDir)
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
func appendCwdProject(infos []tui.ProjectInfo, workDir string) []tui.ProjectInfo {
	sel, err := compose.LoadOrProject(compose.NormalizeOpts{WorkDir: workDir})
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
		if infos[i].Identity == "" {
			infos[i].Identity = projectIdentity(sel.WorkDir, sel.Project)
		}
		return infos
	}
	return append(infos, tui.ProjectInfo{
		Name:     sel.Project,
		Path:     path,
		Workdir:  workdir,
		Identity: projectIdentity(sel.WorkDir, sel.Project),
		Modified: modifiedLabel(path),
	})
}

// projectIdentity is the multiplexer ownership stamp of a project's window, the
// string the switcher hands back in [tui.SwitchTarget] for
// [serviceBackend.SwitchToProject] to find that window by — or to stamp on the
// one it creates. workDir is the work directory as compose itself computes it —
// cleaned and absolute, but NOT symlink-resolved: the hash mux stamped is over
// that form, while tui.ProjectInfo.Workdir is resolved for cwd comparison and
// would hash into a window that does not exist. A project with no directory to
// hash gets no identity rather than one that would match some other project's
// window.
func projectIdentity(workDir, project string) string {
	if workDir == "" || project == "" {
		return ""
	}
	return compose.ProjectSelection{WorkDir: workDir, Project: project}.ProjectIdentity()
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
			Identity: projectIdentity(s.WorkDir, s.Project),
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
