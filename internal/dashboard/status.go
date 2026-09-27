package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Status is what the watcher is doing right now. The watcher writes it to a
// file each time it starts or finishes something, and the dashboard reads
// it (D-0049).
type Status struct {
	PID       int        `json:"pid"`
	Repo      string     `json:"repo"`
	Started   time.Time  `json:"started"`
	Heartbeat time.Time  `json:"heartbeat"`
	Every     float64    `json:"every"`           // seconds between polls
	Issue     int        `json:"issue,omitempty"` // the issue it's working on, if any
	Doing     string     `json:"doing,omitempty"` // formalizing, answering, ratifying or building
	Since     *time.Time `json:"since,omitempty"` // when it started doing it
}

// StatusPath is where the watcher for repo keeps its status, under its work
// directory.
func StatusPath(work, repo string) string {
	return filepath.Join(work, filepath.FromSlash(repo), "status.json")
}

// WriteStatus replaces the status file in one step, so a reader never sees
// half of it.
func WriteStatus(path string, s Status) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadStatus reads the watcher's status. A missing file means no watcher
// has run.
func ReadStatus(path string) (Status, error) {
	var s Status
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Running says whether the watcher that wrote the status is still alive.
func (s Status) Running() bool {
	if s.PID <= 0 {
		return false
	}
	return syscall.Kill(s.PID, 0) == nil
}
