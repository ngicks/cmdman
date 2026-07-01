# cmdman-compose-send-keys(1)

## Name

`cmdman compose send-keys` - send input to one or more compose service PTYs

## Synopsis

```text
cmdman compose [selection flags] send-keys [COMMAND...] -- KEY [KEY...]
```

## Description

Sends the same key sequence to selected compose services. The `--` separator is
mandatory: service names precede it and keys follow it. An empty service list
broadcasts to every command in the selected project.

Targets must have live PTYs (`tty: true`). Default key-name translation,
literal byte input, hexadecimal byte input, and sequence repetition behave like
direct `cmdman send-keys`. Outcomes are reported per service so one failure
does not hide results for the other targets.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `-l, --literal`: send arguments literally, without translating key names.
- `-H, --hex`: treat arguments as hexadecimal byte values.
- `-N, --repeat-count N`: repeat the full key sequence N times. Defaults to 1.

## Examples

```sh
cmdman compose send-keys api worker -- C-c
cmdman compose send-keys -- Enter
cmdman compose send-keys --literal repl -- 'exit()' Enter
```

## See Also

[cmdman-send-keys(1)](./cmdman-send-keys.1.md), [cmdman-compose-attach(1)](./cmdman-compose-attach.1.md)
