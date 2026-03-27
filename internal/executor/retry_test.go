package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildReportJSON serialises a DistributeReport to JSON bytes, failing the
// test on error.
func buildReportJSON(t *testing.T, report DistributeReport) []byte {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal test report: %v", err)
	}
	return data
}

// writeTempReport writes data to a temp file and returns the path. The file
// is automatically removed when the test ends.
func writeTempReport(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp report: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// ParseRetryReportBytes — metadata extraction
// ---------------------------------------------------------------------------

// TestParseRetryReportBytes_SourceHostPreserved verifies that the SourceHost
// from a report is reflected in RetryParams.SourceHost.Host.
func TestParseRetryReportBytes_SourceHostPreserved(t *testing.T) {
	report := NewDistributeReport("hub.example.com", "/src/file.txt", "/dst/file.txt", "hub-spoke", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.SourceHost.Host != "hub.example.com" {
		t.Errorf("SourceHost.Host: expected %q, got %q", "hub.example.com", params.SourceHost.Host)
	}
}

// TestParseRetryReportBytes_LocalSourceEmptyHostPreserved verifies that an
// empty SourceHost (local machine indicator) is preserved as-is.
func TestParseRetryReportBytes_LocalSourceEmptyHostPreserved(t *testing.T) {
	report := NewDistributeReport("", "/local/file.txt", "/dst/file.txt", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.SourceHost.Host != "" {
		t.Errorf("local source: SourceHost.Host should be empty, got %q", params.SourceHost.Host)
	}
}

// TestParseRetryReportBytes_SourcePathPreserved verifies that SourcePath is
// extracted verbatim from the report.
func TestParseRetryReportBytes_SourcePathPreserved(t *testing.T) {
	report := NewDistributeReport("", "/var/data/payload.tar.gz", "/tmp/payload.tar.gz", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.SourcePath != "/var/data/payload.tar.gz" {
		t.Errorf("SourcePath: expected %q, got %q", "/var/data/payload.tar.gz", params.SourcePath)
	}
}

// TestParseRetryReportBytes_DestPathPreserved verifies that DestPath is
// extracted verbatim from the report, including when it is empty.
func TestParseRetryReportBytes_DestPathPreserved(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "/remote/dst.txt", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.DestPath != "/remote/dst.txt" {
		t.Errorf("DestPath: expected %q, got %q", "/remote/dst.txt", params.DestPath)
	}
}

// TestParseRetryReportBytes_EmptyDestPathPreserved verifies that an empty
// DestPath (same-as-source convention) is preserved as-is.
func TestParseRetryReportBytes_EmptyDestPathPreserved(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.DestPath != "" {
		t.Errorf("empty DestPath should be preserved; got %q", params.DestPath)
	}
}

// TestParseRetryReportBytes_CopyModeParallel verifies that the "parallel" copy
// mode is extracted correctly.
func TestParseRetryReportBytes_CopyModeParallel(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.CopyMode != "parallel" {
		t.Errorf("CopyMode: expected %q, got %q", "parallel", params.CopyMode)
	}
}

// TestParseRetryReportBytes_CopyModeHubSpoke verifies that the "hub-spoke"
// copy mode is extracted correctly.
func TestParseRetryReportBytes_CopyModeHubSpoke(t *testing.T) {
	report := NewDistributeReport("hub.example.com", "/src.txt", "/dst.txt", "hub-spoke", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.CopyMode != "hub-spoke" {
		t.Errorf("CopyMode: expected %q, got %q", "hub-spoke", params.CopyMode)
	}
}

// ---------------------------------------------------------------------------
// ParseRetryReportBytes — failed host extraction
// ---------------------------------------------------------------------------

// TestParseRetryReportBytes_AllSucceedFailedHostsEmpty verifies that
// FailedHosts is non-nil but empty when all destination hosts succeeded.
func TestParseRetryReportBytes_AllSucceedFailedHostsEmpty(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", true, "", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.FailedHosts == nil {
		t.Error("FailedHosts should not be nil even when all hosts succeeded")
	}
	if len(params.FailedHosts) != 0 {
		t.Errorf("FailedHosts should be empty; got %d entries", len(params.FailedHosts))
	}
}

// TestParseRetryReportBytes_AllFailFailedHostsContainsAll verifies that
// FailedHosts contains every host when all transfers failed.
func TestParseRetryReportBytes_AllFailFailedHostsContainsAll(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", false, "connection refused", nil),
		makeResult("host2.example.com", false, "timeout", nil),
		makeResult("host3.example.com", false, "permission denied", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if len(params.FailedHosts) != 3 {
		t.Errorf("FailedHosts: expected 3, got %d", len(params.FailedHosts))
	}
}

// TestParseRetryReportBytes_PartialFailOnlyFailedInFailedHosts verifies that
// FailedHosts contains only the hosts that did not succeed.
func TestParseRetryReportBytes_PartialFailOnlyFailedInFailedHosts(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "timeout", nil),
		makeResult("host3.example.com", true, "", nil),
		makeResult("host4.example.com", false, "unreachable", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if len(params.FailedHosts) != 2 {
		t.Errorf("FailedHosts: expected 2, got %d", len(params.FailedHosts))
	}

	// Verify the failed hosts are the correct ones.
	failedSet := make(map[string]bool)
	for _, h := range params.FailedHosts {
		failedSet[h.Host] = true
	}
	if !failedSet["host2.example.com"] {
		t.Error("FailedHosts should contain host2.example.com")
	}
	if !failedSet["host4.example.com"] {
		t.Error("FailedHosts should contain host4.example.com")
	}
	if failedSet["host1.example.com"] {
		t.Error("FailedHosts should not contain host1.example.com (succeeded)")
	}
	if failedSet["host3.example.com"] {
		t.Error("FailedHosts should not contain host3.example.com (succeeded)")
	}
}

// TestParseRetryReportBytes_FailedHostFieldsPreserved verifies that User and
// Port are preserved in the ResolvedHost entries within FailedHosts.
func TestParseRetryReportBytes_FailedHostFieldsPreserved(t *testing.T) {
	results := []CopyResult{
		makeResultWithUser("myhost.example.com", "alice", 2222, false),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if len(params.FailedHosts) != 1 {
		t.Fatalf("expected 1 failed host, got %d", len(params.FailedHosts))
	}
	h := params.FailedHosts[0]
	if h.Host != "myhost.example.com" {
		t.Errorf("Host: expected %q, got %q", "myhost.example.com", h.Host)
	}
	if h.User != "alice" {
		t.Errorf("User: expected %q, got %q", "alice", h.User)
	}
	if h.Port != 2222 {
		t.Errorf("Port: expected 2222, got %d", h.Port)
	}
}

// ---------------------------------------------------------------------------
// ParseRetryReportBytes — AllHosts
// ---------------------------------------------------------------------------

// TestParseRetryReportBytes_AllHostsNonNilWhenEmpty verifies that AllHosts is
// non-nil (empty slice) when the report has no hosts.
func TestParseRetryReportBytes_AllHostsNonNilWhenEmpty(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", nil)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.AllHosts == nil {
		t.Error("AllHosts should not be nil for a report with no hosts")
	}
	if len(params.AllHosts) != 0 {
		t.Errorf("AllHosts should be empty; got %d entries", len(params.AllHosts))
	}
}

// TestParseRetryReportBytes_AllHostsContainsAllRegardlessOfOutcome verifies
// that AllHosts contains both successful and failed hosts.
func TestParseRetryReportBytes_AllHostsContainsAllRegardlessOfOutcome(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "timeout", nil),
		makeResult("host3.example.com", true, "", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if len(params.AllHosts) != 3 {
		t.Errorf("AllHosts: expected 3, got %d", len(params.AllHosts))
	}
}

// TestParseRetryReportBytes_AllHostsOrderPreserved verifies that AllHosts is
// in the same order as the report's Hosts array.
func TestParseRetryReportBytes_AllHostsOrderPreserved(t *testing.T) {
	wantOrder := []string{
		"alpha.example.com",
		"beta.example.com",
		"gamma.example.com",
	}
	results := make([]CopyResult, len(wantOrder))
	for i, h := range wantOrder {
		results[i] = makeResult(h, true, "", nil)
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)
	data := buildReportJSON(t, report)

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if len(params.AllHosts) != len(wantOrder) {
		t.Fatalf("AllHosts length: expected %d, got %d", len(wantOrder), len(params.AllHosts))
	}
	for i, want := range wantOrder {
		if params.AllHosts[i].Host != want {
			t.Errorf("AllHosts[%d]: expected %q, got %q", i, want, params.AllHosts[i].Host)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseRetryReportBytes — error cases
// ---------------------------------------------------------------------------

// TestParseRetryReportBytes_InvalidJSONReturnsError verifies that malformed
// JSON returns a non-nil error with a descriptive message.
func TestParseRetryReportBytes_InvalidJSONReturnsError(t *testing.T) {
	_, err := ParseRetryReportBytes([]byte("{not valid json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestParseRetryReportBytes_EmptyBytesReturnsError verifies that empty input
// returns a non-nil error.
func TestParseRetryReportBytes_EmptyBytesReturnsError(t *testing.T) {
	_, err := ParseRetryReportBytes([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

// ---------------------------------------------------------------------------
// ParseRetryReport — file I/O
// ---------------------------------------------------------------------------

// TestParseRetryReport_ReadsFileAndParsesReport verifies that ParseRetryReport
// reads the given file and returns correct RetryParams.
func TestParseRetryReport_ReadsFileAndParsesReport(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "connection refused", nil),
	}
	report := NewDistributeReport("src.example.com", "/src/file.txt", "/dst/file.txt", "parallel", results)
	data := buildReportJSON(t, report)
	path := writeTempReport(t, data)

	params, err := ParseRetryReport(path)
	if err != nil {
		t.Fatalf("ParseRetryReport: %v", err)
	}

	if params.SourceHost.Host != "src.example.com" {
		t.Errorf("SourceHost.Host: expected %q, got %q", "src.example.com", params.SourceHost.Host)
	}
	if params.SourcePath != "/src/file.txt" {
		t.Errorf("SourcePath: expected %q, got %q", "/src/file.txt", params.SourcePath)
	}
	if params.DestPath != "/dst/file.txt" {
		t.Errorf("DestPath: expected %q, got %q", "/dst/file.txt", params.DestPath)
	}
	if params.CopyMode != "parallel" {
		t.Errorf("CopyMode: expected %q, got %q", "parallel", params.CopyMode)
	}
	if len(params.FailedHosts) != 1 {
		t.Errorf("FailedHosts: expected 1, got %d", len(params.FailedHosts))
	}
	if len(params.AllHosts) != 2 {
		t.Errorf("AllHosts: expected 2, got %d", len(params.AllHosts))
	}
}

// TestParseRetryReport_NonExistentFileReturnsError verifies that a missing
// file produces a non-nil error.
func TestParseRetryReport_NonExistentFileReturnsError(t *testing.T) {
	_, err := ParseRetryReport("/nonexistent/path/report.json")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

// TestParseRetryReport_InvalidJSONFileReturnsError verifies that a file
// containing invalid JSON produces a non-nil error.
func TestParseRetryReport_InvalidJSONFileReturnsError(t *testing.T) {
	path := writeTempReport(t, []byte("{broken json"))

	_, err := ParseRetryReport(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON file, got nil")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: FormatJSON → ParseRetryReportBytes
// ---------------------------------------------------------------------------

// TestParseRetryReport_RoundTripViaFormatJSON verifies that a report produced
// by NewDistributeReport + FormatJSON can be parsed back by
// ParseRetryReportBytes with all fields intact.
func TestParseRetryReport_RoundTripViaFormatJSON(t *testing.T) {
	results := []CopyResult{
		makeResultWithUser("host1.example.com", "alice", 2222, true),
		makeResult("host2.example.com", false, "permission denied", nil),
		makeResult("host3.example.com", false, "unreachable", nil),
	}
	original := NewDistributeReport("hub.example.com", "/src/file.tar.gz", "/opt/file.tar.gz", "hub-spoke", results)

	data, err := original.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	params, err := ParseRetryReportBytes(data)
	if err != nil {
		t.Fatalf("ParseRetryReportBytes: %v", err)
	}

	if params.SourceHost.Host != "hub.example.com" {
		t.Errorf("SourceHost.Host round-trip: expected %q, got %q", "hub.example.com", params.SourceHost.Host)
	}
	if params.SourcePath != "/src/file.tar.gz" {
		t.Errorf("SourcePath round-trip: expected %q, got %q", "/src/file.tar.gz", params.SourcePath)
	}
	if params.DestPath != "/opt/file.tar.gz" {
		t.Errorf("DestPath round-trip: expected %q, got %q", "/opt/file.tar.gz", params.DestPath)
	}
	if params.CopyMode != "hub-spoke" {
		t.Errorf("CopyMode round-trip: expected %q, got %q", "hub-spoke", params.CopyMode)
	}
	if len(params.AllHosts) != 3 {
		t.Errorf("AllHosts round-trip: expected 3, got %d", len(params.AllHosts))
	}
	if len(params.FailedHosts) != 2 {
		t.Errorf("FailedHosts round-trip: expected 2, got %d", len(params.FailedHosts))
	}

	// Verify field-level round-trip for the successful host.
	h := params.AllHosts[0]
	if h.Host != "host1.example.com" {
		t.Errorf("AllHosts[0].Host: expected %q, got %q", "host1.example.com", h.Host)
	}
	if h.User != "alice" {
		t.Errorf("AllHosts[0].User: expected %q, got %q", "alice", h.User)
	}
	if h.Port != 2222 {
		t.Errorf("AllHosts[0].Port: expected 2222, got %d", h.Port)
	}
}

// ---------------------------------------------------------------------------
// PrepareRetryKeypairs — test helpers
// ---------------------------------------------------------------------------

// fakeHubPubKey and fakeHubRemoteDir are the values emitted by the fake SSH
// binary when simulating sshkeys.GenerateOnHub output.
const fakeHubPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeHubKey smux-distribute-fakehub0001"
const fakeHubRemoteDir = "/tmp/smux-distribute-fakehubdir0001"

// injectFakeHubSSH installs a fake SSH binary that:
//   - When any argument contains "ssh-keygen": prints a fake public key and
//     remote directory in the format expected by sshkeys.GenerateOnHub, then
//     exits 0.
//   - For all other calls (e.g. sshkeys.DistributePublicKey): exits with
//     distributionExitCode.
//
// This lets tests exercise prepareHubSpokeRetryKeypair without a real SSH
// server.
func injectFakeHubSSH(t *testing.T, distributionExitCode int) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSHPath := filepath.Join(fakeDir, "ssh")
	// Build the script.  Use %%s so that fmt.Sprintf leaves literal %s for
	// the shell printf, while %s is filled in by Go.
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *ssh-keygen*)
      printf '%%s\n%%s\n' %s %s
      exit 0
      ;;
  esac
done
exit %d
`,
		shelleEscapeForScript(fakeHubPubKey),
		shelleEscapeForScript(fakeHubRemoteDir),
		distributionExitCode,
	)
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hub ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// injectFakeHubSSHWithArgRecording installs a fake SSH binary that behaves
// like injectFakeHubSSH but also records the arguments of every
// non-GenerateOnHub call to argsFile (one argument per line) so tests can
// verify that distribution was attempted for specific hosts.
func injectFakeHubSSHWithArgRecording(t *testing.T, argsFile string, distributionExitCode int) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSHPath := filepath.Join(fakeDir, "ssh")
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *ssh-keygen*)
      printf '%%s\n%%s\n' %s %s
      exit 0
      ;;
  esac
done
printf '%%s\n' "$@" >> %s
exit %d
`,
		shelleEscapeForScript(fakeHubPubKey),
		shelleEscapeForScript(fakeHubRemoteDir),
		shelleEscapeForScript(argsFile),
		distributionExitCode,
	)
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hub ssh with arg recording: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// ---------------------------------------------------------------------------
// PrepareRetryKeypairs — parallel mode tests
// ---------------------------------------------------------------------------

// TestPrepareRetryKeypairs_ParallelMode_ReturnsLocalKP verifies that
// PrepareRetryKeypairs sets LocalKP and leaves HubKP nil for "parallel" mode.
func TestPrepareRetryKeypairs_ParallelMode_ReturnsLocalKP(t *testing.T) {
	requireSSHKeygen(t)

	params := RetryParams{
		CopyMode:    "parallel",
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	if rk == nil {
		t.Fatal("RetryKeypair should not be nil")
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	if rk.LocalKP == nil {
		t.Error("LocalKP should be set for parallel mode")
	}
	if rk.HubKP != nil {
		t.Error("HubKP should be nil for parallel mode")
	}
}

// TestPrepareRetryKeypairs_ParallelMode_FreshUniqueComment verifies that the
// generated keypair carries a non-empty comment with the "smux-distribute-"
// prefix, confirming it is a fresh keypair not recycled from any prior run.
func TestPrepareRetryKeypairs_ParallelMode_FreshUniqueComment(t *testing.T) {
	requireSSHKeygen(t)

	params := RetryParams{
		CopyMode:    "parallel",
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	comment := rk.LocalKP.Comment
	if comment == "" {
		t.Error("keypair Comment should not be empty")
	}
	const prefix = "smux-distribute-"
	if !strings.HasPrefix(comment, prefix) {
		t.Errorf("keypair Comment should start with %q; got %q", prefix, comment)
	}
}

// TestPrepareRetryKeypairs_ParallelMode_TwoCallsDifferentComments verifies
// that consecutive calls produce keypairs with distinct comments, ensuring
// each retry uses a unique credential that won't collide with earlier ones.
func TestPrepareRetryKeypairs_ParallelMode_TwoCallsDifferentComments(t *testing.T) {
	requireSSHKeygen(t)

	params := RetryParams{
		CopyMode:    "parallel",
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk1, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("first PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk1.LocalKP.DeleteKeyFiles() }()

	rk2, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("second PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk2.LocalKP.DeleteKeyFiles() }()

	if rk1.LocalKP.Comment == rk2.LocalKP.Comment {
		t.Errorf("two calls must produce distinct keypair comments; both got %q",
			rk1.LocalKP.Comment)
	}
}

// TestPrepareRetryKeypairs_ParallelMode_DistributesToFailedHosts verifies
// that the new public key is distributed to every host in FailedHosts via SSH.
func TestPrepareRetryKeypairs_ParallelMode_DistributesToFailedHosts(t *testing.T) {
	requireSSHKeygen(t)

	// Install a fake SSH that records its arguments to a file.
	fakeDir := t.TempDir()
	argsFile := filepath.Join(fakeDir, "ssh-args.txt")
	fakeSSHPath := filepath.Join(fakeDir, "ssh")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %s
exit 0
`, shelleEscapeForScript(argsFile))
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	params := RetryParams{
		CopyMode: "parallel",
		FailedHosts: []config.ResolvedHost{
			{Host: "retry-dest1.example.com"},
			{Host: "retry-dest2.example.com"},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read ssh-args: %v", err)
	}
	argsText := string(argsData)

	for _, host := range params.FailedHosts {
		if !strings.Contains(argsText, host.Host) {
			t.Errorf("expected SSH distribution call to %q; recorded args:\n%s",
				host.Host, argsText)
		}
	}
}

// TestPrepareRetryKeypairs_ParallelMode_DistributionFailure_RecordedInDirty
// verifies that when public key distribution fails for a host the host is
// appended to dirty state with the new keypair's comment.
func TestPrepareRetryKeypairs_ParallelMode_DistributionFailure_RecordedInDirty(t *testing.T) {
	requireSSHKeygen(t)
	injectFakeSSHBinary(t, 1, "", "connection refused") // SSH always fails

	params := RetryParams{
		CopyMode: "parallel",
		FailedHosts: []config.ResolvedHost{
			{Host: "unreachable.example.com", User: "alice", Port: 2222},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	if dirty.IsEmpty() {
		t.Fatal("dirty state should be non-empty after distribution failure")
	}

	found := false
	for _, h := range dirty.Hosts {
		if h.Host == "unreachable.example.com" {
			found = true
			if h.User != "alice" {
				t.Errorf("dirty host User: expected %q, got %q", "alice", h.User)
			}
			if h.Port != 2222 {
				t.Errorf("dirty host Port: expected 2222, got %d", h.Port)
			}
			if h.KeyComment != rk.LocalKP.Comment {
				t.Errorf("dirty host KeyComment: expected %q, got %q",
					rk.LocalKP.Comment, h.KeyComment)
			}
		}
	}
	if !found {
		t.Error("unreachable.example.com should be in dirty state after distribution failure")
	}
}

// TestPrepareRetryKeypairs_ParallelMode_DistributionFailure_NoReturnError
// verifies that distribution failures are captured in dirty state rather than
// returned as errors, so the retry execution can still proceed.
func TestPrepareRetryKeypairs_ParallelMode_DistributionFailure_NoReturnError(t *testing.T) {
	requireSSHKeygen(t)
	injectFakeSSHBinary(t, 1, "", "connection refused")

	params := RetryParams{
		CopyMode: "parallel",
		FailedHosts: []config.ResolvedHost{
			{Host: "unreachable.example.com"},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("distribution failure should not be returned as error; got: %v", err)
	}
	if rk == nil {
		t.Fatal("RetryKeypair should not be nil even when distribution fails")
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()
}

// TestPrepareRetryKeypairs_ParallelMode_PartialDistributionFailure_OnlyFailedInDirty
// verifies that only the hosts where distribution failed are added to dirty
// state; hosts that succeeded are not recorded.
func TestPrepareRetryKeypairs_ParallelMode_PartialDistributionFailure_OnlyFailedInDirty(t *testing.T) {
	requireSSHKeygen(t)

	// Fake SSH that fails for hosts containing "fail" in the arguments.
	fakeDir := t.TempDir()
	fakeSSHPath := filepath.Join(fakeDir, "ssh")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *fail*)
      exit 1
      ;;
  esac
done
exit 0
`
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	params := RetryParams{
		CopyMode: "parallel",
		FailedHosts: []config.ResolvedHost{
			{Host: "ok-host.example.com"},
			{Host: "fail-host.example.com"},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	// Exactly one host should be in dirty state.
	if len(dirty.Hosts) != 1 {
		t.Fatalf("expected 1 dirty host, got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}
	if dirty.Hosts[0].Host != "fail-host.example.com" {
		t.Errorf("wrong host in dirty state: expected %q, got %q",
			"fail-host.example.com", dirty.Hosts[0].Host)
	}

	// The successful host must not appear.
	for _, h := range dirty.Hosts {
		if h.Host == "ok-host.example.com" {
			t.Error("ok-host.example.com should not be in dirty state (distribution succeeded)")
		}
	}
}

// ---------------------------------------------------------------------------
// PrepareRetryKeypairs — hub-spoke mode tests
// ---------------------------------------------------------------------------

// TestPrepareRetryKeypairs_HubSpokeMode_ReturnsHubKP verifies that
// PrepareRetryKeypairs sets HubKP and leaves LocalKP nil for "hub-spoke" mode.
func TestPrepareRetryKeypairs_HubSpokeMode_ReturnsHubKP(t *testing.T) {
	injectFakeHubSSH(t, 0)

	params := RetryParams{
		CopyMode:    "hub-spoke",
		SourceHost:  config.ResolvedHost{Host: "hub.example.com"},
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs hub-spoke: %v", err)
	}
	if rk == nil {
		t.Fatal("RetryKeypair should not be nil")
	}
	if rk.HubKP == nil {
		t.Error("HubKP should be set for hub-spoke mode")
	}
	if rk.LocalKP != nil {
		t.Error("LocalKP should be nil for hub-spoke mode")
	}
}

// TestPrepareRetryKeypairs_HubSpokeMode_HubKPPublicKeySet verifies that
// HubKP.PublicKey is populated with the hub-generated public key.
func TestPrepareRetryKeypairs_HubSpokeMode_HubKPPublicKeySet(t *testing.T) {
	injectFakeHubSSH(t, 0)

	params := RetryParams{
		CopyMode:    "hub-spoke",
		SourceHost:  config.ResolvedHost{Host: "hub.example.com"},
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs hub-spoke: %v", err)
	}
	if rk.HubKP.PublicKey == "" {
		t.Error("HubKP.PublicKey should not be empty")
	}
	// The public key must contain the fake key content from the fake SSH script.
	if !strings.Contains(rk.HubKP.PublicKey, "smux-distribute-") {
		t.Errorf("HubKP.PublicKey should contain key comment prefix; got %q",
			rk.HubKP.PublicKey)
	}
}

// TestPrepareRetryKeypairs_HubSpokeMode_DistributesToFailedSpokes verifies
// that the hub public key is distributed to every host in FailedHosts.
func TestPrepareRetryKeypairs_HubSpokeMode_DistributesToFailedSpokes(t *testing.T) {
	fakeDir := t.TempDir()
	argsFile := filepath.Join(fakeDir, "ssh-args.txt")
	injectFakeHubSSHWithArgRecording(t, argsFile, 0)

	params := RetryParams{
		CopyMode:   "hub-spoke",
		SourceHost: config.ResolvedHost{Host: "hub.example.com"},
		FailedHosts: []config.ResolvedHost{
			{Host: "spoke1.example.com"},
			{Host: "spoke2.example.com"},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs hub-spoke: %v", err)
	}
	if rk == nil || rk.HubKP == nil {
		t.Fatal("expected non-nil RetryKeypair with HubKP set")
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read ssh-args: %v", err)
	}
	argsText := string(argsData)

	for _, spoke := range params.FailedHosts {
		if !strings.Contains(argsText, spoke.Host) {
			t.Errorf("expected SSH distribution call to spoke %q; recorded args:\n%s",
				spoke.Host, argsText)
		}
	}
}

// TestPrepareRetryKeypairs_HubSpokeMode_DistributionFailure_RecordedInDirty
// verifies that spoke distribution failures are recorded in dirty state.
func TestPrepareRetryKeypairs_HubSpokeMode_DistributionFailure_RecordedInDirty(t *testing.T) {
	// Fake SSH: GenerateOnHub succeeds; DistributePublicKey fails.
	injectFakeHubSSH(t, 1)

	params := RetryParams{
		CopyMode:   "hub-spoke",
		SourceHost: config.ResolvedHost{Host: "hub.example.com"},
		FailedHosts: []config.ResolvedHost{
			{Host: "spoke-fail.example.com", User: "bob", Port: 2222},
		},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}

	if dirty.IsEmpty() {
		t.Fatal("dirty state should be non-empty after spoke distribution failure")
	}

	found := false
	for _, h := range dirty.Hosts {
		if h.Host == "spoke-fail.example.com" {
			found = true
			if h.User != "bob" {
				t.Errorf("dirty host User: expected %q, got %q", "bob", h.User)
			}
			if h.Port != 2222 {
				t.Errorf("dirty host Port: expected 2222, got %d", h.Port)
			}
			if rk != nil && rk.HubKP != nil && h.KeyComment != rk.HubKP.Comment {
				t.Errorf("dirty host KeyComment: expected %q, got %q",
					rk.HubKP.Comment, h.KeyComment)
			}
		}
	}
	if !found {
		t.Error("spoke-fail.example.com should be in dirty state after distribution failure")
	}
}

// TestPrepareRetryKeypairs_HubSpokeMode_GenerationFails_ReturnsError verifies
// that when hub keypair generation fails (hub unreachable) an error is
// returned and RetryKeypair is nil.
func TestPrepareRetryKeypairs_HubSpokeMode_GenerationFails_ReturnsError(t *testing.T) {
	injectFakeSSHBinary(t, 1, "", "connection refused") // all SSH calls fail

	params := RetryParams{
		CopyMode:   "hub-spoke",
		SourceHost: config.ResolvedHost{Host: "unreachable-hub.example.com"},
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err == nil {
		t.Fatal("expected error when hub keypair generation fails")
	}
	if rk != nil {
		t.Error("RetryKeypair should be nil when generation fails")
	}
	if !dirty.IsEmpty() {
		t.Error("dirty state should not be modified when generation fails")
	}
}

// TestPrepareRetryKeypairs_HubSpokeMode_GenerationFails_DirtyUnchanged
// verifies that a pre-existing dirty state is not mutated when hub generation
// fails.
func TestPrepareRetryKeypairs_HubSpokeMode_GenerationFails_DirtyUnchanged(t *testing.T) {
	injectFakeSSHBinary(t, 1, "", "connection refused")

	params := RetryParams{
		CopyMode:   "hub-spoke",
		SourceHost: config.ResolvedHost{Host: "unreachable-hub.example.com"},
	}
	// Pre-populate dirty with a sentinel entry.
	dirty := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "pre-existing.example.com", KeyComment: "smux-distribute-preexist01"},
		},
	}

	_, _ = PrepareRetryKeypairs(context.Background(), params, dirty)

	// Pre-existing entry must still be present and no new entry added.
	if len(dirty.Hosts) != 1 {
		t.Errorf("dirty state should be unchanged on generation failure; got %d hosts: %v",
			len(dirty.Hosts), dirty.Hosts)
	}
	if dirty.Hosts[0].Host != "pre-existing.example.com" {
		t.Errorf("pre-existing dirty host must be preserved; got %q", dirty.Hosts[0].Host)
	}
}

// ---------------------------------------------------------------------------
// PrepareRetryKeypairs — copy mode routing tests
// ---------------------------------------------------------------------------

// TestPrepareRetryKeypairs_UnknownCopyMode_FallsBackToParallel verifies that
// an unrecognised copy mode falls back to parallel (local keypair) behaviour.
func TestPrepareRetryKeypairs_UnknownCopyMode_FallsBackToParallel(t *testing.T) {
	requireSSHKeygen(t)

	params := RetryParams{
		CopyMode:    "unknown-mode",
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs with unknown mode: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	if rk.LocalKP == nil {
		t.Error("unknown mode should fall back to parallel; LocalKP should be set")
	}
	if rk.HubKP != nil {
		t.Error("unknown mode should not set HubKP")
	}
}

// TestPrepareRetryKeypairs_EmptyFailedHosts_NoDirtyState verifies that when
// FailedHosts is empty, no distribution is attempted and dirty state remains
// unmodified.
func TestPrepareRetryKeypairs_EmptyFailedHosts_NoDirtyState(t *testing.T) {
	requireSSHKeygen(t)

	params := RetryParams{
		CopyMode:    "parallel",
		FailedHosts: nil,
	}
	dirty := &dirtystate.State{}

	rk, err := PrepareRetryKeypairs(context.Background(), params, dirty)
	if err != nil {
		t.Fatalf("PrepareRetryKeypairs: %v", err)
	}
	defer func() { _ = rk.LocalKP.DeleteKeyFiles() }()

	if !dirty.IsEmpty() {
		t.Errorf("dirty state should be empty when no hosts to distribute to; got %v",
			dirty.Hosts)
	}
}
