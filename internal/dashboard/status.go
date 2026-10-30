package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Status records the watcher's running steps and latest actual completions.
// The watcher atomically writes it on activity changes and heartbeats, and
// the dashboard reads it (D-0049). Issue, Doing and Since describe only its
// longest-running active step, with a legacy fallback for statuses without Steps.
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

// Step identifies an issue's activity, original start, agent and named run.
// Ended is set only on a recorded completion, kept separately from running steps.
type Step struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing,omitempty"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent,omitempty"`
	Run   string     `json:"run,omitempty"`   // opaque directory basename under the issue's work directory
	Ended *time.Time `json:"ended,omitempty"` // actual completion reported by the watcher, never inferred
	Limit float64    `json:"limit,omitempty"` // the limit for this run, retained after the watcher changes its timeout
}

// Update replaces the running snapshot and retains only the latest actual
// agent-step completions, including distinct runs that ended at the same time.
// Heartbeats and empty snapshots cannot create or discard a completion.
// The watcher serializes Update and WriteStatus with its status lock.
func (s *Status) Update(steps []Step, at time.Time) {
	previous := s.Steps
	finished := s.Finished
	s.Finished = nil
	for _, step := range finished {
		s.keepFinished(step)
	}
	s.Steps = nil
	s.Issue, s.Doing, s.Since = 0, "", nil
	s.Heartbeat = at
	for _, step := range steps {
		if step.Limit == 0 {
			for _, prior := range previous {
				if prior.Issue == step.Issue && prior.Doing == step.Doing && prior.Since.Equal(step.Since) && (prior.Run == step.Run || prior.Run == "") {
					step.Limit = prior.Limit
					break
				}
			}
			if step.Limit == 0 {
				step.Limit = s.Limit
			}
		}
		if step.Ended != nil {
			s.keepFinished(step)
			continue
		}
		s.Steps = append(s.Steps, step)
		if step.Doing != "" && (s.Since == nil || step.Since.Before(*s.Since)) {
			since := step.Since
			s.Issue, s.Doing, s.Since = step.Issue, step.Doing, &since
		}
	}
}

func (s *Status) keepFinished(step Step) {
	if !step.finished() {
		return
	}
	for _, prior := range s.Finished {
		if prior.Issue == step.Issue && prior.Run == step.Run {
			return // an already recorded run keeps its original metadata
		}
	}
	if len(s.Finished) > 0 {
		latest := s.Finished[0].Ended
		if step.Ended.Before(*latest) {
			return
		}
		if step.Ended.After(*latest) {
			s.Finished = nil
		}
	}
	ended := *step.Ended
	step.Ended = &ended
	s.Finished = append(s.Finished, step)
}

func (s Step) finished() bool {
	prefix, _ := stepFiles(s.Doing)
	return s.Ended != nil && !s.Ended.IsZero() && prefix != "" && s.Issue > 0 && validRun(s.Run)
}

func validRun(run string) bool {
	return run != "" && run != "." && run != ".." && filepath.Base(run) == run
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
