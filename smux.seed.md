goal: >
  Build `smux` — a persistent Go CLI SSH multiplexer (github.com/Suckzoo/smux) with a
  bubbletea TUI for selecting hosts from a YAML cluster inventory, then creating tmux
  split panes with synchronized broadcast SSH sessions. smux stays alive after session
  creation and loops back to a fresh TUI so the user can launch additional sessions.

constraints:
  - Go 1.25, module github.com/Suckzoo/smux
  - "Dependencies: bubbletea v1.3.10, lipgloss v1.1.0, bubbles v1.0.0, yaml.v3 v3.0.1, kevinburke/ssh_config v1.6.0"
  - Config at ~/.config/smux/config.yaml (YAML)
  - Requires tmux to be installed at runtime
  - Auto-bootstraps into a new tmux session if run outside tmux
  - "Distribution: goreleaser + GitHub Actions release workflow (linux+darwin, amd64+arm64)"
  - No CLI arguments for cluster filtering

acceptance_criteria:
  - go build ./cmd/smux compiles cleanly with no errors
  - Running smux outside tmux auto-bootstraps into a new tmux session then shows TUI
  - TUI shows all clusters as a collapsible tree; Tab/←/→ expands and collapses clusters
  - Space toggles selection for an individual host or all hosts in a cluster
  - / opens inline fuzzy filter matching both cluster names and host names; Esc clears filter
  - Pressing Enter with 50+ hosts selected shows a TUI confirmation prompt before proceeding
  - Confirming selection opens a new tmux window with one tiled SSH pane per selected host
  - All SSH panes start with synchronize-panes ON (broadcast mode active by default)
  - M-b toggles synchronize-panes on/off for the current tmux window
  - M-a breaks the currently focused pane to a new tmux window (attach pane)
  - Single mouse click focuses the clicked pane and turns synchronize-panes off
  - Double mouse click breaks the clicked pane to a new tmux window
  - When an SSH session exits the pane shows a prompt; pressing y kills the pane
  - After creating an SSH window smux loops back to a fresh unselected TUI in window 0
  - q or Ctrl+C quits smux entirely
  - Running with missing config.yaml prints a helpful message with a YAML format example
  - Running without tmux installed prints a clear error message
  - Terminal smaller than 40 columns or 10 rows shows "Terminal too small"
  - .goreleaser.yaml present targeting linux/darwin on amd64/arm64
  - .github/workflows/release.yml triggers goreleaser on tag push (v*.*.*)

ontology_schema:
  name: SmuxConfig
  description: SSH cluster multiplexer configuration and runtime domain model
  fields:
    - name: config
      type: object
      description: Top-level config with Keybindings and Clusters map
    - name: keybindings
      type: object
      description: BroadcastToggle and AttachPane each with Key string and Mode string (root or prefix)
    - name: clusters
      type: object
      description: Map of cluster name to ClusterConfig
    - name: cluster_config
      type: object
      description: Defaults HostDefaults plus Hosts []HostEntry
    - name: host_defaults
      type: object
      description: Optional User Port Key JumpHost for cluster-wide SSH defaults
    - name: host_entry
      type: object
      description: String SSH alias or object with Name plus per-host User/Port/Key/JumpHost overrides; custom UnmarshalYAML
    - name: resolved_host
      type: object
      description: Fully merged host ready for SSH - DisplayName Host User Port Key JumpHost ClusterName
    - name: tui_model
      type: object
      description: "bubbletea Model: clusters flatItems cursor selected viewport filterText filterActive Confirmed Selected"

evaluation_principles:
  - name: correctness
    description: All acceptance criteria pass including tmux pane layout and keybinding behavior
    weight: 0.5
  - name: code_quality
    description: Idiomatic Go, proper error handling, clear package boundaries, no unnecessary globals
    weight: 0.2
  - name: ux_fidelity
    description: TUI tree view, inline filter, viewport scroll, and status line match the spec exactly
    weight: 0.2
  - name: build_integrity
    description: go build succeeds cleanly and goreleaser config is valid
    weight: 0.1

exit_conditions:
  - name: all_ac_pass
    description: All acceptance criteria verified green
    criteria: go build succeeds and all 20 acceptance criteria are checked off
  - name: max_iterations
    description: Loop cap reached without full pass
    criteria: iteration >= 10

metadata:
  version: "1.0"
  created: "2026-03-18"
  ambiguity_score: 0.10
  source: interview session (Path B — plugin fallback, no MCP session ID)
  project_dir: /Users/suckzoo/tmp/smux
