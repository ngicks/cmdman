# cmdman

A simple shell command daemonizor written in Go which runs blocking commands in background and let you control them through CLI and TUI.

## TUI

`cmdman tui` opens an interactive terminal UI with two tabs:

- **Commands** — list, inspect, and control supervised commands with a live preview.
- **Compose** — browse compose projects; view a project's definition (`enter`), edit its file (`e`), run compose up (`a`), or cycle the project's mux layout (`c`).

Listing a project's mux layouts and applying one is `cmdman tui widget project-manager`.

Choose the startup tab with `--tab=commands|compose` (default `commands`). Run the TUI inside a tmux popup with `--popup`, sized and positioned via `--popup-width`, `--popup-height`, `--popup-x`, and `--popup-y` (explicit percentages, e.g. `80%`).

## Architecture

### Basic

![](/image/architecture.webp)
