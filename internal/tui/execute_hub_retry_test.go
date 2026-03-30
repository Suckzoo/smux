// Tests for AC 3: hub-first ordering in hub-and-spoke retry execution.
//
// These tests verify that startExecution() routes hub-and-spoke retry
// operations through runHubSpokeRetryWithProgress (which enforces hub-first
// ordering) rather than the normal runHubSpokeWithProgress path.
//
// The unit tests for runHubSpokeRetryWithProgress itself live in
// hub_spoke_retry_test.go (AC 4 coverage).  This file focuses on the
// integration path: DistributeModel.startExecution → runHubSpokeRetryWithProgress.
//
// It also provides shared test helpers used by hub_spoke_retry_test.go.
package tui

import (
	"os/exec"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// ---------------------------------------------------------------------------
// Shared test helpers (used by hub_spoke_retry_test.go and this file)
// ---------------------------------------------------------------------------

// fakeTempKP returns a minimal *sshkeys.TempKeyPair for tests that do not need
// real key material.  Tests that use injectFakeBinaries work correctly with it
// since the fake binaries ignore key paths entirely.
func fakeTempKP() *sshkeys.TempKeyPair {
	return &sshkeys.TempKeyPair{
		PrivateKeyPath: "/nonexistent/id_ed25519",
		PublicKey:      "ssh-ed25519 AAAA fakekey",
		Comment:        "smux-distribute-test",
	}
}

// collectProgressUpdates drains ch until it is closed and returns all updates
// in the order they were received.
func collectProgressUpdates(ch <-chan executor.ProgressUpdate) []executor.ProgressUpdate {
	var updates []executor.ProgressUpdate
	for u := range ch {
		updates = append(updates, u)
	}
	return updates
}

// statusesFor returns the TransferStatus values seen for the given host alias
// in the order they appeared in updates.
func statusesFor(host string, updates []executor.ProgressUpdate) []executor.TransferStatus {
	var out []executor.TransferStatus
	for _, u := range updates {
		if u.Host.Host == host {
			out = append(out, u.Status)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Integration: startExecution routes hub-and-spoke retries through the
// correct path based on retryParams.
// ---------------------------------------------------------------------------

// TestStartExecution_HubSpokeRetry_HubSucceeded_HubNotInProgressCh verifies
// that when a hub-and-spoke retry has only spokes in FailedHosts (hub
// succeeded previously), no progress updates are emitted for the hub.
// The hub push is skipped; only failed spokes are retried via fan-out.
func TestStartExecution_HubSpokeRetry_HubSucceeded_HubNotInProgressCh(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found")
	}
	// All SSH/SCP calls fail; GenerateOnHub (ssh) also fails quickly.
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.integration-test", DisplayName: "hub"}
	spoke1 := config.ResolvedHost{Host: "spoke1.integration-test", DisplayName: "spoke1"}
	spoke2 := config.ResolvedHost{Host: "spoke2.integration-test", DisplayName: "spoke2"}

	// Hub succeeded in the original run; only spokes are in FailedHosts.
	retryP := executor.RetryParams{
		SourceHost:  config.ResolvedHost{},
		SourcePath:  "/tmp/file.txt",
		DestPath:    "/remote/file.txt",
		CopyMode:    "hub-spoke",
		FailedHosts: []config.ResolvedHost{spoke1, spoke2},
		AllHosts:    []config.ResolvedHost{hub, spoke1, spoke2},
	}

	m := DistributeModel{
		step:          DistributeStepExecute,
		sourcePaths:   []string{"/tmp/file.txt"},
		destHosts:     []config.ResolvedHost{spoke1, spoke2}, // only FailedHosts
		copyMode:      "hub-spoke",
		destPath:      "/remote/file.txt",
		retryParams:   &retryP,
		destHostItems: []config.ResolvedHost{hub, spoke1, spoke2},
	}

	m, _ = sendDistributeKey(m, "enter")

	// Collect all progress updates from the goroutine channel.
	updates := collectProgressUpdates(m.progressCh)

	// Hub must NOT appear in any progress update: push was skipped because
	// hub succeeded in the original run.
	hubUpdates := statusesFor("hub.integration-test", updates)
	if len(hubUpdates) > 0 {
		t.Errorf("hub should not appear in progress updates when hub succeeded; "+
			"got %d update(s): %v", len(hubUpdates), hubUpdates)
	}

	// Both failed spokes must have received at least one progress update
	// (the fan-out was attempted even if it failed due to fake SSH).
	for _, spoke := range []string{"spoke1.integration-test", "spoke2.integration-test"} {
		if len(statusesFor(spoke, updates)) == 0 {
			t.Errorf("spoke %s should appear in progress updates during retry", spoke)
		}
	}

	// Also verify hostProgress initialisation: hub must not be in the map
	// (only destHosts are tracked, and hub was not in destHosts).
	if _, ok := m.hostProgress["hub.integration-test"]; ok {
		t.Error("hub should not be in hostProgress (it was not in destHosts)")
	}
}

// TestStartExecution_HubSpokeRetry_HubFailed_HubReceivesUpdates verifies that
// when a hub-and-spoke retry has the hub in FailedHosts, the hub IS tracked in
// the progress channel (push is attempted; hub receives InProgress then
// Failed/Done).
func TestStartExecution_HubSpokeRetry_HubFailed_HubReceivesUpdates(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found")
	}
	// All SSH/SCP fail: hub push fails, spoke is blocked.
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.integration-test2", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.integration-test2", DisplayName: "spoke"}

	// Both hub and spoke failed in the original run.
	retryP := executor.RetryParams{
		SourceHost:  config.ResolvedHost{},
		SourcePath:  "/tmp/file.txt",
		DestPath:    "/remote/file.txt",
		CopyMode:    "hub-spoke",
		FailedHosts: []config.ResolvedHost{hub, spoke},
		AllHosts:    []config.ResolvedHost{hub, spoke},
	}

	m := DistributeModel{
		step:          DistributeStepExecute,
		sourcePaths:   []string{"/tmp/file.txt"},
		destHosts:     []config.ResolvedHost{hub, spoke},
		copyMode:      "hub-spoke",
		destPath:      "/remote/file.txt",
		retryParams:   &retryP,
		destHostItems: []config.ResolvedHost{hub, spoke},
	}

	m, _ = sendDistributeKey(m, "enter")
	updates := collectProgressUpdates(m.progressCh)

	// Hub must appear in progress updates (push was attempted).
	hubStatuses := statusesFor("hub.integration-test2", updates)
	if len(hubStatuses) == 0 {
		t.Fatal("hub should have received progress updates when hub is in FailedHosts")
	}

	// Hub's final status must be TransferFailed (scp exits 1).
	last := hubStatuses[len(hubStatuses)-1]
	if last != executor.TransferFailed {
		t.Errorf("hub: expected final status TransferFailed, got %v", last)
	}

	// Spoke must appear in progress updates (blocked update).
	spokeStatuses := statusesFor("spoke.integration-test2", updates)
	if len(spokeStatuses) == 0 {
		t.Fatal("spoke should have received progress updates after hub failure")
	}
	lastSpoke := spokeStatuses[len(spokeStatuses)-1]
	if lastSpoke != executor.TransferFailed {
		t.Errorf("spoke: expected final status TransferFailed (blocked by hub), got %v", lastSpoke)
	}
}

// TestStartExecution_HubSpokeNonRetry_UsesNormalPath verifies that a normal
// (non-retry) hub-and-spoke execution does NOT use the retry path even when
// copyMode is "hub-spoke".  When retryParams is nil, the standard
// runHubSpokeWithProgress is used, which treats dstHosts[0] as the hub.
func TestStartExecution_HubSpokeNonRetry_UsesNormalPath(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found")
	}
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.normal-test", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.normal-test", DisplayName: "spoke"}

	// No retryParams: normal (non-retry) execution.
	m := DistributeModel{
		step:          DistributeStepExecute,
		sourcePaths:   []string{"/tmp/file.txt"},
		destHosts:     []config.ResolvedHost{hub, spoke},
		copyMode:      "hub-spoke",
		destPath:      "/remote/file.txt",
		retryParams:   nil, // NOT a retry
		destHostItems: []config.ResolvedHost{hub, spoke},
	}

	m, _ = sendDistributeKey(m, "enter")
	updates := collectProgressUpdates(m.progressCh)

	// Hub must appear in progress updates (normal hub-spoke push attempted).
	if len(statusesFor("hub.normal-test", updates)) == 0 {
		t.Error("hub should appear in progress updates in normal hub-spoke execution")
	}
	// Spoke must appear in progress updates.
	if len(statusesFor("spoke.normal-test", updates)) == 0 {
		t.Error("spoke should appear in progress updates in normal hub-spoke execution")
	}
}

// TestStartExecution_HubSpokeRetry_HubOrderedBeforeSpokes verifies that when
// the hub is in FailedHosts, the hub's final status update appears before any
// spoke status update in the progress channel.  This confirms hub-first ordering
// is enforced at the channel level.
func TestStartExecution_HubSpokeRetry_HubOrderedBeforeSpokes(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found")
	}
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.order-test", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.order-test", DisplayName: "spoke"}

	retryP := executor.RetryParams{
		SourceHost:  config.ResolvedHost{},
		SourcePath:  "/tmp/file.txt",
		DestPath:    "/remote/file.txt",
		CopyMode:    "hub-spoke",
		FailedHosts: []config.ResolvedHost{hub, spoke},
		AllHosts:    []config.ResolvedHost{hub, spoke},
	}

	m := DistributeModel{
		step:          DistributeStepExecute,
		sourcePaths:   []string{"/tmp/file.txt"},
		destHosts:     []config.ResolvedHost{hub, spoke},
		copyMode:      "hub-spoke",
		destPath:      "/remote/file.txt",
		retryParams:   &retryP,
		destHostItems: []config.ResolvedHost{hub, spoke},
	}

	m, _ = sendDistributeKey(m, "enter")
	orderedUpdates := collectProgressUpdates(m.progressCh)

	// Find the hub's final (non-InProgress) update index.
	hubFinalIdx := -1
	for i, u := range orderedUpdates {
		if u.Host.Host == "hub.order-test" && u.Status != executor.TransferInProgress {
			hubFinalIdx = i
			break
		}
	}

	// Find the first spoke update index.
	spokeFirstIdx := -1
	for i, u := range orderedUpdates {
		if u.Host.Host == "spoke.order-test" {
			spokeFirstIdx = i
			break
		}
	}

	if hubFinalIdx < 0 || spokeFirstIdx < 0 {
		// If either is missing there may be no updates at all; skip ordering check.
		return
	}

	// Hub's final status must appear before spoke's first update (hub-first ordering).
	if hubFinalIdx > spokeFirstIdx {
		t.Errorf("hub-first ordering violated: hub final update at index %d, "+
			"but spoke first update at index %d (spoke appeared before hub finished)",
			hubFinalIdx, spokeFirstIdx)
	}
}
