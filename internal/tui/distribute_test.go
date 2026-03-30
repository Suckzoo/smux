package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// sendDistributeKey delivers a key to a DistributeModel and returns the
// updated model plus the resulting command.
func sendDistributeKey(m DistributeModel, keyStr string) (DistributeModel, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyStr)}
	switch keyStr {
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+d":
		msg = tea.KeyMsg{Type: tea.KeyCtrlD}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	}
	return m.Update(msg)
}

// newTestDistributeModel is a helper that creates a DistributeModel with a
// known terminal size for testing.
func newTestDistributeModel() DistributeModel {
	return NewDistributeModel(minimalConfig(), 80, 24)
}

// ---------------------------------------------------------------------------
// Step navigation
// ---------------------------------------------------------------------------

// TestDistributeInitialStep verifies that a fresh DistributeModel starts on
// step 0 (source selection).
func TestDistributeInitialStep(t *testing.T) {
	m := newTestDistributeModel()
	if m.step != DistributeStepSourceSelect {
		t.Errorf("expected initial step %d, got %d", DistributeStepSourceSelect, m.step)
	}
}

// TestDistributeEnterAdvancesStep verifies that the correct key sequences
// advance the wizard through all steps.
//
// The file-browse step (step 1) uses Ctrl+D (not Enter) to confirm file
// selection, because Enter in the file tree expands/collapses directories.
func TestDistributeEnterAdvancesStep(t *testing.T) {
	m := newTestDistributeModel()

	// Step 0 → 1: Enter creates local file tree and moves to FileBrowse.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepFileBrowse {
		t.Errorf("after first Enter expected step %d (FileBrowse), got %d", DistributeStepFileBrowse, m.step)
	}

	// Step 1 → 2: Ctrl+D confirms file selection and moves to DestHosts.
	m, _ = sendDistributeKey(m, "ctrl+d")
	if m.step != DistributeStepDestHosts {
		t.Errorf("after Ctrl+D expected step %d (DestHosts), got %d", DistributeStepDestHosts, m.step)
	}

	// Step 2 → 3: Enter from DestHosts moves to CopyMode.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepCopyMode {
		t.Errorf("after Enter on DestHosts expected step %d, got %d", DistributeStepCopyMode, m.step)
	}

	// Step 3 → 4: Enter from CopyMode moves to DestPath.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepDestPath {
		t.Errorf("after Enter on CopyMode expected step %d (DestPath), got %d", DistributeStepDestPath, m.step)
	}

	// Step 4 → 5: Enter a valid destination path to move to Confirm.
	m.destPathInput.SetValue("/tmp/dest")
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepConfirm {
		t.Errorf("after Enter on DestPath expected step %d (Confirm), got %d", DistributeStepConfirm, m.step)
	}

	// Step 5 → 6: Enter from Confirm moves to Execute.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepExecute {
		t.Errorf("after Enter on Confirm expected step %d (Execute), got %d", DistributeStepExecute, m.step)
	}
}

// TestDistributeEnterDoesNotAdvancePastLastStep verifies that pressing Enter on
// the last step does not overflow the step counter.
func TestDistributeEnterDoesNotAdvancePastLastStep(t *testing.T) {
	m := newTestDistributeModel()
	// Jump directly to the last step to avoid having to replicate the full
	// key sequence (which requires Ctrl+D at the file-browse step).
	m.step = DistributeStepExecute

	// Additional Enter should not overflow.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepExecute {
		t.Errorf("Enter on last step should stay on last step, got %d", m.step)
	}
}

// TestDistributeEscGoesBackOneStep verifies that pressing Esc from a non-first
// step moves back exactly one step and preserves IsDone() == false.
func TestDistributeEscGoesBackOneStep(t *testing.T) {
	m := newTestDistributeModel()
	// Advance to DestHosts: Enter (→FileBrowse) then Ctrl+D (→DestHosts).
	m, _ = sendDistributeKey(m, "enter")   // step 0 → 1 (FileBrowse)
	m, _ = sendDistributeKey(m, "ctrl+d")  // step 1 → 2 (DestHosts)

	m, _ = sendDistributeKey(m, "esc") // step 2 → 1 (FileBrowse)
	if m.step != DistributeStepFileBrowse {
		t.Errorf("after Esc expected step %d (FileBrowse), got %d", DistributeStepFileBrowse, m.step)
	}
	if m.IsDone() {
		t.Error("Esc from non-first step should not mark wizard as done")
	}
}

// TestDistributeEscFromFirstStepSignalsExitToMain verifies that pressing Esc
// from step 0 sets exitToMain and done without issuing tea.Quit.
func TestDistributeEscFromFirstStepSignalsExitToMain(t *testing.T) {
	m := newTestDistributeModel()

	m, cmd := sendDistributeKey(m, "esc")

	if !m.IsDone() {
		t.Error("Esc from step 0 should mark wizard as done")
	}
	if !m.IsExitToMain() {
		t.Error("Esc from step 0 should set exitToMain = true")
	}
	if m.IsCancelled() {
		t.Error("Esc from step 0 should not set cancelled = true")
	}
	// No tea.Quit command should be returned.
	if isQuitCmd(cmd) {
		t.Error("Esc from step 0 should not issue tea.Quit (smux should keep running)")
	}
}

// ---------------------------------------------------------------------------
// Cancel / quit
// ---------------------------------------------------------------------------

// TestDistributeQKeyCancelsAndQuits verifies that pressing 'q' anywhere in the
// wizard sets cancelled = true and issues tea.Quit.
func TestDistributeQKeyCancelsAndQuits(t *testing.T) {
	for _, step := range []DistributeStep{
		DistributeStepSourceSelect,
		DistributeStepFileBrowse,
		DistributeStepDestHosts,
		DistributeStepCopyMode,
		DistributeStepHubSelect,
		DistributeStepDestPath,
		DistributeStepConfirm,
		DistributeStepExecute,
	} {
		m := newTestDistributeModel()
		m.step = step

		m, cmd := sendDistributeKey(m, "q")

		if !m.IsDone() {
			t.Errorf("step %d: pressing 'q' should mark wizard as done", step)
		}
		if !m.IsCancelled() {
			t.Errorf("step %d: pressing 'q' should set cancelled = true", step)
		}
		if !isQuitCmd(cmd) {
			t.Errorf("step %d: pressing 'q' should issue tea.Quit", step)
		}
	}
}

// TestDistributeCtrlCCancelsAndQuits verifies that pressing Ctrl+C anywhere in
// the wizard sets cancelled = true and issues tea.Quit.
func TestDistributeCtrlCCancelsAndQuits(t *testing.T) {
	m := newTestDistributeModel()

	m, cmd := sendDistributeKey(m, "ctrl+c")

	if !m.IsDone() {
		t.Error("Ctrl+C should mark wizard as done")
	}
	if !m.IsCancelled() {
		t.Error("Ctrl+C should set cancelled = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C should issue tea.Quit")
	}
}

// ---------------------------------------------------------------------------
// View rendering
// ---------------------------------------------------------------------------

// TestDistributeViewContainsTitleAndBreadcrumb verifies that the view output
// includes the wizard title and step breadcrumb text.
func TestDistributeViewContainsTitleAndBreadcrumb(t *testing.T) {
	m := newTestDistributeModel()
	view := m.View()

	if !strings.Contains(view, "distribute file") {
		t.Error("view should contain 'distribute file' title")
	}
	// All direct-parallel step labels should appear in the breadcrumb.
	// The hub-select label ("Select Hub") is only shown in hub-spoke mode and
	// must NOT appear in the default (parallel) breadcrumb.
	for step, label := range distributeStepLabel {
		if step == DistributeStepHubSelect {
			if strings.Contains(view, label) {
				t.Errorf("parallel-mode breadcrumb should not contain hub-select label %q", label)
			}
			continue
		}
		if !strings.Contains(view, label) {
			t.Errorf("view should contain step label %q", label)
		}
	}
}

// TestDistributeViewShowsCurrentStepContent verifies that the view shows
// content appropriate to the current step.
//
// For DistributeStepFileBrowse the test sets the step directly without
// pressing Enter on step 0, so no file tree is initialised.  The wizard
// falls back to rendering normal chrome (with the breadcrumb active on
// "Browse Files") rather than delegating to a tree model.
func TestDistributeViewShowsCurrentStepContent(t *testing.T) {
	cases := []struct {
		step    DistributeStep
		keyword string
	}{
		{DistributeStepSourceSelect, "Source File"},
		{DistributeStepFileBrowse, "Browse Files"},
		{DistributeStepDestHosts, "Destination Hosts"},
		{DistributeStepCopyMode, "Copy Mode"},
		{DistributeStepHubSelect, "Select Hub Node"},
		{DistributeStepDestPath, "Destination Path"},
		{DistributeStepConfirm, "Confirm Distribution"},
		{DistributeStepExecute, "Execute"},
	}

	for _, tc := range cases {
		m := newTestDistributeModel()
		m.step = tc.step
		view := m.View()
		if !strings.Contains(view, tc.keyword) {
			t.Errorf("step %d: expected view to contain %q", tc.step, tc.keyword)
		}
	}
}

// TestDistributeViewTooSmallTerminal verifies that the wizard handles small
// terminal sizes gracefully.
func TestDistributeViewTooSmallTerminal(t *testing.T) {
	m := NewDistributeModel(minimalConfig(), 30, 5)
	view := m.View()
	if !strings.Contains(view, "Terminal too small") {
		t.Error("tiny terminal: expected 'Terminal too small' message")
	}
}

// TestDistributeWindowSizeUpdated verifies that sending a WindowSizeMsg updates
// the wizard's internal width/height.
func TestDistributeWindowSizeUpdated(t *testing.T) {
	m := newTestDistributeModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if updated.width != 120 || updated.height != 40 {
		t.Errorf("expected width=120 height=40, got %d/%d", updated.width, updated.height)
	}
}

// ---------------------------------------------------------------------------
// Integration with parent Model: Ctrl+D entry
// ---------------------------------------------------------------------------

// TestCtrlDEntersDistributeWizard verifies that pressing Ctrl+D in BrowsingPhase
// activates the distribute wizard within the parent Model.
func TestCtrlDEntersDistributeWizard(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)

	m, _ = sendKey(m, "ctrl+d")

	if m.distributeWizard == nil {
		t.Fatal("Ctrl+D should activate the distribute wizard")
	}
	if m.distributeWizard.step != DistributeStepSourceSelect {
		t.Errorf("wizard should start at step 0, got %d", m.distributeWizard.step)
	}
}

// TestDistributeWizardEscFromFirstStepReturnsToNormalTUI verifies that pressing
// Esc from step 0 of the wizard dismisses it and returns to normal browsing.
func TestDistributeWizardEscFromFirstStepReturnsToNormalTUI(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m, _ = sendKey(m, "ctrl+d") // enter wizard

	// Now send Esc through the parent model.
	m, cmd := sendKey(m, "esc")

	if m.distributeWizard != nil {
		t.Error("after Esc from step 0, distributeWizard should be nil (back to normal TUI)")
	}
	if m.Done() {
		t.Error("returning to normal TUI should not mark the main model as done")
	}
	if isQuitCmd(cmd) {
		t.Error("returning to normal TUI should not issue tea.Quit")
	}
}

// TestDistributeWizardQKeyQuitsSmux verifies that pressing 'q' inside the
// wizard exits smux entirely (issues tea.Quit and marks the parent done).
func TestDistributeWizardQKeyQuitsSmux(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m, _ = sendKey(m, "ctrl+d") // enter wizard

	m, cmd := sendKey(m, "q")

	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' inside the wizard should issue tea.Quit")
	}
}

// TestDistributeWizardViewDelegated verifies that while the wizard is active
// the parent Model.View() returns the wizard's view (contains wizard title).
func TestDistributeWizardViewDelegated(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	m, _ = sendKey(m, "ctrl+d")

	view := m.View()
	if !strings.Contains(view, "distribute file") {
		t.Error("while wizard is active, View() should delegate to the wizard (expected 'distribute file')")
	}
}

// TestDistributeWizardNotEnteredInNonBrowsingPhase verifies that Ctrl+D does
// not enter the wizard when the model is in ConfirmingPhase.
func TestDistributeWizardNotEnteredInNonBrowsingPhase(t *testing.T) {
	m := withWindowSize(New(minimalConfig()), 80, 24)
	// Put model into ConfirmingPhase manually.
	m.state.Phase = ConfirmingPhase{Threshold: 50}

	m, _ = sendKey(m, "ctrl+d")

	if m.distributeWizard != nil {
		t.Error("Ctrl+D should not enter the wizard when not in BrowsingPhase")
	}
}

// ---------------------------------------------------------------------------
// Step 0: source origin selection
// ---------------------------------------------------------------------------

// TestSourceOriginInitialCursor verifies that a fresh model starts with the
// source-origin cursor at index 0 (the "local" entry).
func TestSourceOriginInitialCursor(t *testing.T) {
	m := newTestDistributeModel()
	if m.sourceOriginCursor != 0 {
		t.Errorf("expected initial sourceOriginCursor 0, got %d", m.sourceOriginCursor)
	}
}

// TestSourceOriginListIncludesLocal verifies that the source-origin flat list
// always begins with a Local entry.
func TestSourceOriginListIncludesLocal(t *testing.T) {
	m := newTestDistributeModel()
	if len(m.sourceFlatNodes) == 0 {
		t.Fatal("sourceFlatNodes must not be empty")
	}
	if !m.sourceFlatNodes[0].IsLocal() {
		t.Errorf("first node should be NodeKindLocal, got kind=%v", m.sourceFlatNodes[0].Kind)
	}
}

// TestSourceOriginListIncludesConfiguredHosts verifies that remote hosts from
// the config are present in the source-origin list after the local entry.
func TestSourceOriginListIncludesConfiguredHosts(t *testing.T) {
	m := newTestDistributeModel()
	// minimalConfig has host-01 and host-02 in one cluster, so the flat list
	// is: Local + cluster-header + host-01 + host-02 = 4 nodes.
	if len(m.sourceFlatNodes) != 4 {
		t.Errorf("expected 4 source flat nodes (local + cluster + 2 hosts), got %d", len(m.sourceFlatNodes))
	}
	// All NodeKindHost nodes must have a non-nil, non-empty Host.
	for _, n := range m.sourceFlatNodes {
		if n.IsHost() && (n.Host == nil || n.Host.Host == "") {
			t.Errorf("host node should have non-empty Host.Host, got %+v", n)
		}
	}
}

// TestSourceOriginDownMovessCursor verifies that pressing down (or j) advances
// the source-origin cursor.
func TestSourceOriginDownMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	// Cursor starts at 0.
	m, _ = sendDistributeKey(m, "down")
	if m.sourceOriginCursor != 1 {
		t.Errorf("after down: expected cursor 1, got %d", m.sourceOriginCursor)
	}
	m, _ = sendDistributeKey(m, "j")
	if m.sourceOriginCursor != 2 {
		t.Errorf("after j: expected cursor 2, got %d", m.sourceOriginCursor)
	}
}

// TestSourceOriginUpMovesCursor verifies that pressing up (or k) moves the
// source-origin cursor towards index 0.
func TestSourceOriginUpMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.sourceOriginCursor = 2

	m, _ = sendDistributeKey(m, "up")
	if m.sourceOriginCursor != 1 {
		t.Errorf("after up: expected cursor 1, got %d", m.sourceOriginCursor)
	}
	m, _ = sendDistributeKey(m, "k")
	if m.sourceOriginCursor != 0 {
		t.Errorf("after k: expected cursor 0, got %d", m.sourceOriginCursor)
	}
}

// TestSourceOriginCursorDoesNotGoNegative verifies that pressing up at index 0
// keeps the cursor at 0.
func TestSourceOriginCursorDoesNotGoNegative(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "up")
	if m.sourceOriginCursor != 0 {
		t.Errorf("cursor should remain 0 at top of list, got %d", m.sourceOriginCursor)
	}
}

// TestSourceOriginCursorDoesNotExceedList verifies that pressing down past the
// last item keeps the cursor on the last item.
func TestSourceOriginCursorDoesNotExceedList(t *testing.T) {
	m := newTestDistributeModel()
	last := len(m.sourceFlatNodes) - 1
	m.sourceOriginCursor = last
	m, _ = sendDistributeKey(m, "down")
	if m.sourceOriginCursor != last {
		t.Errorf("cursor should stay at %d (last), got %d", last, m.sourceOriginCursor)
	}
}

// TestSourceOriginEnterPersistsLocalSelection verifies that pressing Enter with
// the cursor on the "local" item (index 0) stores an empty sourceHost string
// and advances to the file-browse step.
func TestSourceOriginEnterPersistsLocalSelection(t *testing.T) {
	m := newTestDistributeModel()
	// Cursor is already at 0 (local).
	m, _ = sendDistributeKey(m, "enter")
	if m.sourceHost != "" {
		t.Errorf("local selection: expected sourceHost \"\", got %q", m.sourceHost)
	}
	if m.step != DistributeStepFileBrowse {
		t.Errorf("after Enter on step 0, expected step %d (FileBrowse), got %d", DistributeStepFileBrowse, m.step)
	}
	// A local file tree should have been initialised.
	if m.localFileTree == nil {
		t.Error("local file tree should be initialised after entering FileBrowse step")
	}
}

// TestSourceOriginEnterPersistsRemoteSelection verifies that pressing Enter with
// the cursor on a remote host stores that host's SSH alias in sourceHost and
// advances to the file-browse step.
func TestSourceOriginEnterPersistsRemoteSelection(t *testing.T) {
	m := newTestDistributeModel()
	// Index 0=Local, 1=cluster-header, 2=host-01. Move cursor to host-01.
	m, _ = sendDistributeKey(m, "down")
	m, _ = sendDistributeKey(m, "down")
	wantHost := m.sourceFlatNodes[2].Host.Host

	m, _ = sendDistributeKey(m, "enter")
	if m.sourceHost != wantHost {
		t.Errorf("remote selection: expected sourceHost %q, got %q", wantHost, m.sourceHost)
	}
	if m.step != DistributeStepFileBrowse {
		t.Errorf("after Enter on step 0 expected step %d (FileBrowse), got %d", DistributeStepFileBrowse, m.step)
	}
	// A remote file tree should have been initialised for the chosen host.
	if m.remoteFileTree == nil {
		t.Error("remote file tree should be initialised after entering FileBrowse step with remote source")
	}
	if m.remoteTreeForHost != wantHost {
		t.Errorf("remoteTreeForHost should be %q, got %q", wantHost, m.remoteTreeForHost)
	}
}

// TestSourceOriginCursorPreservedOnBackNav verifies that navigating to the
// file-browse step and then pressing Esc back to step 0 restores the cursor.
func TestSourceOriginCursorPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	// Move cursor to index 2, then advance to FileBrowse.
	m, _ = sendDistributeKey(m, "down")
	m, _ = sendDistributeKey(m, "down")
	m, _ = sendDistributeKey(m, "enter") // step 0 → 1 (FileBrowse)
	if m.step != DistributeStepFileBrowse {
		t.Fatalf("should be on FileBrowse step, got step %d", m.step)
	}

	// Navigate back to step 0.
	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepSourceSelect {
		t.Fatalf("Esc should return to step 0, got step %d", m.step)
	}

	// Cursor should still be at index 2.
	if m.sourceOriginCursor != 2 {
		t.Errorf("sourceOriginCursor should be preserved as 2, got %d", m.sourceOriginCursor)
	}
}

// TestSourceOriginViewRendersHighlightedItem verifies that the step 0 view
// renders the cursor marker (▶) for the highlighted origin item.
func TestSourceOriginViewRendersHighlightedItem(t *testing.T) {
	m := newTestDistributeModel()
	view := m.View()
	// The first item (local) should be highlighted with "▶".
	if !strings.Contains(view, "▶") {
		t.Error("step 0 view should contain cursor marker ▶ for the highlighted item")
	}
}

// TestSourceOriginAccessor verifies that SourceHost() returns the persisted value.
func TestSourceOriginAccessor(t *testing.T) {
	m := newTestDistributeModel()
	// Default: no selection yet; SourceHost() should return "".
	if m.SourceHost() != "" {
		t.Errorf("before selection SourceHost() should be empty, got %q", m.SourceHost())
	}

	// After selecting a remote host (index 2 = host-01, past the cluster header).
	m.sourceOriginCursor = 2
	want := m.sourceFlatNodes[2].Host.Host
	m, _ = sendDistributeKey(m, "enter")
	// Note: m was returned by sendDistributeKey; re-read accessor.
	fresh := m.SourceHost()
	if fresh != want {
		t.Errorf("SourceHost() after selection: expected %q, got %q", want, fresh)
	}
}

// ---------------------------------------------------------------------------
// Step 1: destination host selection
// ---------------------------------------------------------------------------

// TestDestHostInitialState verifies that the destination host list is populated
// from the config and no hosts are selected initially.
func TestDestHostInitialState(t *testing.T) {
	m := newTestDistributeModel()
	if len(m.destHostItems) == 0 {
		t.Fatal("destHostItems must not be empty")
	}
	if len(m.destHostSelected) != 0 {
		t.Errorf("expected no hosts selected initially, got %d", len(m.destHostSelected))
	}
	if m.destHostCursor != 0 {
		t.Errorf("expected initial destHostCursor 0, got %d", m.destHostCursor)
	}
}

// TestDestHostDownMovesCursor verifies that down/j advance the cursor in the
// destination host list.
func TestDestHostDownMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// minimalConfig has 1 cluster + 2 hosts = 3 flat nodes.
	// Cursor starts at 0 (cluster header).
	m, _ = sendDistributeKey(m, "down")
	if m.destHostCursor != 1 {
		t.Errorf("after down: expected cursor 1, got %d", m.destHostCursor)
	}
	m, _ = sendDistributeKey(m, "j")
	if m.destHostCursor != 2 {
		t.Errorf("after j: expected cursor 2, got %d", m.destHostCursor)
	}
	// At last index (2), another down should stay at 2.
	m, _ = sendDistributeKey(m, "down")
	if m.destHostCursor != 2 {
		t.Errorf("after down at last item: expected cursor 2, got %d", m.destHostCursor)
	}
}

// TestDestHostUpMovesCursor verifies that up/k move the cursor towards index 0.
func TestDestHostUpMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts
	m.destHostCursor = 1

	m, _ = sendDistributeKey(m, "up")
	if m.destHostCursor != 0 {
		t.Errorf("after up: expected cursor 0, got %d", m.destHostCursor)
	}
	// Up at index 0 should stay at 0.
	m, _ = sendDistributeKey(m, "k")
	if m.destHostCursor != 0 {
		t.Errorf("after k at top: expected cursor 0, got %d", m.destHostCursor)
	}
}

// TestDestHostSpaceTogglesSelection verifies that pressing Space toggles the
// selection state of the host under the cursor.
func TestDestHostSpaceTogglesSelection(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Cursor is at index 0; toggle on.
	m, _ = sendDistributeKey(m, " ")
	hostKey0 := m.destHostItems[0].Host
	if !m.destHostSelected[hostKey0] {
		t.Error("space should select the host under the cursor")
	}

	// Toggle off.
	m, _ = sendDistributeKey(m, " ")
	if m.destHostSelected[hostKey0] {
		t.Error("second space should deselect the host")
	}
}

// TestDestHostEnterPersistsSelection verifies that pressing Enter in step 2
// stores the selected hosts (in list order) in destHosts and advances to step 3.
func TestDestHostEnterPersistsSelection(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Cursor starts at 0 (cluster header). Space on cluster selects all hosts.
	m, _ = sendDistributeKey(m, " ") // select all hosts in cluster

	// Confirm.
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepCopyMode {
		t.Errorf("after Enter on step 2 expected step %d, got %d", DistributeStepCopyMode, m.step)
	}
	if len(m.destHosts) != 2 {
		t.Errorf("expected 2 dest hosts, got %d", len(m.destHosts))
	}
}

// TestDestHostEnterWithNoSelectionStillAdvances verifies that Enter with no
// hosts selected still advances the step (empty destHosts is allowed here —
// later sub-ACs can add validation).
func TestDestHostEnterWithNoSelectionStillAdvances(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepCopyMode {
		t.Errorf("expected step %d, got %d", DistributeStepCopyMode, m.step)
	}
	if len(m.destHosts) != 0 {
		t.Errorf("expected empty destHosts when none selected, got %d", len(m.destHosts))
	}
}

// TestDestHostSelectionPreservedOnBackNav verifies that navigating back to step 1
// after advancing to step 2 preserves the previously toggled selections.
func TestDestHostSelectionPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Select host at index 0.
	m, _ = sendDistributeKey(m, " ")
	selectedHost := m.destHostItems[0].Host

	// Advance to step 2, then go back.
	m, _ = sendDistributeKey(m, "enter") // step 1 → 2
	m, _ = sendDistributeKey(m, "esc")   // step 2 → 1

	if m.step != DistributeStepDestHosts {
		t.Fatalf("Esc should return to step 1, got step %d", m.step)
	}
	if !m.destHostSelected[selectedHost] {
		t.Error("destHostSelected should be preserved after Esc back to step 1")
	}
}

// TestDestHostCursorPreservedOnBackNav verifies that the cursor position in the
// destination host list is preserved when navigating back to step 1.
func TestDestHostCursorPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	m, _ = sendDistributeKey(m, "down") // cursor → 1
	m, _ = sendDistributeKey(m, "enter") // step 1 → 2
	m, _ = sendDistributeKey(m, "esc")   // step 2 → 1

	if m.destHostCursor != 1 {
		t.Errorf("destHostCursor should be preserved as 1, got %d", m.destHostCursor)
	}
}

// TestDestHostsAccessor verifies that DestHosts() returns the persisted slice.
func TestDestHostsAccessor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Before any confirmation, DestHosts() should be nil/empty.
	if len(m.DestHosts()) != 0 {
		t.Errorf("before confirmation DestHosts() should be empty, got %d", len(m.DestHosts()))
	}

	// Move to first host (index 1, past cluster header) and select it.
	m, _ = sendDistributeKey(m, "down") // move to host-01
	m, _ = sendDistributeKey(m, " ")    // select host-01
	m, _ = sendDistributeKey(m, "enter")
	if len(m.DestHosts()) != 1 {
		t.Errorf("after confirming one host DestHosts() should have length 1, got %d", len(m.DestHosts()))
	}
}

// TestDestHostViewRendersCheckboxes verifies that the step 1 view contains
// checkbox markers ([ ] or [✓]) for destination hosts.
func TestDestHostViewRendersCheckboxes(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts
	view := m.View()

	if !strings.Contains(view, "[ ]") {
		t.Error("step 1 view should contain '[ ]' checkbox for unselected hosts")
	}

	// Select a host and verify the checked marker appears.
	m, _ = sendDistributeKey(m, " ")
	view = m.View()
	if !strings.Contains(view, "[✓]") {
		t.Error("after selecting a host, step 1 view should contain '[✓]'")
	}
}

// TestDestHostViewShowsSelectionCount verifies that the hint line in step 2
// reflects how many hosts are currently selected.
func TestDestHostViewShowsSelectionCount(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Initially 0 selected.
	view := m.View()
	if !strings.Contains(view, "0 selected") {
		t.Error("step 2 view should show '0 selected' initially")
	}

	// Move to first host (past cluster header) and select it.
	m, _ = sendDistributeKey(m, "down") // move to host-01
	m, _ = sendDistributeKey(m, " ")    // select host-01
	view = m.View()
	if !strings.Contains(view, "1 selected") {
		t.Error("step 2 view should show '1 selected' after one toggle")
	}
}

// ---------------------------------------------------------------------------
// Step 2 (dest hosts): filter
// ---------------------------------------------------------------------------

// TestDestHostFilterActivatesWithSlash verifies that pressing '/' activates
// the filter input and destFilterActive becomes true.
func TestDestHostFilterActivatesWithSlash(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	m, _ = sendDistributeKey(m, "/")
	if !m.destFilterActive {
		t.Error("pressing '/' should activate filter mode")
	}
}

// TestDestHostFilterDeactivatesWithEsc verifies that pressing Esc while the
// filter is active clears the filter text and exits filter mode.
func TestDestHostFilterDeactivatesWithEsc(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Activate filter and type something.
	m, _ = sendDistributeKey(m, "/")
	m, _ = sendDistributeKey(m, "a")
	if m.destFilterInput.Value() != "a" {
		t.Fatalf("expected filter value 'a', got %q", m.destFilterInput.Value())
	}

	// Esc should clear and deactivate.
	m, _ = sendDistributeKey(m, "esc")
	if m.destFilterActive {
		t.Error("Esc should deactivate filter mode")
	}
	if m.destFilterInput.Value() != "" {
		t.Errorf("Esc should clear filter value, got %q", m.destFilterInput.Value())
	}
}

// TestDestHostFilterDeactivatesWithEnter verifies that pressing Enter while the
// filter is active commits the filter text and exits filter mode.
func TestDestHostFilterDeactivatesWithEnter(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Activate filter and type something.
	m, _ = sendDistributeKey(m, "/")
	m, _ = sendDistributeKey(m, "h")
	m, _ = sendDistributeKey(m, "o")

	// Enter should deactivate but keep the filter value.
	m, _ = sendDistributeKey(m, "enter")
	if m.destFilterActive {
		t.Error("Enter should deactivate filter mode")
	}
	if m.destFilterInput.Value() != "ho" {
		t.Errorf("Enter should keep filter value 'ho', got %q", m.destFilterInput.Value())
	}
}

// TestDestHostFilterFiltersInRealTime verifies that typing into the filter
// input rebuilds the flat node list in real time.
func TestDestHostFilterFiltersInRealTime(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Initially: 1 cluster + 2 hosts = 3 nodes.
	initialCount := len(m.destFlatNodes)
	if initialCount != 3 {
		t.Fatalf("expected 3 initial flat nodes, got %d", initialCount)
	}

	// Activate filter and type "01" to match only host-01.
	m, _ = sendDistributeKey(m, "/")
	m, _ = sendDistributeKey(m, "0")
	m, _ = sendDistributeKey(m, "1")

	// Should now show: 1 cluster header + 1 matching host = 2 nodes.
	if len(m.destFlatNodes) != 2 {
		t.Errorf("expected 2 flat nodes after filtering for '01', got %d", len(m.destFlatNodes))
	}
}

// TestDestHostFilterNoMatchShowsEmptyList verifies that a filter with no
// matches results in an empty destFlatNodes list.
func TestDestHostFilterNoMatchShowsEmptyList(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Activate filter and type something that matches nothing.
	m, _ = sendDistributeKey(m, "/")
	m, _ = sendDistributeKey(m, "z")
	m, _ = sendDistributeKey(m, "z")
	m, _ = sendDistributeKey(m, "z")

	if len(m.destFlatNodes) != 0 {
		t.Errorf("expected 0 flat nodes for non-matching filter, got %d", len(m.destFlatNodes))
	}
}

// TestDestHostFilterQDoesNotQuit verifies that pressing 'q' while the filter
// is active types into the filter input rather than quitting the wizard.
func TestDestHostFilterQDoesNotQuit(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	m, _ = sendDistributeKey(m, "/")
	m, cmd := sendDistributeKey(m, "q")

	if m.cancelled {
		t.Error("pressing 'q' while filter is active should not cancel the wizard")
	}
	if m.done {
		t.Error("pressing 'q' while filter is active should not mark wizard as done")
	}
	// The 'q' should have been typed into the filter input.
	if m.destFilterInput.Value() != "q" {
		t.Errorf("expected filter value 'q', got %q", m.destFilterInput.Value())
	}
	_ = cmd
}

// TestDestHostFilterViewShowsFilterLine verifies that the rendered view
// includes the filter input when active, and the filter value when committed.
func TestDestHostFilterViewShowsFilterLine(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Activate filter.
	m, _ = sendDistributeKey(m, "/")
	view := m.View()
	// The view should contain "/" prefix indicating filter mode.
	if !strings.Contains(view, "/") {
		t.Error("view should show '/' when filter is active")
	}

	// Type and commit with Enter.
	m, _ = sendDistributeKey(m, "h")
	m, _ = sendDistributeKey(m, "o")
	m, _ = sendDistributeKey(m, "enter")
	view = m.View()
	if !strings.Contains(view, "filter:") || !strings.Contains(view, "ho") {
		t.Error("view should show committed filter value after Enter")
	}
}

// ---------------------------------------------------------------------------
// Step 1: file-tree browser
// ---------------------------------------------------------------------------

// TestFileBrowseLocalTreeCreatedOnEnter verifies that entering the FileBrowse
// step with "local" as the source origin creates a local FileTreeModel.
func TestFileBrowseLocalTreeCreatedOnEnter(t *testing.T) {
	m := newTestDistributeModel()
	// Cursor at 0 (local).
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepFileBrowse {
		t.Fatalf("expected FileBrowse step, got %d", m.step)
	}
	if m.localFileTree == nil {
		t.Error("localFileTree should be non-nil after entering FileBrowse with local source")
	}
	if m.remoteFileTree != nil {
		t.Error("remoteFileTree should remain nil when local source is chosen")
	}
}

// TestFileBrowseRemoteTreeCreatedOnEnter verifies that entering FileBrowse with
// a remote host creates a RemoteFileTreeModel with the correct host alias.
func TestFileBrowseRemoteTreeCreatedOnEnter(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "down") // cursor → 1 (cluster header)
	m, _ = sendDistributeKey(m, "down") // cursor → 2 (first remote host)
	wantHost := m.sourceFlatNodes[2].Host.Host
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepFileBrowse {
		t.Fatalf("expected FileBrowse step, got %d", m.step)
	}
	if m.remoteFileTree == nil {
		t.Error("remoteFileTree should be non-nil after entering FileBrowse with remote source")
	}
	if m.remoteTreeForHost != wantHost {
		t.Errorf("remoteTreeForHost should be %q, got %q", wantHost, m.remoteTreeForHost)
	}
	if m.localFileTree != nil {
		t.Error("localFileTree should remain nil when remote source is chosen")
	}
}

// TestFileBrowseCtrlDAdvancesToDestHosts verifies that pressing Ctrl+D while
// in the FileBrowse step confirms the file selection and advances to DestHosts.
func TestFileBrowseCtrlDAdvancesToDestHosts(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter")  // → FileBrowse (local tree)
	m, _ = sendDistributeKey(m, "ctrl+d") // → DestHosts

	if m.step != DistributeStepDestHosts {
		t.Errorf("after Ctrl+D expected DistributeStepDestHosts (%d), got %d",
			DistributeStepDestHosts, m.step)
	}
	if m.IsDone() {
		t.Error("Ctrl+D in FileBrowse should not mark wizard as done")
	}
	if m.IsCancelled() {
		t.Error("Ctrl+D in FileBrowse should not set cancelled")
	}
}

// TestFileBrowseCtrlDDoesNotQuitSmux verifies that the tea.Quit command
// normally returned by the file tree on Ctrl+D is suppressed by the wizard.
func TestFileBrowseCtrlDDoesNotQuitSmux(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter")           // → FileBrowse
	_, cmd := sendDistributeKey(m, "ctrl+d")        // confirm

	if isQuitCmd(cmd) {
		t.Error("Ctrl+D in FileBrowse should not propagate tea.Quit to the parent")
	}
}

// TestFileBrowseCtrlDPersistsSelectedPaths verifies that the paths selected
// in the file-tree browser are stored in sourcePaths after Ctrl+D.
func TestFileBrowseCtrlDPersistsSelectedPaths(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse (local tree)

	// Ensure a tree exists.
	if m.localFileTree == nil {
		t.Fatal("localFileTree should be initialised")
	}

	// Manually select paths in the local file tree by injecting Space on the
	// first visible node (if any).
	m, _ = sendDistributeKey(m, " ") // select first node (may be empty dir)

	// Confirm with Ctrl+D.
	m, _ = sendDistributeKey(m, "ctrl+d")

	// sourcePaths should now reflect whatever was selected (possibly empty).
	// The key invariant is that sourcePaths is populated (even if empty) and
	// the wizard advanced.
	if m.step != DistributeStepDestHosts {
		t.Errorf("expected DistributeStepDestHosts after Ctrl+D, got %d", m.step)
	}
	// SourcePaths() accessor should match the internal field.
	if len(m.SourcePaths()) != len(m.sourcePaths) {
		t.Errorf("SourcePaths() length %d != sourcePaths length %d",
			len(m.SourcePaths()), len(m.sourcePaths))
	}
}

// TestFileBrowseEscGoesBackToSourceSelect verifies that pressing Esc while in
// the FileBrowse step returns to SourceSelect (step 0).
func TestFileBrowseEscGoesBackToSourceSelect(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse

	m, _ = sendDistributeKey(m, "esc") // → SourceSelect
	if m.step != DistributeStepSourceSelect {
		t.Errorf("Esc from FileBrowse should go to SourceSelect (%d), got %d",
			DistributeStepSourceSelect, m.step)
	}
	if m.IsDone() {
		t.Error("Esc from FileBrowse should not mark wizard as done")
	}
}

// TestFileBrowseLocalTreePreservedOnBackNav verifies that navigating back to
// SourceSelect and returning to FileBrowse reuses the existing local file tree.
func TestFileBrowseLocalTreePreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse

	firstTree := m.localFileTree
	if firstTree == nil {
		t.Fatal("localFileTree should be initialised")
	}

	m, _ = sendDistributeKey(m, "esc")   // → SourceSelect
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse again (same local source)

	// The pointer should be the same (tree was reused, not recreated).
	if m.localFileTree != firstTree {
		t.Error("localFileTree should be reused (not recreated) when returning to FileBrowse with same local source")
	}
}

// TestFileBrowseRemoteTreeRecreatedWhenHostChanges verifies that changing the
// source host between two FileBrowse visits creates a new remote tree.
func TestFileBrowseRemoteTreeRecreatedWhenHostChanges(t *testing.T) {
	m := newTestDistributeModel()
	// Need at least Local + cluster + 2 hosts (index 0,1,2,3).
	if len(m.sourceFlatNodes) < 4 {
		t.Skip("need at least 2 remote hosts configured")
	}

	// First visit: navigate to host at index 2 (first host in cluster).
	m, _ = sendDistributeKey(m, "down")  // cursor → 1 (cluster header)
	m, _ = sendDistributeKey(m, "down")  // cursor → 2 (host-01)
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse with host[2]
	firstTree := m.remoteFileTree
	if firstTree == nil {
		t.Fatal("remoteFileTree should be initialised")
	}

	m, _ = sendDistributeKey(m, "esc") // back to SourceSelect (cursor stays at 2)

	// Second visit: move cursor to index 3 (host-02) and re-enter.
	m, _ = sendDistributeKey(m, "down")  // cursor → 3 (host-02)
	wantHost := m.sourceFlatNodes[3].Host.Host
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse with host[3]

	if m.remoteFileTree == firstTree {
		t.Error("remoteFileTree should be recreated when the source host changes")
	}
	if m.remoteTreeForHost != wantHost {
		t.Errorf("remoteTreeForHost should be %q, got %q", wantHost, m.remoteTreeForHost)
	}
}

// TestFileBrowseRemoteTreeReusedWhenSameHostChosen verifies that re-entering
// FileBrowse with the same remote host preserves the existing tree.
func TestFileBrowseRemoteTreeReusedWhenSameHostChosen(t *testing.T) {
	m := newTestDistributeModel()
	// Need at least Local + cluster + 1 host (index 0,1,2).
	if len(m.sourceFlatNodes) < 3 {
		t.Skip("need at least 1 remote host configured")
	}

	// Visit FileBrowse with host at index 2 (host-01).
	m, _ = sendDistributeKey(m, "down")  // cursor → 1 (cluster header)
	m, _ = sendDistributeKey(m, "down")  // cursor → 2 (host-01)
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse
	firstTree := m.remoteFileTree

	m, _ = sendDistributeKey(m, "esc")   // back to SourceSelect (cursor stays at 2)
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse again with same host

	if m.remoteFileTree != firstTree {
		t.Error("remoteFileTree should be reused when the same remote host is selected again")
	}
}

// TestFileBrowseViewDelegatesWhenLocalTreeActive verifies that when the local
// file tree is active, View() returns the file tree's own view output.
func TestFileBrowseViewDelegatesWhenLocalTreeActive(t *testing.T) {
	m := newTestDistributeModel()
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse (local tree)

	if m.localFileTree == nil {
		t.Fatal("localFileTree should be initialised")
	}

	view := m.View()
	// The local file tree's title is "smux — select file".
	if !strings.Contains(view, "select file") {
		t.Error("View() during FileBrowse with local tree should contain file tree title ('select file')")
	}
}

// TestFileBrowsePlaceholderWhenNoTreeActive verifies that View() shows wizard
// chrome (including the "Browse Files" label) when the step is FileBrowse but
// no file tree has been initialised (e.g. direct step assignment in tests).
func TestFileBrowsePlaceholderWhenNoTreeActive(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepFileBrowse // set directly — no tree initialised

	view := m.View()
	if !strings.Contains(view, "Browse Files") {
		t.Error("View() without active tree should show wizard chrome containing 'Browse Files'")
	}
}

// TestFileBrowseSourcePathsAccessorBeforeAndAfterConfirm verifies that
// SourcePaths() returns nil before confirmation and the selected slice after.
func TestFileBrowseSourcePathsAccessorBeforeAndAfterConfirm(t *testing.T) {
	m := newTestDistributeModel()
	if len(m.SourcePaths()) != 0 {
		t.Errorf("SourcePaths() before any selection should be empty, got %v", m.SourcePaths())
	}

	m, _ = sendDistributeKey(m, "enter")   // → FileBrowse
	m, _ = sendDistributeKey(m, "ctrl+d")  // confirm (no files selected)

	// sourcePaths may be empty but SourcePaths() must not panic.
	_ = m.SourcePaths()
}

// ---------------------------------------------------------------------------
// Step 3: copy mode selection
// ---------------------------------------------------------------------------

// TestCopyModeInitialCursor verifies that a fresh DistributeModel starts with
// the copy-mode cursor at index 0 (Direct parallel).
func TestCopyModeInitialCursor(t *testing.T) {
	m := newTestDistributeModel()
	if m.copyModeCursor != 0 {
		t.Errorf("expected initial copyModeCursor 0, got %d", m.copyModeCursor)
	}
}

// TestCopyModeDownMovesCursor verifies that pressing down/j advances the
// copy-mode cursor through the available options.
func TestCopyModeDownMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode

	m, _ = sendDistributeKey(m, "down")
	if m.copyModeCursor != 1 {
		t.Errorf("after down: expected cursor 1, got %d", m.copyModeCursor)
	}
	// There are only 2 options; pressing down at the last should clamp.
	m, _ = sendDistributeKey(m, "down")
	if m.copyModeCursor != 1 {
		t.Errorf("down past last item: expected cursor 1 (clamped), got %d", m.copyModeCursor)
	}
}

// TestCopyModeJMovesCursor verifies that pressing j advances the cursor.
func TestCopyModeJMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode

	m, _ = sendDistributeKey(m, "j")
	if m.copyModeCursor != 1 {
		t.Errorf("after j: expected cursor 1, got %d", m.copyModeCursor)
	}
}

// TestCopyModeUpMovesCursor verifies that pressing up/k moves the cursor
// towards index 0.
func TestCopyModeUpMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "up")
	if m.copyModeCursor != 0 {
		t.Errorf("after up: expected cursor 0, got %d", m.copyModeCursor)
	}
	// Up at index 0 should stay at 0.
	m, _ = sendDistributeKey(m, "up")
	if m.copyModeCursor != 0 {
		t.Errorf("after up at top: expected cursor 0, got %d", m.copyModeCursor)
	}
}

// TestCopyModeKMovesCursor verifies that pressing k moves the cursor up.
func TestCopyModeKMovesCursor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "k")
	if m.copyModeCursor != 0 {
		t.Errorf("after k: expected cursor 0, got %d", m.copyModeCursor)
	}
}

// TestCopyModeEnterSelectsParallelByDefault verifies that pressing Enter with
// the cursor at index 0 selects "parallel" mode and advances to DestPath step.
func TestCopyModeEnterSelectsParallelByDefault(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	// Cursor starts at 0 (Direct parallel).

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepDestPath {
		t.Errorf("after Enter on CopyMode expected step %d (DestPath), got %d",
			DistributeStepDestPath, m.step)
	}
	if m.copyMode != "parallel" {
		t.Errorf("expected copyMode \"parallel\", got %q", m.copyMode)
	}
}

// TestCopyModeEnterSelectsHubAndSpoke verifies that pressing Enter with the
// cursor at index 1 selects "hub-spoke" mode and advances to the hub-selection
// step (DistributeStepHubSelect), not directly to DestPath.
func TestCopyModeEnterSelectsHubAndSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepHubSelect {
		t.Errorf("after Enter (hub-spoke) expected step %d (HubSelect), got %d",
			DistributeStepHubSelect, m.step)
	}
	if m.copyMode != "hub-spoke" {
		t.Errorf("expected copyMode \"hub-spoke\", got %q", m.copyMode)
	}
}

// TestCopyModeCursorPreservedOnBackNav verifies that pressing Esc from the
// HubSelect step back to CopyMode preserves the cursor position.
func TestCopyModeCursorPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "enter") // step 3 → 4 (HubSelect, hub-spoke mode)
	m, _ = sendDistributeKey(m, "esc")   // step 4 → 3 (CopyMode)

	if m.step != DistributeStepCopyMode {
		t.Fatalf("Esc from DestPath should return to CopyMode, got step %d", m.step)
	}
	if m.copyModeCursor != 1 {
		t.Errorf("copyModeCursor should be preserved as 1 on back-navigation, got %d", m.copyModeCursor)
	}
}

// TestCopyModeViewShowsOptions verifies that the copy mode view renders both
// available options with their labels and descriptions.
func TestCopyModeViewShowsOptions(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	view := m.View()

	for _, item := range copyModeItems {
		if !strings.Contains(view, item.label) {
			t.Errorf("copy mode view should contain option label %q", item.label)
		}
	}
}

// TestCopyModeViewShowsCursorMarker verifies that the highlighted option is
// marked with the "▶" cursor.
func TestCopyModeViewShowsCursorMarker(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	view := m.View()

	if !strings.Contains(view, "▶") {
		t.Error("copy mode view should contain cursor marker ▶ for the highlighted option")
	}
}

// TestCopyModeViewShowsDescriptions verifies that both option descriptions
// appear in the view.
func TestCopyModeViewShowsDescriptions(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	view := m.View()

	for _, item := range copyModeItems {
		// Check the first distinctive word of each description.
		if !strings.Contains(view, item.description[:20]) {
			t.Errorf("copy mode view should contain beginning of description for %q", item.label)
		}
	}
}

// TestCopyModeAccessor verifies that CopyMode() returns the persisted value.
func TestCopyModeAccessor(t *testing.T) {
	m := newTestDistributeModel()

	// Before selection CopyMode() should return the empty string.
	if m.CopyMode() != "" {
		t.Errorf("before selection CopyMode() should be empty, got %q", m.CopyMode())
	}

	// After selecting "parallel".
	m.step = DistributeStepCopyMode
	m, _ = sendDistributeKey(m, "enter")
	if m.CopyMode() != "parallel" {
		t.Errorf("CopyMode() after selecting first option: expected \"parallel\", got %q", m.CopyMode())
	}
}

// TestCopyModeHubSpokeAccessor verifies CopyMode() returns "hub-spoke" when
// the second option is selected.
func TestCopyModeHubSpokeAccessor(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "enter")
	if m.CopyMode() != "hub-spoke" {
		t.Errorf("CopyMode() after selecting hub-spoke: expected \"hub-spoke\", got %q", m.CopyMode())
	}
}

// TestCopyModeItemsDefinedCorrectly verifies that both copy mode items are
// present in the global copyModeItems slice with correct values.
func TestCopyModeItemsDefinedCorrectly(t *testing.T) {
	if len(copyModeItems) != 2 {
		t.Fatalf("expected 2 copy mode items, got %d", len(copyModeItems))
	}
	values := map[string]bool{}
	for _, item := range copyModeItems {
		if item.label == "" {
			t.Error("copy mode item should have a non-empty label")
		}
		if item.value == "" {
			t.Error("copy mode item should have a non-empty value")
		}
		if item.description == "" {
			t.Error("copy mode item should have a non-empty description")
		}
		values[item.value] = true
	}
	for _, want := range []string{"parallel", "hub-spoke"} {
		if !values[want] {
			t.Errorf("expected a copy mode item with value %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Step 4 (DistributeStepHubSelect): hub node selection (hub-and-spoke only)
// ---------------------------------------------------------------------------

// newHubSelectModel returns a DistributeModel positioned at DistributeStepHubSelect
// with hub-spoke mode and two destination hosts pre-populated, ready for
// hub-selection tests.  copyModeCursor is set to 1 (hub-spoke option) so that
// back-navigation to CopyMode and re-confirmation reproduces the hub-spoke flow.
func newHubSelectModel() DistributeModel {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.copyModeCursor = 1 // hub-spoke is the second option (index 1)
	m.destHosts = []config.ResolvedHost{
		{Host: "host-01", DisplayName: "host-01"},
		{Host: "host-02", DisplayName: "host-02"},
	}
	return m
}

// TestHubSelectInitialCursor verifies that the hub-select cursor starts at 0.
func TestHubSelectInitialCursor(t *testing.T) {
	m := newHubSelectModel()
	if m.hubCursor != 0 {
		t.Errorf("expected initial hubCursor 0, got %d", m.hubCursor)
	}
}

// TestHubSelectDownMovesCursor verifies that down/j advance the cursor.
func TestHubSelectDownMovesCursor(t *testing.T) {
	m := newHubSelectModel()

	m, _ = sendDistributeKey(m, "down")
	if m.hubCursor != 1 {
		t.Errorf("after down: expected hubCursor 1, got %d", m.hubCursor)
	}
	// At last item, down should clamp.
	m, _ = sendDistributeKey(m, "down")
	if m.hubCursor != 1 {
		t.Errorf("down at last item: expected hubCursor 1, got %d", m.hubCursor)
	}
}

// TestHubSelectUpMovesCursor verifies that up/k move the cursor towards index 0.
func TestHubSelectUpMovesCursor(t *testing.T) {
	m := newHubSelectModel()
	m.hubCursor = 1

	m, _ = sendDistributeKey(m, "up")
	if m.hubCursor != 0 {
		t.Errorf("after up: expected hubCursor 0, got %d", m.hubCursor)
	}
	// Up at index 0 should stay at 0.
	m, _ = sendDistributeKey(m, "up")
	if m.hubCursor != 0 {
		t.Errorf("up at top: expected hubCursor 0, got %d", m.hubCursor)
	}
}

// TestHubSelectJKMovesCursor verifies j/k navigation in hub selection.
func TestHubSelectJKMovesCursor(t *testing.T) {
	m := newHubSelectModel()

	m, _ = sendDistributeKey(m, "j")
	if m.hubCursor != 1 {
		t.Errorf("after j: expected hubCursor 1, got %d", m.hubCursor)
	}
	m, _ = sendDistributeKey(m, "k")
	if m.hubCursor != 0 {
		t.Errorf("after k: expected hubCursor 0, got %d", m.hubCursor)
	}
}

// TestHubSelectEnterBlockedWhenNoHosts verifies that Enter does not advance
// the wizard when the destination list is empty (defensive guard).
func TestHubSelectEnterBlockedWhenNoHosts(t *testing.T) {
	m := newHubSelectModel()
	m.destHosts = nil // force empty list

	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepHubSelect {
		t.Errorf("Enter with empty destHosts should stay on HubSelect, got step %d", m.step)
	}
}

// TestHubSelectEnterPersistsHubAndAdvances verifies that pressing Enter with a
// valid selection persists the hub host and advances to DistributeStepDestPath.
func TestHubSelectEnterPersistsHubAndAdvances(t *testing.T) {
	m := newHubSelectModel()
	// Cursor is at 0 (host-01).

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepDestPath {
		t.Errorf("Enter on HubSelect should advance to DestPath (step %d), got %d",
			DistributeStepDestPath, m.step)
	}
	if m.hubHost.Host != "host-01" {
		t.Errorf("expected hubHost.Host \"host-01\", got %q", m.hubHost.Host)
	}
}

// TestHubSelectEnterPicksCorrectHost verifies that the cursor position
// determines which host is recorded as the hub.
func TestHubSelectEnterPicksCorrectHost(t *testing.T) {
	m := newHubSelectModel()
	m.hubCursor = 1 // highlight host-02

	m, _ = sendDistributeKey(m, "enter")

	if m.hubHost.Host != "host-02" {
		t.Errorf("expected hubHost.Host \"host-02\", got %q", m.hubHost.Host)
	}
}

// TestHubSelectBackNavFromHubSelectToCopyMode verifies that Esc from
// HubSelect goes back to CopyMode and that re-confirming hub-spoke advances
// back to HubSelect (with cursor reset to 0 by the CopyMode handler).
func TestHubSelectBackNavFromHubSelectToCopyMode(t *testing.T) {
	m := newHubSelectModel()
	m.hubCursor = 1 // move to host-02

	// Esc from HubSelect → CopyMode.
	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepCopyMode {
		t.Fatalf("Esc from HubSelect should return to CopyMode, got step %d", m.step)
	}

	// Re-confirm hub-spoke via Enter on CopyMode.
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepHubSelect {
		t.Fatalf("Enter on CopyMode (hub-spoke) should go to HubSelect, got step %d", m.step)
	}
	// The CopyMode handler resets hubCursor to 0 on each entry to HubSelect
	// so the user starts at the top of the list.
	if m.hubCursor != 0 {
		t.Errorf("hubCursor should be reset to 0 by CopyMode handler, got %d", m.hubCursor)
	}
}

// TestHubSelectEscFromDestPathInParallelSkipsHubSelect verifies that pressing
// Esc from DestPath in direct-parallel mode goes back to CopyMode directly,
// skipping the hub-select step entirely.
func TestHubSelectEscFromDestPathInParallelSkipsHubSelect(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.copyMode = "parallel"

	m, _ = sendDistributeKey(m, "esc")

	if m.step != DistributeStepCopyMode {
		t.Errorf("Esc from DestPath (parallel) should go to CopyMode, got step %d", m.step)
	}
}

// TestHubSelectEscFromDestPathInHubSpokeGoesToHubSelect verifies that pressing
// Esc from DestPath in hub-and-spoke mode returns to the hub-selection step.
func TestHubSelectEscFromDestPathInHubSpokeGoesToHubSelect(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.copyMode = "hub-spoke"

	m, _ = sendDistributeKey(m, "esc")

	if m.step != DistributeStepHubSelect {
		t.Errorf("Esc from DestPath (hub-spoke) should go to HubSelect, got step %d", m.step)
	}
}

// TestHubSelectViewContainsHosts verifies that the hub-selection view renders
// all destination hosts.
func TestHubSelectViewContainsHosts(t *testing.T) {
	m := newHubSelectModel()
	view := m.View()

	for _, h := range m.destHosts {
		if !strings.Contains(view, h.DisplayName) {
			t.Errorf("hub-select view should contain host %q", h.DisplayName)
		}
	}
}

// TestHubSelectViewShowsStepNumber verifies that the hub-select view renders
// the correct step number (5 of 8 for hub-spoke mode).
func TestHubSelectViewShowsStepNumber(t *testing.T) {
	m := newHubSelectModel()
	view := m.View()

	if !strings.Contains(view, "5 of 8") {
		t.Errorf("hub-select view should show '5 of 8', view:\n%s", view)
	}
}

// TestHubSelectBreadcrumbShows8Steps verifies that the breadcrumb contains 8
// step labels when hub-spoke mode is active.
func TestHubSelectBreadcrumbShows8Steps(t *testing.T) {
	m := newHubSelectModel()
	view := m.View()

	// All 8 hub-spoke step labels should appear in the breadcrumb.
	for step, label := range distributeStepLabel {
		if step == DistributeStepRetryConfirm {
			continue // not a breadcrumb step
		}
		if !strings.Contains(view, label) {
			t.Errorf("hub-spoke breadcrumb should contain step label %q", label)
		}
	}
}

// TestHubSelectHubHostAccessor verifies that HubHost() returns the persisted
// hub host after selection.
func TestHubSelectHubHostAccessor(t *testing.T) {
	m := newHubSelectModel()

	// Before selection, HubHost() returns zero-value.
	if m.HubHost().Host != "" {
		t.Errorf("HubHost() before selection should be empty, got %q", m.HubHost().Host)
	}

	// Select host-01.
	m, _ = sendDistributeKey(m, "enter")
	if m.HubHost().Host != "host-01" {
		t.Errorf("HubHost() after selection: expected \"host-01\", got %q", m.HubHost().Host)
	}
}

// TestConfirmStepShowsHubForHubSpoke verifies that the Confirm step view
// includes the selected hub host name when in hub-and-spoke mode.
func TestConfirmStepShowsHubForHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.copyMode = "hub-spoke"
	m.hubHost = config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub.example.com"}

	view := m.View()
	if !strings.Contains(view, "hub.example.com") {
		t.Errorf("confirm step (hub-spoke) should display the selected hub host, view:\n%s", view)
	}
}

// TestConfirmStepNoHubForParallel verifies that the Confirm step view does NOT
// display a Hub field when in direct-parallel mode.
func TestConfirmStepNoHubForParallel(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.copyMode = "parallel"

	view := m.View()
	if strings.Contains(view, "Hub:") {
		t.Error("confirm step (parallel) should not display a Hub field")
	}
}

// TestBuildSourceFlatListEmptyConfig verifies that buildSourceFlatList returns
// exactly the local entry when no hosts are configured.
func TestBuildSourceFlatListEmptyConfig(t *testing.T) {
	cfg := emptyConfig()
	state := NewTreeState(cfg.ClusterNames())
	nodes := buildSourceFlatList(cfg, &state, "")
	if len(nodes) != 1 {
		t.Errorf("empty config: expected 1 node (local only), got %d", len(nodes))
	}
	if !nodes[0].IsLocal() {
		t.Errorf("first node must be NodeKindLocal, got kind=%v", nodes[0].Kind)
	}
}

// ---------------------------------------------------------------------------
// Step 4 (DistributeStepConfirm): confirmation dialog
// ---------------------------------------------------------------------------

// TestConfirmStepInitialChecksumOff verifies that checksum verification is
// disabled by default when the model is first created.
func TestConfirmStepInitialChecksumOff(t *testing.T) {
	m := newTestDistributeModel()
	if m.verifyChecksum {
		t.Error("verifyChecksum should be false initially")
	}
}

// TestConfirmStepSpaceTogglesChecksum verifies that pressing Space in
// DistributeStepConfirm toggles the verifyChecksum flag.
func TestConfirmStepSpaceTogglesChecksum(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Space toggles on.
	m, _ = sendDistributeKey(m, " ")
	if !m.verifyChecksum {
		t.Error("after first Space verifyChecksum should be true")
	}

	// Space toggles off.
	m, _ = sendDistributeKey(m, " ")
	if m.verifyChecksum {
		t.Error("after second Space verifyChecksum should be false")
	}
}

// TestConfirmStepEnterAdvancesToExecute verifies that pressing Enter in
// DistributeStepConfirm advances the wizard to DistributeStepExecute.
func TestConfirmStepEnterAdvancesToExecute(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepExecute {
		t.Errorf("Enter on Confirm should advance to Execute (step %d), got %d",
			DistributeStepExecute, m.step)
	}
}

// TestConfirmStepEscGoesBackToDestPath verifies that pressing Esc from
// DistributeStepConfirm moves back to DistributeStepDestPath.
func TestConfirmStepEscGoesBackToDestPath(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepDestPath {
		t.Errorf("Esc from Confirm should go back to DestPath (step %d), got %d",
			DistributeStepDestPath, m.step)
	}
}

// TestConfirmStepChecksumPreservedOnBackNav verifies that the checksum setting
// is preserved when the user navigates back from Confirm to a prior step and
// then returns to Confirm.
func TestConfirmStepChecksumPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Enable checksum.
	m, _ = sendDistributeKey(m, " ")
	if !m.verifyChecksum {
		t.Fatal("verifyChecksum should be true after Space")
	}

	// Go back to DestPath.
	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepDestPath {
		t.Fatalf("Esc should return to DestPath, got step %d", m.step)
	}

	// Advance to Confirm again via Enter with a valid path.
	m.destPathInput.SetValue("/tmp/dest")
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepConfirm {
		t.Fatalf("Enter on DestPath should go to Confirm, got step %d", m.step)
	}

	// Checksum state must be preserved.
	if !m.verifyChecksum {
		t.Error("verifyChecksum should be preserved across back-navigation")
	}
}

// TestConfirmStepViewShowsSourceAndDest verifies that the DistributeStepConfirm
// view contains the source and destination details.
func TestConfirmStepViewShowsSourceAndDest(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Set up some state to display.
	m.sourceHost = "web-01"
	m.sourcePaths = []string{"/var/log/app.log"}
	m.destHosts = m.destHostItems // use all configured hosts
	m.copyMode = "direct parallel"

	view := m.View()

	// Source and destination details should be present.
	if !strings.Contains(view, "web-01") {
		t.Error("confirm view should display the source host name")
	}
	if !strings.Contains(view, "/var/log/app.log") {
		t.Error("confirm view should display the source path")
	}
	if !strings.Contains(view, "direct parallel") {
		t.Error("confirm view should display the copy mode")
	}
}

// TestConfirmStepViewShowsLocalSource verifies that the confirm view shows
// "local" when no source host is set.
func TestConfirmStepViewShowsLocalSource(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.sourceHost = "" // local
	m.sourcePaths = []string{"/home/user/file.txt"}

	view := m.View()
	if !strings.Contains(view, "local") {
		t.Error("confirm view should show 'local' when sourceHost is empty")
	}
}

// TestConfirmStepViewShowsChecksumCheckbox verifies that the confirm view
// contains the checksum checkbox and that toggling Space changes its state.
func TestConfirmStepViewShowsChecksumCheckbox(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Initially unchecked.
	view := m.View()
	if !strings.Contains(view, "[ ]") {
		t.Error("confirm view should show unchecked checkbox '[ ]' initially")
	}
	if strings.Contains(view, "[✓]") {
		t.Error("confirm view should not show checked checkbox '[✓]' initially")
	}

	// Toggle on.
	m, _ = sendDistributeKey(m, " ")
	view = m.View()
	if !strings.Contains(view, "[✓]") {
		t.Error("after Space, confirm view should show checked checkbox '[✓]'")
	}
}

// TestConfirmStepViewShowsDestPath verifies that the confirm view renders the
// destination path. When destPath is empty (which should not happen in normal
// flow, since the dest-path step is mandatory), the view shows "(not set)".
func TestConfirmStepViewShowsDestPath(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Empty destPath → show "(not set)" (no implicit fallback to source path).
	view := m.View()
	if !strings.Contains(view, "not set") {
		t.Error("confirm view should show '(not set)' when destPath is empty")
	}
	if strings.Contains(view, "same as source") {
		t.Error("confirm view must not fall back to '(same as source)' — destination path is mandatory")
	}

	// Explicit destPath.
	m.destPath = "/tmp/deploy"
	view = m.View()
	if !strings.Contains(view, "/tmp/deploy") {
		t.Error("confirm view should show the configured destPath")
	}
}

// TestVerifyChecksumAccessor verifies that VerifyChecksum() returns the
// current value of the verifyChecksum field.
func TestVerifyChecksumAccessor(t *testing.T) {
	m := newTestDistributeModel()
	if m.VerifyChecksum() {
		t.Error("VerifyChecksum() should return false initially")
	}

	m.step = DistributeStepConfirm
	m, _ = sendDistributeKey(m, " ")
	if !m.VerifyChecksum() {
		t.Error("VerifyChecksum() should return true after toggling on in Confirm step")
	}
}

// TestDestPathAccessor verifies that DestPath() returns the destPath field.
func TestDestPathAccessor(t *testing.T) {
	m := newTestDistributeModel()
	if m.DestPath() != "" {
		t.Errorf("DestPath() should return empty string initially, got %q", m.DestPath())
	}

	m.destPath = "/opt/app/bin"
	if m.DestPath() != "/opt/app/bin" {
		t.Errorf("DestPath() should return %q, got %q", "/opt/app/bin", m.DestPath())
	}
}

// ---------------------------------------------------------------------------
// Step 4 (DistributeStepConfirm): hub-and-spoke warning
// ---------------------------------------------------------------------------

// TestConfirmStepHubSpokeWarningAppearsForHubSpoke verifies that when
// hub-and-spoke copy mode is selected, the confirm view includes a prominent
// warning about the internal SSH access requirement.
func TestConfirmStepHubSpokeWarningAppearsForHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.copyMode = "hub-spoke"

	view := m.View()

	// The warning must contain the key phrase that communicates the
	// internal SSH access requirement to the user.
	if !strings.Contains(view, "internal SSH access required") {
		t.Error("confirm view with hub-spoke mode should display internal SSH access warning")
	}
	if !strings.Contains(view, "SSH") {
		t.Error("confirm view with hub-spoke mode should mention SSH in the warning")
	}
}

// TestConfirmStepHubSpokeWarningMentionsInternalNetwork verifies that the
// hub-and-spoke warning body text explains that the hub must reach destination
// hosts via the internal network.
func TestConfirmStepHubSpokeWarningMentionsInternalNetwork(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.copyMode = "hub-spoke"

	view := m.View()

	if !strings.Contains(view, "internal network") {
		t.Error("confirm view with hub-spoke mode should mention 'internal network' in warning")
	}
}

// TestConfirmStepHubSpokeWarningAbsentForParallelMode verifies that the
// hub-and-spoke warning is NOT shown when direct parallel copy mode is
// selected.  Only hub-and-spoke mode requires the internal SSH access warning.
func TestConfirmStepHubSpokeWarningAbsentForParallelMode(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	m.copyMode = "parallel"

	view := m.View()

	if strings.Contains(view, "internal SSH access required") {
		t.Error("confirm view with parallel mode should NOT display the hub-and-spoke warning")
	}
}

// TestConfirmStepHubSpokeWarningAbsentWhenNoCopyModeSet verifies that the
// hub-and-spoke warning is NOT shown when no copy mode has been set yet
// (e.g. before the user has passed through the copy-mode step).
func TestConfirmStepHubSpokeWarningAbsentWhenNoCopyModeSet(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm
	// m.copyMode is "" by default.

	view := m.View()

	if strings.Contains(view, "internal SSH access required") {
		t.Error("confirm view with no copy mode set should NOT display the hub-and-spoke warning")
	}
}

// TestRenderHubSpokeWarningContent verifies that the renderHubSpokeWarning
// helper returns a non-empty string that contains both the title phrase and
// the body text about the hub host SSH access requirement.
func TestRenderHubSpokeWarningContent(t *testing.T) {
	warn := renderHubSpokeWarning()

	if warn == "" {
		t.Fatal("renderHubSpokeWarning should return a non-empty string")
	}
	if !strings.Contains(warn, "Hub-and-spoke") {
		t.Error("warning should mention 'Hub-and-spoke' in the title")
	}
	if !strings.Contains(warn, "SSH") {
		t.Error("warning should mention 'SSH' access requirement")
	}
	if !strings.Contains(warn, "hub") {
		t.Error("warning should refer to the hub host")
	}
	if !strings.Contains(warn, "destination") {
		t.Error("warning should mention destination hosts")
	}
}

// ---------------------------------------------------------------------------
// Step: DistributeStepRetryConfirm — NewRetryDistributeModel constructor
// ---------------------------------------------------------------------------

// makeRetryParams is a test helper that constructs an executor.RetryParams
// with the given failed host aliases.
func makeRetryParams(failedHosts ...string) executor.RetryParams {
	hosts := make([]config.ResolvedHost, len(failedHosts))
	for i, h := range failedHosts {
		hosts[i] = config.ResolvedHost{Host: h, DisplayName: h}
	}
	return executor.RetryParams{
		SourceHost:  config.ResolvedHost{Host: "src.example.com"},
		SourcePath:  "/data/payload.tar.gz",
		DestPath:    "/opt/payload.tar.gz",
		CopyMode:    "parallel",
		FailedHosts: hosts,
		AllHosts:    hosts,
	}
}

// TestNewRetryDistributeModelStartsAtRetryConfirmStep verifies that the
// constructor places the wizard at DistributeStepRetryConfirm.
func TestNewRetryDistributeModelStartsAtRetryConfirmStep(t *testing.T) {
	params := makeRetryParams("h1.example.com", "h2.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	if m.step != DistributeStepRetryConfirm {
		t.Errorf("expected step DistributeStepRetryConfirm (%d), got %d",
			DistributeStepRetryConfirm, m.step)
	}
}

// TestNewRetryDistributeModelPopulatesFields verifies that
// NewRetryDistributeModel copies the RetryParams values into the model's
// per-step fields so startExecution() has everything it needs.
func TestNewRetryDistributeModelPopulatesFields(t *testing.T) {
	params := makeRetryParams("h1.example.com", "h2.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	if m.sourceHost != "src.example.com" {
		t.Errorf("sourceHost: expected %q, got %q", "src.example.com", m.sourceHost)
	}
	if len(m.sourcePaths) != 1 || m.sourcePaths[0] != "/data/payload.tar.gz" {
		t.Errorf("sourcePaths: expected [\"/data/payload.tar.gz\"], got %v", m.sourcePaths)
	}
	if m.destPath != "/opt/payload.tar.gz" {
		t.Errorf("destPath: expected %q, got %q", "/opt/payload.tar.gz", m.destPath)
	}
	if m.copyMode != "parallel" {
		t.Errorf("copyMode: expected %q, got %q", "parallel", m.copyMode)
	}
	if len(m.destHosts) != 2 {
		t.Errorf("destHosts: expected 2 hosts, got %d", len(m.destHosts))
	}
}

// TestNewRetryDistributeModelStoresRetryParams verifies that the retryParams
// pointer is non-nil and references the passed RetryParams.
func TestNewRetryDistributeModelStoresRetryParams(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	if m.retryParams == nil {
		t.Fatal("retryParams should not be nil after NewRetryDistributeModel")
	}
	if m.retryParams.SourcePath != "/data/payload.tar.gz" {
		t.Errorf("retryParams.SourcePath: expected %q, got %q",
			"/data/payload.tar.gz", m.retryParams.SourcePath)
	}
}

// ---------------------------------------------------------------------------
// Step: DistributeStepRetryConfirm — key handling
// ---------------------------------------------------------------------------

// TestRetryConfirmEnterAdvancesToExecuteStep verifies that pressing Enter on
// the retry-confirm step transitions to DistributeStepExecute.
func TestRetryConfirmEnterAdvancesToExecuteStep(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepExecute {
		t.Errorf("after Enter on retry-confirm expected step %d (Execute), got %d",
			DistributeStepExecute, m.step)
	}
	if m.IsDone() {
		t.Error("advancing to Execute should not mark wizard as done")
	}
}

// TestRetryConfirmYKeyAdvancesToExecuteStep verifies that pressing 'y' on the
// retry-confirm step (as an alternative to Enter) also transitions to Execute.
func TestRetryConfirmYKeyAdvancesToExecuteStep(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, _ = sendDistributeKey(m, "y")

	if m.step != DistributeStepExecute {
		t.Errorf("after 'y' on retry-confirm expected step %d (Execute), got %d",
			DistributeStepExecute, m.step)
	}
}

// TestRetryConfirmNKeyExitsToMain verifies that pressing 'n' on the retry-
// confirm step sets exitToMain and done without issuing tea.Quit.
func TestRetryConfirmNKeyExitsToMain(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, cmd := sendDistributeKey(m, "n")

	if !m.IsDone() {
		t.Error("pressing 'n' should mark wizard as done")
	}
	if !m.IsExitToMain() {
		t.Error("pressing 'n' should set exitToMain = true")
	}
	if m.IsCancelled() {
		t.Error("pressing 'n' should not set cancelled = true")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing 'n' should not issue tea.Quit")
	}
}

// TestRetryConfirmEscExitsToMain verifies that pressing Esc on the retry-
// confirm step also returns to the normal TUI (exitToMain = true).
func TestRetryConfirmEscExitsToMain(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, cmd := sendDistributeKey(m, "esc")

	if !m.IsDone() {
		t.Error("Esc from retry-confirm should mark wizard as done")
	}
	if !m.IsExitToMain() {
		t.Error("Esc from retry-confirm should set exitToMain = true")
	}
	if isQuitCmd(cmd) {
		t.Error("Esc from retry-confirm should not issue tea.Quit")
	}
}

// TestRetryConfirmQKeyQuits verifies that pressing 'q' on the retry-confirm
// step cancels and issues tea.Quit (same global behaviour as all other steps).
func TestRetryConfirmQKeyQuits(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, cmd := sendDistributeKey(m, "q")

	if !m.IsCancelled() {
		t.Error("pressing 'q' on retry-confirm should set cancelled = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' on retry-confirm should issue tea.Quit")
	}
}

// TestRetryConfirmCtrlCQuits verifies that Ctrl+C on the retry-confirm step
// exits smux (cancelled = true, tea.Quit issued).
func TestRetryConfirmCtrlCQuits(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, cmd := sendDistributeKey(m, "ctrl+c")

	if !m.IsCancelled() {
		t.Error("Ctrl+C on retry-confirm should set cancelled = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C on retry-confirm should issue tea.Quit")
	}
}

// ---------------------------------------------------------------------------
// Step: DistributeStepRetryConfirm — view rendering
// ---------------------------------------------------------------------------

// TestRetryConfirmViewContainsTitle verifies that the retry-confirm step view
// includes the retry-specific title rather than the normal wizard title.
func TestRetryConfirmViewContainsTitle(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "retry") {
		t.Error("retry-confirm view should mention 'retry' in the title")
	}
}

// TestRetryConfirmViewContainsSourceInfo verifies that the recovered source
// host and path are shown in the retry confirmation view.
func TestRetryConfirmViewContainsSourceInfo(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "src.example.com") {
		t.Error("retry-confirm view should show the source host 'src.example.com'")
	}
	if !strings.Contains(view, "/data/payload.tar.gz") {
		t.Error("retry-confirm view should show the source path '/data/payload.tar.gz'")
	}
}

// TestRetryConfirmViewContainsDestPath verifies that the destination path is
// shown in the retry confirmation view.
func TestRetryConfirmViewContainsDestPath(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "/opt/payload.tar.gz") {
		t.Error("retry-confirm view should show the destination path '/opt/payload.tar.gz'")
	}
}

// TestRetryConfirmViewContainsFailedHosts verifies that every failed host
// address is listed in the retry confirmation view.
func TestRetryConfirmViewContainsFailedHosts(t *testing.T) {
	params := makeRetryParams("h1.example.com", "h2.example.com", "h3.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	for _, h := range []string{"h1.example.com", "h2.example.com", "h3.example.com"} {
		if !strings.Contains(view, h) {
			t.Errorf("retry-confirm view should list failed host %q", h)
		}
	}
}

// TestRetryConfirmViewContainsCopyMode verifies that the copy mode is shown in
// the retry confirmation view.
func TestRetryConfirmViewContainsCopyMode(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "parallel") {
		t.Error("retry-confirm view should show the copy mode 'parallel'")
	}
}

// TestRetryConfirmViewContainsConfirmHint verifies that the retry-confirm view
// shows the y/enter and n/esc key hints for confirmation.
func TestRetryConfirmViewContainsConfirmHint(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "y") {
		t.Error("retry-confirm view should show 'y' as an acceptance key")
	}
	if !strings.Contains(view, "n") {
		t.Error("retry-confirm view should show 'n' as a rejection key")
	}
}

// TestRetryConfirmViewNoBreadcrumb verifies that the normal 6-step breadcrumb
// is NOT shown in the retry-confirm view (since it's a separate entry point).
func TestRetryConfirmViewNoBreadcrumb(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	// The breadcrumb step labels (Browse Files, Select Destinations, etc.)
	// should not appear in the retry-confirm view.
	for _, label := range distributeStepLabel {
		if strings.Contains(view, label) {
			t.Errorf("retry-confirm view should not show normal breadcrumb label %q", label)
		}
	}
}

// TestRetryConfirmViewNoFailedHostsMessage verifies that the view shows an
// appropriate message when there are no failed hosts to retry.
func TestRetryConfirmViewNoFailedHostsMessage(t *testing.T) {
	params := makeRetryParams() // no failed hosts
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "nothing to retry") {
		t.Error("retry-confirm view with zero failed hosts should mention 'nothing to retry'")
	}
}

// TestRetryConfirmEnterThenEnterStartsExecution verifies the full retry
// happy path: retry-confirm (Enter) → Execute step → Enter starts execution.
func TestRetryConfirmEnterThenEnterStartsExecution(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	// Confirm the retry.
	m, _ = sendDistributeKey(m, "enter") // retry-confirm → Execute step

	if m.step != DistributeStepExecute {
		t.Fatalf("expected Execute step after confirming retry, got %d", m.step)
	}

	// At Execute step, pressing Enter again starts execution.
	m, _ = sendDistributeKey(m, "enter")

	if !m.executeStarted {
		t.Error("expected executeStarted = true after Enter on Execute step following retry confirm")
	}
}

// ---------------------------------------------------------------------------
// Step 4 (DistributeStepDestPath): input validation — non-empty paths accepted
// ---------------------------------------------------------------------------

// TestDestPathAbsolutePathAccepted verifies that a standard absolute path is
// accepted without further validation and advances to DistributeStepConfirm.
func TestDestPathAbsolutePathAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.destPathInput.SetValue("/tmp/deploy")

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("absolute path should advance to Confirm (step %d), got step %d",
			DistributeStepConfirm, m.step)
	}
	if m.destPath != "/tmp/deploy" {
		t.Errorf("destPath should be %q, got %q", "/tmp/deploy", m.destPath)
	}
	if m.destPathErr != "" {
		t.Errorf("destPathErr should be empty for valid path, got %q", m.destPathErr)
	}
}

// TestDestPathRelativePathAccepted verifies that a relative path (no leading
// slash) is accepted without further validation.  No path format check is
// performed beyond ensuring the input is non-empty.
func TestDestPathRelativePathAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.destPathInput.SetValue("relative/path/to/dest")

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("relative path should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != "relative/path/to/dest" {
		t.Errorf("destPath should be %q, got %q", "relative/path/to/dest", m.destPath)
	}
}

// TestDestPathSingleCharacterAccepted verifies that even a minimal single-
// character non-whitespace path is accepted.
func TestDestPathSingleCharacterAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.destPathInput.SetValue("a")

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("single-char path should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != "a" {
		t.Errorf("destPath should be %q, got %q", "a", m.destPath)
	}
}

// TestDestPathSpecialCharsAccepted verifies that paths containing special
// characters (hyphens, underscores, dots, tildes) are accepted without
// further validation.
func TestDestPathSpecialCharsAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	specialPath := "/opt/my-app_v2.1/data~backup"
	m.destPathInput.SetValue(specialPath)

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("path with special chars should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != specialPath {
		t.Errorf("destPath should be %q, got %q", specialPath, m.destPath)
	}
}

// TestDestPathWithSpacesAccepted verifies that paths containing spaces are
// accepted.  No path character validation is performed; only blank/whitespace-
// only paths are rejected.
func TestDestPathWithSpacesAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	pathWithSpaces := "/path with spaces/file"
	m.destPathInput.SetValue(pathWithSpaces)

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("path with internal spaces should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != pathWithSpaces {
		t.Errorf("destPath should be %q, got %q", pathWithSpaces, m.destPath)
	}
}

// TestDestPathLongPathAccepted verifies that a long (but non-empty) path is
// accepted without error.  No path length limit is enforced beyond the
// textinput's CharLimit.
func TestDestPathLongPathAccepted(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	// 100-character path, well within typical OS limits and the 512 CharLimit.
	longPath := "/very/long/path/that/spans/many/directory/components/and/reaches/a/significant/length/on/disk"
	m.destPathInput.SetValue(longPath)

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("long path should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != longPath {
		t.Errorf("destPath should be %q, got %q", longPath, m.destPath)
	}
}

// TestDestPathLeadingTrailingSpacesTrimmed verifies that leading/trailing
// whitespace is stripped from the input before being stored in destPath.
// The path "/tmp/dest" with surrounding spaces should be stored as "/tmp/dest".
func TestDestPathLeadingTrailingSpacesTrimmed(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	m.destPathInput.SetValue("  /tmp/dest  ")

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("path with surrounding spaces should advance to Confirm, got step %d", m.step)
	}
	if m.destPath != "/tmp/dest" {
		t.Errorf("destPath should have surrounding whitespace trimmed to %q, got %q",
			"/tmp/dest", m.destPath)
	}
}

// ---------------------------------------------------------------------------
// Step 4 (DistributeStepDestPath): input validation — blank/whitespace rejected
// ---------------------------------------------------------------------------

// TestDestPathBlankInputRejectedWithRePrompt verifies that pressing Enter with
// an empty input does NOT advance to DistributeStepConfirm.  The wizard stays
// on DistributeStepDestPath and sets a non-empty destPathErr message.
func TestDestPathBlankInputRejectedWithRePrompt(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath
	// destPathInput value is "" by default.

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepDestPath {
		t.Errorf("blank input should keep wizard on DistributeStepDestPath (%d), got step %d",
			DistributeStepDestPath, m.step)
	}
	if m.destPathErr == "" {
		t.Error("blank input should set a non-empty destPathErr error message")
	}
	if m.destPath != "" {
		t.Errorf("blank input should not update destPath, got %q", m.destPath)
	}
}

// TestDestPathWhitespaceOnlyInputRejectedWithRePrompt verifies that a string
// of only spaces/tabs is treated the same as blank: the wizard stays on the
// DestPath step and an error message is shown.
func TestDestPathWhitespaceOnlyInputRejectedWithRePrompt(t *testing.T) {
	for _, ws := range []string{" ", "   ", "\t", "  \t  "} {
		m := newTestDistributeModel()
		m.step = DistributeStepDestPath
		m.destPathInput.SetValue(ws)

		m, _ = sendDistributeKey(m, "enter")

		if m.step != DistributeStepDestPath {
			t.Errorf("whitespace-only input %q should keep wizard on DistributeStepDestPath, got step %d",
				ws, m.step)
		}
		if m.destPathErr == "" {
			t.Errorf("whitespace-only input %q should set a non-empty destPathErr", ws)
		}
		if m.destPath != "" {
			t.Errorf("whitespace-only input %q should not update destPath, got %q", ws, m.destPath)
		}
	}
}

// TestDestPathErrorClearedOnNextKeystroke verifies that the error message is
// cleared as soon as the user types any character after a failed blank
// submission.  This provides responsive feedback without stale error text.
func TestDestPathErrorClearedOnNextKeystroke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath

	// Submit blank to trigger the error.
	m, _ = sendDistributeKey(m, "enter")
	if m.destPathErr == "" {
		t.Fatal("precondition: destPathErr should be set after blank submission")
	}

	// Typing any character should clear the error.
	m, _ = sendDistributeKey(m, "a")
	if m.destPathErr != "" {
		t.Errorf("destPathErr should be cleared after typing a character, got %q", m.destPathErr)
	}
}

// TestDestPathViewShowsErrorOnBlankSubmission verifies that the rendered view
// for DistributeStepDestPath includes the error message text when destPathErr
// is non-empty (i.e., after the user attempted to submit a blank path).
func TestDestPathViewShowsErrorOnBlankSubmission(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath

	// Submit blank to trigger the error.
	m, _ = sendDistributeKey(m, "enter")
	if m.destPathErr == "" {
		t.Fatal("precondition: destPathErr should be set after blank submission")
	}

	view := m.View()
	if !strings.Contains(view, m.destPathErr) {
		t.Errorf("view should contain the error message %q, but it does not.\nView:\n%s",
			m.destPathErr, view)
	}
}

// TestDestPathRepeatedBlankSubmissionsKeepOnStep verifies that submitting a
// blank path multiple times in a row always keeps the wizard on DistributeStepDestPath
// with an error set.
func TestDestPathRepeatedBlankSubmissionsKeepOnStep(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath

	for i := 0; i < 3; i++ {
		m, _ = sendDistributeKey(m, "enter")
		if m.step != DistributeStepDestPath {
			t.Errorf("attempt %d: blank input should keep wizard on DistributeStepDestPath, got step %d",
				i+1, m.step)
		}
		if m.destPathErr == "" {
			t.Errorf("attempt %d: destPathErr should be set after blank submission", i+1)
		}
	}
}

// TestDestPathValidInputAfterRejectionAdvances verifies that after a rejected
// blank submission, providing a valid non-empty path on the next Enter press
// successfully advances the wizard to DistributeStepConfirm.
func TestDestPathValidInputAfterRejectionAdvances(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestPath

	// First attempt: blank — should be rejected.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepDestPath {
		t.Fatalf("blank input should stay on DistributeStepDestPath, got step %d", m.step)
	}

	// Second attempt: provide a valid path and confirm.
	m.destPathInput.SetValue("/opt/deploy")
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("valid path after rejection should advance to Confirm (step %d), got step %d",
			DistributeStepConfirm, m.step)
	}
	if m.destPath != "/opt/deploy" {
		t.Errorf("destPath should be %q after valid submission, got %q", "/opt/deploy", m.destPath)
	}
	if m.destPathErr != "" {
		t.Errorf("destPathErr should be cleared after valid submission, got %q", m.destPathErr)
	}
}

// ---------------------------------------------------------------------------
// Post-execution retry hint display (AC 1)
// ---------------------------------------------------------------------------

// makeExecuteDoneModelWithFailures returns a DistributeModel in the Execute
// step that has completed execution with the given hosts marked as failed.
// This simulates the state after a distribution run where some hosts failed.
func makeExecuteDoneModelWithFailures(failedHosts []string) DistributeModel {
	hosts := make([]config.ResolvedHost, len(failedHosts))
	for i, h := range failedHosts {
		hosts[i] = config.ResolvedHost{Host: h, DisplayName: h}
	}
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = hosts
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	// Simulate completed execution with all hosts failed.
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = make(map[string]executor.TransferStatus)
	for _, h := range hosts {
		m.hostProgress[h.Host] = executor.TransferFailed
	}
	return m
}

// makeExecuteDoneModelAllSucceeded returns a DistributeModel in the Execute
// step where execution completed with all hosts succeeded.
func makeExecuteDoneModelAllSucceeded(hosts []string) DistributeModel {
	resolved := make([]config.ResolvedHost, len(hosts))
	for i, h := range hosts {
		resolved[i] = config.ResolvedHost{Host: h, DisplayName: h}
	}
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = resolved
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = make(map[string]executor.TransferStatus)
	for _, h := range resolved {
		m.hostProgress[h.Host] = executor.TransferDone
	}
	return m
}

// TestExecuteStepRetryHintShownWhenFailuresExist verifies that the Execute step
// view includes a retry hint ("r") when execution has completed with at least
// one failed host transfer.
func TestExecuteStepRetryHintShownWhenFailuresExist(t *testing.T) {
	m := makeExecuteDoneModelWithFailures([]string{"host1.example.com", "host2.example.com"})

	view := m.View()
	if !strings.Contains(view, "r") {
		t.Error("execute step view should contain 'r' retry hint when there are failures")
	}
	if !strings.Contains(view, "retry") {
		t.Error("execute step view should mention 'retry' when there are failed hosts")
	}
}

// TestExecuteStepRetryHintNotShownOnFullSuccess verifies that the Execute step
// view does NOT include a retry hint when all transfers succeeded.
func TestExecuteStepRetryHintNotShownOnFullSuccess(t *testing.T) {
	m := makeExecuteDoneModelAllSucceeded([]string{"host1.example.com", "host2.example.com"})

	view := m.View()
	// When all succeeded, "r retry failed" hint should not be present.
	if strings.Contains(view, "r retry") {
		t.Error("execute step view should NOT show 'r retry' hint when all transfers succeeded")
	}
}

// TestExecuteStepRetryHintNotShownDuringExecution verifies that the retry hint
// is NOT shown while execution is still in progress (only shown after completion).
func TestExecuteStepRetryHintNotShownDuringExecution(t *testing.T) {
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1.example.com"},
	}
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	// Simulate execution in progress (started but not done).
	m.executeStarted = true
	m.executeDone = false
	m.hostProgress = map[string]executor.TransferStatus{
		"host1.example.com": executor.TransferInProgress,
	}

	view := m.View()
	if strings.Contains(view, "r retry") {
		t.Error("execute step view should NOT show 'r retry' hint while execution is in progress")
	}
}

// TestExecuteStepRetryHintNotShownBeforeExecution verifies that the retry hint
// is NOT shown before execution has started.
func TestExecuteStepRetryHintNotShownBeforeExecution(t *testing.T) {
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1.example.com"},
	}
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	// Not started.
	m.executeStarted = false
	m.executeDone = false

	view := m.View()
	if strings.Contains(view, "r retry") {
		t.Error("execute step view should NOT show 'r retry' hint before execution starts")
	}
}

// TestExecuteStepRetryKeyIgnoredBeforeExecutionComplete verifies that pressing
// 'r' before execution is complete has no effect on the model.
func TestExecuteStepRetryKeyIgnoredBeforeExecutionComplete(t *testing.T) {
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1.example.com"},
	}
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	m.executeStarted = true
	m.executeDone = false
	m.hostProgress = map[string]executor.TransferStatus{
		"host1.example.com": executor.TransferInProgress,
	}

	updated, _ := sendDistributeKey(m, "r")
	// Should still be on Execute step with no state change.
	if updated.step != DistributeStepExecute {
		t.Errorf("pressing 'r' during execution should stay on Execute step, got step %d", updated.step)
	}
	if updated.executeDone {
		t.Error("pressing 'r' during execution should not set executeDone")
	}
}

// TestExecuteStepRetryKeyIgnoredWhenNoFailures verifies that pressing 'r' when
// all transfers succeeded is a no-op.
func TestExecuteStepRetryKeyIgnoredWhenNoFailures(t *testing.T) {
	m := makeExecuteDoneModelAllSucceeded([]string{"host1.example.com"})

	updated, _ := sendDistributeKey(m, "r")
	// Should stay on Execute step (no retry needed).
	if updated.step != DistributeStepExecute {
		t.Errorf("pressing 'r' with no failures should stay on Execute step, got step %d", updated.step)
	}
}

// TestExecuteStepRetryKeyTransitionsToRetryConfirmWhenFailuresExist verifies
// that pressing 'r' after execution completes with failures transitions the
// model to DistributeStepRetryConfirm.
func TestExecuteStepRetryKeyTransitionsToRetryConfirmWhenFailuresExist(t *testing.T) {
	m := makeExecuteDoneModelWithFailures([]string{"host1.example.com", "host2.example.com"})

	updated, _ := sendDistributeKey(m, "r")
	if updated.step != DistributeStepRetryConfirm {
		t.Errorf("pressing 'r' with failures should transition to DistributeStepRetryConfirm, got step %d", updated.step)
	}
}

// TestExecuteStepRetryKeyOnlyIncludesFailedHosts verifies that after pressing
// 'r', the retry model only contains the hosts that failed (not the successful ones).
func TestExecuteStepRetryKeyOnlyIncludesFailedHosts(t *testing.T) {
	// Mix of succeeded and failed hosts.
	allHosts := []config.ResolvedHost{
		{Host: "ok.example.com", DisplayName: "ok.example.com"},
		{Host: "fail.example.com", DisplayName: "fail.example.com"},
	}
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = allHosts
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = map[string]executor.TransferStatus{
		"ok.example.com":   executor.TransferDone,
		"fail.example.com": executor.TransferFailed,
	}

	updated, _ := sendDistributeKey(m, "r")
	if updated.step != DistributeStepRetryConfirm {
		t.Fatalf("expected DistributeStepRetryConfirm after 'r', got step %d", updated.step)
	}
	// The retry model's destHosts should only contain the failed host.
	if len(updated.destHosts) != 1 {
		t.Errorf("retry model should have 1 failed host, got %d", len(updated.destHosts))
	}
	if len(updated.destHosts) > 0 && updated.destHosts[0].Host != "fail.example.com" {
		t.Errorf("retry model should include 'fail.example.com', got %q", updated.destHosts[0].Host)
	}
}

// ---------------------------------------------------------------------------
// AC 8: Checksum verification toggle continues to work during retries
// ---------------------------------------------------------------------------

// TestRetryConfirmSpaceTogglesChecksum verifies that pressing Space in
// DistributeStepRetryConfirm toggles verifyChecksum on and off, mirroring
// the behaviour of the normal DistributeStepConfirm step.
func TestRetryConfirmSpaceTogglesChecksum(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	if m.verifyChecksum {
		t.Error("verifyChecksum should be false initially in retry model")
	}

	// First Space toggles on.
	m, _ = sendDistributeKey(m, " ")
	if !m.verifyChecksum {
		t.Error("after first Space in retry-confirm verifyChecksum should be true")
	}

	// Second Space toggles off.
	m, _ = sendDistributeKey(m, " ")
	if m.verifyChecksum {
		t.Error("after second Space in retry-confirm verifyChecksum should be false")
	}
}

// TestRetryConfirmSpaceDoesNotAdvanceStep verifies that pressing Space in the
// retry-confirm step only toggles the checkbox and does not advance to Execute.
func TestRetryConfirmSpaceDoesNotAdvanceStep(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, _ = sendDistributeKey(m, " ")

	if m.step != DistributeStepRetryConfirm {
		t.Errorf("Space should not advance from retry-confirm; expected step %d, got %d",
			DistributeStepRetryConfirm, m.step)
	}
}

// TestRetryConfirmViewShowsChecksumCheckbox verifies that the retry-confirm
// step view renders the checksum verification checkbox.
func TestRetryConfirmViewShowsChecksumCheckbox(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "checksum") {
		t.Error("retry-confirm view should show the checksum verification checkbox")
	}
}

// TestRetryConfirmViewChecksumUncheckedByDefault verifies that the checksum
// checkbox is shown as unchecked by default.
func TestRetryConfirmViewChecksumUncheckedByDefault(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "[ ]") {
		t.Error("retry-confirm view should show unchecked checkbox [ ] by default")
	}
}

// TestRetryConfirmViewChecksumCheckedAfterToggle verifies that after pressing
// Space the checkbox displays as checked.
func TestRetryConfirmViewChecksumCheckedAfterToggle(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	m, _ = sendDistributeKey(m, " ")
	view := m.View()

	if !strings.Contains(view, "[✓]") {
		t.Error("retry-confirm view should show checked checkbox [✓] after Space toggle")
	}
}

// TestRetryConfirmViewHintIncludesSpaceToggle verifies that the hint line in
// the retry-confirm step mentions the space key for toggling the checkbox.
func TestRetryConfirmViewHintIncludesSpaceToggle(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	view := m.View()

	if !strings.Contains(view, "space") {
		t.Error("retry-confirm hint should mention 'space' for toggling the checksum checkbox")
	}
}

// TestRetryChecksumPreservedFromExecuteStep verifies that when pressing 'r'
// to retry after execution, the current verifyChecksum value is preserved in
// the new retry model so the user does not have to re-toggle it.
func TestRetryChecksumPreservedFromExecuteStep(t *testing.T) {
	m := makeExecuteDoneModelWithFailures([]string{"fail.example.com"})
	// Enable checksum on the execute-step model before pressing 'r'.
	m.verifyChecksum = true

	retryModel, _ := sendDistributeKey(m, "r")

	if !retryModel.verifyChecksum {
		t.Error("verifyChecksum should be preserved (true) in the retry model created by 'r'")
	}
}

// TestRetryChecksumFalsePreservedFromExecuteStep verifies that a false
// verifyChecksum on the execute-step model is also correctly preserved when
// the retry model is created.
func TestRetryChecksumFalsePreservedFromExecuteStep(t *testing.T) {
	m := makeExecuteDoneModelWithFailures([]string{"fail.example.com"})
	// verifyChecksum is false by default — confirm it stays false in retry.
	m.verifyChecksum = false

	retryModel, _ := sendDistributeKey(m, "r")

	if retryModel.verifyChecksum {
		t.Error("verifyChecksum should be preserved (false) in the retry model created by 'r'")
	}
}

// TestRetryChecksumStateNotLostOnConfirm verifies that after the user toggles
// the checksum checkbox in DistributeStepRetryConfirm and presses Enter to
// confirm the retry, the verifyChecksum state is carried into the Execute step.
func TestRetryChecksumStateNotLostOnConfirm(t *testing.T) {
	params := makeRetryParams("h1.example.com")
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	// Toggle checksum on in retry-confirm.
	m, _ = sendDistributeKey(m, " ")
	if !m.verifyChecksum {
		t.Fatal("verifyChecksum should be true after Space in retry-confirm")
	}

	// Confirm the retry.
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepExecute {
		t.Fatalf("expected Execute step after confirming retry, got step %d", m.step)
	}
	if !m.verifyChecksum {
		t.Error("verifyChecksum should remain true after confirming the retry-confirm step")
	}
}
