# Status

State: **plan finalized** — idea gate passed (re-confirmed after the D8
pivot, 2026-08-21); all open questions resolved (D1–D3, D6, D8–D12);
implementation steps detailed; traceability gate walked. Ready to implement.

## Checklist

- [x] Q1–Q3 resolved → D1/D2/D3 recorded
- [x] Adversarial verification recorded and re-checked against code
      (2026-08-21, 6/6 confirmed)
- [x] Q6 resolved → D6: no automatic viewer restore
- [x] Approach pivot → D8 (supervised operation; driver untouched);
      D9 (ephemeral failure records)
- [x] Idea gate: rewritten IDEA.md confirmed by user (2026-08-21)
- [x] Q8–Q10 resolved → D10 (`--rm` registered op, runtime-dir log),
      D11 (compose identity-schema log name), D12 (`logs -f` follow);
      Public surface delta filled
- [x] PLAN.md implementation steps detailed
- [x] Step 1: marker-env worker entrypoint (`__CMDMAN_INTERNAL_MUXOP` →
      verb runs in-process; options serialization rejected — RunOptions
      carries Svc/Stdout live state)
- [x] Step 2: `CycleScaleOptions.Env` seam; `resolveServer` off
      `os.Environ()`
- [x] Step 3: spawn + follow wrapper — D13 deterministic name (concurrency
      lock + stale-leftover recovery), D10 `AutoRemove`, D11/D15 runtime-dir
      log with 1MiB/1-file caps, D12 `Service.Logs` follow, exit-code
      capture before removal
- [ ] Step 4: wire `mux up` / `compose mux up` / `mux down` / `cycle-scale`
      / `mux frame *` and widget CycleMux (D14) through the wrapper
- [ ] Step 5: e2e — matrix rows 3/4 all-✅ (D1 absorb, D3 no extra
      feedback), paneless invocation, multi-window from inside, frame
      replacement, failure injection (D6/D9/D11: log file present, no viewer
      restore, `remain-on-exit off`), UC7 sync-UX regression

## Traceability

- D1 absorb → step 5 (rows 3/4 + extra-pane e2e)
- D2 all verbs (via D8 uniformity) → steps 1, 3, 4
- D3 no extra feedback → step 4 (adds none), step 5 asserts
- D6 no viewer restore → step 5 failure-injection assertion scope
- D8 supervised op → steps 1, 3, 4
- D9/D11 runtime-dir log, identity-schema name → step 3, asserted in step 5
- D10 `--rm` registered op → step 3
- D12 `logs -f` follow → step 3
- D13 deterministic op name / concurrency lock → step 3
- D14 widget via wrapper → step 4
- D15 1MiB/1-file log caps → step 3
- UC1–UC6 → steps 1–4, verified by step 5; UC7 → step 5 regression
- HANDOFF.md H1 = out-of-scope discovery (floating-pane classification) ✓

## Next action

Implement step 1.
