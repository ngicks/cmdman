# capture-screen — status

State: **not started** — all open questions resolved (D1–D11); IDEA.md
folded and awaiting the user's gate review.

## Checklist (mirrors PLAN.md steps)

- [x] Open questions resolved with user (D5–D11, 2026-08-27)
- [ ] Idea gate: IDEA.md confirmed by user
- [ ] Public surface delta finalized post-gate
- [ ] 1. vendored vt accessor for screen-selectable line access
- [ ] 2. `screenTracker.capture` + unit tests (C2 recover, C3 renderer)
- [ ] 3. proto `CaptureScreen` RPC + `buf generate`
- [ ] 4. monitor server handler under `outputMu` (C1)
- [ ] 5. `Service.CaptureScreen` — D7/D11 non-TTY→logs fallback (flags
      ignored), D10 stopped-TTY error, D9 `-S`/`-E` string parsing
- [ ] 6. CLI `capture-screen` command + completion
- [ ] 7. compose variant (pending Q2)
- [ ] 8. e2e coverage (plain / -e / -S / non-TTY error / stopped error)
- [ ] 9. man page + README

## Next action

User reviews IDEA.md; on confirmation flip its Gate line and finalize
PLAN.md contracts/steps.
