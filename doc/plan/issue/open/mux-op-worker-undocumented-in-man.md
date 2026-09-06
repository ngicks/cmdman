---
tags: docs man mux compose
---

# Supervised mux-op worker mechanism is undocumented in doc/man

Recorded 2026-08-22 in the legacy backlog `issue.md`; migrated 2026-09-03.

The pane-destroying mux verbs (`mux up`, `compose mux up`, `mux down`,
`compose mux down`, `cycle-scale`, `mux frame hide|show|cycle`) now run as
detached supervised worker commands so they survive their own invoking
pane being consumed. None of this is described in `doc/man/cmdman-mux.1.md`
or `doc/man/cmdman-compose-mux.1.md`: the worker model, the
`muxop-<identity>` concurrency lock and its "mux op already running for
this window" error, the `<runtime-dir>/mux/*.log` diagnostic location, and
the exception that a spec piped on stdin runs in-process and therefore
gives up surviving its own pane. Nothing existing is invalidated; the new
behavior is just missing.

Fix direction: a docs pass over both man pages adding the worker model,
the lock/error, the log location, and the stdin exception.
