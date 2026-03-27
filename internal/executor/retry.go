// Package executor — retry-report parsing for distribute-file retry flows.
//
// ParseRetryReport reads a previously saved DistributeReport JSON file and
// extracts the distribution parameters (source host, source path, destination
// path, copy mode) along with the subset of destination hosts that failed, so
// that the caller can re-execute just the failed transfers without touching
// hosts that already received the file successfully.
//
// Typical usage:
//
//	params, err := executor.ParseRetryReport("/tmp/smux-report-1234.json")
//	if err != nil { ... }
//	if len(params.FailedHosts) == 0 {
//	    fmt.Println("nothing to retry")
//	    return
//	}
//	dirty := &dirtystate.State{}
//	rk, err := executor.PrepareRetryKeypairs(ctx, params, dirty)
//	if err != nil { ... }
//	results := executor.RunParallel(ctx, params.SourceHost, params.SourcePath,
//	    params.FailedHosts, params.DestPath, rk.LocalKP)
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// RetryParams holds the original distribution parameters extracted from a
// saved DistributeReport, with FailedHosts pre-filtered to only the
// destination hosts that did not succeed in the original run.  Callers pass
// these directly to RunParallel or the hub-and-spoke orchestration functions
// to retry only the failed transfers.
type RetryParams struct {
	// SourceHost identifies the machine that held the source file in the
	// original operation.  An empty Host field means the local machine.
	SourceHost config.ResolvedHost

	// SourcePath is the filesystem path of the file to be distributed.
	SourcePath string

	// DestPath is the filesystem path on each destination host.
	// An empty string means the same path as SourcePath was used.
	DestPath string

	// CopyMode is the distribution strategy used in the original operation:
	// "parallel" for direct parallel copy or "hub-spoke" for hub-and-spoke.
	CopyMode string

	// FailedHosts is the subset of destination hosts from the original
	// report whose transfer did not succeed (Success == false).  This is
	// the recommended target list for a retry operation.
	//
	// The slice is always non-nil; when every host succeeded it is empty.
	FailedHosts []config.ResolvedHost

	// AllHosts is the complete list of destination hosts from the original
	// report, in the same order as the report's Hosts array.  Useful when
	// the caller needs to re-run a full redistribution rather than only
	// the failures.
	//
	// The slice is always non-nil; when the report had no hosts it is empty.
	AllHosts []config.ResolvedHost
}

// ParseRetryReport reads the DistributeReport JSON file at path and returns a
// RetryParams populated from the report's operation metadata and host list.
//
// Only HostReport entries with Success == false are included in
// RetryParams.FailedHosts.  All hosts (regardless of outcome) are available
// in RetryParams.AllHosts in report order.
//
// Errors are returned for:
//   - file read failures (e.g. file not found, permission denied)
//   - JSON parse failures (invalid or truncated JSON)
func ParseRetryReport(path string) (RetryParams, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RetryParams{}, fmt.Errorf("read retry report %s: %w", path, err)
	}
	return ParseRetryReportBytes(data)
}

// ParseRetryReportBytes parses DistributeReport JSON from an in-memory byte
// slice and returns a RetryParams.  It is the byte-slice counterpart of
// ParseRetryReport and is particularly useful in tests that do not need a
// file on disk.
//
// An error is returned when the JSON is invalid or cannot be decoded into a
// DistributeReport.
func ParseRetryReportBytes(data []byte) (RetryParams, error) {
	var report DistributeReport
	if err := json.Unmarshal(data, &report); err != nil {
		return RetryParams{}, fmt.Errorf("parse retry report: %w", err)
	}

	// Reconstruct the source host.  DistributeReport stores only Host
	// (empty == local machine); User/Port/Key/JumpHost are not persisted
	// because the source-side credentials are managed independently (SSH
	// agent / default identity files).
	sourceHost := config.ResolvedHost{
		Host: report.SourceHost,
	}

	// Reconstruct per-host ResolvedHost values from the HostReport entries.
	// We preserve Host, User, and Port because those are stored in the
	// report.  Key and JumpHost are not stored in HostReport and are left
	// as zero values; callers that need them should resolve them from the
	// smux inventory after loading the retry params.
	allHosts := make([]config.ResolvedHost, 0, len(report.Hosts))
	failedHosts := make([]config.ResolvedHost, 0)

	for _, h := range report.Hosts {
		rh := config.ResolvedHost{
			Host: h.Host,
			User: h.User,
			Port: h.Port,
		}
		allHosts = append(allHosts, rh)
		if !h.Success {
			failedHosts = append(failedHosts, rh)
		}
	}

	return RetryParams{
		SourceHost:  sourceHost,
		SourcePath:  report.SourcePath,
		DestPath:    report.DestPath,
		CopyMode:    report.CopyMode,
		FailedHosts: failedHosts,
		AllHosts:    allHosts,
	}, nil
}

// ---------------------------------------------------------------------------
// Fresh keypair generation for retry execution
// ---------------------------------------------------------------------------

// RetryKeypair holds the fresh keypair(s) generated for a retry execution.
// Exactly one of LocalKP or HubKP will be non-nil, determined by the
// CopyMode recorded in the original DistributeReport.
//
// The keypair(s) are always freshly generated—never reused from the original
// operation—so each retry round uses a unique authorized_keys comment that
// can be unambiguously identified and removed during cleanup.
type RetryKeypair struct {
	// LocalKP is non-nil for "parallel" copy mode.  It holds the freshly
	// generated local TempKeyPair whose public key has been distributed to
	// the failed destination hosts.
	LocalKP *sshkeys.TempKeyPair

	// HubKP is non-nil for "hub-spoke" copy mode.  It holds the freshly
	// generated HubKeyPair whose public key has been distributed to the
	// failed spoke hosts.  The private key resides only on the hub host.
	HubKP *sshkeys.HubKeyPair
}

// PrepareRetryKeypairs generates a fresh SSH keypair for a retry execution
// and distributes the public key to every failed host in params.FailedHosts,
// replacing any keypairs referenced in the original DistributeReport.
//
// A new keypair is always generated even if the original keypair still exists
// on disk; the fresh keypair carries a unique comment distinct from any
// previous operation, ensuring clean targeted cleanup.
//
// For "parallel" copy mode a new local TempKeyPair is generated via
// sshkeys.Generate and its public key is distributed to each failed host via
// sshkeys.DistributePublicKey.
//
// For "hub-spoke" copy mode a new HubKeyPair is generated on params.SourceHost
// (the hub) via sshkeys.GenerateOnHub and its public key is distributed to
// each failed spoke.  The source file already resides on the hub from the
// original operation; PrepareRetryKeypairs does not re-transfer it.
//
// If distribution of the public key to any host fails, that host is recorded
// in dirty so that a future cleanup pass can attempt removal if the key was
// partially written, and distribution continues to remaining hosts.
//
// A non-nil error is returned only when keypair generation itself fails (e.g.
// ssh-keygen unavailable for parallel mode, or the hub is unreachable for
// hub-spoke mode).  In that case RetryKeypair is nil and dirty is unchanged.
func PrepareRetryKeypairs(
	ctx context.Context,
	params RetryParams,
	dirty *dirtystate.State,
) (*RetryKeypair, error) {
	if params.CopyMode == "hub-spoke" {
		return prepareHubSpokeRetryKeypair(ctx, params, dirty)
	}
	return prepareParallelRetryKeypair(ctx, params, dirty)
}

// prepareParallelRetryKeypair generates a fresh local keypair and distributes
// it to every failed destination host for a "parallel" retry.
func prepareParallelRetryKeypair(
	ctx context.Context,
	params RetryParams,
	dirty *dirtystate.State,
) (*RetryKeypair, error) {
	kp, err := sshkeys.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate retry keypair: %w", err)
	}

	for _, host := range params.FailedHosts {
		if err := sshkeys.DistributePublicKey(ctx, host, kp.PublicKey); err != nil {
			// Record in dirty state so a future pass can attempt cleanup if
			// the key was partially written to authorized_keys.
			dirty.Add(dirtystate.DirtyHost{
				Host:       host.Host,
				User:       host.User,
				Port:       host.Port,
				KeyComment: kp.Comment,
				AddedAt:    time.Now(),
			})
		}
	}

	return &RetryKeypair{LocalKP: kp}, nil
}

// prepareHubSpokeRetryKeypair generates a fresh keypair on the hub host and
// distributes it to every failed spoke for a "hub-spoke" retry.
//
// The source file is assumed to already reside on the hub from the original
// operation; this function does not re-transfer it.
func prepareHubSpokeRetryKeypair(
	ctx context.Context,
	params RetryParams,
	dirty *dirtystate.State,
) (*RetryKeypair, error) {
	hubKP, err := sshkeys.GenerateOnHub(ctx, params.SourceHost)
	if err != nil {
		return nil, fmt.Errorf("generate retry keypair on hub %s: %w",
			params.SourceHost.Host, err)
	}

	for _, spoke := range params.FailedHosts {
		if err := sshkeys.DistributePublicKey(ctx, spoke, hubKP.PublicKey); err != nil {
			// Record in dirty state for future cleanup if the key was
			// partially written to the spoke's authorized_keys.
			dirty.Add(dirtystate.DirtyHost{
				Host:       spoke.Host,
				User:       spoke.User,
				Port:       spoke.Port,
				KeyComment: hubKP.Comment,
				AddedAt:    time.Now(),
			})
		}
	}

	return &RetryKeypair{HubKP: hubKP}, nil
}
