package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	// DistributeStepHubSelect is the fifth step in hub-and-spoke mode: choose
	// which destination host acts as the hub.  This step is inserted between
	// CopyMode and DestPath only when the user selects hub-and-spoke; it is
	// completely absent from the direct-parallel flow.
	DistributeStepHubSelect
	// DistributeStepDestPath is the fifth step (direct-parallel) or sixth step
	// (hub-and-spoke): enter the destination path on the target hosts.  The
	// user must supply a non-empty path; blank input is rejected with a
	// re-prompt.
	DistributeStepDestPath
	// DistributeStepConfirm is the sixth step: review full copy details and
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
// breadcrumb for direct-parallel mode (Source, Browse, Destinations, CopyMode,
// DestPath, Confirm, Execute = 7 steps).
//
// NOTE: This constant is intentionally NOT derived from int(DistributeStepExecute)+1
// because the enum also contains DistributeStepHubSelect (which only appears in
// hub-and-spoke mode).  Deriving it from the enum would give 8, not 7.
const distributeStepCount = 7

// distributeStepCountFor returns the total number of wizard steps for the
// given copy mode.  Hub-and-spoke mode adds one extra step (hub node
// selection) between copy-mode selection and destination-path entry, so it
// has 8 steps.  Direct-parallel (and any unrecognised / not-yet-chosen mode)
// uses 7 steps.
func distributeStepCountFor(copyMode string) int {
	if copyMode == "hub-spoke" {
		return distributeStepCount + 1 // 8 steps
	}
	return distributeStepCount // 7 steps
}

// distributeStepLabels lists the display names for all seven direct-parallel
// wizard steps in order.  It exists primarily so tests can assert that every
// label appears in the rendered view.
var distributeStepLabels = [distributeStepCount]string{
	"Select Source",
	"Browse Files",
	"Select Destinations",
	"Choose Copy Mode",
	"Destination Path",
	"Confirm",
	"Execute",
}

// distributeStepLabel maps each DistributeStep enum value (including the
// hub-and-spoke-only DistributeStepHubSelect) to its display name.  This is
// the canonical label source used by the dynamic breadcrumb renderer.
var distributeStepLabel = map[DistributeStep]string{
	DistributeStepSourceSelect: "Select Source",
	DistributeStepFileBrowse:   "Browse Files",
	DistributeStepDestHosts:    "Select Destinations",
	DistributeStepCopyMode:     "Choose Copy Mode",
	DistributeStepHubSelect:    "Select Hub",
	DistributeStepDestPath:     "Destination Path",
	DistributeStepConfirm:      "Confirm",
	DistributeStepExecute:      "Execute",
}

// directParallelSteps is the ordered slice of wizard steps for direct-parallel
// copy mode (7 steps, no hub-selection step).
var directParallelSteps = []DistributeStep{
	DistributeStepSourceSelect,
	DistributeStepFileBrowse,
	DistributeStepDestHosts,
	DistributeStepCopyMode,
	DistributeStepDestPath,
	DistributeStepConfirm,
	DistributeStepExecute,
}

// hubSpokeSteps is the ordered slice of wizard steps for hub-and-spoke copy
// mode (8 steps, includes DistributeStepHubSelect between CopyMode and DestPath).
var hubSpokeSteps = []DistributeStep{
	DistributeStepSourceSelect,
	DistributeStepFileBrowse,
	DistributeStepDestHosts,
	DistributeStepCopyMode,
	DistributeStepHubSelect,
	DistributeStepDestPath,
	DistributeStepConfirm,
	DistributeStepExecute,
}

// visibleWizardSteps returns the ordered slice of DistributeStep values that
// make up the wizard for the currently-selected copy mode.
//
//   - Direct-parallel (or not-yet-chosen): 7 steps — DistributeStepHubSelect
//     is omitted.
//   - Hub-and-spoke: 8 steps — DistributeStepHubSelect is inserted between
//     DistributeStepCopyMode and DistributeStepDestPath.
func (m DistributeModel) visibleWizardSteps() []DistributeStep {
	if m.copyMode == "hub-spoke" {
		return hubSpokeSteps
	}
	return directParallelSteps
}

// totalSteps returns the total number of wizard steps for this model, based
// on the currently selected copy mode.
func (m DistributeModel) totalSteps() int {
	return len(m.visibleWizardSteps())
}

// stepIndex returns the 0-based index of the current step (m.step) within the
// active step sequence (visibleWizardSteps).  This is the canonical stepIndex
// used by step headers: N = stepIndex()+1.
//
// Because visibleWizardSteps() is the single source of truth for step ordering,
// stepIndex() must always be derived from it — never stored or computed
// independently.  Breadcrumb and header renderers must use stepIndex()+1 for N
// and totalSteps() for M, so that both values track any runtime change to the
// step sequence (e.g. switching from direct-parallel to hub-and-spoke).
func (m DistributeModel) stepIndex() int {
	for i, s := range m.visibleWizardSteps() {
		if s == m.step {
			return i
		}
	}
	// Fallback: should not be reached for any normal wizard step.
	return int(m.step)
}

// displayStepIndex returns the 1-based display step index for the given
// logical step, taking copy mode into account.
//
// In direct-parallel mode the DistributeStepHubSelect enum value is not
// visited, so steps after it must be shifted down by one to yield a
// contiguous 1-based display sequence.  In hub-and-spoke mode all steps
// appear in natural order.
func (m DistributeModel) displayStepIndex(step DistributeStep) int {
	for i, s := range m.visibleWizardSteps() {
		if s == step {
			return i + 1
		}
	}
	// Fallback (should not be reached for any normal wizard step).
	return int(step) + 1
}

// currentStepName returns the display name for the current wizard step by
// looking up m.step in the distributeStepLabel map.  This is semantically
// equivalent to stepSequence[stepIndex] because visibleWizardSteps()[i] is
// always the same DistributeStep enum value as m.step when step i is active.
// Using this method ensures that every step renderer derives its title from the
// single step-sequence/label source of truth rather than from independent
// hardcoded strings.
func (m DistributeModel) currentStepName() string {
	if label, ok := distributeStepLabel[m.step]; ok {
		return label
	}
	// Fallback for unknown steps (should not be reached in normal usage).
	return fmt.Sprintf("Step %d", int(m.step)+1)
}

// buildSourceFlatList builds the visible flat node list for the source-origin
// picker.  The "Local (this machine)" entry is always first (NodeKindLocal),
// followed by the cluster tree produced by BuildFlatList.  When a filter is
// active, "local" is included only if the filter fuzzy-matches "local".
func buildSourceFlatList(cfg *config.Config, state *TreeState, filter string) []TreeNode {
	var nodes []TreeNode
	if filter == "" || fuzzyMatch(filter, "local") {
		nodes = append(nodes, TreeNode{Kind: NodeKindLocal})
	}
	nodes = append(nodes, BuildFlatList(cfg, state, filter)...)
	return nodes
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
	destPath   string               // destination path on each target host (step 4); must be non-empty

	// Step 4 (DistributeStepConfirm) state.
	// verifyChecksum controls whether a checksum is computed and verified
	// after each transfer.  Toggled with Space in DistributeStepConfirm.
	// Preserved on back-navigation.
	verifyChecksum bool

	// Step 0: source origin selector state.
	// sourceTree tracks expanded/collapsed state per cluster.
	// sourceFlatNodes is the visible node list (Local entry + cluster tree).
	// sourceOriginCursor is the highlighted row index into sourceFlatNodes.
	// sourceViewport tracks the scroll offset.
	// sourceFilterInput / sourceFilterActive handle the '/' fuzzy filter.
	// All are preserved on back-navigation.
	sourceTree         TreeState
	sourceFlatNodes    []TreeNode
	sourceOriginCursor int
	sourceViewport     viewport.Model
	sourceFilterInput  textinput.Model
	sourceFilterActive bool

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
	// destHostItems is the flat list of all resolved hosts from the config
	// (kept for selection collection on Enter).
	// destTree tracks expanded/collapsed state per cluster.
	// destFlatNodes is the visible node list rebuilt via BuildFlatList.
	// destHostCursor is the highlighted row index into destFlatNodes.
	// destHostSelected tracks which hosts are currently checked (keyed by
	// host.Host SSH alias; a key is present iff the host is selected).
	// destViewport tracks the scroll offset for the host list.
	// destFilterInput is the fuzzy-filter text input (activated by '/').
	// destFilterActive is true when the filter input is focused.
	destHostItems      []config.ResolvedHost
	destTree           TreeState
	destFlatNodes      []TreeNode
	destHostCursor     int
	destHostSelected   map[string]bool
	destViewport       viewport.Model
	destFilterInput    textinput.Model
	destFilterActive   bool

	// Step 3: copy mode selector state.
	// copyModeCursor is the index of the highlighted copy mode option
	// (0 = direct parallel, 1 = hub-and-spoke).  Preserved on back-navigation.
	copyModeCursor int

	// Step 4 (hub-and-spoke only): hub node selector state.
	// hubHost is the resolved host selected as the hub.  Set when the user
	// presses Enter in DistributeStepHubSelect.  Cleared when the user backs
	// up past this step and the copy mode changes.
	// hubCursor is the highlighted row index in the hub selector list (indexes
	// into m.destHosts).  Preserved on back-navigation.
	hubHost   config.ResolvedHost
	hubCursor int

	// Step 4 (DistributeStepDestPath) state.
	// destPathInput is the text input for the destination path.
	// destPathErr holds a non-empty error message when the user submitted a
	// blank or whitespace-only path; cleared on the next valid submission.
	destPathInput textinput.Model
	destPathErr   string

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

	// hostErrors stores the trimmed failure reason for each host that failed,
	// keyed by host.Host. Populated by handleProgressUpdate on TransferFailed.
	hostErrors map[string]string

	// progressCursor is the row index of the highlighted host in the execute
	// step's progress list. j/k move it; Enter opens the error overlay.
	progressCursor int

	// errorOverlay is non-nil when the full-error overlay is open. It holds
	// the complete error text for the host selected by progressCursor.
	// Set by handleExecuteKey on Enter; cleared by the global Esc handler.
	errorOverlay *string

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
	m := DistributeModel{
		cfg:    cfg,
		width:  width,
		height: height,
		step:   DistributeStepSourceSelect,

		// Step 0: source-origin tree; cursor starts at Local (index 0).
		sourceTree:         NewTreeState(cfg.ClusterNames()),
		sourceOriginCursor: 0,
		sourceViewport:     newSourceViewport(width, height),
		sourceFilterInput:  newDestFilterInput(),

		// Step 2: initialise destination host list with tree state.
		destHostItems:    cfg.AllResolvedHosts(),
		destTree:         NewTreeState(cfg.ClusterNames()),

		// Step 4: destination path text input.
		destPathInput: newDestPathInput(),
		destHostCursor:   0,
		destHostSelected: make(map[string]bool),
		destViewport:     newDestViewport(width, height),
		destFilterInput:  newDestFilterInput(),
	}
	m.rebuildSourceFlat()
	m.clampSourceViewport()
	m.rebuildDestFlat()
	m.clampDestViewport()
	return m
}

// newDestViewport creates a viewport.Model sized for the dest-host picker.
// It reserves lines for all chrome surrounding the scrollable host list.
//
// Overhead breakdown (counted from View → renderStepContent → renderDestHostsStep):
//
//   Above the host list:
//     1  title line
//     1  blank (title trailing \n\n)
//     1  breadcrumb line
//     1  blank (breadcrumb trailing \n\n)
//     1  box top border
//     1  box top padding (Padding(1,3))
//     1  step header ("Step 3 of N: …")
//     1  filter / blank line
//   Below the host list:
//     1  blank line after host lines
//     1  "N selected" line
//     1  step hint line
//     1  box bottom padding
//     1  box bottom border
//     1  blank (View footer leading \n\n, first)
//     1  blank (View footer leading \n\n, second)
//     1  footer hint line
//
// Total: 16 lines of overhead.
func newDestViewport(width, height int) viewport.Model {
	const destViewportOverhead = 16
	vpHeight := height - destViewportOverhead
	if vpHeight < 1 {
		vpHeight = 1
	}
	return viewport.New(width, vpHeight)
}

// newSourceViewport creates a viewport.Model sized for the source-origin picker.
//
// Overhead breakdown (title+blank+breadcrumb+blank+box-borders+padding = 8,
// inner: step-header+blank+body+blank = 4 above, blank+hint = 2 below,
// footer \n\n+line = 3): total 17 lines.
func newSourceViewport(width, height int) viewport.Model {
	const sourceViewportOverhead = 17
	vpHeight := height - sourceViewportOverhead
	if vpHeight < 1 {
		vpHeight = 1
	}
	return viewport.New(width, vpHeight)
}

// newDestFilterInput creates a textinput.Model for the dest-host filter.
func newDestFilterInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 128
	return ti
}

// newDestPathInput creates a textinput.Model for the destination path entry.
func newDestPathInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "/path/on/target"
	ti.CharLimit = 512
	return ti
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

		sourceTree:         NewTreeState(cfg.ClusterNames()),
		sourceViewport:     newSourceViewport(width, height),
		sourceFilterInput:  newDestFilterInput(),
		destHostItems:      cfg.AllResolvedHosts(),
		destTree:           NewTreeState(cfg.ClusterNames()),
		destHostSelected:   make(map[string]bool),
		destViewport:       newDestViewport(width, height),
		destFilterInput:    newDestFilterInput(),
		destPathInput:      newDestPathInput(),
	}
	m.rebuildSourceFlat()
	m.rebuildDestFlat()
	m.clampDestViewport()
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

// DestPath returns the destination filesystem path configured in
// DistributeStepDestPath.  The user must always supply a non-empty path; blank
// input is rejected by handleDestPathKey.  Valid after the dest-path step is
// confirmed with Enter.
func (m DistributeModel) DestPath() string { return m.destPath }

// HubHost returns the hub host selected in DistributeStepHubSelect.
// Only meaningful when CopyMode() == "hub-spoke".  Returns a zero-value
// ResolvedHost before the hub-selection step has been confirmed.
func (m DistributeModel) HubHost() config.ResolvedHost { return m.hubHost }

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
		// Update viewport dimensions on resize.
		m.sourceViewport = newSourceViewport(msg.Width, msg.Height)
		m.clampSourceViewport()
		m.destViewport = newDestViewport(msg.Width, msg.Height)
		m.clampDestViewport()
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

	// Forward non-key messages (e.g. cursor blink) to whichever filter input
	// is currently focused so the blinking cursor renders correctly.
	if m.sourceFilterActive && m.step == DistributeStepSourceSelect {
		var cmd tea.Cmd
		m.sourceFilterInput, cmd = m.sourceFilterInput.Update(msg)
		return m, cmd
	}
	if m.destFilterActive && m.step == DistributeStepDestHosts {
		var cmd tea.Cmd
		m.destFilterInput, cmd = m.destFilterInput.Update(msg)
		return m, cmd
	}
	if m.step == DistributeStepDestPath {
		var cmd tea.Cmd
		m.destPathInput, cmd = m.destPathInput.Update(msg)
		return m, cmd
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

	if u.Status == executor.TransferFailed {
		if m.hostErrors == nil {
			m.hostErrors = make(map[string]string)
		}
		reason := strings.TrimSpace(u.Stderr)
		if reason == "" && u.Err != nil {
			reason = u.Err.Error()
		}
		if reason == "" {
			reason = "(unknown error)"
		}
		m.hostErrors[u.Host.Host] = reason
	}

	// Continue listening for the next update.
	return m, waitForProgress(m.progressCh)
}

// handleKey is the central key dispatcher for the wizard.
//
// Global keys (q, Ctrl+C, Esc) are processed first; all remaining keys are
// forwarded to the step-specific handler.
//
// Exception: when a step's filter is active, q and Esc are NOT intercepted
// globally — they are routed to the step-specific handler so that q types
// into the filter input and Esc closes the filter.
func (m DistributeModel) handleKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	if m.sourceFilterActive && m.step == DistributeStepSourceSelect {
		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			m.done = true
			return m, tea.Quit
		default:
			return m.handleSourceOriginKey(msg)
		}
	}
	// When the dest-host filter input is focused, bypass the global q/Esc
	// intercept so those keys are handled by the step-specific handler.
	if m.destFilterActive && m.step == DistributeStepDestHosts {
		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			m.done = true
			return m, tea.Quit
		default:
			return m.handleDestHostsKey(msg)
		}
	}

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
		// Priority 1: dismiss the error overlay if it is open.
		if m.errorOverlay != nil {
			m.errorOverlay = nil
			return m, nil
		}
		// Priority 2: return to main host list when execution has finished.
		if m.step == DistributeStepExecute && m.executeDone {
			m.exitToMain = true
			m.done = true
			return m, nil
		}
		// Default: step back through the wizard.
		// DistributeStepRetryConfirm is a terminal entry-point; stepping back
		// from it always returns to the normal TUI rather than going to the
		// previous numeric step (which would be an unrelated wizard step).
		if m.step > 0 && m.step != DistributeStepRetryConfirm {
			m.step--
			// In direct-parallel mode the hub-selection step does not exist.
			// If stepping back lands on DistributeStepHubSelect but the current
			// copy mode is not hub-and-spoke, skip over it.
			if m.step == DistributeStepHubSelect && m.copyMode != "hub-spoke" {
				m.step--
			}
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
	case DistributeStepHubSelect:
		return m.handleHubSelectKey(msg)
	case DistributeStepDestPath:
		return m.handleDestPathKey(msg)
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
// (step 0).  Up/down (or j/k) move the cursor; Tab toggles cluster expand/
// collapse; '/' activates the fuzzy filter; Enter on a host or Local entry
// confirms the selection and advances to step 1 (DistributeStepFileBrowse).
func (m DistributeModel) handleSourceOriginKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	// Filter-active mode: most keys type into the filter input.
	if m.sourceFilterActive {
		switch msg.String() {
		case "esc":
			m.sourceFilterActive = false
			m.sourceFilterInput.SetValue("")
			m.sourceFilterInput.Blur()
			m.rebuildSourceFlat()
			m.clampSourceViewport()
			return m, nil
		case "enter":
			m.sourceFilterActive = false
			m.sourceFilterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.sourceFilterInput, cmd = m.sourceFilterInput.Update(msg)
			m.rebuildSourceFlat()
			// Clamp cursor to new list length.
			if m.sourceOriginCursor >= len(m.sourceFlatNodes) {
				if len(m.sourceFlatNodes) > 0 {
					m.sourceOriginCursor = len(m.sourceFlatNodes) - 1
				} else {
					m.sourceOriginCursor = 0
				}
			}
			m.clampSourceViewport()
			return m, cmd
		}
	}

	switch msg.String() {
	case "up", "k", "ctrl+p":
		if m.sourceOriginCursor > 0 {
			m.sourceOriginCursor--
			m.clampSourceViewport()
		}
	case "down", "j", "ctrl+n":
		if m.sourceOriginCursor < len(m.sourceFlatNodes)-1 {
			m.sourceOriginCursor++
			m.clampSourceViewport()
		}
	case "tab", "right", "l":
		if m.sourceOriginCursor < len(m.sourceFlatNodes) {
			n := m.sourceFlatNodes[m.sourceOriginCursor]
			if n.IsCluster() {
				m.sourceTree.Toggle(n.ClusterName)
				m.rebuildSourceFlat()
				m.clampSourceViewport()
			}
		}
	case "left", "h":
		if m.sourceOriginCursor < len(m.sourceFlatNodes) {
			n := m.sourceFlatNodes[m.sourceOriginCursor]
			if n.IsCluster() && m.sourceTree.IsExpanded(n.ClusterName) {
				m.sourceTree.SetExpanded(n.ClusterName, false)
				m.rebuildSourceFlat()
				m.clampSourceViewport()
			} else if m.sourceOriginCursor > 0 {
				m.sourceOriginCursor--
				m.clampSourceViewport()
			}
		}
	case "/":
		m.sourceFilterActive = true
		m.sourceFilterInput.Focus()
		return m, textinput.Blink
	case "enter":
		if m.sourceOriginCursor >= len(m.sourceFlatNodes) {
			return m, nil
		}
		n := m.sourceFlatNodes[m.sourceOriginCursor]
		if n.IsCluster() {
			// Enter on a cluster header toggles expand/collapse.
			m.sourceTree.Toggle(n.ClusterName)
			m.rebuildSourceFlat()
			m.clampSourceViewport()
			return m, nil
		}
		// Local or host node: set sourceHost and advance.
		if n.IsLocal() {
			m.sourceHost = ""
		} else if n.IsHost() && n.Host != nil {
			m.sourceHost = n.Host.Host
		}

		// Initialise (or reuse) the appropriate file-tree browser.
		var initCmd tea.Cmd
		if m.sourceHost == "" {
			if m.localFileTree == nil {
				ft := NewFileTreeModel("")
				ft.width = m.width
				ft.height = m.height
				m.localFileTree = &ft
			}
		} else {
			if m.remoteFileTree == nil || m.remoteTreeForHost != m.sourceHost {
				for _, h := range m.destHostItems {
					if h.Host == m.sourceHost {
						rft := NewRemoteFileTreeModel(h)
						rft.width = m.width
						rft.height = m.height
						m.remoteFileTree = &rft
						m.remoteTreeForHost = m.sourceHost
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
			m.rebuildDestFlat()
			m.clampDestViewport()
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
			m.rebuildDestFlat()
			m.clampDestViewport()
			return m, nil
		}
		// Pass through any fetch commands (e.g. lazy directory loads).
		return m, cmd
	}

	// No active tree (shouldn't normally happen); no-op.
	return m, nil
}

// rebuildDestFlat rebuilds destFlatNodes from the config using the current
// filter and tree expansion state, clamping the cursor if the list shrank.
func (m *DistributeModel) rebuildDestFlat() {
	m.destFlatNodes = BuildFlatList(m.cfg, &m.destTree, m.destFilterInput.Value())
	if m.destHostCursor >= len(m.destFlatNodes) {
		m.destHostCursor = max(0, len(m.destFlatNodes)-1)
	}
}

// clampDestViewport ensures the cursor is visible within the viewport window.
// It also caps YOffset so short lists render without blank space.
func (m *DistributeModel) clampDestViewport() {
	ClampViewport(&m.destViewport, m.destHostCursor, len(m.destFlatNodes))
}

func (m *DistributeModel) rebuildSourceFlat() {
	m.sourceFlatNodes = buildSourceFlatList(m.cfg, &m.sourceTree, m.sourceFilterInput.Value())
}

func (m *DistributeModel) clampSourceViewport() {
	ClampViewport(&m.sourceViewport, m.sourceOriginCursor, len(m.sourceFlatNodes))
}

// destToggleExpand toggles expansion of the cluster under the cursor.
func (m *DistributeModel) destToggleExpand() {
	if m.destHostCursor >= len(m.destFlatNodes) {
		return
	}
	n := m.destFlatNodes[m.destHostCursor]
	if n.IsCluster() {
		m.destTree.Toggle(n.ClusterName)
		m.rebuildDestFlat()
		m.clampDestViewport()
	}
}

// destCollapseOrMoveUp collapses the cluster at the cursor when it is expanded,
// or moves the cursor up to the parent cluster header when on a host node.
func (m *DistributeModel) destCollapseOrMoveUp() {
	if m.destHostCursor >= len(m.destFlatNodes) {
		return
	}
	n := m.destFlatNodes[m.destHostCursor]
	if n.IsCluster() && m.destTree.IsExpanded(n.ClusterName) {
		m.destTree.SetExpanded(n.ClusterName, false)
		m.rebuildDestFlat()
		m.clampDestViewport()
		return
	}
	// On a host node or collapsed cluster: move cursor up.
	if m.destHostCursor > 0 {
		m.destHostCursor--
		m.clampDestViewport()
	}
}

// destToggleSelect toggles selection for the node under the cursor.
// For a host node, toggles that host. For a cluster node, toggles all hosts
// in that cluster.
func (m *DistributeModel) destToggleSelect() {
	if m.destHostCursor >= len(m.destFlatNodes) {
		return
	}
	n := m.destFlatNodes[m.destHostCursor]
	if n.IsHost() && n.Host != nil {
		key := n.Host.Host
		if m.destHostSelected[key] {
			delete(m.destHostSelected, key)
		} else {
			m.destHostSelected[key] = true
		}
	} else if n.IsCluster() {
		// Toggle all hosts in this cluster: if all are selected, deselect all;
		// otherwise select all.
		cluster := m.cfg.Clusters[n.ClusterName]
		allSelected := true
		for _, h := range cluster.Hosts {
			r := h.Resolve(n.ClusterName, cluster.Defaults)
			if !m.destHostSelected[r.Host] {
				allSelected = false
				break
			}
		}
		for _, h := range cluster.Hosts {
			r := h.Resolve(n.ClusterName, cluster.Defaults)
			if allSelected {
				delete(m.destHostSelected, r.Host)
			} else {
				m.destHostSelected[r.Host] = true
			}
		}
	}
}

// handleDestHostsKey handles navigation and selection in the destination host
// list (step 2).  Up/down (or j/k) move the cursor; Space toggles the host
// under the cursor; Tab/right expands clusters; left collapses; '/' activates
// filter; Enter confirms the selection and advances to copy-mode step.
func (m DistributeModel) handleDestHostsKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	// If filter is active, delegate most keys to the text input.
	if m.destFilterActive {
		switch msg.String() {
		case "esc":
			// Esc discards the filter text and exits filter mode,
			// matching the main SSH host list view behavior.
			m.destFilterActive = false
			m.destFilterInput.SetValue("")
			m.destFilterInput.Blur()
			m.rebuildDestFlat()
			m.clampDestViewport()
			return m, nil
		case "enter":
			// Enter commits the current filter text and exits filter mode.
			// The filter remains applied until cleared by Esc or '/'.
			m.destFilterActive = false
			m.destFilterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.destFilterInput, cmd = m.destFilterInput.Update(msg)
			m.rebuildDestFlat()
			m.clampDestViewport()
			return m, cmd
		}
	}

	switch msg.String() {
	case "up", "k", "ctrl+p":
		if m.destHostCursor > 0 {
			m.destHostCursor--
			m.clampDestViewport()
		}
	case "down", "j", "ctrl+n":
		if m.destHostCursor < len(m.destFlatNodes)-1 {
			m.destHostCursor++
			m.clampDestViewport()
		}
	case " ":
		m.destToggleSelect()
	case "tab", "right", "l":
		m.destToggleExpand()
	case "left", "h":
		m.destCollapseOrMoveUp()
	case "/":
		m.destFilterActive = true
		m.destFilterInput.Focus()
		return m, textinput.Blink
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
		// Persist the highlighted option.
		if m.copyModeCursor < len(copyModeItems) {
			m.copyMode = copyModeItems[m.copyModeCursor].value
		}
		// Route to the hub-selection step for hub-and-spoke, or skip straight
		// to destination-path entry for direct-parallel.
		if m.copyMode == "hub-spoke" {
			m.step = DistributeStepHubSelect
			// Reset hub cursor so the user starts at the top of the list.
			m.hubCursor = 0
		} else {
			m.step = DistributeStepDestPath
			// Focus the destination path input so the user can type immediately.
			m.destPathInput.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

// handleHubSelectKey handles key input during DistributeStepHubSelect.
//
// This step is only reached in hub-and-spoke mode.  The user picks one of the
// destination hosts (from m.destHosts) as the hub node.  Up/down (or j/k) move
// the cursor; Enter confirms the selection and advances to DistributeStepDestPath.
//
// Advancing without a selection (empty destHosts or cursor out of range) is
// blocked.  Esc is consumed by the global handleKey dispatcher before arriving
// here.
func (m DistributeModel) handleHubSelectKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.hubCursor > 0 {
			m.hubCursor--
		}
	case "down", "j":
		if m.hubCursor < len(m.destHosts)-1 {
			m.hubCursor++
		}
	case "enter":
		// Block advancement if no destination hosts are available.
		if len(m.destHosts) == 0 {
			return m, nil
		}
		// Clamp cursor to valid range (defensive).
		if m.hubCursor >= len(m.destHosts) {
			m.hubCursor = len(m.destHosts) - 1
		}
		// Persist the selected hub and advance to destination-path entry.
		m.hubHost = m.destHosts[m.hubCursor]
		m.step = DistributeStepDestPath
		m.destPathInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

// handleDestPathKey handles key input during DistributeStepDestPath (step 4).
//
// The user types a destination path in the text input and presses Enter to
// confirm.  Blank or whitespace-only input is rejected with an inline error
// message; any non-empty path is accepted, persisted in m.destPath, and the
// wizard advances to DistributeStepConfirm.
// (Esc and q/Ctrl+C are already consumed by the global handler and never
// arrive here.)
func (m DistributeModel) handleDestPathKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := strings.TrimSpace(m.destPathInput.Value())
		if val == "" {
			// Reject blank input: show error and keep focus.
			m.destPathErr = "Destination path is required. Please enter a non-empty path."
			return m, nil
		}
		// Accept: persist the path and advance.
		m.destPath = val
		m.destPathErr = ""
		m.destPathInput.Blur()
		m.step = DistributeStepConfirm
		return m, nil
	default:
		// Forward all other keys to the text input.
		var cmd tea.Cmd
		m.destPathInput, cmd = m.destPathInput.Update(msg)
		m.destPathErr = ""
		return m, cmd
	}
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
// begun.  'r' retries only the failed hosts after execution completes by
// constructing a RetryParams from the current model state and returning a new
// DistributeModel positioned at DistributeStepRetryConfirm.  Esc and
// q/Ctrl+C are already consumed by the global handler.
// handleExecuteKey handles key input during DistributeStepExecute.
//
// Enter starts the transfer (first press) or opens the error overlay for the
// selected failed host (when execution is done).  j/k move the cursor through
// the destination host list.  'r' retries failed hosts after execution
// completes.  Esc and q/Ctrl+C are consumed by the global handleKey
// dispatcher and never arrive here.
func (m DistributeModel) handleExecuteKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if !m.executeStarted {
			// Launch the transfer goroutine and begin progress tracking.
			cmd := m.startExecution()
			return m, cmd
		}
		// Open error overlay for the selected host if it failed.
		if m.executeDone && m.errorOverlay == nil && len(m.destHosts) > 0 {
			selected := m.destHosts[m.progressCursor]
			if m.hostProgress[selected.Host] == executor.TransferFailed {
				reason := "(no details available)"
				if m.hostErrors != nil {
					if r, ok := m.hostErrors[selected.Host]; ok && r != "" {
						reason = r
					}
				}
				m.errorOverlay = &reason
			}
		}
	case "j", "down":
		if len(m.destHosts) > 0 && m.progressCursor < len(m.destHosts)-1 {
			m.progressCursor++
		}
	case "k", "up":
		if m.progressCursor > 0 {
			m.progressCursor--
		}
	case "r":
		// Only allow retry after execution has fully completed and there are
		// failed hosts to retry.
		if m.executeDone {
			failed := m.failedHosts()
			if len(failed) > 0 {
				srcPath := ""
				if len(m.sourcePaths) > 0 {
					srcPath = m.sourcePaths[0]
				}
				// When this model was itself created by a retry (m.retryParams !=
				// nil), preserve the original AllHosts from the parent retry params
				// so that hub-first ordering remains correct across multiple retries.
				allHosts := append([]config.ResolvedHost(nil), m.destHosts...)
				if m.retryParams != nil && len(m.retryParams.AllHosts) > 0 {
					allHosts = append([]config.ResolvedHost(nil), m.retryParams.AllHosts...)
				}
				params := executor.RetryParams{
					SourceHost:  m.resolvedSourceHost(),
					SourcePath:  srcPath,
					DestPath:    m.effectiveDestPath(),
					CopyMode:    m.copyMode,
					FailedHosts: failed,
					AllHosts:    allHosts,
				}
				retryModel := NewRetryDistributeModel(m.cfg, m.width, m.height, params)
				// Preserve the checksum verification preference so that the
				// user does not need to re-toggle it for every retry round.
				retryModel.verifyChecksum = m.verifyChecksum
				return retryModel, nil
			}
		}
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
	case " ":
		// Toggle the checksum verification checkbox, mirroring the behaviour
		// of DistributeStepConfirm so the user can change the setting before
		// re-launching the retry.
		m.verifyChecksum = !m.verifyChecksum
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
//
// The set of steps shown is dynamic: direct-parallel mode shows 7 steps while
// hub-and-spoke mode shows 8 steps (including "Select Hub").  The step list is
// derived from visibleWizardSteps() so the breadcrumb always reflects the
// currently-selected copy mode.
func (m DistributeModel) renderStepBreadcrumb() string {
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var parts []string
	for _, step := range m.visibleWizardSteps() {
		label := distributeStepLabel[step]
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
	case DistributeStepHubSelect:
		inner = m.renderHubSelectStep()
	case DistributeStepDestPath:
		inner = m.renderDestPathStep()
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
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)
	clusterStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	localStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
	sb.WriteString("\n")
	sb.WriteString(bodyStyle.Render("Choose a Source File host:"))
	sb.WriteString("\n")

	// Filter line.
	if m.sourceFilterActive {
		sb.WriteString("/" + m.sourceFilterInput.View() + "\n")
	} else if m.sourceFilterInput.Value() != "" {
		sb.WriteString(filterStyle.Render("filter: "+m.sourceFilterInput.Value()) + "\n")
	} else {
		sb.WriteString("\n")
	}

	if len(m.sourceFlatNodes) == 0 {
		sb.WriteString(bodyStyle.Render("(no matches)"))
		sb.WriteString("\n")
	} else {
		lines := make([]string, len(m.sourceFlatNodes))
		for i, n := range m.sourceFlatNodes {
			isCursor := i == m.sourceOriginCursor
			switch {
			case n.IsLocal():
				text := "  Local (this machine)"
				if isCursor {
					lines[i] = cursorStyle.Render("▶" + text)
				} else {
					lines[i] = localStyle.Render(" " + text)
				}
			case n.IsCluster():
				arrow := "▸"
				if m.sourceTree.IsExpanded(n.ClusterName) {
					arrow = "▾"
				}
				text := fmt.Sprintf("%s %s", arrow, n.ClusterName)
				if isCursor {
					lines[i] = cursorStyle.Render("▶ " + text)
				} else {
					lines[i] = clusterStyle.Render("  " + text)
				}
			case n.IsHost() && n.Host != nil:
				text := fmt.Sprintf("    %s", n.Host.DisplayName)
				if isCursor {
					lines[i] = cursorStyle.Render("▶" + text)
				} else {
					lines[i] = bodyStyle.Render(" " + text)
				}
			}
		}
		start, end := VisibleRange(&m.sourceViewport, len(lines))
		for _, line := range lines[start:end] {
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("↑↓/jk navigate  tab fold  / filter  enter select  esc back"))
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
		fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName()),
		fmt.Sprintf("Browsing files on: %s\n\n"+
			"Navigate the file tree, select files with Space, and press Ctrl+D to confirm.", source),
		"↑↓/jk move  →/Enter expand  ←/h collapse  space select  . toggle hidden  Ctrl+D confirm  Esc back",
	)
}

// renderDestHostsStep renders the content for step 2: destination host
// selection.  The user navigates the list with up/down (or j/k), toggles
// individual hosts with Space, folds/unfolds clusters with Tab/arrows,
// filters with /, and presses Enter to confirm.  The list is rendered
// within a viewport to prevent terminal overflow.
func (m DistributeModel) renderDestHostsStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	clusterStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
	sb.WriteString("\n")
	sb.WriteString(bodyStyle.Render("Select Destination Hosts:"))
	sb.WriteString("\n")

	// Filter line.
	if m.destFilterActive {
		sb.WriteString("/" + m.destFilterInput.View() + "\n")
	} else if m.destFilterInput.Value() != "" {
		filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
		sb.WriteString(filterStyle.Render("filter: "+m.destFilterInput.Value()) + "\n")
	} else {
		sb.WriteString("\n")
	}

	if len(m.destFlatNodes) == 0 && m.destFilterInput.Value() == "" {
		sb.WriteString(bodyStyle.Render("(no hosts configured)"))
		sb.WriteString("\n")
	} else if len(m.destFlatNodes) > 0 {
		// Build all lines for the flat node list.
		lines := make([]string, len(m.destFlatNodes))
		for i, n := range m.destFlatNodes {
			isCursor := i == m.destHostCursor

			if n.IsCluster() {
				arrow := "▸"
				if m.destTree.IsExpanded(n.ClusterName) {
					arrow = "▾"
				}
				text := fmt.Sprintf("%s %s", arrow, n.ClusterName)
				if isCursor {
					lines[i] = cursorStyle.Render("▶ " + text)
				} else {
					lines[i] = clusterStyle.Render("  " + text)
				}
			} else if n.IsHost() && n.Host != nil {
				isSelected := m.destHostSelected[n.Host.Host]
				check := "[ ]"
				if isSelected {
					check = "[✓]"
				}
				text := fmt.Sprintf("  %s %s", check, n.Host.DisplayName)
				switch {
				case isCursor && isSelected:
					lines[i] = cursorStyle.Render("▶ " + text)
				case isCursor:
					lines[i] = cursorStyle.Render("▶ " + text)
				case isSelected:
					lines[i] = selectedStyle.Render("  " + text)
				default:
					lines[i] = bodyStyle.Render("  " + text)
				}
			}
		}

		// Apply viewport slicing to prevent terminal overflow.
		start, end := VisibleRange(&m.destViewport, len(lines))
		for _, line := range lines[start:end] {
			sb.WriteString(line + "\n")
		}
	}

	nSel := len(m.destHostSelected)
	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render(fmt.Sprintf("%d selected", nSel)))
	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render(
		"↑↓/jk navigate  space toggle  tab fold  / filter  enter confirm  esc back",
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
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
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


// renderHubSelectStep renders the content for DistributeStepHubSelect.
//
// The user selects one host from the destination list (m.destHosts) to act as
// the hub node in hub-and-spoke mode.  Every destination host is eligible; no
// additional filtering is applied.  The cursor is highlighted and Enter confirms
// the choice.
func (m DistributeModel) renderHubSelectStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s",
		m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render(
		"Select Hub Node.  smux will copy the file here first, then the hub\n" +
			"will fan it out to all other destination hosts via internal SSH."))
	sb.WriteString("\n\n")

	if len(m.destHosts) == 0 {
		sb.WriteString(bodyStyle.Render("(no destination hosts selected — go back and select at least one)"))
		sb.WriteString("\n")
	} else {
		for i, h := range m.destHosts {
			line := fmt.Sprintf("  %s", h.DisplayName)
			if i == m.hubCursor {
				sb.WriteString(cursorStyle.Render("▶ " + line))
			} else {
				sb.WriteString(bodyStyle.Render("  " + line))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("↑↓/jk navigate  enter select hub  esc back"))
	return sb.String()
}

// renderDestPathStep renders the content for DistributeStepDestPath (step 5).
//
// The user enters the destination path on the target hosts.  A non-empty path
// is required; blank input is rejected with an inline error message.
func (m DistributeModel) renderDestPathStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render("Enter the destination path on the target hosts."))
	sb.WriteString("\n")
	sb.WriteString(bodyStyle.Render("This path will be used for all selected destination hosts."))
	sb.WriteString("\n\n")

	sb.WriteString("> " + m.destPathInput.View())
	sb.WriteString("\n")

	if m.destPathErr != "" {
		sb.WriteString(errStyle.Render("  ✗ " + m.destPathErr))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("type path  enter confirm  esc back"))
	return sb.String()
}

// renderConfirmStep renders the content for DistributeStepConfirm.
//
// The view shows a full summary of the pending operation so the user can
// review before committing:
//   - Source host and file path(s)
//   - Destination hosts
//   - Copy mode
//   - Destination path
//   - A prominent warning when hub-and-spoke mode is selected (SSH access requirement)
//   - Checksum verification checkbox (toggled with Space)
func (m DistributeModel) renderConfirmStep() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	checkActiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	var sb strings.Builder
	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: %s", m.stepIndex()+1, m.totalSteps(), m.currentStepName())))
	sb.WriteString("\n\n")
	sb.WriteString(bodyStyle.Render("Confirm Distribution — review the details below before proceeding."))
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

	// Hub node (hub-and-spoke mode only) -----------------------------------
	if m.copyMode == "hub-spoke" {
		hubName := m.hubHost.DisplayName
		if hubName == "" {
			hubName = m.hubHost.Host
		}
		if hubName == "" {
			hubName = "(not yet selected)"
		}
		sb.WriteString(labelStyle.Render("Hub:          "))
		sb.WriteString(bodyStyle.Render(hubName))
		sb.WriteString("\n")
	}

	// Destination path -----------------------------------------------------
	dstPath := m.destPath
	if dstPath == "" {
		dstPath = "(not set)"
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

	// Checksum verification checkbox — mirrors the toggle in DistributeStepConfirm
	// so the user can change the setting before confirming the retry.
	checkActiveStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
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

	sb.WriteString(hintStyle.Render("space toggle checksum  y/enter proceed with retry  n/esc cancel"))
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
