// Tests for AC 5: If hub retry succeeds, failed spokes are retried.
//
// These tests exercise runSpokePullRetryWithProgress to verify that when the
// hub push succeeds, the spoke-pull phase is entered and failed spokes receive
// progress updates indicating that retry was attempted.
package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// injectAllSucceed injects fake scp (exits 0) and fake ssh (exits 0, outputs
// sample ip-addr data) into PATH.  Both hub push (scp) and spoke-pull (ssh)
// succeed.  The fake ssh outputs ip-addr data so ResolvePrivateIP can work.
func injectAllSucceed(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()

	scpScript := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "scp"), []byte(scpScript), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	// ssh exits 0 and outputs sample ip-addr data for ResolvePrivateIP
	sshScript := "#!/bin/sh\nprintf '2: eth0    inet 10.0.0.1/24 brd 10.0.0.255 scope global eth0\\n'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// injectSSHFail injects fake scp and ssh that both exit with the given code.
func injectSSHFail(t *testing.T, exitCode int) {
	t.Helper()
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nexit " + string(rune('0'+exitCode)) + "\n"
	for _, name := range []string{"scp", "ssh"} {
		if err := os.WriteFile(filepath.Join(fakeDir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// fakeTempKPWithFile creates a TempKeyPair backed by a real temp file.
func fakeTempKPWithFile(t *testing.T) *sshkeys.TempKeyPair {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("fake-private-key-content"), 0o600); err != nil {
		t.Fatalf("write fake key: %v", err)
	}
	if err := os.WriteFile(keyPath+".pub", []byte("ssh-ed25519 AAAA fakekey"), 0o644); err != nil {
		t.Fatalf("write fake pubkey: %v", err)
	}
	return &sshkeys.TempKeyPair{
		Dir:            dir,
		PrivateKeyPath: keyPath,
		PublicKeyPath:  keyPath + ".pub",
		PublicKey:      "ssh-ed25519 AAAA fakekey",
		Comment:        "smux-distribute-test",
	}
}

// ---------------------------------------------------------------------------
// AC 5: Hub retry succeeds → failed spokes are pulled
// ---------------------------------------------------------------------------

// TestAC5_HubInRetry_HubSucceeds_SpokesReceiveUpdates verifies that when the
// hub is in the retry set and the hub push succeeds, the spoke-pull phase is
// entered and the failed spokes receive progress updates.
func TestAC5_HubInRetry_HubSucceeds_SpokesReceiveUpdates(t *testing.T) {
	injectAllSucceed(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5-test.com", DisplayName: "hub", InternalCIDR: "10.0.0.0/24"}
	spoke1 := config.ResolvedHost{Host: "spoke1.ac5-test.com", DisplayName: "spoke1"}
	spoke2 := config.ResolvedHost{Host: "spoke2.ac5-test.com", DisplayName: "spoke2"}

	dstHosts := []config.ResolvedHost{hub, spoke1, spoke2}
	ch := make(chan executor.ProgressUpdate, 32)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			dstHosts,
			"/remote/file.txt",
			fakeTempKPWithFile(t),
			ch,
			hub,
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
		t.Errorf("hub: expected final TransferDone, got %v", last)
	}

	// Spokes must receive updates: spoke-pull phase was entered.
	for _, spoke := range []config.ResolvedHost{spoke1, spoke2} {
		ss := statusesFor(spoke.Host, updates)
		if len(ss) == 0 {
			t.Errorf("spoke %q: expected at least one progress update; got none", spoke.Host)
		}
	}
}

// TestAC5_HubInRetry_HubSucceeds_SpokesNotBlockedByHub verifies that the
// spoke result does NOT reference a hub push failure when the hub succeeded.
func TestAC5_HubInRetry_HubSucceeds_SpokesNotBlockedByHub(t *testing.T) {
	injectAllSucceed(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5b-test.com", DisplayName: "hub", InternalCIDR: "10.0.0.0/24"}
	spoke := config.ResolvedHost{Host: "spoke.ac5b-test.com", DisplayName: "spoke"}

	dstHosts := []config.ResolvedHost{hub, spoke}
	ch := make(chan executor.ProgressUpdate, 16)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			dstHosts,
			"/remote/file.txt",
			fakeTempKPWithFile(t),
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

	// Spoke must NOT have "hub retry failed" error.
	for _, u := range updates {
		if u.Host.Host == spoke.Host && u.Status == executor.TransferFailed && u.Err != nil {
			errMsg := u.Err.Error()
			if errMsg == "hub retry failed; spoke-pull skipped" {
				t.Errorf("spoke error should not say hub retry failed when hub succeeded; got: %q", errMsg)
			}
		}
	}
}

// TestAC5_HubNotInRetry_SpokesDirectlyPulled verifies that when the hub is
// NOT in the retry set, the spoke-pull phase is entered directly.
func TestAC5_HubNotInRetry_SpokesDirectlyPulled(t *testing.T) {
	injectSSHFail(t, 1) // both fail — key distribution to hub will fail
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5c-test.com", DisplayName: "hub"}
	spoke := config.ResolvedHost{Host: "spoke.ac5c-test.com", DisplayName: "spoke"}

	// Hub is NOT in dstHosts — only the spoke is being retried.
	dstHosts := []config.ResolvedHost{spoke}
	ch := make(chan executor.ProgressUpdate, 16)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			dstHosts,
			"/remote/file.txt",
			fakeTempKPWithFile(t),
			ch,
			hub, // retryHub — NOT in dstHosts
		)
	}()

	updates := collectProgressUpdates(ch)

	// All hosts should get TransferFailed since SSH fails
	// (key distribution to hub fails).
	spokeStatuses := statusesFor(spoke.Host, updates)
	if len(spokeStatuses) == 0 {
		t.Error("spoke: expected at least one progress update")
	}
	if spokeStatuses[len(spokeStatuses)-1] != executor.TransferFailed {
		t.Errorf("spoke: expected TransferFailed, got %v", spokeStatuses[len(spokeStatuses)-1])
	}
}

// TestAC5_HubSucceeds_OnlyHubInDstHosts_NoSpokePull verifies that when the
// only host being retried is the hub itself (no spokes), a successful hub push
// results in completion with no spoke updates.
func TestAC5_HubSucceeds_OnlyHubInDstHosts_NoSpokePull(t *testing.T) {
	injectAllSucceed(t)
	t.Setenv("HOME", t.TempDir())

	hub := config.ResolvedHost{Host: "hub.ac5d-test.com", DisplayName: "hub", InternalCIDR: "10.0.0.0/24"}

	dstHosts := []config.ResolvedHost{hub}
	ch := make(chan executor.ProgressUpdate, 8)

	ctx := context.Background()
	go func() {
		defer close(ch)
		runSpokePullRetryWithProgress(ctx,
			config.ResolvedHost{},
			[]string{"/local/file.txt"},
			dstHosts,
			"/remote/file.txt",
			fakeTempKPWithFile(t),
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
