package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

func makeExecuteDoneModel() DistributeModel {
	hosts := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1.example.com"},
		{Host: "h2.example.com", DisplayName: "h2.example.com"},
		{Host: "h3.example.com", DisplayName: "h3.example.com"},
	}
	return DistributeModel{
		step:           DistributeStepExecute,
		destHosts:      hosts,
		executeStarted: true,
		executeDone:    true,
		hostProgress: map[string]executor.TransferStatus{
			"h1.example.com": executor.TransferFailed,
			"h2.example.com": executor.TransferDone,
			"h3.example.com": executor.TransferFailed,
		},
		hostErrors: map[string]string{
			"h1.example.com": "connection refused",
			"h3.example.com": "timeout",
		},
		progressCursor: 0,
	}
}

// TestExecuteStep_CursorMovesDown verifies that pressing 'j' increments progressCursor.
func TestExecuteStep_CursorMovesDown(t *testing.T) {
	m := makeExecuteDoneModel()
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.progressCursor != 1 {
		t.Errorf("progressCursor after j: got %d, want 1", updated.progressCursor)
	}
}

// TestExecuteStep_CursorMovesUp verifies that pressing 'k' decrements progressCursor.
func TestExecuteStep_CursorMovesUp(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 2
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if updated.progressCursor != 1 {
		t.Errorf("progressCursor after k: got %d, want 1", updated.progressCursor)
	}
}

// TestExecuteStep_CursorClampsAtBottom verifies that j does not exceed len(destHosts)-1.
func TestExecuteStep_CursorClampsAtBottom(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 2
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.progressCursor != 2 {
		t.Errorf("cursor should clamp at bottom; got %d", updated.progressCursor)
	}
}

// TestExecuteStep_CursorClampsAtTop verifies that k does not go below 0.
func TestExecuteStep_CursorClampsAtTop(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 0
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if updated.progressCursor != 0 {
		t.Errorf("cursor should clamp at top; got %d", updated.progressCursor)
	}
}

// TestExecuteStep_EnterOpenOverlayForFailedHost verifies that Enter on a
// failed host sets errorOverlay to the host's error reason.
func TestExecuteStep_EnterOpenOverlayForFailedHost(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 0 // h1 = failed
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.errorOverlay == nil {
		t.Fatal("expected errorOverlay to be set after Enter on failed host")
	}
	if *updated.errorOverlay != "connection refused" {
		t.Errorf("errorOverlay: got %q, want %q", *updated.errorOverlay, "connection refused")
	}
}

// TestExecuteStep_EnterNoOverlayForSuccessHost verifies that Enter on a
// successful host does not open the overlay.
func TestExecuteStep_EnterNoOverlayForSuccessHost(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 1 // h2 = done
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.errorOverlay != nil {
		t.Error("errorOverlay should not be set for a successful host")
	}
}

// TestExecuteStep_EscWithOverlayClosesOverlay verifies that Esc clears the
// overlay without exiting the wizard.
func TestExecuteStep_EscWithOverlayClosesOverlay(t *testing.T) {
	m := makeExecuteDoneModel()
	reason := "connection refused"
	m.errorOverlay = &reason
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.errorOverlay != nil {
		t.Error("Esc should clear errorOverlay")
	}
	if updated.exitToMain {
		t.Error("Esc should not set exitToMain while overlay is open")
	}
}

// TestExecuteStep_EscAfterDoneExitsToMain verifies that Esc after execution
// completes (with no overlay) sets exitToMain.
func TestExecuteStep_EscAfterDoneExitsToMain(t *testing.T) {
	m := makeExecuteDoneModel()
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !updated.exitToMain {
		t.Error("Esc after executeDone should set exitToMain")
	}
	if !updated.done {
		t.Error("Esc after executeDone should set done")
	}
}
