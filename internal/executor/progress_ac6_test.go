// Tests for AC 6: Successful transfers are confirmed by scp exit code 0
// (TransferDone).
//
// These tests verify that RunParallelWithProgress and FanOutFromHubWithProgress
// correctly map scp exit code 0 to TransferDone and non-zero exit codes to
// TransferFailed on the progress channel.  No additional success signal is
// needed beyond the scp exit code.
package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// collectUpdates drains ch until closed and returns all ProgressUpdates.
func collectUpdates(ch <-chan ProgressUpdate) []ProgressUpdate {
	var updates []ProgressUpdate
	for u := range ch {
		updates = append(updates, u)
	}
	return updates
}

// lastStatusFor returns the final TransferStatus seen for the given host in
// updates.  Returns TransferStatus(-1) if no update for that host was found.
func lastStatusFor(host string, updates []ProgressUpdate) TransferStatus {
	last := TransferStatus(-1)
	for _, u := range updates {
		if u.Host.Host == host {
			last = u.Status
		}
	}
	return last
}

// injectFakeSCPPartialFailure injects a fake scp that succeeds (exit 0) for
// all destination arguments except those containing failHostSubstr, which exit 1.
func injectFakeSCPPartialFailure(t *testing.T, failHostSubstr string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *%s*)
      printf '%s unreachable\n' >&2
      exit 1
      ;;
  esac
done
exit 0
`, failHostSubstr, failHostSubstr)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// injectFakeBothBinaries installs fake ssh and scp scripts that both exit with
// exitCode.
func injectFakeBothBinaries(t *testing.T, exitCode int) {
	t.Helper()
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nexit " + strconv.Itoa(exitCode) + "\n"
	for _, name := range []string{"ssh", "scp"} {
		p := filepath.Join(fakeDir, name)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// fakeMinimalHubKeyPair returns a minimal HubKeyPair for tests that inject fake
// binaries and do not need real key material.
func fakeMinimalHubKeyPair() *sshkeys.HubKeyPair {
	return &sshkeys.HubKeyPair{
		RemotePrivateKeyPath: "/nonexistent/hub_id_ed25519",
		PublicKey:            "ssh-ed25519 AAAA fakehubkey",
		Comment:              "smux-hub-test",
	}
}

// ---------------------------------------------------------------------------
// AC 6: RunParallelWithProgress — scp exit code 0 → TransferDone
// ---------------------------------------------------------------------------

// TestRunParallelWithProgress_ExitCode0_TransferDone verifies that when scp
// exits with code 0, TransferDone is sent on the progress channel for each
// destination host.  This is the core AC 6 contract: scp exit code 0 is the
// sole success signal; no additional confirmation is needed.
func TestRunParallelWithProgress_ExitCode0_TransferDone(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	src := config.ResolvedHost{}
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
	}

	ch := make(chan ProgressUpdate, 16)
	results := RunParallelWithProgress(ctx, src, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	// Every host must have its final status as TransferDone (scp exit 0).
	for _, dst := range dests {
		last := lastStatusFor(dst.Host, updates)
		if last != TransferDone {
			t.Errorf("host %q: expected final TransferDone (scp exit 0), got %v", dst.Host, last)
		}
	}

	// The returned CopyResult slice must also indicate success.
	for i, r := range results {
		if !r.Success {
			t.Errorf("result[%d] (%s): CopyResult.Success must be true for scp exit 0", i, r.Host.Host)
		}
	}
}

// TestRunParallelWithProgress_ExitCode0_NoFailedUpdates verifies that when scp
// exits with code 0, no TransferFailed updates are emitted — only TransferDone.
func TestRunParallelWithProgress_ExitCode0_NoFailedUpdates(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
		{Host: "host3.example.com"},
	}

	ch := make(chan ProgressUpdate, 32)
	RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	for _, u := range updates {
		if u.Status == TransferFailed {
			t.Errorf("host %q: unexpected TransferFailed when scp exits 0 (err: %v)", u.Host.Host, u.Err)
		}
	}
}

// TestRunParallelWithProgress_NonZeroExit_TransferFailed verifies that when scp
// exits with a non-zero code, TransferFailed (not TransferDone) is sent on the
// progress channel.
func TestRunParallelWithProgress_NonZeroExit_TransferFailed(t *testing.T) {
	injectFakeSCP(t, 1, "connection refused")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
	}

	ch := make(chan ProgressUpdate, 16)
	results := RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	// Every host must end with TransferFailed.
	for _, dst := range dests {
		last := lastStatusFor(dst.Host, updates)
		if last != TransferFailed {
			t.Errorf("host %q: expected final TransferFailed (scp non-zero exit), got %v", dst.Host, last)
		}
	}

	// The CopyResult slice must also indicate failure.
	for i, r := range results {
		if r.Success {
			t.Errorf("result[%d] (%s): CopyResult.Success must be false for scp non-zero exit", i, r.Host.Host)
		}
		if r.Err == nil {
			t.Errorf("result[%d] (%s): CopyResult.Err must be non-nil for scp non-zero exit", i, r.Host.Host)
		}
	}
}

// TestRunParallelWithProgress_MixedExits_CorrectStatuses verifies that when
// some hosts receive exit code 0 and others receive non-zero, the progress
// channel correctly sends TransferDone for successes and TransferFailed for
// failures.
func TestRunParallelWithProgress_MixedExits_CorrectStatuses(t *testing.T) {
	// Fake scp that fails for host2 only.
	injectFakeSCPPartialFailure(t, "host2")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
		{Host: "host3.example.com"},
	}

	ch := make(chan ProgressUpdate, 32)
	RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	// host1 and host3: must end with TransferDone (scp exit 0).
	for _, host := range []string{"host1.example.com", "host3.example.com"} {
		last := lastStatusFor(host, updates)
		if last != TransferDone {
			t.Errorf("host %q: expected TransferDone (scp exit 0), got %v", host, last)
		}
	}

	// host2: must end with TransferFailed (scp exit non-zero).
	last2 := lastStatusFor("host2.example.com", updates)
	if last2 != TransferFailed {
		t.Errorf("host2.example.com: expected TransferFailed (scp non-zero exit), got %v", last2)
	}
}

// TestRunParallelWithProgress_TransferDoneCarriesNoErr verifies that
// TransferDone updates have a nil Err field — success carries no error.
func TestRunParallelWithProgress_TransferDoneCarriesNoErr(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{{Host: "host1.example.com"}}

	ch := make(chan ProgressUpdate, 8)
	RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	for _, u := range updates {
		if u.Status == TransferDone && u.Err != nil {
			t.Errorf("TransferDone update should have nil Err; got: %v", u.Err)
		}
	}
}

// TestRunParallelWithProgress_TransferFailedCarriesErr verifies that
// TransferFailed updates always have a non-nil Err field.
func TestRunParallelWithProgress_TransferFailedCarriesErr(t *testing.T) {
	injectFakeSCP(t, 1, "permission denied")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{{Host: "host1.example.com"}}

	ch := make(chan ProgressUpdate, 8)
	RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	for _, u := range updates {
		if u.Status == TransferFailed && u.Err == nil {
			t.Error("TransferFailed update must have non-nil Err")
		}
	}
}

// TestRunParallelWithProgress_InProgressThenTerminal verifies that each host
// receives TransferInProgress before its terminal status (TransferDone or
// TransferFailed).  This confirms the progress channel conveys the full
// lifecycle: pending → in-progress → done/failed.
func TestRunParallelWithProgress_InProgressThenTerminal(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/id_ed25519")
	ctx := context.Background()
	dests := []config.ResolvedHost{{Host: "hostA.example.com"}}

	ch := make(chan ProgressUpdate, 8)
	RunParallelWithProgress(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp, ch)
	close(ch)

	updates := collectUpdates(ch)

	// Find InProgress and Done positions for hostA.
	inProgressIdx := -1
	doneIdx := -1
	for i, u := range updates {
		if u.Host.Host != "hostA.example.com" {
			continue
		}
		if u.Status == TransferInProgress {
			inProgressIdx = i
		}
		if u.Status == TransferDone {
			doneIdx = i
		}
	}

	if inProgressIdx < 0 {
		t.Fatal("expected TransferInProgress update, got none")
	}
	if doneIdx < 0 {
		t.Fatal("expected TransferDone update (scp exit 0), got none")
	}
	if inProgressIdx >= doneIdx {
		t.Errorf("TransferInProgress (idx %d) must precede TransferDone (idx %d)",
			inProgressIdx, doneIdx)
	}
}

// ---------------------------------------------------------------------------
// AC 6: FanOutFromHubWithProgress — scp exit code 0 → TransferDone
// ---------------------------------------------------------------------------

// TestFanOutFromHubWithProgress_ExitCode0_TransferDone verifies that when the
// hub-to-spoke scp command (run via ssh on the hub) exits with code 0, each
// spoke receives TransferDone on the progress channel.
func TestFanOutFromHubWithProgress_ExitCode0_TransferDone(t *testing.T) {
	// Both ssh and scp exit 0.
	injectFakeBothBinaries(t, 0)

	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
	}
	hubKP := fakeMinimalHubKeyPair()

	ch := make(chan ProgressUpdate, 16)
	ctx := context.Background()
	FanOutFromHubWithProgress(ctx, hub, "/hub/file.txt", spokes, "/dst/file.txt", hubKP, ch)
	close(ch)

	updates := collectUpdates(ch)

	// Every spoke must have its final status as TransferDone.
	for _, spoke := range spokes {
		last := lastStatusFor(spoke.Host, updates)
		if last != TransferDone {
			t.Errorf("spoke %q: expected final TransferDone (scp exit 0), got %v", spoke.Host, last)
		}
	}
}

// TestFanOutFromHubWithProgress_NonZeroExit_TransferFailed verifies that when
// the hub-to-spoke command exits with a non-zero code, each spoke receives
// TransferFailed.
func TestFanOutFromHubWithProgress_NonZeroExit_TransferFailed(t *testing.T) {
	injectFakeBothBinaries(t, 1)

	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
	}
	hubKP := fakeMinimalHubKeyPair()

	ch := make(chan ProgressUpdate, 16)
	ctx := context.Background()
	FanOutFromHubWithProgress(ctx, hub, "/hub/file.txt", spokes, "/dst/file.txt", hubKP, ch)
	close(ch)

	updates := collectUpdates(ch)

	// Every spoke must have its final status as TransferFailed.
	for _, spoke := range spokes {
		last := lastStatusFor(spoke.Host, updates)
		if last != TransferFailed {
			t.Errorf("spoke %q: expected final TransferFailed (non-zero exit), got %v", spoke.Host, last)
		}
	}
}
