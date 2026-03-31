// Tests for AC 4: If hub retry fails, spoke retries are not attempted and
// failure is reported.
//
// Shared test helpers (fakeTempKP, collectProgressUpdates, statusesFor) are
// defined in execute_hub_retry_test.go.
package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// ---------------------------------------------------------------------------
// AC 4: Hub retry fails → error message content
// ---------------------------------------------------------------------------

// TestHubRetry_HubFails_SpokeErrMentionsHub verifies that the error attached
// to spoke TransferFailed updates references the hub failure, giving users a
// clear reason why the spoke fan-out was skipped.
func TestHubRetry_HubFails_SpokeErrMentionsHub(t *testing.T) {
	// Fake scp exits 1: hub push fails.
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.example.com", DisplayName: "spoke"}

	dstHosts := []config.ResolvedHost{hub, spoke}
	ch := make(chan executor.ProgressUpdate, 20)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			dstHosts,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub,
		)
	}()

	updates := collectProgressUpdates(ch)

	// Find the spoke's failure update and check the error message contains
	// a reference to the hub failure so users understand why fan-out was skipped.
	for _, u := range updates {
		if u.Host.Host == spoke.Host && u.Status == executor.TransferFailed {
			if u.Err == nil {
				t.Fatal("spoke TransferFailed update should carry a non-nil Err")
			}
			if !strings.Contains(u.Err.Error(), "hub") {
				t.Errorf("spoke failure error should mention hub; got: %q", u.Err.Error())
			}
			return
		}
	}
	t.Error("expected a TransferFailed update for the spoke but found none")
}

// TestHubRetry_HubFails_MultipleSpokesAllReportedFailed verifies that when
// there are multiple spokes and the hub fails, every spoke is reported as
// TransferFailed — none are left in Pending state.
func TestHubRetry_HubFails_MultipleSpokesAllReportedFailed(t *testing.T) {
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	spokes := []config.ResolvedHost{
		{Host: "s1.example.com", DisplayName: "s1"},
		{Host: "s2.example.com", DisplayName: "s2"},
		{Host: "s3.example.com", DisplayName: "s3"},
	}
	all := append([]config.ResolvedHost{hub}, spokes...)

	ch := make(chan executor.ProgressUpdate, 32)
	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			all,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub,
		)
	}()

	updates := collectProgressUpdates(ch)

	// Every spoke must end in TransferFailed.
	for _, spoke := range spokes {
		ss := statusesFor(spoke.Host, updates)
		if len(ss) == 0 {
			t.Errorf("spoke %s: expected at least one update, got none", spoke.Host)
			continue
		}
		if ss[len(ss)-1] != executor.TransferFailed {
			t.Errorf("spoke %s: expected final TransferFailed, got %v", spoke.Host, ss[len(ss)-1])
		}
	}
}

// TestHubRetry_HubFails_NoFanOutAttempted verifies that after hub failure the
// fan-out phase is not entered: specifically, no spoke receives an
// TransferInProgress or TransferDone update (which would indicate the fan-out
// was started).
func TestHubRetry_HubFails_NoFanOutAttempted(t *testing.T) {
	injectFakeBinaries(t, 1)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.example.com", DisplayName: "spoke"}
	all := []config.ResolvedHost{hub, spoke}

	ch := make(chan executor.ProgressUpdate, 16)
	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			all,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub,
		)
	}()

	updates := collectProgressUpdates(ch)

	// Spoke must not have received TransferInProgress or TransferDone.
	for _, u := range updates {
		if u.Host.Host == spoke.Host {
			if u.Status == executor.TransferInProgress {
				t.Error("spoke should not receive TransferInProgress when hub fails (fan-out must not start)")
			}
			if u.Status == executor.TransferDone {
				t.Error("spoke should not receive TransferDone when hub fails")
			}
		}
	}
}

// TestHubRetry_HubFails_EmptyDstHosts_NoPanic verifies that an empty dstHosts
// slice is handled gracefully without panicking or emitting any updates.
func TestHubRetry_HubFails_EmptyDstHosts_NoPanic(t *testing.T) {
	hub := config.ResolvedHost{Host: "hub.example.com", DisplayName: "hub"}
	ch := make(chan executor.ProgressUpdate, 8)

	ctx := context.Background()
	// Should not panic.
	runSpokePullRetryWithProgress(ctx,
		config.ResolvedHost{},
		[]string{"/local/file.txt"},
		nil, // empty dstHosts
		"/remote/file.txt",
		fakeTempKP(),
		ch,
		hub,
	)
	close(ch)

	updates := collectProgressUpdates(ch)
	if len(updates) != 0 {
		t.Errorf("expected no updates for empty dstHosts, got %d", len(updates))
	}
}
