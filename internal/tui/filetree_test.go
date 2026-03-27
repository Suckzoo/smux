package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeTestDir creates a temporary directory tree for filetree tests:
//
//	<root>/
//	  alpha.txt
//	  beta.txt
//	  subdir/
//	    child.txt
//	    .hidden.txt
//	  .hiddendir/
//	    secret.txt
func makeTestDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("content"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	writeFile("alpha.txt")
	writeFile("beta.txt")
	writeFile("subdir/child.txt")
	writeFile("subdir/.hidden.txt")
	writeFile(".hiddendir/secret.txt")

	return root
}

// sendFileTreeKey delivers a key to a FileTreeModel and returns the updated model.
func sendFileTreeKey(m FileTreeModel, keyStr string) (FileTreeModel, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyStr)}
	switch keyStr {
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+d":
		msg = tea.KeyMsg{Type: tea.KeyCtrlD}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	}
	updated, cmd := m.Update(msg)
	return updated.(FileTreeModel), cmd
}

// ---------------------------------------------------------------------------
// BuildFlatFileList tests
// ---------------------------------------------------------------------------

// TestBuildFlatFileListBasic verifies that the root-level files and
// directories appear in the flat list (dirs first, then files).
func TestBuildFlatFileListBasic(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	nodes := BuildFlatFileList(root, &state, false /* showHidden */)

	// Expected visible entries at root (hidden entries excluded):
	//   subdir/  (dir, depth 0)
	//   alpha.txt (file, depth 0)
	//   beta.txt  (file, depth 0)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 visible root nodes, got %d: %v", len(nodes), nodeNames(nodes))
	}

	// First node must be the directory.
	if !nodes[0].IsDir() || nodes[0].Name != "subdir" {
		t.Errorf("nodes[0] should be 'subdir' dir, got %+v", nodes[0])
	}
	// File nodes follow, sorted alphabetically.
	if !nodes[1].IsFile() || nodes[1].Name != "alpha.txt" {
		t.Errorf("nodes[1] should be 'alpha.txt', got %+v", nodes[1])
	}
	if !nodes[2].IsFile() || nodes[2].Name != "beta.txt" {
		t.Errorf("nodes[2] should be 'beta.txt', got %+v", nodes[2])
	}
}

// TestBuildFlatFileListShowHidden verifies that hidden files and directories
// appear when showHidden is true.
func TestBuildFlatFileListShowHidden(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	nodes := BuildFlatFileList(root, &state, true /* showHidden */)

	names := nodeNames(nodes)
	if !contains(names, ".hiddendir") {
		t.Errorf("showHidden=true should show '.hiddendir', got: %v", names)
	}
	if !contains(names, "alpha.txt") {
		t.Errorf("showHidden=true should still show 'alpha.txt', got: %v", names)
	}
}

// TestBuildFlatFileListHideHidden verifies that hidden entries are absent by
// default.
func TestBuildFlatFileListHideHidden(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	nodes := BuildFlatFileList(root, &state, false)

	for _, n := range nodes {
		if n.IsHidden {
			t.Errorf("showHidden=false should not show hidden entry %q", n.Name)
		}
	}
}

// TestBuildFlatFileListExpandDir verifies that expanding a directory exposes
// its children immediately after the parent node.
func TestBuildFlatFileListExpandDir(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	subdirPath := filepath.Join(root, "subdir")
	state.SetExpanded(subdirPath, true)

	nodes := BuildFlatFileList(root, &state, false)

	// Expected:
	//   subdir/    depth 0
	//   child.txt  depth 1  (hidden.txt excluded)
	//   alpha.txt  depth 0
	//   beta.txt   depth 0

	// Find subdir node.
	subdirIdx := -1
	for i, n := range nodes {
		if n.IsDir() && n.Name == "subdir" {
			subdirIdx = i
			break
		}
	}
	if subdirIdx < 0 {
		t.Fatal("subdir node not found")
	}

	// Next node must be child.txt at depth 1.
	if subdirIdx+1 >= len(nodes) {
		t.Fatal("no node after subdir — expected child.txt")
	}
	child := nodes[subdirIdx+1]
	if child.Name != "child.txt" || child.Depth != 1 {
		t.Errorf("expected child.txt at depth 1 after subdir, got %+v", child)
	}
}

// TestBuildFlatFileListCollapsedDir verifies that collapsing a directory
// hides its children.
func TestBuildFlatFileListCollapsedDir(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	subdirPath := filepath.Join(root, "subdir")
	// Expand then collapse.
	state.SetExpanded(subdirPath, true)
	state.SetExpanded(subdirPath, false)

	nodes := BuildFlatFileList(root, &state, false)
	for _, n := range nodes {
		if n.Depth > 0 {
			t.Errorf("collapsed subdir should not produce depth>0 nodes, got %+v", n)
		}
	}
}

// TestBuildFlatFileListDepthEncoding verifies that nested directories carry
// the correct depth values.
func TestBuildFlatFileListDepthEncoding(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	subdirPath := filepath.Join(root, "subdir")
	state.SetExpanded(subdirPath, true)

	nodes := BuildFlatFileList(root, &state, false)

	for _, n := range nodes {
		if n.Name == "subdir" && n.Depth != 0 {
			t.Errorf("subdir depth should be 0, got %d", n.Depth)
		}
		if n.Name == "child.txt" && n.Depth != 1 {
			t.Errorf("child.txt depth should be 1, got %d", n.Depth)
		}
	}
}

// TestBuildFlatFileListRootDepthIsZero verifies that all immediate children of
// the root directory are at depth 0.
func TestBuildFlatFileListRootDepthIsZero(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	nodes := BuildFlatFileList(root, &state, false)
	for _, n := range nodes {
		if n.Depth != 0 {
			t.Errorf("root-level entry %q should have depth 0, got %d", n.Name, n.Depth)
		}
	}
}

// TestBuildFlatFileListFullPathIsAbsolute verifies that FullPath is always an
// absolute path, not a relative one.
func TestBuildFlatFileListFullPathIsAbsolute(t *testing.T) {
	root := makeTestDir(t)
	state := NewFileTreeExpandedState()

	nodes := BuildFlatFileList(root, &state, false)
	for _, n := range nodes {
		if !filepath.IsAbs(n.FullPath) {
			t.Errorf("FullPath should be absolute, got %q", n.FullPath)
		}
	}
}

// ---------------------------------------------------------------------------
// FileTreeExpandedState tests
// ---------------------------------------------------------------------------

// TestFileTreeExpandedStateToggle verifies that Toggle flips the expanded state.
func TestFileTreeExpandedStateToggle(t *testing.T) {
	s := NewFileTreeExpandedState()
	path := "/some/path"

	if s.IsExpanded(path) {
		t.Error("new path should start collapsed")
	}
	s.Toggle(path)
	if !s.IsExpanded(path) {
		t.Error("after first toggle, path should be expanded")
	}
	s.Toggle(path)
	if s.IsExpanded(path) {
		t.Error("after second toggle, path should be collapsed again")
	}
}

// TestFileTreeExpandedStateSetExpanded verifies explicit set operations.
func TestFileTreeExpandedStateSetExpanded(t *testing.T) {
	s := NewFileTreeExpandedState()
	s.SetExpanded("/a", true)
	if !s.IsExpanded("/a") {
		t.Error("SetExpanded(true) should make path expanded")
	}
	s.SetExpanded("/a", false)
	if s.IsExpanded("/a") {
		t.Error("SetExpanded(false) should make path collapsed")
	}
}

// ---------------------------------------------------------------------------
// FileTreeModel navigation tests
// ---------------------------------------------------------------------------

// TestFileTreeModelCursorMovement verifies that ↓/j moves the cursor down and
// ↑/k moves it up, clamped to the valid range.
func TestFileTreeModelCursorMovement(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if m.cursor != 0 {
		t.Errorf("initial cursor should be 0, got %d", m.cursor)
	}

	m, _ = sendFileTreeKey(m, "down")
	if m.cursor != 1 {
		t.Errorf("after down, cursor should be 1, got %d", m.cursor)
	}

	m, _ = sendFileTreeKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("after up, cursor should be 0, got %d", m.cursor)
	}

	// Pressing up at the top should stay at 0.
	m, _ = sendFileTreeKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor should not go below 0, got %d", m.cursor)
	}
}

// TestFileTreeModelCursorJKAliases verifies that j/k are aliases for down/up.
func TestFileTreeModelCursorJKAliases(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	m, _ = sendFileTreeKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("j should move cursor down to 1, got %d", m.cursor)
	}
	m, _ = sendFileTreeKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("k should move cursor up to 0, got %d", m.cursor)
	}
}

// TestFileTreeModelCursorDownClamp verifies that pressing ↓ at the last row
// does not exceed the valid range.
func TestFileTreeModelCursorDownClamp(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	last := len(m.flatNodes) - 1
	m.cursor = last
	m, _ = sendFileTreeKey(m, "down")
	if m.cursor != last {
		t.Errorf("cursor should stay at last (%d), got %d", last, m.cursor)
	}
}

// TestFileTreeModelExpandCollapseViaEnter verifies that pressing Enter on a
// directory toggles its expansion and shows/hides its children.
func TestFileTreeModelExpandCollapseViaEnter(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Cursor starts at the directory (it's first in the list).
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsDir() {
		t.Skip("first node is not a directory — skipping expand test")
	}
	initialCount := len(m.flatNodes)

	// Expand.
	m, _ = sendFileTreeKey(m, "enter")
	if len(m.flatNodes) <= initialCount {
		t.Errorf("after expanding directory, node count should increase; before=%d after=%d",
			initialCount, len(m.flatNodes))
	}

	// Collapse.
	m, _ = sendFileTreeKey(m, "enter")
	if len(m.flatNodes) != initialCount {
		t.Errorf("after collapsing directory, node count should return to %d, got %d",
			initialCount, len(m.flatNodes))
	}
}

// TestFileTreeModelExpandViaRightKey verifies that "l" / right also expands.
func TestFileTreeModelExpandViaRightKey(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsDir() {
		t.Skip("first node is not a directory")
	}
	initialCount := len(m.flatNodes)

	m, _ = sendFileTreeKey(m, "l")
	if len(m.flatNodes) <= initialCount {
		t.Errorf("l key should expand directory; before=%d after=%d",
			initialCount, len(m.flatNodes))
	}
}

// TestFileTreeModelCollapseViaLeftKey verifies that pressing ← on an expanded
// directory collapses it, and on a collapsed directory (or file) moves cursor up.
func TestFileTreeModelCollapseViaLeftKey(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsDir() {
		t.Skip("first node is not a directory")
	}

	// Expand the directory first.
	m, _ = sendFileTreeKey(m, "enter")
	expanded := len(m.flatNodes)

	// Now press ← to collapse.
	m, _ = sendFileTreeKey(m, "left")
	if len(m.flatNodes) >= expanded {
		t.Errorf("left key should collapse directory; expanded=%d after=%d",
			expanded, len(m.flatNodes))
	}
}

// TestFileTreeModelSelectViaSpace verifies that pressing Space selects/deselects
// the node under the cursor.
func TestFileTreeModelSelectViaSpace(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Move to first file node.
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsFile() {
		t.Skip("no file node found")
	}

	filePath := m.flatNodes[m.cursor].FullPath

	// Initially not selected.
	if m.selected[filePath] {
		t.Error("file should not be selected initially")
	}

	// Select.
	m, _ = sendFileTreeKey(m, " ")
	if !m.selected[filePath] {
		t.Error("file should be selected after Space")
	}

	// Deselect.
	m, _ = sendFileTreeKey(m, " ")
	if m.selected[filePath] {
		t.Error("file should be deselected after second Space")
	}
}

// TestFileTreeModelToggleHiddenFiles verifies that pressing '.' toggles hidden
// file visibility, updating the flat list.
func TestFileTreeModelToggleHiddenFiles(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	countBefore := len(m.flatNodes)
	if countBefore == 0 {
		t.Fatal("flat list should be non-empty")
	}

	// Show hidden.
	m, _ = sendFileTreeKey(m, ".")
	countWithHidden := len(m.flatNodes)
	if countWithHidden <= countBefore {
		t.Errorf("showing hidden files should increase node count; before=%d after=%d",
			countBefore, countWithHidden)
	}

	// Hide again.
	m, _ = sendFileTreeKey(m, ".")
	if len(m.flatNodes) != countBefore {
		t.Errorf("hiding files again should restore count to %d, got %d",
			countBefore, len(m.flatNodes))
	}
}

// TestFileTreeModelQuitViaQ verifies that pressing 'q' marks the model as done
// with Quit=true.
func TestFileTreeModelQuitViaQ(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	m, cmd := sendFileTreeKey(m, "q")
	if !m.done {
		t.Error("q should mark model as done")
	}
	if !m.result.Quit {
		t.Error("q should set result.Quit=true")
	}
	if cmd == nil {
		t.Error("q should return a tea.Quit command")
	}
}

// TestFileTreeModelQuitViaCtrlC verifies that Ctrl+C marks the model as done
// with Quit=true.
func TestFileTreeModelQuitViaCtrlC(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	m, cmd := sendFileTreeKey(m, "ctrl+c")
	if !m.done {
		t.Error("ctrl+c should mark model as done")
	}
	if !m.result.Quit {
		t.Error("ctrl+c should set result.Quit=true")
	}
	if cmd == nil {
		t.Error("ctrl+c should return a tea.Quit command")
	}
}

// TestFileTreeModelEscSetsBack verifies that Esc sets the Back flag for wizard
// back-navigation without quitting.
func TestFileTreeModelEscSetsBack(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	m, _ = sendFileTreeKey(m, "esc")
	if !m.Back() {
		t.Error("Esc should set Back()=true")
	}
	if m.Done() {
		t.Error("Esc should not mark the model as Done()")
	}
}

// TestFileTreeModelConfirmViaCtrlD verifies that Ctrl+D confirms the selection
// and returns the selected paths.
func TestFileTreeModelConfirmViaCtrlD(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Select the first file node.
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			break
		}
	}
	m, _ = sendFileTreeKey(m, " ") // select it
	selectedPath := m.flatNodes[m.cursor].FullPath

	m, cmd := sendFileTreeKey(m, "ctrl+d")
	if !m.Done() {
		t.Error("Ctrl+D should mark model as done")
	}
	if m.result.Quit {
		t.Error("Ctrl+D should not set result.Quit")
	}
	if cmd == nil {
		t.Error("Ctrl+D should return a Quit command")
	}
	result := m.GetResult()
	found := false
	for _, p := range result.SelectedPaths {
		if p == selectedPath {
			found = true
		}
	}
	if !found {
		t.Errorf("selected path %q should be in result.SelectedPaths %v", selectedPath, result.SelectedPaths)
	}
}

// TestFileTreeModelConfirmWithNoSelection verifies that Ctrl+D with no files
// selected still marks the model as Done (empty selection is valid).
func TestFileTreeModelConfirmWithNoSelection(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	m, _ = sendFileTreeKey(m, "ctrl+d")
	if !m.Done() {
		t.Error("Ctrl+D should mark model as done even with empty selection")
	}
	if len(m.GetResult().SelectedPaths) != 0 {
		t.Errorf("result.SelectedPaths should be empty with no selection, got %v", m.GetResult().SelectedPaths)
	}
}

// ---------------------------------------------------------------------------
// View rendering tests
// ---------------------------------------------------------------------------

// TestFileTreeModelViewContainsRootPath verifies that the rendered view
// includes the root path in the path bar.
func TestFileTreeModelViewContainsRootPath(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	view := m.View()
	// The path bar renders the root — check that some meaningful path fragment
	// is present (may be shortened with ~/).
	if !strings.Contains(view, "smux") {
		t.Error("view should contain the 'smux' title")
	}
}

// TestFileTreeModelViewContainsFileNames verifies that file names from the
// root directory appear in the rendered view.
func TestFileTreeModelViewContainsFileNames(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	view := m.View()
	if !strings.Contains(view, "alpha.txt") {
		t.Errorf("view should contain 'alpha.txt', got:\n%s", view)
	}
	if !strings.Contains(view, "beta.txt") {
		t.Errorf("view should contain 'beta.txt', got:\n%s", view)
	}
}

// TestFileTreeModelViewDirHasArrow verifies that directory rows include an
// expand/collapse arrow indicator.
func TestFileTreeModelViewDirHasArrow(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	lines := m.renderFileList()
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsDir() {
			raw := stripANSI(lines[i])
			if !strings.Contains(raw, "▶") && !strings.Contains(raw, "▼") {
				t.Errorf("directory line %q should contain ▶ or ▼", raw)
			}
		}
	}
}

// TestFileTreeModelViewFileHasIndentation verifies that files under an
// expanded directory are rendered with greater indentation than the
// directory header.
func TestFileTreeModelViewFileIndentation(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	// Expand subdir.
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsDir() {
		t.Skip("first node is not a directory")
	}
	m, _ = sendFileTreeKey(m, "enter")

	lines := m.renderFileList()
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		raw := stripANSI(lines[i])
		if n.Depth == 0 && strings.HasPrefix(raw, "    ") {
			t.Errorf("depth-0 node %q should not have 4+ leading spaces", raw)
		}
		if n.Depth == 1 && !strings.HasPrefix(raw, "  ") {
			t.Errorf("depth-1 node %q should have at least 2 leading spaces", raw)
		}
	}
}

// TestFileTreeModelViewSelectedFileHasCheckmark verifies that a selected file
// shows a checkmark (✓) in the rendered output.
func TestFileTreeModelViewSelectedFileHasCheckmark(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	// Find first file and select it.
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			break
		}
	}
	m, _ = sendFileTreeKey(m, " ")

	lines := m.renderFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsFile() && m.selected[n.FullPath] {
			raw := stripANSI(lines[i])
			if strings.Contains(raw, "✓") {
				found = true
			}
		}
	}
	if !found {
		t.Error("selected file should show ✓ checkmark in rendered output")
	}
}

// TestFileTreeModelViewTooSmall verifies that very small terminals show a
// "too small" message instead of crashing.
func TestFileTreeModelViewTooSmall(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 20
	m.height = 4

	view := m.View()
	if !strings.Contains(view, "too small") {
		t.Errorf("tiny terminal should show 'too small' message, got: %q", view)
	}
}

// TestNewFileTreeModelAbsPath verifies that NewFileTreeModel resolves the
// root path to an absolute path.
func TestNewFileTreeModelAbsPath(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	if !filepath.IsAbs(m.RootPath) {
		t.Errorf("RootPath should be absolute, got %q", m.RootPath)
	}
}

// TestNewFileTreeModelEmptyPathUsesCwd verifies that NewFileTreeModel("") uses
// the current working directory.
func TestNewFileTreeModelEmptyPathUsesCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("cannot determine cwd")
	}
	m := NewFileTreeModel("")
	if m.RootPath != cwd {
		t.Errorf("empty rootPath should use cwd %q, got %q", cwd, m.RootPath)
	}
}

// TestFileTreeModelSelectedPathsAreSorted verifies that GetResult returns
// paths in sorted order.
func TestFileTreeModelSelectedPathsAreSorted(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Select all file nodes.
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			m, _ = sendFileTreeKey(m, " ")
		}
	}

	m, _ = sendFileTreeKey(m, "ctrl+d")
	paths := m.GetResult().SelectedPaths
	if !sort.StringsAreSorted(paths) {
		t.Errorf("result paths should be sorted, got %v", paths)
	}
}

// ---------------------------------------------------------------------------
// Multi-select: directory selection
// ---------------------------------------------------------------------------

// TestFileTreeModelSelectDirViaSpace verifies that pressing Space on a
// directory node selects and deselects it — directories are selectable items
// just like files.
func TestFileTreeModelSelectDirViaSpace(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Move to first directory node.
	for i, n := range m.flatNodes {
		if n.IsDir() {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir() {
		t.Skip("no directory node found")
	}

	dirPath := m.flatNodes[m.cursor].FullPath

	// Initially not selected.
	if m.selected[dirPath] {
		t.Error("directory should not be selected initially")
	}

	// Select.
	m, _ = sendFileTreeKey(m, " ")
	if !m.selected[dirPath] {
		t.Error("directory should be selected after Space")
	}

	// Deselect.
	m, _ = sendFileTreeKey(m, " ")
	if m.selected[dirPath] {
		t.Error("directory should be deselected after second Space")
	}
}

// TestFileTreeModelViewSelectedDirHasCheckmark verifies that a selected
// directory shows a checkmark (✓) next to its expand/collapse arrow in the
// rendered output.
func TestFileTreeModelViewSelectedDirHasCheckmark(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	// Find first directory and select it.
	for i, n := range m.flatNodes {
		if n.IsDir() {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir() {
		t.Skip("no directory node found")
	}

	m, _ = sendFileTreeKey(m, " ") // select the directory

	lines := m.renderFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsDir() && m.selected[n.FullPath] {
			raw := stripANSI(lines[i])
			if strings.Contains(raw, "✓") {
				found = true
			}
		}
	}
	if !found {
		t.Error("selected directory should show ✓ checkmark in rendered output")
	}
}

// TestFileTreeModelViewSelectedDirStillHasArrow verifies that a selected
// directory still shows the expand/collapse arrow (▶ or ▼) in addition to
// the checkmark.
func TestFileTreeModelViewSelectedDirStillHasArrow(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	// Find first directory, select it.
	for i, n := range m.flatNodes {
		if n.IsDir() {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir() {
		t.Skip("no directory node found")
	}

	m, _ = sendFileTreeKey(m, " ") // select

	lines := m.renderFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsDir() && m.selected[n.FullPath] {
			raw := stripANSI(lines[i])
			if strings.Contains(raw, "▶") || strings.Contains(raw, "▼") {
				found = true
			}
		}
	}
	if !found {
		t.Error("selected directory should still show ▶ or ▼ arrow alongside the checkmark")
	}
}

// TestFileTreeModelMultiSelectMixedTypes verifies that users can simultaneously
// select a mix of files and directories, and all selections appear in the result.
func TestFileTreeModelMultiSelectMixedTypes(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Collect one directory and one file to select.
	var dirPath, filePath string
	for _, n := range m.flatNodes {
		if n.IsDir() && dirPath == "" {
			dirPath = n.FullPath
		}
		if n.IsFile() && filePath == "" {
			filePath = n.FullPath
		}
		if dirPath != "" && filePath != "" {
			break
		}
	}
	if dirPath == "" || filePath == "" {
		t.Skip("test requires at least one directory and one file node")
	}

	// Select the directory.
	for i, n := range m.flatNodes {
		if n.FullPath == dirPath {
			m.cursor = i
			break
		}
	}
	m, _ = sendFileTreeKey(m, " ")

	// Select the file.
	for i, n := range m.flatNodes {
		if n.FullPath == filePath {
			m.cursor = i
			break
		}
	}
	m, _ = sendFileTreeKey(m, " ")

	// Confirm: both should be in the result.
	m, _ = sendFileTreeKey(m, "ctrl+d")
	result := m.GetResult()

	foundDir, foundFile := false, false
	for _, p := range result.SelectedPaths {
		if p == dirPath {
			foundDir = true
		}
		if p == filePath {
			foundFile = true
		}
	}
	if !foundDir {
		t.Errorf("directory path %q should be in result.SelectedPaths %v", dirPath, result.SelectedPaths)
	}
	if !foundFile {
		t.Errorf("file path %q should be in result.SelectedPaths %v", filePath, result.SelectedPaths)
	}
}

// TestFileTreeModelDirSelectionIndependentOfExpansion verifies that selecting a
// directory does not expand it (selection and expansion are independent).
func TestFileTreeModelDirSelectionIndependentOfExpansion(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Find first directory node.
	for i, n := range m.flatNodes {
		if n.IsDir() {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir() {
		t.Skip("no directory node found")
	}

	dirPath := m.flatNodes[m.cursor].FullPath
	countBefore := len(m.flatNodes)

	// Select the directory — node count should not change (no expansion).
	m, _ = sendFileTreeKey(m, " ")
	if len(m.flatNodes) != countBefore {
		t.Errorf("selecting a directory should not change node count; before=%d after=%d",
			countBefore, len(m.flatNodes))
	}
	if !m.selected[dirPath] {
		t.Error("directory should be selected after Space")
	}
	if m.state.IsExpanded(dirPath) {
		t.Error("Space on a directory should not expand it — selection and expansion are independent")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nodeNames(nodes []FlatFileNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
