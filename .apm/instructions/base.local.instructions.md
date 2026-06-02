---
description: "Basic instructions for the project"
applyTo: "*"
---

### General

A simple shell command daemonizor written in Go which runs blocking commands in background and let you control them through CLI and TUI(not implemented).

### Tech stack

- Go
  - `github.com/spf13/cobra` for subcommands
  - Using gRPC and protobuf for communications

### Structure overview

```
.
├── cmd                Entry point. cobra subcommand structure.
│   └── cmdman
├── doc
│   └── plan           old plan files. You may not read this
├── e2e
│   └── cmdman
├── internal
│   ├── cmd            internal helper entry points
│   │   └── release    release helper
│   ├── loggerfactory  internal helper
│   └── versioninfo
└── pkg
    ├── api            IPC / RPC definition
    │   ├── gen        generated code by `buf generate`
    │   └── schema     `buf generate` target
    ├── cmdman         cmdman usecase code
    │   ├── cli        cli wiring
    │   ├── compose    compose functionality
    │   ├── eventlog   log functionality
    │   ├── internal
    │   ├── logdriver  log reader / writer
    │   ├── model      domain models
    │   └── store      SQLite config / state store
    ├── hrstr          human readable string parser / maybe writer
    ├── mux            terminal multiplexer driver
    └── stdcopy        copy cmdman logs to io.Writer
```

### Implementing functionality

- Implement e2e tests if any existing test is not covering the case.
- Don't think too much about backward compatibility, since the app was never actually deployed
