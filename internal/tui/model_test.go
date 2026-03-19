package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
)

// minimalConfig returns a small *config.Config suitable for TUI unit tests.
func minimalConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"test-cluster": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
					{Name: "host-02", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
}

// sendKey delivers a tea.KeyMsg to the model and returns the updated model
// plus the resulting tea.Cmd.
func sendKey(m Model, keyStr string) (Model, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyStr)}
	// Use the special key types for non-rune keys.
	switch keyStr {
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	}
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

// isQuitCmd reports whether cmd is a tea.Quit command.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

// withWindowSize sends a WindowSizeMsg to the model so it is fully initialised.
func withWindowSize(m Model, w, h int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(Model)
}

// TestQKeyQuitsSmux verifies that pressing "q" in the normal (non-filter) TUI
// state sets Result.Quit = true and returns a tea.Quit command, causing the
// bubbletea program — and therefore smux — to exit.
func TestQKeyQuitsSmux(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	m, cmd := sendKey(m, "q")

	if !m.Done() {
		t.Error("pressing 'q' should mark the model as done")
	}
	if !m.GetResult().Quit {
		t.Error("pressing 'q' should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' should return tea.Quit command")
	}
}

// TestCtrlCKeyQuitsSmux verifies that pressing Ctrl+C in the normal (non-filter)
// TUI state sets Result.Quit = true and returns a tea.Quit command.
func TestCtrlCKeyQuitsSmux(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	m, cmd := sendKey(m, "ctrl+c")

	if !m.Done() {
		t.Error("pressing Ctrl+C should mark the model as done")
	}
	if !m.GetResult().Quit {
		t.Error("pressing Ctrl+C should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing Ctrl+C should return tea.Quit command")
	}
}

// TestCtrlCInFilterModeQuitsSmux verifies that pressing Ctrl+C while the
// inline filter is active also exits smux (not just dismisses the filter).
func TestCtrlCInFilterModeQuitsSmux(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Activate filter mode by pressing "/".
	m, _ = sendKey(m, "/")
	if !m.isFilterActive() {
		t.Fatal("pressing '/' should activate filter mode")
	}

	// Now press Ctrl+C inside filter mode.
	m, cmd := sendKey(m, "ctrl+c")

	if !m.Done() {
		t.Error("Ctrl+C in filter mode should mark the model as done")
	}
	if !m.GetResult().Quit {
		t.Error("Ctrl+C in filter mode should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C in filter mode should return tea.Quit command")
	}
}

// TestCtrlCInConfirmModeQuitsSmux verifies that pressing Ctrl+C during the
// large-selection confirmation prompt also exits smux entirely.
func TestCtrlCInConfirmModeQuitsSmux(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Force confirmation mode directly (simulates ≥50-host selection).
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	m, cmd := sendKey(m, "ctrl+c")

	if !m.Done() {
		t.Error("Ctrl+C in confirm mode should mark the model as done")
	}
	if !m.GetResult().Quit {
		t.Error("Ctrl+C in confirm mode should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C in confirm mode should return tea.Quit command")
	}
}

// multiClusterConfig returns a *config.Config where "shared-host" appears in
// both "cluster-a" and "cluster-b", and "unique-host" only appears in
// "cluster-a". This is used to test multi-cluster host selection semantics.
func multiClusterConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"cluster-a": {
				Defaults: config.HostDefaults{User: "alice"},
				Hosts: []config.HostEntry{
					{Name: "shared-host", Provenance: config.ProvenanceAlias},
					{Name: "unique-host", Provenance: config.ProvenanceAlias},
				},
			},
			"cluster-b": {
				Defaults: config.HostDefaults{User: "bob"},
				Hosts: []config.HostEntry{
					{Name: "shared-host", Provenance: config.ProvenanceAlias},
				},
			},
		},
	}
}

// TestMultiClusterHostListedUnderEachCluster verifies that a host appearing in
// two clusters is visible as a separate host node under each cluster in the
// flat tree list when both clusters are expanded.
func TestMultiClusterHostListedUnderEachCluster(t *testing.T) {
	cfg := multiClusterConfig()
	m := withWindowSize(New(cfg), 80, 24)

	// Count how many host nodes have the name "shared-host".
	sharedCount := 0
	clustersSeen := make(map[string]bool)
	for _, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil && node.Host.Host == "shared-host" {
			sharedCount++
			clustersSeen[node.ClusterName] = true
		}
	}

	if sharedCount != 2 {
		t.Errorf("shared-host should appear twice in flat list (once per cluster), got %d", sharedCount)
	}
	if !clustersSeen["cluster-a"] {
		t.Error("shared-host should appear under cluster-a")
	}
	if !clustersSeen["cluster-b"] {
		t.Error("shared-host should appear under cluster-b")
	}
}

// TestMultiClusterSelectedHostCarriesAllClusters verifies that selecting
// "shared-host" from cluster-a produces a ResolvedHost whose ClusterNames
// contains both "cluster-a" and "cluster-b" (all clusters that host belongs to).
func TestMultiClusterSelectedHostCarriesAllClusters(t *testing.T) {
	cfg := multiClusterConfig()
	m := withWindowSize(New(cfg), 80, 24)

	// Navigate to the shared-host node under cluster-a and select it.
	// flat list (all expanded, sorted clusters): cluster-a header, shared-host,
	// unique-host, cluster-b header, shared-host.
	// Find the index of shared-host under cluster-a.
	targetIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil &&
			node.Host.Host == "shared-host" && node.ClusterName == "cluster-a" {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		t.Fatal("shared-host not found under cluster-a in flat list")
	}

	// Move cursor to targetIdx, then press space to select.
	m.view.Cursor = targetIdx
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 selected host, got %d", len(hosts))
	}

	got := hosts[0]
	if got.Host != "shared-host" {
		t.Errorf("selected host: got %q, want %q", got.Host, "shared-host")
	}

	// ClusterNames must include both clusters in sorted order.
	if len(got.ClusterNames) != 2 {
		t.Fatalf("ClusterNames len: got %d, want 2 (both clusters); ClusterNames=%v", len(got.ClusterNames), got.ClusterNames)
	}
	if got.ClusterNames[0] != "cluster-a" || got.ClusterNames[1] != "cluster-b" {
		t.Errorf("ClusterNames: got %v, want [cluster-a cluster-b]", got.ClusterNames)
	}
}

// TestMultiClusterSelectFromSecondClusterCarriesAllClusters verifies that
// selecting shared-host from cluster-b (the second cluster) also produces a
// ResolvedHost with ClusterNames containing both clusters.
func TestMultiClusterSelectFromSecondClusterCarriesAllClusters(t *testing.T) {
	cfg := multiClusterConfig()
	m := withWindowSize(New(cfg), 80, 24)

	// Find shared-host under cluster-b.
	targetIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil &&
			node.Host.Host == "shared-host" && node.ClusterName == "cluster-b" {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		t.Fatal("shared-host not found under cluster-b in flat list")
	}

	m.view.Cursor = targetIdx
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 selected host, got %d", len(hosts))
	}

	got := hosts[0]
	if got.Host != "shared-host" {
		t.Errorf("selected host: got %q, want %q", got.Host, "shared-host")
	}

	// ClusterNames must include both clusters regardless of which cluster
	// the user selected from.
	if len(got.ClusterNames) != 2 {
		t.Fatalf("ClusterNames len: got %d, want 2; ClusterNames=%v", len(got.ClusterNames), got.ClusterNames)
	}
	if got.ClusterNames[0] != "cluster-a" || got.ClusterNames[1] != "cluster-b" {
		t.Errorf("ClusterNames: got %v, want [cluster-a cluster-b]", got.ClusterNames)
	}
}

// ---------------------------------------------------------------------------
// AC 19 – terminal-too-small guard
// ---------------------------------------------------------------------------

const tooSmallMsg = "Terminal too small (need at least 40×10)"

// TestTerminalTooSmallNarrow checks that a terminal narrower than 40 columns
// shows the guard message.
func TestTerminalTooSmallNarrow(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 39, 24)
	view := m.View()
	if view != tooSmallMsg {
		t.Errorf("expected too-small message for 39-col terminal, got: %q", view)
	}
}

// TestTerminalTooSmallShort checks that a terminal shorter than 10 rows shows
// the guard message.
func TestTerminalTooSmallShort(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 9)
	view := m.View()
	if view != tooSmallMsg {
		t.Errorf("expected too-small message for 9-row terminal, got: %q", view)
	}
}

// TestTerminalAtMinimumSize checks that a terminal exactly at the 40×10
// boundary does NOT show the guard message (boundary is inclusive).
func TestTerminalAtMinimumSize(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 40, 10)
	view := m.View()
	if view == tooSmallMsg {
		t.Error("40×10 terminal should not show too-small message")
	}
}

// TestTerminalNormalSize checks that a 120×40 terminal shows the normal TUI,
// not the too-small guard.
func TestTerminalNormalSize(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 120, 40)
	view := m.View()
	if view == tooSmallMsg {
		t.Error("120×40 terminal should show normal TUI, not too-small message")
	}
}

// TestTerminalTooSmallBothDimensions checks the guard fires when both width
// and height are below their respective minimums.
func TestTerminalTooSmallBothDimensions(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 20, 5)
	view := m.View()
	if view != tooSmallMsg {
		t.Errorf("expected too-small message for 20×5 terminal, got: %q", view)
	}
}

// TestTerminalTooSmallAtZero checks that a zero-value Model (no WindowSizeMsg
// delivered yet) triggers the guard because width and height are both 0.
func TestTerminalTooSmallAtZero(t *testing.T) {
	m := New(minimalConfig())
	view := m.View()
	if view != tooSmallMsg {
		t.Errorf("expected too-small message for zero-size model, got: %q", view)
	}
}

// ---------------------------------------------------------------------------
// AC 4 – space toggles selection for individual host or entire cluster
// ---------------------------------------------------------------------------

// TestSpaceSelectsHost verifies that pressing Space on a host node marks it as
// selected and that pressing Space again deselects it.
func TestSpaceSelectsHost(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Find the index of the first host node ("host-01" under "test-cluster").
	hostIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() {
			hostIdx = i
			break
		}
	}
	if hostIdx < 0 {
		t.Fatal("no host node found in flat list")
	}

	// Move cursor to host and press Space → should be selected.
	m.view.Cursor = hostIdx
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 1 {
		t.Fatalf("after first Space expected 1 selected host, got %d", len(hosts))
	}

	// Press Space again → should deselect.
	m, _ = sendKey(m, " ")
	hosts = m.selectedHosts()
	if len(hosts) != 0 {
		t.Fatalf("after second Space expected 0 selected hosts, got %d", len(hosts))
	}
}

// TestSpaceOnClusterSelectsAll verifies that pressing Space on a cluster node
// when no hosts are selected marks every host in that cluster as selected.
func TestSpaceOnClusterSelectsAll(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// The first flat node should be the cluster header.
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsCluster() {
		t.Fatal("expected first flat node to be a cluster header")
	}

	// cursor starts at 0 (cluster node) — press Space.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	cfg := minimalConfig()
	expected := len(cfg.Clusters["test-cluster"].Hosts)
	if len(hosts) != expected {
		t.Errorf("after Space on cluster, expected %d hosts selected, got %d",
			expected, len(hosts))
	}
}

// TestSpaceOnClusterDeselectsAll verifies that pressing Space on a cluster node
// when ALL hosts are already selected deselects every host in that cluster.
func TestSpaceOnClusterDeselectsAll(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// First Space: select all.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) == 0 {
		t.Fatal("expected all hosts to be selected after first Space on cluster")
	}

	// Second Space: deselect all.
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != 0 {
		t.Errorf("expected 0 hosts selected after second Space on cluster, got %d",
			len(m.selectedHosts()))
	}
}

// TestSpaceOnClusterPartialSelectsAll verifies that pressing Space on a cluster
// where only some hosts are selected selects ALL hosts (not a toggle of the
// partial state).
func TestSpaceOnClusterPartialSelectsAll(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Select only the first host manually.
	hostIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() {
			hostIdx = i
			break
		}
	}
	if hostIdx < 0 {
		t.Fatal("no host node found in flat list")
	}
	m.view.Cursor = hostIdx
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != 1 {
		t.Fatal("expected exactly 1 host selected after manual select")
	}

	// Now press Space on the cluster node → should select ALL hosts.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")

	cfg := minimalConfig()
	expected := len(cfg.Clusters["test-cluster"].Hosts)
	if len(m.selectedHosts()) != expected {
		t.Errorf("Space on partially-selected cluster should select all %d hosts, got %d",
			expected, len(m.selectedHosts()))
	}
}

// TestSpaceOnCollapsedClusterSelectsAll verifies that pressing Space on a
// collapsed cluster node (where host rows are hidden) still selects all of its
// hosts — the toggle operates on the cluster's membership, not the visible rows.
func TestSpaceOnCollapsedClusterSelectsAll(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Collapse the cluster by pressing Tab (which calls toggleExpand).
	m.view.Cursor = 0
	m, _ = sendKey(m, "tab") // collapse — was expanded by default

	// Sanity: after collapsing, only 1 node visible (the cluster header).
	if len(m.flatNodes) != 1 {
		t.Fatalf("expected 1 flat node after collapse, got %d", len(m.flatNodes))
	}

	// Press Space on the collapsed cluster → should select all hosts.
	m, _ = sendKey(m, " ")

	cfg := minimalConfig()
	expected := len(cfg.Clusters["test-cluster"].Hosts)
	if len(m.selectedHosts()) != expected {
		t.Errorf("Space on collapsed cluster should select %d hosts, got %d",
			expected, len(m.selectedHosts()))
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 1 – fuzzy filter state: filterActive bool, filterInput textinput,
//             '/' keybinding enters filter mode with focused text input.
// ---------------------------------------------------------------------------

// filterConfig returns a config with two clusters so we can test that filtering
// reduces the visible host list.
func filterConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "web-01", Provenance: config.ProvenanceFull},
					{Name: "db-01", Provenance: config.ProvenanceFull},
				},
			},
			"staging": {
				Hosts: []config.HostEntry{
					{Name: "web-01", Provenance: config.ProvenanceFull},
					{Name: "cache-01", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
}

// TestSlashEntersFilterMode verifies that pressing '/' in normal mode activates
// filter mode (filterActive becomes true) and focuses the inline text input.
func TestSlashEntersFilterMode(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	if m.isFilterActive() {
		t.Fatal("filterActive should be false at start")
	}

	m, _ = sendKey(m, "/")

	if !m.isFilterActive() {
		t.Error("pressing '/' should set filterActive = true")
	}
	if !m.filterInput.Focused() {
		t.Error("pressing '/' should focus the filter text input")
	}
}

// TestFilterActiveFieldExists verifies the Model struct exposes a filterActive
// boolean field (compile-time check that the field is accessible within the
// package).
func TestFilterActiveFieldExists(t *testing.T) {
	m := New(minimalConfig())
	// Access both fields to prove they exist with expected types.
	var active bool = m.isFilterActive()
	filterVal := m.filterInput.Value()
	if active {
		t.Error("filterActive should be false on a fresh model")
	}
	if filterVal != "" {
		t.Errorf("filterInput value should be empty on a fresh model, got %q", filterVal)
	}
}

// TestEscExitsFilterModeAndClearsInput verifies that pressing Esc in filter
// mode deactivates filter mode and clears the filter text, restoring the full
// host list.
func TestEscExitsFilterModeAndClearsInput(t *testing.T) {
	m := withWindowSize(New(filterConfig()), 80, 24)

	// Enter filter mode.
	m, _ = sendKey(m, "/")
	if !m.isFilterActive() {
		t.Fatal("expected filterActive after '/'")
	}

	// Type a character to set filter value; send as rune key.
	m, _ = sendKey(m, "w")

	// Press Esc to exit filter mode.
	m, _ = sendKey(m, "esc")

	if m.isFilterActive() {
		t.Error("Esc should deactivate filter mode")
	}
	if m.filterInput.Value() != "" {
		t.Errorf("Esc should clear filter input, got %q", m.filterInput.Value())
	}
}

// TestEnterCommitsFilterAndExitsTypingMode verifies that pressing Enter in
// filter mode commits the current filter (keeps its value) and exits filter
// typing mode (filterActive becomes false).
func TestEnterCommitsFilterAndExitsTypingMode(t *testing.T) {
	m := withWindowSize(New(filterConfig()), 80, 24)

	m, _ = sendKey(m, "/")

	// Send Enter to commit.
	m, _ = sendKey(m, "enter")

	if m.isFilterActive() {
		t.Error("Enter should exit filter typing mode (filterActive = false)")
	}
}

// TestFilterReducesVisibleNodes verifies that typing a filter string while in
// filter mode narrows the flat node list to only matching entries.
func TestFilterReducesVisibleNodes(t *testing.T) {
	m := withWindowSize(New(filterConfig()), 80, 24)

	// Record initial node count (all clusters expanded, all hosts visible).
	initialCount := len(m.flatNodes)
	if initialCount == 0 {
		t.Fatal("expected at least one node before filtering")
	}

	// Enter filter mode and type "cache" (only cache-01 in staging matches).
	m, _ = sendKey(m, "/")
	for _, ch := range "cache" {
		m, _ = sendKey(m, string(ch))
	}

	filteredCount := len(m.flatNodes)
	if filteredCount >= initialCount {
		t.Errorf("filter should reduce visible nodes: got %d, started with %d",
			filteredCount, initialCount)
	}
	// Expect: staging cluster header + cache-01 = 2 nodes
	if filteredCount == 0 {
		t.Error("filter should not produce an empty list when a matching host exists")
	}
}

// TestFilterEmptyAfterEsc verifies that after Esc the flat list is restored to
// its unfiltered state (same size as a freshly built model).
func TestFilterEmptyAfterEsc(t *testing.T) {
	m := withWindowSize(New(filterConfig()), 80, 24)
	initialCount := len(m.flatNodes)

	// Enter filter mode, type something, then Esc.
	m, _ = sendKey(m, "/")
	for _, ch := range "xyz" {
		m, _ = sendKey(m, string(ch))
	}
	m, _ = sendKey(m, "esc")

	restoredCount := len(m.flatNodes)
	if restoredCount != initialCount {
		t.Errorf("after Esc node count should be restored: got %d, want %d",
			restoredCount, initialCount)
	}
}

// TestSlashDoesNotQuit verifies that pressing '/' does not accidentally trigger
// the quit path.
func TestSlashDoesNotQuit(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	m, cmd := sendKey(m, "/")

	if m.Done() {
		t.Error("pressing '/' should not mark the model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing '/' should not return a tea.Quit command")
	}
}

// ---------------------------------------------------------------------------
// AC 23 – multi-cluster host selection
// ---------------------------------------------------------------------------

// TestMultiClusterDeduplicatesBothSelected verifies that selecting shared-host
// from BOTH cluster-a AND cluster-b results in a single deduplicated entry in
// the selection result (no duplicate SSH connections to the same host).
func TestMultiClusterDeduplicatesBothSelected(t *testing.T) {
	cfg := multiClusterConfig()
	m := withWindowSize(New(cfg), 80, 24)

	// Select shared-host under cluster-a.
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil &&
			node.Host.Host == "shared-host" && node.ClusterName == "cluster-a" {
			m.view.Cursor = i
			m, _ = sendKey(m, " ")
			break
		}
	}
	// Also select shared-host under cluster-b.
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil &&
			node.Host.Host == "shared-host" && node.ClusterName == "cluster-b" {
			m.view.Cursor = i
			m, _ = sendKey(m, " ")
			break
		}
	}

	hosts := m.selectedHosts()

	// Despite selecting from two clusters, shared-host should appear only once.
	sharedCount := 0
	for _, h := range hosts {
		if h.Host == "shared-host" {
			sharedCount++
		}
	}
	if sharedCount != 1 {
		t.Errorf("shared-host should be deduplicated to 1 entry in result, got %d", sharedCount)
	}
}

// ---------------------------------------------------------------------------
// AC 3 Sub-AC 3 – Esc clears filter, exits filter mode, restores full list
// ---------------------------------------------------------------------------

// TestEscClearsFilterString is a dedicated Sub-AC 3 test verifying that Esc
// in filter mode zeroes the filter string so that filterInput.Value() == "".
func TestEscClearsFilterString(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Enter filter mode and type a query.
	m, _ = sendKey(m, "/")
	m, _ = sendKey(m, "h")
	m, _ = sendKey(m, "o")
	m, _ = sendKey(m, "s")
	if m.filterInput.Value() == "" {
		t.Fatal("filter input should be non-empty after typing")
	}

	// Esc must clear the string.
	m, _ = sendKey(m, "esc")
	if got := m.filterInput.Value(); got != "" {
		t.Errorf("after Esc filterInput.Value() = %q; want empty string", got)
	}
}

// TestEscExitsFilterMode is a dedicated Sub-AC 3 test verifying that Esc
// transitions the model out of filter mode (filterActive becomes false).
func TestEscExitsFilterMode(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	m, _ = sendKey(m, "/")
	if !m.isFilterActive() {
		t.Fatal("filterActive should be true after '/'")
	}

	m, _ = sendKey(m, "esc")
	if m.isFilterActive() {
		t.Error("after Esc filterActive should be false")
	}
}

// TestEscRestoresFullUnfilteredList is a dedicated Sub-AC 3 test verifying
// that after Esc the flat node list matches the unfiltered baseline count.
func TestEscRestoresFullUnfilteredList(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Baseline: all clusters expanded, no filter.
	baseline := len(m.flatNodes)
	if baseline == 0 {
		t.Fatal("baseline list must be non-empty")
	}

	// Filter to fewer nodes then restore with Esc.
	m, _ = sendKey(m, "/")
	// Type "01" — only host-01 matches; should reduce the list.
	m, _ = sendKey(m, "0")
	m, _ = sendKey(m, "1")
	if len(m.flatNodes) >= baseline {
		t.Fatalf("filter did not reduce list: baseline=%d filtered=%d",
			baseline, len(m.flatNodes))
	}

	m, _ = sendKey(m, "esc")
	if got := len(m.flatNodes); got != baseline {
		t.Errorf("after Esc flatNodes len = %d; want %d (baseline)", got, baseline)
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 6c – Confirming→Launching transition on a second Enter press
// ---------------------------------------------------------------------------

// confirmingConfig builds a *config.Config whose cluster contains exactly n
// hosts. Used to trigger (or avoid) the large-selection confirmation gate.
func confirmingConfig(n int) *config.Config {
	hosts := make([]config.HostEntry, n)
	for i := range hosts {
		hosts[i] = config.HostEntry{
			Name:       fmt.Sprintf("host-%03d", i+1),
			Provenance: config.ProvenanceFull,
		}
	}
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"big-cluster": {Hosts: hosts},
		},
	}
}

// TestConfirmingEnterConfirmsLaunch verifies that pressing Enter while the
// large-selection confirmation prompt is displayed confirms the selection and
// causes the TUI to exit with the selected hosts in the result (same as y/Y).
func TestConfirmingEnterConfirmsLaunch(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Select one host BEFORE entering confirming mode, so result will be non-empty.
	for i, node := range m.flatNodes {
		if node.IsHost() {
			m.view.Cursor = i
			m, _ = sendKey(m, " ")
			break
		}
	}
	if len(m.selectedHosts()) == 0 {
		t.Fatal("expected at least one host selected before confirming")
	}

	// Force confirming mode (simulates ≥50-host selection gate).
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	// Press Enter in confirming state — should confirm.
	m, cmd := sendKey(m, "enter")

	if !m.Done() {
		t.Error("Enter in confirming state should mark the model as done")
	}
	if m.GetResult().Quit {
		t.Error("Enter in confirming state should not set Result.Quit")
	}
	if len(m.GetResult().Hosts) == 0 {
		t.Error("Enter in confirming state should populate Result.Hosts")
	}
	if !isQuitCmd(cmd) {
		t.Error("Enter in confirming state should return tea.Quit command")
	}
}

// TestConfirmingEnterResultHasCorrectHosts verifies that the Result.Hosts
// slice produced by a second Enter press matches the selected hosts.
func TestConfirmingEnterResultHasCorrectHosts(t *testing.T) {
	cfg := minimalConfig()
	m := withWindowSize(New(cfg), 80, 24)

	// Select every host in the cluster.
	m.view.Cursor = 0 // cluster node
	m, _ = sendKey(m, " ")

	expectedCount := len(cfg.Clusters["test-cluster"].Hosts)
	if len(m.selectedHosts()) != expectedCount {
		t.Fatalf("expected %d hosts selected, got %d", expectedCount, len(m.selectedHosts()))
	}

	// Force confirming mode then press Enter.
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}
	m, _ = sendKey(m, "enter")

	got := m.GetResult().Hosts
	if len(got) != expectedCount {
		t.Errorf("Result.Hosts len: got %d, want %d", len(got), expectedCount)
	}
}

// TestConfirmingNGoesBack verifies that pressing n in confirming state cancels
// the confirmation prompt and returns the model to the browsing phase.
func TestConfirmingNGoesBack(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	m, cmd := sendKey(m, "n")

	if m.isConfirming() {
		t.Error("pressing n should cancel confirming mode")
	}
	if m.Done() {
		t.Error("pressing n should not mark the model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing n should not return tea.Quit")
	}
}

// TestConfirmingEscGoesBack verifies that pressing Esc in confirming state
// also cancels the prompt and returns to the browsing phase.
func TestConfirmingEscGoesBack(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	m, cmd := sendKey(m, "esc")

	if m.isConfirming() {
		t.Error("Esc should cancel confirming mode")
	}
	if m.Done() {
		t.Error("Esc in confirming mode should not mark the model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("Esc in confirming mode should not return tea.Quit")
	}
}

// TestDefaultConfirmThresholdIs50 verifies that a Config with LargeSelectionThreshold
// unset (zero) reports an effective threshold of 50.
func TestDefaultConfirmThresholdIs50(t *testing.T) {
	cfg := &config.Config{}
	if got := cfg.EffectiveConfirmThreshold(); got != 50 {
		t.Errorf("default EffectiveConfirmThreshold = %d; want 50", got)
	}
}

// TestCustomConfirmThresholdRespected verifies that a non-zero LargeSelectionThreshold
// in the config is used instead of the default 50.
func TestCustomConfirmThresholdRespected(t *testing.T) {
	cfg := &config.Config{LargeSelectionThreshold: 5}
	if got := cfg.EffectiveConfirmThreshold(); got != 5 {
		t.Errorf("EffectiveConfirmThreshold = %d; want 5", got)
	}
}

// TestSmallSelectionSkipsConfirmationPrompt verifies that selecting fewer hosts
// than the threshold proceeds directly to launch without the confirmation prompt.
func TestSmallSelectionSkipsConfirmationPrompt(t *testing.T) {
	cfg := minimalConfig() // 2 hosts — well below default threshold of 50
	m := withWindowSize(New(cfg), 80, 24)

	// Select all hosts.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")

	// Press Enter — should skip the confirmation prompt and exit directly.
	m, cmd := sendKey(m, "enter")

	if m.isConfirming() {
		t.Error("small selection should NOT enter confirming mode")
	}
	if !m.Done() {
		t.Error("small selection Enter should mark the model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("small selection Enter should return tea.Quit command")
	}
	if len(m.GetResult().Hosts) == 0 {
		t.Error("small selection Enter should populate Result.Hosts")
	}
}

// TestLargeSelectionEntersConfirmingMode verifies that when the number of
// selected hosts meets the threshold, pressing Enter shows the confirmation
// prompt rather than launching immediately.
func TestLargeSelectionEntersConfirmingMode(t *testing.T) {
	// Use a custom low threshold so we can test with a small cluster.
	cfg := &config.Config{
		LargeSelectionThreshold: 2,
		Clusters: map[string]config.ClusterConfig{
			"test-cluster": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
					{Name: "host-02", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := withWindowSize(New(cfg), 80, 24)

	// Select all 2 hosts (meets threshold of 2).
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != 2 {
		t.Fatalf("expected 2 hosts selected, got %d", len(m.selectedHosts()))
	}

	// First Enter should show confirmation prompt.
	m, cmd := sendKey(m, "enter")

	if !m.isConfirming() {
		t.Error("≥threshold hosts: first Enter should enter confirming mode")
	}
	if m.Done() {
		t.Error("first Enter with large selection should NOT mark model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("first Enter with large selection should NOT return tea.Quit")
	}

	// Second Enter should confirm and launch.
	m, cmd = sendKey(m, "enter")

	if !m.Done() {
		t.Error("second Enter (confirming→launching) should mark model as done")
	}
	if m.GetResult().Quit {
		t.Error("second Enter should not set Result.Quit")
	}
	if len(m.GetResult().Hosts) == 0 {
		t.Error("second Enter should populate Result.Hosts")
	}
	if !isQuitCmd(cmd) {
		t.Error("second Enter should return tea.Quit command")
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 6b – Enter-key phase state machine, threshold guard, distinct UI
// ---------------------------------------------------------------------------

// TestEnterWithNoSelectionDoesNothing verifies that pressing Enter when no
// hosts are selected leaves the model in BrowsingPhase and does not exit.
func TestEnterWithNoSelectionDoesNothing(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	if len(m.selectedHosts()) != 0 {
		t.Fatal("expected no hosts selected at start")
	}

	m, cmd := sendKey(m, "enter")

	if m.isConfirming() {
		t.Error("Enter with empty selection should NOT enter ConfirmingPhase")
	}
	if m.Done() {
		t.Error("Enter with empty selection should NOT mark model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("Enter with empty selection should NOT return tea.Quit")
	}
}

// TestBrowsingPhaseViewShowsTitle verifies that View() in PhaseBrowsing
// includes the smux title string — distinguishing it from the confirming UI.
func TestBrowsingPhaseViewShowsTitle(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	view := m.View()

	if !strings.Contains(view, "smux") {
		t.Error("BrowsingPhase View() should contain 'smux' title")
	}
	if strings.Contains(view, "Large selection") {
		t.Error("BrowsingPhase View() should NOT contain confirming-phase text")
	}
}

// TestConfirmingPhaseViewIsDistinctFromBrowsing verifies that View() in
// PhaseConfirming renders the confirmation box — not the cluster tree — so
// the user sees a distinct prompt asking them to confirm a large selection.
func TestConfirmingPhaseViewIsDistinctFromBrowsing(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	view := m.View()

	if !strings.Contains(view, "Large selection") {
		t.Error("ConfirmingPhase View() should contain 'Large selection' header")
	}
	if !strings.Contains(view, "y") {
		t.Error("ConfirmingPhase View() should contain 'y' confirmation hint")
	}
	// The browsing title should NOT be present in the confirming view.
	if strings.Contains(view, "smux — select hosts") {
		t.Error("ConfirmingPhase View() should NOT contain the normal browsing title")
	}
}

// TestPhaseTypesAreDistinctStructs is a compile-time check verifying that
// the four Phase types are independently constructable structs, confirming
// the selection state machine is modelled with distinct named types.
func TestPhaseTypesAreDistinctStructs(t *testing.T) {
	var _ Phase = BrowsingPhase{}
	var _ Phase = SelectingPhase{}
	var _ Phase = ConfirmingPhase{}
	var _ Phase = LaunchingPhase{}
}

// TestSelectionStateAndViewStateAreDistinctStructs is a compile-time check
// verifying that SelectionState and ViewState exist as distinct named types,
// satisfying the domain-model constraint.
func TestSelectionStateAndViewStateAreDistinctStructs(t *testing.T) {
	var ds SelectionState
	var vs ViewState
	// Access exported fields to verify the struct shapes.
	_ = ds.Phase
	_ = ds.Selected
	_ = vs.Cursor
	_ = vs.Width
	_ = vs.Height
}

// TestThresholdGuardUsesConfigValue verifies that handleEnter uses the
// configurable threshold from Config.LargeSelectionThreshold rather than the
// hardcoded default, so the guard fires at the right count.
func TestThresholdGuardUsesConfigValue(t *testing.T) {
	// Set threshold to 3; select 3 hosts → should trigger ConfirmingPhase.
	cfg := &config.Config{
		LargeSelectionThreshold: 3,
		Clusters: map[string]config.ClusterConfig{
			"c": {
				Hosts: []config.HostEntry{
					{Name: "h1", Provenance: config.ProvenanceFull},
					{Name: "h2", Provenance: config.ProvenanceFull},
					{Name: "h3", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := withWindowSize(New(cfg), 80, 24)

	// Select all 3 hosts by pressing Space on the cluster header.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != 3 {
		t.Fatalf("expected 3 hosts selected, got %d", len(m.selectedHosts()))
	}

	// Enter should advance to ConfirmingPhase (3 >= threshold 3).
	m, _ = sendKey(m, "enter")
	if !m.isConfirming() {
		t.Error("Enter with 3 hosts (threshold=3) should enter ConfirmingPhase")
	}
}

// TestThresholdGuardBelowCustomValue verifies that selecting fewer hosts
// than a custom threshold skips ConfirmingPhase and exits directly.
func TestThresholdGuardBelowCustomValue(t *testing.T) {
	// Set threshold to 3; select only 2 hosts → should skip ConfirmingPhase.
	cfg := &config.Config{
		LargeSelectionThreshold: 3,
		Clusters: map[string]config.ClusterConfig{
			"c": {
				Hosts: []config.HostEntry{
					{Name: "h1", Provenance: config.ProvenanceFull},
					{Name: "h2", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := withWindowSize(New(cfg), 80, 24)

	// Select all 2 hosts (below threshold 3).
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != 2 {
		t.Fatalf("expected 2 hosts selected, got %d", len(m.selectedHosts()))
	}

	// Enter should confirm immediately (2 < 3).
	m, cmd := sendKey(m, "enter")
	if m.isConfirming() {
		t.Error("Enter with 2 hosts (threshold=3) should NOT enter ConfirmingPhase")
	}
	if !m.Done() {
		t.Error("Enter with 2 hosts (threshold=3) should mark model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("Enter with 2 hosts (threshold=3) should return tea.Quit")
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 6a — ValidTransition edge table + ConfirmingPhase.Threshold
// ---------------------------------------------------------------------------

// TestValidTransitionEdges exhaustively checks every documented edge of the
// phase state machine returns true from ValidTransition, and that undocumented
// edges return false.
func TestValidTransitionEdges(t *testing.T) {
	valid := []struct{ src, dst Phase }{
		{BrowsingPhase{}, SelectingPhase{}},
		{BrowsingPhase{}, ConfirmingPhase{}},
		{BrowsingPhase{}, LaunchingPhase{}},
		{SelectingPhase{}, BrowsingPhase{}},
		{ConfirmingPhase{}, BrowsingPhase{}},
		{ConfirmingPhase{}, LaunchingPhase{}},
	}
	for _, e := range valid {
		if !ValidTransition(e.src, e.dst) {
			t.Errorf("ValidTransition(%T → %T) = false; want true", e.src, e.dst)
		}
	}

	invalid := []struct{ src, dst Phase }{
		{LaunchingPhase{}, BrowsingPhase{}},
		{LaunchingPhase{}, SelectingPhase{}},
		{LaunchingPhase{}, ConfirmingPhase{}},
		{SelectingPhase{}, ConfirmingPhase{}},
		{SelectingPhase{}, LaunchingPhase{}},
		{ConfirmingPhase{}, SelectingPhase{}},
		{BrowsingPhase{}, BrowsingPhase{}},
		{SelectingPhase{}, SelectingPhase{}},
	}
	for _, e := range invalid {
		if ValidTransition(e.src, e.dst) {
			t.Errorf("ValidTransition(%T → %T) = true; want false", e.src, e.dst)
		}
	}
}

// TestConfirmingPhaseThresholdFieldIsConfigurable verifies that
// ConfirmingPhase.Threshold carries an arbitrary integer value, and that the
// zero value of ConfirmingPhase does not default to DefaultConfirmThreshold
// automatically (callers must set the field explicitly).
func TestConfirmingPhaseThresholdFieldIsConfigurable(t *testing.T) {
	p := ConfirmingPhase{Threshold: 100}
	if p.Threshold != 100 {
		t.Errorf("ConfirmingPhase.Threshold = %d; want 100", p.Threshold)
	}

	// Zero value should be 0, not DefaultConfirmThreshold.
	var zero ConfirmingPhase
	if zero.Threshold != 0 {
		t.Errorf("zero ConfirmingPhase.Threshold = %d; want 0", zero.Threshold)
	}
}

// TestModelStatePhaseIsSelectionDomain verifies that the Model's SelectionState
// is the canonical holder of the Phase — not a loose bool — separating domain
// concerns from view concerns.
func TestModelStatePhaseIsSelectionDomain(t *testing.T) {
	m := New(minimalConfig())

	// SelectionState.Phase must start as BrowsingPhase.
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("Model.state.Phase initial type = %T; want BrowsingPhase", m.state.Phase)
	}

	// ViewState.Cursor must start at 0.
	if m.view.Cursor != 0 {
		t.Errorf("Model.view.Cursor initial value = %d; want 0", m.view.Cursor)
	}
}

// ---------------------------------------------------------------------------
// AC 7 Sub-AC 2 – confirmation prompt displays configured threshold value
// ---------------------------------------------------------------------------

// TestConfirmViewDisplaysDefaultThreshold verifies that when the confirmation
// prompt is shown with the default threshold (50), the threshold value 50
// appears in the rendered view.
func TestConfirmViewDisplaysDefaultThreshold(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	// Force confirming mode with the default threshold value.
	m.state.Phase = ConfirmingPhase{Threshold: DefaultConfirmThreshold}

	view := m.View()

	// The view must mention the threshold.
	if !strings.Contains(view, "50") {
		t.Errorf("confirmView should display configured threshold 50; got:\n%s", view)
	}
}

// TestConfirmViewDisplaysCustomThreshold verifies that when a non-default
// threshold is configured, that exact value is shown in the confirmation prompt.
func TestConfirmViewDisplaysCustomThreshold(t *testing.T) {
	cfg := &config.Config{
		LargeSelectionThreshold: 7,
		Clusters: map[string]config.ClusterConfig{
			"c": {
				Hosts: []config.HostEntry{
					{Name: "h1", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := withWindowSize(New(cfg), 80, 24)

	// Trigger confirming mode with threshold=7 (as handleEnter would set it).
	m.state.Phase = ConfirmingPhase{Threshold: 7}

	view := m.View()

	// The view must mention the custom threshold 7.
	if !strings.Contains(view, "7") {
		t.Errorf("confirmView should display configured threshold 7; got:\n%s", view)
	}
}

// TestConfirmViewThresholdFromHandleEnter verifies the end-to-end path: when
// Enter is pressed with ≥threshold hosts selected, the resulting confirmation
// view contains the threshold value that was read from the config.
func TestConfirmViewThresholdFromHandleEnter(t *testing.T) {
	// Use a small custom threshold so we can trigger it with a small cluster.
	const customThreshold = 3
	cfg := &config.Config{
		LargeSelectionThreshold: customThreshold,
		Clusters: map[string]config.ClusterConfig{
			"c": {
				Hosts: []config.HostEntry{
					{Name: "h1", Provenance: config.ProvenanceFull},
					{Name: "h2", Provenance: config.ProvenanceFull},
					{Name: "h3", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	m := withWindowSize(New(cfg), 80, 24)

	// Select all 3 hosts (meets threshold).
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")
	if len(m.selectedHosts()) != customThreshold {
		t.Fatalf("expected %d hosts selected, got %d", customThreshold, len(m.selectedHosts()))
	}

	// Press Enter — should enter ConfirmingPhase.
	m, _ = sendKey(m, "enter")
	if !m.isConfirming() {
		t.Fatal("expected ConfirmingPhase after Enter with ≥threshold hosts")
	}

	// The confirmation view must display the configured threshold value.
	view := m.View()
	threshStr := fmt.Sprintf("%d", customThreshold)
	if !strings.Contains(view, threshStr) {
		t.Errorf("confirmView should display threshold %d from config; got:\n%s", customThreshold, view)
	}
}

// ---------------------------------------------------------------------------
// AC 15 Sub-AC 2 – fresh TUI on loop-back: selections cleared
// ---------------------------------------------------------------------------

// TestNewModelHasNoSelections verifies that tui.New always creates a model
// with an empty selection set. This is the guarantee that allows the main loop
// to produce a fresh TUI after each SSH window creation without any previous
// host selections leaking into the next iteration.
func TestNewModelHasNoSelections(t *testing.T) {
	m := New(minimalConfig())

	if len(m.state.Selected) != 0 {
		t.Errorf("New model state.Selected has %d entries; want 0 (fresh TUI must have no selections)",
			len(m.state.Selected))
	}
	if len(m.selectedHosts()) != 0 {
		t.Errorf("New model selectedHosts() = %d; want 0", len(m.selectedHosts()))
	}
}

// TestNewModelStartsInBrowsingPhase verifies that tui.New always starts in
// BrowsingPhase (the initial interaction state), not a mid-workflow phase
// such as ConfirmingPhase or LaunchingPhase. Each loop-back must present the
// user with a clean browsing experience.
func TestNewModelStartsInBrowsingPhase(t *testing.T) {
	m := New(minimalConfig())

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("New model state.Phase = %T; want BrowsingPhase (fresh TUI must start in browsing state)",
			m.state.Phase)
	}
}

// TestSelectionsNotCarriedBetweenTUIIterations verifies that creating a new
// Model via tui.New does not inherit any selection state from a previous Model.
// This mirrors what the main loop does: discard the old model and create a new
// one after each SSH window creation — the returned model must be blank.
func TestSelectionsNotCarriedBetweenTUIIterations(t *testing.T) {
	cfg := minimalConfig()

	// Simulate first TUI iteration: user selects all hosts.
	m1 := withWindowSize(New(cfg), 80, 24)
	m1.view.Cursor = 0
	m1, _ = sendKey(m1, " ") // select the cluster (all hosts)
	if len(m1.selectedHosts()) == 0 {
		t.Fatal("first iteration: expected at least one host selected after Space on cluster")
	}

	// Simulate second TUI iteration: a brand-new model is constructed.
	// In main.go this happens when runTUI calls tui.New(cfg, ...) at the top
	// of every loop body.
	m2 := New(cfg)

	if len(m2.state.Selected) != 0 {
		t.Errorf("second TUI iteration: state.Selected has %d entries; want 0 — selections must not carry over",
			len(m2.state.Selected))
	}
	if len(m2.selectedHosts()) != 0 {
		t.Errorf("second TUI iteration: selectedHosts() = %d; want 0", len(m2.selectedHosts()))
	}
}

// TestNewModelCursorStartsAtZero verifies that every fresh TUI model positions
// the cursor at row 0 so that focus is always at the top of the host tree when
// looping back — not wherever it was before the previous iteration ended.
func TestNewModelCursorStartsAtZero(t *testing.T) {
	cfg := minimalConfig()

	// Simulate first iteration moving cursor down.
	m1 := withWindowSize(New(cfg), 80, 24)
	m1, _ = sendKey(m1, "down")
	m1, _ = sendKey(m1, "down")
	if m1.view.Cursor == 0 {
		t.Skip("cursor did not move — not enough nodes to test this case")
	}

	// Second iteration must reset the cursor.
	m2 := New(cfg)
	if m2.view.Cursor != 0 {
		t.Errorf("new TUI model view.Cursor = %d; want 0 (fresh TUI must reset cursor to top)",
			m2.view.Cursor)
	}
}
