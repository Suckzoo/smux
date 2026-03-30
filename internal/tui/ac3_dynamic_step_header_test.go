package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestAC3StepIndexMethodReturnsCorrectIndex verifies that stepIndex() returns
// the 0-based position of the current step in visibleWizardSteps(), which is
// the canonical stepSequence.
func TestAC3StepIndexMethodReturnsCorrectIndex(t *testing.T) {
	// Direct-parallel mode: 7-step sequence.
	parallelCases := []struct {
		step      DistributeStep
		wantIndex int
	}{
		{DistributeStepSourceSelect, 0},
		{DistributeStepFileBrowse, 1},
		{DistributeStepDestHosts, 2},
		{DistributeStepCopyMode, 3},
		{DistributeStepDestPath, 4},
		{DistributeStepConfirm, 5},
		{DistributeStepExecute, 6},
	}
	for _, tc := range parallelCases {
		m := newTestDistributeModel()
		m.copyMode = "parallel"
		m.step = tc.step
		if got := m.stepIndex(); got != tc.wantIndex {
			t.Errorf("direct-parallel stepIndex(%v) = %d, want %d", tc.step, got, tc.wantIndex)
		}
	}

	// Hub-and-spoke mode: 8-step sequence.
	hubSpokeCases := []struct {
		step      DistributeStep
		wantIndex int
	}{
		{DistributeStepSourceSelect, 0},
		{DistributeStepFileBrowse, 1},
		{DistributeStepDestHosts, 2},
		{DistributeStepCopyMode, 3},
		{DistributeStepHubSelect, 4},
		{DistributeStepDestPath, 5},
		{DistributeStepConfirm, 6},
		{DistributeStepExecute, 7},
	}
	for _, tc := range hubSpokeCases {
		m := newTestDistributeModel()
		m.copyMode = "hub-spoke"
		m.step = tc.step
		if got := m.stepIndex(); got != tc.wantIndex {
			t.Errorf("hub-spoke stepIndex(%v) = %d, want %d", tc.step, got, tc.wantIndex)
		}
	}
}

// TestAC3StepIndexPlusOneEqualsTotalDisplayN verifies the invariant:
// stepIndex()+1 is always the correct 1-based N shown in "Step N of M".
func TestAC3StepIndexPlusOneEqualsTotalDisplayN(t *testing.T) {
	modes := []struct {
		name     string
		copyMode string
		steps    []DistributeStep
	}{
		{
			name:     "direct-parallel",
			copyMode: "parallel",
			steps:    directParallelSteps,
		},
		{
			name:     "hub-and-spoke",
			copyMode: "hub-spoke",
			steps:    hubSpokeSteps,
		},
	}
	for _, mode := range modes {
		for wantIdx, step := range mode.steps {
			m := newTestDistributeModel()
			m.copyMode = mode.copyMode
			m.step = step
			got := m.stepIndex() + 1
			want := wantIdx + 1
			if got != want {
				t.Errorf("%s step %v: stepIndex()+1 = %d, want %d", mode.name, step, got, want)
			}
		}
	}
}

// TestAC3StepIndexEqualsDisplayStepIndex verifies that stepIndex()+1 == displayStepIndex(m.step)
// for all steps in both modes — confirming that the two methods are equivalent.
func TestAC3StepIndexEqualsDisplayStepIndex(t *testing.T) {
	modes := []struct {
		copyMode string
		steps    []DistributeStep
	}{
		{"parallel", directParallelSteps},
		{"hub-spoke", hubSpokeSteps},
	}
	for _, mode := range modes {
		for _, step := range mode.steps {
			m := newTestDistributeModel()
			m.copyMode = mode.copyMode
			m.step = step
			fromStepIndex := m.stepIndex() + 1
			fromDisplayStep := m.displayStepIndex(step)
			if fromStepIndex != fromDisplayStep {
				t.Errorf("copyMode=%s step=%v: stepIndex()+1=%d != displayStepIndex(step)=%d",
					mode.copyMode, step, fromStepIndex, fromDisplayStep)
			}
		}
	}
}

// TestAC3AllStepHeadersShowDynamicNOfM verifies that every step renderer
// produces a "Step N of M" header where:
//   - M = len(visibleWizardSteps()) = totalSteps()
//   - N = stepIndex()+1
//
// This test is the canonical AC 3 acceptance test.
func TestAC3AllStepHeadersShowDynamicNOfM(t *testing.T) {
	modes := []struct {
		name     string
		copyMode string
		steps    []DistributeStep
		total    int
	}{
		{"direct-parallel", "parallel", directParallelSteps, 7},
		{"hub-and-spoke", "hub-spoke", hubSpokeSteps, 8},
	}

	for _, mode := range modes {
		for i, step := range mode.steps {
			wantN := i + 1
			wantM := mode.total
			keyword := fmt.Sprintf("Step %d of %d", wantN, wantM)

			m := newTestDistributeModel()
			m.step = step
			m.copyMode = mode.copyMode
			// Populate destHosts so the hub-select step can render.
			if step == DistributeStepHubSelect {
				m.destHosts = minimalConfig().AllResolvedHosts()
			}

			view := m.View()
			if !strings.Contains(view, keyword) {
				t.Errorf("%s step %v: expected view to contain %q\n--- view snippet ---\n%s",
					mode.name, step, keyword, firstNLines(view, 20))
			}
		}
	}
}

// TestAC3TotalStepsEqualsLenStepSequence verifies that totalSteps() always
// equals len(visibleWizardSteps()), never an independently stored constant.
func TestAC3TotalStepsEqualsLenStepSequence(t *testing.T) {
	cases := []struct {
		copyMode string
		want     int
	}{
		{"", 7},
		{"parallel", 7},
		{"hub-spoke", 8},
	}
	for _, tc := range cases {
		m := newTestDistributeModel()
		m.copyMode = tc.copyMode
		seq := m.visibleWizardSteps()
		total := m.totalSteps()
		if total != len(seq) {
			t.Errorf("copyMode=%q: totalSteps()=%d but len(visibleWizardSteps())=%d — must be equal",
				tc.copyMode, total, len(seq))
		}
		if total != tc.want {
			t.Errorf("copyMode=%q: totalSteps()=%d, want %d", tc.copyMode, total, tc.want)
		}
	}
}

// TestAC3CurrentStepNameMatchesSequenceAtStepIndex verifies that
// currentStepName() always returns the label for visibleWizardSteps()[stepIndex()],
// i.e. the current step name is always derived from the sequence.
func TestAC3CurrentStepNameMatchesSequenceAtStepIndex(t *testing.T) {
	modes := []struct {
		copyMode string
		steps    []DistributeStep
	}{
		{"parallel", directParallelSteps},
		{"hub-spoke", hubSpokeSteps},
	}
	for _, mode := range modes {
		for _, step := range mode.steps {
			m := newTestDistributeModel()
			m.copyMode = mode.copyMode
			m.step = step

			idx := m.stepIndex()
			seq := m.visibleWizardSteps()
			seqStep := seq[idx]

			wantName := distributeStepLabel[seqStep]
			gotName := m.currentStepName()

			if gotName != wantName {
				t.Errorf("copyMode=%s step=%v: currentStepName()=%q, want stepSequence[stepIndex()]=%q",
					mode.copyMode, step, gotName, wantName)
			}
		}
	}
}

// firstNLines returns the first n lines of s for use in error messages.
func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
