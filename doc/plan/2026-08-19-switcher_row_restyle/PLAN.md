# Switcher command row restyle + wheel scroll

One-line: make command rows title-first on both surfaces (switcher widget and
Commands tab) — state carried by the command name's color, fixed unbracketed
2-cell index and bell columns, name and title clamped with ellipses — plus,
on the switcher: mouse-wheel scrolling and an `M` binding that manages the
active project.

IDEA.md gate: confirmed by user, 2026-08-20 (reconfirmed after the
`M`-binding addition; first confirmed 2026-08-19). All open questions
resolved — see DECISION.md R1–R9.

## Goal / success criteria

1. A command row's title is cut to the pane width with a right-side ellipsis;
   no row ever exceeds the pane or wraps. (IDEA "Title is the payload")
2. The command name is cut with a right-side ellipsis, never hard-cut; its
   column shrinks before the title on narrow panes but never below 6 cells.
   (R8)
3. Command rows carry no per-row reported-status word / hollow circle; the
   name's color says the state: strong red = waiting, flat yellow = working,
   flat green = reported idle/done, dim (faint) green = running-unreported.
   (R1/R3)
4. A dead row keeps its lifecycle word (`exited(0)`, `failed`, `pending…`)
   where the title would sit; its name renders in the plain weak shade. (R2)
5. The scale index renders right-aligned in a fixed 2-cell column, no
   brackets; the bell in a fixed 2-cell column; both present on every row
   (spaces when absent) so all columns align on every row. (R5, IDEA
   "Alignment is unconditional")
6. Both surfaces render this same language. (R4)
7. Mouse wheel over the switcher scrolls the list 3 lines per notch without
   moving the selection; clicks resolve against the scrolled view; the next
   keyboard move snaps the view back to the selection. (R7, D31)
8. `M` in the switcher opens the project manager for the active project (the
   currently displayed tmux window's), selection irrelevant; `m` unchanged;
   no active project → hint-line message. (R9)

## Scope

- `cmdman/tui/internal/core/render.go` — new `ScaleCell`, `RowNameStyle`,
  `ClampCells`; retire `ScaleBadge`, `RowStateBadge`, `ReportedStatusBadge`.
- `cmdman/tui/widget/switcher/view.go` — `commandLine`, geometry/offset.
- `cmdman/tui/widget/switcher/switcher.go` — wheel handling, scroll state.
- `cmdman/tui/view.go` — `renderCommandList` command-row branch.
- Tests: `widget/switcher/switcher_test.go`, `cmdman/tui/tui_test.go`,
  `e2e/cmdman/tui_widget_test.go` (wherever row text is asserted).

## Non-goals

- Project head lines (marker slot, 🔔/●/○ aggregate, `MarkerGlyph`) — both
  surfaces — stay as they are; D24's hollow/filled dot stands there.
- Launcher and projectmanager widgets unchanged.
- Wheel scroll on the Commands tab (R4: the tab keeps its own
  selection/clamping model; the request was about the switcher pane).
- The compose-up progress view and CLI progress (`cli/progress_tty.go`) keep
  their glyphs.

## Context (current behavior)

Branch fast-forwarded to main's cb1f875 (2026-08-20, routine call): that commit
removed the Layout tab but left `renderCommandList` and the Commands tab
intact, and it touched the same files steps 5–6 edit (`cmdman/tui/view.go`,
`tui_test.go`, `e2e/cmdman/tui_widget_test.go`), so implementing on top of it
avoids a later conflict. Line refs below are against cb1f875.

- Switcher row (`widget/switcher/view.go:241` `commandLine`):
  `"    " + PadCells(name, 12) + " " + RowStateBadge + [" [i]"] + [" 🔔"] + [" · " + title]`
  — optional pieces shift everything after them; `PadCells` hard-cuts a long
  name; a long title is hard-cut at the pane edge by `PadLine` with no mark.
- Commands tab row (`cmdman/tui/view.go:336-391`):
  `indent + prefix + statusGlyph + " " + name(16 hard-cut) + " " + lifecycle label + [" [i]"] + [" 🔔"] + ["  " + status(+detail)] + ["  " + title]`.
- `RowStateBadge` / `ReportedStatusBadge` (`core/render.go:155-175`): live →
  status word or hollow ○ (D24); dead → lifecycle label (D13).
- `ScaleBadge` (`core/render.go:192`): `" [i]"` only when scaled; two callers
  (`switcher/view.go:248`, `tui/view.go:361`).
- Colors (`core/render.go:133-135`): `StyleMarkerIdle`(ANSI 2) /
  `StyleMarkerWork`(3) / `StyleMarkerBlocked`(1); names currently render in
  the `WeakStyle` fg→bg blend.
- Switcher scrolling: `switcherGeometry.off` is recomputed per render by
  `viewportOffset(lines, m.selected, avail)`; no stored scroll position;
  `Update` handles `tea.MouseClickMsg` only (`switcher.go:209`); j/k call
  `moveSelection` (`switcher.go:378-381`). The launcher's `wheel`
  (`launcher/view.go:164`) is the model: step 3, cursor unmoved (D31).

## Approach

### Row anatomy (both surfaces)

```
switcher:  "    " name(nameW) " " idx(2) " " bell(2) " · " payload…
tab:       indent prefix glyph " " name(16) " " idx(2) " " bell(2) " · " payload…
```

- **name** — clamped with `ClampCells` (right-side "…"), styled by
  `RowNameStyle`. Switcher `nameW = clamp(w-21, 6, 12)`: 12 as today when
  the pane affords it (w ≥ 33), shrinking before the title down to 6
  (fixed overhead is 13 cells: indent 4 + gaps 2 + idx 2 + bell 2 + " · " 3;
  the 21 reserves that plus an 8-cell title floor — routine call). The tab
  keeps its fixed 16, ellipsis instead of hard cut.
- **idx** — `ScaleCell`: `fmt.Sprintf("%2d", ScaleIndex)` when scaled, `"  "`
  otherwise; rendered in the weak shade (switcher) / `stylePath` (tab).
- **bell** — `GlyphBell` when `c.Bell && LiveReport(c)`, else 2 spaces
  (`GlyphWidth(GlyphBell)` is the slot width, measured not assumed).
- **payload** — live row: the title (dimmed: switcher `StyleActive`, tab
  `stylePath`), clamped to exactly the remaining cells with "…" so `PadLine`
  never cuts; dead row: the lifecycle word in `StatusStyle(state, pending)`
  (R2) — `label`/`Pending+"…"` exactly as `RowStateBadge` computes it today.
  The `" · "` separator renders only when the payload is nonempty (its
  position is fixed, so alignment is unaffected). Tab live rows additionally
  append `" (" + Detail + ")"` dimmed when the command reported a detail —
  the status *word* goes (color carries it), the command's own words stay
  (routine call).
- **Colors** (`RowNameStyle`, R1/R3): waiting → ANSI 1 bold; working → ANSI 3;
  done/idle reported → ANSI 2; live-unreported → ANSI 2 faint; not live →
  the weak shade (switcher) / plain (tab) as today. Basic ANSI like the rest
  of the markers, so the user's theme applies.

### Wheel scroll (switcher only)

- New `Model.scrollOff int` + `scrolled bool`: while `scrolled`, geometry uses
  `scrollOff` (clamped to `[0, len(lines)-avail]`) instead of
  `viewportOffset`.
- `tea.MouseWheelMsg` in `Update`: ±3 lines (same step as the launcher),
  sets `scrolled`, seeds `scrollOff` from the current derived offset on the
  first notch so the view moves from where it is.
- `moveSelection` clears `scrolled`, restoring selection-following (R7).
- `groupAt` and `renderSwitcher` both read `switcherGeometry`, which is where
  the offset choice lives — clicks stay true by construction.

## Public surface delta (internal-package surface; nothing module-public)

```go
// cmdman/tui/internal/core

// Added:
func ScaleCell(c CommandRow) string                    // " 2", "10", "  " — fixed 2 cells
func RowNameStyle(c CommandRow, weak color.Color) lipgloss.Style // R1/R3 map above
func ClampCells(s string, w int) string                // right-side "…" truncation
func RowPayload(c CommandRow) (text string, live bool) // title, or lifecycle word for dead rows

// Removed (all callers migrate in this plan):
func ScaleBadge(c CommandRow) string
func RowStateBadge(c CommandRow, bg RowBg) string
func ReportedStatusBadge(status string, bg RowBg) string

// Unchanged: GlyphBell/GlyphFilled/GlyphHollow, MarkerGlyph/MarkerStyle,
// ReportedStatusStyle, StatusStyle, PadCells, PadLine, TruncateLeftCells.
```

```go
// cmdman/tui/widget/switcher — Model gains unexported state only:
// scrollOff int; scrolled bool
// Update additionally matches tea.MouseWheelMsg.
```

No config keys, CLI flags, RPC, or persistent formats change.

## Ordered implementation steps

1. **core: helpers** (`internal/core/render.go` + `render_test.go` or the
   package's existing test file): add `ClampCells`, `ScaleCell`,
   `RowNameStyle`, `RowPayload`; delete `RowStateBadge`,
   `ReportedStatusBadge`, `ScaleBadge` (compile breaks guide the caller
   migration in steps 2/4). Unit-test: ScaleCell widths for 0/1/2/10/99,
   ClampCells at exact fit / one over / wide-rune boundary / w≤1,
   RowNameStyle per (State, Pending, Status) row, RowPayload live/dead.
   Verify: `go test ./cmdman/tui/internal/core/`.
2. **switcher row** (`widget/switcher/view.go` `commandLine`): render the new
   anatomy with `nameW = clamp(w-21, 6, 12)` (thread `w` into `commandLine`
   from `switcherLines`); payload clamped to the exact remaining cells.
   Update `switcher_test.go` row-text assertions; add cases: unscaled vs
   scaled alignment, belled vs un-belled alignment, long name ellipsis,
   long title ellipsis at narrow w, dead-row word, name-color per state.
   Verify: `go test ./cmdman/tui/widget/switcher/`.
3. **switcher wheel** (`widget/switcher/switcher.go` + `view.go`): add
   `scrollOff`/`scrolled`, `tea.MouseWheelMsg` case, clamp in
   `switcherGeometry`, clear in `moveSelection`. Tests: wheel moves the
   window and not `selected`; clamped at both ends; j/k snaps back; `groupAt`
   resolves post-scroll clicks. Verify: `go test ./cmdman/tui/widget/switcher/`.
4. **switcher `M` binding** (`widget/switcher/switcher.go`): add
   `case "M": return m.summonActive()` in `onKey`; `summonActive` finds the
   first group with `Active` set and reuses `summonSelected`'s body against
   it (extract the shared summon into a helper taking the group), status
   `"no active project to manage"` when none or unnamed; footer hint
   (`switcherFooter`, `view.go:106`) becomes `"m/M manage"`. Tests: M with an
   active group summons that group (not the selected one); M without one
   sets the status; hint text. Verify: `go test ./cmdman/tui/widget/switcher/`.
5. **Commands tab** (`cmdman/tui/view.go` command-row branch of
   `renderCommandList`): drop the reported word (`reportedText` shrinks to
   detail-only or inlines away — delete it if unused), running `●` case in
   `statusGlyph` becomes `" "` (spinner/◌/✔/✘ stay), `truncate(c.Name, 16)`
   gains the "…" tail, `ScaleCell` + fixed bell cell replace the optional
   badges, live rows drop the `running` label, title clamped. Update
   `tui_test.go` assertions. Verify: `go test ./cmdman/tui/`.
6. **e2e + sweep**: fix `e2e/cmdman/tui_widget_test.go` row-text expectations;
   `go build ./... && go test ./...` and `golangci-lint run` at the repo
   root; run the review skills (`go-cmdman-review-checklist`,
   `go-review-checklist`, `go-check-outdated-patterns`, `go-edit-cobra` not
   needed — no `./cmd` edits).

Steps 1→2→3 and 1→5 are dependent chains; 3 and 4 are independent of 2's
rendering details but touch the same files — land in order to keep diffs
reviewable.

## Testing / verification

- Unit: as listed per step; alignment asserted by comparing the title-start
  column across row variants (strip ANSI with `core.StripANSI`, find " · ").
- e2e: existing `tui_widget_test.go` drives the real widget; update
  expectations, and add a wheel-scroll interaction in step 6. The harness CAN
  deliver wheel events (confirmed by inspection, 2026-08-20): it drives input
  with `tmux send-keys`, so `send-keys -l $'\x1b[<64;x;yM'` (65 = wheel down)
  writes a raw SGR mouse sequence to the widget's stdin, and the vendored
  input decoder (`ultraviolet/decoder.go:412` → `parseSGRMouseEvent`) parses
  SGR mouse unconditionally — no mouse-mode handshake gates it — yielding
  `tea.MouseWheelMsg`.
- Manual: `go build -o bin/cmdman ./cmd/cmdman`, dock the switcher, eyeball
  colors on light and dark terminals.

## Risks

- Color-only state for live rows is invisible to colorblind users; the bell
  and the red/bold pairing (bold survives monochrome) mitigate; accepted by
  R1.
- Faint (dim green) support varies by terminal; where faint is not rendered,
  unreported collapses into idle — exactly R3's accepted fallback.
- `statusGlyph` is shared with `composeUpGlyph`'s visual language
  (`view.go:165`); changing only the running case keeps ✔/✘/spinner parity
  but the two surfaces' "running" now differ — deliberate (compose-up is a
  progress list, out of scope).
- e2e text assertions are brittle against exact cell counts; expect one
  round of expectation fixes.

## Open questions

(none — Q1–Q7 resolved into DECISION.md R1–R8.)

## Traceability

| Decision clause | Owner |
| --- | --- |
| R1 color map (red bold / yellow / green / faint green) | step 1 (`RowNameStyle`) |
| R2 dead rows keep lifecycle word, weak name | step 1 (`RowPayload`), steps 2/4 render it |
| R3 dim-vs-normal green | step 1 (`RowNameStyle`) |
| R4 both surfaces, ScaleBadge→ScaleCell repo-wide, tab glyph stays but ● → space, wheel switcher-only | steps 1, 5; non-goals |
| R5 fixed 2-cell bell column | steps 2, 5 |
| R6 keep " · " separator | steps 2, 5 |
| R7 wheel scrolls view, keyboard snaps back (D31) | step 3 |
| R8 name ellipsis, column ≥ 6, shrinks before title | step 1 (`ClampCells`), step 2 (nameW formula), step 5 (tab 16 + ellipsis) |
| R9 `M` manages the active project; `m` unchanged; no-active → status; hint "m/M manage" | step 4 |
| R9 retracted "dashboard target on top" sort idea | not planned (user retracted) |
| D13 dead run shows no report/title | step 1 (`RowPayload` gates on `LiveReport`) |
| D24 hollow/filled: command-row half retired into R3; project-head half stands | step 1 (R3); non-goals (heads untouched) |
| D31 wheel scrolls away from the cursor | step 3 (R7 adopts it) |
| IDEA use case "glancing at progress" | steps 1–2, 5 |
| IDEA use case "scrolling with the mouse" | step 3 |
| IDEA use case "managing the project on screen" | step 4 |
