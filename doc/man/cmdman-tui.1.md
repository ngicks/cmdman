# cmdman-tui(1)

## Name

`cmdman tui` - interactively inspect and control compose-managed commands

## Synopsis

```text
cmdman tui
cmdman tui --popup[=tmux]
cmdman tui --workdir DIR
cmdman tui widget switcher|launcher [--workdir DIR] [--no-quit]
cmdman tui widget project-manager [--mux-token TOKEN] [-f FILE] [-p NAME]
```

## Description

Starts the terminal UI over the same data and runtime directories used by the
CLI. The TUI focuses on compose projects and their managed commands, providing
project navigation, command actions, previews, and mux layout cycling.

Rows are live: every command with a running monitor is subscribed to, so the
title it sets, the status it reports and the bell it rings reach the screen as
they happen, while commands appearing and disappearing follow cmdman's event
log.

The TUI can also be launched in a multiplexer popup. Driver inference uses the
current environment; v1 implements tmux only. The popup launcher and child
communicate over an internal IPC endpoint.

## Subcommands

`cmdman tui` with no subcommand is unchanged: it opens the full dashboard
described above.

### widget

Run one TUI widget on its own — a single view filling the terminal, with no tab
bar and no tab switching. A frame definition referencing a built-in component
resolves to exactly this invocation, so a widget is also debuggable by hand: run
it in any terminal or pane. Each widget is its own subcommand.

- `switcher`: every known compose project — running, exited, and never run —
  each heading a group with its commands listed under it. A group heads with the
  directory it sits in, written `~/…` where that is under your home and cut
  keeping the tail; projects sharing a directory add their name in parentheses to
  tell them apart, and a project that has never run anywhere — so has no
  directory to name — heads with its own name instead. A command that is one
  replica among several carries a `[i]` badge after its state — replicas of one
  command share its name, so the index is what tells them apart. `j`/`k` (or the
  arrow keys) move the selection; `enter` and a left mouse click take the client
  to the selected project's window and mark that project's bells read — a
  project with no window up yet gets one opened at its directory and lands in
  that. `m` opens the project-manager panel over the selected project in a
  floating pane, and where there is no floating pane to open — a plain terminal,
  or a multiplexer with no popup support — says so on the hint line; `q` quits.
  Landing in a window is all a selection does: it never brings the project up,
  and starting, stopping and removing one command at a time stay in the full
  dashboard. A project with no directory or name to address a window by — one
  that has never run anywhere in particular — is reported on the hint line
  instead. The two teardowns act on the whole project: `d` tears its dashboard
  windows down and leaves its commands running — the dashboard is only a viewer
  of them — and a project whose compose file declares no `mux:` section says so
  on the hint line. `D` stops and removes the commands themselves: it asks
  `compose down <project>? y/n` on the hint line first, `y` goes ahead and any
  other key takes the question back, and what the teardown did — `stopped N,
  removed M` — is reported there when it ends.
- `launcher`: quick-launch selector. The left pane lists target locations: with
  the input empty, the directories you have brought projects up in, most recent
  first, then the directories that the projects named under cmdman's config
  `compose/` directory declare with `work_dir:`, sorted by the name each row
  shows — one never launched from has no recency to be placed by. Typing widens
  that to everything the filter reaches. The right pane lists the compose
  projects at the location under the cursor, toggled on or off: one brought up
  before arrives on, one known only from the config `compose/` directory arrives
  off until `space` turns it on. A project named there that declares no
  `work_dir:` belongs to no directory of its own, so it is offered at every
  location — select or type the directory you want it in and it starts there,
  which is how the editor-and-shell project you keep around comes up in a
  directory it has never run in. Where you have already brought it up, the
  location's own row for it stands: the one that opens enabled. Type to filter,
  tab completes what is typed, enter steps input → locations → projects, esc
  walks back and then dismisses. On a list, `s` starts the enabled projects — so
  a config-only row waits for its `space` — and `S` launches and lands in one;
  `d` and `D` tear the project `S` would launch back down — `d` its dashboard
  windows, leaving the commands running, `D` the commands themselves after the
  `compose down <project>? y/n` confirm described under the switcher. In the
  input every key is text, so ctrl+c is the dismissal that works from anywhere
  (unless `--no-quit` took the quit keys away).
- `project-manager`: shortcuts over one project — the replica count of each of
  its services, which replica a scaled command's dashboard pane shows, and the
  project's mux layouts. Every action wraps the command that already does it, so
  nothing here is reachable only from the panel. Services are listed above,
  layouts below, and `tab` moves the keyboard between the two; `j`/`k` (or the
  up and down arrows) move in the focused list. On services, `+`/`=` and `-` set
  the replica count one step either way, and `l`/`right` and `h`/`left` show the
  next and the previous replica of a cycle target — the rows marked `↻`. Naming
  the previous replica needs the shown one to be known: where a project's
  dashboard windows disagree about it, or none is showing it, the badge reads
  `[?]` and only `l` applies. On layouts, `enter` applies the one under the
  cursor and `c` cycles to the next, with the running dashboard's own layout
  marked. `d` and `D` tear the project down — `d` its dashboard windows,
  leaving the commands running, `D` the commands themselves after the
  `compose down <project>? y/n` confirm described under the switcher — and both
  are the whole project's, so neither list has to have the keyboard for them.
  `r` reloads what the panel shows; `q` quits. The project it manages is
  the one it detects — the window `--mux-token` names, else the window it runs
  in, else the project of the working directory — and `--file` and
  `--project-name` name one outright, ahead of all three; the project it lands
  on must declare a `mux:` section. The switcher's `m` summons this panel over
  the project under its own cursor.

A widget fills its window, so popup framing belongs to the multiplexer:

```sh
bind-key -n M-Space display-popup -E -w 80% -h 60% 'cmdman tui widget launcher'
```

The project-manager takes the window it was summoned from, which the binding
has to hand it. tmux does not expand formats in a `display-popup`
shell-command, so the binding goes through `run-shell`, which is expanded:

```sh
bind-key -n M-p run-shell 'tmux display-popup -E -w 80% -h 60% \
  "cmdman tui widget project-manager --mux-token #{window_id}"'
```

The launcher's input line is a path field as much as a filter. A leading `~` or
`$HOME` is expanded for matching and completion — on a path-component boundary,
so `~work` names something else entirely — while the input keeps the spelling
that was typed. For input shaped like a path (`/`, `~`, `$HOME`, `./`, `../`),
tab completes over the locations and the on-disk directories that extend what is
typed, and a completion that can only be the one directory gains the trailing
separator, so the next tab reaches inside it. A bare word stays a fuzzy query
over the listing — branch, repo, path, project name — where tab extends to the
common prefix of the locations it matches, with no filesystem read and no
suggestion list.

What tab cannot decide it shows: two or more candidates leave a suggestion list
under the input line. On it, tab and shift+tab cycle candidates into the input
one at a time, enter accepts the candidate it has put there and leaves the zone
step to the next enter, esc drops the list and puts back the text the cycling
started from, and typing drops the list but keeps what was inserted. The list is
the input zone's, so leaving the zone takes it down too.

A path typed to an existing directory that is none of the known locations
becomes a selectable row of its own in the left pane, carrying whatever compose
projects are there and the config projects that declare no `work_dir:` — so a
directory nothing has ever run in is still somewhere to start one.

## Options

### tui

- `--popup[=tmux|zellij]`: run the TUI in a multiplexer popup. A bare
  `--popup` infers the driver from the environment; v1 supports tmux.
- `-w, --workdir DIR`: override the effective work directory used to discover
  the cwd-active compose project. Without it the process working directory is
  used.

### widget

- `-w, --workdir DIR`: as above. Every widget subcommand accepts it.
- `--no-quit`: unbind the quit keys, so no keypress ends the widget. A widget
  invoked as a frame component always runs with it: a frame pane whose widget
  exited would stand empty until the frame is taken down and put back up.

### widget project-manager

On top of the two above:

- `--mux-token TOKEN`: the multiplexer window to take the project from, spelled
  the way the driver spells it — a tmux window id such as `@7`, which a
  keybinding passes as `#{window_id}`. Pane ids are not resolved. This is the
  highest-priority detection probe; a token naming no cmdman-owned window is not
  an error, detection simply falls through to the probes under it.
- `-f, --file PATH`: compose file of the project to manage, resolved the way
  [cmdman-compose(1)](./cmdman-compose.1.md) resolves `-f`.
- `-p, --project-name NAME`: project name to manage. Given with `--file` it
  overrides that file's top-level `name:`, as `cmdman compose`'s `-p` does.
  Given on its own it names the project's file instead, resolved the way `-f`
  resolves a bare name, and the file's own `name:` stands — the two spellings
  would otherwise resolve one file into two differently named projects, only one
  of which owns the stored commands.

Either of `--file` and `--project-name` names the project outright, ahead of
every detection probe — that is how the switcher's `m` targets the row under its
cursor rather than the window its popup opens over. `--workdir` says where that
project stands: it is part of the explicit target, not only of the working
directory the cwd match runs against. A project is its work directory and its
name together, and that pair is what its commands, its replica counts and its
dashboard window are recorded under — so a `--file` given without it loads the
named file against the panel's own directory and manages a project that is not
the one on disk. The switcher's `m` passes the row's own directory for exactly
that reason.

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose-mux(1)](./cmdman-compose-mux.1.md),
[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-frame(5)](./cmdman-frame.5.md)
