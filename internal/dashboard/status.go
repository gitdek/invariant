package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Status is what the watcher is doing right now and its latest recorded
// completions. The watcher writes it each time it starts or finishes
// something, and the dashboard reads it (D-0049). Issue, Doing and Since
// are its longest-running step that's doing something, and a status that
// lists no steps counts as that one. Finished never supplies running work.
type Status struct {
	PID       int        `json:"pid"`
	Repo      string     `json:"repo"`
	Holder    string     `json:"holder,omitempty"` // its name on the repository's lease, whose holder the page follows
	Started   time.Time  `json:"started"`
	Heartbeat time.Time  `json:"heartbeat"`
	Every     float64    `json:"every"`              // seconds between polls
	Issue     int        `json:"issue,omitempty"`    // the issue it's working on, if any
	Doing     string     `json:"doing,omitempty"`    // formalizing, answering, ratifying or building
	Since     *time.Time `json:"since,omitempty"`    // when it started doing it
	Steps     []Step     `json:"steps,omitempty"`    // every step it's running, one per issue (D-0113)
	Finished  []Step     `json:"finished,omitempty"` // latest actual completions, including every exact tie
	Limit     float64    `json:"limit,omitempty"`    // seconds an agent's run may take, from the watcher's -timeout
}

// Step is one step the watcher is running: its issue, what it's doing,
// formalizing, answering, ratifying or building, or nothing yet, since
// when, and the coding agent and output directory of its current run.
// A completed step also records its actual end and that run's time limit.
type Step struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing,omitempty"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent,omitempty"`
	Run   string     `json:"run,omitempty"` // opaque directory basename under the issue's work directory
	Ended *time.Time `json:"ended,omitempty"`
	Limit float64    `json:"limit,omitempty"` // seconds allowed for this run, retained after it ends
}

// Update replaces the running snapshot and records any explicit completions
// in it. Missing steps never imply an end. Only the greatest recorded end
// is retained, with every distinct issue and run tied at that instant.
// Callers serialize updates and writes together.
func (s *Status) Update(steps []Step, at time.Time) {
	s.Heartbeat = at
	s.Issue, s.Doing, s.Since, s.Steps = 0, "", nil, nil
	for _, step := range steps {
		if step.Ended != nil {
			s.finish(step)
			continue
		}
		s.Steps = append(s.Steps, step)
		if step.Doing != "" && (s.Since == nil || step.Since.Before(*s.Since)) {
			since := step.Since
			s.Issue, s.Doing, s.Since = step.Issue, step.Doing, &since
		}
	}
}

func (s *Status) finish(step Step) {
	if step.Run == "" || step.Ended.IsZero() {
		return
	}
	var latest time.Time
	for _, finished := range s.Finished {
		// Once recorded, a completion's metadata never changes, even if
		// another notification for that run has a different end or limit.
		if finished.Issue == step.Issue && finished.Run == step.Run {
			return
		}
		if finished.Ended != nil && finished.Ended.After(latest) {
			latest = *finished.Ended
		}
	}
	if step.Ended.Before(latest) {
		return
	}
	if step.Ended.After(latest) {
		s.Finished = nil
	}
	ended := *step.Ended
	step.Ended = &ended
	if step.Limit == 0 {
		step.Limit = s.Limit
	}
	s.Finished = append(s.Finished, step)
}

// steps is every step the status lists, or, from a watcher that lists none,
// the one its issue, doing and since name.
func (s Status) steps() []Step {
	if len(s.Steps) > 0 || s.Issue == 0 {
		return s.Steps
	}
	step := Step{Issue: s.Issue, Doing: s.Doing}
	if s.Since != nil {
		step.Since = *s.Since
	}
	return []Step{step}
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
