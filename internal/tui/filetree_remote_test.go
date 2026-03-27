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
		host:       testHost(),
		dirCache:   make(map[string][]filetree.FileEntry),
		loading:    make(map[string]bool),
		loadErrors: make(map[string]string),
		expanded:   make(map[string]bool),
		selected:   make(map[string]bool),
		width:      80,
		height:     24,
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
// contains exactly one loading placeholder while the home dir is being fetched.
func TestRemoteFileTreeModel_ShowsLoadingPlaceholder(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24

	if len(m.flatNodes) != 1 {
		t.Fatalf("expected 1 loading placeholder node, got %d", len(m.flatNodes))
	}
	n := m.flatNodes[0]
	if !n.IsPlaceholder {
		t.Error("node should be a placeholder during loading")
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

	if len(m.flatNodes) != 1 {
		t.Fatalf("expected 1 error placeholder, got %d nodes", len(m.flatNodes))
	}
	n := m.flatNodes[0]
	if !n.IsPlaceholder {
		t.Error("should be a placeholder after error")
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

	// Cursor starts on the first node (should be "Documents" dir).
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath

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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath

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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath

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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath

	// Pre-populate and expand.
	m.dirCache[dirPath] = []filetree.FileEntry{{Name: "child.txt"}}
	m, _ = sendRemoteKey(m, "enter") // expand
	countExpanded := len(m.flatNodes)

	// Collapse.
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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath
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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	dirPath := m.flatNodes[0].RemotePath

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

	// Move to a directory node.
	for i, n := range m.flatNodes {
		if n.IsDir {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir {
		t.Skip("no directory node found")
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

	// Find a directory and select it.
	for i, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder {
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
		if n.IsDir && !n.IsPlaceholder {
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

	// Collect one directory and one file.
	var dirPath, filePath string
	for _, n := range m.flatNodes {
		if n.IsDir && !n.IsPlaceholder && dirPath == "" {
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
		if n.IsDir && !n.IsPlaceholder {
			m.cursor = i
			break
		}
	}
	if m.cursor >= len(m.flatNodes) || !m.flatNodes[m.cursor].IsDir {
		t.Skip("no directory node found")
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
// nodes cannot be selected via Space.
func TestRemoteFileTreeModel_PlaceholderNotSelectable(t *testing.T) {
	m := NewRemoteFileTreeModel(testHost())
	m.width = 80
	m.height = 24
	// Home dir is loading → first node is a placeholder.
	if len(m.flatNodes) == 0 || !m.flatNodes[0].IsPlaceholder {
		t.Skip("first node is not a placeholder")
	}
	placeholderPath := m.flatNodes[0].RemotePath

	m, _ = sendRemoteKey(m, " ")
	if m.selected[placeholderPath] {
		t.Error("placeholder node should not be selectable via Space")
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

// TestRemoteFileTreeModel_ViewDirHasArrow verifies that directory rows include
// an expand/collapse arrow.
func TestRemoteFileTreeModel_ViewDirHasArrow(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())
	lines := m.renderRemoteFileList()
	for i, n := range m.flatNodes {
		if i >= len(lines) || n.IsPlaceholder {
			break
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
func TestRemoteFileTreeModel_PathEncoding_RootEntries(t *testing.T) {
	m := newRemoteModelWithEntries(homeEntries())

	for _, n := range m.flatNodes {
		if n.IsPlaceholder {
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
	if !m.flatNodes[0].IsDir {
		t.Skip("first node is not a directory")
	}
	parentName := m.flatNodes[0].Name
	parentPath := m.flatNodes[0].RemotePath

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
