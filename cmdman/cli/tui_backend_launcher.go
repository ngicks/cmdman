package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// launchGitProbes bounds how many git probes run at once. The launcher opens on
// a key binding, so the listing has to feel instant; a handful of processes is
// enough to hide the latency of a slow work tree without forking one per entry.
const launchGitProbes = 8

// ListLaunchTargets builds the launcher's list: every compose project known from
// history, from the store, from the named defs and from the working directory,
// grouped by the directory it runs in and ordered by recency (D7).
//
// The grouping key is the canonical work directory compose itself computes
// (filepath.Clean(filepath.Abs(p)), no symlink resolution) — the same string the
// history table stores and the same one mux stamps its windows with, so a
// project's running window is recognized and a symlinked directory is one
// location rather than two. It is deliberately not tui.ProjectInfo.Workdir,
// which is symlink-resolved for cwd comparison.
func (b *serviceBackend) ListLaunchTargets(ctx context.Context) ([]tui.LaunchLocation, error) {
	acc := &launchAccumulator{}

	history, err := b.svc.ListComposeHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("launcher: list compose history: %w", err)
	}
	for _, h := range history {
		acc.add(h.WorkDir, h.Project, h.File, h.LastUsed, true)
	}

	summaries, err := b.compose.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("launcher: list compose projects: %w", err)
	}
	for _, s := range summaries {
		acc.add(s.WorkDir, s.Project, s.ComposeFile, time.Time{}, false)
	}

	resolver := &composeResolver{}
	// Never-run named defs and the project sitting in the working directory: the
	// same two sources the Compose tab merges in, resolved here so their location
	// is the canonical work directory rather than an empty one.
	named, _ := compose.ListNamedProjects()
	for _, n := range named {
		if sel, err := resolver.resolve(n); err == nil && sel.Spec != nil {
			acc.add(sel.WorkDir, sel.Project, sel.Spec.ComposeFile, time.Time{}, false)
		}
	}
	if sel, err := compose.LoadOrProject(
		compose.NormalizeOpts{WorkDir: b.workDir},
	); err == nil && sel.Spec != nil {
		acc.add(sel.WorkDir, sel.Project, sel.Spec.ComposeFile, time.Time{}, false)
	}

	locs := acc.locations(resolver, b.runningIdentities(ctx))
	fillGitInfo(ctx, locs)
	return locs, nil
}

// runningIdentities is the set of mux ownership identities with a live window,
// which is how a location's projects learn they are already up. Listing is
// best-effort: no multiplexer server (the launcher may be summoned from a bare
// shell, D8) means nothing is running, not that the launcher cannot open.
func (b *serviceBackend) runningIdentities(ctx context.Context) map[string]bool {
	windows, err := mux.List(ctx, mux.ListOptions{})
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(windows))
	for _, w := range windows {
		out[w.Identity] = true
	}
	return out
}

// launchAccumulator groups project rows by their work directory, keeping first
// insertion order within a location so the history rows (added first, already
// recency-ordered) lead the list.
type launchAccumulator struct {
	byDir map[string]*launchDir
	order []string
}

type launchDir struct {
	dir         string
	lastUsed    time.Time
	fromHistory bool
	projects    []launchProj
	seen        map[string]int
}

type launchProj struct {
	name        string
	file        string
	fromHistory bool
}

// add records one project at a location. A project already recorded keeps the
// first file it was seen with — history is added first, so the file a project
// was actually brought up with wins over one merely discovered.
func (a *launchAccumulator) add(dir, project, file string, lastUsed time.Time, fromHistory bool) {
	if dir == "" {
		return
	}
	if a.byDir == nil {
		a.byDir = map[string]*launchDir{}
	}
	d := a.byDir[dir]
	if d == nil {
		d = &launchDir{dir: dir, seen: map[string]int{}}
		a.byDir[dir] = d
		a.order = append(a.order, dir)
	}
	if fromHistory {
		d.fromHistory = true
		if lastUsed.After(d.lastUsed) {
			d.lastUsed = lastUsed
		}
	}
	if i, ok := d.seen[project]; ok {
		d.projects[i].fromHistory = d.projects[i].fromHistory || fromHistory
		return
	}
	d.seen[project] = len(d.projects)
	d.projects = append(d.projects, launchProj{
		name: project, file: file, fromHistory: fromHistory,
	})
}

// locations projects the accumulated rows onto the launcher's listing, resolving
// each project's compose file so a stale entry says so where it stands (D10) and
// a mux-less one is marked before it is launched (D9).
func (a *launchAccumulator) locations(
	resolver *composeResolver,
	running map[string]bool,
) []tui.LaunchLocation {
	out := make([]tui.LaunchLocation, 0, len(a.order))
	for _, dir := range a.order {
		d := a.byDir[dir]
		loc := tui.LaunchLocation{
			Dir:         d.dir,
			LastUsed:    d.lastUsed,
			FromHistory: d.fromHistory,
			Projects:    make([]tui.LaunchProject, 0, len(d.projects)),
		}
		for _, p := range d.projects {
			identity := compose.ProjectSelection{WorkDir: d.dir, Project: p.name}.ProjectIdentity()
			entry := tui.LaunchProject{
				Name:        p.name,
				File:        p.file,
				FromHistory: p.fromHistory,
				Running:     identity != "" && running[identity],
			}
			entry.HasMux, entry.Problem, entry.Missing = resolver.classify(p.file, p.name)
			loc.Projects = append(loc.Projects, entry)
		}
		out = append(out, loc)
	}
	// Recency first, so the everyday case is at the top and stays there while
	// state changes (D7); ties fall back to the path for a stable order.
	slices.SortStableFunc(out, func(x, y tui.LaunchLocation) int {
		if !x.LastUsed.Equal(y.LastUsed) {
			return y.LastUsed.Compare(x.LastUsed)
		}
		return cmp.Compare(x.Dir, y.Dir)
	})
	return out
}

// composeResolver loads compose files once per launcher listing. The same file
// reaches it under several keys (a bare project name and the absolute path it
// resolved to), so a successful load is cached under both.
type composeResolver struct {
	cache map[string]resolvedCompose
}

type resolvedCompose struct {
	sel compose.ProjectSelection
	err error
}

func (r *composeResolver) resolve(key string) (compose.ProjectSelection, error) {
	if v, ok := r.cache[key]; ok {
		return v.sel, v.err
	}
	sel, err := compose.LoadOrProject(compose.NormalizeOpts{File: key})
	if r.cache == nil {
		r.cache = map[string]resolvedCompose{}
	}
	r.cache[key] = resolvedCompose{sel: sel, err: err}
	if err == nil && sel.Spec != nil {
		r.cache[sel.Spec.ComposeFile] = resolvedCompose{sel: sel, err: err}
	}
	return sel, err
}

// classify reports what the launcher needs to know about a project's compose
// file before anything is launched: whether it declares a mux: section, and why
// it cannot be brought up at all. A file that is simply gone is the removable
// case (ctrl+d); a file that fails to parse blocks the launch just as hard but
// is not a history row to forget.
func (r *composeResolver) classify(
	file, project string,
) (hasMux bool, problem string, missing bool) {
	key := file
	if key == "" {
		key = project
	}
	sel, err := r.resolve(key)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, "missing: " + key, true
	case err != nil:
		return false, err.Error(), false
	case sel.Spec == nil:
		return false, "missing: no compose file found for " + key, true
	}
	return sel.Spec.Mux != nil, "", false
}

// fillGitInfo reads each location's git identity concurrently (D41). Probes are
// independent and never fail the listing, so the group carries no error.
func fillGitInfo(ctx context.Context, locs []tui.LaunchLocation) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(launchGitProbes)
	for i := range locs {
		g.Go(func() error {
			info := probeGit(ctx, locs[i].Dir)
			locs[i].RepoName = info.RepoName
			locs[i].RepoURI = info.RepoURI
			locs[i].Branch = info.Branch
			return nil
		})
	}
	_ = g.Wait()
}

// StartProject brings a project up in the background — the launcher's `s` (D4).
// It runs the same two calls the Compose tab makes, compose up followed by the
// mux bring-up, and returns when they are done; the launcher shows the flight in
// the row's marker rather than in a progress view (D29).
//
// A project with no mux: section is brought up and left at that: the shell
// window D9 gives it belongs to the landing, and `s` is the gesture that stays
// where it is.
func (b *serviceBackend) StartProject(ctx context.Context, target tui.LaunchTarget) error {
	spec, err := loadLaunchSpec(target)
	if err != nil {
		return err
	}
	return b.bringUp(ctx, target, spec)
}

// LaunchProject brings a project up and lands focus inside it — the launcher's
// `S` (D4/D10). The landing is [compose.Service.MuxLand]: the project's window
// is focused, synthesized as a bare shell window first when the project has no
// mux: section (D9), and the terminal is handed over when the launcher was
// summoned from outside the multiplexer and there is no client to switch (D8).
//
// A launch failure that prevents landing at all comes back as an error and stays
// inline in the launcher. Raising the project's attention state for failures the
// user has already switched away from is the rest of D10, and waits for phase
// 2's badge surface.
func (b *serviceBackend) LaunchProject(
	ctx context.Context,
	target tui.LaunchTarget,
) (tui.LaunchOutcome, error) {
	spec, err := loadLaunchSpec(target)
	if err != nil {
		return tui.LaunchOutcome{}, err
	}
	if err := b.bringUp(ctx, target, spec); err != nil {
		return tui.LaunchOutcome{}, err
	}

	res, err := b.compose.MuxLand(ctx, compose.MuxLandOption{
		Selection: compose.SelectionFromSpec(&spec),
	})
	if err != nil {
		return tui.LaunchOutcome{}, fmt.Errorf(
			"launcher: land in %s: %w", launchLabel(target), err)
	}

	outcome := tui.LaunchOutcome{AttachCommand: res.AttachCommand}
	if spec.Mux == nil {
		// D9: the landing promise holds for a project with no dashboard to land
		// in, and the warning is what explains the bare window.
		outcome.Warning = fmt.Sprintf(
			"%s is up, but its compose file declares no mux: section — "+
				"opened a shell window at %s",
			launchLabel(target), spec.WorkDir)
	}
	return outcome, nil
}

// bringUp is the bring-up both launcher actions share: compose up, then the mux
// dashboard for a project that declares one.
//
// The dashboard is (re)built only when the up actually changed a command — a
// created or recreated command has a new id, so panes bound to the old one are
// stale — or when there is no dashboard yet. Re-applying a layout for its own
// sake is visible churn: it tears the window's panes down and respawns their
// viewers, which is not what an idempotent "take me there" (D4) should cost.
func (b *serviceBackend) bringUp(
	ctx context.Context,
	target tui.LaunchTarget,
	spec compose.ComposeSpec,
) error {
	res, upErr := b.compose.Up(ctx, spec, compose.UpOption{})
	if err := upVerdict(target, res, upErr); err != nil {
		return err
	}
	if spec.Mux == nil {
		return nil
	}
	selection := compose.SelectionFromSpec(&spec)
	if !upChangedCommands(res) && b.hasDashboard(ctx, spec.Mux, selection) {
		return nil
	}
	// The mux tail rides the spec that was just normalized rather than resolving
	// the project by name again: a by-name resolution carries no work directory,
	// so it would fall back to the launcher's own — and a popup runs in whatever
	// directory the user summoned it from, which is exactly the case D3 exists
	// for. Same identity as the up, no second load.
	if err := b.compose.MuxUp(ctx, compose.MuxUpOption{
		Selection: selection,
		// The launcher is a popup over somebody else's window: building the
		// dashboard there would repurpose the window the user was looking at,
		// which is the opposite of both launcher gestures (D4).
		KeepCurrentWindow: true,
		Stdout:            io.Discard,
	}); err != nil {
		return fmt.Errorf("launcher: mux %s: %w", launchLabel(target), err)
	}
	return nil
}

// upVerdict is a bring-up's whole verdict: the call's own error, and the
// per-command failures compose reports inside the result rather than as an
// error of its own (D21's aggregation — the remaining commands carry on). Both
// halves have to be read, or `S` reports success and lands the user on a
// dashboard of dead panes; the CLI's own up path reads them the same way.
func upVerdict(target tui.LaunchTarget, res *compose.UpResult, err error) error {
	if err == nil && res != nil {
		err = UpResultErr(res)
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("launcher: up %s: %w", launchLabel(target), err)
}

// upChangedCommands reports whether a bring-up created or recreated anything.
// Only those two invalidate a running dashboard: they give the command a new id,
// which the panes viewing the old one no longer address.
func upChangedCommands(res *compose.UpResult) bool {
	if res == nil {
		return true
	}
	for _, a := range res.Actions {
		switch compose.ActionKind(a.Action) {
		case compose.ActionCreate, compose.ActionRecreate:
			return true
		}
	}
	return false
}

// hasDashboard reports whether the project already has a built dashboard: a
// window carrying its identity that has had a layout applied. The layout marker
// is what tells a dashboard apart from the bare shell window a landing
// synthesizes (D9) — otherwise a project that grew a mux: section after being
// landed on would never get its dashboard built.
func (b *serviceBackend) hasDashboard(
	ctx context.Context,
	muxSpec *mux.Spec,
	selection compose.ProjectSelection,
) bool {
	// An unnamed project has no identity of its own; the window name is what mux
	// stamps for it, exactly as compose's own mux operations resolve it.
	identity := selection.ProjectIdentity()
	if identity == "" {
		identity = selection.MuxWindowName()
	}
	windows, err := mux.List(ctx, mux.ListOptions{
		Driver:   muxSpec.Driver,
		Identity: identity,
	})
	if err != nil {
		return false
	}
	for _, w := range windows {
		if w.Marker >= 0 {
			return true
		}
	}
	return false
}

// ForgetLaunchTarget removes a stale project from the launch history (D10/Q12).
// Forgetting a project that was never recorded is not an error: the same removal
// is offered for a discovered project whose compose file has gone, and there the
// list row is all there is to drop.
func (b *serviceBackend) ForgetLaunchTarget(
	ctx context.Context,
	target tui.LaunchTarget,
) error {
	if _, err := b.svc.DeleteComposeHistory(ctx, target.WorkDir, target.Project); err != nil {
		return fmt.Errorf("launcher: forget %s: %w", launchLabel(target), err)
	}
	return nil
}

// loadLaunchSpec loads the compose spec a launcher action operates on. The
// target's work directory is passed back in so a project recorded under an
// explicit --workdir comes up in the same place — and therefore under the same
// mux identity — as it did when it was recorded.
func loadLaunchSpec(target tui.LaunchTarget) (compose.ComposeSpec, error) {
	opts := compose.NormalizeOpts{File: target.File, WorkDir: target.WorkDir}
	if opts.File == "" {
		opts.File = target.Project
	}
	spec, err := compose.LoadAndNormalize(opts)
	if err != nil {
		return compose.ComposeSpec{}, fmt.Errorf("launcher: load %s: %w", launchLabel(target), err)
	}
	return spec, nil
}

// launchLabel names a target in an error the user reads in the launcher.
func launchLabel(target tui.LaunchTarget) string {
	if target.Project == "" {
		return target.WorkDir
	}
	return target.Project
}
