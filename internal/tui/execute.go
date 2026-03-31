// Execute step logic for the distribute-file wizard.
//
// This file contains:
//   - The transferProgressMsg and executeCompleteMsg tea.Msg types used to
//     route live transfer updates from background goroutines into the BubbleTea
//     event loop.
//   - waitForProgress: a tea.Cmd factory that reads one update from the
//     progress channel and converts it into a tea.Msg.
//   - startExecution: the method on DistributeModel that kicks off the
//     background transfer goroutine and initialises per-host progress state.
//   - renderExecuteStepWithProgress: the view renderer for the Execute step
//     when transfers are in flight or complete.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
	"github.com/Suckzoo/smux/internal/sshkeys"
)

// ---------------------------------------------------------------------------
// Message types
// ---------------------------------------------------------------------------

// transferProgressMsg carries a single ProgressUpdate from the background
// transfer goroutine into the BubbleTea event loop.  Received by
// DistributeModel.Update and forwarded by the parent Model.Update.
type transferProgressMsg executor.ProgressUpdate

// executeCompleteMsg is sent by the background goroutine after all transfers
// have finished and the progress channel has been drained.  It carries the
// final results and any setup error.
type executeCompleteMsg struct {
	results []executor.CopyResult
	setupErr error // non-nil when key generation or key distribution failed
}

// ---------------------------------------------------------------------------
// tea.Cmd factories
// ---------------------------------------------------------------------------

// waitForProgress returns a tea.Cmd that blocks until the next update is
// available on ch or the channel is closed.
//
//   - When an update is available it returns transferProgressMsg.
//   - When ch is closed it returns executeCompleteMsg with a nil setupErr to
//     signal that all transfers have finished (results are already reflected
//     in the model's hostProgress map by prior transferProgressMsg deliveries).
func waitForProgress(ch <-chan executor.ProgressUpdate) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			// Channel closed: all transfers are done.
			return executeCompleteMsg{}
		}
		return transferProgressMsg(update)
	}
}

// ---------------------------------------------------------------------------
// Execution launch
// ---------------------------------------------------------------------------

// startExecution initialises the per-host progress state and returns a
// tea.Cmd that launches the transfer goroutine and begins routing progress
// updates into the BubbleTea event loop.
//
// The method mutates m to set executeStarted = true and initialises
// hostProgress with TransferPending for every destination host.
//
// The returned tea.Cmd, when run by the BubbleTea runtime, will block waiting
// for the first progress update (or channel close) and return the corresponding
// message.  On receiving each transferProgressMsg the caller must return
// waitForProgress(m.progressCh) as the next command to continue draining
// the channel.
func (m *DistributeModel) startExecution() tea.Cmd {
	// Guard: only start once.
	if m.executeStarted {
		return nil
	}
	m.executeStarted = true

	// Initialise progress map with Pending for all hosts.
	m.hostProgress = make(map[string]executor.TransferStatus)
	for _, h := range m.destHosts {
		m.hostProgress[h.Host] = executor.TransferPending
	}

	// Buffered channel: give room for all InProgress + Done/Failed pairs so
	// sends from the goroutine do not block even if the TUI is slow.
	bufSize := len(m.destHosts)*2 + 4
	if bufSize < 8 {
		bufSize = 8
	}
	ch := make(chan executor.ProgressUpdate, bufSize)
	m.progressCh = ch

	// Snapshot all parameters that the goroutine needs; it must not touch m.
	srcHost := m.resolvedSourceHost()
	srcPaths := append([]string(nil), m.sourcePaths...)
	dstHosts := append([]config.ResolvedHost(nil), m.destHosts...)
	mode := m.copyMode
	dstPath := m.effectiveDestPath()
	// Snapshot the user-selected hub host (only meaningful in hub-spoke mode).
	selectedHub := m.hubHost

	// For hub-and-spoke retries, snapshot the original hub host so the retry
	// goroutine can enforce hub-first ordering.  The hub is always AllHosts[0]
	// in the original operation; FailedHosts may or may not include it.
	var retryHubHost *config.ResolvedHost
	if m.retryParams != nil && m.copyMode == "hub-spoke" && len(m.retryParams.AllHosts) > 0 {
		hub := m.retryParams.AllHosts[0]
		retryHubHost = &hub
	}

	// For non-retry hub-and-spoke mode, ensure the user-selected hub is first
	// in dstHosts so that runHubSpokeWithProgress uses it as the hub
	// (which always picks dstHosts[0]).
	if mode == "hub-spoke" && retryHubHost == nil && selectedHub.Host != "" {
		for i, h := range dstHosts {
			if h.Host == selectedHub.Host {
				if i != 0 {
					dstHosts = append(dstHosts[:i], dstHosts[i+1:]...)
					dstHosts = append([]config.ResolvedHost{selectedHub}, dstHosts...)
				}
				break
			}
		}
	}

	go func() {
		defer close(ch)
		ctx := context.Background()

		if len(srcPaths) == 0 || len(dstHosts) == 0 {
			return
		}

		srcPath := srcPaths[0]

		// Generate a fresh temporary keypair for this operation.
		kp, err := sshkeys.Generate()
		if err != nil {
			for _, h := range dstHosts {
				ch <- executor.ProgressUpdate{
					Host:   h,
					Status: executor.TransferFailed,
					Err:    fmt.Errorf("key generation failed: %w", err),
				}
			}
			return
		}
		defer func() { _ = kp.DeleteKeyFiles() }()

		switch mode {
		case "hub-spoke":
			// Spoke-pull mode: distribute key to hub only, push files to hub,
			// resolve hub's private IP, then each spoke pulls from hub.
			if retryHubHost != nil {
				runSpokePullRetryWithProgress(ctx, srcHost, srcPaths, dstHosts, dstPath, kp, ch, *retryHubHost)
			} else {
				runSpokePullWithProgress(ctx, srcHost, srcPaths, dstHosts, dstPath, kp, ch)
			}

		default: // "parallel" or empty
			// Direct-parallel mode: distribute key to all dests, mkdir, scp.
			var reachable []config.ResolvedHost
			for _, h := range dstHosts {
				if err := sshkeys.DistributePublicKey(ctx, h, kp.PublicKey); err != nil {
					ch <- executor.ProgressUpdate{
						Host:   h,
						Status: executor.TransferFailed,
						Err:    fmt.Errorf("key distribution failed: %w", err),
					}
				} else {
					reachable = append(reachable, h)
				}
			}
			if len(reachable) == 0 {
				return
			}

			var mkdirReady []config.ResolvedHost
			for _, h := range reachable {
				if mkErr := mkdirOnHost(ctx, h, dstPath, kp); mkErr != nil {
					ch <- executor.ProgressUpdate{
						Host:   h,
						Status: executor.TransferFailed,
						Err:    fmt.Errorf("mkdir -p failed: %w", mkErr),
					}
				} else {
					mkdirReady = append(mkdirReady, h)
				}
			}
			if len(mkdirReady) == 0 {
				return
			}

			executor.RunParallelWithProgress(ctx, srcHost, srcPath, mkdirReady, dstPath, kp, ch)
			_ = executor.CleanupAfterParallel(ctx, kp, mkdirReady)
		}
	}()

	return waitForProgress(ch)
}

// runSpokePullWithProgress orchestrates spoke-pull distribution with live
// progress updates. Phase 1 pushes all source files to the hub. Phase 2
// resolves the hub's private IP via CIDR, then each spoke pulls all files.
//
// The hub is dstHosts[0]. All remaining hosts are spokes.
func runSpokePullWithProgress(
	ctx context.Context,
	srcHost config.ResolvedHost,
	srcPaths []string,
	dstHosts []config.ResolvedHost,
	dstPath string,
	kp *sshkeys.TempKeyPair,
	ch chan<- executor.ProgressUpdate,
) {
	if len(dstHosts) == 0 || len(srcPaths) == 0 {
		return
	}

	hub := dstHosts[0]
	spokes := dstHosts[1:]

	// Distribute key to hub only.
	if err := sshkeys.DistributePublicKey(ctx, hub, kp.PublicKey); err != nil {
		ch <- executor.ProgressUpdate{
			Host:   hub,
			Status: executor.TransferFailed,
			Err:    fmt.Errorf("key distribution to hub failed: %w", err),
		}
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub setup failed; spoke-pull skipped"),
			}
		}
		return
	}
	defer func() { _ = executor.CleanupAfterParallel(ctx, kp, []config.ResolvedHost{hub}) }()

	// Use directory form so scp puts files inside the dir.
	hubDir := dstPath
	if !strings.HasSuffix(hubDir, "/") {
		hubDir += "/"
	}

	// --- Phase 1: push all source files to hub ---
	ch <- executor.ProgressUpdate{Host: hub, Status: executor.TransferInProgress}

	for _, srcPath := range srcPaths {
		hubResult := executor.PushToHub(ctx, srcHost, srcPath, hub, hubDir, kp)
		if !hubResult.Success {
			ch <- executor.ProgressUpdate{
				Host:   hub,
				Status: executor.TransferFailed,
				Err:    hubResult.Err,
				Stderr: hubResult.Stderr,
			}
			for _, spoke := range spokes {
				ch <- executor.ProgressUpdate{
					Host:   spoke,
					Status: executor.TransferFailed,
					Err:    fmt.Errorf("hub push failed; spoke-pull skipped"),
				}
			}
			return
		}
	}
	ch <- executor.ProgressUpdate{Host: hub, Status: executor.TransferDone}

	if len(spokes) == 0 {
		return
	}

	// Build hub file paths for all source files.
	var hubFilePaths []string
	for _, srcPath := range srcPaths {
		hubFilePaths = append(hubFilePaths, hubDir+path.Base(srcPath))
	}

	// --- Phase 2: resolve hub private IP, then spoke-pull ---
	hubPrivateIP, err := executor.ResolvePrivateIP(ctx, hub)
	if err != nil {
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub IP resolution failed: %w", err),
			}
		}
		return
	}

	privKeyContent, err := kp.PrivateKeyContent()
	if err != nil {
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("read private key: %w", err),
			}
		}
		return
	}

	hubUser := hub.User
	if hubUser == "" {
		hubUser = "root"
	}

	executor.SpokePullWithProgress(ctx, spokes, hubUser, hubPrivateIP, hubFilePaths, hubDir, privKeyContent, ch)
}

// runSpokePullRetryWithProgress is the retry-path counterpart of
// runSpokePullWithProgress. It enforces hub-first ordering using the known
// original hub host (retryHub).
//
//   - If retryHub IS in dstHosts (the hub failed previously): re-push source →
//     hub first. If this push fails, all spokes are reported as failed.
//   - If retryHub is NOT in dstHosts (only spokes failed): skip the push phase
//     and go straight to spoke-pull. The file is assumed to already exist on
//     the hub from the prior operation.
func runSpokePullRetryWithProgress(
	ctx context.Context,
	srcHost config.ResolvedHost,
	srcPaths []string,
	dstHosts []config.ResolvedHost,
	dstPath string,
	kp *sshkeys.TempKeyPair,
	ch chan<- executor.ProgressUpdate,
	retryHub config.ResolvedHost,
) {
	if len(dstHosts) == 0 {
		return
	}

	// Distribute key to hub for auth.
	if err := sshkeys.DistributePublicKey(ctx, retryHub, kp.PublicKey); err != nil {
		for _, h := range dstHosts {
			ch <- executor.ProgressUpdate{
				Host:   h,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("key distribution to hub failed: %w", err),
			}
		}
		return
	}
	defer func() { _ = executor.CleanupAfterParallel(ctx, kp, []config.ResolvedHost{retryHub}) }()

	// Partition dstHosts: find hub (if present) and collect spokes.
	hubIdx := -1
	for i, h := range dstHosts {
		if h.Host == retryHub.Host {
			hubIdx = i
			break
		}
	}

	var spokes []config.ResolvedHost
	for i, h := range dstHosts {
		if i != hubIdx {
			spokes = append(spokes, h)
		}
	}

	// Compute hub file paths (same logic as runSpokePullWithProgress).
	hubDir := dstPath
	if !strings.HasSuffix(hubDir, "/") {
		hubDir += "/"
	}
	var hubFilePaths []string
	for _, sp := range srcPaths {
		hubFilePaths = append(hubFilePaths, hubDir+path.Base(sp))
	}

	// Phase 1 (conditional): re-push all source files to hub if hub is in retry set.
	if hubIdx >= 0 {
		ch <- executor.ProgressUpdate{Host: retryHub, Status: executor.TransferInProgress}

		for _, srcPath := range srcPaths {
			hubResult := executor.PushToHub(ctx, srcHost, srcPath, retryHub, hubDir, kp)
			if !hubResult.Success {
				ch <- executor.ProgressUpdate{
					Host:   retryHub,
					Status: executor.TransferFailed,
					Err:    hubResult.Err,
					Stderr: hubResult.Stderr,
				}
				for _, spoke := range spokes {
					ch <- executor.ProgressUpdate{
						Host:   spoke,
						Status: executor.TransferFailed,
						Err:    fmt.Errorf("hub retry failed; spoke-pull skipped"),
					}
				}
				return
			}
		}
		ch <- executor.ProgressUpdate{Host: retryHub, Status: executor.TransferDone}
	}

	if len(spokes) == 0 {
		return
	}

	// Phase 2: resolve hub private IP, then spoke-pull.
	hubPrivateIP, err := executor.ResolvePrivateIP(ctx, retryHub)
	if err != nil {
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub IP resolution failed: %w", err),
			}
		}
		return
	}

	privKeyContent, err := kp.PrivateKeyContent()
	if err != nil {
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("read private key: %w", err),
			}
		}
		return
	}

	hubUser := retryHub.User
	if hubUser == "" {
		hubUser = "root"
	}

	executor.SpokePullWithProgress(ctx, spokes, hubUser, hubPrivateIP, hubFilePaths, hubDir, privKeyContent, ch)
}

// ---------------------------------------------------------------------------
// mkdir -p helper
// ---------------------------------------------------------------------------

// mkdirOnHost SSHes to host using kp's private key and runs "mkdir -p <dir>"
// where dir is the parent directory of destPath.  This ensures the destination
// directory exists before scp transfers data.
//
// The function uses the same SSH options (BatchMode, StrictHostKeyChecking,
// ConnectTimeout) as the SCP transfers so that behaviour is consistent.
// Errors are returned so the caller can mark the host as failed.
func mkdirOnHost(ctx context.Context, host config.ResolvedHost, destPath string, kp *sshkeys.TempKeyPair) error {
	// Derive the directory to create: use the parent directory of destPath so
	// that a caller-supplied file path like /home/user/dir/file.txt creates
	// /home/user/dir, and a directory path like /home/user/dir/ creates
	// /home/user/dir (path.Dir strips the trailing slash).
	dir := path.Dir(destPath)
	if dir == "" || dir == "." {
		// Relative or bare filename: nothing useful to mkdir.
		return nil
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-i", kp.PrivateKeyPath,
	}
	if host.User != "" {
		args = append(args, "-l", host.User)
	}
	if host.Port != 0 {
		args = append(args, "-p", strconv.Itoa(host.Port))
	}
	if host.JumpHost != "" {
		args = append(args, "-J", host.JumpHost)
	}
	args = append(args, host.EffectiveAddress(), "--", "mkdir -p "+shellEscapePath(dir))

	var stderrBuf strings.Builder
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkdir -p %q on %s: %w (stderr: %s)",
			dir, host.Host, err, strings.TrimSpace(stderrBuf.String()))
	}
	return nil
}

// shellEscapePath wraps p in single quotes and escapes any embedded single
// quotes so the result is safe to pass as a POSIX shell argument.
func shellEscapePath(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// Helper accessors on DistributeModel
// ---------------------------------------------------------------------------

// resolvedSourceHost returns a config.ResolvedHost for the chosen source
// origin.  When m.sourceHost is empty the source is the local machine and an
// empty ResolvedHost is returned (RunParallel treats Host=="" as local).
func (m DistributeModel) resolvedSourceHost() config.ResolvedHost {
	if m.sourceHost == "" {
		return config.ResolvedHost{}
	}
	// Find the full ResolvedHost by SSH alias.
	for _, h := range m.destHostItems {
		if h.Host == m.sourceHost {
			return h
		}
	}
	// Fallback: minimal struct with just the alias.
	return config.ResolvedHost{Host: m.sourceHost}
}

// effectiveDestPath returns the destination path to use for each host.
// The user must always supply a non-empty destPath in DistributeStepDestPath;
// there is no implicit fallback to the source path or any default directory.
func (m DistributeModel) effectiveDestPath() string {
	return m.destPath
}

// ---------------------------------------------------------------------------
// View renderer for the Execute step
// ---------------------------------------------------------------------------

// renderExecuteStepWithProgress renders the Execute step body.
//
//   - Before execution starts: shows a ready prompt with Enter to begin.
//   - During execution: shows per-host status rows with live icons.
//   - After execution: shows a summary (all done / some failed).
//   - When errorOverlay is set: shows the full-error overlay instead.
func (m DistributeModel) renderExecuteStepWithProgress() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Show error overlay when active (takes over the whole step content area).
	if m.errorOverlay != nil {
		return m.renderErrorOverlay(*m.errorOverlay)
	}

	var sb strings.Builder

	if !m.executeStarted {
		// Pre-execution: prompt to begin.
		sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: Execute Distribution", m.stepIndex()+1, m.totalSteps())))
		sb.WriteString("\n\n")
		sb.WriteString(m.renderOperationSummary())
		sb.WriteString("\n\n")
		sb.WriteString(hintStyle.Render("enter to start  esc back  q host list"))
		return sb.String()
	}

	// Execution in progress or complete.
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: Distributing Files", m.stepIndex()+1, m.totalSteps())))
	sb.WriteString("\n\n")
	sb.WriteString(m.renderHostProgressRows())

	if m.executeDone {
		sb.WriteString("\n")
		sb.WriteString(m.renderCompletionSummary())
		sb.WriteString("\n\n")
		if len(m.failedHosts()) > 0 {
			sb.WriteString(hintStyle.Render("j/k select  enter view error  r retry failed  esc back  q host list"))
		} else {
			sb.WriteString(hintStyle.Render("esc back  q host list"))
		}
	}

	return sb.String()
}

// renderOperationSummary shows a one-line summary of what will be transferred.
func (m DistributeModel) renderOperationSummary() string {
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

	srcHost := m.sourceHost
	if srcHost == "" {
		srcHost = "local"
	}
	srcPath := "(no file)"
	if len(m.sourcePaths) > 0 {
		srcPath = m.sourcePaths[0]
	}
	nDest := len(m.destHosts)
	mode := m.copyMode
	if mode == "" {
		mode = "parallel"
	}

	return bodyStyle.Render(fmt.Sprintf(
		"Source: %s:%s\nDestinations: %d host(s)\nMode: %s\nDest path: %s",
		srcHost, srcPath, nDest, mode, m.effectiveDestPath(),
	))
}

// renderHostProgressRows renders one row per destination host showing the
// current transfer status icon, host name, and (for failed hosts) a truncated
// error reason. The row at progressCursor is highlighted with a cursor marker.
func (m DistributeModel) renderHostProgressRows() string {
	pendingStyle    := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	inProgressStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	doneStyle       := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failedStyle     := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle     := lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Bold(true)

	// Reserve space for "  X name-padded-to-30  " prefix; rest for truncated error.
	maxLineWidth := m.width - 6
	if maxLineWidth < 40 {
		maxLineWidth = 40
	}

	var sb strings.Builder
	for i, host := range m.destHosts {
		status := executor.TransferPending
		if m.hostProgress != nil {
			if s, ok := m.hostProgress[host.Host]; ok {
				status = s
			}
		}

		var icon string
		var style lipgloss.Style
		var detail string
		switch status {
		case executor.TransferPending:
			icon = "○"
			detail = "pending"
			style = pendingStyle
		case executor.TransferInProgress:
			icon = "→"
			detail = "transferring…"
			style = inProgressStyle
		case executor.TransferDone:
			icon = "✓"
			detail = "done"
			style = doneStyle
		case executor.TransferFailed:
			icon = "✗"
			detail = "failed"
			if m.hostErrors != nil {
				if reason, ok := m.hostErrors[host.Host]; ok && reason != "" {
					prefix := "failed: "
					// available chars after "  X name-padded-30  prefix"
					available := maxLineWidth - 2 - 1 - 30 - 2 - len(prefix)
					if available < 10 {
						available = 10
					}
					truncated := reason
					if len(truncated) > available {
						truncated = truncated[:available-1] + "…"
					}
					detail = prefix + truncated
				}
			}
			style = failedStyle
		}

		isCursor := i == m.progressCursor
		line := fmt.Sprintf("  %s %-30s  %s", icon, host.DisplayName, detail)
		if isCursor {
			// Replace the leading space with the cursor arrow.
			line = cursorStyle.Render("▶" + line[1:])
		} else {
			line = style.Render(line)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

// renderErrorOverlay renders a full-screen overlay box showing the complete
// error text for a failed host. Dismissed with Esc.
//
// The full error text is embedded verbatim so that callers inspecting the raw
// output can use strings.Contains to verify the error is present.
func (m DistributeModel) renderErrorOverlay(fullError string) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hintStyle  := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	// Build the overlay as a sequence of lines joined by newlines so that the
	// full error text (which may itself contain newlines) is never padded or
	// word-wrapped by lipgloss — preserving the literal string for tests.
	var sb strings.Builder
	sb.WriteString(borderStyle.Render("╭── Transfer Error ──────────────────────────────────────────╮"))
	sb.WriteString("\n")
	sb.WriteString(borderStyle.Render("│"))
	sb.WriteString("\n")
	sb.WriteString("  " + titleStyle.Render("Transfer Error"))
	sb.WriteString("\n\n")
	sb.WriteString("  " + fullError)
	sb.WriteString("\n\n")
	sb.WriteString("  " + hintStyle.Render("esc to close"))
	sb.WriteString("\n")
	sb.WriteString(borderStyle.Render("╰────────────────────────────────────────────────────────────╯"))
	return sb.String()
}

// failedHosts returns the subset of m.destHosts whose transfer status is
// TransferFailed.  It is used to determine whether a retry is possible and to
// build the RetryParams passed to NewRetryDistributeModel.
//
// Returns nil (not an empty slice) when there are no failures, so callers can
// use len(m.failedHosts()) > 0 as the guard for retry availability.
func (m DistributeModel) failedHosts() []config.ResolvedHost {
	var failed []config.ResolvedHost
	for _, h := range m.destHosts {
		if m.hostProgress != nil && m.hostProgress[h.Host] == executor.TransferFailed {
			failed = append(failed, h)
		}
	}
	return failed
}

// renderCompletionSummary renders a brief done/failed count after all
// transfers complete.
func (m DistributeModel) renderCompletionSummary() string {
	doneStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	failStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))

	var nDone, nFailed int
	for _, h := range m.destHosts {
		s := m.hostProgress[h.Host]
		switch s {
		case executor.TransferDone:
			nDone++
		case executor.TransferFailed:
			nFailed++
		}
	}

	if nFailed == 0 {
		return doneStyle.Render(fmt.Sprintf("  All %d transfer(s) completed successfully.", nDone))
	}
	return strings.Join([]string{
		doneStyle.Render(fmt.Sprintf("  %d transfer(s) succeeded.", nDone)),
		failStyle.Render(fmt.Sprintf("  %d transfer(s) failed.", nFailed)),
	}, "\n")
}
