---
tags: monitor test cwd
---

# Runtime-stream cwd test does not pin immediate delivery

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

`TestStreamRuntimeState_PushesParsedCwd`
(`cmdman/monitor/runtime_stream_test.go`) comments that a cwd change
reaches the watcher at once, but its multi-second receive window cannot
distinguish immediate delivery from the 150ms title throttle. The behavior
is in fact immediate (`titleOnlyChange` compares the whole `runtimeView`,
so a cwd change takes the unthrottled branch); the test just doesn't prove
it.

Fix direction: mirror `TestStreamRuntimeState_ThrottlesTitleBurst`'s
timing assertions.
