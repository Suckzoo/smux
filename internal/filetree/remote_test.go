package filetree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// parseLsLine unit tests
// ---------------------------------------------------------------------------

func TestParseLsLine_RegularFile(t *testing.T) {
	line := `-rw-r--r--  1 alice staff  1234 Jan  1 00:00 README.md`
	entry, ok := parseLsLine(line)
	if !ok {
		t.Fatal("expected parseLsLine to succeed")
	}
	if entry.Name != "README.md" {
		t.Errorf("Name: got %q, want %q", entry.Name, "README.md")
	}
	if entry.IsDir {
		t.Error("IsDir: expected false for regular file")
	}
	if entry.Size != 1234 {
		t.Errorf("Size: got %d, want 1234", entry.Size)
	}
}

func TestParseLsLine_Directory(t *testing.T) {
	line := `drwxr-xr-x  5 alice staff   160 Jan  1 00:00 subdir`
	entry, ok := parseLsLine(line)
	if !ok {
		t.Fatal("expected parseLsLine to succeed")
	}
	if entry.Name != "subdir" {
		t.Errorf("Name: got %q, want %q", entry.Name, "subdir")
	}
	if !entry.IsDir {
		t.Error("IsDir: expected true for directory")
	}
}

func TestParseLsLine_Symlink(t *testing.T) {
	line := `lrwxrwxrwx  1 alice staff    7 Jan  1 00:00 link -> target`
	entry, ok := parseLsLine(line)
	if !ok {
		t.Fatal("expected parseLsLine to succeed")
	}
	if entry.Name != "link" {
		t.Errorf("Name: got %q, want %q after stripping arrow", entry.Name, "link")
	}
	if entry.IsDir {
		t.Error("IsDir: symlink should not be reported as dir")
	}
}

func TestParseLsLine_NameWithSpaces(t *testing.T) {
	line := `-rw-r--r--  1 alice staff  42 Jan  2 08:00 my file with spaces.txt`
	entry, ok := parseLsLine(line)
	if !ok {
		t.Fatal("expected parseLsLine to succeed")
	}
	if entry.Name != "my file with spaces.txt" {
		t.Errorf("Name: got %q, want %q", entry.Name, "my file with spaces.txt")
	}
}

func TestParseLsLine_TotalLine(t *testing.T) {
	_, ok := parseLsLine("total 42")
	if ok {
		t.Error("expected parseLsLine to return false for 'total' line")
	}
}

func TestParseLsLine_EmptyLine(t *testing.T) {
	_, ok := parseLsLine("")
	if ok {
		t.Error("expected parseLsLine to return false for empty line")
	}
}

func TestParseLsLine_SFTPPromptLine(t *testing.T) {
	_, ok := parseLsLine("sftp> ls -la /tmp")
	if ok {
		t.Error("expected parseLsLine to return false for sftp prompt line")
	}
}

func TestParseLsLine_TooFewFields(t *testing.T) {
	_, ok := parseLsLine("drwxr-xr-x  5 alice staff")
	if ok {
		t.Error("expected parseLsLine to return false for line with < 9 fields")
	}
}

// ---------------------------------------------------------------------------
// parseLsOutput unit tests
// ---------------------------------------------------------------------------

func TestParseLsOutput_SkipsDotAndDotDot(t *testing.T) {
	output := `total 16
drwxr-xr-x  3 alice staff   96 Jan  1 00:00 .
drwxr-xr-x  5 alice staff  160 Jan  1 00:00 ..
-rw-r--r--  1 alice staff 1234 Jan  1 00:00 foo.txt
drwxr-xr-x  2 alice staff   64 Jan  1 00:00 bar
`
	entries, err := parseLsOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (foo.txt + bar), got %d: %v", len(entries), entries)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["foo.txt"] {
		t.Error("expected foo.txt in results")
	}
	if !names["bar"] {
		t.Error("expected bar in results")
	}
}

func TestParseLsOutput_Empty(t *testing.T) {
	entries, err := parseLsOutput("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no entries for empty output, got %d", len(entries))
	}
}

func TestParseLsOutput_SFTPBatchOutputWithPrompts(t *testing.T) {
	// sftp batch mode may include prompt lines; they should be ignored.
	output := `sftp> ls -la /home/alice
total 8
drwxr-xr-x  2 alice staff  64 Jan  1 00:00 .
drwxr-xr-x  4 root  wheel 128 Jan  1 00:00 ..
-rw-r--r--  1 alice staff  42 Jan  1 00:00 .profile
`
	entries, err := parseLsOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (.profile), got %d: %v", len(entries), entries)
	}
	if entries[0].Name != ".profile" {
		t.Errorf("expected .profile, got %q", entries[0].Name)
	}
}

// ---------------------------------------------------------------------------
// shellescape unit tests
// ---------------------------------------------------------------------------

func TestShellescape_Plain(t *testing.T) {
	got := shellescape("/home/alice/docs")
	want := "'/home/alice/docs'"
	if got != want {
		t.Errorf("shellescape: got %q, want %q", got, want)
	}
}

func TestShellescape_WithSingleQuote(t *testing.T) {
	got := shellescape("/home/alice/it's here")
	// The embedded ' becomes '\''
	want := `'/home/alice/it'\''s here'`
	if got != want {
		t.Errorf("shellescape: got %q, want %q", got, want)
	}
}

func TestShellescape_WithSpaces(t *testing.T) {
	got := shellescape("/path/with spaces/file")
	want := "'/path/with spaces/file'"
	if got != want {
		t.Errorf("shellescape: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// buildSSHArgs unit tests
// ---------------------------------------------------------------------------

func TestBuildSSHArgs_Minimal(t *testing.T) {
	host := config.ResolvedHost{Host: "example.com"}
	args := buildSSHArgs(host)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "example.com") {
		t.Errorf("expected hostname in args: %q", joined)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Errorf("expected BatchMode=yes in args: %q", joined)
	}
}

func TestBuildSSHArgs_WithUserPortKey(t *testing.T) {
	host := config.ResolvedHost{
		Host: "server.example.com",
		User: "deploy",
		Port: 2222,
		Key:  "~/.ssh/deploy_key",
	}
	args := buildSSHArgs(host)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-l deploy") {
		t.Errorf("expected -l deploy in args: %q", joined)
	}
	if !strings.Contains(joined, "-p 2222") {
		t.Errorf("expected -p 2222 in args: %q", joined)
	}
	if !strings.Contains(joined, "-i ~/.ssh/deploy_key") {
		t.Errorf("expected -i in args: %q", joined)
	}
}

func TestBuildSSHArgs_WithJumpHost(t *testing.T) {
	host := config.ResolvedHost{
		Host:     "internal.example.com",
		JumpHost: "bastion.example.com",
	}
	args := buildSSHArgs(host)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-J bastion.example.com") {
		t.Errorf("expected -J bastion.example.com in args: %q", joined)
	}
}

func TestBuildSFTPArgs_Minimal(t *testing.T) {
	host := config.ResolvedHost{Host: "example.com"}
	args := buildSFTPArgs(host)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "example.com") {
		t.Errorf("expected hostname in sftp args: %q", joined)
	}
	if !strings.Contains(joined, "-q") {
		t.Errorf("expected -q quiet flag in sftp args: %q", joined)
	}
	if !strings.Contains(joined, "-b -") {
		t.Errorf("expected -b - batch flag in sftp args: %q", joined)
	}
}

func TestBuildSFTPArgs_WithUserAndPort(t *testing.T) {
	host := config.ResolvedHost{
		Host: "files.example.com",
		User: "transfer",
		Port: 22,
	}
	args := buildSFTPArgs(host)
	joined := strings.Join(args, " ")
	// sftp uses "user@host" destination syntax and -P for port.
	if !strings.Contains(joined, "transfer@files.example.com") {
		t.Errorf("expected user@host in sftp args: %q", joined)
	}
	if !strings.Contains(joined, "-P 22") {
		t.Errorf("expected -P 22 in sftp args: %q", joined)
	}
}

// ---------------------------------------------------------------------------
// Integration test: RemoteListDir against localhost (skipped unless SSH is available)
// ---------------------------------------------------------------------------

// TestRemoteListDir_Localhost is an integration test that enumerates a
// temporary local directory via localhost SSH/SFTP.  It requires:
//   - A running SSH daemon on localhost port 22.
//   - The current user able to SSH to localhost without a password (key auth).
//
// The test is skipped when these conditions are not met.
func TestRemoteListDir_Localhost(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not found in PATH")
	}

	// Quick check: can we SSH to localhost?
	ctx := context.Background()
	probe := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=3",
		"localhost", "--", "true",
	)
	if err := probe.Run(); err != nil {
		t.Skipf("cannot SSH to localhost (BatchMode): %v", err)
	}

	// Create a temporary directory with known contents.
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("setup: write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "subdir"), 0o755); err != nil {
		t.Fatalf("setup: mkdir: %v", err)
	}

	host := config.ResolvedHost{Host: "localhost"}
	entries, err := RemoteListDir(ctx, host, tmpDir)
	if err != nil {
		t.Fatalf("RemoteListDir: %v", err)
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}

	if !names["hello.txt"] {
		t.Errorf("expected hello.txt in results; got: %v", names)
	}
	if !names["subdir"] {
		t.Errorf("expected subdir in results; got: %v", names)
	}

	// Verify IsDir is set correctly for the subdir.
	for _, e := range entries {
		if e.Name == "subdir" && !e.IsDir {
			t.Error("subdir should have IsDir=true")
		}
		if e.Name == "hello.txt" && e.IsDir {
			t.Error("hello.txt should have IsDir=false")
		}
	}
}

// TestRemoteListDir_SFTPFallsBackToSSH verifies the fallback behaviour by
// injecting a fake sftp binary that always exits with code 1.
func TestRemoteListDir_SFTPFallsBackToSSH(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not found in PATH")
	}

	// Quick connectivity check.
	ctx := context.Background()
	probe := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=3",
		"localhost", "--", "true",
	)
	if err := probe.Run(); err != nil {
		t.Skipf("cannot SSH to localhost: %v", err)
	}

	// Create a fake sftp binary that exits non-zero.
	fakeDir := t.TempDir()
	fakeSFTP := filepath.Join(fakeDir, "sftp")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeSFTP, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sftp: %v", err)
	}

	// Prepend fakeDir to PATH so the fake sftp is found first.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	// Use a temp dir with a known file.
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "fallback.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	host := config.ResolvedHost{Host: "localhost"}
	entries, err := RemoteListDir(ctx, host, tmpDir)
	if err != nil {
		t.Fatalf("RemoteListDir (with fake sftp): %v", err)
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["fallback.txt"] {
		t.Errorf("expected fallback.txt via SSH fallback; got: %v", names)
	}
}
