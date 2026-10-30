package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Status is what the watcher is doing and the latest runs it finished. The
// watcher writes it each time it starts or finishes something, and the
// dashboard reads it (D-0049). Issue, Doing and Since name its longest-running
// active step. A legacy status that lists no steps counts as that one.
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
	Finished  []Step     `json:"finished,omitempty"` // latest recorded completion, including every exact tie
	Limit     float64    `json:"limit,omitempty"`    // seconds an agent's run may take, from the watcher's -timeout
}

// Step is one step the watcher is running or has finished: its issue,
// activity, start, coding agent and run directory. Ended is recorded by the
// watcher when the step ends, never inferred from a missing running step.
type Step struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing,omitempty"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent,omitempty"`
	Run   string     `json:"run,omitempty"` // opaque directory basename under the issue's work directory
	Ended *time.Time `json:"ended,omitempty"`
	Limit float64    `json:"limit,omitempty"` // seconds allowed for this run, retained after it ends
}

// Update consumes a watcher snapshot, including its transient completion
// entries. Only running steps contribute to Steps and the headline. Finished
// keeps the greatest recorded end time and distinct issue/run pairs tied at it,
// independently of heartbeats or whether a dashboard has read the status.
func (s *Status) Update(steps []Step, at time.Time) {
	type identity struct {
		issue int
		run   string
	}
	limits := make(map[identity]float64, len(s.Steps))
	for _, step := range s.Steps {
		if step.Run != "" {
			limits[identity{step.Issue, step.Run}] = step.Limit
		}
	}

	var finished []Step
	var latest time.Time
	seen := make(map[identity]bool)
	remember := func(step Step) {
		if step.Run == "" || step.Ended == nil || step.Ended.IsZero() {
			return
		}
		key := identity{step.Issue, step.Run}
		if seen[key] {
			return
		}
		seen[key] = true
		if step.Ended.Before(latest) {
			return
		}
		if step.Ended.After(latest) {
			latest = *step.Ended
			finished = nil
		}
		ended := *step.Ended
		step.Ended = &ended
		finished = append(finished, step)
	}
	for _, step := range s.Finished {
		remember(step)
	}

	s.Heartbeat = at
	s.Steps, s.Issue, s.Doing, s.Since = nil, 0, "", nil
	for _, step := range steps {
		if step.Run != "" && step.Limit == 0 {
			step.Limit = limits[identity{step.Issue, step.Run}]
			if step.Limit == 0 {
				step.Limit = s.Limit
			}
		}
		if step.Ended != nil {
			remember(step)
			continue
		}
		s.Steps = append(s.Steps, step)
		if step.Doing != "" && (s.Since == nil || step.Since.Before(*s.Since)) {
			since := step.Since
			s.Issue, s.Doing, s.Since = step.Issue, step.Doing, &since
		}
	}
	s.Finished = finished
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
