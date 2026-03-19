package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/tmux"
)

// Result is what the TUI returns after the user confirms a selection.
type Result struct {
	// Hosts is the list of selected hosts to connect to.
	Hosts []config.ResolvedHost
	// Quit is true when the user pressed q or Ctrl+C.
	Quit bool
}

// BreakPaneMsg is emitted when the user presses the break-pane key (M-a)
// while a previously created SSH window exists. The Update loop handles this
// message by invoking the breakPane callback, which calls `tmux break-pane`
// to detach the focused SSH pane into its own window.
type BreakPaneMsg struct {
	// WindowID is the tmux window ID of the SSH window whose active pane
	// should be broken out into a new window.
	WindowID string
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

	// lastWindowID is the tmux window ID of the most recently created SSH
	// window. It is used by the M-b broadcast-toggle binding to target the
	// correct window while the TUI is active.
	lastWindowID string

	// toggleBroadcast is called when the user presses the broadcast-toggle key
	// (default M-b). It receives the last known SSH window ID. A nil function
	// means the binding is inactive (no SSH window created yet).
	toggleBroadcast func(windowID string) error

	// breakPane is called when the user presses the break-pane key (default
	// M-a) or when a double-click identifies a specific pane to break. When
	// triggered from M-a it receives the last SSH window ID so tmux breaks
	// the focused pane of that window. When triggered by a double-click it
	// receives the specific pane ID identified by click coordinates.
	// A nil function means the binding is inactive.
	breakPane func(paneID string) error

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

	// paneLayouts holds the geometry (position + size) of each SSH pane in the
	// most recently created tmux window. Used by the mouse double-click handler
	// to identify which tmux pane was clicked based on terminal coordinates.
	paneLayouts []tmux.PaneLayout

	// lastClickTime, lastClickX, lastClickY track the position and time of the
	// most recent left-button mouse press for double-click detection.
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int

	// Returned after the user confirms a selection or quits.
	done   bool
	result Result
}

// doubleClickWindow is the maximum elapsed time between two mouse press events
// at the same location for them to be considered a double-click.
const doubleClickWindow = 300 * time.Millisecond

// doubleClickRadius is the maximum distance (in terminal cells) between two
// mouse presses for them to be treated as a double-click on the same target.
const doubleClickRadius = 1

// ModelOption is a functional option for configuring a Model at construction
// time without changing New()'s required parameter list.
type ModelOption func(*Model)

// WithPaneLayouts supplies the current tmux pane geometry to the model so that
// the mouse double-click handler can map terminal coordinates to a pane ID.
// Obtain layouts via tmux.GetPaneLayouts after calling tmux.CreateSSHWindow.
func WithPaneLayouts(layouts []tmux.PaneLayout) ModelOption {
	return func(m *Model) { m.paneLayouts = layouts }
}

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
// lastWindowID is the tmux window ID of the most recently created SSH window,
// used by the M-b broadcast-toggle and M-a break-pane bindings
// (pass "" if no window exists yet).
//
// toggleBroadcast is an optional callback invoked when the user presses the
// broadcast-toggle key (M-b). It receives the last SSH window ID. Pass nil to
// make the binding a no-op until an SSH window has been created.
//
// breakPane is an optional callback invoked when the user presses the
// break-pane key (M-a). It receives the last SSH window ID. Pass nil to
// make the binding a no-op until an SSH window has been created.
// New creates a fresh Model from the given config.
// All clusters start expanded and no hosts are selected.
//
// lastWindowID is the tmux window ID of the most recently created SSH window,
// used by the M-b broadcast-toggle and M-a break-pane bindings
// (pass "" if no window exists yet).
//
// toggleBroadcast is an optional callback invoked when the user presses the
// broadcast-toggle key (M-b). It receives the last SSH window ID. Pass nil to
// make the binding a no-op until an SSH window has been created.
//
// breakPane is an optional callback invoked when the user presses the
// break-pane key (M-a) or double-clicks a pane. Pass nil to disable.
//
// opts are optional ModelOption values (e.g. WithPaneLayouts) that configure
// additional model behaviour without changing the required parameter list.
func New(cfg *config.Config, lastWindowID string, toggleBroadcast func(string) error, breakPane func(string) error, opts ...ModelOption) Model {
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
		filterInput:     ti,
		viewport:        vp,
		lastWindowID:    lastWindowID,
		toggleBroadcast: toggleBroadcast,
		breakPane:       breakPane,
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.rebuildFlat()
	return m
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

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.view.Width = msg.Width
		m.view.Height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 4 // reserve space for title + filter + status
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case BreakPaneMsg:
		// Execute the tmux break-pane action for the SSH window identified by
		// the message. The error is intentionally ignored so that a failing
		// break-pane does not crash or otherwise disrupt the TUI.
		if m.breakPane != nil && msg.WindowID != "" {
			_ = m.breakPane(msg.WindowID)
		}
		return m, nil
	}

	if m.isFilterActive() {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.rebuildFlat()
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
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
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "ctrl+c":
		m.done = true
		m.result = Result{Quit: true}
		return m, tea.Quit

	case "alt+b":
		// Toggle synchronize-panes for the most recently created SSH window.
		// This lets the user toggle broadcast mode without leaving the TUI.
		if m.toggleBroadcast != nil && m.lastWindowID != "" {
			fn := m.toggleBroadcast
			id := m.lastWindowID
			return m, func() tea.Msg {
				_ = fn(id)
				return nil
			}
		}

	case "alt+a":
		// Break the focused pane of the most recently created SSH window into
		// its own tmux window. Emits BreakPaneMsg so the update loop can
		// handle the tmux action asynchronously.
		if m.lastWindowID != "" {
			id := m.lastWindowID
			return m, func() tea.Msg {
				return BreakPaneMsg{WindowID: id}
			}
		}

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
		// Kill all non-smux windows then exit.
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
	sb.WriteString(statusStyle.Render(
		fmt.Sprintf("  %d selected  |  ↑↓ move  tab/←/→ expand  space select  / filter  enter confirm  q quit",
			nSelected),
	))

	return sb.String()
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
// cluster-header, and selection styles. Styles are applied to raw (ANSI-free)
// text so that no inner reset sequence can break the cursor background colour.
func (m Model) renderList() []string {
	clusterStyle := lipgloss.NewStyle().Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)

	var lines []string
	for i, n := range m.flatNodes {
		isCursor := i == m.view.Cursor

		var text string
		var isCluster, isSelected bool

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
			check := "  [ ] "
			if isSelected {
				check = "  [✓] "
			}
			text = check + n.Host.DisplayName
		}

		// Apply exactly one style so no inner ANSI reset can interrupt the
		// cursor background.
		var line string
		switch {
		case isCursor:
			line = cursorStyle.Render(padRight(text, m.view.Width))
		case isCluster:
			line = clusterStyle.Render(text)
		case isSelected:
			line = selectedStyle.Render(text)
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

// handleMouse processes a bubbletea mouse event. Only left-button presses are
// considered. Two presses at the same location within doubleClickWindow are
// treated as a double-click: the pane at the click coordinates is identified
// via the stored pane layout geometry and the breakPane callback is invoked
// with that pane's ID. A single click moves the TUI cursor to the clicked row.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Only handle left-button presses; ignore releases, motion, wheel, etc.
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	now := time.Now()
	isDoubleClick := !m.lastClickTime.IsZero() &&
		now.Sub(m.lastClickTime) <= doubleClickWindow &&
		absInt(msg.X-m.lastClickX) <= doubleClickRadius &&
		absInt(msg.Y-m.lastClickY) <= doubleClickRadius

	// Update click-tracking state for next event.
	m.lastClickTime = now
	m.lastClickX = msg.X
	m.lastClickY = msg.Y

	// Single click: move cursor to clicked row (rows 0–1 are header rows).
	const headerRows = 2
	listIdx := msg.Y - headerRows + m.viewport.YOffset
	if listIdx >= 0 && listIdx < len(m.flatNodes) {
		m.view.Cursor = listIdx
		m.clampViewport()
	}

	if !isDoubleClick || m.breakPane == nil || len(m.paneLayouts) == 0 {
		return m, nil
	}

	// Double-click with pane layout data: identify which tmux pane was clicked
	// and invoke breakPane asynchronously so the TUI remains responsive.
	paneID := m.findPaneAt(msg.X, msg.Y)
	if paneID == "" {
		return m, nil
	}
	fn := m.breakPane
	return m, func() tea.Msg {
		_ = fn(paneID)
		return nil
	}
}

// findPaneAt returns the PaneID of the first PaneLayout whose rectangle
// contains the terminal coordinate (x, y). Returns "" if no pane covers the
// point.
func (m Model) findPaneAt(x, y int) string {
	for _, p := range m.paneLayouts {
		if x >= p.X && x < p.X+p.Width && y >= p.Y && y < p.Y+p.Height {
			return p.PaneID
		}
	}
	return ""
}

// absInt returns the absolute value of n.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

