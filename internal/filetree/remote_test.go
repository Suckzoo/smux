package filetree

import (
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

// TestBuildSSHArgsForHost_UsesHost verifies that BuildSSHArgsForHost uses
// host.Host (the SSH alias) as the target address, even when InternalIP is set.
// EffectiveAddress() always returns Host; internal IPs are for spoke-pull only.
func TestBuildSSHArgsForHost_UsesHost(t *testing.T) {
	host := config.ResolvedHost{
		Host:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
	}
	args := BuildSSHArgsForHost(host)
	last := args[len(args)-1]
	if last != "spoke-01.example.com" {
		t.Errorf("BuildSSHArgsForHost: last arg (host) = %q, want %q", last, "spoke-01.example.com")
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

