# cmdman-version(1)

## Name

`cmdman version` - print build version information

## Synopsis

```text
cmdman version
cmdman --version
```

## Description

Prints cmdman's version and, when embedded by the Go build, the source commit,
dirty-worktree marker, commit time, and Go toolchain version. Development builds
may omit some fields.

## Options

No command-specific options. The root `cmdman --version` flag is an alias for
this command.

## See Also

[cmdman(1)](./cmdman.1.md)
