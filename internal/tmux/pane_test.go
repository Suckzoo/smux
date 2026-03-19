package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// ConnectionState unit tests — no tmux required
// ---------------------------------------------------------------------------

// TestConnectionStateString verifies that each ConnectionState value returns
// a distinct, human-readable label from its String() method.
func TestConnectionStateString(t *testing.T) {
	tests := []struct {
		state ConnectionState
		want  string
	}{
		{StateConnecting, "connecting"},
		{StateActive, "active"},
		{StateExited, "exited"},
	}
	for _, tc := range tests {
		got := tc.state.String()
		if got != tc.want {
			t.Errorf("ConnectionState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// TestConnectionStateStringUnknown verifies that an out-of-range value returns
// "unknown" rather than panicking, so log lines are always printable.
func TestConnectionStateStringUnknown(t *testing.T) {
	var cs ConnectionState = 999
	if got := cs.String(); got != "unknown" {
		t.Errorf("ConnectionState(999).String() = %q, want %q", got, "unknown")
	}
}

// TestConnectionStateValuesAreOrdered verifies that the lifecycle constants
// are defined in forward-transition order: Connecting < Active < Exited.
// This ordering allows callers to compare states with < / > to guard backward
// transitions.
func TestConnectionStateValuesAreOrdered(t *testing.T) {
	if StateConnecting >= StateActive {
		t.Errorf("StateConnecting (%d) must be less than StateActive (%d)",
			StateConnecting, StateActive)
	}
	if StateActive >= StateExited {
		t.Errorf("StateActive (%d) must be less than StateExited (%d)",
			StateActive, StateExited)
	}
}

// ---------------------------------------------------------------------------
// PaneSession unit tests — no tmux required
// ---------------------------------------------------------------------------

// TestPaneSessionInitialState verifies that a newly constructed PaneSession
// carries the supplied PaneID, Host, and State without modification.
func TestPaneSessionInitialState(t *testing.T) {
	host := config.ResolvedHost{
		DisplayName:  "web-01",
		Host:         "web-01.example.com",
		User:         "ubuntu",
		ClusterNames: []string{"production"},
	}
	ps := PaneSession{
		PaneID: "%5",
		Host:   host,
		State:  StateConnecting,
	}

	if ps.PaneID != "%5" {
		t.Errorf("PaneID = %q, want %q", ps.PaneID, "%5")
	}
	if ps.Host.Host != "web-01.example.com" {
		t.Errorf("Host.Host = %q, want %q", ps.Host.Host, "web-01.example.com")
	}
	if ps.State != StateConnecting {
		t.Errorf("State = %v, want StateConnecting", ps.State)
	}
}

// TestPaneSessionStateTransition verifies that PaneSession.State can be
// advanced forward through the full lifecycle: Connecting → Active → Exited.
func TestPaneSessionStateTransition(t *testing.T) {
	ps := PaneSession{
		PaneID: "%1",
		Host:   config.ResolvedHost{Host: "host.example.com"},
		State:  StateConnecting,
	}

	ps.State = StateActive
	if ps.State != StateActive {
		t.Errorf("after Connecting→Active: State = %v, want StateActive", ps.State)
	}

	ps.State = StateExited
	if ps.State != StateExited {
		t.Errorf("after Active→Exited: State = %v, want StateExited", ps.State)
	}
}

// TestPaneSessionHostIsImmutableByConvention verifies (by inspection of the
// type) that Host is a value type (config.ResolvedHost, not a pointer), so
// the configuration-time data cannot be replaced after construction without
// assigning a new struct — enforcing the config/runtime separation by design.
func TestPaneSessionHostIsImmutableByConvention(t *testing.T) {
	original := config.ResolvedHost{Host: "original.example.com"}
	ps := PaneSession{PaneID: "%1", Host: original, State: StateConnecting}

	// Mutate the original; the PaneSession copy must be unaffected.
	original.Host = "mutated.example.com"
	if ps.Host.Host != "original.example.com" {
		t.Errorf("PaneSession.Host was affected by mutation of source struct; "+
			"Host.Host = %q, want %q", ps.Host.Host, "original.example.com")
	}
}

// ---------------------------------------------------------------------------
// Fake tmux helper for lifecycle integration tests
// ---------------------------------------------------------------------------

// writePaneFakeTmux writes a fake tmux shell script that handles the
// display-message subcommand used by IsPaneDead.
//
// When called with "display-message -p -t <pane> #{pane_dead}" the script:
//   - returns "1" (dead) for any pane whose ID appears in the deadPanes set.
//   - returns "0" (alive) for all other panes.
//
// All other subcommands silently succeed (exit 0) without producing output.
func writePaneFakeTmux(t *testing.T, dir string, deadPanes []string) string {
	t.Helper()

	// Build a shell case expression for each dead pane.
	var caseLines strings.Builder
	for _, pane := range deadPanes {
		caseLines.WriteString("        " + pane + ") echo 1; exit 0 ;;\n")
	}

	script := "#!/bin/sh\n" +
		`case "$1" in` + "\n" +
		"    display-message)\n" +
		// The pane target is the argument after "-t".
		// Walk the positional args to find the value after -t.
		`        target=""` + "\n" +
		`        prev=""` + "\n" +
		`        for arg in "$@"; do` + "\n" +
		`            if [ "$prev" = "-t" ]; then target="$arg"; fi` + "\n" +
		`            prev="$arg"` + "\n" +
		`        done` + "\n" +
		`        case "$target" in` + "\n" +
		caseLines.String() +
		`            *) echo 0 ;;` + "\n" +
		`        esac` + "\n" +
		"        ;;\n" +
		"esac\n" +
		"exit 0\n"

	fakeBin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatalf("writePaneFakeTmux: %v", err)
	}
	return fakeBin
}

// ---------------------------------------------------------------------------
// IsPaneDead integration tests — use fake tmux binary
// ---------------------------------------------------------------------------

// TestIsPaneDeadReturnsTrueForDeadPane verifies that IsPaneDead returns
// (true, nil) when tmux reports #{pane_dead} = 1 for the queried pane.
func TestIsPaneDeadReturnsTrueForDeadPane(t *testing.T) {
	tmpDir := t.TempDir()
	writePaneFakeTmux(t, tmpDir, []string{"%3"})
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dead, err := IsPaneDead("%3")
	if err != nil {
		t.Fatalf("IsPaneDead(%q) error = %v; want nil", "%3", err)
	}
	if !dead {
		t.Errorf("IsPaneDead(%q) = false; want true (pane is dead)", "%3")
	}
}

// TestIsPaneDeadReturnsFalseForLivePane verifies that IsPaneDead returns
// (false, nil) when tmux reports #{pane_dead} = 0 for the queried pane.
func TestIsPaneDeadReturnsFalseForLivePane(t *testing.T) {
	tmpDir := t.TempDir()
	// No dead panes configured — all panes are alive.
	writePaneFakeTmux(t, tmpDir, nil)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dead, err := IsPaneDead("%5")
	if err != nil {
		t.Fatalf("IsPaneDead(%q) error = %v; want nil", "%5", err)
	}
	if dead {
		t.Errorf("IsPaneDead(%q) = true; want false (pane is alive)", "%5")
	}
}

// TestIsPaneDeadReturnsErrorWhenTmuxFails verifies that IsPaneDead returns an
// error (and false) when tmux is not available in PATH, so callers can handle
// the failure gracefully rather than silently treating absent tmux as "alive".
func TestIsPaneDeadReturnsErrorWhenTmuxFails(t *testing.T) {
	// Point PATH at an empty dir so tmux cannot be found.
	t.Setenv("PATH", t.TempDir())

	dead, err := IsPaneDead("%1")
	if err == nil {
		t.Error("IsPaneDead() error = nil; want error when tmux is not in PATH")
	}
	if dead {
		t.Error("IsPaneDead() = true; want false when tmux query fails")
	}
}

// ---------------------------------------------------------------------------
// WatchPaneExit integration tests — use fake tmux binary
// ---------------------------------------------------------------------------

// TestWatchPaneExitClosesChannelForDeadPane verifies that WatchPaneExit closes
// its returned channel promptly when the target pane is already dead.
func TestWatchPaneExitClosesChannelForDeadPane(t *testing.T) {
	tmpDir := t.TempDir()
	writePaneFakeTmux(t, tmpDir, []string{"%7"})
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := WatchPaneExit(ctx, "%7")

	select {
	case <-ch:
		// Good — channel closed as expected.
	case <-ctx.Done():
		t.Fatal("WatchPaneExit channel was not closed within timeout for an already-dead pane")
	}
}

// TestWatchPaneExitRespectsContextCancellation verifies that WatchPaneExit
// terminates its background goroutine and closes the channel when the context
// is cancelled, even if the pane is still alive.
func TestWatchPaneExitRespectsContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	// Pane is alive (not in dead list).
	writePaneFakeTmux(t, tmpDir, nil)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())

	ch := WatchPaneExit(ctx, "%9")

	// Cancel the context almost immediately.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	select {
	case <-ch:
		// Good — channel closed after context cancellation.
	case <-time.After(3 * time.Second):
		t.Fatal("WatchPaneExit did not close its channel after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// TrackPaneExit integration tests
// ---------------------------------------------------------------------------

// TestTrackPaneExitUpdatesStateToExited verifies the primary contract of
// TrackPaneExit: when the watched pane becomes dead, ps.State is advanced to
// StateExited.
func TestTrackPaneExitUpdatesStateToExited(t *testing.T) {
	tmpDir := t.TempDir()
	writePaneFakeTmux(t, tmpDir, []string{"%2"}) // %2 is immediately dead
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ps := &PaneSession{
		PaneID: "%2",
		Host:   config.ResolvedHost{Host: "host.example.com"},
		State:  StateActive,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	TrackPaneExit(ctx, ps)

	// Poll until state transitions or timeout.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ps.State == StateExited {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if ps.State != StateExited {
		t.Errorf("ps.State = %v after pane exit; want StateExited", ps.State)
	}
}

// TestTrackPaneExitDoesNotUpdateOnContextCancel verifies that TrackPaneExit
// does NOT change ps.State when the context is cancelled before the pane exits.
func TestTrackPaneExitDoesNotUpdateOnContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	// Pane is alive — it will never die in this test.
	writePaneFakeTmux(t, tmpDir, nil)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ps := &PaneSession{
		PaneID: "%4",
		Host:   config.ResolvedHost{Host: "host.example.com"},
		State:  StateActive,
	}

	ctx, cancel := context.WithCancel(context.Background())
	TrackPaneExit(ctx, ps)

	// Cancel quickly so the goroutine takes the ctx.Done() branch.
	cancel()

	// Give the goroutine a moment to process the cancellation.
	time.Sleep(100 * time.Millisecond)

	if ps.State == StateExited {
		t.Errorf("ps.State = StateExited after context cancel; want StateActive (no pane exit occurred)")
	}
}

// TestPaneSessionSourceInspection is a source-inspection test that confirms
// the key design constraints of pane.go are preserved:
//   - ConnectionState is a typed int (not bool or string)
//   - PaneSession has PaneID, Host, and State fields
//   - IsPaneDead and WatchPaneExit and TrackPaneExit are exported
//   - #{pane_dead} is used for detection (tmux native query)
func TestPaneSessionSourceInspection(t *testing.T) {
	src, err := os.ReadFile("pane.go")
	if err != nil {
		t.Fatalf("cannot read pane.go: %v", err)
	}
	content := string(src)

	checks := []struct {
		desc string
		want string
	}{
		{"ConnectionState is a typed int", "type ConnectionState int"},
		{"PaneSession struct is defined", "type PaneSession struct"},
		{"PaneID field present", "PaneID string"},
		{"Host field is a ResolvedHost value (not pointer)", "Host config.ResolvedHost"},
		{"State field is ConnectionState", "State ConnectionState"},
		{"IsPaneDead is exported", "func IsPaneDead("},
		{"WatchPaneExit is exported", "func WatchPaneExit("},
		{"TrackPaneExit is exported", "func TrackPaneExit("},
		{"Detection uses #{pane_dead} tmux format", "#{pane_dead}"},
		{"StateConnecting defined", "StateConnecting"},
		{"StateActive defined", "StateActive"},
		{"StateExited defined", "StateExited"},
	}

	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("pane.go missing %s: expected to find %q", c.desc, c.want)
		}
	}
}
