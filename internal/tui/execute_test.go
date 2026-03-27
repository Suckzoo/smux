package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// sendMsgToDistribute sends a pre-built tea.Msg to the DistributeModel.
func sendMsgToDistribute(m DistributeModel, msg tea.Msg) (DistributeModel, tea.Cmd) {
	return m.Update(msg)
}

// advanceToExecuteStep advances a fresh DistributeModel through all wizard
// steps up to and including DistributeStepExecute by directly setting the
// step field — avoids real file-tree / SSH interaction.
func advanceToExecuteStep(t *testing.T) DistributeModel {
	t.Helper()
	m := newTestDistributeModel()
	m.step = DistributeStepExecute
	return m
}

// ---------------------------------------------------------------------------
// Execute step — pre-execution state
// ---------------------------------------------------------------------------

// TestExecuteStepInitialState verifies that a model at the Execute step has
// not yet started execution.
func TestExecuteStepInitialState(t *testing.T) {
	m := advanceToExecuteStep(t)
	if m.executeStarted {
		t.Error("expected executeStarted = false on a fresh Execute step")
	}
	if m.executeDone {
		t.Error("expected executeDone = false on a fresh Execute step")
	}
	if m.hostProgress != nil {
		t.Error("expected hostProgress = nil before execution starts")
	}
}

// TestExecuteStepViewContainsReadyPrompt verifies that the Execute step view
// contains a prompt to press Enter when execution has not yet started.
func TestExecuteStepViewContainsReadyPrompt(t *testing.T) {
	m := advanceToExecuteStep(t)
	view := m.View()
	if !strings.Contains(view, "Execute") {
		t.Error("execute step view should contain 'Execute'")
	}
}

// ---------------------------------------------------------------------------
// Enter key on Execute step
// ---------------------------------------------------------------------------

// TestExecuteStepEnterSetsStarted verifies that pressing Enter on the Execute
// step sets executeStarted = true.
func TestExecuteStepEnterSetsStarted(t *testing.T) {
	m := advanceToExecuteStep(t)
	m, _ = sendDistributeKey(m, "enter")
	if !m.executeStarted {
		t.Error("expected executeStarted = true after pressing Enter on Execute step")
	}
}

// TestExecuteStepEnterDoesNotAdvanceStep verifies that pressing Enter on the
// Execute step does not change the step number.
func TestExecuteStepEnterDoesNotAdvanceStep(t *testing.T) {
	m := advanceToExecuteStep(t)
	m, _ = sendDistributeKey(m, "enter")
	if m.step != DistributeStepExecute {
		t.Errorf("expected step to remain %d (Execute), got %d", DistributeStepExecute, m.step)
	}
}

// TestExecuteStepSecondEnterIsNoOp verifies that pressing Enter a second time
// after execution has started is a no-op (does not start a second goroutine
// or change executeStarted state).
func TestExecuteStepSecondEnterIsNoOp(t *testing.T) {
	m := advanceToExecuteStep(t)
	m, _ = sendDistributeKey(m, "enter") // first Enter: starts execution
	progressCh := m.progressCh

	m, _ = sendDistributeKey(m, "enter") // second Enter: should be no-op
	if m.progressCh != progressCh {
		t.Error("second Enter should not replace the progress channel")
	}
}

// TestExecuteStepEnterInitialisesHostProgress verifies that pressing Enter
// initialises hostProgress with TransferPending for all destination hosts.
func TestExecuteStepEnterInitialisesHostProgress(t *testing.T) {
	m := advanceToExecuteStep(t)
	// Add a couple of synthetic dest hosts directly to the model.
	m.destHosts = minimalConfig().AllResolvedHosts()
	if len(m.destHosts) == 0 {
		t.Skip("no hosts in minimalConfig; skipping host-progress init test")
	}

	m, _ = sendDistributeKey(m, "enter")

	if m.hostProgress == nil {
		t.Fatal("hostProgress should be non-nil after Enter")
	}
	for _, h := range m.destHosts {
		s, ok := m.hostProgress[h.Host]
		if !ok {
			t.Errorf("hostProgress missing entry for host %q", h.Host)
			continue
		}
		if s != executor.TransferPending {
			t.Errorf("host %q: expected TransferPending, got %v", h.Host, s)
		}
	}
}

// ---------------------------------------------------------------------------
// transferProgressMsg handling
// ---------------------------------------------------------------------------

// TestHandleProgressUpdateUpdatesHostProgress verifies that receiving a
// transferProgressMsg updates the correct host's status in hostProgress.
func TestHandleProgressUpdateUpdatesHostProgress(t *testing.T) {
	m := advanceToExecuteStep(t)
	// Seed hostProgress manually (simulates post-Enter state).
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		"host1.example.com": executor.TransferPending,
		"host2.example.com": executor.TransferPending,
	}
	// Create a fake one-buffer channel so waitForProgress has something to
	// read (prevents goroutine leak in tests).
	ch := make(chan executor.ProgressUpdate, 1)
	m.progressCh = ch

	// Deliver an InProgress update for host1.
	msg := transferProgressMsg{
		Host:   fakeResolvedHost("host1.example.com"),
		Status: executor.TransferInProgress,
	}
	m, _ = sendMsgToDistribute(m, msg)

	if got := m.hostProgress["host1.example.com"]; got != executor.TransferInProgress {
		t.Errorf("expected TransferInProgress for host1, got %v", got)
	}
	// host2 should be unchanged.
	if got := m.hostProgress["host2.example.com"]; got != executor.TransferPending {
		t.Errorf("expected TransferPending for host2, got %v", got)
	}
}

// TestHandleProgressDoneUpdatesHostProgress verifies Done status is recorded.
func TestHandleProgressDoneUpdatesHostProgress(t *testing.T) {
	m := advanceToExecuteStep(t)
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		"host1.example.com": executor.TransferInProgress,
	}
	ch := make(chan executor.ProgressUpdate, 1)
	m.progressCh = ch

	msg := transferProgressMsg{
		Host:   fakeResolvedHost("host1.example.com"),
		Status: executor.TransferDone,
	}
	m, _ = sendMsgToDistribute(m, msg)

	if got := m.hostProgress["host1.example.com"]; got != executor.TransferDone {
		t.Errorf("expected TransferDone for host1, got %v", got)
	}
}

// TestHandleProgressFailedUpdatesHostProgress verifies Failed status is recorded.
func TestHandleProgressFailedUpdatesHostProgress(t *testing.T) {
	m := advanceToExecuteStep(t)
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		"host1.example.com": executor.TransferInProgress,
	}
	ch := make(chan executor.ProgressUpdate, 1)
	m.progressCh = ch

	msg := transferProgressMsg{
		Host:   fakeResolvedHost("host1.example.com"),
		Status: executor.TransferFailed,
	}
	m, _ = sendMsgToDistribute(m, msg)

	if got := m.hostProgress["host1.example.com"]; got != executor.TransferFailed {
		t.Errorf("expected TransferFailed for host1, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// executeCompleteMsg handling
// ---------------------------------------------------------------------------

// TestHandleExecuteCompleteSetsExecuteDone verifies that receiving
// executeCompleteMsg sets executeDone = true.
func TestHandleExecuteCompleteSetsExecuteDone(t *testing.T) {
	m := advanceToExecuteStep(t)
	m.executeStarted = true
	m.hostProgress = make(map[string]executor.TransferStatus)

	m, _ = sendMsgToDistribute(m, executeCompleteMsg{})

	if !m.executeDone {
		t.Error("expected executeDone = true after executeCompleteMsg")
	}
}

// ---------------------------------------------------------------------------
// View rendering during execution
// ---------------------------------------------------------------------------

// TestExecuteStepViewShowsHostsAfterStart verifies that after execution starts
// the view contains host display names.
func TestExecuteStepViewShowsHostsAfterStart(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferInProgress,
	}

	view := m.View()
	if !strings.Contains(view, hosts[0].DisplayName) {
		t.Errorf("execute view should contain host display name %q", hosts[0].DisplayName)
	}
}

// TestExecuteStepViewShowsPendingIcon verifies that a pending host shows
// the pending icon (○) in the view.
func TestExecuteStepViewShowsPendingIcon(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferPending,
	}

	view := m.View()
	if !strings.Contains(view, "○") {
		t.Error("execute view should show ○ icon for pending host")
	}
}

// TestExecuteStepViewShowsInProgressIcon verifies that an in-progress host
// shows the transferring icon (→) in the view.
func TestExecuteStepViewShowsInProgressIcon(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferInProgress,
	}

	view := m.View()
	if !strings.Contains(view, "→") {
		t.Error("execute view should show → icon for in-progress host")
	}
}

// TestExecuteStepViewShowsDoneIcon verifies that a completed host shows ✓.
func TestExecuteStepViewShowsDoneIcon(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferDone,
	}

	view := m.View()
	if !strings.Contains(view, "✓") {
		t.Error("execute view should show ✓ icon for done host")
	}
}

// TestExecuteStepViewShowsFailedIcon verifies that a failed host shows ✗.
func TestExecuteStepViewShowsFailedIcon(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferFailed,
	}

	view := m.View()
	if !strings.Contains(view, "✗") {
		t.Error("execute view should show ✗ icon for failed host")
	}
}

// TestExecuteStepViewShowsCompletionSummary verifies that after executeDone
// the view contains a completion summary.
func TestExecuteStepViewShowsCompletionSummary(t *testing.T) {
	m := advanceToExecuteStep(t)
	hosts := minimalConfig().AllResolvedHosts()
	if len(hosts) == 0 {
		t.Skip("no hosts in minimalConfig")
	}
	m.destHosts = hosts
	m.executeStarted = true
	m.executeDone = true
	m.hostProgress = map[string]executor.TransferStatus{
		hosts[0].Host: executor.TransferDone,
	}

	view := m.View()
	// The completion summary should mention "succeeded" or "transfer(s)".
	if !strings.Contains(view, "transfer") {
		t.Error("execute view should contain completion summary text after executeDone")
	}
}

// ---------------------------------------------------------------------------
// waitForProgress
// ---------------------------------------------------------------------------

// TestWaitForProgressReturnsUpdateMsg verifies that waitForProgress returns a
// transferProgressMsg when the channel has a value.
func TestWaitForProgressReturnsUpdateMsg(t *testing.T) {
	ch := make(chan executor.ProgressUpdate, 1)
	u := executor.ProgressUpdate{
		Host:   fakeResolvedHost("host1"),
		Status: executor.TransferDone,
	}
	ch <- u

	cmd := waitForProgress(ch)
	msg := cmd()

	got, ok := msg.(transferProgressMsg)
	if !ok {
		t.Fatalf("expected transferProgressMsg, got %T", msg)
	}
	if got.Status != executor.TransferDone {
		t.Errorf("expected TransferDone, got %v", got.Status)
	}
}

// TestWaitForProgressReturnsCompleteMsgOnClose verifies that waitForProgress
// returns executeCompleteMsg when the channel is closed.
func TestWaitForProgressReturnsCompleteMsgOnClose(t *testing.T) {
	ch := make(chan executor.ProgressUpdate)
	close(ch)

	cmd := waitForProgress(ch)
	msg := cmd()

	if _, ok := msg.(executeCompleteMsg); !ok {
		t.Fatalf("expected executeCompleteMsg, got %T", msg)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeResolvedHost creates a minimal config.ResolvedHost for testing.
func fakeResolvedHost(host string) config.ResolvedHost {
	return config.ResolvedHost{
		Host:        host,
		DisplayName: host,
	}
}
