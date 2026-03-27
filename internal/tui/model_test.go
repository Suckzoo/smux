package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
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

// emptyConfig returns a *config.Config with no clusters, used to test edge
// cases where the host list is empty.
func emptyConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{},
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

// ---------------------------------------------------------------------------
// Sub-AC 1 – multi-select support for dirty hosts + combined visual indicators
// ---------------------------------------------------------------------------

// TestDirtyHostIsSelectableWithSpace verifies that a dirty host (one with
// pending SSH key cleanup) can be selected via the Space key just like any
// other host. The dirty state must not prevent selection.
func TestDirtyHostIsSelectableWithSpace(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	// Mark host-01 as dirty.
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Find host-01 in the flat list and select it.
	hostIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil && node.Host.Host == "host-01" {
			hostIdx = i
			break
		}
	}
	if hostIdx < 0 {
		t.Fatal("host-01 not found in flat list")
	}

	m.view.Cursor = hostIdx
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 selected host after Space on dirty host, got %d", len(hosts))
	}
	if hosts[0].Host != "host-01" {
		t.Errorf("selected host = %q, want host-01", hosts[0].Host)
	}
}

// TestDirtySelectedHostShowsWarningGlyph verifies that a host which is both
// selected AND dirty still displays the ⚠ warning glyph in the inventory row.
// The glyph must be visible regardless of selection state, so the user always
// knows which hosts have pending cleanup work.
func TestDirtySelectedHostShowsWarningGlyph(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Select host-01 (it is also dirty).
	hostIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() && node.Host != nil && node.Host.Host == "host-01" {
			hostIdx = i
			break
		}
	}
	if hostIdx < 0 {
		t.Fatal("host-01 not found in flat list")
	}
	m.view.Cursor = hostIdx
	m, _ = sendKey(m, " ")

	// Verify it is selected.
	if len(m.selectedHosts()) != 1 {
		t.Fatal("host-01 should be selected")
	}

	// Verify the ⚠ glyph is present in the rendered row for host-01.
	lines := m.renderList()
	found := false
	for _, line := range lines {
		if strings.Contains(line, "host-01") && strings.Contains(line, "⚠") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("selected+dirty host-01 should render with ⚠ glyph; lines: %v", lines)
	}
}

// TestDirtySelectedHostShowsCheckbox verifies that a dirty host which is also
// selected renders the [✓] checkbox (selection indicator), so the user can
// clearly see both the selection state and the dirty state at a glance.
func TestDirtySelectedHostShowsCheckbox(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Select host-01.
	hostIdx := -1
	for i, node := range m.flatNodes {
		if node.IsHost() {
			hostIdx = i
			break
		}
	}
	if hostIdx < 0 {
		t.Fatal("no host node found")
	}
	m.view.Cursor = hostIdx
	m, _ = sendKey(m, " ")

	lines := m.renderList()
	found := false
	for _, line := range lines {
		if strings.Contains(line, "host-01") && strings.Contains(line, "✓") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("selected dirty host-01 should render with [✓] checkbox; lines: %v", lines)
	}
}

// TestClusterSpaceSelectsDirtyHosts verifies that pressing Space on a cluster
// header selects ALL hosts in the cluster including dirty ones.
func TestClusterSpaceSelectsDirtyHosts(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
					{Name: "host-02", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	// Mark host-01 as dirty; host-02 is clean.
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Space on the cluster header should select both hosts including the dirty one.
	m.view.Cursor = 0 // cluster header node
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 2 {
		t.Errorf("Space on cluster should select all 2 hosts (including dirty), got %d", len(hosts))
	}

	// Verify host-01 (dirty) is among the selected.
	dirty01Selected := false
	for _, h := range hosts {
		if h.Host == "host-01" {
			dirty01Selected = true
			break
		}
	}
	if !dirty01Selected {
		t.Error("dirty host-01 should be selected after Space on cluster header")
	}
}

// TestMultipleDirtyHostsAllSelectable verifies that a cluster where all hosts
// are dirty can still have all of them selected simultaneously.
func TestMultipleDirtyHostsAllSelectable(t *testing.T) {
	cfg := &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
					{Name: "host-02", Provenance: config.ProvenanceFull},
					{Name: "host-03", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
	// All three hosts are dirty.
	dirtySet := map[string]bool{
		"host-01": true,
		"host-02": true,
		"host-03": true,
	}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Select all via cluster header.
	m.view.Cursor = 0
	m, _ = sendKey(m, " ")

	hosts := m.selectedHosts()
	if len(hosts) != 3 {
		t.Errorf("all 3 dirty hosts should be selectable; got %d selected", len(hosts))
	}

	// All rendered rows for hosts should show both ⚠ and ✓.
	lines := m.renderList()
	for _, hostName := range []string{"host-01", "host-02", "host-03"} {
		foundGlyph := false
		foundCheck := false
		for _, line := range lines {
			if strings.Contains(line, hostName) {
				if strings.Contains(line, "⚠") {
					foundGlyph = true
				}
				if strings.Contains(line, "✓") {
					foundCheck = true
				}
			}
		}
		if !foundGlyph {
			t.Errorf("dirty+selected %s should show ⚠ glyph", hostName)
		}
		if !foundCheck {
			t.Errorf("dirty+selected %s should show ✓ checkbox", hostName)
		}
	}
}

// ---------------------------------------------------------------------------
// Dirty-state inventory marker tests
// ---------------------------------------------------------------------------

// dirtyConfig returns a config with two hosts whose SSH addresses (ResolvedHost.Host)
// are "host-01" and "host-02" (HostEntry.Name is used as the SSH address by Resolve).
func dirtyConfig() *config.Config {
	return &config.Config{
		Clusters: map[string]config.ClusterConfig{
			"prod": {
				Hosts: []config.HostEntry{
					{Name: "host-01", Provenance: config.ProvenanceFull},
					{Name: "host-02", Provenance: config.ProvenanceFull},
				},
			},
		},
	}
}

// TestDirtyHostRendersWarningGlyph verifies that a host flagged as dirty in
// the model's dirtyHosts map is rendered with the ⚠ warning glyph in the
// inventory list.
//
// HostEntry.Name doubles as the SSH address (ResolvedHost.Host) per the
// Resolve() contract, so "host-01" is both the display name and the SSH address.
func TestDirtyHostRendersWarningGlyph(t *testing.T) {
	cfg := dirtyConfig()
	// "host-01" is both the display name and the SSH address for this host entry.
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	lines := m.renderList()
	// Expect at least: cluster header + 2 host rows.
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 rendered lines, got %d", len(lines))
	}

	found := false
	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-01") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dirty host-01 should render with ⚠ glyph in inventory view; lines: %v", lines)
	}
}

// TestCleanHostDoesNotRenderWarningGlyph verifies that a host NOT flagged as
// dirty is never rendered with the ⚠ glyph.
func TestCleanHostDoesNotRenderWarningGlyph(t *testing.T) {
	cfg := dirtyConfig()
	// Only host-01 is dirty; host-02 is clean.
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	lines := m.renderList()
	for _, line := range lines {
		if strings.Contains(line, "host-02") && strings.Contains(line, "⚠") {
			t.Errorf("clean host-02 must not render with ⚠ glyph; line: %q", line)
		}
	}
}

// TestNoDirtyHostsNoWarningGlyph verifies that when no hosts are dirty the ⚠
// glyph never appears in any inventory row.
func TestNoDirtyHostsNoWarningGlyph(t *testing.T) {
	cfg := dirtyConfig()
	m := withWindowSize(New(cfg, WithDirtyHosts(map[string]bool{})), 80, 24)

	lines := m.renderList()
	for _, line := range lines {
		if strings.Contains(line, "⚠") {
			t.Errorf("no dirty hosts set, but ⚠ appeared in line: %q", line)
		}
	}
}

// TestDirtyStatusBarLegendPresent verifies that the status bar includes the
// dirty-state legend message when dirty hosts are present.
func TestDirtyStatusBarLegendPresent(t *testing.T) {
	cfg := dirtyConfig()
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	view := m.View()
	if !strings.Contains(view, "need key cleanup") {
		t.Error("status bar should contain 'need key cleanup' legend when dirty hosts are present")
	}
	if !strings.Contains(view, "⚠") {
		t.Error("status bar should contain ⚠ glyph in legend when dirty hosts are present")
	}
}

// TestCleanStatusBarNoLegend verifies that the status bar does NOT include
// the dirty-state legend when no hosts are dirty.
func TestCleanStatusBarNoLegend(t *testing.T) {
	cfg := dirtyConfig()
	m := withWindowSize(New(cfg, WithDirtyHosts(map[string]bool{})), 80, 24)

	view := m.View()
	if strings.Contains(view, "need key cleanup") {
		t.Error("status bar must not contain dirty legend when no hosts are dirty")
	}
}

// TestDirtyHostCountInLegend verifies that the legend displays the correct
// count of dirty hosts.
func TestDirtyHostCountInLegend(t *testing.T) {
	cfg := dirtyConfig()
	dirtySet := map[string]bool{"host-01": true, "host-02": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	view := m.View()
	if !strings.Contains(view, "2 host(s) need key cleanup") {
		t.Errorf("status bar legend should mention '2 host(s) need key cleanup', got view:\n%s", view)
	}
}

// TestDirtyHostWarningGlyphOnCursorRow verifies that even when the cursor is
// positioned on a dirty host the ⚠ glyph is visible in the rendered line.
func TestDirtyHostWarningGlyphOnCursorRow(t *testing.T) {
	cfg := dirtyConfig()
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	// Navigate to host-01 (index 1 in flat list: cluster header=0, host-01=1).
	m.view.Cursor = 1
	lines := m.renderList()

	if len(lines) < 2 {
		t.Fatal("expected at least 2 rendered lines")
	}
	cursorLine := lines[1]
	if !strings.Contains(cursorLine, "⚠") {
		t.Errorf("cursor row on dirty host-01 should still display ⚠ glyph; got: %q", cursorLine)
	}
}

// TestWithDirtyHostsOption verifies that WithDirtyHosts correctly overrides
// the auto-loaded dirty state, making it possible to test marker rendering
// without touching ~/.smux/dirty-state.json.
func TestWithDirtyHostsOption(t *testing.T) {
	cfg := dirtyConfig()

	// Create two models: one with dirty overrides, one without.
	mDirty := New(cfg, WithDirtyHosts(map[string]bool{"host-01": true}))
	mClean := New(cfg, WithDirtyHosts(map[string]bool{}))

	if !mDirty.dirtyHosts["host-01"] {
		t.Error("WithDirtyHosts: expected host-01 to be marked dirty")
	}
	if mDirty.dirtyHosts["host-02"] {
		t.Error("WithDirtyHosts: host-02 should not be dirty")
	}
	if len(mClean.dirtyHosts) != 0 {
		t.Errorf("WithDirtyHosts(empty): expected no dirty hosts, got %d", len(mClean.dirtyHosts))
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

// ---------------------------------------------------------------------------
// Sub-AC 2 – Startup dirty-state warning dialog
// ---------------------------------------------------------------------------

// makeDirtyState is a test helper that builds a *dirtystate.State from a
// slice of (host, keyComment) pairs.  AddedAt is set to a fixed timestamp so
// tests are deterministic.
func makeDirtyState(pairs ...string) *dirtystate.State {
	if len(pairs)%2 != 0 {
		panic("makeDirtyState: pairs must have even length (host, keyComment)")
	}
	s := &dirtystate.State{}
	for i := 0; i < len(pairs); i += 2 {
		s.Add(dirtystate.DirtyHost{
			Host:       pairs[i],
			KeyComment: pairs[i+1],
			AddedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
	}
	return s
}

// TestDirtyStateWarningPhaseActivatedOnStartup verifies that injecting a
// non-empty dirty state via WithDirtyState causes the model to start in
// DirtyStateWarningPhase rather than BrowsingPhase.
func TestDirtyStateWarningPhaseActivatedOnStartup(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc123")
	m := New(cfg, WithDirtyState(ds))

	if _, ok := m.state.Phase.(DirtyStateWarningPhase); !ok {
		t.Errorf("expected DirtyStateWarningPhase when non-empty dirty state is injected; got %T",
			m.state.Phase)
	}
}

// TestDirtyStateWarningPhaseNotShownWhenClean verifies that an empty dirty
// state does NOT trigger the warning dialog: the initial phase is BrowsingPhase.
func TestDirtyStateWarningPhaseNotShownWhenClean(t *testing.T) {
	cfg := dirtyConfig()
	ds := &dirtystate.State{} // empty
	m := New(cfg, WithDirtyState(ds))

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("expected BrowsingPhase for empty dirty state; got %T", m.state.Phase)
	}
}

// TestDirtyStateWarningViewContainsHosts verifies that the warning dialog view
// includes the SSH addresses of every dirty host.
func TestDirtyStateWarningViewContainsHosts(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc", "host-02", "smux-distribute-xyz")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 120, 40)

	view := m.View()

	if !strings.Contains(view, "host-01") {
		t.Error("dirty-state warning view should list host-01")
	}
	if !strings.Contains(view, "host-02") {
		t.Error("dirty-state warning view should list host-02")
	}
}

// TestDirtyStateWarningViewContainsKeyComment verifies that the key comment
// (the unique identifier for the temporary SSH key) is visible in the warning
// dialog for spoke-type dirty records.
func TestDirtyStateWarningViewContainsKeyComment(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc123ef")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 120, 40)

	view := m.View()
	if !strings.Contains(view, "smux-distribute-abc123ef") {
		t.Error("dirty-state warning view should display the key comment for spoke records")
	}
}

// TestDirtyStateWarningViewShowsHubKeyDir verifies that hub-type dirty records
// (HubKeyDir != "") show the remote directory path instead of a key comment.
func TestDirtyStateWarningViewShowsHubKeyDir(t *testing.T) {
	cfg := dirtyConfig()
	ds := &dirtystate.State{}
	ds.Add(dirtystate.DirtyHost{
		Host:       "hub-01",
		KeyComment: "smux-distribute-hub123",
		HubKeyDir:  "/tmp/smux-distribute-ABCDEF",
		AddedAt:    time.Now(),
	})
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 120, 40)

	view := m.View()
	if !strings.Contains(view, "/tmp/smux-distribute-ABCDEF") {
		t.Error("dirty-state warning view should show HubKeyDir for hub records")
	}
}

// TestDirtyStateWarningViewShowsCountInTitle verifies that the host count is
// visible in the dialog title.
func TestDirtyStateWarningViewShowsCountInTitle(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-a", "host-02", "smux-distribute-b")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 120, 40)

	view := m.View()
	if !strings.Contains(view, "2 host(s)") {
		t.Errorf("dirty-state warning title should show '2 host(s)'; got:\n%s", view)
	}
}

// TestDirtyStateWarningViewContainsHints verifies that the warning dialog
// shows key-binding hints for acknowledge ('y'/'Enter') and cleanup ('c').
func TestDirtyStateWarningViewContainsHints(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 120, 40)

	view := m.View()
	if !strings.Contains(view, "acknowledge") {
		t.Error("dirty-state warning view should mention 'acknowledge'")
	}
	if !strings.Contains(view, "clean up") {
		t.Error("dirty-state warning view should mention 'clean up'")
	}
}

// TestDirtyStateWarningAcknowledgeWithY verifies that pressing 'y' in the
// warning dialog transitions the model to BrowsingPhase without triggering
// cleanup.
func TestDirtyStateWarningAcknowledgeWithY(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	if _, ok := m.state.Phase.(DirtyStateWarningPhase); !ok {
		t.Fatal("expected DirtyStateWarningPhase at start")
	}

	m, cmd := sendKey(m, "y")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing 'y' should transition to BrowsingPhase; got %T", m.state.Phase)
	}
	if cmd != nil {
		t.Error("pressing 'y' (acknowledge) should return nil cmd, not trigger any background work")
	}
}

// TestDirtyStateWarningAcknowledgeWithEnter verifies that pressing Enter has
// the same effect as pressing 'y' (acknowledge without cleanup).
func TestDirtyStateWarningAcknowledgeWithEnter(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	m, cmd := sendKey(m, "enter")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing Enter should transition to BrowsingPhase; got %T", m.state.Phase)
	}
	if cmd != nil {
		t.Error("pressing Enter (acknowledge) should return nil cmd")
	}
}

// TestDirtyStateWarningQuitWithQ verifies that pressing 'q' from the warning
// dialog exits smux (sets Done + Quit result + returns tea.Quit).
func TestDirtyStateWarningQuitWithQ(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	m, cmd := sendKey(m, "q")

	if !m.Done() {
		t.Error("pressing 'q' in dirty-state warning should mark model as done")
	}
	if !m.GetResult().Quit {
		t.Error("pressing 'q' in dirty-state warning should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' in dirty-state warning should return tea.Quit command")
	}
}

// TestDirtyStateWarningQuitWithCtrlC verifies that pressing Ctrl+C from the
// warning dialog exits smux.
func TestDirtyStateWarningQuitWithCtrlC(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	m, cmd := sendKey(m, "ctrl+c")

	if !m.Done() {
		t.Error("Ctrl+C in dirty-state warning should mark model as done")
	}
	if !m.GetResult().Quit {
		t.Error("Ctrl+C in dirty-state warning should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C in dirty-state warning should return tea.Quit command")
	}
}

// TestDirtyStateWarningCleanupTrigger verifies that pressing 'c' in the
// warning dialog sets the Cleaning flag on DirtyStateWarningPhase and returns
// a non-nil background command.
func TestDirtyStateWarningCleanupTrigger(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	m, cmd := sendKey(m, "c")

	phase, ok := m.state.Phase.(DirtyStateWarningPhase)
	if !ok {
		t.Fatalf("after pressing 'c' expected DirtyStateWarningPhase; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("pressing 'c' should set DirtyStateWarningPhase.Cleaning = true")
	}
	if cmd == nil {
		t.Error("pressing 'c' should return a non-nil background cleanup command")
	}
}

// TestDirtyStateWarningCleaningViewShownWhileCleaning verifies that the view
// switches to a "Cleaning up…" message once Cleaning is set.
func TestDirtyStateWarningCleaningViewShownWhileCleaning(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	// Trigger cleanup.
	m, _ = sendKey(m, "c")

	view := m.View()
	if !strings.Contains(view, "Cleaning") {
		t.Errorf("view while cleaning should contain 'Cleaning'; got:\n%s", view)
	}
}

// TestDirtyStateWarningKeysIgnoredWhileCleaning verifies that key presses
// other than the ones handled are ignored while cleanup is in progress.
func TestDirtyStateWarningKeysIgnoredWhileCleaning(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	// Start cleaning.
	m, _ = sendKey(m, "c")
	if _, ok := m.state.Phase.(DirtyStateWarningPhase); !ok {
		t.Fatal("expected DirtyStateWarningPhase after pressing c")
	}

	// 'y' should be ignored while cleaning (still in warning phase).
	m2, _ := sendKey(m, "y")
	if _, ok := m2.state.Phase.(DirtyStateWarningPhase); !ok {
		t.Error("'y' should be ignored while cleaning; phase should remain DirtyStateWarningPhase")
	}
}

// TestDirtyCleanupCompleteTransitionsToBrowsing verifies that delivering a
// dirtyCleanupCompleteMsg transitions the model from DirtyStateWarningPhase
// to BrowsingPhase.
func TestDirtyCleanupCompleteTransitionsToBrowsing(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	// Trigger cleanup.
	m, _ = sendKey(m, "c")

	// Deliver the cleanup-complete message.
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("after cleanup complete, expected BrowsingPhase; got %T", m.state.Phase)
	}
}

// TestDirtyStateWarningNotShownWhenNoDirtyState verifies that when no dirty
// state is injected (the default for most tests), the model starts directly in
// BrowsingPhase — the warning dialog never appears.
func TestDirtyStateWarningNotShownWhenNoDirtyState(t *testing.T) {
	// Use WithDirtyHosts (empty) to ensure no disk-loaded state interferes.
	m := withWindowSize(New(minimalConfig(), WithDirtyHosts(map[string]bool{})), 80, 24)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("model with no dirty state should start in BrowsingPhase; got %T",
			m.state.Phase)
	}
	view := m.View()
	if strings.Contains(view, "Pending SSH Key Cleanup") {
		t.Error("view should not show dirty-state warning when there is no dirty state")
	}
}

// TestWithDirtyHostsDoesNotTriggerWarningPhase verifies that WithDirtyHosts
// (the inventory-marker test helper) does NOT trigger the DirtyStateWarningPhase.
// This protects existing inventory-marker tests from being broken by the
// startup warning feature.
func TestWithDirtyHostsDoesNotTriggerWarningPhase(t *testing.T) {
	cfg := dirtyConfig()
	dirtySet := map[string]bool{"host-01": true}
	m := New(cfg, WithDirtyHosts(dirtySet))

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("WithDirtyHosts should leave phase as BrowsingPhase; got %T", m.state.Phase)
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 3 – Exit dirty-state warning dialog (QuitDirtyWarningPhase)
// ---------------------------------------------------------------------------

// TestQKeyWithDirtyStateShowsExitWarning verifies that pressing 'q' in
// BrowsingPhase when dirty state is present transitions to
// QuitDirtyWarningPhase instead of quitting immediately.
func TestQKeyWithDirtyStateShowsExitWarning(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	// WithDirtyState sets DirtyStateWarningPhase; acknowledge it first.
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	// Acknowledge startup warning to enter BrowsingPhase.
	m, _ = sendKey(m, "y")
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("expected BrowsingPhase after 'y', got %T", m.state.Phase)
	}

	// Press 'q' — should show exit dirty-state warning, not quit.
	m, cmd := sendKey(m, "q")

	phase, ok := m.state.Phase.(QuitDirtyWarningPhase)
	if !ok {
		t.Fatalf("pressing 'q' with dirty state should enter QuitDirtyWarningPhase; got %T", m.state.Phase)
	}
	if len(phase.Hosts) == 0 {
		t.Error("QuitDirtyWarningPhase.Hosts should contain the dirty hosts")
	}
	if m.Done() {
		t.Error("model should not be done yet; user has not confirmed quit")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing 'q' with dirty state should not return tea.Quit command immediately")
	}
}

// TestQKeyWithNoDirtyStateQuitsImmediately verifies that pressing 'q' when
// there is no dirty state still quits immediately (no regression).
func TestQKeyWithNoDirtyStateQuitsImmediately(t *testing.T) {
	m := withWindowSize(New(minimalConfig(), WithDirtyHosts(map[string]bool{})), 80, 24)

	m, cmd := sendKey(m, "q")

	if !m.Done() {
		t.Error("pressing 'q' with no dirty state should mark model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' with no dirty state should return tea.Quit")
	}
}

// TestQuitDirtyWarningYQuitsWithoutCleanup verifies that pressing 'y' in
// QuitDirtyWarningPhase quits immediately without performing cleanup.
func TestQuitDirtyWarningYQuitsWithoutCleanup(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning

	// Enter QuitDirtyWarningPhase via 'q'.
	m, _ = sendKey(m, "q")
	if _, ok := m.state.Phase.(QuitDirtyWarningPhase); !ok {
		t.Fatalf("expected QuitDirtyWarningPhase, got %T", m.state.Phase)
	}

	// Press 'y' to quit without cleanup.
	m, cmd := sendKey(m, "y")

	if !m.Done() {
		t.Error("pressing 'y' in QuitDirtyWarningPhase should mark model as done")
	}
	if !m.GetResult().Quit {
		t.Error("pressing 'y' in QuitDirtyWarningPhase should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'y' in QuitDirtyWarningPhase should return tea.Quit")
	}
}

// TestQuitDirtyWarningEnterQuitsWithoutCleanup verifies that Enter also
// triggers an immediate quit from QuitDirtyWarningPhase.
func TestQuitDirtyWarningEnterQuitsWithoutCleanup(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

	m, cmd := sendKey(m, "enter")

	if !m.Done() {
		t.Error("pressing Enter in QuitDirtyWarningPhase should mark model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing Enter in QuitDirtyWarningPhase should return tea.Quit")
	}
}

// TestQuitDirtyWarningNEscCancelsQuit verifies that pressing 'n' or Esc in
// QuitDirtyWarningPhase cancels the quit and returns to BrowsingPhase.
func TestQuitDirtyWarningNEscCancelsQuit(t *testing.T) {
	for _, key := range []string{"n", "esc"} {
		t.Run("key="+key, func(t *testing.T) {
			cfg := dirtyConfig()
			ds := makeDirtyState("host-01", "smux-distribute-abc")
			m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
			m, _ = sendKey(m, "y") // acknowledge startup warning
			m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

			m, cmd := sendKey(m, key)

			if _, ok := m.state.Phase.(BrowsingPhase); !ok {
				t.Errorf("pressing %q should return to BrowsingPhase; got %T", key, m.state.Phase)
			}
			if m.Done() {
				t.Errorf("pressing %q should not mark model as done", key)
			}
			if isQuitCmd(cmd) {
				t.Errorf("pressing %q should not return tea.Quit", key)
			}
		})
	}
}

// TestQuitDirtyWarningCtrlCEmergencyQuit verifies that Ctrl+C in
// QuitDirtyWarningPhase performs an emergency quit (no cleanup).
func TestQuitDirtyWarningCtrlCEmergencyQuit(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

	m, cmd := sendKey(m, "ctrl+c")

	if !m.Done() {
		t.Error("Ctrl+C in QuitDirtyWarningPhase should mark model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C in QuitDirtyWarningPhase should return tea.Quit")
	}
}

// TestQuitDirtyWarningCStartsCleanup verifies that pressing 'c' in
// QuitDirtyWarningPhase sets Cleaning=true and returns a non-nil tea.Cmd
// (the background cleanup goroutine).
func TestQuitDirtyWarningCStartsCleanup(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

	m, cmd := sendKey(m, "c")

	phase, ok := m.state.Phase.(QuitDirtyWarningPhase)
	if !ok {
		t.Fatalf("after pressing 'c' expected QuitDirtyWarningPhase; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("pressing 'c' should set QuitDirtyWarningPhase.Cleaning = true")
	}
	if cmd == nil {
		t.Error("pressing 'c' should return a non-nil background cleanup command")
	}
	if m.Done() {
		t.Error("model should not be done yet while cleanup is running")
	}
}

// TestQuitDirtyWarningCleaningIgnoresKeys verifies that key presses are
// ignored while cleanup is in progress (Cleaning=true).
func TestQuitDirtyWarningCleaningIgnoresKeys(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase
	m, _ = sendKey(m, "c") // start cleaning

	// 'y' should be ignored while cleaning.
	m2, _ := sendKey(m, "y")
	if _, ok := m2.state.Phase.(QuitDirtyWarningPhase); !ok {
		t.Error("'y' should be ignored while cleaning; phase should remain QuitDirtyWarningPhase")
	}
	if m2.Done() {
		t.Error("model should not be done while cleanup is running")
	}
}

// TestQuitDirtyWarningCleanupCompleteQuitsSmux verifies that delivering a
// quitDirtyCleanupCompleteMsg causes the model to quit.
func TestQuitDirtyWarningCleanupCompleteQuitsSmux(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase
	m, _ = sendKey(m, "c") // start cleaning

	// Deliver the quit-cleanup-complete message.
	updated, cmd := m.Update(quitDirtyCleanupCompleteMsg{err: nil, needsWindowKill: false})
	m = updated.(Model)

	if !m.Done() {
		t.Error("after quitDirtyCleanupCompleteMsg, model should be done")
	}
	if !m.GetResult().Quit {
		t.Error("after quitDirtyCleanupCompleteMsg, Result.Quit should be true")
	}
	if !isQuitCmd(cmd) {
		t.Error("after quitDirtyCleanupCompleteMsg, should return tea.Quit command")
	}
}

// TestQuitDirtyWarningViewContainsDirtyHosts verifies that the exit
// dirty-state warning dialog view includes the dirty host names.
func TestQuitDirtyWarningViewContainsDirtyHosts(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc", "host-02", "smux-distribute-def")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

	view := m.View()
	if !strings.Contains(view, "host-01") {
		t.Errorf("exit dirty-state warning view should mention 'host-01'; got:\n%s", view)
	}
	if !strings.Contains(view, "host-02") {
		t.Errorf("exit dirty-state warning view should mention 'host-02'; got:\n%s", view)
	}
	if !strings.Contains(view, "Unresolved SSH Key Cleanup") {
		t.Errorf("exit dirty-state warning view should contain title; got:\n%s", view)
	}
}

// TestQuitDirtyWarningViewShowsCleaningWhileCleaning verifies that the view
// switches to a "Cleaning up…" message once Cleaning is set.
func TestQuitDirtyWarningViewShowsCleaningWhileCleaning(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase
	m, _ = sendKey(m, "c") // trigger cleanup (Cleaning=true)

	view := m.View()
	if !strings.Contains(view, "Cleaning") {
		t.Errorf("view while cleaning should contain 'Cleaning'; got:\n%s", view)
	}
}

// TestQuitDirtyWarningViewShowsKeyBindings verifies that the exit
// dirty-state warning dialog shows the key binding hints.
func TestQuitDirtyWarningViewShowsKeyBindings(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // enter QuitDirtyWarningPhase

	view := m.View()
	// Must mention the three options.
	for _, hint := range []string{"c", "y", "n"} {
		if !strings.Contains(view, hint) {
			t.Errorf("exit dirty-state warning view should mention key %q; got:\n%s", hint, view)
		}
	}
}

// TestPersistentQuitWithDirtyStateShowsExitWarning verifies that in
// persistent mode, when the user confirms quit (QuitConfirmingPhase → 'y')
// while dirty state is present, the model transitions to QuitDirtyWarningPhase
// instead of killing windows and quitting immediately.
func TestPersistentQuitWithDirtyStateShowsExitWarning(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")

	windowsKilled := false
	m := withWindowSize(New(cfg, WithDirtyState(ds),
		WithPersistentMode(
			func() int { return 1 },
			func() error { windowsKilled = true; return nil },
		),
	), 80, 24)

	// Acknowledge startup warning.
	m, _ = sendKey(m, "y")
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("expected BrowsingPhase after 'y', got %T", m.state.Phase)
	}

	// Press 'q' in persistent mode → should enter QuitConfirmingPhase.
	m, _ = sendKey(m, "q")
	if _, ok := m.state.Phase.(QuitConfirmingPhase); !ok {
		t.Fatalf("persistent 'q' should enter QuitConfirmingPhase; got %T", m.state.Phase)
	}

	// Confirm quit ('y') → should enter QuitDirtyWarningPhase (not quit yet).
	m, cmd := sendKey(m, "y")
	phase, ok := m.state.Phase.(QuitDirtyWarningPhase)
	if !ok {
		t.Fatalf("confirming persistent quit with dirty state should enter QuitDirtyWarningPhase; got %T", m.state.Phase)
	}
	if !phase.NeedsWindowKill {
		t.Error("QuitDirtyWarningPhase.NeedsWindowKill should be true in persistent mode")
	}
	if m.Done() {
		t.Error("model should not be done yet")
	}
	if isQuitCmd(cmd) {
		t.Error("should not quit immediately; dirty warning must be shown first")
	}
	if windowsKilled {
		t.Error("managed windows should not be killed before dirty warning is resolved")
	}
}

// TestPersistentQuitDirtyWarningYKillsWindowsAndQuits verifies that pressing
// 'y' in QuitDirtyWarningPhase when NeedsWindowKill=true calls
// killManagedWindows before quitting.
func TestPersistentQuitDirtyWarningYKillsWindowsAndQuits(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")

	windowsKilled := false
	m := withWindowSize(New(cfg, WithDirtyState(ds),
		WithPersistentMode(
			func() int { return 1 },
			func() error { windowsKilled = true; return nil },
		),
	), 80, 24)

	// Navigate to QuitDirtyWarningPhase with NeedsWindowKill=true.
	m, _ = sendKey(m, "y") // ack startup warning
	m, _ = sendKey(m, "q") // → QuitConfirmingPhase
	m, _ = sendKey(m, "y") // → QuitDirtyWarningPhase (NeedsWindowKill=true)

	if _, ok := m.state.Phase.(QuitDirtyWarningPhase); !ok {
		t.Fatalf("expected QuitDirtyWarningPhase, got %T", m.state.Phase)
	}

	// Confirm quit without cleanup.
	m, cmd := sendKey(m, "y")

	if !m.Done() {
		t.Error("model should be done after confirming quit")
	}
	if !isQuitCmd(cmd) {
		t.Error("should return tea.Quit command")
	}
	if !windowsKilled {
		t.Error("killManagedWindows should be called when NeedsWindowKill=true")
	}
}

// TestPersistentQuitNoDirtyStateStillQuitsNormally verifies that in
// persistent mode, if there is no dirty state, the quit-confirm dialog still
// kills windows and quits without showing the dirty warning.
func TestPersistentQuitNoDirtyStateStillQuitsNormally(t *testing.T) {
	windowsKilled := false
	m := withWindowSize(New(minimalConfig(), WithDirtyHosts(map[string]bool{}),
		WithPersistentMode(
			func() int { return 1 },
			func() error { windowsKilled = true; return nil },
		),
	), 80, 24)

	// Press 'q' → QuitConfirmingPhase.
	m, _ = sendKey(m, "q")
	if _, ok := m.state.Phase.(QuitConfirmingPhase); !ok {
		t.Fatalf("expected QuitConfirmingPhase, got %T", m.state.Phase)
	}

	// Confirm → should kill windows and quit immediately (no dirty warning).
	m, cmd := sendKey(m, "y")

	if _, ok := m.state.Phase.(QuitDirtyWarningPhase); ok {
		t.Error("should not enter QuitDirtyWarningPhase when no dirty state")
	}
	if !m.Done() {
		t.Error("model should be done after confirming quit")
	}
	if !isQuitCmd(cmd) {
		t.Error("should return tea.Quit")
	}
	if !windowsKilled {
		t.Error("managed windows should be killed when no dirty state")
	}
}

// TestQuitDirtyWarningCleanupCompleteWithWindowKill verifies that
// quitDirtyCleanupCompleteMsg with NeedsWindowKill=true calls
// killManagedWindows before quitting.
func TestQuitDirtyWarningCleanupCompleteWithWindowKill(t *testing.T) {
	cfg := dirtyConfig()
	ds := makeDirtyState("host-01", "smux-distribute-abc")

	windowsKilled := false
	m := withWindowSize(New(cfg, WithDirtyState(ds),
		WithPersistentMode(
			func() int { return 1 },
			func() error { windowsKilled = true; return nil },
		),
	), 80, 24)

	m, _ = sendKey(m, "y") // ack startup warning
	m, _ = sendKey(m, "q") // → QuitConfirmingPhase
	m, _ = sendKey(m, "y") // → QuitDirtyWarningPhase (NeedsWindowKill=true)
	m, _ = sendKey(m, "c") // start cleanup

	// Deliver cleanup-complete with needsWindowKill=true.
	updated, cmd := m.Update(quitDirtyCleanupCompleteMsg{err: nil, needsWindowKill: true})
	m = updated.(Model)

	if !m.Done() {
		t.Error("model should be done after cleanup-complete with quit")
	}
	if !isQuitCmd(cmd) {
		t.Error("should return tea.Quit after cleanup-complete")
	}
	if !windowsKilled {
		t.Error("killManagedWindows should be called when needsWindowKill=true")
	}
}
