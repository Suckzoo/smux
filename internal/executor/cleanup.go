// Cleanup orchestrators for distribute-file mode.
//
// CleanupAfterParallel and CleanupAfterHubSpoke are the top-level entry
// points that callers invoke after a file-distribution operation finishes
// (successfully or with errors). They:
//
//  1. Load any pre-existing dirty state from disk (hosts that failed cleanup
//     in a previous run).
//  2. Attempt to remove the temporary public key(s) from every relevant
//     remote host's authorized_keys via the sshkeys package.
//  3. Delete local temporary key files.
//  4. Merge newly-failed hosts into the dirty state and persist it to disk,
//     so that a future cleanup pass can retry them.
//
// Per-host remote cleanup failures are never returned as errors; they are
// silently recorded in dirty state. The returned error covers only
// infrastructure failures: loading or saving dirty state, or failing to
// delete local key files from the local filesystem.
package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// CleanupAfterParallel removes the temporary keypair used in a direct-parallel
// distribution from every destination host and from the local filesystem.
//
// kp is the keypair whose public key was distributed to every host in
// destHosts via sshkeys.DistributePublicKey before the transfers started.
// destHosts must reflect the complete set of hosts that received the public
// key; any host omitted will not have its authorized_keys cleaned up.
//
// Cleanup order:
//  1. Load existing dirty state from disk (missing file → empty state, no error).
//  2. For each host in destHosts, attempt to remove kp's public key from
//     ~/.ssh/authorized_keys.  Failures are appended to dirty state.
//  3. Delete local keypair files unconditionally.
//  4. Save dirty state to disk (even when empty, to record "all clear").
//
// The returned error is non-nil only when:
//   - the local key file deletion fails, or
//   - saving the dirty state file fails.
//
// Per-host SSH failures are never returned; they are captured in dirty state.
func CleanupAfterParallel(
	ctx context.Context,
	kp *sshkeys.TempKeyPair,
	destHosts []config.ResolvedHost,
) error {
	dirty, loadErr := dirtystate.Load()
	if loadErr != nil {
		// Non-fatal: proceed with a fresh state.  The load error is reported
		// at the end so the caller can surface it without blocking cleanup.
		dirty = &dirtystate.State{}
	}

	// Remove from each dest host; failures go into dirty state.
	localKeyErr := sshkeys.Cleanup(ctx, kp, destHosts, dirty)

	// Persist dirty state regardless of per-host SSH outcomes.
	saveErr := dirtystate.Save(dirty)

	return combineErrors(loadErr, localKeyErr, saveErr)
}

// CleanupAfterHubSpoke removes both temporary keypairs used in a
// hub-and-spoke distribution after the operation finishes (success or
// failure).
//
// kp is the keypair used for the source-to-hub push; its public key was
// distributed to hubHost only. hubKP is the keypair generated on the hub
// for hub-to-spoke transfers; its public key was distributed to every host
// in spokes.
//
// Cleanup order:
//  1. Load existing dirty state from disk.
//  2. Remove kp's public key from hubHost's authorized_keys; delete local
//     keypair files unconditionally.
//  3. Remove hubKP's public key from every spoke's authorized_keys.
//  4. Delete the hub keypair directory from the hub.
//  5. Save dirty state to disk.
//
// Any remote cleanup failure (hub or spoke) is recorded in dirty state
// rather than returned as an error, so the caller can continue regardless.
// The returned error covers only infrastructure failures (dirty state
// load/save or local key file deletion).
func CleanupAfterHubSpoke(
	ctx context.Context,
	kp *sshkeys.TempKeyPair,
	hubHost config.ResolvedHost,
	hubKP *sshkeys.HubKeyPair,
	spokes []config.ResolvedHost,
) error {
	dirty, loadErr := dirtystate.Load()
	if loadErr != nil {
		dirty = &dirtystate.State{}
	}

	// Step 1: clean up the push keypair from the hub.
	// sshkeys.Cleanup removes the public key from hubHost's authorized_keys
	// and deletes the local key files. Failures go into dirty state.
	localKeyErr := sshkeys.Cleanup(ctx, kp, []config.ResolvedHost{hubHost}, dirty)

	// Step 2: clean up the hub keypair from all spokes and the hub itself.
	// CleanupHubKeypair never returns a non-nil error; all failures go into
	// dirty state.
	_ = sshkeys.CleanupHubKeypair(ctx, hubKP, spokes, dirty)

	// Persist dirty state unconditionally.
	saveErr := dirtystate.Save(dirty)

	return combineErrors(loadErr, localKeyErr, saveErr)
}

// combineErrors returns a formatted error combining up to three distinct
// error values, skipping nils.  Returns nil when all inputs are nil.
func combineErrors(errs ...error) error {
	var msgs []string
	for _, e := range errs {
		if e != nil {
			msgs = append(msgs, e.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) == 1 {
		return errors.New(msgs[0])
	}
	combined := msgs[0]
	for _, m := range msgs[1:] {
		combined = fmt.Sprintf("%s; %s", combined, m)
	}
	return errors.New(combined)
}
