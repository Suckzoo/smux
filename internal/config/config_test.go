package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestResolveAliasVsFullEquivalence asserts that a bare string alias produces an
// identical ResolvedHost to a full object with only Name set, except for the
// Provenance field which must differ.
func TestResolveAliasVsFullEquivalence(t *testing.T) {
	defaults := HostDefaults{
		User:     "ubuntu",
		Port:     22,
		Key:      "~/.ssh/id_rsa",
		JumpHost: "",
	}
	clusterName := "production"

	aliasEntry := HostEntry{
		Name:       "web-01.example.com",
		Provenance: ProvenanceAlias,
	}
	fullEntry := HostEntry{
		Name:       "web-01.example.com",
		Provenance: ProvenanceFull,
	}

	aliasResolved := aliasEntry.Resolve(clusterName, defaults)
	fullResolved := fullEntry.Resolve(clusterName, defaults)

	// All fields except Provenance must be identical.
	if aliasResolved.DisplayName != fullResolved.DisplayName {
		t.Errorf("DisplayName mismatch: alias=%q full=%q", aliasResolved.DisplayName, fullResolved.DisplayName)
	}
	if aliasResolved.Host != fullResolved.Host {
		t.Errorf("Host mismatch: alias=%q full=%q", aliasResolved.Host, fullResolved.Host)
	}
	if aliasResolved.User != fullResolved.User {
		t.Errorf("User mismatch: alias=%q full=%q", aliasResolved.User, fullResolved.User)
	}
	if aliasResolved.Port != fullResolved.Port {
		t.Errorf("Port mismatch: alias=%d full=%d", aliasResolved.Port, fullResolved.Port)
	}
	if aliasResolved.Key != fullResolved.Key {
		t.Errorf("Key mismatch: alias=%q full=%q", aliasResolved.Key, fullResolved.Key)
	}
	if aliasResolved.JumpHost != fullResolved.JumpHost {
		t.Errorf("JumpHost mismatch: alias=%q full=%q", aliasResolved.JumpHost, fullResolved.JumpHost)
	}
	if !reflect.DeepEqual(aliasResolved.ClusterNames, fullResolved.ClusterNames) {
		t.Errorf("ClusterNames mismatch: alias=%v full=%v", aliasResolved.ClusterNames, fullResolved.ClusterNames)
	}

	// Provenance must differ.
	if aliasResolved.Provenance != ProvenanceAlias {
		t.Errorf("alias provenance: got %q, want %q", aliasResolved.Provenance, ProvenanceAlias)
	}
	if fullResolved.Provenance != ProvenanceFull {
		t.Errorf("full provenance: got %q, want %q", fullResolved.Provenance, ProvenanceFull)
	}
}

// TestUnmarshalYAML_Provenance verifies that UnmarshalYAML sets Provenance correctly
// for both bare-string and object forms.
func TestUnmarshalYAML_Provenance(t *testing.T) {
	type clusterHosts struct {
		Hosts []HostEntry `yaml:"hosts"`
	}

	input := `
hosts:
  - web-01.example.com
  - name: db-01.example.com
    user: postgres
  - name: only-name.example.com
`
	var ch clusterHosts
	if err := yaml.Unmarshal([]byte(input), &ch); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if len(ch.Hosts) != 3 {
		t.Fatalf("expected 3 hosts, got %d", len(ch.Hosts))
	}

	// First host: bare string alias
	h0 := ch.Hosts[0]
	if h0.Name != "web-01.example.com" {
		t.Errorf("hosts[0].Name: got %q, want %q", h0.Name, "web-01.example.com")
	}
	if h0.Provenance != ProvenanceAlias {
		t.Errorf("hosts[0].Provenance: got %q, want %q", h0.Provenance, ProvenanceAlias)
	}

	// Second host: full object with overrides
	h1 := ch.Hosts[1]
	if h1.Name != "db-01.example.com" {
		t.Errorf("hosts[1].Name: got %q, want %q", h1.Name, "db-01.example.com")
	}
	if h1.Provenance != ProvenanceFull {
		t.Errorf("hosts[1].Provenance: got %q, want %q", h1.Provenance, ProvenanceFull)
	}
	if h1.User != "postgres" {
		t.Errorf("hosts[1].User: got %q, want %q", h1.User, "postgres")
	}

	// Third host: full object with only Name set
	h2 := ch.Hosts[2]
	if h2.Name != "only-name.example.com" {
		t.Errorf("hosts[2].Name: got %q, want %q", h2.Name, "only-name.example.com")
	}
	if h2.Provenance != ProvenanceFull {
		t.Errorf("hosts[2].Provenance: got %q, want %q", h2.Provenance, ProvenanceFull)
	}
}

// TestResolveDefaultMerging verifies that cluster defaults are merged into
// per-host fields when no per-host override is provided.
func TestResolveDefaultMerging(t *testing.T) {
	defaults := HostDefaults{
		User:     "ubuntu",
		Port:     2222,
		Key:      "~/.ssh/cluster_key",
		JumpHost: "bastion.example.com",
	}

	// Host with no overrides — should inherit all defaults.
	entry := HostEntry{
		Name:       "host.example.com",
		Provenance: ProvenanceAlias,
	}
	r := entry.Resolve("mycluster", defaults)

	if r.User != "ubuntu" {
		t.Errorf("User: got %q, want %q", r.User, "ubuntu")
	}
	if r.Port != 2222 {
		t.Errorf("Port: got %d, want %d", r.Port, 2222)
	}
	if r.Key != "~/.ssh/cluster_key" {
		t.Errorf("Key: got %q, want %q", r.Key, "~/.ssh/cluster_key")
	}
	if r.JumpHost != "bastion.example.com" {
		t.Errorf("JumpHost: got %q, want %q", r.JumpHost, "bastion.example.com")
	}
	if len(r.ClusterNames) != 1 || r.ClusterNames[0] != "mycluster" {
		t.Errorf("ClusterNames: got %v, want [mycluster]", r.ClusterNames)
	}
}

// TestResolvePerHostOverride verifies that per-host fields take precedence over defaults.
func TestResolvePerHostOverride(t *testing.T) {
	defaults := HostDefaults{
		User: "ubuntu",
		Port: 22,
		Key:  "~/.ssh/id_rsa",
	}

	entry := HostEntry{
		Name:       "db.example.com",
		User:       "postgres",
		Port:       5432,
		Provenance: ProvenanceFull,
	}
	r := entry.Resolve("db-cluster", defaults)

	if r.User != "postgres" {
		t.Errorf("User: got %q, want %q", r.User, "postgres")
	}
	if r.Port != 5432 {
		t.Errorf("Port: got %d, want %d", r.Port, 5432)
	}
	// Key not set in entry, should fall back to default.
	if r.Key != "~/.ssh/id_rsa" {
		t.Errorf("Key: got %q, want %q", r.Key, "~/.ssh/id_rsa")
	}
}

// TestProvenanceRoundTripViaYAML verifies that a bare string and a name-only object
// produce the same Resolve output (except Provenance) when parsed from YAML.
func TestProvenanceRoundTripViaYAML(t *testing.T) {
	type clusterHosts struct {
		Hosts []HostEntry `yaml:"hosts"`
	}

	input := `
hosts:
  - myhost.example.com
  - name: myhost.example.com
`
	var ch clusterHosts
	if err := yaml.Unmarshal([]byte(input), &ch); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if len(ch.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(ch.Hosts))
	}

	defaults := HostDefaults{User: "ec2-user", Port: 22}
	aliasResolved := ch.Hosts[0].Resolve("test", defaults)
	fullResolved := ch.Hosts[1].Resolve("test", defaults)

	// All fields except Provenance should be equal.
	if aliasResolved.Host != fullResolved.Host {
		t.Errorf("Host mismatch: %q vs %q", aliasResolved.Host, fullResolved.Host)
	}
	if aliasResolved.User != fullResolved.User {
		t.Errorf("User mismatch: %q vs %q", aliasResolved.User, fullResolved.User)
	}
	if aliasResolved.Port != fullResolved.Port {
		t.Errorf("Port mismatch: %d vs %d", aliasResolved.Port, fullResolved.Port)
	}
	if aliasResolved.Key != fullResolved.Key {
		t.Errorf("Key mismatch: %q vs %q", aliasResolved.Key, fullResolved.Key)
	}
	if aliasResolved.JumpHost != fullResolved.JumpHost {
		t.Errorf("JumpHost mismatch: %q vs %q", aliasResolved.JumpHost, fullResolved.JumpHost)
	}
	if !reflect.DeepEqual(aliasResolved.ClusterNames, fullResolved.ClusterNames) {
		t.Errorf("ClusterNames mismatch: %v vs %v", aliasResolved.ClusterNames, fullResolved.ClusterNames)
	}

	// Provenance must differ.
	if aliasResolved.Provenance != ProvenanceAlias {
		t.Errorf("alias entry provenance: got %q, want %q", aliasResolved.Provenance, ProvenanceAlias)
	}
	if fullResolved.Provenance != ProvenanceFull {
		t.Errorf("full entry provenance: got %q, want %q", fullResolved.Provenance, ProvenanceFull)
	}
}

// TestResolveSingleClusterPopulatesClusterNames verifies that Resolve() populates
// the ClusterNames slice with a single element equal to the cluster argument.
func TestResolveSingleClusterPopulatesClusterNames(t *testing.T) {
	entry := HostEntry{Name: "web-01.example.com", Provenance: ProvenanceAlias}
	r := entry.Resolve("production", HostDefaults{User: "ubuntu"})

	if len(r.ClusterNames) != 1 {
		t.Fatalf("ClusterNames len: got %d, want 1", len(r.ClusterNames))
	}
	if r.ClusterNames[0] != "production" {
		t.Errorf("ClusterNames[0]: got %q, want %q", r.ClusterNames[0], "production")
	}
}

// TestAllResolvedHostsAggregatesClusterNames verifies that when the same SSH alias
// appears in multiple clusters AllResolvedHosts returns a single ResolvedHost
// whose ClusterNames slice contains all cluster names, not just the first one.
func TestAllResolvedHostsAggregatesClusterNames(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"alpha": {
				Defaults: HostDefaults{User: "ubuntu"},
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
					{Name: "alpha-only.example.com", Provenance: ProvenanceAlias},
				},
			},
			"beta": {
				Defaults: HostDefaults{User: "ec2-user"},
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
					{Name: "beta-only.example.com", Provenance: ProvenanceAlias},
				},
			},
			"gamma": {
				Defaults: HostDefaults{User: "admin"},
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
				},
			},
		},
	}

	all := cfg.AllResolvedHosts()

	// Expect 3 unique hosts: shared, alpha-only, beta-only.
	if len(all) != 3 {
		t.Fatalf("AllResolvedHosts len: got %d, want 3", len(all))
	}

	// Find shared.example.com in the result.
	var shared *ResolvedHost
	for i := range all {
		if all[i].Host == "shared.example.com" {
			shared = &all[i]
			break
		}
	}
	if shared == nil {
		t.Fatal("shared.example.com not found in AllResolvedHosts result")
	}

	// shared must have all three cluster names aggregated.
	if len(shared.ClusterNames) != 3 {
		t.Fatalf("shared.ClusterNames len: got %d, want 3 (alpha, beta, gamma)", len(shared.ClusterNames))
	}

	wantClusters := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, c := range shared.ClusterNames {
		if !wantClusters[c] {
			t.Errorf("unexpected cluster in shared.ClusterNames: %q", c)
		}
		delete(wantClusters, c)
	}
	for missing := range wantClusters {
		t.Errorf("cluster %q missing from shared.ClusterNames", missing)
	}

	// The primary resolution came from "alpha" (lexicographically first).
	if shared.ClusterNames[0] != "alpha" {
		t.Errorf("shared.ClusterNames[0]: got %q, want %q (lexicographically first)", shared.ClusterNames[0], "alpha")
	}

	// Unique hosts must have a single-element ClusterNames slice.
	for _, r := range all {
		if r.Host == "shared.example.com" {
			continue
		}
		if len(r.ClusterNames) != 1 {
			t.Errorf("host %q ClusterNames len: got %d, want 1", r.Host, len(r.ClusterNames))
		}
	}
}

// TestAllResolvedHostsOrderDeterministic verifies that AllResolvedHosts processes
// clusters in sorted order so the first-seen resolution is deterministic across
// repeated calls.
func TestAllResolvedHostsOrderDeterministic(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"zzz": {
				Defaults: HostDefaults{User: "last"},
				Hosts:    []HostEntry{{Name: "shared.example.com", Provenance: ProvenanceAlias}},
			},
			"aaa": {
				Defaults: HostDefaults{User: "first"},
				Hosts:    []HostEntry{{Name: "shared.example.com", Provenance: ProvenanceAlias}},
			},
		},
	}

	all := cfg.AllResolvedHosts()
	if len(all) != 1 {
		t.Fatalf("expected 1 unique host, got %d", len(all))
	}

	// Because "aaa" < "zzz", the primary resolution should use "aaa"'s defaults.
	r := all[0]
	if r.ClusterNames[0] != "aaa" {
		t.Errorf("ClusterNames[0]: got %q, want %q (lexicographically first)", r.ClusterNames[0], "aaa")
	}
	if r.User != "first" {
		t.Errorf("User: got %q, want %q (should come from aaa's defaults)", r.User, "first")
	}
	if len(r.ClusterNames) != 2 {
		t.Fatalf("ClusterNames len: got %d, want 2", len(r.ClusterNames))
	}
}

// TestAllClustersForHostMultiCluster verifies that AllClustersForHost returns
// all cluster names (sorted) when the same SSH alias appears in more than one
// cluster, exactly one name for a unique host, and an empty non-nil slice for
// an unknown alias.
func TestAllClustersForHostMultiCluster(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"alpha": {
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
					{Name: "alpha-only.example.com", Provenance: ProvenanceAlias},
				},
			},
			"beta": {
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
					{Name: "beta-only.example.com", Provenance: ProvenanceAlias},
				},
			},
			"gamma": {
				Hosts: []HostEntry{
					{Name: "shared.example.com", Provenance: ProvenanceAlias},
				},
			},
		},
	}

	// shared.example.com appears in alpha, beta, gamma → sorted result.
	clusters := cfg.AllClustersForHost("shared.example.com")
	if len(clusters) != 3 {
		t.Fatalf("AllClustersForHost(shared): got %v (len %d), want 3 elements", clusters, len(clusters))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, got := range clusters {
		if got != want[i] {
			t.Errorf("clusters[%d]: got %q, want %q", i, got, want[i])
		}
	}

	// alpha-only.example.com appears in alpha only.
	alphaOnly := cfg.AllClustersForHost("alpha-only.example.com")
	if len(alphaOnly) != 1 || alphaOnly[0] != "alpha" {
		t.Errorf("AllClustersForHost(alpha-only): got %v, want [alpha]", alphaOnly)
	}

	// Unknown alias → empty non-nil slice.
	unknown := cfg.AllClustersForHost("no-such-host.example.com")
	if unknown == nil {
		t.Error("AllClustersForHost(unknown): got nil, want empty non-nil slice")
	}
	if len(unknown) != 0 {
		t.Errorf("AllClustersForHost(unknown): got %v, want []", unknown)
	}
}

// TestLargeSelectionThresholdDefaultIs50 verifies that a Config with
// LargeSelectionThreshold unset (zero) returns an effective threshold of 50.
func TestLargeSelectionThresholdDefaultIs50(t *testing.T) {
	cfg := &Config{}
	if got := cfg.EffectiveConfirmThreshold(); got != 50 {
		t.Errorf("default EffectiveConfirmThreshold = %d; want 50", got)
	}
}

// TestLargeSelectionThresholdCustomValue verifies that a non-zero
// LargeSelectionThreshold value is used instead of the default 50.
func TestLargeSelectionThresholdCustomValue(t *testing.T) {
	cfg := &Config{LargeSelectionThreshold: 10}
	if got := cfg.EffectiveConfirmThreshold(); got != 10 {
		t.Errorf("EffectiveConfirmThreshold = %d; want 10", got)
	}
}

// TestLargeSelectionThresholdParsedFromYAML verifies that large_selection_threshold
// is correctly parsed from a YAML config string.
func TestLargeSelectionThresholdParsedFromYAML(t *testing.T) {
	input := `large_selection_threshold: 25
clusters: {}
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if cfg.LargeSelectionThreshold != 25 {
		t.Errorf("LargeSelectionThreshold = %d; want 25", cfg.LargeSelectionThreshold)
	}
	if got := cfg.EffectiveConfirmThreshold(); got != 25 {
		t.Errorf("EffectiveConfirmThreshold = %d; want 25", got)
	}
}

// TestLargeSelectionThresholdAbsentDefaultsTo50 verifies that when
// large_selection_threshold is absent from config.yaml the effective value is 50.
func TestLargeSelectionThresholdAbsentDefaultsTo50(t *testing.T) {
	input := `clusters: {}
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if cfg.LargeSelectionThreshold != 0 {
		t.Errorf("LargeSelectionThreshold = %d; want 0 (absent)", cfg.LargeSelectionThreshold)
	}
	if got := cfg.EffectiveConfirmThreshold(); got != 50 {
		t.Errorf("EffectiveConfirmThreshold = %d; want 50 (default)", got)
	}
}

// ---------------------------------------------------------------------------
// AC 17 — missing config message includes large_selection_threshold
// ---------------------------------------------------------------------------

// TestExampleConfigContainsLargeSelectionThreshold verifies that ExampleConfig
// (the YAML shown to users when config is missing) includes the
// large_selection_threshold key so new users know it is configurable.
func TestExampleConfigContainsLargeSelectionThreshold(t *testing.T) {
	if !strings.Contains(ExampleConfig, "large_selection_threshold") {
		t.Error("ExampleConfig does not contain 'large_selection_threshold' — the missing-config YAML example must document this key")
	}
}

// TestExampleConfigLargeSelectionThresholdIsValidYAML verifies that ExampleConfig
// is valid YAML and that its large_selection_threshold value can be parsed.
func TestExampleConfigLargeSelectionThresholdIsValidYAML(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(ExampleConfig), &cfg); err != nil {
		t.Fatalf("ExampleConfig is not valid YAML: %v", err)
	}
	if cfg.LargeSelectionThreshold != 50 {
		t.Errorf("ExampleConfig large_selection_threshold = %d; want 50", cfg.LargeSelectionThreshold)
	}
}

// TestInternalIPUnmarshalYAML verifies that the internal_ip field is decoded
// from the object form of a host entry.
func TestInternalIPUnmarshalYAML(t *testing.T) {
	type clusterHosts struct {
		Hosts []HostEntry `yaml:"hosts"`
	}
	input := `
hosts:
  - name: spoke-01.example.com
    internal_ip: 10.0.0.1
  - web-01.example.com
`
	var ch clusterHosts
	if err := yaml.Unmarshal([]byte(input), &ch); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ch.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(ch.Hosts))
	}
	if ch.Hosts[0].InternalIP != "10.0.0.1" {
		t.Errorf("InternalIP: got %q, want %q", ch.Hosts[0].InternalIP, "10.0.0.1")
	}
	if ch.Hosts[1].InternalIP != "" {
		t.Errorf("alias host InternalIP: got %q, want empty", ch.Hosts[1].InternalIP)
	}
}

// TestInternalIPResolve verifies that Resolve() copies InternalIP into ResolvedHost.
func TestInternalIPResolve(t *testing.T) {
	entry := HostEntry{
		Name:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
		Provenance: ProvenanceFull,
	}
	resolved := entry.Resolve("prod", HostDefaults{User: "ubuntu"})
	if resolved.InternalIP != "10.0.0.1" {
		t.Errorf("InternalIP: got %q, want %q", resolved.InternalIP, "10.0.0.1")
	}
}

// TestEffectiveAddress_WithInternalIP verifies that EffectiveAddress always
// returns Host, even when InternalIP is set. Internal IPs are exclusively
// for inter-host spoke-pull communication (resolved at runtime via
// executor.ResolvePrivateIP), never for local→host connections.
func TestEffectiveAddress_WithInternalIP(t *testing.T) {
	h := ResolvedHost{Host: "spoke-01.example.com", InternalIP: "10.0.0.1"}
	if got := h.EffectiveAddress(); got != "spoke-01.example.com" {
		t.Errorf("EffectiveAddress: got %q, want %q (should always return Host)", got, "spoke-01.example.com")
	}
}

// TestEffectiveAddress_WithoutInternalIP verifies that EffectiveAddress falls
// back to Host when InternalIP is empty.
func TestEffectiveAddress_WithoutInternalIP(t *testing.T) {
	h := ResolvedHost{Host: "web-01.example.com"}
	if got := h.EffectiveAddress(); got != "web-01.example.com" {
		t.Errorf("EffectiveAddress: got %q, want %q", got, "web-01.example.com")
	}
}

// TestRemoteReachableAddress_WithInternalIP verifies that RemoteReachableAddress
// returns InternalIP when it is set (same as EffectiveAddress).
func TestRemoteReachableAddress_WithInternalIP(t *testing.T) {
	h := ResolvedHost{Host: "spoke-01.example.com", InternalIP: "10.0.0.1"}
	if got := h.RemoteReachableAddress(); got != "10.0.0.1" {
		t.Errorf("RemoteReachableAddress: got %q, want %q", got, "10.0.0.1")
	}
}

// TestRemoteReachableAddress_WithoutInternalIP_NoSSHConfig verifies that
// RemoteReachableAddress falls back to Host when InternalIP is empty and
// no SSH config Hostname directive is found.
func TestRemoteReachableAddress_WithoutInternalIP_NoSSHConfig(t *testing.T) {
	// Use a host name that won't appear in any real SSH config.
	h := ResolvedHost{Host: "nonexistent-host-xyz-12345.example.com"}
	if got := h.RemoteReachableAddress(); got != "nonexistent-host-xyz-12345.example.com" {
		t.Errorf("RemoteReachableAddress: got %q, want %q", got, "nonexistent-host-xyz-12345.example.com")
	}
}

// TestAllClustersForHostSingleCluster verifies that AllClustersForHost returns
// a slice of length 1 when the alias exists in exactly one cluster.
func TestAllClustersForHostSingleCluster(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"production": {
				Hosts: []HostEntry{
					{Name: "web-01.prod", Provenance: ProvenanceAlias},
				},
			},
		},
	}

	clusters := cfg.AllClustersForHost("web-01.prod")
	if len(clusters) != 1 {
		t.Fatalf("AllClustersForHost: got len %d, want 1", len(clusters))
	}
	if clusters[0] != "production" {
		t.Errorf("AllClustersForHost: got %q, want %q", clusters[0], "production")
	}
}

// ---------------------------------------------------------------------------
// Subgroup tests
// ---------------------------------------------------------------------------

// TestSubgroupYAMLParsing verifies that a cluster with subgroups parses correctly.
func TestSubgroupYAMLParsing(t *testing.T) {
	input := `
clusters:
  gpu-cluster:
    defaults:
      user: root
      key: ~/.ssh/id.pem
    subgroups:
      rack-a:
        internal_cidr: 10.0.1.0/24
        hosts:
          - host-a1
          - host-a2
      rack-b:
        internal_ip_base: "10.0.2.{10+$index}"
        hosts:
          - host-b1
          - host-b2
          - host-b3
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cluster, ok := cfg.Clusters["gpu-cluster"]
	if !ok {
		t.Fatal("cluster 'gpu-cluster' not found")
	}
	if len(cluster.Hosts) != 0 {
		t.Errorf("flat Hosts should be empty; got %d", len(cluster.Hosts))
	}
	if len(cluster.Subgroups) != 2 {
		t.Fatalf("expected 2 subgroups; got %d", len(cluster.Subgroups))
	}
	rackA := cluster.Subgroups["rack-a"]
	if rackA.InternalCIDR != "10.0.1.0/24" {
		t.Errorf("rack-a InternalCIDR: got %q", rackA.InternalCIDR)
	}
	if len(rackA.Hosts) != 2 {
		t.Errorf("rack-a hosts: got %d, want 2", len(rackA.Hosts))
	}
	rackB := cluster.Subgroups["rack-b"]
	if rackB.InternalIPBase != "10.0.2.{10+$index}" {
		t.Errorf("rack-b InternalIPBase: got %q", rackB.InternalIPBase)
	}
}

// TestAllResolvedHosts_WithSubgroups verifies that subgroup hosts are flattened
// into the result with correct SubgroupName and InternalCIDR.
func TestAllResolvedHosts_WithSubgroups(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"cluster-1": {
				Defaults: HostDefaults{User: "root"},
				Subgroups: map[string]SubgroupConfig{
					"rack-a": {
						InternalCIDR: "10.0.1.0/24",
						Hosts: []HostEntry{
							{Name: "a1", Provenance: ProvenanceAlias},
							{Name: "a2", Provenance: ProvenanceAlias},
						},
					},
					"rack-b": {
						InternalCIDR: "10.0.2.0/24",
						Hosts: []HostEntry{
							{Name: "b1", Provenance: ProvenanceAlias},
						},
					},
				},
			},
		},
	}

	hosts := cfg.AllResolvedHosts()
	if len(hosts) != 3 {
		t.Fatalf("expected 3 hosts; got %d", len(hosts))
	}

	// Subgroups sorted: rack-a before rack-b.
	if hosts[0].Host != "a1" || hosts[0].SubgroupName != "rack-a" {
		t.Errorf("hosts[0]: got %q subgroup=%q", hosts[0].Host, hosts[0].SubgroupName)
	}
	if hosts[0].InternalCIDR != "10.0.1.0/24" {
		t.Errorf("hosts[0].InternalCIDR: got %q", hosts[0].InternalCIDR)
	}
	if hosts[2].Host != "b1" || hosts[2].SubgroupName != "rack-b" {
		t.Errorf("hosts[2]: got %q subgroup=%q", hosts[2].Host, hosts[2].SubgroupName)
	}
	if hosts[0].User != "root" {
		t.Errorf("cluster defaults should cascade: got user=%q", hosts[0].User)
	}
}

// TestResolveIPBase verifies the IP base template resolution.
func TestResolveIPBase(t *testing.T) {
	tests := []struct {
		template string
		index    int
		want     string
		wantErr  bool
	}{
		{"10.61.3.{191+$index}", 0, "10.61.3.191", false},
		{"10.61.3.{191+$index}", 3, "10.61.3.194", false},
		{"10.0.0.{40+$index}", 5, "10.0.0.45", false},
		{"10.0.0.{250+$index}", 6, "", true},  // 256 > 255
		{"10.0.0.{$index}", 42, "10.0.0.42", false},
		{"bad-template", 0, "", true},
	}

	for _, tt := range tests {
		got, err := resolveIPBase(tt.template, tt.index)
		if tt.wantErr {
			if err == nil {
				t.Errorf("resolveIPBase(%q, %d): expected error", tt.template, tt.index)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveIPBase(%q, %d): unexpected error: %v", tt.template, tt.index, err)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveIPBase(%q, %d): got %q, want %q", tt.template, tt.index, got, tt.want)
		}
	}
}

// TestSubgroup_InternalIPBase_OverriddenByPerHost verifies that ip_base
// resolution is deferred: hosts store InternalIPBase and IPBaseIndex for
// runtime resolution at spoke-pull time. A per-host internal_ip override
// is still preserved in InternalIP.
func TestSubgroup_InternalIPBase_OverriddenByPerHost(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"test": {
				Defaults: HostDefaults{User: "root"},
				Subgroups: map[string]SubgroupConfig{
					"sg": {
						InternalIPBase: "10.0.0.{100+$index}",
						Hosts: []HostEntry{
							{Name: "h0", Provenance: ProvenanceAlias},                         // index 0
							{Name: "h1", InternalIP: "192.168.1.1", Provenance: ProvenanceFull}, // override
						},
					},
				},
			},
		},
	}

	hosts := cfg.AllResolvedHosts()
	// h0: no per-host override — InternalIPBase and IPBaseIndex stored for deferred resolution.
	if hosts[0].InternalIPBase != "10.0.0.{100+$index}" {
		t.Errorf("h0 InternalIPBase: got %q, want %q", hosts[0].InternalIPBase, "10.0.0.{100+$index}")
	}
	if hosts[0].IPBaseIndex != 0 {
		t.Errorf("h0 IPBaseIndex: got %d, want 0", hosts[0].IPBaseIndex)
	}
	// h1: per-host internal_ip override is preserved.
	if hosts[1].InternalIP != "192.168.1.1" {
		t.Errorf("h1 should keep per-host override; got %q", hosts[1].InternalIP)
	}
	if hosts[1].IPBaseIndex != 1 {
		t.Errorf("h1 IPBaseIndex: got %d, want 1", hosts[1].IPBaseIndex)
	}
}

// TestValidation_MixedHostsAndSubgroups verifies that a cluster with both
// hosts and subgroups is rejected.
func TestValidation_MixedHostsAndSubgroups(t *testing.T) {
	input := `
clusters:
  bad-cluster:
    hosts:
      - host-1
    subgroups:
      sg:
        hosts:
          - host-2
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := cfg.validate(); err == nil {
		t.Error("expected validation error for mixed hosts+subgroups")
	}
}

// TestAllClustersForHost_InSubgroup verifies that AllClustersForHost finds
// hosts inside subgroups.
func TestAllClustersForHost_InSubgroup(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"my-cluster": {
				Subgroups: map[string]SubgroupConfig{
					"sg1": {
						Hosts: []HostEntry{{Name: "deep-host", Provenance: ProvenanceAlias}},
					},
				},
			},
		},
	}

	clusters := cfg.AllClustersForHost("deep-host")
	if len(clusters) != 1 || clusters[0] != "my-cluster" {
		t.Errorf("expected [my-cluster]; got %v", clusters)
	}
}

// TestClusterDefaults_InternalIPBase verifies that internal_ip_base at the
// cluster defaults level stores InternalIPBase and IPBaseIndex on each host
// for deferred resolution at spoke-pull time (not eagerly resolved).
func TestClusterDefaults_InternalIPBase(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"cpu-nodes": {
				Defaults: HostDefaults{
					User:           "root",
					InternalIPBase: "10.61.3.{191+$index}",
				},
				Hosts: []HostEntry{
					{Name: "cpu-05", Provenance: ProvenanceAlias},
					{Name: "cpu-06", Provenance: ProvenanceAlias},
					{Name: "cpu-07", Provenance: ProvenanceAlias},
					{Name: "cpu-08", Provenance: ProvenanceAlias},
				},
			},
		},
	}

	hosts := cfg.AllResolvedHosts()
	if len(hosts) != 4 {
		t.Fatalf("expected 4 hosts; got %d", len(hosts))
	}
	for i, h := range hosts {
		if h.InternalIPBase != "10.61.3.{191+$index}" {
			t.Errorf("host %d (%s): InternalIPBase got %q, want %q", i, h.Host, h.InternalIPBase, "10.61.3.{191+$index}")
		}
		if h.IPBaseIndex != i {
			t.Errorf("host %d (%s): IPBaseIndex got %d, want %d", i, h.Host, h.IPBaseIndex, i)
		}
	}
}

// TestBackwardCompat_FlatCluster verifies that a cluster with only flat hosts
// continues to work exactly as before.
func TestBackwardCompat_FlatCluster(t *testing.T) {
	cfg := &Config{
		Clusters: map[string]ClusterConfig{
			"simple": {
				Defaults: HostDefaults{User: "ubuntu"},
				Hosts: []HostEntry{
					{Name: "web-01", Provenance: ProvenanceAlias},
					{Name: "web-02", Provenance: ProvenanceAlias},
				},
			},
		},
	}

	hosts := cfg.AllResolvedHosts()
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts; got %d", len(hosts))
	}
	if hosts[0].SubgroupName != "" {
		t.Errorf("flat cluster hosts should have empty SubgroupName; got %q", hosts[0].SubgroupName)
	}
	if hosts[0].User != "ubuntu" {
		t.Errorf("defaults should cascade; got user=%q", hosts[0].User)
	}
}
