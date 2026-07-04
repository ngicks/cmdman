# DECISION — muxctl-00-tier1-hoist

Inherited: scope and placement fixed by
`doc/plan/2026-07-04-01-design_refactors/DECISION.md` D6 (scale codec →
`pkg/cmdman/mux`) and D7 (tier 1 only, no interface extraction). Entries
below are execution-level decisions only.

## D-M0-1: tmux scale helpers go raw-string — RESOLVED 2026-07-04

- Context: the codec is consumed inside `pkg/muxctl/tmux` (`scale_state.go`
  RMW, `list.go` OwnedWindow population), and tmux cannot import
  `pkg/cmdman/mux` (cycle).
- Choice: tmux's scale-option surface deals only in raw strings —
  `OwnedWindow` carries the raw option value, the read helper returns a raw
  string, the write helper takes an encoded string (unsetting when empty) —
  and `pkg/cmdman/mux` owns decode/encode plus the read-modify-write.
- Rationale: keeps the D6 boundary honest ("scale" semantics out of muxctl)
  without new interfaces (D7); option storage mechanics stay in the driver.
- Rejected: leaving the codec in tmux and re-exporting it (violates D6);
  duplicating the codec in both packages (drift).

## D-M0-2: `@cmdman_scale` option name stays in tmux — RESOLVED 2026-07-04

- Choice: the option-name constant remains in `tmux/scale_state.go` beside
  `markerOption`/`leafOption`.
- Rationale: which window option stores the value is driver storage
  mechanics, consistent with the other cmdman-branded options that remain
  tmux-side; only the value's meaning (the codec) moves.
- Rejected: threading the option name in from `pkg/cmdman/mux` (parameter
  noise for a single-driver world; tier-2 territory per D7).
