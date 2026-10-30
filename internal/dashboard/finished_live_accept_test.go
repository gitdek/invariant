package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These fixtures are the watcher's on-disk wire contract, not a dashboard
// cache. Finished entries carry the complete step identity and its original
// limit. Title is the text app.js renders before the issue reference.
// Local wire types let the old reader compile and fail on its behavior.
type finished269Step struct {
	Issue int        `json:"issue"`
	Doing string     `json:"doing"`
	Since time.Time  `json:"since"`
	Agent string     `json:"agent"`
	Run   string     `json:"run"`
	Ended *time.Time `json:"ended,omitempty"`
	Limit float64    `json:"limit,omitempty"`
}

type finished269Card struct {
	Live
	Title    string            `json:"title"`
	Commands []json.RawMessage `json:"commands"`
}

type finished269Fixture struct {
	work, workspace string
	s               *Server
}

func finished269At(hour, minute int) time.Time {
	return time.Date(2026, 10, 30, hour, minute, 0, 123456789, time.UTC)
}

func newFinished269(t *testing.T, repos ...string) *finished269Fixture {
	t.Helper()
	f := &finished269Fixture{work: t.TempDir(), workspace: t.TempDir()}
	f.s = &Server{Work: f.work}
	for _, repo := range repos {
		f.s.Repos = append(f.s.Repos, &Repo{Name: repo, Status: StatusPath(f.work, repo)})
	}
	return f
}

func finished269Run(issue int, doing, run string, since time.Time) finished269Step {
	return finished269Step{Issue: issue, Doing: doing, Since: since, Agent: "codex", Run: run, Limit: 9000}
}

func finished269End(step finished269Step, at time.Time) finished269Step {
	step.Ended = &at
	return step
}

func (f *finished269Fixture) status(t *testing.T, repo string, pid int, running, finished []finished269Step) {
	t.Helper()
	// The current timeout differs from historical entries on purpose.
	st := map[string]any{"pid": pid, "repo": repo, "holder": "watcher-269", "started": finished269At(0, 0),
		"heartbeat": finished269At(7, 29), "every": 30, "limit": 300, "steps": running, "finished": finished}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	finished269Write(t, StatusPath(f.work, repo), b)
}

func finished269Write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *finished269Fixture) transcript(t *testing.T, repo string, step finished269Step, complete bool) {
	t.Helper()
	path := filepath.Join(f.work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", step.Issue), step.Run)
	finished269Write(t, filepath.Join(f.workspace, "inside.go"), []byte("package secret // PRIVATE269_CODE\n"))
	events := []map[string]any{
		{"type": "invariant.workspace", "cwd": f.workspace, "model": "PRIVATE269_MODEL"},
		{"type": "turn.started", "timestamp": step.Since.Add(time.Minute)},
		{"type": "item.completed", "timestamp": step.Since.Add(2 * time.Minute), "item": map[string]any{
			"id": "read", "type": "command_execution", "command": "cat inside.go", "aggregated_output": "PRIVATE269_OUTPUT"}},
	}
	if complete {
		events = append(events,
			map[string]any{"type": "item.completed", "timestamp": step.Since.Add(3 * time.Minute), "item": map[string]any{
				"id": "outside", "type": "command_execution", "command": "cat /Users/PRIVATE269_USER/.netrc", "aggregated_output": "PRIVATE269_KEY"}},
			map[string]any{"type": "item.completed", "timestamp": step.Since.Add(4 * time.Minute), "item": map[string]any{
				"id": "reason", "type": "reasoning", "text": "PRIVATE269_THOUGHT"}},
			map[string]any{"type": "item.completed", "timestamp": step.Since.Add(5 * time.Minute), "item": map[string]any{
				"id": "answer", "type": "agent_message", "text": "PRIVATE269_WORDS"}},
			map[string]any{"type": "turn.completed", "timestamp": step.Since.Add(6 * time.Minute), "usage": map[string]any{
				"output_tokens": 321, "reasoning_output_tokens": 123}},
		)
	}
	var lines []byte
	for _, event := range events {
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, b...)
		lines = append(lines, '\n')
	}
	transcript := filepath.Join(path, "transcript.jsonl")
	finished269Write(t, transcript, lines)
	// Neither the last transcript event nor its mtime is the end of the
	// factory's step, which includes checking the agent's work.
	if err := os.Chtimes(transcript, finished269At(7, 0), finished269At(7, 0)); err != nil {
		t.Fatal(err)
	}
	log := "check-runs.jsonl"
	if step.Doing == "building" {
		log = "test-runs.jsonl"
	}
	finished269Write(t, filepath.Join(path, log), []byte(`{"run":1,"passed":true,"at":"2026-10-30T00:43:00Z"}`+"\n"))
}

func finished269Read(t *testing.T, s *Server, at time.Time, private bool) ([]finished269Card, string) {
	t.Helper()
	b, err := s.liveJSON(at, private)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Steps []finished269Card `json:"steps"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	return body.Steps, string(b)
}

func finished269Want(t *testing.T, cards []finished269Card, step finished269Step) finished269Card {
	t.Helper()
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want the recorded step #%d: %+v", len(cards), step.Issue, cards)
	}
	c := cards[0]
	if c.Issue != step.Issue || c.Doing != step.Doing || c.Run != step.Run || c.By != step.Agent ||
		c.Since == nil || !c.Since.Equal(step.Since) || c.Ended == nil || !c.Ended.Equal(*step.Ended) {
		t.Fatalf("card must carry the recorded identity and times: %+v; want %+v", c, step)
	}
	if c.Ended.Sub(*c.Since) != step.Ended.Sub(step.Since) || c.Limit != step.Limit || c.Now != "" {
		t.Errorf("finished duration/limit/activity = %v / %v / %q; want %v / %v / empty", c.Ended.Sub(*c.Since), c.Limit, c.Now, step.Ended.Sub(step.Since), step.Limit)
	}
	return c
}

func TestLiveUsesRecordedEndAfterUnattendedCompletion(t *testing.T) {
	f := newFinished269(t, "owner/app")
	run := finished269Run(227, "formalizing", "draft-opaque-269", finished269At(0, 21))
	f.transcript(t, "owner/app", run, false)
	f.status(t, "owner/app", os.Getpid(), []finished269Step{run}, nil)
	before, _ := finished269Read(t, f.s, finished269At(0, 35), false)
	if len(before) != 1 || before[0].Ended != nil {
		t.Fatalf("expected running draft: %+v", before)
	}
	// No page asks between the initial observation and the next morning.
	f.transcript(t, "owner/app", run, true)
	ended := finished269End(run, finished269At(0, 45))
	f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{ended})
	for _, at := range []time.Time{finished269At(7, 29), finished269At(23, 59)} {
		cards, _ := finished269Read(t, f.s, at, false)
		c := finished269Want(t, cards, ended)
		if c.Turns != 3 || c.Thinking != 123 || !c.ThinkingExact || c.Output != 321 || len(c.Runs) != 1 || !c.Runs[0].Passed {
			t.Errorf("completion must read the final transcript and checks, not the last page's snapshot: %+v", c)
		}
	}
}

func TestLiveRestoresFinishedCardsAfterDashboardRestart(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		for _, multiple := range []bool{false, true} {
			t.Run(fmt.Sprintf("stopped_%v/work_list_%v", stopped, multiple), func(t *testing.T) {
				f := newFinished269(t, "owner/app")
				step := finished269End(finished269Run(227, "building", "build-opaque-269", finished269At(0, 21)), finished269At(0, 45))
				f.transcript(t, "owner/app", step, true)
				pid := os.Getpid()
				if stopped {
					pid = 0
				}
				f.status(t, "owner/app", pid, nil, []finished269Step{step})
				for i := 0; i < 2; i++ {
					// Each Server starts with no live cache or prior page request.
					s := &Server{Work: f.work, Repos: f.s.Repos}
					if multiple {
						s.Work, s.Works = "", []string{f.work}
					}
					cards, _ := finished269Read(t, s, finished269At(7+i, 29), false)
					c := finished269Want(t, cards, step)
					if c.Turns != 3 || c.Output != 321 {
						t.Errorf("restarted dashboard lost final counts: %+v", c)
					}
				}
			})
		}
	}
}

func TestLiveKeepsLatestFinishersAndTiesPerRepository(t *testing.T) {
	f := newFinished269(t, "owner/app", "owner/other")
	a := finished269Run(1, "formalizing", "z-earlier-run", finished269At(0, 21))
	b := finished269Run(2, "building", "a-later-run", finished269At(0, 25))
	c := finished269Run(3, "formalizing", "c-tied-run", finished269At(0, 30))
	d := finished269Run(4, "answering", "d-tied-run", finished269At(0, 31))
	other := finished269End(finished269Run(1, "formalizing", "other-repo-run", finished269At(0, 20)), finished269At(0, 35))
	for _, step := range []finished269Step{a, b, c, d} {
		f.transcript(t, "owner/app", step, true)
	}
	f.transcript(t, "owner/other", other, true)
	f.status(t, "owner/other", os.Getpid(), nil, []finished269Step{other})
	// Only A is observed. B, C and D finish without another page read.
	f.status(t, "owner/app", os.Getpid(), []finished269Step{a}, nil)
	finished269Read(t, f.s, finished269At(0, 36), false)
	f.status(t, "owner/app", os.Getpid(), []finished269Step{b}, []finished269Step{finished269End(a, finished269At(0, 40))})
	bf := finished269End(b, finished269At(0, 45))
	f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{bf})
	check := func(at time.Time, want ...finished269Step) {
		t.Helper()
		cards, _ := finished269Read(t, f.s, at, false)
		byRepo := map[string][]finished269Card{}
		for _, card := range cards {
			byRepo[card.Repo] = append(byRepo[card.Repo], card)
		}
		finished269Want(t, byRepo["owner/other"], other)
		app := byRepo["owner/app"]
		if len(app) != len(want) {
			t.Fatalf("retained app cards %+v, want %+v", app, want)
		}
		for i, step := range want {
			finished269Want(t, app[i:i+1], step)
		}
	}
	check(finished269At(7, 29), bf)
	cf, df := finished269End(c, finished269At(1, 0)), finished269End(d, finished269At(1, 0))
	// Reverse publication order: display still uses the existing start order.
	f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{df, cf})
	check(finished269At(8, 29), cf, df)
	// A retry of the same issue has a different opaque run identity.
	retry := finished269End(finished269Run(3, "formalizing", "a-new-retry", finished269At(2, 0)), finished269At(2, 10))
	f.transcript(t, "owner/app", retry, true)
	f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{retry})
	check(finished269At(9, 29), retry)
}

func TestLiveFinishedTitlesDescribeEndedAttempts(t *testing.T) {
	for _, tc := range []struct{ doing, running, ended string }{
		{"formalizing", "Drafting", "Draft step ended"},
		{"answering", "Answering", "Answer step ended"},
		{"building", "Building", "Build step ended"},
	} {
		t.Run(tc.doing, func(t *testing.T) {
			f := newFinished269(t, "owner/app")
			run := finished269Run(269, tc.doing, "opaque-title-run", finished269At(0, 21))
			f.transcript(t, "owner/app", run, false)
			f.status(t, "owner/app", os.Getpid(), []finished269Step{run}, nil)
			cards, _ := finished269Read(t, f.s, finished269At(0, 35), false)
			if len(cards) != 1 || cards[0].Title != tc.running {
				t.Errorf("running card title: %+v; want %q", cards, tc.running)
			}
			// A stopped attempt still ended; its heading must not claim success.
			path := filepath.Join(f.work, "owner/app/issues/issue-269", run.Run, "transcript.jsonl")
			out, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := out.WriteString("{\"type\":\"turn.failed\",\"error\":\"PRIVATE269_FAILURE\"}\n")
			closeErr := out.Close()
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			ended := finished269End(run, finished269At(0, 45))
			f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{ended})
			cards, _ = finished269Read(t, f.s, finished269At(7, 29), false)
			card := finished269Want(t, cards, ended)
			if card.Title != tc.ended || card.Agent != "stopped" {
				t.Errorf("ended attempt title/result = %q / %q, want %q / stopped", card.Title, card.Agent, tc.ended)
			}
		})
	}
}

func TestLiveFinishedCardsKeepPublicPrivacy(t *testing.T) {
	f := newFinished269(t, "owner/app")
	run := finished269Run(269, "building", "opaque-private-run", finished269At(0, 21))
	f.transcript(t, "owner/app", run, true)
	f.status(t, "owner/app", os.Getpid(), []finished269Step{run}, nil)
	private, _ := finished269Read(t, f.s, finished269At(0, 35), true)
	if len(private) != 1 || len(private[0].Commands) == 0 {
		t.Fatal("fixture must first read a private command-bearing running card")
	}
	ended := finished269End(run, finished269At(0, 45))
	f.status(t, "owner/app", os.Getpid(), nil, []finished269Step{ended})
	var prior Live
	for i, commands := range []bool{true, false, true, false} {
		cards, raw := finished269Read(t, f.s, finished269At(7+i, 29), commands)
		card := finished269Want(t, cards, ended)
		if len(card.Commands) != 0 {
			t.Error("ended card retained private commands")
		}
		for _, forbidden := range []string{"PRIVATE269", "cat inside.go", "cat /Users/", f.workspace, f.work, `"commands"`, `"command"`} {
			if strings.Contains(raw, forbidden) {
				t.Errorf("finished live response leaked %q", forbidden)
			}
		}
		if len(card.Files) != 1 || card.Files[0].Path != "inside.go" || card.Files[0].Reads != 1 || card.Tools["read"] != 2 {
			t.Errorf("finished view lost public counts and relative paths: %+v", card)
		}
		if i > 0 && !reflect.DeepEqual(prior, card.Live) {
			t.Errorf("private and public reads changed the finished view")
		}
		prior = card.Live
	}
}

func TestLiveDoesNotInventUnrecordedCompletion(t *testing.T) {
	for _, dead := range []bool{false, true} {
		t.Run(fmt.Sprintf("dead_watcher_%v", dead), func(t *testing.T) {
			f := newFinished269(t, "owner/app")
			run := finished269Run(269, "building", "unrecorded-run", finished269At(0, 21))
			f.transcript(t, "owner/app", run, true)
			f.status(t, "owner/app", os.Getpid(), []finished269Step{run}, nil)
			cards, _ := finished269Read(t, f.s, finished269At(0, 35), false)
			if len(cards) != 1 || cards[0].Ended != nil {
				t.Fatal("fixture must first observe a running step")
			}
			pid, steps := os.Getpid(), []finished269Step(nil)
			if dead {
				pid, steps = 0, []finished269Step{run}
			}
			f.status(t, "owner/app", pid, steps, nil)
			cards, _ = finished269Read(t, f.s, finished269At(7, 29), false)
			if len(cards) != 0 {
				t.Errorf("no recorded completion exists: must not resurrect or stamp the old card at read time: %+v", cards)
			}
		})
	}
}
