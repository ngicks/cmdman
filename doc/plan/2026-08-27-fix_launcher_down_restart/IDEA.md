# IDEA — launcher restart after down, and down removing the window

Gate: skipped by user request, 2026-08-27

The user asked to skip the idea phase; this stub records only the behavior the
fix targets, stated as it should be.

- After a project is taken down — its dashboard alone (`d`) or its commands
  (`D` + `y`) — pressing `s` on it starts it again. "Already up here" is only
  said about a project that actually is up.
- Both teardown gestures remove the window the launch *created* — `d` must not
  leave behind a bare shell pane wearing the frame, and `D` must not leave a
  window of dead panes. This holds for the D9-synthesized bare shell window of
  a project with no `mux:` section, too.
- A window cmdman merely *borrowed* (the reuse-current-window takeover of the
  user's own shell window) is still restored, never killed: it was the user's
  before the up and stays theirs after the down.
