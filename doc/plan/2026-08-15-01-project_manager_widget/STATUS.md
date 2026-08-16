# STATUS — project-manager widget

Current state: **plan finalized; implementation not started.**

## Planning progress

- [x] Ground in codebase (explorer report folded into PLAN.md Context)
- [x] Rough scaffold (IDEA / PLAN skeleton / STATUS / DECISION stubs)
- [x] Resolve idea-level open questions Q1–Q4 with user (D1–D5 recorded)
- [x] Fold in convenience-shortcut motivation and D10 (mux token) per user
- [x] Idea gate: user confirmed IDEA.md (2026-08-16)
- [x] Contract round Q6–Q9 resolved with user (D6–D9 recorded)
- [x] Detail PLAN.md (public surface delta, approach, steps)
- [x] Traceability gate (PLAN.md Traceability table: D1–D11 + UC1–UC3 each
      owned by a step)
- [x] Re-grounded against post-finalization main drift (2026-08-16):
      `CommandInfo.ScaleIndex/ScaleCount` now filled via `compose.ScaleOf`,
      badge index-only; upstream D44 staleness of `LabelScale` pinned as D11
      (Replicas = live instance count). Verified unchanged: switcher keys
      (`m` free), `WidgetDefs`, `builtinComponents`, popup-path line refs,
      `compose_scale.go`, cited e2e test name.

## Implementation checklist (mirrors PLAN.md steps)

- [x] Step 1 — spike: D10 "the active muxctl driver interprets [the token]" +
      `CurrentWindowID` behavior in popup/frame contexts → NOTES.md
      (2026-08-16; findings recorded as D12–D14 [automatic], plan amended:
      run-shell bind-key form, ListWindows token resolution + `$TMUX` gate,
      agreement-only Shown)
- [x] Step 2 — registration plumbing; D6 "does **not** join
      `builtinComponents`" held (spec_test.go rejection row); D8
      `--mux-token` flag lands (2026-08-16; stub model until step 5;
      `tui.WidgetProjectManager` alias re-export added beyond the fenced
      delta — cmd layer names widgets via alias.go like every sibling)
- [ ] Step 3 — detection: D3 "TUI-wide … cwd fallback"; D10 "highest-priority
      detection probe"; D4 "message naming both probes that failed"; D13
      "token probe … against `ListWindows` rows … only when
      `$TMUX`/`$ZELLIJ` is present"
- [ ] Step 4 — backend ops: D2 mapping (SetScale = replica count,
      CycleScale = shown replica, layouts = existing methods); D11 "Replicas
      … is the per-service instance count … not `LabelScale`"; D14 "reports
      … only when every dashboard window … agrees"; incl.
      refactoring `cmd/.../compose_scale.go` onto the hoisted
      `compose.Service.Scale` (user request 2026-08-16)
- [ ] Step 5 — widget model/view per PLAN.md key table; D14 "error line …
      must not imply the cycle didn't happen"
- [ ] Step 6 — switcher summon: D1 "same mux auto-detect + flags path", D5,
      D7 `m`, D9 "row under cursor", D4 inline popup-unavailable message
- [ ] Step 7 — docs: man page + bind-key snippet with `--mux-token`; D12
      "wraps it in `run-shell`"
- [ ] Step 8 — test sweep incl. new e2e (detection, summon, scale/cycle)

## Next action

Step 3 (detection). User away for this run: unclear corners are decided
automatically and tagged `[automatic]` in DECISION.md.
