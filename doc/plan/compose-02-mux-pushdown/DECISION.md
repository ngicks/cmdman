# DECISION — compose-02-mux-pushdown

Inherited: API shape (methods on `compose.Service`, uniform four-verb surface)
is fixed by `doc/plan/2026-07-04-01-design_refactors/DECISION.md` D8; codec/
scope boundaries by D6/D7. Entries below are execution-level decisions only.

## D-02-1: Directory naming — RESOLVED 2026-07-04

- Choice: `doc/plan/compose-02-mux-pushdown` per parent D1's `<topic>-NN`
  convention (existing `compose-00`, `compose-01`).
- Rejected: date-based `2026-07-04-NN-*` naming (used for survey/backlog plans,
  not per-item execution plans).

## D-02-2: Error wording where the three copies disagree — RESOLVED 2026-07-04

- Choice: the consolidated `compose.Service` methods use the CLI wording
  (e.g. `"locate cmdman binary: %w"`, `"read scale state: %w"`); the TUI's
  extra `"mux: "` prefixes disappear.
- Rationale: CLI wording is what e2e and users see today; the TUI prefix was
  local convention in tui_backend.go, not user-facing contract.
- Rejected: keeping per-caller wording via options (noise for no value).

## D-02-3: How Config reaches PaneArgvOpts — RESOLVED 2026-07-04

- Choice: extend the `cmdmanSvc` consumer interface
  (`pkg/cmdman/compose/service.go:16-29`) with `Config()`.
- Rationale: `*cmdman.Service` already has it; keeps options structs free of
  redundant DataDir/RuntimeDir plumbing at every call site.
- Rejected: passing config (or exe path) through each `Mux*Options` struct.

## D-02-4: nil-tolerant `NewService` for service-free mux verbs — RESOLVED 2026-07-04

- Context: making `MuxDown`/`MuxLs` methods on `compose.Service` (D8) initially
  forced the thin cmd wrappers to build a `*cmdman.Service` just to reach the
  method. Review flagged this as two user-visible regressions against the plan's
  "no behavior change" bar: (1) `compose mux down`'s Long help promises "Down
  needs no cmdman service", but the wrapper now resolved config and constructed
  one; (2) `compose mux ls` previously listed windows even when the service could
  not be built (replica counts degrade to "?"), but the wrapper now failed hard.
- Choice: make `compose.NewService(nil)` valid — guard the assignment
  (`if svc != nil { s.svc = svc }`) so the stored interface is a genuine nil, not
  a typed-nil pointer. `runComposeMuxDown` calls `NewService(nil).MuxDown(...)`
  (no service, no `rootCfg`); `runComposeMuxLs` builds the service best-effort and
  passes nil on failure, and `Service.MuxLs` guards its replica-count resolution
  with `if s.svc != nil`. This keeps D8's uniform method surface while restoring
  both documented behaviors.
- Rejected: accepting the new service dependency and rewriting the down help text
  (a real behavior change the plan forbids). Rejected: demoting `MuxDown`/`MuxLs`
  to package-level functions (contradicts parent D8's uniform-surface choice).
