# Issue catalog

Derived index of `open/`; regenerate after any fold or close. When it
disagrees with `open/`, `open/` wins. Rebuilt 2026-09-03 after migrating
the legacy `issue.md` entries to per-item files.

| Title | File | Tags |
| --- | --- | --- |
| compose-attach man page omits --scale | `open/compose-attach-man-omits-scale.md` | docs man compose attach |
| compose.GenerateName joins name halves ambiguously | `open/compose-generatename-ambiguous-join.md` | compose naming bug |
| TestComposeMuxCycleScale_NoWindowError flakes with empty output | `open/compose-mux-cycle-scale-nowindow-test-flakes.md` | e2e test flake mux compose |
| frameComponentArgv call site untested with a real window id | `open/frame-component-argv-call-site-untested.md` | mux frame test |
| k8s-file writer can split log entries larger than 4096 bytes | `open/k8sfile-writer-splits-entries-over-4096-bytes.md` | logdriver k8sfile writer concurrency bug |
| Monitor hard-death paths append no exit event | `open/monitor-hard-death-appends-no-exit-event.md` | monitor eventlog lifecycle bug |
| Supervised mux-op worker mechanism is undocumented in doc/man | `open/mux-op-worker-undocumented-in-man.md` | docs man mux compose |
| README does not cover the interaction commands | `open/readme-lacks-interaction-commands.md` | docs readme cli |
| Relative command Dir fabricates an absolute-looking reported cwd | `open/relative-dir-fabricates-absolute-cwd.md` | model validation cwd monitor bug |
| Runtime-stream cwd test does not pin immediate delivery | `open/runtime-stream-cwd-test-timing-unpinned.md` | monitor test cwd |
| StatWindow layout marker breaks on unmarked user-split panes | `open/statwindow-marker-breaks-on-unmarked-user-split-panes.md` | muxctl tmux stat cycle-scale bug |
| Sticky attach's chdir-once guarantee has no behavioral test | `open/sticky-attach-chdir-once-untested.md` | cli attach test |
| `cmdman stop` exits 0 after per-target failures | `open/stop-exits-zero-after-per-target-failures.md` | cli stop exit-status bug |
| TTY monitor wedges in "running" when a grandchild keeps the pty slave open | `open/tty-monitor-wedges-when-grandchild-holds-pty-slave.md` | monitor pty stop tty lifecycle bug |
| Window-level start dirs via multiplexer -c plumbing | `open/window-start-dirs-via-mux-c-plumbing.md` | mux tmux cwd feature |
