package sshkeys

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
)

// ---------------------------------------------------------------------------
// shellescape
// ---------------------------------------------------------------------------

func TestShellescape_Plain(t *testing.T) {
	got := shellescape("/home/alice/docs")
	want := "'/home/alice/docs'"
	if got != want {
		t.Errorf("shellescape plain: got %q, want %q", got, want)
	}
}

func TestShellescape_WithSpaces(t *testing.T) {
	got := shellescape("/path/with spaces/file")
	want := "'/path/with spaces/file'"
	if got != want {
		t.Errorf("shellescape spaces: got %q, want %q", got, want)
	}
}

func TestShellescape_WithSingleQuote(t *testing.T) {
	got := shellescape("it's a test")
	want := `'it'\''s a test'`
	if got != want {
		t.Errorf("shellescape single-quote: got %q, want %q", got, want)
	}
}

func TestShellescape_SSHPublicKeyLine(t *testing.T) {
	// A real public key line should round-trip through shellescape without
	// introducing shell metacharacters.
	pubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake smux-distribute-abcdef01"
	got := shellescape(pubKey)
	// Must start and end with single quotes.
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("expected single-quoted result, got %q", got)
	}
	// Must contain the full key content.
	if !strings.Contains(got, "AAAAC3NzaC1lZDI1NTE5") {
		t.Errorf("shellescape dropped key body: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Generate
// ---------------------------------------------------------------------------

func TestGenerate_CreatesKeyPair(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	t.Cleanup(func() { _ = kp.DeleteKeyFiles() })

	// Temp directory must exist.
	if _, err := os.Stat(kp.Dir); err != nil {
		t.Errorf("temp dir %q must exist after Generate: %v", kp.Dir, err)
	}

	// Private key must exist.
	if _, err := os.Stat(kp.PrivateKeyPath); err != nil {
		t.Errorf("private key %q must exist: %v", kp.PrivateKeyPath, err)
	}

	// Public key must exist.
	if _, err := os.Stat(kp.PublicKeyPath); err != nil {
		t.Errorf("public key %q must exist: %v", kp.PublicKeyPath, err)
	}

	// Public key content must start with the Ed25519 algorithm identifier.
	if !strings.HasPrefix(kp.PublicKey, "ssh-ed25519") {
		t.Errorf("PublicKey must start with 'ssh-ed25519'")
	}

	// Comment must follow the expected pattern.
	if !strings.HasPrefix(kp.Comment, "smux-distribute-") {
		t.Errorf("Comment must start with 'smux-distribute-', got %q", kp.Comment)
	}

	// Comment must appear in the public key file content.
	if !strings.Contains(kp.PublicKey, kp.Comment) {
		t.Errorf("public key must contain the comment %q", kp.Comment)
	}
}

func TestGenerate_UniquePerCall(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp1, err := Generate()
	if err != nil {
		t.Fatalf("Generate #1: %v", err)
	}
	defer func() { _ = kp1.DeleteKeyFiles() }()

	kp2, err := Generate()
	if err != nil {
		t.Fatalf("Generate #2: %v", err)
	}
	defer func() { _ = kp2.DeleteKeyFiles() }()

	if kp1.Comment == kp2.Comment {
		t.Errorf("expected unique comments per Generate call, both got %q", kp1.Comment)
	}
	if kp1.PublicKey == kp2.PublicKey {
		t.Error("expected different public keys from two Generate calls")
	}
	if kp1.Dir == kp2.Dir {
		t.Errorf("expected different temp dirs, both got %q", kp1.Dir)
	}
}

func TestGenerate_PrivateKeyInSubdir(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer func() { _ = kp.DeleteKeyFiles() }()

	// Private key must be inside the reported Dir.
	if !strings.HasPrefix(kp.PrivateKeyPath, kp.Dir) {
		t.Errorf("PrivateKeyPath %q not inside Dir %q", kp.PrivateKeyPath, kp.Dir)
	}
	if !strings.HasPrefix(kp.PublicKeyPath, kp.Dir) {
		t.Errorf("PublicKeyPath %q not inside Dir %q", kp.PublicKeyPath, kp.Dir)
	}
}

// ---------------------------------------------------------------------------
// DeleteKeyFiles
// ---------------------------------------------------------------------------

func TestDeleteKeyFiles_RemovesDir(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := kp.Dir

	if err := kp.DeleteKeyFiles(); err != nil {
		t.Fatalf("DeleteKeyFiles: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected temp dir %q to be removed; stat err: %v", dir, err)
	}
}

func TestDeleteKeyFiles_ClearsDirField(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := kp.DeleteKeyFiles(); err != nil {
		t.Fatalf("DeleteKeyFiles: %v", err)
	}
	if kp.Dir != "" {
		t.Errorf("expected Dir to be empty after DeleteKeyFiles, got %q", kp.Dir)
	}
}

func TestDeleteKeyFiles_Idempotent(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := kp.DeleteKeyFiles(); err != nil {
		t.Fatalf("first DeleteKeyFiles: %v", err)
	}
	// Second call must not return an error.
	if err := kp.DeleteKeyFiles(); err != nil {
		t.Errorf("second DeleteKeyFiles (no-op): unexpected error: %v", err)
	}
}

func TestDeleteKeyFiles_ZeroValue_NoError(t *testing.T) {
	// A zero-value TempKeyPair (Dir == "") must not error.
	kp := &TempKeyPair{}
	if err := kp.DeleteKeyFiles(); err != nil {
		t.Errorf("DeleteKeyFiles on zero-value: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cleanup — records dirty state when remote SSH fails
// ---------------------------------------------------------------------------

// TestCleanup_RecordsDirtyStateOnSSHFailure injects a fake ssh binary that
// always exits 1 and verifies that each host is recorded in the dirty state.
func TestCleanup_RecordsDirtyStateOnSSHFailure(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	// Inject fake ssh that always exits with code 1.
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	hosts := []config.ResolvedHost{
		{Host: "host1.example.com", User: "ubuntu"},
		{Host: "host2.example.com", Port: 2222},
	}
	dirty := &dirtystate.State{}

	cleanupErr := Cleanup(context.Background(), kp, hosts, dirty)
	if cleanupErr != nil {
		// The local key file deletion might succeed even if remote failed.
		// An error here would mean the temp dir removal failed.
		t.Logf("Cleanup returned local error (may be expected if fake ssh interfered): %v", cleanupErr)
	}

	// Both hosts must be in dirty state.
	if len(dirty.Hosts) != 2 {
		t.Fatalf("expected 2 dirty hosts, got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}

	commentsSeen := map[string]bool{}
	hostsSeen := map[string]bool{}
	for _, h := range dirty.Hosts {
		commentsSeen[h.KeyComment] = true
		hostsSeen[h.Host] = true
		if h.AddedAt.IsZero() {
			t.Errorf("dirty host %q: AddedAt must not be zero", h.Host)
		}
	}
	if !commentsSeen[kp.Comment] {
		t.Errorf("expected dirty hosts to have comment %q", kp.Comment)
	}
	if !hostsSeen["host1.example.com"] {
		t.Error("expected host1.example.com in dirty state")
	}
	if !hostsSeen["host2.example.com"] {
		t.Error("expected host2.example.com in dirty state")
	}
}

// TestCleanup_DeletesKeyFilesEvenOnSSHFailure verifies that local keypair
// files are always removed even when the remote SSH cleanup fails.
func TestCleanup_DeletesKeyFilesEvenOnSSHFailure(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	// Inject fake ssh that always exits 1.
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := kp.Dir

	dirty := &dirtystate.State{}
	_ = Cleanup(context.Background(), kp, []config.ResolvedHost{{Host: "unreachable.example.com"}}, dirty)

	// Temp dir must be gone.
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q should be removed after Cleanup; stat: %v", dir, statErr)
	}
}

// TestCleanup_NoDirtyStateOnSuccess verifies that when remote SSH succeeds
// (no hosts are added to dirty state).
func TestCleanup_NoDirtyStateOnSuccess(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	// Inject a fake ssh binary that always succeeds.
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	hosts := []config.ResolvedHost{
		{Host: "ok1.example.com"},
		{Host: "ok2.example.com"},
	}
	dirty := &dirtystate.State{}

	if err := Cleanup(context.Background(), kp, hosts, dirty); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if !dirty.IsEmpty() {
		t.Errorf("expected no dirty hosts when SSH succeeds, got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}
}

// TestCleanup_EmptyHostList_NoError verifies Cleanup works with no hosts.
func TestCleanup_EmptyHostList_NoError(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH")
	}

	kp, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	dirty := &dirtystate.State{}
	if err := Cleanup(context.Background(), kp, nil, dirty); err != nil {
		t.Fatalf("Cleanup with empty hosts: %v", err)
	}
	if !dirty.IsEmpty() {
		t.Error("expected no dirty hosts for empty host list")
	}
}

// ---------------------------------------------------------------------------
// GenerateOnHub
// ---------------------------------------------------------------------------

// injectFakeSSH installs a fake ssh shell script into a temporary directory
// prepended to PATH. The script exits exitCode; if stderrMsg is non-empty it
// writes that to stderr; if stdoutMsg is non-empty it writes that to stdout.
func injectFakeSSH(t *testing.T, exitCode int, stdoutMsg, stderrMsg string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := "#!/bin/sh\n"
	if stdoutMsg != "" {
		script += fmt.Sprintf("printf '%%s' %s\n", shellescape(stdoutMsg))
	}
	if stderrMsg != "" {
		script += fmt.Sprintf("printf '%%s\\n' %s >&2\n", shellescape(stderrMsg))
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// fakeGenerateOnHubOutput returns a plausible fake stdout from the GenerateOnHub
// remote command: one line for the public key, one line for the remote dir.
func fakeGenerateOnHubOutput(pubKey, remoteDir string) string {
	return pubKey + "\n" + remoteDir + "\n"
}

func TestGenerateOnHub_Success(t *testing.T) {
	fakePubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake smux-distribute-aabbccdd"
	fakeDir := "/tmp/smux-distribute-abcdef"
	injectFakeSSH(t, 0, fakeGenerateOnHubOutput(fakePubKey, fakeDir), "")

	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}
	kp, err := GenerateOnHub(context.Background(), hub)
	if err != nil {
		t.Fatalf("GenerateOnHub: %v", err)
	}

	if kp.Hub.Host != hub.Host {
		t.Errorf("Hub.Host: got %q, want %q", kp.Hub.Host, hub.Host)
	}
	if kp.RemoteDir != fakeDir {
		t.Errorf("RemoteDir: got %q, want %q", kp.RemoteDir, fakeDir)
	}
	if kp.RemotePrivateKeyPath != fakeDir+"/id_ed25519" {
		t.Errorf("RemotePrivateKeyPath: got %q", kp.RemotePrivateKeyPath)
	}
	if kp.RemotePublicKeyPath != fakeDir+"/id_ed25519.pub" {
		t.Errorf("RemotePublicKeyPath: got %q", kp.RemotePublicKeyPath)
	}
	if kp.PublicKey != fakePubKey {
		t.Errorf("PublicKey: got %q, want %q", kp.PublicKey, fakePubKey)
	}
	if !strings.HasPrefix(kp.Comment, "smux-distribute-") {
		t.Errorf("Comment must start with 'smux-distribute-', got %q", kp.Comment)
	}
}

func TestGenerateOnHub_CommentUnique(t *testing.T) {
	// Two separate calls to GenerateOnHub must produce different comments even
	// when the fake SSH returns the same output (the comment is generated
	// locally before the SSH call).
	fakePubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake smux-distribute-test"
	fakeDir := "/tmp/smux-distribute-test"

	// Inject a fake ssh that records nothing and always outputs the same thing.
	fakeSSHDir := t.TempDir()
	fakeSSHPath := filepath.Join(fakeSSHDir, "ssh")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %s\nexit 0\n",
		shellescape(fakeGenerateOnHubOutput(fakePubKey, fakeDir)))
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeSSHDir+string(os.PathListSeparator)+origPath)

	hub := config.ResolvedHost{Host: "hub.example.com"}

	kp1, err := GenerateOnHub(context.Background(), hub)
	if err != nil {
		t.Fatalf("GenerateOnHub #1: %v", err)
	}
	kp2, err := GenerateOnHub(context.Background(), hub)
	if err != nil {
		t.Fatalf("GenerateOnHub #2: %v", err)
	}

	if kp1.Comment == kp2.Comment {
		t.Errorf("expected unique comments, both got %q", kp1.Comment)
	}
}

func TestGenerateOnHub_SSHFailure(t *testing.T) {
	injectFakeSSH(t, 1, "", "connection refused")

	hub := config.ResolvedHost{Host: "unreachable.example.com"}
	_, err := GenerateOnHub(context.Background(), hub)
	if err == nil {
		t.Fatal("expected error when SSH fails, got nil")
	}
	if !strings.Contains(err.Error(), "generate keypair on hub") {
		t.Errorf("error should mention 'generate keypair on hub': %v", err)
	}
}

func TestGenerateOnHub_UnexpectedOutput_TooFewLines(t *testing.T) {
	// SSH succeeds but outputs only one line (missing the remote dir line).
	injectFakeSSH(t, 0, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake smux-distribute-test\n", "")

	hub := config.ResolvedHost{Host: "hub.example.com"}
	_, err := GenerateOnHub(context.Background(), hub)
	if err == nil {
		t.Fatal("expected error for unexpected output format, got nil")
	}
}

func TestGenerateOnHub_EmptyOutput(t *testing.T) {
	injectFakeSSH(t, 0, "", "")

	hub := config.ResolvedHost{Host: "hub.example.com"}
	_, err := GenerateOnHub(context.Background(), hub)
	if err == nil {
		t.Fatal("expected error for empty output, got nil")
	}
}

// ---------------------------------------------------------------------------
// DeleteHubKeyFiles
// ---------------------------------------------------------------------------

func TestDeleteHubKeyFiles_SendsRmRf(t *testing.T) {
	// Fake ssh that records the command it received.
	fakeSSHDir := t.TempDir()
	argsFile := filepath.Join(fakeSSHDir, "args.txt")
	fakeSSHPath := filepath.Join(fakeSSHDir, "ssh")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeSSHDir+string(os.PathListSeparator)+origPath)

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
	}

	if err := kp.DeleteHubKeyFiles(context.Background()); err != nil {
		t.Fatalf("DeleteHubKeyFiles: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	if !strings.Contains(string(argsData), "rm -rf") {
		t.Errorf("expected 'rm -rf' in ssh args; got:\n%s", string(argsData))
	}
	if !strings.Contains(string(argsData), "/tmp/smux-distribute-abc123") {
		t.Errorf("expected remote dir in ssh args; got:\n%s", string(argsData))
	}
}

func TestDeleteHubKeyFiles_ClearsRemoteDir(t *testing.T) {
	injectFakeSSH(t, 0, "", "")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
	}

	if err := kp.DeleteHubKeyFiles(context.Background()); err != nil {
		t.Fatalf("DeleteHubKeyFiles: %v", err)
	}
	if kp.RemoteDir != "" {
		t.Errorf("expected RemoteDir to be cleared after success, got %q", kp.RemoteDir)
	}
}

func TestDeleteHubKeyFiles_Idempotent(t *testing.T) {
	injectFakeSSH(t, 0, "", "")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
	}

	if err := kp.DeleteHubKeyFiles(context.Background()); err != nil {
		t.Fatalf("first DeleteHubKeyFiles: %v", err)
	}
	// Second call must be a no-op (RemoteDir is now "").
	if err := kp.DeleteHubKeyFiles(context.Background()); err != nil {
		t.Errorf("second DeleteHubKeyFiles (no-op): unexpected error: %v", err)
	}
}

func TestDeleteHubKeyFiles_ZeroValue_NoError(t *testing.T) {
	kp := &HubKeyPair{} // RemoteDir is ""
	if err := kp.DeleteHubKeyFiles(context.Background()); err != nil {
		t.Errorf("DeleteHubKeyFiles on zero-value: %v", err)
	}
}

func TestDeleteHubKeyFiles_SSHFailure_ReturnsError(t *testing.T) {
	injectFakeSSH(t, 1, "", "permission denied")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
	}

	err := kp.DeleteHubKeyFiles(context.Background())
	if err == nil {
		t.Fatal("expected error when SSH fails, got nil")
	}
	if !strings.Contains(err.Error(), "delete hub keypair dir") {
		t.Errorf("error should mention 'delete hub keypair dir': %v", err)
	}
	// RemoteDir must NOT be cleared when deletion fails.
	if kp.RemoteDir == "" {
		t.Error("RemoteDir should remain set when deletion fails")
	}
}

// ---------------------------------------------------------------------------
// CleanupHubKeypair
// ---------------------------------------------------------------------------

func TestCleanupHubKeypair_AllSucceed_NoDirtyState(t *testing.T) {
	injectFakeSSH(t, 0, "", "")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
	}
	dirty := &dirtystate.State{}

	if err := CleanupHubKeypair(context.Background(), kp, spokes, dirty); err != nil {
		t.Fatalf("CleanupHubKeypair: %v", err)
	}
	if !dirty.IsEmpty() {
		t.Errorf("expected no dirty state when all succeed, got %d entries: %v", len(dirty.Hosts), dirty.Hosts)
	}
}

func TestCleanupHubKeypair_SpokeSSHFails_RecordsDirtyState(t *testing.T) {
	// Fake ssh that fails for spoke hosts but succeeds for the hub rm -rf call.
	// We distinguish by checking if "rm -rf" appears in the arguments.
	fakeSSHDir := t.TempDir()
	fakeSSHPath := filepath.Join(fakeSSHDir, "ssh")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *rm\ -rf*) exit 0 ;;
  esac
done
exit 1
`
	if err := os.WriteFile(fakeSSHPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeSSHDir+string(os.PathListSeparator)+origPath)

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com", User: "ubuntu"},
		{Host: "spoke2.example.com", Port: 2222},
	}
	dirty := &dirtystate.State{}

	if err := CleanupHubKeypair(context.Background(), kp, spokes, dirty); err != nil {
		t.Fatalf("CleanupHubKeypair: %v", err)
	}

	// Both spokes must be in dirty state.
	if len(dirty.Hosts) != 2 {
		t.Fatalf("expected 2 dirty hosts (spokes), got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}
	// All dirty entries must have the key comment and no HubKeyDir.
	for _, h := range dirty.Hosts {
		if h.KeyComment != kp.Comment {
			t.Errorf("dirty host %q: KeyComment %q, want %q", h.Host, h.KeyComment, kp.Comment)
		}
		if h.HubKeyDir != "" {
			t.Errorf("dirty host %q: HubKeyDir should be empty for spoke records, got %q", h.Host, h.HubKeyDir)
		}
	}
}

func TestCleanupHubKeypair_HubDeleteFails_RecordsDirtyState(t *testing.T) {
	// Fake ssh that always fails.
	injectFakeSSH(t, 1, "", "rm: permission denied")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com", User: "hubuser"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	dirty := &dirtystate.State{}

	if err := CleanupHubKeypair(context.Background(), kp, nil, dirty); err != nil {
		t.Fatalf("CleanupHubKeypair: %v", err)
	}

	// Hub must be in dirty state with HubKeyDir set.
	if len(dirty.Hosts) != 1 {
		t.Fatalf("expected 1 dirty host (hub), got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}
	h := dirty.Hosts[0]
	if h.Host != kp.Hub.Host {
		t.Errorf("dirty host: Host %q, want %q", h.Host, kp.Hub.Host)
	}
	if h.HubKeyDir != "/tmp/smux-distribute-abc123" {
		t.Errorf("dirty host: HubKeyDir %q, want %q", h.HubKeyDir, "/tmp/smux-distribute-abc123")
	}
	if h.KeyComment != kp.Comment {
		t.Errorf("dirty host: KeyComment %q, want %q", h.KeyComment, kp.Comment)
	}
	if h.AddedAt.IsZero() {
		t.Error("dirty host: AddedAt must not be zero")
	}
}

func TestCleanupHubKeypair_AllFail_AllRecordedInDirtyState(t *testing.T) {
	injectFakeSSH(t, 1, "", "connection refused")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
	}
	dirty := &dirtystate.State{}

	if err := CleanupHubKeypair(context.Background(), kp, spokes, dirty); err != nil {
		t.Fatalf("CleanupHubKeypair: %v", err)
	}

	// Expect 2 dirty entries: one spoke + hub.
	if len(dirty.Hosts) != 2 {
		t.Fatalf("expected 2 dirty hosts, got %d: %v", len(dirty.Hosts), dirty.Hosts)
	}
	var hubEntry, spokeEntry *dirtystate.DirtyHost
	for i := range dirty.Hosts {
		if dirty.Hosts[i].HubKeyDir != "" {
			hubEntry = &dirty.Hosts[i]
		} else {
			spokeEntry = &dirty.Hosts[i]
		}
	}
	if hubEntry == nil {
		t.Error("expected a hub dirty entry with HubKeyDir set")
	}
	if spokeEntry == nil {
		t.Error("expected a spoke dirty entry without HubKeyDir")
	}
}

func TestCleanupHubKeypair_EmptySpokes_NoDirtyState(t *testing.T) {
	injectFakeSSH(t, 0, "", "")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	dirty := &dirtystate.State{}

	if err := CleanupHubKeypair(context.Background(), kp, nil, dirty); err != nil {
		t.Fatalf("CleanupHubKeypair: %v", err)
	}
	if !dirty.IsEmpty() {
		t.Errorf("expected no dirty state for empty spokes + hub success, got: %v", dirty.Hosts)
	}
}

func TestCleanupHubKeypair_AlwaysReturnsNil(t *testing.T) {
	// Even when SSH always fails, CleanupHubKeypair must return nil.
	// Errors are recorded in dirty state.
	injectFakeSSH(t, 1, "", "connection refused")

	kp := &HubKeyPair{
		Hub:       config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir: "/tmp/smux-distribute-abc123",
		Comment:   "smux-distribute-aabbccdd",
	}
	dirty := &dirtystate.State{}

	err := CleanupHubKeypair(context.Background(), kp, []config.ResolvedHost{{Host: "spoke.example.com"}}, dirty)
	if err != nil {
		t.Errorf("CleanupHubKeypair must always return nil, got: %v", err)
	}
}

