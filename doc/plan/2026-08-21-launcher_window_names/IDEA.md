# Launcher window names — how they should behave

Gate: confirmed by user, 2026-08-21

The launcher should name a newly created tmux window after the working context
the user selected, not after the compose project. Compose project names are
reused heavily across unrelated directories and worktrees, so titles such as
`cmdman-default` do not help a user distinguish the windows in tmux.

## Use cases

### UC1 — launch a Git worktree

The user selects a compose project whose work directory is a Git working tree.
When the launcher creates its dashboard or mux-less shell window, the window is
named `<repo-name>-<branch-name>` with no `cmdman-` prefix. For example, the
`cmdman` repository on branch `main` is shown as `cmdman-main` before length
limiting.

The name is presentation only. The existing work-directory/project ownership
identity continues to decide which window is found, focused, cycled, or torn
down, so shortened or duplicate titles are safe.

### UC2 — launch a non-Git directory

When the selected work directory is not a Git working tree, Git is unavailable,
or probing fails, the launcher uses the cleaned work-directory basename. A Git
probe failure must not prevent project bring-up or landing.

### UC3 — keep tmux window lists compact

The final title is at most 10 terminal cells, including a single Unicode
ellipsis when truncation is needed. The limit is applied after forming either
`<repo-name>-<branch-name>` or the directory-basename fallback, so wide Unicode
names do not overflow the intended display width.

### UC4 — leave other window creators alone

Only windows newly created through the launcher use the new schema. Direct
`compose mux up`, the switcher, and standalone mux commands retain their current
default naming unless they explicitly supply an override. An already-owned
window found by identity is focused as-is and is not renamed.

## Usability requirements

- No `cmdman-` prefix is added to launcher-created names.
- The same computed name reaches both launcher paths: dashboard creation via
  `MuxUp` and bare-window creation/landing via `MuxLand`.
- Git metadata probing remains bounded and failure-tolerant.
- Git worktrees, submodules, and gitdir indirection continue to work through the
  existing command-based probe rather than filesystem assumptions.
- Window identity and lifecycle behavior do not change; this is a display-name
  change only.

## Confirmed naming details

- A detached HEAD or otherwise unavailable branch falls back to the
  work-directory basename rather than emitting `<repo>-HEAD` (D3).
- “10 chars” means at most 10 terminal display cells for the complete title,
  including the ellipsis (`verylongw…`) (D4).
