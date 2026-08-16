# HANDOFF — project-manager widget

Ledger of work leaving this plan. Entries are out-of-scope discoveries or
user-approved deferrals only; recording here does not authorize dropping
in-scope work.

## Explicit Compose-tab selection loses to ambient detection (out-of-scope discovery, 2026-08-16)

In the full TUI, selecting project X on the Compose tab and opening the
Layout tab yields the *enclosing window's* project whenever the user sits in
any cmdman-owned window — `resolveLayoutSelection`
(`cmdman/cli/tui_backend_mux.go`) probes identity ahead of its chain, per
PLAN step 3 / D15. The pre-existing chain already preferred cwd over the
explicit selection, so this plan preserved that philosophy rather than
redesigning it. **Follow-up**: a future plan may rank explicit user
selection above ambient probes across the TUI; needs a UX decision.

## Full TUI Compose-tab Active mark is still cwd-only (out-of-scope discovery, 2026-08-16)

`cmdman/tui/state.go:371` marks the Compose tab's active project by cwd
match only — it is not among D3's enumerated consumers (switcher Active
mark, `resolveLayoutSelection`, project-manager), so step 3 deliberately did
not convert it. **Follow-up**: fold it onto `ActiveIdentity` in a later
change for full D3 consistency.
