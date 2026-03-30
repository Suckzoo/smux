// Tests for AC 7: Switching from hub-and-spoke to direct-parallel via
// back-navigation removes the hub step and drops the total from 8 to 7
// seamlessly.
//
// These tests exercise the back-navigation path where a user:
//  1. Advances into hub-and-spoke mode (total = 8 steps).
//  2. Navigates back through Esc to the copy-mode selection step.
//  3. Changes the selection to direct-parallel.
//  4. Verifies that the total drops to 7, the hub-selection step disappears
//     from the visible sequence, and all step headers update accordingly.
package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// advanceToHubSpokeDestPath sets up a model that has progressed all the way
// through hub-and-spoke mode to the DestPath step (step 6 of 8).  The caller
// may then navigate back or continue forward.
func advanceToHubSpokeDestPath(t *testing.T) DistributeModel {
	t.Helper()
	m := newTestDistributeModel()

	// Inject two destination hosts so hub selection is possible.
	m.destHosts = []config.ResolvedHost{
		{Host: "host-01", DisplayName: "host-01"},
		{Host: "host-02", DisplayName: "host-02"},
	}

	// Jump straight to the CopyMode step (avoids needing real SSH for
	// file-browsing and dest-host selection).
	m.step = DistributeStepCopyMode

	// Select hub-and-spoke (cursor index 1) and advance.
	m.copyModeCursor = 1
	m, _ = sendDistributeKey(m, "enter") // CopyMode → HubSelect

	if m.step != DistributeStepHubSelect {
		t.Fatalf("expected HubSelect after entering hub-spoke, got step %v", m.step)
	}

	// Confirm hub selection (first host) to advance to DestPath.
	m, _ = sendDistributeKey(m, "enter") // HubSelect → DestPath

	if m.step != DistributeStepDestPath {
		t.Fatalf("expected DestPath after hub selection, got step %v", m.step)
	}

	return m
}

// ---------------------------------------------------------------------------
// AC 7: Back-navigation from hub-and-spoke to direct-parallel
// ---------------------------------------------------------------------------

// TestAC7_BackNavFromDestPathReachesHubSelect verifies that pressing Esc from
// DestPath in hub-and-spoke mode takes the user back to the HubSelect step
// (not past it).
func TestAC7_BackNavFromDestPathReachesHubSelect(t *testing.T) {
	m := advanceToHubSpokeDestPath(t)

	// Should be at DestPath with hub-spoke mode and total=8.
	if m.totalSteps() != 8 {
		t.Fatalf("pre-condition: expected 8 total steps, got %d", m.totalSteps())
	}

	m, _ = sendDistributeKey(m, "esc") // DestPath → HubSelect

	if m.step != DistributeStepHubSelect {
		t.Errorf("Esc from DestPath (hub-spoke) should land on HubSelect, got step %v", m.step)
	}
	// Still hub-spoke mode, still 8 steps.
	if m.totalSteps() != 8 {
		t.Errorf("expected 8 total steps after back-nav to HubSelect, got %d", m.totalSteps())
	}
}

// TestAC7_BackNavFromHubSelectReachesCopyMode verifies that pressing Esc from
// HubSelect returns the user to the CopyMode step.
func TestAC7_BackNavFromHubSelectReachesCopyMode(t *testing.T) {
	m := advanceToHubSpokeDestPath(t)
	m, _ = sendDistributeKey(m, "esc") // DestPath → HubSelect
	m, _ = sendDistributeKey(m, "esc") // HubSelect → CopyMode

	if m.step != DistributeStepCopyMode {
		t.Errorf("Esc from HubSelect should land on CopyMode, got step %v", m.step)
	}
}

// TestAC7_SelectDirectParallelAfterBackNavDropsToSevenSteps verifies that
// after navigating back to CopyMode from hub-and-spoke mode and selecting
// direct-parallel, the total step count drops from 8 to 7.
func TestAC7_SelectDirectParallelAfterBackNavDropsToSevenSteps(t *testing.T) {
	m := advanceToHubSpokeDestPath(t) // hub-spoke, total=8

	// Navigate back to CopyMode.
	m, _ = sendDistributeKey(m, "esc") // DestPath → HubSelect
	m, _ = sendDistributeKey(m, "esc") // HubSelect → CopyMode

	// Select direct-parallel (cursor index 0).
	m.copyModeCursor = 0
	m, _ = sendDistributeKey(m, "enter") // CopyMode → DestPath (direct-parallel)

	if m.totalSteps() != 7 {
		t.Errorf("expected 7 total steps after switching to direct-parallel, got %d", m.totalSteps())
	}
}

// TestAC7_SwitchToDirectParallelRemovesHubStep verifies that after switching
// from hub-and-spoke to direct-parallel via back-navigation, DistributeStepHubSelect
// is no longer present in the visible wizard steps.
func TestAC7_SwitchToDirectParallelRemovesHubStep(t *testing.T) {
	m := advanceToHubSpokeDestPath(t)

	// Navigate back to CopyMode and switch to direct-parallel.
	m, _ = sendDistributeKey(m, "esc")
	m, _ = sendDistributeKey(m, "esc")
	m.copyModeCursor = 0
	m, _ = sendDistributeKey(m, "enter")

	for _, s := range m.visibleWizardSteps() {
		if s == DistributeStepHubSelect {
			t.Error("DistributeStepHubSelect must NOT appear in visible steps after switching to direct-parallel")
		}
	}
}

// TestAC7_SwitchToDirectParallelLandsOnDestPath verifies that after switching
// to direct-parallel via back-navigation the wizard is positioned at DestPath.
func TestAC7_SwitchToDirectParallelLandsOnDestPath(t *testing.T) {
	m := advanceToHubSpokeDestPath(t)

	m, _ = sendDistributeKey(m, "esc")
	m, _ = sendDistributeKey(m, "esc")
	m.copyModeCursor = 0
	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepDestPath {
		t.Errorf("expected to land on DestPath after switching to direct-parallel, got step %v", m.step)
	}
}

// TestAC7_StepHeaderShowsOf7AfterSwitch verifies that after switching from
// hub-and-spoke to direct-parallel, each step header correctly displays "of 7".
func TestAC7_StepHeaderShowsOf7AfterSwitch(t *testing.T) {
	switchToCases := []struct {
		step    DistributeStep
		keyword string
	}{
		{DistributeStepDestPath, "Step 5 of 7"},
		{DistributeStepConfirm, "Step 6 of 7"},
		{DistributeStepExecute, "Step 7 of 7"},
	}

	for _, tc := range switchToCases {
		m := newTestDistributeModel()
		// Simulate having switched back to direct-parallel.
		m.copyMode = "parallel"
		m.step = tc.step

		view := m.View()
		if !strings.Contains(view, tc.keyword) {
			t.Errorf("step %v after switch: expected view to contain %q\n%s",
				tc.step, tc.keyword, view)
		}
		// Must not still say "of 8".
		if strings.Contains(view, "of 8") {
			t.Errorf("step %v after switch to direct-parallel: view must not contain 'of 8'\n%s",
				tc.step, view)
		}
	}
	fmt.Println("AC7: step headers show 'of 7' after switching to direct-parallel OK")
}

// TestAC7_BackNavFromDestPathInDirectParallelSkipsHubSelect verifies that
// pressing Esc from DestPath in direct-parallel mode skips the hub-selection
// step entirely and lands on CopyMode.
func TestAC7_BackNavFromDestPathInDirectParallelSkipsHubSelect(t *testing.T) {
	m := newTestDistributeModel()
	m.destHosts = []config.ResolvedHost{
		{Host: "host-01", DisplayName: "host-01"},
	}
	m.copyMode = "parallel"
	m.step = DistributeStepDestPath

	m, _ = sendDistributeKey(m, "esc") // DestPath → CopyMode (HubSelect skipped)

	if m.step == DistributeStepHubSelect {
		t.Error("back-nav from DestPath in direct-parallel mode must NOT land on HubSelect")
	}
	if m.step != DistributeStepCopyMode {
		t.Errorf("back-nav from DestPath in direct-parallel mode should land on CopyMode, got step %v", m.step)
	}
	if m.totalSteps() != 7 {
		t.Errorf("total steps should remain 7 in direct-parallel mode, got %d", m.totalSteps())
	}
}

// TestAC7_TotalStepsDynamicAfterModeSwitch verifies that totalSteps() correctly
// reflects the copy mode at each transition point during back-navigation and
// re-selection.
func TestAC7_TotalStepsDynamicAfterModeSwitch(t *testing.T) {
	m := advanceToHubSpokeDestPath(t)

	// At DestPath in hub-spoke: expect 8.
	if got := m.totalSteps(); got != 8 {
		t.Errorf("[DestPath, hub-spoke] expected totalSteps=8, got %d", got)
	}

	m, _ = sendDistributeKey(m, "esc") // → HubSelect
	if got := m.totalSteps(); got != 8 {
		t.Errorf("[HubSelect, hub-spoke] expected totalSteps=8, got %d", got)
	}

	m, _ = sendDistributeKey(m, "esc") // → CopyMode (copyMode still "hub-spoke")
	if got := m.totalSteps(); got != 8 {
		t.Errorf("[CopyMode, hub-spoke before re-select] expected totalSteps=8, got %d", got)
	}

	// Now switch to direct-parallel.
	m.copyModeCursor = 0
	m, _ = sendDistributeKey(m, "enter") // CopyMode → DestPath
	if got := m.totalSteps(); got != 7 {
		t.Errorf("[DestPath, direct-parallel after switch] expected totalSteps=7, got %d", got)
	}

	// Back again from DestPath should stay at 7 and not pass through HubSelect.
	m, _ = sendDistributeKey(m, "esc") // DestPath → CopyMode (HubSelect skipped)
	if m.step != DistributeStepCopyMode {
		t.Errorf("after re-switch back-nav: expected CopyMode, got step %v", m.step)
	}
	if got := m.totalSteps(); got != 7 {
		t.Errorf("[CopyMode, direct-parallel] expected totalSteps=7, got %d", got)
	}
}

// TestAC7_BreadcrumbDropsToSevenAfterSwitch verifies that the breadcrumb
// rendered by View() shows exactly 6 separators (7 items) after switching from
// hub-and-spoke to direct-parallel.
func TestAC7_BreadcrumbDropsToSevenAfterSwitch(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "parallel" // simulate post-switch state
	m.step = DistributeStepDestPath

	view := m.View()

	// 7 breadcrumb items → 6 " → " separators.
	sep := strings.Count(view, " → ")
	if sep != 6 {
		t.Errorf("after switch to direct-parallel: expected 6 breadcrumb separators (7 steps), got %d", sep)
	}

	// "Select Hub" must not appear.
	if strings.Contains(view, "Select Hub") {
		t.Error("after switch to direct-parallel: 'Select Hub' must NOT appear in breadcrumb")
	}
}
