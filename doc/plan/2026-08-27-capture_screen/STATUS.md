# capture-screen — status

State: **in progress** — gate confirmed 2026-08-28; implementing.

## Checklist (mirrors PLAN.md steps)

- [x] Open questions resolved with user (D5–D11, 2026-08-27)
- [x] Idea gate: IDEA.md confirmed by user (2026-08-28)
- [x] Public surface delta finalized; traceability table in PLAN.md
- [ ] 2. `screenTracker.capture` + unit tests (C2 recover, C3 renderer,
      D12: existing emulator surface only — no vendored vt edits)
- [ ] 3. proto `CaptureScreen` RPC + `buf generate`
- [ ] 4. monitor server handler under `outputMu` (C1)
- [ ] 5. `Service.CaptureScreen` — D14 non-TTY rejected (hint: cmdman
      logs), D10 stopped-TTY error, D9 `-S`/`-E` string parsing
- [ ] 6. CLI `capture-screen` command + completion
- [ ] 7. compose variant (pending Q2)
- [ ] 8. e2e coverage (plain / -e / -S / non-TTY error / stopped error)
- [ ] 9. man page + README

## Next action

Implement steps in order via ng-orchestrator, committing after each.
