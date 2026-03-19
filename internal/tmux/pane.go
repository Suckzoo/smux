package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// ConnectionState — typed SSH session lifecycle
// ---------------------------------------------------------------------------

// ConnectionState represents the lifecycle state of an SSH session running
// inside a tmux pane. Using a typed integer constant (rather than a bare bool
// or untyped string) keeps transitions explicit and supports exhaustive switch
// statements.
//
// Valid forward transitions:
//
//	StateConnecting → StateActive → StateExited
//
// Backward transitions are not valid; a PaneSession only moves forward.
type ConnectionState int

const (
	// StateConnecting is the initial state: the SSH command has been sent to the
	// tmux pane via send-keys but no out-of-band confirmation of an established
	// session has been received yet. This is a transient state; smux immediately
	// optimistically advances to StateActive after sending the command because
	// tmux provides no reliable notification of SSH session establishment.
	StateConnecting ConnectionState = iota

	// StateActive indicates that the SSH session is presumed to be running.
	// smux enters this state as soon as the SSH command is dispatched to the
	// pane. It remains in this state until exit is detected.
	StateActive

	// StateExited is the terminal state: the SSH process has ended. The tmux
	// pane may still be visible on screen (showing the "Press 'y' to close pane"
	// post-exit prompt defined in buildSSHCommand) but the underlying SSH process
	// is no longer running.
	StateExited
)

// String returns a human-readable label for the ConnectionState value.
// The label is suitable for logging and status-line display.
func (cs ConnectionState) String() string {
	switch cs {
	case StateConnecting:
		return "connecting"
	case StateActive:
		return "active"
	case StateExited:
		return "exited"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// PaneSession — runtime entity binding a tmux pane to an SSH host
// ---------------------------------------------------------------------------

// PaneSession represents a single tmux pane that hosts an SSH session.
//
// It deliberately separates configuration-time data (Host — immutable after
// construction) from runtime data (PaneID, State — assigned/updated by tmux
// at run time). This separation honours the domain model constraint stated in
// the project specification.
//
// PaneID is the tmux pane target string returned by split-window (e.g. "%3").
// Host is the fully merged ResolvedHost that this pane is connecting to.
// State tracks where in the connection lifecycle this pane currently sits.
type PaneSession struct {
	PaneID string             // tmux pane target (e.g. "%3"); opaque, assigned at split-window time
	Host config.ResolvedHost // SSH target (configuration-time data, immutable after construction)
	State ConnectionState    // current lifecycle state; advances forward only
}

// ---------------------------------------------------------------------------
// Lifecycle detection
// ---------------------------------------------------------------------------

// IsPaneDead queries tmux to determine whether the shell running inside
// paneID has exited — i.e. whether the pane is "dead" in tmux terminology.
//
// A dead pane is still visible on screen until the user dismisses it, but its
// shell process (and therefore the SSH command running inside it) is no longer
// running.
//
// Returns:
//   - (true, nil)   — the pane is dead (SSH session has ended)
//   - (false, nil)  — the pane is still alive (SSH session is active)
//   - (false, err)  — tmux could not be queried (pane doesn't exist, tmux absent, etc.)
func IsPaneDead(paneID string) (bool, error) {
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", paneID, "#{pane_dead}",
	).Output()
	if err != nil {
		return false, fmt.Errorf("cannot query pane %s state: %w", paneID, err)
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

// WatchPaneExit polls tmux every pollInterval until paneID reports as dead
// (or the context is cancelled), then closes the returned channel.
//
// The channel is closed immediately if the pane is already dead when
// WatchPaneExit is called. Callers select on the returned channel (and
// ctx.Done()) to be notified asynchronously:
//
//	exit := tmux.WatchPaneExit(ctx, "%3")
//	select {
//	case <-exit:
//	    ps.State = tmux.StateExited
//	case <-ctx.Done():
//	    // context cancelled before pane exited
//	}
//
// If tmux cannot be queried (e.g. tmux is not installed or the pane ID is
// invalid) WatchPaneExit treats the query failure as a dead pane and closes
// the channel immediately, so callers are not blocked indefinitely.
func WatchPaneExit(ctx context.Context, paneID string) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		const pollInterval = 500 * time.Millisecond
		for {
			dead, err := IsPaneDead(paneID)
			if err != nil || dead {
				// Either the pane has died or we cannot query it; either way
				// treat it as exited so callers are not blocked indefinitely.
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}()
	return ch
}

// TrackPaneExit starts a background goroutine that watches for paneID to
// become dead and, when exit is detected, advances ps.State to StateExited.
//
// The goroutine terminates when the pane exits or when ctx is cancelled.
// TrackPaneExit returns immediately; the state update happens asynchronously.
//
// Typical call-site (immediately after dispatching the SSH command):
//
//	ps := &tmux.PaneSession{PaneID: paneID, Host: host, State: tmux.StateActive}
//	tmux.TrackPaneExit(ctx, ps)
func TrackPaneExit(ctx context.Context, ps *PaneSession) {
	exit := WatchPaneExit(ctx, ps.PaneID)
	go func() {
		select {
		case <-exit:
			ps.State = StateExited
		case <-ctx.Done():
		}
	}()
}
