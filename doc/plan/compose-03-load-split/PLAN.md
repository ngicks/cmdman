# compose-03 — split `compose/load.go` into discovery and normalization files

One-line summary: split the 843-line `pkg/cmdman/compose/load.go` into
`discover.go` (file/project discovery) and `normalize.go`
(normalize/validate), same package, pure file reorganization.

Executes item **C7** of `doc/plan/2026-07-04-01-design_refactors/PLAN.md`
(rank 8). Per that plan's D1, C7 gets its own plan dir (it is not on the
direct-execution list).

## Goal / success criteria

- `load.go`'s two independent stages live in separate files:
  - discovery: `DiscoverFile`, `resolveNamedComposeFile`,
    `ListNamedProjects`, `MuxProject`, `ListMuxProjects`, `DecodeFile`,
    `decodeFile`, plus the discovery-owned consts/vars
    (`defaultFileNames`, `namedComposeFile`, file/dir env vars).
  - normalization: `LoadAndNormalize`, `Normalize`, `buildCommandEnv`,
    env/lookup/path helpers, all `validate*` functions, `normalizeAfter`,
    `isNameChar`, plus normalization-owned consts.
- Behavior-preserving verbatim move: no renames, no signature changes, no
  logic edits, no import-path changes for consumers (same package).
- Shared/ambiguous declarations (`NormalizeOpts`, `warnUnknownFields`,
  scale env consts) are placed by primary usage, verified by grep, and the
  placement is recorded in DECISION.md.
- `go build ./...`, `go test ./...`, e2e green.

## Scope / non-goals

- Non-goals: any logic change, API change, or splitting of test files that
  are not cleanly separable. No new packages.

## Context

- `load.go` mixes discovery (`load.go:71-267`) with normalize/validate
  (`load.go:280-843`); little shared state between the stages
  (design_refactors PLAN.md C7).
- Tests live in external-package test files (`compose_test.go`,
  `default_dir_test.go`, `list_mux_test.go`, ...) — likely no per-file
  move needed; verify.

## Implementation steps

1. Grep usages of each ambiguous decl; assign every top-level decl of
   load.go to discover.go or normalize.go (load.go disappears unless a
   natural small remainder argues otherwise — record in DECISION.md).
2. Cut/paste verbatim; fix per-file import lists only.
3. Verify: build, package tests, full `go test ./...` (incl. e2e), lint.

## Testing / verification

- `go build ./...`, `go test ./...` (e2e included), golangci-lint via
  hooks. Reviewer pass to confirm the move is verbatim and complete.
