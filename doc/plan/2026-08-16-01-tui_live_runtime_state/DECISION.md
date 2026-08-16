# Decision log — tui_live_runtime_state


Entries are numbered L1, L2, … (L for "live") to avoid colliding with the
quicklaunch parent's D-series, which this plan inherits (D32, D35 quoted
in PLAN.md).

## Resolved

- **L1 — launcher is out of scope** (user, 2026-08-16; supersedes the
  same day's earlier "live markers only" answer). The user's operative
  words: "We don't need live update of launch target since it is only
  opened when needed and closed immediately after all needed projects
  are started." The launcher is a transient widget; its one-shot
  listing, its command/project state marker behavior, and the dormant
  bell glyph (`cmdman/tui/widget/launcher/view.go:45-47`) all stay
  exactly as they are. The initial "all tui widgets" phrasing was the
  user's own conflation, withdrawn by them. Rejected along the way:
  live Running-dot refresh (L5's mechanism question, now moot); wiring
  the pre-built bell glyph (user chose to leave it dormant even before
  withdrawing the launcher entirely); full title display on launcher
  rows.
- **L2 — layer streams on the eventlog, do not replace it** (user,
  2026-08-16). The eventlog re-list remains the sole membership /
  discovery mechanism (which commands exist); per-command
  `WatchRuntimeState` streams carry only title / reported status /
  detail / bell for already-known commands, reconciled against each
  list reload. Rejected: deriving membership from streams — commands
  the client doesn't know about have sockets it doesn't know to dial;
  no push-discovery mechanism exists.
- **L3 — drop the one-shot `RuntimeStates` fan-out from the TUI's list
  path** (agent, routine, 2026-08-16). Each `WatchRuntimeState` stream
  sends an initial snapshot on subscribe, so the bounded one-shot dial
  inside the TUI-facing `ListCommands`
  (`cmdman/cli/tui_backend_commands.go:36`) becomes redundant once the
  subscription manager covers every listed command; keeping both
  invites snapshot-vs-push ordering races. `ls`/`ps` keep the fan-out
  exactly as D32 assigned them. Rejected: keeping both paths (races,
  double dial cost for no benefit).
- **L4 — switcher recency stamps on push arrival** (agent, routine,
  2026-08-16). `stampTitles` records the title-change time when the
  pushed update arrives, replacing the load-time-observed approximation
  its own comment flags (`switcher.go:441-446`); D20 bucket sorting is
  unchanged, only fed honest timestamps. Rejected: keeping load-time
  stamping (defeats the point of live pushes).
