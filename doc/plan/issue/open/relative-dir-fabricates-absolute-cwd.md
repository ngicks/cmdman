---
tags: model validation cwd monitor bug
---

# Relative command Dir fabricates an absolute-looking reported cwd

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

`model.ValidateCreate` checks `CommandConfig.Dir` only for non-emptiness,
never `filepath.IsAbs`, and `-w/--workdir` reaches it verbatim. A relative
dir (`rel/sub`) round-trips through the monitor's cwd seed
(`cmdman/monitor/runtime_state.go`, `cwdURL` → `file://localhost/rel/sub`)
and comes back out of `cwdPath` as `/rel/sub` — a fabricated absolute path
that contradicts the proto `RuntimeState.cwd` field's documented "absolute
path" contract. The same input makes the attach viewer's `chdirWorkDir`
resolve against the viewer's cwd rather than the monitor's.

Fix direction: validate `Dir` absolute at create, or absolutize it at
create/seed time.
