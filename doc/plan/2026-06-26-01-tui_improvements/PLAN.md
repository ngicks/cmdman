# TUI big improvements

> One-line: five sizeable TUI upgrades — popup sizing flags, a reworked Compose
> tab (read def / edit file / compose-up), full terminal emulation for the
> preview via `charmbracelet/x/vt`, a new Layout tab, and a `--tab` default-tab
> flag.

## Goal / success criteria

Ship the following, each independently verifiable and covered by tests:

1. **Popup size & position** — `cmdman tui --popup` can be sized and positioned
   with percentage (and tmux-style) values, forwarded to `tmux display-popup`.
2. **Compose tab rework** — on the Compose tab:
   - `enter` opens a read-only viewer of the selected project's definition;
   - `e` opens the project's compose file in `${VISUAL:-${EDITOR:-vim}}` via a
     terminal handoff, returning to the TUI on exit;
   - `a` runs "compose up" for the project behind a confirmation screen.
3. **Full terminal view** — the preview is rendered through a
   `github.com/charmbracelet/x/vt` virtual-terminal emulator so programs that
   paint the screen (cursor addressing, colors, clears) render correctly instead
   of as sanitized text.
4. **Layout tab** — a new top-level "Layout" tab lists the current project's mux
   layouts in definition order and highlights the layout the running dashboard
   currently displays; selecting one applies it.
5. **`--tab` flag** — `cmdman tui --tab=commands|compose|layout` selects the tab
   shown on startup; default `commands` (backward compatible).

## Scope / non-goals

- **In scope:** `pkg/cmdman/tui/*`, `pkg/cmdman/cli/tui*.go`,
  `cmd/cmdman/commands/tui.go`, a new `compose.Service`/`mux` plumbing method or
  two on `tui.Backend`, go.mod (`charmbracelet/x/vt`), tests (unit + e2e).
- **Non-goals:**
  - No zellij popup support (still tmux-only, matching current `--popup`).
  - No change to the on-disk log format or the gRPC attach protocol.
  - No new compose verbs; "compose up" reuses `compose.Service.Up`.
  - Layout tab does **not** add per-pane live terminal mirroring — it lists and
    switches layouts only.

## Context (current behavior & real paths)

TUI model (bubbletea v2, `charm.land/bubbletea/v2`):
- `pkg/cmdman/tui/tui.go` — `Options{Backend, Version, AltScreen, PopupMode}`,
  `Run`, `New`, the `Backend` interface, and message/stream types.
- `pkg/cmdman/tui/state.go` — `Model`, `tab` enum (`tabCommands`, `tabCompose`,
  `numTabs = 2`), `commandsTab`, `composeTab`, `composeRow`, `previewState`.
- `pkg/cmdman/tui/keys.go` — key routing; `onComposeKey` currently maps
  `enter`→`openSelectedProject` (switches to Commands tab), `c`→cycle mux,
  `l`→"not available yet", `r`→refresh.
- `pkg/cmdman/tui/view.go` — `renderTabBar` (hardcoded `{"Commands","Compose"}`),
  `renderComposeBody`, `renderPreview`, `box`/`overlay` helpers, footer hints.
- `pkg/cmdman/tui/update.go` — `Init` batches `loadCommandsCmd`,
  `loadProjectsCmd`, `subscribeEventsCmd`; `Update` switch.
- `pkg/cmdman/tui/runtime.go` — events/debounce, **preview** pipeline
  (`reconcilePreview`, `openPreviewCmd`, `readLineCmd`, `onPreviewLine`), and the
  `tea.Exec` attach handoff (`startAttach`, `attachExec`).
- `pkg/cmdman/tui/preview_sanitize.go` — `sanitizePreviewLine` strips terminal
  control sequences (keeps SGR only); this is what vt replaces.
- `pkg/cmdman/tui/popup.go` — confirmation popups (`popupAttach`, `popupRemove`,
  `popupForceRemove`, `popupMuxWarn`) and the read-only help overlay.
- `pkg/cmdman/tui/mux.go` — `cycleMux`, `cycleMuxCmd`, `muxDoneMsg`.

Backend / launch:
- `pkg/cmdman/cli/tui_backend.go` — `serviceBackend` implements `tui.Backend`
  over `*cmdman.Service` + `compose.Service`. `ListProjects` builds
  `tui.ProjectInfo{Name,Path,Workdir,…,HasMux}`. `CycleMux` rebuilds & runs the
  dashboard via `compose.LoadOrProject`+`mux.Build`+`mux.Run`.
- `pkg/cmdman/cli/tui.go` — `RunTUI`, `RunTUIChild`, and the popup launcher
  `RunTUIPopup`/`runTmuxPopup`. The tmux popup argv is built here:
  `args := []string{"display-popup","-E"}` (+ optional `-d cwd`).
- `cmd/cmdman/commands/tui.go` — cobra wiring; `--popup` is a `BoolFunc`
  captured in `tuiPopupFlag{set,value}`; hidden `tui __child` runs inside popup.

Mux layout data:
- `pkg/cmdman/mux/list.go` — `mux.List(ctx, ListOptions) []OwnedWindow`;
  `OwnedWindow.Marker` is the **applied layout index** (or -1).
- `pkg/cmdman/mux/spec.go` — `Spec.Layouts []Layout` (definition order),
  `Layout.Name`.
- `pkg/cmdman/mux/cycle_scale.go` — `mux.ReadScaleState`; marker→layout mapping
  pattern (`spec.Layouts[window.Marker]`).
- `cmd/cmdman/commands/compose_mux.go` — `runComposeMuxUp` applies a specific
  layout by name/index via `mux.Run(... RunOptions{Layout: layout})`;
  `resolveComposeMuxSelection`, `composeMuxWindowName`.
- `compose.Service.Up` (`pkg/cmdman/compose/service_up.go`) — Create+Start;
  the CLI wrapper is `runComposeUp` in `cmd/cmdman/commands/compose_up.go`.

Tests:
- `pkg/cmdman/tui/tui_test.go` — drives `Model.Update` directly with a
  `fakeBackend` (in-package). New features extend this fake.
- `pkg/cmdman/cli/tui_backend_test.go` — unit tests for the projection helpers.
- `e2e/cmdman/tui_test.go` — black-box e2e.

## Approach

Five mostly-independent workstreams (A–E). Ordering note: **A** (tabs as a
slice + `--tab` flag) is a small enabler that **D** (Layout tab) builds on, so do
A before D. **C** (vt preview) is self-contained. **B** (Compose rework) and
**E** (popup flags) are independent. Recommended order: A → E → B → C → D.

### Workstream A — generalize tabs + `--tab` flag (feature 5)

**Single source of truth for tabs.** Define one canonical table in the `tui`
package (new `tabs.go`) that every consumer — tab-bar render, `--tab` flag usage,
validation, and completion — reads from, so names never drift:

```go
// Tab identifies a top-level tab. Exported so cmd/cli can name it for --tab.
type Tab int
const ( TabCommands Tab = iota; TabCompose; TabLayout )

// tabDefs is THE source of truth: order, display name, and CLI token.
var tabDefs = []struct{ tab Tab; name, key string }{
    {TabCommands, "Commands", "commands"},
    {TabCompose,  "Compose",  "compose"},
    {TabLayout,   "Layout",   "layout"},
}

func TabNames() []string          // display names, e.g. for renderTabBar
func TabKeys()  []string          // CLI tokens, e.g. {"commands","compose","layout"}
func ParseTab(s string) (Tab, error) // token → Tab (validates against tabDefs)
func NumTabs() int                // == len(tabDefs)
```

- Rename the current unexported `tab`/`tabCommands`/`tabCompose`/`numTabs`
  (`state.go`) to the exported `Tab`/`TabCommands`/… driven by `tabDefs`; add
  `TabLayout`. `renderTabBar` (`view.go`) renders `TabNames()` instead of the
  literal `{"Commands","Compose"}`; tab cycling uses `NumTabs()`.
- Add `Options.InitialTab Tab` (default `TabCommands`); `New` sets `m.active`.
- `--tab` flag wiring follows the **established enum-flag convention** (same shape
  as `--progress`). The single source of truth is `tabDefs`/`TabKeys()` in `tui`;
  everything `--tab`-specific is composed in `cmd` from those data helpers — **no
  usage-string wrapper is added to `cli` or `tui`**:
  - **Usage string is inline in `cmd`**, composed from `tui.TabKeys()` (e.g.
    `"Tab shown on startup: " + strings.Join(tui.TabKeys(), ", ")`), exactly like
    the `--popup` usage string already written inline in `cmd/.../tui.go`. It
    stays correct automatically because it reads `TabKeys()` — adding a tab needs
    no edit here.
  - `tui.ParseTab(s) (tui.Tab, error)` is the validator. This one *must* live in
    `tui` with the enum it maps to (it is the inverse of `tabDefs`, not help
    text); RunE calls it and passes the resulting `tui.Tab` down.
  - `cmd/cmdman/commands/tui.go`: `cmd.Flags().StringVar(&flagTab, "tab",
    "commands", <inline usage>)` + `cmd.RegisterFlagCompletionFunc("tab",
    tabCompletions)` where `tabCompletions` returns `tui.TabKeys()`.
  - `cli.RunTUI`/`cli.LaunchTUIPopup` gain a `tui.Tab` parameter and just set
    `tui.Options.InitialTab` — no parsing or help text in `cli`. Forward the
    chosen token to the popup child via a new `--tab` arg in
    `PopupConfig.childCommand` so popup mode honors it too. (Use the
    `go-edit-cobra` skill for the cobra edits.)

### Workstream B — Compose tab rework (feature 2)

- **`enter` → definition viewer.** New scrollable read-only overlay (modeled on
  the help overlay in `popup.go`/`view.go`) showing the selected project's
  **raw compose YAML file** (OQ2 resolved: raw text, matching what `e` edits).
  Add a `defViewer` state field to `Model` plus a `Backend` method
  `ProjectDefinition(ctx, name, composeFile) (string, error)` implemented in
  `serviceBackend` (reads the compose file from disk). Scrolls with
  j/k/PgUp/PgDn; `esc`/`q` closes.
  - *Deferred (note, not this plan):* a canonical/normalized view (`compose
    config` output) was considered but shelved — it would require wiring env-var
    resolution so interpolation can be tested correctly and adds UI clutter; may
    be added later as a toggle.
- **`e` → edit in `$VISUAL`/`$EDITOR`/vim.** Reuse the `tea.Exec` handoff pattern
  from `runtime.go` (`attachExec`): resolve the editor (`$VISUAL` → `$EDITOR` →
  `vim`), run `editor <composeFile>` with the real std fds, then on return
  `tea.ClearScreen`+`tea.RequestWindowSize`+reload projects. Backend resolves the
  compose-file path for never-run named projects (empty `Path`) on demand via
  `compose.LoadOrProject`.
- **`a` → compose up + confirm, with a live progress overlay** *(OQ4: in-TUI
  overlay)*. New `popupComposeUp` confirm kind; on confirm, run
  `compose.Service.Up` with a **TUI-side `compose.Reporter`** that forwards each
  `compose.Event{Command,Phase,ExitCode,Err}` into the model as a tea message
  (via `program.Send`/a channel command), driving a progress overlay that mirrors
  the compose TTY reporter (per-service spinner/◌/●/✔/✘ marks — reuse the glyph
  set already in `view.go`). The overlay stays up until the op's terminal phase,
  then collapses to a footer summary; the event-driven debounce re-list surfaces
  the new commands. `serviceBackend.ComposeUp` builds the spec via
  `compose.LoadAndNormalize` and calls `Up` with `compose.WithReporter(...)`.
- **Old `enter` (open-in-Commands): dropped** (OQ3 resolved). `openSelectedProject`
  and its `enter` binding are removed; users switch with tab + filter, and the new
  Layout tab covers mux navigation. (Check for any test relying on it.)
- Update footer hints (`view.go`) and the help overlay (`popup.go`).

### Workstream C — vt-backed terminal view (feature 3) — *attach-source only* (OQ7)

Decision: vt full emulation is fed by a **raw attach stream**, not the
line-structured log reader. Logs stay sanitized text (avoids the R1 round-trip
risk entirely).

- Add `github.com/charmbracelet/x/vt` to go.mod.
- The attach `Session` (`pkg/cmdman/attach_session.go`) already exposes
  `Recv() []byte` (raw stdout, scrollback replay then live) and `Resize`. Add a
  **read-only raw stream** on `tui.Backend` — e.g. `RawView(ctx, id)
  (RawStream, error)` — implemented in `serviceBackend` by opening an attach
  session and only ever calling `Recv()` (never `SendStdin`), so the command is
  never affected by input.
- **Thread `Tty` through the projection so the predicate exists.** The fallback
  decision below needs to know whether a command is TTY-backed, but the current
  projection drops it: `tui.CommandInfo` has no TTY field and `commandInfos`
  (`tui_backend.go`) does not read `e.ConfigJSON.Tty` (the source field is
  `model.CommandConfig.Tty bool`, reachable via the store entry's
  `ConfigJSON *model.CommandConfig`). Add `Tty bool` to `tui.CommandInfo`, set it
  from `e.ConfigJSON.Tty` in `commandInfos`, carry it onto `commandRow`
  (`state.go`) and through `groupFromInfos`, and update the `tui_backend_test.go`
  projection tests. The preview predicate then reads `commandRow.tty`.
- Preview pane gains a **terminal-view mode**: when the selected command has a
  raw/attachable source — running **and** TTY-backed (`row.state == running &&
  row.tty`) — feed its raw bytes into a persistent `*vt.Emulator` sized to the
  pane and render `term.Render()` each frame in `renderPreview`. When no raw
  source is available (exited, or a non-TTY log-only command), fall back to the
  existing sanitized log text. Keep `preview_sanitize.go` for that fallback.
- **Resize side-effect:** do NOT forward the pane size to the remote via
  `Session.Resize` — that would resize the real command's PTY and disturb the
  program and any concurrent attach. Size only the local `vt.Emulator` to the
  pane and let vt clip/scroll. (Decision D9; revisit if fidelity demands it.)
- Lifecycle: close the raw stream when the selection moves or the command is no
  longer attachable, mirroring the existing `reconcilePreview`/`stopPreview`
  pattern in `runtime.go`.

### Workstream D — Layout tab (feature 4)

- New `layoutTab` state in `state.go`: `rows []layoutRow{name string}`,
  `selected int`, plus the resolved current project + the running dashboard's
  marker.
- Data: a `Backend.ListLayouts(ctx, project, composeFile) (LayoutsInfo, error)`
  returning the spec layouts in definition order **and** the current marker.
  `serviceBackend` implements it via `compose.LoadOrProject` (→ `Spec.Mux.Layouts`)
  + `mux.List` (→ `OwnedWindow.Marker`). **Project scope (OQ5 resolved):** a
  single "current" project — the cwd-active mux project, falling back to the
  Compose-tab selection.
- Default selection = current marker (so focus lands on the displayed layout).
- `enter` applies the selected layout: new `Backend.ApplyLayout(ctx, project,
  composeFile, layoutName)` wrapping `mux.Run(... RunOptions{Layout:name})` (same
  build path as `serviceBackend.CycleMux`). In direct mode reuse the
  `popupMuxWarn` confirmation (rearranges the current window); in popup mode apply
  immediately. **If no dashboard is running for the project (OQ6 resolved),
  `enter` starts one at the chosen layout** — `mux.Run` with `Layout:name` already
  does this (a fresh window applies the named layout), so the same call path
  covers both apply-on-running and start-if-absent. This also retires the Compose
  tab's `l` "not available yet" stub.
- Render in `view.go` (`renderLayoutBody`) and add footer/help text.

### Workstream E — popup size/position flags (feature 1) — *resolved (OQ1)*

- `cmd/cmdman/commands/tui.go`: add four flags `--popup-width`, `--popup-height`,
  `--popup-x`, `--popup-y`. **Values must be an explicit percentage** matching
  `^[0-9]{1,3}%$` (e.g. `80%`); a bare `80` is **rejected**, and so are tmux
  position tokens like `C`. (Resolved — see D1; no decision deferred to E1.)
- Thread them through `cli.LaunchTUIPopup` → `PopupConfig` (new `Width/Height/
  X/Y string` fields) → `runTmuxPopup`, which appends `-w/-h/-x/-y` to the
  `display-popup` argv when set (tmux accepts `%` for all four). Validate the
  percentage format before invoking tmux so bad input fails fast (R5).
- These flags only make sense with `--popup`; error if given without it.

## Implementation steps (ordered, each independently verifiable)

1. **A1** — add the canonical `tabs.go` table (`Tab`, `TabCommands/Compose/Layout`,
   `tabDefs`, `TabNames`/`TabKeys`/`ParseTab`/`NumTabs`); migrate `state.go`/
   `view.go` to it (render `TabNames()`, cycle via `NumTabs()`). Add
   `Options.InitialTab` + `New`. Unit-test that `TabNames`/`TabKeys`/`ParseTab`
   stay in sync with `tabDefs`. Existing tests green.
2. **A2** — `--tab` cobra flag in `cmd/cmdman/commands/tui.go`: usage composed
   inline from `tui.TabKeys()`, validated with `tui.ParseTab`, completion via
   `tabCompletions` → `tui.TabKeys()`; thread `tui.Tab` through `cli.RunTUI`/
   `cli.LaunchTUIPopup`; forward to popup child argv. Unit test the token→tab
   mapping (`tui.ParseTab`); e2e `--tab`.
3. **E1** — popup geometry flags + `PopupConfig` fields + `runTmuxPopup` argv;
   unit-test the argv builder; validation errors.
4. **B1** — `popupComposeUp` + `Backend.ComposeUp`; footer/help; confirm flow.
5. **B2** — `e` editor handoff + editor resolution + path resolution for named
   projects.
6. **B3** — `enter` definition viewer overlay + `Backend.ProjectDefinition` +
   scroll keys.
7. **C0** — thread `Tty` through the projection: `tui.CommandInfo.Tty` (from
   `e.ConfigJSON.Tty`), `commandRow.tty`, `groupFromInfos`, and the
   `tui_backend_test.go` projection tests. (Prereq for the C2 predicate.)
8. **C1** — add `charmbracelet/x/vt`; raw read-only preview stream contract on
   `tui.Backend` (`RawView`/`RawStream`) + `serviceBackend` impl over an attach
   session (`Recv` only, never `SendStdin`).
9. **C2** — vt emulator preview renderer + local-only resize; predicate
   (`running && tty`) selects terminal-view vs the sanitized fallback; lifecycle
   close mirrors `reconcilePreview`/`stopPreview`.
10. **D1** — `Backend.ListLayouts` + `layoutTab` state + render + default-to-marker.
11. **D2** — `enter` applies layout (`Backend.ApplyLayout`), direct-mode warning;
    remove the `l` stub.
12. **Docs/tests** — refresh `pkg/cmdman/tui` help text, `e2e/cmdman/tui_test.go`,
    and any README/architecture note about the TUI tabs.

## Testing & verification

- **Unit (`pkg/cmdman/tui`)**: extend `fakeBackend` with `ComposeUp`,
  `ProjectDefinition`, `ListLayouts`, `ApplyLayout`, and a raw preview stream.
  Drive `Model.Update` for: `--tab` initial selection; Compose `a`/`e`/`enter`
  flows incl. confirmation; Layout tab default-selection = marker and apply;
  tab-bar rendering with 3 tabs.
- **Unit (`pkg/cmdman/cli`)**: argv builders for popup geometry; editor
  resolution; `ListLayouts`/`ApplyLayout` projection (where pure).
- **e2e (`e2e/cmdman/tui_test.go`)**: `--tab` accepted/validated; (popup/editor
  paths are environment-sensitive — assert flag plumbing, not interactive tmux).
- Build + `go test ./...`; golangci-lint runs via PostToolUse hooks.

## Risks

- **R1 (vt vs log format):** the k8s-file log driver is line-structured; a
  screen-painting program's raw control stream may not round-trip well through
  it, so vt emulation could look wrong for some programs. May need a raw
  passthrough on the reader, or to scope vt to attach-like sources. (OQ7)
- **R2 (direct-mode layout apply):** applying a layout in direct mode rearranges
  the window holding the TUI — must reuse the existing `popupMuxWarn` guard.
- **R3 (tab count assumptions):** several places assume 2 tabs
  (`numTabs`, `renderTabBar`, filter focus routing in `keys.go`) — the A1
  refactor must catch them all.
- **R4 (named-project paths):** Compose `e`/`enter`/`a` need a real compose-file
  path; never-run named projects have empty `Path` and must be resolved on
  demand, else these actions no-op with a clear status.
- **R5 (popup value validation):** bad `-w/-h/-x/-y` values should fail fast with
  a clear error rather than a cryptic tmux failure.

## Open questions

**All resolved** — see DECISION.md (D1–D9). Summary:

- OQ1 popup: four `%`-only flags (`--popup-width/-height/-x/-y`).
- OQ2 def viewer: raw compose YAML file; canonical view deferred (noted).
- OQ3 old Compose `enter`: dropped (open-in-Commands removed).
- OQ4 compose-up: in-TUI live progress overlay via a TUI-side `compose.Reporter`.
- OQ5 Layout tab scope: current project (cwd-active mux → Compose-tab selection).
- OQ6 Layout apply: apply on running dashboard, **and** start one if absent.
- OQ7 vt: read-only raw attach stream (`Session.Recv`); logs stay sanitized.
- OQ8 `--tab`: follow the `--progress` enum-flag convention (StringVar + parse +
  completion); D9: vt does not forward resize to the remote PTY.
