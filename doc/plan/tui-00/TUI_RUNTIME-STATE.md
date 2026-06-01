# TUI_RUNTIME — implementation state

Status: **DONE**

## What landed

Backend interface grew with `Events`, `Logs`, `Attach` (plus `EventStream`,
`EventSignal`, `LogStream`, `LogLine` types and `AttachDetached`/`AttachExited`
outcomes) in `pkg/cmdman/tui/tui.go`.

The production backend moved from `pkg/cmdman/tui` to `pkg/cmdman/cli`
(`cli/tui_backend.go`) to break the import cycle with `cli.Attach`:

- `composeCommandInfos` (pure) — `Service.List(AllStates)` projected to compose
  rows, dropping standalone commands lacking the project/workdir labels.
- `mergeProjectInfos` (pure) — `ListProjects()` ∪ `ListNamedProjects()` by name
  (never-run projects appear with 0 commands).
- `Events` — subscribes to `Service.Events`, coalesces records into a buffered
  change-signal channel; surfaces tail errors via `EventSignal.Err`.
- `Logs` — `Service.Logs{Tail, Follow:true}`; goroutine pumps lines, `done`
  channel guards against a send-blocked leak on Close.
- `Attach` — `OpenAttachSession` + `cli.Attach` directly (non-sticky). Uses
  `muesli/cancelreader` for the stdin pipe so detach stops reading os.Stdin
  cleanly (no reader racing bubbletea); maps detach→"detached",
  `ErrRemoteEOF`→"exited".

`pkg/cmdman/tui/runtime.go`:

- **Events/refresh**: `Init` subscribes; `onEventSignal` bumps a debounce
  generation and schedules a `tea.Tick`; `onReloadTick` re-lists only for the
  latest generation (coalesces bursts). Tail errors set footer status and keep
  listening; a closed stream stops the loop. Reloads preserve tab/filter/fold
  (fold map + filter persist; selection restored by id).
- **Preview**: `reconcilePreview` runs after every key (and after a reload),
  starting one Tail+Follow reader per selected command and cancelling the prior
  reader on selection change. `Tail:N` sized ~2× viewport. None-driver →
  no-storage state without opening a reader; read errors → error state; lines
  delivered as `previewLineMsg` and appended (capped). Stale-reader messages
  (cmdID mismatch) are ignored.
- **Attach handoff**: confirming the attach popup runs `tea.Exec` with an
  `attachExec` (ignores bubbletea's std streams; backend uses real fds), and on
  return clears the screen, re-queries `tea.WindowSize`, and refreshes. Detach
  keeps selection; exit/error reported in the footer.

## Tests

`runtime_test.go`: debounced re-list + stale-generation ignore, tail-error
keeps-listening, closed-stream stops, refresh preserves fold/filter/tab, preview
start + previous-reader cancel, line append + stale ignore, read-error state,
none-driver no-storage, project-row clears preview, attach detach/exit/error
status + selection preserved, attach confirm starts handoff, Init subscribes.
`cli/tui_backend_test.go`: standalone filtered out, zero-command named projects.

## Notes / deferred

- Live attach + terminal release/restore needs a real terminal; covered
  structurally (the e2e attach tests already require a PTY the sandbox lacks).
- MUX (`c` cycle, `mux:` badge, popup warning, zellij stub) still pending — the
  `c`/`l` keys and `ProjectInfo.HasMux` remain placeholders for TUI_MUX.
