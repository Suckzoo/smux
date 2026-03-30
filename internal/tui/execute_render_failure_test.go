package tui

import (
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// TestRenderHostProgressRows_ShowsTruncatedError verifies that a failed host
// row includes a truncated error reason after "failed".
func TestRenderHostProgressRows_ShowsTruncatedError(t *testing.T) {
	m := DistributeModel{
		width: 120,
		destHosts: []config.ResolvedHost{
			{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"},
		},
		hostProgress: map[string]executor.TransferStatus{
			"spoke-01.example.com": executor.TransferFailed,
		},
		hostErrors: map[string]string{
			"spoke-01.example.com": "ssh: connect to host 10.0.0.1 port 22: Connection refused",
		},
	}
	out := m.renderHostProgressRows()
	if !strings.Contains(out, "failed") {
		t.Error("expected 'failed' in output")
	}
	if !strings.Contains(out, "ssh: connect to host") {
		t.Error("expected truncated error message in output")
	}
}

// TestRenderHostProgressRows_CursorHighlight verifies that the row at
// progressCursor is visually distinct (contains the cursor marker).
func TestRenderHostProgressRows_CursorHighlight(t *testing.T) {
	m := DistributeModel{
		width: 120,
		destHosts: []config.ResolvedHost{
			{Host: "h1.example.com", DisplayName: "h1.example.com"},
			{Host: "h2.example.com", DisplayName: "h2.example.com"},
		},
		hostProgress: map[string]executor.TransferStatus{
			"h1.example.com": executor.TransferDone,
			"h2.example.com": executor.TransferDone,
		},
		progressCursor: 1,
	}
	out := m.renderHostProgressRows()
	if !strings.Contains(out, "▶") {
		t.Error("expected cursor marker '▶' in progress rows output")
	}
}

// TestRenderErrorOverlay_ContainsFullError verifies that the overlay renders
// the full error text.
func TestRenderErrorOverlay_ContainsFullError(t *testing.T) {
	m := DistributeModel{width: 100, height: 30}
	full := "ssh: connect to host 10.0.0.1 port 22: Connection refused\nsome extra detail"
	out := m.renderErrorOverlay(full)
	if !strings.Contains(out, full) {
		t.Errorf("overlay should contain full error text; got:\n%s", out)
	}
}
