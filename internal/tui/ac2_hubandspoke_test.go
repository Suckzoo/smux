package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestAC2HubSpokeShows8Steps verifies AC 2: hub-and-spoke mode wizard shows
// exactly 8 steps: [Source, Browse, Destinations, CopyMode, HubNodeSelection,
// DestPath, Confirm, Execute].
func TestAC2HubSpokeShows8Steps(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "hub-spoke"

	steps := m.visibleWizardSteps()
	if len(steps) != 8 {
		t.Errorf("hub-and-spoke: expected 8 steps, got %d: %v", len(steps), steps)
	}

	// Verify the 8 steps are exactly the right ones in order.
	expected := []DistributeStep{
		DistributeStepSourceSelect,
		DistributeStepFileBrowse,
		DistributeStepDestHosts,
		DistributeStepCopyMode,
		DistributeStepHubSelect,
		DistributeStepDestPath,
		DistributeStepConfirm,
		DistributeStepExecute,
	}
	for i, want := range expected {
		if steps[i] != want {
			t.Errorf("step[%d]: expected %v, got %v", i, want, steps[i])
		}
	}
}

// TestAC2HubSpokeHubSelectAtIndex4 verifies that HubNodeSelection is at
// position index 4 (1-based: step 5) in the hub-and-spoke sequence.
func TestAC2HubSpokeHubSelectAtIndex4(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "hub-spoke"

	steps := m.visibleWizardSteps()
	if steps[4] != DistributeStepHubSelect {
		t.Errorf("hub-and-spoke: step[4] should be DistributeStepHubSelect, got %v", steps[4])
	}
}

// TestAC2TotalStepsHubSpoke verifies that totalSteps() returns 8 for
// hub-and-spoke mode.
func TestAC2TotalStepsHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "hub-spoke"

	if got := m.totalSteps(); got != 8 {
		t.Errorf("copyMode='hub-spoke': expected totalSteps()=8, got %d", got)
	}
}

// TestAC2DisplayStepIndexHubSpoke verifies that displayStepIndex() returns
// 1-8 (with no gap) for hub-and-spoke mode, and that HubSelect is at index 5.
func TestAC2DisplayStepIndexHubSpoke(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "hub-spoke"

	cases := []struct {
		step    DistributeStep
		wantIdx int
	}{
		{DistributeStepSourceSelect, 1},
		{DistributeStepFileBrowse, 2},
		{DistributeStepDestHosts, 3},
		{DistributeStepCopyMode, 4},
		{DistributeStepHubSelect, 5},
		{DistributeStepDestPath, 6},
		{DistributeStepConfirm, 7},
		{DistributeStepExecute, 8},
	}
	for _, tc := range cases {
		got := m.displayStepIndex(tc.step)
		if got != tc.wantIdx {
			t.Errorf("displayStepIndex(%v) = %d, want %d", tc.step, got, tc.wantIdx)
		}
	}
}

// TestAC2StepHeadersShowOf8InHubSpokeMode verifies that each step header in
// hub-and-spoke mode contains "of 8".
func TestAC2StepHeadersShowOf8InHubSpokeMode(t *testing.T) {
	cases := []struct {
		step    DistributeStep
		keyword string
	}{
		{DistributeStepSourceSelect, "Step 1 of 8"},
		{DistributeStepDestHosts, "Step 3 of 8"},
		{DistributeStepCopyMode, "Step 4 of 8"},
		{DistributeStepHubSelect, "Step 5 of 8"},
		{DistributeStepDestPath, "Step 6 of 8"},
		{DistributeStepConfirm, "Step 7 of 8"},
		{DistributeStepExecute, "Step 8 of 8"},
	}
	for _, tc := range cases {
		m := newTestDistributeModel()
		m.step = tc.step
		m.copyMode = "hub-spoke"
		// Populate destHosts for hub-select step to render correctly.
		if tc.step == DistributeStepHubSelect {
			m.destHosts = minimalConfig().AllResolvedHosts()
		}
		view := m.View()
		if !strings.Contains(view, tc.keyword) {
			t.Errorf("step %v: expected view to contain %q\n%s", tc.step, tc.keyword, view)
		}
	}
}

// TestAC2BreadcrumbHas8ItemsInHubSpokeMode verifies that the breadcrumb
// renders exactly 8 step labels for hub-and-spoke mode including "Select Hub".
func TestAC2BreadcrumbHas8ItemsInHubSpokeMode(t *testing.T) {
	m := newHubSelectModel()
	view := m.View()

	// Count the step arrows (→) in the breadcrumb — should be 7 separators for 8 items.
	separators := strings.Count(view, " → ")
	if separators != 7 {
		t.Errorf("hub-and-spoke breadcrumb: expected 7 separators (for 8 steps), got %d", separators)
	}

	// "Select Hub" must appear in hub-and-spoke mode.
	if !strings.Contains(view, "Select Hub") {
		t.Error("hub-and-spoke breadcrumb must contain 'Select Hub'")
	}

	// All 8 hub-spoke step labels must appear.
	hubSpokeLabels := []string{
		"Select Source",
		"Browse Files",
		"Select Destinations",
		"Choose Copy Mode",
		"Select Hub",
		"Destination Path",
		"Confirm",
		"Execute",
	}
	for _, label := range hubSpokeLabels {
		if !strings.Contains(view, label) {
			t.Errorf("hub-and-spoke breadcrumb missing label %q", label)
		}
	}
	fmt.Println("Hub-and-spoke breadcrumb OK — 8 steps, includes hub selection")
}

// TestAC2HubSpokeSequenceMatchesHubSpokeStepsVar verifies that the
// visibleWizardSteps() result for hub-spoke matches the hubSpokeSteps package variable.
func TestAC2HubSpokeSequenceMatchesHubSpokeStepsVar(t *testing.T) {
	m := newTestDistributeModel()
	m.copyMode = "hub-spoke"

	got := m.visibleWizardSteps()
	if len(got) != len(hubSpokeSteps) {
		t.Fatalf("visibleWizardSteps() length %d != hubSpokeSteps length %d", len(got), len(hubSpokeSteps))
	}
	for i := range hubSpokeSteps {
		if got[i] != hubSpokeSteps[i] {
			t.Errorf("step[%d]: visibleWizardSteps()=%v, hubSpokeSteps=%v", i, got[i], hubSpokeSteps[i])
		}
	}
}
