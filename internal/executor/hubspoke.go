// Hub-and-spoke distribution phases for the distribute-file wizard.
//
// In hub-and-spoke mode, file distribution is split into two phases:
//
//  1. Initial push (PushToHub): copy the source file from the originating
//     host to a designated hub host.
//  2. Fan-out (FanOutFromHub): copy the file from the hub to every spoke
//     (destination) host in parallel. The hub SSHes into each spoke using a
//     temporary keypair generated on the hub itself.
//
// PushToHub and FanOutFromHub are kept in this file so that hub-and-spoke
// orchestration logic is co-located.
package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/filetree"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// HubPushResult holds the outcome of the initial push phase: copying the
// source file from the originating host to the hub host.
type HubPushResult struct {
	// Hub is the host that was used as the distribution hub.
	Hub config.ResolvedHost

	// Success is true when scp exited with code 0.
	Success bool

	// Err is non-nil when Success is false. It wraps the scp exit error and
	// includes any captured stderr output.
	Err error

	// Stderr is the raw stderr captured from the scp process regardless of
	// success or failure.
	Stderr string
}

// PushToHub copies sourcePath (from sourceHost, or the local machine when
// sourceHost.Host is empty) to hubPath on hubHost, using kp's private key
// for authentication to the hub.
//
// The caller must ensure kp's public key has been installed on hubHost (via
// sshkeys.DistributePublicKey) before calling PushToHub. After PushToHub
// returns, the caller is responsible for removing kp from hubHost (via
// sshkeys.RemovePublicKey or sshkeys.Cleanup) and persisting any cleanup
// failure to dirty state.
//
// Source semantics follow the same rules as RunParallel:
//   - sourceHost.Host == "" → local filesystem source; sourcePath is a local path.
//   - sourceHost.Host != "" → remote source; scp's three-way copy (-3) relays
//     data through the local host. Source-side authentication uses the SSH agent
//     or default identity files; kp authenticates to the hub only.
//
// The returned HubPushResult always has Hub set to hubHost regardless of
// success or failure.
func PushToHub(
	ctx context.Context,
	sourceHost config.ResolvedHost,
	sourcePath string,
	hubHost config.ResolvedHost,
	hubPath string,
	kp *sshkeys.TempKeyPair,
) HubPushResult {
	r := runSingleSCP(ctx, sourceHost, sourcePath, hubHost, hubPath, kp)
	return HubPushResult{
		Hub:     hubHost,
		Success: r.Success,
		Err:     r.Err,
		Stderr:  r.Stderr,
	}
}

// FanOutFromHub executes parallel SCP transfers from the hub host to every
// spoke host, using hubKP's remote private key for hub-to-spoke authentication.
//
// For each spoke, FanOutFromHub SSHes to the hub and runs scp from the hub to
// the spoke. Transfers run concurrently; a failure on one spoke does not
// prevent transfers to other spokes.
//
// The caller must ensure hubKP's public key has already been installed on
// every spoke (via sshkeys.DistributePublicKey) before calling FanOutFromHub.
// After FanOutFromHub returns, the caller is responsible for cleaning up hubKP
// from all spokes and from the hub (via sshkeys.CleanupHubKeypair) and
// persisting any cleanup failures to dirty state.
//
// Results are returned in the same order as spokes.
func FanOutFromHub(
	ctx context.Context,
	hub config.ResolvedHost,
	hubPath string,
	spokes []config.ResolvedHost,
	destPath string,
	hubKP *sshkeys.HubKeyPair,
) []CopyResult {
	results := make([]CopyResult, len(spokes))
	var wg sync.WaitGroup

	for i, spoke := range spokes {
		wg.Add(1)
		go func(idx int, dst config.ResolvedHost) {
			defer wg.Done()
			results[idx] = runHubToSpokeTransfer(ctx, hub, hubPath, dst, destPath, hubKP)
		}(i, spoke)
	}

	wg.Wait()
	return results
}

// runHubToSpokeTransfer SSHes to hub and executes a single scp transfer from
// hub to one spoke, returning the result.
func runHubToSpokeTransfer(
	ctx context.Context,
	hub config.ResolvedHost,
	hubPath string,
	spoke config.ResolvedHost,
	destPath string,
	hubKP *sshkeys.HubKeyPair,
) CopyResult {
	// Build the scp shell command that will run on the hub.
	remoteCmd := buildHubSCPCommand(hubPath, spoke, destPath, hubKP)

	// SSH to the hub and run the scp command on it.
	sshArgs := filetree.BuildSSHArgsForHost(hub)
	sshArgs = append(sshArgs, "--", remoteCmd)

	var stderrBuf strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stderrStr := stderrBuf.String()

	if err != nil {
		return CopyResult{
			Host:    spoke,
			Success: false,
			Err: fmt.Errorf("hub-to-spoke scp to %s: %w (stderr: %s)",
				spoke.Host, err, strings.TrimSpace(stderrStr)),
			Stderr: stderrStr,
		}
	}

	return CopyResult{
		Host:    spoke,
		Success: true,
		Stderr:  stderrStr,
	}
}

// buildHubSCPCommand constructs a POSIX shell command string that runs scp on
// the hub host, copying hubPath to [user@]spoke:destPath using hubKP's remote
// private key for spoke authentication.
//
// The returned string is suitable for passing as the remote command to ssh(1).
func buildHubSCPCommand(
	hubPath string,
	spoke config.ResolvedHost,
	destPath string,
	hubKP *sshkeys.HubKeyPair,
) string {
	parts := []string{
		"scp",
		"-q",
		"-i", hubShellEscape(hubKP.RemotePrivateKeyPath),
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
	}
	if spoke.Port != 0 {
		parts = append(parts, "-P", strconv.Itoa(spoke.Port))
	}
	if spoke.JumpHost != "" {
		parts = append(parts, "-J", hubShellEscape(spoke.JumpHost))
	}
	// Source: the file path on the hub.
	parts = append(parts, hubShellEscape(hubPath))
	// Destination: [user@]spoke:destPath
	// Use RemoteReachableAddress instead of EffectiveAddress because this
	// command runs on the hub, not locally. The hub cannot resolve local
	// SSH aliases; it needs the real hostname or IP.
	destAddr := spoke.RemoteReachableAddress()
	if spoke.User != "" {
		destAddr = spoke.User + "@" + spoke.RemoteReachableAddress()
	}
	parts = append(parts, hubShellEscape(destAddr+":"+destPath))

	return strings.Join(parts, " ")
}

// hubShellEscape wraps s in single quotes and escapes any embedded single
// quotes with the '\'' idiom. Used to build safe POSIX shell command strings
// for remote execution on the hub host.
func hubShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
