# STATUS — muxctl Server split

**State: DONE, including addendum** (base split D1–D5 and addendum D6–D8 both
implemented 2026-07-24 and verified: full-tree build/test green, reviewer
verdicts approve / approve-with-nits, all nits fixed)

## Addendum checklist (D6–D8)

- [x] 7. muxctl: `ServerConfig`; `Connect(ctx, ServerConfig)`;
      `MuxSpec.Driver DriverSpec` (yaml object) + `ServerConfig()` helper —
      `go build`/`go test ./pkg/muxctl` green. Follow-up in flight: nested
      driver-object unknown-key handling vs package convention.
- [x] 8. tmux: Connect from ServerConfig; path-aware socket (-S path vs
      -L name per D7, three-way switch in exec.go buildArgs); new
      exec_test.go (arg selection) + socket_path_test.go (live -S
      round-trip); tmux testdata fixtures → driver-object form
- [x] 9. cmdman layers: mux.Spec `Driver muxctl.DriverSpec`;
      resolveServer(ctx, DriverSpec, env); option structs carry DriverSpec;
      cli/compose/cmd glue; all YAML fixtures + e2e specs migrated.
      Finding: mux layer has NO stray-key warn sweep (pre-existing) — old
      `driver_opt:` lands in Spec.Unknown captured but unwarned.
- [x] 10. verify: `go build ./...` + `go test ./...` green (incl. tmux and
      e2e suites, no skips); lint clean except the two known pre-existing
      eventlog golines failures; reviewer: approve-with-nits, both nits
      fixed — man pages (doc/man/*.md) migrated to the driver-object form
      (they were hand-written; old scalar examples would now hard-fail),
      and `DriverSpec.ServerConfig()` + `path:` decode gained unit tests.

## Base split (D1–D5): DONE

(verified by full-tree build/test; independent review: approved, no blocking
findings)

## Checklist

- [x] 1. muxctl contract: `Server` interface (incl. `CurrentSessionName`),
      one-method `Driver.Connect`, drop `DriverOpt` from
      `Config`/`ListOptions`
- [x] 2. tmux driver: exported `Server` struct over `*executor`; package
      funcs → methods; `CurrentSessionName` (+ new current_session_test.go);
      adapter `Driver.Connect`; `newExecutorFor` deleted; all tmux tests
      migrated
- [x] 3. mux consumer: `resolveServer` (lookup + Connect); run/down/list/
      cycle_scale rewired to `server.*`; `currentTmuxSession` deleted;
      queryCurrent hook now (string, bool, error)
- [x] 4. mux tests updated. Deviation from plan: no fake Driver/Server was
      needed — the only driver-touching mux test (scale_rmw_test.go) uses
      the real tmux driver; the rest test pure logic. Post-review addition:
      TestResolveSessionName gained the ("", false, nil) not-attached case.
- [x] 5. docs sweep — muxctl/doc.go, tmux/doc.go, mux/doc.go, all
      [muxctl.Server.*] doc links; repo-wide staleness grep clean
      (no newExecutorFor / currentTmuxSession / old package-level funcs /
      Config.DriverOpt / ListOptions.DriverOpt references remain)
- [x] 6. verify: `go build ./...` green; `go test ./...` green (one tmux e2e
      flake, TestComposeMuxCycleScale_DownResetsPosition "server exited
      unexpectedly", passed 3/3 on rerun — sandbox tmux-server contention,
      not a regression); reviewer verdict: approve, zero blocking findings

## Known non-issues / accepted deltas

- `CurrentSessionName` returns ok=false on a successful-but-empty
  `display-message` output, where the old inlined `currentTmuxSession` would
  have returned an empty name as success. Unreachable with a real tmux
  server (session names cannot be empty); accepted as defensive robustness.
- `golangci-lint run` reports two golines format failures in
  `pkg/cmdman/eventlog/{watcher_linux_test.go,writer_test.go}` — verified
  pre-existing at HEAD (files untouched by this refactor); out of scope.

## Next action

None — plan complete. Changes are uncommitted in the working tree.
