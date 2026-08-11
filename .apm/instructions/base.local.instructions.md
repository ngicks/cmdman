---
description: "Basic instructions for the project"
applyTo: "*"
---

### General

A simple shell command daemonizor written in Go which runs blocking commands in background and let you control them through CLI and TUI.

### Tech stack

- Go
  - `github.com/spf13/cobra` for subcommands
  - Using gRPC and protobuf for communications

### Structure overview

```
.
├── api                 IPC / RPC definition
│   ├── gen             generated code by `buf generate`
│   └── schema          `buf generate` target
├── cmd                 Entry point. cobra subcommand structure.
│   └── cmdman
├── cmdman              cmdman usecase code
│   ├── cli             cli presentation layer
│   ├── compose         compose functionality
│   ├── config          configuration model, layering and loading
│   ├── eventlog        event log functionality
│   ├── frame           frame definitions: components docked to screen edges
│   ├── internal
│   ├── logdriver       log reader / writer
│   ├── model           domain models
│   ├── monitor         per-command monitor process
│   ├── mux             cmdman's YAML layer on top of pkg/muxctl
│   ├── store           SQLite config / state store
│   └── tui             bubbletea TUI
├── doc
│   ├── man             man pages written in markdown
│   └── plan            old plan files. You may not read this
├── e2e
│   └── cmdman
├── internal
│   ├── cmdsignals      signals that cancel top-level CLI execution
│   ├── libver          release-controlled version constant
│   ├── loggerfactory   internal helper
│   ├── templateutil    text/template helpers shared by --format renderers
│   └── versioninfo
└── pkg
    ├── hrstr           human readable string parser / maybe writer
    ├── muxctl          terminal multiplexer driver
    └── stdcopy         copy cmdman logs to io.Writer
```

### Implementing functionality

- Implement e2e tests if any existing test is not covering the case.
- Don't think too much about backward compatibility, since the app was never actually deployed
