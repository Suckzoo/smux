package tui

import (
	"strings"

	"github.com/Suckzoo/smux/internal/config"
)

// NodeKind distinguishes cluster header nodes from leaf host nodes.
type NodeKind int

const (
	// NodeKindCluster represents a collapsible cluster header row.
	NodeKindCluster NodeKind = iota
	// NodeKindSubgroup represents a collapsible subgroup header row nested
	// under a cluster. Subgroup nodes are only present when the cluster
	// uses the subgroups config form.
	NodeKindSubgroup
	// NodeKindHost represents a leaf host row nested under a cluster or subgroup.
	NodeKindHost
	// NodeKindLocal represents the synthetic "Local (this machine)" entry
	// in the source-origin picker; it is not associated with any cluster.
	NodeKindLocal
)

// TreeNode is a single displayable row in the cluster tree.
//
// Cluster nodes (NodeKindCluster) can be expanded or collapsed; their
// expanded/collapsed state is tracked by TreeState. Subgroup nodes
// (NodeKindSubgroup) are nested under clusters and also expandable.
// Host nodes (NodeKindHost) are children of a cluster or subgroup and
// are shown only when the parent is expanded (or when a filter forces
// expansion).
type TreeNode struct {
	Kind         NodeKind
	ClusterName  string               // set for cluster, subgroup, and host nodes
	SubgroupName string               // set for subgroup and host-under-subgroup nodes
	Host         *config.ResolvedHost // non-nil only for NodeKindHost
}

// IsCluster reports whether this node represents a cluster header.
func (n TreeNode) IsCluster() bool { return n.Kind == NodeKindCluster }

// IsSubgroup reports whether this node represents a subgroup header.
func (n TreeNode) IsSubgroup() bool { return n.Kind == NodeKindSubgroup }

// IsHost reports whether this node represents a leaf host entry.
func (n TreeNode) IsHost() bool { return n.Kind == NodeKindHost }

// IsLocal reports whether this node represents the "Local (this machine)" entry.
func (n TreeNode) IsLocal() bool { return n.Kind == NodeKindLocal }

// TreeState tracks the expanded/collapsed state for each cluster in the tree.
// It is embedded in the bubbletea Model so that the TUI can efficiently
// update and query expansion state without touching the underlying config.
type TreeState struct {
	expanded map[string]bool // cluster name → is expanded?
}

// NewTreeState creates a TreeState with all named clusters and subgroups
// initially expanded.
func NewTreeState(clusterNames []string) TreeState {
	expanded := make(map[string]bool, len(clusterNames))
	for _, name := range clusterNames {
		expanded[name] = true
	}
	return TreeState{expanded: expanded}
}

// NewTreeStateFromConfig creates a TreeState with all clusters and subgroups
// initially expanded. Subgroups use composite keys "cluster/subgroup".
func NewTreeStateFromConfig(cfg *config.Config) TreeState {
	expanded := make(map[string]bool)
	for _, name := range cfg.ClusterNames() {
		expanded[name] = true
		cluster := cfg.Clusters[name]
		for _, sgName := range cluster.SubgroupNames() {
			expanded[name+"/"+sgName] = true
		}
	}
	return TreeState{expanded: expanded}
}

// subgroupKey returns the composite key used for tracking subgroup expansion.
func subgroupKey(clusterName, subgroupName string) string {
	return clusterName + "/" + subgroupName
}

// IsExpanded reports whether the named cluster is currently expanded.
func (ts *TreeState) IsExpanded(clusterName string) bool {
	return ts.expanded[clusterName]
}

// SetExpanded sets the expanded state of the named cluster explicitly.
func (ts *TreeState) SetExpanded(clusterName string, v bool) {
	ts.expanded[clusterName] = v
}

// Toggle flips the expanded/collapsed state of the named cluster.
func (ts *TreeState) Toggle(clusterName string) {
	ts.expanded[clusterName] = !ts.expanded[clusterName]
}

// fuzzyMatch reports whether all runes in pattern appear in target in order
// (case-insensitive). This is a standard subsequence / fuzzy-match check:
// every character of the pattern must exist somewhere in the target, in the
// same left-to-right order, but not necessarily adjacent.
//
// Examples:
//
//	fuzzyMatch("prod", "production-us-east")  → true
//	fuzzyMatch("prd",  "production-us-east")  → true  (non-contiguous)
//	fuzzyMatch("xyz",  "production-us-east")  → false
func fuzzyMatch(pattern, target string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.ToLower(pattern)
	target = strings.ToLower(target)

	pi := 0 // index into pattern runes
	patRunes := []rune(pattern)
	for _, r := range target {
		if r == patRunes[pi] {
			pi++
			if pi == len(patRunes) {
				return true
			}
		}
	}
	return false
}

// BuildFlatList constructs an ordered, flat slice of TreeNodes that are
// currently visible, applying the filter string and honouring expanded/
// collapsed state.
//
// Rules:
//   - When filter is empty, only hosts under expanded clusters are included.
//   - When filter is non-empty, fuzzy matching is applied to both cluster
//     names and host names. Matching clusters and their hosts (plus hosts
//     whose own name matches) are shown regardless of expanded/collapsed
//     state (filter forces expansion).
//   - A cluster row is omitted entirely when a filter is active and no hosts
//     in that cluster match (unless the cluster name itself fuzzy-matches).
func BuildFlatList(cfg *config.Config, state *TreeState, filter string) []TreeNode {
	clusterNames := cfg.ClusterNames() // already sorted
	var nodes []TreeNode

	for _, name := range clusterNames {
		cluster := cfg.Clusters[name]
		clusterNameMatches := filter == "" || fuzzyMatch(filter, name)

		hasSubgroups := len(cluster.Subgroups) > 0

		if hasSubgroups {
			// Subgroup-based cluster: cluster > subgroup > host.
			anyMatch := false
			type sgData struct {
				sgName string
				hosts  []config.ResolvedHost
			}
			var subgroups []sgData

			for _, sgName := range cluster.SubgroupNames() {
				sg := cluster.Subgroups[sgName]
				sgNameMatches := clusterNameMatches || fuzzyMatch(filter, sgName)

				var matched []config.ResolvedHost
				for i, h := range sg.Hosts {
					r := h.ResolveInSubgroup(name, cluster.Defaults, sgName, sg, i)
					if filter == "" || sgNameMatches || fuzzyMatch(filter, r.DisplayName) {
						matched = append(matched, r)
					}
				}
				if len(matched) > 0 || (filter != "" && sgNameMatches) {
					subgroups = append(subgroups, sgData{sgName: sgName, hosts: matched})
					anyMatch = true
				}
			}

			if filter != "" && !anyMatch {
				continue
			}

			nodes = append(nodes, TreeNode{Kind: NodeKindCluster, ClusterName: name})

			if state.IsExpanded(name) || filter != "" {
				for _, sg := range subgroups {
					nodes = append(nodes, TreeNode{
						Kind:         NodeKindSubgroup,
						ClusterName:  name,
						SubgroupName: sg.sgName,
					})

					sgKey := subgroupKey(name, sg.sgName)
					if state.IsExpanded(sgKey) || filter != "" {
						for i := range sg.hosts {
							h := sg.hosts[i]
							nodes = append(nodes, TreeNode{
								Kind:         NodeKindHost,
								ClusterName:  name,
								SubgroupName: sg.sgName,
								Host:         &h,
							})
						}
					}
				}
			}
		} else {
			// Flat cluster: cluster > host (existing behavior).
			var matchedHosts []config.ResolvedHost
			for _, h := range cluster.Hosts {
				r := h.Resolve(name, cluster.Defaults)
				if filter == "" || clusterNameMatches || fuzzyMatch(filter, r.DisplayName) {
					matchedHosts = append(matchedHosts, r)
				}
			}

			if filter != "" && len(matchedHosts) == 0 {
				continue
			}

			nodes = append(nodes, TreeNode{Kind: NodeKindCluster, ClusterName: name})

			if state.IsExpanded(name) || filter != "" {
				for i := range matchedHosts {
					h := matchedHosts[i]
					nodes = append(nodes, TreeNode{
						Kind:        NodeKindHost,
						ClusterName: name,
						Host:        &h,
					})
				}
			}
		}
	}

	return nodes
}
