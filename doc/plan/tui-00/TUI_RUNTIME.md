# TUI runtime plan

## Scope

This plan covers runtime behavior for `cmdman tui`:

- list data loading
- events API subscription
- preview/log refresh
- async command action lifecycle
- attach terminal handoff
- detach and return-to-menu behavior

Static screen layout and compose mux integration are covered by sibling plans.

## List Loading

The TUI builds its list views from baseline queries and uses events only as
change signals.

Commands tab:

- load stored compose commands with `cmdman.Service.List` and
  `ListRequest{AllStates:true}`
- keep only entries with compose labels, including `compose.LabelWorkdir` and
  `compose.LabelProject`
- group rows client-side by workdir and project labels
- merge in never-run/default-location projects from `compose.ListNamedProjects()`
  so project rows can appear with zero commands
- do not scan the current working directory for compose files in v1

Compose tab:

- load store-known project counts with `compose.Service.ListProjects()`
- merge in names from `compose.ListNamedProjects()` as zero-command projects
- compute the active project by comparing compose workdir labels to `os.Getwd()`
- render a `mux` badge only after loading the project's compose file and finding
  `Spec.Mux != nil`; this badge is not present in `ProjectSummary`

## Events And Refresh

The TUI backend subscribes to `cmdman.Service.Events` while the TUI is running
and reflects state changes to the screen. `Service.Events` tails the local
JSONL event log under the cmdman data directory; it is not a network stream, and
cmdman does not have a central daemon to reconnect to. The event log is global
and includes standalone commands, so the TUI does not apply lifecycle events as
raw row deltas.

Expected behavior:

- subscribe to `cmdman.Service.Events` while the TUI is running
- debounce lifecycle events and re-run the list loaders
- filter reloaded command rows to compose scope by labels
- update visible status messages when action-related events arrive
- use lifecycle events as a signal to refresh command state and reload preview
  content when needed; events do not carry command output
- report local event-tail errors in the footer without closing the TUI
- use targeted refreshes after actions and selection changes for data not fully
  covered by events

Refresh must preserve view state where possible:

- active tab
- selected row
- tab filter text
- fold state
- scroll position

If the selected command disappears, move selection to the nearest visible row.

## Preview

The preview pane shows recent output for the selected command.

Preview content comes from `Service.Logs`, not from the `Service.Events`
subscription. Lifecycle events may trigger a preview reload, but they do not
carry output lines.

Initial behavior:

- load a snapshot with `Service.Logs` and `Tail:N` when selection changes
- size `Tail:N` to keep at least about 2x the current preview viewport height in
  memory, so small scrolls and resizes do not immediately re-read logs
- start one live `Service.Logs` reader with `Follow` for the selected command
- cancel the previous live follow reader when selection changes
- run snapshot and follow readers in goroutines
- deliver loaded lines back to the Bubble Tea update loop as messages
- never block update, navigation, or rendering while logs load
- show an empty state when no output exists
- show a distinct state when the command uses the `none` log driver and no log
  storage exists
- show a clear error when logs cannot be read

Empty state:

```text
No output yet.
```

No-storage state:

```text
No log storage configured for this command.
```

Error state:

```text
Unable to read command output:
permission denied
```

## Async Actions

Start, stop, restart, remove, and attach setup should not block TUI rendering
while service calls are in flight.

Expected behavior:

- set pending state before dispatching the service call
- clear pending state after success or failure
- trigger an immediate refresh after action completion
- preserve selection after refresh when the command still exists
- report action errors in the footer

Duplicate actions against a pending command should be ignored or surfaced as a
status message.

## Service Concurrency

The TUI backend may share one `*cmdman.Service` across event subscription,
preview loading, and command actions. `Service` protects its lazily opened store
and event-log writer, and the store uses `*sql.DB`, which is safe for concurrent
use.

The TUI model itself should still serialize state updates through the Bubble Tea
update loop. Service calls may run in goroutines, but their results should be
sent back as messages rather than mutating view state directly. Duplicate
actions for the same command remain guarded by the pending-action state.

## Attach Flow

Attach uses the existing attach primitive directly.

Flow:

```text
a
↓
Attach confirmation popup
↓
enter on <yes>
↓
suspend TUI rendering
↓
open attach session
↓
tea.Exec with custom tea.ExecCommand or ReleaseTerminal/RestoreTerminal handoff
↓
cli.Attach(...) with real os.Stdin/os.Stdout
↓
detach keys, command exit, or attach error
↓
restore/redraw TUI
```

Implementation constraints:

- open the session with `Service.OpenAttachSession`
- call `cli.Attach` directly
- do not route through the default Cobra `cmdman attach` path
- do not use sticky attach behavior from the TUI attach flow
- pass the real `*os.File` descriptors for stdin and stdout to `cli.Attach`
- ensure only the Bubble Tea command goroutine owns the terminal while attach
  runs

Reason: `cli.Attach` returns `nil` when the user detaches, while sticky attach
has its own post-exit prompt loop.

## Terminal Handoff

Attach temporarily owns the user's terminal. The TUI must release its terminal
state before calling attach and restore/redraw after attach returns.

Expected behavior:

- use Bubble Tea terminal release plumbing before attach starts:
  `tea.Exec` with a custom `tea.ExecCommand` around the direct `cli.Attach`
  call, or `Program.ReleaseTerminal` / `Program.RestoreTerminal`
- leave alternate screen or suspend TUI rendering before attach starts
- allow attach to enter raw mode and forward resize/signal events
- let `cli.Attach` own raw-mode toggling and SIGWINCH ioctl handling while the
  handoff is active
- after detach, restore the TUI renderer and previous selection
- after command exit, restore the TUI renderer and show a status message
- after attach error, restore the TUI renderer and show the error
- after restoring Bubble Tea, force a redraw and re-issue a resize message so the
  layout matches the current terminal size

Detach should return to the TUI menu. `Ctrl+c` during attach is forwarded to the
remote command by the attach implementation; users should detach with the
configured detach keys.

## Test Coverage

Runtime tests should cover:

- startup loads command rows with `Service.List`
- default-dir projects from `compose.ListNamedProjects()` appear with zero
  commands
- standalone commands from the global event log/list results are filtered out
- lifecycle events trigger a debounced re-list instead of direct row mutation
- local event-tail errors are reported without closing the TUI
- action-triggered refresh does not reset tab/filter/fold state
- preview refreshes on selection change
- preview snapshots are loaded through `Service.Logs` with `Tail:N`
- only one live `Follow` reader runs for the selected command
- selection change cancels the previous live `Follow` reader
- log readers deliver Bubble Tea messages without blocking navigation
- preview errors are rendered as view state
- `none` log driver renders the no-storage preview state
- async action completion triggers immediate refresh
- attach confirmation is required
- detach returns to the previous selected row
- command exit during attach returns to the TUI with a status message
- attach errors are reported after TUI redraw
- attach handoff releases and restores the Bubble Tea terminal
