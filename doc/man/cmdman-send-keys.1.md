# cmdman-send-keys(1)

## Name

`cmdman send-keys` - write key input to a managed PTY

## Synopsis

```text
cmdman send-keys ID|NAME KEY [KEY...]
cmdman send ID|NAME KEY [KEY...]
```

## Description

Writes input to a running command's PTY without opening an interactive attach
session. The target must have a PTY.

In the default mode, tokens such as `Enter`, `Tab`, `Escape`, `C-c`, and
ordinary text are translated to terminal bytes.

## Options

- `-l, --literal`: send arguments literally, without translating key names.
- `-H, --hex`: treat arguments as hexadecimal byte values.
- `-N, --repeat-count N`: repeat the full key sequence N times. Defaults to 1.

## Examples

```sh
cmdman send-keys repl 'print(1)' Enter
cmdman send-keys server C-c
cmdman send-keys --literal editor ':wq' Enter
cmdman send-keys --hex app 03
```

## See Also

[cmdman-attach(1)](./cmdman-attach.1.md), [cmdman-compose-send-keys(1)](./cmdman-compose-send-keys.1.md)
