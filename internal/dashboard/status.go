package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Status is what the watcher is doing right now, and the steps it finished
// last. The watcher writes it to a file each time it starts or finishes
// something, and the dashboard reads it (D-0049). Issue, Doing and Since are
// its longest-running step that's doing something, and a status that lists
// no steps counts as that one. A watcher that stops leaves its status with
// no PID, and the next one starts from its finished steps (#269).
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
	Finished  []Step     `json:"finished,omitempty"` // the agent steps that ended last, all at the same time
	Limit     float64    `json:"limit,omitempty"`    // seconds an agent's run may take, from the watcher's -timeout
}

// Step is one step the watcher is running: its issue, what it's doing,
// formalizing, answering, ratifying or building, or nothing yet, since
// when, and the coding agent and output directory of its current run. A
// finished step also says when the watcher saw it end, and the limit its
// agent ran under, which a later watcher's -timeout doesn't change.
type Step struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing,omitempty"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent,omitempty"`
	Run   string     `json:"run,omitempty"`   // opaque directory basename under the issue's work directory
	Ended *time.Time `json:"ended,omitempty"` // when the step ended, as the watcher recorded it, never inferred
	Limit float64    `json:"limit,omitempty"` // seconds its agent's run could take, on a finished step
}

// Update is the watcher's next snapshot of its steps, at the time at, as
// its heartbeat. Steps that carry an end are completions: the status keeps
// those that ended last, every one that ended at that same time, until a
// step ends later, and the rest are its running steps. A step that leaves
// the snapshot without an end never becomes a completion, and a completion
// that ended earlier than those kept, or one already kept, changes nothing.
func (s *Status) Update(steps []Step, at time.Time) {
	kept := s.Finished
	s.Heartbeat = at
	s.Issue, s.Doing, s.Since, s.Steps, s.Finished = 0, "", nil, nil, nil
	for _, step := range kept {
		s.finish(step)
	}
	for _, step := range steps {
		if step.Ended != nil {
			if step.Limit == 0 {
				step.Limit = s.Limit
			}
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

// finish keeps a completion if it ended as late as those kept, and drops
// those it ended later than. Only an agent's step, with the run it named
// and its end, is one. A run is kept once, as it first ended.
func (s *Status) finish(step Step) {
	if !step.finished() {
		return
	}
	for _, kept := range s.Finished {
		if kept.Issue == step.Issue && kept.Run == step.Run {
			return
		}
	}
	if len(s.Finished) > 0 {
		switch latest := *s.Finished[0].Ended; {
		case step.Ended.Before(latest):
			return
		case step.Ended.After(latest):
			s.Finished = nil
		}
	}
	ended := *step.Ended
	step.Ended = &ended
	s.Finished = append(s.Finished, step)
}

// finished says whether a step is a completion the dashboard can show: an
// agent's draft, answer or build, with its issue, run and end.
func (s Step) finished() bool {
	prefix, _ := stepFiles(s.Doing)
	return s.Ended != nil && !s.Ended.IsZero() && prefix != "" && s.Issue > 0 && validRun(s.Run)
}

// validRun says whether a run is a directory basename, the only kind the
// watcher publishes.
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
