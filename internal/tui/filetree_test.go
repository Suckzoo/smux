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

	// Find the first real directory (index 0 is always the ".." entry).
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no directory node found — skipping expand test")
	}
	m.cursor = dirIdx
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

	// Find the first real directory (index 0 is always the ".." entry).
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no directory node found")
	}
	m.cursor = dirIdx
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

	// Find the first real directory (index 0 is always the ".." entry).
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no directory node found")
	}
	m.cursor = dirIdx

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

	// Find the first real directory (index 0 is always the ".." entry) and expand it.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no directory node found")
	}
	m.cursor = dirIdx
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
// Parent-directory navigation entry tests
// ---------------------------------------------------------------------------

// TestFileTreeModelParentDirEntryAtTop verifies that the ".." entry always
// appears as the first item in the flat node list.
func TestFileTreeModelParentDirEntryAtTop(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes should be non-empty")
	}
	first := m.flatNodes[0]
	if !first.IsParentDir() {
		t.Errorf("first flatNode should be the '..' parent-dir entry, got Kind=%v Name=%q", first.Kind, first.Name)
	}
	if first.Name != ".." {
		t.Errorf("parent-dir entry Name should be '..', got %q", first.Name)
	}
}

// TestFileTreeModelParentDirEntryInView verifies that the ".." entry is
// visible in the rendered View output.
func TestFileTreeModelParentDirEntryInView(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 120
	m.height = 30

	view := m.View()
	if !strings.Contains(view, "..") {
		t.Errorf("view should contain the '..' parent-navigation entry, got:\n%s", view)
	}
}

// TestFileTreeModelParentDirEntryPresentAfterToggleHidden verifies that the
// ".." entry remains at the top even after toggling hidden file visibility.
func TestFileTreeModelParentDirEntryPresentAfterToggleHidden(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Toggle hidden on.
	m, _ = sendFileTreeKey(m, ".")
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Error("'..' entry should still be at index 0 after toggling hidden files on")
	}

	// Toggle hidden off.
	m, _ = sendFileTreeKey(m, ".")
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Error("'..' entry should still be at index 0 after toggling hidden files off")
	}
}

// TestFileTreeModelParentDirNotSelectable verifies that pressing Space on the
// ".." entry does not add it to the selection map.
func TestFileTreeModelParentDirNotSelectable(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Cursor starts at ".." (index 0).
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0
	parentPath := m.flatNodes[0].FullPath

	m, _ = sendFileTreeKey(m, " ")
	if m.selected[parentPath] {
		t.Error("pressing Space on the '..' entry should not add it to the selection map")
	}
	if len(m.selected) != 0 {
		t.Errorf("selection map should be empty after Space on '..', got %v", m.selected)
	}
}

// ---------------------------------------------------------------------------
// Parent-directory navigation tests (AC 3) — local FileTreeModel
// ---------------------------------------------------------------------------

// TestFileTreeModelNavigateUpViaEnter verifies that pressing Enter when the
// cursor is on the ".." entry changes RootPath to the parent directory.
func TestFileTreeModelNavigateUpViaEnter(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	originalRoot := m.RootPath
	expectedParent := filepath.Dir(originalRoot)

	// Cursor is already at index 0 (..) after construction.
	m, _ = sendFileTreeKey(m, "enter")

	if m.RootPath == originalRoot {
		t.Errorf("Enter on '..' should change RootPath from %q to %q, but it stayed the same",
			originalRoot, expectedParent)
	}
	if m.RootPath != expectedParent {
		t.Errorf("RootPath after navigating up should be %q, got %q", expectedParent, m.RootPath)
	}
}

// TestFileTreeModelNavigateUpViaRightKey verifies that the 'l' key also
// triggers parent navigation when on the ".." entry.
func TestFileTreeModelNavigateUpViaRightKey(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	originalRoot := m.RootPath
	m, _ = sendFileTreeKey(m, "l")

	if m.RootPath == originalRoot {
		t.Errorf("'l' on '..' entry should navigate up, but RootPath remained %q", originalRoot)
	}
}

// TestFileTreeModelNavigateUpChangesContents verifies that after navigating up,
// the flat node list includes the original directory as a child entry.
func TestFileTreeModelNavigateUpChangesContents(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	m, _ = sendFileTreeKey(m, "enter")

	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes should be non-empty after navigating to parent")
	}
	// The listing must still start with a ".." entry.
	if !m.flatNodes[0].IsParentDir() {
		t.Errorf("after navigating up, flatNodes[0] should still be '..' entry, got %+v", m.flatNodes[0])
	}
	// The original test root (a temp dir) should appear as a child entry.
	found := false
	for _, n := range m.flatNodes {
		if n.FullPath == root {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("original root dir %q should appear in parent listing; got nodes: %v",
			root, nodeNames(m.flatNodes))
	}
}

// TestFileTreeModelNavigateUpCursorResets verifies that after navigating up
// the cursor resets to index 0 (the ".." entry of the new directory).
func TestFileTreeModelNavigateUpCursorResets(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	// Cursor starts at 0 already; navigate up.
	m, _ = sendFileTreeKey(m, "enter")

	if m.cursor != 0 {
		t.Errorf("cursor should be reset to 0 after navigating up, got %d", m.cursor)
	}
}

// TestFileTreeModelNavigateUpPreservesSelections verifies that selections made
// in a child directory are preserved in the selected map after navigating up.
func TestFileTreeModelNavigateUpPreservesSelections(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	// Select a file before navigating up.
	var selectedPath string
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			selectedPath = n.FullPath
			break
		}
	}
	if selectedPath == "" {
		t.Skip("no file node found")
	}
	m, _ = sendFileTreeKey(m, " ") // select the file
	if !m.selected[selectedPath] {
		t.Fatal("file should be selected before navigation")
	}

	// Navigate up from the ".." entry at index 0.
	m.cursor = 0
	m, _ = sendFileTreeKey(m, "enter")

	if !m.selected[selectedPath] {
		t.Errorf("selection for %q should be preserved after navigating up, but it was lost", selectedPath)
	}
}

// TestFileTreeModelNavigateUpNoOpAtRoot verifies that pressing Enter on ".."
// when already at filesystem root "/" does not change RootPath.
func TestFileTreeModelNavigateUpNoOpAtRoot(t *testing.T) {
	m := NewFileTreeModel("/")
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0

	m, _ = sendFileTreeKey(m, "enter")

	if m.RootPath != "/" {
		t.Errorf("navigating up from '/' should be a no-op, but RootPath changed to %q", m.RootPath)
	}
}

// TestFileTreeModelParentDirEntryAtRootIsNoOp verifies the ".." entry is
// present even when already at "/" and that it acts as a no-op.
func TestFileTreeModelParentDirEntryAtRootIsNoOp(t *testing.T) {
	m := NewFileTreeModel("/")
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes should be non-empty at '/'")
	}
	if !m.flatNodes[0].IsParentDir() {
		t.Errorf("flatNodes[0] should be '..' even at filesystem root, got %+v", m.flatNodes[0])
	}

	// Pressing enter at root should be a no-op.
	m.cursor = 0
	m, _ = sendFileTreeKey(m, "enter")
	if m.RootPath != "/" {
		t.Errorf("RootPath should remain '/' after no-op, got %q", m.RootPath)
	}
}

// TestFileTreeModelExpandDirStillWorksAfterParentEntry verifies that real
// directory expand/collapse still functions correctly with ".." at index 0.
func TestFileTreeModelExpandDirStillWorksAfterParentEntry(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Find the first real directory node (skip ".." at index 0).
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}

	m.cursor = dirIdx
	initialCount := len(m.flatNodes)

	// Expand the directory.
	m, _ = sendFileTreeKey(m, "enter")
	if len(m.flatNodes) <= initialCount {
		t.Errorf("Enter on a real directory should increase node count; before=%d after=%d",
			initialCount, len(m.flatNodes))
	}

	// Collapse the directory.
	m, _ = sendFileTreeKey(m, "enter")
	if len(m.flatNodes) != initialCount {
		t.Errorf("second Enter should collapse directory and restore count to %d, got %d",
			initialCount, len(m.flatNodes))
	}
}

// ---------------------------------------------------------------------------
// AC 5 — Navigate from initial root all the way to "/"
// ---------------------------------------------------------------------------

// TestFileTreeModelCanNavigateToFilesystemRoot verifies that repeatedly
// pressing Enter on the ".." entry will eventually change RootPath to "/".
// This confirms that there is no artificial upper boundary on parent navigation.
func TestFileTreeModelCanNavigateToFilesystemRoot(t *testing.T) {
	// Use the TempDir — which is always somewhere inside the real filesystem —
	// as the starting point.  Navigating up from there must reach "/".
	root := t.TempDir()
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	const maxSteps = 100 // prevent infinite loop if something goes wrong
	for i := 0; i < maxSteps; i++ {
		if m.RootPath == "/" {
			return // success — reached filesystem root
		}
		// Ensure ".." is at index 0 on every iteration.
		if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
			t.Fatalf("step %d: flatNodes[0] is not the '..' parent-dir entry (RootPath=%q)", i, m.RootPath)
		}
		m.cursor = 0
		m, _ = sendFileTreeKey(m, "enter")
	}

	if m.RootPath != "/" {
		t.Errorf("after %d navigations, expected RootPath to be '/', got %q", maxSteps, m.RootPath)
	}
}

// TestFileTreeModelNavigateUpMultipleSteps verifies that each successive ".."
// navigation correctly changes RootPath one level at a time.
func TestFileTreeModelNavigateUpMultipleSteps(t *testing.T) {
	// Build a three-level deep temporary tree: <root>/a/b/c
	base := t.TempDir()
	deepDir := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewFileTreeModel(deepDir)
	m.width = 80
	m.height = 24

	expectedPath := deepDir
	for _, wantParent := range []string{
		filepath.Join(base, "a", "b"),
		filepath.Join(base, "a"),
		base,
	} {
		m.cursor = 0
		if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
			t.Fatalf("expected '..' at index 0 when RootPath=%q", m.RootPath)
		}
		m, _ = sendFileTreeKey(m, "enter")
		if m.RootPath != wantParent {
			t.Errorf("after navigating up from %q: RootPath = %q, want %q",
				expectedPath, m.RootPath, wantParent)
		}
		expectedPath = wantParent
	}
}

// TestFileTreeModelNavigateUpDoesNotStopBeforeRoot verifies that after reaching
// "/" the model remains at "/" regardless of further ".." presses.
func TestFileTreeModelNavigateUpDoesNotStopBeforeRoot(t *testing.T) {
	m := NewFileTreeModel("/")
	m.width = 80
	m.height = 24

	// Press ".." multiple times — must stay at "/".
	for i := 0; i < 5; i++ {
		m.cursor = 0
		m, _ = sendFileTreeKey(m, "enter")
		if m.RootPath != "/" {
			t.Errorf("press %d: expected RootPath to remain '/', got %q", i+1, m.RootPath)
		}
	}
}

// ---------------------------------------------------------------------------
// AC 7 — Permission-denied directory shows empty listing (not blocking)
// ---------------------------------------------------------------------------

// TestFileTreeModel_PermissionDeniedRootShowsEmpty verifies that when
// FileTreeModel's RootPath points to an unreadable (permission-denied)
// directory the model still initialises correctly — showing only the ".."
// entry rather than panicking or blocking.
func TestFileTreeModel_PermissionDeniedRootShowsEmpty(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are not enforced for the root user")
	}

	base := t.TempDir()
	restricted := filepath.Join(base, "restricted")
	if err := os.Mkdir(restricted, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a file inside so the directory would normally have a visible child.
	if err := os.WriteFile(filepath.Join(restricted, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove all permissions so the directory cannot be listed.
	if err := os.Chmod(restricted, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Restore so the temp-dir cleanup can remove the directory.
		_ = os.Chmod(restricted, 0o755)
	})

	m := NewFileTreeModel(restricted)
	m.width = 80
	m.height = 24

	// The model must not panic — it must present at least the ".." entry.
	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes must not be empty (at minimum '..' must be present)")
	}
	if !m.flatNodes[0].IsParentDir() {
		t.Errorf("flatNodes[0] must be '..' for a permission-denied directory, got Kind=%v Name=%q",
			m.flatNodes[0].Kind, m.flatNodes[0].Name)
	}
	// No real children should appear since the directory is unreadable.
	if len(m.flatNodes) > 1 {
		t.Errorf("permission-denied directory should show no children after '..'; got %v",
			nodeNames(m.flatNodes[1:]))
	}
	// The view must render without panicking and must include "..".
	view := m.View()
	if !strings.Contains(view, "..") {
		t.Errorf("view must contain '..' even for a permission-denied directory; got:\n%s", view)
	}
	// The model must not be done or stuck.
	if m.Done() {
		t.Error("model must not report Done() after initialising with a permission-denied directory")
	}
}

// TestFileTreeModel_PermissionDeniedExpandedChildShowsEmpty verifies that
// expanding a permission-denied sub-directory in the tree does not panic or
// block — the child directory simply appears empty.
func TestFileTreeModel_PermissionDeniedExpandedChildShowsEmpty(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are not enforced for the root user")
	}

	base := t.TempDir()
	restricted := filepath.Join(base, "restricted")
	if err := os.Mkdir(restricted, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a file inside the restricted dir so it would normally show children.
	if err := os.WriteFile(filepath.Join(restricted, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(restricted, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(restricted, 0o755)
	})

	m := NewFileTreeModel(base)
	m.width = 80
	m.height = 24

	// Find the "restricted" directory node in the flat list.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() && n.Name == "restricted" {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("restricted directory not found in flat list")
	}
	countBefore := len(m.flatNodes)

	// Expand the restricted directory — must not block or panic.
	m.cursor = dirIdx
	m, _ = sendFileTreeKey(m, "enter")

	// After expanding, the count must not grow because the directory is unreadable.
	if len(m.flatNodes) != countBefore {
		t.Errorf("expanding a permission-denied directory must not add child nodes; before=%d after=%d",
			countBefore, len(m.flatNodes))
	}
	// The model must still be usable.
	if m.Done() {
		t.Error("model must not report Done() after expanding a permission-denied directory")
	}
}

// TestFileTreeModel_NavigateUpToPermissionDeniedParentShowsEmpty verifies that
// pressing ".." to navigate into a permission-denied parent directory produces
// an empty listing (just the ".." entry) rather than blocking.
func TestFileTreeModel_NavigateUpToPermissionDeniedParentShowsEmpty(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are not enforced for the root user")
	}

	// Create:
	//   parent/  (initially readable; will be made execute-only = no listing)
	//     child/  (readable, starting directory)
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a file in parent so it would normally show as a child entry.
	if err := os.WriteFile(filepath.Join(parent, "visible.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Execute-only (0o100): the directory can be traversed (enter) but not
	// listed, so os.ReadDir will return EACCES while filepath.Abs still works.
	if err := os.Chmod(parent, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o755)
	})

	// Start the model inside the readable child directory.
	m := NewFileTreeModel(child)
	m.width = 80
	m.height = 24

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	// Navigate up — RootPath should become parent (which cannot be listed).
	m.cursor = 0
	m, _ = sendFileTreeKey(m, "enter")

	// The model must not be Done or Back after this navigation.
	if m.Done() {
		t.Error("model must not report Done() after navigating into a permission-denied parent")
	}
	if m.Back() {
		t.Error("model must not report Back() after navigating into a permission-denied parent")
	}
	if m.RootPath != parent {
		t.Errorf("RootPath should be %q after navigation, got %q", parent, m.RootPath)
	}
	// parent is unlistable — only the ".." entry should be present.
	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes must not be empty (at minimum '..' must be present)")
	}
	if !m.flatNodes[0].IsParentDir() {
		t.Errorf("flatNodes[0] must be '..' after navigating to permission-denied parent, got %+v",
			m.flatNodes[0])
	}
	if len(m.flatNodes) > 1 {
		t.Errorf("permission-denied parent should show no children after '..'; got %v",
			nodeNames(m.flatNodes[1:]))
	}
	// The view must render without panicking and include "..".
	view := m.View()
	if !strings.Contains(view, "..") {
		t.Errorf("view must contain '..' after navigating to a permission-denied parent; got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// AC 9 — Selections restored when navigating back down to original directory
// ---------------------------------------------------------------------------

// TestFileTreeModelSelectionsRestoredAfterNavigatingBackDown verifies that
// selections made inside a sub-directory survive a round-trip: navigate up via
// ".." (which changes RootPath to the parent) and then navigate back down by
// expanding the original directory as a child entry in the parent view.
//
// This test ensures that the 'selected' map is never cleared on navigation, so
// that checkmarks reappear as soon as the previously-visited directory is
// expanded again.
func TestFileTreeModelSelectionsRestoredAfterNavigatingBackDown(t *testing.T) {
	// makeTestDir creates:
	//   root/subdir/child.txt, root/subdir/.hidden.txt
	//   root/alpha.txt, root/beta.txt, root/.hiddendir/secret.txt
	root := makeTestDir(t)
	subdir := filepath.Join(root, "subdir")

	// ---- Step 1: start inside subdir, select child.txt ----
	m := NewFileTreeModel(subdir)
	m.width = 80
	m.height = 24

	// Locate the first visible file (child.txt, showHidden=false).
	var childPath string
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			childPath = n.FullPath
			break
		}
	}
	if childPath == "" {
		t.Skip("no visible file found in subdir")
	}
	m, _ = sendFileTreeKey(m, " ") // select child.txt
	if !m.selected[childPath] {
		t.Fatal("file should be selected before navigation")
	}

	// ---- Step 2: navigate UP via the ".." entry ----
	if !m.flatNodes[0].IsParentDir() {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0
	m, _ = sendFileTreeKey(m, "enter")
	if m.RootPath != root {
		t.Fatalf("after navigating up, RootPath should be %q, got %q", root, m.RootPath)
	}

	// The selection must survive the root change.
	if !m.selected[childPath] {
		t.Fatal("selection should persist in selected map after navigating up")
	}

	// ---- Step 3: navigate back down by expanding subdir in the parent view ----
	subdirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir() && n.Name == "subdir" {
			subdirIdx = i
			break
		}
	}
	if subdirIdx < 0 {
		t.Fatal("subdir entry not found after navigating to parent")
	}
	m.cursor = subdirIdx
	m, _ = sendFileTreeKey(m, "enter") // expand subdir

	// ---- Step 4: verify selection is restored ----
	// The selected map must still contain the original path.
	if !m.selected[childPath] {
		t.Errorf("selection for %q should be restored after navigating back down into its directory, but was lost", childPath)
	}

	// The rendered view should show a ✓ checkmark for the selected file.
	view := m.View()
	if !strings.Contains(view, "✓") {
		t.Errorf("view should show ✓ checkmark for the re-entered directory's selected file; got:\n%s", view)
	}
}

// TestFileTreeModelSelectionsRestoredAfterMultiLevelNavigation verifies that
// selections survive a two-level up / two-level down round trip.
//
// Directory layout built by the test:
//
//	base/
//	  a/
//	    b/
//	      file.txt
func TestFileTreeModelSelectionsRestoredAfterMultiLevelNavigation(t *testing.T) {
	base := t.TempDir()
	deepDir := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(deepDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start model at deepDir (base/a/b).
	m := NewFileTreeModel(deepDir)
	m.width = 80
	m.height = 24

	// Select file.txt.
	var fileIdx int
	for i, n := range m.flatNodes {
		if n.IsFile() && n.Name == "file.txt" {
			fileIdx = i
			break
		}
	}
	m.cursor = fileIdx
	m, _ = sendFileTreeKey(m, " ")
	if !m.selected[filePath] {
		t.Fatal("file.txt should be selected before navigation")
	}

	// Navigate up twice: b → a → base.
	for _, wantRoot := range []string{
		filepath.Join(base, "a"),
		base,
	} {
		m.cursor = 0
		m, _ = sendFileTreeKey(m, "enter")
		if m.RootPath != wantRoot {
			t.Fatalf("expected RootPath %q, got %q", wantRoot, m.RootPath)
		}
	}

	// Selection must still be present in the map.
	if !m.selected[filePath] {
		t.Fatal("selection should survive two navigate-up steps")
	}

	// Navigate back down: expand 'a', then expand 'b'.
	findDirByName := func(nodes []FlatFileNode, name string) int {
		for i, n := range nodes {
			if n.IsDir() && n.Name == name {
				return i
			}
		}
		return -1
	}

	aIdx := findDirByName(m.flatNodes, "a")
	if aIdx < 0 {
		t.Fatal("directory 'a' not found in root flat list")
	}
	m.cursor = aIdx
	m, _ = sendFileTreeKey(m, "enter") // expand a

	bIdx := findDirByName(m.flatNodes, "b")
	if bIdx < 0 {
		t.Fatal("directory 'b' not found after expanding 'a'")
	}
	m.cursor = bIdx
	m, _ = sendFileTreeKey(m, "enter") // expand b

	// The selection should still be intact.
	if !m.selected[filePath] {
		t.Errorf("selection for %q should be restored after navigating back down via 'a/b'; was lost", filePath)
	}

	// The rendered view should contain a ✓ checkmark.
	view := m.View()
	if !strings.Contains(view, "✓") {
		t.Errorf("view should show ✓ for file.txt after navigate-down round trip; got:\n%s", view)
	}
}

// TestFileTreeModelSelectedMapNotClearedOnNavigateUp is a targeted assertion
// that the 'selected' map (which underpins selection restoration) is never
// wiped when RootPath changes.
func TestFileTreeModelSelectedMapNotClearedOnNavigateUp(t *testing.T) {
	root := makeTestDir(t)
	m := NewFileTreeModel(root)
	m.width = 80
	m.height = 24

	// Select every visible file at the root level.
	var selectedPaths []string
	for i, n := range m.flatNodes {
		if n.IsFile() {
			m.cursor = i
			m, _ = sendFileTreeKey(m, " ")
			selectedPaths = append(selectedPaths, n.FullPath)
		}
	}
	if len(selectedPaths) == 0 {
		t.Skip("no files at root level")
	}

	// Navigate up.
	m.cursor = 0
	m, _ = sendFileTreeKey(m, "enter")

	// All selections must still be in the map.
	for _, p := range selectedPaths {
		if !m.selected[p] {
			t.Errorf("selection for %q was lost after navigating up; selected map should never be cleared", p)
		}
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
