package dashboard

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A wait belongs to the existing build. It changes the words the page
// displays, without changing which transcript, checks or start it reads.
func TestCheckWaitKeepsTheBuildCardAndItsChecks(t *testing.T) {
	const repo = "owner/checks"
	f := newFinished269(t, repo)
	run := finished269Run(458, "building", "build-opaque-458", finished269At(0, 21))
	f.transcript(t, repo, run, false)
	waiting := run
	waiting.Doing = "waiting for checks"
	f.status(t, repo, os.Getpid(), []finished269Step{waiting}, nil)
	for _, private := range []bool{false, true} {
		cards, _ := finished269Read(t, f.s, finished269At(0, 35), private)
		if len(cards) != 1 {
			t.Fatalf("waiting for checks hid the build card: %+v", cards)
		}
		c := cards[0]
		if c.Issue != run.Issue || c.Run != run.Run || c.By != run.Agent || c.Since == nil || !c.Since.Equal(run.Since) || c.Ended != nil {
			t.Errorf("waiting changed the build's identity or start: %+v; want %+v", c, run)
		}
		if c.Doing != waiting.Doing || c.Title != "Waiting for checks" {
			t.Errorf("waiting card says doing=%q title=%q", c.Doing, c.Title)
		}
		if c.RunsKind != "test" || len(c.Runs) != 1 || !c.Runs[0].Passed {
			t.Errorf("waiting card lost the build's completed test runs: kind=%q runs=%+v", c.RunsKind, c.Runs)
		}
	}
	// Admission resumes the same card; its clock still includes the wait.
	f.status(t, repo, os.Getpid(), []finished269Step{run}, nil)
	cards, _ := finished269Read(t, f.s, finished269At(0, 40), false)
	if len(cards) != 1 || cards[0].Run != run.Run || cards[0].Since == nil || !cards[0].Since.Equal(run.Since) || cards[0].Title != "Building" || cards[0].Ended != nil {
		t.Fatalf("admission replaced or restarted the build card: %+v", cards)
	}
}

func TestCheckWaitAppearsInSingleAndParallelHeadlines(t *testing.T) {
	since := finished269At(0, 21)
	waiting := Step{Issue: 458, Doing: "waiting for checks", Since: since, Agent: "codex", Run: "build-opaque-458"}
	for _, parallel := range []bool{false, true} {
		steps := []Step{waiting}
		if parallel {
			steps = append(steps, Step{Issue: 459, Doing: "building", Since: since.Add(time.Minute), Agent: "claude-code", Run: "build-other-459"})
		}
		n := nowLine([]namedWatcher{{repo: "owner/checks", w: Watcher{Running: true, Steps: steps}}}, nil, "owner/checks")
		if !strings.Contains(strings.ToLower(n.Headline), "waiting for checks") || !strings.Contains(n.Headline, "#458") {
			t.Errorf("parallel=%v: headline hides the check wait: %q", parallel, n.Headline)
		}
		if n.Issue != 458 || n.Stage != StageBuilding || n.Running != len(steps) || n.Since == nil || !n.Since.Equal(since) {
			t.Errorf("parallel=%v: wait changed the build's headline identity or clock: %+v", parallel, n)
		}
		if parallel && !strings.Contains(n.Headline, "#459") {
			t.Errorf("waiting hid the other running build: %q", n.Headline)
		}
	}
}

func TestCheckWaitTimeoutRemainsAFinishedBuild(t *testing.T) {
	const repo = "owner/checks"
	f := newFinished269(t, repo)
	run := finished269Run(458, "building", "build-timeout-458", finished269At(0, 21))
	f.transcript(t, repo, run, false)
	ended := finished269At(1, 1)
	waiting := Step{Issue: run.Issue, Doing: "waiting for checks", Since: run.Since, Agent: run.Agent, Run: run.Run}
	st := Status{PID: os.Getpid(), Repo: repo, Limit: 2400}
	st.Update([]Step{waiting}, ended.Add(-time.Minute))
	waiting.Ended = &ended
	st.Update([]Step{waiting}, ended)
	if len(st.Steps) != 0 || len(st.Finished) != 1 {
		t.Fatalf("a build ending in the check queue lost its completion: %+v", st)
	}
	if err := WriteStatus(StatusPath(f.work, repo), st); err != nil {
		t.Fatal(err)
	}
	cards, _ := finished269Read(t, f.s, ended.Add(time.Minute), false)
	if len(cards) != 1 {
		t.Fatalf("a build that ended while waiting has no finished card: %+v", cards)
	}
	c := cards[0]
	if c.Issue != run.Issue || c.Run != run.Run || c.By != run.Agent || c.Since == nil || !c.Since.Equal(run.Since) || c.Ended == nil || !c.Ended.Equal(ended) || c.Limit != 2400 {
		t.Errorf("finished waiting build lost its original identity, start, end or limit: %+v", c)
	}
	if c.Title != "Build step ended" || c.Now != "" || c.RunsKind != "test" || len(c.Runs) != 1 || !c.Runs[0].Passed {
		t.Errorf("finished waiting build lost its title or check history: %+v", c)
	}
}
