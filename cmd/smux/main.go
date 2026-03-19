package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/tmux"
	"github.com/Suckzoo/smux/internal/tui"
)

// Build-time variables set by goreleaser via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	popup := flag.Bool("popup", false, "run as ephemeral popup (used internally by the prefix+s tmux keybinding)")
	smartOpen := flag.Bool("smart-open", false, "open popup if smux is running, revive smux if not (used by the prefix+s tmux keybinding)")
	flag.Parse()

	if err := run(*popup, *smartOpen); err != nil {
		fmt.Fprintf(os.Stderr, "smux: %v\n", err)
		os.Exit(1)
	}
}

func run(popup, smartOpen bool) error {
	// 1. Verify tmux is installed.
	if err := tmux.CheckInstalled(); err != nil {
		return fmt.Errorf("%w\n\nPlease install tmux: https://github.com/tmux/tmux", err)
	}

	// 2. Auto-bootstrap: if we are not already inside a tmux session, create one
	//    and re-exec smux inside it. This call replaces the current process.
	if !tmux.InTmux() {
		fmt.Println("smux: not running inside tmux — creating a new session...")
		if err := tmux.Bootstrap("smux"); err != nil {
			return fmt.Errorf("cannot bootstrap tmux session: %w", err)
		}
		// After attach returns, we are done.
		return nil
	}

	// 3. Enable mouse mode and pane click/double-click bindings at session start
	//    so events are detectable even before the first SSH window is created.
	tmux.ConfigureMouseMode()

	// 4. Load configuration, auto-creating an example if none exists.
	cfg, err := config.Load()
	if err != nil {
		if err == config.ErrMissingConfig {
			if createErr := config.CreateDefault(); createErr != nil {
				return fmt.Errorf("no config found and could not create one: %w", createErr)
			}
			fmt.Fprintf(os.Stderr, "smux: no config found — created example at ~/.config/smux/config.yaml\n\nEdit it to add your clusters. Example format:\n\n%s\n", config.ExampleConfig)
			cfg, err = config.Load()
			if err != nil {
				return fmt.Errorf("loading config after creation: %w", err)
			}
		} else {
			return fmt.Errorf("loading config: %w", err)
		}
	}

	// 4b. Validate config fields that are not caught by YAML parsing.
	if _, err := cfg.EffectivePaneLayout(); err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	if smartOpen {
		return runSmartOpen(cfg)
	}
	if popup {
		return runPopup(cfg)
	}
	return runPersistent(cfg)
}

// runPopup runs smux as an ephemeral one-shot TUI inside a tmux display-popup.
// It creates SSH windows exactly once and then exits (closing the popup).
// After a successful launch focus is moved to the new SSH window.
func runPopup(cfg *config.Config) error {
	var lastWindowID string
	var paneLayouts []tmux.PaneLayout
	if lastWindowID != "" {
		if layouts, err := tmux.GetPaneLayouts(lastWindowID); err == nil {
			paneLayouts = layouts
		}
	}

	result, err := runTUI(cfg, lastWindowID, paneLayouts)
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	if result.Quit || len(result.Hosts) == 0 {
		return nil
	}

	windowID, err := tmux.CreateSSHWindow(result.Hosts, cfg)
	if err != nil {
		return fmt.Errorf("cannot create SSH window: %w", err)
	}
	// Move focus to the new SSH window before the popup closes.
	tmux.SelectWindow(windowID)
	return nil
}

// runSmartOpen is the handler for the --smart-open flag, bound to M-s.
// It decides at runtime what to do:
//   - No smux window exists: create a new one (focus switches to it), move it
//     to the front, and exit — the new smux process handles the rest.
//   - Current window IS the smux window: no-op (user is already in smux).
//   - Smux window exists elsewhere: show an ephemeral popup TUI.
func runSmartOpen(cfg *config.Config) error {
	smuxPath, _ := os.Executable()

	currentWindowID, _ := tmux.CurrentWindowID()
	smuxWindowID, found := tmux.FindSmuxWindow()

	if !found {
		// No running smux — create a new window and switch focus to it.
		windowID, err := tmux.NewSmuxWindow(smuxPath)
		if err != nil {
			return fmt.Errorf("cannot create smux window: %w", err)
		}
		tmux.MoveWindowToFront(windowID)
		return nil
	}

	if smuxWindowID == currentWindowID {
		// Already in the smux window — nothing to do.
		return nil
	}

	// Smux exists in another window — open a popup for quick access.
	tmux.DisplayPopup(smuxPath + " --popup")
	return nil
}

// runPersistent runs smux as the long-lived window-0 TUI process. It:
//   - Checks for another running smux instance (switches to it if found)
//   - Moves the smux window to the front (base-index position)
//   - Registers the prefix+s smart-open keybinding (permanent — not cleaned up on exit)
//   - Cleans up broadcast and attach-pane bindings on exit
//   - Loops: show TUI → create SSH window → switch focus to SSH window → repeat
func runPersistent(cfg *config.Config) error {
	// 5. Get our own window ID before doing anything else.
	smuxWindowID, err := tmux.CurrentWindowID()
	if err != nil {
		smuxWindowID = ""
	}

	// 6. Singleton check: if another smux instance is already running in this
	//    tmux session, switch focus to it and exit cleanly.
	if smuxWindowID != "" {
		if other, found := tmux.FindOtherSmuxWindow(smuxWindowID); found {
			tmux.SelectWindow(other)
			return nil
		}
	}

	// 7. Move the smux window to the front so it is always the leftmost window.
	if smuxWindowID != "" {
		tmux.MoveWindowToFront(smuxWindowID)
	}

	// 8. Register the prefix+s smart-open keybinding. This binding is PERMANENT:
	//    it is NOT unbound when smux exits so the user can press prefix+s again to
	//    revive smux after closing it.
	popupKey, popupMode := resolvePopupKeybinding(cfg)
	smuxPath, _ := os.Executable()
	tmux.SetupSmartOpenKeybinding(popupKey, popupMode, smuxPath)

	// Clean up broadcast-toggle and attach-pane bindings on exit.
	// These are installed by CreateSSHWindow and should be removed when smux
	// is no longer running.
	defer tmux.CleanupKeybindings(cfg)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		tmux.CleanupKeybindings(cfg)
		os.Exit(0)
	}()

	// 9. Adopt any SSH windows from a previous smux run by querying the
	//    @smux-managed window option. The last managed window becomes the
	//    initial lastWindowID so the TUI can offer broadcast-toggle for it.
	var lastWindowID string
	if managed := tmux.GetManagedWindows(); len(managed) > 0 {
		lastWindowID = managed[len(managed)-1]
	}

	// Closures for the quit-confirm dialog: count and kill non-smux windows.
	countFn := func() int { return tmux.CountOtherWindows(smuxWindowID) }
	killFn := func() error { return tmux.KillOtherWindows(smuxWindowID) }

	// 10. Main loop: show TUI, create SSH window, switch to it, repeat.
	// lastWindowID tracks the most recently created SSH window so the TUI can
	// offer broadcast-toggle for that window and re-query pane layouts.
	for {
		// Re-query pane layouts fresh each TUI iteration. Silently use an empty
		// slice if the window was closed or rearranged since last time.
		var paneLayouts []tmux.PaneLayout
		if lastWindowID != "" {
			if layouts, lerr := tmux.GetPaneLayouts(lastWindowID); lerr == nil {
				paneLayouts = layouts
			}
		}

		result, err := runTUI(cfg, lastWindowID, paneLayouts,
			tui.WithPersistentMode(countFn, killFn))
		if err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}
		if result.Quit || len(result.Hosts) == 0 {
			break
		}

		windowID, err := tmux.CreateSSHWindow(result.Hosts, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "smux: cannot create SSH window: %v\n", err)
			// Continue — don't exit on a window creation failure.
		} else {
			lastWindowID = windowID
			// Move smux to the leftmost position first, then switch focus to
			// the SSH window. This avoids focus thrash: smux is repositioned
			// silently while the user lands directly on their SSH sessions.
			if smuxWindowID != "" {
				tmux.MoveWindowToFront(smuxWindowID)
			}
			tmux.SelectWindow(windowID)
		}
	}
	return nil
}

// resolvePopupKeybinding returns the popup key and mode from config, falling
// back to the defaults (s in prefix mode) when unset.
func resolvePopupKeybinding(cfg *config.Config) (key, mode string) {
	key = "s"
	mode = "prefix"
	if cfg == nil {
		return
	}
	if cfg.Keybindings.PopupToggle.Key != "" {
		key = cfg.Keybindings.PopupToggle.Key
	}
	if cfg.Keybindings.PopupToggle.Mode != "" {
		mode = cfg.Keybindings.PopupToggle.Mode
	}
	return
}

// runTUI runs the bubbletea TUI and returns the user's selection or quit intent.
// lastWindowID is the tmux window ID of the most recently created SSH window;
// it is forwarded to the Model so the broadcast toggle key can toggle synchronize-panes on it.
// paneLayouts is the freshly queried geometry of panes in the last SSH window,
// used by the double-click handler to identify which pane was clicked.
// opts are additional ModelOptions passed to tui.New (e.g. WithPersistentMode).
func runTUI(cfg *config.Config, lastWindowID string, paneLayouts []tmux.PaneLayout, opts ...tui.ModelOption) (tui.Result, error) {
	allOpts := append([]tui.ModelOption{tui.WithPaneLayouts(paneLayouts)}, opts...)
	m := tui.New(cfg, lastWindowID, tmux.ToggleSynchronizePanes, tmux.BreakPane, allOpts...)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return tui.Result{}, err
	}
	tm, ok := finalModel.(tui.Model)
	if !ok {
		return tui.Result{Quit: true}, nil
	}
	return tm.GetResult(), nil
}

// ensure version variables are referenced to satisfy goreleaser ldflags.
var _ = version
var _ = commit
var _ = date
