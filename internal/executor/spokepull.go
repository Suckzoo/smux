// Spoke-pull fan-out executor for hub-spoke file distribution.
//
// In spoke-pull mode, instead of the hub pushing files to each spoke, each
// spoke PULLS from the hub. The local machine SSHes to each spoke and runs
// scp on the spoke to pull the file from the hub.
//
// This avoids the need to generate a keypair on the hub and distribute it to
// every spoke. Instead, the hub's temporary keypair private key is injected
// into the spoke as an ephemeral temp file that is removed immediately after
// the scp transfer completes.
package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"sync"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/filetree"
)

// shellEscapeForRemote wraps s in single quotes and escapes any embedded
// single quotes with the '\'' idiom. Used to build safe POSIX shell command
// strings for remote execution on spoke hosts.
func shellEscapeForRemote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SpokePull SSHes to a single spoke host and runs scp on the spoke to pull
// files from the hub. The private key content is written to a temporary file
// on the spoke, used for the scp transfer, and then removed regardless of
// whether scp succeeds or fails.
//
// hubPaths are the full file paths on the hub. destPath is the destination
// directory (should end with /).
//
// The caller must ensure that the corresponding public key has been installed
// on the hub host before calling SpokePull.
func SpokePull(
	ctx context.Context,
	spoke config.ResolvedHost,
	hubUser string,
	hubPrivateIP string,
	hubPaths []string,
	destPath string,
	privateKeyContent string,
) CopyResult {
	// Generate a random suffix for the temp key file on the spoke.
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)

	// Build scp source arguments: user@hub:path for each file.
	var scpSrcs []string
	for _, hp := range hubPaths {
		scpSrcs = append(scpSrcs, shellEscapeForRemote(hubUser+"@"+hubPrivateIP+":"+hp))
	}

	// Build the remote command that will run on the spoke:
	//   1. Write the private key to a temp file
	//   2. chmod 600 it
	//   3. mkdir -p the destination directory
	//   4. scp from hub to local dest using the temp key
	//   5. Always remove the temp key, preserving scp's exit code
	destDir := path.Dir(destPath)
	remoteCmd := fmt.Sprintf(
		"f=/tmp/smux-pull-%s && printf '%%s' %s > \"$f\" && chmod 600 \"$f\" && mkdir -p %s && scp -q -i \"$f\" -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=10 %s %s ; rc=$? ; rm -f \"$f\" ; exit $rc",
		suffix,
		shellEscapeForRemote(privateKeyContent),
		shellEscapeForRemote(destDir),
		strings.Join(scpSrcs, " "),
		shellEscapeForRemote(destPath),
	)

	// SSH to the spoke using the spoke's existing auth (from user's SSH config).
	sshArgs := filetree.BuildSSHArgsForHost(spoke)
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
			Err: fmt.Errorf("spoke-pull scp on %s: %w (stderr: %s)",
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

// SpokePullWithProgress executes SpokePull concurrently for every spoke,
// sending ProgressUpdate notifications on the progress channel for each
// host state transition. progress may be nil; in that case updates are
// silently skipped.
//
// Results are returned in the same order as spokes.
func SpokePullWithProgress(
	ctx context.Context,
	spokes []config.ResolvedHost,
	hubUser string,
	hubPrivateIP string,
	hubPaths []string,
	destPath string,
	privateKeyContent string,
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

			result := SpokePull(ctx, dst, hubUser, hubPrivateIP, hubPaths, destPath, privateKeyContent)
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
