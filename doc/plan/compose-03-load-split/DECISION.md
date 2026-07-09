# DECISION — compose-03-load-split

Decision log. One entry per material decision: choice, rationale, rejected
alternatives.

## D1: Placement of shared/ambiguous declarations — RESOLVED

Resolved during implementation (2026-07-08). `load.go` was **deleted
entirely**; every declaration moved verbatim into `discover.go` or
`normalize.go` (two-file split), matching the plan's default in step 1
("load.go disappears unless a natural small remainder argues otherwise").
No compelling standalone remainder exists — the two shared decls both fit
`normalize.go` cleanly (see below), so a third composition file would only
add a ~35-line seam without a distinct responsibility.

### Final decl-to-file assignment

`discover.go` (file/project discovery):
- consts/vars: `defaultFileNames`, `namedComposeFile`,
  `ENV_CMDMAN_COMPOSE_FILE`, `ENV_CMDMAN_COMPOSE_DIR`
- funcs/types: `DiscoverFile`, `resolveNamedComposeFile`,
  `ListNamedProjects`, `MuxProject`, `ListMuxProjects`, `DecodeFile`,
  `decodeFile`

`normalize.go` (normalize/validate):
- consts/types: `ENV_CMDMAN_COMPOSE_SCALE_INDEX`,
  `ENV_CMDMAN_COMPOSE_SCALE`, `NormalizeOpts`
- funcs: `warnUnknownFields`, `LoadAndNormalize`, `Normalize`,
  `buildCommandEnv`, `osEnvMap`, `buildLookup`, `buildLookupFromMaps`,
  `mapToEnvSlice`, `resolvePath`, `resolveLogOptPaths`,
  `validateUserLabels`, `validateName`, `validateMux`, `validateMuxPane`,
  `isNameChar`, `normalizeAfter`, `validateRuntimeFields`

### Rationale for the ambiguous decls (verified by grep)

- **`NormalizeOpts` → normalize.go.** Used by both stages (`DiscoverFile`
  in discover, `Normalize`/`LoadAndNormalize` in normalize) and by
  `selection.go`. Placed with `Normalize` because its identity and doc
  ("caller-supplied overrides for Normalize") tie it to normalization;
  discovery's use is incidental (it only reads `opts.File`). Same-package
  cross-file reference from `discover.go` is free.
- **`warnUnknownFields` → normalize.go.** Only call sites are the two
  inside `Normalize`; pure normalization helper.
- **`LoadAndNormalize` → normalize.go.** It is the result-producing entry
  point (discover then normalize); keeping it beside `Normalize` groups
  the normalized-spec logic. It calls `DiscoverFile` (discover.go) —
  fine, same package.
- **`ENV_CMDMAN_COMPOSE_SCALE_INDEX` / `ENV_CMDMAN_COMPOSE_SCALE` →
  normalize.go.** Neither is referenced inside the moved code; the only
  consumer is `service_create.go`. They concern per-replica command
  environment (a normalize/command-runtime concept) and have nothing to
  do with locating files, so they sit with the normalization domain. This
  keeps the ENV-const split semantically clean: `FILE`/`DIR` (which expose
  the *discovered file's* path) stay in discover.go, `SCALE*` (replica
  semantics) go to normalize.go.
