# third_party

Vendored dependencies carrying local patches, wired in via `replace`
directives in the root `go.mod`.

## charmbracelet-x-ansi

`github.com/charmbracelet/x/ansi` **v0.11.7** plus the fix from upstream
PR [charmbracelet/x#946](https://github.com/charmbracelet/x/pull/946)
(for issue [#848](https://github.com/charmbracelet/x/issues/848)): the
parser treated `0x9C` — an 8-bit C1 String Terminator that is also a
valid UTF-8 continuation byte — as ST inside OSC/DCS/SOS/PM/APC string
data, cutting titles like `✳ done` (U+2733 = `E2 9C B3`) mid-rune. The
monitor latched the invalid fragment, which broke proto marshaling of
every runtime-state response.

Drop this vendored copy (delete the directory and the `replace`
directive, then bump the dep) once a release containing the fix is
published upstream.
