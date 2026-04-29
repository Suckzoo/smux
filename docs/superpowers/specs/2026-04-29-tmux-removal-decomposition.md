# smux v0.1.0 — Native Rewrite (Tmux Removal) Decomposition

**Status:** brainstorm output, decomposition only — each milestone gets its own design doc when its turn comes.
**Date:** 2026-04-29
**Target version:** v0.1.0

## Why

smux is currently a TUI built on top of tmux. The tmux foundation forces three recurring pains on daily use:

1. **Prefix model** — tmux's "leader key, then command" model is the dominant friction. The user's chosen prefix (C-a) collides with readline's start-of-line, making every shell line edit a near-miss with the prefix.
2. **Mouse mode for scroll** — scrolling pane history requires mouse mode enabled and lands the user in copy-mode, which has its own keymap and exit dance.
3. **Inherited tmux idioms** — every smux binding rides on top of the user's tmux prefix; we can't make smux's UX better than tmux's UX without escaping tmux entirely.

The user has decided: leave tmux entirely. smux v0.1.0 is a native multiplexer with no tmux dependency.

## Scope decisions (locked in)

| Question | Decision |
| --- | --- |
| Backend | Native — own PTYs, own terminal emulation, own layout. No tmux, no zellij. |
| Persistence (detach/reattach across SSH disconnects) | **Dropped.** Anyone who needs it wraps `smux` in `tmux`/`screen` themselves. |
| Multi-group model | **Tabs inside smux.** Tab bar at top; multiple host groups can be open. |
| Keybinding model | **Function keys.** F1–F12 own all smux actions. No prefix, no leader, no waiting. |
| Pane focus | Click to focus, Modifier+Arrow to focus by direction (modifier TBD in M2 design) |
| Zoom (single-pane) mode | **Double-click** focused pane to zoom; double-click again to un-zoom. F-key alternative also bound. |
| Mouse-wheel scroll | Smux owns it — scrolls native scrollback ring buffer per pane. No copy-mode. |
| Shipping strategy | **Big-bang rewrite.** Existing v0.0.x users stay on tmux-backed smux until v0.1.0 ships. No coexistence flag, no parallel backends. |

## What survives the rewrite

These packages have no tmux dependency and carry over essentially unchanged:

- `internal/config/` — YAML cluster inventory, host resolution, IP resolution
- `internal/sshconfig/` — `~/.ssh/config` reader
- `internal/sshkeys/` — temporary SSH keypair lifecycle
- `internal/filetree/` — remote file tree browsing (SFTP-based)
- `internal/executor/` — direct-parallel / hub-spoke / spoke-pull file distribution
- `internal/tui/` host picker, subgroup tree, distribute-file wizard — bubbletea-based, will be re-hosted inside the new TUI shell

## What disappears

- `internal/tmux/` — deleted entirely
- `cmd/smux/main.go` run modes: `runPersistent`, `runPopup`, `runSmartOpen`
- Flags: `--popup`, `--smart-open`
- `TMUX` env detection / bootstrap / re-exec inside tmux
- `@smux-managed` window markers, window adoption on revival
- All tmux keybinding configuration (`broadcast-toggle`, `attach-pane`)
- Fake-tmux test harness (`tmux_test.go` and the `writeFakeTmux` helper)

## Architecture (target)

```
                 [ Ghostty / iTerm / etc. ]   ← user's terminal emulator
                         ↑
                     outer PTY
                         ↑
            ┌────────[ smux ]────────┐
            │  bubbletea TUI shell   │
            │  ┌─ Tab bar ─────────┐ │
            │  │ [staging] prod*   │ │   * = broadcast on
            │  └───────────────────┘ │
            │  ┌─ Active tab ──────┐ │
            │  │ ┌──────┬───────┐  │ │
            │  │ │ pane │ pane  │  │ │   ← each pane = cell-grid view
            │  │ ├──────┼───────┤  │ │     of an inner-PTY emulator
            │  │ │ pane │ pane  │  │ │
            │  │ └──────┴───────┘  │ │
            │  └───────────────────┘ │
            └───┬───────┬───────┬────┘
                ▼       ▼       ▼
            inner   inner   inner
            PTY 1   PTY 2   PTY 3      ← one per pane
                ▼       ▼       ▼
              ssh A   ssh B   ssh C
```

**Key invariant:** smux runs an in-process terminal emulator (cell-grid model) for each inner PTY. This is non-optional because (a) multiple PTY output streams cannot share one outer terminal without a multiplexer, and (b) broadcast-typing requires smux to be in the input path.

The terminal-emulator library choice is the largest open risk. M1.1 evaluates `hinshun/vt10x`, charmbracelet candidates, and any other contenders, then commits.

## Decomposition — five milestones

### M1 — Terminal core (foundation, biggest unknown)

Goal: smux can SSH to one host in a single full-screen native pane. vim works, less works, mouse-wheel scrolls scrollback, resize works.

Subtasks:
- **M1.1** Spike & pick a Go terminal-emulator library (`hinshun/vt10x`, charmbracelet candidates, or hand-rolled). Output: comparison + decision committed to repo.
- **M1.2** PTY allocation + child process management (`creack/pty`).
- **M1.3** ANSI/escape stream → in-memory cell grid via the chosen lib.
- **M1.4** Render the cell grid in a bubbletea View; integrate with smux's existing TUI loop.
- **M1.5** Scrollback ring buffer + mouse-wheel scrolling (no copy-mode UI).
- **M1.6** SIGWINCH propagation: terminal resize → re-tile → notify each PTY.

Ship state: a `smux-spike` binary that takes a hostname arg and SSHes into it natively. Not user-facing yet, not connected to host picker. **Risk-burndown milestone.**

### M2 — Multi-pane group (the heart of smux)

Goal: replicate today's "select N hosts, get a tiled SSH window" — natively, no tmux.

Subtasks:
- **M2.1** Tiled layout engine: N panes in a window region; reflow on resize.
- **M2.2** Per-pane focus state + visual focus indicator.
- **M2.3** Input routing core: F-keys → smux, mouse events → smux, everything else → focused pane's PTY.
- **M2.4** Arrow-key pane navigation with a modifier (Modifier+Arrow; concrete modifier settled in M2 design).
- **M2.5** Mouse: single click → focus pane; double-click → toggle zoom (single-pane) mode.
- **M2.6** Zoom mode: focused pane fills viewport, others hidden, indicator visible.
- **M2.7** Broadcast mode: input multiplex to all panes; visible "BROADCAST" indicator.
- **M2.8** Wire the existing host-picker TUI into the launch flow. On submit, create the multi-pane view.

Ship state: native smux can do single-group SSH with broadcast & zoom. tmux-backed smux still ships in parallel until M4.

### M3 — Tabs (multi-group)

Goal: multiple host groups open simultaneously via in-app tabs.

Subtasks:
- **M3.1** Tab data model: each tab owns a multi-pane group, broadcast state, focus state.
- **M3.2** Tab bar render (top): current tab, count, broadcast indicator per tab.
- **M3.3** F-key bindings:
  - F1 picker / new tab
  - F2 broadcast toggle
  - F3 next tab, F4 prev tab
  - F5 zoom toggle
  - F6 close tab
  - (final assignments confirmed in M3 design)
- **M3.4** New-tab flow: F1 → picker overlay → select hosts → new tab opens.
- **M3.5** Close-tab UX with confirmation if SSH still running.

Ship state: feature parity with current tmux-backed smux, all native.

### M4 — Migration cutover (this is the v0.1.0 release)

Goal: delete tmux. Ship v0.1.0.

Subtasks:
- **M4.1** Strip `internal/tmux/` (delete entire package).
- **M4.2** Rewrite `cmd/smux/main.go`: drop `runPersistent`, `runPopup`, `runSmartOpen`, `--smart-open`, `--popup` flags, TMUX env detection, bootstrap.
- **M4.3** Verify `internal/sshkeys/`, `internal/sshconfig/`, `internal/filetree/` still work (none should depend on tmux but verify).
- **M4.4** Adapt distribute-file mode to native pane execution (verify tmux dep status during this task).
- **M4.5** Update `internal/config/config.go`: drop tmux-prefix keybinding fields, add F-key overrides.
- **M4.6** Update tests: remove fake-tmux harness (`tmux_test.go`), add native equivalents.
- **M4.7** Update README, CLAUDE.md, example config — remove tmux setup instructions.
- **M4.8** Bump to v0.1.0, ship.

Ship state: v0.1.0 released. tmux dependency gone forever.

### M5 — Polish (post-v0.1.0)

Each its own small ticket; v0.1.1+. Stretch:
- Scrollback search overlay
- Configurable layouts beyond tiled (main-vertical, main-horizontal)
- Themeable status bar
- Configurable F-key bindings
- Copy/paste integration with system clipboard

## Sequencing

Strictly sequential M1 → M2 → M3 → M4. M5 is post-v0.1.0.

```
M1 ──▶ M2 ──▶ M3 ──▶ M4 (v0.1.0)
                              ╲
                               ▶ M5 (v0.1.x)
```

## Estimates (honest, not committed)

- M1: ~1 week (library quality is the gating risk)
- M2: ~2 weeks
- M3: ~3–4 days
- M4: ~3–5 days
- **Total to v0.1.0: ~3–4 weeks of focused work**

## Open questions deferred to per-milestone design

- M1.1 — which terminal-emulator library wins?
- M2.4 — Alt+Arrow vs Ctrl+Arrow vs Shift+Arrow for pane navigation? Which collides least with shell editing?
- M2.5 — single-click focus only, or click-and-hold for selection? (Selection might need M5-era copy/paste work.)
- M3.3 — final F-key assignments (and which subset are user-overridable in config).
- M4.4 — does distribute-file mode currently depend on tmux? Audit during M4.
