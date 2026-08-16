# STATUS — project-manager widget

Current state: **implemented (steps 1–8 + D20/D21 fixes), merged with
main, final review addressed.**

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
      **Correction (final review)**: this check compared the branch against
      its own fork point, not main's tip — main was already 18 commits
      ahead (statusbar removal, panel→switcher fold, `SwitchTarget`). The
      drift surfaced at the final review gate and was resolved by the D22
      merge; the D44-related conclusions above were unaffected.

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
- [x] Step 3 — detection: D3 "TUI-wide … cwd fallback"; D10 "highest-priority
      detection probe"; D4 "message naming both probes that failed" (token ·
      window · cwd trail); D13 "token probe … against `ListWindows` rows …
      only when `$TMUX`/`$ZELLIJ` is present" (2026-08-16; e2e
      `TestTUIWidget_SwitcherMarksWindowProject` with negative control;
      D15 [automatic] precedence call; statusbar inherits identity-first
      Active; two discoveries in HANDOFF.md; `mux.CurrentWindowID` wrapper
      per D13 amendment)
- [x] Step 4 — backend ops: D2 mapping (SetScale = replica count,
      CycleScale = shown replica, layouts = existing methods); D11 "Replicas
      … is the per-service instance count … not `LabelScale`"; D14 "reports
      … only when every dashboard window … agrees" (+ abstention amendment,
      pinned by `TestAgreedScalePositions` and e2e
      `TestComposeMuxScaleState_AgreementOnly`); incl. refactoring
      `cmd/.../compose_scale.go` onto the hoisted `compose.Service.Scale`
      (user request 2026-08-16) — done 2026-08-16, D16 contract deviations
      recorded; e2e for ProjectManager/SetScale/CycleScale deferred to
      step 5+ (no CLI surface until the widget acts)
- [x] Step 5 — widget model/view per PLAN.md key table; D14 "error line …
      must not imply the cycle didn't happen" ("cycle reported: …",
      pinned by `TestManagerCycleFailureWording`) — done 2026-08-16; D18
      backward-cycle/refusal semantics; `ctrl+d` not bound (key table lists
      only `q`/`ctrl+c`; panel widgets still bind it — parity open to user);
      16 model unit tests; manual pty launch verified against a real
      project
- [x] Step 6 — switcher summon: D1 "same mux auto-detect + flags path", D5,
      D7 `m`, D9 "row under cursor", D4 inline popup-unavailable message;
      D17 "explicit compose target … skips the ambient identity/cwd chain"
      — done 2026-08-16; D19 seam shape (`PopupChild` argv, `Silent`
      summon); e2e `TestTUIWidget_SwitcherSummonsProjectManager` (summon B
      from inside A's window, negative control run) +
      `TestTUI_PopupRunsTheFullTUI` regression guard; new upstream bug and
      lint nit logged in HANDOFF.md
- [x] Step 7 — docs: man page + bind-key snippet with `--mux-token`; D12
      "wraps it in `run-shell`" — done 2026-08-16; every public-surface
      flag verified mentioned; frame.5 verified correct as-is; two stale
      PLAN claims fixed (arrows row, `--workdir`-as-target superseded by
      D17) and the cobra Long's `--workdir` sentence corrected to match
- [x] Step 8 — test sweep incl. new e2e — done 2026-08-16. Four e2e added in
      `e2e/cmdman/tui_widget_projectmanager_test.go`: D10 token detection from
      outside the project's window and directory (with a negative control run:
      dropping `--mux-token` fails), the D4 probe trail under a bogus token,
      `+` scaling a live service through the widget (row and store both), and
      `l` cycling the shown replica (pane title, `@cmdman_scale`, badge).
      Sweep green: `go test ./... -count=1`, `go test ./e2e/cmdman -count=1`,
      `golangci-lint run ./...` (0 issues); e2e ran three times without a
      flake. One out-of-scope product bug found and logged in HANDOFF.md
      (explicit compose target drops `--workdir`, so a summoned panel reads
      `×0` and its `+` creates a phantom command under the panel's own cwd).

## Post-sweep fixes (in-scope defects found by step 8)

- [x] D20 — work directory carried into the project-manager loads; summon
      passes the row's `Workdir`; token-path read fixed (reproduce-first
      e2e, HANDOFF entry closed; symlink residual ledgered)
- [x] D21 — write verbs (`SetScale`/`CycleScale`/`ApplyLayout`/`CycleMux`)
      act on the shown project's `info.WorkDir`; token-path phantom write
      fixed (`TestTUIWidget_ProjectManagerActsOnTheProjectItShows`,
      reproduce-first)

## Final gate (2026-08-16)

- [x] Full sweep green pre-merge (build, vet, `go test ./... -count=1`
      incl. tmux e2e, lint)
- [x] ng-reviewer over the 11-commit diff: focus areas clean; one blocker
      (stale fork point → resolved by the D22 merge with main) and six
      minors — all addressed in the D23 batch (`-p` override semantics,
      scale floor, D17-stale comment, coretest arg recording, outside-tmux
      summon e2e; STATUS header fixed here)
- [x] Post-merge + post-batch re-verification sweep

## Next action

None — done. Remaining follow-ups: three moved to `doc/plan/issue.md`
(2026-08-17, user request — upstream `--mux` window-index collision,
symlink-workdir residual, Compose-tab cwd-only mark, the last carrying the
user's undecided lean toward dropping the TUI panel in favor of widgets);
the layout-tab precedence UX stays in HANDOFF.md. User away for the
implementation run: `[automatic]` decisions are D11–D14 amendments,
D15–D23.
