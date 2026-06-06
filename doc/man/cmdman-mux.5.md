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

`cmdman mux` and `cmdman compose mux` apply one layout to one cmdman-owned
window. Re-running the command cycles layouts unless a layout name or index is
selected.

## Pane Tree

A pane is either a container or a leaf.

A container has:

- `dir`: split direction, `h` for panes side by side or `v` for panes stacked.
- `splits`: size list, one item per child pane.
- `panes`: child pane list.

A leaf has:

- `command`: cmdman command ID/name, or compose service name under
  `cmdman compose mux`.
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
