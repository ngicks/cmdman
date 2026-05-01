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
├── AGENTS.md
├── bin             git-ignore'd bin dir.
├── cmd             Entry point. cobra subcommand structure.
│   └─── cmdman
├── e2e             e2e test
│   └── cmdman
└─── pkg
    ├── api         API definition and generated code / related type definitions. proto schema basically sit here.
    ├── cmdman      cmdman implementation: a simple command daemon. It's like Podman without pods, tmux without terminals.
    └── mux         terminal multiplexer helper implementation.
```

### Implementing functionality

- Implement e2e tests if any existing test is not covering the case.
