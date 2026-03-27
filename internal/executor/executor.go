// Package executor implements parallel file distribution via scp.
//
// RunParallel executes SCP transfers from a source (local or remote) to every
// destination host concurrently, using a temporary SSH keypair for
// authentication to each destination. Per-host success/failure is collected
// in a CopyResult slice returned in the same order as the destination list.
package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// CopyResult holds the outcome of a single SCP transfer to one destination host.
type CopyResult struct {
	// Host is the destination this result describes.
	Host config.ResolvedHost

	// Success is true when scp exited with code 0.
	Success bool

	// Err is non-nil when Success is false. It wraps the scp exit error and
	// includes any captured stderr.
	Err error

	// Stderr is the raw stderr captured from the scp process regardless of
	// success or failure. Useful for structured reporting.
	Stderr string
}

// RunParallel executes SCP from the source to every host in destHosts
// concurrently, using kp's private key for authentication to each destination.
//
// sourceHost identifies the machine that holds the source file.
//   - When sourceHost.Host is empty the source is the local machine and
//     sourcePath is a local filesystem path.
//   - When sourceHost.Host is non-empty the source is a remote machine;
//     scp's three-way copy mode (-3) is used so data is relayed through
//     the local host. Source-side authentication relies on the SSH agent
//     or default identity files; the temporary key authenticates to dests.
//
// destPath is the destination path on every destination host. The parent
// directory is not created automatically; callers that need mkdir -p must
// arrange that separately.
//
// Results are returned in the same order as destHosts. Transfers to
// individual hosts fail independently; a failure for one host does not
// prevent transfers to others.
func RunParallel(
	ctx context.Context,
	sourceHost config.ResolvedHost,
	sourcePath string,
	destHosts []config.ResolvedHost,
	destPath string,
	kp *sshkeys.TempKeyPair,
) []CopyResult {
	results := make([]CopyResult, len(destHosts))
	var wg sync.WaitGroup

	for i, dest := range destHosts {
		wg.Add(1)
		go func(idx int, dst config.ResolvedHost) {
			defer wg.Done()
			results[idx] = runSingleSCP(ctx, sourceHost, sourcePath, dst, destPath, kp)
		}(i, dest)
	}

	wg.Wait()
	return results
}

// runSingleSCP executes one scp transfer and returns its CopyResult.
func runSingleSCP(
	ctx context.Context,
	sourceHost config.ResolvedHost,
	sourcePath string,
	dst config.ResolvedHost,
	destPath string,
	kp *sshkeys.TempKeyPair,
) CopyResult {
	args := buildSCPArgs(sourceHost, sourcePath, dst, destPath, kp)

	var stderrBuf strings.Builder
	cmd := exec.CommandContext(ctx, "scp", args...)
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stderrStr := stderrBuf.String()

	if err != nil {
		return CopyResult{
			Host:    dst,
			Success: false,
			Err: fmt.Errorf("scp to %s: %w (stderr: %s)",
				dst.Host, err, strings.TrimSpace(stderrStr)),
			Stderr: stderrStr,
		}
	}

	return CopyResult{
		Host:    dst,
		Success: true,
		Stderr:  stderrStr,
	}
}

// buildSCPArgs constructs the argument slice for scp(1).
//
// Local source (sourceHost.Host == ""):
//
//	scp -q -i <privkey> -o BatchMode=yes -o StrictHostKeyChecking=no
//	    -o ConnectTimeout=10 [-P dstPort] [-J dstJump]
//	    <sourcePath> [dstUser@]<dstHost>:<destPath>
//
// Remote source (sourceHost.Host != ""):
//
//	scp -3 -q -i <privkey> -o BatchMode=yes -o StrictHostKeyChecking=no
//	    -o ConnectTimeout=10 [-P dstPort] [-J dstJump]
//	    [srcUser@]<srcHost>:<sourcePath> [dstUser@]<dstHost>:<destPath>
//
// With -3 the local host relays the data. The -i flag applies to the
// destination connection; source authentication uses the SSH agent or
// default identity files.
func buildSCPArgs(
	src config.ResolvedHost,
	srcPath string,
	dst config.ResolvedHost,
	dstPath string,
	kp *sshkeys.TempKeyPair,
) []string {
	var args []string

	isRemoteSrc := src.Host != ""
	if isRemoteSrc {
		// -3: copy between two remote hosts relayed through the local host.
		// This also suppresses the progress meter, which is desirable for
		// programmatic use.
		args = append(args, "-3")
	}

	// Quiet mode + standard SSH options applied to the destination side.
	args = append(args,
		"-q",
		"-i", kp.PrivateKeyPath,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
	)

	// Destination-side port and jump host.
	if dst.Port != 0 {
		args = append(args, "-P", strconv.Itoa(dst.Port))
	}
	if dst.JumpHost != "" {
		args = append(args, "-J", dst.JumpHost)
	}

	// Source argument.
	if isRemoteSrc {
		srcAddr := src.Host
		if src.User != "" {
			srcAddr = src.User + "@" + src.Host
		}
		args = append(args, srcAddr+":"+srcPath)
	} else {
		args = append(args, srcPath)
	}

	// Destination argument: [user@]host:path
	dstAddr := dst.Host
	if dst.User != "" {
		dstAddr = dst.User + "@" + dst.Host
	}
	args = append(args, dstAddr+":"+dstPath)

	return args
}
