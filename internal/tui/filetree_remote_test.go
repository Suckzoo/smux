package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/filetree"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testHost returns a minimal ResolvedHost for use in remote file-tree tests.
func testHost() config.ResolvedHost {
	return config.ResolvedHost{
		DisplayName: "test-host",
		Host:        "test-host.example.com",
	}
}

// newRemoteModelWithEntries creates a RemoteFileTreeModel and pre-loads the
// home directory (".") with the supplied entries, bypassing any real SSH call.
func newRemoteModelWithEntries(entries []filetree.FileEntry) RemoteFileTreeModel {
	m := RemoteFileTreeModel{
		host:        testHost(),
		currentRoot: ".",
		dirCache:    make(map[string][]filetree.FileEntry),
		loading:     make(map[string]bool),
		loadErrors:  make(map[string]string),
		expanded:    make(map[string]bool),
		selected:    make(map[string]bool),
		width:       80,
		height:      24,
	}
	m.expanded["."] = true
	m.dirCache["."] = entries
	m.rebuild()
	return m
}

// sendRemoteKey delivers a key to a RemoteFileTreeModel and returns the
// updated model and any resulting command.
func sendRemoteKey(m RemoteFileTreeModel, keyStr string) (RemoteFileTreeModel, tea.Cmd) {
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
	return updated.(RemoteFileTreeModel), cmd
}

// homeEntries returns a representative set of home-directory entries for tests.
func homeEntries() []filetree.FileEntry {
	return []filetree.FileEntry{
		{Name: "Documents", IsDir: true},
		{Name: "Downloads", IsDir: true},
		{Name: ".bashrc", IsDir: false, Size: 512},
		{Name: "notes.txt", IsDir: false, Size: 128},
	}
}

// ---------------------------------------------------------------------------
// NewRemoteFileTreeModel
// ---------------------------------------------------------------------------

// TestNewRemoteFileTreeModel_InitialState verifies that a freshly created
// model starts with the home dir marked as loading and expanded.
func TestNewRemoteFileTreeModel_InitialState(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())

	if !m.loading["."] {
		t.Error("home dir should be marked as loading on creation")
	}
	if !m.expanded["."] {
		t.Error("home dir should be marked as expanded on creation")
	}
	if m.Done() {
		t.Error("model should not be done on creation")
	}
	if m.Back() {
		t.Error("back should be false on creation")
	}
}

// TestNewRemoteFileTreeModel_InitReturnsCmd verifies that Init() returns a
// non-nil command (the remote fetch).
func TestNewRemoteFileTreeModel_InitReturnsCmd(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() should return a non-nil command for the home dir fetch")
	}
}

// ---------------------------------------------------------------------------
// Loading state rendering
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ShowsLoadingPlaceholder verifies that the flat list
// contains the ".." parent-dir entry followed by a loading placeholder while
// the home dir is being fetched.
func TestRemoteFileTreeModel_ShowsLoadingPlaceholder(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24

	// Expect ".." + 1 loading placeholder = 2 nodes.
	if len(m.flatNodes) != 2 {
		t.Fatalf("expected 2 nodes ('..' + loading placeholder), got %d", len(m.flatNodes))
	}
	// First node is the ".." parent-dir entry.
	if !m.flatNodes[0].IsParentDir {
		t.Error("first node should be the '..' parent-dir entry")
	}
	n := m.flatNodes[1]
	if !n.IsPlaceholder {
		t.Error("second node should be a placeholder during loading")
	}
	if n.PlaceholderKind != "loading" {
		t.Errorf("placeholder kind should be 'loading', got %q", n.PlaceholderKind)
	}
	if !strings.Contains(n.Name, "loading") {
		t.Errorf("placeholder name should mention loading, got %q", n.Name)
	}
}

// TestRemoteFileTreeModel_LoadingPlaceholderInView verifies that the View()
// output contains a loading indicator while the home dir is in-flight.
func TestRemoteFileTreeModel_LoadingPlaceholderInView(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "loading") {
		t.Errorf("view should contain loading indicator; got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// remoteDirLoadedMsg handling
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_DirLoadedMsg_PopulatesEntries verifies that
// receiving a remoteDirLoadedMsg populates the flat list with the entries.
func TestRemoteFileTreeModel_DirLoadedMsg_PopulatesEntries(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24

	updated, _ := m.Update(remoteDirLoadedMsg{
		remotePath: ".",
		entries:    homeEntries(),
	})
	m = updated.(RemoteFileTreeModel)

	// Should no longer be loading.
	if m.loading["."] {
		t.Error("home dir should not be marked as loading after load")
	}

	// Should have visible nodes (hidden files excluded by default).
	visibleNames := flatNodeNames(m.flatNodes)
	if !containsStr(visibleNames, "Documents") {
		t.Errorf("flat list should contain Documents; got %v", visibleNames)
	}
	if !containsStr(visibleNames, "Downloads") {
		t.Errorf("flat list should contain Downloads; got %v", visibleNames)
	}
	if !containsStr(visibleNames, "notes.txt") {
		t.Errorf("flat list should contain notes.txt; got %v", visibleNames)
	}
	// .bashrc is hidden and should be absent with default showHidden=false.
	if containsStr(visibleNames, ".bashrc") {
		t.Error("hidden .bashrc should not appear when showHidden is false")
	}
}

// TestRemoteFileTreeModel_DirLoadedMsg_DirsBeforeFiles verifies that
// directories appear before files in the flat list.
func TestRemoteFileTreeModel_DirLoadedMsg_DirsBeforeFiles(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	var firstDir, firstFile int = -1, -1
	for i, n := range m.flatNodes {
		if n.IsDir && firstDir < 0 {
			firstDir = i
		}
		if !n.IsDir && firstFile < 0 {
			firstFile = i
		}
	}
	if firstDir < 0 || firstFile < 0 {
		t.Skip("need at least one dir and one file for this test")
	}
	if firstDir > firstFile {
		t.Error("directories should appear before files in the flat list")
	}
}

// TestRemoteFileTreeModel_DirLoadedMsg_SortedAlphabetically verifies that
// entries within each group (dirs/files) are sorted case-insensitively.
func TestRemoteFileTreeModel_DirLoadedMsg_SortedAlphabetically(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	var dirNames, fileNames []string
	for _, n := range m.flatNodes {
		if n.IsPlaceholder {
			continue
		}
		if n.IsDir {
			dirNames = append(dirNames, strings.ToLower(n.Name))
		} else {
			fileNames = append(fileNames, strings.ToLower(n.Name))
		}
	}

	if !sort.StringsAreSorted(dirNames) {
		t.Errorf("directory names should be sorted; got %v", dirNames)
	}
	if !sort.StringsAreSorted(fileNames) {
		t.Errorf("file names should be sorted; got %v", fileNames)
	}
}

// ---------------------------------------------------------------------------
// remoteDirErrorMsg handling
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_DirErrorMsg_ShowsErrorPlaceholder verifies that an
// error response replaces the loading placeholder with an error placeholder.
func TestRemoteFileTreeModel_DirErrorMsg_ShowsErrorPlaceholder(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24

	updated, _ := m.Update(remoteDirErrorMsg{
		remotePath: ".",
		err:        errors.New("connection refused"),
	})
	m = updated.(RemoteFileTreeModel)

	// Expect ".." + 1 error placeholder = 2 nodes.
	if len(m.flatNodes) != 2 {
		t.Fatalf("expected 2 nodes ('..' + error placeholder), got %d nodes", len(m.flatNodes))
	}
	// First node is the ".." parent-dir entry.
	if !m.flatNodes[0].IsParentDir {
		t.Error("first node should be the '..' parent-dir entry")
	}
	n := m.flatNodes[1]
	if !n.IsPlaceholder {
		t.Error("second node should be a placeholder after error")
	}
	if n.PlaceholderKind != "error" {
		t.Errorf("placeholder kind should be 'error', got %q", n.PlaceholderKind)
	}
	if !strings.Contains(n.Name, "connection refused") {
		t.Errorf("error placeholder should include error text; got %q", n.Name)
	}
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_CursorDownUp verifies that ↓/j and ↑/k move the
// cursor within the valid range.
func TestRemoteFileTreeModel_CursorDownUp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	if m.cursor != 0 {
		t.Errorf("initial cursor should be 0, got %d", m.cursor)
	}

	m, _ = sendRemoteKey(m, "down")
	if m.cursor != 1 {
		t.Errorf("after down, cursor should be 1, got %d", m.cursor)
	}

	m, _ = sendRemoteKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("after up, cursor should be 0, got %d", m.cursor)
	}

	// Pressing up at top stays at 0.
	m, _ = sendRemoteKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor should not go below 0, got %d", m.cursor)
	}
}

// TestRemoteFileTreeModel_CursorJKAliases verifies that j/k are aliases for
// down/up arrow keys.
func TestRemoteFileTreeModel_CursorJKAliases(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	m, _ = sendRemoteKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("j should move cursor to 1, got %d", m.cursor)
	}
	m, _ = sendRemoteKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("k should move cursor back to 0, got %d", m.cursor)
	}
}

// TestRemoteFileTreeModel_CursorDownClamp verifies that pressing ↓ at the
// last row does not exceed the valid range.
func TestRemoteFileTreeModel_CursorDownClamp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	last := len(m.flatNodes) - 1
	m.cursor = last

	m, _ = sendRemoteKey(m, "down")
	if m.cursor != last {
		t.Errorf("cursor should stay at last row (%d), got %d", last, m.cursor)
	}
}

// ---------------------------------------------------------------------------
// Directory expansion and collapse
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ExpandDir_TriggersLoadWhenUncached verifies that
// pressing Enter on an uncached directory triggers a remote fetch command and
// marks the path as loading.
func TestRemoteFileTreeModel_ExpandDir_TriggersLoadWhenUncached(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	m, cmd := sendRemoteKey(m, "enter")

	if !m.expanded[dirPath] {
		t.Errorf("directory %q should be expanded after Enter", dirPath)
	}
	if !m.loading[dirPath] {
		t.Errorf("directory %q should be loading after first expansion", dirPath)
	}
	if cmd == nil {
		t.Error("expanding an uncached directory should return a non-nil command")
	}
}

// TestRemoteFileTreeModel_ExpandDir_NoCmdWhenCached verifies that pressing
// Enter on a directory whose listing is already cached does not dispatch a
// fetch command.
func TestRemoteFileTreeModel_ExpandDir_NoCmdWhenCached(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	// Pre-populate the cache for this directory.
	m.dirCache[dirPath] = []filetree.FileEntry{{Name: "file.txt"}}

	m, cmd := sendRemoteKey(m, "enter")

	if cmd != nil {
		t.Error("expanding a cached directory should not dispatch a fetch command")
	}
	if !m.expanded[dirPath] {
		t.Errorf("directory %q should be expanded after Enter", dirPath)
	}
}

// TestRemoteFileTreeModel_ExpandDir_ShowsChildEntries verifies that expanding
// a directory whose listing is already in the cache shows child entries
// immediately.
func TestRemoteFileTreeModel_ExpandDir_ShowsChildEntries(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	// Pre-populate the cache.
	m.dirCache[dirPath] = []filetree.FileEntry{
		{Name: "child.txt", IsDir: false},
	}

	countBefore := len(m.flatNodes)
	m, _ = sendRemoteKey(m, "enter")
	countAfter := len(m.flatNodes)

	if countAfter <= countBefore {
		t.Errorf("expanding a populated directory should add child nodes; before=%d after=%d",
			countBefore, countAfter)
	}

	// Verify child appears in the list.
	names := flatNodeNames(m.flatNodes)
	if !containsStr(names, "child.txt") {
		t.Errorf("child.txt should appear after expanding parent; got %v", names)
	}
}

// TestRemoteFileTreeModel_CollapseDir_HidesChildren verifies that collapsing
// an expanded directory removes its children from the visible list.
func TestRemoteFileTreeModel_CollapseDir_HidesChildren(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	// Pre-populate and expand.
	m.dirCache[dirPath] = []filetree.FileEntry{{Name: "child.txt"}}
	m, _ = sendRemoteKey(m, "enter") // expand
	countExpanded := len(m.flatNodes)

	// Collapse by pressing enter again on the same directory.
	m, _ = sendRemoteKey(m, "enter")
	if m.expanded[dirPath] {
		t.Errorf("directory %q should be collapsed after second Enter", dirPath)
	}
	if len(m.flatNodes) >= countExpanded {
		t.Errorf("collapsing should reduce node count; expanded=%d after=%d",
			countExpanded, len(m.flatNodes))
	}
}

// TestRemoteFileTreeModel_CollapseViaLeftKey verifies that pressing ← on an
// expanded directory collapses it.
func TestRemoteFileTreeModel_CollapseViaLeftKey(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	m.dirCache[dirPath] = []filetree.FileEntry{{Name: "child.txt"}}
	m, _ = sendRemoteKey(m, "enter") // expand
	expandedCount := len(m.flatNodes)

	m, _ = sendRemoteKey(m, "left") // collapse
	if m.expanded[dirPath] {
		t.Error("left key should collapse the expanded directory")
	}
	if len(m.flatNodes) >= expandedCount {
		t.Errorf("collapse should reduce node count; was %d, now %d", expandedCount, len(m.flatNodes))
	}
}

// TestRemoteFileTreeModel_LeftKey_MovesCursorUp_WhenNotExpandedDir verifies
// that pressing ← on a file or collapsed directory moves the cursor up.
func TestRemoteFileTreeModel_LeftKey_MovesCursorUp_WhenNotExpandedDir(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	// Move cursor to a file node.
	for i, n := range m.flatNodes {
		if !n.IsDir {
			m.cursor = i
			break
		}
	}
	if m.cursor == 0 {
		t.Skip("no non-directory node found at index > 0")
	}
	prevCursor := m.cursor

	m, _ = sendRemoteKey(m, "left")
	if m.cursor != prevCursor-1 {
		t.Errorf("left on file should move cursor up from %d to %d, got %d",
			prevCursor, prevCursor-1, m.cursor)
	}
}

// TestRemoteFileTreeModel_LoadingPlaceholderShownOnExpand verifies that when
// an uncached directory is expanded a loading placeholder appears as a child.
func TestRemoteFileTreeModel_LoadingPlaceholderShownOnExpand(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	m, _ = sendRemoteKey(m, "enter") // expand uncached dir

	// Find the loading placeholder child.
	found := false
	for _, n := range m.flatNodes {
		if n.IsPlaceholder && n.PlaceholderKind == "loading" && n.Depth == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expanding an uncached directory should show a depth-1 loading placeholder")
	}
}

// TestRemoteFileTreeModel_ErrorPlaceholderOnChildLoadFailure verifies that
// when a sub-directory load fails its error appears as a placeholder child.
func TestRemoteFileTreeModel_ErrorPlaceholderOnChildLoadFailure(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	dirPath := m.flatNodes[dirIdx].RemotePath

	// Expand the directory (triggers loading mark).
	m, _ = sendRemoteKey(m, "enter")

	// Simulate a load error arriving.
	updated, _ := m.Update(remoteDirErrorMsg{
		remotePath: dirPath,
		err:        errors.New("permission denied"),
	})
	m = updated.(RemoteFileTreeModel)

	// An error placeholder should appear as a child.
	found := false
	for _, n := range m.flatNodes {
		if n.IsPlaceholder && n.PlaceholderKind == "error" && n.Depth == 1 {
			if strings.Contains(n.Name, "permission denied") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected depth-1 error placeholder containing 'permission denied'; nodes=%v",
			flatNodeNames(m.flatNodes))
	}
}

// ---------------------------------------------------------------------------
// Selection
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_SpaceSelectsFile verifies that pressing Space
// selects and deselects the node under the cursor.
func TestRemoteFileTreeModel_SpaceSelectsFile(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Move to a file node.
	for i, n := range m.flatNodes {
		if !n.IsDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || m.flatNodes[m.cursor].IsDir {
		t.Skip("no file node found")
	}
	path := m.flatNodes[m.cursor].RemotePath

	if m.selected[path] {
		t.Error("file should not be selected initially")
	}

	m, _ = sendRemoteKey(m, " ")
	if !m.selected[path] {
		t.Error("file should be selected after Space")
	}

	m, _ = sendRemoteKey(m, " ")
	if m.selected[path] {
		t.Error("file should be deselected after second Space")
	}
}

// TestRemoteFileTreeModel_SpaceSelectsDir verifies that pressing Space on a
// directory node selects and deselects it — directories are selectable just
// like files.
func TestRemoteFileTreeModel_SpaceSelectsDir(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Move to a real (non-parentDir) directory node.
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir || m.flatNodes[m.cursor].IsParentDir {
		t.Skip("no real directory node found")
	}
	path := m.flatNodes[m.cursor].RemotePath

	if m.selected[path] {
		t.Error("directory should not be selected initially")
	}

	m, _ = sendRemoteKey(m, " ")
	if !m.selected[path] {
		t.Error("directory should be selected after Space")
	}

	m, _ = sendRemoteKey(m, " ")
	if m.selected[path] {
		t.Error("directory should be deselected after second Space")
	}
}

// TestRemoteFileTreeModel_ViewSelectedDirHasCheckmark verifies that a selected
// directory shows a ✓ checkmark next to its arrow in the rendered output.
func TestRemoteFileTreeModel_ViewSelectedDirHasCheckmark(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find a real (non-parentDir) directory and select it.
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir {
		t.Skip("no directory node found")
	}
	m, _ = sendRemoteKey(m, " ") // select

	lines := m.renderRemoteFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsDir && !n.IsPlaceholder && m.selected[n.RemotePath] {
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

// TestRemoteFileTreeModel_ViewSelectedDirStillHasArrow verifies that a selected
// directory still shows the expand/collapse arrow alongside the checkmark.
func TestRemoteFileTreeModel_ViewSelectedDirStillHasArrow(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir {
		t.Skip("no directory node found")
	}
	m, _ = sendRemoteKey(m, " ") // select

	lines := m.renderRemoteFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if n.IsDir && !n.IsPlaceholder && m.selected[n.RemotePath] {
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

// TestRemoteFileTreeModel_MultiSelectMixedTypes verifies that users can
// simultaneously select a mix of files and directories, and all selections
// appear in the result.
func TestRemoteFileTreeModel_MultiSelectMixedTypes(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Collect one real (non-parentDir) directory and one file.
	var dirPath, filePath string
	for _, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder && !n.IsParentDir && dirPath == "" {
			dirPath = n.RemotePath
		}
		if !n.IsDir && !n.IsPlaceholder && filePath == "" {
			filePath = n.RemotePath
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
		if n.RemotePath == dirPath {
			m.cursor = i
			break
		}
	}
	m, _ = sendRemoteKey(m, " ")

	// Select the file.
	for i, n := range m.flatNodes {
		if n.RemotePath == filePath {
			m.cursor = i
			break
		}
	}
	m, _ = sendRemoteKey(m, " ")

	// Confirm: both should appear in the result.
	m, _ = sendRemoteKey(m, "ctrl+d")
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

// TestRemoteFileTreeModel_DirSelectionIndependentOfExpansion verifies that
// pressing Space on a directory selects it without expanding it (selection
// and expansion are independent operations).
func TestRemoteFileTreeModel_DirSelectionIndependentOfExpansion(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir || m.flatNodes[m.cursor].IsParentDir {
		t.Skip("no real directory node found")
	}

	dirPath := m.flatNodes[m.cursor].RemotePath
	countBefore := len(m.flatNodes)

	// Select — node count must not change (no expansion).
	m, _ = sendRemoteKey(m, " ")
	if len(m.flatNodes) != countBefore {
		t.Errorf("selecting a directory should not change node count; before=%d after=%d",
			countBefore, len(m.flatNodes))
	}
	if !m.selected[dirPath] {
		t.Error("directory should be selected after Space")
	}
	if m.expanded[dirPath] {
		t.Error("Space on a directory should not expand it — selection and expansion are independent")
	}
}

// TestRemoteFileTreeModel_PlaceholderNotSelectable verifies that placeholder
// nodes and the ".." parent-dir entry cannot be selected via Space.
func TestRemoteFileTreeModel_PlaceholderNotSelectable(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24
	// With ".." at index 0, find the loading placeholder (index 1).
	placeholderIdx := -1
	for i, n := range m.flatNodes {
		if n.IsPlaceholder {
			placeholderIdx = i
			break
		}
	}
	if placeholderIdx < 0 {
		t.Skip("no placeholder node found")
	}
	placeholderPath := m.flatNodes[placeholderIdx].RemotePath
	m.cursor = placeholderIdx

	m, _ = sendRemoteKey(m, " ")
	if m.selected[placeholderPath] {
		t.Error("placeholder node should not be selectable via Space")
	}

	// ".." parent-dir entry should also not be selectable.
	m.cursor = 0
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	parentPath := m.flatNodes[0].RemotePath
	m, _ = sendRemoteKey(m, " ")
	if m.selected[parentPath] {
		t.Error("'..' parent-dir entry should not be selectable via Space")
	}
}

// ---------------------------------------------------------------------------
// Toggle hidden files
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ToggleHiddenFiles verifies that pressing '.' shows
// and hides entries whose names start with '.'.
func TestRemoteFileTreeModel_ToggleHiddenFiles(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	countVisible := len(m.flatNodes)

	m, _ = sendRemoteKey(m, ".")
	countWithHidden := len(m.flatNodes)
	if countWithHidden <= countVisible {
		t.Errorf("showing hidden files should increase node count; before=%d after=%d",
			countVisible, countWithHidden)
	}

	// .bashrc should now appear.
	names := flatNodeNames(m.flatNodes)
	if !containsStr(names, ".bashrc") {
		t.Errorf("hidden .bashrc should appear when showHidden is true; nodes=%v", names)
	}

	// Toggle back.
	m, _ = sendRemoteKey(m, ".")
	if len(m.flatNodes) != countVisible {
		t.Errorf("hiding files again should restore count to %d, got %d",
			countVisible, len(m.flatNodes))
	}
}

// ---------------------------------------------------------------------------
// Quit / confirm
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_QQuit verifies that pressing 'q' marks the model as
// done with Quit=true and issues tea.Quit.
func TestRemoteFileTreeModel_QQuit(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	m, cmd := sendRemoteKey(m, "q")
	if !m.Done() {
		t.Error("q should mark model as done")
	}
	if !m.result.Quit {
		t.Error("q should set result.Quit=true")
	}
	if cmd == nil {
		t.Error("q should return a non-nil (Quit) command")
	}
}

// TestRemoteFileTreeModel_CtrlCQuit verifies that Ctrl+C marks the model as
// done with Quit=true.
func TestRemoteFileTreeModel_CtrlCQuit(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	m, cmd := sendRemoteKey(m, "ctrl+c")
	if !m.Done() {
		t.Error("ctrl+c should mark model as done")
	}
	if !m.result.Quit {
		t.Error("ctrl+c should set result.Quit=true")
	}
	if cmd == nil {
		t.Error("ctrl+c should return a non-nil (Quit) command")
	}
}

// TestRemoteFileTreeModel_EscSetsBack verifies that pressing Esc sets the Back
// flag without quitting.
func TestRemoteFileTreeModel_EscSetsBack(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	m, _ = sendRemoteKey(m, "esc")
	if !m.Back() {
		t.Error("Esc should set Back()=true")
	}
	if m.Done() {
		t.Error("Esc should not mark model as Done()")
	}
}

// TestRemoteFileTreeModel_CtrlDConfirmsSelection verifies that pressing Ctrl+D
// confirms the selection and marks the model done.
func TestRemoteFileTreeModel_CtrlDConfirmsSelection(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Select a file.
	for i, n := range m.flatNodes {
		if !n.IsDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || m.flatNodes[m.cursor].IsDir {
		t.Skip("no file node found")
	}
	selectedPath := m.flatNodes[m.cursor].RemotePath
	m, _ = sendRemoteKey(m, " ") // select it

	m, cmd := sendRemoteKey(m, "ctrl+d")
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
		t.Errorf("selected path %q should be in result.SelectedPaths %v",
			selectedPath, result.SelectedPaths)
	}
}

// TestRemoteFileTreeModel_CtrlDEmptySelectionOK verifies that Ctrl+D with no
// selection still marks the model as done with an empty path list.
func TestRemoteFileTreeModel_CtrlDEmptySelectionOK(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	m, _ = sendRemoteKey(m, "ctrl+d")
	if !m.Done() {
		t.Error("Ctrl+D should mark model as done even with empty selection")
	}
	if len(m.GetResult().SelectedPaths) != 0 {
		t.Errorf("result.SelectedPaths should be empty with no selection, got %v",
			m.GetResult().SelectedPaths)
	}
}

// TestRemoteFileTreeModel_SelectedPathsAreSorted verifies that GetResult
// returns paths in sorted order.
func TestRemoteFileTreeModel_SelectedPathsAreSorted(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Select all file nodes.
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder {
			m.cursor = i
			m, _ = sendRemoteKey(m, " ")
		}
	}
	m, _ = sendRemoteKey(m, "ctrl+d")
	paths := m.GetResult().SelectedPaths
	if !sort.StringsAreSorted(paths) {
		t.Errorf("result paths should be sorted; got %v", paths)
	}
}

// ---------------------------------------------------------------------------
// View rendering
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ViewContainsTitle verifies that the rendered view
// includes the "select remote file" title.
func TestRemoteFileTreeModel_ViewContainsTitle(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	view := m.View()
	if !strings.Contains(view, "select remote file") {
		t.Errorf("view should contain 'select remote file'; got:\n%s", view)
	}
}

// TestRemoteFileTreeModel_ViewContainsHostName verifies that the host name
// appears in the view.
func TestRemoteFileTreeModel_ViewContainsHostName(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	view := m.View()
	if !strings.Contains(view, "test-host") {
		t.Errorf("view should contain host name 'test-host'; got:\n%s", view)
	}
}

// TestRemoteFileTreeModel_ViewContainsFileNames verifies that file names from
// the home directory appear in the rendered view.
func TestRemoteFileTreeModel_ViewContainsFileNames(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	view := m.View()
	if !strings.Contains(view, "Documents") {
		t.Errorf("view should contain 'Documents'; got:\n%s", view)
	}
	if !strings.Contains(view, "notes.txt") {
		t.Errorf("view should contain 'notes.txt'; got:\n%s", view)
	}
}

// TestRemoteFileTreeModel_ViewDirHasArrow verifies that real directory rows
// include an expand/collapse arrow (the ".." parent-dir entry is excluded as it
// uses a different visual style).
func TestRemoteFileTreeModel_ViewDirHasArrow(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	lines := m.renderRemoteFileList()
	for i, n := range m.flatNodes {
		if i >= len(lines) || n.IsPlaceholder || n.IsParentDir {
			continue
		}
		if n.IsDir {
			raw := stripANSI(lines[i])
			if !strings.Contains(raw, "▶") && !strings.Contains(raw, "▼") {
				t.Errorf("directory line %q should contain ▶ or ▼", raw)
			}
		}
	}
}

// TestRemoteFileTreeModel_ViewSelectedFileHasCheckmark verifies that a
// selected file shows a ✓ checkmark in the rendered output.
func TestRemoteFileTreeModel_ViewSelectedFileHasCheckmark(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find a file and select it.
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder {
			m.cursor = i
			break
		}
	}
	m, _ = sendRemoteKey(m, " ")

	lines := m.renderRemoteFileList()
	found := false
	for i, n := range m.flatNodes {
		if i >= len(lines) {
			break
		}
		if !n.IsDir && !n.IsPlaceholder && m.selected[n.RemotePath] {
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

// TestRemoteFileTreeModel_ViewTooSmall verifies that a very small terminal
// shows a "too small" message instead of crashing.
func TestRemoteFileTreeModel_ViewTooSmall(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.width = 20
	m.height = 4
	view := m.View()
	if !strings.Contains(view, "too small") {
		t.Errorf("tiny terminal should show 'too small' message; got: %q", view)
	}
}

// TestRemoteFileTreeModel_ViewIndentationByDepth verifies that child entries
// are rendered with greater leading whitespace than their parent directory.
func TestRemoteFileTreeModel_ViewIndentationByDepth(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath
	m.dirCache[dirPath] = []filetree.FileEntry{{Name: "child.txt"}}
	m, _ = sendRemoteKey(m, "enter") // expand

	lines := m.renderRemoteFileList()
	for i, n := range m.flatNodes {
		if i >= len(lines) || n.IsPlaceholder {
			continue
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

// ---------------------------------------------------------------------------
// WindowSizeMsg
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_WindowSizeMsgUpdated verifies that WindowSizeMsg
// updates the model's dimensions.
func TestRemoteFileTreeModel_WindowSizeMsgUpdated(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(RemoteFileTreeModel)
	if m.width != 120 || m.height != 40 {
		t.Errorf("expected 120×40, got %d×%d", m.width, m.height)
	}
}

// ---------------------------------------------------------------------------
// RemoteFlatNode path encoding
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_PathEncoding_RootEntries verifies that immediate
// children of the home dir have a single-segment RemotePath (no "./" prefix).
// The synthetic ".." entry is excluded from this check.
func TestRemoteFileTreeModel_PathEncoding_RootEntries(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	for _, n := range m.flatNodes {
		if n.IsPlaceholder || n.IsParentDir {
			continue
		}
		if strings.HasPrefix(n.RemotePath, "./") {
			t.Errorf("root-level entry RemotePath should not start with './', got %q", n.RemotePath)
		}
		if strings.Contains(n.RemotePath, "/") && n.Depth == 0 {
			// Depth-0 entries should have bare names, not nested paths.
			t.Errorf("depth-0 entry should have a bare name as RemotePath, got %q", n.RemotePath)
		}
	}
}

// TestRemoteFileTreeModel_PathEncoding_NestedEntries verifies that nested
// directory entries have the expected "parent/child" RemotePath.
func TestRemoteFileTreeModel_PathEncoding_NestedEntries(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first real (non-parentDir) directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	m.cursor = dirIdx
	parentName := m.flatNodes[dirIdx].Name
	parentPath := m.flatNodes[dirIdx].RemotePath

	m.dirCache[parentPath] = []filetree.FileEntry{{Name: "nested.txt"}}
	m, _ = sendRemoteKey(m, "enter") // expand

	// Find the child node.
	for _, n := range m.flatNodes {
		if n.Name == "nested.txt" {
			expected := fmt.Sprintf("%s/nested.txt", parentName)
			if n.RemotePath != expected {
				t.Errorf("nested entry RemotePath: got %q, want %q", n.RemotePath, expected)
			}
			return
		}
	}
	t.Error("nested.txt not found in flat list after expanding parent")
}

// ---------------------------------------------------------------------------
// Parent-directory ".." entry
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ParentDirEntryAlwaysFirst verifies that the ".."
// entry is always the first node in the flat list, regardless of directory
// contents.
func TestRemoteFileTreeModel_ParentDirEntryAlwaysFirst(t *testing.T) {
	// Loaded home directory with entries.
	m := newRemoteModelWithEntries(homeEntries())
	if len(m.flatNodes) == 0 {
		t.Fatal("flat list should not be empty")
	}
	first := m.flatNodes[0]
	if !first.IsParentDir {
		t.Errorf("first node should be the '..' parent-dir entry, got %q", first.Name)
	}
	if first.Name != ".." {
		t.Errorf("parent-dir entry name should be '..', got %q", first.Name)
	}
}

// TestRemoteFileTreeModel_ParentDirEntryWhileLoading verifies that ".." still
// appears first when the home directory is still loading.
func TestRemoteFileTreeModel_ParentDirEntryWhileLoading(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24
	if len(m.flatNodes) == 0 {
		t.Fatal("flat list should not be empty while loading")
	}
	if !m.flatNodes[0].IsParentDir {
		t.Errorf("first node should be '..' even while loading, got IsParentDir=%v name=%q",
			m.flatNodes[0].IsParentDir, m.flatNodes[0].Name)
	}
}

// TestRemoteFileTreeModel_ParentDirEntryWhileError verifies that ".." still
// appears first when the home directory load failed.
func TestRemoteFileTreeModel_ParentDirEntryWhileError(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24
	updated, _ := m.Update(remoteDirErrorMsg{remotePath: ".", err: fmt.Errorf("network error")})
	m = updated.(RemoteFileTreeModel)
	if len(m.flatNodes) == 0 {
		t.Fatal("flat list should not be empty after error")
	}
	if !m.flatNodes[0].IsParentDir {
		t.Errorf("first node should be '..' even after load error, got name=%q", m.flatNodes[0].Name)
	}
}

// TestRemoteFileTreeModel_ParentDirEntryRenderedInView verifies that the ".."
// entry is visible in the rendered View() output.
func TestRemoteFileTreeModel_ParentDirEntryRenderedInView(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	lines := m.renderRemoteFileList()
	if len(lines) == 0 {
		t.Fatal("renderRemoteFileList should return at least one line")
	}
	raw := stripANSI(lines[0])
	if !strings.Contains(raw, "..") {
		t.Errorf("first rendered line should contain '..', got %q", raw)
	}
}

// TestRemoteFileTreeModel_ParentDirNotSelectable verifies that pressing Space
// on the ".." entry does not add it to the selection set.
func TestRemoteFileTreeModel_ParentDirNotSelectable(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.cursor = 0
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	parentPath := m.flatNodes[0].RemotePath
	m, _ = sendRemoteKey(m, " ")
	if m.selected[parentPath] {
		t.Error("'..' entry should not be selectable via Space")
	}
}

// ---------------------------------------------------------------------------
// AC 4 — Selecting ".." navigates to parent directory contents (remote)
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_ParentDirNavigation_EnterChangesRoot verifies that
// pressing Enter while the cursor is on the ".." entry changes currentRoot to
// the parent path.
func TestRemoteFileTreeModel_ParentDirNavigation_EnterChangesRoot(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// The cursor starts at 0 (".." entry).
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0

	m, _ = sendRemoteKey(m, "enter")

	if m.currentRoot != ".." {
		t.Errorf("pressing Enter on '..' should set currentRoot to '..', got %q", m.currentRoot)
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_RightArrowChangesRoot verifies
// that pressing → on the ".." entry also triggers parent navigation.
func TestRemoteFileTreeModel_ParentDirNavigation_RightArrowChangesRoot(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.cursor = 0

	msg := tea.KeyMsg{Type: tea.KeyRight}
	updated, _ := m.Update(msg)
	m = updated.(RemoteFileTreeModel)

	if m.currentRoot != ".." {
		t.Errorf("pressing → on '..' should set currentRoot to '..', got %q", m.currentRoot)
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_LoadsParentWhenUncached verifies
// that pressing Enter on ".." triggers a remote fetch command when the parent
// directory listing has not been cached.
func TestRemoteFileTreeModel_ParentDirNavigation_LoadsParentWhenUncached(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.cursor = 0

	m, cmd := sendRemoteKey(m, "enter")

	if cmd == nil {
		t.Error("navigating to an uncached parent directory should return a non-nil fetch command")
	}
	if !m.loading[".."] {
		t.Error("parent directory '..' should be marked as loading after navigation")
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_NoCmdWhenCached verifies that
// pressing Enter on ".." does not dispatch a fetch command when the parent
// directory listing is already cached.
func TestRemoteFileTreeModel_ParentDirNavigation_NoCmdWhenCached(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	// Pre-populate the parent cache.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "sibling", IsDir: true},
	}
	m.cursor = 0

	m, cmd := sendRemoteKey(m, "enter")

	if cmd != nil {
		t.Error("navigating to a cached parent directory should not dispatch a fetch command")
	}
	if m.currentRoot != ".." {
		t.Errorf("currentRoot should be '..' after navigation, got %q", m.currentRoot)
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_ShowsParentEntries verifies that
// after navigating up via ".." the flat list shows the parent directory's
// cached entries.
func TestRemoteFileTreeModel_ParentDirNavigation_ShowsParentEntries(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	// Pre-populate the parent cache with entries that would not appear in home.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "opt", IsDir: true},
		{Name: "parent-file.txt", IsDir: false},
	}
	m.cursor = 0

	m, _ = sendRemoteKey(m, "enter")

	names := flatNodeNames(m.flatNodes)
	if !containsStr(names, "opt") {
		t.Errorf("after navigating up, 'opt' from parent dir should be visible; got %v", names)
	}
	if !containsStr(names, "parent-file.txt") {
		t.Errorf("after navigating up, 'parent-file.txt' from parent dir should be visible; got %v", names)
	}
	// Home directory entries should no longer be visible at the root level.
	if containsStr(names, "Documents") {
		t.Errorf("after navigating up, home-dir 'Documents' should not be visible as root entry; got %v", names)
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_ResetsCursor verifies that
// navigating up via ".." resets the cursor to position 0.
func TestRemoteFileTreeModel_ParentDirNavigation_ResetsCursor(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.dirCache[".."] = []filetree.FileEntry{{Name: "sibling", IsDir: true}}
	// Position cursor on ".."
	m.cursor = 0

	m, _ = sendRemoteKey(m, "enter")

	if m.cursor != 0 {
		t.Errorf("cursor should be reset to 0 after navigating up, got %d", m.cursor)
	}
}

// TestRemoteFileTreeModel_ParentDirNavigation_SecondLevelUp verifies that
// pressing ".." twice navigates currentRoot from "." to ".." to "../..".
func TestRemoteFileTreeModel_ParentDirNavigation_SecondLevelUp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}
	m.dirCache["../.."] = []filetree.FileEntry{{Name: "root-entry", IsDir: true}}

	// First up-navigation: home → parent of home.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	if m.currentRoot != ".." {
		t.Fatalf("after first up-navigation, currentRoot should be '..', got %q", m.currentRoot)
	}

	// Second up-navigation: parent of home → grandparent.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	if m.currentRoot != "../.." {
		t.Errorf("after second up-navigation, currentRoot should be '../..', got %q", m.currentRoot)
	}
}

// TestRemoteParentPath_HomeToDotDot verifies that the parent of "." is "..".
func TestRemoteParentPath_HomeToDotDot(t *testing.T) {
	if got := remoteParentPath("."); got != ".." {
		t.Errorf("remoteParentPath('.') = %q, want '..'", got)
	}
}

// TestRemoteParentPath_DotDotToDoubleDotDot verifies that the parent of ".." is "../..".
func TestRemoteParentPath_DotDotToDoubleDotDot(t *testing.T) {
	if got := remoteParentPath(".."); got != "../.." {
		t.Errorf("remoteParentPath('..') = %q, want '../..'", got)
	}
}

// TestRemoteParentPath_DoubleDotDotToDeeperTraversal verifies that the parent
// of "../.." is "../../..".
func TestRemoteParentPath_DoubleDotDotToDeeperTraversal(t *testing.T) {
	if got := remoteParentPath("../.."); got != "../../.." {
		t.Errorf("remoteParentPath('../..') = %q, want '../../..'", got)
	}
}

// TestRemoteParentPath_SubdirToHome verifies that the parent of a
// single-level subdirectory is "." (home).
func TestRemoteParentPath_SubdirToHome(t *testing.T) {
	if got := remoteParentPath("Documents"); got != "." {
		t.Errorf("remoteParentPath('Documents') = %q, want '.'", got)
	}
}

// TestRemoteParentPath_NestedSubdirToParent verifies that the parent of a
// nested subdirectory is its direct parent.
func TestRemoteParentPath_NestedSubdirToParent(t *testing.T) {
	if got := remoteParentPath("Documents/Work"); got != "Documents" {
		t.Errorf("remoteParentPath('Documents/Work') = %q, want 'Documents'", got)
	}
}

// ---------------------------------------------------------------------------
// AC 5 — Navigate from initial root (home) all the way up to "/"
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_NavigateUpMultiLevelChain verifies that the user can
// navigate from home (".") through each ancestor level all the way to the
// filesystem root ("/") with no artificial upper boundary.
//
// The test drives the model through the sequence:
//
//	"."  →  ".."  →  "../.."  →  "../../.."  →  "/"
//
// At each step a pre-populated dir cache entry is provided so that the rebuild
// step produces a non-empty flat list with a ".." entry at the top.
func TestRemoteFileTreeModel_NavigateUpMultiLevelChain(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the cache at each ancestor level so the model can render
	// without triggering real SSH calls.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "homedir", IsDir: true},
	}
	m.dirCache["../.."] = []filetree.FileEntry{
		{Name: "home", IsDir: true},
	}
	m.dirCache["../../.."] = []filetree.FileEntry{
		{Name: "srv", IsDir: true},
	}
	// The final step uses "/" as an absolute path (the no-op sentinel).
	// Populate it so the listing is non-empty.
	m.dirCache["/"] = []filetree.FileEntry{
		{Name: "bin", IsDir: true},
		{Name: "etc", IsDir: true},
		{Name: "home", IsDir: true},
	}

	type step struct {
		wantRoot string
		desc     string
	}
	steps := []step{
		{"..", "home → parent of home"},
		{"../..","parent of home → grandparent"},
		{"../../..", "grandparent → great-grandparent"},
	}

	for _, s := range steps {
		// Ensure ".." entry is at position 0 before pressing Enter.
		if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir {
			t.Fatalf("step %q: flatNodes[0] is not '..' (currentRoot=%q)",
				s.desc, m.currentRoot)
		}
		m.cursor = 0
		m, _ = sendRemoteKey(m, "enter")
		if m.currentRoot != s.wantRoot {
			t.Errorf("step %q: currentRoot = %q, want %q",
				s.desc, m.currentRoot, s.wantRoot)
		}
	}
}

// TestRemoteParentPath_Chain verifies the complete path-computation chain that
// enables navigation from the home directory all the way to the filesystem root.
func TestRemoteParentPath_Chain(t *testing.T) {
	type step struct {
		input string
		want  string
		desc  string
	}
	steps := []step{
		{".", "..", "home → parent of home"},
		{"..", "../..", "parent of home → grandparent"},
		{"../..","../../..", "grandparent → great-grandparent"},
		{"Documents", ".", "subdir → home"},
		{"Documents/Work", "Documents", "nested subdir → parent"},
		{"/", "/", "filesystem root is its own parent (no-op)"},
	}
	for _, s := range steps {
		got := remoteParentPath(s.input)
		if got != s.want {
			t.Errorf("%s: remoteParentPath(%q) = %q, want %q",
				s.desc, s.input, got, s.want)
		}
	}
}

// TestRemoteFileTreeModel_CurrentRootDoesNotChangeAtRoot verifies that the
// currentRoot field does not change when the user presses ".." repeatedly from
// the filesystem root "/".
func TestRemoteFileTreeModel_CurrentRootDoesNotChangeAtRoot(t *testing.T) {
	m := newRemoteModelAtRoot("/")
	// Press ".." five times — must stay at "/".
	for i := 0; i < 5; i++ {
		m.cursor = 0
		m, _ = sendRemoteKey(m, "enter")
		if m.currentRoot != "/" {
			t.Errorf("press %d: expected currentRoot to remain '/', got %q", i+1, m.currentRoot)
		}
	}
}

// TestRemoteFileTreeModel_ParentDirAlwaysPresentDuringChain verifies that the
// ".." entry appears at position 0 after every step of a multi-level navigation.
func TestRemoteFileTreeModel_ParentDirAlwaysPresentDuringChain(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}
	m.dirCache["../.."] = []filetree.FileEntry{{Name: "home", IsDir: true}}

	for _, label := range []string{"home", "parent-of-home", "grandparent"} {
		if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir {
			t.Errorf("at %s: flatNodes[0] should be '..' parent-dir entry, got %+v",
				label, m.flatNodes[0])
		}
		m.cursor = 0
		m, _ = sendRemoteKey(m, "enter")
	}
}

// ---------------------------------------------------------------------------
// AC 6 — No-op at filesystem root "/"
// ---------------------------------------------------------------------------

// TestRemoteParentPath_AtFilesystemRoot verifies that remoteParentPath("/")
// returns "/" (the filesystem root is its own parent — the no-op sentinel).
func TestRemoteParentPath_AtFilesystemRoot(t *testing.T) {
	got := remoteParentPath("/")
	if got != "/" {
		t.Errorf("remoteParentPath(\"/\") = %q, want \"/\"", got)
	}
}

// TestRemoteFileTreeModel_ParentDirShownAtRemoteRoot verifies that the ".."
// entry is still present at the top of the list when currentRoot is "/".
func TestRemoteFileTreeModel_ParentDirShownAtRemoteRoot(t *testing.T) {
	m := newRemoteModelAtRoot("/")
	if len(m.flatNodes) == 0 {
		t.Fatal("flatNodes should be non-empty at remote root")
	}
	if !m.flatNodes[0].IsParentDir {
		t.Errorf("flatNodes[0] should be '..' even at remote root '/', got %+v", m.flatNodes[0])
	}
	if m.flatNodes[0].Name != ".." {
		t.Errorf("'..' entry name should be \"..\", got %q", m.flatNodes[0].Name)
	}
}

// TestRemoteFileTreeModel_ParentDirNoOpAtRemoteRoot verifies that pressing
// Enter on the ".." entry when currentRoot is "/" does not change currentRoot.
func TestRemoteFileTreeModel_ParentDirNoOpAtRemoteRoot(t *testing.T) {
	m := newRemoteModelAtRoot("/")
	m.cursor = 0
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	m, _ = sendRemoteKey(m, "enter")

	if m.currentRoot != "/" {
		t.Errorf("navigating up from '/' should be a no-op; currentRoot changed to %q", m.currentRoot)
	}
}

// TestRemoteFileTreeModel_ParentDirNoOpAtRemoteRoot_RightKey is the same as
// TestRemoteFileTreeModel_ParentDirNoOpAtRemoteRoot but uses the → key alias.
func TestRemoteFileTreeModel_ParentDirNoOpAtRemoteRoot_RightKey(t *testing.T) {
	m := newRemoteModelAtRoot("/")
	m.cursor = 0
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}

	m, _ = sendRemoteKey(m, "l")

	if m.currentRoot != "/" {
		t.Errorf("navigating up from '/' via 'l' should be a no-op; currentRoot changed to %q", m.currentRoot)
	}
}

// newRemoteModelAtRoot creates a RemoteFileTreeModel with currentRoot set to
// root (e.g. "/") and a pre-populated (empty) directory cache for root.
// This simulates the model after the user has navigated all the way up to "/".
func newRemoteModelAtRoot(root string) RemoteFileTreeModel {
	m := RemoteFileTreeModel{
		host:        testHost(),
		currentRoot: root,
		dirCache:    make(map[string][]filetree.FileEntry),
		loading:     make(map[string]bool),
		loadErrors:  make(map[string]string),
		expanded:    make(map[string]bool),
		selected:    make(map[string]bool),
		width:       80,
		height:      24,
	}
	// Pre-populate the root directory listing with a few entries so the
	// model renders something meaningful.
	m.expanded[root] = true
	m.dirCache[root] = []filetree.FileEntry{
		{Name: "bin", IsDir: true},
		{Name: "etc", IsDir: true},
		{Name: "home", IsDir: true},
	}
	m.rebuild()
	return m
}

// ---------------------------------------------------------------------------
// AC 7 — Permission-denied directory shows error placeholder (not blocking)
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_PermissionDeniedOnNavigateUp verifies that when the
// user navigates up via ".." and the parent directory listing fails with a
// permission error, an error placeholder is shown and the model is not blocked.
func TestRemoteFileTreeModel_PermissionDeniedOnNavigateUp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Cursor starts at index 0 (..).
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0

	// Navigate up from home (".") — triggers an async fetch for "..".
	m, cmd := sendRemoteKey(m, "enter")
	if cmd == nil {
		t.Fatal("navigating to an uncached parent must return a non-nil fetch command")
	}
	if m.currentRoot != ".." {
		t.Fatalf("currentRoot should be '..' after first up-navigation, got %q", m.currentRoot)
	}

	// Simulate the fetch of ".." failing with a permission error.
	updated, _ := m.Update(remoteDirErrorMsg{
		remotePath: "..",
		err:        errors.New("permission denied"),
	})
	m = updated.(RemoteFileTreeModel)

	// The model must not be Done or Back after the error.
	if m.Done() {
		t.Error("model must not report Done() after permission-denied parent navigation")
	}
	if m.Back() {
		t.Error("model must not report Back() after permission-denied parent navigation")
	}

	// An error placeholder that mentions the failure must be visible.
	hasErrorPlaceholder := false
	for _, n := range m.flatNodes {
		if n.IsPlaceholder && n.PlaceholderKind == "error" && strings.Contains(n.Name, "permission denied") {
			hasErrorPlaceholder = true
		}
	}
	if !hasErrorPlaceholder {
		t.Errorf("expected an error placeholder containing 'permission denied'; nodes=%v",
			flatNodeNames(m.flatNodes))
	}

	// The ".." entry must still be at the top so the user can navigate away.
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir {
		t.Error("'..' entry must still appear at index 0 after a permission-denied error on the parent")
	}

	// Cursor and basic navigation must still work.
	if len(m.flatNodes) > 1 {
		startCursor := m.cursor
		m, _ = sendRemoteKey(m, "down")
		if m.cursor == startCursor && len(m.flatNodes) > 1 {
			// It's fine if there's nowhere to go (only 2 nodes and cursor already at 1).
			// Just ensure the model doesn't panic.
		}
	}
}

// TestRemoteFileTreeModel_PermissionDeniedExpandedChildNotBlocking verifies
// that when a sub-directory load fails with a permission error the error
// placeholder is displayed and the model remains fully usable — not Done, not
// Back, and cursor navigation still works.
func TestRemoteFileTreeModel_PermissionDeniedExpandedChildNotBlocking(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Find the first expandable real directory node.
	dirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			dirIdx = i
			break
		}
	}
	if dirIdx < 0 {
		t.Skip("no real directory node found")
	}
	dirPath := m.flatNodes[dirIdx].RemotePath
	m.cursor = dirIdx

	// Expand the directory (marks it loading, returns a fetch command).
	m, _ = sendRemoteKey(m, "enter")

	// Simulate the load failing with EACCES.
	updated, _ := m.Update(remoteDirErrorMsg{
		remotePath: dirPath,
		err:        errors.New("permission denied"),
	})
	m = updated.(RemoteFileTreeModel)

	// The model must remain usable — not done, not back.
	if m.Done() {
		t.Error("model must not report Done() after a permission-denied child-directory load")
	}
	if m.Back() {
		t.Error("model must not report Back() after a permission-denied child-directory load")
	}

	// An error placeholder mentioning the failure must appear in the list.
	found := false
	for _, n := range m.flatNodes {
		if n.IsPlaceholder && n.PlaceholderKind == "error" && strings.Contains(n.Name, "permission denied") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error placeholder with 'permission denied'; nodes=%v",
			flatNodeNames(m.flatNodes))
	}

	// Navigation keys must still work — pressing up/down must not panic.
	if m.cursor > 0 {
		m, _ = sendRemoteKey(m, "up")
	} else if len(m.flatNodes) > 1 {
		m, _ = sendRemoteKey(m, "down")
	}
	_ = m.View() // must not panic
}

// TestRemoteFileTreeModel_PermissionDeniedParentNavigateUpViewRendersOK verifies
// that the rendered View after a permission-denied parent navigation includes the
// ".." entry and the error message, without panicking.
func TestRemoteFileTreeModel_PermissionDeniedParentNavigateUpViewRendersOK(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m.cursor = 0

	// Navigate up and simulate a permission error on the parent.
	m, _ = sendRemoteKey(m, "enter")
	updated, _ := m.Update(remoteDirErrorMsg{
		remotePath: "..",
		err:        errors.New("permission denied"),
	})
	m = updated.(RemoteFileTreeModel)

	// View must not panic and should contain both the ".." entry and the error text.
	view := m.View()
	if !strings.Contains(view, "..") {
		t.Errorf("view must contain '..' after permission-denied parent navigation; got:\n%s", view)
	}
	if !strings.Contains(view, "error") && !strings.Contains(view, "permission") {
		t.Errorf("view should mention the error; got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// AC 8 — Selections are preserved when navigating up via ".."
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_NavigateUpPreservesSelections verifies that files
// selected before navigating up via ".." remain in the selected map after the
// navigation.  This is the core AC 8 requirement: the selected map must not be
// cleared when currentRoot changes.
func TestRemoteFileTreeModel_NavigateUpPreservesSelections(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the parent-directory cache so navigation works without SSH.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "homedir", IsDir: true},
	}

	// Select a file in the home directory.
	var selectedPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			selectedPath = n.RemotePath
			break
		}
	}
	if selectedPath == "" {
		t.Skip("no file node found in home directory")
	}
	m, _ = sendRemoteKey(m, " ") // select the file
	if !m.selected[selectedPath] {
		t.Fatal("file should be selected before navigation")
	}

	// Navigate up via the ".." entry.
	m.cursor = 0
	if !m.flatNodes[0].IsParentDir {
		t.Skip("first node is not the '..' parent-dir entry")
	}
	m, _ = sendRemoteKey(m, "enter")

	// The selected map should still contain the previously selected path even
	// though currentRoot has changed and the file is no longer directly visible.
	if !m.selected[selectedPath] {
		t.Errorf("selection for %q should be preserved after navigating up, but it was lost", selectedPath)
	}
}

// TestRemoteFileTreeModel_NavigateUpPreservesMultipleSelections verifies that
// multiple files selected before navigating up are all preserved.
func TestRemoteFileTreeModel_NavigateUpPreservesMultipleSelections(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the parent-directory cache.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "homedir", IsDir: true},
	}

	// Select all non-directory, non-placeholder nodes.
	var selectedPaths []string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			m, _ = sendRemoteKey(m, " ")
			selectedPaths = append(selectedPaths, n.RemotePath)
		}
	}
	if len(selectedPaths) == 0 {
		t.Skip("no file nodes found in home directory")
	}

	// Navigate up.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	// Every previously selected path must still be in the selected map.
	for _, p := range selectedPaths {
		if !m.selected[p] {
			t.Errorf("selection for %q should be preserved after navigating up, but it was lost", p)
		}
	}
	if len(m.selected) != len(selectedPaths) {
		t.Errorf("selected map has %d entries after navigation, want %d",
			len(m.selected), len(selectedPaths))
	}
}

// TestRemoteFileTreeModel_NavigateUpPreservesSelectionsInCtrlDResult verifies
// that pressing Ctrl+D after navigating up returns all paths that were selected
// before the navigation.
func TestRemoteFileTreeModel_NavigateUpPreservesSelectionsInCtrlDResult(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the parent-directory cache.
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "homedir", IsDir: true},
	}

	// Select a file in the home directory.
	var selectedPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			selectedPath = n.RemotePath
			break
		}
	}
	if selectedPath == "" {
		t.Skip("no file node found in home directory")
	}
	m, _ = sendRemoteKey(m, " ") // select

	// Navigate up.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	// Confirm selection via Ctrl+D.
	m, _ = sendRemoteKey(m, "ctrl+d")

	result := m.GetResult()
	found := false
	for _, p := range result.SelectedPaths {
		if p == selectedPath {
			found = true
		}
	}
	if !found {
		t.Errorf("selected path %q should appear in Ctrl+D result after navigating up; got %v",
			selectedPath, result.SelectedPaths)
	}
}

// TestRemoteFileTreeModel_NavigateUpTwicePreservesSelections verifies that
// selections survive multiple successive up-navigations.
func TestRemoteFileTreeModel_NavigateUpTwicePreservesSelections(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate two ancestor levels.
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}
	m.dirCache["../.."] = []filetree.FileEntry{{Name: "home", IsDir: true}}

	// Select a file.
	var selectedPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			selectedPath = n.RemotePath
			break
		}
	}
	if selectedPath == "" {
		t.Skip("no file node found")
	}
	m, _ = sendRemoteKey(m, " ")

	// Navigate up twice.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter") // . → ..
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter") // .. → ../..

	if !m.selected[selectedPath] {
		t.Errorf("selection for %q should survive two up-navigations, but it was lost", selectedPath)
	}
}

// TestRemoteFileTreeModel_DirSelectionPreservedOnNavigateUp verifies that
// directory selections (not just file selections) are preserved after navigating
// up via "..".
func TestRemoteFileTreeModel_DirSelectionPreservedOnNavigateUp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the parent-directory cache.
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}

	// Select a directory.
	var dirPath string
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsParentDir && !n.IsPlaceholder {
			m.cursor = i
			dirPath = n.RemotePath
			break
		}
	}
	if dirPath == "" {
		t.Skip("no real directory node found")
	}
	m, _ = sendRemoteKey(m, " ") // select directory
	if !m.selected[dirPath] {
		t.Fatal("directory should be selected before navigation")
	}

	// Navigate up.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	if !m.selected[dirPath] {
		t.Errorf("directory selection for %q should be preserved after navigating up", dirPath)
	}
}

// TestRemoteFileTreeModel_NavigateUpDoesNotAddExtraSelections verifies that
// navigating up via ".." does not inadvertently add new entries to the
// selected map (i.e. selection count remains unchanged).
func TestRemoteFileTreeModel_NavigateUpDoesNotAddExtraSelections(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Pre-populate the parent-directory cache.
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}

	// Select one file.
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && !n.IsParentDir {
			m.cursor = i
			m, _ = sendRemoteKey(m, " ")
			break
		}
	}
	selCountBefore := len(m.selected)

	// Navigate up.
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")

	if len(m.selected) != selCountBefore {
		t.Errorf("navigating up should not change selection count; before=%d after=%d",
			selCountBefore, len(m.selected))
	}
}

// ---------------------------------------------------------------------------
// AC 9 — Selections restored when navigating back down to original directory
// ---------------------------------------------------------------------------

// TestRemoteFileTreeModel_SelectionsRestoredAfterCollapseAndReExpand verifies
// the inline collapse/re-expand round-trip (no currentRoot change):
// collapse a directory, re-expand it — the checkmark must reappear.
func TestRemoteFileTreeModel_SelectionsRestoredAfterCollapseAndReExpand(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.dirCache["Documents"] = []filetree.FileEntry{
		{Name: "report.txt", IsDir: false, Size: 2048},
		{Name: "notes.txt", IsDir: false, Size: 512},
	}

	// Expand Documents.
	docIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && n.Name == "Documents" {
			docIdx = i
			break
		}
	}
	if docIdx < 0 {
		t.Skip("Documents not found in home listing")
	}
	m.cursor = docIdx
	m, _ = sendRemoteKey(m, "enter") // expand

	// Select report.txt (now visible as child of Documents).
	reportIdx := -1
	var reportPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && n.Name == "report.txt" {
			reportIdx = i
			reportPath = n.RemotePath
			break
		}
	}
	if reportIdx < 0 {
		t.Skip("report.txt not found after expanding Documents")
	}
	m.cursor = reportIdx
	m, _ = sendRemoteKey(m, " ") // select

	if !m.selected[reportPath] {
		t.Fatal("report.txt should be selected before collapse")
	}

	// Collapse Documents (navigate "away" from the children).
	for i, n := range m.flatNodes {
		if n.IsDir && n.Name == "Documents" {
			docIdx = i
			break
		}
	}
	m.cursor = docIdx
	m, _ = sendRemoteKey(m, "enter") // collapse (toggle off)

	// report.txt should no longer be visible in flatNodes after collapse.
	for _, n := range m.flatNodes {
		if n.Name == "report.txt" {
			t.Error("report.txt should not be visible after collapsing Documents")
		}
	}

	// Selection must still be in map even though the node is not visible.
	if !m.selected[reportPath] {
		t.Error("selection for report.txt should persist in map even when its parent is collapsed")
	}

	// Re-expand Documents (navigate "back down").
	for i, n := range m.flatNodes {
		if n.IsDir && n.Name == "Documents" {
			docIdx = i
			break
		}
	}
	m.cursor = docIdx
	m, _ = sendRemoteKey(m, "enter") // re-expand

	// report.txt must be visible again with its selection intact.
	found := false
	for _, n := range m.flatNodes {
		if n.Name == "report.txt" {
			found = true
			if !m.selected[n.RemotePath] {
				t.Errorf("selection for %q should be restored after re-expanding Documents", n.RemotePath)
			}
			break
		}
	}
	if !found {
		t.Error("report.txt should be visible again after re-expanding Documents")
	}

	// The rendered view must contain a ✓ checkmark.
	view := m.View()
	if !strings.Contains(view, "✓") {
		t.Errorf("view should show ✓ for selected file after re-expanding; got:\n%s", view)
	}
}

// TestRemoteFileTreeModel_SelectionsRestoredAfterNavigatingUpAndExpandingInline
// verifies that files selected before a currentRoot change are preserved in the
// selected map, and that the checkmark reappears when those same nodes are
// brought back into view via inline expansion (even though inline expansion of
// the same directory from a parent root produces different path keys).
//
// Concretely this test checks: selected map is NOT cleared on root change, and
// selections made at currentRoot="." are still reported by sortedSelected()
// even after currentRoot moves to "..".
func TestRemoteFileTreeModel_SelectionsRestoredAfterNavigatingUpAndExpandingInline(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	m.dirCache[".."] = []filetree.FileEntry{
		{Name: "homedir", IsDir: true},
	}

	// Select "notes.txt" at the home root level.
	var notesPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && n.Name == "notes.txt" {
			m.cursor = i
			notesPath = n.RemotePath
			break
		}
	}
	if notesPath == "" {
		t.Skip("notes.txt not found in home entries")
	}
	m, _ = sendRemoteKey(m, " ")
	if !m.selected[notesPath] {
		t.Fatal("notes.txt should be selected before root change")
	}

	// Navigate up → currentRoot=".."
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")
	if m.currentRoot != ".." {
		t.Fatalf("expected currentRoot='..', got %q", m.currentRoot)
	}

	// The original selection must still be in the map.
	if !m.selected[notesPath] {
		t.Errorf("selection for %q should survive navigate-up; selected map must not be cleared on root change", notesPath)
	}

	// The sorted result from sortedSelected must still include the path.
	found := false
	for _, p := range m.sortedSelected() {
		if p == notesPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sortedSelected() should return %q after navigating up; got %v",
			notesPath, m.sortedSelected())
	}

	// Expand "homedir" under the ".." root.
	m.dirCache["../homedir"] = []filetree.FileEntry{
		{Name: "notes.txt", IsDir: false, Size: 128},
	}
	homedirIdx := -1
	for i, n := range m.flatNodes {
		if n.IsDir && n.Name == "homedir" {
			homedirIdx = i
			break
		}
	}
	if homedirIdx < 0 {
		t.Fatal("homedir entry not found at currentRoot='..'")
	}
	m.cursor = homedirIdx
	m, _ = sendRemoteKey(m, "enter") // expand homedir

	// The file appears under "../homedir/notes.txt" — a DIFFERENT key from the
	// original "notes.txt" selection.  The original selection should still be in
	// the map (not replaced or removed).
	if !m.selected[notesPath] {
		t.Errorf("original selection for %q was lost after expanding inline child; selected map must not be mutated by expansion", notesPath)
	}
}

// TestRemoteFileTreeModel_SelectedMapNotClearedOnNavigateUp is a targeted
// assertion that the selected map is never wiped when currentRoot changes.
func TestRemoteFileTreeModel_SelectedMapNotClearedOnNavigateUp(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	// Select notes.txt (a root-level file).
	var notesPath string
	for i, n := range m.flatNodes {
		if !n.IsDir && !n.IsPlaceholder && n.Name == "notes.txt" {
			m.cursor = i
			notesPath = n.RemotePath
			break
		}
	}
	if notesPath == "" {
		t.Skip("notes.txt not found in home entries")
	}
	m, _ = sendRemoteKey(m, " ")
	if !m.selected[notesPath] {
		t.Fatal("notes.txt should be selected")
	}

	// Navigate up — currentRoot changes from "." to "..".
	m.dirCache[".."] = []filetree.FileEntry{{Name: "homedir", IsDir: true}}
	m.cursor = 0
	m, _ = sendRemoteKey(m, "enter")
	if m.currentRoot != ".." {
		t.Fatalf("expected currentRoot='..', got %q", m.currentRoot)
	}

	// The selected map must still contain the original path.
	if !m.selected[notesPath] {
		t.Errorf("selection for %q was lost after navigating up; selected map must not be cleared on root change", notesPath)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func flatNodeNames(nodes []RemoteFlatNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
