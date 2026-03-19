package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ErrMissingConfig is returned when the config file does not exist.
var ErrMissingConfig = errors.New("config file not found")

// ExampleConfig is a YAML example shown to users when config is missing.
const ExampleConfig = `clusters:
  production:
    defaults:
      user: ubuntu
      key: ~/.ssh/id_rsa
    hosts:
      - web-01.example.com
      - web-02.example.com
      - name: db-01.example.com
        user: postgres
  staging:
    defaults:
      user: ubuntu
    hosts:
      - staging-01.example.com

# Prompt for confirmation when this many or more hosts are selected.
# Defaults to 50 if omitted.
large_selection_threshold: 50

# Pane layout when a new SSH window is created.
# Accepted values: tiled (default), horizontal, vertical.
# default_layout: tiled

keybindings:
  broadcast_toggle:
    key: b
    mode: prefix
  attach_pane:
    key: a
    mode: prefix
  popup_toggle:
    key: s
    mode: prefix
# To use single-keypress Alt bindings (Linux / iTerm2 with Option-as-Meta):
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
`

// PaneLayout names the three supported tmux pane layout styles.
// "horizontal" maps to even-horizontal, "vertical" to even-vertical,
// and "tiled" keeps all panes the same size.
type PaneLayout string

const (
	PaneLayoutTiled      PaneLayout = "tiled"
	PaneLayoutHorizontal PaneLayout = "horizontal"
	PaneLayoutVertical   PaneLayout = "vertical"
)

// TmuxLayout returns the tmux layout name for the PaneLayout.
func (l PaneLayout) TmuxLayout() string {
	switch l {
	case PaneLayoutHorizontal:
		return "even-horizontal"
	case PaneLayoutVertical:
		return "even-vertical"
	default:
		return "tiled"
	}
}

// Config is the top-level configuration structure.
type Config struct {
	Keybindings             Keybindings             `yaml:"keybindings"`
	Clusters                map[string]ClusterConfig `yaml:"clusters"`
	LargeSelectionThreshold int                     `yaml:"large_selection_threshold"`
	DefaultLayout           PaneLayout              `yaml:"default_layout"`
}

// EffectivePaneLayout returns the resolved tmux layout name. It validates the
// configured value and returns an error if it is not one of the three supported
// values. When unset (empty string) it defaults to "tiled".
func (c *Config) EffectivePaneLayout() (string, error) {
	if c == nil || c.DefaultLayout == "" {
		return "tiled", nil
	}
	switch c.DefaultLayout {
	case PaneLayoutTiled, PaneLayoutHorizontal, PaneLayoutVertical:
		return c.DefaultLayout.TmuxLayout(), nil
	default:
		return "", fmt.Errorf("invalid default_layout %q: must be one of horizontal, vertical, tiled", c.DefaultLayout)
	}
}

// EffectiveConfirmThreshold returns the large-selection confirmation threshold.
// If LargeSelectionThreshold is unset (zero) in the config file the default of
// 50 is used, matching the spec requirement.
func (c *Config) EffectiveConfirmThreshold() int {
	if c != nil && c.LargeSelectionThreshold > 0 {
		return c.LargeSelectionThreshold
	}
	return 50
}

// Keybindings holds configurable tmux key bindings.
type Keybindings struct {
	BroadcastToggle KeyBinding `yaml:"broadcast_toggle"`
	AttachPane      KeyBinding `yaml:"attach_pane"`
	PopupToggle     KeyBinding `yaml:"popup_toggle"`
}

// KeyBinding represents a tmux key binding.
type KeyBinding struct {
	Key  string `yaml:"key"`  // e.g. "M-b"
	Mode string `yaml:"mode"` // "root" or "prefix"
}

// ClusterConfig holds defaults and hosts for a named cluster.
type ClusterConfig struct {
	Defaults HostDefaults `yaml:"defaults"`
	Hosts    []HostEntry  `yaml:"hosts"`
}

// HostDefaults provides cluster-wide SSH defaults.
type HostDefaults struct {
	User     string `yaml:"user"`
	Port     int    `yaml:"port"`
	Key      string `yaml:"key"`
	JumpHost string `yaml:"jump_host"`
}

// Provenance indicates how a host entry was specified in the config file.
// "alias" means it was a bare string (SSH alias), "full" means it was an
// object with explicit fields.
type Provenance string

const (
	// ProvenanceAlias indicates the host was specified as a bare string SSH alias.
	ProvenanceAlias Provenance = "alias"
	// ProvenanceFull indicates the host was specified as a full object with fields.
	ProvenanceFull Provenance = "full"
)

// HostEntry is a single host in a cluster. It can be a plain string (SSH alias)
// or an object with per-host overrides. Custom UnmarshalYAML handles both forms.
type HostEntry struct {
	Name       string
	User       string
	Port       int
	Key        string
	JumpHost   string
	Provenance Provenance
}

// UnmarshalYAML implements yaml.Unmarshaler to handle both string and object forms.
func (h *HostEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		h.Name = value.Value
		h.Provenance = ProvenanceAlias
		return nil
	}
	var obj struct {
		Name     string `yaml:"name"`
		User     string `yaml:"user"`
		Port     int    `yaml:"port"`
		Key      string `yaml:"key"`
		JumpHost string `yaml:"jump_host"`
	}
	if err := value.Decode(&obj); err != nil {
		return err
	}
	h.Name = obj.Name
	h.User = obj.User
	h.Port = obj.Port
	h.Key = obj.Key
	h.JumpHost = obj.JumpHost
	h.Provenance = ProvenanceFull
	return nil
}

// ResolvedHost is a fully merged host, ready for SSH command construction.
// ClusterNames holds all cluster names that contain this SSH alias; when a
// host appears in only one cluster the slice has exactly one element.
type ResolvedHost struct {
	DisplayName  string
	Host         string
	User         string
	Port         int
	Key          string
	JumpHost     string
	ClusterNames []string // all cluster names this host belongs to
	Provenance   Provenance
}

// Resolve merges per-host fields with cluster defaults to produce a ResolvedHost.
func (h *HostEntry) Resolve(clusterName string, defaults HostDefaults) ResolvedHost {
	user := h.User
	if user == "" {
		user = defaults.User
	}
	port := h.Port
	if port == 0 {
		port = defaults.Port
	}
	key := h.Key
	if key == "" {
		key = defaults.Key
	}
	jumpHost := h.JumpHost
	if jumpHost == "" {
		jumpHost = defaults.JumpHost
	}
	return ResolvedHost{
		DisplayName:  h.Name,
		Host:         h.Name,
		User:         user,
		Port:         port,
		Key:          key,
		JumpHost:     jumpHost,
		ClusterNames: []string{clusterName},
		Provenance:   h.Provenance,
	}
}

// Load reads and parses the config file at ~/.config/smux/config.yaml.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	path := filepath.Join(home, ".config", "smux", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMissingConfig
		}
		return nil, fmt.Errorf("cannot read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}
	return &cfg, nil
}

// CreateDefault writes the example config to ~/.config/smux/config.yaml,
// creating the directory if necessary.
func CreateDefault() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "smux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(ExampleConfig), 0o644); err != nil {
		return fmt.Errorf("cannot write example config: %w", err)
	}
	return nil
}

// AllResolvedHosts returns one ResolvedHost per unique SSH alias across all
// clusters. When the same alias appears in more than one cluster all cluster
// names are aggregated into the ClusterNames slice of the resulting ResolvedHost.
// The returned slice is ordered by first-seen cluster (sorted) then by host
// position within that cluster.
func (c *Config) AllResolvedHosts() []ResolvedHost {
	// Process clusters in sorted order so first-seen is deterministic.
	clusterOrder := c.ClusterNames()

	// seen maps SSH alias → index into result slice.
	seen := make(map[string]int)
	var result []ResolvedHost

	for _, name := range clusterOrder {
		cluster := c.Clusters[name]
		for _, h := range cluster.Hosts {
			r := h.Resolve(name, cluster.Defaults)
			if idx, ok := seen[r.Host]; ok {
				// Alias already present — just append the cluster name.
				result[idx].ClusterNames = append(result[idx].ClusterNames, name)
			} else {
				seen[r.Host] = len(result)
				result = append(result, r)
			}
		}
	}
	return result
}

// AllClustersForHost returns a sorted list of all cluster names that contain
// a host with the given SSH alias (host address). If the alias does not appear
// in any cluster an empty (non-nil) slice is returned.
//
// The result is in sorted cluster-name order, matching ClusterNames(), so
// callers can rely on deterministic output.
func (c *Config) AllClustersForHost(hostName string) []string {
	var clusters []string
	for _, name := range c.ClusterNames() { // already sorted
		cluster := c.Clusters[name]
		for _, h := range cluster.Hosts {
			if h.Name == hostName {
				clusters = append(clusters, name)
				break // host can only appear once per cluster
			}
		}
	}
	if clusters == nil {
		return []string{}
	}
	return clusters
}

// ClusterNames returns sorted cluster names.
func (c *Config) ClusterNames() []string {
	names := make([]string, 0, len(c.Clusters))
	for name := range c.Clusters {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
