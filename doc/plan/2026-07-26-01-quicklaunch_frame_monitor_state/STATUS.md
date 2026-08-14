# Status

**Current state: implemented through step 14 — every step except the
muxctl-blocked step 15.** (2026-08-11)

## Question resolution

- [x] Usability questions 1–10, 21–34 (IDEA.md) — all resolved
      (DECISION.md D3–D31).
- [x] Implementation questions 11–20, 35–36 (NOTES.md) — all resolved
      (DECISION.md D32–D41).
- [x] Contracts section of PLAN.md finalized: ComposeHistory table,
      monitor-held streamed runtime state (D32), status verb family
      (D33), OSC hooks (D40), frame spec + widget entrypoint (D37),
      git-aware launcher rows via exec git (D41).
- [x] Frame usability mock built (D16, updated for D20's grouped
      switcher): `frame_mock/` — run
      `go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/frame_mock`.
- [x] Mock review round 1 folded in (D21): project dots
      (green idle / yellow working / red blocked, bell → blocked),
      whole-group selection highlight, dot legend.
- [x] Mock review round 2 folded in (D22): bell un-folded from the dot —
      distinct 🔔 marker, cleared by selecting the project (immediate
      resolve when already selected); dot reflects reported status only.
- [x] Mock review round 3 folded in (D23): 🔔 replaces the status dot
      until checked (one marker slot: bell when unread, dot otherwise).
- [x] Mock review round 4 folded in (D24): marker margin, green for
      unknown (no status reported), mouse click selects a project
      (first concrete Q31 input), `*` removed — focused-but-not-selected
      gets a weak highlight instead.
- [x] Mock review round 5 folded in (D24 amendment): unknown is a green
      hollow ○, distinct from the filled green ● (idle/done). Plus D25:
      the switcher list is scrollable (viewport follows cursor, mouse
      wheel scrolls).
- [x] Mock review round 7 folded in (D26): one less space between
      marker and name; app rows in a weaker color derived from the
      detected terminal foreground color.
- [x] Launcher mock built (`launcher_mock/` — the full-sized selector
      form factor: D7 merged list, D18 git-aware rows/matching, D4/D10
      landing semantics, Q12 stale-entry removal, D9 warning) and the
      frame_mock statusbar rebuilt as the intended built-in.
- [x] D27 folded in: the launcher fills its window edge-to-edge — popup
      framing belongs to tmux (`tmux display-popup -E -w 80 -h 20 …` to
      preview). Fixed a real viewport bug (narrowing filter stranded the
      scroll window) found by the invariant probe.
- [x] D28 folded in (three rounds): two-pane launcher — locations left
      (fuzzy + tab completion), projects right (toggleable, history
      pre-enabled), `s` start-and-leave / `S` jump-and-attach. The
      original empty-input key rule failed validation (first keystroke
      of `src`/`staging` silently started projects); replaced by the
      three-zone focus model (input → left list → right list via Enter,
      esc walks back, `/` jumps to input, ctrl+u erases). Collision
      table now a passing regression test. Correction (D42): "tab
      completion" had shipped as known-location prefix only; it now
      completes filesystem paths with `~`/`$HOME` expansion, and a
      typed directory resolves into a selectable location.
- [x] D29 folded in: no blocking start view — `s` keeps the launcher
      interactive; starting rows spin ◜◝◞◟ (1 cell under every locale
      condition — ◐◑ rejected as EA-ambiguous) in the marker slot,
      staggered fake bring-ups complete out of order, tick loop stops
      when idle. Precedence: starting > bell > dot > blank. `s` now
      skips already-running enabled projects (flagged change).
      Pending user confirmations: esc from input quits without clearing
      the filter (ctrl+u clears; fzf reflex says esc-clear first?);
      double weak-highlight on both pane cursors while input focused;
      `repo(branch) (project)` double-paren row format; blank marker
      slot for cold entries; `s` skipping already-running projects.
      Awaiting further review — Q27 (percent base) and Q31 (docked
      interaction, mouse now confirmed as one mode) still ride on the
      mock.

Process note: implementation tasks (including mock TUI changes) are
delegated to opus-class subagents via the Agent tool per the user's
instruction (2026-08-09).

## Implementation checklist (mirrors PLAN.md steps)

### Phase 0 — one-shot CLI

- [x] 1. `compose up --mux` (layout pinned to 0 for idempotency; no-mux
      section warns + skips; focus switch does not exist in the mux
      layer yet — see step 5)

### Phase 1 — history + launcher (A)

- [x] 2. `ComposeHistory` migration + queries + store wrapper
- [x] 3. History write on `up` (upsert in `compose.Service.Create` —
      which runs on every `up`, correcting D34's nuance; `Start` bumps
      recency only)
- [x] 4. Launcher view — `cmdman tui widget launcher` (two-pane/three-zone
      D28 form; popup binding:
      `bind-key -n M-Space display-popup -E -w 80% -h 60% 'cmdman tui widget launcher'`)
- [x] 5. Landing — new `muxctl` `FocusWindow`/`AttachCommand` primitives,
      `mux.Land`/`compose.MuxLand`; D8 attach-handoff outside tmux, D9
      synthesized shell window (note: D9's warning is visible inside tmux
      only)
- [x] 6. Stale-entry failure experience (inline error, ctrl+d deletes the
      history row)

### Phase 2 — runtime state (C)

- [x] 7. vt Bell/Title callbacks in the monitor (+ OSC 9/777 per D39)
- [x] 8. Monitor-held state + stream (D32): extended `Status`,
      `WatchRuntimeState` (debounced titles), status set/get/delete
      RPCs, bounded dial helper
- [x] 9. OSC hook dispatch (D17/D40; `block` filters the Attach path only —
      `logs`/`Subscribe` keep the faithful byte record)
- [x] 10. Report verb — `cmdman status set|get|delete` + get-only
      `cmdman compose status` mirror
- [x] 11. Surfacing — `ls`/`ps` STATUS/BELL/DETAIL/TITLE columns +
      `--format` fields (1 s overall dial budget), Inspect, TUI rows,
      switcher markers + D20 bucket sort (D22/D23 bell-replaces-dot
      supersedes D14's literal ranking in the dot)

### Phase 3 — frame (B)

- [x] 12. Frame spec type + def discovery + carving mapping
      (`pkg/cmdman/frame`)
- [x] 13. Widget entrypoint (`cmdman tui widget switcher|statusbar`)
- [x] 14. muxctl sub-plan scoped and drafted:
      `doc/plan/2026-08-10-01-muxctl_first_class_frame/` (10 open
      questions await user resolution)
- [ ] 15. Frame verbs (show/hide/select/cycle) + switcher widget —
      **blocked** on the muxctl sub-plan's outcome (D36)

## Next action

Resolve the 10 open questions in
`doc/plan/2026-08-10-01-muxctl_first_class_frame/` with the user, then
implement that sub-plan and step 15 on top of it. Deferred item to plan
later, with phase 3: keyboard strip-reach in tmux (D31). Smaller
follow-ups noted by implementers: `--hook` flag on `create`/`run` (the
per-command hook layer is currently config-JSON-only), launch-failure →
attention badge (D10's phase-2 consequence), Compose-tab project badges,
launcher right-pane scan for arbitrary compose files, per-driver
`runningIdentities` listing.
