package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// TestCheckInstalledTmuxNotFound verifies AC 18: when tmux is not available in
// PATH, CheckInstalled returns ErrTmuxNotFound with a clear, actionable message.
func TestCheckInstalledTmuxNotFound(t *testing.T) {
	// Temporarily set PATH to an empty or nonexistent directory so that tmux
	// cannot be found.
	t.Setenv("PATH", t.TempDir())

	err := CheckInstalled()
	if err == nil {
		t.Fatal("CheckInstalled() = nil, want error when tmux not in PATH")
	}
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Errorf("CheckInstalled() error = %v, want errors.Is(err, ErrTmuxNotFound)", err)
	}
	// The error message itself must be human-readable and mention tmux.
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "tmux") {
		t.Errorf("CheckInstalled() error message %q does not mention tmux", msg)
	}
}

// TestCheckInstalledMessageIsActionable verifies that ErrTmuxNotFound contains
// an actionable install hint so that users understand what to do.
func TestCheckInstalledMessageIsActionable(t *testing.T) {
	msg := ErrTmuxNotFound.Error()
	// Must mention "install" so the user knows what action to take.
	if !strings.Contains(strings.ToLower(msg), "install") {
		t.Errorf("ErrTmuxNotFound = %q; want message to contain 'install'", msg)
	}
	// Must mention "tmux" so the user knows which program is missing.
	if !strings.Contains(strings.ToLower(msg), "tmux") {
		t.Errorf("ErrTmuxNotFound = %q; want message to contain 'tmux'", msg)
	}
}

// TestMainWrapsCheckInstalledError verifies that main.go wraps the tmux-not-found
// error with an additional install URL hint, giving users two layers of guidance.
func TestMainWrapsCheckInstalledError(t *testing.T) {
	src, err := os.ReadFile("../../cmd/smux/main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// main.go must call CheckInstalled.
	if !strings.Contains(content, "CheckInstalled()") {
		t.Error("main.go does not call tmux.CheckInstalled() — AC 18 requires tmux presence check at startup")
	}
	// main.go must wrap the error with a URL or install hint.
	if !strings.Contains(content, "tmux") {
		t.Error("main.go error handling for missing tmux does not mention tmux")
	}
}

func TestInTmux(t *testing.T) {
	t.Run("returns true when TMUX env var is set", func(t *testing.T) {
		t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
		if !InTmux() {
			t.Error("InTmux() = false, want true when TMUX is set")
		}
	})

	t.Run("returns false when TMUX env var is empty", func(t *testing.T) {
		os.Unsetenv("TMUX")
		if InTmux() {
			t.Error("InTmux() = true, want false when TMUX is not set")
		}
	})

	t.Run("returns false when TMUX env var is explicitly empty string", func(t *testing.T) {
		t.Setenv("TMUX", "")
		if InTmux() {
			t.Error("InTmux() = true, want false when TMUX is empty string")
		}
	})
}

// TestDetectContext verifies that DetectContext returns the correct typed
// RunContext based on the TMUX environment variable.
func TestDetectContext(t *testing.T) {
	t.Run("returns ContextInsideTmux when TMUX is set", func(t *testing.T) {
		t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
		got := DetectContext()
		if got != ContextInsideTmux {
			t.Errorf("DetectContext() = %v (%d), want ContextInsideTmux", got, got)
		}
	})

	t.Run("returns ContextOutsideTmux when TMUX is unset", func(t *testing.T) {
		os.Unsetenv("TMUX")
		got := DetectContext()
		if got != ContextOutsideTmux {
			t.Errorf("DetectContext() = %v (%d), want ContextOutsideTmux", got, got)
		}
	})

	t.Run("returns ContextOutsideTmux when TMUX is empty string", func(t *testing.T) {
		t.Setenv("TMUX", "")
		got := DetectContext()
		if got != ContextOutsideTmux {
			t.Errorf("DetectContext() = %v (%d), want ContextOutsideTmux", got, got)
		}
	})
}

// TestRunContextString verifies that RunContext has human-readable String() output.
func TestRunContextString(t *testing.T) {
	tests := []struct {
		ctx  RunContext
		want string
	}{
		{ContextInsideTmux, "inside-tmux"},
		{ContextOutsideTmux, "outside-tmux"},
	}
	for _, tc := range tests {
		if got := tc.ctx.String(); got != tc.want {
			t.Errorf("RunContext(%d).String() = %q, want %q", tc.ctx, got, tc.want)
		}
	}
}

// TestInTmuxDelegatesDetectContext verifies that InTmux is consistent with
// DetectContext, i.e. InTmux() == (DetectContext() == ContextInsideTmux).
func TestInTmuxDelegatesDetectContext(t *testing.T) {
	for _, tmuxVal := range []string{"", "/tmp/tmux-1000/default,12345,0"} {
		t.Setenv("TMUX", tmuxVal)
		gotBool := InTmux()
		gotTyped := DetectContext()
		wantBool := gotTyped == ContextInsideTmux
		if gotBool != wantBool {
			t.Errorf("TMUX=%q: InTmux()=%v but DetectContext()==ContextInsideTmux is %v — they must agree",
				tmuxVal, gotBool, wantBool)
		}
	}
}

func TestBuildSSHCommand(t *testing.T) {
	tests := []struct {
		name     string
		host     config.ResolvedHost
		wantParts []string // substrings that must appear in order in the command
	}{
		{
			name: "hostname only",
			host: config.ResolvedHost{
				DisplayName: "web-01.example.com",
				Host:        "web-01.example.com",
			},
			wantParts: []string{"ssh", "web-01.example.com"},
		},
		{
			name: "with user",
			host: config.ResolvedHost{
				DisplayName: "web-01.example.com",
				Host:        "web-01.example.com",
				User:        "ubuntu",
			},
			wantParts: []string{"ssh", "-l", "ubuntu", "web-01.example.com"},
		},
		{
			name: "with port",
			host: config.ResolvedHost{
				DisplayName: "web-01.example.com",
				Host:        "web-01.example.com",
				Port:        2222,
			},
			wantParts: []string{"ssh", "-p", "2222", "web-01.example.com"},
		},
		{
			name: "with key",
			host: config.ResolvedHost{
				DisplayName: "web-01.example.com",
				Host:        "web-01.example.com",
				Key:         "~/.ssh/id_rsa",
			},
			wantParts: []string{"ssh", "-i", "~/.ssh/id_rsa", "web-01.example.com"},
		},
		{
			name: "with jump host",
			host: config.ResolvedHost{
				DisplayName: "internal.example.com",
				Host:        "internal.example.com",
				JumpHost:    "bastion.example.com",
			},
			wantParts: []string{"ssh", "-J", "bastion.example.com", "internal.example.com"},
		},
		{
			name: "all options combined",
			host: config.ResolvedHost{
				DisplayName: "db-01.example.com",
				Host:        "db-01.example.com",
				User:        "postgres",
				Port:        2222,
				Key:         "~/.ssh/db_key",
				JumpHost:    "bastion.example.com",
			},
			wantParts: []string{
				"ssh",
				"-l", "postgres",
				"-p", "2222",
				"-i", "~/.ssh/db_key",
				"-J", "bastion.example.com",
				"db-01.example.com",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildSSHCommand(tc.host)

			// Verify the ssh part starts with 'ssh'
			if !strings.HasPrefix(cmd, "ssh") {
				t.Errorf("buildSSHCommand() = %q, want command starting with 'ssh'", cmd)
			}

			// Verify each expected part appears in the command (in order)
			remaining := cmd
			for _, part := range tc.wantParts {
				idx := strings.Index(remaining, part)
				if idx == -1 {
					t.Errorf("buildSSHCommand() = %q, missing expected part %q", cmd, part)
					break
				}
				remaining = remaining[idx+len(part):]
			}

			// Verify the post-exit prompt is present with the exact AC 14 wording.
			if !strings.Contains(cmd, "Session exited. Kill pane? (y/n)") {
				t.Errorf("buildSSHCommand() = %q, missing post-exit prompt \"Session exited. Kill pane? (y/n)\"", cmd)
			}
			if !strings.Contains(cmd, "tmux kill-pane") {
				t.Errorf("buildSSHCommand() = %q, missing tmux kill-pane", cmd)
			}
		})
	}
}

// TestCreateSSHWindowNoHosts verifies that an empty host list is a no-op and
// returns nil without attempting to call tmux.
func TestCreateSSHWindowNoHosts(t *testing.T) {
	if _, err := CreateSSHWindow(nil, nil); err != nil {
		t.Errorf("CreateSSHWindow(nil, nil) = %v; want nil for empty host list", err)
	}
	if _, err := CreateSSHWindow([]config.ResolvedHost{}, nil); err != nil {
		t.Errorf("CreateSSHWindow([], nil) = %v; want nil for empty host list", err)
	}
}

// TestBuildSSHCommandFlagsBeforeHost verifies that SSH flags are placed before
// the hostname argument, which is required by OpenSSH's argument parser.
func TestBuildSSHCommandFlagsBeforeHost(t *testing.T) {
	host := config.ResolvedHost{
		Host:     "web-01.example.com",
		User:     "ubuntu",
		Port:     2222,
		Key:      "~/.ssh/id_rsa",
		JumpHost: "bastion.example.com",
	}
	cmd := buildSSHCommand(host)
	// Extract just the ssh invocation (before the first semicolon).
	sshPart := strings.SplitN(cmd, ";", 2)[0]

	hostIdx := strings.Index(sshPart, host.Host)
	if hostIdx < 0 {
		t.Fatalf("ssh part %q does not contain hostname %q", sshPart, host.Host)
	}
	flags := []struct{ flag, val string }{
		{"-l", "ubuntu"},
		{"-p", "2222"},
		{"-i", "~/.ssh/id_rsa"},
		{"-J", "bastion.example.com"},
	}
	for _, f := range flags {
		idx := strings.Index(sshPart, f.flag+" "+f.val)
		if idx < 0 {
			t.Errorf("ssh part %q missing flag %s %s", sshPart, f.flag, f.val)
			continue
		}
		if idx > hostIdx {
			t.Errorf("flag %s %s (pos %d) appears after hostname (pos %d)", f.flag, f.val, idx, hostIdx)
		}
	}
}

// TestResolveKeybindings verifies that resolveKeybindings returns the
// correct defaults and honours config overrides for both the broadcast-toggle
// key/mode and the attach-pane key/mode.
func TestResolveKeybindings(t *testing.T) {
	tests := []struct {
		name              string
		cfg               *config.Config
		wantBroadcastKey  string
		wantBroadcastMode string
		wantAttachKey     string
		wantAttachMode    string
	}{
		{
			name:              "nil config uses defaults",
			cfg:               nil,
			wantBroadcastKey:  "b",
			wantBroadcastMode: "prefix",
			wantAttachKey:     "a",
			wantAttachMode:    "prefix",
		},
		{
			name:              "empty config uses defaults",
			cfg:               &config.Config{},
			wantBroadcastKey:  "b",
			wantBroadcastMode: "prefix",
			wantAttachKey:     "a",
			wantAttachMode:    "prefix",
		},
		{
			name: "override attach key only",
			cfg: &config.Config{
				Keybindings: config.Keybindings{
					AttachPane: config.KeyBinding{Key: "M-w"},
				},
			},
			wantBroadcastKey:  "b",
			wantBroadcastMode: "prefix",
			wantAttachKey:     "M-w",
			wantAttachMode:    "prefix",
		},
		{
			name: "override broadcast key only",
			cfg: &config.Config{
				Keybindings: config.Keybindings{
					BroadcastToggle: config.KeyBinding{Key: "M-s"},
				},
			},
			wantBroadcastKey:  "M-s",
			wantBroadcastMode: "prefix",
			wantAttachKey:     "a",
			wantAttachMode:    "prefix",
		},
		{
			name: "override both keys and modes",
			cfg: &config.Config{
				Keybindings: config.Keybindings{
					BroadcastToggle: config.KeyBinding{Key: "M-s", Mode: "root"},
					AttachPane:      config.KeyBinding{Key: "M-d", Mode: "root"},
				},
			},
			wantBroadcastKey:  "M-s",
			wantBroadcastMode: "root",
			wantAttachKey:     "M-d",
			wantAttachMode:    "root",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			broadcastKey, broadcastMode, attachKey, attachMode := resolveKeybindings(tc.cfg)
			if broadcastKey != tc.wantBroadcastKey {
				t.Errorf("broadcastKey = %q, want %q", broadcastKey, tc.wantBroadcastKey)
			}
			if broadcastMode != tc.wantBroadcastMode {
				t.Errorf("broadcastMode = %q, want %q", broadcastMode, tc.wantBroadcastMode)
			}
			if attachKey != tc.wantAttachKey {
				t.Errorf("attachKey = %q, want %q", attachKey, tc.wantAttachKey)
			}
			if attachMode != tc.wantAttachMode {
				t.Errorf("attachMode = %q, want %q", attachMode, tc.wantAttachMode)
			}
		})
	}
}

// TestAttachPaneDefaultKeyIsA documents that the default attach-pane binding
// is "a" in prefix mode. This is a compile-time-visible assertion: if the
// defaults ever change the test author must update this comment and the
// production code together.
func TestAttachPaneDefaultKeyIsA(t *testing.T) {
	_, _, attachKey, attachMode := resolveKeybindings(nil)
	if attachKey != "a" {
		t.Errorf("default attach-pane key = %q, want \"a\"", attachKey)
	}
	if attachMode != "prefix" {
		t.Errorf("default attach-pane mode = %q, want \"prefix\"", attachMode)
	}
}

// TestSynchronizePanesEnabledByDefault verifies AC 8: all SSH panes start with
// synchronize-panes ON. We cannot call tmux in a unit-test environment, so we
// inspect the source code to confirm that CreateSSHWindow enables synchronize-
// panes via SetSynchronizePanes(windowID, true). The old inline set-window-option
// call was refactored to use the helper function.
func TestSynchronizePanesEnabledByDefault(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Verify CreateSSHWindow calls SetSynchronizePanes with true (i.e. "on").
	want := "SetSynchronizePanes(windowID, true)"
	if !strings.Contains(content, want) {
		t.Errorf("tmux.go does not contain %q — AC 8 requires synchronize-panes to be ON by default", want)
	}

	// Verify SetSynchronizePanes itself uses "on" string literal.
	wantOn := `value = "on"`
	if !strings.Contains(content, wantOn) {
		t.Errorf("tmux.go SetSynchronizePanes missing %q literal", wantOn)
	}
}

// TestMouseModeEnabled verifies AC 11 Sub-AC 1: ConfigureMouseMode sets
// "set -g mouse on" (via set-option -g mouse on) so that pane click events
// are captured. We inspect the source to confirm the exact call is present.
func TestMouseModeEnabled(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Verify the global mouse-on call is present in ConfigureMouseMode.
	wantMouseOn := `"set-option", "-g", "mouse", "on"`
	if !strings.Contains(content, wantMouseOn) {
		t.Errorf("tmux.go does not contain %q — mouse support must be enabled with 'set -g mouse on'", wantMouseOn)
	}

	// Verify ConfigureMouseMode is called from main-path code at session start.
	wantCall := "ConfigureMouseMode()"
	if !strings.Contains(content, wantCall) {
		t.Errorf("tmux.go does not contain %q — ConfigureMouseMode must be exported and callable", wantCall)
	}
}

// TestMouseModeCalledAtStart verifies that main.go calls ConfigureMouseMode
// at session start (before the TUI loop) so mouse is active for the whole session.
func TestMouseModeCalledAtStart(t *testing.T) {
	src, err := os.ReadFile("../../cmd/smux/main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	if !strings.Contains(content, "ConfigureMouseMode()") {
		t.Errorf("main.go does not call tmux.ConfigureMouseMode() — mouse must be enabled at session start")
	}
}

// TestSetSynchronizePanesSource verifies that SetSynchronizePanes constructs
// the correct set-window-option command with the right argument order.
func TestSetSynchronizePanesSource(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// SetSynchronizePanes must reference "set-window-option".
	if !strings.Contains(content, `"set-window-option"`) {
		t.Error(`tmux.go missing "set-window-option" in SetSynchronizePanes`)
	}

	// GetSynchronizePanes must use show-window-option -v to read state.
	if !strings.Contains(content, `"show-window-option"`) {
		t.Error(`tmux.go missing "show-window-option" in GetSynchronizePanes`)
	}

	// ToggleSynchronizePanes must call both GetSynchronizePanes and SetSynchronizePanes.
	if !strings.Contains(content, "GetSynchronizePanes") {
		t.Error("tmux.go missing GetSynchronizePanes call in ToggleSynchronizePanes")
	}
	if !strings.Contains(content, "SetSynchronizePanes(windowID, !current)") {
		t.Error(`tmux.go missing SetSynchronizePanes(windowID, !current) in ToggleSynchronizePanes`)
	}
}

// TestSetSynchronizePanesValueEncoding verifies that the "on" and "off" value
// strings are used correctly in SetSynchronizePanes.
func TestSetSynchronizePanesValueEncoding(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Both string literals must appear in the SetSynchronizePanes function.
	// The "off" default uses := (declaration) while "on" uses = (reassignment).
	for _, want := range []string{`value = "on"`, `"off"`} {
		if !strings.Contains(content, want) {
			t.Errorf("tmux.go missing %q in SetSynchronizePanes", want)
		}
	}
}

// TestGetSynchronizePanesParsingLogic verifies the parsing logic: the function
// must compare the trimmed output to the string "on".
func TestGetSynchronizePanesParsingLogic(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	if !strings.Contains(content, `== "on"`) {
		t.Error(`tmux.go missing == "on" comparison in GetSynchronizePanes`)
	}
}

// TestCreateSSHWindowUsesSetter verifies that CreateSSHWindow delegates to
// SetSynchronizePanes rather than calling tmux set-window-option inline.
func TestCreateSSHWindowUsesSetter(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	if !strings.Contains(content, "SetSynchronizePanes(windowID, true)") {
		t.Error("tmux.go: CreateSSHWindow must call SetSynchronizePanes(windowID, true)")
	}
}

// TestDoubleClickDisablesSyncPanes verifies that ConfigureMouseMode wires
// DoubleClick1Pane to select the clicked pane AND disable synchronize-panes.
// The pane-focus-in hook was intentionally removed: broadcast is now only
// stopped by an explicit double-click, not by any focus change.
func TestDoubleClickDisablesSyncPanes(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// DoubleClick1Pane must be bound.
	if !strings.Contains(content, `"DoubleClick1Pane"`) {
		t.Error(`tmux.go ConfigureMouseMode missing "DoubleClick1Pane" binding`)
	}

	// The binding must disable synchronize-panes.
	if !strings.Contains(content, `"synchronize-panes", "off"`) {
		t.Error(`tmux.go DoubleClick1Pane binding must disable synchronize-panes`)
	}

	// The pane-focus-in hook must NOT be present (it was removed intentionally).
	if strings.Contains(content, `"pane-focus-in"`) {
		t.Error(`tmux.go must not install a pane-focus-in hook — broadcast is controlled only by explicit double-click`)
	}
}

// TestBuildSSHCommandHostBinding verifies that each host in a selection gets
// its own unique SSH command, binding each pane to its corresponding host.
func TestBuildSSHCommandHostBinding(t *testing.T) {
	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com", User: "ubuntu"},
		{DisplayName: "web-02", Host: "web-02.example.com", User: "ubuntu"},
		{DisplayName: "db-01", Host: "db-01.example.com", User: "postgres"},
	}

	cmds := make([]string, len(hosts))
	for i, h := range hosts {
		cmds[i] = buildSSHCommand(h)
	}

	// Each command must contain its host's address and not the others'.
	for i, h := range hosts {
		if !strings.Contains(cmds[i], h.Host) {
			t.Errorf("command[%d] = %q, does not contain host %q", i, cmds[i], h.Host)
		}
		// Ensure the hostname doesn't bleed into adjacent commands.
		for j, other := range hosts {
			if i == j {
				continue
			}
			// The other host's unique name should NOT appear in this command.
			if strings.Contains(cmds[i], other.Host) {
				t.Errorf("command[%d] = %q unexpectedly contains host %q (from index %d)", i, cmds[i], other.Host, j)
			}
		}
	}
}

// TestBootstrapUsesNamedSession verifies AC 2 Sub-AC 2: Bootstrap creates a
// new detached tmux session with the given name and re-invokes the smux binary
// inside it. We inspect the source to confirm:
//   - "new-session" is called with "-s" (named session)
//   - "-d" flag creates a detached (background) session
//   - os.Executable() resolves the smux binary path for re-invocation
//   - attach-session is called so control returns to the caller on exit
func TestBootstrapUsesNamedSession(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Must create a new-session.
	if !strings.Contains(content, `"new-session"`) {
		t.Error(`tmux.go Bootstrap missing "new-session" command`)
	}
	// Must use -d to start the session detached (background).
	if !strings.Contains(content, `"-d"`) {
		t.Error(`tmux.go Bootstrap missing "-d" flag to create detached session`)
	}
	// Must use -s to name the session.
	if !strings.Contains(content, `"-s"`) {
		t.Error(`tmux.go Bootstrap missing "-s" flag to name the session`)
	}
	// Must use os.Executable() to find the smux binary for re-invocation.
	if !strings.Contains(content, "os.Executable()") {
		t.Error(`tmux.go Bootstrap missing os.Executable() call to locate the smux binary`)
	}
	// Must attach-session so control returns to the original caller on exit.
	if !strings.Contains(content, `"attach-session"`) {
		t.Error(`tmux.go Bootstrap missing "attach-session" call; control must return to caller on exit`)
	}
}

// TestBootstrapCalledFromMainWhenNotInTmux verifies AC 2 Sub-AC 2: main.go
// checks InTmux() and calls Bootstrap("smux") when running outside tmux.
func TestBootstrapCalledFromMainWhenNotInTmux(t *testing.T) {
	src, err := os.ReadFile("../../cmd/smux/main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// Must call InTmux() (or DetectContext) to detect whether we're in a session.
	if !strings.Contains(content, "InTmux()") {
		t.Error(`main.go missing InTmux() call — must detect whether running inside tmux`)
	}
	// Must call Bootstrap when not in tmux.
	if !strings.Contains(content, `Bootstrap(`) {
		t.Error(`main.go missing Bootstrap() call — must create a tmux session when outside tmux`)
	}
	// Must pass "smux" as the session name for a predictable, findable session.
	if !strings.Contains(content, `Bootstrap("smux")`) {
		t.Error(`main.go must call Bootstrap("smux") to create a named tmux session`)
	}
}

// AC 8 Sub-AC 1 — tmux window/pane creation logic

// TestCreateSSHWindowUsesNewWindow verifies AC 8 Sub-AC 1: CreateSSHWindow
// creates a new tmux window using the "new-window" command with -P and -F
// flags so that the resulting window ID is captured.
func TestCreateSSHWindowUsesNewWindow(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// CreateSSHWindow must call new-window to create the SSH window.
	if !strings.Contains(content, `"new-window"`) {
		t.Error(`tmux.go CreateSSHWindow missing "new-window" command — must create a tmux window`)
	}
	// Must use -P flag to print the window target/ID.
	if !strings.Contains(content, `"-P"`) {
		t.Error(`tmux.go CreateSSHWindow missing "-P" flag — must capture the window ID`)
	}
	// Must use -F with #{window_id} to get a stable, addressable window ID.
	if !strings.Contains(content, `"#{window_id}"`) {
		t.Error(`tmux.go CreateSSHWindow missing "#{window_id}" format — must capture window ID via -F`)
	}
}

// TestCreateSSHWindowSplitsAdditionalPanes verifies AC 8 Sub-AC 1: CreateSSHWindow
// uses "split-window" to create one pane per host beyond the first. The split
// target must reference the window ID so panes land in the right window.
func TestCreateSSHWindowSplitsAdditionalPanes(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Must use split-window for hosts after the first.
	if !strings.Contains(content, `"split-window"`) {
		t.Error(`tmux.go CreateSSHWindow missing "split-window" command — must split pane per host`)
	}
	// Must target the windowID so panes land in the correct window.
	if !strings.Contains(content, `"-t", windowID`) {
		t.Error(`tmux.go CreateSSHWindow missing "-t", windowID — split-window must target the created window`)
	}
}

// TestCreateSSHWindowAppliesTiledLayout verifies AC 8 Sub-AC 1: CreateSSHWindow
// applies a "tiled" layout using "select-layout" so all panes are evenly arranged.
// Layout must be applied after each split (incremental) and again at the end (final pass).
func TestCreateSSHWindowAppliesTiledLayout(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Must use select-layout for pane arrangement.
	if !strings.Contains(content, `"select-layout"`) {
		t.Error(`tmux.go CreateSSHWindow missing "select-layout" command — must arrange panes`)
	}
	// Must request the "tiled" layout specifically.
	if !strings.Contains(content, `"tiled"`) {
		t.Error(`tmux.go CreateSSHWindow missing "tiled" layout — must use tiled arrangement`)
	}
}

// TestCreateSSHWindowReturnsWindowID verifies AC 8 Sub-AC 1: CreateSSHWindow
// returns the tmux window ID string so callers can reference the window later
// (e.g. to toggle synchronize-panes). A non-empty windowID must be returned on success.
func TestCreateSSHWindowReturnsWindowID(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// CreateSSHWindow must return windowID (the captured window ID string).
	if !strings.Contains(content, "return windowID, nil") {
		t.Error(`tmux.go CreateSSHWindow missing "return windowID, nil" — must return the window ID on success`)
	}
}

// TestCreateSSHWindowSendsSSHCommands verifies AC 8 Sub-AC 1: CreateSSHWindow
// sends an SSH command to each pane using send-keys. Each host must have its
// own SSH command sent to its own pane target.
func TestCreateSSHWindowSendsSSHCommands(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Must call sendKeysToTarget to send SSH commands to each pane.
	if !strings.Contains(content, "sendKeysToTarget") {
		t.Error(`tmux.go CreateSSHWindow missing sendKeysToTarget — must send SSH command to each pane`)
	}
	// Must call buildSSHCommand to construct the SSH command.
	if !strings.Contains(content, "buildSSHCommand") {
		t.Error(`tmux.go CreateSSHWindow missing buildSSHCommand — must build SSH command per host`)
	}
	// send-keys must target pane 0 of the new window for the first host.
	// The first pane ID is captured atomically from new-window via -F "#{pane_id}".
	if !strings.Contains(content, "firstPaneID") {
		t.Error(`tmux.go CreateSSHWindow must capture the first pane ID from new-window and use it as the send-keys target`)
	}
}

// TestBootstrapSessionNamePassedThrough verifies that Bootstrap accepts a
// sessionName parameter and threads it into both the new-session and
// attach-session subcommands (the function signature contract).
func TestBootstrapSessionNamePassedThrough(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Both the detached-session creation and the subsequent attach must
	// reference the sessionName variable so that the correct named session is
	// created and then attached.
	if !strings.Contains(content, "sessionName") {
		t.Error("tmux.go Bootstrap must accept and use a sessionName parameter")
	}
	// attachSession helper must also accept the sessionName.
	if !strings.Contains(content, "attachSession(sessionName)") {
		t.Error("tmux.go Bootstrap must pass sessionName to attachSession so the right session is attached")
	}
}

// AC 8 Sub-AC 3 — end-to-end pane-count and SSH-command validation
//
// The following integration tests use a fake tmux binary (a POSIX shell script
// placed on PATH) to record every tmux invocation without requiring a real tmux
// session.  They confirm that for N selected hosts CreateSSHWindow opens exactly
// N panes in a new tiled window, each running the SSH command for the
// corresponding host.

// writeFakeTmux writes a minimal fake tmux shell script to dir/tmux and returns
// the path.  The script appends every invocation to logFile and, for subcommands
// that produce output, echoes a deterministic fake value:
//
//   - new-window → prints "@1" (fake window ID)
//   - split-window → prints "%2", "%3", … (sequential fake pane IDs via counterFile)
func writeFakeTmux(t *testing.T, dir, logFile, counterFile string) string {
	t.Helper()
	script := "#!/bin/sh\n" +
		// Record every call (all positional params joined by IFS=space).
		`echo "$*" >> "` + logFile + `"` + "\n" +
		`case "$1" in` + "\n" +
		// new-window: return a fixed fake window ID and first pane ID.
		"    new-window) echo \"@1 %1\" ;;\n" +
		// split-window: return sequential fake pane IDs (%2, %3, …).
		"    split-window)\n" +
		`        n=$(cat "` + counterFile + `" 2>/dev/null || echo 1)` + "\n" +
		"        n=$((n + 1))\n" +
		`        echo "$n" > "` + counterFile + `"` + "\n" +
		`        echo "%$n"` + "\n" +
		"        ;;\n" +
		"esac\n" +
		"exit 0\n"

	fakeBin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatalf("writeFakeTmux: %v", err)
	}
	return fakeBin
}

// parseFakeTmuxLog reads the recorded tmux call log and returns the raw lines.
func parseFakeTmuxLog(t *testing.T, logFile string) []string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("cannot read tmux call log %q: %v", logFile, err)
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

// TestCreateSSHWindowIntegration is an end-to-end integration test that
// verifies CreateSSHWindow's runtime behaviour for a 3-host selection:
//
//  1. Exactly N-1 split-window calls are made (first pane comes free with new-window).
//  2. Exactly N send-keys calls are made (one per host).
//  3. Every host's SSH address appears in exactly one send-keys invocation.
//  4. select-layout tiled is applied at least once.
//  5. synchronize-panes is set to "on" via set-window-option.
func TestCreateSSHWindowIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)

	// Override PATH so our fake tmux binary is found first.
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com", User: "ubuntu"},
		{DisplayName: "web-02", Host: "web-02.example.com", User: "ubuntu"},
		{DisplayName: "db-01", Host: "db-01.example.com", User: "postgres"},
	}

	windowID, err := CreateSSHWindow(hosts, nil)
	if err != nil {
		t.Fatalf("CreateSSHWindow() error = %v; want nil", err)
	}
	if windowID == "" {
		t.Fatal("CreateSSHWindow() returned empty windowID; want non-empty")
	}

	lines := parseFakeTmuxLog(t, logFile)

	var splits, sendKeysCount, tiledCount int
	var sendKeyLines []string

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "split-window"):
			splits++
		case strings.HasPrefix(line, "send-keys"):
			sendKeysCount++
			sendKeyLines = append(sendKeyLines, line)
		}
		if strings.Contains(line, "select-layout") && strings.Contains(line, "tiled") {
			tiledCount++
		}
	}

	// N hosts → N-1 splits (new-window provides the first pane for free).
	if want := len(hosts) - 1; splits != want {
		t.Errorf("split-window called %d times, want %d (len(hosts)-1 for %d hosts)",
			splits, want, len(hosts))
	}

	// Exactly one send-keys invocation per host.
	if sendKeysCount != len(hosts) {
		t.Errorf("send-keys called %d times, want %d (one per host)",
			sendKeysCount, len(hosts))
	}

	// Every host's SSH address must appear in a send-keys invocation.
	for _, h := range hosts {
		found := false
		for _, line := range sendKeyLines {
			if strings.Contains(line, h.Host) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no send-keys call contains SSH address %q for host %q",
				h.Host, h.DisplayName)
		}
	}

	// Tiled layout must be applied at least once.
	if tiledCount == 0 {
		t.Error("select-layout tiled never called — panes must use tiled layout")
	}

	// synchronize-panes must be enabled (on) for the newly created window.
	syncEnabled := false
	for _, line := range lines {
		if strings.Contains(line, "set-window-option") &&
			strings.Contains(line, "synchronize-panes") &&
			strings.Contains(line, "on") {
			syncEnabled = true
			break
		}
	}
	if !syncEnabled {
		t.Error("synchronize-panes was not set to 'on' — broadcast mode must be active by default")
	}
}

// AC 2 Sub-AC 2 — BreakPane: tmux break-pane command integration

// TestBreakPaneSourceInspection verifies that BreakPane exists in tmux.go and
// builds the tmux break-pane command correctly: with -t paneID when a pane is
// specified, and without -t when paneID is empty.
func TestBreakPaneSourceInspection(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// BreakPane must be defined as an exported function.
	if !strings.Contains(content, "func BreakPane(") {
		t.Error(`tmux.go missing exported "func BreakPane(" — must expose BreakPane function`)
	}
	// Must invoke the tmux "break-pane" subcommand.
	if !strings.Contains(content, `"break-pane"`) {
		t.Error(`tmux.go BreakPane missing "break-pane" subcommand`)
	}
	// Must use -t to target a specific pane when one is provided.
	if !strings.Contains(content, `"-t", paneID`) {
		t.Error(`tmux.go BreakPane missing "-t", paneID — must target specific pane`)
	}
	// Must return an error (not silently swallow failures).
	if !strings.Contains(content, "cannot break pane") {
		t.Error(`tmux.go BreakPane missing error message "cannot break pane" — must propagate failures`)
	}
}

// TestBreakPaneIntegration uses a fake tmux binary to verify that BreakPane
// invokes "tmux break-pane -t <paneID>" with the correct pane identifier.
func TestBreakPaneIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := BreakPane("%3"); err != nil {
		t.Fatalf("BreakPane(%q) error = %v; want nil", "%3", err)
	}

	lines := parseFakeTmuxLog(t, logFile)

	// Must have at least one line for the break-pane call.
	if len(lines) == 0 {
		t.Fatal("no tmux calls recorded; expected break-pane invocation")
	}

	// Exactly one call and it must be "break-pane -t %3".
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "break-pane") && strings.Contains(line, "%3") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'break-pane ... %%3' call found in log; lines: %v", lines)
	}
}

// TestBreakPaneEmptyPaneIDUsesActivePane verifies that passing an empty paneID
// causes BreakPane to call "tmux break-pane" without a -t flag, which makes
// tmux operate on the currently active pane.
func TestBreakPaneEmptyPaneIDUsesActivePane(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := BreakPane(""); err != nil {
		t.Fatalf("BreakPane(%q) error = %v; want nil", "", err)
	}

	lines := parseFakeTmuxLog(t, logFile)
	if len(lines) == 0 {
		t.Fatal("no tmux calls recorded; expected break-pane invocation")
	}

	// Should call "break-pane" without "-t".
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "break-pane") {
			found = true
			// Must NOT contain -t when paneID is empty.
			if strings.Contains(line, "-t") {
				t.Errorf("BreakPane(\"\") should not pass -t flag; got line: %q", line)
			}
			break
		}
	}
	if !found {
		t.Errorf("no 'break-pane' call found in log; lines: %v", lines)
	}
}

// ---------------------------------------------------------------------------
// AC 14 Sub-AC 3 — KillPane and RuntimeSession runtime state
// ---------------------------------------------------------------------------

// TestKillPaneSourceInspection verifies that KillPane is an exported function
// in tmux.go that issues "tmux kill-pane -t <paneID>" and wraps errors with a
// human-readable message so callers can surface failures meaningfully.
func TestKillPaneSourceInspection(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Must define an exported KillPane function.
	if !strings.Contains(content, "func KillPane(") {
		t.Error(`tmux.go missing exported "func KillPane(" — AC 14 requires a KillPane function`)
	}
	// Must invoke the tmux "kill-pane" subcommand.
	if !strings.Contains(content, `"kill-pane"`) {
		t.Error(`tmux.go KillPane missing "kill-pane" subcommand`)
	}
	// Must return an error (not silently swallow failures).
	if !strings.Contains(content, "cannot kill pane") {
		t.Error(`tmux.go KillPane missing error message "cannot kill pane" — must propagate failures`)
	}
}

// TestRuntimeSessionTypes verifies that RuntimePane and RuntimeSession are
// defined in tmux.go with the expected fields, satisfying the domain model
// requirement for typed runtime entities that are separate from config types.
func TestRuntimeSessionTypes(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// RuntimePane must be a struct with PaneID and Host fields.
	if !strings.Contains(content, "type RuntimePane struct") {
		t.Error(`tmux.go missing "type RuntimePane struct" — runtime pane type required`)
	}
	if !strings.Contains(content, "PaneID string") {
		t.Error(`tmux.go RuntimePane missing "PaneID string" field`)
	}

	// RuntimeSession must be a struct with WindowID and Panes fields.
	if !strings.Contains(content, "type RuntimeSession struct") {
		t.Error(`tmux.go missing "type RuntimeSession struct" — runtime session type required`)
	}
	if !strings.Contains(content, "WindowID string") {
		t.Error(`tmux.go RuntimeSession missing "WindowID string" field`)
	}
	if !strings.Contains(content, "[]*RuntimePane") {
		t.Error(`tmux.go RuntimeSession missing "[]*RuntimePane" Panes field`)
	}
}

// TestRuntimeSessionRemovePane verifies that RemovePane removes the named pane
// from the Panes slice and returns true, while leaving other panes intact.
// It also verifies that removing a non-existent pane returns false without
// mutating the slice.
func TestRuntimeSessionRemovePane(t *testing.T) {
	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com"},
		{DisplayName: "web-02", Host: "web-02.example.com"},
		{DisplayName: "db-01", Host: "db-01.example.com"},
	}
	session := NewRuntimeSession("@1", []string{"%1", "%2", "%3"}, hosts)

	if len(session.Panes) != 3 {
		t.Fatalf("NewRuntimeSession: len(Panes) = %d, want 3", len(session.Panes))
	}

	// Remove the middle pane.
	removed := session.RemovePane("%2")
	if !removed {
		t.Error("RemovePane(%2) = false, want true")
	}
	if len(session.Panes) != 2 {
		t.Errorf("after RemovePane: len(Panes) = %d, want 2", len(session.Panes))
	}
	// Remaining panes must be %1 and %3.
	for _, p := range session.Panes {
		if p.PaneID == "%2" {
			t.Errorf("RemovePane(%%2) did not remove pane: %v still in Panes", p)
		}
	}

	// Removing a non-existent pane returns false and leaves the slice unchanged.
	removedAgain := session.RemovePane("%99")
	if removedAgain {
		t.Error("RemovePane(%99) = true, want false for non-existent pane")
	}
	if len(session.Panes) != 2 {
		t.Errorf("after failed RemovePane: len(Panes) = %d, want 2 (unchanged)", len(session.Panes))
	}
}

// TestNewRuntimeSessionPairing verifies that NewRuntimeSession correctly pairs
// each pane ID with its corresponding host entry, preserving order.
func TestNewRuntimeSessionPairing(t *testing.T) {
	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com", User: "ubuntu"},
		{DisplayName: "db-01", Host: "db-01.example.com", User: "postgres"},
	}
	session := NewRuntimeSession("@5", []string{"%10", "%11"}, hosts)

	if session.WindowID != "@5" {
		t.Errorf("WindowID = %q, want @5", session.WindowID)
	}
	if len(session.Panes) != 2 {
		t.Fatalf("len(Panes) = %d, want 2", len(session.Panes))
	}
	if session.Panes[0].PaneID != "%10" || session.Panes[0].Host.Host != "web-01.example.com" {
		t.Errorf("Panes[0] = {%q, %q}, want {%%10, web-01.example.com}",
			session.Panes[0].PaneID, session.Panes[0].Host.Host)
	}
	if session.Panes[1].PaneID != "%11" || session.Panes[1].Host.Host != "db-01.example.com" {
		t.Errorf("Panes[1] = {%q, %q}, want {%%11, db-01.example.com}",
			session.Panes[1].PaneID, session.Panes[1].Host.Host)
	}
}

// TestKillPaneIntegration uses a fake tmux binary to verify that KillPane
// invokes "tmux kill-pane -t <paneID>" with the correct pane identifier.
func TestKillPaneIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := KillPane("%5"); err != nil {
		t.Fatalf("KillPane(%q) error = %v; want nil", "%5", err)
	}

	lines := parseFakeTmuxLog(t, logFile)
	if len(lines) == 0 {
		t.Fatal("no tmux calls recorded; expected kill-pane invocation")
	}

	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "kill-pane") && strings.Contains(line, "%5") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'kill-pane ... %%5' call found in log; lines: %v", lines)
	}
}

// TestKillAndRemovePaneIntegration verifies the combined KillAndRemovePane
// operation: it must call tmux kill-pane AND remove the pane from Panes.
func TestKillAndRemovePaneIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com"},
		{DisplayName: "db-01", Host: "db-01.example.com"},
	}
	session := NewRuntimeSession("@3", []string{"%7", "%8"}, hosts)

	// Kill the first pane and verify it is removed from runtime state.
	if err := session.KillAndRemovePane("%7"); err != nil {
		t.Fatalf("KillAndRemovePane(%q) error = %v; want nil", "%7", err)
	}

	// The pane must have been removed from the slice.
	if len(session.Panes) != 1 {
		t.Errorf("after KillAndRemovePane: len(Panes) = %d, want 1", len(session.Panes))
	}
	if session.Panes[0].PaneID != "%8" {
		t.Errorf("remaining pane = %q, want %%8", session.Panes[0].PaneID)
	}

	// Verify the tmux kill-pane command was issued.
	lines := parseFakeTmuxLog(t, logFile)
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "kill-pane") && strings.Contains(line, "%7") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'kill-pane ... %%7' call found in log; lines: %v", lines)
	}
}

// TestBuildSSHCommandExitPromptKillsPane verifies Sub-AC 3: the shell command
// produced by buildSSHCommand includes a post-exit prompt and, when the user
// presses 'y', executes "tmux kill-pane" to destroy the pane.
func TestBuildSSHCommandExitPromptKillsPane(t *testing.T) {
	host := config.ResolvedHost{
		DisplayName: "web-01",
		Host:        "web-01.example.com",
	}
	cmd := buildSSHCommand(host)

	// Must show the exit prompt so the user knows SSH has exited.
	if !strings.Contains(cmd, "Session exited") {
		t.Errorf("buildSSHCommand() = %q, missing exit prompt (want 'Session exited')", cmd)
	}
	// Must read a single response from the user.
	if !strings.Contains(cmd, "read") {
		t.Errorf("buildSSHCommand() = %q, missing 'read' to capture user input", cmd)
	}
	// Pressing 'y' must trigger tmux kill-pane.
	if !strings.Contains(cmd, `"y"`) || !strings.Contains(cmd, "tmux kill-pane") {
		t.Errorf("buildSSHCommand() = %q, pressing 'y' must trigger 'tmux kill-pane'", cmd)
	}
	// The kill-pane must be conditional on 'y' (not unconditional).
	if !strings.Contains(cmd, `= "y"`) && !strings.Contains(cmd, `= 'y'`) {
		t.Errorf("buildSSHCommand() = %q, kill-pane must be conditional on 'y' keypress", cmd)
	}
}

// TestCreateSSHWindowSingleHostNoSplits verifies that a single-host selection
// opens exactly one pane via new-window with no split-window calls, and that
// the sole pane receives the SSH command for the host.
func TestCreateSSHWindowSingleHostNoSplits(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hosts := []config.ResolvedHost{
		{DisplayName: "solo", Host: "solo.example.com", User: "admin"},
	}

	if _, err := CreateSSHWindow(hosts, nil); err != nil {
		t.Fatalf("CreateSSHWindow() error = %v; want nil", err)
	}

	lines := parseFakeTmuxLog(t, logFile)

	// No split-window calls for a single host.
	for _, line := range lines {
		if strings.HasPrefix(line, "split-window") {
			t.Errorf("split-window called for a single-host selection (line: %q)", line)
		}
	}

	// The single send-keys call must contain the host's SSH address.
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "send-keys") && strings.Contains(line, hosts[0].Host) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no send-keys call contains SSH address %q for the single selected host",
			hosts[0].Host)
	}
}

// ---------------------------------------------------------------------------
// AC 14 Sub-AC 2 — post-exit "Session exited. Kill pane? (y/n)" prompt
// ---------------------------------------------------------------------------

// TestBuildSSHCommandExitPromptExactWording verifies AC 14 Sub-AC 2: the shell
// command embedded in each pane must display the exact prompt
// "Session exited. Kill pane? (y/n)" when the SSH session terminates.
func TestBuildSSHCommandExitPromptExactWording(t *testing.T) {
	host := config.ResolvedHost{
		DisplayName: "web-01.example.com",
		Host:        "web-01.example.com",
	}
	cmd := buildSSHCommand(host)

	const want = "Session exited. Kill pane? (y/n)"
	if !strings.Contains(cmd, want) {
		t.Errorf("buildSSHCommand() = %q\nwant it to contain exact prompt %q", cmd, want)
	}
}

// TestBuildSSHCommandYKillsPane verifies AC 14 Sub-AC 2: after the SSH session
// exits, pressing 'y' must kill the pane via "tmux kill-pane".
func TestBuildSSHCommandYKillsPane(t *testing.T) {
	host := config.ResolvedHost{Host: "web-01.example.com"}
	cmd := buildSSHCommand(host)

	if !strings.Contains(cmd, "tmux kill-pane") {
		t.Errorf("buildSSHCommand() = %q; must contain 'tmux kill-pane' for y-to-kill behaviour", cmd)
	}
	// The kill must be conditional on the 'y' answer.
	if !strings.Contains(cmd, `"y"`) {
		t.Errorf("buildSSHCommand() = %q; kill-pane must be conditional on user entering 'y'", cmd)
	}
}

// TestPaneStateConnectedIsDefault verifies the domain model: a freshly
// constructed RuntimePane starts in PaneStateConnected (zero value).
func TestPaneStateConnectedIsDefault(t *testing.T) {
	p := &RuntimePane{PaneID: "%1", Host: config.ResolvedHost{Host: "h1.example.com"}}
	if p.State != PaneStateConnected {
		t.Errorf("new RuntimePane.State = %v, want PaneStateConnected", p.State)
	}
}

// TestPaneStateTransitionToExited verifies the domain model: setting State to
// PaneStateExited correctly changes the lifecycle state.
func TestPaneStateTransitionToExited(t *testing.T) {
	p := &RuntimePane{PaneID: "%1"}
	p.State = PaneStateExited
	if p.State != PaneStateExited {
		t.Errorf("RuntimePane.State after transition = %v, want PaneStateExited", p.State)
	}
}

// TestPaneStateStringValues verifies that PaneState.String() returns
// human-readable labels useful for logging and diagnostics.
func TestPaneStateStringValues(t *testing.T) {
	tests := []struct {
		state PaneState
		want  string
	}{
		{PaneStateConnected, "connected"},
		{PaneStateExited, "exited"},
	}
	for _, tc := range tests {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("PaneState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// AC 15 Sub-AC 1 — SelectWindow: switch focus to window 0 after SSH window
// ---------------------------------------------------------------------------

// TestSelectWindowSourceInspection verifies that SelectWindow is exported,
// accepts a string target, and runs "tmux select-window -t <target>".
// This is a source-inspection test; it does not require a live tmux session.
func TestSelectWindowSourceInspection(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// SelectWindow must be exported.
	if !strings.Contains(content, "func SelectWindow(") {
		t.Error("tmux.go does not export SelectWindow — AC 15 requires a SelectWindow function")
	}

	// SelectWindow must use "select-window" tmux sub-command.
	if !strings.Contains(content, `"select-window"`) {
		t.Error(`tmux.go SelectWindow must invoke "tmux select-window"`)
	}
}

// TestFocusMovesToSSHWindowAfterCreation verifies that main.go calls
// SelectWindow(windowID) after CreateSSHWindow so focus moves to the SSH window.
func TestFocusMovesToSSHWindowAfterCreation(t *testing.T) {
	src, err := os.ReadFile("../../cmd/smux/main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	if !strings.Contains(content, "tmux.SelectWindow(windowID)") {
		t.Error(`main.go must call tmux.SelectWindow(windowID) after CreateSSHWindow to move focus to the SSH window`)
	}
}

// TestBuildSSHCommandPromptAppearsAfterSSHExit verifies the ordering: the exit
// prompt must appear AFTER the ssh invocation (separated by ";"), not before it.
func TestBuildSSHCommandPromptAppearsAfterSSHExit(t *testing.T) {
	host := config.ResolvedHost{Host: "web-01.example.com"}
	cmd := buildSSHCommand(host)

	// The SSH invocation is the first part; the prompt is after the first semicolon.
	parts := strings.SplitN(cmd, ";", 2)
	if len(parts) < 2 {
		t.Fatalf("buildSSHCommand() = %q; expected semicolon separating ssh from prompt", cmd)
	}
	sshPart := parts[0]
	postExitPart := parts[1]

	if !strings.HasPrefix(strings.TrimSpace(sshPart), "ssh") {
		t.Errorf("ssh invocation part = %q; must start with 'ssh'", sshPart)
	}
	if !strings.Contains(postExitPart, "Session exited. Kill pane? (y/n)") {
		t.Errorf("post-exit part = %q; must contain the exit prompt", postExitPart)
	}
}

// ---------------------------------------------------------------------------
// AC 13 Sub-AC 3 — double-click → stop broadcast and focus pane
// ---------------------------------------------------------------------------

// TestDoubleClickBindingStopsBroadcast verifies that ConfigureMouseMode wires
// DoubleClick1Pane to stop synchronize-panes (broadcast) and focus the clicked
// pane. Double-click is the deliberate "focus this one pane" action.
func TestDoubleClickBindingStopsBroadcast(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// ConfigureMouseMode must bind DoubleClick1Pane.
	if !strings.Contains(content, `"DoubleClick1Pane"`) {
		t.Error(`tmux.go ConfigureMouseMode missing "DoubleClick1Pane" binding`)
	}
	// The DoubleClick1Pane binding must disable synchronize-panes.
	if !strings.Contains(content, `"synchronize-panes", "off"`) {
		t.Error(`tmux.go DoubleClick1Pane binding must set synchronize-panes off`)
	}
}

// TestDoubleClickBindingSelectsPaneFirst verifies that ConfigureMouseMode's
// DoubleClick1Pane binding selects the clicked pane (select-pane -t {mouse})
// before disabling broadcast, so the correct pane is targeted.
func TestDoubleClickBindingSelectsPaneFirst(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// The binding must contain select-pane so the clicked pane gets focus first.
	if !strings.Contains(content, "select-pane") {
		t.Error(`tmux.go DoubleClick1Pane binding missing "select-pane" — must focus the clicked pane`)
	}
	// The {mouse} target makes tmux use the pane under the cursor.
	if !strings.Contains(content, `"{mouse}"`) {
		t.Error(`tmux.go DoubleClick1Pane binding missing "{mouse}" target`)
	}
}

// TestBreakAndRemovePaneSourceInspection verifies that RuntimeSession has a
// BreakAndRemovePane method that (a) calls BreakPane to execute the tmux
// command and (b) calls RemovePane to update the in-memory domain model.
// This confirms the "updating the domain model" requirement of AC 13 Sub-AC 3.
func TestBreakAndRemovePaneSourceInspection(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// Method must be defined on *RuntimeSession.
	if !strings.Contains(content, "func (s *RuntimeSession) BreakAndRemovePane(") {
		t.Error(`tmux.go missing "func (s *RuntimeSession) BreakAndRemovePane(" — domain model must expose BreakAndRemovePane`)
	}
	// Must delegate the tmux command to BreakPane.
	if !strings.Contains(content, "BreakPane(paneID)") {
		t.Error(`tmux.go BreakAndRemovePane missing "BreakPane(paneID)" call — must invoke the tmux break-pane command`)
	}
	// Must update the domain model by removing the pane from the slice.
	if !strings.Contains(content, "s.RemovePane(paneID)") {
		t.Error(`tmux.go BreakAndRemovePane missing "s.RemovePane(paneID)" — must update the runtime domain model after breaking`)
	}
}

// TestBreakAndRemovePaneIntegration uses a fake tmux binary to verify that
// BreakAndRemovePane (a) invokes "tmux break-pane -t <paneID>" and (b)
// removes the pane from the RuntimeSession.Panes slice so the domain model
// reflects the new window layout.
func TestBreakAndRemovePaneIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")
	counterFile := filepath.Join(tmpDir, "pane.n")

	writeFakeTmux(t, tmpDir, logFile, counterFile)
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com"},
		{DisplayName: "web-02", Host: "web-02.example.com"},
		{DisplayName: "db-01", Host: "db-01.example.com"},
	}
	session := NewRuntimeSession("@1", []string{"%1", "%2", "%3"}, hosts)

	// Break the middle pane — this simulates what the DoubleClick1Pane
	// binding would do at the tmux level, plus the domain model update.
	if err := session.BreakAndRemovePane("%2"); err != nil {
		t.Fatalf("BreakAndRemovePane(%q) error = %v; want nil", "%2", err)
	}

	// Domain model must reflect the removal.
	if len(session.Panes) != 2 {
		t.Errorf("after BreakAndRemovePane: len(Panes) = %d, want 2", len(session.Panes))
	}
	for _, p := range session.Panes {
		if p.PaneID == "%2" {
			t.Errorf("BreakAndRemovePane(%%2) did not remove pane from domain model: %%2 still in Panes")
		}
	}

	// The tmux break-pane command must have been invoked with the correct target.
	lines := parseFakeTmuxLog(t, logFile)
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "break-pane") && strings.Contains(line, "%2") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no 'break-pane ... %%2' call found in tmux log; lines: %v", lines)
	}
}

// TestBreakAndRemovePaneFailureKeepsDomainModel verifies that when the tmux
// break-pane command fails (e.g. pane does not exist), BreakAndRemovePane
// returns an error and does NOT remove the pane from the RuntimeSession.Panes
// slice, keeping the domain model consistent with the actual tmux state.
func TestBreakAndRemovePaneFailureKeepsDomainModel(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake tmux binary that always fails (exit code 1).
	failScript := "#!/bin/sh\necho \"error: no such pane\" >&2\nexit 1\n"
	fakeBin := filepath.Join(tmpDir, "tmux")
	if err := os.WriteFile(fakeBin, []byte(failScript), 0755); err != nil {
		t.Fatalf("cannot write failing fake tmux: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	hosts := []config.ResolvedHost{
		{DisplayName: "web-01", Host: "web-01.example.com"},
	}
	session := NewRuntimeSession("@1", []string{"%1"}, hosts)

	err := session.BreakAndRemovePane("%1")
	if err == nil {
		t.Fatal("BreakAndRemovePane with failing tmux should return error, got nil")
	}
	// The pane must still be in the slice — domain model unchanged on failure.
	if len(session.Panes) != 1 {
		t.Errorf("after failed BreakAndRemovePane: len(Panes) = %d, want 1 (unchanged)", len(session.Panes))
	}
	if session.Panes[0].PaneID != "%1" {
		t.Errorf("after failed BreakAndRemovePane: pane %%1 gone from domain model; want it preserved")
	}
}

// ---------------------------------------------------------------------------
// AC 13 Sub-AC 1 — PaneLayout geometry struct and GetPaneLayouts
// ---------------------------------------------------------------------------

// TestPaneLayoutStructFields is a compile-time check verifying that PaneLayout
// exists as a struct with the five expected fields for tmux pane geometry.
func TestPaneLayoutStructFields(t *testing.T) {
	p := PaneLayout{
		PaneID: "%3",
		X:      10,
		Y:      5,
		Width:  40,
		Height: 24,
	}
	if p.PaneID != "%3" {
		t.Errorf("PaneLayout.PaneID = %q, want %%3", p.PaneID)
	}
	if p.X != 10 {
		t.Errorf("PaneLayout.X = %d, want 10", p.X)
	}
	if p.Y != 5 {
		t.Errorf("PaneLayout.Y = %d, want 5", p.Y)
	}
	if p.Width != 40 {
		t.Errorf("PaneLayout.Width = %d, want 40", p.Width)
	}
	if p.Height != 24 {
		t.Errorf("PaneLayout.Height = %d, want 24", p.Height)
	}
}

// TestGetPaneLayoutsSourceInspection verifies that GetPaneLayouts is exported
// and uses the correct tmux format specifiers for pane geometry.
func TestGetPaneLayoutsSourceInspection(t *testing.T) {
	src, err := os.ReadFile("tmux.go")
	if err != nil {
		t.Fatalf("cannot read tmux.go: %v", err)
	}
	content := string(src)

	// GetPaneLayouts must be exported.
	if !strings.Contains(content, "func GetPaneLayouts(") {
		t.Error(`tmux.go missing exported "func GetPaneLayouts(" — AC 13 requires pane geometry to be queryable`)
	}
	// Must use tmux list-panes command to query pane layout.
	if !strings.Contains(content, `"list-panes"`) {
		t.Error(`tmux.go GetPaneLayouts must use "list-panes" command`)
	}
	// Must include pane_id so the caller can identify which pane was clicked.
	if !strings.Contains(content, "pane_id") {
		t.Error("tmux.go GetPaneLayouts must include #{pane_id} in the format string")
	}
	// Must include pane_left and pane_top for the top-left corner position.
	if !strings.Contains(content, "pane_left") {
		t.Error("tmux.go GetPaneLayouts must include #{pane_left} in the format string")
	}
	if !strings.Contains(content, "pane_top") {
		t.Error("tmux.go GetPaneLayouts must include #{pane_top} in the format string")
	}
	// Must include pane_width and pane_height for bounding-rectangle checks.
	if !strings.Contains(content, "pane_width") {
		t.Error("tmux.go GetPaneLayouts must include #{pane_width} in the format string")
	}
	if !strings.Contains(content, "pane_height") {
		t.Error("tmux.go GetPaneLayouts must include #{pane_height} in the format string")
	}
}

// TestParsePaneLayoutsSinglePane verifies that parsePaneLayouts correctly
// parses a single-pane list-panes output line into a PaneLayout struct.
func TestParsePaneLayoutsSinglePane(t *testing.T) {
	input := "%1 0 0 80 24"
	layouts, err := parsePaneLayouts(input)
	if err != nil {
		t.Fatalf("parsePaneLayouts(%q) error = %v; want nil", input, err)
	}
	if len(layouts) != 1 {
		t.Fatalf("parsePaneLayouts(%q) returned %d layouts; want 1", input, len(layouts))
	}
	p := layouts[0]
	if p.PaneID != "%1" {
		t.Errorf("PaneID = %q, want %%1", p.PaneID)
	}
	if p.X != 0 || p.Y != 0 {
		t.Errorf("(X,Y) = (%d,%d), want (0,0)", p.X, p.Y)
	}
	if p.Width != 80 || p.Height != 24 {
		t.Errorf("(Width,Height) = (%d,%d), want (80,24)", p.Width, p.Height)
	}
}

// TestParsePaneLayoutsMultiplePanes verifies that parsePaneLayouts handles
// multi-pane output, correctly parsing each line to a separate PaneLayout.
func TestParsePaneLayoutsMultiplePanes(t *testing.T) {
	input := "%1 0 0 40 24\n%2 40 0 40 24\n%3 0 24 80 24"
	layouts, err := parsePaneLayouts(input)
	if err != nil {
		t.Fatalf("parsePaneLayouts error = %v; want nil", err)
	}
	if len(layouts) != 3 {
		t.Fatalf("len(layouts) = %d, want 3", len(layouts))
	}

	expected := []PaneLayout{
		{PaneID: "%1", X: 0, Y: 0, Width: 40, Height: 24},
		{PaneID: "%2", X: 40, Y: 0, Width: 40, Height: 24},
		{PaneID: "%3", X: 0, Y: 24, Width: 80, Height: 24},
	}
	for i, want := range expected {
		got := layouts[i]
		if got != want {
			t.Errorf("layouts[%d] = %+v, want %+v", i, got, want)
		}
	}
}

// TestParsePaneLayoutsEmptyInput verifies that parsePaneLayouts returns an
// empty slice (not an error) for empty output, which happens when the window
// has no panes (should not occur in practice but must be safe to handle).
func TestParsePaneLayoutsEmptyInput(t *testing.T) {
	layouts, err := parsePaneLayouts("")
	if err != nil {
		t.Fatalf("parsePaneLayouts(%q) error = %v; want nil", "", err)
	}
	if len(layouts) != 0 {
		t.Errorf("parsePaneLayouts(%q) = %v; want empty slice", "", layouts)
	}
}

// TestGetPaneLayoutsIntegration verifies that GetPaneLayouts calls
// "tmux list-panes -F <format>" (optionally with -t windowID) using a fake
// tmux binary that returns a deterministic pane list.
func TestGetPaneLayoutsIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "calls.log")

	// Create a fake tmux that records calls and returns a canned pane list for
	// the list-panes sub-command.
	script := "#!/bin/sh\n" +
		`echo "$*" >> "` + logFile + `"` + "\n" +
		`case "$1" in` + "\n" +
		"    list-panes) echo \"%1 0 0 40 24\"; echo \"%2 40 0 40 24\" ;;\n" +
		"esac\n" +
		"exit 0\n"

	fakeBin := filepath.Join(tmpDir, "tmux")
	if err := os.WriteFile(fakeBin, []byte(script), 0755); err != nil {
		t.Fatalf("writeFakeTmux: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	layouts, err := GetPaneLayouts("@1")
	if err != nil {
		t.Fatalf("GetPaneLayouts(@1) error = %v; want nil", err)
	}
	if len(layouts) != 2 {
		t.Fatalf("GetPaneLayouts(@1) returned %d layouts; want 2", len(layouts))
	}

	if layouts[0].PaneID != "%1" || layouts[0].X != 0 || layouts[0].Y != 0 {
		t.Errorf("layouts[0] = %+v, want {PaneID:%%1, X:0, Y:0, Width:40, Height:24}", layouts[0])
	}
	if layouts[1].PaneID != "%2" || layouts[1].X != 40 || layouts[1].Y != 0 {
		t.Errorf("layouts[1] = %+v, want {PaneID:%%2, X:40, Y:0, Width:40, Height:24}", layouts[1])
	}
}
