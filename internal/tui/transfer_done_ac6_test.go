// Tests for AC 6: Successful transfers are confirmed by scp exit code 0
// (TransferDone).
//
// These tests verify that in the TUI layer:
//  1. failedHosts() returns only hosts whose hostProgress is TransferFailed —
//     hosts with TransferDone are NOT included in the retry candidate list.
//  2. Pressing 'r' after all hosts succeeded (TransferDone) does not trigger
//     a retry — there are no failed hosts to retry.
//  3. A mixed result (some TransferDone, some TransferFailed) causes only the
//     TransferFailed hosts to appear in the RetryParams passed to the retry model.
//
// The core invariant: TransferDone (scp exit code 0) means the transfer
// succeeded permanently; only TransferFailed hosts require retry.
package tui

import (
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// ---------------------------------------------------------------------------
// failedHosts() unit tests
// ---------------------------------------------------------------------------

// TestFailedHosts_AllDone_ReturnsNil verifies that when every host has
// TransferDone (scp exited 0 for all), failedHosts() returns nil (no failures).
func TestFailedHosts_AllDone_ReturnsNil(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"},
		{Host: "host2.example.com", DisplayName: "host2"},
		{Host: "host3.example.com", DisplayName: "host3"},
	}
	m := DistributeModel{
		destHosts: hosts,
		hostProgress: map[string]executor.TransferStatus{
			"host1.example.com": executor.TransferDone,
			"host2.example.com": executor.TransferDone,
			"host3.example.com": executor.TransferDone,
		},
	}

	failed := m.failedHosts()
	if len(failed) != 0 {
		t.Errorf("expected no failed hosts when all are TransferDone, got %d: %v", len(failed), failed)
	}
}

// TestFailedHosts_AllFailed_ReturnsAll verifies that when every host has
// TransferFailed, failedHosts() returns all hosts.
func TestFailedHosts_AllFailed_ReturnsAll(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"},
		{Host: "host2.example.com", DisplayName: "host2"},
	}
	m := DistributeModel{
		destHosts: hosts,
		hostProgress: map[string]executor.TransferStatus{
			"host1.example.com": executor.TransferFailed,
			"host2.example.com": executor.TransferFailed,
		},
	}

	failed := m.failedHosts()
	if len(failed) != 2 {
		t.Errorf("expected 2 failed hosts, got %d", len(failed))
	}
}

// TestFailedHosts_Mixed_ReturnOnlyFailed verifies that when some hosts have
// TransferDone and others have TransferFailed, only the TransferFailed hosts
// are returned.  TransferDone hosts are excluded — they do not need retry.
func TestFailedHosts_Mixed_ReturnOnlyFailed(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"}, // succeeded
		{Host: "host2.example.com", DisplayName: "host2"}, // failed
		{Host: "host3.example.com", DisplayName: "host3"}, // succeeded
		{Host: "host4.example.com", DisplayName: "host4"}, // failed
	}
	m := DistributeModel{
		destHosts: hosts,
		hostProgress: map[string]executor.TransferStatus{
			"host1.example.com": executor.TransferDone,
			"host2.example.com": executor.TransferFailed,
			"host3.example.com": executor.TransferDone,
			"host4.example.com": executor.TransferFailed,
		},
	}

	failed := m.failedHosts()
	if len(failed) != 2 {
		t.Errorf("expected 2 failed hosts, got %d: %v", len(failed), failed)
	}

	// Verify the correct hosts are in the failed list.
	failedSet := make(map[string]bool)
	for _, h := range failed {
		failedSet[h.Host] = true
	}
	if failedSet["host1.example.com"] || failedSet["host3.example.com"] {
		t.Error("TransferDone hosts must not appear in failedHosts()")
	}
	if !failedSet["host2.example.com"] || !failedSet["host4.example.com"] {
		t.Error("TransferFailed hosts must appear in failedHosts()")
	}
}

// TestFailedHosts_NilProgressMap_ReturnsNil verifies that failedHosts() safely
// handles a nil hostProgress map (e.g. before execution started).
func TestFailedHosts_NilProgressMap_ReturnsNil(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"},
	}
	m := DistributeModel{
		destHosts:    hosts,
		hostProgress: nil,
	}

	failed := m.failedHosts()
	if len(failed) != 0 {
		t.Errorf("expected no failed hosts with nil hostProgress, got %d", len(failed))
	}
}

// TestFailedHosts_PendingNotIncluded verifies that TransferPending hosts are
// NOT treated as failed — only TransferFailed is a failure.
func TestFailedHosts_PendingNotIncluded(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"},
	}
	m := DistributeModel{
		destHosts: hosts,
		hostProgress: map[string]executor.TransferStatus{
			"host1.example.com": executor.TransferPending,
		},
	}

	failed := m.failedHosts()
	if len(failed) != 0 {
		t.Errorf("TransferPending must not be treated as failed; got %d failed hosts", len(failed))
	}
}

// TestFailedHosts_InProgressNotIncluded verifies that TransferInProgress hosts
// are NOT treated as failed.
func TestFailedHosts_InProgressNotIncluded(t *testing.T) {
	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", DisplayName: "host1"},
	}
	m := DistributeModel{
		destHosts: hosts,
		hostProgress: map[string]executor.TransferStatus{
			"host1.example.com": executor.TransferInProgress,
		},
	}

	failed := m.failedHosts()
	if len(failed) != 0 {
		t.Errorf("TransferInProgress must not be treated as failed; got %d failed hosts", len(failed))
	}
}

// ---------------------------------------------------------------------------
// AC 6: Retry key 'r' interaction with TransferDone
// ---------------------------------------------------------------------------

// TestRetryKey_AllSucceeded_NoRetryTriggered verifies that pressing 'r' after
// execution completes with all hosts in TransferDone state does NOT trigger a
// retry (there are no failures to retry).  The model must remain a DistributeModel,
// not switch to a retry model.
func TestRetryKey_AllSucceeded_NoRetryTriggered(t *testing.T) {
	m := makeExecuteDoneModelAllSucceeded([]string{"host1.example.com", "host2.example.com"})

	updated, _ := sendDistributeKey(m, "r")

	// The returned model must still be a DistributeModel (not a retry model).
	// If retry was triggered, the model would transition to DistributeStepRetryConfirm.
	// With no failures, it must remain at DistributeStepExecute.
	if updated.step != DistributeStepExecute {
		t.Errorf("expected step to remain DistributeStepExecute after 'r' with no failures; got step %d", updated.step)
	}

	// The model must still have all hosts as TransferDone — none should be reset.
	for host, status := range updated.hostProgress {
		if status != executor.TransferDone {
			t.Errorf("host %q: status should remain TransferDone after no-op retry key; got %v", host, status)
		}
	}
}

// TestRetryKey_SomeFailed_OnlyFailedInRetryParams verifies that when 'r' is
// pressed after a mixed result (some TransferDone, some TransferFailed), the
// resulting RetryParams.FailedHosts contains ONLY the TransferFailed hosts —
// not the TransferDone hosts.
func TestRetryKey_SomeFailed_OnlyFailedInRetryParams(t *testing.T) {
	// Build a model with mixed results.
	hosts := []config.ResolvedHost{
		{Host: "ok.example.com", DisplayName: "ok"},
		{Host: "fail.example.com", DisplayName: "fail"},
	}
	m := NewDistributeModel(minimalConfig(), 80, 24)
	m.step = DistributeStepExecute
	m.destHosts = hosts
	m.sourcePaths = []string{"/data/file.tar.gz"}
	m.destPath = "/tmp/dest"
	m.copyMode = "parallel"
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = map[string]executor.TransferStatus{
		"ok.example.com":   executor.TransferDone,
		"fail.example.com": executor.TransferFailed,
	}

	updated, _ := sendDistributeKey(m, "r")

	// After pressing 'r', the model should have transitioned to a retry-confirm
	// model.  The retry model starts at DistributeStepRetryConfirm.
	if updated.step != DistributeStepRetryConfirm {
		t.Errorf("expected DistributeStepRetryConfirm after 'r' with failures; got step %d", updated.step)
	}

	// retryParams must be set and must contain only the failed host.
	if updated.retryParams == nil {
		t.Fatal("retryParams must be non-nil after pressing 'r' when failures exist")
	}
	if len(updated.retryParams.FailedHosts) != 1 {
		t.Errorf("expected 1 failed host in RetryParams, got %d: %v",
			len(updated.retryParams.FailedHosts), updated.retryParams.FailedHosts)
	}
	if updated.retryParams.FailedHosts[0].Host != "fail.example.com" {
		t.Errorf("expected FailedHosts[0] = fail.example.com, got %q",
			updated.retryParams.FailedHosts[0].Host)
	}

	// The succeeded host must NOT be in FailedHosts.
	for _, h := range updated.retryParams.FailedHosts {
		if h.Host == "ok.example.com" {
			t.Error("TransferDone host ok.example.com must NOT appear in RetryParams.FailedHosts")
		}
	}
}

// TestRetryKey_AllFailed_AllInRetryParams verifies that when all hosts failed,
// all hosts appear in RetryParams.FailedHosts.
func TestRetryKey_AllFailed_AllInRetryParams(t *testing.T) {
	m := makeExecuteDoneModelWithFailures([]string{"host1.example.com", "host2.example.com"})

	updated, _ := sendDistributeKey(m, "r")

	if updated.step != DistributeStepRetryConfirm {
		t.Errorf("expected DistributeStepRetryConfirm after 'r' with all failures; got step %d", updated.step)
	}

	if updated.retryParams == nil {
		t.Fatal("retryParams must be non-nil after pressing 'r' when all hosts failed")
	}
	if len(updated.retryParams.FailedHosts) != 2 {
		t.Errorf("expected 2 failed hosts in RetryParams, got %d", len(updated.retryParams.FailedHosts))
	}
}

// ---------------------------------------------------------------------------
// AC 6: TransferDone as the sole success signal — no additional check needed
// ---------------------------------------------------------------------------

// TestTransferDone_IsDefinedAsSCPExit0 verifies that the TransferDone constant
// in the executor package has the expected semantic documented in progress.go:
// "TransferDone means scp exited with code 0 (success)".
// This is a compile-time / documentation check confirming the constant exists
// and is distinct from TransferFailed.
func TestTransferDone_IsDefinedAsSCPExit0(t *testing.T) {
	// These constants must be distinct values.
	if executor.TransferDone == executor.TransferFailed {
		t.Error("TransferDone and TransferFailed must be distinct constants")
	}
	if executor.TransferDone == executor.TransferPending {
		t.Error("TransferDone and TransferPending must be distinct constants")
	}
	if executor.TransferDone == executor.TransferInProgress {
		t.Error("TransferDone and TransferInProgress must be distinct constants")
	}

	// TransferDone.String() must return "done".
	if executor.TransferDone.String() != "done" {
		t.Errorf("TransferDone.String() = %q, want \"done\"", executor.TransferDone.String())
	}
}
