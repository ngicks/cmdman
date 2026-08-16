package cli

import (
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/tui"
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
	// muxToken is the opaque --mux-token the caller was summoned with ("" when
	// unset). It is the highest-priority active-project probe (D10); cmdman
	// never parses it, the driver resolves it.
	muxToken string
	// file and projectName are the --file/--project-name the invocation named,
	// "" when it named none. Either one set is an explicit compose target, which
	// outranks every detection probe (D17): what the caller asked for is not a
	// guess to be improved on.
	file        string
	projectName string
}

// backendTarget is what a `cmdman tui …` invocation says about the project its
// TUI works on: the work directory to discover one in, the mux token to resolve
// one from, and the compose file/name pair that names one outright.
type backendTarget struct {
	// WorkDir is the --workdir override: when set it replaces the process CWD as
	// the effective work directory for cwd-active grouping and project
	// discovery.
	WorkDir string
	// MuxToken is the --mux-token the invocation carried.
	MuxToken string
	// File and ProjectName are the --file/--project-name pair (compose's -f/-p).
	File        string
	ProjectName string
}

// newServiceBackend builds a tui.Backend over the given cmdman service, working
// on the project the invocation named.
func newServiceBackend(svc *cmdman.Service, target backendTarget) tui.Backend {
	cwd := currentDir()
	if target.WorkDir != "" {
		cwd = normalizePath(target.WorkDir)
	}
	return &serviceBackend{
		svc: svc,
		// The frame seam travels with the compose service because the TUI's
		// layout verbs go through MuxUp, whose default_frame auto-show may hold a
		// managed entry.
		compose:     compose.NewService(svc, compose.WithFrameSvc(NewFrameSvc(svc))),
		cwd:         cwd,
		workDir:     target.WorkDir,
		muxToken:    target.MuxToken,
		file:        target.File,
		projectName: target.ProjectName,
	}
}

func (b *serviceBackend) Cwd() string { return b.cwd }

// targetWorkDir is the work directory a project-targeting action loads against:
// the one the caller named, falling back to the invocation's own --workdir
// override when it named none. A project is (work directory, name), so the two
// travel together (D20) — the caller that names one is the project-manager
// widget, whose project was resolved from a mux token or a summon and stands
// wherever it stands, not where the panel does. The full TUI's layout verbs name
// none: their project came from the directory the TUI itself works on, which is
// what the fallback is.
func (b *serviceBackend) targetWorkDir(workDir string) string {
	if workDir != "" {
		return workDir
	}
	return b.workDir
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
