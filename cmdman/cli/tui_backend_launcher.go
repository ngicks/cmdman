package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman"
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
// A named def that declares no work_dir: is grouped nowhere, because it belongs
// nowhere: it is offered at every location instead, so the directory it comes up
// in is the one the user selects rather than the one the launcher was summoned
// from (see (*launchAccumulator).offer).
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
		acc.add(h.WorkDir, h.Project, h.File, h.LastUsed, true, false)
	}

	summaries, err := b.compose.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("launcher: list compose projects: %w", err)
	}
	for _, s := range summaries {
		acc.add(s.WorkDir, s.Project, s.ComposeFile, time.Time{}, false, false)
	}

	resolver := &composeResolver{}
	// Never-run named defs and the project sitting in the working directory: the
	// same two sources the Compose tab merges in, resolved here so their location
	// is the canonical work directory rather than an empty one. Only the defs
	// that name a directory have one; the rest are offered at all of them.
	pinned, floating := namedConfigProjects(resolver)
	for _, p := range pinned {
		acc.add(p.dir, p.proj.name, p.proj.file, time.Time{}, false, true)
	}
	if sel, err := compose.LoadOrProject(
		compose.NormalizeOpts{WorkDir: b.workDir},
	); err == nil && sel.Spec != nil {
		acc.add(sel.WorkDir, sel.Project, sel.Spec.ComposeFile, time.Time{}, false, false)
	}
	acc.offer(floating)

	locs := acc.locations(resolver, b.runningIdentities(ctx))
	fillGitInfo(ctx, locs)
	return locs, nil
}

// ResolveLaunchDir builds the listing row for one directory on its own — the
// launcher turning a path the user typed into a location it can select (D28).
//
// A directory with nothing to run is still a location, so "no compose file, no
// history" is an empty Projects rather than an error; what does come back as an
// error is a failure that would hide what is there, which is the history query.
func (b *serviceBackend) ResolveLaunchDir(
	ctx context.Context,
	dir string,
) (tui.LaunchLocation, error) {
	history, err := b.svc.ListComposeHistory(ctx)
	if err != nil {
		return tui.LaunchLocation{}, fmt.Errorf("launcher: list compose history: %w", err)
	}
	resolver := &composeResolver{}
	_, floating := namedConfigProjects(resolver)
	return resolveLaunchDir(ctx, dir, history, floating, resolver, b.runningIdentities(ctx))
}

// resolveLaunchDir is the listing's per-directory build fed only what belongs to
// dir: the same accumulator, the same projection and the same git fill
// ListLaunchTargets runs, so a resolved row and a listed row are the same shape.
// It takes the history and the offered projects as data rather than reading them
// so the merge can be exercised without a live service.
func resolveLaunchDir(
	ctx context.Context,
	dir string,
	history []cmdman.ComposeHistoryEntry,
	floating []launchProj,
	resolver *composeResolver,
	running map[string]bool,
) (tui.LaunchLocation, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return tui.LaunchLocation{}, fmt.Errorf("launcher: resolve %s: %w", dir, err)
	}
	dir = abs

	acc := &launchAccumulator{}
	// dir is a location before anything is found at it: a directory nothing was
	// ever run in is exactly where an unpinned config project is worth offering,
	// and offering needs a location to offer at.
	acc.at(dir)
	for _, h := range history {
		if h.WorkDir == dir {
			acc.add(dir, h.Project, h.File, h.LastUsed, true, false)
		}
	}
	// The discovered project is recorded under dir rather than under the work
	// directory its spec resolved to: this row is the answer about dir, and a
	// spec declaring work_dir: elsewhere would otherwise open a second location
	// the caller never asked about.
	if spec, err := discoverLaunchDirSpec(ctx, dir); err == nil {
		acc.add(dir, spec.Project, spec.ComposeFile, time.Time{}, false, false)
	}
	acc.offer(floating)

	locs := acc.locations(resolver, running)
	fillGitInfo(ctx, locs)
	return locs[0], nil
}

// pinnedConfigProject is a named project from the compose config dir whose spec
// declares work_dir:, paired with the directory it declares.
type pinnedConfigProject struct {
	dir  string
	proj launchProj
}

// namedConfigProjects reads the compose config dir into the two kinds of row a
// named project makes. One whose spec declares work_dir: belongs to that
// directory and is listed there. One that declares none belongs to no directory
// in particular — it comes up wherever it is started from — so it is returned to
// be offered at every location instead of pinned to the directory the launcher
// process happens to stand in, which for a popup is wherever the user summoned
// it from.
//
// A project that fails to resolve is skipped, so one broken file in the config
// dir does not hide the rest of it.
func namedConfigProjects(
	resolver *composeResolver,
) (pinned []pinnedConfigProject, floating []launchProj) {
	names, _ := compose.ListNamedProjects()
	for _, n := range names {
		sel, err := resolver.resolve(n)
		if err != nil || sel.Spec == nil {
			continue
		}
		proj := launchProj{name: sel.Project, file: sel.Spec.ComposeFile, fromConfig: true}
		if sel.Spec.WorkDirDeclared {
			pinned = append(pinned, pinnedConfigProject{dir: sel.WorkDir, proj: proj})
			continue
		}
		floating = append(floating, proj)
	}
	// By project name rather than by file name: the name is what the list shows
	// and what a location's rows are deduplicated on, so two files declaring one
	// name land in a fixed order rather than in readdir order.
	slices.SortStableFunc(floating, func(x, y launchProj) int {
		return cmp.Or(cmp.Compare(x.name, y.name), cmp.Compare(x.file, y.file))
	})
	return pinned, floating
}

// discoverLaunchDirSpec loads the compose file sitting in dir. Discovery is
// pointed at dir because [compose.LoadOrProject] searches the process working
// directory, which for a launcher popup is wherever the user summoned it from.
//
// The work directory is forced to dir for the same reason a launch forces it
// (see loadLaunchSpec): a spec that declares none falls back to the process
// working directory, and a row that claimed to be somewhere else than where its
// launch puts the project would be lying about both.
func discoverLaunchDirSpec(ctx context.Context, dir string) (compose.ComposeSpec, error) {
	file, raw, err := compose.DiscoverFile(dir, compose.NormalizeOpts{})
	if err != nil {
		return compose.ComposeSpec{}, err
	}
	return compose.Normalize(ctx, file, raw, compose.NormalizeOpts{WorkDir: dir})
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
	fromConfig  bool
	projects    []launchProj
	seen        map[string]int
}

type launchProj struct {
	name        string
	file        string
	fromHistory bool
	fromConfig  bool
}

// at returns the location dir accumulates into, recording it when it is new. A
// mention is enough to make one: a directory with nothing at it is still a place
// to start something.
func (a *launchAccumulator) at(dir string) *launchDir {
	if a.byDir == nil {
		a.byDir = map[string]*launchDir{}
	}
	d := a.byDir[dir]
	if d == nil {
		d = &launchDir{dir: dir, seen: map[string]int{}}
		a.byDir[dir] = d
		a.order = append(a.order, dir)
	}
	return d
}

// offer appends the projects that belong to no directory of their own to every
// location, which is how a config project with no work_dir: is listed: it comes
// up in whatever directory it is started from, so the launcher offers it at
// whichever location the user is looking at.
//
// It runs after every add. A location that already lists the project keeps its
// own row — the file it was recorded with, and the provenance that makes it open
// enabled — and the offered rows land after the rows that are really there.
// Nothing an offer adds flags the location: a location the config dir merely has
// something to offer at stays as visible as it was, since it is the project the
// user keeps around and not the place.
func (a *launchAccumulator) offer(projects []launchProj) {
	for _, dir := range a.order {
		d := a.byDir[dir]
		for _, p := range projects {
			if _, ok := d.seen[p.name]; ok {
				continue
			}
			d.seen[p.name] = len(d.projects)
			d.projects = append(d.projects, p)
		}
	}
}

// add records one project at a location. A project already recorded keeps the
// first file it was seen with — history is added first, so the file a project
// was actually brought up with wins over one merely discovered.
func (a *launchAccumulator) add(
	dir, project, file string,
	lastUsed time.Time,
	fromHistory, fromConfig bool,
) {
	if dir == "" {
		return
	}
	d := a.at(dir)
	if fromHistory {
		d.fromHistory = true
		if lastUsed.After(d.lastUsed) {
			d.lastUsed = lastUsed
		}
	}
	if fromConfig {
		d.fromConfig = true
	}
	if i, ok := d.seen[project]; ok {
		d.projects[i].fromHistory = d.projects[i].fromHistory || fromHistory
		d.projects[i].fromConfig = d.projects[i].fromConfig || fromConfig
		return
	}
	d.seen[project] = len(d.projects)
	d.projects = append(d.projects, launchProj{
		name: project, file: file, fromHistory: fromHistory, fromConfig: fromConfig,
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
			FromConfig:  d.fromConfig,
			Projects:    make([]tui.LaunchProject, 0, len(d.projects)),
		}
		for _, p := range d.projects {
			identity := compose.ProjectSelection{WorkDir: d.dir, Project: p.name}.ProjectIdentity()
			entry := tui.LaunchProject{
				Name:        p.name,
				File:        p.file,
				FromHistory: p.fromHistory,
				FromConfig:  p.fromConfig,
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
		// Neither launcher gesture says anything about layouts, so the dashboard
		// comes back on the layout the user left it on. Advancing it is what the
		// cycle key is for.
		KeepLayout: true,
		Stdout:     io.Discard,
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
	windows, err := mux.List(ctx, mux.ListOptions{
		Driver:   muxSpec.Driver,
		Identity: selection.ProjectIdentity(),
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
