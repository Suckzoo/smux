package dirtystate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempHome overrides the HOME environment variable so stateFilePath
// resolves inside a test-controlled temp directory. It returns the temp
// home path and registers cleanup with t.Cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	// Also set USERPROFILE for compatibility with os.UserHomeDir on Windows
	// (no-op on Unix, harmless).
	t.Setenv("USERPROFILE", tmpHome)
	return tmpHome
}

// ---------------------------------------------------------------------------
// stateFilePath
// ---------------------------------------------------------------------------

func TestStateFilePath_ContainsSmuxDir(t *testing.T) {
	withTempHome(t)
	path, err := stateFilePath()
	if err != nil {
		t.Fatalf("stateFilePath: %v", err)
	}
	if filepath.Base(path) != "dirty-state.json" {
		t.Errorf("expected file name 'dirty-state.json', got %q", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != ".smux" {
		t.Errorf("expected parent dir '.smux', got %q", filepath.Base(filepath.Dir(path)))
	}
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

func TestLoad_FileNotExist_ReturnsEmptyState(t *testing.T) {
	withTempHome(t)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load (no file): unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("Load (no file): returned nil state")
	}
	if !s.IsEmpty() {
		t.Errorf("expected empty state, got %d hosts", len(s.Hosts))
	}
}

func TestLoad_ValidJSON_ParsesCorrectly(t *testing.T) {
	tmpHome := withTempHome(t)
	smuxDir := filepath.Join(tmpHome, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	raw := DirtyHost{
		Host:       "host1.example.com",
		User:       "ubuntu",
		Port:       22,
		KeyComment: "smux-distribute-abcdef01",
		AddedAt:    now,
	}
	initial := State{Hosts: []DirtyHost{raw}}
	data, err := json.MarshalIndent(initial, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(smuxDir, "dirty-state.json"), data, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(s.Hosts))
	}
	h := s.Hosts[0]
	if h.Host != "host1.example.com" {
		t.Errorf("Host: got %q, want %q", h.Host, "host1.example.com")
	}
	if h.User != "ubuntu" {
		t.Errorf("User: got %q, want %q", h.User, "ubuntu")
	}
	if h.KeyComment != "smux-distribute-abcdef01" {
		t.Errorf("KeyComment: got %q, want %q", h.KeyComment, "smux-distribute-abcdef01")
	}
}

func TestLoad_CorruptJSON_ReturnsError(t *testing.T) {
	tmpHome := withTempHome(t)
	smuxDir := filepath.Join(tmpHome, ".smux")
	if err := os.MkdirAll(smuxDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(smuxDir, "dirty-state.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load()
	if err == nil {
		t.Error("expected error for corrupt JSON, got nil")
	}
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

func TestSave_CreatesDirectoryAndFile(t *testing.T) {
	tmpHome := withTempHome(t)
	s := &State{
		Hosts: []DirtyHost{
			{
				Host:       "server.example.com",
				KeyComment: "smux-distribute-deadbeef",
				AddedAt:    time.Now(),
			},
		},
	}
	if err := Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := filepath.Join(tmpHome, ".smux", "dirty-state.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %q to exist after Save: %v", path, err)
	}
}

func TestSave_FileHasRestrictedPermissions(t *testing.T) {
	withTempHome(t)
	s := &State{}
	if err := Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := stateFilePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("expected file mode 0600, got %04o", mode)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	withTempHome(t)
	now := time.Now().UTC().Truncate(time.Second)
	original := &State{
		Hosts: []DirtyHost{
			{Host: "a.example.com", User: "ops", Port: 2222, KeyComment: "smux-distribute-11223344", AddedAt: now},
			{Host: "b.example.com", KeyComment: "smux-distribute-55667788", AddedAt: now},
		},
	}
	if err := Save(original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Hosts) != 2 {
		t.Fatalf("round-trip: expected 2 hosts, got %d", len(loaded.Hosts))
	}
	if loaded.Hosts[0].Host != "a.example.com" {
		t.Errorf("Hosts[0].Host: got %q, want %q", loaded.Hosts[0].Host, "a.example.com")
	}
	if loaded.Hosts[1].KeyComment != "smux-distribute-55667788" {
		t.Errorf("Hosts[1].KeyComment: got %q, want %q", loaded.Hosts[1].KeyComment, "smux-distribute-55667788")
	}
}

func TestSave_EmptyState_WritesValidJSON(t *testing.T) {
	withTempHome(t)
	if err := Save(&State{}); err != nil {
		t.Fatalf("Save empty state: %v", err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load after empty save: %v", err)
	}
	if !s.IsEmpty() {
		t.Errorf("expected empty state after round-trip of empty state")
	}
}

// ---------------------------------------------------------------------------
// State.Add
// ---------------------------------------------------------------------------

func TestAdd_AppendsHost(t *testing.T) {
	s := &State{}
	h := DirtyHost{Host: "x.example.com", KeyComment: "smux-distribute-abc", AddedAt: time.Now()}
	s.Add(h)
	if len(s.Hosts) != 1 {
		t.Fatalf("expected 1 host after Add, got %d", len(s.Hosts))
	}
	if s.Hosts[0].Host != "x.example.com" {
		t.Errorf("Host: got %q, want %q", s.Hosts[0].Host, "x.example.com")
	}
}

func TestAdd_MultipleHosts(t *testing.T) {
	s := &State{}
	s.Add(DirtyHost{Host: "h1", KeyComment: "c1", AddedAt: time.Now()})
	s.Add(DirtyHost{Host: "h2", KeyComment: "c2", AddedAt: time.Now()})
	s.Add(DirtyHost{Host: "h3", KeyComment: "c3", AddedAt: time.Now()})
	if len(s.Hosts) != 3 {
		t.Fatalf("expected 3 hosts, got %d", len(s.Hosts))
	}
}

// ---------------------------------------------------------------------------
// State.RemoveByComment
// ---------------------------------------------------------------------------

func TestRemoveByComment_RemovesMatchingEntries(t *testing.T) {
	s := &State{
		Hosts: []DirtyHost{
			{Host: "h1", KeyComment: "smux-distribute-aaa"},
			{Host: "h2", KeyComment: "smux-distribute-bbb"},
			{Host: "h3", KeyComment: "smux-distribute-aaa"},
		},
	}
	s.RemoveByComment("smux-distribute-aaa")
	if len(s.Hosts) != 1 {
		t.Fatalf("expected 1 host after remove, got %d: %v", len(s.Hosts), s.Hosts)
	}
	if s.Hosts[0].Host != "h2" {
		t.Errorf("expected remaining host to be h2, got %q", s.Hosts[0].Host)
	}
}

func TestRemoveByComment_NoMatch_NoChange(t *testing.T) {
	s := &State{
		Hosts: []DirtyHost{
			{Host: "h1", KeyComment: "smux-distribute-aaa"},
		},
	}
	s.RemoveByComment("smux-distribute-nonexistent")
	if len(s.Hosts) != 1 {
		t.Errorf("expected no change, got %d hosts", len(s.Hosts))
	}
}

func TestRemoveByComment_EmptyState_NoError(t *testing.T) {
	s := &State{}
	// Should not panic or error.
	s.RemoveByComment("smux-distribute-anything")
	if !s.IsEmpty() {
		t.Error("expected state to remain empty")
	}
}

// ---------------------------------------------------------------------------
// State.IsEmpty
// ---------------------------------------------------------------------------

func TestIsEmpty_TrueWhenNoHosts(t *testing.T) {
	s := &State{}
	if !s.IsEmpty() {
		t.Error("expected IsEmpty() == true for zero-value State")
	}
}

func TestIsEmpty_FalseWhenHostsPresent(t *testing.T) {
	s := &State{Hosts: []DirtyHost{{Host: "h", KeyComment: "c"}}}
	if s.IsEmpty() {
		t.Error("expected IsEmpty() == false when hosts are present")
	}
}
