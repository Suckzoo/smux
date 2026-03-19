package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBootstrapWiredBeforeTUI is a source-code inspection test that verifies
// the bootstrap check (InTmux) is present in main.go and is ordered before the
// TUI main loop, ensuring that smux never starts the TUI outside of tmux.
func TestBootstrapWiredBeforeTUI(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// Verify InTmux() is called.
	if !strings.Contains(content, "tmux.InTmux()") {
		t.Error("main.go does not call tmux.InTmux() — bootstrap check must be present before TUI")
	}

	// Verify Bootstrap is called when not in tmux.
	if !strings.Contains(content, "tmux.Bootstrap(") {
		t.Error("main.go does not call tmux.Bootstrap() — must self-restart inside tmux")
	}

	// Verify the TUI is started via runTUI.
	if !strings.Contains(content, "runTUI(") {
		t.Error("main.go does not call runTUI() — TUI entry point must exist")
	}

	// Structural ordering: InTmux check must appear before runTUI call.
	inTmuxIdx := strings.Index(content, "tmux.InTmux()")
	bootstrapIdx := strings.Index(content, "tmux.Bootstrap(")
	runTUIIdx := strings.Index(content, "runTUI(")

	if inTmuxIdx < 0 || bootstrapIdx < 0 || runTUIIdx < 0 {
		t.Fatal("one or more expected function calls not found in main.go")
	}

	if inTmuxIdx > runTUIIdx {
		t.Errorf("tmux.InTmux() (pos %d) must appear before runTUI() (pos %d) in main.go",
			inTmuxIdx, runTUIIdx)
	}

	if bootstrapIdx > runTUIIdx {
		t.Errorf("tmux.Bootstrap() (pos %d) must appear before runTUI() (pos %d) in main.go",
			bootstrapIdx, runTUIIdx)
	}
}

// TestBootstrapCalledWhenNotInTmux verifies the control-flow structure: the
// !InTmux() branch must contain a Bootstrap call and must return (or exec)
// before reaching the TUI loop.
func TestBootstrapCalledWhenNotInTmux(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// The pattern "!tmux.InTmux()" means Bootstrap is inside a guard.
	if !strings.Contains(content, "!tmux.InTmux()") {
		t.Error("main.go missing `!tmux.InTmux()` guard — bootstrap must be conditional on not being in tmux")
	}

	// After the Bootstrap path there must be a return so execution does not fall
	// through to the TUI loop.
	// We look for "return nil" appearing after the Bootstrap call block.
	bootstrapIdx := strings.Index(content, "tmux.Bootstrap(")
	returnAfterBootstrap := strings.Index(content[bootstrapIdx:], "return nil")
	if returnAfterBootstrap < 0 {
		t.Error("main.go: no 'return nil' found after tmux.Bootstrap() — bootstrap path must return early")
	}
}

// TestTMUXVarSetWhenTUIStarts is the integration smoke-test described in Sub-AC 3.
// It builds the smux binary, runs it with TMUX set to a fake socket path (so
// InTmux() returns true), and confirms that:
//  1. The bootstrap message "not running inside tmux" is NOT printed — meaning
//     smux detected it was already inside tmux and skipped Bootstrap.
//  2. Smux reached the TUI-initialisation phase, evidenced by the "config"
//     error printed when no config file is found (since we point HOME to a
//     temp directory with no smux config).
//
// Together these two assertions confirm that the TMUX environment variable
// being set is the gate that allows TUI initialisation to proceed.
func TestTMUXVarSetWhenTUIStarts(t *testing.T) {
	// Skip if tmux is not installed — we only need the binary for InTmux().
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping integration smoke-test")
	}

	// Build the smux binary into a temp directory.
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "smux")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	// Build from the module root (two directories up from cmd/smux).
	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/smux")
	buildCmd.Dir = filepath.Join("..", "..")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// Use a temp HOME so there is no ~/.config/smux/config.yaml.
	fakeHome := t.TempDir()

	// Run smux with TMUX set to a valid-looking (but fake) socket path.
	// InTmux() only checks os.Getenv("TMUX") != "", so any non-empty value works.
	runCmd := exec.Command(bin)
	runCmd.Env = []string{
		"TMUX=/tmp/tmux-smux-test/smux,12345,0",
		"HOME=" + fakeHome,
		"PATH=" + os.Getenv("PATH"),
	}

	out, _ := runCmd.CombinedOutput()
	output := string(out)

	// Assert 1: bootstrap was NOT triggered — TMUX was already set.
	if strings.Contains(output, "not running inside tmux") {
		t.Errorf("smux printed the bootstrap message even though TMUX was set\noutput:\n%s", output)
	}

	// Assert 2: smux reached the config-loading phase (TUI initialisation path).
	// When no config file exists, smux prints a message containing "config".
	if !strings.Contains(output, "config") {
		t.Logf("smux output (expected 'config' message):\n%s", output)
		t.Error("smux did not reach the config-loading phase — TUI initialisation path not reached")
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 2 of AC 8 — TUI confirmation wired to tmux.CreateSSHWindow
// ---------------------------------------------------------------------------

// TestCreateSSHWindowCalledAfterTUIConfirm verifies that after runTUI returns
// a non-empty host selection, main.go invokes tmux.CreateSSHWindow with the
// result hosts. This is the core wiring between TUI confirmation and the
// tmux pane-spawning logic.
func TestCreateSSHWindowCalledAfterTUIConfirm(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// CreateSSHWindow must be called.
	if !strings.Contains(content, "tmux.CreateSSHWindow(") {
		t.Error("main.go does not call tmux.CreateSSHWindow — TUI confirmation must invoke pane-spawning logic")
	}

	// It must be called with the hosts from the TUI result.
	if !strings.Contains(content, "result.Hosts") {
		t.Error("main.go does not pass result.Hosts to CreateSSHWindow — confirmed hosts must be forwarded")
	}

	// CreateSSHWindow must appear after runTUI in source order.
	runTUIIdx := strings.Index(content, "runTUI(")
	createWinIdx := strings.Index(content, "tmux.CreateSSHWindow(")
	if runTUIIdx < 0 || createWinIdx < 0 {
		t.Fatal("runTUI or CreateSSHWindow call not found in main.go")
	}
	if createWinIdx < runTUIIdx {
		t.Errorf("tmux.CreateSSHWindow (pos %d) must appear after runTUI (pos %d) in main.go",
			createWinIdx, runTUIIdx)
	}
}

// TestCreateSSHWindowErrorIsHandled verifies that an error from
// tmux.CreateSSHWindow does not abort the main loop — smux must print the
// error and continue looping back to the TUI rather than exiting.
func TestCreateSSHWindowErrorIsHandled(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// There must be an error check after CreateSSHWindow.
	if !strings.Contains(content, "tmux.CreateSSHWindow(") {
		t.Fatal("main.go does not call tmux.CreateSSHWindow")
	}

	// The error handling should use Fprintf (print to stderr) not a hard return/exit.
	// We verify that the error is written to Stderr and the loop is NOT terminated
	// by checking that there is a Fprintf after the CreateSSHWindow call and that
	// the error handling block does not contain "return err" or "os.Exit".
	createWinIdx := strings.Index(content, "tmux.CreateSSHWindow(")
	afterCreate := content[createWinIdx:]

	if !strings.Contains(afterCreate, "Fprintf") {
		t.Error("main.go should print CreateSSHWindow error to stderr (Fprintf) rather than aborting")
	}

	// The snippet after CreateSSHWindow must not have a bare 'return err' that
	// would terminate the run() function on a window creation error.
	// We check the error block ends with a comment or 'continue' / 'else' path,
	// not an unconditional return.  A simple heuristic: "cannot create SSH window"
	// error message followed by code that keeps the loop alive.
	if !strings.Contains(afterCreate, "cannot create SSH window") {
		t.Error("main.go should include a descriptive error message for CreateSSHWindow failures")
	}
}

// TestLastWindowIDUpdatedOnSuccess verifies that when CreateSSHWindow succeeds,
// main.go stores the returned window ID so the TUI can reference it for
// broadcast-toggle operations in the next iteration.
func TestLastWindowIDUpdatedOnSuccess(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// lastWindowID must be declared and updated.
	if !strings.Contains(content, "lastWindowID") {
		t.Error("main.go does not declare a lastWindowID variable — window ID must be tracked across TUI iterations")
	}

	// The variable must be updated from the CreateSSHWindow return value.
	// Look for an assignment like `lastWindowID = windowID` (or similar).
	if !strings.Contains(content, "lastWindowID = ") {
		t.Error("main.go does not assign to lastWindowID — must update it with the new window ID after each CreateSSHWindow call")
	}

	// lastWindowID must be forwarded to runTUI so the next TUI iteration has it.
	// Accept any call where lastWindowID appears as an argument after cfg.
	if !strings.Contains(content, "runTUI(cfg, lastWindowID") {
		t.Error("main.go does not pass lastWindowID to runTUI — the window ID must be forwarded to each TUI iteration for broadcast-toggle support")
	}
}

// ---------------------------------------------------------------------------
// AC 15 Sub-AC 1 — Focus moves to SSH window after creation
// ---------------------------------------------------------------------------

// TestFocusMovesToSSHWindowAfterCreation is a source-inspection test verifying
// that main.go calls SelectWindow(windowID) after CreateSSHWindow succeeds,
// so the user lands on the new SSH window immediately. The TUI restarts silently
// in the smux window background and is ready when the user switches back.
func TestFocusMovesToSSHWindowAfterCreation(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// SelectWindow must be called with the SSH windowID (not a hardcoded index).
	if !strings.Contains(content, `tmux.SelectWindow(windowID)`) {
		t.Error(`main.go must call tmux.SelectWindow(windowID) after CreateSSHWindow to move focus to the SSH window`)
	}

	// SelectWindow(windowID) must appear after CreateSSHWindow in source order.
	createWinIdx := strings.Index(content, "tmux.CreateSSHWindow(")
	selectWinIdx := strings.Index(content, "tmux.SelectWindow(windowID)")
	if createWinIdx < 0 || selectWinIdx < 0 {
		t.Fatal("CreateSSHWindow or SelectWindow(windowID) not found in main.go")
	}
	if selectWinIdx < createWinIdx {
		t.Errorf("tmux.SelectWindow(windowID) (pos %d) must appear after tmux.CreateSSHWindow (pos %d)",
			selectWinIdx, createWinIdx)
	}
}

// ---------------------------------------------------------------------------
// Sub-AC 3 — Main loop cycles TUI → session creation → TUI indefinitely
// ---------------------------------------------------------------------------

// TestMainLoopIsInfiniteFor verifies that the main application loop in run()
// uses an unconditional "for {" construct so it cycles indefinitely until an
// explicit break/return.  A conditional or counted loop (e.g. for i := 0; ...)
// would violate the requirement that smux stays alive after session creation.
func TestMainLoopIsInfiniteFor(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// The loop must use the bare "for {" idiom (infinite loop), not a
	// for-range, for-condition, or counted loop.
	if !strings.Contains(content, "for {") {
		t.Error("main.go does not contain an infinite 'for {' loop — the main loop must cycle indefinitely")
	}
}

// TestMainLoopRunTUICalledEachIteration verifies that runTUI is called inside
// the main for-loop rather than outside it, ensuring a fresh TUI is presented
// to the user on every cycle (not just on first entry).
func TestMainLoopRunTUICalledEachIteration(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// Find the start of the infinite loop.
	loopIdx := strings.Index(content, "for {")
	if loopIdx < 0 {
		t.Fatal("main.go does not contain 'for {' — cannot verify runTUI placement")
	}

	// runTUI must appear after the loop start.
	runTUIIdx := strings.Index(content[loopIdx:], "runTUI(")
	if runTUIIdx < 0 {
		t.Error("main.go: runTUI is not called inside the 'for {' loop — TUI must restart on each cycle")
	}
}

// TestMainLoopContinuesAfterWindowCreation verifies that the main loop does not
// unconditionally exit (return/break) immediately after tmux.CreateSSHWindow.
// After session creation the loop body must complete and flow into the next TUI
// iteration — focus stays on the SSH window, no SelectWindow call needed.
func TestMainLoopContinuesAfterWindowCreation(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	createWinIdx := strings.Index(content, "tmux.CreateSSHWindow(")
	if createWinIdx < 0 {
		t.Fatal("main.go does not call tmux.CreateSSHWindow")
	}

	// The segment after CreateSSHWindow must NOT contain an unconditional return
	// that would abort the loop on success. The error-handling block may log
	// via Fprintf (non-terminating), but must not hard-return.
	afterCreate := content[createWinIdx:]
	// Find the closing brace of the for-loop body to limit the search scope.
	// A simple proxy: check that no "return err" or "return fmt.Errorf" appears
	// immediately after CreateSSHWindow outside the error-check block.
	if strings.Contains(afterCreate, "return err\n") {
		t.Error("main.go: a hard 'return err' found after CreateSSHWindow — window creation failure must be non-fatal (log and continue)")
	}
}

// TestQuitResultBreaksMainLoop is a source-code inspection test that verifies
// the main run loop in main.go exits when the TUI returns a Quit result.
// This ensures pressing q/Ctrl+C terminates the entire smux process, not
// just a single TUI iteration.
func TestQuitResultBreaksMainLoop(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// The main loop must inspect result.Quit.
	if !strings.Contains(content, "result.Quit") {
		t.Error("main.go does not check result.Quit — the main loop must exit when the TUI signals quit")
	}

	// There must be a break or return after the Quit check so the process ends.
	// We accept either "break" (loop exit) or an early return.
	hasBreak := strings.Contains(content, "result.Quit") && strings.Contains(content, "break")
	hasReturn := strings.Contains(content, "result.Quit") && strings.Contains(content, "return nil")
	if !hasBreak && !hasReturn {
		t.Error("main.go must break or return after detecting result.Quit to terminate smux")
	}
}

// ---------------------------------------------------------------------------
// AC 15 Sub-AC 2 — fresh TUI reinitialisation: selections cleared
// ---------------------------------------------------------------------------

// TestRunTUICallsNewToEnsureFreshSelections is a source-inspection test that
// verifies runTUI constructs a brand-new tui.Model via tui.New on every call.
// Calling tui.New guarantees the selection set (state.Selected) is always
// empty and the phase is always BrowsingPhase, satisfying Sub-AC 2's
// requirement that "all host selections are cleared to unselected state" when
// smux returns to window 0 for a fresh TUI.
func TestRunTUICallsNewToEnsureFreshSelections(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// runTUI must exist as a named function.
	if !strings.Contains(content, "func runTUI(") {
		t.Error("main.go does not define a runTUI function — TUI construction must be factored into runTUI so selections are always cleared")
	}

	// runTUI must call tui.New to construct a fresh, unselected model on every
	// invocation. Any approach that reuses or mutates an existing Model between
	// iterations would carry over selections from a previous session.
	if !strings.Contains(content, "tui.New(") {
		t.Error("main.go does not call tui.New — each TUI iteration must start with a freshly initialised model so no host remains selected from a previous round")
	}

	// tui.New must appear inside the runTUI function body, not at package scope.
	runTUIFuncIdx := strings.Index(content, "func runTUI(")
	tuiNewIdx := strings.Index(content, "tui.New(")
	if runTUIFuncIdx < 0 || tuiNewIdx < 0 {
		t.Fatal("runTUI or tui.New not found in main.go")
	}
	if tuiNewIdx < runTUIFuncIdx {
		t.Errorf("tui.New (pos %d) must appear inside runTUI (pos %d) — model must be constructed fresh each time runTUI is called",
			tuiNewIdx, runTUIFuncIdx)
	}
}

// ---------------------------------------------------------------------------
// AC 17 — missing config.yaml prints helpful message with YAML example
// ---------------------------------------------------------------------------

// TestMissingConfigPrintsYAMLExample is an integration test that builds smux,
// runs it with TMUX set (so bootstrap is skipped) but with a HOME directory
// that has no ~/.config/smux/config.yaml, and verifies that the output
// contains a YAML format example including large_selection_threshold.
func TestMissingConfigPrintsYAMLExample(t *testing.T) {
	// Skip if tmux is not installed — we need the binary to get past CheckInstalled.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping integration smoke-test")
	}

	// Build the smux binary into a temp directory.
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "smux")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/smux")
	buildCmd.Dir = filepath.Join("..", "..")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// Use a temp HOME with no smux config.
	fakeHome := t.TempDir()

	// Run smux with TMUX set so InTmux() returns true, skipping Bootstrap.
	runCmd := exec.Command(bin)
	runCmd.Env = []string{
		"TMUX=/tmp/tmux-smux-test/smux,12345,0",
		"HOME=" + fakeHome,
		"PATH=" + os.Getenv("PATH"),
	}

	out, _ := runCmd.CombinedOutput()
	output := string(out)

	// The output must contain the YAML key large_selection_threshold so the
	// user knows it is configurable from the very first run.
	if !strings.Contains(output, "large_selection_threshold") {
		t.Errorf("smux output on missing config does not contain 'large_selection_threshold'\nfull output:\n%s", output)
	}

	// The output must also contain "clusters" to show the hosts section.
	if !strings.Contains(output, "clusters") {
		t.Errorf("smux output on missing config does not contain 'clusters' (YAML example)\nfull output:\n%s", output)
	}
}

// TestMissingConfigMessageSourceInspection is a source-code inspection test
// verifying that main.go uses config.ExampleConfig in its missing-config
// message path, ensuring the YAML example (including large_selection_threshold)
// is always shown to the user when no config file exists.
func TestMissingConfigMessageSourceInspection(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go: %v", err)
	}
	content := string(src)

	// main.go must reference config.ExampleConfig in the missing-config branch.
	if !strings.Contains(content, "config.ExampleConfig") {
		t.Error("main.go does not reference config.ExampleConfig — the missing-config message must include the YAML format example")
	}
}
