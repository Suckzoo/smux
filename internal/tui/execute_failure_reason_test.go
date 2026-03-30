package tui

import (
	"errors"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// TestHandleProgressUpdate_StoresErrorReason verifies that a TransferFailed
// update populates hostErrors with the Err message when Stderr is empty.
func TestHandleProgressUpdate_StoresErrorReason(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{
		Host:   host,
		Status: executor.TransferFailed,
		Err:    errors.New("connection refused"),
	}
	updated, _ := m.handleProgressUpdate(u)
	got, ok := updated.hostErrors["spoke-01.example.com"]
	if !ok {
		t.Fatal("expected hostErrors entry for failed host")
	}
	if got != "connection refused" {
		t.Errorf("hostErrors reason: got %q, want %q", got, "connection refused")
	}
}

// TestHandleProgressUpdate_PrefersStderrOverErr verifies that Stderr is used
// as the failure reason when it is non-empty.
func TestHandleProgressUpdate_PrefersStderrOverErr(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{
		Host:   host,
		Status: executor.TransferFailed,
		Err:    errors.New("exit status 1"),
		Stderr: "  ssh: connect to host 10.0.0.1 port 22: Connection refused\n",
	}
	updated, _ := m.handleProgressUpdate(u)
	want := "ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if updated.hostErrors["spoke-01.example.com"] != want {
		t.Errorf("hostErrors reason: got %q, want %q", updated.hostErrors["spoke-01.example.com"], want)
	}
}

// TestHandleProgressUpdate_NoErrorOnSuccess verifies that TransferDone does
// not create a hostErrors entry.
func TestHandleProgressUpdate_NoErrorOnSuccess(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{Host: host, Status: executor.TransferDone}
	updated, _ := m.handleProgressUpdate(u)
	if _, ok := updated.hostErrors["spoke-01.example.com"]; ok {
		t.Error("hostErrors should not have entry for successful host")
	}
}
