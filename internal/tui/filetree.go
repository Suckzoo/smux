package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// FileNodeKind
// ---------------------------------------------------------------------------

// FileNodeKind distinguishes directory nodes from file nodes in the file tree.
type FileNodeKind int

const (
	// FileNodeKindDir represents a navigable directory entry.
	FileNodeKindDir FileNodeKind = iota
	// FileNodeKindFile represents a selectable file entry.
	FileNodeKindFile
	// FileNodeKindParentDir represents the ".." parent-navigation entry.
	FileNodeKindParentDir
)

// ---------------------------------------------------------------------------
// FileTreeNode — raw domain node (not yet flattened)
// ---------------------------------------------------------------------------

// FileTreeNode is a single entry in the file tree (either a file or directory).
// It represents the domain information about an entry; display/depth state is
// captured in FlatFileNode when the tree is flattened for rendering.
type FileTreeNode struct {
	Kind     FileNodeKind
	Name     string // base name (not the full path)
	FullPath string // absolute path
	IsHidden bool   // true when Name starts with '.'
}

// IsDir reports whether this node represents a directory.
func (n FileTreeNode) IsDir() bool { return n.Kind == FileNodeKindDir }

// IsFile reports whether this node represents a regular file.
func (n FileTreeNode) IsFile() bool { return n.Kind == FileNodeKindFile }

// IsParentDir reports whether this node is the special ".." parent-navigation entry.
func (n FileTreeNode) IsParentDir() bool { return n.Kind == FileNodeKindParentDir }

// ---------------------------------------------------------------------------
// FileTreeExpandedState — tracks which directories are open
// ---------------------------------------------------------------------------

// FileTreeExpandedState records the expanded/collapsed state for each directory
// in the file-tree browser. Keys are absolute paths.
type FileTreeExpandedState struct {
	expanded map[string]bool
}

// NewFileTreeExpandedState returns a new, empty expanded-state map.
func NewFileTreeExpandedState() FileTreeExpandedState {
	return FileTreeExpandedState{expanded: make(map[string]bool)}
}

// IsExpanded reports whether the directory at path is currently expanded.
func (s *FileTreeExpandedState) IsExpanded(path string) bool {
	return s.expanded[path]
}

// SetExpanded explicitly sets the expanded state for a directory.
func (s *FileTreeExpandedState) SetExpanded(path string, v bool) {
	s.expanded[path] = v
}

// Toggle flips the expanded/collapsed state for the directory at path.
func (s *FileTreeExpandedState) Toggle(path string) {
	s.expanded[path] = !s.expanded[path]
}

// ---------------------------------------------------------------------------
// FlatFileNode — a tree node enriched with display depth
// ---------------------------------------------------------------------------

// FlatFileNode is a FileTreeNode that has been flattened for rendering.  The
// Depth field carries the indentation level (0 = immediate children of the
// root directory, 1 = one level down, etc.).
type FlatFileNode struct {
	FileTreeNode
	Depth int
}

// ---------------------------------------------------------------------------
// readDirEntries — reads a directory, dirs first then files, sorted
// ---------------------------------------------------------------------------

// readDirEntries reads path and returns FileTreeNodes sorted directories-first,
// then files, each group sorted alphabetically (case-insensitive).
// Returns an empty slice (not an error) when the directory cannot be read.
func readDirEntries(dirPath string) []FileTreeNode {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}
	var dirs, files []FileTreeNode
	for _, e := range entries {
		abs := filepath.Join(dirPath, e.Name())
		hidden := strings.HasPrefix(e.Name(), ".")
		if e.IsDir() {
			dirs = append(dirs, FileTreeNode{
				Kind:     FileNodeKindDir,
				Name:     e.Name(),
				FullPath: abs,
				IsHidden: hidden,
			})
		} else {
			files = append(files, FileTreeNode{
				Kind:     FileNodeKindFile,
				Name:     e.Name(),
				FullPath: abs,
				IsHidden: hidden,
			})
		}
	}
	// os.ReadDir already returns entries in directory order (alphabetical on most
	// systems).  Sort explicitly to guarantee stability across platforms.
	sortNodes := func(ns []FileTreeNode) {
		sort.Slice(ns, func(i, j int) bool {
			return strings.ToLower(ns[i].Name) < strings.ToLower(ns[j].Name)
		})
	}
	sortNodes(dirs)
	sortNodes(files)
	return append(dirs, files...)
}

// ---------------------------------------------------------------------------
// BuildFlatFileList — produces the ordered visible-node list
// ---------------------------------------------------------------------------

// BuildFlatFileList builds a flat, ordered slice of FlatFileNodes that are
// currently visible under rootPath, respecting the expanded/collapsed state
// and the showHidden flag.
//
// Directories are shown before files at each level.  Hidden entries (names
// starting with '.') are included only when showHidden is true.
func BuildFlatFileList(rootPath string, state *FileTreeExpandedState, showHidden bool) []FlatFileNode {
	return buildFlatFileListAt(rootPath, state, 0, showHidden)
}

// buildFlatFileListAt is the recursive helper for BuildFlatFileList.
func buildFlatFileListAt(dirPath string, state *FileTreeExpandedState, depth int, showHidden bool) []FlatFileNode {
	entries := readDirEntries(dirPath)
	var flat []FlatFileNode
	for _, n := range entries {
		if !showHidden && n.IsHidden {
			continue
		}
		flat = append(flat, FlatFileNode{FileTreeNode: n, Depth: depth})
		if n.IsDir() && state.IsExpanded(n.FullPath) {
			children := buildFlatFileListAt(n.FullPath, state, depth+1, showHidden)
			flat = append(flat, children...)
		}
	}
	return flat
}

// ---------------------------------------------------------------------------
// FileTreeResult — output from the file-tree browser
// ---------------------------------------------------------------------------

// FileTreeResult is what the FileTreeModel returns when the user finishes
// selecting files (either by confirming or by pressing q/Ctrl+C).
type FileTreeResult struct {
	// SelectedPaths are the absolute paths of all selected files/directories.
	// Empty when Quit is true.
	SelectedPaths []string
	// Quit is true when the user pressed q or Ctrl+C without confirming.
	Quit bool
}

// ---------------------------------------------------------------------------
// FileTreeModel — bubbletea model for the local file-tree browser
// ---------------------------------------------------------------------------

// FileTreeModel is the bubbletea model for browsing local files and
// directories.  It displays the contents of RootPath in a navigable tree,
// supports expand/collapse of sub-directories, and allows multi-file selection
// via Space.
//
// Keybindings:
//
//	↑/k          move cursor up
//	↓/j          move cursor down
//	→/l/Enter    expand directory (or no-op on files)
//	←/h          collapse directory or move cursor up
//	Space        select/deselect the entry under the cursor
//	.            toggle display of hidden files
//	Esc          signal "back" (sets result to a non-quit, empty result so
//	             the parent wizard can step back)
//	q/Ctrl+C     quit the entire wizard
//	Ctrl+D       confirm the current selection and advance
type FileTreeModel struct {
	RootPath string // root directory being browsed (absolute)

	state      FileTreeExpandedState
	flatNodes  []FlatFileNode
	cursor     int
	selected   map[string]bool // absolute path → selected
	showHidden bool

	width   int
	height  int
	yOffset int // viewport scroll offset

	// done / result follow the same convention as the host-selection Model.
	done   bool
	result FileTreeResult

	// back is set when the user pressed Esc (parent wizard should step back).
	back bool
}

// NewFileTreeModel creates a FileTreeModel rooted at rootPath.
// If rootPath is empty or ".", the current working directory is used.
func NewFileTreeModel(rootPath string) FileTreeModel {
	if rootPath == "" || rootPath == "." {
		var err error
		rootPath, err = os.Getwd()
		if err != nil {
			rootPath = "."
		}
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		abs = rootPath
	}
	m := FileTreeModel{
		RootPath:   abs,
		state:      NewFileTreeExpandedState(),
		selected:   make(map[string]bool),
		showHidden: false,
	}
	m.rebuild()
	return m
}

// Done reports whether the file-tree browser has finished.
func (m FileTreeModel) Done() bool { return m.done }

// Back reports whether the user pressed Esc to step back in the wizard.
func (m FileTreeModel) Back() bool { return m.back }

// GetResult returns the FileTreeResult (valid only when Done() is true).
func (m FileTreeModel) GetResult() FileTreeResult { return m.result }

// Init implements tea.Model.
func (m FileTreeModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m FileTreeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m FileTreeModel) handleKey(msg tea.KeyMsg) (FileTreeModel, tea.Cmd) {
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
		// Signal the parent wizard to step back.
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
		m.expandOrOpen()

	case "left", "h":
		m.collapseOrMoveUp()

	case " ":
		m.toggleSelect()

	case ".":
		m.showHidden = !m.showHidden
		m.rebuild()

	case "ctrl+d":
		// Confirm selection and advance.
		paths := m.sortedSelected()
		m.done = true
		m.result = FileTreeResult{SelectedPaths: paths}
		return m, tea.Quit
	}

	return m, nil
}

// expandOrOpen expands the directory under the cursor (if collapsed), or
// collapses it (if already expanded).  When the cursor is on the ".." entry
// the view navigates to the parent directory.  Has no effect on file nodes.
func (m *FileTreeModel) expandOrOpen() {
	if m.cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.cursor]
	if n.IsParentDir() {
		// Navigate to the parent directory.  filepath.Dir("/") == "/" so this
		// is a safe no-op at the filesystem root.
		parent := filepath.Dir(m.RootPath)
		if parent == m.RootPath {
			// Already at filesystem root — nothing to navigate to.
			return
		}
		m.RootPath = parent
		m.cursor = 0
		m.yOffset = 0
		m.state = NewFileTreeExpandedState()
		m.rebuild()
		return
	}
	if n.IsDir() {
		m.state.Toggle(n.FullPath)
		m.rebuild()
	}
}

// collapseOrMoveUp collapses an expanded directory under the cursor, or moves
// the cursor up when the current entry is a file or already-collapsed directory.
func (m *FileTreeModel) collapseOrMoveUp() {
	if m.cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.cursor]
	if n.IsDir() && m.state.IsExpanded(n.FullPath) {
		m.state.SetExpanded(n.FullPath, false)
		m.rebuild()
		return
	}
	if m.cursor > 0 {
		m.cursor--
		m.clampViewport()
	}
}

// toggleSelect selects or deselects the node under the cursor.
// The ".." parent-navigation entry is not selectable.
func (m *FileTreeModel) toggleSelect() {
	if m.cursor >= len(m.flatNodes) {
		return
	}
	n := m.flatNodes[m.cursor]
	if n.IsParentDir() {
		return
	}
	p := n.FullPath
	if m.selected[p] {
		delete(m.selected, p)
	} else {
		m.selected[p] = true
	}
}

// sortedSelected returns all selected paths in sorted order.
func (m *FileTreeModel) sortedSelected() []string {
	paths := make([]string, 0, len(m.selected))
	for p := range m.selected {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// rebuild regenerates the flat node list from the current expanded state.
// A ".." parent-navigation entry is always prepended at index 0.
func (m *FileTreeModel) rebuild() {
	entries := BuildFlatFileList(m.RootPath, &m.state, m.showHidden)

	// Determine the parent path for the ".." entry.
	// filepath.Dir("/") == "/" so at root the entry still appears but navigates nowhere.
	parentPath := filepath.Dir(m.RootPath)
	parentEntry := FlatFileNode{
		FileTreeNode: FileTreeNode{
			Kind:     FileNodeKindParentDir,
			Name:     "..",
			FullPath: parentPath,
		},
		Depth: 0,
	}
	m.flatNodes = append([]FlatFileNode{parentEntry}, entries...)

	if m.cursor >= len(m.flatNodes) && len(m.flatNodes) > 0 {
		m.cursor = len(m.flatNodes) - 1
	}
	if len(m.flatNodes) == 0 {
		m.cursor = 0
	}
}

// clampViewport adjusts yOffset so the cursor row is always visible.
func (m *FileTreeModel) clampViewport() {
	viewH := m.viewportHeight()
	if m.cursor < m.yOffset {
		m.yOffset = m.cursor
	} else if m.cursor >= m.yOffset+viewH {
		m.yOffset = m.cursor - viewH + 1
	}
}

// viewportHeight returns the number of lines available for the file list.
// Reserves 3 lines: 1 title + 1 path bar + 1 status bar.
func (m *FileTreeModel) viewportHeight() int {
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
func (m FileTreeModel) View() string {
	if m.width < 40 || m.height < 6 {
		return "Terminal too small (need at least 40×6)"
	}

	var sb strings.Builder

	// Title bar.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	sb.WriteString(titleStyle.Render("smux — select file") + "\n")

	// Current path bar.
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	displayRoot := m.RootPath
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, m.RootPath); err == nil && !strings.HasPrefix(rel, "..") {
			displayRoot = "~/" + rel
		}
	}
	sb.WriteString(pathStyle.Render(displayRoot) + "\n")

	// File list (scrollable slice of the flat nodes).
	lines := m.renderFileList()
	viewH := m.viewportHeight()
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

// renderFileList renders one line per visible FlatFileNode.
func (m FileTreeModel) renderFileList() []string {
	dirStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	hiddenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(false)

	var lines []string
	for i, n := range m.flatNodes {
		isCursor := i == m.cursor
		indent := strings.Repeat("  ", n.Depth)
		isSelected := m.selected[n.FullPath]

		var prefix, name string
		if n.IsParentDir() {
			// The ".." entry always appears at depth 0 with an up-arrow indicator.
			// It is not selectable, so no checkmark is shown.
			prefix = "↑ "
			name = "../"
		} else if n.IsDir() {
			arrow := "▶"
			if m.state.IsExpanded(n.FullPath) {
				arrow = "▼"
			}
			// Replace the trailing space after the arrow with ✓ when selected,
			// keeping the 2-char prefix width aligned with file entries.
			selChar := " "
			if isSelected {
				selChar = "✓"
			}
			prefix = indent + arrow + selChar
			name = n.Name + "/"
		} else {
			// Files get a small bullet aligned with dir arrows.
			check := "  "
			if isSelected {
				check = "✓ "
			}
			prefix = indent + check
			name = n.Name
		}

		text := prefix + name

		// isSelected takes priority over directory/hidden styles so that
		// selected items (including directories) are always rendered in the
		// selection colour (green).
		var line string
		switch {
		case isCursor:
			line = cursorStyle.Render(padRight(text, m.width))
		case n.IsParentDir():
			// ".." entry rendered with dim/muted style to distinguish from real entries.
			line = hiddenStyle.Render(text)
		case isSelected:
			line = selectedStyle.Render(text)
		case n.IsDir() && !n.IsHidden:
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
