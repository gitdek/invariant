package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// liveStepFixture is a watcher building issue 7 of gitdek/app: its status,
// the agent's copy of the repository, and the step's directory.
type liveStepFixture struct {
	s          *Server
	work, ws   string
	status     string
	transcript string
	since      time.Time
	now        time.Time
}

func newLiveStepFixture(t *testing.T) *liveStepFixture {
	t.Helper()
	f := &liveStepFixture{work: t.TempDir(), ws: t.TempDir(), since: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), now: time.Now()}
	f.status = StatusPath(f.work, "gitdek/app")
	f.setDoing(t, "building")
	for p, text := range map[string]string{"internal/app/app.go": "package app\n\nfunc New() {}\n", "internal/app/new_test.go": "package app\n"} {
		full := filepath.Join(f.ws, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	step := filepath.Join(f.work, "gitdek", "app", "issues", "issue-7", "build-20260928-120000")
	if err := os.MkdirAll(step, 0o755); err != nil {
		t.Fatal(err)
	}
	runs := `{"run":1,"passed":false,"failed":["code"],"at":"2026-09-28T12:01:40Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(step, "gate-runs.jsonl"), []byte(runs), 0o644); err != nil {
		t.Fatal(err)
	}
	f.transcript = filepath.Join(step, "transcript.jsonl")
	f.s = &Server{Work: f.work, Repos: []*Repo{{Name: "gitdek/app", Status: f.status}}}
	return f
}

func (f *liveStepFixture) setDoing(t *testing.T, doing string) {
	t.Helper()
	st := Status{PID: os.Getpid(), Repo: "gitdek/app", Started: f.since, Heartbeat: f.since, Every: 30, Limit: 9000}
	if doing != "" {
		since := f.since
		st.Issue, st.Doing, st.Since = 7, doing, &since
	}
	if err := WriteStatus(f.status, st); err != nil {
		t.Fatal(err)
	}
}

// ev is a transcript line.
func ev(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lines is the step's transcript: every kind of event a coding agent's
// stream writes, with code, words, thinking, a search pattern, a command, a
// tool's output, the model and a path outside the copy in them. SECRET
// marks everything the page must never see.
func (f *liveStepFixture) lines(t *testing.T) []string {
	app := filepath.Join(f.ws, "internal", "app", "app.go")
	usage := func(in, read, made int) map[string]any {
		return map[string]any{"input_tokens": in, "cache_read_input_tokens": read, "cache_creation_input_tokens": made, "output_tokens": 4}
	}
	assistant := func(at, id string, u map[string]any, content ...map[string]any) string {
		return ev(t, map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"id": id, "model": "claude-SECRETMODEL", "content": content, "usage": u}})
	}
	user := func(at, body string) string {
		return ev(t, map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"content": []map[string]any{{"type": "tool_result", "content": body}}}, "tool_use_result": map[string]any{"stdout": body}})
	}
	tool := func(name string, input map[string]any) map[string]any {
		return map[string]any{"type": "tool_use", "id": "t-" + name, "name": name, "input": input}
	}
	thinking := func(delta int) string {
		return ev(t, map[string]any{"type": "system", "subtype": "thinking_tokens", "estimated_tokens": delta, "estimated_tokens_delta": delta})
	}
	return []string{
		ev(t, map[string]any{"type": "system", "subtype": "init", "cwd": f.ws, "model": "claude-SECRETMODEL", "tools": []string{"Read", "SECRETTOOLLIST"}}),
		thinking(50),
		thinking(100),
		assistant("2026-09-28T12:00:10Z", "m1", usage(3, 1000, 200),
			map[string]any{"type": "thinking", "thinking": "SECRETTHOUGHT"}, tool("Read", map[string]any{"file_path": app})),
		user("2026-09-28T12:00:11Z", "package app // SECRETFILEBODY"),
		ev(t, map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "allowed", "note": "SECRETRATE"}}),
		assistant("2026-09-28T12:00:20Z", "m2", usage(2, 1500, 0),
			map[string]any{"type": "text", "text": "SECRETWORDS"}, tool("Grep", map[string]any{"pattern": "SECRETPATTERN", "path": f.ws})),
		user("2026-09-28T12:00:21Z", "internal/app/app.go SECRETMATCH"),
		assistant("2026-09-28T12:00:30Z", "m3", usage(2, 1600, 0), tool("Read", map[string]any{"file_path": "/Users/SECRETUSER/.netrc"})),
		user("2026-09-28T12:00:31Z", "SECRETNETRC"),
		thinking(400),
		assistant("2026-09-28T12:01:00Z", "m4", usage(2, 1700, 0),
			tool("Edit", map[string]any{"file_path": app, "old_string": "func New() {}", "new_string": "func SECRETCODE() {}"})),
		user("2026-09-28T12:01:01Z", "SECRETEDITED"),
		assistant("2026-09-28T12:01:10Z", "m5", usage(2, 1800, 0),
			tool("Write", map[string]any{"file_path": "internal/app/new_test.go", "content": "package app // SECRETWRITTEN"})),
		user("2026-09-28T12:01:11Z", "SECRETWROTE"),
		ev(t, map[string]any{"type": "system", "subtype": "task_summary", "detail": "SECRETSUMMARY"}),
		assistant("2026-09-28T12:01:20Z", "m6", usage(2, 1900, 0), tool("Bash", map[string]any{"command": "cat SECRETCOMMAND"})),
		user("2026-09-28T12:01:21Z", "SECRETSTDOUT"),
		assistant("2026-09-28T12:01:30Z", "m7", usage(1, 2000, 100), tool("mcp__invariant__gate", map[string]any{})),
		ev(t, map[string]any{"type": "tool_progress", "tool_name": "mcp__invariant__gate", "elapsed_time_seconds": 30}),
		ev(t, map[string]any{"type": "system", "subtype": "post_turn_summary", "status_detail": "SECRETSTATUS"}),
		assistant("2026-09-28T12:02:00Z", "m8", usage(1, 2100, 0), tool("Read", map[string]any{"file_path": app})),
	}
}

// write writes the transcript: every line but the last whole, and the last
// cut off mid-write unless whole is set. extra lines follow a whole last
// line.
func (f *liveStepFixture) write(t *testing.T, whole bool, extra ...string) {
	t.Helper()
	ls := f.lines(t)
	text := strings.Join(ls[:len(ls)-1], "\n") + "\n"
	last := ls[len(ls)-1]
	if whole {
		text += last + "\n"
		for _, x := range extra {
			text += x + "\n"
		}
	} else {
		text += last[:len(last)/2]
	}
	if err := os.WriteFile(f.transcript, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// read serves the live view, as the page gets it. Each read is later than
// the one before, so none is served from the last.
func (f *liveStepFixture) read(t *testing.T) ([]Live, string) {
	t.Helper()
	f.now = f.now.Add(liveEvery + time.Second)
	b, err := f.s.LiveJSON(f.now)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Steps []Live `json:"steps"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got.Steps, string(b)
}

func (f *liveStepFixture) one(t *testing.T) Live {
	t.Helper()
	steps, _ := f.read(t)
	if len(steps) != 1 {
		t.Fatalf("%d steps, want the running one", len(steps))
	}
	return steps[0]
}

func fileAt(l Live, p string) *LiveFile {
	for i := range l.Files {
		if l.Files[i].Path == p {
			return &l.Files[i]
		}
	}
	return nil
}

// The live view counts a running step's turns, tokens and tool calls, lays
// out its tool calls and stretches of thinking in time, lists the files it
// touched inside its copy, and shows its gate runs with the checks that
// failed. It skips the line still being written.
func TestLiveReadsARunningStep(t *testing.T) {
	f := newLiveStepFixture(t)
	f.write(t, false)
	l := f.one(t)

	if l.Repo != "gitdek/app" || l.Issue != 7 || l.Doing != "building" || l.Limit != 9000 || l.Since == nil || !l.Since.Equal(f.since) {
		t.Errorf("step %s #%d %s since %v, limit %v", l.Repo, l.Issue, l.Doing, l.Since, l.Limit)
	}
	if l.Turns != 7 || l.Thinking != 550 || l.Context != 2101 {
		t.Errorf("%d turns, %d thinking tokens, %d in context; want 7, 550 and 2,101", l.Turns, l.Thinking, l.Context)
	}
	if want := map[string]int{"read": 2, "search": 1, "edit": 2, "other": 1, "gate": 1}; !reflect.DeepEqual(l.Tools, want) {
		t.Errorf("tool calls %v, want %v", l.Tools, want)
	}
	var kinds []string
	for _, m := range l.Marks {
		kinds = append(kinds, m.Kind)
	}
	if want := []string{"think", "read", "search", "read", "think", "edit", "edit", "other", "gate"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("marks %v, want %v", kinds, want)
	}
	first, second := l.Marks[0], l.Marks[4]
	if !first.At.Equal(f.since) || first.Until == nil || !first.Until.Equal(time.Date(2026, 9, 28, 12, 0, 10, 0, time.UTC)) || first.Tokens != 150 {
		t.Errorf("the first stretch of thinking %+v: want from the step's start to 12:00:10, 150 tokens", first)
	}
	if !second.At.Equal(time.Date(2026, 9, 28, 12, 0, 31, 0, time.UTC)) || second.Until == nil || !second.Until.Equal(time.Date(2026, 9, 28, 12, 1, 0, 0, time.UTC)) || second.Tokens != 400 {
		t.Errorf("the second stretch of thinking %+v: want 12:00:31 to 12:01:00, 400 tokens", second)
	}
	if !l.Marks[8].At.Equal(time.Date(2026, 9, 28, 12, 1, 30, 0, time.UTC)) {
		t.Errorf("the gate call at %v, want 12:01:30", l.Marks[8].At)
	}
	if l.Now != "gate" {
		t.Errorf("now %q, want the gate it's running", l.Now)
	}
	if l.Last == nil {
		t.Error("no time for the transcript's last change")
	}

	if len(l.Files) != 2 {
		t.Fatalf("files %+v, want the two inside the copy", l.Files)
	}
	app, newTest := fileAt(l, "internal/app/app.go"), fileAt(l, "internal/app/new_test.go")
	if app == nil || newTest == nil {
		t.Fatalf("files %+v, want paths inside the repository", l.Files)
	}
	if app.Reads != 1 || app.Edits != 1 || app.Size != int64(len("package app\n\nfunc New() {}\n")) || app.Lit {
		t.Errorf("app.go %+v, want 1 read, 1 edit and its size now", *app)
	}
	if newTest.Reads != 0 || newTest.Edits != 1 || newTest.Size != int64(len("package app\n")) || !newTest.Lit {
		t.Errorf("new_test.go %+v, want 1 edit, its size now, and lit as the last one touched", *newTest)
	}
	if l.RunsKind != "gate" || len(l.Runs) != 1 || l.Runs[0].Passed || !reflect.DeepEqual(l.Runs[0].Failed, []string{"code"}) {
		t.Errorf("checkpoints %s %+v, want one gate run that failed code", l.RunsKind, l.Runs)
	}
	if l.Agent != "" || l.Output != 0 || l.Ended != nil {
		t.Errorf("a running step says its agent %q, %d output tokens, ended %v", l.Agent, l.Output, l.Ended)
	}
}

// The dashboard reads only what's new in the transcript: a line cut off
// mid-write counts once it's whole, and nothing counts twice.
func TestLiveReadsOnlyWhatsNew(t *testing.T) {
	f := newLiveStepFixture(t)
	f.write(t, false)
	if l := f.one(t); l.Turns != 7 {
		t.Fatalf("%d turns before the last line is whole, want 7", l.Turns)
	}
	f.write(t, true)
	l := f.one(t)
	if l.Turns != 8 || l.Tools["read"] != 3 || len(l.Marks) != 10 || l.Now != "read" {
		t.Errorf("after the last line landed: %d turns, tools %v, %d marks, now %q; want 8, 3 reads, 10 marks and a read", l.Turns, l.Tools, len(l.Marks), l.Now)
	}
	if app := fileAt(l, "internal/app/app.go"); app == nil || app.Reads != 2 || !app.Lit {
		t.Errorf("app.go %+v, want 2 reads, and lit again", app)
	}
	if again := f.one(t); again.Turns != 8 || again.Tools["read"] != 3 || len(again.Marks) != 10 {
		t.Errorf("a read with nothing new changed the counts: %d turns, tools %v, %d marks", again.Turns, again.Tools, len(again.Marks))
	}
}

// None of the transcript's code, words, thinking, search patterns,
// commands, tool output, model, the copy's location or paths outside it
// reach the page (D-0051, D-0052).
func TestLiveShowsNothingPrivate(t *testing.T) {
	f := newLiveStepFixture(t)
	f.write(t, true, ev(t, map[string]any{"type": "result", "subtype": "success", "result": "SECRETRESULT", "usage": map[string]any{"output_tokens": 99}}))
	_, out := f.read(t)
	for _, secret := range []string{"SECRET", "claude", ".netrc", "func New", "package app", f.ws, f.work} {
		if strings.Contains(out, secret) {
			t.Errorf("the live view's JSON holds %q", secret)
		}
	}
	if !strings.Contains(out, `"internal/app/app.go"`) {
		t.Error("the live view doesn't name the file the agent read inside the repository")
	}
}

// Once the step ends, the page keeps showing it, with how the agent's run
// ended, until the next step starts.
func TestLiveKeepsTheEndedStep(t *testing.T) {
	f := newLiveStepFixture(t)
	f.write(t, true, ev(t, map[string]any{"type": "result", "subtype": "error_max_budget_usd", "is_error": true, "usage": map[string]any{"output_tokens": 4321}}))
	l := f.one(t)
	if l.Agent != "stopped at its spend cap" || l.Output != 4321 || l.Now != "" || l.Ended != nil {
		t.Errorf("agent %q, %d output tokens, now %q, ended %v; want stopped at its spend cap, 4,321, nothing, and the step still running", l.Agent, l.Output, l.Now, l.Ended)
	}
	f.setDoing(t, "")
	l = f.one(t)
	if l.Ended == nil || l.Issue != 7 || l.Turns != 8 || l.Agent != "stopped at its spend cap" {
		t.Errorf("after the step ended: ended %v, #%d, %d turns, agent %q; want the last step, marked ended", l.Ended, l.Issue, l.Turns, l.Agent)
	}
	for _, m := range l.Marks {
		if m.Kind == "think" && m.Until == nil {
			t.Errorf("an ended step still thinking: %+v", m)
		}
	}
}

// With a watcher per work directory, the page follows the one that's
// working, whichever holds the lease.
func TestLiveFollowsTheWorkingWatcher(t *testing.T) {
	f := newLiveStepFixture(t)
	f.write(t, true)
	idle := t.TempDir()
	if err := WriteStatus(StatusPath(idle, "gitdek/app"), Status{PID: os.Getpid(), Repo: "gitdek/app", Started: f.since, Heartbeat: f.since}); err != nil {
		t.Fatal(err)
	}
	f.s.Works = []string{idle, f.work}
	st, work := f.s.watcherOf(f.s.Repos[0])
	if work != f.work || st.Doing != "building" {
		t.Fatalf("watcher %s in %s, want the one building, in %s", st.Doing, work, f.work)
	}
	if l := f.one(t); l.Issue != 7 || l.Turns != 8 {
		t.Errorf("step #%d with %d turns, want the working watcher's", l.Issue, l.Turns)
	}
}

// The page reads every field of the live view.
func TestThePageReadsEveryLiveField(t *testing.T) {
	js, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{Live{}, Mark{}, LiveFile{}, RunMark{}} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
			if tag != "" && !strings.Contains(string(js), "."+tag) {
				t.Errorf("the page never reads %s.%s", rt.Name(), tag)
			}
		}
	}
}
