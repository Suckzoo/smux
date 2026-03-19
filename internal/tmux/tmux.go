package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Suckzoo/smux/internal/config"
)

// ErrTmuxNotFound is returned when tmux is not installed or not in PATH.
var ErrTmuxNotFound = errors.New("tmux is not installed; please install tmux and try again")

// RunContext is a typed result that indicates whether smux is executing inside
// an existing tmux session or in a plain terminal outside of tmux. Using a
// dedicated type (rather than a bare bool) makes call-sites self-documenting
// and allows exhaustive switch statements.
type RunContext int

const (
	// ContextOutsideTmux means smux is NOT running inside a tmux session.
	// smux should bootstrap a new tmux session and re-exec itself inside it.
	ContextOutsideTmux RunContext = iota

	// ContextInsideTmux means smux is running inside an existing tmux session
	// and can proceed directly to the TUI and SSH window creation.
	ContextInsideTmux
)

// String returns a human-readable label for the RunContext value.
func (rc RunContext) String() string {
	switch rc {
	case ContextInsideTmux:
		return "inside-tmux"
	case ContextOutsideTmux:
		return "outside-tmux"
	default:
		return "unknown"
	}
}

// DetectContext inspects the TMUX environment variable and returns a typed
// RunContext indicating whether smux is currently executing inside an existing
// tmux session (ContextInsideTmux) or outside one (ContextOutsideTmux).
//
// This is the preferred API for new callers; InTmux is retained for
// backward-compatibility with existing code.
func DetectContext() RunContext {
	if os.Getenv("TMUX") != "" {
		return ContextInsideTmux
	}
	return ContextOutsideTmux
}

// CheckInstalled verifies that tmux is available in PATH.
func CheckInstalled() error {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return ErrTmuxNotFound
	}
	return nil
}

// InTmux reports whether the current process is running inside a tmux session.
// Prefer DetectContext for new call-sites that need a typed result.
func InTmux() bool {
	return DetectContext() == ContextInsideTmux
}

// Bootstrap creates a new tmux session named sessionName, re-execs smux inside
// it, and attaches. This call replaces the current process with the tmux attach.
func Bootstrap(sessionName string) error {
	smuxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}

	// Create detached session with smux running in it.
	newCmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		smuxPath)
	newCmd.Stdin = os.Stdin
	newCmd.Stdout = os.Stdout
	newCmd.Stderr = os.Stderr
	if err := newCmd.Run(); err != nil {
		// Session may already exist; attempt to attach directly.
		return attachSession(sessionName)
	}

	return attachSession(sessionName)
}

func attachSession(sessionName string) error {
	attachCmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	attachCmd.Stdin = os.Stdin
	attachCmd.Stdout = os.Stdout
	attachCmd.Stderr = os.Stderr
	return attachCmd.Run()
}

// CreateSSHWindow opens a new tmux window, splits it into one pane per host,
// applies the configured layout, and enables synchronize-panes. Keybindings
// are configured according to cfg. It returns the tmux window ID of the newly
// created window so callers can reference it later (e.g. to toggle
// synchronize-panes from the TUI).
func CreateSSHWindow(hosts []config.ResolvedHost, cfg *config.Config) (string, error) {
	if len(hosts) == 0 {
		return "", nil
	}

	// Resolve the pane layout from config (validated; defaults to "tiled").
	layout := "tiled"
	if cfg != nil {
		if l, err := cfg.EffectivePaneLayout(); err != nil {
			return "", err
		} else {
			layout = l
		}
	}

	// Create a new window in detached mode (-d) so focus stays in the smux TUI
	// window. Atomically capture both the window ID and the first pane's ID via
	// a single -F format string, avoiding a separate display-message call that
	// could race against focus changes.
	newWindowOut, err := exec.Command("tmux", "new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot create tmux window: %w", err)
	}
	parts := strings.Fields(string(newWindowOut))
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected new-window output: %q", strings.TrimSpace(string(newWindowOut)))
	}
	windowID, firstPaneID := parts[0], parts[1]

	// Send SSH command to first pane (already exists in new window).
	if err := sendKeysToTarget(firstPaneID, buildSSHCommand(hosts[0])); err != nil {
		return "", fmt.Errorf("cannot send command to pane 0: %w", err)
	}

	// Split additional panes. Each split targets the most recently created pane
	// by its unique pane ID (e.g. %3) rather than the window ID, making the
	// target unambiguous regardless of which window is currently focused.
	// A tiled layout is applied after each split so panes never become too small
	// for the next split to succeed.
	lastPaneID := firstPaneID
	for i := 1; i < len(hosts); i++ {
		splitOut, err := exec.Command(
			"tmux", "split-window", "-t", lastPaneID, "-P", "-F", "#{pane_id}",
		).Output()
		if err != nil {
			return "", fmt.Errorf("cannot split window for host %d: %w", i, err)
		}
		paneID := strings.TrimSpace(string(splitOut))

		// Re-tile after each split so there is always enough room for the next one.
		if err := exec.Command("tmux", "select-layout", "-t", windowID, "tiled").Run(); err != nil {
			return "", fmt.Errorf("cannot apply tiled layout after split %d: %w", i, err)
		}

		if err := sendKeysToTarget(paneID, buildSSHCommand(hosts[i])); err != nil {
			return "", fmt.Errorf("cannot send command to pane %d: %w", i, err)
		}
		lastPaneID = paneID
	}

	// Final layout pass to apply the configured layout to all panes.
	if err := exec.Command("tmux", "select-layout", "-t", windowID, layout).Run(); err != nil {
		return "", fmt.Errorf("cannot apply %s layout: %w", layout, err)
	}

	// Enable synchronize-panes (broadcast mode on by default).
	if err := SetSynchronizePanes(windowID, true); err != nil {
		return "", fmt.Errorf("cannot enable synchronize-panes: %w", err)
	}

	// Mark this window as smux-managed so it can be identified on smux revival.
	_ = exec.Command("tmux", "set-option", "-t", windowID, "@smux-managed", "true").Run()

	// Configure keybindings.
	if cfg != nil {
		setupKeybindings(cfg)
	}

	return windowID, nil
}

// SelectWindow switches the tmux client's focused window to the given target.
// target may be an index (e.g. "0"), a name, or any valid tmux window target.
// If the switch fails (e.g. the window doesn't exist yet) the error is silently
// ignored so that the caller is not interrupted.
func SelectWindow(target string) {
	_ = exec.Command("tmux", "select-window", "-t", target).Run()
}

// CurrentWindowID returns the tmux window ID (e.g. "@1") of the window that
// is currently active in the running session. This is used to capture the smux
// TUI window's ID at startup so SelectWindow can switch back to it after
// creating each SSH window, regardless of the user's base-index setting.
func CurrentWindowID() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot get current window ID: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("tmux returned empty window ID")
	}
	return id, nil
}

// ConfigureMouseMode enables tmux mouse support and installs pane mouse bindings
// for the running session. Call this once at session start so that click and
// double-click events are forwarded and detectable before any SSH window opens.
//
// Bindings configured:
//   - MouseDown1Pane   — select the clicked pane (no broadcast change)
//   - DoubleClick1Pane — select the clicked pane and disable synchronize-panes
func ConfigureMouseMode() {
	// Enable mouse globally so click/double-click events reach tmux bindings.
	_ = exec.Command("tmux", "set-option", "-g", "mouse", "on").Run()

	// Single click: select the clicked pane.
	// '{mouse}' is the tmux target specifier for the pane under the mouse cursor.
	_ = exec.Command("tmux", "bind-key", "-T", "root", "MouseDown1Pane",
		"select-pane", "-t", "{mouse}",
	).Run()

	// Double click: focus the clicked pane and disable broadcast (sync-panes).
	_ = exec.Command("tmux", "bind-key", "-T", "root", "DoubleClick1Pane",
		"select-pane", "-t", "{mouse}",
		";", "set-window-option", "synchronize-panes", "off",
	).Run()
}

// resolveKeybindings returns the effective broadcast-toggle and attach-pane
// key/mode pairs from config, falling back to defaults when unset.
func resolveKeybindings(cfg *config.Config) (broadcastKey, broadcastMode, attachKey, attachMode string) {
	broadcastKey = "b"
	broadcastMode = "prefix"
	attachKey = "a"
	attachMode = "prefix"
	if cfg == nil {
		return
	}
	if cfg.Keybindings.BroadcastToggle.Key != "" {
		broadcastKey = cfg.Keybindings.BroadcastToggle.Key
	}
	if cfg.Keybindings.BroadcastToggle.Mode != "" {
		broadcastMode = cfg.Keybindings.BroadcastToggle.Mode
	}
	if cfg.Keybindings.AttachPane.Key != "" {
		attachKey = cfg.Keybindings.AttachPane.Key
	}
	if cfg.Keybindings.AttachPane.Mode != "" {
		attachMode = cfg.Keybindings.AttachPane.Mode
	}
	return
}

// setupKeybindings installs session-wide tmux key bindings based on config.
func setupKeybindings(cfg *config.Config) {
	broadcastKey, broadcastMode, attachKey, attachMode := resolveKeybindings(cfg)

	// Broadcast toggle: toggle synchronize-panes for current window.
	_ = exec.Command("tmux", "bind-key", "-T", broadcastMode, broadcastKey,
		"run-shell",
		`if [ "$(tmux show-window-option -v synchronize-panes 2>/dev/null)" = "on" ]; `+
			`then tmux set-window-option synchronize-panes off; `+
			`else tmux set-window-option synchronize-panes on; fi`,
	).Run()

	// Attach pane: break focused pane to a new window.
	_ = exec.Command("tmux", "bind-key", "-T", attachMode, attachKey, "break-pane").Run()

	// Ensure mouse mode and pane click bindings are up to date (idempotent).
	ConfigureMouseMode()
}

// SetSynchronizePanes sets the synchronize-panes window option for the given
// tmux window target to on (true) or off (false). An empty windowID targets
// the currently active window.
func SetSynchronizePanes(windowID string, on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	args := []string{"set-window-option"}
	if windowID != "" {
		args = append(args, "-t", windowID)
	}
	args = append(args, "synchronize-panes", value)
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("cannot set synchronize-panes %s: %w", value, err)
	}
	return nil
}

// GetSynchronizePanes queries the current synchronize-panes state for the
// given tmux window target. Returns true when the option is "on".
// An empty windowID queries the currently active window.
func GetSynchronizePanes(windowID string) (bool, error) {
	args := []string{"show-window-option", "-v"}
	if windowID != "" {
		args = append(args, "-t", windowID)
	}
	args = append(args, "synchronize-panes")
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return false, fmt.Errorf("cannot query synchronize-panes: %w", err)
	}
	return strings.TrimSpace(string(out)) == "on", nil
}

// ToggleSynchronizePanes queries the current synchronize-panes state for the
// given tmux window target and flips it (on→off, off→on).
// An empty windowID operates on the currently active window.
func ToggleSynchronizePanes(windowID string) error {
	current, err := GetSynchronizePanes(windowID)
	if err != nil {
		return err
	}
	return SetSynchronizePanes(windowID, !current)
}

// buildSSHCommand constructs the SSH command string for a resolved host, including
// a post-exit prompt that lets the user press 'y' to kill the pane.
func buildSSHCommand(host config.ResolvedHost) string {
	args := []string{"ssh"}
	if host.User != "" {
		args = append(args, "-l", host.User)
	}
	if host.Port != 0 {
		args = append(args, "-p", strconv.Itoa(host.Port))
	}
	if host.Key != "" {
		args = append(args, "-i", host.Key)
	}
	if host.JumpHost != "" {
		args = append(args, "-J", host.JumpHost)
	}
	args = append(args, host.Host)

	sshPart := strings.Join(args, " ")
	// After SSH exits the pane transitions to PaneStateExited. The shell prompt
	// below mirrors that state in the pane's terminal, matching the exact wording
	// specified by AC 14 Sub-AC 2: "Session exited. Kill pane? (y/n)".
	return sshPart +
		`; echo "Session exited. Kill pane? (y/n)"; ` +
		`read -r _smux_r; [ "$_smux_r" = "y" ] && tmux kill-pane`
}

// ---------------------------------------------------------------------------
// Runtime state — live tmux session and pane tracking
// ---------------------------------------------------------------------------

// PaneState represents the connection lifecycle state of a single tmux pane.
// The valid lifecycle transitions are:
//
//	PaneStateConnected ──── SSH session exits ────▶ PaneStateExited
//
// When a pane enters PaneStateExited the shell inside it displays the prompt
// "Session exited. Kill pane? (y/n)" and waits for user input. Pressing 'y'
// kills the pane; any other key leaves it open (so the user can inspect the
// exit output before closing).
type PaneState int

const (
	// PaneStateConnected indicates the SSH session in the pane is active.
	// This is the initial state immediately after the SSH command is sent.
	PaneStateConnected PaneState = iota

	// PaneStateExited indicates the SSH session has terminated. The pane's
	// terminal shows "Session exited. Kill pane? (y/n)" and is awaiting
	// user input.
	PaneStateExited
)

// String returns a human-readable label for the PaneState value.
func (ps PaneState) String() string {
	switch ps {
	case PaneStateConnected:
		return "connected"
	case PaneStateExited:
		return "exited"
	default:
		return "unknown"
	}
}

// RuntimePane represents a live tmux pane that is running an SSH session.
// It captures both the tmux identity (PaneID) and the SSH target (Host),
// separating runtime concerns from configuration-time concerns.
//
// State tracks the connection lifecycle: PaneStateConnected while the SSH
// session is active, PaneStateExited once the session terminates.
type RuntimePane struct {
	PaneID string              // tmux pane identifier, e.g. "%3"
	Host   config.ResolvedHost // SSH target that this pane is connected to
	State  PaneState           // current connection lifecycle state
}

// RuntimeSession represents a live tmux window that contains one or more
// SSH panes created by smux.  It provides methods to kill individual panes
// and to remove them from the in-memory runtime state.
type RuntimeSession struct {
	WindowID string         // tmux window identifier, e.g. "@2"
	Panes    []*RuntimePane // active panes, in creation order
}

// RemovePane removes the pane with the given paneID from the session's Panes
// slice.  Returns true if the pane was found and removed, false otherwise.
// RemovePane does NOT kill the pane; call KillPane or KillAndRemovePane for that.
func (s *RuntimeSession) RemovePane(paneID string) bool {
	for i, p := range s.Panes {
		if p.PaneID == paneID {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			return true
		}
	}
	return false
}

// KillAndRemovePane kills the tmux pane identified by paneID and removes it
// from the session's Panes slice.  It is the idiomatic way to handle the
// post-SSH-exit 'y' confirmation: the shell-side script sends "tmux kill-pane"
// from within the pane, but Go code that manages pane lifetime (e.g. a monitor
// goroutine) should call this method so the runtime state stays consistent.
//
// If the tmux kill-pane command fails the pane is NOT removed from Panes, so
// the caller can retry or surface the error.
func (s *RuntimeSession) KillAndRemovePane(paneID string) error {
	if err := KillPane(paneID); err != nil {
		return err
	}
	s.RemovePane(paneID)
	return nil
}

// BreakAndRemovePane breaks the pane identified by paneID into a new tmux
// window and removes it from the session's Panes slice, keeping the runtime
// domain model consistent with the new tmux layout.
//
// This is the Go-callable counterpart of the DoubleClick1Pane tmux binding
// configured by ConfigureMouseMode: when the user double-clicks a pane tmux
// fires the native binding, but programmatic callers (e.g. tests or future
// Go-level event handling) should use this method so that the RuntimeSession
// accurately reflects how many panes remain in the original window.
//
// If the tmux break-pane command fails the pane is NOT removed from Panes, so
// the caller can retry or surface the error.
func (s *RuntimeSession) BreakAndRemovePane(paneID string) error {
	if err := BreakPane(paneID); err != nil {
		return err
	}
	s.RemovePane(paneID)
	return nil
}

// KillPane executes "tmux kill-pane -t <paneID>" to destroy the specified pane.
// An empty paneID targets the currently active pane (tmux default behaviour).
// This function does not update any in-memory runtime state; use
// RuntimeSession.KillAndRemovePane for the combined operation.
func KillPane(paneID string) error {
	args := []string{"kill-pane"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("cannot kill pane %q: %w", paneID, err)
	}
	return nil
}

// NewRuntimeSession constructs a RuntimeSession from the window ID and the
// ordered list of pane IDs returned by CreateSSHWindow.  Each pane is paired
// with its corresponding resolved host so that callers can later identify which
// SSH session lives in which pane.
func NewRuntimeSession(windowID string, paneIDs []string, hosts []config.ResolvedHost) *RuntimeSession {
	panes := make([]*RuntimePane, 0, len(paneIDs))
	for i, pid := range paneIDs {
		var host config.ResolvedHost
		if i < len(hosts) {
			host = hosts[i]
		}
		panes = append(panes, &RuntimePane{PaneID: pid, Host: host, State: PaneStateConnected})
	}
	return &RuntimeSession{WindowID: windowID, Panes: panes}
}

// ---------------------------------------------------------------------------
// Pane layout geometry — used for mouse click → pane identification
// ---------------------------------------------------------------------------

// PaneLayout describes the geometry of a single tmux pane within a window.
// X and Y are the terminal column and row of the pane's top-left corner, and
// Width/Height are its dimensions in columns/rows.
//
// PaneLayouts are obtained from a live tmux session via GetPaneLayouts and
// are passed to the TUI so that double-click coordinates can be mapped to a
// specific pane ID.
type PaneLayout struct {
	PaneID string // tmux pane identifier, e.g. "%1"
	X      int    // left column of the pane (0-based)
	Y      int    // top row of the pane (0-based)
	Width  int    // width in columns
	Height int    // height in rows
}

// GetPaneLayouts queries the current geometry of all panes in the given tmux
// window and returns a slice of PaneLayout describing each pane's position and
// size. An empty windowID targets the currently active window.
func GetPaneLayouts(windowID string) ([]PaneLayout, error) {
	const format = "#{pane_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height}"
	args := []string{"list-panes", "-F", format}
	if windowID != "" {
		args = []string{"list-panes", "-t", windowID, "-F", format}
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("cannot list pane layouts for window %q: %w", windowID, err)
	}
	return parsePaneLayouts(strings.TrimSpace(string(out)))
}

// parsePaneLayouts parses the output of "tmux list-panes -F '#{pane_id}
// #{pane_left} #{pane_top} #{pane_width} #{pane_height}'" into a slice of
// PaneLayout structs. Each non-empty line must contain exactly five
// space-delimited fields.
func parsePaneLayouts(output string) ([]PaneLayout, error) {
	var layouts []PaneLayout
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		var p PaneLayout
		_, err := fmt.Sscanf(line, "%s %d %d %d %d",
			&p.PaneID, &p.X, &p.Y, &p.Width, &p.Height)
		if err != nil {
			return nil, fmt.Errorf("cannot parse pane layout line %q: %w", line, err)
		}
		layouts = append(layouts, p)
	}
	return layouts, nil
}

// BreakPane moves the specified tmux pane to a new window using tmux break-pane.
// paneID must be a valid tmux pane target (e.g. "%1", or a window.pane pair
// like "@2.%3"). After calling BreakPane the pane is no longer part of its
// original window, so synchronize-panes in the origin window will not affect it.
// An empty paneID targets the currently active pane.
func BreakPane(paneID string) error {
	args := []string{"break-pane"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("cannot break pane %q to new window: %w", paneID, err)
	}
	return nil
}

func sendKeysToTarget(target, cmd string) error {
	return exec.Command("tmux", "send-keys", "-t", target, cmd, "Enter").Run()
}

// ---------------------------------------------------------------------------
// Window management helpers
// ---------------------------------------------------------------------------

// BaseIndex returns the tmux global base-index setting. If it cannot be
// queried it defaults to 0.
func BaseIndex() int {
	out, err := exec.Command("tmux", "show-option", "-gv", "base-index").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

// MoveWindowToFront moves the given window to the first (base-index) position,
// pushing any existing window there to a higher index. If the move fails
// (window already at front) the error is silently ignored.
func MoveWindowToFront(windowID string) {
	target := strconv.Itoa(BaseIndex())
	// Try a direct move first. If index is occupied tmux returns an error;
	// swap-window handles that case by exchanging the two windows.
	if err := exec.Command("tmux", "move-window", "-s", windowID, "-t", target).Run(); err != nil {
		_ = exec.Command("tmux", "swap-window", "-s", windowID, "-t", target).Run()
	}
}

// FindOtherSmuxWindow searches all panes in the current tmux session for a
// pane running "smux" whose window ID is different from excludeWindowID (our
// own window). Returns the window ID of the first match and true, or ("",
// false) if no other smux window exists.
func FindOtherSmuxWindow(excludeWindowID string) (string, bool) {
	// -s restricts list-panes to the current session.
	out, err := exec.Command("tmux", "list-panes", "-s", "-F", "#{window_id} #{pane_current_command}").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "smux" && parts[0] != excludeWindowID {
			return parts[0], true
		}
	}
	return "", false
}

// SetupPopupKeybinding registers a tmux key binding in the given mode that
// opens smuxPath with the --popup flag inside a tmux display-popup overlay.
func SetupPopupKeybinding(key, mode, smuxPath string) {
	_ = exec.Command("tmux", "bind-key", "-T", mode, key,
		"display-popup", "-w", "80%", "-h", "60%", "-E", smuxPath+" --popup",
	).Run()
}

// UnbindKey removes a tmux key binding in the given key table.
func UnbindKey(key, mode string) {
	_ = exec.Command("tmux", "unbind-key", "-T", mode, key).Run()
}

// CleanupKeybindings removes the broadcast-toggle and attach-pane tmux key
// bindings installed by CreateSSHWindow. Call on smux exit so stale bindings
// are not left behind for other tmux sessions.
func CleanupKeybindings(cfg *config.Config) {
	broadcastKey, broadcastMode, attachKey, attachMode := resolveKeybindings(cfg)
	UnbindKey(broadcastKey, broadcastMode)
	UnbindKey(attachKey, attachMode)
}

// SetupSmartOpenKeybinding registers a tmux key binding that runs
// smuxPath --smart-open via run-shell. Unlike SetupPopupKeybinding (which
// opens a display-popup unconditionally), --smart-open decides at runtime
// whether to show a popup or revive smux in a new window.
func SetupSmartOpenKeybinding(key, mode, smuxPath string) {
	_ = exec.Command("tmux", "bind-key", "-T", mode, key,
		"run-shell", smuxPath+" --smart-open",
	).Run()
}

// DisplayPopup opens an ephemeral tmux popup running the given command.
// The popup occupies 80% of the terminal width and 60% of the height.
func DisplayPopup(command string) {
	_ = exec.Command("tmux", "display-popup", "-w", "80%", "-h", "60%", "-E", command).Run()
}

// NewSmuxWindow creates a new tmux window running smuxPath and returns its
// window ID. The window is NOT created detached so focus switches to it.
func NewSmuxWindow(smuxPath string) (string, error) {
	out, err := exec.Command("tmux", "new-window", "-P", "-F", "#{window_id}", smuxPath).Output()
	if err != nil {
		return "", fmt.Errorf("cannot create smux window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// FindSmuxWindow returns the window ID of the first window in the current
// tmux session that has a pane running "smux". Returns ("", false) if none.
func FindSmuxWindow() (string, bool) {
	out, err := exec.Command("tmux", "list-panes", "-s", "-F", "#{window_id} #{pane_current_command}").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "smux" {
			return parts[0], true
		}
	}
	return "", false
}

// GetManagedWindows returns the IDs of all windows in the current tmux session
// that have the @smux-managed option set to "true".
func GetManagedWindows() []string {
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id} #{@smux-managed}").Output()
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "true" {
			ids = append(ids, parts[0])
		}
	}
	return ids
}

// KillOtherWindows kills all tmux windows in the current session except the
// one identified by keepWindowID. Used when the user confirms quit from the
// persistent smux TUI.
func KillOtherWindows(keepWindowID string) error {
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		return fmt.Errorf("cannot list windows: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id := strings.TrimSpace(line)
		if id == "" || id == keepWindowID {
			continue
		}
		_ = exec.Command("tmux", "kill-window", "-t", id).Run()
	}
	return nil
}

// CountOtherWindows returns the number of tmux windows in the current session
// other than keepWindowID.
func CountOtherWindows(keepWindowID string) int {
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id := strings.TrimSpace(line)
		if id != "" && id != keepWindowID {
			count++
		}
	}
	return count
}
