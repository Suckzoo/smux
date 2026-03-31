package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// parseIPv4Addresses tests
// ---------------------------------------------------------------------------

func TestParseIPv4Addresses(t *testing.T) {
	output := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 10.61.2.36/24 brd 10.61.2.255 scope global eth0\       valid_lft forever preferred_lft forever
3: docker0    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0\       valid_lft forever preferred_lft forever`

	got := parseIPv4Addresses(output)
	want := []string{"127.0.0.1", "10.61.2.36", "172.17.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseIPv4Addresses:\n  got  %v\n  want %v", got, want)
	}
}

func TestParseIPv4Addresses_Empty(t *testing.T) {
	got := parseIPv4Addresses("")
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ResolvePrivateIP tests
// ---------------------------------------------------------------------------

func TestResolvePrivateIP_AlreadyHasInternalIP(t *testing.T) {
	host := config.ResolvedHost{
		Host:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
	}
	ip, err := ResolvePrivateIP(context.Background(), host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.1" {
		t.Errorf("got %q, want %q", ip, "10.0.0.1")
	}
}

func TestResolvePrivateIP_NoCIDR(t *testing.T) {
	host := config.ResolvedHost{
		Host: "example.com",
	}
	ip, err := ResolvePrivateIP(context.Background(), host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := host.RemoteReachableAddress()
	if ip != want {
		t.Errorf("got %q, want %q", ip, want)
	}
}

func TestResolvePrivateIP_CIDRMatch(t *testing.T) {
	ipOutput := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 10.61.2.36/24 brd 10.61.2.255 scope global eth0\       valid_lft forever preferred_lft forever
3: docker0    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0\       valid_lft forever preferred_lft forever`

	injectFakeSSHWithOutput(t, 0, ipOutput, "")

	host := config.ResolvedHost{
		Host:         "spoke-01.example.com",
		InternalCIDR: "10.61.2.0/24",
	}

	ip, err := ResolvePrivateIP(context.Background(), host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.61.2.36" {
		t.Errorf("got %q, want %q", ip, "10.61.2.36")
	}
}

func TestResolvePrivateIP_NoCIDRMatch(t *testing.T) {
	ipOutput := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 10.61.2.36/24 brd 10.61.2.255 scope global eth0\       valid_lft forever preferred_lft forever`

	injectFakeSSHWithOutput(t, 0, ipOutput, "")

	host := config.ResolvedHost{
		Host:         "spoke-01.example.com",
		InternalCIDR: "192.168.1.0/24",
	}

	_, err := ResolvePrivateIP(context.Background(), host)
	if err == nil {
		t.Fatal("expected error when no IP matches CIDR, got nil")
	}
	if !strings.Contains(err.Error(), "no IP matching CIDR") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// injectFakeSSHWithOutput installs a fake ssh shell script into a temporary
// directory prepended to PATH. The script writes stdoutContent to stdout,
// stderrContent to stderr, and exits with exitCode.
func injectFakeSSHWithOutput(t *testing.T, exitCode int, stdoutContent, stderrContent string) {
	t.Helper()
	fakeDir := t.TempDir()
	fakeSSH := filepath.Join(fakeDir, "ssh")
	script := "#!/bin/sh\n"
	if stdoutContent != "" {
		script += fmt.Sprintf("printf '%%s' %s\n", shellQuote(stdoutContent))
	}
	if stderrContent != "" {
		script += fmt.Sprintf("printf '%%s\\n' %s >&2\n", shellQuote(stderrContent))
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
