// Package dirtystate tracks SSH hosts that have leftover temporary public
// keys in their authorized_keys files after a distribute-file operation.
//
// When the distribute-file wizard completes (or is interrupted), it attempts
// to remove the temporary public key from each destination host's
// authorized_keys. If that remote cleanup fails for any host, the host is
// recorded here so that a future pass can retry.
//
// State is persisted as JSON to ~/.smux/dirty-state.json. The directory is
// created with mode 0700 if it does not exist.
package dirtystate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DirtyHost describes a remote host that has a leftover smux temporary public
// key in its authorized_keys file, or a hub host whose temporary keypair
// directory needs to be deleted.
//
// Two distinct cleanup record types are represented by this struct:
//
//  1. Spoke-side cleanup (HubKeyDir == ""): KeyComment identifies the line to
//     remove from ~/.ssh/authorized_keys on Host.
//  2. Hub-side cleanup (HubKeyDir != ""): RemoteDir is the path to delete
//     (rm -rf) on Host. KeyComment is set for traceability but the actual
//     cleanup action is directory deletion.
type DirtyHost struct {
	// Host is the SSH address (hostname or IP) of the host.
	Host string `json:"host"`
	// User is the SSH login user. May be empty when the system default is used.
	User string `json:"user,omitempty"`
	// Port is the SSH port. Zero means the standard port 22.
	Port int `json:"port,omitempty"`
	// KeyComment is the unique comment embedded in the temporary public key
	// (e.g. "smux-distribute-a1b2c3d4e5f6g7h8"). For spoke records it
	// identifies the exact line to remove from authorized_keys. For hub
	// records it is set for traceability alongside HubKeyDir.
	KeyComment string `json:"key_comment"`
	// AddedAt is the timestamp when the public key was distributed to this host.
	AddedAt time.Time `json:"added_at"`
	// HubKeyDir, when non-empty, marks this as a hub-side cleanup record.
	// The value is the path to the remote temp directory on Host that must be
	// deleted (rm -rf) to remove the hub's generated keypair files.
	// When empty this is a spoke-side authorized_keys cleanup record.
	HubKeyDir string `json:"hub_key_dir,omitempty"`
}

// State is the full set of hosts with pending cleanup work.
type State struct {
	Hosts []DirtyHost `json:"hosts"`
}

// stateFilePath returns the absolute path to the dirty-state file.
func stateFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".smux", "dirty-state.json"), nil
}

// Load reads the dirty state from ~/.smux/dirty-state.json.
//
// If the file does not exist an empty, non-nil State is returned without
// error, which is the normal case for a fresh installation.
func Load() (*State, error) {
	path, err := stateFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("read dirty state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse dirty state: %w", err)
	}
	return &s, nil
}

// Save persists s to ~/.smux/dirty-state.json.
//
// The ~/.smux directory is created (mode 0700) if it does not exist.
// The file is written with mode 0600 because it records host information.
// When s is empty (no dirty hosts), Save still writes the file so that
// explicit "all clear" state is visible on disk.
func Save(s *State) error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create .smux directory: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dirty state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write dirty state: %w", err)
	}
	return nil
}

// Add appends h to the state.
//
// It does not persist the change; call Save after any mutation.
func (s *State) Add(h DirtyHost) {
	s.Hosts = append(s.Hosts, h)
}

// RemoveByComment removes every entry whose KeyComment matches comment.
//
// It does not persist the change; call Save after any mutation.
func (s *State) RemoveByComment(comment string) {
	remaining := s.Hosts[:0]
	for _, h := range s.Hosts {
		if h.KeyComment != comment {
			remaining = append(remaining, h)
		}
	}
	s.Hosts = remaining
}

// IsEmpty reports whether there are no hosts with pending cleanup work.
func (s *State) IsEmpty() bool {
	return len(s.Hosts) == 0
}
