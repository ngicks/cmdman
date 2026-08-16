# Idea: monitor process-handle race fix

Idea phase skipped by user decision (see DECISION.md R1): this plan
fixes an internal data race inherited from
[../2026-08-16-01-tui_live_runtime_state/HANDOFF.md](../2026-08-16-01-tui_live_runtime_state/HANDOFF.md)
and introduces no new use case or user-visible behavior. The only
"should" statement: a monitor serving RPCs while its command's run ends
must never race — under `-race` or in production — and in-process tests
must be free to combine RPCs with run endings.
