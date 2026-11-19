package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// turn948Card is a live card as the page reads it.
type turn948Card struct {
	Repo   string     `json:"repo"`
	Issue  int        `json:"issue"`
	Title  string     `json:"title"`
	Since  *time.Time `json:"since"`
	Turn   *time.Time `json:"turn"`
	Waits  *time.Time `json:"waits"`
	Last   *time.Time `json:"last"`
	HeldBy *struct {
		Repo  string `json:"repo"`
		Issue int    `json:"issue"`
		Doing string `json:"doing"`
	} `json:"heldBy"`
}

// turn948Now is the headline and each watcher, as the page reads them.
type turn948Now struct {
	Now struct {
		Headline string `json:"headline"`
		Running  int    `json:"running"`
		Waiting  int    `json:"waiting"`
	} `json:"now"`
	NowBy map[string]struct {
		Headline string `json:"headline"`
		Running  int    `json:"running"`
		Waiting  int    `json:"waiting"`
	} `json:"nowBy"`
	Repos []struct {
		Name    string `json:"name"`
		Factory struct {
			Waiting []int `json:"waiting"`
		} `json:"factory"`
	} `json:"repos"`
}

// A Codex run waits for Codex's turn, which one run holds at a time on a
// machine, and its time limit starts once its turn has come (#948). The
// watchers record that already: the run's transcript is made as it starts
// to wait, and stays empty until Codex's backend writes its first line,
// stamped, once the turn comes. A waiting run says so and what holds the
// turn, with no clock, and the headline counts it apart from what runs.
// Once it has its turn, its card is a running one, timed from its turn.
func TestARunWaitingForItsTurnSaysSoThenTakesIt948(t *testing.T) {
	work := t.TempDir()
	at := time.Date(2026, 11, 19, 12, 15, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	write := func(path, text string, mtime time.Time) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	build := Step{Issue: 944, Doing: "building", Since: at, Agent: "codex", Run: "build-944"}
	draft := Step{Issue: 946, Doing: "formalizing", Since: at.Add(time.Minute), Agent: "codex", Run: "formalize-946"}
	onair := Step{Issue: 16, Doing: "formalizing", Since: at.Add(2 * time.Minute), Agent: "codex", Run: "formalize-16"}
	for repo, steps := range map[string][]Step{"gitdek/invariant": {build, draft}, "gitdek/onair": {onair}} {
		st := Status{PID: os.Getpid(), Repo: repo, Started: at, Heartbeat: at, Every: 30, Limit: 9000}
		st.Update(steps, at)
		if err := WriteStatus(StatusPath(work, repo), st); err != nil {
			t.Fatal(err)
		}
	}
	transcript := func(repo string, s Step) string {
		return filepath.Join(stepDir(work, repo, s), "transcript.jsonl")
	}
	// #944's run has its turn and works. #946's and On Air's #16's runs
	// have begun to wait: their transcripts are there, and empty.
	held := `{"timestamp":"` + stamp(10*time.Second) + `","type":"invariant.workspace","cwd":"/nowhere"}` + "\n" +
		`{"timestamp":"` + stamp(11*time.Second) + `","type":"turn.started"}` + "\n"
	write(transcript("gitdek/invariant", build), held, at.Add(time.Minute))
	write(transcript("gitdek/invariant", draft), "", at.Add(90*time.Second))
	write(transcript("gitdek/onair", onair), "", at.Add(150*time.Second))

	s := &Server{Work: work, Works: []string{work}, Branch: "main", Repos: []*Repo{{Name: "gitdek/invariant"}, {Name: "gitdek/onair"}}}
	now := time.Now()
	cards := func() ([]turn948Card, map[int]turn948Card) {
		t.Helper()
		now = now.Add(3 * time.Second) // past the live view's cache
		b, err := s.LiveJSON(now)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Steps []turn948Card `json:"steps"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatal(err)
		}
		by := map[int]turn948Card{}
		for _, c := range body.Steps {
			by[c.Issue] = c
		}
		return body.Steps, by
	}
	headline := func() turn948Now {
		t.Helper()
		b, err := json.Marshal(s.assemble(at.Add(2 * time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		var n turn948Now
		if err := json.Unmarshal(b, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	holds := func(c turn948Card, repo string, issue int, doing string) bool {
		return c.HeldBy != nil && c.HeldBy.Repo == repo && c.HeldBy.Issue == issue && c.HeldBy.Doing == doing
	}

	list, by := cards()
	if len(list) != 3 {
		t.Fatalf("got %d cards, want #944's build and the two drafts that wait: %+v", len(list), list)
	}
	w := by[946]
	if w.Waits == nil || !w.Waits.Equal(at.Add(90*time.Second)) || w.Turn != nil || w.Last != nil || w.Title != "Waiting to draft" {
		t.Errorf("a draft waiting for Codex's turn reads %+v; want it waiting since its transcript was made, with no clock or activity, headed as waiting", w)
	}
	if !holds(w, "gitdek/invariant", 944, "building") || !holds(by[16], "gitdek/invariant", 944, "building") {
		t.Errorf("the waiting runs name %+v and %+v as holding the turn; want #944's build, across repositories", w.HeldBy, by[16].HeldBy)
	}
	if b := by[944]; b.Waits != nil || b.HeldBy != nil || b.Turn == nil || !b.Turn.Equal(at.Add(10*time.Second)) || b.Since == nil || !b.Since.Equal(at) {
		t.Errorf("the build holding the turn reads %+v; want it running, timed from its turn, with its step's start kept", b)
	}
	if list[0].Issue != 944 || list[1].Issue != 946 || list[2].Issue != 16 {
		t.Errorf("cards come %d, %d, %d; want what runs first, then the waits in the order they began", list[0].Issue, list[1].Issue, list[2].Issue)
	}
	n := headline()
	if n.Now.Running != 1 || n.Now.Waiting != 2 || n.Now.Headline != "Building #944 · waiting for Codex's turn on #946 and onair#16" {
		t.Errorf("the headline says %q, %d running and %d waiting; want #944 running and the two drafts waiting their turn", n.Now.Headline, n.Now.Running, n.Now.Waiting)
	}
	if o := n.NowBy["gitdek/onair"]; o.Running != 0 || o.Waiting != 1 || o.Headline != "Waiting for Codex's turn on onair#16" {
		t.Errorf("On Air's headline says %q, %d running and %d waiting; want its draft waiting its turn", o.Headline, o.Running, o.Waiting)
	}
	if !reflect.DeepEqual(n.Repos[0].Factory.Waiting, []int{946}) || !reflect.DeepEqual(n.Repos[1].Factory.Waiting, []int{16}) {
		t.Errorf("the watchers' waiting steps are %v and %v; want [946] and [16]", n.Repos[0].Factory.Waiting, n.Repos[1].Factory.Waiting)
	}

	// #944's run gives its turn back, and #946's takes it: its backend writes
	// the transcript's first line, stamped.
	write(transcript("gitdek/invariant", build), held+`{"timestamp":"`+stamp(80*time.Minute)+`","type":"turn.completed","usage":{"output_tokens":7}}`+"\n", at.Add(80*time.Minute))
	write(transcript("gitdek/invariant", draft), `{"timestamp":"`+stamp(81*time.Minute)+`","type":"invariant.workspace","cwd":"/nowhere"}`+"\n"+
		`{"timestamp":"`+stamp(81*time.Minute)+`","type":"turn.started"}`+"\n", at.Add(81*time.Minute))
	_, by = cards()
	if d := by[946]; d.Waits != nil || d.HeldBy != nil || d.Turn == nil || !d.Turn.Equal(at.Add(81*time.Minute)) || d.Title != "Drafting" {
		t.Errorf("the draft that took its turn reads %+v; want it drafting, timed from its turn", d)
	}
	if !holds(by[16], "gitdek/invariant", 946, "formalizing") {
		t.Errorf("On Air's waiting draft names %+v as holding the turn; want #946's draft, which took it", by[16].HeldBy)
	}
	if n := headline(); n.Now.Running != 2 || n.Now.Waiting != 1 {
		t.Errorf("after #946 took its turn the headline counts %d running and %d waiting; want 2 and 1", n.Now.Running, n.Now.Waiting)
	}
}

// Only a Codex run waits for a turn: an empty transcript of another agent's
// run, or of a run whose agent the watcher hasn't named, is a running card,
// as before (#948).
func TestOnlyACodexRunWaitsForItsTurn948(t *testing.T) {
	for _, agent := range []string{"claude-code", ""} {
		f := newLiveStepFixture(t)
		st := Status{PID: os.Getpid(), Repo: "gitdek/app", Started: f.since, Heartbeat: f.since, Every: 30, Limit: 9000,
			Steps: []Step{{Issue: 7, Doing: "building", Since: f.since, Agent: agent, Run: "build-20260928-120000"}}}
		if err := WriteStatus(f.status, st); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.transcript, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if l := f.one(t); l.Waits != nil || l.Title != "Building" {
			t.Errorf("agent %q: an empty transcript reads as %+v; want a running build", agent, l)
		}
		if _, ok := waitsForTurn(f.work, "gitdek/app", st.Steps[0]); ok {
			t.Errorf("agent %q: the snapshot counts a run that doesn't wait for a turn as waiting", agent)
		}
	}
}
