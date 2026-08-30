# STATUS — fix_launcher_down_restart

State: done 2026-08-28.

## Checklist

- [x] Step 1 — D1: `Target DownTarget` added to `MuxDownMsg`/`ComposeDownMsg`,
      populated by `MuxDownCmd`/`ComposeDownCmd`
- [x] Step 2 — D1: launcher resets `Running`/`starting` on successful
      `ComposeDownMsg`/`MuxDownMsg`; `s` after `D`/`d` starts again
- [x] Step 3 — D5 "stamps a window it creates … kills stamped windows … still
      restores unstamped ones" + D6 "KillCreated is set by
      serviceBackend.MuxDown": `@cmdman_created` stamp in `Server.New`,
      `WindowRow.Created`, `DownOptions.KillCreated`,
      `MuxDownOption.KillCreated`; CLI `compose mux down` unchanged
- [x] Step 4 — D2 (direct `mux.Down` by identity, mux-less D9 window
      included) + D3 (TUI-only) + D4 (only on fully successful down) + D6
      (`KillCreated: true`): `serviceBackend.ComposeDown` removes windows
      behind a testable seam
- [x] Step 5 — e2e: widget up → `D`y → window gone → `s` restarts; `d` kills
      launched window (no shell, no frame) with commands still running;
      reuse-takeover window restored not killed; mux-less D9 case

## Next action

Done — all 5 steps implemented; review gate passed after one fix
(restore-not-kill for the self-hosting window); full suite green modulo
the pre-existing TestStatus_WithoutRunningMonitor failure.
