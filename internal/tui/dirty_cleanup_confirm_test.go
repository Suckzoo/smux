// Tests for the on-demand dirty-host cleanup confirmation flow.
//
// These tests verify the DirtyCleanupConfirmPhase behaviour that is entered
// when the user presses 'C' (Shift+C) in BrowsingPhase with dirty hosts
// present.  The flow shows a security risk warning dialog before starting
// background cleanup of the selected (or all) dirty hosts.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Suckzoo/smux/internal/dirtystate"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// dirtyStateWithHosts builds a *dirtystate.State containing one spoke-type
// dirty record for each given SSH host address.
func dirtyStateWithHosts(hosts ...string) *dirtystate.State {
	s := &dirtystate.State{}
	for _, h := range hosts {
		s.Add(dirtystate.DirtyHost{
			Host:       h,
			KeyComment: "smux-distribute-" + h,
			AddedAt:    time.Now(),
		})
	}
	return s
}

// modelInBrowsingWithDirtyHosts returns a Model pre-loaded with a dirty state
// containing the given hosts.  It starts in BrowsingPhase (the normal startup
// phase used when the user acknowledges the startup warning).
func modelInBrowsingWithDirtyHosts(t *testing.T, hosts ...string) Model {
	t.Helper()
	cfg := dirtyConfig()
	ds := dirtyStateWithHosts(hosts...)
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	// Acknowledge the startup warning so we're in BrowsingPhase.
	m, _ = sendKey(m, "y")
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("expected BrowsingPhase after acknowledging startup warning; got %T", m.state.Phase)
	}
	return m
}

// ---------------------------------------------------------------------------
// 'C' keybind — entering DirtyCleanupConfirmPhase
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirm_CKeyEntersPhase verifies that pressing 'C' in
// BrowsingPhase when dirty hosts exist transitions to DirtyCleanupConfirmPhase.
func TestDirtyCleanupConfirm_CKeyEntersPhase(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")

	m, _ = sendKey(m, "C")

	if _, ok := m.state.Phase.(DirtyCleanupConfirmPhase); !ok {
		t.Errorf("pressing 'C' with dirty hosts should enter DirtyCleanupConfirmPhase; got %T",
			m.state.Phase)
	}
}

// TestDirtyCleanupConfirm_CKeyNoop_WhenNoDirtyHosts verifies that pressing 'C'
// in BrowsingPhase when no dirty hosts are tracked is a no-op (stays in
// BrowsingPhase).
func TestDirtyCleanupConfirm_CKeyNoop_WhenNoDirtyHosts(t *testing.T) {
	cfg := dirtyConfig()
	m := withWindowSize(New(cfg, WithDirtyHosts(map[string]bool{})), 80, 24)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("pre-condition: expected BrowsingPhase; got %T", m.state.Phase)
	}

	m, _ = sendKey(m, "C")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing 'C' with no dirty hosts should remain in BrowsingPhase; got %T",
			m.state.Phase)
	}
}

// TestDirtyCleanupConfirm_TargetsAllDirtyHosts_WhenNoneSelected verifies that
// when the user has not selected any hosts in the TUI, pressing 'C' targets
// all dirty hosts.
func TestDirtyCleanupConfirm_TargetsAllDirtyHosts_WhenNoneSelected(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01", "host-02")
	// No hosts selected — pressing 'C' should target all dirty hosts.
	m, _ = sendKey(m, "C")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase; got %T", m.state.Phase)
	}
	if len(phase.Hosts) != 2 {
		t.Errorf("expected 2 target hosts (all dirty); got %d: %v", len(phase.Hosts), phase.Hosts)
	}
}

// TestDirtyCleanupConfirm_TargetsSelectedDirtyHosts verifies that when the
// user has selected a dirty host with Space, pressing 'C' targets only the
// selected dirty hosts (not all dirty hosts).
func TestDirtyCleanupConfirm_TargetsSelectedDirtyHosts(t *testing.T) {
	cfg := dirtyConfig()
	// Both host-01 and host-02 are dirty.
	ds := dirtyStateWithHosts("host-01", "host-02")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	// Acknowledge startup warning.
	m, _ = sendKey(m, "y")
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("expected BrowsingPhase; got %T", m.state.Phase)
	}

	// Navigate to and select host-01 (first host after cluster header).
	// The flat list is: [cluster header, host-01, host-02]
	// Cursor starts at 0 (cluster header); move down once to host-01.
	m, _ = sendKey(m, "down")
	m, _ = sendKey(m, " ") // select host-01

	m, _ = sendKey(m, "C")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase; got %T", m.state.Phase)
	}
	if len(phase.Hosts) != 1 {
		t.Errorf("expected 1 target host (only selected dirty); got %d: %v",
			len(phase.Hosts), phase.Hosts)
	}
	if phase.Hosts[0].Host != "host-01" {
		t.Errorf("expected target host to be 'host-01'; got %q", phase.Hosts[0].Host)
	}
}

// TestDirtyCleanupConfirm_FallsBackToAllDirty_WhenSelectedHostsAreNotDirty
// verifies that if the user selects hosts that are NOT dirty, pressing 'C'
// falls back to targeting all dirty hosts.
func TestDirtyCleanupConfirm_FallsBackToAllDirty_WhenSelectedHostsAreNotDirty(t *testing.T) {
	cfg := dirtyConfig()
	// Only host-01 is dirty; host-02 is clean.
	ds := dirtyStateWithHosts("host-01")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("expected BrowsingPhase; got %T", m.state.Phase)
	}

	// Navigate to and select host-02 (second host; not dirty).
	m, _ = sendKey(m, "down") // to host-01
	m, _ = sendKey(m, "down") // to host-02
	m, _ = sendKey(m, " ")    // select host-02 (not dirty)

	// Pressing 'C': selected host-02 is not dirty, so fall back to all dirty.
	m, _ = sendKey(m, "C")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase; got %T", m.state.Phase)
	}
	// Should target host-01 (the only dirty host).
	if len(phase.Hosts) != 1 {
		t.Errorf("expected 1 target host (all dirty hosts); got %d: %v",
			len(phase.Hosts), phase.Hosts)
	}
	if phase.Hosts[0].Host != "host-01" {
		t.Errorf("expected target 'host-01'; got %q", phase.Hosts[0].Host)
	}
}

// ---------------------------------------------------------------------------
// DirtyCleanupConfirmPhase view — security warning
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirmView_ContainsSecurityWarning verifies that the
// confirmation dialog view includes a clearly visible security risk warning.
func TestDirtyCleanupConfirmView_ContainsSecurityWarning(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "10.0.0.1")
	m, _ = sendKey(m, "C")

	view := m.View()
	securityKeywords := []string{
		"SECURITY",
		"risk",
	}
	for _, kw := range securityKeywords {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(kw)) {
			t.Errorf("confirmation view should contain security warning keyword %q; view:\n%s",
				kw, view)
		}
	}
}

// TestDirtyCleanupConfirmView_ListsDirtyHosts verifies that all target dirty
// hosts are listed in the confirmation dialog view.
func TestDirtyCleanupConfirmView_ListsDirtyHosts(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "10.0.0.1", "10.0.0.2")
	m, _ = sendKey(m, "C")

	view := m.View()
	for _, host := range []string{"10.0.0.1", "10.0.0.2"} {
		if !strings.Contains(view, host) {
			t.Errorf("confirmation view should list target host %q; view:\n%s", host, view)
		}
	}
}

// TestDirtyCleanupConfirmView_ShowsConfirmHint verifies that the view shows
// the 'y' / Enter confirm hint.
func TestDirtyCleanupConfirmView_ShowsConfirmHint(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	view := m.View()
	if !strings.Contains(view, "y") && !strings.Contains(view, "Enter") {
		t.Errorf("confirmation view should show a confirm hint (y / Enter); view:\n%s", view)
	}
}

// TestDirtyCleanupConfirmView_ShowsKeyComment verifies that the key comment
// from the dirty record is shown in the dialog so the user can identify the key.
func TestDirtyCleanupConfirmView_ShowsKeyComment(t *testing.T) {
	cfg := dirtyConfig()
	ds := &dirtystate.State{}
	ds.Add(dirtystate.DirtyHost{
		Host:       "10.0.0.5",
		KeyComment: "smux-distribute-unique-abc123",
		AddedAt:    time.Now(),
	})
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning

	m, _ = sendKey(m, "C")

	view := m.View()
	if !strings.Contains(view, "smux-distribute-unique-abc123") {
		t.Errorf("confirmation view should show key comment 'smux-distribute-unique-abc123'; view:\n%s", view)
	}
}

// TestDirtyCleanupConfirmView_ShowsHubKeyDir verifies that hub-type dirty
// records display the hub key dir path in the dialog.
func TestDirtyCleanupConfirmView_ShowsHubKeyDir(t *testing.T) {
	cfg := dirtyConfig()
	ds := &dirtystate.State{}
	ds.Add(dirtystate.DirtyHost{
		Host:       "hub.example.com",
		KeyComment: "smux-distribute-hubtest",
		HubKeyDir:  "/tmp/smux-hub-dir-xyz",
		AddedAt:    time.Now(),
	})
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y")

	m, _ = sendKey(m, "C")

	view := m.View()
	if !strings.Contains(view, "/tmp/smux-hub-dir-xyz") {
		t.Errorf("confirmation view should show hub key dir '/tmp/smux-hub-dir-xyz'; view:\n%s", view)
	}
}

// TestDirtyCleanupConfirmView_CleaningState verifies that once the user presses
// 'y', the view switches to a "Cleaning up…" message.
func TestDirtyCleanupConfirmView_CleaningState(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "10.0.0.1")
	m, _ = sendKey(m, "C")
	// Confirm — this sets Cleaning=true and returns a background cmd.
	m, _ = sendKey(m, "y")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase after 'y'; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("expected Cleaning=true after pressing 'y'")
	}

	view := m.View()
	// View should now show a progress message indicating cleanup is in progress.
	// The view uses "Removing" to describe the operation in progress.
	progressPhrases := []string{"Removing", "Cleaning", "clean", "progress", "keys"}
	found := false
	for _, ph := range progressPhrases {
		if strings.Contains(view, ph) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("view during cleanup should contain a cleanup progress message; view:\n%s", view)
	}
}

// TestDirtyCleanupConfirmView_CleaningIgnoresKeys verifies that key presses
// are ignored while Cleaning=true.
func TestDirtyCleanupConfirmView_CleaningIgnoresKeys(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "10.0.0.1")
	m, _ = sendKey(m, "C")
	m, _ = sendKey(m, "y") // sets Cleaning=true

	// Further key presses should be ignored.
	m2, _ := sendKey(m, "n")
	if _, ok := m2.state.Phase.(DirtyCleanupConfirmPhase); !ok {
		t.Error("'n' should be ignored while Cleaning=true; phase should remain DirtyCleanupConfirmPhase")
	}
	if _, ok := m2.state.Phase.(BrowsingPhase); ok {
		t.Error("'n' should NOT transition to BrowsingPhase while Cleaning=true")
	}
}

// ---------------------------------------------------------------------------
// Cancel path — n / Esc
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirm_CancelWithN verifies that pressing 'n' in the
// confirmation dialog returns to BrowsingPhase without starting cleanup.
func TestDirtyCleanupConfirm_CancelWithN(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	if _, ok := m.state.Phase.(DirtyCleanupConfirmPhase); !ok {
		t.Fatal("precondition: expected DirtyCleanupConfirmPhase")
	}

	m, _ = sendKey(m, "n")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing 'n' should return to BrowsingPhase; got %T", m.state.Phase)
	}
}

// TestDirtyCleanupConfirm_CancelWithEsc verifies that pressing Esc in the
// confirmation dialog returns to BrowsingPhase.
func TestDirtyCleanupConfirm_CancelWithEsc(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m, _ = sendKey(m, "esc")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing Esc should return to BrowsingPhase; got %T", m.state.Phase)
	}
}

// TestDirtyCleanupConfirm_CancelWithUpperN verifies that 'N' (Shift+N) also
// cancels the dialog.
func TestDirtyCleanupConfirm_CancelWithUpperN(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m, _ = sendKey(m, "N")

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("pressing 'N' should return to BrowsingPhase; got %T", m.state.Phase)
	}
}

// ---------------------------------------------------------------------------
// Quit path — q / Ctrl+C
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirm_QuitWithQ verifies that pressing 'q' from the
// confirmation dialog quits smux.
func TestDirtyCleanupConfirm_QuitWithQ(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m2, cmd := sendKey(m, "q")

	if !m2.Done() {
		t.Error("pressing 'q' from confirmation dialog should mark the model as done")
	}
	if !m2.GetResult().Quit {
		t.Error("pressing 'q' should set Result.Quit = true")
	}
	if !isQuitCmd(cmd) {
		t.Error("pressing 'q' should return tea.Quit command")
	}
}

// TestDirtyCleanupConfirm_QuitWithCtrlC verifies that pressing Ctrl+C from
// the confirmation dialog quits smux.
func TestDirtyCleanupConfirm_QuitWithCtrlC(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m2, cmd := sendKey(m, "ctrl+c")

	if !m2.Done() {
		t.Error("Ctrl+C from confirmation dialog should mark the model as done")
	}
	if !isQuitCmd(cmd) {
		t.Error("Ctrl+C should return tea.Quit command")
	}
}

// ---------------------------------------------------------------------------
// Confirm path — y / Enter
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirm_ConfirmWithY_SetsCleaningFlag verifies that pressing
// 'y' in the confirmation dialog sets Cleaning=true on the phase and returns
// a non-nil tea.Cmd (the background cleanup command).
func TestDirtyCleanupConfirm_ConfirmWithY_SetsCleaningFlag(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m, cmd := sendKey(m, "y")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase after 'y'; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("pressing 'y' should set DirtyCleanupConfirmPhase.Cleaning = true")
	}
	if cmd == nil {
		t.Error("pressing 'y' should return a non-nil background cleanup command")
	}
}

// TestDirtyCleanupConfirm_ConfirmWithEnter_SetsCleaningFlag verifies that
// pressing Enter also confirms and starts cleanup.
func TestDirtyCleanupConfirm_ConfirmWithEnter_SetsCleaningFlag(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m, cmd := sendKey(m, "enter")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase after Enter; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("pressing Enter should set DirtyCleanupConfirmPhase.Cleaning = true")
	}
	if cmd == nil {
		t.Error("pressing Enter should return a non-nil background cleanup command")
	}
}

// TestDirtyCleanupConfirm_ConfirmWithUpperY_SetsCleaningFlag verifies that
// 'Y' (Shift+Y) also confirms.
func TestDirtyCleanupConfirm_ConfirmWithUpperY_SetsCleaningFlag(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")

	m, cmd := sendKey(m, "Y")

	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("expected DirtyCleanupConfirmPhase after 'Y'; got %T", m.state.Phase)
	}
	if !phase.Cleaning {
		t.Error("pressing 'Y' should set DirtyCleanupConfirmPhase.Cleaning = true")
	}
	if cmd == nil {
		t.Error("pressing 'Y' should return a non-nil background cleanup command")
	}
}

// ---------------------------------------------------------------------------
// Cleanup complete → BrowsingPhase transition
// ---------------------------------------------------------------------------

// TestDirtyCleanupConfirm_CleanupComplete_TransitionsToBrowsing verifies that
// when dirtyCleanupCompleteMsg is received while in DirtyCleanupConfirmPhase,
// the model transitions to BrowsingPhase.
func TestDirtyCleanupConfirm_CleanupComplete_TransitionsToBrowsing(t *testing.T) {
	m := modelInBrowsingWithDirtyHosts(t, "host-01")
	m, _ = sendKey(m, "C")
	m, _ = sendKey(m, "y") // enter Cleaning state

	if _, ok := m.state.Phase.(DirtyCleanupConfirmPhase); !ok {
		t.Fatal("precondition: expected DirtyCleanupConfirmPhase")
	}

	// Deliver the cleanup complete message.
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("after cleanup complete, expected BrowsingPhase; got %T", m.state.Phase)
	}
}

// ---------------------------------------------------------------------------
// Status bar — cleanup keybind hint
// ---------------------------------------------------------------------------

// TestStatusBarShowsCleanupHint_WhenDirtyHostsExist verifies that the status
// bar includes a hint for the 'C' cleanup keybind when dirty hosts exist.
func TestStatusBarShowsCleanupHint_WhenDirtyHostsExist(t *testing.T) {
	cfg := dirtyConfig()
	dirtySet := map[string]bool{"host-01": true}
	m := withWindowSize(New(cfg, WithDirtyHosts(dirtySet)), 80, 24)

	view := m.View()
	if !strings.Contains(view, "C") || !strings.Contains(strings.ToLower(view), "cleanup") {
		t.Errorf("status bar should hint at 'C cleanup' when dirty hosts exist; view:\n%s", view)
	}
}

// TestStatusBarNoCleanupHint_WhenNoDirtyHosts verifies that the status bar
// does NOT include a cleanup hint when there are no dirty hosts.
func TestStatusBarNoCleanupHint_WhenNoDirtyHosts(t *testing.T) {
	cfg := dirtyConfig()
	m := withWindowSize(New(cfg, WithDirtyHosts(map[string]bool{})), 80, 24)

	view := m.View()
	if strings.Contains(strings.ToLower(view), "cleanup") {
		t.Errorf("status bar should not show cleanup hint when no dirty hosts; view:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// ValidTransition coverage for DirtyCleanupConfirmPhase
// ---------------------------------------------------------------------------

// TestValidTransition_BrowsingToDirtyCleanupConfirm verifies that the
// BrowsingPhase → DirtyCleanupConfirmPhase edge is legal.
func TestValidTransition_BrowsingToDirtyCleanupConfirm(t *testing.T) {
	if !ValidTransition(BrowsingPhase{}, DirtyCleanupConfirmPhase{}) {
		t.Error("BrowsingPhase → DirtyCleanupConfirmPhase should be a valid transition")
	}
}

// TestValidTransition_DirtyCleanupConfirmToBrowsing verifies that the
// DirtyCleanupConfirmPhase → BrowsingPhase edge (cancel) is legal.
func TestValidTransition_DirtyCleanupConfirmToBrowsing(t *testing.T) {
	if !ValidTransition(DirtyCleanupConfirmPhase{}, BrowsingPhase{}) {
		t.Error("DirtyCleanupConfirmPhase → BrowsingPhase should be a valid transition")
	}
}

// TestValidTransition_DirtyCleanupConfirmToSelf verifies that the
// DirtyCleanupConfirmPhase → DirtyCleanupConfirmPhase edge (Cleaning=true) is legal.
func TestValidTransition_DirtyCleanupConfirmToSelf(t *testing.T) {
	if !ValidTransition(DirtyCleanupConfirmPhase{}, DirtyCleanupConfirmPhase{Cleaning: true}) {
		t.Error("DirtyCleanupConfirmPhase → DirtyCleanupConfirmPhase (Cleaning) should be valid")
	}
}

// TestValidTransition_DirtyCleanupConfirm_NoOtherEdges verifies that
// DirtyCleanupConfirmPhase cannot transition to unrelated phases.
func TestValidTransition_DirtyCleanupConfirm_NoOtherEdges(t *testing.T) {
	src := DirtyCleanupConfirmPhase{}
	invalidDests := []Phase{
		SelectingPhase{},
		ConfirmingPhase{},
		LaunchingPhase{},
		QuitConfirmingPhase{},
		DirtyStateWarningPhase{},
		QuitDirtyWarningPhase{},
	}
	for _, dst := range invalidDests {
		if ValidTransition(src, dst) {
			t.Errorf("DirtyCleanupConfirmPhase → %T should NOT be a valid transition", dst)
		}
	}
}
