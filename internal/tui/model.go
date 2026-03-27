package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/dirtystate"
	"github.com/Suckzoo/smux/internal/executor"
)

// Result is what the TUI returns after the user confirms a selection.
type Result struct {
	// Hosts is the list of selected hosts to connect to.
	Hosts []config.ResolvedHost
	// Quit is true when the user pressed q or Ctrl+C.
	Quit bool
}

// Model is the bubbletea model for the host-selection TUI.
//
// Domain state (which hosts are selected, which phase the selection is in)
// is held in the SelectionState field.  Presentational state (cursor
// position, terminal dimensions) is held in the ViewState field.  Both are
// defined in phase.go and kept intentionally separate.
//
// The model also embeds a TreeState (defined in tree.go) for per-cluster
// expanded/collapsed tracking and maintains a flat visible-node list
// ([]TreeNode) that is rebuilt via BuildFlatList on every state change.
type Model struct {
	cfg      *config.Config
	clusters []string  // sorted cluster names
	tree     TreeState // expanded/collapsed state per cluster

	// state holds the domain-level selection state (phase + selected hosts).
	// Use the isConfirming() / isFilterActive() helpers rather than
	// type-asserting state.Phase directly in every call-site.
	state SelectionState

	// view holds the purely presentational state (cursor, terminal size).
	view ViewState

	flatNodes []TreeNode // visible nodes after filter + expansion applied

	filterInput textinput.Model // UI component — not a domain concern
	viewport    viewport.Model  // UI component — not a domain concern

	// persistent enables the quit-confirmation flow: pressing 'q' in
	// BrowsingPhase transitions to QuitConfirmingPhase instead of exiting
	// immediately. Only set true when smux is running as the long-lived
	// window-0 process (runPersistent), not in popup mode.
	persistent bool

	// countManagedWindows returns the number of non-smux tmux windows that
	// will be killed if the user confirms the quit. Used to populate
	// QuitConfirmingPhase.WindowCount. Ignored when persistent is false.
	countManagedWindows func() int

	// killManagedWindows is called when the user confirms quit in persistent
	// mode. It should kill all non-smux tmux windows. After it returns the
	// TUI exits. Ignored when persistent is false.
	killManagedWindows func() error

	// distributeWizard is non-nil while the distribute-file wizard is active.
	// Ctrl+D in BrowsingPhase creates a new DistributeModel and stores it
	// here.  All Update/View calls are delegated to it until the wizard is
	// done.  When the wizard signals exitToMain the field is set back to nil
	// and normal TUI browsing resumes.
	distributeWizard *DistributeModel

	// dirtyHosts is the set of SSH host addresses (e.g. "10.0.0.1") that have
	// pending cleanup work from a previous distribute-file operation (leftover
	// temporary SSH keys in authorized_keys). Loaded from
	// ~/.smux/dirty-state.json at startup. Used by renderList to display ⚠
	// warning markers next to affected hosts in the inventory view.
	dirtyHosts map[string]bool

	// dirtyFullState is the complete dirty state loaded from disk at startup.
	// It is used by the DirtyStateWarningPhase dialog (which shows per-host
	// details) and by startDirtyCleanup to drive the cleanup command.
	// nil when no dirty state was found at startup.
	dirtyFullState *dirtystate.State

	// Returned after the user confirms a selection or quits.
	done   bool
	result Result
}

// ModelOption is a functional option for configuring a Model at construction
// time without changing New()'s required parameter list.
type ModelOption func(*Model)

// WithPersistentMode enables the quit-confirmation dialog for long-lived smux
// processes. When active, pressing 'q' in BrowsingPhase transitions to
// QuitConfirmingPhase instead of exiting immediately.
//
// count returns the number of non-smux tmux windows that will be killed.
// kill is called when the user confirms the quit; it kills all those windows.
func WithPersistentMode(count func() int, kill func() error) ModelOption {
	return func(m *Model) {
		m.persistent = true
		m.countManagedWindows = count
		m.killManagedWindows = kill
	}
}

// WithDirtyHosts overrides the dirty-host set used for inventory markers.
// The map should be keyed by SSH host address (the Host field of
// config.ResolvedHost). This option is intended for unit tests that need to
// exercise the dirty-state rendering path without touching the file system.
//
// Using this option resets the phase to BrowsingPhase so that tests for
// inventory markers are not affected by any DirtyStateWarningPhase that may
// have been set by auto-loading from disk.  Use WithDirtyState when the
// warning dialog itself is under test.
func WithDirtyHosts(hosts map[string]bool) ModelOption {
	return func(m *Model) {
		m.dirtyHosts = hosts
		m.dirtyFullState = nil
		// Reset to BrowsingPhase so inventory-marker tests remain unaffected
		// by any DirtyStateWarningPhase set during New().
		if _, ok := m.state.Phase.(DirtyStateWarningPhase); ok {
			m.state.Phase = BrowsingPhase{}
		}
	}
}

// WithDirtyState injects a pre-loaded dirty state into the model, setting
// both the inventory markers and the full dirty-state struct used by the
// DirtyStateWarningPhase dialog.  When state is non-empty the initial phase
// is set to DirtyStateWarningPhase so the startup warning is shown.
//
// This option is intended for unit tests that need to exercise the startup
// dirty-state warning dialog without touching ~/.smux/dirty-state.json.
func WithDirtyState(state *dirtystate.State) ModelOption {
	return func(m *Model) {
		m.dirtyHosts = make(map[string]bool, len(state.Hosts))
		for _, h := range state.Hosts {
			m.dirtyHosts[h.Host] = true
		}
		m.dirtyFullState = state
		if !state.IsEmpty() {
			m.state.Phase = DirtyStateWarningPhase{Hosts: state.Hosts}
		}
	}
}

// hostKey returns a string key for a ResolvedHost that uniquely identifies it
// within its originating cluster (cluster/host). This key is used to track
// selection state in the state.Selected map.
//
// ClusterNames[0] is the primary cluster the host was resolved from; it equals
// the clusterName argument that was passed to HostEntry.Resolve.
func hostKey(r config.ResolvedHost) string {
	primary := ""
	if len(r.ClusterNames) > 0 {
		primary = r.ClusterNames[0]
	}
	return primary + "/" + r.DisplayName
}

// New creates a fresh Model from the given config.
// All clusters start expanded and no hosts are selected.
//
// opts are optional ModelOption values (e.g. WithPersistentMode) that
// configure additional model behaviour without changing the required parameter list.
func New(cfg *config.Config, opts ...ModelOption) Model {
	ti := textinput.New()
	ti.Placeholder = "filter hosts..."
	ti.CharLimit = 64

	vp := viewport.New(80, 20)

	clusterNames := cfg.ClusterNames()
	m := Model{
		cfg:      cfg,
		clusters: clusterNames,
		tree:     NewTreeState(clusterNames), // all clusters start expanded
		state: SelectionState{
			Phase:    BrowsingPhase{},
			Selected: make(map[string]bool),
		},
		// view is zero-valued; Width/Height are set by the first WindowSizeMsg.
		filterInput: ti,
		viewport:    vp,
	}

	// Load dirty state from ~/.smux/dirty-state.json. Errors are silently
	// ignored so that a missing or malformed file never prevents the TUI from
	// starting.  WithDirtyHosts() and WithDirtyState() (applied in the opts
	// loop below) can override this for testing without file-system access.
	if ds, err := dirtystate.Load(); err == nil && !ds.IsEmpty() {
		m.dirtyHosts = make(map[string]bool, len(ds.Hosts))
		for _, h := range ds.Hosts {
			m.dirtyHosts[h.Host] = true
		}
		m.dirtyFullState = ds
		// Show the startup warning dialog so the user is aware of pending
		// cleanup before they proceed with normal host-selection browsing.
		m.state.Phase = DirtyStateWarningPhase{Hosts: ds.Hosts}
	} else {
		m.dirtyHosts = make(map[string]bool)
	}

	for _, opt := range opts {
		opt(&m)
	}
	m.rebuildFlat()
	return m
}

// dirtyCleanupCompleteMsg is sent by the background cleanup goroutine when
// it has finished attempting to remove temporary SSH keys from all dirty
// hosts.  The model transitions to BrowsingPhase on receipt.
type dirtyCleanupCompleteMsg struct {
	// err is non-nil only when saving the updated dirty state to disk failed;
	// individual per-host SSH errors are recorded in dirty state, not here.
	err error
}

// quitDirtyCleanupCompleteMsg is sent by the background cleanup goroutine
// that was triggered from QuitDirtyWarningPhase (the exit dirty-state warning
// dialog).  Unlike dirtyCleanupCompleteMsg, receiving this message causes the
// model to quit rather than return to BrowsingPhase.
type quitDirtyCleanupCompleteMsg struct {
	// err is non-nil only when saving the updated dirty state to disk failed.
	err error
	// needsWindowKill indicates that killManagedWindows should be called
	// before exiting (set when the warning was entered from persistent-mode
	// quit-confirm dialog).
	needsWindowKill bool
}

// isDirtyWarning reports whether the model is currently showing the startup
// dirty-state warning dialog (DirtyStateWarningPhase).
func (m Model) isDirtyWarning() bool {
	_, ok := m.state.Phase.(DirtyStateWarningPhase)
	return ok
}

// isConfirming reports whether the model is currently in ConfirmingPhase.
func (m Model) isConfirming() bool {
	_, ok := m.state.Phase.(ConfirmingPhase)
	return ok
}

// isQuitConfirming reports whether the model is currently in QuitConfirmingPhase.
func (m Model) isQuitConfirming() bool {
	_, ok := m.state.Phase.(QuitConfirmingPhase)
	return ok
}

// isQuitDirtyWarning reports whether the model is currently in QuitDirtyWarningPhase.
func (m Model) isQuitDirtyWarning() bool {
	_, ok := m.state.Phase.(QuitDirtyWarningPhase)
	return ok
}

// isDirtyCleanupConfirming reports whether the model is currently showing the
// on-demand cleanup confirmation dialog (DirtyCleanupConfirmPhase).
func (m Model) isDirtyCleanupConfirming() bool {
	_, ok := m.state.Phase.(DirtyCleanupConfirmPhase)
	return ok
}

// isFilterActive reports whether the model is currently in SelectingPhase
// (i.e. the inline filter text input has focus).
func (m Model) isFilterActive() bool {
	_, ok := m.state.Phase.(SelectingPhase)
	return ok
}

// Done reports whether the TUI has finished (selection confirmed or quit).
func (m Model) Done() bool { return m.done }

// GetResult returns the TUI result (valid only after Done() is true).
func (m Model) GetResult() Result { return m.result }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// isDistributing reports whether the distribute-file wizard is currently active.
func (m Model) isDistributing() bool {
	return m.distributeWizard != nil
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.view.Width = msg.Width
		m.view.Height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 4 // reserve space for title + filter + status
		// Also forward size changes to the wizard if it is active.
		if m.distributeWizard != nil {
			updated, _ := m.distributeWizard.Update(msg)
			m.distributeWizard = &updated
		}
		return m, nil

	case remoteDirLoadedMsg:
		// Async result of a remote directory listing initiated by the
		// remote file-tree browser inside the distribute wizard.
		if m.distributeWizard != nil {
			updated, cmd := m.distributeWizard.Update(msg)
			m.distributeWizard = &updated
			return m, cmd
		}
		return m, nil

	case remoteDirErrorMsg:
		// Async error from a remote directory listing.
		if m.distributeWizard != nil {
			updated, cmd := m.distributeWizard.Update(msg)
			m.distributeWizard = &updated
			return m, cmd
		}
		return m, nil

	case transferProgressMsg:
		// Live per-host transfer progress update from the execute step goroutine.
		if m.distributeWizard != nil {
			updated, cmd := m.distributeWizard.Update(msg)
			m.distributeWizard = &updated
			return m, cmd
		}
		return m, nil

	case executeCompleteMsg:
		// All transfers have finished.
		if m.distributeWizard != nil {
			updated, cmd := m.distributeWizard.Update(msg)
			m.distributeWizard = &updated
			return m, cmd
		}
		return m, nil

	case dirtyCleanupCompleteMsg:
		// Background cleanup has finished.  Reload dirty state from disk
		// (some hosts may have been successfully cleaned, others may remain).
		// Then transition to BrowsingPhase so the user can proceed normally.
		if ds, err := dirtystate.Load(); err == nil {
			m.dirtyHosts = make(map[string]bool, len(ds.Hosts))
			for _, h := range ds.Hosts {
				m.dirtyHosts[h.Host] = true
			}
			m.dirtyFullState = ds
		} else {
			m.dirtyHosts = make(map[string]bool)
			m.dirtyFullState = nil
		}
		m.state.Phase = BrowsingPhase{}
		return m, nil

	case quitDirtyCleanupCompleteMsg:
		// Cleanup triggered from the exit dirty-state warning dialog has
		// finished.  Reload dirty state to reflect any successful cleanups,
		// then quit smux.  If the warning was reached via the persistent-mode
		// quit-confirm dialog, also kill managed windows first.
		if ds, err := dirtystate.Load(); err == nil {
			m.dirtyHosts = make(map[string]bool, len(ds.Hosts))
			for _, h := range ds.Hosts {
				m.dirtyHosts[h.Host] = true
			}
			m.dirtyFullState = ds
		} else {
			m.dirtyHosts = make(map[string]bool)
			m.dirtyFullState = nil
		}
		if msg.needsWindowKill && m.killManagedWindows != nil {
			_ = m.killManagedWindows()
		}
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case tea.KeyMsg:
		// While the distribute wizard is active, route all key events to it.
		if m.distributeWizard != nil {
			return m.handleDistributeKey(msg)
		}
		return m.handleKey(msg)
	}

	if m.isFilterActive() {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.rebuildFlat()
		return m, cmd
	}

	return m, nil
}

// handleDistributeKey delegates a key message to the active distribute wizard
// and handles terminal states (exitToMain → resume normal TUI; cancelled →
// propagate tea.Quit to exit smux).
func (m Model) handleDistributeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	updated, cmd := m.distributeWizard.Update(msg)
	m.distributeWizard = &updated
	if updated.IsDone() {
		if updated.IsExitToMain() {
			// User pressed Esc from step 0: dismiss the wizard and return to
			// normal browsing without quitting smux.
			m.distributeWizard = nil
			return m, nil
		}
		// User pressed q/Ctrl+C: propagate the quit command to exit smux.
		return m, cmd
	}
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Startup dirty-state warning dialog.
	if m.isDirtyWarning() {
		return m.handleDirtyWarningKey(msg)
	}

	// Exit dirty-state warning dialog (shown when quitting with pending cleanup).
	if m.isQuitDirtyWarning() {
		return m.handleQuitDirtyWarningKey(msg)
	}

	// On-demand cleanup confirmation dialog (entered via 'C' in BrowsingPhase).
	if m.isDirtyCleanupConfirming() {
		return m.handleDirtyCleanupConfirmKey(msg)
	}

	// Quit confirmation dialog (persistent mode only).
	if m.isQuitConfirming() {
		return m.handleQuitConfirmKey(msg)
	}

	// Large-selection confirmation prompt.
	if m.isConfirming() {
		return m.handleConfirmKey(msg)
	}

	// Filter mode: most keys feed the text input; a few are intercepted.
	if m.isFilterActive() {
		switch msg.Type {
		case tea.KeyCtrlC:
			m.done = true
			m.result = Result{Quit: true}
			return m, tea.Quit
		case tea.KeyEsc:
			m.state.Phase = BrowsingPhase{}
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.rebuildFlat()
			return m, nil
		case tea.KeyEnter:
			m.state.Phase = BrowsingPhase{}
			m.filterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.rebuildFlat()
			return m, cmd
		}
	}

	switch msg.String() {
	case "q":
		if m.persistent && m.countManagedWindows != nil {
			count := m.countManagedWindows()
			m.state.Phase = QuitConfirmingPhase{WindowCount: count}
			return m, nil
		}
		// If there is pending SSH key cleanup, show the exit dirty-state
		// warning before allowing the quit so the user can retry cleanup.
		if len(m.dirtyHosts) > 0 && m.dirtyFullState != nil && !m.dirtyFullState.IsEmpty() {
			m.state.Phase = QuitDirtyWarningPhase{
				Hosts:           m.dirtyFullState.Hosts,
				NeedsWindowKill: false,
			}
			return m, nil
		}
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "ctrl+c":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "ctrl+d":
		// Ctrl+D enters the distribute-file wizard from BrowsingPhase.
		// Only available when not already in a sub-phase.
		if _, ok := m.state.Phase.(BrowsingPhase); ok {
			dw := NewDistributeModel(m.cfg, m.view.Width, m.view.Height)
			m.distributeWizard = &dw
			return m, nil
		}
		return m, nil

	case "C":
		// 'C' (Shift+C) triggers on-demand cleanup of dirty hosts.
		// Target hosts = intersection of currently-selected hosts and dirty set.
		// If no dirty hosts are selected, all dirty hosts are targeted.
		if len(m.dirtyHosts) == 0 || m.dirtyFullState == nil || m.dirtyFullState.IsEmpty() {
			// No dirty hosts — nothing to do.
			return m, nil
		}
		// Compute the intersection of selected hosts and dirty set.
		selected := m.selectedHosts()
		targetAddrs := make(map[string]bool)
		for _, h := range selected {
			if m.dirtyHosts[h.Host] {
				targetAddrs[h.Host] = true
			}
		}
		// Collect the DirtyHost records matching the target addresses.
		var targetHosts []dirtystate.DirtyHost
		for _, dh := range m.dirtyFullState.Hosts {
			if targetAddrs[dh.Host] {
				targetHosts = append(targetHosts, dh)
			}
		}
		// Fall back to all dirty hosts when no dirty hosts are selected.
		if len(targetHosts) == 0 {
			targetHosts = append([]dirtystate.DirtyHost(nil), m.dirtyFullState.Hosts...)
		}
		m.state.Phase = DirtyCleanupConfirmPhase{Hosts: targetHosts}
		return m, nil

	case "/":
		m.state.Phase = SelectingPhase{}
		m.filterInput.Focus()
		return m, textinput.Blink

	case "up", "k":
		if m.view.Cursor > 0 {
			m.view.Cursor--
			m.clampViewport()
		}
	case "down", "j":
		if m.view.Cursor < len(m.flatNodes)-1 {
			m.view.Cursor++
			m.clampViewport()
		}

	case "tab", "right", "l":
		m.toggleExpand()
	case "left", "h":
		m.collapseOrMoveUp()

	case " ":
		m.toggleSelect()

	case "enter":
		return m.handleEnter()
	}

	return m, nil
}

// handleDirtyWarningKey processes key events while the startup dirty-state
// warning dialog is shown.
//
//   - 'y' / Enter: acknowledge and transition to BrowsingPhase.
//   - 'c': start background cleanup, set Cleaning=true on the phase.
//   - 'q' / Ctrl+C: quit smux.
//   - all other keys: ignored (especially while Cleaning is true).
func (m Model) handleDirtyWarningKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	phase, _ := m.state.Phase.(DirtyStateWarningPhase)

	// Ignore input while background cleanup is running.
	if phase.Cleaning {
		return m, nil
	}

	switch msg.String() {
	case "y", "Y", "enter":
		// Acknowledge: proceed to normal browsing without triggering cleanup.
		m.state.Phase = BrowsingPhase{}
		return m, nil

	case "c", "C":
		// Trigger cleanup: mark cleaning in-progress and launch background cmd.
		phase.Cleaning = true
		m.state.Phase = phase
		return m, m.startDirtyCleanup()

	case "q":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "ctrl+c":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	}
	return m, nil
}

// startDirtyCleanup returns a tea.Cmd that runs CleanupDirtyState in a
// background goroutine, then delivers dirtyCleanupCompleteMsg to the event
// loop when it finishes.
func (m Model) startDirtyCleanup() tea.Cmd {
	var hosts []dirtystate.DirtyHost
	if m.dirtyFullState != nil {
		hosts = m.dirtyFullState.Hosts
	}
	return func() tea.Msg {
		ctx := context.Background()
		err := executor.CleanupDirtyState(ctx, hosts)
		return dirtyCleanupCompleteMsg{err: err}
	}
}

// handleDirtyCleanupConfirmKey processes key events while the on-demand
// cleanup confirmation dialog is shown (DirtyCleanupConfirmPhase).
//
//   - 'y' / Enter: start background cleanup, set Cleaning=true on the phase.
//   - 'n' / 'N' / Esc: cancel and return to BrowsingPhase.
//   - 'q' / Ctrl+C: quit smux.
//   - all other keys: ignored (especially while Cleaning is true).
func (m Model) handleDirtyCleanupConfirmKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	phase, _ := m.state.Phase.(DirtyCleanupConfirmPhase)

	// Ignore input while background cleanup is running.
	if phase.Cleaning {
		return m, nil
	}

	switch msg.String() {
	case "y", "Y", "enter":
		// Confirm: start background cleanup.
		phase.Cleaning = true
		m.state.Phase = phase
		return m, m.startSelectedDirtyCleanup(phase.Hosts)

	case "n", "N", "esc":
		// Cancel: return to normal browsing.
		m.state.Phase = BrowsingPhase{}
		return m, nil

	case "q":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "ctrl+c":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	}
	return m, nil
}

// startSelectedDirtyCleanup returns a tea.Cmd that runs CleanupDirtyStateSubset
// for the provided hosts in a background goroutine, then delivers
// dirtyCleanupCompleteMsg to the event loop when it finishes.
//
// Unlike startDirtyCleanup (which cleans all dirty hosts), this function
// cleans only the specified subset while preserving other dirty hosts in the
// persistent state.
func (m Model) startSelectedDirtyCleanup(hosts []dirtystate.DirtyHost) tea.Cmd {
	// Snapshot the host list so the closure does not capture the model.
	targets := append([]dirtystate.DirtyHost(nil), hosts...)
	return func() tea.Msg {
		ctx := context.Background()
		err := executor.CleanupDirtyStateSubset(ctx, targets)
		return dirtyCleanupCompleteMsg{err: err}
	}
}

// handleQuitDirtyWarningKey processes key events while the exit dirty-state
// warning dialog is shown (QuitDirtyWarningPhase).
//
//   - 'c' / 'C': start background cleanup; after it finishes, quit smux.
//   - 'y' / 'Y' / Enter: quit without cleanup (leaving keys on remote hosts).
//   - 'n' / 'N' / Esc: cancel quit and return to BrowsingPhase.
//   - Ctrl+C: emergency exit (bypass warning, no cleanup).
//   - all other keys: ignored (especially while Cleaning is true).
func (m Model) handleQuitDirtyWarningKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	phase, _ := m.state.Phase.(QuitDirtyWarningPhase)

	// Ignore input while background cleanup is running.
	if phase.Cleaning {
		return m, nil
	}

	switch msg.String() {
	case "c", "C":
		// Trigger cleanup, then quit after it finishes.
		phase.Cleaning = true
		m.state.Phase = phase
		return m, m.startQuitDirtyCleanup(phase.NeedsWindowKill)

	case "y", "Y", "enter":
		// Quit without cleanup.  Kill managed windows first if in persistent mode.
		if phase.NeedsWindowKill && m.killManagedWindows != nil {
			_ = m.killManagedWindows()
		}
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "n", "N", "esc":
		// Cancel — return to normal browsing.
		m.state.Phase = BrowsingPhase{}
		return m, nil

	case "ctrl+c":
		// Emergency exit without cleanup.
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	}
	return m, nil
}

// startQuitDirtyCleanup returns a tea.Cmd that runs CleanupDirtyState in a
// background goroutine, then delivers quitDirtyCleanupCompleteMsg (which
// causes the model to quit) when it finishes.  needsWindowKill is forwarded
// so the completion handler knows whether to call killManagedWindows.
func (m Model) startQuitDirtyCleanup(needsWindowKill bool) tea.Cmd {
	var hosts []dirtystate.DirtyHost
	if m.dirtyFullState != nil {
		hosts = m.dirtyFullState.Hosts
	}
	return func() tea.Msg {
		ctx := context.Background()
		err := executor.CleanupDirtyState(ctx, hosts)
		return quitDirtyCleanupCompleteMsg{err: err, needsWindowKill: needsWindowKill}
	}
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		// 'y'/'Y' are the explicit confirmations; Enter is also accepted so
		// the user can quickly confirm by pressing Enter twice.
		hosts := m.selectedHosts()
		m.state.Phase = LaunchingPhase{}
		m.done = true
		m.result = Result{Hosts: hosts}
		return m, tea.Quit
	case "ctrl+c":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	case "n", "N", "esc":
		m.state.Phase = BrowsingPhase{}
		return m, nil
	}
	return m, nil
}

func (m Model) handleQuitConfirmKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		// If there is pending SSH key cleanup, show the exit dirty-state
		// warning before killing windows and quitting.
		if len(m.dirtyHosts) > 0 && m.dirtyFullState != nil && !m.dirtyFullState.IsEmpty() {
			m.state.Phase = QuitDirtyWarningPhase{
				Hosts:           m.dirtyFullState.Hosts,
				NeedsWindowKill: true,
			}
			return m, nil
		}
		// No dirty state — kill all non-smux windows then exit.
		if m.killManagedWindows != nil {
			_ = m.killManagedWindows()
		}
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	case "n", "N", "esc":
		m.state.Phase = BrowsingPhase{}
		return m, nil
	case "ctrl+c":
		// Emergency exit without killing windows.
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit
	}
	return m, nil
}

// handleEnter advances the selection state machine on Enter:
//   - With no hosts selected: no-op (stays in BrowsingPhase).
//   - With < threshold hosts selected: immediately confirms (returns Done).
//   - With ≥ threshold hosts selected: advances to ConfirmingPhase so the
//     user must explicitly acknowledge the large selection.
//
// The threshold is read from the config (default 50 when not configured).
func (m Model) handleEnter() (Model, tea.Cmd) {
	hosts := m.selectedHosts()
	if len(hosts) == 0 {
		return m, nil
	}
	threshold := m.cfg.EffectiveConfirmThreshold()
	if len(hosts) >= threshold {
		m.state.Phase = ConfirmingPhase{Threshold: threshold}
		return m, nil
	}
	m.state.Phase = LaunchingPhase{}
	m.done = true
	m.result = Result{Hosts: hosts}
	return m, tea.Quit
}

// toggleExpand expands or collapses the cluster node under the cursor via
// TreeState.Toggle. Has no effect when the cursor is on a host node.
func (m *Model) toggleExpand() {
	if m.view.Cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.view.Cursor]
	if n.IsCluster() {
		m.tree.Toggle(n.ClusterName)
		m.rebuildFlat()
	}
}

// collapseOrMoveUp collapses the cluster at the cursor when it is expanded,
// otherwise moves the cursor up by one row.
func (m *Model) collapseOrMoveUp() {
	if m.view.Cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.view.Cursor]
	if n.IsCluster() && m.tree.IsExpanded(n.ClusterName) {
		m.tree.SetExpanded(n.ClusterName, false)
		m.rebuildFlat()
		return
	}
	if m.view.Cursor > 0 {
		m.view.Cursor--
		m.clampViewport()
	}
}

// toggleSelect selects/deselects the host under the cursor, or all hosts in
// the cluster when the cursor is on a cluster node.
func (m *Model) toggleSelect() {
	if m.view.Cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.view.Cursor]
	if n.IsCluster() {
		allSelected := m.clusterAllSelected(n.ClusterName)
		cluster := m.cfg.Clusters[n.ClusterName]
		for _, h := range cluster.Hosts {
			r := h.Resolve(n.ClusterName, cluster.Defaults)
			k := hostKey(r)
			if allSelected {
				delete(m.state.Selected, k)
			} else {
				m.state.Selected[k] = true
			}
		}
	} else if n.Host != nil {
		k := hostKey(*n.Host)
		if m.state.Selected[k] {
			delete(m.state.Selected, k)
		} else {
			m.state.Selected[k] = true
		}
	}
}

// clusterAllSelected reports whether every host in the named cluster is selected.
func (m *Model) clusterAllSelected(clusterName string) bool {
	cluster, ok := m.cfg.Clusters[clusterName]
	if !ok {
		return false
	}
	for _, h := range cluster.Hosts {
		r := h.Resolve(clusterName, cluster.Defaults)
		if !m.state.Selected[hostKey(r)] {
			return false
		}
	}
	return len(cluster.Hosts) > 0
}

// selectedHosts returns all currently selected hosts in cluster-sorted order.
//
// Deduplication is performed by SSH alias (host address): if the same host
// appears in multiple clusters and is selected from more than one of them,
// only the first-encountered entry (in cluster-sorted order) is included.
// The returned ResolvedHost always carries the full Clusters list (every
// cluster that contains that SSH alias), regardless of which cluster the
// user used to select it.
func (m *Model) selectedHosts() []config.ResolvedHost {
	var hosts []config.ResolvedHost
	// seenByHost deduplicates by SSH alias across clusters.
	seenByHost := make(map[string]bool)
	for _, name := range m.clusters {
		cluster := m.cfg.Clusters[name]
		for _, h := range cluster.Hosts {
			r := h.Resolve(name, cluster.Defaults)
			k := hostKey(r) // cluster/hostName key — used to look up selection state
			if m.state.Selected[k] && !seenByHost[r.Host] {
				seenByHost[r.Host] = true
				// Enrich with the full list of clusters that contain this alias.
				r.ClusterNames = m.cfg.AllClustersForHost(r.Host)
				hosts = append(hosts, r)
			}
		}
	}
	return hosts
}

// rebuildFlat delegates to BuildFlatList (tree.go) to produce the ordered,
// filtered flat slice of TreeNodes that the TUI should render.
func (m *Model) rebuildFlat() {
	m.flatNodes = BuildFlatList(m.cfg, &m.tree, m.filterInput.Value())
	if m.view.Cursor >= len(m.flatNodes) {
		m.view.Cursor = max(0, len(m.flatNodes)-1)
	}
}

func (m *Model) clampViewport() {
	if m.view.Cursor < m.viewport.YOffset {
		m.viewport.YOffset = m.view.Cursor
	} else if m.view.Cursor >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.YOffset = m.view.Cursor - m.viewport.Height + 1
	}
}

// View implements tea.Model.
func (m Model) View() string {
	if m.view.Width < 40 || m.view.Height < 10 {
		return "Terminal too small (need at least 40×10)"
	}

	// Startup dirty-state warning dialog takes precedence over everything else.
	if m.isDirtyWarning() {
		return m.dirtyWarningView()
	}

	// Exit dirty-state warning dialog (shown when quitting with pending cleanup).
	if m.isQuitDirtyWarning() {
		return m.quitDirtyWarningView()
	}

	// On-demand cleanup confirmation dialog (entered via 'C' in BrowsingPhase).
	if m.isDirtyCleanupConfirming() {
		return m.dirtyCleanupConfirmView()
	}

	// While the distribute wizard is active it owns the entire screen.
	if m.distributeWizard != nil {
		return m.distributeWizard.View()
	}

	if m.isQuitConfirming() {
		return m.quitConfirmView()
	}

	if m.isConfirming() {
		return m.confirmView()
	}

	var sb strings.Builder

	// Title bar.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	sb.WriteString(titleStyle.Render("smux — select hosts") + "\n")

	// Filter line.
	if m.isFilterActive() {
		sb.WriteString("/" + m.filterInput.View() + "\n")
	} else if m.filterInput.Value() != "" {
		filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
		sb.WriteString(filterStyle.Render("filter: "+m.filterInput.Value()) + "\n")
	} else {
		sb.WriteString("\n")
	}

	// Scrollable item list.
	listLines := m.renderList()
	start := m.viewport.YOffset
	end := start + m.viewport.Height
	if end > len(listLines) {
		end = len(listLines)
	}
	if start > end {
		start = end
	}
	for _, line := range listLines[start:end] {
		sb.WriteString(line + "\n")
	}

	// Status bar.
	nSelected := len(m.selectedHosts())
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	status := fmt.Sprintf("  %d selected  |  ↑↓ move  tab/←/→ expand  space select  / filter  enter confirm  ctrl+d distribute  q quit",
		nSelected)
	// Append a dirty-state legend when any hosts have pending cleanup work.
	// This keeps the hint visible even when dirty hosts are scrolled out of view.
	// Also advertises the 'C' keybind to trigger on-demand cleanup.
	if len(m.dirtyHosts) > 0 {
		status += fmt.Sprintf("  |  ⚠ %d host(s) need key cleanup  C cleanup", len(m.dirtyHosts))
	}
	sb.WriteString(statusStyle.Render(status))

	return sb.String()
}

// dirtyWarningView renders the startup dirty-state warning dialog.
//
// Two sub-states:
//  1. Normal (Cleaning==false): lists dirty hosts with acknowledge / cleanup / quit hints.
//  2. Cleaning (Cleaning==true): shows a "Cleaning up…" spinner while the
//     background cleanup command runs.
func (m Model) dirtyWarningView() string {
	phase, _ := m.state.Phase.(DirtyStateWarningPhase)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	hostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Padding(1, 3)

	var inner string
	if phase.Cleaning {
		inner = strings.Join([]string{
			titleStyle.Render("⚠  Cleaning up SSH keys…"),
			"",
			bodyStyle.Render("Removing temporary public keys from remote hosts."),
			bodyStyle.Render("This may take a moment."),
		}, "\n")
	} else {
		var lines []string
		lines = append(lines,
			titleStyle.Render(fmt.Sprintf(
				"⚠  Pending SSH Key Cleanup (%d host(s))", len(phase.Hosts),
			)),
			"",
			bodyStyle.Render("The following hosts have leftover temporary SSH keys"),
			bodyStyle.Render("from a previous distribute-file operation:"),
			"",
		)

		for _, h := range phase.Hosts {
			label := h.Host
			if h.User != "" {
				label = h.User + "@" + label
			}
			if h.HubKeyDir != "" {
				label += fmt.Sprintf("  [hub dir: %s]", h.HubKeyDir)
			} else {
				label += fmt.Sprintf("  [key: %s]", h.KeyComment)
			}
			lines = append(lines, hostStyle.Render("  • "+label))
		}

		lines = append(lines,
			"",
			hintStyle.Render("Press  y / Enter  to acknowledge and continue"),
			hintStyle.Render("Press  c          to clean up now"),
			hintStyle.Render("Press  q          to quit"),
		)
		inner = strings.Join(lines, "\n")
	}

	box := boxStyle.Render(inner)
	boxLines := strings.Count(box, "\n") + 1
	topPad := (m.view.Height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + box
}

// quitDirtyWarningView renders the exit dirty-state warning dialog.
//
// Two sub-states:
//  1. Normal (Cleaning==false): lists dirty hosts with cleanup / quit / cancel hints.
//  2. Cleaning (Cleaning==true): shows a "Cleaning up…" message while the
//     background cleanup command runs, after which smux will quit.
func (m Model) quitDirtyWarningView() string {
	phase, _ := m.state.Phase.(QuitDirtyWarningPhase)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 3)

	var inner string
	if phase.Cleaning {
		inner = strings.Join([]string{
			titleStyle.Render("⚠  Cleaning up SSH keys before quitting…"),
			"",
			bodyStyle.Render("Removing temporary public keys from remote hosts."),
			bodyStyle.Render("smux will quit when cleanup finishes."),
		}, "\n")
	} else {
		var lines []string
		lines = append(lines,
			titleStyle.Render(fmt.Sprintf(
				"⚠  Unresolved SSH Key Cleanup (%d host(s))", len(phase.Hosts),
			)),
			"",
			bodyStyle.Render("The following hosts still have temporary SSH keys"),
			bodyStyle.Render("from a previous distribute-file operation:"),
			"",
		)

		for _, h := range phase.Hosts {
			label := h.Host
			if h.User != "" {
				label = h.User + "@" + label
			}
			if h.HubKeyDir != "" {
				label += fmt.Sprintf("  [hub dir: %s]", h.HubKeyDir)
			} else {
				label += fmt.Sprintf("  [key: %s]", h.KeyComment)
			}
			lines = append(lines, hostStyle.Render("  • "+label))
		}

		lines = append(lines,
			"",
			bodyStyle.Render("These keys will remain on the hosts if you quit without cleaning up."),
			"",
			hintStyle.Render("Press  c          to retry cleanup, then quit"),
			hintStyle.Render("Press  y / Enter  to quit anyway (leave keys)"),
			hintStyle.Render("Press  n / Esc    to cancel quit"),
		)
		inner = strings.Join(lines, "\n")
	}

	box := boxStyle.Render(inner)
	boxLines := strings.Count(box, "\n") + 1
	topPad := (m.view.Height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + box
}

// dirtyCleanupConfirmView renders the on-demand cleanup confirmation dialog.
//
// Two sub-states:
//  1. Normal (Cleaning==false): lists target dirty hosts, displays a security
//     risk warning, and shows y/n/q hints.
//  2. Cleaning (Cleaning==true): shows a "Cleaning up…" message while the
//     background cleanup command runs.
func (m Model) dirtyCleanupConfirmView() string {
	phase, _ := m.state.Phase.(DirtyCleanupConfirmPhase)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	warnStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 3)

	var inner string
	if phase.Cleaning {
		inner = strings.Join([]string{
			titleStyle.Render("🔑  Removing Temporary SSH Keys…"),
			"",
			bodyStyle.Render("Removing leftover temporary public keys from remote hosts."),
			bodyStyle.Render("This may take a moment."),
		}, "\n")
	} else {
		var lines []string
		lines = append(lines,
			titleStyle.Render(fmt.Sprintf(
				"🔑  Clean Up Temporary SSH Keys (%d host(s))", len(phase.Hosts),
			)),
			"",
			warnStyle.Render("⚠  SECURITY RISK WARNING"),
			bodyStyle.Render("Leftover temporary SSH keys grant unauthorized access to these hosts."),
			bodyStyle.Render("Removing them as soon as possible is strongly recommended."),
			"",
			bodyStyle.Render("The following hosts will have their temporary keys removed:"),
			"",
		)

		for _, h := range phase.Hosts {
			label := h.Host
			if h.User != "" {
				label = h.User + "@" + label
			}
			if h.HubKeyDir != "" {
				label += fmt.Sprintf("  [hub dir: %s]", h.HubKeyDir)
			} else {
				label += fmt.Sprintf("  [key: %s]", h.KeyComment)
			}
			lines = append(lines, hostStyle.Render("  • "+label))
		}

		lines = append(lines,
			"",
			hintStyle.Render("Press  y / Enter  to clean up now"),
			hintStyle.Render("Press  n / Esc    to cancel"),
			hintStyle.Render("Press  q          to quit"),
		)
		inner = strings.Join(lines, "\n")
	}

	box := boxStyle.Render(inner)
	boxLines := strings.Count(box, "\n") + 1
	topPad := (m.view.Height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + box
}

func (m Model) confirmView() string {
	hosts := m.selectedHosts()
	n := len(hosts)

	// Extract the threshold stored in the ConfirmingPhase so the user sees the
	// configured value that triggered the prompt.
	threshold := 0
	if cp, ok := m.state.Phase.(ConfirmingPhase); ok {
		threshold = cp.Threshold
	}

	// Styles.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("11")).
		Padding(1, 3)

	// Box content — include both the selection count and the configured threshold
	// so the user understands why the prompt appeared.
	title := titleStyle.Render(fmt.Sprintf("⚠  Large selection (%d hosts, threshold %d)", n, threshold))
	body := bodyStyle.Render(fmt.Sprintf(
		"You are about to open %d SSH panes.\nThis will split your tmux window into %d panes.", n, n,
	))
	hint := hintStyle.Render("Press  y  to confirm,  n / Esc  to go back")

	inner := strings.Join([]string{title, "", body, "", hint}, "\n")
	box := boxStyle.Render(inner)

	// Vertically centre the box within the terminal.
	boxLines := strings.Count(box, "\n") + 1
	topPad := (m.view.Height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + box
}

func (m Model) quitConfirmView() string {
	count := 0
	if qcp, ok := m.state.Phase.(QuitConfirmingPhase); ok {
		count = qcp.WindowCount
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 3)

	title := titleStyle.Render("Quit smux?")
	var body string
	if count > 0 {
		body = bodyStyle.Render(fmt.Sprintf(
			"This will kill %d SSH window(s) and exit smux.", count,
		))
	} else {
		body = bodyStyle.Render("Exit smux? (No SSH windows will be killed.)")
	}
	hint := hintStyle.Render("Press  y  to quit,  n / Esc  to stay")

	inner := strings.Join([]string{title, "", body, "", hint}, "\n")
	box := boxStyle.Render(inner)

	boxLines := strings.Count(box, "\n") + 1
	topPad := (m.view.Height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + box
}

// renderList builds one rendered string per visible TreeNode, applying cursor,
// cluster-header, selection, and dirty-state styles. Styles are applied to
// raw (ANSI-free) text so that no inner reset sequence can break the cursor
// background colour.
//
// Dirty hosts (those with leftover temporary SSH keys from a previous
// distribute-file operation) are rendered with a ⚠ warning prefix in amber.
// The dirty indicator is visible regardless of selection or cursor state.
func (m Model) renderList() []string {
	clusterStyle := lipgloss.NewStyle().Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)
	// dirtyStyle: amber/yellow foreground for hosts with pending cleanup work.
	dirtyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	// dirtySelectedStyle: slightly brighter amber for dirty + selected hosts so
	// the selection state remains distinguishable.
	dirtySelectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

	var lines []string
	for i, n := range m.flatNodes {
		isCursor := i == m.view.Cursor

		var text string
		var isCluster, isSelected, isDirty bool

		if n.IsCluster() {
			isCluster = true
			arrow := "▶ "
			if m.tree.IsExpanded(n.ClusterName) {
				arrow = "▼ "
			}
			allSel := m.clusterAllSelected(n.ClusterName)
			check := "[ ]"
			if allSel {
				check = "[✓]"
			}
			cluster := m.cfg.Clusters[n.ClusterName]
			count := len(cluster.Hosts)
			text = fmt.Sprintf("%s%s %s (%d hosts)", arrow, check, n.ClusterName, count)
		} else if n.Host != nil {
			k := hostKey(*n.Host)
			isSelected = m.state.Selected[k]
			isDirty = m.dirtyHosts[n.Host.Host]
			check := "  [ ] "
			if isSelected {
				check = "  [✓] "
			}
			text = check + n.Host.DisplayName
			// Prepend the warning glyph so it is always visible, including
			// when the cursor is on this row or the row is selected.
			if isDirty {
				text = "⚠ " + text
			}
		}

		// Apply exactly one style so no inner ANSI reset can interrupt the
		// cursor background. Priority: cursor > cluster > dirty+selected >
		// selected > dirty > default.
		var line string
		switch {
		case isCursor:
			line = cursorStyle.Render(padRight(text, m.view.Width))
		case isCluster:
			line = clusterStyle.Render(text)
		case isSelected && isDirty:
			line = dirtySelectedStyle.Render(text)
		case isSelected:
			line = selectedStyle.Render(text)
		case isDirty:
			line = dirtyStyle.Render(text)
		default:
			line = text
		}

		lines = append(lines, line)
	}
	return lines
}

// padRight pads s with spaces until its visible width reaches width.
// lipgloss.Width is used so Unicode characters and ANSI sequences are
// measured correctly.
func padRight(s string, width int) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vis)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
