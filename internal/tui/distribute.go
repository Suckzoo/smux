package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// DistributeStep enumerates the wizard steps for distribute-file mode.
type DistributeStep int

const (
	// DistributeStepSourceSelect is the first step: choose a source host
	// (local or remote) as the origin for file distribution.
	DistributeStepSourceSelect DistributeStep = iota
	// DistributeStepFileBrowse is the second step: browse the file-tree on
	// the chosen source host and select one or more files to distribute.
	DistributeStepFileBrowse
	// DistributeStepDestHosts is the third step: choose destination hosts.
	DistributeStepDestHosts
	// DistributeStepCopyMode is the fourth step: choose copy mode
	// (direct parallel or hub-and-spoke).
	DistributeStepCopyMode
	// DistributeStepConfirm is the fifth step: review full copy details and
	// optionally enable checksum verification before proceeding.
	DistributeStepConfirm
	// DistributeStepExecute is the final step: execute the distribution.
	DistributeStepExecute
	// DistributeStepRetryConfirm is a special step used exclusively by the
	// retry flow (NewRetryDistributeModel).  It presents the recovered
	// distribution parameters from a previous DistributeReport and asks the
	// user to confirm before re-executing only the failed transfers.
	//
	// This step does NOT appear in the normal six-step wizard breadcrumb; it
	// is only reached when smux is started with a retry report argument.  On
	// confirmation (Enter or y) the model populates its fields from
	// retryParams and transitions directly to DistributeStepExecute.  On
	// rejection (n, Esc, or q/Ctrl+C) the model returns to normal TUI or
	// quits smux.
	DistributeStepRetryConfirm
)

// distributeStepCount is the total number of wizard steps shown in the
// breadcrumb.  DistributeStepRetryConfirm is NOT counted here because it is
// only used as an alternative entry-point for the retry flow, not as part of
// the normal six-step wizard.
const distributeStepCount = int(DistributeStepExecute) + 1

// distributeStepLabels maps each step to its display name.
var distributeStepLabels = [distributeStepCount]string{
	"Select Source",
	"Browse Files",
	"Select Destinations",
	"Choose Copy Mode",
	"Confirm",
	"Execute",
}

// sourceOriginItem represents one selectable row in the source-origin list.
// The first item is always "local", followed by all configured remote hosts.
type sourceOriginItem struct {
	label string // human-readable label, e.g. "Local (this machine)" or host alias
	host  string // empty string = local; SSH alias = remote SFTP source
}

// copyModeItem represents one selectable row in the copy mode list.
type copyModeItem struct {
	label       string // short name, e.g. "Direct parallel"
	value       string // internal value stored in DistributeModel.copyMode
	description string // one-sentence explanation shown below the label
}

// copyModeItems is the fixed ordered list of copy mode options presented to
// the user in DistributeStepCopyMode.
var copyModeItems = []copyModeItem{
	{
		label: "Direct parallel",
		value: "parallel",
		description: "Copy the file from the source to every destination host " +
			"simultaneously.  Best when the source has high outbound bandwidth.",
	},
	{
		label: "Hub-and-spoke",
		value: "hub-spoke",
		description: "Copy the file to one hub host first, then fan out from the " +
			"hub to all destination hosts in parallel.  Reduces source bandwidth " +
			"usage when many destinations are involved.",
	},
}

// buildSourceOriginItems constructs the list of source-origin choices:
// "Local (this machine)" first, then every configured host in
// cluster-sorted order (deduplicating by SSH alias via AllResolvedHosts).
func buildSourceOriginItems(cfg *config.Config) []sourceOriginItem {
	items := []sourceOriginItem{
		{label: "Local (this machine)", host: ""},
	}
	for _, h := range cfg.AllResolvedHosts() {
		items = append(items, sourceOriginItem{
			label: h.DisplayName,
			host:  h.Host,
		})
	}
	return items
}

// DistributeModel is the bubbletea sub-model for the distribute-file wizard.
//
// It manages step-by-step navigation and preserves intermediate state when the
// user navigates backwards via Esc.  The parent Model delegates all Update/View
// calls to this model while m.distributeWizard != nil.
//
// Navigation contract:
//   - Esc from step > 0 — go back one step, preserving state.
//   - Esc from step 0   — set exitToMain=true so the parent returns to normal TUI.
//   - q / Ctrl+C        — set cancelled=true and return tea.Quit to exit smux.
//   - Enter             — advance to the next step (or execute on the last step).
type DistributeModel struct {
	cfg    *config.Config
	width  int
	height int

	// step is the current position in the wizard flow.
	step DistributeStep

	// Per-step state — preserved when navigating backwards with Esc.
	sourcePath string               // local or remote file path (step 0, set by file browser sub-AC)
	sourceHost string               // empty = local; SSH alias = SFTP remote (step 0)
	destHosts  []config.ResolvedHost // selected destination hosts (step 2)
	copyMode   string               // "parallel" or "hub-spoke" (step 3)
	destPath   string               // destination path on each target host (step 4); empty = same as source

	// Step 4 (DistributeStepConfirm) state.
	// verifyChecksum controls whether a checksum is computed and verified
	// after each transfer.  Toggled with Space in DistributeStepConfirm.
	// Preserved on back-navigation.
	verifyChecksum bool

	// Step 0: source origin selector state.
	// sourceOriginItems is the full list of choosable source origins
	// (local + all configured hosts).  sourceOriginCursor is the index of
	// the highlighted row.  Both are preserved on back-navigation.
	sourceOriginItems  []sourceOriginItem
	sourceOriginCursor int

	// Step 1: file-tree browser state.
	//
	// localFileTree is non-nil when the user chose "local" as the source
	// origin.  remoteFileTree is non-nil when a remote host was chosen.
	// Both are preserved on back-navigation so cursor/expanded state is kept.
	// remoteTreeForHost records the SSH alias the remoteFileTree was built for,
	// so the tree can be recreated when the user changes the source host.
	localFileTree     *FileTreeModel
	remoteFileTree    *RemoteFileTreeModel
	remoteTreeForHost string

	// sourcePaths holds the file paths selected in the file-tree browser.
	// Populated when the user confirms the selection with Ctrl+D in step 1.
	sourcePaths []string

	// Step 2: destination host selector state.
	// destHostItems is the flat list of all resolved hosts from the config.
	// destHostCursor is the highlighted row index.
	// destHostSelected tracks which hosts are currently checked (keyed by
	// host.Host SSH alias; a key is present iff the host is selected).
	destHostItems    []config.ResolvedHost
	destHostCursor   int
	destHostSelected map[string]bool

	// Step 3: copy mode selector state.
	// copyModeCursor is the index of the highlighted copy mode option
	// (0 = direct parallel, 1 = hub-and-spoke).  Preserved on back-navigation.
	copyModeCursor int

	// retryParams is non-nil when the model was created via
	// NewRetryDistributeModel.  It holds the distribution parameters
	// recovered from a previous DistributeReport.  The retry confirmation
	// step (DistributeStepRetryConfirm) presents these parameters to the
	// user; on approval the fields are copied into the model's normal
	// per-step state (sourcePaths, destHosts, copyMode, destPath) and the
	// wizard jumps directly to DistributeStepExecute.
	retryParams *executor.RetryParams

	// Execute step (DistributeStepExecute) state.
	//
	// executeStarted is true once the transfer goroutine has been launched.
	// It is set to true by startExecution() and never reset.
	executeStarted bool

	// executeDone is true once the progress channel has been closed (all
	// transfers have completed, succeeded or failed).
	executeDone bool

	// hostProgress tracks the current TransferStatus for each destination
	// host, keyed by host.Host (SSH alias).  Initialised by startExecution().
	hostProgress map[string]executor.TransferStatus

	// progressCh is the read end of the channel produced by startExecution().
	// It is drained by successive waitForProgress commands; nil before
	// execution starts.
	progressCh <-chan executor.ProgressUpdate

	// Terminal signals consumed by the parent Model.
	//
	// done is true once the wizard has reached a terminal state (either because
	// the user cancelled or because execution is complete).
	done bool
	// exitToMain is true when the user pressed Esc from step 0: the parent
	// Model should discard the wizard and return to normal browsing mode
	// without exiting smux.
	exitToMain bool
	// cancelled is true when the user pressed q or Ctrl+C: the parent Model
	// should propagate tea.Quit.
	cancelled bool
}

// NewDistributeModel creates a fresh DistributeModel starting at step 0.
func NewDistributeModel(cfg *config.Config, width, height int) DistributeModel {
	return DistributeModel{
		cfg:    cfg,
		width:  width,
		height: height,
		step:   DistributeStepSourceSelect,

		// Step 0: initialise source-origin list; cursor starts at "local" (index 0).
		sourceOriginItems:  buildSourceOriginItems(cfg),
		sourceOriginCursor: 0,

		// Step 1: initialise destination host list; no hosts selected initially.
		destHostItems:    cfg.AllResolvedHosts(),
		destHostCursor:   0,
		destHostSelected: make(map[string]bool),
	}
}

// NewRetryDistributeModel creates a DistributeModel pre-loaded with the
// recovered distribution parameters from params and positioned at the
// DistributeStepRetryConfirm step.
//
// The user is shown a summary of the recovered operation (source, failed
// destinations, copy mode, destination path) and must confirm with Enter or
// 'y' before the retry is launched.  Pressing 'n', Esc, 'q', or Ctrl+C
// aborts the retry: 'n'/Esc returns the user to the normal TUI while
// 'q'/Ctrl+C exits smux.
func NewRetryDistributeModel(cfg *config.Config, width, height int, params executor.RetryParams) DistributeModel {
	m := DistributeModel{
		cfg:    cfg,
		width:  width,
		height: height,
		step:   DistributeStepRetryConfirm,

		// Retry-flow state.
		retryParams: &params,

		// Populate the normal per-step fields from params so that after the
		// user confirms, startExecution() has everything it needs.
		sourceHost:  params.SourceHost.Host,
		sourcePaths: []string{params.SourcePath},
		destHosts:   params.FailedHosts,
		copyMode:    params.CopyMode,
		destPath:    params.DestPath,

		// Minimal list support for resolvedSourceHost().
		sourceOriginItems:  buildSourceOriginItems(cfg),
		destHostItems:      cfg.AllResolvedHosts(),
		destHostSelected:   make(map[string]bool),
	}
	return m
}

// IsDone reports whether the wizard has reached a terminal state.
func (m DistributeModel) IsDone() bool { return m.done }

// IsExitToMain reports whether the wizard should return to the normal TUI.
func (m DistributeModel) IsExitToMain() bool { return m.exitToMain }

// IsCancelled reports whether the user chose to quit smux from within the wizard.
func (m DistributeModel) IsCancelled() bool { return m.cancelled }

// SourceHost returns the SSH alias of the chosen source host, or the empty
// string when "local" was selected.  Valid only after step 0 is confirmed.
func (m DistributeModel) SourceHost() string { return m.sourceHost }

// SourcePaths returns the slice of file paths selected in the file-tree
// browser (step 1).  Valid only after step 1 is confirmed with Ctrl+D.
func (m DistributeModel) SourcePaths() []string { return m.sourcePaths }

// DestHosts returns the slice of destination hosts selected in step 2.
// Valid only after step 2 is confirmed.
func (m DistributeModel) DestHosts() []config.ResolvedHost { return m.destHosts }

// CopyMode returns the copy mode selected in step 3: "parallel" for direct
// parallel copy, or "hub-spoke" for hub-and-spoke.  Returns the empty string
// before step 3 is confirmed.
func (m DistributeModel) CopyMode() string { return m.copyMode }

// VerifyChecksum reports whether the user toggled on the checksum verification
// option in DistributeStepConfirm.  Valid after step 4 is confirmed with Enter.
func (m DistributeModel) VerifyChecksum() bool { return m.verifyChecksum }

// DestPath returns the destination filesystem path configured in the confirm
// step.  An empty string means "use the same path as the source" at execution
// time.  Valid after step 4 is confirmed with Enter.
func (m DistributeModel) DestPath() string { return m.destPath }

// Init satisfies the tea.Model interface; the wizard has no start-up commands.
func (m DistributeModel) Init() tea.Cmd { return nil }

// Update handles messages directed at the distribute wizard.
// The parent Model is responsible for routing messages here while the wizard
// is active.  In addition to key and window-size events, the parent must also
// forward remoteDirLoadedMsg and remoteDirErrorMsg so that the remote
// file-tree browser can process asynchronous directory listings.
func (m DistributeModel) Update(msg tea.Msg) (DistributeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Forward size to active file trees so their viewports are correct.
		if m.localFileTree != nil {
			raw, _ := m.localFileTree.Update(msg)
			ft := raw.(FileTreeModel)
			m.localFileTree = &ft
		}
		if m.remoteFileTree != nil {
			raw, _ := m.remoteFileTree.Update(msg)
			rfm := raw.(RemoteFileTreeModel)
			m.remoteFileTree = &rfm
		}
		return m, nil

	case remoteDirLoadedMsg:
		// Async response from a remote directory listing initiated by the
		// remote file-tree browser.  Only relevant while in FileBrowse step.
		if m.step == DistributeStepFileBrowse && m.remoteFileTree != nil {
			raw, cmd := m.remoteFileTree.Update(msg)
			rfm := raw.(RemoteFileTreeModel)
			m.remoteFileTree = &rfm
			return m, cmd
		}
		return m, nil

	case remoteDirErrorMsg:
		// Async error from a remote directory listing.
		if m.step == DistributeStepFileBrowse && m.remoteFileTree != nil {
			raw, cmd := m.remoteFileTree.Update(msg)
			rfm := raw.(RemoteFileTreeModel)
			m.remoteFileTree = &rfm
			return m, cmd
		}
		return m, nil

	case transferProgressMsg:
		// Live progress update from the transfer goroutine.
		return m.handleProgressUpdate(executor.ProgressUpdate(msg))

	case executeCompleteMsg:
		// All transfers have finished; drain complete.
		m.executeDone = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleProgressUpdate applies a single ProgressUpdate to the model's
// hostProgress map and returns the next waitForProgress command so the
// BubbleTea runtime continues draining the channel.
func (m DistributeModel) handleProgressUpdate(u executor.ProgressUpdate) (DistributeModel, tea.Cmd) {
	if m.hostProgress == nil {
		m.hostProgress = make(map[string]executor.TransferStatus)
	}
	m.hostProgress[u.Host.Host] = u.Status
	// Continue listening for the next update.
	return m, waitForProgress(m.progressCh)
}

// handleKey is the central key dispatcher for the wizard.
//
// Global keys (q, Ctrl+C, Esc) are processed first; all remaining keys are
// forwarded to the step-specific handler.
func (m DistributeModel) handleKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "q":
		// q cancels the entire wizard flow and exits smux.
		m.cancelled = true
		m.done = true
		return m, tea.Quit

	case "ctrl+c":
		// Ctrl+C also exits smux from anywhere in the wizard.
		m.cancelled = true
		m.done = true
		return m, tea.Quit

	case "esc":
		if m.step > DistributeStepSourceSelect && m.step != DistributeStepRetryConfirm {
			// Step back one step, preserving all state collected so far.
			m.step--
		} else {
			// Esc from the first step or retry-confirm: signal the parent to
			// return to normal TUI.
			m.exitToMain = true
			m.done = true
		}
		return m, nil
	}

	// Route to the step-specific handler.
	switch m.step {
	case DistributeStepSourceSelect:
		return m.handleSourceOriginKey(msg)
	case DistributeStepFileBrowse:
		return m.handleFileBrowse(msg)
	case DistributeStepDestHosts:
		return m.handleDestHostsKey(msg)
	case DistributeStepCopyMode:
		return m.handleCopyModeKey(msg)
	case DistributeStepConfirm:
		return m.handleConfirmKey(msg)
	case DistributeStepExecute:
		return m.handleExecuteKey(msg)
	case DistributeStepRetryConfirm:
		return m.handleRetryConfirmKey(msg)
	default:
		return m, nil
	}
}

// handleSourceOriginKey handles navigation within the source-origin selector
// (step 0).  Up/down (or j/k) move the cursor; Enter confirms the selection,
// initialises the appropriate file-tree browser, and advances to step 1
// (DistributeStepFileBrowse).
func (m DistributeModel) handleSourceOriginKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.sourceOriginCursor > 0 {
			m.sourceOriginCursor--
		}
	case "down", "j":
		if m.sourceOriginCursor < len(m.sourceOriginItems)-1 {
			m.sourceOriginCursor++
		}
	case "enter":
		// Persist the selected source origin.
		if m.sourceOriginCursor < len(m.sourceOriginItems) {
			m.sourceHost = m.sourceOriginItems[m.sourceOriginCursor].host
		}

		// Initialise (or reuse) the appropriate file-tree browser.
		var initCmd tea.Cmd
		if m.sourceHost == "" {
			// Local source: create a local file-tree rooted at CWD if not
			// yet present.  The existing tree is reused on back-navigation
			// so cursor position and expanded directories are preserved.
			if m.localFileTree == nil {
				ft := NewFileTreeModel("")
				ft.width = m.width
				ft.height = m.height
				m.localFileTree = &ft
			}
		} else {
			// Remote source: create or recreate the remote tree when the
			// chosen host has changed since the last time FileBrowse was
			// active.
			if m.remoteFileTree == nil || m.remoteTreeForHost != m.sourceHost {
				for _, h := range m.destHostItems {
					if h.Host == m.sourceHost {
						rft := NewRemoteFileTreeModel(h)
						rft.width = m.width
						rft.height = m.height
						m.remoteFileTree = &rft
						m.remoteTreeForHost = m.sourceHost
						// Init triggers the initial home-directory fetch.
						initCmd = rft.Init()
						break
					}
				}
			}
		}

		m.step = DistributeStepFileBrowse
		return m, initCmd
	}
	return m, nil
}

// handleFileBrowse routes key messages to the active file-tree browser and
// handles its terminal signals.
//
// Pre-conditions:
//   - m.step == DistributeStepFileBrowse
//   - Esc, q, and Ctrl+C have already been consumed by the global handler and
//     will never arrive here.
//
// Ctrl+D in the file tree confirms the selection: the chosen paths are
// persisted in m.sourcePaths and the wizard advances to DistributeStepDestHosts.
// The tea.Quit command returned by the file tree is intercepted so that the
// wizard (and smux) keep running.
func (m DistributeModel) handleFileBrowse(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	if m.localFileTree != nil {
		raw, _ := m.localFileTree.Update(msg)
		ft := raw.(FileTreeModel)
		m.localFileTree = &ft
		if ft.Done() {
			result := ft.GetResult()
			if result.Quit {
				// Defensive: q/Ctrl+C should be caught globally, but handle here too.
				m.cancelled = true
				m.done = true
				return m, tea.Quit
			}
			// Ctrl+D: persist the selected paths and advance the wizard.
			// The file tree returns tea.Quit on Ctrl+D; we do NOT propagate
			// it because the wizard should continue, not exit.
			m.sourcePaths = result.SelectedPaths
			m.step = DistributeStepDestHosts
			return m, nil
		}
		return m, nil
	}

	if m.remoteFileTree != nil {
		raw, cmd := m.remoteFileTree.Update(msg)
		rfm := raw.(RemoteFileTreeModel)
		m.remoteFileTree = &rfm
		if rfm.Done() {
			result := rfm.GetResult()
			if result.Quit {
				m.cancelled = true
				m.done = true
				return m, tea.Quit
			}
			// Ctrl+D: persist paths and advance wizard (suppress tea.Quit).
			m.sourcePaths = result.SelectedPaths
			m.step = DistributeStepDestHosts
			return m, nil
		}
		// Pass through any fetch commands (e.g. lazy directory loads).
		return m, cmd
	}

	// No active tree (shouldn't normally happen); no-op.
	return m, nil
}

// handleDestHostsKey handles navigation and selection in the destination host
// list (step 1).  Up/down (or j/k) move the cursor; Space toggles the host
// under the cursor; Enter confirms the selection and advances to step 2.
func (m DistributeModel) handleDestHostsKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.destHostCursor > 0 {
			m.destHostCursor--
		}
	case "down", "j":
		if m.destHostCursor < len(m.destHostItems)-1 {
			m.destHostCursor++
		}
	case " ":
		if m.destHostCursor < len(m.destHostItems) {
			key := m.destHostItems[m.destHostCursor].Host
			if m.destHostSelected[key] {
				delete(m.destHostSelected, key)
			} else {
				m.destHostSelected[key] = true
			}
		}
	case "enter":
		// Collect the currently selected hosts (in list order) and persist
		// them in destHosts, then advance to the copy-mode step.
		var selected []config.ResolvedHost
		for _, h := range m.destHostItems {
			if m.destHostSelected[h.Host] {
				selected = append(selected, h)
			}
		}
		m.destHosts = selected
		m.step = DistributeStepCopyMode
	}
	return m, nil
}

// handleCopyModeKey handles navigation and selection in the copy mode list
// (step 3).  Up/down (or j/k) move the cursor; Enter confirms the selection,
// persists m.copyMode, and advances to DistributeStepConfirm.
// (Esc and q/Ctrl+C are already consumed by the global handler and never
// arrive here.)
func (m DistributeModel) handleCopyModeKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.copyModeCursor > 0 {
			m.copyModeCursor--
		}
	case "down", "j":
		if m.copyModeCursor < len(copyModeItems)-1 {
			m.copyModeCursor++
		}
	case "enter":
		// Persist the highlighted option and advance.
		if m.copyModeCursor < len(copyModeItems) {
			m.copyMode = copyModeItems[m.copyModeCursor].value
		}
		m.step = DistributeStepConfirm
	}
	return m, nil
}

// handleConfirmKey handles key input during DistributeStepConfirm.
//
// Space toggles the checksum verification checkbox.
// Enter accepts the current settings and advances to DistributeStepExecute.
// (Esc and q/Ctrl+C are already consumed by the global handler and never
// arrive here.)
func (m DistributeModel) handleConfirmKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case " ":
		// Toggle the checksum verification checkbox.
		m.verifyChecksum = !m.verifyChecksum
	case "enter":
		// User confirmed: proceed to execution step.
		m.step = DistributeStepExecute
		// Do not auto-start execution; the user still sees the Execute step
		// summary and presses Enter to actually begin.
	}
	return m, nil
}

// handleExecuteKey handles key input during DistributeStepExecute.
//
// Enter starts the transfer (first press) or is a no-op once execution has
// begun.  Esc and q/Ctrl+C are already consumed by the global handler.
func (m DistributeModel) handleExecuteKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if !m.executeStarted {
			// Launch the transfer goroutine and begin progress tracking.
			cmd := m.startExecution()
			return m, cmd
		}
		// Execution already in progress or complete; no-op.
	}
	return m, nil
}

// handleRetryConfirmKey handles key input during DistributeStepRetryConfirm.
//
// This step is only reached when the model was created via
// NewRetryDistributeModel.  It presents the recovered distribution parameters
// to the user and waits for explicit confirmation before launching the retry.
//
// Accepted keys:
//   - 'y' or Enter: approve the retry — copy retryParams into the model's
//     normal per-step fields and advance to DistributeStepExecute.
//   - 'n': decline — return to normal TUI (exitToMain = true).
//
// Note: 'q', Ctrl+C, and Esc are already consumed by the global handleKey
// dispatcher and never arrive here.
func (m DistributeModel) handleRetryConfirmKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		// User approved the retry.  The model fields (sourcePaths, destHosts,
		// copyMode, destPath, sourceHost) were already populated by
		// NewRetryDistributeModel, so we can jump straight to the Execute step.
		m.step = DistributeStepExecute
	case "n":
		// User declined — return to normal TUI without retrying.
		m.exitToMain = true
		m.done = true
	}
	return m, nil
}

// View renders the distribute-file wizard for the current step.
//
// When the file-tree browser is active (DistributeStepFileBrowse), the view
// is delegated entirely to the active file-tree model so it can render its own
// title bar, path bar, file list, and status bar without additional chrome.
// When the tree has not been initialised yet (e.g. when the step is set
// directly in tests) the normal wizard chrome is shown instead.
func (m DistributeModel) View() string {
	if m.width < 40 || m.height < 10 {
		return "Terminal too small (need at least 40×10)"
	}

	// Delegate to the active file-tree browser when it is ready.
	if m.step == DistributeStepFileBrowse {
		if m.localFileTree != nil {
			return m.localFileTree.View()
		}
		if m.remoteFileTree != nil {
			return m.remoteFileTree.View()
		}
		// Tree not yet initialised — fall through to normal wizard chrome
		// (the breadcrumb will show "▶ Browse Files").
	}

	var sb strings.Builder

	// Title bar.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	if m.step == DistributeStepRetryConfirm {
		sb.WriteString(titleStyle.Render("smux — retry distribute file") + "\n\n")
	} else {
		sb.WriteString(titleStyle.Render("smux — distribute file") + "\n\n")
	}

	// Step breadcrumb.  The retry-confirm step bypasses the normal
	// breadcrumb because it is not part of the six-step wizard.
	if m.step != DistributeStepRetryConfirm {
		sb.WriteString(m.renderStepBreadcrumb() + "\n\n")
	}

	// Step content inside a rounded border.
	sb.WriteString(m.renderStepContent())

	// Footer hint.
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	var footerParts []string
	switch m.step {
	case DistributeStepSourceSelect:
		footerParts = []string{"esc return to main", "q quit"}
	case DistributeStepRetryConfirm:
		footerParts = []string{"y/enter confirm retry", "n/esc cancel", "q quit"}
	default:
		footerParts = []string{"esc back", "q quit"}
	}
	sb.WriteString("\n\n")
	sb.WriteString(hintStyle.Render("  " + strings.Join(footerParts, "  |  ")))

	return sb.String()
}

// renderStepBreadcrumb renders a horizontal progress indicator showing all
// steps with visual markers for completed (✓), current (▶), and pending (○).
func (m DistributeModel) renderStepBreadcrumb() string {
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var parts []string
	for i := 0; i < distributeStepCount; i++ {
		label := distributeStepLabels[i]
		step := DistributeStep(i)
		var s string
		switch {
		case step < m.step:
			s = doneStyle.Render(fmt.Sprintf("✓ %s", label))
		case step == m.step:
			s = activeStyle.Render(fmt.Sprintf("▶ %s", label))
		default:
			s = pendingStyle.Render(fmt.Sprintf("○ %s", label))
		}
		parts = append(parts, s)
	}
	sep := sepStyle.Render(" → ")
	return "  " + strings.Join(parts, sep)
}

// renderStepContent renders the body content for the current step inside a
// rounded-border box that spans the available terminal width.
func (m DistributeModel) renderStepContent() string {
	boxWidth := m.width - 6
	if boxWidth < 20 {
		boxWidth = 20
	}
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(1, 3).
		Width(boxWidth)

	var inner string
	switch m.step {
	case DistributeStepSourceSelect:
		inner = m.renderSourceSelectStep()
	case DistributeStepFileBrowse:
		inner = m.renderFileBrowsePlaceholder()
	case DistributeStepDestHosts:
		inner = m.renderDestHostsStep()
	case DistributeStepCopyMode:
		inner = m.renderCopyModeStep()
	case DistributeStepConfirm:
		inner = m.renderConfirmStep()
	case DistributeStepExecute:
		inner = m.renderExecuteStepWithProgress()
	case DistributeStepRetryConfirm:
		inner = m.renderRetryConfirmStep()
	default:
		inner = "Unknown step"
	}

	return boxStyle.Render(inner)
}

// renderSourceSelectStep renders the content for step 0: source origin
// selection.  The user picks "local" or one of the configured remote hosts
// as the origin.  Enter advances to the file-tree browser (step 1).
func (m DistributeModel) renderSourceSelectStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render("Step 1 of 6: Select Source File"))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render("Choose the source location for the file to distribute:"))
	sb.WriteString("\n\n")

	if len(m.sourceOriginItems) == 0 {
		sb.WriteString(bodyStyle.Render("(no hosts configured)"))
		sb.WriteString("\n")
	} else {
		for i, item := range m.sourceOriginItems {
			var line string
			if i == m.sourceOriginCursor {
				line = cursorStyle.Render("▶ " + item.label)
			} else {
				line = bodyStyle.Render("  " + item.label)
			}
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("↑↓/jk navigate  enter select  esc back"))
	return sb.String()
}

// renderFileBrowsePlaceholder renders the content for step 1 when the
// file-tree browser has not been initialised yet.  In practice the View()
// method delegates to the active file-tree model before reaching this
// function, so this placeholder is shown only in tests that set the step
// directly without pressing Enter on step 0.
func (m DistributeModel) renderFileBrowsePlaceholder() string {
	source := "local machine"
	if m.sourceHost != "" {
		source = m.sourceHost
	}
	return renderWizardStepContent(
		"Step 2 of 6: Browse and Select Files",
		fmt.Sprintf("Browsing files on: %s\n\n"+
			"Navigate the file tree, select files with Space, and press Ctrl+D to confirm.", source),
		"↑↓/jk move  →/Enter expand  ←/h collapse  space select  . toggle hidden  Ctrl+D confirm  Esc back",
	)
}

// renderDestHostsStep renders the content for step 2: destination host
// selection.  The user navigates the list with up/down (or j/k), toggles
// individual hosts with Space, and presses Enter to confirm.
func (m DistributeModel) renderDestHostsStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render("Step 3 of 6: Select Destination Hosts"))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render("Choose the hosts to copy the file to:"))
	sb.WriteString("\n\n")

	if len(m.destHostItems) == 0 {
		sb.WriteString(bodyStyle.Render("(no hosts configured)"))
		sb.WriteString("\n")
	} else {
		for i, h := range m.destHostItems {
			isCursor := i == m.destHostCursor
			isSelected := m.destHostSelected[h.Host]

			check := "[ ]"
			if isSelected {
				check = "[✓]"
			}
			text := fmt.Sprintf("%s %s", check, h.DisplayName)

			var line string
			switch {
			case isCursor && isSelected:
				line = cursorStyle.Render("▶ " + text)
			case isCursor:
				line = cursorStyle.Render("▶ " + text)
			case isSelected:
				line = selectedStyle.Render("  " + text)
			default:
				line = bodyStyle.Render("  " + text)
			}
			sb.WriteString(line + "\n")
		}
	}

	nSel := len(m.destHostSelected)
	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render(
		fmt.Sprintf("↑↓/jk navigate  space toggle  enter confirm (%d selected)  esc back", nSel),
	))
	return sb.String()
}

// renderCopyModeStep renders the content for step 3 (copy mode selection).
// The user navigates two options with ↑/↓ (or j/k) and confirms with Enter.
func (m DistributeModel) renderCopyModeStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render("Step 4 of 6: Choose Copy Mode"))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render("Select how to distribute the file to destination hosts:"))
	sb.WriteString("\n\n")

	for i, item := range copyModeItems {
		isCursor := i == m.copyModeCursor

		var labelLine string
		switch {
		case isCursor:
			labelLine = cursorStyle.Render("▶ " + item.label)
		default:
			labelLine = bodyStyle.Render("  " + item.label)
		}
		sb.WriteString(labelLine + "\n")

		// Render description indented under the label.
		var descLine string
		if isCursor {
			descLine = selectedStyle.Render("    " + item.description)
		} else {
			descLine = descStyle.Render("    " + item.description)
		}
		sb.WriteString(descLine + "\n\n")
	}

	sb.WriteString(hintStyle.Render("↑↓/jk navigate  enter select  esc back"))
	return sb.String()
}


// renderConfirmStep renders the content for DistributeStepConfirm (step 5).
//
// The view shows a full summary of the pending operation so the user can
// review before committing:
//   - Source host and file path(s)
//   - Destination hosts
//   - Copy mode
//   - Destination path (or "(same as source)" when empty)
//   - A prominent warning when hub-and-spoke mode is selected (SSH access requirement)
//   - Checksum verification checkbox (toggled with Space)
func (m DistributeModel) renderConfirmStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	checkActiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render("Step 5 of 6: Confirm Distribution"))
	sb.WriteString("\n\n")

	// Source ---------------------------------------------------------------
	srcHost := m.sourceHost
	if srcHost == "" {
		srcHost = "local"
	}
	srcPath := m.sourcePath
	if srcPath == "" && len(m.sourcePaths) > 0 {
		srcPath = strings.Join(m.sourcePaths, ", ")
	}
	if srcPath == "" {
		srcPath = "(no file selected)"
	}
	sb.WriteString(labelStyle.Render("Source:       "))
	sb.WriteString(bodyStyle.Render(srcHost + ":" + srcPath))
	sb.WriteString("\n")

	// Destinations ---------------------------------------------------------
	destNames := make([]string, 0, len(m.destHosts))
	for _, h := range m.destHosts {
		destNames = append(destNames, h.DisplayName)
	}
	destStr := strings.Join(destNames, ", ")
	if destStr == "" {
		destStr = "(none selected)"
	}
	sb.WriteString(labelStyle.Render("Destinations: "))
	sb.WriteString(bodyStyle.Render(destStr))
	sb.WriteString("\n")

	// Copy mode ------------------------------------------------------------
	mode := m.copyMode
	if mode == "" {
		mode = "direct parallel (default)"
	}
	sb.WriteString(labelStyle.Render("Copy mode:    "))
	sb.WriteString(bodyStyle.Render(mode))
	sb.WriteString("\n")

	// Destination path -----------------------------------------------------
	dstPath := m.destPath
	if dstPath == "" {
		dstPath = "(same as source)"
	}
	sb.WriteString(labelStyle.Render("Dest path:    "))
	sb.WriteString(bodyStyle.Render(dstPath))
	sb.WriteString("\n\n")

	// Hub-and-spoke warning ------------------------------------------------
	// When hub-and-spoke mode is selected, render a prominent warning box
	// reminding the user that the hub host must have internal SSH (SCP)
	// access to all destination hosts.  This is a hard requirement: without
	// it the fan-out transfers from the hub will fail.
	if m.copyMode == "hub-spoke" {
		sb.WriteString(renderHubSpokeWarning())
		sb.WriteString("\n\n")
	}

	// Checksum verification checkbox ---------------------------------------
	check := "[ ]"
	var checkLine string
	if m.verifyChecksum {
		check = "[✓]"
		checkLine = checkActiveStyle.Render(check + " Verify checksum after copy")
	} else {
		checkLine = bodyStyle.Render(check + " Verify checksum after copy")
	}
	sb.WriteString(checkLine)
	sb.WriteString("\n\n")

	sb.WriteString(hintStyle.Render("space toggle checksum  enter confirm  esc back"))
	return sb.String()
}

// renderHubSpokeWarning returns a prominently styled warning block that
// reminds the user of the internal SSH access requirement for hub-and-spoke
// mode.
//
// In hub-and-spoke mode, smux copies the file to a designated hub host and
// then the hub SSHes into each remaining destination host to fan the file out.
// This requires the hub to have passwordless SSH (SCP) access to every
// destination host on the internal network.  If that access is not already
// configured, the operation will fail at fan-out time.
//
// The warning is rendered as styled text lines (not a nested box) to avoid
// wrapping artifacts when embedded inside the outer step-content box.
func renderHubSpokeWarning() string {
	warnTitleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	warnBodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	lines := []string{
		warnTitleStyle.Render("⚠  Hub-and-spoke: internal SSH access required"),
		warnBodyStyle.Render("   The hub host must reach every destination via SSH (SCP)."),
		warnBodyStyle.Render("   smux installs a temporary keypair on the hub, but the hub"),
		warnBodyStyle.Render("   must already have access to each host on the internal network."),
		warnBodyStyle.Render("   Fan-out will fail if internal network access is unavailable."),
	}
	return strings.Join(lines, "\n")
}

// renderRetryConfirmStep renders the content for DistributeStepRetryConfirm.
//
// It presents the recovered distribution parameters (source, failed
// destination hosts, copy mode, and destination path) to the user so they can
// decide whether to proceed with the retry.  The parameters are drawn from
// m.retryParams (populated by NewRetryDistributeModel).
//
// The user confirms by pressing Enter or 'y', or declines with 'n' or Esc.
func (m DistributeModel) renderRetryConfirmStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	warnTitleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render("Retry Failed Distribution"))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render(
		"The following parameters were recovered from the previous operation.\n" +
			"Only the hosts that failed will be retried."))
	sb.WriteString("\n\n")

	if m.retryParams == nil {
		sb.WriteString(warnTitleStyle.Render("⚠  No retry parameters available."))
		sb.WriteString("\n")
		return sb.String()
	}

	p := m.retryParams

	// Source ---------------------------------------------------------------
	srcHost := p.SourceHost.Host
	if srcHost == "" {
		srcHost = "local"
	}
	srcPath := p.SourcePath
	if srcPath == "" {
		srcPath = "(unknown)"
	}
	sb.WriteString(labelStyle.Render("Source:       "))
	sb.WriteString(bodyStyle.Render(srcHost + ":" + srcPath))
	sb.WriteString("\n")

	// Copy mode ------------------------------------------------------------
	mode := p.CopyMode
	if mode == "" {
		mode = "parallel (default)"
	}
	sb.WriteString(labelStyle.Render("Copy mode:    "))
	sb.WriteString(bodyStyle.Render(mode))
	sb.WriteString("\n")

	// Destination path -----------------------------------------------------
	dstPath := p.DestPath
	if dstPath == "" {
		dstPath = "(same as source)"
	}
	sb.WriteString(labelStyle.Render("Dest path:    "))
	sb.WriteString(bodyStyle.Render(dstPath))
	sb.WriteString("\n\n")

	// Failed hosts ---------------------------------------------------------
	sb.WriteString(labelStyle.Render(fmt.Sprintf("Retry targets (%d host(s) failed):", len(p.FailedHosts))))
	sb.WriteString("\n")
	if len(p.FailedHosts) == 0 {
		sb.WriteString(bodyStyle.Render("  (no failed hosts — nothing to retry)"))
		sb.WriteString("\n")
	} else {
		for _, h := range p.FailedHosts {
			addr := h.Host
			if h.User != "" {
				addr = h.User + "@" + addr
			}
			if h.Port != 0 {
				addr = fmt.Sprintf("%s:%d", addr, h.Port)
			}
			sb.WriteString(bodyStyle.Render("  • " + addr))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("y/enter proceed with retry  n/esc cancel"))
	return sb.String()
}

// renderWizardStepContent is a shared helper that formats a wizard step with a
// bold title, body text, and a dimmed hint line.
func renderWizardStepContent(title, body, hint string) string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	return strings.Join([]string{
		headStyle.Render(title),
		"",
		bodyStyle.Render(body),
		"",
		hintStyle.Render(hint),
	}, "\n")
}
