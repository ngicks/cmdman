# cmdman-frame(5)

## Name

`cmdman mux frame` file - dock components around the edges of a window

## Format

A frame definition is a YAML document with a top-level `frame:` key wrapping an
ordered array of entries:

```yaml
frame:
  - edge: left
    size: 20%
    component: switcher
  - edge: bottom
    size: 2
    component: statusbar
  - edge: right
    size: 30%
    command: ["tail", "-f", "/var/log/app.log"]
    managed: true
```

Each entry docks one pane to a screen edge. What is left over once every entry
has taken its slice is the region the window already holds: the dashboard
`cmdman mux up` built, or whatever else is on the window.

Definition files are read by `cmdman mux frame show` and `cmdman mux frame
cycle`, and by `cmdman mux up` and `cmdman compose mux up` when the
`default_frame` config key names one. `cmdman mux frame hide` reads no
definition, and `cmdman mux frame ls` lists definition names without reading
them. See [cmdman-mux(1)](./cmdman-mux.1.md) for the verbs.

Unknown fields are ignored with a warning.

## File Location

Named definitions live in the `frame` subdirectory of the cmdman configuration
directory: `<config-dir>/frame/<name>.yaml` or `<name>.yml`. `<config-dir>` is
the directory holding the config file — the directory of `$CMDMAN_CONF`, or
`<user config dir>/cmdman` by default. The `--config` flag is not consulted
here.

A definition is always named, by argument or by the `default_frame` config key.
There is no discovery from the working directory the way `cmdman compose` finds
`cmd-compose.yaml`. A reference `DEF` resolves in this order:

1. as a filesystem path relative to the working directory (an absolute path is
   used as-is); a regular file there wins;
2. as a bare name — one holding no `/` or `\`, and neither `.` nor `..` — under
   the frame directory, trying `<name>` when it already ends in `.yaml` or
   `.yml`, then `<name>.yaml`, then `<name>.yml`;
3. otherwise the path form of step 1 is read, so the error names the path that
   was tried.

Step 1 runs first, so a file named `dev` in the working directory takes
precedence over the `dev` definition in the frame directory.

`cmdman mux frame ls` lists one name per `<name>.yaml` or `<name>.yml` file,
sorted and deduplicated. That same list is the rotation `cmdman mux frame cycle`
walks, and the candidate list reported when a bare name resolves to no file.

## Entry Fields

- `edge`: the screen edge the entry docks to, one of `top`, `bottom`, `left`,
  `right`. Required.
- `size`: the entry's extent along that edge. Required; see
  [Sizes](#sizes).
- `component`: name of a built-in cmdman widget; see
  [Components](#components).
- `command`: argv run in the entry's pane.
- `managed`: run `command` under cmdman supervision instead of in the pane; see
  [Managed Entries](#managed-entries). `command:` entries only.
- `hooks`: hook configuration for a managed entry's command; see
  [Hooks](#hooks).

Exactly one of `component:` and `command:` is required per entry.

Unknown fields, at the top level and inside an entry, are reported as one
warning per stray key and otherwise ignored.

## Carving Order

Entries are carved in file order, and that order is the nesting: an entry
divides the rectangle the entries before it left over, not the whole window.

`[left 20%, bottom 2]` gives a side column running the full window height and a
status bar spanning only the width that remains beside it. The same two entries
written as `[bottom 2, left 20%]` give a status bar running the full window
width and a side column shortened by it.

## Sizes

`size` is one of:

- `N`: absolute size in character cells.
- `Nc`: absolute size in character cells.
- `N%`: percentage of the rectangle remaining at that entry's turn.

`N` must be a positive integer. Percent values must be in `1..100`. Whitespace
and floating point values are not accepted.

A bare `N` is character cells here, unlike a `splits:` item in
[cmdman-mux(5)](./cmdman-mux.5.md), where it is a proportional weight: a frame
entry has no sibling to share a ratio with.

Percentages follow the carving order. Two `left: 50%` entries on a window 200
cells wide give a 100-cell column and then a column about half as wide again —
half of what the first entry left, not half of the window.

## Components

`component:` names a built-in widget:

- `switcher`: the project switcher.
- `statusbar`: the one-line status bar.

A component entry runs `cmdman tui widget <name> --no-quit` in its pane, using
the same cmdman binary that docked the frame. The widgets and their key
bindings are described in [cmdman-tui(1)](./cmdman-tui.1.md). `--no-quit` is
always applied, so no keypress can leave a frame pane empty.

Any other `component:` value is an error. A widget that is not built in is
docked as a `command:` entry naming its own argv, for example
`["cmdman", "tui", "widget", "launcher", "--no-quit"]`.

`managed:` is not accepted on a component entry.

## Command Entries

`command:` is an argv array, run in the entry's pane as written.

By default the entry is ephemeral: its process belongs to the pane, goes away
when the frame is taken down, and is run again by the next `frame show`.

## Managed Entries

`managed: true` opts a `command:` entry into cmdman supervision. The argv is
then not run in the pane: it runs as a supervised cmdman command named
`frame-<def>-<i>` — the definition name plus the entry's index in the array —
and the pane attaches to that command.

- On `frame show`, the command is created and started when no command of that
  name exists. One that is already running or starting is adopted rather than
  started a second time; one that is created, exited, or failed is started
  again under the same name.
- The name is the whole identity. Editing an entry's `command:` does not
  replace the process already running under that name; stop
  `frame-<def>-<i>` by hand to pick the new argv up.
- The command survives `frame hide` and frame replacement, keeps running under
  its own monitor, and stays visible in `cmdman ls`.
- It runs behind a PTY. Everything else is left at the cmdman service defaults,
  the working directory included: the directory the verb ran in is not the
  entry's.

## Hooks

`hooks:` on a managed entry becomes the supervised command's own hook
configuration, which takes precedence over the `default_hooks` config key. It
is a map keyed by event, in the same shape as `default_hooks`:

```yaml
frame:
  - edge: right
    size: 30%
    command: ["tail", "-f", "/var/log/app.log"]
    managed: true
    hooks:
      bell:
        action: block
        exec: ["notify-send", "app is asking for attention"]
```

Events are `bell` (BEL), `title` (a window-title sequence), and `notification`
(a desktop-notification sequence). Each event takes:

- `action`: `passthrough`, the default, leaves the sequence in the stream an
  attached viewer sees; `block` swallows it.
- `exec`: argv run fire-and-forget when the event fires, with the event data in
  `CMDMAN_HOOK_*` environment variables.

An unknown event or action name is an error naming the definition file and the
entry. `hooks:` on an entry that is not `managed: true` has no supervised
command to apply to and is ignored with a warning.

## Validation

A definition is rejected when:

- it carries no `frame:` array, or the array is empty;
- an entry has no `edge`, or an edge other than `top`, `bottom`, `left`,
  `right`;
- an entry has no `size`, or a size outside the grammar above;
- an entry sets both `component:` and `command:`, or neither;
- an entry names a `component:` that is not built in;
- a `component:` entry sets `managed:`;
- a managed entry's `hooks:` names an unknown event or action.

Errors name the definition file and the index of the entry at fault. Unknown
keys, and a `hooks:` override with nothing to apply to, warn instead; neither
fails the load.

`cmdman mux frame show` reads and carves the definition before the multiplexer
is touched, so a broken definition never disturbs the frame already standing.
Under `cmdman mux up` and `cmdman compose mux up`, a `default_frame` that is
missing or broken warns and does not fail the up.

## See Also

[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-tui(1)](./cmdman-tui.1.md)
