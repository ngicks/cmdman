package tui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// serviceBackend is the production Backend, adapting *cmdman.Service and
// *compose.Service to the data the TUI model renders.
type serviceBackend struct {
	svc     *cmdman.Service
	compose *compose.Service
	cwd     string
}

// NewServiceBackend builds a Backend over the given cmdman service.
func NewServiceBackend(svc *cmdman.Service) Backend {
	return &serviceBackend{
		svc:     svc,
		compose: compose.NewService(svc),
		cwd:     currentDir(),
	}
}

func (b *serviceBackend) Cwd() string { return b.cwd }

// ListCommands lists compose-scoped commands by requiring the compose project
// and workdir labels; standalone commands (without those labels) are dropped.
func (b *serviceBackend) ListCommands(ctx context.Context) ([]CommandInfo, error) {
	entries, err := b.svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return nil, err
	}
	var out []CommandInfo
	for _, e := range entries {
		labels := map[string]string{}
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
		out = append(out, CommandInfo{
			ID:        e.ID,
			Name:      name,
			Project:   project,
			Workdir:   normalizePath(workdir),
			State:     e.State,
			ExitCode:  e.ExitCode,
			LogDriver: driver,
		})
	}
	return out, nil
}

// ListProjects merges store-known project counts with never-run projects found
// under the default compose directory. The mux badge is populated by the mux
// layer; here HasMux is left false.
func (b *serviceBackend) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	summaries, err := b.compose.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []ProjectInfo
	for _, s := range summaries {
		seen[s.Project] = true
		out = append(out, ProjectInfo{
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
	// Merge never-run / default-location projects by name.
	named, _ := compose.ListNamedProjects()
	for _, n := range named {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, ProjectInfo{Name: n})
	}
	return out, nil
}

func (b *serviceBackend) Start(ctx context.Context, id string) error {
	return b.svc.Start(ctx, id)
}

func (b *serviceBackend) Stop(ctx context.Context, id string) error {
	res, err := b.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
	if err != nil {
		return err
	}
	return firstResultErr(res)
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

func firstResultErr(res []cmdman.StopResult) error {
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

// normalizePath returns an absolute, symlink-resolved, trailing-slash-free path
// so that workdir labels and os.Getwd() compare equal even through symlinks.
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
// compose file's mtime. It returns "" when the file is unavailable.
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
