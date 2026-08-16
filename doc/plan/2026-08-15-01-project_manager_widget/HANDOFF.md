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

## `compose up --mux` collides on window index in a shared session (out-of-scope discovery, 2026-08-16)

Bringing a *second* project up with `--mux` into a session that already
holds one fails: `tmux new-window -d -t <session> …: create window failed:
index 1 in use` — `-t <session>` resolves to the session's current window's
*index*, so the insert collides once the current window is not the last
(find-or-create path under `pkg/muxctl/tmux/`). Found while building the
step-6 summon e2e (it forced the test's project B to be `create`d instead
of brought up). **Follow-up**: fix window creation to append (`-a`) or pick
an explicit free index; deserves its own small plan/commit.

## Pre-existing lint finding in e2e (out-of-scope discovery, 2026-08-16)

`e2e/cmdman/compose_test.go:1595` — `avoid os.IsNotExist … use
errors.Is(err, fs.ErrNotExist)`, reported by the edit-hook linter on every
touch of that package; predates this plan. **Follow-up**: one-line cleanup
commit.

## Explicit target drops `--workdir`, so the summoned panel manages a phantom project (out-of-scope discovery, 2026-08-16)

The explicit compose target (D17) never carries the work directory into the
compose load, so the load falls back to the panel process's own CWD
(`cmdman/compose/normalize.go:104-109`). Three call sites drop it:
`resolveManagerSelection` → `ResolveMuxSelectionByName(b.projectName, b.file)`
(`cmdman/cli/tui_backend_projectmanager.go:69-71`), `SetScale` →
`compose.ScaleOption{File: …}` with no `WorkDir`
(`tui_backend_projectmanager.go:103-109`), and `CycleScale`
(`tui_backend_projectmanager.go:125`). `b.workDir` is available at all three.

The summon passes `--workdir` on the child argv
(`tui_backend_projectmanager.go:177-189`), which is exactly the invocation that
loses it. Observed with a live project of 3 running replicas and the panel run
from an unrelated directory with `--workdir <wd> --file <path> --project-name
<name>`: every row read `×0`, and `+` reported "web scaled to 1" while creating
**one new command** under `cmdman.compose.workdir=<panel cwd>` and leaving the
real project's 3 replicas untouched. `@cmdman_scale` reads and the layout marker
miss for the same reason (the identity hashes the wrong work directory).

Step 6's e2e passes over it because it asserts the summoned panel's *service
name*, which comes from the spec rather than the store. Step 8's new token/e2e
coverage therefore asserts names on the explicit/token paths and replica counts
only on the cwd path. **Follow-up**: thread `b.workDir` into the three loads;
deserves its own commit, and a summon e2e that asserts a count.

## Full TUI Compose-tab Active mark is still cwd-only (out-of-scope discovery, 2026-08-16)

`cmdman/tui/state.go:371` marks the Compose tab's active project by cwd
match only — it is not among D3's enumerated consumers (switcher Active
mark, `resolveLayoutSelection`, project-manager), so step 3 deliberately did
not convert it. **Follow-up**: fold it onto `ActiveIdentity` in a later
change for full D3 consistency.
