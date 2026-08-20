# Status

State: **implementation in progress — steps 1–3 complete**.

## Checklist

- [x] Current launcher/compose naming path mapped
- [x] Existing `cmdman/cli/gitinfo.go` reuse opportunity identified
- [x] Rough IDEA/PLAN/DECISION/STATUS scaffold written
- [x] Q1 resolved → D3: detached HEAD uses work-directory basename
- [x] Q2 resolved → D4: complete title is at most 10 cells including ellipsis
- [x] IDEA.md gate confirmed by user, 2026-08-21
- [x] Public surface and implementation steps finalized
- [x] Traceability gate passed: UC1–UC4 and D1–D4 map to steps; no handoff
- [x] Step 1: D2 — reuse the existing bounded Git probe and preserve launcher listing
- [x] Step 2: D3/D4 — apply detached-HEAD fallback and the complete 10-cell limit
- [x] Step 3: D1/UC4 — add launcher-only overrides with existing compose defaults
- [ ] Step 4: thread one title through launcher dashboard and landing paths
- [ ] Step 5: run tests and required Go review skills

## Next action

Begin step 4 by threading one computed title through both launcher paths.
