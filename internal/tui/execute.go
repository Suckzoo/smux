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
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
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

	go func() {
		defer close(ch)
		ctx := context.Background()

		if len(srcPaths) == 0 || len(dstHosts) == 0 {
			// Nothing to transfer; channel closes immediately.
			return
		}

		// Use the first selected path as the source file.
		// Multiple-file selection is not yet handled; the first path is used.
		srcPath := srcPaths[0]

		// Generate a fresh temporary keypair for this operation.
		kp, err := sshkeys.Generate()
		if err != nil {
			// Signal setup failure via executeCompleteMsg embedded in the
			// channel-close path; the close triggers executeCompleteMsg with
			// zero setupErr so we need an alternative route.  We write a
			// single synthetic ProgressUpdate with status TransferFailed for
			// every host to surface the error.
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

		// Distribute the public key to all destination hosts.
		// Failures are surfaced as TransferFailed progress updates so the
		// user sees per-host granularity.
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

		// Execute the transfer phase.
		switch mode {
		case "hub-spoke":
			runHubSpokeWithProgress(ctx, srcHost, srcPath, reachable, dstPath, kp, ch)
		default: // "parallel" or empty
			executor.RunParallelWithProgress(ctx, srcHost, srcPath, reachable, dstPath, kp, ch)
		}

		// Cleanup the temporary keypair immediately after the operation
		// completes (or partially completes). CleanupAfterParallel removes
		// kp from every host that received it, records any SSH failures in
		// the persistent dirty state, and deletes the local keypair files.
		// The defer above is an additional safety net for local file cleanup.
		_ = executor.CleanupAfterParallel(ctx, kp, reachable)
	}()

	return waitForProgress(ch)
}

// runHubSpokeWithProgress orchestrates hub-and-spoke distribution with live
// progress updates.  It sends TransferInProgress + TransferDone/TransferFailed
// for the hub-push phase, then delegates to FanOutFromHubWithProgress for the
// fan-out phase.
//
// The hub is dstHosts[0].  All remaining hosts are spokes.
func runHubSpokeWithProgress(
	ctx context.Context,
	srcHost config.ResolvedHost,
	srcPath string,
	dstHosts []config.ResolvedHost,
	dstPath string,
	kp *sshkeys.TempKeyPair,
	ch chan<- executor.ProgressUpdate,
) {
	if len(dstHosts) == 0 {
		return
	}

	hub := dstHosts[0]
	spokes := dstHosts[1:]

	// --- Phase 1: push source → hub ---
	ch <- executor.ProgressUpdate{Host: hub, Status: executor.TransferInProgress}

	hubResult := executor.PushToHub(ctx, srcHost, srcPath, hub, dstPath, kp)
	if !hubResult.Success {
		ch <- executor.ProgressUpdate{
			Host:   hub,
			Status: executor.TransferFailed,
			Err:    hubResult.Err,
			Stderr: hubResult.Stderr,
		}
		// Mark remaining spokes as failed too (hub push failed, so no fan-out).
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub push failed; fan-out skipped"),
			}
		}
		return
	}
	ch <- executor.ProgressUpdate{Host: hub, Status: executor.TransferDone}

	if len(spokes) == 0 {
		return
	}

	// --- Phase 2: fan out hub → spokes ---
	// Generate a hub keypair for hub-to-spoke authentication.
	hubKP, err := sshkeys.GenerateOnHub(ctx, hub)
	if err != nil {
		for _, spoke := range spokes {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub keypair generation failed: %w", err),
			}
		}
		return
	}

	// Collect spoke hosts that successfully received the hub public key so
	// that deferred cleanup can target exactly those hosts.
	var reachableSpokes []config.ResolvedHost

	// Cleanup the hub keypair immediately when this function returns,
	// regardless of whether the fan-out succeeded or partially failed.
	// CleanupHubKeypair removes the hub's public key from every spoke's
	// authorized_keys, deletes the hub's temp key directory, and records
	// any SSH failures in the persistent dirty state.
	defer func() {
		dirty, loadErr := dirtystate.Load()
		if loadErr != nil {
			// Non-fatal: proceed with an empty state so cleanup is
			// attempted even if the existing state file is unreadable.
			dirty = &dirtystate.State{}
		}
		_ = sshkeys.CleanupHubKeypair(ctx, hubKP, reachableSpokes, dirty)
		_ = dirtystate.Save(dirty)
	}()

	// Distribute the hub's public key to all spokes.
	for _, spoke := range spokes {
		if err := sshkeys.DistributePublicKey(ctx, spoke, hubKP.PublicKey); err != nil {
			ch <- executor.ProgressUpdate{
				Host:   spoke,
				Status: executor.TransferFailed,
				Err:    fmt.Errorf("hub key distribution failed: %w", err),
			}
		} else {
			reachableSpokes = append(reachableSpokes, spoke)
		}
	}

	if len(reachableSpokes) > 0 {
		executor.FanOutFromHubWithProgress(ctx, hub, dstPath, reachableSpokes, dstPath, hubKP, ch)
	}
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
// When m.destPath is empty and there are source paths, the first source path
// is used so the file lands at the same location on the destination.
func (m DistributeModel) effectiveDestPath() string {
	if m.destPath != "" {
		return m.destPath
	}
	if len(m.sourcePaths) > 0 {
		return m.sourcePaths[0]
	}
	return "/tmp/smux-distribute"
}

// ---------------------------------------------------------------------------
// View renderer for the Execute step
// ---------------------------------------------------------------------------

// renderExecuteStepWithProgress renders the Execute step body.
//
//   - Before execution starts: shows a ready prompt with Enter to begin.
//   - During execution: shows per-host status rows with live icons.
//   - After execution: shows a summary (all done / some failed).
func (m DistributeModel) renderExecuteStepWithProgress() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder

	if !m.executeStarted {
		// Pre-execution: prompt to begin.
		sb.WriteString(headStyle.Render("Step 6 of 6: Execute Distribution"))
		sb.WriteString("\n\n")
		sb.WriteString(m.renderOperationSummary())
		sb.WriteString("\n\n")
		sb.WriteString(hintStyle.Render("enter to start  esc back  q quit"))
		return sb.String()
	}

	// Execution in progress or complete.
	sb.WriteString(headStyle.Render("Step 6 of 6: Distributing Files"))
	sb.WriteString("\n\n")
	sb.WriteString(m.renderHostProgressRows())

	if m.executeDone {
		sb.WriteString("\n")
		summary := m.renderCompletionSummary()
		sb.WriteString(summary)
		sb.WriteString("\n\n")
		sb.WriteString(hintStyle.Render("esc to exit  q quit"))
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
// current transfer status icon and host name.
func (m DistributeModel) renderHostProgressRows() string {
	pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	inProgressStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	var sb strings.Builder
	for _, host := range m.destHosts {
		status := executor.TransferPending
		if m.hostProgress != nil {
			if s, ok := m.hostProgress[host.Host]; ok {
				status = s
			}
		}

		var icon, detail string
		var style lipgloss.Style
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
			style = failedStyle
		}

		line := style.Render(fmt.Sprintf("  %s %-30s  %s", icon, host.DisplayName, detail))
		sb.WriteString(line + "\n")
	}
	return sb.String()
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
