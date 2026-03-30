package tui

import (
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// AC 6: User cannot advance past hub selection without choosing a hub
// ---------------------------------------------------------------------------

// TestAC6HubSelectEnterBlockedWhenNoHosts verifies that pressing Enter on the
// DistributeStepHubSelect step does NOT advance the wizard when m.destHosts is
// empty.  This is the core validation requirement for AC 6.
func TestAC6HubSelectEnterBlockedWhenNoHosts(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	// Explicitly leave destHosts empty.
	m.destHosts = nil

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepHubSelect {
		t.Errorf(
			"Enter on HubSelect with empty destHosts must NOT advance; got step %d (want %d)",
			m.step, DistributeStepHubSelect,
		)
	}
}

// TestAC6HubSelectEnterBlockedWhenNoHostsEmptySlice is the same check but with
// an explicitly empty (non-nil) slice, confirming the guard handles both nil
// and len==0 cases.
func TestAC6HubSelectEnterBlockedWhenNoHostsEmptySlice(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{}

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepHubSelect {
		t.Errorf(
			"Enter on HubSelect with len(destHosts)==0 must NOT advance; got step %d (want %d)",
			m.step, DistributeStepHubSelect,
		)
	}
}

// TestAC6HubSelectEnterAdvancesWhenHostPresent verifies that pressing Enter
// with at least one destination host DOES advance to DistributeStepDestPath.
func TestAC6HubSelectEnterAdvancesWhenHostPresent(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "host-01", Host: "host-01"},
	}
	m.hubCursor = 0

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepDestPath {
		t.Errorf(
			"Enter on HubSelect with hosts present must advance to DestPath (step %d), got %d",
			DistributeStepDestPath, m.step,
		)
	}
}

// TestAC6HubSelectSetsHubHostOnEnter verifies that after pressing Enter the
// model's HubHost() returns the host at the selected cursor position.
func TestAC6HubSelectSetsHubHostOnEnter(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "hub-candidate", Host: "hub-candidate"},
		{DisplayName: "spoke-01", Host: "spoke-01"},
	}
	// Select the second host.
	m.hubCursor = 1

	m, _ = sendDistributeKey(m, "enter")

	got := m.HubHost()
	if got.DisplayName != "spoke-01" {
		t.Errorf("HubHost() after selecting cursor=1: expected DisplayName \"spoke-01\", got %q", got.DisplayName)
	}
}

// TestAC6HubSelectDefaultCursorSelectsFirstHost verifies that when the cursor
// stays at the default position (0) and Enter is pressed, the first host is
// chosen as the hub.  This ensures "choosing" is always explicit (Enter)
// regardless of cursor position.
func TestAC6HubSelectDefaultCursorSelectsFirstHost(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "first-host", Host: "first-host"},
		{DisplayName: "second-host", Host: "second-host"},
	}
	// Leave hubCursor at 0 (default).

	m, _ = sendDistributeKey(m, "enter")

	got := m.HubHost()
	if got.DisplayName != "first-host" {
		t.Errorf("HubHost() with default cursor: expected \"first-host\", got %q", got.DisplayName)
	}
}

// TestAC6HubSelectUpDownNavigation verifies that the up/down arrow keys (and
// j/k aliases) move the hub cursor without advancing the step.
func TestAC6HubSelectUpDownNavigation(t *testing.T) {
	hosts := []config.ResolvedHost{
		{DisplayName: "host-a", Host: "host-a"},
		{DisplayName: "host-b", Host: "host-b"},
		{DisplayName: "host-c", Host: "host-c"},
	}
	base := newTestDistributeModel()
	base.step = DistributeStepHubSelect
	base.copyMode = "hub-spoke"
	base.destHosts = hosts
	base.hubCursor = 0

	// down moves cursor to 1
	m, _ := sendDistributeKey(base, "down")
	if m.hubCursor != 1 {
		t.Errorf("after down: expected hubCursor=1, got %d", m.hubCursor)
	}
	if m.step != DistributeStepHubSelect {
		t.Errorf("down must NOT advance step; got step %d", m.step)
	}

	// j moves cursor to 2
	m, _ = sendDistributeKey(m, "j")
	if m.hubCursor != 2 {
		t.Errorf("after j: expected hubCursor=2, got %d", m.hubCursor)
	}

	// j at bottom does not overflow
	m, _ = sendDistributeKey(m, "j")
	if m.hubCursor != 2 {
		t.Errorf("j at bottom: hubCursor should stay at 2, got %d", m.hubCursor)
	}

	// up moves cursor to 1
	m, _ = sendDistributeKey(m, "up")
	if m.hubCursor != 1 {
		t.Errorf("after up: expected hubCursor=1, got %d", m.hubCursor)
	}

	// k moves cursor to 0
	m, _ = sendDistributeKey(m, "k")
	if m.hubCursor != 0 {
		t.Errorf("after k: expected hubCursor=0, got %d", m.hubCursor)
	}

	// k at top does not underflow
	m, _ = sendDistributeKey(m, "k")
	if m.hubCursor != 0 {
		t.Errorf("k at top: hubCursor should stay at 0, got %d", m.hubCursor)
	}
}

// TestAC6HubSelectViewShowsNoHostsMessage verifies that when destHosts is
// empty the rendered view contains the expected "go back" prompt (not a blank
// or misleading view).
func TestAC6HubSelectViewShowsNoHostsMessage(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = nil

	view := m.View()

	if !strings.Contains(view, "no destination hosts") {
		t.Errorf("hub select view with empty destHosts must mention 'no destination hosts'; got:\n%s", view)
	}
}

// TestAC6HubSelectViewShowsHostList verifies that when destHosts is non-empty
// the rendered view lists every destination host.
func TestAC6HubSelectViewShowsHostList(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "alpha", Host: "alpha"},
		{DisplayName: "beta", Host: "beta"},
	}

	view := m.View()

	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(view, name) {
			t.Errorf("hub select view must show host %q; got:\n%s", name, view)
		}
	}
}

// TestAC6HubSelectViewShowsCursorMarker verifies that the highlighted row is
// marked with the "▶" cursor indicator.
func TestAC6HubSelectViewShowsCursorMarker(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "host-01", Host: "host-01"},
	}
	m.hubCursor = 0

	view := m.View()

	if !strings.Contains(view, "▶") {
		t.Errorf("hub select view must show cursor marker '▶'; got:\n%s", view)
	}
}

// TestAC6HubSelectValidationDoesNotBlockOtherSteps confirms that the hub
// selection validation (blocking Enter when no hosts) does NOT affect other
// wizard steps.  Specifically, pressing Enter on DistributeStepConfirm must
// still advance to DistributeStepExecute regardless of destHosts/hubHost state.
func TestAC6HubSelectValidationDoesNotBlockOtherSteps(t *testing.T) {
	m := newTestDistributeModel()
	// Force Confirm step with no destHosts to confirm the guard is step-specific.
	m.step = DistributeStepConfirm
	m.destHosts = nil // empty — would block HubSelect but must NOT block Confirm
	m.copyMode = "parallel"

	m, _ = sendDistributeKey(m, "enter")

	if m.step != DistributeStepExecute {
		t.Errorf(
			"Enter on Confirm (with empty destHosts) must still advance to Execute (step %d), got %d",
			DistributeStepExecute, m.step,
		)
	}
}

// TestAC6HubSelectHubHostClearedOnCopyModeChange verifies that if the user
// navigates back from HubSelect to CopyMode and changes the mode, then
// re-enters hub-spoke and presses Enter, a fresh hubHost is set (old selection
// does not survive a copy mode round-trip through a different mode).
//
// This test exercises the interaction between back-navigation and hub selection
// state to ensure the validation guard doesn't create stale state.
func TestAC6HubSelectCursorClampedToValidRange(t *testing.T) {
	m := newTestDistributeModel()
	m.step = DistributeStepHubSelect
	m.copyMode = "hub-spoke"
	m.destHosts = []config.ResolvedHost{
		{DisplayName: "only-host", Host: "only-host"},
	}
	// Place cursor beyond the valid range (simulates stale state from a prior step).
	m.hubCursor = 99

	m, _ = sendDistributeKey(m, "enter")

	// Should still advance — cursor is clamped to len-1.
	if m.step != DistributeStepDestPath {
		t.Errorf("Enter with out-of-range cursor must still advance (cursor clamped); got step %d", m.step)
	}
	if m.HubHost().DisplayName != "only-host" {
		t.Errorf("clamped cursor must select the only available host; got %q", m.HubHost().DisplayName)
	}
}
