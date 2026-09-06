---
tags: e2e test flake mux compose
---

# TestComposeMuxCycleScale_NoWindowError flakes with empty output

Recorded 2026-08-30 in the legacy backlog `issue.md`; migrated 2026-09-03.

`TestComposeMuxCycleScale_NoWindowError`
(`e2e/cmdman/mux_cycle_scale_test.go:222`) failed once during a full-suite
run with a non-nil error but empty stdout *and* stderr, then passed on
retry, on a baseline run of the same HEAD, and under a compose-family
stress run. Observed while sweeping the e2e tests onto the Cmd/Session
harness; the composed child environment, timeout, and WaitDelay are
byte-identical before and after the sweep, so the flake predates it.

The empty-output-with-error signature fits exec.Cmd's 3s WaitDelay
aborting the stdout/stderr copy under load — a bound the harness and the
old hand-rolled wiring share.

Fix direction: reproduce under load (e.g. -count with parallel suite
pressure), then either capture the real failure (surface the WaitDelay
abort distinctly from an empty run) or raise/rethink the WaitDelay bound
for invocations that legitimately run long under contention.
