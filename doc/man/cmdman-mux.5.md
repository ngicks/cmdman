# cmdman-mux(5)

## Name

`cmdman mux` file - define tmux dashboards for managed commands

## Format

A standalone mux file is a YAML document with a top-level `mux:` key:

```yaml
mux:
  driver: tmux
  driver_opt:
    socket: cmdman
  layouts:
    - name: main
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: api
            focus: true
          - command: worker
            mode: logs
            cmd_opt:
              title: worker log
```

In a compose file, the same spec body is embedded as the top-level `mux:` field.
Leaves name compose services instead of global cmdman IDs or names.

## Top-Level Fields

- `driver`: multiplexer backend. `tmux` is the v1 driver.
- `driver_opt`: driver-specific options.
- `layouts`: list of named layouts.

Unknown fields are captured by the parser and may be reported as warnings by
callers.

## Tmux Driver Options

For `driver: tmux`, `driver_opt` recognizes:

- `path`: tmux binary path. Defaults to `tmux`.
- `socket`: tmux socket name passed as `tmux -L SOCKET`.

An empty socket uses the current tmux server when invoked inside tmux and the
default tmux socket otherwise.

## Layouts

Each layout has:

- `name`: required unique layout name.
- `root`: pane tree root.

`cmdman mux [up]` and `cmdman compose mux [up]` apply one layout to one
cmdman-owned window. Re-running the command cycles layouts unless a layout name
or index is selected. Each spec layout maps to exactly one applied layout; there
is no per-replica layout expansion.

## Pane Tree

A pane is either a container or a leaf.

A container has:

- `dir`: split direction, `h` for panes side by side or `v` for panes stacked.
- `splits`: size list, one item per child pane.
- `panes`: child pane list.

A leaf has:

- `command`: cmdman command ID/name, or compose service name under
  `cmdman compose mux`.
- `scale`: replica pin or cycle-scale behaviour (see below).
- `mode`: `attach` or `logs`. Empty means `attach`.
- `cmd_opt`: per-pane driver options.
- `focus`: request initial focus for this pane.

A leaf can also be written as a scalar shorthand:

```yaml
panes:
  - api
  - command: worker
    mode: logs
```

The shorthand is equivalent to `{command: api}`.

## Replica Pinning and Cycle-Scale (`scale:`)

`scale:` is a compose-context field that controls which replica of a scaled
command a leaf displays.

- **Absent or zero** (`scale: 0`, the default): the leaf is a *cycle-scale
  target*. Its displayed replica is controlled by
  `cmdman compose mux cycle-scale` and persists in the dashboard window's
  `@cmdman_scale` option across layout switches. It defaults to replica 1 when
  no position has been stored yet.
- **Positive integer** (`scale: N`): the leaf is *pinned* to replica N. It
  always resolves to `<command>-N` and is never advanced by `cycle-scale`.

Layout cycling (`mux up`) and replica cycling (`cycle-scale`) are independent
operations. Applying a new layout does not reset stored replica positions;
`mux down` does reset them.

In a compose file, `scale: N` in a `mux:` leaf must not exceed the `scale:` of
the same command in `commands:` (see [cmdman-compose(5)](./cmdman-compose.5.md)
for the static validation rule). This constraint is compose-specific; standalone
`cmdman mux` ignores it.

For standalone `cmdman mux`, a positive `scale:` resolves the suffixed command
name `<command>-<N>` (e.g. `scale: 2` on leaf `api` resolves `api-2`). An
absent `scale:` resolves the command name without a suffix.

## Split Sizes

Each `splits` item is one of:

- `N`: proportional weight.
- `Nc`: absolute size in character cells.
- `N%`: percentage of the parent dimension.

`N` must be a positive integer. Percent values must be in `1..100`.
Whitespace and floating point values are not accepted.

`splits` must have the same length as `panes`.

## Validation

The mux spec enforces:

- each layout has a non-empty, unique name;
- every pane is exactly one of container or leaf;
- container direction is `h` or `v`;
- containers have at least one child;
- `splits` and `panes` lengths match;
- leaf names are unique within one layout;
- at most one pane has `focus: true` per layout;
- a resolved command may appear only once within one layout.

For user-facing cmdman specs, the leaf name is the `command` value. Standalone
`cmdman mux` resolves it as a cmdman ID or name. `cmdman compose mux` resolves
it as a service in the selected compose project.

In the compose context, additional static validation is applied at file load
time:

- every leaf `command` must name a declared command in `commands:`;
- a pinned `scale: N` must not exceed the command's own `scale:` in
  `commands:`.

## Pane Modes

`mode: attach` runs sticky `cmdman attach` in the pane.

`mode: logs` runs `cmdman logs --sticky` in the pane.

Dashboard panes are viewers only. Closing or recreating the tmux window does
not stop the supervised commands.

## Per-Pane Tmux Options

For the tmux driver, `cmd_opt` recognizes:

- `title`: pane-border title. Defaults to the leaf command name.

Other `cmd_opt` keys are ignored by the tmux driver.

## See Also

[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-compose-mux(1)](./cmdman-compose-mux.1.md),
[cmdman-compose(5)](./cmdman-compose.5.md)
