# DECISION LOG — TUI big improvements

Append one entry per material decision as open questions resolve. Stubs below are
seeded from PLAN.md "Open questions"; fill each with the choice, rationale, and
rejected alternatives once decided.

---

## D0 — Plan layout: five workstreams A–E
- **Decision:** Split the work into A (tabs + `--tab`), B (Compose rework),
  C (vt preview), D (Layout tab), E (popup flags); implement A → E → B → C → D.
- **Rationale:** A is a small enabler that D depends on; the rest are largely
  independent, so they can land in separate, individually verifiable PRs.
- **Rejected:** One monolithic change (hard to review/verify per feature).

---

## D1 — (OQ1) Popup size/position flag shape & defaults — **RESOLVED**
- **Decision:** Four flags `--popup-width`, `--popup-height`, `--popup-x`,
  `--popup-y`; **explicit-percentage values only** (`^[0-9]{1,3}%$`, e.g. `80%`) —
  a bare `80` is rejected, and no tmux position tokens (`C`, cell counts). Keep
  tmux's default geometry when unset.
- **Rationale:** One flag per `tmux display-popup -w/-h/-x/-y` is the most
  discoverable in `--help`; restricting to `%` keeps validation simple and the
  values portable/intuitive.
- **Rejected:** Two combined flags (`--popup-size`/`--popup-position`) — needs
  parsing, less discoverable. Config-file setting — too heavy, not per-invocation.
  tmux tokens (`C`, cell counts) — extra surface the user explicitly declined.

## D2 — (OQ2) Definition viewer content — **RESOLVED**
- **Decision:** Show the **raw compose YAML file** text. A canonical/normalized
  view is **deferred** (note only, not built now).
- **Rationale:** Raw matches what `e` edits, so read/edit stay consistent. A
  normalized view would require wiring env-var resolution so interpolation can be
  tested correctly, and adds UI clutter needing more design.
- **Rejected (for now):** Canonical/normalized spec; both-with-toggle — revisit
  later if demand appears.

## D3 — (OQ3) Fate of old Compose `enter` (open-in-Commands) — **RESOLVED**
- **Decision:** **Drop** open-in-Commands; remove `openSelectedProject` and its
  `enter` binding. `enter` becomes the definition viewer.
- **Rationale:** `enter` is reassigned to the def viewer per the request; tab +
  filter and the new Layout tab cover the navigation it provided.
- **Rejected:** Rebind to `o` (keeps a now-redundant shortcut); keep on `enter`
  and move def elsewhere (contradicts the request).

## D4 — (OQ4) Compose-up UX in the TUI — **RESOLVED**
- **Decision:** Build an in-TUI live progress overlay. A TUI-side
  `compose.Reporter` forwards each `compose.Event` (Command/Phase/ExitCode/Err)
  into the model as a tea message; the overlay shows per-service spinner/◌/●/✔/✘
  marks (reusing `view.go`'s glyphs) until the terminal phase, then collapses to a
  footer summary.
- **Rationale:** Compose up can touch many services; live per-service feedback is
  far more legible than a single footer line, and the `Reporter` hook makes it
  cheap to feed.
- **Rejected:** Background action + footer status only — less feedback for a
  multi-service operation.

## D5 — (OQ5) Layout tab project scope — **RESOLVED**
- **Decision:** Scope to a single "current" project: the cwd-active mux project,
  falling back to the Compose-tab selection.
- **Rationale:** Matches the request's "currently running mux dashboard" and the
  single running window's marker; keeps the tab state small.
- **Rejected:** Compose-tab selection only (ignores cwd-active project). All
  running dashboards grouped (bigger UI/state than asked for).

## D6 — (OQ6) Layout tab apply semantics — **RESOLVED**
- **Decision:** `enter` applies the chosen layout to the running dashboard, and
  **starts a dashboard at that layout if none is running** (compose mux up
  <layout>). Direct mode reuses the rearrange-window warning popup.
- **Rationale:** `mux.Run` with `RunOptions{Layout:name}` already applies a named
  layout to a fresh or existing window, so one call path covers both; "start at
  layout" is the natural, frictionless behavior.
- **Rejected:** Apply-only with a "start one first" hint (extra step for no gain).

## D7 — (OQ7) vt scope & preview stream contract — **RESOLVED**
- **Decision:** vt full emulation is fed by a **read-only raw attach stream**
  (`Session.Recv`, never `SendStdin`); the log preview stays sanitized text.
  vt applies to the Commands preview pane's terminal-view mode only.
- **Rationale:** The k8s-file log driver is line-structured and would not
  round-trip a screen-painting program's control stream (risk R1); the attach
  stream already carries faithful raw PTY bytes + scrollback replay, so it
  emulates correctly with no log-format change.
- **Rejected:** Full byte stream from the log reader (R1). Per-line vt styling
  only (does not render cursor-addressing / screen painters).

## D9 — (OQ7 follow-on) vt resize side-effect — **RESOLVED (own call)**
- **Decision:** Do not forward the preview pane size to the remote command via
  `Session.Resize`; size only the local `vt.Emulator` and let it clip/scroll.
- **Rationale:** Resizing the remote PTY would disturb the real program and any
  concurrent interactive attach — unacceptable for a passive preview.
- **Rejected:** Sending resize for pixel-perfect fidelity (has live side effects).

## D10 — Tabs single source of truth — **RESOLVED**
- **Decision:** Define one canonical `tabDefs` table in `tui/tabs.go` carrying
  each tab's order, display name, and CLI token, plus exported helpers
  `TabNames()`, `TabKeys()`, `ParseTab()`, `NumTabs()` and the exported `Tab`
  type/constants. The tab bar, `--tab` usage text, validation, and completion all
  derive from it — adding a tab updates every consumer automatically.
- **Rationale:** Avoids the names drifting between the renderer and the flag help
  (the request's concern); makes the `--tab` help text correct by construction and
  the enum easy to extend.
- **Rejected:** Separate literal lists in `view.go` (tab bar) and the cobra flag
  usage — the current duplication that would drift.
- **Note:** This exports the previously-unexported `tab` enum to `tui.Tab` so
  `cmd`/`cli` can name it for `Options.InitialTab` and parsing.

## D8 — (OQ8) `--tab` flag mechanism — **RESOLVED**
- **Decision:** Follow the project's existing enum-flag convention (as `--progress`
  does): `cmd.Flags().StringVar(&flagTab, "tab", "commands", <inline usage from
  tui.TabKeys()>)` + a `tui.ParseTab` validator + `cmd.RegisterFlagCompletionFunc(
  "tab", tabCompletions)`. Default `commands` (backward compatible).
- **Rationale:** The repo already implements value-bearing enum flags this way
  dozens of times (`--progress`/`ParseProgressMode`/`progressCompletions`,
  `--condition`, `--signal`, `--format`); reuse it rather than inventing a
  bool-func variant that cannot carry an enum value.
- **Rejected:** A literal `BoolFunc` (cannot carry `commands|compose|layout`).

## D11 — Where the `--tab` helpers live — **RESOLVED**
- **Decision:**
  - `tui` owns only the **data + parse**: `Tab`, `tabDefs`, `TabNames()`,
    `TabKeys()`, `ParseTab()`, `NumTabs()`. `ParseTab` lives here because it is the
    inverse of `tabDefs` (token→`Tab`), not help text.
  - The **`--tab` usage string is composed inline in `cmd`** from `tui.TabKeys()`,
    like every other flag-usage string in `cmd` (the `--popup` usage is inline
    there already). No `TabFlagUsage()` function in `tui` **or** `cli`.
  - `cli.RunTUI`/`LaunchTUIPopup` only take a `tui.Tab` and set
    `Options.InitialTab` — no `cli` parse/usage symbols.
- **Rationale:** Flag help text is a `cmd`/cobra concern and the repo writes it
  inline; the single source of truth that must be shared is the token list
  (`TabKeys()`), which `cmd` reads, so the help stays correct without a wrapper.
  A `cli` *or* `tui` usage helper would be needless indirection.
- **Rejected:** `cli.TabFlagUsage`/`cli.ParseInitialTab` wrappers (earlier draft);
  a `tui.TabFlagUsage()` helper (a later draft) — both unnecessary once `cmd`
  composes usage from `TabKeys()`.

## D12 — vt preview must drain the emulator's response pipe — **RESOLVED (live-bug fix)**
- **Decision:** The terminal-view drain goroutine continuously drains and
  discards the vt emulator's internal response pipe (`io.Copy(io.Discard, term)`),
  and on exit closes only that pipe's *writer* (via the exported `InputPipe()`),
  never `Emulator.Close()`.
- **Rationale:** `vt.NewEmulator` backs the terminal's input/response channel with
  an **unbuffered `io.Pipe`**. A full-screen TUI (claude/codex) constantly emits
  terminal *queries* (DA1/DA2 `ESC[c`/`ESC[>c`, cursor reports `ESC[6n`, DECRQM
  mode queries); the emulator answers each by writing the reply into that pipe.
  Because the preview is read-only (D7/D9) we never call `term.Read()`, so the
  **first** reply blocked `term.Write` forever — and that block is held under the
  `SafeEmulator` write lock, which deadlocked `Render()` and froze the whole TUI.
  This was the real cause of the reported popup hang (a `sleep` test command emits
  no queries, which is why tests never caught it). Closing the *pipe writer* (not
  `Emulator.Close()`) ends the discard goroutine without writing vt's
  unsynchronized `closed` flag, so it stays `-race`-clean.
- **Rejected:** Per-chunk render on the message loop (floods Update); moving Write
  off-loop without draining (lock is still shared with Render); `term.Close()` to
  stop the drain (data-races vt's internal `closed` field under `-race`).

## D13 — vt preview must be crash-proof, with log fallback — **RESOLVED (live-bug fix)**
- **Decision:** Every emulator interaction (`Write` in the drain, `Render` in the
  view, `Resize`) is wrapped in `recover()`. On a panic the model sets a session
  flag `termPreviewDisabled`, tears down the terminal-view, and falls back to the
  crash-proof sanitized-log preview for the rest of the session.
- **Rationale:** `vt`/`ultraviolet` (pinned `vt v0.0.0-20260622092256`,
  `ultraviolet v0.0.0-20260525132238`) **panics** on some real control-sequence
  combinations a full-screen TUI emits — verified deterministic: scroll region +
  XTWINOPS resize (`ESC[8;r;c t`) + line-insert →
  `ultraviolet.(*Buffer).InsertLineArea: index out of range` from inside
  `Emulator.Write`. This crashed the whole popup TUI when previewing codex. The
  bug is in the third-party library (read-only module cache), not patchable here;
  a passive preview must never crash the app, so we recover and degrade to logs.
- **Rejected:** Removing the vt terminal-view entirely (loses the feature when it
  works); per-command (vs per-session) disable (more state for negligible gain —
  if vt panics once it is likely to again).

## D14 — PTY-size reporting fixes the cluttered preview — **RESOLVED (clutter fix)**
- **Decision:** The monitor (1) sets a default PTY size of **80×24** when starting
  a TTY command (`pty.Setsize` in `writeTty`), and (2) reports the current PTY
  size to a viewer over the attach stream via a new `AttachResponse.resize` field,
  sent first on attach. The TUI sizes its local vt emulator to that reported PTY
  size (not the preview pane), and the existing render crop (`clampLines` + `box`
  ANSI-truncate) clips the correctly-laid-out frame to the pane. The remote PTY is
  never resized by the preview (still D9).
- **Rationale:** `pty.Start` created the PTY at 0×0, so a full-screen TUI rendered
  at an unknown/guessed size while the emulator was sized to the *narrow* preview
  pane → the lines wrapped/garbled (the reported clutter). Matching the emulator to
  the command's actual PTY width makes the layout faithful; cropping (not
  reflowing) to the pane keeps it clean. A new `Session.RecvMessage`/`AttachMessage`
  surfaces size reports; `Session.Recv` skips them so the interactive attach is
  unaffected.
- **Scope note:** The fix lives in the **monitor**, so it applies to a command
  whose monitor is the new binary. Existing commands on a pre-fix monitor must be
  restarted (`restart` = stop + start spawns a fresh monitor) or re-run; until
  then their preview uses the 80×24 emulator default as a best-effort guess.
- **Rejected:** Resizing the remote PTY to the pane (violates D9, disturbs the live
  program and any interactive attach); inferring width from the byte stream
  (unreliable); a static stored size (goes stale on interactive resize — the
  attach-stream report is always current).
