# capture-screen — decision log

## D1 — Output always goes to stdout (no buffers)

**Decided** (by user, in the request). cmdman has no paste-buffer concept;
behave as if tmux `-p` were always given. `-b` and buffer semantics are out.

## D2 — Drop `-J` (join wrapped lines)

**Decided** (verified infeasible). Neither the vendored vt emulator nor
`github.com/charmbracelet/ultraviolet` records per-line wrap flags
(`uv.LineData` carries only damage indices). Joining wrapped lines would
require teaching the emulator to track wrap points — out of proportion for v1.
Alternative rejected: heuristic joining of full-width lines (guesses wrong on
exactly-width lines).

## D3 — Drop `-C` (octal-escape non-printables)

**Decided** (verified no-op). Emulator cells hold printable graphemes only;
control bytes never land in the grid, so there is nothing to escape.

## D4 — Capture bypasses hook filtering by construction

**Decided**. Attach filters blocked sequences per stream (upstream D40).
Capture regenerates output from emulator cells rather than replaying raw
bytes, so blocked sequences cannot appear; no filter is wired in.

## D5 — Command name: `capture-screen`, no alias

**Decided by user (2026-08-27).** Single canonical name. Rejected: `capture`
alias, tmux-compat `capture-pane`/`capturep` aliases.

## D6 — Compose variant ships in v1

**Decided by user (2026-08-27).** `cmdman compose capture-screen`, mirroring
`compose send-keys`. Rejected: deferring to a follow-up.

## D7 — Non-TTY commands emit `cmdman logs` output [superseded by D14]

**Decided by user (2026-08-27):** "just emit `cmdman logs` output". A non-TTY
command has no emulator screen; `capture-screen` falls back to the same
output `cmdman logs` (non-follow) produces. Rejected: hard error with a hint;
feeding the ring through a throwaway emulator. How screen-shaped flags
(`-a`, `-e`, `-S`/`-E`) behave on this path is Q7.

## D8 — Drop `-P -M -L -F -H -T`; buffers already out (D1)

**Decided by user (2026-08-27).** Adopted flag set is exactly
`-e -a -q -N -S -E`. `-T`'s trimming is the default behavior; `-N` is the
preserve toggle. Rejected: keeping `-L`/`-F`.

## D9 — `-S`/`-E` are string flags accepting `N`, `-N`, `-`

**Decided by user (2026-08-27).** `-S/--start-line`, `-E/--end-line` are
string-typed and parsed like tmux: nonnegative = visible row, negative =
history line, literal `-` = extreme (start of history / end of visible
screen). Rejected: int flags plus a `--whole-history` bool.

## D10 — Stopped TTY command is a "not running" error

**Decided by user (2026-08-27).** The screen exists only in monitor memory;
error explains that. Rejected: falling back to logs output (raw TUI escape
soup is not a screen snapshot).

## D11 — Screen-shaped flags on the logs fallback [moot — see D14]

**Decided by user (2026-08-27).** On a non-TTY target (D7 logs path),
`-e -a -q -N -S -E` are accepted and ignored so one script line works across
TTY and non-TTY targets; the behavior is documented in help and man page.
Rejected: warning per ignored flag; hard error.

## D12 — No changes to the vendored vt package

**Decided by user (2026-08-28):** expanding `internal/third_party/
charmbracelet-x-vt` is to be avoided. Verified feasible: `-a` only renders
when the program is in alt-screen mode (tmux errors otherwise unless `-q`),
and in alt mode the alt screen is the current screen, so the existing
exported surface — `Render()`, `CellAt(x, y)`, `Scrollback().Line(i)`,
`IsAltScreen()` — covers every capture path. Rejected: adding
screen-selectable line accessors to the vendored emulator.

## D13 — edit-in-editor is not a cmdman feature

**Decided by user (2026-08-28):** "It is something users can do. Not a
feature of cmdman." Capture stays a narrow, composable primitive; workflows
like capture → file → `${VISUAL:-$EDITOR}` are user shell compositions.
Rejected: shipping an `edit-screen` command now or planning it for later.

## D14 — Non-TTY commands are rejected (supersedes D7 and D11)

**Decided by user (2026-08-28):** "Reject if command has no tty enabled."
`capture-screen` errors on a non-TTY target with a hint to use
`cmdman logs`. This supersedes D7 (logs-output fallback) and makes D11
(flag handling on that fallback) moot. Rejected: the earlier logs fallback;
emulating the ring buffer.
