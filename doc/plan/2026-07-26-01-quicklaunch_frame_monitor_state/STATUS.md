# Status

**Current state: planning complete — all questions resolved, contracts
finalized. Implementation not started.** (2026-08-10)

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
      table now a passing regression test.
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
- [ ] Contracts section of PLAN.md finalized (table, state fields, proto,
      CLI spellings, frame spec)

## Implementation checklist (mirrors PLAN.md steps)

### Phase 0 — one-shot CLI

- [ ] 1. `compose up --mux`

### Phase 1 — history + launcher (A)

- [ ] 2. `ComposeHistory` migration + queries + store wrapper
- [ ] 3. History write on `up`
- [ ] 4. Launcher view (merged list)
- [ ] 5. Landing (focus switch, background variant, outside-tmux, mux-less)
- [ ] 6. Stale-entry failure experience

### Phase 2 — runtime state (C)

- [ ] 7. vt Bell/Title callbacks in the monitor
- [ ] 8. Storage (CommandState + eventlog composite, pending Q17)
- [ ] 9. OSC hook dispatch (D17, schema pending Q35)
- [ ] 10. Report verb
- [ ] 11. Surfacing in `ls`/`ps`/TUI/Inspect (incl. D20 bucket sort)

### Phase 3 — frame (B)

- [ ] 12. Frame spec type + def discovery + carving mapping
- [ ] 13. Widget entrypoint
- [ ] 14. Scope/spawn muxctl sub-plan (separate plan directory)
- [ ] 15. Frame verbs (show/hide/select/cycle) + switcher widget

## Next action

Begin phase 0 (`compose up --mux`, PLAN.md step 1), delegated to
opus-class implementer agents per the standing process note. The muxctl
first-class-frame sub-plan (D36) is drafted when phase 3 approaches.
Deferred item to plan later, with phase 3: keyboard strip-reach in
tmux (D31).
