# NOTES — step 1 spike: token + window resolution contexts

Run 2026-08-16 against a live tmux on this machine. Purpose: settle the three
questions PLAN.md step 1 gates step 3 on. No production code changed.

## Environment

```console
$ tmux -V
tmux 3.7b
$ echo "TMUX=$TMUX ZELLIJ=$ZELLIJ"
TMUX= ZELLIJ=
```

The spike ran outside any multiplexer, on the repo's own toolchain (`go.mod`
declares `go 1.26.0`).

Two scratch tmux servers, both torn down at the end:

- `tmux -L spike-q1 -f /dev/null …` — Q1(a)(b) and Q2 (synthetic windows).
- the default socket redirected with `TMUX_TMPDIR=/run/user/1000/spk` — Q3 and
  Q1(c), because `cmdman mux frame` verbs take no socket flag and must reach the
  same server as `compose mux`.

Driver calls were made from three throwaway Go programs in the scratchpad
(`spike/probe`, `spike/ptyattach`, `spike/scalestate`), a module with
`replace github.com/ngicks/cmdman => <this worktree>`, so every observation below
goes through the real `pkg/muxctl` / `cmdman/mux` code rather than raw tmux.

Two environment constraints hit during the run, recorded because they will bite
anyone reproducing this:

- Both the tmux socket and cmdman's `<runtime-dir>/cmd/<id>/monitor.sock` must
  live on a short path — the scratchpad path is ~100 chars and blows the
  `sun_path` limit. tmux says `error connecting to … (File name too long)`;
  cmdman fails less legibly, as `timeout waiting for state "running", last
  state: "starting"`. `CMDMAN_RUNTIME_DIR=/run/user/1000/spk-run` fixed it.
- `script(1)` and `pkill(1)` are not installed. The attached client the popup
  legs need was produced with `creack/pty` (already a repo dependency) instead.

---

## Q1 — what does `Server.CurrentWindowID` return in each context?

Contract: `pkg/muxctl/driver.go:253-265`. tmux implementation:
`pkg/muxctl/tmux/tmux.go:59-87` — `display-message -p '#{window_id}'`, with
`-t "=<session>:"` prepended when a session is named, and **every** failure
collapsed to `ok=false, err=nil` (tmux.go:82-86). The executor
(`pkg/muxctl/tmux/exec.go:28-42`) never sets `cmd.Env`, so the child inherits
`$TMUX` / `$TMUX_PANE` from the calling process — which is what makes the
question a process-context question at all.

### Headline result

**`CurrentWindowID` reports the window the *attached client* is looking at, not
the window the calling process lives in.** `$TMUX_PANE` is set but never used —
the driver passes no `-t`, and tmux does not fall back to `TMUX_PANE` for the
default target.

### (a) Regular pane

Setup: session `spike` with windows `plain` `@0` (current) and `third` `@2`
(created with `new-window -d`, so not current); session `other` with `second`
`@1`. The probe was typed into `@2`'s pane with `send-keys`.

```console
$ tmux -L spike-q1 send-keys -t @2 "SPIKE_SOCKET=spike-q1 … probe > out" Enter
=== probe regular-pane-@2 ===
env TMUX="/run/user/1000/tmux-0/spike-q1,11798,0" TMUX_PANE="%2"
CurrentSessionName() = "spike", ok=true, err=<nil>
CurrentWindowID("") = "@0", ok=true, err=<nil>
CurrentWindowID("spike") = "@0", ok=true, err=<nil>
```

The process is in `@2`; the answer is `@0`, `ok=true`. Raw tmux from the same
pane shows this is tmux's own resolution, not a driver bug:

```console
TMUX=/run/user/1000/tmux-0/spike-q1,11798,0 TMUX_PANE=%2
bare  tmux display-message -p: spike @0 %0
-L    tmux display-message -p: spike @0 %0
-t TMUX_PANE            : spike @2 %2
```

With a client attached the answer tracks the client, in both directions:

```console
# client /dev/pts/4 on @2, probe run from a pane in @0
CurrentWindowID("") = "@2", ok=true, err=<nil>
# same pane, after `select-window -t @0`
CurrentWindowID("") = "@0", ok=true, err=<nil>
```

Also worth recording — with **no client at all**, the call still answers
`ok=true`:

```console
# no clients attached; 2 sessions, 3 windows; probe run outside tmux entirely
=== probe baseline-multi ===
env TMUX="" TMUX_PANE=""
CurrentSessionName() = "other", ok=true, err=<nil>
CurrentWindowID("") = "@1", ok=true, err=<nil>
```

`@1` is simply some session's current window. There is no `ok=false` signalling
"you are not in a window": the not-inside-the-multiplexer case the contract
describes (driver.go:261-265) only materialises when the server is absent or the
named session does not exist.

### (b) `tmux display-popup`

`display-popup` with zero clients does not run at all — recorded because it is
why the headless legs above could not cover this case:

```console
$ tmux -L spike-q1 display-popup -E "…probe > out"
no current client
(exit=1)
```

With one client attached (via the `creack/pty` helper) the popup runs, and
`CurrentWindowID` **does** see the source window:

```console
# client on @0
=== probe popup-over-@0 ===
env TMUX="/run/user/1000/tmux-0/spike-q1,11798,0" TMUX_PANE=""
CurrentWindowID("") = "@0", ok=true, err=<nil>
# after select-window -t @2, same popup command
=== probe popup-over-@2 ===
CurrentWindowID("") = "@2", ok=true, err=<nil>
```

and against the real project (Q3 setup), summoned over the dashboard window:

```console
=== probe popup-over-dashboard ===
CurrentSessionName() = "sessA", ok=true, err=<nil>
CurrentWindowID("") = "@1", ok=true, err=<nil>
```

`$TMUX_PANE` is empty inside a popup; `$TMUX` is set, and its session-id field
is what the answer follows.

**But it is unreliable with more than one client.** With clients on
`spike:@0` and `other:@1`, a popup summoned explicitly from the first
(`display-popup -c /dev/pts/4`) resolved to the *other* client's window:

```console
$ tmux -L spike-q1 list-clients -F '#{client_name} session=#{client_session} current_window=#{window_id}'
/dev/pts/4 session=spike current_window=@0
/dev/pts/5 session=other current_window=@1
$ tmux -L spike-q1 display-popup -c /dev/pts/4 -E "…probe…"
=== probe popup-2clients ===
env TMUX="/run/user/1000/tmux-0/spike-q1,11798,1" TMUX_PANE=""
CurrentSessionName() = "other", ok=true, err=<nil>
CurrentWindowID("") = "@1", ok=true, err=<nil>
```

After `switch-client -c /dev/pts/4 -t spike; select-window -t @0` the same popup
answered `@0`. So the resolution follows tmux's notion of the current client,
which the summoning client does not control — silently, with `ok=true`. This is
the same effect already recorded on `FocusOptions.ClientSession`
(`pkg/muxctl/driver.go:129-134`).

### (c) Frame-style pane

`Session.ShowFrame` (`pkg/muxctl/tmux/frame.go:157`) builds frame panes with
`split-window` **in the session's own window** (frame.go:412) — a frame pane is
an ordinary pane of the cmdman-owned window, not a separate window or popup.

A real frame was raised on the live dashboard window with `cmdman mux frame show
spike -s sessA`:

```console
$ tmux list-windows -a -F '#{window_id} … identity=#{@cmdman_window} frame=#{@cmdman_frame_def}'
@1 sessA:cmdman-pmspike identity=66a73ab3d3f0-pmspike frame=spike
$ tmux list-panes -t @1 -F '#{pane_id} title=#{pane_title} cmd=#{pane_current_command}'
%1 title=web-2 cmd=cmdman
%5 title=frame-0 cmd=cmdman
```

The frame component process itself could not be instrumented (it is
`cmdman`'s own statusbar), so the probe was run from an extra pane split into
that same framed, identity-stamped window:

```console
# client on @1 (the framed dashboard window)
=== probe frame-window-pane-client-on-@1 ===
env TMUX="/run/user/1000/spk/tmux-0/default,16787,0" TMUX_PANE="%6"
CurrentSessionName() = "sessA", ok=true, err=<nil>
CurrentWindowID("") = "@1", ok=true, err=<nil>
# after select-window -t @0, same pane
=== probe frame-window-pane-client-on-@0 ===
CurrentWindowID("") = "@0", ok=true, err=<nil>
```

So a frame pane behaves exactly like (a): correct while the user is looking at
the window it belongs to, silently wrong once the client moves away.

(Artifact note: that extra split pane carries no `@cmdman_marker` and is not a
frame pane, so `StatWindow` reported `Marker: -1` for `@1` while it existed —
`pkg/muxctl/tmux/stat.go:58-60` skips frame panes but not this one. Killing it
restored `LAYOUT 0` in `compose mux ls`. The frame itself never disturbs the
marker.)

### Q1 conclusion

- The enclosing-window probe is **client-relative, not process-relative**. It is
  right exactly when the calling process's pane sits in the window the client is
  currently displaying — the normal interactive case for a TUI in a pane, and
  the normal case for a popup with a single client.
- It has **no honest "don't know"**: outside any pane, with no client, or with
  the client elsewhere, it still answers `ok=true` with some other window. Step
  3 must therefore not treat `ok=true` as proof of an enclosing window. The one
  existing consumer already guards this way — `cmdman/mux/frame.go:384-388`
  checks `$TMUX`/`$ZELLIJ` before calling it at frame.go:390 — and the
  identity probe should follow that precedent, plus require the resolved window
  to actually carry an ownership stamp.
- A popup **can** see its source window, contra D10's stated rationale ("a
  popup/floating pane runs detached from the window the user summoned it from,
  so process-context probing (`CurrentWindowID`) cannot see the source window").
  The accurate statement is that it is unreliable, not blind: single client →
  correct; multiple clients → silently another client's window. D10's conclusion
  (explicit token is the highest-priority probe) still holds; only the rationale
  needs restating.
- **`display-popup` needs an attached client** (`no current client`, exit 1).
  Any e2e for the summon path (step 6) must attach one — a `creack/pty` helper
  works and is already a repo dependency.

### Q1 side-finding — the documented bind-key snippet does not expand `#{window_id}`

PLAN.md:179-182 documents

```tmux
bind-key -n M-p display-popup -E -w 80% -h 60% \
  'cmdman tui widget project-manager --mux-token "#{window_id}"'
```

Tested as a real key binding, triggered by injecting `M-p` into an attached
client's pty (tmux key bindings are processed on client input, so `send-keys`
cannot reach them):

```console
$ tmux bind-key -n M-p display-popup -E "… SPIKE_TOKEN='#{window_id}' … probe > out"
=== probe bind-key-popup ===
ReadWindowState("#{window_id}", scale) = "", err=<nil>
FindPane("#{window_id}", "web") = "", ok=false, err=tmux: list panes for #{window_id}: … can't find window: #{window_id}
```

The child received the **literal** `#{window_id}`. tmux 3.7b does not
format-expand `display-popup`'s `shell-command`, and `-e VAR=#{window_id}` is
not expanded either (same literal reached the child). The man page agrees: in
the whole `display-popup` entry, the only cross-reference to `FORMATS` is on
`-T` (the popup *title*) — neither `shell-command` nor `-e` is documented as
expanded.

```console
$ gzip -dc …/tmux.1.gz | sed -n '7447,7620p' | grep -n -i 'format\|expand'
97:is a format for the popup title (see
98:.Sx FORMATS ) .
```

`run-shell` **is** expanded:

```console
$ tmux run-shell "echo 'run-shell saw: #{window_id}' > out"
run-shell saw: @1
```

so the working form of the snippet wraps the popup in `run-shell`:

```console
$ tmux bind-key -n M-o run-shell "tmux display-popup -E \"… SPIKE_TOKEN=#{window_id} … probe > out\""
=== probe bind-runshell ===
ReadWindowState("@1", scale) = "web=2", err=<nil>
ReadWindowState("@1", "window") = "66a73ab3d3f0-pmspike", err=<nil>
FindPane("@1", "web") = "%1", ok=true, err=<nil>
```

Step 7's man-page snippet needs this shape (or another expanding carrier), not
the one in PLAN.md today.

---

## Q2 — can `ReadWindowState` / `ListWindows` / `FindPane` resolve a raw `@N`?

### Valid token: yes, as-is, no driver addition needed

`@2` was stamped by hand (`@cmdman_window=proj-a`, `@cmdman_scale=web=2`,
pane option `@cmdman_leaf=web`) and probed from a process **outside tmux
entirely**:

```console
=== probe q2-token-@2 ===
env TMUX="" TMUX_PANE=""
ReadWindowState("@2", scale) = "web=2", err=<nil>
FindPane("@2", "web") = "%2", ok=true, err=<nil>
ListWindows(all) err=<nil> rows=2
  row: id=@1 session=other name=second identity="proj-b" frame="" marker=-1 state=map[scale:]
  row: id=@2 session=spike name=third identity="proj-a" frame="" marker=-1 state=map[scale:web=2]
```

Window ids are server-global: `@2` lives in session `spike`, the probe named no
session, and both calls resolved it. So the token form D10 specifies is existing
surface, as PLAN.md's "`pkg/muxctl`: no change" claims.

**Pane-form tokens also work.** D10 left open whether pane ids are acceptable;
they are — tmux resolves a pane target to its window for these
window-targeting commands:

```console
=== probe q2-stale-%2 ===
ReadWindowState("%2", scale) = "web=2", err=<nil>
FindPane("%2", "web") = "%2", ok=true, err=<nil>
```

### Token → identity: reachable, but only by abusing the StateKey vocabulary

`stateOption` maps a key to `"@cmdman_" + key` (`pkg/muxctl/tmux/scale_state.go:19-21`),
and the ownership stamp is `@cmdman_window` (`pkg/muxctl/tmux/tmux.go:19`), so
`ReadWindowState(token, StateKey("window"))` reads the identity:

```console
=== probe q2-identity ===
ReadWindowState("@2", scale) = "web=2", err=<nil>
ReadWindowState("@2", "window") = "proj-a", err=<nil>
```

This works but `StateKey` is documented as "a closed, code-declared vocabulary"
(`pkg/muxctl/driver.go:46-51`) and `window` is not in it (driver.go:54-62 declares
only `scale` and `frame_def`). Step 3 has two honest options: declare the key, or
resolve the token by matching it against `ListWindows` rows, which carry both
`WindowID` and `Identity` (`pkg/muxctl/driver.go:87-118`). The row match is
preferable — see the staleness result below.

### Stale / bogus token: the three calls disagree, and two of them lie

```console
=== probe q2-stale-@9999 ===
ReadWindowState("@9999", scale) = "", err=<nil>
ReadWindowState("@9999", "window") = "", err=<nil>
FindPane("@9999", "web") = "", ok=false, err=tmux: list panes for @9999: tmux -L spike-q1 list-panes -t @9999 -F #{pane_id}	#{@cmdman_leaf}: exit status 1: can't find window: @9999
ListWindows(all) err=<nil> rows=2

=== probe q2-stale-garbage-token ===
ReadWindowState("garbage-token", scale) = "", err=<nil>
FindPane("garbage-token", "web") = "", ok=false, err=tmux: list panes for garbage-token: … can't find window: garbage-token
```

The diagnostics the driver hides:

```console
$ tmux -L spike-q1 show-options -w -t @9999 -v @cmdman_scale
no such window: @9999
(exit=1)
$ tmux -L spike-q1 list-panes -t @9999 -F '#{pane_id}'
can't find window: @9999
(exit=1)
```

- **`ReadWindowState` swallows every error** and returns `"", nil`
  (`pkg/muxctl/tmux/scale_state.go:52-55`, whose comment scopes the tolerance to
  "the option is absent"). A dead window, a nonsense token, and a live window
  with no state set are all indistinguishable.
- **`FindPane` surfaces the error verbatim** (`pkg/muxctl/tmux/leaf.go:137-139`),
  including the full argv, so it is the one call that reports staleness — but it
  is a leaf-pane query, not a token validator.
- **`ListWindows` takes no window id at all** (`pkg/muxctl/tmux/list.go:31-62`);
  "resolving a token" there means matching it client-side against
  `Window.WindowID`. It also swallows a missing server / missing session into
  zero rows (list.go:66-85), so an empty result cannot be read as "the token is
  stale" on its own.

### Q2 conclusion

Raw `@N` (and `%N`) resolve as-is against `ReadWindowState` and `FindPane`,
server-globally, from any process context — **no `ResolveWindow`-style driver
addition is required to *use* a token**.

What is missing is a way to *validate* one: no call answers "this token names no
window" except `FindPane`, which is the wrong question. For step 3 the
lowest-risk design is to resolve the token through `ListWindows` and match
`Window.WindowID`, because that single call yields identity **and** staleness
(no matching row) with no new driver surface and no misuse of the `StateKey`
vocabulary. The stale-token branch of D4's failure message keys off exactly that
absent row. If a direct `ReadWindowState`-based probe is preferred instead, note
that it cannot distinguish a stale token from an unowned window, and both read
as `""`.

---

## Q3 — does `@cmdman_scale` agree across a project's dashboard windows?

Setup, mirroring `e2e/cmdman/mux_cycle_scale_test.go:17-30` (scale-3 `web`, one
unpinned leaf), with throwaway data/runtime dirs and `TMUX_TMPDIR` isolation:

```yaml
name: pmspike
commands:
  web:
    args: [sleep, "300"]
    scale: 3
mux:
  driver:
    name: tmux
  layouts:
    - name: main
      root:
        command: web
```

```console
$ cmdman compose --workdir … -f … up      # web-1..3 running
$ cmdman compose … mux -s sessA
$ cmdman compose … mux -s sessB
$ tmux list-windows -a -F '#{session_name}:#{window_name} id=#{window_id} identity=#{@cmdman_window} scale=#{@cmdman_scale}'
sessA:zsh id=@0 identity= scale=
sessA:cmdman-pmspike id=@1 identity=66a73ab3d3f0-pmspike scale=
sessB:tmux id=@2 identity= scale=
sessB:cmdman-pmspike id=@3 identity=66a73ab3d3f0-pmspike scale=
```

Two dashboard windows for one project, same identity — produced legitimately
with `-s`, no hand-stamping.

### Server-wide cycle-scale: the windows agree

```console
$ cmdman compose … mux cycle-scale web
sessA:cmdman-pmspike web -> web-2
sessB:cmdman-pmspike web -> web-2
(exit=0)
$ tmux list-windows -a -F '#{window_id} … scale=#{@cmdman_scale}'
@1 sessA identity=66a73ab3d3f0-pmspike scale=web=2
@3 sessB identity=66a73ab3d3f0-pmspike scale=web=2
$ # pane titles: @1: web-2   @3: web-2
$ cmdman compose … mux ls
SESSION   WINDOW           ID   IDENTITY               FRAME   LAYOUT   SCALE
sessA     cmdman-pmspike   @1   66a73ab3d3f0-pmspike   -       0        web=2/3
sessB     cmdman-pmspike   @3   66a73ab3d3f0-pmspike   -       0        web=2/3
```

This is `CycleScale` doing what `cmdman/mux/cycle_scale.go:94-127` describes:
enumerate every window with the identity, then per window respawn the leaf pane
and write the position (`writeScalePosition`, cycle_scale.go:245-252, 263-288).
It held with a frame on one of the two windows as well:

```console
$ cmdman compose … mux cycle-scale web       # @1 framed, @3 not
sessA:cmdman-pmspike web -> web-3
sessB:cmdman-pmspike web -> web-3
@1 … frame=spike scale=web=3      panes: web-3 frame-0
@3 … frame=      scale=web=3      panes: web-3
```

### They disagree after a session-narrowed cycle-scale

```console
$ cmdman compose … mux -s sessA cycle-scale web
sessA:cmdman-pmspike web -> web-3
(exit=0)
@1 sessA identity=66a73ab3d3f0-pmspike scale=web=3
@3 sessB identity=66a73ab3d3f0-pmspike scale=web=2
# pane titles: @1: web-3   @3: web-2
$ cmdman compose … mux ls
sessA     cmdman-pmspike   @1   …   0   web=3/3
sessB     cmdman-pmspike   @3   …   0   web=2/3
```

`-s` narrows `ListWindows` (cycle_scale.go:94-98), so the other window is never
visited. The states then genuinely disagree, and the **merged** read the PLAN's
`compose.Service.MuxScaleState` would be built on silently picks one:

```console
$ # via mux.ReadScaleState (cmdman/mux/cycle_scale.go:294-325)
ReadScaleState(identity="66a73ab3d3f0-pmspike", session="")      = map[web:2], err=<nil>
ReadScaleState(identity="66a73ab3d3f0-pmspike", session="sessA") = map[web:3], err=<nil>
ReadScaleState(identity="66a73ab3d3f0-pmspike", session="sessB") = map[web:2], err=<nil>
```

`web:2` is last-row-wins (`maps.Copy` over the rows, cycle_scale.go:317-323) and
contradicts what `sessA`'s window is actually showing. `MuxUp` already consumes
this merged value to seed a rebuild (`cmdman/compose/mux.go:70-77`), so the
behavior is pre-existing, not something the widget introduces.

### An identity-stamped window with no layout marker is skipped, not written

Manufactured (`new-window` + `set-option -w @cmdman_window <identity>`), because
`compose mux` has no path that produces one:

```console
$ cmdman compose … mux cycle-scale web
sessA:cmdman-pmspike web -> web-2
sessB:cmdman-pmspike web -> web-2
sessB:bogus web ->  (not visible in layout "")
error: mux: window bogus (@4 in session sessB): marker -1 out of range [0,1)
(exit=1)
@1 … scale=web=2
@3 … scale=web=2
@4 sessB:bogus identity=66a73ab3d3f0-pmspike scale=
```

The marker guard at `cmdman/mux/cycle_scale.go:145-151` rejects it before any
write, so the real dashboards stay consistent while the command exits non-zero.
Its empty state contributes nothing to the merge (`ReadScaleState` still
returned `map[web:2]`) — only a *non-empty diverging* state corrupts the merged
answer.

### Q3 conclusion

After a normal (un-narrowed) `cmdman compose mux cycle-scale`, `@cmdman_scale`
**does** agree across all of a project's dashboard windows, including a framed
one — the consistency `ServiceScaleInfo.Shown` is described as relying on
(PLAN.md:235-238).

Two caveats step 4 should carry:

1. Agreement is a property of *how cycle-scale was invoked*, not an invariant.
   `-s <session>` leaves the other windows behind, and after that the merged
   `ReadScaleState` reports the last row's value with no signal that the windows
   disagree. `Shown` will therefore be wrong for at least one window whenever a
   user has run a session-narrowed cycle-scale. Either accept it (documenting
   `Shown` as "the position of the last enumerated window"), or have
   `MuxScaleState` report disagreement so the widget can render `Shown` as
   unknown.
2. A stray identity-stamped window without a valid marker makes `cycle-scale`
   exit non-zero even though every real dashboard succeeded (`errors.Join` over
   per-window errors, cycle_scale.go:113-126). `CycleScale` in the Backend will
   surface that as a failed action; the widget's error line should not imply the
   cycle did not happen.

---

## Cleanup

Both scratch tmux servers were killed, the pty-attach helpers detached, and the
spike project torn down with `cmdman compose … down` (which stops the detached
monitors — killing the tmux server alone does not). Nothing was left running.

## Things this spike did NOT run

- **A zellij leg.** There is no zellij driver — autodetect resolves
  `$TMUX > $ZELLIJ > tmux` and errors on the zellij branch
  (`cmdman/mux/run.go:97-103`, :308) — so every observation here is tmux-only
  and D1's "keeps erroring at that one seam" was not exercised.
- **The frame component process itself.** Q1(c) measured a sibling pane of the
  framed cmdman-owned window rather than instrumenting the statusbar component,
  because that pane runs cmdman's own binary. The mechanism is identical — frame
  panes are `split-window` panes of the same window (`pkg/muxctl/tmux/frame.go:412`)
  — but the component's own view of `CurrentWindowID` was inferred, not observed.
- **`--mux-token` itself.** The flag does not exist yet (step 2); the token path
  was exercised by feeding the same value straight into the driver calls the flag
  will feed.
