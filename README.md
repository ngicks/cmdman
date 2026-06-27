# cmdman

A simple shell command daemonizor written in Go which runs blocking commands in background and let you control them through CLI and TUI.

## TUI

`cmdman tui` opens an interactive terminal UI with three tabs:

- **Commands** — list, inspect, and control supervised commands with a live preview.
- **Compose** — browse compose projects; view a project's definition (`enter`), edit its file (`e`), or run compose up (`a`).
- **Layout** — list the current project's mux layouts and switch between them (`enter`).

Choose the startup tab with `--tab=commands|compose|layout` (default `commands`). Run the TUI inside a tmux popup with `--popup`, sized and positioned via `--popup-width`, `--popup-height`, `--popup-x`, and `--popup-y` (explicit percentages, e.g. `80%`).

## Architecture

### Basic

![](/image/architecture.webp)
