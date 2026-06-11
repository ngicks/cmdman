# mux-01 — implementation status

Companion to [PLAN.md](./PLAN.md). One section per workstream; agents update
only their own section. States: `todo` / `in-progress` / `done` / `blocked`.

## 1. driver (`pkg/muxctl/tmux`, `pkg/muxctl/doc.go`)

state: done

### Key files changed

- `pkg/muxctl/tmux/tmux.go` — `Config.OwnedIdentity string` field; `New` stamps
  `ownerOption` (`"@cmdman_window"`) on the resolved window when non-empty; both the
  `WindowID`-direct path and the find-or-create path stamp.
- `pkg/muxctl/tmux/reuse.go` — `currentWindowToReuse` now checks `#{@cmdman_window}`
  instead of calling `windowIsMarked`; `currentWindowIfOwned` replaces
  `currentWindowIfMarked` (every-pane check); `windowIsMarked` deleted. Bug fix: the
  4-field `display-message` parse now tolerates the trailing tab being stripped by
  `executor.run`'s `TrimSpace` when the option is unset (accept ≥3 parts, treat
  missing 4th as empty identity).
- `pkg/muxctl/tmux/detach.go` — `Detach` unsets `@cmdman_window` best-effort after
  clearing `pane-border-status`.
- `pkg/muxctl/tmux/list.go` — new file; `ListOwnedWindows(ctx, ListOwnedWindowsOptions)`
  returning `[]OwnedWindow`; "no server" and "can't find session" errors treated as zero
  rows (nil, nil).
- `pkg/muxctl/doc.go` — driver contract note appended: stamp + enumerate capabilities,
  driver-private storage, titles-not-for-identity rule, sidecar-deferred rationale.
- `pkg/muxctl/tmux/ownership_test.go` — new test file (all new tests).
- `pkg/muxctl/tmux/tmux_test.go` — `TestNew_ReusesMarkedCurrentWindow` renamed to
  `TestNew_ReusesOwnedCurrentWindow` and updated to use `OwnedIdentity`.
- `pkg/muxctl/tmux/detach_test.go` — `TestOpenExisting_FindsMarkedCurrentWindow` renamed
  to `TestOpenExisting_FindsOwnedCurrentWindow` and updated to use `OwnedIdentity`.

### Exported API (exact names / signatures)

```go
// pkg/muxctl/tmux
const ownerOption = "@cmdman_window"  // unexported; value is the public contract

type Config struct {
    // ... existing fields ...
    OwnedIdentity string  // new
}

type OwnedWindow struct {
    SessionName string
    WindowID    string
    WindowName  string
    Identity    string
    Marker      int
}

type ListOwnedWindowsOptions struct {
    Path     string
    Socket   string
    Session  string
    Identity string
}

func ListOwnedWindows(ctx context.Context, opts ListOwnedWindowsOptions) ([]OwnedWindow, error)
```

### Decisions refining the plan

- `currentWindowToReuse` 4-field parse: `executor.run` trims stdout, stripping the
  trailing tab when `@cmdman_window` is empty. Fixed by accepting 3 or 4 parts; a
  missing 4th field is treated as empty identity (unowned). Without this fix the
  single-pane reuse and owned-window cycling paths both silently fell through to
  find-or-create.
- Test coverage for the `ReuseCurrentWindow`/`display-message` takeover path: in a
  headless test context `display-message` without `-t` resolves the server's current
  window, which does work for find-or-create and owned-window detection. The test
  `TestNew_ReusesOwnedCurrentWindow` covers this via `select-window` + `OwnedIdentity`.
  True attached-client takeover (where `display-message` resolves via `$TMUX`) cannot be
  driven without a real client; this is noted in `TestNew_StampsOwnerOption_WindowIDPath`.

### Verification

```
go build ./... && go test ./pkg/muxctl/... && golangci-lint run ./pkg/muxctl/...
```

Result: all tests pass, 0 lint issues.

## 2. mux-layer (`pkg/cmdman/mux`, compose identity helper)

state: done

### Key files changed

- `pkg/cmdman/mux/run.go` — `RunOptions.Identity string`; defaults to the resolved
  window name when empty; passed through as `tmux.Config.OwnedIdentity`.
- `pkg/cmdman/mux/down.go` (replaces `detach.go`) —
  `Down(ctx, DownOptions) error`; `DownOptions{Driver, DriverOpt, SessionName,
  WindowName, Identity, Env, Stdout}`. Enumerates by identity via
  `tmux.ListOwnedWindows` (SessionName is a pure narrowing filter, never derived
  from context), tears down each match via `OpenExisting{WindowID}` + `Detach`,
  prints `Restored window <name> (<id>) in session <session>` per match,
  friendly note + nil on zero matches, `errors.Join` across per-window failures.
  Window vanished between list and open = silent skip (raced teardown).
- `pkg/cmdman/mux/list.go` — `List(ctx, ListOptions) ([]OwnedWindow, error)`;
  mux-level `OwnedWindow{SessionName, WindowID, WindowName, Identity, Marker}`
  so upper layers never import the driver type. No printing.
- `pkg/cmdman/compose/hash.go` — `GenerateProjectIdentity(wdHash, project)` =
  `<wdhash>-<escaped-project>` (strict prefix of `GenerateName`).
- `pkg/cmdman/compose/selection.go` — `ProjectSelection.ProjectIdentity()`;
  returns "" for an unnamed project.
- Mechanical call-site updates to keep the build green (full restructure is ws3,
  marked `TODO(mux-01 ws3)`): `cmd/cmdman/commands/{mux,compose_mux}.go` call
  `mux.Down`; `pkg/cmdman/cli/tui_backend.go` `CycleMux` passes
  `selection.ProjectIdentity()` so TUI-built dashboards are stamped identically.
- Tests: `pkg/cmdman/mux/down_internal_test.go` (identity defaulting),
  `pkg/cmdman/compose/identity_test.go` (format/escaping/prefix-of-GenerateName).

### Decisions refining the plan

- Unnamed compose project: `ProjectIdentity()` returns "" → `Down` falls back to
  the same window-name derivation `Run` used; `compose_mux.go` passes
  `WindowName` so up/down derive the same value ("cmdman").
- `List` returns nil (not an error) on no-server / missing-session, mirroring
  the driver semantics.

### Verification

```
go build ./... && go test ./pkg/cmdman/... ./pkg/muxctl/... && golangci-lint run ./...
```

Result: build + tests pass. `golangci-lint run ./...` has 2 pre-existing golines
findings in `pkg/cmdman/eventlog/writer_test.go` (untouched by this plan, present
on main); zero findings in changed packages.

## 3. cli (`cmd/cmdman/commands`, `pkg/cmdman/cli`)

state: done

### Key files changed

- `cmd/cmdman/commands/mux.go` — restructured by the previous agent into `mux` root
  (alias of `up`) + `muxUpCmd` / `muxDownCmd` / `muxLsCmd` subcommands; `--detach`
  deleted; Long text updated with shadowed-arg note and server-wide discovery note.
- `pkg/cmdman/cli/mux_ls.go` — new file by the previous agent; `RenderMuxWindows`
  (table + `--format` rendering via `muxTemplateFuncMap`), `MuxLsFormatUsage()`,
  `DefaultMuxLsRowFormat`, `measureMuxLs`.
- `cmd/cmdman/commands/compose_mux.go` — fully restructured (this workstream):
  - `composeMuxCmd` root is alias of `up`; `composeMuxUpCmd`, `composeMuxDownCmd`,
    `composeMuxLsCmd` subcommands added; `flagDetach`/`--detach` and all
    `TODO(mux-01 ws3)` markers deleted.
  - `runComposeMuxUp` (extracted from old `runComposeMux`): open/cycle behavior,
    layout arg, session flag, cmdman service + leaf resolution.
  - `runComposeMuxDown`: no service or leaf resolution; passes
    `Identity: selection.ProjectIdentity()` and `WindowName: composeMuxWindowName(selection)`
    preserving unnamed-project identity alignment.
  - `runComposeMuxLs`: calls `mux.List` filtered to `selection.ProjectIdentity()`; for
    unnamed project (identity "") falls back to `composeMuxWindowName(selection)` so the
    filter matches what `up` stamped; renders via `cli.RenderMuxWindows` (same entry
    point as `mux ls`, including `--format`).
  - `completeComposeMuxLayout` and helper functions unchanged / preserved.

### Final command surface

#### `cmdman mux`

```
Usage: cmdman mux [path] [flags]
       cmdman mux [command]

Subcommands: up, down, ls

mux / mux up [path]
  -s, --session string   Target tmux session (default: current session when
                         inside tmux, else cmdman)

mux down [path]
  -s, --session string   Narrow teardown to this tmux session only
                         (default: server-wide)

mux ls
  -s, --session string   Narrow listing to this tmux session only
                         (default: server-wide)
      --format string    Output format: "table" (default) or Go text/template
```

#### `cmdman compose mux`

```
Usage: cmdman compose mux [layout] [flags]
       cmdman compose mux [command]

Subcommands: up, down, ls

compose mux / compose mux up [layout]
  -s, --session string   Target tmux session (default: current session when
                         inside tmux, else cmdman)

compose mux down
  -s, --session string   Narrow teardown to this tmux session only
                         (default: server-wide)

compose mux ls
  -s, --session string   Narrow listing to this tmux session only
                         (default: server-wide)
      --format string    Output format: "table" (default) or Go text/template
```

### ls output columns and --format behavior

Columns (both `mux ls` and `compose mux ls`):
```
SESSION  WINDOW  ID  IDENTITY  LAYOUT
```
- `LAYOUT` column: the layout index last applied (`Marker`); `-1` is displayed as `-`.
- `--format ""` or `--format table`: aligned table with header.
- `--format <template>`: Go `text/template` applied per row; no header printed.
  Template fields: `.SessionName`, `.WindowName`, `.WindowID`, `.Identity`, `.Marker (int)`
  Extra func: `muxMarker` (renders `-1` as `"-"`).
  Standard funcs: `cell`, `command`, `deref`, `exitCode`, `fit`, `join`, `json`,
  `pad`, `shortID`, `trunc`, `width`.

### Decisions

- `compose mux ls` identity filter for unnamed project: `ProjectIdentity()` returns ""
  (no stable identity); `runComposeMuxLs` falls back to `composeMuxWindowName(selection)`
  (i.e. `"cmdman"`) so `mux.List` still matches what `up` stamped via its
  `WindowName`-derived identity path.
- `compose mux down` preserves the existing `WindowName: composeMuxWindowName(selection)`
  field (unchanged from ws2 stub) so the unnamed-project down path stays aligned with `up`.
- Parent `-s/--session` is forwarded to child subcommands via the `parentSession *string`
  pattern, mirroring `mux.go` exactly.
- No small bugs found in `mux.go` or `mux_ls.go` while mirroring; both consumed as-is.

### Zero-match message text printed by `down`

Named project (identity non-empty), no `--session`:
```
No cmdman dashboard found for identity "<identity>"
```
Named project (identity non-empty), with `--session`:
```
No cmdman dashboard found for identity "<identity>" in session "<session>"
```
For an unnamed compose project the identity passed is the window name (`"cmdman"`);
the same message text applies.

### Verification

```
go build ./... && go test $(go list ./... | grep -v e2e) \
  && golangci-lint run ./cmd/... ./pkg/cmdman/cli/...
go build -o /tmp/cmdman ./cmd/cmdman \
  && /tmp/cmdman compose mux --help \
  && /tmp/cmdman compose mux down --help \
  && /tmp/cmdman compose mux ls --help \
  && /tmp/cmdman mux --help
```

Result: build clean, all non-e2e tests pass, 0 lint issues, all help texts render correctly.

## 4. docs (`doc/man/*`)

state: done

### Key files changed

- `doc/man/cmdman-mux.1.md` — already partially updated by a prior agent (diff was staged);
  content reviewed and confirmed correct: `up`/`down`/`ls` subcommands, root-as-`up` alias,
  `--detach` removed, server-wide discovery, shadowed-layout-name edge, teardown semantics
  (non-destructive, every matching window, zero-match is exit 0), `ls` columns and `--format`
  template details, Known limitation section.
- `doc/man/cmdman-compose-mux.1.md` — fully rewritten (was still the old single-command
  `--detach` form). Now documents `up`/`down`/`ls` subcommands, root alias, shadowed-name
  edge, server-wide discovery, project-identity-based down (no service/leaf resolution needed),
  `compose mux ls` filters to project identity, all flag options and `--format` details,
  updated synopsis, examples, and See Also.
- `doc/man/cmdman-mux.5.md` — wording on `cmdman mux` / `cmdman compose mux` layout
  application updated to `cmdman mux [up]` / `cmdman compose mux [up]` to reflect the
  root-as-alias structure.

### Files verified unchanged (cross-reference/description sweep)

- `doc/man/cmdman.1.md` — `mux` cross-reference link is correct, no changes needed.
- `doc/man/cmdman-compose.1.md` — `mux` cross-reference link is correct, no changes needed.
- `doc/man/cmdman-compose.5.md` — `cmdman compose mux` reference is correct, no changes needed.
- `doc/man/cmdman-tui.1.md` — "mux layout cycling" description correct, link correct.
- `doc/man/cmdman-attach.1.md`, `doc/man/cmdman-compose-attach.1.md` — only `--detach-keys`
  (stdin detach sequence, unrelated to mux teardown); no changes needed.
- `README.md` — no `mux --detach` or stale mux invocation mentions found.

### Decisions

- `cmdman-mux.1.md`: the staged diff from a prior agent was already the correct target state;
  reviewed and left as-is rather than regenerating.
- `--detach-keys` hits in attach man pages are NOT mux-related; correctly left alone.
- `cmdman-mux.5.md` Validation section still says `cmdman mux` and `cmdman compose mux` as
  names for the leaf-resolution paths (lines 119-120) — these refer to the commands generally
  and are correct regardless of the up/down/ls split; no change needed.

### Verification

```sh
# Build binary (source of truth for flags/usage)
go build -o /tmp/cmdman ./cmd/cmdman

# Verified all four help pages match doc content:
/tmp/cmdman mux --help
/tmp/cmdman mux up --help
/tmp/cmdman mux down --help
/tmp/cmdman mux ls --help
/tmp/cmdman compose mux --help
/tmp/cmdman compose mux up --help
/tmp/cmdman compose mux down --help
/tmp/cmdman compose mux ls --help

# Grep sweep — every hit is consistent with the new surface:
grep -rn 'mux\|--detach' doc/man/
```

Grep result: no `--detach` (mux teardown flag) appears anywhere in `doc/man/`; all
`--detach` hits are `--detach-keys` in attach man pages (unrelated). All `mux` mentions
are consistent with the `up`/`down`/`ls` surface and root-as-alias structure.

## 5. e2e (`e2e/cmdman`)

state: done

### Key files changed

- `e2e/cmdman/mux_test.go` — updated two existing tests and added six new helpers
  plus four new test functions.

### Tests updated

- `TestMux_DetachRestoresWindowAndKeepsCommands` — `"mux", "--detach", specPath`
  → `"mux", "down", specPath`; added assertion that stdout contains
  `"Restored window"`.
- `TestComposeMux_DetachRestoresWindow` — `"mux", "--detach"` → `"mux", "down"`;
  added assertion that stdout contains `"Restored window"`.

### Tests added

- `TestComposeMux_DownFindsWindowServerWide` — builds a compose dashboard (session
  `cmdman`, socket-isolated via `driver_opt.socket`), then invokes `compose mux down`
  with no `--session` from outside tmux (`muxExec` strips `$TMUX`). Asserts that
  stdout contains `"Restored window"` and the project window name, the window
  survives with one pane, and `@cmdman_window` is cleared. Covers the headline
  capability: identity-stamp-based server-wide discovery without `$TMUX` context.
- `TestMuxLs_ListsDashboard` — builds a standalone dashboard on the default tmux
  socket (redirected via `TMUX_TMPDIR`), then runs `mux ls --format
  '{{.SessionName}}\t{{.Identity}}'` against the same redirected socket and asserts
  a row with `session="cmdman"`, `identity="cmdman"`.
- `TestComposeMuxLs_ListsDashboard` — same isolation approach; uses a compose spec
  with no `driver_opt.socket`; runs `compose mux ls --format …` and asserts a row
  with `session="cmdman"` and an identity suffix of `"-muxls"`.
- `TestMux_RootAliasEqualsUp` — table-driven sub-tests: both `cmdman mux <path>`
  (root alias) and `cmdman mux up <path>` (explicit) produce the attach hint and
  create the dashboard window; each sub-test uses its own isolated socket.

### Helpers added

- `muxExecWithTmpdir` / `tmuxRunWithTmpdir` / `killDefaultTmuxServer` — run the
  binary (or bare `tmux`) with a custom `TMUX_TMPDIR`, redirecting the default
  socket so `mux ls` (which has no `--socket` flag) can be tested in isolation
  without touching the developer's tmux server.
- `muxLsYAML` / `composeMuxLsYAML` — spec / compose YAML with no `driver_opt.socket`
  (uses the default socket redirected via `TMUX_TMPDIR`); needed by the ls tests.

### Production bug found: `compose mux ls` ignores `driver_opt.socket`

**Resolved 2026-06-12 — see PLAN.md decision log and ws6 section below.**

`runComposeMuxLs` in `cmd/cmdman/commands/compose_mux.go` called `mux.List` without
passing `DriverOpt` (specifically the `socket` key). If the compose file specified a
non-default `driver_opt.socket`, `compose mux ls` would silently query the wrong
(default) tmux server and return no rows. `runComposeMuxDown` passed `spec.DriverOpt`
correctly; only `ls` was affected. Similarly, `runMuxLs` (`mux ls`) had no spec-file
argument and no `--socket` flag, so it always used the default server. Both bugs were
fixed in ws6 (user-approved decision, 2026-06-12):
- `compose mux ls` now passes `spec.Driver` / `spec.DriverOpt` to `mux.List`.
- `mux ls` gained an optional `[path]` argument with the same `mux down [path]`
  semantics (read only for driver/driver_opt; stdin default skips the read).
The TMUX_TMPDIR-based ls tests still cover the default-socket path; two new tests
(`TestMuxLs_HonorsDriverOpt`, `TestComposeMuxLs_HonorsDriverOpt`) cover the custom-socket path.

### Decisions refining the plan

- Per-test `TMUX_TMPDIR` isolation (rather than `-L <socket>`) is the correct
  approach for `mux ls` / `compose mux ls` tests when no spec-file path is given:
  redirecting `TMUX_TMPDIR` sends both `mux up` (from a no-socket spec) and `mux ls`
  to the same private default server.
- After the ws6 fix: `mux ls <specPath>` and `compose mux ls` with a custom socket
  are now both testable end-to-end; covered by the two new HonorsDriverOpt tests.

### Verification

```
go test ./e2e/... -run 'Mux' -v -timeout 180s
```

Result: all 14 Mux tests pass (4.4 s).

```
go test ./e2e/... -timeout 300s
```

Result: full e2e suite passes (139 s, 0 failures).

```
golangci-lint run ./e2e/...
```

Result: 0 issues.

## 6. ls driver-opt fix (post-ws5, user-approved)

state: done

Decision logged in PLAN.md 2026-06-12: fix `compose mux ls` driver_opt passthrough
and add optional `[path]` arg to `mux ls` to match `mux down [path]` semantics.

### Key files changed

- `cmd/cmdman/commands/mux.go` — `muxLsCmd` changed `Use` to `"ls [path]"` with
  `MaximumNArgs(1)`; `runMuxLs` reads `args[0]` and calls `specDriverOpts(path)` (the
  same helper used by `runMuxDown`) to extract driver/driver_opt; Long text updated to
  document the optional path semantics.
- `cmd/cmdman/commands/compose_mux.go` — `runComposeMuxLs` now passes
  `Driver: spec.Driver, DriverOpt: spec.DriverOpt` to `mux.List`, matching what
  `runComposeMuxDown` already did.
- `doc/man/cmdman-mux.1.md` — synopsis updated (`ls [path]` added); `ls` subcommand
  section updated with the optional-path semantics paragraph.
- `doc/man/cmdman-compose-mux.1.md` — `ls` subcommand section updated with a note
  that listing targets the server selected by the spec's driver/driver_opt.
- `e2e/cmdman/mux_test.go` — stale bug comment removed/rewritten; two new tests added:
  - `TestMuxLs_HonorsDriverOpt` — standalone `mux ls <specPath>` on a custom socket.
  - `TestComposeMuxLs_HonorsDriverOpt` — `compose mux ls` on a custom socket.
- `doc/plan/mux-01-fix-fragile-window-detection/STATUS.md` — this section.

### New / updated test names

- Added: `TestMuxLs_HonorsDriverOpt`
- Added: `TestComposeMuxLs_HonorsDriverOpt`
- Updated comment: `TestComposeMuxLs_ListsDashboard` (no longer mentions known bug)
- Updated comment: `composeMuxLsYAML` (no longer mentions the workaround)

### Verification commands

```
go build ./...
go test ./e2e/... -run 'Mux' -v -timeout 300s
golangci-lint run ./cmd/... ./e2e/...
go build -o /tmp/cmdman ./cmd/cmdman && /tmp/cmdman mux ls --help
```

## 7. supervisor review pass (final)

state: done

Checklist review (go-cmdman-review-checklist, go-review-checklist,
go-check-outdated-patterns) over the full diff: no blockers. Nits fixed:

- `pkg/cmdman/mux/run.go` — extracted `deriveIdentity(identity, windowName,
  sessionName)`: the identity-defaulting chain previously duplicated inline in
  `Run` and `Down`; both now call it.
- `pkg/cmdman/mux/down_internal_test.go` — `TestDown_IdentityExplicit` was
  tautological (tested a local variable, no production code); replaced with
  `TestDeriveIdentity` exercising the real helper.
- `pkg/muxctl/tmux/list.go` — throwaway `Session` no longer sets the unused
  `windowID` field.
- `doc/man/cmdman-mux.1.md` — synopsis `[path]` → `[PATH]` for case
  consistency.

Nits accepted as-is: file sizes slightly over the 300-line guideline
(`cmd/cmdman/commands/{mux,compose_mux}.go`, `ownership_test.go` — no semantic
split point); `WindowID` naming follows the pre-existing package convention.

### Verification

```
go build ./... && go test $(go list ./... | grep -v e2e)   # all pass
go test ./e2e/... -count=1 -timeout 600s                   # full suite passes (134s)
go test ./e2e/... -run 'Mux' -count=1                      # 16/16 pass post-refactor
golangci-lint run ./...   # only the 2 pre-existing golines findings in
                          # pkg/cmdman/eventlog (present on main, untouched)
```
