package tui

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/filetree"
)

// ---------------------------------------------------------------------------
// Async messages for remote directory loading
// ---------------------------------------------------------------------------

// remoteDirLoadedMsg is dispatched by fetchDirCmd when a remote directory
// listing completes successfully.
type remoteDirLoadedMsg struct {
	remotePath string
	entries    []filetree.FileEntry
}

// remoteDirErrorMsg is dispatched by fetchDirCmd when a remote directory
// listing fails.
type remoteDirErrorMsg struct {
	remotePath string
	err        error
}

// ---------------------------------------------------------------------------
// RemoteFlatNode — a flattened remote tree node ready for rendering
// ---------------------------------------------------------------------------

// RemoteFlatNode is a single visible entry in the remote file-tree display.
// It carries both the display metadata (Name, Depth) and the path key used to
// look up expanded/selected state.
type RemoteFlatNode struct {
	// Name is the base filename shown in the list.
	Name string
	// RemotePath is the path relative to the home directory (e.g.
	// "Documents/report.pdf"). The root (home dir) uses the special value ".".
	RemotePath string
	// IsDir is true when the entry is a directory.
	IsDir bool
	// IsHidden is true when the name starts with '.'.
	IsHidden bool
	// Size is the file size in bytes (zero for directories).
	Size int64
	// Depth is the indentation level (0 = immediate children of home dir).
	Depth int
	// IsPlaceholder is true for synthetic loading/error indicator rows that
	// cannot be selected or expanded.
	IsPlaceholder bool
	// PlaceholderKind is "loading" or "error" when IsPlaceholder is true.
	PlaceholderKind string
}

// ---------------------------------------------------------------------------
// RemoteFileTreeModel — bubbletea model for the remote file-tree browser
// ---------------------------------------------------------------------------

// RemoteFileTreeModel is the bubbletea model for browsing a remote host's
// filesystem via SSH/SFTP.  It displays the home directory (`.`) of the given
// ResolvedHost in a navigable tree and performs directory listings lazily over
// the network.
//
// On Init() the home directory listing is fetched immediately.  Expanding a
// sub-directory whose listing has not yet been cached also triggers a fetch.
// While a listing is in-flight a "(loading…)" placeholder appears; if the
// fetch fails an "(error: …)" placeholder is shown instead.
//
// Keybindings mirror FileTreeModel:
//
//	↑/k          move cursor up
//	↓/j          move cursor down
//	→/l/Enter    expand directory (triggers SSH fetch if not cached)
//	←/h          collapse directory or move cursor up
//	Space        select/deselect the entry under the cursor
//	.            toggle display of hidden files
//	Esc          signal "back" — sets Back()=true without quitting
//	q/Ctrl+C     quit the entire wizard
//	Ctrl+D       confirm the current selection and advance
type RemoteFileTreeModel struct {
	host config.ResolvedHost // remote host to browse

	// dirCache holds the fetched entries for each remote path.
	// The home directory uses the key ".".
	dirCache map[string][]filetree.FileEntry
	// loading tracks paths whose listings are currently being fetched.
	loading map[string]bool
	// loadErrors holds the error message for paths that failed to load.
	loadErrors map[string]string
	// expanded tracks which directory paths are currently open.
	expanded map[string]bool

	// flatNodes is the ordered list of visible nodes, rebuilt after every
	// state change.
	flatNodes []RemoteFlatNode

	cursor     int
	selected   map[string]bool // remote path → selected
	showHidden bool

	width   int
	height  int
	yOffset int // viewport scroll offset

	// Terminal state signals — consumed by the parent wizard.
	done   bool
	back   bool
	result FileTreeResult
}

// NewRemoteFileTreeModel creates a RemoteFileTreeModel for the given host.
// The home directory listing is requested on Init().
func NewRemoteFileTreeModel(host config.ResolvedHost) RemoteFileTreeModel {
	m := RemoteFileTreeModel{
		host:       host,
		dirCache:   make(map[string][]filetree.FileEntry),
		loading:    make(map[string]bool),
		loadErrors: make(map[string]string),
		expanded:   make(map[string]bool),
		selected:   make(map[string]bool),
	}
	// The home dir is marked as expanded so its contents appear once loaded.
	m.expanded["."] = true
	// Mark as loading immediately so the UI shows the placeholder at once.
	m.loading["."] = true
	m.rebuild()
	return m
}

// Done reports whether the remote file-tree browser has finished.
func (m RemoteFileTreeModel) Done() bool { return m.done }

// Back reports whether the user pressed Esc to step back in the wizard.
func (m RemoteFileTreeModel) Back() bool { return m.back }

// GetResult returns the FileTreeResult (valid only when Done() is true).
func (m RemoteFileTreeModel) GetResult() FileTreeResult { return m.result }

// Init implements tea.Model. Triggers the initial home directory fetch.
func (m RemoteFileTreeModel) Init() tea.Cmd {
	return fetchDirCmd(m.host, ".")
}

// fetchDirCmd returns a tea.Cmd that lists remotePath on host, delivering
// either a remoteDirLoadedMsg or a remoteDirErrorMsg.
func fetchDirCmd(host config.ResolvedHost, remotePath string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		entries, err := filetree.RemoteListDir(ctx, host, remotePath)
		if err != nil {
			return remoteDirErrorMsg{remotePath: remotePath, err: err}
		}
		return remoteDirLoadedMsg{remotePath: remotePath, entries: entries}
	}
}

// Update implements tea.Model.
func (m RemoteFileTreeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case remoteDirLoadedMsg:
		delete(m.loading, msg.remotePath)
		delete(m.loadErrors, msg.remotePath)
		m.dirCache[msg.remotePath] = msg.entries
		m.rebuild()
		return m, nil

	case remoteDirErrorMsg:
		delete(m.loading, msg.remotePath)
		m.loadErrors[msg.remotePath] = msg.err.Error()
		m.rebuild()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RemoteFileTreeModel) handleKey(msg tea.KeyMsg) (RemoteFileTreeModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.done = true
		m.result = FileTreeResult{Quit: true}
		return m, tea.Quit

	case "q":
		m.done = true
		m.result = FileTreeResult{Quit: true}
		return m, tea.Quit

	case "esc":
		m.back = true
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.clampViewport()
		}

	case "down", "j":
		if m.cursor < len(m.flatNodes)-1 {
			m.cursor++
			m.clampViewport()
		}

	case "enter", "right", "l":
		cmd := m.expandOrOpen()
		return m, cmd

	case "left", "h":
		m.collapseOrMoveUp()

	case " ":
		m.toggleSelect()

	case ".":
		m.showHidden = !m.showHidden
		m.rebuild()

	case "ctrl+d":
		paths := m.sortedSelected()
		m.done = true
		m.result = FileTreeResult{SelectedPaths: paths}
		return m, tea.Quit
	}

	return m, nil
}

// expandOrOpen expands or collapses the directory under the cursor.
// If the directory has no cached listing and is not already loading, it
// triggers a fetch and returns the corresponding tea.Cmd.
func (m *RemoteFileTreeModel) expandOrOpen() tea.Cmd {
	if m.cursor >= len(m.flatNodes) {
		return nil
	}
	n := m.flatNodes[m.cursor]
	if !n.IsDir || n.IsPlaceholder {
		return nil
	}
	remotePath := n.RemotePath

	if m.expanded[remotePath] {
		// Collapse the directory.
		m.expanded[remotePath] = false
		m.rebuild()
		return nil
	}

	// Expand the directory.
	m.expanded[remotePath] = true

	// If the directory listing is not cached and not already in-flight, mark
	// the path as loading BEFORE calling rebuild so the loading placeholder
	// appears immediately in the rendered list.
	var cmd tea.Cmd
	if _, cached := m.dirCache[remotePath]; !cached && !m.loading[remotePath] {
		m.loading[remotePath] = true
		cmd = fetchDirCmd(m.host, remotePath)
	}

	m.rebuild()
	return cmd
}

// collapseOrMoveUp collapses an expanded directory under the cursor, or moves
// the cursor up when the current entry is not an expanded directory.
func (m *RemoteFileTreeModel) collapseOrMoveUp() {
	if m.cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.cursor]
	if n.IsDir && !n.IsPlaceholder && m.expanded[n.RemotePath] {
		m.expanded[n.RemotePath] = false
		m.rebuild()
		return
	}
	if m.cursor > 0 {
		m.cursor--
		m.clampViewport()
	}
}

// toggleSelect selects or deselects the node under the cursor.
// Placeholder nodes (loading/error) cannot be selected.
func (m *RemoteFileTreeModel) toggleSelect() {
	if m.cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.cursor]
	if n.IsPlaceholder {
		return
	}
	if m.selected[n.RemotePath] {
		delete(m.selected, n.RemotePath)
	} else {
		m.selected[n.RemotePath] = true
	}
}

// sortedSelected returns all selected paths in sorted order.
func (m *RemoteFileTreeModel) sortedSelected() []string {
	paths := make([]string, 0, len(m.selected))
	for p := range m.selected {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// rebuild regenerates the flat visible node list from the current state.
func (m *RemoteFileTreeModel) rebuild() {
	m.flatNodes = m.buildFlatNodes()
	if m.cursor >= len(m.flatNodes) && len(m.flatNodes) > 0 {
		m.cursor = len(m.flatNodes) - 1
	}
	if len(m.flatNodes) == 0 {
		m.cursor = 0
	}
}

// buildFlatNodes constructs the full visible node list starting from the home
// directory. Special-cases the root loading/error state.
func (m *RemoteFileTreeModel) buildFlatNodes() []RemoteFlatNode {
	if m.loading["."] {
		return []RemoteFlatNode{{
			Name:            "(loading…)",
			RemotePath:      "./__loading__",
			Depth:           0,
			IsPlaceholder:   true,
			PlaceholderKind: "loading",
		}}
	}
	if errMsg, hasErr := m.loadErrors["."]; hasErr {
		return []RemoteFlatNode{{
			Name:            fmt.Sprintf("(error: %s)", errMsg),
			RemotePath:      "./__error__",
			Depth:           0,
			IsPlaceholder:   true,
			PlaceholderKind: "error",
		}}
	}
	return m.buildFlatList(".", 0)
}

// buildFlatList recursively builds the visible flat node list for the given
// remote directory path at the given indentation depth.
func (m *RemoteFileTreeModel) buildFlatList(dirPath string, depth int) []RemoteFlatNode {
	entries := m.dirCache[dirPath]

	// Separate dirs and files; sort each group alphabetically.
	var dirs, files []filetree.FileEntry
	for _, e := range entries {
		if e.IsDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sortEntries := func(es []filetree.FileEntry) {
		sort.Slice(es, func(i, j int) bool {
			return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
		})
	}
	sortEntries(dirs)
	sortEntries(files)
	sorted := append(dirs, files...) //nolint:gocritic

	var flat []RemoteFlatNode
	for _, e := range sorted {
		isHidden := strings.HasPrefix(e.Name, ".")
		if !m.showHidden && isHidden {
			continue
		}

		// Build the remote path for this entry.
		// path.Join(".", "foo") → "foo"; path.Join("foo", "bar") → "foo/bar".
		entryPath := path.Join(dirPath, e.Name)

		flat = append(flat, RemoteFlatNode{
			Name:       e.Name,
			RemotePath: entryPath,
			IsDir:      e.IsDir,
			IsHidden:   isHidden,
			Size:       e.Size,
			Depth:      depth,
		})

		if !e.IsDir || !m.expanded[entryPath] {
			continue
		}

		// The directory is expanded — show its contents or a status placeholder.
		switch {
		case m.loading[entryPath]:
			flat = append(flat, RemoteFlatNode{
				Name:            "(loading…)",
				RemotePath:      entryPath + "/__loading__",
				Depth:           depth + 1,
				IsPlaceholder:   true,
				PlaceholderKind: "loading",
			})
		case m.loadErrors[entryPath] != "":
			flat = append(flat, RemoteFlatNode{
				Name:            fmt.Sprintf("(error: %s)", m.loadErrors[entryPath]),
				RemotePath:      entryPath + "/__error__",
				Depth:           depth + 1,
				IsPlaceholder:   true,
				PlaceholderKind: "error",
			})
		default:
			if _, cached := m.dirCache[entryPath]; cached {
				children := m.buildFlatList(entryPath, depth+1)
				flat = append(flat, children...)
			}
		}
	}
	return flat
}

// clampViewport adjusts yOffset so the cursor row is always visible.
func (m *RemoteFileTreeModel) clampViewport() {
	viewH := m.remoteViewportHeight()
	if m.cursor < m.yOffset {
		m.yOffset = m.cursor
	} else if m.cursor >= m.yOffset+viewH {
		m.yOffset = m.cursor - viewH + 1
	}
}

// remoteViewportHeight returns the number of lines available for the file list.
// Reserves 3 lines: 1 title + 1 path bar + 1 status bar.
func (m *RemoteFileTreeModel) remoteViewportHeight() int {
	h := m.height - 3
	if h < 1 {
		h = 1
	}
	return h
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View implements tea.Model.
func (m RemoteFileTreeModel) View() string {
	if m.width < 40 || m.height < 6 {
		return "Terminal too small (need at least 40×6)"
	}

	var sb strings.Builder

	// Title bar.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	sb.WriteString(titleStyle.Render("smux — select remote file") + "\n")

	// Host + path bar.
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	displayHost := m.host.DisplayName
	if displayHost == "" {
		displayHost = m.host.Host
	}
	sb.WriteString(pathStyle.Render(displayHost+":~/") + "\n")

	// File list (scrollable window into flatNodes).
	lines := m.renderRemoteFileList()
	viewH := m.remoteViewportHeight()
	start := m.yOffset
	end := start + viewH
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		start = end
	}
	for _, line := range lines[start:end] {
		sb.WriteString(line + "\n")
	}

	// Status bar.
	nSel := len(m.selected)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hiddenHint := ""
	if m.showHidden {
		hiddenHint = " (hidden shown)"
	}
	sb.WriteString(statusStyle.Render(fmt.Sprintf(
		"  %d selected%s  |  ↑↓ move  →/Enter expand  ←/h collapse  space select  . toggle hidden  Ctrl+D confirm  Esc back  q quit",
		nSel, hiddenHint,
	)))

	return sb.String()
}

// renderRemoteFileList returns one rendered line per visible RemoteFlatNode.
func (m RemoteFileTreeModel) renderRemoteFileList() []string {
	dirStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	hiddenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	loadingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)

	var lines []string
	for i, n := range m.flatNodes {
		isCursor := i == m.cursor
		indent := strings.Repeat("  ", n.Depth)

		var text string

		if n.IsPlaceholder {
			text = indent + "  " + n.Name
			var line string
			switch {
			case isCursor:
				line = cursorStyle.Render(padRight(text, m.width))
			case n.PlaceholderKind == "error":
				line = errorStyle.Render(text)
			default:
				line = loadingStyle.Render(text)
			}
			lines = append(lines, line)
			continue
		}

		isSelected := m.selected[n.RemotePath]

		if n.IsDir {
			arrow := "▶"
			if m.expanded[n.RemotePath] {
				arrow = "▼"
			}
			// Replace the trailing space after the arrow with ✓ when selected,
			// keeping the 2-char prefix width aligned with file entries.
			selChar := " "
			if isSelected {
				selChar = "✓"
			}
			text = indent + arrow + selChar + n.Name + "/"
		} else {
			check := "  "
			if isSelected {
				check = "✓ "
			}
			text = indent + check + n.Name
		}

		// isSelected takes priority over directory/hidden styles so that
		// selected items (including directories) are always rendered in the
		// selection colour (green).
		var line string
		switch {
		case isCursor:
			line = cursorStyle.Render(padRight(text, m.width))
		case isSelected:
			line = selectedStyle.Render(text)
		case n.IsDir && !n.IsHidden:
			line = dirStyle.Render(text)
		case n.IsHidden:
			line = hiddenStyle.Render(text)
		default:
			line = text
		}

		lines = append(lines, line)
	}
	return lines
}
