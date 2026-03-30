// Tests for AC 8: When copyMode changes from direct-parallel to hub-and-spoke,
// stepSequence recomputes to length 8 and stepIndex remains at 3 (CopyMode).
//
// These tests verify that:
//  1. The step sequence dynamically recomputes from 7 to 8 steps when copyMode
//     switches from "parallel" to "hub-spoke".
//  2. The CopyMode step's position in the new 8-step sequence is index 3
//     (0-based), meaning it is the 4th step (1-based display: "Step 4 of 8").
//  3. Pressing Enter on CopyMode with hub-spoke selected advances to HubSelect
//     (index 4) while the step sequence is 8 long.
package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestAC8StepSequenceRecomputesTo8OnHubSpoke verifies that when copyMode is
// changed from "parallel" to "hub-spoke" while the wizard is at the CopyMode
// step, visibleWizardSteps() immediately returns a sequence of length 8.
func TestAC8StepSequenceRecomputesTo8OnHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "parallel"

	// Pre-condition: 7-step sequence in direct-parallel mode.
	if got := len(m.visibleWizardSteps()); got != 7 {
		t.Fatalf("pre-condition: expected 7 steps in parallel mode, got %d", got)
	}

	// Change copyMode to hub-spoke (simulates the user selecting hub-and-spoke).
	m.copyMode = "hub-spoke"

	// Post-condition: step sequence recomputes to 8.
	steps := m.visibleWizardSteps()
	if got := len(steps); got != 8 {
		t.Errorf("after changing to hub-spoke: expected 8 steps, got %d: %v", got, steps)
	}
}

// TestAC8CopyModeStepRemainsAtIndex3AfterSwitch verifies that after copyMode
// changes from "parallel" to "hub-spoke", DistributeStepCopyMode is still at
// index 3 (0-based) in the newly-computed 8-step sequence.
func TestAC8CopyModeStepRemainsAtIndex3AfterSwitch(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "hub-spoke" // switch to hub-and-spoke

	steps := m.visibleWizardSteps()

	// Find the position of DistributeStepCopyMode in the sequence.
	copyModeIdx := -1
	for i, s := range steps {
		if s == DistributeStepCopyMode {
			copyModeIdx = i
			break
		}
	}

	if copyModeIdx != 3 {
		t.Errorf("DistributeStepCopyMode should be at index 3 in hub-spoke sequence, got index %d", copyModeIdx)
	}
}

// TestAC8TotalStepsIs8AfterSwitchToHubSpoke verifies that totalSteps() returns
// 8 immediately after copyMode changes from "parallel" to "hub-spoke".
func TestAC8TotalStepsIs8AfterSwitchToHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode

	// Before switch: 7 steps.
	m.copyMode = "parallel"
	if got := m.totalSteps(); got != 7 {
		t.Fatalf("pre-condition parallel: expected 7 total steps, got %d", got)
	}

	// After switch: 8 steps.
	m.copyMode = "hub-spoke"
	if got := m.totalSteps(); got != 8 {
		t.Errorf("post-switch hub-spoke: expected 8 total steps, got %d", got)
	}
}

// TestAC8DisplayStepIndexCopyModeIs4InHubSpoke verifies that after switching to
// hub-and-spoke, displayStepIndex(DistributeStepCopyMode) returns 4 (1-based),
// confirming that CopyMode is at 0-based index 3 in the 8-step sequence.
func TestAC8DisplayStepIndexCopyModeIs4InHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "hub-spoke"

	got := m.displayStepIndex(DistributeStepCopyMode)
	if got != 4 {
		t.Errorf("displayStepIndex(CopyMode) in hub-spoke = %d, want 4 (index 3, 1-based)", got)
	}
}

// TestAC8EnterOnHubSpokeAdvancesToHubSelectWith8Steps verifies that pressing
// Enter on the CopyMode step with hub-and-spoke selected advances the wizard to
// DistributeStepHubSelect and the sequence is 8 steps long.
func TestAC8EnterOnHubSpokeAdvancesToHubSelectWith8Steps(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "parallel"    // start in direct-parallel
	m.copyModeCursor = 1       // cursor points to hub-and-spoke (index 1 in copyModeItems)

	// Press Enter to select hub-and-spoke.
	m, _ = sendDistributeKey(m, "enter")

	// Wizard should advance to HubSelect.
	if m.step != DistributeStepHubSelect {
		t.Errorf("after Enter with hub-spoke cursor: expected HubSelect, got step %v", m.step)
	}

	// copyMode must be set to "hub-spoke".
	if m.copyMode != "hub-spoke" {
		t.Errorf("after Enter: expected copyMode='hub-spoke', got %q", m.copyMode)
	}

	// Step sequence must now be 8 long.
	if got := m.totalSteps(); got != 8 {
		t.Errorf("after advancing to HubSelect: expected totalSteps=8, got %d", got)
	}
}

// TestAC8StepHeaderShowsStep4Of8AtCopyModeInHubSpoke verifies that the CopyMode
// step header reads "Step 4 of 8" when the wizard is in hub-and-spoke mode, i.e.,
// copyMode is set to "hub-spoke" at the CopyMode step.
func TestAC8StepHeaderShowsStep4Of8AtCopyModeInHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "hub-spoke"

	view := m.View()
	want := "Step 4 of 8"
	if !strings.Contains(view, want) {
		t.Errorf("CopyMode step (hub-spoke): expected view to contain %q\n%s", want, view)
	}
}

// TestAC8HubSelectIsAtIndex4InSequenceAfterSwitch verifies that after switching
// to hub-and-spoke, DistributeStepHubSelect appears at index 4 (0-based) in
// the 8-step sequence — immediately after CopyMode (index 3).
func TestAC8HubSelectIsAtIndex4InSequenceAfterSwitch(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepCopyMode
	m.copyMode = "hub-spoke"

	steps := m.visibleWizardSteps()
	if len(steps) != 8 {
		t.Fatalf("expected 8-step sequence for hub-spoke, got %d", len(steps))
	}
	if steps[4] != DistributeStepHubSelect {
		t.Errorf("steps[4] should be DistributeStepHubSelect after switch to hub-spoke, got %v", steps[4])
	}
	fmt.Println("AC8: hub-spoke switch recomputes step sequence to 8, CopyMode stays at index 3 OK")
}
