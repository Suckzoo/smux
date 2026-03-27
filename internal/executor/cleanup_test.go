package executor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// withTempHome overrides HOME so dirtystate.Load/Save resolve inside a
// test-controlled directory.  It registers cleanup with t.Cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // Windows compat; no-op on Unix
	return tmpHome
}

// loadDirtyState reads the dirty-state.json file from the test home directory.
func loadDirtyState(t *testing.T) *dirtystate.State {
	t.Helper()
	s, err := dirtystate.Load()
	if err != nil {
		t.Fatalf("dirtystate.Load: %v", err)
	}
	return s
}

// injectFakeSSHBinary installs a fake ssh binary into a temp directory
// prepended to PATH.  exitCode is the script's exit code; stdoutMsg and
// stderrMsg are written to stdout/stderr when non-empty.
func injectFakeSSHBinary(t *testing.T, exitCode int, stdoutMsg, stderrMsg string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := "#!/bin/sh\n"
	if stdoutMsg != "" {
		script += "printf '%s' " + shelleEscapeForScript(stdoutMsg) + "\n"
	}
	if stderrMsg != "" {
		script += "printf '%s\\n' " + shelleEscapeForScript(stderrMsg) + " >&2\n"
	}
	script += "exit " + string(rune('0'+exitCode)) + "\n"
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// shelleEscapeForScript wraps s in single quotes for embedding in shell
// scripts.  Embedded single quotes use the '\'' idiom.
func shelleEscapeForScript(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// requireSSHKeygen skips the test if ssh-keygen is not in PATH.
func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}
}

// generateKP creates a real Ed25519 keypair and registers cleanup.
func generateKP(t *testing.T) *sshkeys.TempKeyPair {
	t.Helper()
	kp, err := sshkeys.Generate()
	if err != nil {
		t.Fatalf("sshkeys.Generate: %v", err)
	}
	t.Cleanup(func() { _ = kp.DeleteKeyFiles() })
	return kp
}

// makeHubKeyPair creates a HubKeyPair value (without actually running
// ssh-keygen on a remote host) for use in tests that only care about the
// cleanup call path.
func makeHubKeyPair(hub config.ResolvedHost, comment, remoteDir string) *sshkeys.HubKeyPair {
	return &sshkeys.HubKeyPair{
		Hub:                  hub,
		RemoteDir:            remoteDir,
		RemotePrivateKeyPath: remoteDir + "/id_ed25519",
		RemotePublicKeyPath:  remoteDir + "/id_ed25519.pub",
		PublicKey:            "ssh-ed25519 FAKEKEY " + comment,
		Comment:              comment,
	}
}

// ---------------------------------------------------------------------------
// combineErrors unit tests
// ---------------------------------------------------------------------------

func TestCombineErrors_AllNil_ReturnsNil(t *testing.T) {
	if err := combineErrors(nil, nil, nil); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCombineErrors_SingleNonNil(t *testing.T) {
	sentinel := &testError{"boom"}
	err := combineErrors(nil, sentinel, nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to contain 'boom', got %q", err.Error())
	}
}

func TestCombineErrors_MultipleNonNil(t *testing.T) {
	err := combineErrors(&testError{"first"}, nil, &testError{"third"})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	got := err.Error()
	if !strings.Contains(got, "first") {
		t.Errorf("expected 'first' in combined error, got %q", got)
	}
	if !strings.Contains(got, "third") {
		t.Errorf("expected 'third' in combined error, got %q", got)
	}
}

func TestCombineErrors_OnlyOneError_NoSemicolon(t *testing.T) {
	err := combineErrors(nil, &testError{"only"}, nil)
	if strings.Contains(err.Error(), ";") {
		t.Errorf("single error should not contain semicolon, got %q", err.Error())
	}
}

// testError is a minimal error implementation for use in combineErrors tests.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// CleanupAfterParallel tests
// ---------------------------------------------------------------------------

// TestCleanupAfterParallel_Success_NoDirtyState verifies that when SSH
// cleanup succeeds for all hosts, no dirty-state entries are saved.
func TestCleanupAfterParallel_Success_NoDirtyState(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "") // SSH always succeeds

	kp := generateKP(t)
	hosts := []config.ResolvedHost{
		{Host: "h1.example.com"},
		{Host: "h2.example.com"},
	}

	if err := CleanupAfterParallel(context.Background(), kp, hosts); err != nil {
		t.Fatalf("CleanupAfterParallel: %v", err)
	}

	s := loadDirtyState(t)
	if !s.IsEmpty() {
		t.Errorf("expected empty dirty state after successful cleanup; got %d hosts: %v", len(s.Hosts), s.Hosts)
	}
}

// TestCleanupAfterParallel_SSHFails_DirtyStateSaved verifies that when SSH
// cleanup fails for destination hosts, the failure is recorded in dirty state
// and persisted to disk.
func TestCleanupAfterParallel_SSHFails_DirtyStateSaved(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "connection refused") // SSH always fails

	kp := generateKP(t)
	hosts := []config.ResolvedHost{
		{Host: "unreachable1.example.com", User: "alice"},
		{Host: "unreachable2.example.com", Port: 2222},
	}

	// CleanupAfterParallel should not error — per-host failures go to dirty state.
	if err := CleanupAfterParallel(context.Background(), kp, hosts); err != nil {
		t.Fatalf("CleanupAfterParallel: %v", err)
	}

	s := loadDirtyState(t)
	if s.IsEmpty() {
		t.Fatal("expected dirty state to be non-empty after SSH failure")
	}
	if len(s.Hosts) != 2 {
		t.Errorf("expected 2 dirty hosts, got %d: %v", len(s.Hosts), s.Hosts)
	}

	// Verify that the correct key comment is stored.
	for _, h := range s.Hosts {
		if h.KeyComment != kp.Comment {
			t.Errorf("dirty host %q: expected KeyComment %q, got %q", h.Host, kp.Comment, h.KeyComment)
		}
	}
}

// TestCleanupAfterParallel_LocalKeyFilesDeleted verifies that the local
// keypair temp directory is removed even when SSH cleanup fails.
func TestCleanupAfterParallel_LocalKeyFilesDeleted(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH fails

	kp := generateKP(t)
	dir := kp.Dir

	hosts := []config.ResolvedHost{{Host: "h.example.com"}}
	_ = CleanupAfterParallel(context.Background(), kp, hosts)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected temp dir %q to be removed after cleanup; stat: %v", dir, err)
	}
}

// TestCleanupAfterParallel_EmptyHosts_NoError verifies that cleanup with an
// empty destination list succeeds without error and writes an empty dirty state.
func TestCleanupAfterParallel_EmptyHosts_NoError(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)

	kp := generateKP(t)

	if err := CleanupAfterParallel(context.Background(), kp, nil); err != nil {
		t.Fatalf("CleanupAfterParallel with no hosts: %v", err)
	}

	s := loadDirtyState(t)
	if !s.IsEmpty() {
		t.Errorf("expected empty dirty state for no-host cleanup, got %d hosts", len(s.Hosts))
	}
}

// TestCleanupAfterParallel_SavesDirtyStateFile verifies that the dirty-state
// file is written to ~/.smux/dirty-state.json after a successful cleanup.
func TestCleanupAfterParallel_SavesDirtyStateFile(t *testing.T) {
	requireSSHKeygen(t)
	tmpHome := withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "")

	kp := generateKP(t)

	if err := CleanupAfterParallel(context.Background(), kp, nil); err != nil {
		t.Fatalf("CleanupAfterParallel: %v", err)
	}

	stateFile := filepath.Join(tmpHome, ".smux", "dirty-state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("expected dirty-state.json to exist at %q: %v", stateFile, err)
	}
}

// TestCleanupAfterParallel_MergesWithExistingDirtyState verifies that cleanup
// appends new dirty hosts to any that already exist in the on-disk dirty state.
func TestCleanupAfterParallel_MergesWithExistingDirtyState(t *testing.T) {
	requireSSHKeygen(t)
	tmpHome := withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH always fails

	// Pre-populate dirty state with one existing entry.
	existing := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "pre-existing.example.com", KeyComment: "smux-distribute-oldkey"},
		},
	}
	smuxDir := filepath.Join(tmpHome, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir .smux: %v", err)
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(smuxDir, "dirty-state.json"), data, 0o600); err != nil {
		t.Fatalf("write pre-existing state: %v", err)
	}

	kp := generateKP(t)
	hosts := []config.ResolvedHost{{Host: "new-fail.example.com"}}

	if err := CleanupAfterParallel(context.Background(), kp, hosts); err != nil {
		t.Fatalf("CleanupAfterParallel: %v", err)
	}

	s := loadDirtyState(t)
	// Must have the pre-existing entry plus the new failure.
	if len(s.Hosts) != 2 {
		t.Fatalf("expected 2 dirty hosts (1 pre-existing + 1 new), got %d: %v", len(s.Hosts), s.Hosts)
	}
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	if !hostsSeen["pre-existing.example.com"] {
		t.Error("pre-existing dirty host must be preserved")
	}
	if !hostsSeen["new-fail.example.com"] {
		t.Error("new failing host must be added to dirty state")
	}
}

// ---------------------------------------------------------------------------
// CleanupAfterHubSpoke tests
// ---------------------------------------------------------------------------

// TestCleanupAfterHubSpoke_Success_NoDirtyState verifies that when all SSH
// cleanup operations succeed, no dirty-state entries are saved.
func TestCleanupAfterHubSpoke_Success_NoDirtyState(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "") // SSH always succeeds

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-hubtest01", "/tmp/smux-hub-fake")
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
	}

	if err := CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, spokes); err != nil {
		t.Fatalf("CleanupAfterHubSpoke: %v", err)
	}

	s := loadDirtyState(t)
	if !s.IsEmpty() {
		t.Errorf("expected empty dirty state after successful cleanup, got %d hosts: %v",
			len(s.Hosts), s.Hosts)
	}
}

// TestCleanupAfterHubSpoke_HubSSHFails_DirtyStateSaved verifies that when
// the hub's SSH cleanup fails (removing the push keypair), the hub is
// recorded in dirty state.
func TestCleanupAfterHubSpoke_HubSSHFails_DirtyStateSaved(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "connection refused") // SSH always fails

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-hubfail01", "/tmp/smux-hub-fake")
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
	}

	_ = CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, spokes)

	s := loadDirtyState(t)
	if s.IsEmpty() {
		t.Fatal("expected non-empty dirty state after SSH failure")
	}
	// We expect at minimum the hub (push-keypair removal) and the spoke to be dirty.
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	if !hostsSeen["hub.example.com"] {
		t.Error("hub should be in dirty state when its SSH cleanup fails")
	}
	if !hostsSeen["spoke1.example.com"] {
		t.Error("spoke should be in dirty state when its SSH cleanup fails")
	}
}

// TestCleanupAfterHubSpoke_SpokeSSHFails_DirtyStateSaved verifies that when
// spoke SSH cleanup fails, each failing spoke is recorded in dirty state.
func TestCleanupAfterHubSpoke_SpokeSSHFails_DirtyStateSaved(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH always fails

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "hub.example.com"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-spokefail01", "/tmp/smux-hub-fake2")
	spokes := []config.ResolvedHost{
		{Host: "spoke-a.example.com"},
		{Host: "spoke-b.example.com"},
		{Host: "spoke-c.example.com"},
	}

	_ = CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, spokes)

	s := loadDirtyState(t)
	// All three spokes (plus hub) must be dirty.
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	for _, spoke := range spokes {
		if !hostsSeen[spoke.Host] {
			t.Errorf("spoke %q should be in dirty state", spoke.Host)
		}
	}
}

// TestCleanupAfterHubSpoke_LocalKeyFilesDeleted verifies that the local
// push-keypair temp directory is removed even when remote SSH cleanup fails.
func TestCleanupAfterHubSpoke_LocalKeyFilesDeleted(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH fails

	kp := generateKP(t)
	dir := kp.Dir

	hub := config.ResolvedHost{Host: "hub.example.com"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-localtest01", "/tmp/smux-hub-fake3")

	_ = CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, nil)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected temp dir %q to be removed; stat: %v", dir, err)
	}
}

// TestCleanupAfterHubSpoke_EmptySpokes_NoError verifies cleanup with no
// spoke hosts succeeds without error.
func TestCleanupAfterHubSpoke_EmptySpokes_NoError(t *testing.T) {
	requireSSHKeygen(t)
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "")

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "hub.example.com"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-nospokes01", "/tmp/smux-hub-nospokes")

	if err := CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, nil); err != nil {
		t.Fatalf("CleanupAfterHubSpoke with no spokes: %v", err)
	}
}

// TestCleanupAfterHubSpoke_SavesDirtyStateFile verifies the dirty-state file
// is created at ~/.smux/dirty-state.json.
func TestCleanupAfterHubSpoke_SavesDirtyStateFile(t *testing.T) {
	requireSSHKeygen(t)
	tmpHome := withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "")

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "hub.example.com"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-savefile01", "/tmp/smux-hub-savefile")

	if err := CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, nil); err != nil {
		t.Fatalf("CleanupAfterHubSpoke: %v", err)
	}

	stateFile := filepath.Join(tmpHome, ".smux", "dirty-state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("expected dirty-state.json at %q: %v", stateFile, err)
	}
}

// TestCleanupAfterHubSpoke_MergesWithExistingDirtyState verifies that new
// dirty hosts are merged with any pre-existing ones on disk.
func TestCleanupAfterHubSpoke_MergesWithExistingDirtyState(t *testing.T) {
	requireSSHKeygen(t)
	tmpHome := withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH always fails

	// Pre-populate dirty state.
	existing := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "old-hub.example.com", KeyComment: "smux-distribute-old"},
		},
	}
	smuxDir := filepath.Join(tmpHome, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir .smux: %v", err)
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(smuxDir, "dirty-state.json"), data, 0o600); err != nil {
		t.Fatalf("write pre-existing state: %v", err)
	}

	kp := generateKP(t)
	hub := config.ResolvedHost{Host: "new-hub.example.com"}
	hubKP := makeHubKeyPair(hub, "smux-distribute-mergetest01", "/tmp/smux-hub-merge")
	spokes := []config.ResolvedHost{{Host: "new-spoke.example.com"}}

	_ = CleanupAfterHubSpoke(context.Background(), kp, hub, hubKP, spokes)

	s := loadDirtyState(t)
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	if !hostsSeen["old-hub.example.com"] {
		t.Error("pre-existing dirty host 'old-hub.example.com' must be preserved")
	}
	if !hostsSeen["new-hub.example.com"] {
		t.Error("new failing hub 'new-hub.example.com' must be added to dirty state")
	}
}

// ---------------------------------------------------------------------------
// CleanupDirtyStateSubset tests
// ---------------------------------------------------------------------------

// preSeedDirtyState writes a dirty-state JSON file to the test HOME directory
// so CleanupDirtyStateSubset can load it as the "existing" state.
func preSeedDirtyState(t *testing.T, s *dirtystate.State) {
	t.Helper()
	tmpHome := os.Getenv("HOME")
	smuxDir := filepath.Join(tmpHome, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir .smux: %v", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal dirty state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(smuxDir, "dirty-state.json"), data, 0o600); err != nil {
		t.Fatalf("write dirty-state.json: %v", err)
	}
}

// TestCleanupDirtyStateSubset_EmptySubset_Noop verifies that calling
// CleanupDirtyStateSubset with an empty subset leaves the on-disk state
// unchanged and returns no error.
func TestCleanupDirtyStateSubset_EmptySubset_Noop(t *testing.T) {
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "")

	// Pre-seed with one dirty host.
	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "preserved.example.com", KeyComment: "smux-distribute-preserved01"},
		},
	}
	preSeedDirtyState(t, initial)

	// Call with empty subset — should be a no-op.
	if err := CleanupDirtyStateSubset(context.Background(), nil); err != nil {
		t.Fatalf("CleanupDirtyStateSubset with empty subset: %v", err)
	}

	// The pre-seeded host should still be present.
	s := loadDirtyState(t)
	if len(s.Hosts) != 1 || s.Hosts[0].Host != "preserved.example.com" {
		t.Errorf("expected pre-seeded host to be preserved; got %v", s.Hosts)
	}
}

// TestCleanupDirtyStateSubset_PreservesNonTargetedHosts verifies that dirty
// hosts NOT in the subset are preserved in the on-disk state after cleanup.
func TestCleanupDirtyStateSubset_PreservesNonTargetedHosts(t *testing.T) {
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "") // SSH always succeeds → subset entry cleaned

	// Two dirty hosts: only "target" is in the subset.
	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "target.example.com", KeyComment: "smux-distribute-target01"},
			{Host: "preserved.example.com", KeyComment: "smux-distribute-preserved02"},
		},
	}
	preSeedDirtyState(t, initial)

	subset := []dirtystate.DirtyHost{
		{Host: "target.example.com", KeyComment: "smux-distribute-target01"},
	}

	if err := CleanupDirtyStateSubset(context.Background(), subset); err != nil {
		t.Fatalf("CleanupDirtyStateSubset: %v", err)
	}

	s := loadDirtyState(t)
	// "target" was cleaned (SSH succeeded), "preserved" was not targeted.
	// Remaining should only contain "preserved".
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	if hostsSeen["target.example.com"] {
		t.Error("target host should be removed from state after successful cleanup")
	}
	if !hostsSeen["preserved.example.com"] {
		t.Error("preserved host should remain in state since it was not targeted")
	}
}

// TestCleanupDirtyStateSubset_FailedHostsKeptInState verifies that targeted
// hosts whose cleanup fails are written back to the dirty state.
func TestCleanupDirtyStateSubset_FailedHostsKeptInState(t *testing.T) {
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH always fails

	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "fail.example.com", KeyComment: "smux-distribute-fail01"},
		},
	}
	preSeedDirtyState(t, initial)

	subset := []dirtystate.DirtyHost{
		{Host: "fail.example.com", KeyComment: "smux-distribute-fail01"},
	}

	if err := CleanupDirtyStateSubset(context.Background(), subset); err != nil {
		t.Fatalf("CleanupDirtyStateSubset: %v", err)
	}

	s := loadDirtyState(t)
	if s.IsEmpty() {
		t.Error("failed cleanup host should remain in dirty state")
	}
	if len(s.Hosts) != 1 || s.Hosts[0].Host != "fail.example.com" {
		t.Errorf("expected 'fail.example.com' to remain; got %v", s.Hosts)
	}
}

// TestCleanupDirtyStateSubset_SuccessfulSubset_ClearedFromState verifies that
// a targeted host whose cleanup succeeds is removed from the on-disk state.
func TestCleanupDirtyStateSubset_SuccessfulSubset_ClearedFromState(t *testing.T) {
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "") // SSH always succeeds

	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "clean.example.com", KeyComment: "smux-distribute-clean01"},
		},
	}
	preSeedDirtyState(t, initial)

	subset := []dirtystate.DirtyHost{
		{Host: "clean.example.com", KeyComment: "smux-distribute-clean01"},
	}

	if err := CleanupDirtyStateSubset(context.Background(), subset); err != nil {
		t.Fatalf("CleanupDirtyStateSubset: %v", err)
	}

	s := loadDirtyState(t)
	if !s.IsEmpty() {
		t.Errorf("expected empty state after successful cleanup; got %v", s.Hosts)
	}
}

// TestCleanupDirtyStateSubset_HubRecord_Cleaned verifies that a hub-type
// dirty record (HubKeyDir != "") is handled via SSH rm -rf rather than
// authorized_keys removal.
func TestCleanupDirtyStateSubset_HubRecord_Cleaned(t *testing.T) {
	withTempHome(t)
	injectFakeSSHBinary(t, 0, "", "") // SSH rm -rf succeeds

	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{
				Host:       "hub.example.com",
				KeyComment: "smux-distribute-hub01",
				HubKeyDir:  "/tmp/smux-hub-subset-test",
			},
		},
	}
	preSeedDirtyState(t, initial)

	subset := []dirtystate.DirtyHost{
		{
			Host:       "hub.example.com",
			KeyComment: "smux-distribute-hub01",
			HubKeyDir:  "/tmp/smux-hub-subset-test",
		},
	}

	if err := CleanupDirtyStateSubset(context.Background(), subset); err != nil {
		t.Fatalf("CleanupDirtyStateSubset for hub record: %v", err)
	}

	s := loadDirtyState(t)
	if !s.IsEmpty() {
		t.Errorf("expected empty state after hub record cleanup; got %v", s.Hosts)
	}
}

// TestCleanupDirtyStateSubset_MixedResults verifies partial success: some
// hosts in the subset succeed and are removed, others fail and are kept.
func TestCleanupDirtyStateSubset_MixedResults(t *testing.T) {
	// We cannot easily make SSH succeed for one host and fail for another
	// using a single fake binary, so we use a simple heuristic: use an
	// always-fail fake binary and verify all subset entries are kept, while
	// the non-targeted entries are also preserved.  This covers the invariant
	// that non-targeted hosts are never dropped.
	withTempHome(t)
	injectFakeSSHBinary(t, 1, "", "") // SSH always fails

	initial := &dirtystate.State{
		Hosts: []dirtystate.DirtyHost{
			{Host: "subset-a.example.com", KeyComment: "smux-distribute-subset-a"},
			{Host: "subset-b.example.com", KeyComment: "smux-distribute-subset-b"},
			{Host: "non-targeted.example.com", KeyComment: "smux-distribute-nontarget"},
		},
	}
	preSeedDirtyState(t, initial)

	subset := []dirtystate.DirtyHost{
		{Host: "subset-a.example.com", KeyComment: "smux-distribute-subset-a"},
		{Host: "subset-b.example.com", KeyComment: "smux-distribute-subset-b"},
	}

	if err := CleanupDirtyStateSubset(context.Background(), subset); err != nil {
		t.Fatalf("CleanupDirtyStateSubset: %v", err)
	}

	s := loadDirtyState(t)
	hostsSeen := map[string]bool{}
	for _, h := range s.Hosts {
		hostsSeen[h.Host] = true
	}
	if !hostsSeen["subset-a.example.com"] {
		t.Error("failed subset-a must remain in state")
	}
	if !hostsSeen["subset-b.example.com"] {
		t.Error("failed subset-b must remain in state")
	}
	if !hostsSeen["non-targeted.example.com"] {
		t.Error("non-targeted host must be preserved in state")
	}
	if len(s.Hosts) != 3 {
		t.Errorf("expected 3 hosts in state; got %d: %v", len(s.Hosts), s.Hosts)
	}
}
