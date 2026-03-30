package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestAC1DirectParallelShows7Steps verifies AC 1: direct-parallel mode wizard
// shows exactly 7 steps (Source, Browse, Destinations, CopyMode, DestPath,
// Confirm, Execute) and never includes the hub-selection step.
func TestAC1DirectParallelShows7Steps(t *testing.T) {
	m := newTestDistributeModel()
	// Default (no copy mode selected yet) — should be 7 steps.
	m.copyMode = ""
	steps := m.visibleWizardSteps()
	if len(steps) != 7 {
		t.Errorf("direct-parallel (empty copyMode): expected 7 steps, got %d: %v", len(steps), steps)
	}

	// Explicit direct-parallel — also 7 steps.
	m.copyMode = "parallel"
	steps = m.visibleWizardSteps()
	if len(steps) != 7 {
		t.Errorf("direct-parallel ('parallel'): expected 7 steps, got %d: %v", len(steps), steps)
	}

	// Verify the 7 steps are exactly the right ones in order.
	expected := []DistributeStep{
		DistributeStepSourceSelect,
		DistributeStepFileBrowse,
		DistributeStepDestHosts,
		DistributeStepCopyMode,
		DistributeStepDestPath,
		DistributeStepConfirm,
		DistributeStepExecute,
	}
	for i, want := range expected {
		if steps[i] != want {
			t.Errorf("step[%d]: expected %v, got %v", i, want, steps[i])
		}
	}

	// Verify HubSelect is NOT in the list.
	for _, s := range steps {
		if s == DistributeStepHubSelect {
			t.Error("direct-parallel mode must NOT include DistributeStepHubSelect")
		}
	}
}

// TestAC1TotalStepsDirectParallel verifies that totalSteps() returns 7 for
// direct-parallel mode.
func TestAC1TotalStepsDirectParallel(t *testing.T) {
	m := newTestDistributeModel()
	for _, mode := range []string{"", "parallel"} {
		m.copyMode = mode
		if got := m.totalSteps(); got != 7 {
			t.Errorf("copyMode=%q: expected totalSteps()=7, got %d", mode, got)
		}
	}
}

// TestAC1DisplayStepIndexDirectParallel verifies that displayStepIndex()
// returns 1-7 (with no gap) for direct-parallel mode.
func TestAC1DisplayStepIndexDirectParallel(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "parallel"

	cases := []struct {
		step    DistributeStep
		wantIdx int
	}{
		{DistributeStepSourceSelect, 1},
		{DistributeStepFileBrowse, 2},
		{DistributeStepDestHosts, 3},
		{DistributeStepCopyMode, 4},
		{DistributeStepDestPath, 5},
		{DistributeStepConfirm, 6},
		{DistributeStepExecute, 7},
	}
	for _, tc := range cases {
		got := m.displayStepIndex(tc.step)
		if got != tc.wantIdx {
			t.Errorf("displayStepIndex(%v) = %d, want %d", tc.step, got, tc.wantIdx)
		}
	}
}

// TestAC1StepHeadersShowOf7InDirectParallelMode verifies that each step
// header in direct-parallel mode contains "of 7".
func TestAC1StepHeadersShowOf7InDirectParallelMode(t *testing.T) {
	cases := []struct {
		step    DistributeStep
		keyword string
	}{
		{DistributeStepSourceSelect, "Step 1 of 7"},
		{DistributeStepDestHosts, "Step 3 of 7"},
		{DistributeStepCopyMode, "Step 4 of 7"},
		{DistributeStepDestPath, "Step 5 of 7"},
		{DistributeStepConfirm, "Step 6 of 7"},
		{DistributeStepExecute, "Step 7 of 7"},
	}
	for _, tc := range cases {
		m := newTestDistributeModel()
		m.step = tc.step
		m.copyMode = "parallel"
		view := m.View()
		if !strings.Contains(view, tc.keyword) {
			t.Errorf("step %v: expected view to contain %q\n%s", tc.step, tc.keyword, view)
		}
	}
}

// TestAC1BreadcrumbHas7ItemsInDirectParallelMode verifies that the breadcrumb
// renders exactly 7 step labels for direct-parallel mode (no "Select Hub").
func TestAC1BreadcrumbHas7ItemsInDirectParallelMode(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "parallel"
	view := m.View()

	// Count the step arrows (→) in the breadcrumb - should be 6 separators for 7 items.
	// Each separator is " → " (with spaces).
	separators := strings.Count(view, " → ")
	if separators != 6 {
		t.Errorf("direct-parallel breadcrumb: expected 6 separators (for 7 steps), got %d", separators)
	}

	// "Select Hub" must not appear in direct-parallel mode.
	if strings.Contains(view, "Select Hub") {
		t.Error("direct-parallel breadcrumb must NOT contain 'Select Hub'")
	}

	// All 7 direct-parallel labels must appear.
	for _, label := range distributeStepLabels {
		if !strings.Contains(view, label) {
			t.Errorf("direct-parallel breadcrumb missing label %q", label)
		}
	}
	fmt.Println("Direct-parallel breadcrumb OK — 7 steps, no hub selection")
}
