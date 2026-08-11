// Package history persists hopper's connection history in a small JSON
// state file. History powers the RECENT section and last-connected display;
// a missing or corrupt file is never an error.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// Entry records one host selection.
type Entry struct {
	Host      string    `json:"host"`
	Timestamp time.Time `json:"timestamp"`
}

type stateFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const maxEntries = 100

// Path returns the platform-appropriate history file location: on Linux
// $XDG_STATE_HOME/hopper/history.json (default ~/.local/state/...), on
// macOS/Windows the os.UserConfigDir equivalent.
func Path() (string, error) {
	if runtime.GOOS == "linux" {
		if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
			return filepath.Join(dir, "hopper", "history.json"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "hopper", "history.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hopper", "history.json"), nil
}

// Load reads entries newest-first. Missing or corrupt files yield nil.
func Load(path string) []Entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	sort.SliceStable(f.Entries, func(i, j int) bool {
		return f.Entries[i].Timestamp.After(f.Entries[j].Timestamp)
	})
	return f.Entries
}

// Record prepends an entry and rewrites the file, capped at maxEntries.
// The write is atomic: it's staged in a temp file in the same directory
// (so the rename is same-filesystem) and renamed over the target, so a
// crash or concurrent read never observes a partially written file.
func Record(path, hostName string, now time.Time) error {
	entries := append([]Entry{{Host: hostName, Timestamp: now}}, Load(path)...)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stateFile{Version: 1, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "history-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Recent returns up to n distinct host names, newest first. Assumes entries
// are pre-sorted by timestamp (newest first).
func Recent(entries []Entry, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.Host] {
			continue
		}
		seen[e.Host] = true
		out = append(out, e.Host)
		if len(out) == n {
			break
		}
	}
	return out
}

// LastConnected returns each host's most recent timestamp.
func LastConnected(entries []Entry) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, e := range entries {
		if e.Timestamp.After(out[e.Host]) {
			out[e.Host] = e.Timestamp
		}
	}
	return out
}
