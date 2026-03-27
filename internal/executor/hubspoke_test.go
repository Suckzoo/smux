package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// ---------------------------------------------------------------------------
// PushToHub tests
// ---------------------------------------------------------------------------

// TestPushToHub_LocalSource_Success verifies that PushToHub returns a
// successful HubPushResult when scp exits with code 0 and the source is local.
func TestPushToHub_LocalSource_Success(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	ctx := context.Background()

	src := config.ResolvedHost{}            // local
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}

	result := PushToHub(ctx, src, "/local/file.txt", hub, "/remote/file.txt", kp)

	if !result.Success {
		t.Errorf("expected success, got err: %v", result.Err)
	}
	if result.Err != nil {
		t.Errorf("expected nil Err on success, got: %v", result.Err)
	}
}

// TestPushToHub_HubFieldPopulated verifies that Hub in the result is always
// populated with the hubHost argument regardless of success or failure.
func TestPushToHub_HubFieldPopulated(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	ctx := context.Background()

	hub := config.ResolvedHost{
		Host: "hub.example.com",
		User: "hubuser",
		Port: 2222,
	}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)

	if result.Hub.Host != hub.Host {
		t.Errorf("Hub.Host: expected %q, got %q", hub.Host, result.Hub.Host)
	}
	if result.Hub.User != hub.User {
		t.Errorf("Hub.User: expected %q, got %q", hub.User, result.Hub.User)
	}
	if result.Hub.Port != hub.Port {
		t.Errorf("Hub.Port: expected %d, got %d", hub.Port, result.Hub.Port)
	}
}

// TestPushToHub_HubFieldPopulated_OnFailure verifies that Hub is populated
// even when the transfer fails.
func TestPushToHub_HubFieldPopulated_OnFailure(t *testing.T) {
	injectFakeSCP(t, 1, "connection refused")

	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)

	if result.Hub.Host != hub.Host {
		t.Errorf("Hub.Host: expected %q even on failure, got %q", hub.Host, result.Hub.Host)
	}
	if result.Success {
		t.Error("expected failure for exit code 1")
	}
}

// TestPushToHub_Failure_ErrNonNil verifies that a failed scp produces a
// non-nil Err and Success==false in the result.
func TestPushToHub_Failure_ErrNonNil(t *testing.T) {
	injectFakeSCP(t, 1, "permission denied (publickey)")

	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)

	if result.Success {
		t.Error("expected failure for exit code 1")
	}
	if result.Err == nil {
		t.Error("expected non-nil Err for failed scp")
	}
}

// TestPushToHub_StderrCaptured verifies that stderr from scp is captured in
// the HubPushResult both on success (for informational purposes) and on
// failure (for error reporting).
func TestPushToHub_StderrCaptured(t *testing.T) {
	injectFakeSCP(t, 1, "permission denied (publickey)")

	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)

	if !strings.Contains(result.Stderr, "permission denied (publickey)") {
		t.Errorf("Stderr should contain the error message; got: %q", result.Stderr)
	}
	if result.Err == nil || !strings.Contains(result.Err.Error(), "permission denied (publickey)") {
		t.Errorf("Err should mention the stderr content; got: %v", result.Err)
	}
}

// TestPushToHub_ContextCancelled verifies that context cancellation is
// propagated and produces a failure result without hanging indefinitely.
func TestPushToHub_ContextCancelled(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	if err := os.WriteFile(fakeSCP, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)

	if result.Success {
		t.Error("expected failure due to cancelled context")
	}
	if result.Err == nil {
		t.Error("expected non-nil Err for cancelled context")
	}
}

// TestPushToHub_LocalSource_NoThreeWayFlag verifies that PushToHub does NOT
// include the -3 flag when the source is local.
func TestPushToHub_LocalSource_NoThreeWayFlag(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	src := config.ResolvedHost{} // local
	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, src, "/local/file.txt", hub, "/hub/file.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	if strings.Contains(string(argsData), "-3") {
		t.Error("local source: -3 flag should NOT be present in scp args")
	}
}

// TestPushToHub_RemoteSource_ThreeWayFlag verifies that PushToHub includes the
// -3 flag when the source is a remote host, ensuring data is relayed through
// the local machine (three-way copy).
func TestPushToHub_RemoteSource_ThreeWayFlag(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	src := config.ResolvedHost{Host: "origin.example.com", User: "origuser"}
	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, src, "/origin/file.txt", hub, "/hub/file.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsText := string(argsData)

	if !strings.Contains(argsText, "-3") {
		t.Errorf("remote source: expected -3 in scp args; got:\n%s", argsText)
	}
	if !strings.Contains(argsText, "origuser@origin.example.com:/origin/file.txt") {
		t.Errorf("remote source: expected source address in args; got:\n%s", argsText)
	}
}

// TestPushToHub_RemoteSource_HubAsDestination verifies that the hub host
// appears in the scp arguments as the destination (not as a source) when the
// source is a remote host.
func TestPushToHub_RemoteSource_HubAsDestination(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	src := config.ResolvedHost{Host: "origin.example.com"}
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}

	result := PushToHub(ctx, src, "/origin/src.txt", hub, "/hub/dest.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsText := string(argsData)

	expectedDest := "hubuser@hub.example.com:/hub/dest.txt"
	if !strings.Contains(argsText, expectedDest) {
		t.Errorf("expected hub as destination %q in args; got:\n%s", expectedDest, argsText)
	}
}

// TestPushToHub_HubWithPort verifies that PushToHub passes the hub's port
// to scp via the -P flag when the hub uses a non-default port.
func TestPushToHub_HubWithPort(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com", Port: 2222}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsLines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	found := false
	for i := 0; i < len(argsLines)-1; i++ {
		if argsLines[i] == "-P" && argsLines[i+1] == "2222" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -P 2222 in scp args; got:\n%s", string(argsData))
	}
}

// TestPushToHub_HubWithJumpHost verifies that PushToHub passes the hub's jump
// host to scp via the -J flag when a jump host is configured.
func TestPushToHub_HubWithJumpHost(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com", JumpHost: "jump.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsLines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	found := false
	for i := 0; i < len(argsLines)-1; i++ {
		if argsLines[i] == "-J" && argsLines[i+1] == "jump.example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -J jump.example.com in scp args; got:\n%s", string(argsData))
	}
}

// TestPushToHub_UsesTempKey verifies that PushToHub passes kp.PrivateKeyPath
// as the -i argument to authenticate to the hub.
func TestPushToHub_UsesTempKey(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	keyPath := "/tmp/fakedir/id_ed25519"
	kp := fakeTempKeyPair(keyPath)
	ctx := context.Background()

	hub := config.ResolvedHost{Host: "hub.example.com"}

	result := PushToHub(ctx, config.ResolvedHost{}, "/local/file.txt", hub, "/hub/file.txt", kp)
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsLines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	found := false
	for i := 0; i < len(argsLines)-1; i++ {
		if argsLines[i] == "-i" && argsLines[i+1] == keyPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -i %s in scp args; got:\n%s", keyPath, string(argsData))
	}
}

// ---------------------------------------------------------------------------
// fakeHubKeyPair helper
// ---------------------------------------------------------------------------

// fakeHubKeyPair returns a HubKeyPair with predetermined paths for testing
// without generating a real keypair on a real hub host.
func fakeHubKeyPair(remoteDir, remotePrivKeyPath string) *sshkeys.HubKeyPair {
	return &sshkeys.HubKeyPair{
		Hub:                  config.ResolvedHost{Host: "hub.example.com"},
		RemoteDir:            remoteDir,
		RemotePrivateKeyPath: remotePrivKeyPath,
		RemotePublicKeyPath:  remotePrivKeyPath + ".pub",
		PublicKey:            "ssh-ed25519 FAKEHUBKEY smux-distribute-hub-test",
		Comment:              "smux-distribute-hub-test",
	}
}

// injectFakeSSH installs a fake ssh shell script into a temporary directory
// that is prepended to PATH. The script exits with exitCode and writes
// stderrMsg to stderr if non-empty.
func injectFakeSSH(t *testing.T, exitCode int, stderrMsg string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := "#!/bin/sh\n"
	if stderrMsg != "" {
		script += fmt.Sprintf("printf '%%s\\n' %q >&2\n", stderrMsg)
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// ---------------------------------------------------------------------------
// FanOutFromHub tests
// ---------------------------------------------------------------------------

// TestFanOutFromHub_AllSpokesSucceed verifies that FanOutFromHub returns a
// successful CopyResult for every spoke when SSH/scp exits with code 0.
func TestFanOutFromHub_AllSpokesSucceed(t *testing.T) {
	injectFakeSSH(t, 0, "")

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
		{Host: "spoke3.example.com"},
	}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Errorf("result[%d] (%s): expected success, got err: %v", i, r.Host.Host, r.Err)
		}
		if r.Err != nil {
			t.Errorf("result[%d] (%s): expected nil Err on success, got: %v", i, r.Host.Host, r.Err)
		}
	}
}

// TestFanOutFromHub_AllFail verifies that FanOutFromHub records failures for
// every spoke when SSH exits with a non-zero code.
func TestFanOutFromHub_AllFail(t *testing.T) {
	injectFakeSSH(t, 1, "connection refused")

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
	}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Success {
			t.Errorf("result[%d] (%s): expected failure", i, r.Host.Host)
		}
		if r.Err == nil {
			t.Errorf("result[%d] (%s): expected non-nil Err on failure", i, r.Host.Host)
		}
	}
}

// TestFanOutFromHub_PartialFailure verifies that a failure for one spoke does
// not prevent other spokes from completing.
func TestFanOutFromHub_PartialFailure(t *testing.T) {
	// Fake ssh that fails for any arg containing "spoke2".
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *spoke2*)
      printf 'spoke2 unreachable\n' >&2
      exit 1
      ;;
  esac
done
exit 0
`
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
		{Host: "spoke3.example.com"},
	}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results[0].Success {
		t.Errorf("spoke1: expected success, got %v", results[0].Err)
	}
	if results[1].Success {
		t.Errorf("spoke2: expected failure")
	}
	if results[1].Err == nil {
		t.Errorf("spoke2: expected non-nil Err")
	}
	if !results[2].Success {
		t.Errorf("spoke3: expected success, got %v", results[2].Err)
	}
}

// TestFanOutFromHub_ResultOrderPreserved verifies that the result slice is
// returned in the same order as the input spokes slice, even when transfers
// complete out of order.
func TestFanOutFromHub_ResultOrderPreserved(t *testing.T) {
	// Fake ssh that delays spoke1 to encourage out-of-order completion.
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *spoke1*) sleep 0.05 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{
		{Host: "spoke1.example.com"},
		{Host: "spoke2.example.com"},
		{Host: "spoke3.example.com"},
	}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	for i, r := range results {
		if r.Host.Host != spokes[i].Host {
			t.Errorf("result[%d]: expected Host=%q, got Host=%q (order not preserved)",
				i, spokes[i].Host, r.Host.Host)
		}
	}
}

// TestFanOutFromHub_EmptySpokes verifies that FanOutFromHub returns an empty
// (non-nil) slice when given no spoke hosts.
func TestFanOutFromHub_EmptySpokes(t *testing.T) {
	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", nil, "/dest/file.txt", hubKP)

	if results == nil {
		t.Fatal("expected non-nil slice for empty spoke list, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty spoke list, got %d", len(results))
	}
}

// TestFanOutFromHub_ContextCancelled verifies that context cancellation is
// propagated and produces failure results without hanging indefinitely.
func TestFanOutFromHub_ContextCancelled(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{{Host: "spoke1.example.com"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	results := FanOutFromHub(ctx, hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Success {
		t.Error("expected failure due to cancelled context")
	}
	if results[0].Err == nil {
		t.Error("expected non-nil Err for cancelled context")
	}
}

// TestFanOutFromHub_SpokeFieldPopulatedInResult verifies that Host in each
// CopyResult is set to the corresponding spoke host.
func TestFanOutFromHub_SpokeFieldPopulatedInResult(t *testing.T) {
	injectFakeSSH(t, 0, "")

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spoke := config.ResolvedHost{Host: "spoke1.example.com", User: "alice", Port: 2222}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", []config.ResolvedHost{spoke}, "/dest/file.txt", hubKP)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0].Host
	if got.Host != spoke.Host {
		t.Errorf("Host.Host: expected %q, got %q", spoke.Host, got.Host)
	}
	if got.User != spoke.User {
		t.Errorf("Host.User: expected %q, got %q", spoke.User, got.User)
	}
	if got.Port != spoke.Port {
		t.Errorf("Host.Port: expected %d, got %d", spoke.Port, got.Port)
	}
}

// TestFanOutFromHub_SpokeFieldPopulatedOnFailure verifies that Host is
// populated even when the SSH/scp transfer fails.
func TestFanOutFromHub_SpokeFieldPopulatedOnFailure(t *testing.T) {
	injectFakeSSH(t, 1, "permission denied")

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spoke := config.ResolvedHost{Host: "spoke1.example.com"}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", []config.ResolvedHost{spoke}, "/dest/file.txt", hubKP)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Host.Host != spoke.Host {
		t.Errorf("Host.Host: expected %q even on failure, got %q", spoke.Host, results[0].Host.Host)
	}
}

// TestFanOutFromHub_StderrCaptured verifies that stderr from the SSH process
// is captured in the CopyResult on failure.
func TestFanOutFromHub_StderrCaptured(t *testing.T) {
	injectFakeSSH(t, 1, "permission denied (publickey)")

	hubKP := fakeHubKeyPair("/tmp/hub-dir", "/tmp/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{{Host: "spoke1.example.com"}}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Success {
		t.Error("expected failure for exit code 1")
	}
	if !strings.Contains(r.Stderr, "permission denied (publickey)") {
		t.Errorf("Stderr should contain the error; got: %q", r.Stderr)
	}
	if r.Err == nil {
		t.Error("expected non-nil Err")
	}
	if !strings.Contains(r.Err.Error(), "permission denied (publickey)") {
		t.Errorf("Err should mention stderr; got: %v", r.Err)
	}
}

// ---------------------------------------------------------------------------
// buildHubSCPCommand unit tests
// ---------------------------------------------------------------------------

// TestBuildHubSCPCommand_BasicCommand verifies that the scp command includes
// the required quiet flag, identity file, BatchMode, StrictHostKeyChecking,
// ConnectTimeout, source path, and destination.
func TestBuildHubSCPCommand_BasicCommand(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com", User: "alice"}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	checks := []string{
		"scp",
		"-q",
		"BatchMode=yes",
		"StrictHostKeyChecking=no",
		"ConnectTimeout=10",
		"/hub/file.txt",
		"alice@spoke.example.com:/dest/file.txt",
	}
	for _, want := range checks {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in command; got:\n%s", want, cmd)
		}
	}
}

// TestBuildHubSCPCommand_IdentityKey verifies that the remote private key
// path from HubKeyPair is passed to scp via -i.
func TestBuildHubSCPCommand_IdentityKey(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com"}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	// -i must be followed by the key path (possibly quoted).
	if !strings.Contains(cmd, "-i") {
		t.Errorf("expected -i flag in command; got:\n%s", cmd)
	}
	if !strings.Contains(cmd, "/hub-dir/id_ed25519") {
		t.Errorf("expected remote private key path in command; got:\n%s", cmd)
	}
}

// TestBuildHubSCPCommand_WithPort verifies that a non-zero spoke port is
// passed to scp via the -P flag.
func TestBuildHubSCPCommand_WithPort(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com", Port: 2222}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	if !strings.Contains(cmd, "-P") || !strings.Contains(cmd, "2222") {
		t.Errorf("expected -P 2222 in command; got:\n%s", cmd)
	}
}

// TestBuildHubSCPCommand_WithJumpHost verifies that a jump host in the spoke
// configuration is passed to scp via the -J flag.
func TestBuildHubSCPCommand_WithJumpHost(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com", JumpHost: "jump.example.com"}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	if !strings.Contains(cmd, "-J") || !strings.Contains(cmd, "jump.example.com") {
		t.Errorf("expected -J jump.example.com in command; got:\n%s", cmd)
	}
}

// TestBuildHubSCPCommand_NoUser verifies that when the spoke has no user, the
// destination argument is host:path without a user@ prefix.
func TestBuildHubSCPCommand_NoUser(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com"} // no user

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	if !strings.Contains(cmd, "spoke.example.com:/dest/file.txt") {
		t.Errorf("expected host:path destination (no user@) in command; got:\n%s", cmd)
	}
	if strings.Contains(cmd, "@spoke.example.com") {
		t.Errorf("should not include user@ when user is empty; got:\n%s", cmd)
	}
}

// TestBuildHubSCPCommand_NoPortWhenZero verifies that the -P flag is omitted
// when the spoke port is zero (default port 22).
func TestBuildHubSCPCommand_NoPortWhenZero(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com", Port: 0}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	if strings.Contains(cmd, "-P") {
		t.Errorf("should not include -P when port is 0; got:\n%s", cmd)
	}
}

// TestBuildHubSCPCommand_NoJumpHostWhenEmpty verifies that the -J flag is
// omitted when the spoke has no jump host configured.
func TestBuildHubSCPCommand_NoJumpHostWhenEmpty(t *testing.T) {
	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	spoke := config.ResolvedHost{Host: "spoke.example.com", JumpHost: ""}

	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dest/file.txt", hubKP)

	if strings.Contains(cmd, "-J") {
		t.Errorf("should not include -J when jump host is empty; got:\n%s", cmd)
	}
}

// TestFanOutFromHub_HubSSHArgsUsed verifies that FanOutFromHub connects to the
// hub (not directly to the spoke) to run the scp command. The fake ssh script
// records its args to a file so we can confirm the hub's host appears in them.
func TestFanOutFromHub_HubSSHArgsUsed(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	hubKP := fakeHubKeyPair("/hub-dir", "/hub-dir/id_ed25519")
	hub := config.ResolvedHost{Host: "hub.example.com", User: "hubuser"}
	spokes := []config.ResolvedHost{{Host: "spoke1.example.com"}}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("expected 1 successful result, got: %+v", results)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsText := string(argsData)

	// The SSH connection should target the hub, not the spoke.
	if !strings.Contains(argsText, "hub.example.com") {
		t.Errorf("expected hub hostname in SSH args; got:\n%s", argsText)
	}
	// The remote scp command should mention the spoke as destination.
	if !strings.Contains(argsText, "spoke1.example.com") {
		t.Errorf("expected spoke hostname in the scp command passed to SSH; got:\n%s", argsText)
	}
}

// TestFanOutFromHub_RemoteKeyPassedToSCP verifies that the hub's remote
// private key path appears in the scp command executed on the hub.
func TestFanOutFromHub_RemoteKeyPassedToSCP(t *testing.T) {
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nexit 0\n", argsFile)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	remoteKeyPath := "/tmp/hub-keys/id_ed25519"
	hubKP := fakeHubKeyPair("/tmp/hub-keys", remoteKeyPath)
	hub := config.ResolvedHost{Host: "hub.example.com"}
	spokes := []config.ResolvedHost{{Host: "spoke1.example.com"}}

	results := FanOutFromHub(context.Background(), hub, "/hub/file.txt", spokes, "/dest/file.txt", hubKP)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("expected 1 successful result, got: %+v", results)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	if !strings.Contains(string(argsData), remoteKeyPath) {
		t.Errorf("expected remote key path %q in SSH args; got:\n%s", remoteKeyPath, string(argsData))
	}
}
