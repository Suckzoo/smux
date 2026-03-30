// Package filetree provides remote and local filesystem enumeration
// for the distribute-file wizard.
//
// Remote directory listing tries the SFTP subsystem first (via the local
// sftp(1) binary in batch mode) and falls back to running ls over a plain
// SSH shell when the SFTP subsystem is unavailable.
package filetree

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Suckzoo/smux/internal/config"
)

// FileEntry describes a single item returned by a directory listing.
type FileEntry struct {
	// Name is the base filename (not the full path).
	Name string
	// IsDir is true when the entry is a directory (or a symlink to one).
	IsDir bool
	// Size is the file size in bytes. It may be zero for directories.
	Size int64
}

// RemoteListDir lists the contents of path on the given host.
//
// It first attempts to use the SFTP subsystem by spawning sftp(1) in batch
// mode. If the SFTP attempt fails (binary not found, connection refused,
// subsystem not enabled, etc.) it falls back to executing ls -la over a
// regular SSH shell.
//
// Entries for "." and ".." are always excluded from the returned slice.
//
// An error is returned only when both methods fail; in that case the error
// wraps the SSH-fallback error.
func RemoteListDir(ctx context.Context, host config.ResolvedHost, path string) ([]FileEntry, error) {
	if path == "" {
		path = "."
	}

	// --- attempt 1: SFTP subsystem ---
	entries, sftpErr := listViaSFTP(ctx, host, path)
	if sftpErr == nil {
		return entries, nil
	}

	// --- attempt 2: SSH shell ls fallback ---
	entries, sshErr := listViaSSH(ctx, host, path)
	if sshErr == nil {
		return entries, nil
	}

	return nil, fmt.Errorf("remote list %q on %s: sftp: %w; ssh fallback: %v", path, host.Host, sftpErr, sshErr)
}

// -----------------------------------------------------------------------
// SFTP implementation
// -----------------------------------------------------------------------

// listViaSFTP spawns sftp(1) in quiet batch mode, sends a single "ls -la
// <path>" command, and parses the long-listing output.
//
// The sftp binary is expected to be on PATH. ConnectTimeout and BatchMode
// are passed via -o SSH options so the call fails fast when the subsystem
// is not available.
func listViaSFTP(ctx context.Context, host config.ResolvedHost, path string) ([]FileEntry, error) {
	args := buildSFTPArgs(host)
	cmd := exec.CommandContext(ctx, "sftp", args...)

	// Batch-mode input: list the requested directory then quit.
	batchInput := fmt.Sprintf("ls -la %s\n", shellescape(path))
	cmd.Stdin = strings.NewReader(batchInput)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sftp run: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	entries, err := parseLsOutput(stdout.String())
	if err != nil {
		return nil, fmt.Errorf("sftp parse: %w", err)
	}
	return entries, nil
}

// buildSFTPArgs constructs the argument slice for sftp(1).
// Flags mirror what buildSSHArgs does for ssh(1) but use sftp's syntax.
func buildSFTPArgs(host config.ResolvedHost) []string {
	// Common SSH options passed via -o.
	args := []string{
		"-q",              // quiet: suppress banner/progress
		"-b", "-",        // batch mode from stdin
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
	}
	if host.Port != 0 {
		args = append(args, "-P", strconv.Itoa(host.Port))
	}
	if host.Key != "" {
		args = append(args, "-i", host.Key)
	}
	if host.JumpHost != "" {
		args = append(args, "-J", host.JumpHost)
	}

	// Destination: [user@]host
	dest := host.EffectiveAddress()
	if host.User != "" {
		dest = host.User + "@" + host.EffectiveAddress()
	}
	args = append(args, dest)
	return args
}

// -----------------------------------------------------------------------
// SSH shell fallback
// -----------------------------------------------------------------------

// listViaSSH runs "ls -la <path>" on the remote host over a plain SSH
// connection and parses the output.
func listViaSSH(ctx context.Context, host config.ResolvedHost, path string) ([]FileEntry, error) {
	args := buildSSHArgs(host)
	// Remote command: list the directory in long format.
	// Use -- to prevent the path from being misinterpreted as a flag.
	remoteCmd := fmt.Sprintf("ls -la -- %s", shellescape(path))
	args = append(args, "--", remoteCmd)

	cmd := exec.CommandContext(ctx, "ssh", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh run: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	entries, err := parseLsOutput(stdout.String())
	if err != nil {
		return nil, fmt.Errorf("ssh parse: %w", err)
	}
	return entries, nil
}

// buildSSHArgs returns the argument slice for ssh(1), excluding the remote
// command. Appending the remote command is the caller's responsibility.
func buildSSHArgs(host config.ResolvedHost) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
	}
	if host.User != "" {
		args = append(args, "-l", host.User)
	}
	if host.Port != 0 {
		args = append(args, "-p", strconv.Itoa(host.Port))
	}
	if host.Key != "" {
		args = append(args, "-i", host.Key)
	}
	if host.JumpHost != "" {
		args = append(args, "-J", host.JumpHost)
	}
	args = append(args, host.EffectiveAddress())
	return args
}

// -----------------------------------------------------------------------
// Output parsing
// -----------------------------------------------------------------------

// parseLsOutput parses the output of "ls -la" (or sftp "ls -la") and
// returns one FileEntry per non-dot entry.
//
// The expected line format is:
//
//	<perms>  <links>  <user>  <group>  <size>  <month>  <day>  <time>  <name>
//
// Lines that don't match this pattern (e.g. "total 42" or blank lines)
// are silently skipped. Entries named "." and ".." are excluded.
func parseLsOutput(output string) ([]FileEntry, error) {
	var entries []FileEntry
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		entry, ok := parseLsLine(line)
		if !ok {
			continue
		}
		if entry.Name == "." || entry.Name == ".." {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// parseLsLine parses a single line of ls -la output.
//
// It is intentionally lenient: if the line does not have at least 9
// whitespace-separated fields it returns (FileEntry{}, false).
//
// The file type is derived from the first character of the permissions
// column:
//   - 'd' → directory
//   - 'l' → symlink (reported as directory=false; callers may follow)
//   - anything else → regular file
//
// The size field (column 5, 0-indexed) is parsed; on failure the size
// is left as zero.
func parseLsLine(line string) (FileEntry, bool) {
	// Skip empty lines and sftp prompt lines ("sftp>").
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "sftp>") {
		return FileEntry{}, false
	}

	// Skip "total N" lines emitted by ls.
	if strings.HasPrefix(line, "total ") {
		return FileEntry{}, false
	}

	// Split on whitespace; need at least 9 fields.
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return FileEntry{}, false
	}

	perms := fields[0]
	if len(perms) == 0 {
		return FileEntry{}, false
	}

	isDir := perms[0] == 'd'

	// Size is the 5th field (index 4).
	size, _ := strconv.ParseInt(fields[4], 10, 64)

	// The filename is everything from field 8 onwards, joined with a
	// single space. This handles names that contain spaces.
	// For symlinks the name includes " -> target"; we strip that suffix.
	name := strings.Join(fields[8:], " ")
	if perms[0] == 'l' {
		// Symlink: strip " -> <target>".
		if idx := strings.Index(name, " -> "); idx >= 0 {
			name = name[:idx]
		}
	}

	return FileEntry{
		Name:  name,
		IsDir: isDir,
		Size:  size,
	}, true
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// shellescape wraps path in single quotes and escapes any embedded single
// quotes so the path can be used safely in a remote shell command.
//
// This is a minimal implementation suitable for the common case of paths
// that do not contain NUL bytes. Embedded single quotes are replaced with
// the classic '\'' idiom.
func shellescape(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// BuildSSHArgsForHost is the exported form of buildSSHArgs, provided so
// that other packages in the distribute-file flow (e.g. the copy executor)
// can build consistent SSH argument slices from a ResolvedHost.
func BuildSSHArgsForHost(host config.ResolvedHost) []string {
	return buildSSHArgs(host)
}
