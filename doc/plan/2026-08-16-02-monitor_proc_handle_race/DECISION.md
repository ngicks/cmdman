# Decision log — monitor_proc_handle_race

- **R1 — idea phase skipped** (user, 2026-08-16). The user's operative
  words: "Make HANDOFF into new plan. skip idea phase since it does
  not introduce any new usecase." IDEA.md is a stub recording this;
  the plan goes straight to contract/steps. Rejected: a full idea
  walkthrough (nothing user-facing changes).
- **R2 — cover `ptmx` alongside `stdin`/`cmd`** (agent, routine,
  2026-08-16). The inherited HANDOFF names only `stdin`/`cmd`, but
  `m.ptmx` shares the same unguarded writer line
  (`mon_run.go:213-215`) and reader pattern (`Resize`/`PtySize`);
  fixing two of three would leave the same race one field over.
  Rejected: strict-letter scope (half a fix).
- **R3 — one `procMu` over the trio, copy-under-lock readers**
  (agent, routine, 2026-08-16). Rename `stdinMu` → `procMu`; two
  short publish/clear sections in `runOnce`; readers copy the handle
  out and act on the copy (`QueueStdin` keeps holding across the
  write, preserving today's writer serialization). Rejected:
  per-field atomics (three fields change together); reusing
  `outputMu` (different concern; a blocking stdin write would stall
  output fan-out).
