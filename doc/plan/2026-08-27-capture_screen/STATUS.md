# capture-screen — status

State: **done** — implemented, reviewed (approve-with-nits; nits fixed), full suite green.

## Checklist (mirrors PLAN.md steps)

- [x] Open questions resolved with user (D5–D11, 2026-08-27)
- [x] Idea gate: IDEA.md confirmed by user (2026-08-28)
- [x] Public surface delta finalized; traceability table in PLAN.md
- [x] 2. `screenTracker.capture` + unit tests (C2 recover, C3 renderer,
      D12: existing emulator surface only — no vendored vt edits)
- [x] 3. proto `CaptureScreen` RPC + `buf generate`
- [x] 4. monitor server handler under `outputMu` (C1)
- [x] 5. `Service.CaptureScreen` — D14 non-TTY rejected (hint: cmdman
      logs), D10 stopped-TTY error, D9 `-S`/`-E` string parsing
- [x] 6. CLI `capture-screen` command + completion
- [x] 7. compose variant (pending Q2)
- [x] 8. e2e coverage (plain / -e / -S / non-TTY error / stopped error)
- [x] 9. man page + README (QA 2026-08-29: README half not done — README
      covers no interaction commands at all; folded into
      `doc/plan/issue/issue.md` as a broader README-coverage issue)

## Next action

None — awaiting user review. HANDOFF.md holds two out-of-scope discoveries
(compose-attach man page missing --scale; non-hermetic
TestStatus_WithoutRunningMonitor).
