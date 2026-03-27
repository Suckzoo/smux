package executor

import (
	"testing"
)

// ---------------------------------------------------------------------------
// TransferStatus.String
// ---------------------------------------------------------------------------

func TestTransferStatusString(t *testing.T) {
	cases := []struct {
		status TransferStatus
		want   string
	}{
		{TransferPending, "pending"},
		{TransferInProgress, "transferring"},
		{TransferDone, "done"},
		{TransferFailed, "failed"},
		{TransferStatus(99), "unknown"},
	}
	for _, tc := range cases {
		got := tc.status.String()
		if got != tc.want {
			t.Errorf("TransferStatus(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// sendProgress
// ---------------------------------------------------------------------------

// TestSendProgressNilChannel verifies that sendProgress does not panic when
// ch is nil.
func TestSendProgressNilChannel(t *testing.T) {
	// Should not panic.
	sendProgress(nil, ProgressUpdate{Status: TransferDone})
}

// TestSendProgressBuffered verifies that a message is placed on a buffered
// channel.
func TestSendProgressBuffered(t *testing.T) {
	ch := make(chan ProgressUpdate, 1)
	u := ProgressUpdate{Status: TransferInProgress}
	sendProgress(ch, u)

	select {
	case got := <-ch:
		if got.Status != TransferInProgress {
			t.Errorf("expected TransferInProgress, got %v", got.Status)
		}
	default:
		t.Error("expected update on channel, got nothing")
	}
}

// TestSendProgressFullChannelDoesNotBlock verifies that sendProgress does not
// block when the channel buffer is full; instead it silently drops the update.
func TestSendProgressFullChannelDoesNotBlock(t *testing.T) {
	ch := make(chan ProgressUpdate, 0) // zero-buffer: always full without a reader
	// Should not block.
	sendProgress(ch, ProgressUpdate{Status: TransferDone})
}
