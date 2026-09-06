---
tags: monitor eventlog lifecycle bug
---

# Monitor hard-death paths append no exit event

Recorded 2026-08-22 in the legacy backlog `issue.md`; migrated 2026-09-03.

`monitor.MarkMonitorDied` (`cmdman/monitor/mon_clean.go:74-103`) deletes
auto-remove command records without appending any event to the event log,
and `emitEvent` (`cmdman/monitor/mon.go:162`) warns-and-continues when an
append fails. Event-log consumers therefore see such commands vanish
silently. The mux op follower tolerates this via its liveness poll plus a
grace period, but any other consumer relying on a terminal event will hang
or misreport.

Fix direction: append a synthetic failed/exited event in `MarkMonitorDied`
(and consider surfacing repeated `emitEvent` append failures louder).
