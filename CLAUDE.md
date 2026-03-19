<!-- ooo:START -->
<!-- ooo:VERSION:0.14.0 -->
# Ouroboros — Specification-First AI Development

> Before telling AI what to build, define what should be built.
> As Socrates asked 2,500 years ago — "What do you truly know?"
> Ouroboros turns that question into an evolutionary AI workflow engine.

Most AI coding fails at the input, not the output. Ouroboros fixes this by
**exposing hidden assumptions before any code is written**.

1. **Socratic Clarity** — Question until ambiguity ≤ 0.2
2. **Ontological Precision** — Solve the root problem, not symptoms
3. **Evolutionary Loops** — Each evaluation cycle feeds back into better specs

```
Interview → Seed → Execute → Evaluate
    ↑                           ↓
    └─── Evolutionary Loop ─────┘
```

## ooo Commands

Each command loads its agent/MCP on-demand. Details in each skill file.

| Command | Loads |
|---------|-------|
| `ooo` | — |
| `ooo interview` | `ouroboros:socratic-interviewer` |
| `ooo seed` | `ouroboros:seed-architect` |
| `ooo run` | MCP required |
| `ooo evolve` | MCP: `evolve_step` |
| `ooo evaluate` | `ouroboros:evaluator` |
| `ooo unstuck` | `ouroboros:{persona}` |
| `ooo status` | MCP: `session_status` |
| `ooo setup` | — |
| `ooo help` | — |

## Agents

Loaded on-demand — not preloaded.

**Core**: socratic-interviewer, ontologist, seed-architect, evaluator,
wonder, reflect, advocate, contrarian, judge
**Support**: hacker, simplifier, researcher, architect
<!-- ooo:END -->

# smux — Development Guide

## Project Overview

smux is a terminal SSH multiplexer: a bubbletea TUI for selecting hosts from a
YAML cluster inventory, backed by tmux for window and pane management. It runs
as a persistent window-0 process inside a tmux session and creates split-pane
SSH windows on demand.

## Architecture

```
cmd/smux/main.go          — entry point, flag parsing, run modes
internal/config/          — YAML config parsing, host resolution
  config.go               — Config struct, cluster/host types, loader
  sshconfig.go            — ~/.ssh/config reader (kevinburke/ssh_config)
internal/tui/             — bubbletea TUI
  model.go                — Model, Update, View; host selection logic
  phase.go                — Phase state machine (Browsing/Selecting/Confirming/Launching/QuitConfirming)
  tree.go                 — TreeNode and BuildFlatList for cluster tree rendering
internal/tmux/            — tmux command wrappers
  tmux.go                 — window creation, keybindings, mouse mode, window management
  pane.go                 — PaneSession lifecycle, WatchPaneExit
```

**Run modes** (mutually exclusive flags):
- *(no flag)*: `runPersistent` — long-lived window-0 TUI process
- `--popup`: `runPopup` — ephemeral one-shot TUI inside a `display-popup`
- `--smart-open`: `runSmartOpen` — dispatches to popup or window-creation based on session state

## Key Invariants

- `CreateSSHWindow` creates the window **detached** (`-d`), so focus never jumps unexpectedly.
- Window focus sequence after SSH window creation: `CreateSSHWindow` → `MoveWindowToFront(smuxWindowID)` → `SelectWindow(sshWindowID)`.
- The `@smux-managed` tmux window option marks SSH windows so they can be adopted on revival.
- M-s / prefix+s binding is **permanent** (not unbound on smux exit) so the user can always revive smux.
- M-b / prefix+b and M-a / prefix+a are cleaned up on exit via `defer CleanupKeybindings`.

## Development

```bash
make build    # build ./bin/smux
make test     # go test ./...
make install  # install to /usr/local/bin/smux
make clean    # remove ./bin
```

All tests are source-inspection tests or unit tests; none require a live tmux session. The `writeFakeTmux` helper in `tmux_test.go` injects a fake `tmux` binary via `PATH`.

## Adding New Config Fields

1. Add the field to the appropriate struct in `internal/config/config.go`.
2. Add a default accessor method (e.g. `EffectiveX() T`) that returns the configured value or a sensible default.
3. Update `ExampleConfig` to document the new field (commented out with default value).
4. Validate in `run()` in `main.go` if the field can be invalid.

## Test Conventions

- Tests live next to the code they test (`*_test.go` in the same package).
- Source-inspection tests use `os.ReadFile("file.go")` and `strings.Contains`.
- Fake tmux is injected by writing a shell script to a temp dir and prepending it to `PATH`.
- Never mock the config loader; use `config.Config{}` literals directly.
