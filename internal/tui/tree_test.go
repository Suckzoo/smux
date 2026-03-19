package tui

import (
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// makeTestConfig builds a minimal Config with two clusters for tree tests.
func makeTestConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"production": {
				Defaults: config.HostDefaults{User: "ubuntu"},
				Hosts: []config.HostEntry{
					{Name: "web-01.prod", Provenance: config.ProvenanceFull},
					{Name: "web-02.prod", Provenance: config.ProvenanceFull},
				},
			},
			"staging": {
				Defaults: config.HostDefaults{User: "ubuntu"},
				Hosts: []config.HostEntry{
					{Name: "web-01.staging", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
}

// TestBuildFlatListAllExpanded verifies that when all clusters are expanded,
// cluster header nodes are present and each cluster's host nodes appear
// immediately after their parent cluster node.
func TestBuildFlatListAllExpanded(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	nodes := BuildFlatList(cfg, &state, "")

	// With 2 clusters (production: 2 hosts, staging: 1 host) all expanded:
	// production cluster header + 2 hosts + staging cluster header + 1 host = 5
	if len(nodes) != 5 {
		t.Fatalf("expected 5 nodes (2 cluster + 3 host), got %d", len(nodes))
	}

	// First node must be the "production" cluster header (ClusterNames() is sorted).
	if !nodes[0].IsCluster() || nodes[0].ClusterName != "production" {
		t.Errorf("nodes[0] should be production cluster header, got %+v", nodes[0])
	}
	// Next two must be host nodes under production.
	if !nodes[1].IsHost() || nodes[1].ClusterName != "production" {
		t.Errorf("nodes[1] should be production host, got %+v", nodes[1])
	}
	if !nodes[2].IsHost() || nodes[2].ClusterName != "production" {
		t.Errorf("nodes[2] should be production host, got %+v", nodes[2])
	}
	// Then the staging cluster header.
	if !nodes[3].IsCluster() || nodes[3].ClusterName != "staging" {
		t.Errorf("nodes[3] should be staging cluster header, got %+v", nodes[3])
	}
	// Last is the staging host.
	if !nodes[4].IsHost() || nodes[4].ClusterName != "staging" {
		t.Errorf("nodes[4] should be staging host, got %+v", nodes[4])
	}
}

// TestBuildFlatListCollapsed verifies that collapsing a cluster hides its host
// nodes from the flat list while keeping the cluster header visible.
func TestBuildFlatListCollapsed(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// Collapse production cluster.
	state.SetExpanded("production", false)

	nodes := BuildFlatList(cfg, &state, "")

	// production (collapsed, no hosts) + staging header + 1 host = 3 nodes
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes after collapsing production, got %d", len(nodes))
	}

	// production header still visible.
	if !nodes[0].IsCluster() || nodes[0].ClusterName != "production" {
		t.Errorf("nodes[0] should be production cluster header, got %+v", nodes[0])
	}
	// Followed directly by staging header (no production hosts).
	if !nodes[1].IsCluster() || nodes[1].ClusterName != "staging" {
		t.Errorf("nodes[1] should be staging cluster header, got %+v", nodes[1])
	}
	// Then staging host.
	if !nodes[2].IsHost() || nodes[2].ClusterName != "staging" {
		t.Errorf("nodes[2] should be staging host, got %+v", nodes[2])
	}
}

// TestBuildFlatListAllCollapsed verifies that collapsing all clusters leaves only
// the cluster header nodes.
func TestBuildFlatListAllCollapsed(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	state.SetExpanded("production", false)
	state.SetExpanded("staging", false)

	nodes := BuildFlatList(cfg, &state, "")

	// Only the 2 cluster headers should be visible.
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes (only cluster headers), got %d", len(nodes))
	}
	for _, n := range nodes {
		if !n.IsCluster() {
			t.Errorf("expected only cluster nodes when all collapsed, got %+v", n)
		}
	}
}

// TestTreeStateToggle verifies that TreeState.Toggle flips expansion state.
func TestTreeStateToggle(t *testing.T) {
	state := NewTreeState([]string{"mycluster"})

	if !state.IsExpanded("mycluster") {
		t.Error("new cluster should start expanded")
	}

	state.Toggle("mycluster")
	if state.IsExpanded("mycluster") {
		t.Error("after first toggle, cluster should be collapsed")
	}

	state.Toggle("mycluster")
	if !state.IsExpanded("mycluster") {
		t.Error("after second toggle, cluster should be expanded again")
	}
}

// TestBuildFlatListFilterForcesExpansion verifies that an active filter exposes
// matching hosts even when the cluster is collapsed.
func TestBuildFlatListFilterForcesExpansion(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// Collapse everything.
	state.SetExpanded("production", false)
	state.SetExpanded("staging", false)

	// Filter for "prod" — should match production cluster + its hosts.
	nodes := BuildFlatList(cfg, &state, "prod")

	if len(nodes) == 0 {
		t.Fatal("expected matching nodes with filter 'prod', got none")
	}

	// All returned nodes must involve "production" (filter matches that cluster).
	for _, n := range nodes {
		if n.ClusterName != "production" {
			t.Errorf("expected only production nodes with filter 'prod', got cluster %q", n.ClusterName)
		}
	}

	// First node must be the cluster header.
	if !nodes[0].IsCluster() {
		t.Errorf("first node with active filter should be cluster header, got %+v", nodes[0])
	}

	// Host nodes must follow.
	hostCount := 0
	for _, n := range nodes[1:] {
		if n.IsHost() {
			hostCount++
		}
	}
	if hostCount == 0 {
		t.Error("filter-forced expansion should show host nodes")
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 2 – real-time fuzzy matching against both cluster names and host names
// ---------------------------------------------------------------------------

// TestFuzzyMatchBasicSubsequence verifies core subsequence matching: every
// character in the pattern must appear in the target in order.
func TestFuzzyMatchBasicSubsequence(t *testing.T) {
	cases := []struct {
		pattern string
		target  string
		want    bool
	}{
		{"prod", "production", true},
		{"prd", "production", true},   // non-contiguous
		{"pn", "production", true},    // skips chars
		{"xyz", "production", false},  // no match
		{"", "production", true},      // empty pattern always matches
		{"PROD", "production", true},  // case-insensitive
		{"prod", "PRODUCTION", true},  // case-insensitive target
		{"staging", "staging", true},  // exact match
		{"stagng", "staging", true},   // non-contiguous
		{"x", "staging", false},
		{"web01", "web-01.prod", true},  // subsequence: w,e,b,0,1 all present in order
		{"web01", "web01", true},
		{"w1", "web-01.prod", true},   // w then 1
		{"10", "web-01.prod", false},  // 1 comes after 0 in target; order violated
	}
	for _, tc := range cases {
		got := fuzzyMatch(tc.pattern, tc.target)
		if got != tc.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
		}
	}
}

// TestBuildFlatListFilterByClusterName verifies that a filter matching only
// the cluster name (not any host name) still returns the cluster header and
// all its hosts.
func TestBuildFlatListFilterByClusterName(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// "stg" fuzzy-matches "staging" but not "production".
	nodes := BuildFlatList(cfg, &state, "stg")

	// Must have at least a cluster header + one host.
	if len(nodes) < 2 {
		t.Fatalf("expected staging cluster + hosts with filter 'stg', got %d nodes", len(nodes))
	}

	// All nodes must belong to staging.
	for _, n := range nodes {
		if n.ClusterName != "staging" {
			t.Errorf("with filter 'stg' all nodes should be from staging, got %q", n.ClusterName)
		}
	}

	// First node must be the cluster header.
	if !nodes[0].IsCluster() {
		t.Errorf("first node should be cluster header, got %+v", nodes[0])
	}

	// There must be at least one host node.
	hostCount := 0
	for _, n := range nodes[1:] {
		if n.IsHost() {
			hostCount++
		}
	}
	if hostCount == 0 {
		t.Error("cluster-name filter should include host nodes under the matched cluster")
	}
}

// TestBuildFlatListFilterByHostName verifies that a filter matching only a
// host name (not the cluster name) returns the cluster header + matching hosts
// while omitting non-matching hosts.
func TestBuildFlatListFilterByHostName(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// "web01" fuzzy-matches "web-01.prod" and "web-01.staging" but neither
	// cluster name contains the character sequence "w...0...1".
	// The production cluster name "production" does not match "web01";
	// the staging cluster name "staging" does not match "web01" either.
	// However hosts "web-01.prod" and "web-01.staging" do match.
	nodes := BuildFlatList(cfg, &state, "web01")

	if len(nodes) == 0 {
		t.Fatal("expected nodes with host-name filter 'web01', got none")
	}

	// Every returned host node must have a DisplayName that fuzzy-matches "web01".
	for _, n := range nodes {
		if n.IsHost() {
			if !fuzzyMatch("web01", n.Host.DisplayName) {
				t.Errorf("host node %q should fuzzy-match filter 'web01'", n.Host.DisplayName)
			}
		}
	}
}

// TestBuildFlatListFilterMatchesBothClusterAndHost verifies that when a filter
// matches the cluster name, ALL hosts in that cluster appear — not just the
// ones whose host name also matches the filter.
func TestBuildFlatListFilterMatchesBothClusterAndHost(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// "production" filter: matches cluster "production" exactly. Both hosts in
	// that cluster should be present even if their names don't individually
	// match the filter.
	nodes := BuildFlatList(cfg, &state, "production")

	clusterHeaderFound := false
	hostCount := 0
	for _, n := range nodes {
		if n.IsCluster() && n.ClusterName == "production" {
			clusterHeaderFound = true
		}
		if n.IsHost() && n.ClusterName == "production" {
			hostCount++
		}
	}

	if !clusterHeaderFound {
		t.Error("production cluster header should be present when filter matches cluster name")
	}
	if hostCount != 2 {
		t.Errorf("all 2 production hosts should appear when cluster name matches, got %d", hostCount)
	}
}

// TestBuildFlatListFilterHidesNonMatchingClusters verifies that clusters with
// neither a matching name nor matching hosts are completely excluded from the
// flat list.
func TestBuildFlatListFilterHidesNonMatchingClusters(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	// "stg" matches "staging" but not "production".
	nodes := BuildFlatList(cfg, &state, "stg")

	for _, n := range nodes {
		if n.ClusterName == "production" {
			t.Errorf("production cluster should be hidden by filter 'stg', found node %+v", n)
		}
	}
}

// TestBuildFlatListFilterNoMatch verifies that a filter matching nothing
// returns an empty list.
func TestBuildFlatListFilterNoMatch(t *testing.T) {
	cfg := makeTestConfig()
	names := cfg.ClusterNames()
	state := NewTreeState(names)

	nodes := BuildFlatList(cfg, &state, "zzznomatch")

	if len(nodes) != 0 {
		t.Errorf("filter 'zzznomatch' should return 0 nodes, got %d", len(nodes))
	}
}

// TestBuildFlatListFilterPartialHostMatch verifies that within a cluster whose
// name does NOT match the filter, only the individual hosts whose names match
// are returned (not the non-matching sibling hosts).
func TestBuildFlatListFilterPartialHostMatch(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"mycluster": {
				Hosts: []config.HostEntry{
					{Name: "alpha-01", Provenance: config.ProvenanceFull},
					{Name: "beta-01", Provenance: config.ProvenanceFull},
					{Name: "alpha-02", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	state := NewTreeState(cfg.ClusterNames())

	// "alp" matches "alpha-01" and "alpha-02" but not "beta-01".
	// The cluster name "mycluster" does not match "alp" either.
	nodes := BuildFlatList(cfg, &state, "alp")

	if len(nodes) == 0 {
		t.Fatal("expected nodes with filter 'alp', got none")
	}

	// Cluster header must appear (because there are matching hosts).
	if !nodes[0].IsCluster() {
		t.Errorf("first node should be cluster header, got %+v", nodes[0])
	}

	// Count host nodes — must be exactly 2 (alpha-01, alpha-02).
	hostCount := 0
	for _, n := range nodes[1:] {
		if n.IsHost() {
			hostCount++
			if n.Host.DisplayName != "alpha-01" && n.Host.DisplayName != "alpha-02" {
				t.Errorf("unexpected host %q with filter 'alp'", n.Host.DisplayName)
			}
		}
	}
	if hostCount != 2 {
		t.Errorf("expected exactly 2 matching hosts with filter 'alp', got %d", hostCount)
	}
}

// TestModelFilterUpdatesListInRealTime verifies that the Model's flat node list
// is updated as the user types characters in the filter box. After activating
// filter mode with "/" and typing characters, only matching nodes should be
// visible.
func TestModelFilterUpdatesListInRealTime(t *testing.T) {
	// Build a config with distinct clusters so we can verify selective filtering.
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"production": {
				Hosts: []config.HostEntry{
					{Name: "web-01.prod", Provenance: config.ProvenanceFull},
					{Name: "web-02.prod", Provenance: config.ProvenanceFull},
				},
			},
			"staging": {
				Hosts: []config.HostEntry{
					{Name: "web-01.stg", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	// Initially all clusters + hosts are shown.
	initialCount := len(m.flatNodes)
	if initialCount == 0 {
		t.Fatal("expected non-empty flat list before filtering")
	}

	// Activate filter mode.
	m, _ = sendKey(m, "/")
	if !m.isFilterActive() {
		t.Fatal("pressing '/' should activate filter mode")
	}

	// Type "stg" — should narrow to staging only.
	for _, ch := range []string{"s", "t", "g"} {
		m, _ = sendKey(m, ch)
	}

	filteredCount := len(m.flatNodes)
	if filteredCount >= initialCount {
		t.Errorf("filtered list should have fewer nodes than unfiltered (%d >= %d)",
			filteredCount, initialCount)
	}

	// All remaining nodes must belong to staging.
	for _, n := range m.flatNodes {
		if n.ClusterName != "staging" {
			t.Errorf("after filter 'stg', all nodes should be from staging; got %q", n.ClusterName)
		}
	}
}

// TestModelFilterResetsOnEsc verifies that pressing Esc in filter mode clears
// the filter and restores the full node list.
func TestModelFilterResetsOnEsc(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	fullCount := len(m.flatNodes)

	// Enter filter mode and type something to narrow the list.
	m, _ = sendKey(m, "/")
	for _, ch := range []string{"s", "t", "g"} {
		m, _ = sendKey(m, ch)
	}
	if len(m.flatNodes) >= fullCount {
		t.Fatalf("expected filtering to reduce node count before Esc")
	}

	// Press Esc — should clear filter and restore full list.
	m, _ = sendKey(m, "esc")

	if m.isFilterActive() {
		t.Error("filter mode should be deactivated after Esc")
	}
	if m.filterInput.Value() != "" {
		t.Errorf("filter input should be empty after Esc, got %q", m.filterInput.Value())
	}
	if len(m.flatNodes) != fullCount {
		t.Errorf("node count should be restored to %d after Esc, got %d", fullCount, len(m.flatNodes))
	}
}

// TestModelFilterEnterConfirmsAndDeactivates verifies that pressing Enter in
// filter mode deactivates filter mode while keeping the typed filter value
// active (so the narrowed view persists for host selection).
func TestModelFilterEnterConfirmsAndDeactivates(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	// Activate filter and type a query.
	m, _ = sendKey(m, "/")
	for _, ch := range []string{"s", "t", "g"} {
		m, _ = sendKey(m, ch)
	}
	filteredCount := len(m.flatNodes)

	// Press Enter to confirm the filter.
	m, _ = sendKey(m, "enter")

	if m.isFilterActive() {
		t.Error("filter mode should be deactivated after Enter")
	}
	if m.filterInput.Value() != "stg" {
		t.Errorf("filter value should be preserved after Enter, got %q", m.filterInput.Value())
	}
	if len(m.flatNodes) != filteredCount {
		t.Errorf("filtered node count should be preserved after Enter: want %d, got %d",
			filteredCount, len(m.flatNodes))
	}
}

// TestRenderListHostsAreIndented verifies that host rows in the rendered list
// carry more leading whitespace than cluster header rows, establishing visual
// parent-child hierarchy.
func TestRenderListHostsAreIndented(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	// Set a known terminal size so View() doesn't bail out early.
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	lines := m.renderList()

	// Separate cluster-header lines from host lines by scanning flatNodes.
	for i, node := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		// Strip ANSI codes for measurement — use strings.TrimLeft for a simple
		// leading-space check on the raw text embedded in the line.
		raw := stripANSI(lines[i])
		if node.IsCluster() {
			// Cluster lines start with an arrow character (▶ or ▼), no leading spaces.
			if strings.HasPrefix(raw, "  ") {
				t.Errorf("cluster header line should not start with two spaces: %q", raw)
			}
		} else if node.IsHost() {
			// Host lines start with at least two leading spaces (indentation).
			if !strings.HasPrefix(raw, "  ") {
				t.Errorf("host line should start with at least two leading spaces: %q", raw)
			}
		}
	}
}

// TestClusterNodeShowsArrow verifies that cluster header rows contain the
// collapse/expand indicator character (▶ or ▼).
func TestClusterNodeShowsArrow(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	lines := m.renderList()

	for i, node := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if node.IsCluster() {
			raw := stripANSI(lines[i])
			if !strings.Contains(raw, "▶") && !strings.Contains(raw, "▼") {
				t.Errorf("cluster header line missing arrow indicator: %q", raw)
			}
		}
	}
}

// TestCollapsedClusterHidesHostsInRender verifies that after collapsing a cluster
// the view does not include host nodes for that cluster.
func TestCollapsedClusterHidesHostsInRender(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	// Collapse production.
	m.tree.SetExpanded("production", false)
	m.rebuildFlat()

	for _, node := range m.flatNodes {
		if node.IsHost() && node.ClusterName == "production" {
			t.Error("collapsed production cluster should not have host nodes in flatNodes")
		}
	}
}

// TestExpandedClusterShowsHostsInRender verifies that expanding a cluster
// makes its host nodes appear in the flat list.
func TestExpandedClusterShowsHostsInRender(t *testing.T) {
	cfg := makeTestConfig()
	m := New(cfg)
	m.view.Width = 80
	m.view.Height = 24
	m.viewport.Width = 80
	m.viewport.Height = 20

	// Collapse then re-expand production.
	m.tree.SetExpanded("production", false)
	m.rebuildFlat()
	m.tree.SetExpanded("production", true)
	m.rebuildFlat()

	hostCount := 0
	for _, node := range m.flatNodes {
		if node.IsHost() && node.ClusterName == "production" {
			hostCount++
		}
	}
	if hostCount != 2 {
		t.Errorf("expected 2 production host nodes after re-expanding, got %d", hostCount)
	}
}

// stripANSI removes ANSI escape sequences for plain-text comparisons.
// This is a simple implementation suitable for test assertions.
func stripANSI(s string) string {
	var out strings.Builder
	inSeq := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inSeq = true
		case inSeq && r == 'm':
			inSeq = false
		case inSeq:
			// skip
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
