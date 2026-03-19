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
	// NodeKindHost represents a leaf host row nested under a cluster.
	NodeKindHost
)

// TreeNode is a single displayable row in the cluster tree.
//
// Cluster nodes (NodeKindCluster) can be expanded or collapsed; their
// expanded/collapsed state is tracked by TreeState. Host nodes
// (NodeKindHost) are children of a cluster and are shown only when the
// parent cluster is expanded (or when a filter forces expansion).
type TreeNode struct {
	Kind        NodeKind
	ClusterName string               // set for both cluster and host nodes
	Host        *config.ResolvedHost // non-nil only for NodeKindHost
}

// IsCluster reports whether this node represents a cluster header.
func (n TreeNode) IsCluster() bool { return n.Kind == NodeKindCluster }

// IsHost reports whether this node represents a leaf host entry.
func (n TreeNode) IsHost() bool { return n.Kind == NodeKindHost }

// TreeState tracks the expanded/collapsed state for each cluster in the tree.
// It is embedded in the bubbletea Model so that the TUI can efficiently
// update and query expansion state without touching the underlying config.
type TreeState struct {
	expanded map[string]bool // cluster name → is expanded?
}

// NewTreeState creates a TreeState with all named clusters initially expanded.
func NewTreeState(clusterNames []string) TreeState {
	expanded := make(map[string]bool, len(clusterNames))
	for _, name := range clusterNames {
		expanded[name] = true
	}
	return TreeState{expanded: expanded}
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

		// Collect hosts that pass the filter.
		var matchedHosts []config.ResolvedHost
		for _, h := range cluster.Hosts {
			r := h.Resolve(name, cluster.Defaults)
			if filter == "" ||
				clusterNameMatches ||
				fuzzyMatch(filter, r.DisplayName) {
				matchedHosts = append(matchedHosts, r)
			}
		}

		// Skip the entire cluster when a filter is active and nothing matches.
		if filter != "" && len(matchedHosts) == 0 {
			continue
		}

		// Emit the cluster header node.
		nodes = append(nodes, TreeNode{Kind: NodeKindCluster, ClusterName: name})

		// Emit host nodes when the cluster is expanded, or when a filter is
		// active (active filter forces expansion so results are visible).
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

	return nodes
}
