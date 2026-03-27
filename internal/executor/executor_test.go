package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// fakeTempKeyPair returns a TempKeyPair with a predetermined private key path
// for unit-testing argument construction without generating a real keypair.
func fakeTempKeyPair(privKeyPath string) *sshkeys.TempKeyPair {
	return &sshkeys.TempKeyPair{
		Dir:            filepath.Dir(privKeyPath),
		PrivateKeyPath: privKeyPath,
		PublicKeyPath:  privKeyPath + ".pub",
		PublicKey:      "ssh-ed25519 FAKEKEY smux-distribute-test",
		Comment:        "smux-distribute-test",
	}
}

// injectFakeSCP installs a fake scp shell script into a temporary directory
// that is prepended to PATH. The script exits with exitCode and writes
// stderrMsg to stderr if non-empty.
func injectFakeSCP(t *testing.T, exitCode int, stderrMsg string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	script := "#!/bin/sh\n"
	if stderrMsg != "" {
		// Use printf to avoid echo -e portability issues.
		script += fmt.Sprintf("printf '%%s\\n' %q >&2\n", stderrMsg)
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// contains reports whether s appears anywhere in slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// hasConsecutive reports whether key immediately precedes val in slice.
func hasConsecutive(slice []string, key, val string) bool {
	for i := 0; i < len(slice)-1; i++ {
		if slice[i] == key && slice[i+1] == val {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// buildSCPArgs unit tests
// ---------------------------------------------------------------------------

func TestBuildSCPArgs_LocalSource_BasicArgs(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{} // local — Host is empty
	dst := config.ResolvedHost{Host: "dest.example.com", User: "alice"}

	args := buildSCPArgs(src, "/local/file.txt", dst, "/remote/dest.txt", kp)

	argStr := strings.Join(args, " ")

	// Must NOT include three-way flag for local source.
	if contains(args, "-3") {
		t.Error("local source should not include -3 flag")
	}

	// Must pass the private key.
	if !hasConsecutive(args, "-i", kp.PrivateKeyPath) {
		t.Errorf("args should contain -i %s; got: %s", kp.PrivateKeyPath, argStr)
	}

	// Source must be the local path (no host: prefix).
	if !contains(args, "/local/file.txt") {
		t.Errorf("args should contain local source path; got: %s", argStr)
	}

	// Destination must be user@host:path.
	expectedDst := "alice@dest.example.com:/remote/dest.txt"
	if !contains(args, expectedDst) {
		t.Errorf("args should contain destination %q; got: %s", expectedDst, argStr)
	}
}

func TestBuildSCPArgs_LocalSource_WithPort(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com", Port: 2222}

	args := buildSCPArgs(src, "/file.txt", dst, "/dst/file.txt", kp)

	if !hasConsecutive(args, "-P", "2222") {
		t.Errorf("args should contain -P 2222; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_LocalSource_WithJumpHost(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com", JumpHost: "jump.example.com"}

	args := buildSCPArgs(src, "/file.txt", dst, "/dst/file.txt", kp)

	if !hasConsecutive(args, "-J", "jump.example.com") {
		t.Errorf("args should contain -J jump.example.com; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_LocalSource_NoUser_DestFormat(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com"} // no user

	args := buildSCPArgs(src, "/file.txt", dst, "/dst/file.txt", kp)

	// Destination should be host:path without user@ prefix.
	expectedDst := "dest.example.com:/dst/file.txt"
	if !contains(args, expectedDst) {
		t.Errorf("args should contain %q (no user prefix); got: %s", expectedDst, strings.Join(args, " "))
	}
	// Must not contain user@host format.
	if contains(args, "@dest.example.com:/dst/file.txt") {
		t.Errorf("args should not contain user@ prefix when user is empty; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_RemoteSource_IncludesThreeWayFlag(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{Host: "src.example.com", User: "bob"}
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/remote/src.txt", dst, "/remote/dst.txt", kp)

	if !contains(args, "-3") {
		t.Errorf("remote source should include -3 flag; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_RemoteSource_SourceAddressWithUser(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{Host: "src.example.com", User: "bob"}
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/remote/src.txt", dst, "/remote/dst.txt", kp)

	// Source argument must be user@host:path.
	expectedSrc := "bob@src.example.com:/remote/src.txt"
	if !contains(args, expectedSrc) {
		t.Errorf("args should contain source %q; got: %s", expectedSrc, strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_RemoteSource_SourceAddressWithoutUser(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{Host: "src.example.com"} // no user
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/src.txt", dst, "/dst.txt", kp)

	// Source argument must be host:path (no user@ prefix).
	expectedSrc := "src.example.com:/src.txt"
	if !contains(args, expectedSrc) {
		t.Errorf("args should contain source %q; got: %s", expectedSrc, strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_BatchModeAndStrictHostKeyChecking(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/file.txt", dst, "/file.txt", kp)

	if !hasConsecutive(args, "-o", "BatchMode=yes") {
		t.Errorf("args should contain -o BatchMode=yes; got: %s", strings.Join(args, " "))
	}
	if !hasConsecutive(args, "-o", "StrictHostKeyChecking=no") {
		t.Errorf("args should contain -o StrictHostKeyChecking=no; got: %s", strings.Join(args, " "))
	}
	if !hasConsecutive(args, "-o", "ConnectTimeout=10") {
		t.Errorf("args should contain -o ConnectTimeout=10; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_QuietFlag(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/file.txt", dst, "/file.txt", kp)

	if !contains(args, "-q") {
		t.Errorf("args should contain -q flag; got: %s", strings.Join(args, " "))
	}
}

func TestBuildSCPArgs_SourceBeforeDest(t *testing.T) {
	kp := fakeTempKeyPair("/tmp/fakedir/id_ed25519")
	src := config.ResolvedHost{}
	dst := config.ResolvedHost{Host: "dest.example.com"}

	args := buildSCPArgs(src, "/local/src.txt", dst, "/remote/dst.txt", kp)

	srcIdx := -1
	dstIdx := -1
	for i, a := range args {
		if a == "/local/src.txt" {
			srcIdx = i
		}
		if a == "dest.example.com:/remote/dst.txt" {
			dstIdx = i
		}
	}
	if srcIdx < 0 {
		t.Fatal("source path not found in args")
	}
	if dstIdx < 0 {
		t.Fatal("destination path not found in args")
	}
	if srcIdx >= dstIdx {
		t.Errorf("source arg (idx %d) must come before destination arg (idx %d)", srcIdx, dstIdx)
	}
}

// ---------------------------------------------------------------------------
// RunParallel integration tests using fake scp binary
// ---------------------------------------------------------------------------

func TestRunParallel_AllSucceed(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	src := config.ResolvedHost{}
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
		{Host: "host3.example.com"},
	}

	results := RunParallel(ctx, src, "/src/file.txt", dests, "/dst/file.txt", kp)

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

func TestRunParallel_AllFail(t *testing.T) {
	injectFakeSCP(t, 1, "connection refused")

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	src := config.ResolvedHost{}
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
	}

	results := RunParallel(ctx, src, "/src/file.txt", dests, "/dst/file.txt", kp)

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

func TestRunParallel_PartialFailure(t *testing.T) {
	// Fake scp that fails for any argument containing "host2".
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *host2*)
      printf 'host2 unreachable\n' >&2
      exit 1
      ;;
  esac
done
exit 0
`
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	src := config.ResolvedHost{}
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
		{Host: "host3.example.com"},
	}

	results := RunParallel(ctx, src, "/src/file.txt", dests, "/dst/file.txt", kp)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results[0].Success {
		t.Errorf("host1: expected success, got %v", results[0].Err)
	}
	if results[1].Success {
		t.Errorf("host2: expected failure")
	}
	if results[1].Err == nil {
		t.Errorf("host2: expected non-nil Err")
	}
	if !results[2].Success {
		t.Errorf("host3: expected success, got %v", results[2].Err)
	}
}

func TestRunParallel_ResultOrderPreserved(t *testing.T) {
	// Fake scp that deliberately delays host1 to encourage out-of-order
	// completion. The result slice must still be ordered as the input.
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *host1*) sleep 0.05 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	src := config.ResolvedHost{}
	dests := []config.ResolvedHost{
		{Host: "host1.example.com"},
		{Host: "host2.example.com"},
		{Host: "host3.example.com"},
	}

	results := RunParallel(ctx, src, "/src/file.txt", dests, "/dst/file.txt", kp)

	for i, r := range results {
		if r.Host.Host != dests[i].Host {
			t.Errorf("result[%d]: expected Host=%q, got Host=%q (order not preserved)",
				i, dests[i].Host, r.Host.Host)
		}
	}
}

func TestRunParallel_EmptyDestHosts(t *testing.T) {
	// No fake scp needed; nothing is executed.
	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()

	results := RunParallel(ctx, config.ResolvedHost{}, "/src/file.txt", nil, "/dst/file.txt", kp)

	if results == nil {
		t.Fatal("expected non-nil slice for empty dest list, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty dest list, got %d", len(results))
	}
}

func TestRunParallel_ContextCancelled(t *testing.T) {
	// Fake scp that hangs; context cancellation should unblock RunParallel.
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	if err := os.WriteFile(fakeSCP, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before the transfers start

	dests := []config.ResolvedHost{{Host: "host1.example.com"}}
	results := RunParallel(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Success {
		t.Errorf("expected failure due to cancelled context")
	}
	if results[0].Err == nil {
		t.Errorf("expected non-nil Err for cancelled context")
	}
}

func TestRunParallel_StderrCapturedInResult(t *testing.T) {
	injectFakeSCP(t, 1, "permission denied (publickey)")

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	dests := []config.ResolvedHost{{Host: "host1.example.com"}}

	results := RunParallel(ctx, config.ResolvedHost{}, "/src/file.txt", dests, "/dst/file.txt", kp)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Success {
		t.Error("expected failure for exit code 1")
	}
	if !strings.Contains(r.Stderr, "permission denied (publickey)") {
		t.Errorf("Stderr should contain the error message; got: %q", r.Stderr)
	}
	if r.Err == nil {
		t.Error("expected non-nil Err for failed scp")
	}
	if !strings.Contains(r.Err.Error(), "permission denied (publickey)") {
		t.Errorf("Err should mention the stderr; got: %v", r.Err)
	}
}

func TestRunParallel_HostFieldPopulatedInResult(t *testing.T) {
	injectFakeSCP(t, 0, "")

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	dst := config.ResolvedHost{Host: "myhost.example.com", User: "carol", Port: 2222}

	results := RunParallel(ctx, config.ResolvedHost{}, "/src/file.txt", []config.ResolvedHost{dst}, "/dst/file.txt", kp)

	if len(results) != 1 {
		t.Fatalf("expected 1 result")
	}
	got := results[0].Host
	if got.Host != dst.Host {
		t.Errorf("Host.Host: expected %q, got %q", dst.Host, got.Host)
	}
	if got.User != dst.User {
		t.Errorf("Host.User: expected %q, got %q", dst.User, got.User)
	}
	if got.Port != dst.Port {
		t.Errorf("Host.Port: expected %d, got %d", dst.Port, got.Port)
	}
}

func TestRunParallel_RemoteSource_ThreeWayArgs(t *testing.T) {
	// Verify that when source is remote, the fake scp receives -3 in its args.
	fakeDir := t.TempDir()
	fakeSCP := filepath.Join(fakeDir, "scp")
	// This script prints its args to a temp file so we can inspect them.
	argsFile := filepath.Join(fakeDir, "args.txt")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %s
exit 0
`, argsFile)
	if err := os.WriteFile(fakeSCP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake scp: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	kp := fakeTempKeyPair("/nonexistent/key")
	ctx := context.Background()
	src := config.ResolvedHost{Host: "src.example.com", User: "srcuser"}
	dst := config.ResolvedHost{Host: "dst.example.com"}

	results := RunParallel(ctx, src, "/remote/src.txt", []config.ResolvedHost{dst}, "/remote/dst.txt", kp)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("expected 1 successful result, got: %+v", results)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argsText := string(argsData)

	if !strings.Contains(argsText, "-3") {
		t.Errorf("remote source: expected -3 in scp args; recorded args:\n%s", argsText)
	}
	if !strings.Contains(argsText, "srcuser@src.example.com:/remote/src.txt") {
		t.Errorf("remote source: expected source address in args; recorded args:\n%s", argsText)
	}
}
