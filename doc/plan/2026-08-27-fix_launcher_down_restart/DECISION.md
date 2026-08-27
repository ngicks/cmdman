# DECISION — fix_launcher_down_restart

## D1 — reset launcher row state via target-carrying down messages

**Decided.** `core.MuxDownMsg` / `core.ComposeDownMsg` gain a
`Target DownTarget` field; the launcher resets `Running`/`starting` on a
successful teardown by `find()`ing the row through it.
Rationale: the messages today carry only `Name`, which cannot address a row —
two locations can hold same-named projects. Additive field keeps the switcher
and projectmanager untouched.
Rejected: re-listing the whole launcher after a down (costs a git probe per
entry, D41); keying by `pendingDown` held in the model (MuxDown has no pending
state, and an in-flight down must survive list edits — same reason `find`
exists).

## D2 — remove windows with `mux.Down` by identity, never via the mux-section helpers

**Decided.** The window teardown in `serviceBackend.ComposeDown` calls
`cmdman/mux.Down` directly with `Identity: selection.ProjectIdentity()` and
the spec's driver when declared.
Rationale: `compose.Service.MuxDown` dereferences `selection.Spec.Mux` (nil
panic) and `compose.ResolveMuxSelectionByName` rejects projects without a
`mux:` section — but the window to remove may be the D9-synthesized bare
shell window of exactly such a project. Do not "simplify" back to those
helpers.
Rejected: `compose.MuxDown` / `ResolveMuxSelectionByName` (above).

## D3 — window removal is TUI-only (Q1, user 2026-08-27)

**Decided.** Window removal lives in `serviceBackend.ComposeDown`; CLI
`cmdman compose down` is unchanged.
Rationale: matches the reported bug with the smallest surface.
Rejected: putting it in `compose.Service.Down` so the CLI removes windows too.

## D4 — on partial down failure the window stays (Q2, user 2026-08-27)

**Decided.** The window is removed only when the down fully succeeded — the
call error and `DownResultErr(result)` both nil. On partial failure what is
left stays visible in its panes.
Rejected: removing the window after every down attempt.

## D5 — down kills only windows cmdman created (Q3, user 2026-08-28)

**Decided.** `Server.New` stamps a window it creates (`createWindow` branch
only) with the `@cmdman_created` tmux window option; `mux.Down` with
`KillCreated` set kills stamped windows (`Session.Close`) and still restores
unstamped ones (`Session.Detach`).
Rationale: the `reuseCurrent` takeover (cmdman/mux/run.go:175) can make the
dashboard out of the user's own shell window; killing that would SIGHUP their
shell. Only cmdman knows at creation time which case it is, so it records it.
The launcher/landing always create (`KeepCurrentWindow: true`, `mux.Land`), so
`d`/`D` from the TUI remove those windows outright — frame included, since
kill-window takes the whole window.
Rejected: always killing on down (destroys borrowed windows); a caller-side
"always kill" flag with no stamp (the TUI cannot know per-window provenance —
switcher `d` acts on windows brought up by CLI `mux up`).

## D6 — kill-on-down is TUI-only (Q4, user 2026-08-28)

**Decided.** `KillCreated` is set by `serviceBackend.MuxDown` and by
`ComposeDown`'s window removal; CLI `cmdman compose mux down`
(cmd/cmdman/commands/compose_mux_down.go) leaves it false and keeps today's
restore behavior.
Rejected: flipping `mux.Down`'s default so the CLI kills created windows too.

## Keep the existing D31 cross-reference style in launcher.go comments [automatic]

The new onMuxDown/onComposeDown doc comment cites (D31) the same way the
surrounding file already does (launcher.go:503, :940). The reasoning is
spelled out in plain words in the comment itself; the token is only a
cross-reference consistent with the file's established convention, so it
stays rather than diverging from neighboring comments.
