# cmdman-compose-mux(1)

## Name

`cmdman compose mux` - open or cycle a tmux dashboard for a compose project

## Synopsis

```text
cmdman compose [selection flags] mux [--session NAME] [LAYOUT]
cmdman compose [selection flags] mux --detach [--session NAME]
```

## Description

Reads the selected compose file's top-level `mux:` section. Each leaf names a
compose service and resolves to the stored cmdman command for the selected
project. Default leaves run sticky attach viewers; `mode: logs` leaves run
sticky log viewers. Mux never moves command ownership into tmux, so closing the
window does not stop the services.

The embedded `mux:` section uses the same layout format documented in
[cmdman-mux(5)](./cmdman-mux.5.md), except that `command` values name compose
services in the selected project.

Without `LAYOUT`, successive invocations cycle through declared layouts. A
layout can be selected by name or zero-based index; names take precedence over
numeric interpretation. The selected index becomes the next cycle position.

Inside tmux, the current session is selected by default. Outside tmux, the
session defaults to `cmdman` and an attach hint is printed. The owned window is
named `cmdman-PROJECT`.

When no compose file is explicitly selected, mux selects a compose associated
with the current directory that declares a mux section. Ambiguous selection is
an error. Unlike other compose commands, a missing mux section is always an
error.

Detaching collapses the owned window to one clean shell pane and clears cmdman
tmux options. Services and their monitors remain running.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `-s, --session NAME`: target tmux session. Defaults to the current tmux
  session when inside tmux, otherwise `cmdman`.
- `--detach`: tear down the owned dashboard window instead of applying a
  layout.

## Compose Mux Example

```yaml
mux:
  driver: tmux
  layouts:
    - name: dev
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: api
            focus: true
          - command: worker
            mode: logs
            cmd_opt: {title: worker-log}
```

## See Also

[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose(5)](./cmdman-compose.5.md), [cmdman-compose-attach(1)](./cmdman-compose-attach.1.md)
