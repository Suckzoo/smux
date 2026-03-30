// Tests for AC 5: If hub retry succeeds, failed spokes are retried.
//
// These tests exercise runHubSpokeRetryWithProgress to verify that when the
// hub push succeeds, the fan-out phase is entered and failed spokes receive
// progress updates indicating that retry was attempted.
package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// injectSCPSuccessSSHFail injects a fake scp (exits 0) and fake ssh (exits 1)
// into PATH.  This allows the hub push phase (which uses scp) to succeed while
// the fan-out infrastructure (which uses ssh for GenerateOnHub) fails, so we
// can verify the fan-out phase is entered without needing real SSH access.
func injectSCPSuccessSSHFail(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()
	// scp exits 0 (success)
	scpScript := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "scp"), []byte(scpScript), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	// ssh exits 1 (failure) — causes GenerateOnHub / DistributePublicKey to fail
	sshScript := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// ---------------------------------------------------------------------------
// AC 5: Hub retry succeeds → failed spokes are fanned out to
// ---------------------------------------------------------------------------

// TestAC5_HubInRetry_HubSucceeds_SpokesReceiveUpdates verifies that when the
// hub is in the retry set and the hub push succeeds, the fan-out phase is
// entered and the failed spokes receive progress updates.
//
// Setup: scp exits 0 (hub push succeeds), ssh exits 1 (GenerateOnHub fails).
// Expected outcome: hub receives TransferDone; spokes receive TransferFailed
// (from GenerateOnHub failure, NOT from hub-push-blocked error).
func TestAC5_HubInRetry_HubSucceeds_SpokesReceiveUpdates(t *testing.T) {
	injectSCPSuccessSSHFail(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5-test.com", DisplayName: "hub"}
	spoke1 := config.ResolvedHost{Host: "spoke1.ac5-test.com", DisplayName: "spoke1"}
	spoke2 := config.ResolvedHost{Host: "spoke2.ac5-test.com", DisplayName: "spoke2"}

	// Both hub and spokes were in the original failed set.
	dstHosts := []config.ResolvedHost{hub, spoke1, spoke2}
	ch := make(chan executor.ProgressUpdate, 32)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runHubSpokeRetryWithProgress(ctx,
			config.ResolvedHost{}, // local source
			"/local/file.txt",
			dstHosts,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub, // retryHub
		)
	}()

	updates := collectProgressUpdates(ch)

	// Hub must end with TransferDone (scp exits 0).
	hubStatuses := statusesFor(hub.Host, updates)
	if len(hubStatuses) == 0 {
		t.Fatal("hub: expected progress updates, got none")
	}
	last := hubStatuses[len(hubStatuses)-1]
	if last != executor.TransferDone {
		t.Errorf("hub: expected final TransferDone (scp succeeded), got %v", last)
	}

	// Spokes must receive updates: they were passed to the fan-out phase.
	// With ssh failing (GenerateOnHub fails), they will be TransferFailed —
	// but the key assertion is that they GOT updates at all (fan-out was entered).
	for _, spoke := range []config.ResolvedHost{spoke1, spoke2} {
		ss := statusesFor(spoke.Host, updates)
		if len(ss) == 0 {
			t.Errorf("spoke %q: expected at least one progress update (fan-out must be attempted after hub success); got none",
				spoke.Host)
		}
	}
}

// TestAC5_HubInRetry_HubSucceeds_SpokesNotBlockedByHub verifies that the
// spoke failure reason (when the fan-out infrastructure fails) does NOT
// reference a hub push failure — the hub push succeeded, and the spokes
// failed for a different reason (GenerateOnHub failure).
func TestAC5_HubInRetry_HubSucceeds_SpokesNotBlockedByHub(t *testing.T) {
	injectSCPSuccessSSHFail(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5b-test.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.ac5b-test.com", DisplayName: "spoke"}

	dstHosts := []config.ResolvedHost{hub, spoke}
	ch := make(chan executor.ProgressUpdate, 16)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runHubSpokeRetryWithProgress(ctx,
			config.ResolvedHost{},
			"/local/file.txt",
			dstHosts,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub,
		)
	}()

	updates := collectProgressUpdates(ch)

	// Hub must have TransferDone.
	hubFinal := statusesFor(hub.Host, updates)
	if len(hubFinal) == 0 || hubFinal[len(hubFinal)-1] != executor.TransferDone {
		t.Fatalf("hub: expected TransferDone, got %v", hubFinal)
	}

	// Spoke must be TransferFailed, but the error should NOT reference hub failure.
	for _, u := range updates {
		if u.Host.Host == spoke.Host && u.Status == executor.TransferFailed && u.Err != nil {
			// The error must NOT say "hub retry failed" — that message only appears
			// when the hub push itself failed (AC 4 scenario), not when the hub
			// succeeded and fan-out infrastructure failed.
			errMsg := u.Err.Error()
			if errMsg == "hub retry failed; spoke fan-out skipped" {
				t.Errorf("spoke error should not say hub retry failed when hub actually succeeded; got: %q", errMsg)
			}
			return
		}
	}
	// Spoke may have received no TransferFailed updates if GenerateOnHub failure
	// was silently swallowed; ensure we got at least one update.
	if len(statusesFor(spoke.Host, updates)) == 0 {
		t.Error("spoke: expected at least one progress update after hub success")
	}
}

// TestAC5_HubNotInRetry_SpokesDirectlyFannedOut verifies that when the hub is
// NOT in the retry set (hub succeeded previously; only spokes failed), the
// fan-out phase is entered directly — no hub push attempted.
//
// The spoke receives a progress update from the fan-out phase (GenerateOnHub
// fails with ssh exiting 1, so spoke gets TransferFailed from the fan-out
// infrastructure, NOT from a hub-push-blocking error).
func TestAC5_HubNotInRetry_SpokesDirectlyFannedOut(t *testing.T) {
	// Here both ssh and scp fail: since hub is not in dstHosts, no scp runs for
	// the push phase.  The fan-out phase tries GenerateOnHub (ssh) which fails.
	injectFakeBinaries(t, 1) // both fail
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5c-test.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.ac5c-test.com", DisplayName: "spoke"}

	// Hub is NOT in dstHosts — only the spoke is being retried.
	dstHosts := []config.ResolvedHost{spoke}
	ch := make(chan executor.ProgressUpdate, 16)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runHubSpokeRetryWithProgress(ctx,
			config.ResolvedHost{},
			"/local/file.txt",
			dstHosts,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub, // retryHub — NOT in dstHosts
		)
	}()

	updates := collectProgressUpdates(ch)

	// Hub must NOT have received any progress update (no push was attempted).
	if len(statusesFor(hub.Host, updates)) != 0 {
		t.Error("hub: must not receive progress updates when not in dstHosts (push was skipped)")
	}

	// Spoke must receive at least one update (fan-out was attempted directly).
	ss := statusesFor(spoke.Host, updates)
	if len(ss) == 0 {
		t.Error("spoke: expected at least one progress update (fan-out must be attempted when hub not in dstHosts)")
	}
	// Spoke should be TransferFailed (GenerateOnHub / ssh failed).
	if ss[len(ss)-1] != executor.TransferFailed {
		t.Errorf("spoke: expected final TransferFailed (fan-out infrastructure failed), got %v", ss[len(ss)-1])
	}
}

// TestAC5_HubSucceeds_OnlyHubInDstHosts_NoSpokeFanOut verifies that when the
// only host being retried is the hub itself (no spokes), a successful hub push
// results in completion with no spoke updates (there are no spokes to fan out to).
func TestAC5_HubSucceeds_OnlyHubInDstHosts_NoSpokeFanOut(t *testing.T) {
	injectSCPSuccessSSHFail(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5d-test.com", DisplayName: "hub"}

	// Only hub in retry set; no spokes.
	dstHosts := []config.ResolvedHost{hub}
	ch := make(chan executor.ProgressUpdate, 8)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runHubSpokeRetryWithProgress(ctx,
			config.ResolvedHost{},
			"/local/file.txt",
			dstHosts,
			"/remote/file.txt",
			fakeTempKP(),
			ch,
			hub,
		)
	}()

	updates := collectProgressUpdates(ch)

	// Hub must receive TransferDone.
	hubStatuses := statusesFor(hub.Host, updates)
	if len(hubStatuses) == 0 {
		t.Fatal("hub: expected TransferDone, got no updates")
	}
	if hubStatuses[len(hubStatuses)-1] != executor.TransferDone {
		t.Errorf("hub: expected final TransferDone, got %v", hubStatuses[len(hubStatuses)-1])
	}

	// Total updates: InProgress + Done = 2 for hub; nothing else.
	if len(updates) > 2 {
		t.Errorf("expected at most 2 updates (hub InProgress + Done), got %d", len(updates))
	}
}
