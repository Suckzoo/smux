// Tests for AC 7: User can repeatedly retry until all hosts succeed or they
// manually quit.
//
// These tests verify that:
//  1. After a retry execution completes with failures, pressing 'r' creates
//     another retry model (the retry loop is unbounded).
//  2. AllHosts is correctly preserved from the original retryParams across
//     multiple retries so that hub-first ordering is maintained in hub-spoke mode.
//  3. Esc/q/n from any retry-confirm step correctly terminates the retry loop.
package tui

import (
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeRetryModelWithFailures creates a DistributeModel that simulates a
// completed retry execution where the given hosts have failed.  The model is
// placed at DistributeStepExecute with executeStarted=true, executeDone=true,
// and hostProgress pre-seeded with the provided statuses.
func makeRetryModelWithFailures(
	failedHosts []config.ResolvedHost,
	allHosts []config.ResolvedHost,
	copyMode string,
) DistributeModel {
	params := executor.RetryParams{
		SourceHost:  config.ResolvedHost{Host: "src.example.com"},
		SourcePath:  "/data/file.tar.gz",
		DestPath:    "/opt/file.tar.gz",
		CopyMode:    copyMode,
		FailedHosts: failedHosts,
		AllHosts:    allHosts,
	}
	m := NewRetryDistributeModel(minimalConfig(), 80, 24, params)

	// Advance past retry-confirm to the Execute step.
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepExecute {
		panic("expected Execute step after confirming retry")
	}

	// Simulate execution having completed: mark all failedHosts as failed.
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = make(map[string]executor.TransferStatus)
	for _, h := range failedHosts {
		m.hostProgress[h.Host] = executor.TransferFailed
	}

	return m
}

// ---------------------------------------------------------------------------
// AC 7: basic repeated retry loop
// ---------------------------------------------------------------------------

// TestRepeatedRetry_PressRAfterRetryCreatesAnotherRetryModel verifies that
// pressing 'r' after a retry execution that still has failures creates a new
// retry model at DistributeStepRetryConfirm, enabling the user to retry again.
func TestRepeatedRetry_PressRAfterRetryCreatesAnotherRetryModel(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
		{Host: "h2.example.com", DisplayName: "h2"},
	}
	all := append([]config.ResolvedHost{
		{Host: "hub.example.com", DisplayName: "hub"},
	}, failed...)

	m := makeRetryModelWithFailures(failed, all, "hub-spoke")

	// Press 'r': should return a new retry model.
	m2, _ := sendDistributeKey(m, "r")

	if m2.step != DistributeStepRetryConfirm {
		t.Errorf("expected DistributeStepRetryConfirm (%d) after pressing 'r' on retry model, got %d",
			DistributeStepRetryConfirm, m2.step)
	}
	if m2.retryParams == nil {
		t.Fatal("second retry model must have non-nil retryParams")
	}
}

// TestRepeatedRetry_SecondRetryFailedHostsMatchCurrentFailures verifies that
// the FailedHosts in the second retry's params are exactly the hosts that
// failed in the first retry's execution.
func TestRepeatedRetry_SecondRetryFailedHostsMatchCurrentFailures(t *testing.T) {
	h1 := config.ResolvedHost{Host: "h1.example.com", DisplayName: "h1"}
	h2 := config.ResolvedHost{Host: "h2.example.com", DisplayName: "h2"}
	failed := []config.ResolvedHost{h1, h2}
	all := []config.ResolvedHost{
		{Host: "hub.example.com", DisplayName: "hub"},
		h1, h2,
	}

	m := makeRetryModelWithFailures(failed, all, "parallel")

	m2, _ := sendDistributeKey(m, "r")

	if m2.retryParams == nil {
		t.Fatal("retryParams must not be nil")
	}
	if len(m2.retryParams.FailedHosts) != 2 {
		t.Errorf("expected 2 failed hosts in second retry params, got %d",
			len(m2.retryParams.FailedHosts))
	}
	found := make(map[string]bool)
	for _, h := range m2.retryParams.FailedHosts {
		found[h.Host] = true
	}
	if !found["h1.example.com"] {
		t.Error("h1.example.com should be in FailedHosts of second retry")
	}
	if !found["h2.example.com"] {
		t.Error("h2.example.com should be in FailedHosts of second retry")
	}
}

// TestRepeatedRetry_RKeyIsNoOpBeforeExecutionComplete verifies that pressing
// 'r' before execution is complete (executeDone = false) is a no-op: the model
// remains at DistributeStepExecute without creating a new retry.
func TestRepeatedRetry_RKeyIsNoOpBeforeExecutionComplete(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
	}
	all := []config.ResolvedHost{
		{Host: "hub.example.com", DisplayName: "hub"},
		failed[0],
	}
	m := makeRetryModelWithFailures(failed, all, "parallel")
	// Undo the "done" flag to simulate execution still in-progress.
	m.executeDone = false

	m2, _ := sendDistributeKey(m, "r")

	// Should remain at Execute step (no new retry-confirm was created).
	if m2.step != DistributeStepExecute {
		t.Errorf("expected DistributeStepExecute (%d) when pressing 'r' mid-execution, got %d",
			DistributeStepExecute, m2.step)
	}
	// retryParams should still be the original ones (from makeRetryModelWithFailures).
	if m2.retryParams == nil {
		t.Error("retryParams should still be set on the model")
	}
}

// TestRepeatedRetry_RKeyIsNoOpWhenNoFailures verifies that pressing 'r' after
// a retry where all hosts succeeded is a no-op.
func TestRepeatedRetry_RKeyIsNoOpWhenNoFailures(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
	}
	all := []config.ResolvedHost{failed[0]}
	m := makeRetryModelWithFailures(failed, all, "parallel")
	// Override: mark h1 as done (success) so there are no failures.
	m.hostProgress["h1.example.com"] = executor.TransferDone

	m2, _ := sendDistributeKey(m, "r")

	// Should remain at Execute step.
	if m2.step != DistributeStepExecute {
		t.Errorf("expected DistributeStepExecute when no failures remain, got step %d", m2.step)
	}
}

// ---------------------------------------------------------------------------
// AC 7: AllHosts preservation for hub-spoke across multiple retries
// ---------------------------------------------------------------------------

// TestRepeatedRetry_AllHostsPreservedFromOriginalRetryParams verifies that
// when pressing 'r' on a retry model (second retry), the AllHosts in the new
// RetryParams is taken from the current model's retryParams.AllHosts — not
// from m.destHosts.  This ensures the original hub remains at AllHosts[0]
// across multiple retries for hub-and-spoke mode.
func TestRepeatedRetry_AllHostsPreservedFromOriginalRetryParams(t *testing.T) {
	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	spoke1 := config.ResolvedHost{Host: "spoke1.example.com", DisplayName: "spoke1"}
	spoke2 := config.ResolvedHost{Host: "spoke2.example.com", DisplayName: "spoke2"}

	// Simulate the state after the first retry:
	//   - The first retry attempted [spoke1, spoke2] (hub had succeeded earlier).
	//   - AllHosts from the original operation is [hub, spoke1, spoke2].
	//   - In this retry execution, spoke1 succeeded but spoke2 still failed.
	failed := []config.ResolvedHost{spoke1, spoke2} // both were retried
	all := []config.ResolvedHost{hub, spoke1, spoke2}

	m := makeRetryModelWithFailures(failed, all, "hub-spoke")
	// Override: mark spoke1 as done, spoke2 as failed (partial success in retry).
	m.hostProgress["spoke1.example.com"] = executor.TransferDone
	m.hostProgress["spoke2.example.com"] = executor.TransferFailed

	// Press 'r' for the second retry.
	m2, _ := sendDistributeKey(m, "r")

	if m2.retryParams == nil {
		t.Fatal("second retry model must have non-nil retryParams")
	}

	// AllHosts in the second retry must be the ORIGINAL all-hosts list
	// [hub, spoke1, spoke2], NOT just [spoke1, spoke2] (the retry model's destHosts).
	if len(m2.retryParams.AllHosts) != 3 {
		t.Errorf("expected 3 AllHosts (original full list), got %d: %v",
			len(m2.retryParams.AllHosts), m2.retryParams.AllHosts)
	}
	// The hub must be AllHosts[0] to maintain hub-first ordering.
	if len(m2.retryParams.AllHosts) > 0 && m2.retryParams.AllHosts[0].Host != hub.Host {
		t.Errorf("AllHosts[0] must be the hub %q, got %q",
			hub.Host, m2.retryParams.AllHosts[0].Host)
	}
}

// TestRepeatedRetry_AllHostsContainsHubWhenHubSucceededEarlier verifies that
// even when the hub is NOT in FailedHosts (it succeeded in the previous round),
// AllHosts still contains the hub so hub-spoke retry knows where to fan out from.
func TestRepeatedRetry_AllHostsContainsHubWhenHubSucceededEarlier(t *testing.T) {
	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.example.com", DisplayName: "spoke"}

	// Retry model: only spoke failed (hub was fine); AllHosts is [hub, spoke].
	failed := []config.ResolvedHost{spoke}
	all := []config.ResolvedHost{hub, spoke}

	m := makeRetryModelWithFailures(failed, all, "hub-spoke")

	// Press 'r' — spoke is still failing.
	m2, _ := sendDistributeKey(m, "r")

	if m2.retryParams == nil {
		t.Fatal("second retry model must have non-nil retryParams")
	}
	// AllHosts must include the hub even though it's not in FailedHosts.
	hubFound := false
	for _, h := range m2.retryParams.AllHosts {
		if h.Host == hub.Host {
			hubFound = true
			break
		}
	}
	if !hubFound {
		t.Errorf("AllHosts in second retry must include hub %q; got %v",
			hub.Host, m2.retryParams.AllHosts)
	}
}

// TestRepeatedRetry_ParallelModeAllHostsIsDestHosts verifies that for parallel
// mode (no hub), AllHosts in the second retry equals m.destHosts (the hosts
// that were being retried), since there is no special hub to preserve.
func TestRepeatedRetry_ParallelModeAllHostsIsDestHosts(t *testing.T) {
	h1 := config.ResolvedHost{Host: "h1.example.com", DisplayName: "h1"}
	h2 := config.ResolvedHost{Host: "h2.example.com", DisplayName: "h2"}
	h3 := config.ResolvedHost{Host: "h3.example.com", DisplayName: "h3"}

	// For parallel mode, AllHosts == FailedHosts (no hub distinction).
	failed := []config.ResolvedHost{h1, h2, h3}
	all := []config.ResolvedHost{h1, h2, h3}

	m := makeRetryModelWithFailures(failed, all, "parallel")
	// Only h1 is still failing in this second retry.
	m.hostProgress["h1.example.com"] = executor.TransferFailed
	m.hostProgress["h2.example.com"] = executor.TransferDone
	m.hostProgress["h3.example.com"] = executor.TransferDone

	m2, _ := sendDistributeKey(m, "r")

	if m2.retryParams == nil {
		t.Fatal("second retry model must have non-nil retryParams")
	}
	// FailedHosts must be exactly [h1].
	if len(m2.retryParams.FailedHosts) != 1 || m2.retryParams.FailedHosts[0].Host != "h1.example.com" {
		t.Errorf("expected FailedHosts=[h1.example.com], got %v", m2.retryParams.FailedHosts)
	}
	// AllHosts comes from original retryParams.AllHosts = [h1, h2, h3].
	if len(m2.retryParams.AllHosts) != 3 {
		t.Errorf("expected AllHosts to have 3 entries (from original retryParams), got %d: %v",
			len(m2.retryParams.AllHosts), m2.retryParams.AllHosts)
	}
}

// ---------------------------------------------------------------------------
// AC 7: manual exit from retry loop
// ---------------------------------------------------------------------------

// TestRepeatedRetry_EscFromRetryConfirmExitsLoop verifies that pressing Esc
// on the retry-confirm step of any retry model (including the second retry)
// sets exitToMain = true and done = true, ending the retry loop.
func TestRepeatedRetry_EscFromRetryConfirmExitsLoop(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
	}
	all := []config.ResolvedHost{
		{Host: "hub.example.com", DisplayName: "hub"},
		failed[0],
	}
	m := makeRetryModelWithFailures(failed, all, "parallel")

	// Press 'r' to get a second retry model at RetryConfirm.
	m2, _ := sendDistributeKey(m, "r")
	if m2.step != DistributeStepRetryConfirm {
		t.Fatalf("expected DistributeStepRetryConfirm, got %d", m2.step)
	}

	// Press Esc on the retry-confirm step to abort.
	m3, cmd := sendDistributeKey(m2, "esc")

	if !m3.IsDone() {
		t.Error("Esc from retry-confirm should mark wizard as done")
	}
	if !m3.IsExitToMain() {
		t.Error("Esc from retry-confirm should set exitToMain = true")
	}
	if isQuitCmd(cmd) {
		t.Error("Esc from retry-confirm should not issue tea.Quit")
	}
}

// TestRepeatedRetry_QFromRetryConfirmReturnsToHostList verifies that pressing
// 'q' on the retry-confirm step of a repeated retry returns to the host list.
func TestRepeatedRetry_QFromRetryConfirmReturnsToHostList(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
	}
	all := []config.ResolvedHost{failed[0]}
	m := makeRetryModelWithFailures(failed, all, "parallel")

	// Press 'r' to get the second retry model.
	m2, _ := sendDistributeKey(m, "r")
	if m2.step != DistributeStepRetryConfirm {
		t.Fatalf("expected DistributeStepRetryConfirm, got %d", m2.step)
	}

	// Press 'q' to return to host list.
	m3, cmd := sendDistributeKey(m2, "q")

	if !m3.IsExitToMain() {
		t.Error("pressing 'q' on retry-confirm should set exitToMain = true")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing 'q' on retry-confirm should not issue tea.Quit")
	}
}

// TestRepeatedRetry_NFromRetryConfirmExitsToMain verifies that pressing 'n'
// on the retry-confirm step of a repeated retry returns to the main TUI.
func TestRepeatedRetry_NFromRetryConfirmExitsToMain(t *testing.T) {
	failed := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1"},
	}
	all := []config.ResolvedHost{failed[0]}
	m := makeRetryModelWithFailures(failed, all, "parallel")

	// Press 'r' → second retry model at RetryConfirm.
	m2, _ := sendDistributeKey(m, "r")
	if m2.step != DistributeStepRetryConfirm {
		t.Fatalf("expected DistributeStepRetryConfirm, got %d", m2.step)
	}

	// Press 'n' to decline the retry.
	m3, cmd := sendDistributeKey(m2, "n")

	if !m3.IsDone() {
		t.Error("pressing 'n' on retry-confirm should mark wizard as done")
	}
	if !m3.IsExitToMain() {
		t.Error("pressing 'n' on retry-confirm should set exitToMain = true")
	}
	if isQuitCmd(cmd) {
		t.Error("pressing 'n' on retry-confirm should not issue tea.Quit")
	}
}

// TestRepeatedRetry_ThreeRoundsAllHostsPreserved verifies that AllHosts
// is consistently preserved across three consecutive retry rounds.  This
// simulates a user retrying twice in hub-spoke mode where the hub always
// stays at AllHosts[0].
func TestRepeatedRetry_ThreeRoundsAllHostsPreserved(t *testing.T) {
	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	s1 := config.ResolvedHost{Host: "s1.example.com", DisplayName: "s1"}
	s2 := config.ResolvedHost{Host: "s2.example.com", DisplayName: "s2"}
	s3 := config.ResolvedHost{Host: "s3.example.com", DisplayName: "s3"}

	originalAll := []config.ResolvedHost{hub, s1, s2, s3}

	// Round 1: hub + all spokes failed.
	m := makeRetryModelWithFailures(originalAll, originalAll, "hub-spoke")

	// Round 2: press 'r' → second retry model.
	m2, _ := sendDistributeKey(m, "r")
	if m2.step != DistributeStepRetryConfirm {
		t.Fatalf("round 2: expected RetryConfirm step, got %d", m2.step)
	}
	if m2.retryParams == nil {
		t.Fatal("round 2: retryParams must not be nil")
	}
	// AllHosts should be the original list.
	if len(m2.retryParams.AllHosts) != len(originalAll) {
		t.Errorf("round 2: AllHosts len = %d, want %d", len(m2.retryParams.AllHosts), len(originalAll))
	}
	if m2.retryParams.AllHosts[0].Host != hub.Host {
		t.Errorf("round 2: AllHosts[0] = %q, want hub %q", m2.retryParams.AllHosts[0].Host, hub.Host)
	}

	// Advance round 2 model to Execute and simulate hub + s1 still failing.
	m2, _ = sendDistributeKey(m2, "enter") // confirm retry
	m2.executeStarted = true
	m2.executeDone = true
	m2.hostProgress = map[string]executor.TransferStatus{
		hub.Host: executor.TransferFailed,
		s1.Host:  executor.TransferFailed,
		s2.Host:  executor.TransferDone,
		s3.Host:  executor.TransferDone,
	}

	// Round 3: press 'r' again.
	m3, _ := sendDistributeKey(m2, "r")
	if m3.step != DistributeStepRetryConfirm {
		t.Fatalf("round 3: expected RetryConfirm step, got %d", m3.step)
	}
	if m3.retryParams == nil {
		t.Fatal("round 3: retryParams must not be nil")
	}
	// AllHosts must still be the original list with the hub first.
	if len(m3.retryParams.AllHosts) != len(originalAll) {
		t.Errorf("round 3: AllHosts len = %d, want %d", len(m3.retryParams.AllHosts), len(originalAll))
	}
	if m3.retryParams.AllHosts[0].Host != hub.Host {
		t.Errorf("round 3: AllHosts[0] = %q, want hub %q", m3.retryParams.AllHosts[0].Host, hub.Host)
	}
	// FailedHosts must be only [hub, s1].
	if len(m3.retryParams.FailedHosts) != 2 {
		t.Errorf("round 3: FailedHosts len = %d, want 2: %v",
			len(m3.retryParams.FailedHosts), m3.retryParams.FailedHosts)
	}
}
