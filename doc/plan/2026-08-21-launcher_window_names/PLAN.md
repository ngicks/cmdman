# Launcher work-directory window names

Give newly created launcher windows a compact work-directory/Git-derived title,
while preserving project identity and every non-launcher naming default.

## Goal / success criteria

- A launcher-created Git-worktree window is named from repository and branch,
  with no `cmdman-` prefix.
- A launcher-created non-Git window is named from its work-directory basename.
- The final title is ellipsized to the confirmed 10-cell policy.
- Dashboard and mux-less launcher paths compute the same title.
- Existing window ownership, lookup, and non-launcher names are unchanged.
- The existing Git probe is moved from `cmdman/cli` into a reusable root
  `internal` package and remains covered by tests.

## Scope

- Extract `cmdman/cli/gitinfo.go` into `internal/gitinfo` and update the
  launcher's location listing to consume it.
- Add a launcher window-name builder in `cmdman/cli` using the selected work
  directory, reusable Git metadata, and cell-aware truncation.
- Add an optional window-name override to `compose.MuxUpOption` and
  `compose.MuxLandOption`; the launcher supplies it, while other callers retain
  `ProjectSelection.MuxWindowName()` as the default.
- Test naming, fallback, truncation, and propagation through both launcher
  creation paths.

## Non-goals

- Changing `ProjectSelection.ProjectIdentity`, window ownership stamps, lookup,
  teardown, or duplicate-title behavior.
- Renaming an existing owned window when the launcher lands in it.
- Changing direct compose, switcher, or standalone mux naming defaults.
- Adding CLI flags, configuration keys, or persisted state.

## Context

- `cmdman/compose/selection.go:44-55` currently derives every compose mux title
  as `cmdman-<project>` or `cmdman`.
- `cmdman/compose/mux.go:100-109,143-149` passes that title to both `mux.Run` and
  `mux.Land`.
- `cmdman/cli/tui_backend_launcher.go:452-525` is the launcher-only seam that
  calls both operations and knows the selected work directory.
- `cmdman/cli/gitinfo.go` already probes repository name, origin URI, and branch
  with a two-second timeout and supports worktrees/submodules via `git -C`.
- `internal/templateutil.Trunc` already truncates to terminal cells with a
  Unicode ellipsis.

## Approach

Keep the generic compose default intact and make launcher naming explicit.
Compute the title once per launcher action from `spec.WorkDir`, pass it through
the optional `WindowName` fields on both `MuxUpOption` and `MuxLandOption`, and
have those services fall back to `selection.MuxWindowName()` when the override
is empty.

Move the existing Git probe rather than duplicating it. The new
`internal/gitinfo` package owns the bounded command execution, metadata type,
and remote-URI parsing; `cmdman/cli` remains responsible for turning that data
into a launcher-specific presentation label.

Rejected alternatives:

- Change `ProjectSelection.MuxWindowName()` globally: this would silently rename
  direct compose and switcher-created windows despite the launcher-only request.
- Use a title as window identity: titles are mutable and collisions become more
  likely after truncation; the existing opaque identity already solves lookup.
- Reimplement Git discovery for naming: the launcher already has a tested probe
  with the required worktree behavior.

## Public surface delta

The internal helper becomes reusable inside this module, while the compose
service gets an optional display-name override. Empty overrides preserve every
existing caller's behavior.

```go
// internal/gitinfo
type Info struct {
    RepoName string
    RepoURI  string
    Branch   string
}

func Probe(ctx context.Context, dir string) Info
func RepoNameFromURI(uri string) string

// cmdman/compose
type MuxUpOption struct {
    // existing fields...
    // WindowName overrides the display name of a newly created window.
    // Empty preserves ProjectSelection.MuxWindowName().
    WindowName string
}

type MuxLandOption struct {
    // existing fields...
    // WindowName overrides the display name of a newly created window.
    // Empty preserves ProjectSelection.MuxWindowName().
    WindowName string
}
```

No CLI, config-file, RPC, or persistent-state delta.

## Implementation steps

1. **Extract the reusable Git probe without changing launcher listing behavior.**
   Move the implementation in `cmdman/cli/gitinfo.go` to a new
   `internal/gitinfo/gitinfo.go`. Export `Info`, `Probe`, and
   `RepoNameFromURI`; keep `gitOutput`, the two-second timeout, silent failure,
   origin preference, and toplevel fallback internal. Move the URI tests from
   `cmdman/cli/tui_backend_launcher_test.go` and add probe tests in
   `internal/gitinfo/gitinfo_test.go`. Update `fillGitInfo` in
   `cmdman/cli/tui_backend_launcher.go` to use `gitinfo.Probe`, proving the
   extraction is behavior-neutral before adding window naming. This delivers
   D2 and preserves UC1/UC2 Git detection semantics.

2. **Define and test the launcher title transformation.** Add unexported
   `launcherWindowName(ctx context.Context, workDir string) string` beside the
   launcher backend. It calls `gitinfo.Probe`; when both `RepoName` and a useful
   branch (`Branch != "" && Branch != "HEAD"`) exist, form
   `RepoName + "-" + Branch`; otherwise use `filepath.Base(filepath.Clean(workDir))`.
   Apply `templateutil.Trunc(name, 10)` once to the complete name. Return `""`
   only when no usable work-directory label exists, allowing the compose
   default to remain the last-resort fallback. Add table tests covering a Git
   worktree, no origin, non-Git/probe failure, detached HEAD (D3), slash-bearing
   branches, exactly 10 cells, truncation to 10 cells including `…` (D4), and
   wide Unicode. This delivers UC1–UC3.

3. **Add a default-preserving compose window-name override.** Add
   `WindowName string` to `compose.MuxUpOption` and `compose.MuxLandOption` in
   `cmdman/compose/mux.go`. In each method, select `opts.WindowName` when
   non-empty and otherwise call `selection.MuxWindowName()`, then pass the
   resolved value to `mux.RunOptions.WindowName` / `mux.LandOptions.WindowName`.
   Add focused tests in `cmdman/compose/mux_internal_test.go` (or the closest
   existing service seam) for explicit override and empty-value fallback.
   Keep `ProjectSelection.MuxWindowName()` and its current tests unchanged.
   This delivers D1/UC4 and makes the launcher-only boundary enforceable.

4. **Thread one computed title through each launcher action.** In
   `cmdman/cli/tui_backend_launcher.go`, compute the title from the normalized
   `spec.WorkDir` once in `StartProject` and once in `LaunchProject`. Extend the
   private `bringUp` helper to accept it and set `MuxUpOption.WindowName`; set
   the same value on `MuxLandOption.WindowName`. Consequently `s` dashboard
   creation, `S` dashboard creation, and `S` mux-less bare-window creation all
   use the same schema, while a window found by identity is not renamed. Add
   backend tests that observe both forwarded option paths or, if the concrete
   compose service prevents a narrow fake, cover the computation plus the two
   service consumers at their existing seams. This delivers UC1–UC4 and D1.

5. **Verify the change and run the Go review gates.** Run the focused package
   tests, `go test ./...`, then apply `go-cmdman-review-checklist`,
   `go-review-checklist`, and `go-check-outdated-patterns` to the Go diff. Confirm
   that no command/config/RPC/persistence surface changed and that direct
   compose/switcher tests still see `cmdman-<project>` defaults.

## Testing and verification

- `go test ./internal/gitinfo ./cmdman/cli ./cmdman/compose`
- `go test ./...`
- Existing compose-selection tests continue to expect `cmdman-<project>` for
  callers that do not provide an override.
- New table tests cover exactly 10 cells, 11+ cells, wide Unicode, no Git,
  missing Git, no origin, detached HEAD, and a slash-containing branch.
- If the existing backend seams cannot observe the forwarded options, add the
  smallest consumer-side test at `cmdman/compose/mux_internal_test.go`; no e2e
  test is required unless unit tests cannot cover the tmux-visible title.

## Risks

- Truncating the composite to 10 cells can hide most of a long branch and create
  many identical display titles. This is acceptable only because identity is
  separate; IDEA question 2 confirms the compactness policy.
- Existing `probeGit` prefers the origin-derived repository name and falls back
  to the worktree toplevel basename. A linked worktree without an origin may
  therefore display its directory name as the repo name; preserve this behavior
  unless implementation research finds a reliable cheap common-dir name.
- Git probing adds work to launch actions as well as launcher listing. Retain the
  existing timeout and treat every failure as a directory-name fallback.

## Open questions

None.

## Traceability

| Requirement / decision | Owning step(s) |
|---|---:|
| UC1: Git worktree becomes `<repo>-<branch>` without `cmdman-` | 1, 2, 4 |
| UC2: non-Git/probe failure falls back to work-directory basename | 1, 2, 4 |
| UC3: complete title is at most 10 cells including ellipsis | 2 |
| UC4: launcher-only; existing windows and other creators unchanged | 3, 4 |
| D1: launcher-only naming override, identity unchanged | 3, 4 |
| D2: move and reuse the existing Git probe | 1 |
| D3: detached HEAD uses work-directory basename | 2 |
| D4: 10-cell limit includes ellipsis | 2 |

Replaying IDEA.md end to end: launcher actions load the normalized spec (step
4), probe and form/fallback/truncate the display name (steps 1–2), pass the same
override through dashboard and bare-window creation (steps 3–4), and leave
identity lookup plus non-launcher defaults untouched (steps 3–4). Every decided
clause has an owner; no work is handed off.
