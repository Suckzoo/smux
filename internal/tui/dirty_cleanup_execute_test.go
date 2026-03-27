// Tests for Sub-AC 3: Execute the cleanup action on confirmed hosts and
// reflect the updated dirty-state in the TUI after cleanup completes or fails.
//
// The cleanup action is triggered via the DirtyCleanupConfirmPhase dialog
// ('C' in BrowsingPhase → confirm with 'y'). Once the background goroutine
// finishes, it delivers dirtyCleanupCompleteMsg to the BubbleTea event loop.
//
// After receiving dirtyCleanupCompleteMsg the model must:
//  1. Reload the dirty state from ~/.smux/dirty-state.json (which reflects
//     whatever the cleanup goroutine managed to remove).
//  2. Update m.dirtyHosts and m.dirtyFullState to match the new on-disk state.
//  3. Transition to BrowsingPhase so the user can continue working.
//  4. Reflect the updated state in the TUI:
//     - ⚠ inventory markers shown only for hosts still on disk.
//     - Status bar cleanup hint shown only when dirty hosts remain.
//
// These tests exercise all four scenarios:
//
//	A) All hosts cleaned successfully (empty disk state).
//	B) Partial success (some hosts cleaned, others remain on disk).
//	C) All cleanup calls failed (all hosts still on disk).
//	D) The cleanup goroutine's Save call failed (msg.err != nil).
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Suckzoo/smux/internal/dirtystate"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// tempHomeForCleanup redirects HOME (and USERPROFILE on Windows) to a fresh
// temp directory so dirtystate.Load / Save use an isolated path.
// Returns the temp home directory path.
func tempHomeForCleanup(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // Windows compatibility
	return tmpHome
}

// writeDirtyStateFile writes a dirty-state.json containing the given hosts
// inside the .smux directory under home.  Passing nil or an empty slice
// produces an explicit "all clear" file (empty hosts array) so that
// dirtystate.Load returns an empty state instead of treating a missing
// file as a no-op.
func writeDirtyStateFile(t *testing.T, home string, hosts []dirtystate.DirtyHost) {
	t.Helper()
	smuxDir := filepath.Join(home, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir .smux: %v", err)
	}
	s := &dirtystate.State{}
	for _, h := range hosts {
		s.Add(h)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal dirty state: %v", err)
	}
	path := filepath.Join(smuxDir, "dirty-state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write dirty-state.json: %v", err)
	}
}

// newTestDirtyHost builds a DirtyHost with deterministic field values
// suitable for assertions.
func newTestDirtyHost(host, keyComment string) dirtystate.DirtyHost {
	return dirtystate.DirtyHost{
		Host:       host,
		KeyComment: keyComment,
		AddedAt:    time.Now(),
	}
}

// modelAtCleaningState builds a Model at the point where the user has
// confirmed cleanup (Cleaning=true in DirtyCleanupConfirmPhase).  This is the
// state the model is in while the background cleanup goroutine is running.
// It constructs the model using dirtyConfig() so the inventory contains
// host-01 and host-02.
func modelAtCleaningState(t *testing.T, ds *dirtystate.State) Model {
	t.Helper()
	cfg := dirtyConfig()
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning → BrowsingPhase
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Fatalf("precondition: expected BrowsingPhase; got %T", m.state.Phase)
	}
	m, _ = sendKey(m, "C") // open cleanup confirm dialog
	if _, ok := m.state.Phase.(DirtyCleanupConfirmPhase); !ok {
		t.Fatalf("precondition: expected DirtyCleanupConfirmPhase; got %T", m.state.Phase)
	}
	m, _ = sendKey(m, "y") // confirm → Cleaning=true
	if ph, ok := m.state.Phase.(DirtyCleanupConfirmPhase); !ok || !ph.Cleaning {
		t.Fatalf("precondition: expected Cleaning=true; got phase %T", m.state.Phase)
	}
	return m
}

// ---------------------------------------------------------------------------
// Scenario A: All hosts cleaned successfully (empty disk state)
// ---------------------------------------------------------------------------

// TestDirtyCleanupExecute_AllCleaned_TransitionsToBrowsing verifies that when
// the cleanup goroutine completes and the disk has no remaining dirty hosts,
// the model transitions to BrowsingPhase.
func TestDirtyCleanupExecute_AllCleaned_TransitionsToBrowsing(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil) // disk is empty after cleanup

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("expected BrowsingPhase after complete cleanup; got %T", m.state.Phase)
	}
}

// TestDirtyCleanupExecute_AllCleaned_ClearsInventoryWarnings verifies that
// after a complete cleanup the ⚠ glyphs disappear from the inventory view.
func TestDirtyCleanupExecute_AllCleaned_ClearsInventoryWarnings(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil) // empty disk state

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	// Verify ⚠ markers are present while cleanup is in progress.
	preLines := m.renderList()
	hasDirtyBefore := false
	for _, line := range preLines {
		if strings.Contains(line, "⚠") {
			hasDirtyBefore = true
			break
		}
	}
	if !hasDirtyBefore {
		t.Fatal("precondition: expected ⚠ markers while Cleaning=true")
	}

	// Deliver cleanup complete: disk is empty.
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	// No ⚠ markers should remain.
	lines := m.renderList()
	for _, line := range lines {
		if strings.Contains(line, "⚠") {
			t.Errorf("after complete cleanup, no ⚠ should appear; found: %q", line)
		}
	}
}

// TestDirtyCleanupExecute_AllCleaned_ClearsDirtyHostsMap verifies that
// m.dirtyHosts is empty after all hosts are cleaned.
func TestDirtyCleanupExecute_AllCleaned_ClearsDirtyHostsMap(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if len(m.dirtyHosts) != 0 {
		t.Errorf("dirtyHosts should be empty after complete cleanup; got %v", m.dirtyHosts)
	}
}

// TestDirtyCleanupExecute_AllCleaned_ClearsDirtyFullState verifies that
// m.dirtyFullState is empty (or has zero hosts) after a complete cleanup.
func TestDirtyCleanupExecute_AllCleaned_ClearsDirtyFullState(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if m.dirtyFullState != nil && !m.dirtyFullState.IsEmpty() {
		t.Errorf("dirtyFullState should be empty after complete cleanup; got %d host(s)",
			len(m.dirtyFullState.Hosts))
	}
}

// TestDirtyCleanupExecute_AllCleaned_StatusBarHintClears verifies that the
// status bar cleanup hint disappears after all dirty hosts are removed.
func TestDirtyCleanupExecute_AllCleaned_StatusBarHintClears(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	view := m.View()
	if strings.Contains(strings.ToLower(view), "cleanup") {
		t.Errorf("status bar should not show 'cleanup' hint after all hosts are cleaned; view:\n%s",
			view)
	}
}

// TestDirtyCleanupExecute_AllCleaned_CKeyBecomesNoop verifies that pressing
// 'C' after a complete cleanup is a no-op (no dirty hosts remain).
func TestDirtyCleanupExecute_AllCleaned_CKeyBecomesNoop(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	// 'C' with no dirty hosts should remain in BrowsingPhase.
	m, _ = sendKey(m, "C")
	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("'C' after complete cleanup should stay in BrowsingPhase; got %T",
			m.state.Phase)
	}
}

// ---------------------------------------------------------------------------
// Scenario B: Partial success — some hosts cleaned, others still on disk
// ---------------------------------------------------------------------------

// TestDirtyCleanupExecute_PartialSuccess_RemainingHostsStillDirty verifies
// that hosts still present on disk after partial cleanup are rendered with ⚠.
func TestDirtyCleanupExecute_PartialSuccess_RemainingHostsStillDirty(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// host-01 remains on disk; host-02 was successfully cleaned.
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	lines := m.renderList()

	// host-01 (still on disk) must still show ⚠.
	foundHost01Dirty := false
	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-01") {
			foundHost01Dirty = true
			break
		}
	}
	if !foundHost01Dirty {
		t.Error("host-01 should still show ⚠ because it remains on disk after partial cleanup")
	}

	// host-02 (cleaned from disk) must NOT show ⚠.
	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-02") {
			t.Errorf("host-02 should not show ⚠ because it was successfully cleaned; line: %q",
				line)
		}
	}
}

// TestDirtyCleanupExecute_PartialSuccess_UpdatesDirtyHostsMap verifies that
// m.dirtyHosts is updated to contain only the remaining dirty hosts.
func TestDirtyCleanupExecute_PartialSuccess_UpdatesDirtyHostsMap(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if !m.dirtyHosts["host-01"] {
		t.Error("dirtyHosts should contain 'host-01' after partial cleanup")
	}
	if m.dirtyHosts["host-02"] {
		t.Error("dirtyHosts should NOT contain 'host-02' after it was successfully cleaned")
	}
	if len(m.dirtyHosts) != 1 {
		t.Errorf("dirtyHosts should have exactly 1 entry; got %d: %v",
			len(m.dirtyHosts), m.dirtyHosts)
	}
}

// TestDirtyCleanupExecute_PartialSuccess_UpdatesDirtyFullState verifies that
// m.dirtyFullState contains only the remaining dirty host records.
func TestDirtyCleanupExecute_PartialSuccess_UpdatesDirtyFullState(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if m.dirtyFullState == nil {
		t.Fatal("dirtyFullState should not be nil after partial cleanup")
	}
	if len(m.dirtyFullState.Hosts) != 1 {
		t.Errorf("dirtyFullState should have 1 remaining host; got %d",
			len(m.dirtyFullState.Hosts))
	}
	if m.dirtyFullState.Hosts[0].Host != "host-01" {
		t.Errorf("remaining dirty host should be 'host-01'; got %q",
			m.dirtyFullState.Hosts[0].Host)
	}
}

// TestDirtyCleanupExecute_PartialSuccess_StatusBarRetainsHint verifies that
// the status bar still shows the cleanup hint when dirty hosts remain.
func TestDirtyCleanupExecute_PartialSuccess_StatusBarRetainsHint(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(strings.ToLower(view), "cleanup") {
		t.Errorf("status bar should retain 'cleanup' hint when host-01 still needs cleanup; view:\n%s",
			view)
	}
}

// TestDirtyCleanupExecute_PartialSuccess_CKeyTargetsRemainingHosts verifies
// that pressing 'C' after partial cleanup targets only the remaining dirty hosts.
func TestDirtyCleanupExecute_PartialSuccess_CKeyTargetsRemainingHosts(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	// Press 'C' again — should target only remaining host-01.
	m, _ = sendKey(m, "C")
	phase, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	if !ok {
		t.Fatalf("'C' after partial cleanup should enter DirtyCleanupConfirmPhase; got %T",
			m.state.Phase)
	}
	if len(phase.Hosts) != 1 {
		t.Errorf("should target 1 remaining dirty host; got %d: %v",
			len(phase.Hosts), phase.Hosts)
	}
	if phase.Hosts[0].Host != "host-01" {
		t.Errorf("target host should be 'host-01'; got %q", phase.Hosts[0].Host)
	}
}

// ---------------------------------------------------------------------------
// Scenario C: All cleanup calls failed — all hosts still on disk
// ---------------------------------------------------------------------------

// TestDirtyCleanupExecute_AllFailed_AllHostsStillShownDirty verifies that
// when cleanup fails for all hosts (disk still has every dirty host), the TUI
// continues to show ⚠ for all of them.
func TestDirtyCleanupExecute_AllFailed_AllHostsStillShownDirty(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// All hosts remain on disk (cleanup failed for all).
	allHosts := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
		newTestDirtyHost("host-02", "smux-distribute-host02"),
	}
	writeDirtyStateFile(t, tmpHome, allHosts)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	lines := m.renderList()
	for _, hostName := range []string{"host-01", "host-02"} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, "⚠") && strings.Contains(line, hostName) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("after failed cleanup, %s should still show ⚠ (disk still has it)",
				hostName)
		}
	}
}

// TestDirtyCleanupExecute_AllFailed_DirtyHostsMapPreservesAllHosts verifies
// that m.dirtyHosts still contains all hosts when cleanup failed for all.
func TestDirtyCleanupExecute_AllFailed_DirtyHostsMapPreservesAllHosts(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	allHosts := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
		newTestDirtyHost("host-02", "smux-distribute-host02"),
	}
	writeDirtyStateFile(t, tmpHome, allHosts)

	ds := dirtyStateWithHosts("host-01", "host-02")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	for _, hostName := range []string{"host-01", "host-02"} {
		if !m.dirtyHosts[hostName] {
			t.Errorf("dirtyHosts should still contain %q after failed cleanup", hostName)
		}
	}
	if len(m.dirtyHosts) != 2 {
		t.Errorf("dirtyHosts should have 2 entries when all cleanup failed; got %d",
			len(m.dirtyHosts))
	}
}

// TestDirtyCleanupExecute_AllFailed_StatusBarRetainsHint verifies that the
// status bar cleanup hint is still present when all cleanup calls failed.
func TestDirtyCleanupExecute_AllFailed_StatusBarRetainsHint(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	allHosts := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, allHosts)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(strings.ToLower(view), "cleanup") {
		t.Errorf("status bar should retain cleanup hint when all cleanup calls failed; view:\n%s",
			view)
	}
}

// TestDirtyCleanupExecute_AllFailed_TransitionsToBrowsing verifies that even
// when all cleanup calls fail the model still transitions to BrowsingPhase.
func TestDirtyCleanupExecute_AllFailed_TransitionsToBrowsing(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	allHosts := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, allHosts)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("should transition to BrowsingPhase even when cleanup fails; got %T",
			m.state.Phase)
	}
}

// ---------------------------------------------------------------------------
// Scenario D: Save error — msg.err is non-nil (disk write failed)
// ---------------------------------------------------------------------------

// TestDirtyCleanupExecute_SaveError_TransitionsToBrowsing verifies that even
// when the cleanup goroutine could not save the updated dirty state (msg.err
// non-nil), the model still transitions to BrowsingPhase.
func TestDirtyCleanupExecute_SaveError_TransitionsToBrowsing(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// Write empty state; the model ignores msg.err and just reloads from disk.
	writeDirtyStateFile(t, tmpHome, nil)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	// Deliver cleanup complete with a non-nil error (simulating a disk-full
	// or permissions error that prevented saving the updated dirty state).
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: fmt.Errorf("disk full")})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("should transition to BrowsingPhase even with a save error; got %T",
			m.state.Phase)
	}
}

// TestDirtyCleanupExecute_SaveError_DirtyStateReflectsDisk verifies that when
// the cleanup goroutine reports a save error, the model still reloads from disk
// and reflects whatever state is currently there (stale-but-safe approach).
func TestDirtyCleanupExecute_SaveError_DirtyStateReflectsDisk(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// Disk still has host-01 (save of the cleaned state failed, stale data remains).
	oldState := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, oldState)

	ds := dirtyStateWithHosts("host-01")
	m := modelAtCleaningState(t, ds)

	// msg.err signals that the save failed; disk has old stale data.
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: fmt.Errorf("save failed")})
	m = updated.(Model)

	// Model should reflect the stale-but-safe disk state: host-01 is still dirty.
	if !m.dirtyHosts["host-01"] {
		t.Error("with stale disk state, dirtyHosts should still contain 'host-01'")
	}

	lines := m.renderList()
	foundDirty := false
	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-01") {
			foundDirty = true
			break
		}
	}
	if !foundDirty {
		t.Error("with stale disk state, host-01 should still show ⚠ after save error")
	}
}

// ---------------------------------------------------------------------------
// Startup warning cleanup path — same dirtyCleanupCompleteMsg handler
// ---------------------------------------------------------------------------

// TestDirtyStartupCleanup_AllCleaned_ClearsInventoryWarnings verifies that
// the startup dirty-state warning dialog ('c' to clean) also clears the TUI
// markers when cleanup completes successfully.  Both the startup warning path
// and the on-demand 'C' path converge on the same dirtyCleanupCompleteMsg
// handler, so this is a regression guard.
func TestDirtyStartupCleanup_AllCleaned_ClearsInventoryWarnings(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil) // disk is empty after cleanup

	cfg := dirtyConfig()
	ds := dirtyStateWithHosts("host-01")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	// In DirtyStateWarningPhase — press 'c' to trigger cleanup.
	if _, ok := m.state.Phase.(DirtyStateWarningPhase); !ok {
		t.Fatalf("expected DirtyStateWarningPhase at startup; got %T", m.state.Phase)
	}
	m, _ = sendKey(m, "c")
	if ph, ok := m.state.Phase.(DirtyStateWarningPhase); !ok || !ph.Cleaning {
		t.Fatalf("precondition: expected DirtyStateWarningPhase with Cleaning=true; got %T",
			m.state.Phase)
	}

	// Deliver cleanup complete.
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if _, ok := m.state.Phase.(BrowsingPhase); !ok {
		t.Errorf("expected BrowsingPhase after startup cleanup; got %T", m.state.Phase)
	}

	lines := m.renderList()
	for _, line := range lines {
		if strings.Contains(line, "⚠") {
			t.Errorf("after startup cleanup, no ⚠ should appear; found: %q", line)
		}
	}
}

// TestDirtyStartupCleanup_PartialSuccess_RemainingHostsStillDirty verifies
// that partial cleanup results from the startup warning path are correctly
// reflected in the inventory view.
func TestDirtyStartupCleanup_PartialSuccess_RemainingHostsStillDirty(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// host-01 remains; host-02 was cleaned.
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	cfg := dirtyConfig()
	ds := dirtyStateWithHosts("host-01", "host-02")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)

	m, _ = sendKey(m, "c") // trigger cleanup from startup warning
	updated, _ := m.Update(dirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	lines := m.renderList()

	foundHost01Dirty := false
	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-01") {
			foundHost01Dirty = true
			break
		}
	}
	if !foundHost01Dirty {
		t.Error("host-01 should still show ⚠ after partial startup cleanup")
	}

	for _, line := range lines {
		if strings.Contains(line, "⚠") && strings.Contains(line, "host-02") {
			t.Errorf("host-02 should not show ⚠ after being successfully cleaned; line: %q",
				line)
		}
	}
}

// ---------------------------------------------------------------------------
// Quit-path cleanup — quitDirtyCleanupCompleteMsg
// ---------------------------------------------------------------------------

// TestQuitDirtyCleanup_AllCleaned_QuitsAfterCleanup verifies that when
// quitDirtyCleanupCompleteMsg arrives (with all hosts cleaned on disk) the
// model sets done=true and triggers tea.Quit.
func TestQuitDirtyCleanup_AllCleaned_QuitsAfterCleanup(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	writeDirtyStateFile(t, tmpHome, nil) // empty disk state

	cfg := dirtyConfig()
	ds := dirtyStateWithHosts("host-01")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning → BrowsingPhase
	m, _ = sendKey(m, "q") // → QuitDirtyWarningPhase (dirty hosts exist)

	if _, ok := m.state.Phase.(QuitDirtyWarningPhase); !ok {
		t.Fatalf("expected QuitDirtyWarningPhase; got %T", m.state.Phase)
	}

	m, _ = sendKey(m, "c") // trigger cleanup-then-quit → Cleaning=true
	if ph, ok := m.state.Phase.(QuitDirtyWarningPhase); !ok || !ph.Cleaning {
		t.Fatalf("expected QuitDirtyWarningPhase with Cleaning=true; got %T", m.state.Phase)
	}

	// Deliver cleanup complete for the quit path.
	updated, cmd := m.Update(quitDirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	if !m.Done() {
		t.Error("model should be done after quit-path cleanup complete")
	}
	if !m.GetResult().Quit {
		t.Error("result should have Quit=true after quit-path cleanup complete")
	}
	if !isQuitCmd(cmd) {
		t.Error("should return tea.Quit after quit-path cleanup complete")
	}
}

// TestQuitDirtyCleanup_PartialSuccess_DirtyStateRefreshedBeforeQuit verifies
// that quitDirtyCleanupCompleteMsg refreshes m.dirtyHosts and m.dirtyFullState
// from disk before the model exits, so any subsequent inspection of the model
// reflects the actual outcome.
func TestQuitDirtyCleanup_PartialSuccess_DirtyStateRefreshedBeforeQuit(t *testing.T) {
	tmpHome := tempHomeForCleanup(t)
	// host-01 still on disk; host-02 was cleaned.
	remaining := []dirtystate.DirtyHost{
		newTestDirtyHost("host-01", "smux-distribute-host01"),
	}
	writeDirtyStateFile(t, tmpHome, remaining)

	cfg := dirtyConfig()
	ds := dirtyStateWithHosts("host-01", "host-02")
	m := withWindowSize(New(cfg, WithDirtyState(ds)), 80, 24)
	m, _ = sendKey(m, "y") // acknowledge startup warning
	m, _ = sendKey(m, "q") // → QuitDirtyWarningPhase
	m, _ = sendKey(m, "c") // trigger cleanup-then-quit

	updated, _ := m.Update(quitDirtyCleanupCompleteMsg{err: nil})
	m = updated.(Model)

	// Model is done, but dirtyHosts/dirtyFullState should reflect disk state.
	if !m.dirtyHosts["host-01"] {
		t.Error("dirtyHosts should contain 'host-01' (still on disk) after partial quit cleanup")
	}
	if m.dirtyHosts["host-02"] {
		t.Error("dirtyHosts should NOT contain 'host-02' (cleaned from disk)")
	}
}
