// CleanupDirtyState retries pending SSH key cleanup from dirty-state records
// that were saved by a previous distribute-file operation.
//
// Each entry in state.Hosts describes one remote host with either a
// spoke-side cleanup (remove a specific line from authorized_keys) or a
// hub-side cleanup (delete a temporary keypair directory via SSH).
//
// Successfully cleaned entries are removed from the persistent dirty state.
// Entries that still fail are re-saved so that a future pass can retry them.
// The returned error is non-nil only when saving the updated state fails;
// per-host SSH failures are never returned as errors.
package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/filetree"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// CleanupDirtyState attempts to clean up all hosts recorded in the provided
// dirty-state host slice.
//
// For each host:
//   - Spoke record (HubKeyDir == ""): removes the matching authorized_keys
//     line via sshkeys.RemovePublicKey.
//   - Hub record (HubKeyDir != ""): deletes the remote keypair directory
//     via SSH rm -rf.
//
// Any host that is still unreachable or whose cleanup fails is written back
// to ~/.smux/dirty-state.json so that a subsequent pass can retry.
// Hosts that are successfully cleaned are omitted from the saved state.
//
// The returned error is non-nil only when saving the updated dirty state
// fails; per-host SSH errors are always recorded silently in dirty state.
func CleanupDirtyState(ctx context.Context, hosts []dirtystate.DirtyHost) error {
	if len(hosts) == 0 {
		// Nothing to clean up; save an explicit empty state so that a
		// previous failed-save does not leave stale data on disk.
		return dirtystate.Save(&dirtystate.State{})
	}

	remaining := &dirtystate.State{}

	for _, dh := range hosts {
		host := config.ResolvedHost{
			Host: dh.Host,
			User: dh.User,
			Port: dh.Port,
		}

		var cleanErr error
		if dh.HubKeyDir != "" {
			// Hub record: delete the remote keypair directory.
			cleanErr = removeRemoteDir(ctx, host, dh.HubKeyDir)
		} else {
			// Spoke record: remove the key line from authorized_keys.
			cleanErr = sshkeys.RemovePublicKey(ctx, host, dh.KeyComment)
		}

		if cleanErr != nil {
			// Keep the entry in the dirty state for the next retry.
			remaining.Add(dh)
		}
	}

	return dirtystate.Save(remaining)
}

// CleanupDirtyStateSubset attempts to clean up only the provided subset of
// dirty-state hosts, preserving any hosts that were NOT in the subset.
//
// This is the per-selection variant of CleanupDirtyState, used when the user
// picks a specific subset of dirty hosts to clean from the BrowsingPhase
// (via the 'C' keybind and DirtyCleanupConfirmPhase dialog) rather than
// cleaning everything at once.
//
// Algorithm:
//  1. Load the full current dirty state from disk.
//  2. Build a result set starting with all entries NOT in subset.
//  3. Try to clean each entry in subset; keep failures in the result set.
//  4. Save the result set (non-targeted preserved + cleanup failures).
//
// The returned error is non-nil only when saving the updated dirty state
// fails; per-host SSH errors are always silently recorded in dirty state.
func CleanupDirtyStateSubset(ctx context.Context, subset []dirtystate.DirtyHost) error {
	if len(subset) == 0 {
		// Nothing to clean; leave the existing state unchanged.
		return nil
	}

	// Load the full current state so non-targeted hosts are preserved.
	full, loadErr := dirtystate.Load()
	if loadErr != nil {
		// Non-fatal: treat the on-disk state as empty so we only deal with
		// the subset.  We cannot preserve hosts we cannot read.
		full = &dirtystate.State{}
	}

	// Build a set of targeted KeyComments (the unique identifier for each record).
	targetSet := make(map[string]bool, len(subset))
	for _, dh := range subset {
		targetSet[dh.KeyComment] = true
	}

	// Start with all non-subset entries preserved unchanged.
	remaining := &dirtystate.State{}
	for _, dh := range full.Hosts {
		if !targetSet[dh.KeyComment] {
			remaining.Add(dh)
		}
	}

	// Attempt cleanup of each targeted host; add failures back to remaining.
	for _, dh := range subset {
		host := config.ResolvedHost{
			Host: dh.Host,
			User: dh.User,
			Port: dh.Port,
		}

		var cleanErr error
		if dh.HubKeyDir != "" {
			// Hub record: delete the remote keypair directory.
			cleanErr = removeRemoteDir(ctx, host, dh.HubKeyDir)
		} else {
			// Spoke record: remove the key line from authorized_keys.
			cleanErr = sshkeys.RemovePublicKey(ctx, host, dh.KeyComment)
		}

		if cleanErr != nil {
			// Keep the entry for the next retry pass.
			remaining.Add(dh)
		}
	}

	return dirtystate.Save(remaining)
}

// removeRemoteDir SSHes to host and runs rm -rf on the given remote directory
// path.  It is the hub-side analogue of sshkeys.RemovePublicKey.
func removeRemoteDir(ctx context.Context, host config.ResolvedHost, dir string) error {
	args := filetree.BuildSSHArgsForHost(host)
	args = append(args, "--", fmt.Sprintf("rm -rf %s", sshShellescape(dir)))

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove hub key dir %q on %s: %w (stderr: %s)",
			dir, host.Host, err, stderr.String())
	}
	return nil
}

// sshShellescape wraps s in single quotes and escapes embedded single quotes
// with the classic '\'' idiom.  Duplicated locally so this package does not
// need to expose the private helper from sshkeys.
func sshShellescape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
