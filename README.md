# smux

smux is a terminal SSH multiplexer. It presents an interactive TUI for selecting
hosts from a YAML cluster inventory and opens them as synchronized split panes
inside a tmux window — all from a single keypress.

## Features

- Cluster-tree TUI with fuzzy filtering and multi-select
- Synchronized broadcast to all panes (type once, send everywhere)
- Single-key broadcast toggle and pane break-out via tmux prefix bindings
- Persistent window-0 presence: smux stays in the background and is always one keypress away
- Popup mode: press `prefix+s` from any tmux window to open a floating smux TUI
- Smart revival: if smux is not running, `prefix+s` creates a new smux window automatically
- Large-selection confirmation prompt (configurable threshold)

## Requirements

- Go 1.21+ (build only)
- tmux 3.2+

## Install

### From source

```bash
git clone https://github.com/Suckzoo/smux.git
cd smux
make install          # installs to /usr/local/bin/smux
```

### Pre-built binaries

Download from the [Releases](https://github.com/Suckzoo/smux/releases) page.

## Quick Start

1. Create `~/.config/smux/config.yaml` (smux creates an example on first run).
2. Run `smux` in your terminal.
   - If not already in tmux, smux bootstraps a new tmux session automatically.
3. Navigate the host tree, select hosts with `Space`, press `Enter` to connect.
4. Press `prefix+s` from any tmux window to open a floating smux TUI.

## Usage

```
smux [flags]

Flags:
  --popup        Run as an ephemeral popup (used internally by prefix+s)
  --smart-open   Open popup if smux is running, revive it if not (used by prefix+s)
```

### TUI keybindings

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `Tab` / `→` / `l` | Expand cluster / move right |
| `←` / `h` | Collapse cluster / move up |
| `Space` | Select / deselect host (or all hosts in cluster) |
| `/` | Open filter input |
| `Esc` | Close filter input |
| `Enter` | Confirm selection and open SSH panes |
| `q` | Quit (in persistent mode: shows confirmation dialog) |
| `Ctrl+C` | Force quit |

### tmux keybindings (set by smux, default prefix table)

| Binding | Action |
|---------|--------|
| `prefix+s` | Open smux popup / revive smux window |
| `prefix+b` | Toggle broadcast (synchronize-panes) for current window |
| `prefix+a` | Break focused pane to its own window |
| Double-click pane | Disable broadcast and focus that pane |

> The `prefix+b` and `prefix+a` bindings are active while smux is running and
> are cleaned up when smux exits. The `prefix+s` binding is permanent.

## Configuration

Config file: `~/.config/smux/config.yaml`

smux auto-creates an example config on first run if none exists.

### Full example with all options

```yaml
# ─────────────────────────────────────────────
# Cluster definitions
# ─────────────────────────────────────────────
clusters:

  # Simple cluster: bare hostnames (SSH aliases from ~/.ssh/config)
  web:
    defaults:
      user: ubuntu           # SSH username applied to all hosts in this cluster
      key: ~/.ssh/id_ed25519 # Private key path (~ is expanded by the shell)
      port: 22               # Default SSH port (omit to use SSH config default)
      # jump_host: bastion.example.com  # Optional jump/bastion host
    hosts:
      - web-01.example.com   # Simple form: bare SSH alias or hostname
      - web-02.example.com
      - web-03.example.com

  # Verbose cluster: per-host overrides
  database:
    defaults:
      user: ubuntu
      key: ~/.ssh/id_ed25519
    hosts:
      # Verbose form: override any default per-host
      - name: db-primary.example.com
        user: postgres         # Overrides cluster default
        port: 2222             # Overrides cluster default
        key: ~/.ssh/db.pem     # Overrides cluster default
        jump_host: bastion.example.com  # Per-host jump host

      # Mix of simple and verbose in the same cluster is fine
      - db-replica-01.example.com
      - db-replica-02.example.com

  # A host can appear in multiple clusters; smux deduplicates by SSH alias
  staging:
    defaults:
      user: ubuntu
    hosts:
      - staging-01.example.com

# ─────────────────────────────────────────────
# Large-selection confirmation
# ─────────────────────────────────────────────
# Prompt for confirmation when this many or more hosts are selected.
# Default: 50
large_selection_threshold: 50

# ─────────────────────────────────────────────
# Pane layout
# ─────────────────────────────────────────────
# Layout applied to panes in the SSH window after all splits are created.
# Accepted values:
#   tiled      — all panes equal size, grid arrangement (default)
#   horizontal — panes side by side in a single row
#   vertical   — panes stacked in a single column
default_layout: tiled

# ─────────────────────────────────────────────
# Keybindings
# ─────────────────────────────────────────────
# Default: prefix-table bindings (works on macOS without terminal reconfiguration).
# mode: prefix — press your tmux prefix (e.g. Ctrl+A), then the key
# mode: root   — press the key alone with no prefix (requires terminal Meta support)
keybindings:
  broadcast_toggle:
    key: b       # Toggle synchronize-panes for current window
    mode: prefix # Available values: prefix, root
  attach_pane:
    key: a       # Break focused pane to its own window
    mode: prefix
  popup_toggle:
    key: s       # Open smux popup / revive smux window
    mode: prefix

# Alternative: single-keypress Alt bindings for Linux or iTerm2 (Option-as-Meta)
# Requires iTerm2: Profiles → Keys → Left Option Key → Esc+
# Or Terminal.app: Preferences → Profiles → Keyboard → Use Option as Meta key
#
# keybindings:
#   broadcast_toggle:
#     key: M-b
#     mode: root
#   attach_pane:
#     key: M-a
#     mode: root
#   popup_toggle:
#     key: M-s
#     mode: root
```

### SSH host resolution

For each host, smux builds the SSH command by merging per-host fields with
cluster defaults (per-host takes precedence):

```
ssh [-l user] [-p port] [-i key] [-J jump_host] hostname
```

If a field is not set in the config, the standard SSH config at `~/.ssh/config`
is consulted automatically. Bare hostname entries (simple form) act as SSH
aliases.

## Architecture

```
smux
├── cmd/smux/main.go          Entry point, flag parsing, run-mode dispatch
├── internal/
│   ├── config/
│   │   ├── config.go         YAML config types, host resolution, loader
│   │   └── sshconfig.go      ~/.ssh/config reader
│   ├── tui/
│   │   ├── model.go          bubbletea Model — selection logic, key/mouse handling
│   │   ├── phase.go          Phase state machine
│   │   └── tree.go           Cluster tree rendering
│   └── tmux/
│       ├── tmux.go           tmux command wrappers (window, pane, keybinding, mouse)
│       └── pane.go           PaneSession lifecycle, exit watching
└── Makefile
```

### Run modes

| Mode | Flag | Description |
|------|------|-------------|
| Persistent | *(none)* | Long-lived window-0 TUI. Loops: show TUI → create SSH window → repeat |
| Popup | `--popup` | Ephemeral one-shot TUI inside a `display-popup`. Creates window then exits |
| Smart-open | `--smart-open` | If smux is running → show popup. If not → create new smux window |

### Phase state machine (TUI)

```
BrowsingPhase ──/──────────────► SelectingPhase
              ──Enter(<threshold)► LaunchingPhase
              ──Enter(≥threshold)► ConfirmingPhase ──y──► LaunchingPhase
              ──q (persistent)──► QuitConfirmingPhase ──y──► exit + kill windows
                                                      ──n──► BrowsingPhase
```

### Window focus sequence

After `Enter` confirms a host selection:

1. `CreateSSHWindow` — creates the SSH window detached (focus unchanged)
2. `MoveWindowToFront(smuxWindowID)` — smux moves silently to window index 0
3. `SelectWindow(sshWindowID)` — focus switches to the new SSH window

The user lands on SSH sessions immediately; smux is ready in the background.

## Building

```bash
make build    # produces ./bin/smux
make test     # runs all tests
make install  # copies ./bin/smux to /usr/local/bin/smux
make clean    # removes ./bin
```

For release builds (goreleaser):

```bash
goreleaser release --snapshot --clean
```
