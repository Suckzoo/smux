package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// SpokePull tests
// ---------------------------------------------------------------------------

// TestSpokePull_Success verifies that SpokePull returns a successful
// CopyResult when the SSH command exits with code 0.
func TestSpokePull_Success(t *testing.T) {
	injectFakeSSH(t, 0, "")

	ctx := context.Background()
	spoke := config.ResolvedHost{Host: "spoke1.example.com", User: "ubuntu"}

	result := SpokePull(ctx, spoke, "hubuser", "10.0.0.1", []string{"/hub/file.txt"}, "/dest/file.txt", "FAKE-PRIVATE-KEY")

	if !result.Success {
		t.Errorf("expected success, got err: %v", result.Err)
	}
	if result.Err != nil {
		t.Errorf("expected nil Err on success, got: %v", result.Err)
	}
	if result.Host.Host != spoke.Host {
		t.Errorf("Host: expected %q, got %q", spoke.Host, result.Host.Host)
	}
}

// TestSpokePull_Failure verifies that SpokePull returns a failed CopyResult
// when the SSH command exits with a non-zero code.
func TestSpokePull_Failure(t *testing.T) {
	injectFakeSSH(t, 1, "permission denied")

	ctx := context.Background()
	spoke := config.ResolvedHost{Host: "spoke1.example.com", User: "ubuntu"}

	result := SpokePull(ctx, spoke, "hubuser", "10.0.0.1", []string{"/hub/file.txt"}, "/dest/file.txt", "FAKE-PRIVATE-KEY")

	if result.Success {
		t.Error("expected failure for exit code 1")
	}
	if result.Err == nil {
		t.Error("expected non-nil Err for failed ssh")
	}
	if !strings.Contains(result.Stderr, "permission denied") {
		t.Errorf("Stderr should contain error message; got: %q", result.Stderr)
	}
	if !strings.Contains(result.Err.Error(), "permission denied") {
		t.Errorf("Err should mention stderr content; got: %v", result.Err)
	}
}

// TestSpokePull_CommandContainsSCPFromHub verifies that the remote command
// passed to SSH contains the expected scp invocation pulling from the hub,
// mkdir -p for the destination directory, and rm -f for temp key cleanup.
func TestSpokePull_CommandContainsSCPFromHub(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	ctx := context.Background()
	spoke := config.ResolvedHost{Host: "spoke1.example.com", User: "ubuntu"}

	result := SpokePull(ctx, spoke, "hubuser", "10.0.0.1", []string{"/hub/data/file.txt"}, "/dest/data/file.txt", "FAKE-PRIVATE-KEY-CONTENT")

	if !result.Success {
		t.Fatalf("expected success, got err: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsStr := string(argsData)

	// The remote command (after --) should contain scp pulling from hub.
	if !strings.Contains(argsStr, "scp") {
		t.Error("remote command should contain 'scp'")
	}
	if !strings.Contains(argsStr, "hubuser@10.0.0.1:") {
		t.Error("remote command should contain hub address 'hubuser@10.0.0.1:'")
	}
	if !strings.Contains(argsStr, "/hub/data/file.txt") {
		t.Error("remote command should contain hub path")
	}
	if !strings.Contains(argsStr, "/dest/data/file.txt") {
		t.Error("remote command should contain dest path")
	}
	if !strings.Contains(argsStr, "mkdir -p") {
		t.Error("remote command should contain 'mkdir -p' for dest directory")
	}
	if !strings.Contains(argsStr, "rm -f") {
		t.Error("remote command should contain 'rm -f' for temp key cleanup")
	}

	// The private key content should be in the remote command (which is one
	// of the args after --), not as a top-level SSH argument. Verify the
	// args before "--" do not contain the raw key.
	lines := strings.Split(strings.TrimSpace(argsStr), "\n")
	for _, line := range lines {
		if line == "--" {
			break
		}
		if strings.Contains(line, "FAKE-PRIVATE-KEY-CONTENT") {
			t.Error("private key content should not appear in SSH args before '--'")
		}
	}
}

// TestSpokePullWithProgress_SendsUpdates verifies that SpokePullWithProgress
// sends TransferInProgress and TransferDone updates for each spoke.
func TestSpokePullWithProgress_SendsUpdates(t *testing.T) {
	injectFakeSSH(t, 0, "")

	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com", User: "ubuntu"},
		{Host: "spoke2.example.com", User: "ubuntu"},
	}

	// Buffer large enough for all updates (2 per spoke).
	progress := make(chan ProgressUpdate, 10)

	ctx := context.Background()
	results := SpokePullWithProgress(ctx, spokes, "hubuser", "10.0.0.1", []string{"/hub/file.txt"}, "/dest/file.txt", "FAKE-KEY", progress)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("result[%d]: expected success, got err: %v", i, r.Err)
		}
	}

	// Drain channel and count updates per host.
	close(progress)
	inProgressCount := 0
	doneCount := 0
	for u := range progress {
		switch u.Status {
		case TransferInProgress:
			inProgressCount++
		case TransferDone:
			doneCount++
		case TransferFailed:
			t.Errorf("unexpected TransferFailed for %s: %v", u.Host.Host, u.Err)
		}
	}

	if inProgressCount != 2 {
		t.Errorf("expected 2 TransferInProgress updates, got %d", inProgressCount)
	}
	if doneCount != 2 {
		t.Errorf("expected 2 TransferDone updates, got %d", doneCount)
	}
}

// TestSpokePull_TempKeyCleanedUp verifies that the remote command includes
// rm -f to clean up the temporary private key file on the spoke.
func TestSpokePull_TempKeyCleanedUp(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", fakeDir+":"+os.Getenv("PATH"))

	ctx := context.Background()
	spoke := config.ResolvedHost{Host: "spoke1.example.com", User: "ubuntu"}

	result := SpokePull(ctx, spoke, "hubuser", "10.0.0.1", []string{"/hub/file.txt"}, "/dest/file.txt", "FAKE-KEY")
	if !result.Success {
		t.Fatalf("expected success, got err: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsStr := string(argsData)

	// The remote command must contain rm -f to clean up the temp key.
	if !strings.Contains(argsStr, "rm -f") {
		t.Error("remote command should contain 'rm -f' for temp key cleanup")
	}

	// The cleanup should reference the smux-pull temp file pattern.
	if !strings.Contains(argsStr, "/tmp/smux-pull-") {
		t.Error("remote command should reference /tmp/smux-pull-* temp file")
	}

	// The exit code preservation pattern should be present.
	if !strings.Contains(argsStr, "rc=$?") {
		t.Error("remote command should preserve scp exit code with 'rc=$?'")
	}
	if !strings.Contains(argsStr, "exit $rc") {
		t.Error("remote command should exit with preserved code via 'exit $rc'")
	}
}
