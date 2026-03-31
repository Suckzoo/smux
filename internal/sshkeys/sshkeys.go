// Package sshkeys manages temporary SSH keypairs for the distribute-file wizard.
//
// A fresh Ed25519 keypair is generated per distribute operation (and per
// retry). The public key is appended to ~/.ssh/authorized_keys on each
// destination host using that host's existing SSH credentials. After the
// file transfer completes, the temporary key is removed from every host's
// authorized_keys file.
//
// If remote cleanup fails for a host, the host is recorded in the provided
// dirtystate.State so that a future cleanup pass can retry. The caller is
// responsible for persisting the dirty state (dirtystate.Save) when the
// returned State is non-empty.
//
// Temporary keypair files are always stored in a process-owned temp
// directory and are deleted by Cleanup regardless of whether remote cleanup
// succeeded.
package sshkeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/filetree"
)

// TempKeyPair holds the on-disk paths and content of a generated temporary
// Ed25519 SSH keypair.
type TempKeyPair struct {
	// Dir is the temporary directory that owns both key files.
	// It is set to "" after DeleteKeyFiles succeeds.
	Dir string

	// PrivateKeyPath is the absolute path to the private key.
	PrivateKeyPath string

	// PublicKeyPath is the absolute path to the public key (.pub file).
	PublicKeyPath string

	// PublicKey is the trimmed content of the public key file, suitable
	// for appending to authorized_keys.
	PublicKey string

	// Comment is the unique string embedded as the key's comment field.
	// It has the form "smux-distribute-<16-hex-chars>" and is used both to
	// identify the key in authorized_keys and as the target for removal.
	Comment string
}

// Generate creates a new Ed25519 keypair in a fresh temporary directory.
//
// A unique comment of the form "smux-distribute-<hex>" is embedded in the
// public key so cleanup can unambiguously target the right authorized_keys
// line on each host.
//
// The caller must eventually call DeleteKeyFiles or Cleanup to remove the
// temporary directory.
func Generate() (*TempKeyPair, error) {
	dir, err := os.MkdirTemp("", "smux-distribute-*")
	if err != nil {
		return nil, fmt.Errorf("create keypair temp dir: %w", err)
	}

	// Generate a 8-byte (16 hex char) random suffix for uniqueness.
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("generate random comment suffix: %w", err)
	}
	comment := "smux-distribute-" + hex.EncodeToString(b)

	keyPath := filepath.Join(dir, "id_ed25519")

	// ssh-keygen flags:
	//   -t ed25519   : use the Ed25519 algorithm
	//   -N ""        : empty passphrase (no interaction required)
	//   -f <path>    : private key output path; public key written to <path>.pub
	//   -C <comment> : embed the unique comment in the public key
	var sshKeygenStderr strings.Builder
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath, "-C", comment)
	cmd.Stderr = &sshKeygenStderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("ssh-keygen: %w (stderr: %s)", err, sshKeygenStderr.String())
	}

	pubKeyBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("read generated public key: %w", err)
	}

	return &TempKeyPair{
		Dir:            dir,
		PrivateKeyPath: keyPath,
		PublicKeyPath:  keyPath + ".pub",
		PublicKey:      strings.TrimSpace(string(pubKeyBytes)),
		Comment:        comment,
	}, nil
}

// DistributePublicKey appends pubKey to ~/.ssh/authorized_keys on host,
// using the host's existing SSH credentials.
//
// The remote command atomically:
//  1. Creates ~/.ssh (mode 0700) if it does not exist.
//  2. Appends pubKey as a new line to authorized_keys.
//  3. Sets authorized_keys permissions to 0600.
//
// No command= restrictions or expiry markers are added; the key is a
// plain authorized_keys entry.
func DistributePublicKey(ctx context.Context, host config.ResolvedHost, pubKey string) error {
	// Use printf '%s\n' instead of echo to avoid flag interpretation across
	// different shells (e.g. echo -n behaviour differs between bash and sh).
	remoteCmd := fmt.Sprintf(
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && printf '%%s\n' %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
		shellescape(pubKey),
	)

	args := filetree.BuildSSHArgsForHost(host)
	args = append(args, "--", remoteCmd)

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("distribute key to %s: %w (stderr: %s)", host.Host, err, stderr.String())
	}
	return nil
}

// RemovePublicKey removes the authorized_keys line whose comment field
// matches comment from host, using existing SSH credentials.
//
// The unique comment (TempKeyPair.Comment) is used as the matching
// criterion so only the exact temporary key is removed. If authorized_keys
// does not exist or no matching line is present, the operation succeeds
// silently.
//
// Implementation uses grep -v to avoid sed -i cross-platform differences
// between Linux and macOS. The filtered content is written to a temp file
// and atomically renamed over the original.
func RemovePublicKey(ctx context.Context, host config.ResolvedHost, comment string) error {
	remoteCmd := fmt.Sprintf(
		// If authorized_keys does not exist, exit 0 immediately.
		// Otherwise: filter out lines containing our unique comment,
		// write to a tmp file, then atomically rename.
		// If the rename fails for any reason, clean up the tmp file.
		`f=~/.ssh/authorized_keys; [ -f "$f" ] || exit 0; grep -v %s "$f" > "$f.smux_tmp" && mv "$f.smux_tmp" "$f" || rm -f "$f.smux_tmp"`,
		shellescape(comment),
	)

	args := filetree.BuildSSHArgsForHost(host)
	args = append(args, "--", remoteCmd)

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove key from %s: %w (stderr: %s)", host.Host, err, stderr.String())
	}
	return nil
}

// Cleanup removes the temporary keypair files and attempts to clean up the
// authorized_keys entry on every host in hosts.
//
// For each host where remote cleanup fails, a DirtyHost entry is appended
// to dirty. The caller is responsible for calling dirtystate.Save if
// dirty.IsEmpty() returns false after Cleanup returns.
//
// Temporary key files are always deleted, regardless of whether remote
// cleanup succeeded on all hosts. Errors from individual host cleanups do
// not prevent subsequent hosts from being attempted.
//
// The returned error is non-nil only when the local key file deletion fails;
// per-host remote errors are recorded in dirty state instead.
func Cleanup(ctx context.Context, kp *TempKeyPair, hosts []config.ResolvedHost, dirty *dirtystate.State) error {
	for _, host := range hosts {
		if err := RemovePublicKey(ctx, host, kp.Comment); err != nil {
			// Record cleanup failure in dirty state for a future retry pass.
			dirty.Add(dirtystate.DirtyHost{
				Host:       host.Host,
				User:       host.User,
				Port:       host.Port,
				KeyComment: kp.Comment,
				AddedAt:    time.Now(),
			})
		}
	}

	// Always remove the local keypair files, even if remote cleanup
	// partially failed. The dirty state is the safety net for those hosts.
	return kp.DeleteKeyFiles()
}

// DeleteKeyFiles removes the temporary directory containing the keypair.
//
// It is safe to call multiple times; calls after the first successful
// deletion are no-ops (kp.Dir is cleared on success).
func (kp *TempKeyPair) DeleteKeyFiles() error {
	if kp.Dir == "" {
		return nil
	}
	if err := os.RemoveAll(kp.Dir); err != nil {
		return fmt.Errorf("delete keypair temp dir %q: %w", kp.Dir, err)
	}
	kp.Dir = ""
	return nil
}

// PrivateKeyContent reads and returns the private key file content as a string.
func (kp *TempKeyPair) PrivateKeyContent() (string, error) {
	data, err := os.ReadFile(kp.PrivateKeyPath)
	if err != nil {
		return "", fmt.Errorf("read private key: %w", err)
	}
	return string(data), nil
}

// shellescape wraps s in single quotes and escapes any embedded single
// quotes with the classic '\'' idiom. This is safe for use in POSIX shell
// commands when the value may contain spaces or special characters.
func shellescape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// Hub keypair — keypair generated on the hub host for hub-to-spoke transfers
// ---------------------------------------------------------------------------

// HubKeyPair holds information about a temporary Ed25519 keypair generated
// on a remote hub host. The private key resides only on the hub; the caller
// distributes the public key to spoke hosts via DistributePublicKey and
// cleans up afterwards via CleanupHubKeypair.
type HubKeyPair struct {
	// Hub is the host where the keypair was generated.
	Hub config.ResolvedHost

	// RemoteDir is the temporary directory on the hub that owns both key files.
	// It is set to "" after DeleteHubKeyFiles succeeds.
	RemoteDir string

	// RemotePrivateKeyPath is the absolute path to the private key on the hub.
	RemotePrivateKeyPath string

	// RemotePublicKeyPath is the absolute path to the public key on the hub.
	RemotePublicKeyPath string

	// PublicKey is the trimmed content of the public key file, suitable for
	// appending to authorized_keys on spoke hosts.
	PublicKey string

	// Comment is the unique string embedded as the key's comment field, in
	// the form "smux-distribute-<16-hex-chars>". It is used both to identify
	// the key in authorized_keys and as the target for removal.
	Comment string
}

// GenerateOnHub creates a new Ed25519 keypair on hubHost by SSHing to the
// hub and running ssh-keygen remotely. The private key is never transferred
// to the local host; only the public key is returned in HubKeyPair.PublicKey.
//
// A unique comment of the form "smux-distribute-<hex>" is embedded in the
// public key so cleanup can unambiguously target the right authorized_keys
// line on each spoke.
//
// The caller must eventually call DeleteHubKeyFiles or CleanupHubKeypair to
// remove the temporary directory from the hub.
func GenerateOnHub(ctx context.Context, hub config.ResolvedHost) (*HubKeyPair, error) {
	// Generate a unique comment for this keypair.
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate random comment suffix: %w", err)
	}
	comment := "smux-distribute-" + hex.EncodeToString(b)

	// Single SSH command that:
	//   1. Creates a secure temp directory under /tmp on the hub.
	//   2. Runs ssh-keygen to produce id_ed25519 and id_ed25519.pub in that dir.
	//   3. Outputs the public key content on one line, then the temp dir path
	//      on the next line. Ed25519 public keys are always a single line, so
	//      the last line of output is always the remote dir path.
	//
	// ssh-keygen -q suppresses all banner output; only printf produces stdout.
	remoteCmd := fmt.Sprintf(
		`dir=$(mktemp -d /tmp/smux-distribute-XXXXXX) && `+
			`ssh-keygen -t ed25519 -N '' -f "$dir/id_ed25519" -C %s -q && `+
			`printf '%%s\n%%s\n' "$(cat "$dir/id_ed25519.pub")" "$dir"`,
		shellescape(comment),
	)

	args := filetree.BuildSSHArgsForHost(hub)
	args = append(args, "--", remoteCmd)

	var stdout, stderr strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("generate keypair on hub %s: %w (stderr: %s)",
			hub.Host, err, stderr.String())
	}

	// Parse output: last non-empty line is the remote dir; everything before
	// it is the public key (joined in case there are trailing newlines).
	raw := strings.TrimRight(stdout.String(), "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("generate keypair on hub %s: unexpected output %q",
			hub.Host, stdout.String())
	}
	remoteDir := lines[len(lines)-1]
	pubKey := strings.Join(lines[:len(lines)-1], "\n")

	if remoteDir == "" {
		return nil, fmt.Errorf("generate keypair on hub %s: empty remote dir in output",
			hub.Host)
	}

	return &HubKeyPair{
		Hub:                  hub,
		RemoteDir:            remoteDir,
		RemotePrivateKeyPath: remoteDir + "/id_ed25519",
		RemotePublicKeyPath:  remoteDir + "/id_ed25519.pub",
		PublicKey:            pubKey,
		Comment:              comment,
	}, nil
}

// DeleteHubKeyFiles removes the temporary keypair directory from the hub host
// via SSH.
//
// It is safe to call multiple times; calls after the first successful
// deletion are no-ops (kp.RemoteDir is cleared on success).
func (kp *HubKeyPair) DeleteHubKeyFiles(ctx context.Context) error {
	if kp.RemoteDir == "" {
		return nil
	}

	args := filetree.BuildSSHArgsForHost(kp.Hub)
	args = append(args, "--", "rm -rf "+shellescape(kp.RemoteDir))

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete hub keypair dir %q on %s: %w (stderr: %s)",
			kp.RemoteDir, kp.Hub.Host, err, stderr.String())
	}
	kp.RemoteDir = ""
	return nil
}

// CleanupHubKeypair removes the hub's public key from every spoke's
// authorized_keys and then deletes the keypair directory from the hub.
//
// For each spoke where remote cleanup fails, a DirtyHost entry (with
// KeyComment set) is appended to dirty. If hub keypair deletion fails, a
// DirtyHost entry with both KeyComment and HubKeyDir set is appended to dirty
// for the hub host.
//
// The caller is responsible for calling dirtystate.Save if dirty.IsEmpty()
// returns false after CleanupHubKeypair returns.
//
// The returned error is always nil; all per-host errors are recorded in dirty
// state to allow subsequent retry passes.
func CleanupHubKeypair(ctx context.Context, kp *HubKeyPair, spokes []config.ResolvedHost, dirty *dirtystate.State) error {
	// Step 1: remove the hub's public key from each spoke's authorized_keys.
	for _, spoke := range spokes {
		if err := RemovePublicKey(ctx, spoke, kp.Comment); err != nil {
			dirty.Add(dirtystate.DirtyHost{
				Host:       spoke.Host,
				User:       spoke.User,
				Port:       spoke.Port,
				KeyComment: kp.Comment,
				AddedAt:    time.Now(),
			})
		}
	}

	// Step 2: delete the keypair directory from the hub.
	// Save RemoteDir before potential mutation by DeleteHubKeyFiles.
	remoteDir := kp.RemoteDir
	if err := kp.DeleteHubKeyFiles(ctx); err != nil {
		dirty.Add(dirtystate.DirtyHost{
			Host:       kp.Hub.Host,
			User:       kp.Hub.User,
			Port:       kp.Hub.Port,
			KeyComment: kp.Comment,
			HubKeyDir:  remoteDir,
			AddedAt:    time.Now(),
		})
	}
	return nil
}
