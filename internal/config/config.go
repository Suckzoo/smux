package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
      # internal_ip is optional. When set, all SSH/SCP connections to this
      # host use the internal IP instead of the hostname/public address.
      # Useful for hub-and-spoke mode where spokes are on a private network.
      # - name: spoke-01.example.com
      #   internal_ip: 10.0.0.11
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

// EffectiveAddress returns the SSH alias (Host) for local→host connections.
// Internal IPs are never used here — they are exclusively for inter-host
// communication (spoke-pull). Use ResolvePrivateIP at spoke-pull time instead.
func (h ResolvedHost) EffectiveAddress() string {
	return h.Host
}

// RemoteReachableAddress returns the address that a remote host can use to
// reach this host. Unlike EffectiveAddress (which may return an SSH alias
// that only the local machine can resolve), RemoteReachableAddress resolves
// the alias through ~/.ssh/config's Hostname directive when InternalIP is
// not explicitly set. This is necessary for hub-to-spoke transfers where
// the hub must connect to the spoke by a resolvable address, not a local
// SSH alias.
func (h ResolvedHost) RemoteReachableAddress() string {
	if h.InternalIP != "" {
		return h.InternalIP
	}
	// Look up the Hostname directive from ~/.ssh/config. If the alias has
	// a Hostname entry (e.g. Host nhn-gpu050 → Hostname 10.0.1.50), use
	// that. Otherwise fall back to the alias itself.
	return SSHConfigGet(h.Host, "Hostname", h.Host)
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
// A cluster may define hosts either as a flat list (Hosts) or grouped
// into named subgroups (Subgroups). Both may not be non-empty simultaneously.
type ClusterConfig struct {
	Defaults  HostDefaults               `yaml:"defaults"`
	Hosts     []HostEntry                `yaml:"hosts"`
	Subgroups map[string]SubgroupConfig  `yaml:"subgroups"`
}

// SubgroupConfig holds hosts and IP resolution settings for a named subgroup
// within a cluster. Subgroups allow grouping hosts by rack, region, etc.,
// and enable bulk private-IP resolution via CIDR or index-based templates.
type SubgroupConfig struct {
	Hosts          []HostEntry `yaml:"hosts"`
	InternalCIDR   string      `yaml:"internal_cidr"`
	InternalIPBase string      `yaml:"internal_ip_base"`
}

// HostDefaults provides cluster-wide SSH defaults.
type HostDefaults struct {
	User           string `yaml:"user"`
	Port           int    `yaml:"port"`
	Key            string `yaml:"key"`
	JumpHost       string `yaml:"jump_host"`
	InternalCIDR   string `yaml:"internal_cidr"`
	InternalIPBase string `yaml:"internal_ip_base"`
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
	InternalIP string
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
		Name       string `yaml:"name"`
		User       string `yaml:"user"`
		Port       int    `yaml:"port"`
		Key        string `yaml:"key"`
		JumpHost   string `yaml:"jump_host"`
		InternalIP string `yaml:"internal_ip"`
	}
	if err := value.Decode(&obj); err != nil {
		return err
	}
	h.Name = obj.Name
	h.User = obj.User
	h.Port = obj.Port
	h.Key = obj.Key
	h.JumpHost = obj.JumpHost
	h.InternalIP = obj.InternalIP
	h.Provenance = ProvenanceFull
	return nil
}

// ResolvedHost is a fully merged host, ready for SSH command construction.
// ClusterNames holds all cluster names that contain this SSH alias; when a
// host appears in only one cluster the slice has exactly one element.
type ResolvedHost struct {
	DisplayName    string
	Host           string
	User           string
	Port           int
	Key            string
	JumpHost       string
	InternalIP     string // explicit per-host internal_ip from config
	InternalCIDR   string // from subgroup or cluster defaults; resolved at runtime
	InternalIPBase string // template like "10.0.0.{100+$index}"; resolved at spoke-pull time
	IPBaseIndex    int    // host's index within its subgroup/cluster for ip_base resolution
	SubgroupName   string // empty for flat clusters
	ClusterNames   []string // all cluster names this host belongs to
	Provenance     Provenance
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
		InternalIP:   h.InternalIP,
		ClusterNames: []string{clusterName},
		Provenance:   h.Provenance,
	}
}

// ResolveInSubgroup merges per-host fields with cluster defaults and subgroup
// context to produce a ResolvedHost. IP resolution fields (InternalCIDR,
// InternalIPBase, IPBaseIndex) are stored for deferred resolution at spoke-pull
// time — they do NOT affect EffectiveAddress() or local connections.
func (h *HostEntry) ResolveInSubgroup(clusterName string, defaults HostDefaults, subgroupName string, sg SubgroupConfig, indexInSubgroup int) ResolvedHost {
	r := h.Resolve(clusterName, defaults)
	r.SubgroupName = subgroupName
	r.IPBaseIndex = indexInSubgroup

	// Store IP base template (subgroup takes priority over cluster defaults).
	ipBase := sg.InternalIPBase
	if ipBase == "" {
		ipBase = defaults.InternalIPBase
	}
	r.InternalIPBase = ipBase

	// Store CIDR for runtime resolution (subgroup takes priority).
	cidr := sg.InternalCIDR
	if cidr == "" {
		cidr = defaults.InternalCIDR
	}
	r.InternalCIDR = cidr

	return r
}

// ResolveIPBasePublic is the exported form of resolveIPBase for use by other
// packages (e.g. the TUI tree builder).
func ResolveIPBasePublic(template string, index int) (string, error) {
	return resolveIPBase(template, index)
}

// resolveIPBase evaluates an IP base template like "10.61.3.{191+$index}"
// by substituting $index with the given value and computing the result.
// The template must contain exactly one {base+$index} expression in the
// last octet position. Returns an error if the result exceeds 255.
func resolveIPBase(template string, index int) (string, error) {
	// Find the {expr} portion.
	openBrace := strings.IndexByte(template, '{')
	closeBrace := strings.IndexByte(template, '}')
	if openBrace < 0 || closeBrace < 0 || closeBrace <= openBrace {
		return "", fmt.Errorf("invalid internal_ip_base template %q: missing {base+$index}", template)
	}

	prefix := template[:openBrace]
	suffix := template[closeBrace+1:]
	expr := template[openBrace+1 : closeBrace]

	// Parse "base+$index" or just "$index" or just "base".
	expr = strings.ReplaceAll(expr, "$index", strconv.Itoa(index))

	// Evaluate simple addition: "191+0" → 191
	parts := strings.SplitN(expr, "+", 2)
	var result int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return "", fmt.Errorf("invalid internal_ip_base expression %q: %w", template, err)
		}
		result += v
	}

	if result < 0 || result > 255 {
		return "", fmt.Errorf("internal_ip_base %q at index %d: octet %d out of range 0-255", template, index, result)
	}

	return prefix + strconv.Itoa(result) + suffix, nil
}

// SubgroupNames returns the sorted subgroup names for a cluster.
func (cc ClusterConfig) SubgroupNames() []string {
	names := make([]string, 0, len(cc.Subgroups))
	for name := range cc.Subgroups {
		names = append(names, name)
	}
	sortStrings(names)
	return names
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
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validate checks config invariants. Currently rejects clusters that have
// both flat Hosts and Subgroups non-empty.
func (c *Config) validate() error {
	for name, cluster := range c.Clusters {
		if len(cluster.Hosts) > 0 && len(cluster.Subgroups) > 0 {
			return fmt.Errorf("cluster %q: cannot have both 'hosts' and 'subgroups'", name)
		}
	}
	return nil
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
// position within that cluster. Subgroup hosts are flattened in subgroup-name
// order within each cluster.
func (c *Config) AllResolvedHosts() []ResolvedHost {
	// Process clusters in sorted order so first-seen is deterministic.
	clusterOrder := c.ClusterNames()

	// seen maps SSH alias → index into result slice.
	seen := make(map[string]int)
	var result []ResolvedHost

	addHost := func(r ResolvedHost, clusterName string) {
		if idx, ok := seen[r.Host]; ok {
			result[idx].ClusterNames = append(result[idx].ClusterNames, clusterName)
		} else {
			seen[r.Host] = len(result)
			result = append(result, r)
		}
	}

	for _, name := range clusterOrder {
		cluster := c.Clusters[name]

		// Flat hosts (backward compat).
		for i, h := range cluster.Hosts {
			r := h.Resolve(name, cluster.Defaults)
			// Store IP resolution fields for deferred spoke-pull resolution.
			r.IPBaseIndex = i
			r.InternalIPBase = cluster.Defaults.InternalIPBase
			if r.InternalCIDR == "" {
				r.InternalCIDR = cluster.Defaults.InternalCIDR
			}
			addHost(r, name)
		}

		// Subgroup hosts (sorted by subgroup name).
		for _, sgName := range cluster.SubgroupNames() {
			sg := cluster.Subgroups[sgName]
			for i, h := range sg.Hosts {
				r := h.ResolveInSubgroup(name, cluster.Defaults, sgName, sg, i)
				addHost(r, name)
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
// callers can rely on deterministic output. Both flat hosts and subgroup hosts
// are searched.
func (c *Config) AllClustersForHost(hostName string) []string {
	var clusters []string
	for _, name := range c.ClusterNames() { // already sorted
		cluster := c.Clusters[name]
		found := false
		for _, h := range cluster.Hosts {
			if h.Name == hostName {
				found = true
				break
			}
		}
		if !found {
			for _, sg := range cluster.Subgroups {
				for _, h := range sg.Hosts {
					if h.Name == hostName {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
		if found {
			clusters = append(clusters, name)
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
