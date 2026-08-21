# Decision log

## D1: launcher-only naming override (2026-08-21)

Decision: newly created launcher windows use the new work-directory-derived
schema. Other compose/mux callers retain their current default, existing owned
windows are not renamed, and ownership identity does not change.

Rationale: the requested problem is the launcher presenting heavily reused
project names. Window titles are presentation, while the existing opaque
identity already distinguishes work directory plus project.

Rejected: globally replacing `ProjectSelection.MuxWindowName()` and changing
every compose/switcher window title.

## D2: reuse the existing Git probe (2026-08-21)

Decision: move `cmdman/cli/gitinfo.go` into a reusable root `internal/gitinfo`
package and consume it from both launcher listing and launcher window naming.

Rationale: it already handles command timeouts, Git worktrees, submodules,
gitdir indirection, origin-derived repository names, and silent fallback.

Rejected: a second Git probing implementation dedicated to window titles.

## D3: detached HEAD uses the work-directory basename (2026-08-21)

Decision: when Git reports a detached HEAD or no useful branch name, use the
work-directory basename rather than form `<repo>-HEAD`.

Rationale: confirmed by the user; a literal `HEAD` is not a useful work-context
label, while the selected directory remains meaningful.

Rejected: displaying `<repo>-HEAD`.

## D4: the 10-cell limit includes the ellipsis (2026-08-21)

Decision: cap the complete title at 10 terminal display cells, including the
Unicode ellipsis when truncation occurs.

Rationale: confirmed by the user; this makes the tmux window-name width a true
upper bound, including for wide Unicode names.

Rejected: allowing ten content cells plus an additional ellipsis cell.

## D5: keep pre-existing-window coverage at the service seam [automatic] (2026-08-21)

Decision: retain the focused compose/launcher tests that prove identity remains
separate from the optional title and do not add another tmux e2e case solely
for landing on a pre-existing direct-compose window. Update the existing live
launcher tests to assert the new title for windows the launcher creates.

Rationale: the service tests cover empty-name fallback, explicit override
forwarding, and shared identity lookup, while the updated e2e tests cover both
dashboard and mux-less launcher creation. A further tmux scenario would
duplicate those seams without covering a new production branch.

Rejected: expanding step 5 with a second direct-compose-to-launcher lifecycle
test after the required focused and full suites already cover the changed paths.
