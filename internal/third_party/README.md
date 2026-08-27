# internal/third_party

Vendored dependencies carrying local patches. The copies are part of this
module (no `go.mod` of their own, no `replace` directives — a `replace`
would make `go install github.com/ngicks/cmdman/cmd/cmdman@<version>`
refuse the module) and are imported by their
`github.com/ngicks/cmdman/internal/third_party/...` paths. Placed under
`internal/` to signal the copies are this module's build detail, not an
API for anyone else. `.golangci.yaml` excludes the directory from lint
and formatting so the copies keep upstream style.

Both copies are from [charmbracelet/x](https://github.com/charmbracelet/x),
MIT licensed; each directory retains the upstream
[`LICENSE`](https://github.com/charmbracelet/x/blob/main/LICENSE)
([charmbracelet-x-ansi/LICENSE](./charmbracelet-x-ansi/LICENSE),
[charmbracelet-x-vt/LICENSE](./charmbracelet-x-vt/LICENSE)) as the MIT
terms require.

## charmbracelet-x-ansi

`github.com/charmbracelet/x/ansi` **v0.11.7** plus the fix from upstream
PR [charmbracelet/x#946](https://github.com/charmbracelet/x/pull/946)
(for issue [#848](https://github.com/charmbracelet/x/issues/848)): the
parser treated `0x9C` — an 8-bit C1 String Terminator that is also a
valid UTF-8 continuation byte — as ST inside OSC/DCS/SOS/PM/APC string
data, cutting titles like `✳ done` (U+2733 = `E2 9C B3`) mid-rune. The
monitor latched the invalid fragment, which broke proto marshaling of
every runtime-state response.

## charmbracelet-x-vt

`github.com/charmbracelet/x/vt` **v0.0.0-20260622092256-25656177ba8e**,
unpatched except for the rewiring below. Vendored because `vt.Emulator`
constructs `ansi.NewParser()` itself: only a copy whose imports point at
the patched `charmbracelet-x-ansi` above gives the monitor's emulator the
fixed parser — the upstream release would resolve the unpatched ansi and
reintroduce the mid-rune OSC cut.

Local deviations from upstream:

- All `github.com/charmbracelet/x/ansi` imports point at the vendored
  copy.
- `csi_sgr.go` and `mouse.go` convert between the vendored and upstream
  ansi types at the `charmbracelet/ultraviolet` boundary
  (`uv.ReadStyle` takes the upstream `ansi.Params`; `uv.MouseButton`
  aliases the upstream `ansi.MouseButton`).

## Unwinding

Once a `charmbracelet/x/ansi` release containing the #946 fix is
published: delete both directories, restore the plain
`github.com/charmbracelet/x/ansi` / `github.com/charmbracelet/x/vt`
imports throughout, bump the deps, and drop the `internal/third_party`
exclusions from `.golangci.yaml`.
