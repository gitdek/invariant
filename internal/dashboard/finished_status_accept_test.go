package dashboard_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
)

// The watcher persists its latest completions in Status.finished. Each entry
// has the ordinary step identity, its recorded ended time and the timeout
// limit for that run. Only the greatest ended time is retained, with all ties.
// Neither a page request nor a vanished running step supplies an end time.
// JSON keeps these acceptance tests compilable before the new fields exist.
type status269StepJSON struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing,omitempty"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent,omitempty"`
	Run   string     `json:"run,omitempty"`
	Ended *time.Time `json:"ended,omitempty"`
	Limit float64    `json:"limit,omitempty"`
}

type status269StatusJSON struct {
	PID       int                 `json:"pid"`
	Repo      string              `json:"repo"`
	Started   time.Time           `json:"started"`
	Heartbeat time.Time           `json:"heartbeat"`
	Issue     int                 `json:"issue,omitempty"`
	Doing     string              `json:"doing,omitempty"`
	Since     *time.Time          `json:"since,omitempty"`
	Steps     []status269StepJSON `json:"steps,omitempty"`
	Finished  []status269StepJSON `json:"finished,omitempty"`
	Limit     float64             `json:"limit,omitempty"`
}

func status269JSON(t *testing.T, from, to any) {
	t.Helper()
	b, err := json.Marshal(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, to); err != nil {
		t.Fatal(err)
	}
}

func status269At(minute int) time.Time {
	return time.Date(2026, 10, 30, 0, minute, 0, 0, time.UTC)
}

func status269Step(issue int, doing, run string, start, end int, limit float64) status269StepJSON {
	s := status269StepJSON{Issue: issue, Doing: doing, Since: status269At(start), Agent: "codex", Run: run, Limit: limit}
	if end != 0 {
		at := status269At(end)
		s.Ended = &at
	}
	return s
}

func status269View(t *testing.T, s dashboard.Status) status269StatusJSON {
	t.Helper()
	var got status269StatusJSON
	status269JSON(t, s, &got)
	return got
}

// Update is the public status accumulator called by the watcher for each
// snapshot, including its transient ended entries. Testing through a runtime
// interface gives the base a useful behavioral failure instead of a build error.
func status269Update(t *testing.T, s *dashboard.Status, at time.Time, steps ...status269StepJSON) {
	t.Helper()
	u, ok := any(s).(interface {
		Update([]dashboard.Step, time.Time)
	})
	if !ok {
		t.Fatal("watcher status has no Update([]Step, time.Time): completed steps cannot survive subsequent status snapshots")
	}
	var input []dashboard.Step
	status269JSON(t, steps, &input)
	u.Update(input, at)
}

func status269Want(t *testing.T, s dashboard.Status, want ...status269StepJSON) {
	t.Helper()
	got := status269View(t, s).Finished
	if len(got) != len(want) {
		t.Fatalf("persisted finished steps = %+v; want %+v", got, want)
	}
	byRun := make(map[string]status269StepJSON, len(got))
	for _, step := range got {
		key := fmt.Sprintf("%d/%s", step.Issue, step.Run)
		if _, duplicate := byRun[key]; duplicate {
			t.Fatalf("finished step %s appears twice: %+v", key, got)
		}
		byRun[key] = step
	}
	for _, step := range want {
		key := fmt.Sprintf("%d/%s", step.Issue, step.Run)
		if actual, found := byRun[key]; !found || !reflect.DeepEqual(actual, step) {
			t.Errorf("finished step %s = %+v (present %v); want %+v", key, actual, found, step)
		}
	}
}

func status269Idle(t *testing.T, s dashboard.Status) {
	t.Helper()
	if len(s.Steps) != 0 || s.Issue != 0 || s.Doing != "" || s.Since != nil {
		t.Errorf("finished-only status still advertises work: steps %+v, headline #%d %s since %v", s.Steps, s.Issue, s.Doing, s.Since)
	}
}

func TestFinishedStatusSurvivesWritesHeartbeatsAndWatcherShutdown269(t *testing.T) {
	ended := status269Step(227, "formalizing", "formalize-20261030-002100", 21, 45, 9000)
	var status dashboard.Status
	status269JSON(t, status269StatusJSON{
		PID: os.Getpid(), Repo: "owner/app", Started: status269At(0), Heartbeat: status269At(45),
		Limit: 9000, Finished: []status269StepJSON{ended},
	}, &status)
	path := dashboard.StatusPath(t.TempDir(), "owner/app")
	roundTrip := func() {
		t.Helper()
		if err := dashboard.WriteStatus(path, status); err != nil {
			t.Fatal(err)
		}
		var err error
		status, err = dashboard.ReadStatus(path)
		if err != nil {
			t.Fatal(err)
		}
		status269Want(t, status, ended)
		status269Idle(t, status)
	}
	roundTrip()
	if !status.Running() {
		t.Error("the live watcher's status should still report its process as running")
	}

	// A later heartbeat starts from the persisted status. Changing the
	// watcher's current timeout must not relabel the already completed run.
	status.Limit = 1800
	heartbeat := status269At(60)
	status269Update(t, &status, heartbeat)
	if !status.Heartbeat.Equal(heartbeat) {
		t.Errorf("heartbeat = %s; want %s", status.Heartbeat, heartbeat)
	}
	roundTrip()
	if status.Limit != 1800 {
		t.Errorf("current watcher limit = %v; want 1800 while completed run keeps 9000", status.Limit)
	}

	// Shutdown clears the PID, not the completed run. A dashboard starting
	// after shutdown can read the same timestamps and original timeout.
	status.PID = 0
	status269Update(t, &status, status269At(61))
	roundTrip()
	if status.Running() {
		t.Error("stopped watcher status reports its process as running")
	}
}

func TestFinishedStatusTracksLatestRealEndsAndTies269(t *testing.T) {
	status := dashboard.Status{PID: os.Getpid(), Repo: "owner/app", Started: status269At(0), Limit: 9000}
	build := status269Step(227, "building", "build-20261030-002100", 21, 0, 0)
	draft := status269Step(228, "formalizing", "formalize-20261030-002200", 22, 0, 0)
	status269Update(t, &status, status269At(35), draft, build)
	if len(status.Steps) != 2 || status.Issue != build.Issue || status.Doing != build.Doing || status.Since == nil || !status.Since.Equal(build.Since) {
		t.Fatalf("running headline and steps = %+v; want both steps and longest-running build #227", status)
	}
	status269Want(t, status)

	build.Ended = new(time.Time)
	*build.Ended = status269At(40)
	status269Update(t, &status, status269At(41), draft, build)
	build.Limit = 9000
	status269Want(t, status, build)
	if len(status.Steps) != 1 || status.Steps[0].Issue != draft.Issue || status.Issue != draft.Issue || status.Doing != draft.Doing || status.Since == nil || !status.Since.Equal(draft.Since) {
		t.Fatalf("completion displaced the still-running draft: %+v", status)
	}
	var active status269StepJSON
	status269JSON(t, status.Steps[0], &active)
	if active.Ended != nil {
		t.Errorf("an ended entry remained among running steps: %+v", active)
	}

	// A disappearance alone is no evidence of an end. An empty snapshot
	// clears the active headline but retains the only recorded completion.
	status.Limit = 1800
	status269Update(t, &status, status269At(42))
	status269Want(t, status, build)
	status269Idle(t, status)

	draft.Ended = new(time.Time)
	*draft.Ended = status269At(45)
	draft.Limit = 600 // an explicitly recorded per-run limit wins
	answer := status269Step(229, "answering", draft.Run, 43, 45, 0)
	retry := status269Step(228, "formalizing", "formalize-20261030-004400-retry", 44, 45, 0)
	status269Update(t, &status, status269At(46), draft, answer, retry, draft)
	answer.Limit, retry.Limit = 1800, 1800
	// Both issue and run distinguish tied completions: a reused basename
	// in another issue and another run of the same issue each remain.
	status269Want(t, status, draft, answer, retry)
	status269Idle(t, status)

	// Old events, duplicate snapshots and non-agent activities must not
	// overwrite the latest genuine completions, even with later timestamps.
	status269Update(t, &status, status269At(52), build, draft,
		status269Step(230, "", "", 46, 48, 0),
		status269Step(231, "ratifying", "", 46, 49, 0),
		status269Step(232, "building", "", 46, 50, 0))
	status269Want(t, status, draft, answer, retry)
	status269Idle(t, status)

	later := status269Step(228, "formalizing", "formalize-20261030-005200", 52, 55, 0)
	status269Update(t, &status, status269At(56), later)
	later.Limit = 1800
	status269Want(t, status, later)
	status269Idle(t, status)
}
