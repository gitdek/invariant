package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// These tests show every step the watcher is running on the page (D-0113):
// the live view lists each one, read from its own transcript, the headline
// names each one, and the page draws a card for each.

// esAt is a time on the morning of 2026-09-29.
func esAt(hour, minute int) time.Time {
	return time.Date(2026, 9, 29, hour, minute, 0, 0, time.UTC)
}

// esWrite writes v to a file as JSON in one step, so a reader never sees half
// of it.
func esWrite(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// esWatcher writes a live watcher's status to its work directory as the
// watcher writes it: every step it's running, its longest-running step that's
// doing something, the time limit on an agent's run, and, when holder is set,
// its name on the repository's lease.
func esWatcher(t *testing.T, work, repo, holder string, steps ...Step) {
	t.Helper()
	st := Status{PID: os.Getpid(), Repo: repo, Started: esAt(8, 0), Heartbeat: esAt(8, 0), Every: 60, Limit: 2400, Steps: steps}
	for _, s := range steps {
		if s.Doing != "" && (st.Since == nil || s.Since.Before(*st.Since)) {
			since := s.Since
			st.Issue, st.Doing, st.Since = s.Issue, s.Doing, &since
		}
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if holder != "" {
		file["holder"] = holder
	}
	esWrite(t, StatusPath(work, repo), file)
}

// esLine is one line of a coding agent's transcript.
func esLine(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// esInit is a transcript's first line, which names the agent's copy of the
// repository.
func esInit(t *testing.T, cwd string) string {
	return esLine(t, map[string]any{"type": "system", "subtype": "init", "cwd": cwd})
}

// esThink is a stretch of n tokens of thinking.
func esThink(t *testing.T, n int) string {
	return esLine(t, map[string]any{"type": "system", "subtype": "thinking_tokens", "estimated_tokens": n, "estimated_tokens_delta": n})
}

// esCall is a turn of the agent's that calls a tool, holding context tokens.
func esCall(t *testing.T, at time.Time, id string, context int, tool string, input map[string]any) string {
	return esLine(t, map[string]any{"type": "assistant", "timestamp": at.Format(time.RFC3339), "message": map[string]any{
		"id":      id,
		"usage":   map[string]any{"input_tokens": 0, "cache_read_input_tokens": context, "cache_creation_input_tokens": 0, "output_tokens": 5},
		"content": []map[string]any{{"type": "tool_use", "id": "tool-" + id, "name": tool, "input": input}},
	}})
}

// esResult is a tool's result coming back.
func esResult(t *testing.T, at time.Time) string {
	return esLine(t, map[string]any{"type": "user", "timestamp": at.Format(time.RFC3339), "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "content": "done"}},
	}})
}

// esStepDir writes a step's directory under a watcher's work directory, named
// for the step and when it started, such as build-20260929-100000: its
// transcript, and when log is set, its checks' log, one line per check.
func esStepDir(t *testing.T, work, repo string, issue int, name string, transcript []string, log string, checks ...string) {
	t.Helper()
	dir := filepath.Join(work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", issue), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(strings.Join(transcript, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if log != "" {
		if err := os.WriteFile(filepath.Join(dir, log), []byte(strings.Join(checks, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// esLive is the live view, as the page reads it from /api/live.json.
func esLive(t *testing.T, s *Server) []Live {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/live.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/live.json: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Steps []Live `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Steps
}

// esSteps names the steps a live view lists, in its order, as
// "#148 building, #150 building".
func esSteps(steps []Live) string {
	var out []string
	for _, l := range steps {
		s := fmt.Sprintf("#%d %s", l.Issue, l.Doing)
		if l.Ended != nil {
			s += " (ended)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// esGH is a gh that answers the dashboard's reads of a repository's lease
// from a directory: lease.json is the lease's ref, and commits/<sha>.json is
// each commit the ref can name. Every other read is Not Found, as for a
// repository with no issues, commits or runs to show.
const esGH = `#!/bin/sh
d='@DIR@'
for last; do :; done
missing() { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
case "$last" in
*/git/ref/invariant/lease) [ -f "$d/lease.json" ] || missing; cat "$d/lease.json" ;;
*/git/commits/*) f="$d/commits/${last##*/}.json"; [ -f "$f" ] || missing; cat "$f" ;;
*) missing ;;
esac
`

// esServer starts a dashboard for repo, reading GitHub through a fake gh in
// the directory it returns, and the watchers' statuses from works, the first
// of which is its -work.
func esServer(t *testing.T, repo string, works ...string) (*Server, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	fake, err := os.MkdirTemp("", "invariant-steps-gh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(fake) })
	if err := os.WriteFile(filepath.Join(fake, "gh"), []byte(strings.ReplaceAll(esGH, "@DIR@", fake)), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := github.Client{Repo: repo, GH: filepath.Join(fake, "gh")}
	s := &Server{Repos: []*Repo{{Name: repo, GitHub: gh, Status: StatusPath(works[0], repo)}}, Branch: "main",
		Work: works[0], Works: works, Every: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
	})
	s.Start(ctx)
	return s, fake
}

// esLease gives the fake's lease to holder, in a commit of its own, as a
// watcher does when it takes the lease.
func esLease(t *testing.T, fake, holder, sha string) {
	t.Helper()
	msg, err := json.Marshal(map[string]any{"holder": holder, "until": time.Now().Add(time.Hour).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	esWrite(t, filepath.Join(fake, "commits", sha+".json"), map[string]any{"sha": sha, "message": string(msg)})
	esWrite(t, filepath.Join(fake, "lease.json"), map[string]any{"ref": "refs/invariant/lease", "object": map[string]any{"sha": sha, "type": "commit"}})
}

// esSnapshot is what the page reads of the snapshot here: the headline, how
// many steps it says are running, and each repository's lease.
type esSnapshot struct {
	Now struct {
		Headline string `json:"headline"`
		Running  int    `json:"running"`
	} `json:"now"`
	Repos []struct {
		Lease *struct {
			Holder string `json:"holder"`
		} `json:"lease"`
	} `json:"repos"`
}

// esState is the snapshot the page gets now, and whether there's one yet.
func esState(t *testing.T, s *Server) (esSnapshot, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
	var snap esSnapshot
	if rec.Code != http.StatusOK {
		return snap, false
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	return snap, true
}

// esSoon waits up to half a minute for ok, and fails the test with what it
// last saw if ok never holds.
func esSoon(t *testing.T, what string, ok func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		done, saw := ok()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited half a minute for %s; last saw %s", what, saw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// With three steps running in one watcher's work directory, the live view
// lists all three, longest-running first: #152's draft, then the builds of
// #148 and #150, a plumbing build that logs test runs. Each has its own issue,
// what it's doing and since when, with the watcher's time limit, and its own
// counts, files and checks, read from its own transcript and log. #155's step
// is doing nothing yet and #154's is committing its ratification, so neither
// has an agent at work, and #99's old step directory is for an issue the
// watcher isn't on: none of them is listed.
func TestTheLiveViewListsEveryRunningStep(t *testing.T) {
	const repo = "gitdek/invariant"
	work := t.TempDir()
	ws := map[int]string{99: t.TempDir(), 148: t.TempDir(), 150: t.TempDir(), 152: t.TempDir()}
	in := func(n int, p string) string { return filepath.Join(ws[n], filepath.FromSlash(p)) }
	esWatcher(t, work, repo, "",
		Step{Issue: 148, Doing: "building", Since: esAt(10, 0)},
		Step{Issue: 150, Doing: "building", Since: esAt(10, 5)},
		Step{Issue: 152, Doing: "formalizing", Since: esAt(9, 50)},
		Step{Issue: 154, Doing: "ratifying", Since: esAt(10, 8)},
		Step{Issue: 155, Since: esAt(10, 9)})
	at := func(from time.Time, sec int) time.Time { return from.Add(time.Duration(sec) * time.Second) }
	esStepDir(t, work, repo, 148, "build-20260929-100000", []string{
		esInit(t, ws[148]),
		esThink(t, 100),
		esCall(t, at(esAt(10, 0), 10), "a1", 1000, "Read", map[string]any{"file_path": in(148, "internal/a/a.go")}),
		esResult(t, at(esAt(10, 0), 11)),
		esCall(t, at(esAt(10, 0), 20), "a2", 1200, "Edit", map[string]any{"file_path": in(148, "internal/a/a.go"), "old_string": "x", "new_string": "y"}),
		esResult(t, at(esAt(10, 0), 21)),
	}, "gate-runs.jsonl", `{"run":1,"passed":false,"failed":["code"],"at":"2026-09-29T10:01:00Z"}`)
	esStepDir(t, work, repo, 150, "build-20260929-100500", []string{
		esInit(t, ws[150]),
		esCall(t, at(esAt(10, 5), 10), "b1", 2000, "Read", map[string]any{"file_path": in(150, "internal/b/b.go")}),
		esResult(t, at(esAt(10, 5), 11)),
		esCall(t, at(esAt(10, 5), 20), "b2", 2100, "Grep", map[string]any{"pattern": "func", "path": ws[150]}),
		esResult(t, at(esAt(10, 5), 21)),
		esCall(t, at(esAt(10, 5), 30), "b3", 2400, "Write", map[string]any{"file_path": in(150, "internal/b/b_test.go"), "content": "package b\n"}),
		esResult(t, at(esAt(10, 5), 31)),
	}, "test-runs.jsonl", `{"run":1,"passed":true,"at":"2026-09-29T10:06:00Z"}`)
	esStepDir(t, work, repo, 152, "formalize-20260929-095000", []string{
		esInit(t, ws[152]),
		esThink(t, 300),
		esCall(t, at(esAt(9, 50), 30), "c1", 3600, "mcp__invariant__check", map[string]any{}),
		esResult(t, esAt(9, 51)),
	}, "check-runs.jsonl", `{"run":1,"passed":false,"failed":["TypeOK"],"at":"2026-09-29T09:50:40Z"}`, `{"run":2,"passed":true,"at":"2026-09-29T09:50:50Z"}`)
	esStepDir(t, work, repo, 99, "build-20260928-090000", []string{
		esInit(t, ws[99]),
		esCall(t, at(esAt(9, 0), -86400), "d1", 500, "Read", map[string]any{"file_path": in(99, "old.go")}),
		esResult(t, at(esAt(9, 0), -86399)),
	}, "")
	s := &Server{Work: work, Works: []string{work}, Repos: []*Repo{{Name: repo, Status: StatusPath(work, repo)}}}

	steps := esLive(t, s)
	if got, want := esSteps(steps), "#152 formalizing, #148 building, #150 building"; got != want {
		t.Fatalf("the live view lists [%s]; want [%s]: every step with an agent at work, longest-running first", got, want)
	}
	wants := []struct {
		since                    time.Time
		turns, thinking, context int
		tools                    map[string]int
		files                    map[string][2]int // reads and edits, by path in the agent's copy
		runsKind                 string
		runs                     []bool // whether each check passed
	}{
		{esAt(9, 50), 1, 300, 3600, map[string]int{"check": 1}, map[string][2]int{}, "check", []bool{false, true}},
		{esAt(10, 0), 2, 100, 1200, map[string]int{"read": 1, "edit": 1}, map[string][2]int{"internal/a/a.go": {1, 1}}, "gate", []bool{false}},
		{esAt(10, 5), 3, 0, 2400, map[string]int{"read": 1, "search": 1, "edit": 1}, map[string][2]int{"internal/b/b.go": {1, 0}, "internal/b/b_test.go": {0, 1}}, "test", []bool{true}},
	}
	for i, w := range wants {
		l := steps[i]
		step := fmt.Sprintf("#%d's step", l.Issue)
		if l.Repo != repo || l.Since == nil || !l.Since.Equal(w.since) || l.Limit != 2400 || l.Ended != nil {
			t.Errorf("%s is in %s since %v with a limit of %vs, ended %v; want %s since %s, a limit of 2400s, and still running",
				step, l.Repo, l.Since, l.Limit, l.Ended, repo, w.since.Format("15:04"))
		}
		if l.Turns != w.turns || l.Thinking != w.thinking || l.Context != w.context {
			t.Errorf("%s has %d turns, %d thinking tokens and %d in context; want %d, %d and %d, from its own transcript",
				step, l.Turns, l.Thinking, l.Context, w.turns, w.thinking, w.context)
		}
		if !reflect.DeepEqual(l.Tools, w.tools) {
			t.Errorf("%s's tool calls are %v; want %v, from its own transcript", step, l.Tools, w.tools)
		}
		files := map[string][2]int{}
		for _, f := range l.Files {
			files[f.Path] = [2]int{f.Reads, f.Edits}
		}
		if !reflect.DeepEqual(files, w.files) {
			t.Errorf("%s's files, as reads and edits, are %v; want %v, the ones its own agent touched", step, files, w.files)
		}
		var runs []bool
		for _, r := range l.Runs {
			runs = append(runs, r.Passed)
		}
		if l.RunsKind != w.runsKind || !reflect.DeepEqual(runs, w.runs) {
			t.Errorf("%s's checks are %s runs that passed %v; want %s runs that passed %v, from its own log", step, l.RunsKind, runs, w.runsKind, w.runs)
		}
	}
}

// With steps running in two watchers' work directories, as for a moment when
// the lease moves, the live view shows the steps of the watcher whose status
// names the holder of the repository's lease, which the dashboard reads
// through GitHub, even when that one's work directory comes second. When the
// lease moves to the other watcher, the live view shows that one's steps.
func TestTheLiveViewShowsTheLeaseHoldersWatcher(t *testing.T) {
	const repo = "gitdek/app"
	a, b := t.TempDir(), t.TempDir()
	esWatcher(t, a, repo, "watcher-aaaaaaaaaaaa",
		Step{Issue: 5, Doing: "building", Since: esAt(9, 0)},
		Step{Issue: 6, Doing: "formalizing", Since: esAt(9, 10)})
	esWatcher(t, b, repo, "watcher-bbbbbbbbbbbb",
		Step{Issue: 7, Doing: "building", Since: esAt(9, 30)})
	// Each step's agent has read a file in as many turns as its issue says.
	turns := map[int]int{5: 2, 6: 1, 7: 4}
	for _, st := range []struct {
		work  string
		issue int
		dir   string
		since time.Time
	}{{a, 5, "build-20260929-090000", esAt(9, 0)}, {a, 6, "formalize-20260929-091000", esAt(9, 10)}, {b, 7, "build-20260929-093000", esAt(9, 30)}} {
		ws := t.TempDir()
		lines := []string{esInit(t, ws)}
		for k := 1; k <= turns[st.issue]; k++ {
			lines = append(lines,
				esCall(t, st.since.Add(time.Duration(10*k)*time.Second), fmt.Sprintf("m%d-%d", st.issue, k), 100*k, "Read", map[string]any{"file_path": filepath.Join(ws, "x.go")}),
				esResult(t, st.since.Add(time.Duration(10*k+1)*time.Second)))
		}
		esStepDir(t, st.work, repo, st.issue, st.dir, lines, "")
	}
	s, fake := esServer(t, repo, a, b)

	for _, c := range []struct {
		holder, sha, want string
	}{
		{"watcher-bbbbbbbbbbbb", strings.Repeat("b", 40), "#7 building"},
		{"watcher-aaaaaaaaaaaa", strings.Repeat("a", 40), "#5 building, #6 formalizing"},
	} {
		esLease(t, fake, c.holder, c.sha)
		esSoon(t, "the page to show that "+c.holder+" holds the lease", func() (bool, string) {
			snap, ok := esState(t, s)
			if !ok || len(snap.Repos) == 0 || snap.Repos[0].Lease == nil {
				return false, "no lease"
			}
			return snap.Repos[0].Lease.Holder == c.holder, "the lease held by " + snap.Repos[0].Lease.Holder
		})
		var steps []Live
		esSoon(t, fmt.Sprintf("the live view to list [%s], the steps of %s, which holds the lease", c.want, c.holder), func() (bool, string) {
			steps = esLive(t, s)
			return esSteps(steps) == c.want, "[" + esSteps(steps) + "]"
		})
		for _, l := range steps {
			if l.Turns != turns[l.Issue] {
				t.Errorf("with %s holding the lease, #%d's step has %d turns; want %d, from its own transcript", c.holder, l.Issue, l.Turns, turns[l.Issue])
			}
		}
	}
}

// While the watcher builds #148 and #150 and drafts #152, the headline names
// all three, grouped by what each is doing, longest-running first, and the
// snapshot says three steps are running. #155's step is doing nothing yet, so
// it's in neither. Once only #148's build runs, the headline is the one the
// page shows now for one step, and one step is running.
func TestTheHeadlineNamesEveryRunningStep(t *testing.T) {
	const repo = "gitdek/invariant"
	work := t.TempDir()
	esWatcher(t, work, repo, "",
		Step{Issue: 148, Doing: "building", Since: esAt(10, 0)},
		Step{Issue: 150, Doing: "building", Since: esAt(10, 2)},
		Step{Issue: 152, Doing: "formalizing", Since: esAt(10, 5)},
		Step{Issue: 155, Since: esAt(10, 9)})
	s, _ := esServer(t, repo, work)
	var snap esSnapshot
	esSoon(t, "the page's first snapshot", func() (bool, string) {
		var ok bool
		snap, ok = esState(t, s)
		return ok, "none"
	})
	if want := "Building #148 and #150 · drafting #152"; snap.Now.Headline != want || snap.Now.Running != 3 {
		t.Fatalf("the headline is %q, with %d steps running; want %q, with 3", snap.Now.Headline, snap.Now.Running, want)
	}

	esWatcher(t, work, repo, "",
		Step{Issue: 148, Doing: "building", Since: esAt(10, 0)},
		Step{Issue: 155, Since: esAt(10, 9)})
	esSoon(t, `the headline "Writing the code for #148", with one step running`, func() (bool, string) {
		snap, _ = esState(t, s)
		return snap.Now.Headline == "Writing the code for #148" && snap.Now.Running == 1,
			fmt.Sprintf("%q with %d steps running", snap.Now.Headline, snap.Now.Running)
	})
}

// The page draws a card for each step in the live view. Its live section
// holds the cards, each made from one template with its own title, now line,
// dial, stats, heartbeat strip, legend, checks and files. A page has one
// element for each id, so no part of a step's card is an id, and the script
// reads none of the ids the one step's panel had. It makes each card from the
// template, and the chip beside the headline says how many steps are running.
func TestThePageDrawsACardForEachStep(t *testing.T) {
	html, err := web.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	page, script := string(html), string(js)
	if !strings.Contains(page, `id="live-cards"`) {
		t.Error(`the page's live section has no list of cards, id="live-cards"`)
	}
	for _, id := range []string{"live-title", "live-now", "live-dial", "live-stats", "live-strip", "live-legend", "live-runs", "live-files"} {
		if strings.Contains(page, `id="`+id+`"`) {
			t.Errorf(`the page has one %s for every step: id="%s"`, id, id)
		}
		if strings.Contains(script, "#"+id) {
			t.Errorf("the script reads the one step's #%s", id)
		}
	}
	if !regexp.MustCompile(`live-card[^\w-]`).MatchString(script) {
		t.Error("the script never makes a card from the template, live-card")
	}
	if !strings.Contains(script, "steps running") {
		t.Error(`the chip beside the headline never says how many steps are running, as "3 steps running"`)
	}
	start := strings.Index(page, `<template id="live-card"`)
	if start < 0 {
		t.Fatal(`the page has no template for a step's card, <template id="live-card">`)
	}
	end := strings.Index(page[start:], "</template>")
	if end < 0 {
		t.Fatal(`the template for a step's card, <template id="live-card">, never ends`)
	}
	classes := map[string]bool{}
	for _, m := range regexp.MustCompile(`class="([^"]*)"`).FindAllStringSubmatch(page[start:start+end], -1) {
		for _, c := range strings.Fields(m[1]) {
			classes[c] = true
		}
	}
	for _, part := range []string{"live-title", "live-now", "dial", "live-stats", "strip", "legend", "live-runs", "live-files"} {
		if !classes[part] {
			t.Errorf("a step's card has no %s of its own: nothing in its template has the class %s", part, part)
		}
	}
}
