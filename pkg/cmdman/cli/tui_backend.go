package cli

import (
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
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
	// workDir is the raw --workdir override ("" when not given). It overrides the
	// effective work directory used for cwd-active compose project discovery.
	workDir string
}

// newServiceBackend builds a tui.Backend over the given cmdman service. workDir
// is the --workdir override: when set it replaces the process CWD as the
// effective work directory for cwd-active grouping and project discovery.
func newServiceBackend(svc *cmdman.Service, workDir string) tui.Backend {
	cwd := currentDir()
	if workDir != "" {
		cwd = normalizePath(workDir)
	}
	return &serviceBackend{
		svc:     svc,
		compose: compose.NewService(svc),
		cwd:     cwd,
		workDir: workDir,
	}
}

func (b *serviceBackend) Cwd() string { return b.cwd }

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
