# cmdman-mux(1)

## Name

`cmdman mux` - materialize a tmux dashboard for managed commands

## Synopsis

```text
cmdman mux [--session NAME] [PATH|-]
cmdman mux --detach [--session NAME] [PATH]
```

## Description

Reads a YAML document with a top-level `mux:` section and resolves each leaf's
`command` as a cmdman ID or name. A default leaf runs sticky `cmdman attach`;
`mode: logs` runs sticky logs instead. The supervised commands remain owned by
their detached monitors: destroying the tmux window only destroys viewers.

When invoked inside tmux, cmdman targets the current session by default and may
reuse a safe current window. Outside tmux it creates or updates a detached
session named `cmdman` and prints an attach command. The v1 driver is tmux;
`driver_opt.path` and `driver_opt.socket` can select a binary or isolated tmux
server.

Layouts are trees of horizontal (`h`) or vertical (`v`) containers with
weighted `splits` and leaf panes. Standalone mux cycles layouts on successive
applications. Duplicate commands within one layout are rejected.

The mux file format is documented in [cmdman-mux(5)](./cmdman-mux.5.md).

Detaching restores the owned window to one clean shell pane and clears cmdman
tmux options. Supply the same spec path when it selects a custom socket.

## Options

- `-s, --session NAME`: target tmux session. Defaults to the current tmux
  session when inside tmux, otherwise `cmdman`.
- `--detach`: tear down the owned dashboard window instead of applying a
  layout.

## Example

```yaml
mux:
  driver: tmux
  layouts:
    - name: main
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: api
          - command: worker
            mode: logs
```

```sh
cmdman mux dashboard.yaml
cmdman mux --detach dashboard.yaml
```

## See Also

[cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose-mux(1)](./cmdman-compose-mux.1.md), [cmdman-attach(1)](./cmdman-attach.1.md)
