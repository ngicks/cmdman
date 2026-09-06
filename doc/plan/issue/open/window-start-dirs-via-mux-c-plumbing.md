---
tags: mux tmux cwd feature
---

# Window-level start dirs via multiplexer -c plumbing

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

Viewer panes now report per-command cwd, but windows/panes created by the
mux layer still start in the invoker's directory. `mux.RunOptions` could
gain a work-dir and the tmux driver pass `split-window -c` / session start
directories, fixing dashboard window start dirs independently of
per-command truth. Deliberately left out of the pane-cwd work as
orthogonal.
