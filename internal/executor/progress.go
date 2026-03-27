// Progress tracking for parallel file distribution.
//
// RunParallelWithProgress and FanOutFromHubWithProgress are drop-in
// alternatives to RunParallel and FanOutFromHub that send a ProgressUpdate on
// the supplied channel for each host status transition:
//
//  1. TransferInProgress is sent immediately before starting the scp process.
//  2. TransferDone or TransferFailed is sent once the scp process exits.
//
// The channel is never closed by these functions; closing is the caller's
// responsibility (typically done after all goroutines finish). A nil channel
// is accepted and silently skipped so callers that do not need progress
// tracking can still use these functions.
package executor

import (
	"context"
	"sync"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// TransferStatus describes the current execution state of a single host's
// file transfer.
type TransferStatus int

const (
	// TransferPending means the transfer has not yet started.
	TransferPending TransferStatus = iota

	// TransferInProgress means the scp process has been launched and is
	// running.
	TransferInProgress

	// TransferDone means scp exited with code 0 (success).
	TransferDone

	// TransferFailed means scp exited with a non-zero code or could not be
	// started.
	TransferFailed
)

// String returns a short human-readable representation of the status.
func (s TransferStatus) String() string {
	switch s {
	case TransferPending:
		return "pending"
	case TransferInProgress:
		return "transferring"
	case TransferDone:
		return "done"
	case TransferFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ProgressUpdate is sent on the progress channel for each host status
// transition.  The Host field identifies which host changed state.
type ProgressUpdate struct {
	// Host is the destination host whose state changed.
	Host config.ResolvedHost

	// Status is the new state of this host's transfer.
	Status TransferStatus

	// Err is non-nil only when Status == TransferFailed. It wraps the scp
	// exit error and includes any captured stderr.
	Err error

	// Stderr is the raw stderr output captured from the scp process.
	// Present for both TransferDone (may be empty) and TransferFailed.
	Stderr string
}

// sendProgress sends an update on ch if ch is non-nil.
// Non-blocking: if the channel buffer is full the update is dropped rather
// than deadlocking.  The buffer should be sized generously by callers
// (at least 2*len(destHosts)) to avoid drops in normal usage.
func sendProgress(ch chan<- ProgressUpdate, u ProgressUpdate) {
	if ch == nil {
		return
	}
	select {
	case ch <- u:
	default:
		// Buffer full: drop this update rather than block.
	}
}

// RunParallelWithProgress is like RunParallel but sends ProgressUpdates on
// progress for each host state transition.  progress may be nil; in that case
// it behaves identically to RunParallel.
//
// The caller must close progress after RunParallelWithProgress returns if
// the channel will be used with waitForProgress in the TUI.
func RunParallelWithProgress(
	ctx context.Context,
	sourceHost config.ResolvedHost,
	sourcePath string,
	destHosts []config.ResolvedHost,
	destPath string,
	kp *sshkeys.TempKeyPair,
	progress chan<- ProgressUpdate,
) []CopyResult {
	results := make([]CopyResult, len(destHosts))
	var wg sync.WaitGroup

	for i, dest := range destHosts {
		wg.Add(1)
		go func(idx int, dst config.ResolvedHost) {
			defer wg.Done()

			// Announce start.
			sendProgress(progress, ProgressUpdate{
				Host:   dst,
				Status: TransferInProgress,
			})

			result := runSingleSCP(ctx, sourceHost, sourcePath, dst, destPath, kp)
			results[idx] = result

			// Announce completion.
			if result.Success {
				sendProgress(progress, ProgressUpdate{
					Host:   dst,
					Status: TransferDone,
					Stderr: result.Stderr,
				})
			} else {
				sendProgress(progress, ProgressUpdate{
					Host:   dst,
					Status: TransferFailed,
					Err:    result.Err,
					Stderr: result.Stderr,
				})
			}
		}(i, dest)
	}

	wg.Wait()
	return results
}

// FanOutFromHubWithProgress is like FanOutFromHub but sends ProgressUpdates on
// progress for each spoke state transition.  progress may be nil; in that case
// it behaves identically to FanOutFromHub.
//
// The caller must close progress after FanOutFromHubWithProgress returns if
// the channel will be used with waitForProgress in the TUI.
func FanOutFromHubWithProgress(
	ctx context.Context,
	hub config.ResolvedHost,
	hubPath string,
	spokes []config.ResolvedHost,
	destPath string,
	hubKP *sshkeys.HubKeyPair,
	progress chan<- ProgressUpdate,
) []CopyResult {
	results := make([]CopyResult, len(spokes))
	var wg sync.WaitGroup

	for i, spoke := range spokes {
		wg.Add(1)
		go func(idx int, dst config.ResolvedHost) {
			defer wg.Done()

			// Announce start.
			sendProgress(progress, ProgressUpdate{
				Host:   dst,
				Status: TransferInProgress,
			})

			result := runHubToSpokeTransfer(ctx, hub, hubPath, dst, destPath, hubKP)
			results[idx] = result

			// Announce completion.
			if result.Success {
				sendProgress(progress, ProgressUpdate{
					Host:   dst,
					Status: TransferDone,
					Stderr: result.Stderr,
				})
			} else {
				sendProgress(progress, ProgressUpdate{
					Host:   dst,
					Status: TransferFailed,
					Err:    result.Err,
					Stderr: result.Stderr,
				})
			}
		}(i, spoke)
	}

	wg.Wait()
	return results
}
