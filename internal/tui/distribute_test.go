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

	// Step 3 → 4: Enter from CopyMode moves to Confirm.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepConfirm {
		t.Errorf("after Enter on CopyMode expected step %d (Confirm), got %d", DistributeStepConfirm, m.step)
	}

	// Step 4 → 5: Enter from Confirm moves to Execute.
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
	// All step labels should appear in the breadcrumb.
	for _, label := range distributeStepLabels {
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

// TestSourceOriginListIncludesLocal verifies that the source-origin item list
// always begins with a "local" entry (empty host field).
func TestSourceOriginListIncludesLocal(t *testing.T) {
	m := newTestDistributeModel()
	if len(m.sourceOriginItems) == 0 {
		t.Fatal("sourceOriginItems must not be empty")
	}
	if m.sourceOriginItems[0].host != "" {
		t.Errorf("first item should be local (host=\"\"), got host=%q", m.sourceOriginItems[0].host)
	}
}

// TestSourceOriginListIncludesConfiguredHosts verifies that remote hosts from
// the config are present in the source-origin list after the local entry.
func TestSourceOriginListIncludesConfiguredHosts(t *testing.T) {
	m := newTestDistributeModel()
	// minimalConfig has host-01 and host-02; so list should have 3 items.
	if len(m.sourceOriginItems) != 3 {
		t.Errorf("expected 3 source origin items (local + 2 hosts), got %d", len(m.sourceOriginItems))
	}
	// Items 1 and 2 should be the configured hosts.
	for _, item := range m.sourceOriginItems[1:] {
		if item.host == "" {
			t.Errorf("remote item should have non-empty host, got label=%q", item.label)
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
	last := len(m.sourceOriginItems) - 1
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
	// Move cursor to the first remote host (index 1).
	m, _ = sendDistributeKey(m, "down")
	wantHost := m.sourceOriginItems[1].host

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

	// After selecting a remote host.
	m.sourceOriginCursor = 1
	m, _ = sendDistributeKey(m, "enter")
	want := m.sourceOriginItems[1].host
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

	m, _ = sendDistributeKey(m, "down")
	if m.destHostCursor != 1 {
		t.Errorf("after down: expected cursor 1, got %d", m.destHostCursor)
	}
	m, _ = sendDistributeKey(m, "j")
	// minimalConfig has 2 hosts; cursor should stay at 1 (last index).
	if m.destHostCursor != 1 {
		t.Errorf("after j at last item: expected cursor 1, got %d", m.destHostCursor)
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

// TestDestHostEnterPersistsSelection verifies that pressing Enter in step 1
// stores the selected hosts (in list order) in destHosts and advances to step 2.
func TestDestHostEnterPersistsSelection(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Select both hosts.
	m, _ = sendDistributeKey(m, " ")      // select host-01
	m, _ = sendDistributeKey(m, "down")   // move to host-02
	m, _ = sendDistributeKey(m, " ")      // select host-02

	// Confirm.
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepCopyMode {
		t.Errorf("after Enter on step 1 expected step %d, got %d", DistributeStepCopyMode, m.step)
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

	// Select one host and confirm.
	m, _ = sendDistributeKey(m, " ")
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

// TestDestHostViewShowsSelectionCount verifies that the hint line in step 1
// reflects how many hosts are currently selected.
func TestDestHostViewShowsSelectionCount(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepDestHosts

	// Initially 0 selected.
	view := m.View()
	if !strings.Contains(view, "0 selected") {
		t.Error("step 1 view should show '0 selected' initially")
	}

	// Select one host.
	m, _ = sendDistributeKey(m, " ")
	view = m.View()
	if !strings.Contains(view, "1 selected") {
		t.Error("step 1 view should show '1 selected' after one toggle")
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
	m, _ = sendDistributeKey(m, "down")          // cursor → 1 (first remote host)
	wantHost := m.sourceOriginItems[1].host
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
	if len(m.sourceOriginItems) < 3 {
		t.Skip("need at least 2 remote hosts configured")
	}

	// First visit: select host at index 1.
	m, _ = sendDistributeKey(m, "down")  // cursor → 1
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse with host[1]
	firstTree := m.remoteFileTree
	if firstTree == nil {
		t.Fatal("remoteFileTree should be initialised")
	}

	m, _ = sendDistributeKey(m, "esc") // back to SourceSelect

	// Second visit: move cursor to host[2] and re-enter.
	m, _ = sendDistributeKey(m, "down") // cursor → 2
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse with host[2]

	if m.remoteFileTree == firstTree {
		t.Error("remoteFileTree should be recreated when the source host changes")
	}
	if m.remoteTreeForHost != m.sourceOriginItems[2].host {
		t.Errorf("remoteTreeForHost should be %q, got %q",
			m.sourceOriginItems[2].host, m.remoteTreeForHost)
	}
}

// TestFileBrowseRemoteTreeReusedWhenSameHostChosen verifies that re-entering
// FileBrowse with the same remote host preserves the existing tree.
func TestFileBrowseRemoteTreeReusedWhenSameHostChosen(t *testing.T) {
	m := newTestDistributeModel()
	if len(m.sourceOriginItems) < 2 {
		t.Skip("need at least 1 remote host configured")
	}

	// Visit FileBrowse with host[1].
	m, _ = sendDistributeKey(m, "down")  // cursor → 1
	m, _ = sendDistributeKey(m, "enter") // → FileBrowse
	firstTree := m.remoteFileTree

	m, _ = sendDistributeKey(m, "esc")   // back to SourceSelect (cursor stays at 1)
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
// the cursor at index 0 selects "parallel" mode and advances to ConfirmStep.
func TestCopyModeEnterSelectsParallelByDefault(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	// Cursor starts at 0 (Direct parallel).

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("after Enter on CopyMode expected step %d (Confirm), got %d",
			DistributeStepConfirm, m.step)
	}
	if m.copyMode != "parallel" {
		t.Errorf("expected copyMode \"parallel\", got %q", m.copyMode)
	}
}

// TestCopyModeEnterSelectsHubAndSpoke verifies that pressing Enter with the
// cursor at index 1 selects "hub-spoke" mode.
func TestCopyModeEnterSelectsHubAndSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepConfirm {
		t.Errorf("after Enter (hub-spoke) expected step %d (Confirm), got %d",
			DistributeStepConfirm, m.step)
	}
	if m.copyMode != "hub-spoke" {
		t.Errorf("expected copyMode \"hub-spoke\", got %q", m.copyMode)
	}
}

// TestCopyModeCursorPreservedOnBackNav verifies that pressing Esc from the
// Confirm step back to CopyMode preserves the cursor position.
func TestCopyModeCursorPreservedOnBackNav(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyModeCursor = 1

	m, _ = sendDistributeKey(m, "enter") // step 3 → 4 (Confirm)
	m, _ = sendDistributeKey(m, "esc")   // step 4 → 3 (CopyMode)

	if m.step != DistributeStepCopyMode {
		t.Fatalf("Esc from Confirm should return to CopyMode, got step %d", m.step)
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

// TestBuildSourceOriginItemsEmptyConfig verifies that buildSourceOriginItems
// returns exactly the local entry when no hosts are configured.
func TestBuildSourceOriginItemsEmptyConfig(t *testing.T) {
	cfg := emptyConfig()
	items := buildSourceOriginItems(cfg)
	if len(items) != 1 {
		t.Errorf("empty config: expected 1 item (local only), got %d", len(items))
	}
	if items[0].host != "" {
		t.Errorf("first item must be local (host=\"\"), got %q", items[0].host)
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

// TestConfirmStepEscGoesBackToCopyMode verifies that pressing Esc from
// DistributeStepConfirm moves back to DistributeStepCopyMode.
func TestConfirmStepEscGoesBackToCopyMode(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepCopyMode {
		t.Errorf("Esc from Confirm should go back to CopyMode (step %d), got %d",
			DistributeStepCopyMode, m.step)
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

	// Go back to CopyMode.
	m, _ = sendDistributeKey(m, "esc")
	if m.step != DistributeStepCopyMode {
		t.Fatalf("Esc should return to CopyMode, got step %d", m.step)
	}

	// Advance to Confirm again via Enter.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepConfirm {
		t.Fatalf("Enter on CopyMode should go to Confirm, got step %d", m.step)
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
// destination path, falling back to "(same as source)" when it is empty.
func TestConfirmStepViewShowsDestPath(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepConfirm

	// Empty destPath → show fallback.
	view := m.View()
	if !strings.Contains(view, "same as source") {
		t.Error("confirm view should show '(same as source)' when destPath is empty")
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
	for _, label := range distributeStepLabels {
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
