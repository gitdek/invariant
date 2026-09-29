package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
)

// These tests show a command posted from /act at once (#175). As soon as
// GitHub takes a command, /api/state.json shows its issue acted on, to every
// page that reads it, and keeps showing it so until a refresh reads the
// comment on GitHub, when the factory's reading takes over.

// atOnceGH is a gh that answers the dashboard's reads of o/r from a
// directory: issues.json is its issues, and comments-<n>.json each issue's
// comments. Every other read is Not Found, as for a repository with no
// commits, runs or lease to show. It logs each path it's asked for to calls.
const atOnceGH = `#!/bin/sh
d='@DIR@'
for last; do :; done
echo "$last" >> "$d/calls"
case "$last" in
*/issues\?*) cat "$d/issues.json" ;;
*/issues/*/comments\?*)
	n=${last#*/issues/}
	n=${n%%/*}
	if [ -f "$d/comments-$n.json" ]; then cat "$d/comments-$n.json"; else echo '[]'; fi ;;
*) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
esac
`

// atOnceHashes are the hashes of the proposals o/r's issues wait to have
// ratified, by issue.
var atOnceHashes = map[int]string{14: "sha256:3f2a9c1b7d4e5f60a1b2", 15: "sha256:8c6e1d2f4a7b9c01d3e4", 21: "sha256:5b9d3e7a1c2f4e82f5a6"}

// atOnceShort is the short hash a person ratifies issue n's proposal by.
func atOnceShort(n int) string {
	return strings.TrimPrefix(atOnceHashes[n], "sha256:")[:12]
}

// atOnceMarker is a post of the factory's that carries m, as the dashboard
// reads it among an issue's comments.
func atOnceMarker(t *testing.T, m factory.Marker) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return "**Invariant** update\n\n<!-- invariant:" + base64.StdEncoding.EncodeToString(b) + " -->\n"
}

// atOncePlan is the factory's proposal of #14's plumbing plan.
func atOncePlan() factory.Marker {
	plan := &plumbing.Plan{Name: "a link to each card on /act", Summary: "Link each card in Needs you to its card on /act.", Files: []string{"internal/dashboard/web/app.js"},
		Tests: []plumbing.Test{{Name: "TestTheCardLinks", File: "internal/dashboard/link_accept_test.go", Says: "The card links to its card on /act."}}}
	return factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name, Language: "go"}, Hash: atOnceHashes[14], Plan: plan}}
}

// atOnceURL is where GitHub shows comment id on issue n of o/r.
func atOnceURL(n int, id int64) string {
	return fmt.Sprintf("https://github.com/o/r/issues/%d#issuecomment-%d", n, id)
}

// atOnceComment is comment id on issue n of o/r, as GitHub lists it: by
// login, at at.
func atOnceComment(n int, id int64, login string, at time.Time, body string) github.Comment {
	user := github.User{Login: login, Type: "User"}
	if strings.HasSuffix(login, "[bot]") {
		user.Type = "Bot"
	}
	return github.Comment{ID: id, Body: body, User: user, URL: atOnceURL(n, id), IssueURL: fmt.Sprintf("https://api.github.com/repos/o/r/issues/%d", n), CreatedAt: at.Format(time.RFC3339)}
}

// atOnceCommand is a command on issue n of o/r, as /act sends it.
func atOnceCommand(t *testing.T, n int, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"repo": "o/r", "issue": n, "body": command})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// atOnceWrite writes v to a file as JSON in one step, so a reader never sees
// half of it.
func atOnceWrite(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
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

// atOnceJSON is v as JSON, for a message.
func atOnceJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// atOnceGist is what an issue, read whole from /api/state.json, says of
// where it is: its stage, what it waits for, and what's shown posted on it.
func atOnceGist(is map[string]any) string {
	return atOnceJSON(map[string]any{"stage": is["stage"], "waiting": is["waiting"], "acted": is["acted"]})
}

// atOnceFake is o/r on a fake GitHub, and a dashboard that reads it, with
// the owner signed in at /act through Cloudflare Access with owner's token.
// t0 is when the fixture was made, and its times count from it. GitHub takes
// every comment the dashboard posts, giving it the next ID, and lists it
// only once the test adds it, as a refresh reads it: posts is each comment
// it took, as o/r#n and its body, and ids the ID of the last one it took on
// each issue. It refuses the next post on each issue marked in refuse.
type atOnceFake struct {
	t     *testing.T
	dir   string
	t0    time.Time
	s     *Server
	owner string

	mu       sync.Mutex
	issues   []*github.Issue
	comments map[int][]github.Comment
	posts    []string
	ids      map[int]int64
	refuse   map[int]bool
	next     int64
	logged   []string
}

// atOnceStart puts o/r's open issues on a fake GitHub, each with the
// factory's post saying what it waits for, and starts a dashboard behind
// Cloudflare Access that reads it every every, once it has read it the first
// time. #14 waits for the ratify of a plumbing plan, #15 of a plan of issues
// and #21 of new statements, #19 for answers to two questions, and #20 for a
// retry of a build that stopped.
func atOnceStart(t *testing.T, every time.Duration) *atOnceFake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir, err := os.MkdirTemp("", "invariant-atonce-gh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(strings.ReplaceAll(atOnceGH, "@DIR@", dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &atOnceFake{t: t, dir: dir, t0: time.Now().UTC().Truncate(time.Second), comments: map[int][]github.Comment{}, ids: map[int]int64{}, refuse: map[int]bool{}, next: 9000}
	issuePlan := &formalize.IssuePlan{Name: "a decision graph on the dashboard", Summary: "Serve the graph, then prove its layout settles.",
		Issues: []formalize.PlannedIssue{{Title: "Serve the decision graph", Body: "The dashboard serves the graph.\n\nKind: plumbing"}, {Title: "Prove the layout settles", Body: "The layout settles.\n\nProject: invariant/layout"}}}
	said := []project.Statement{{Name: "OneLeaseHolder", Kind: "invariant", Says: "At most one worker holds a lease."}}
	yesNo := []formalize.Option{{ID: "A", Says: "Yes"}, {ID: "B", Says: "No"}}
	f.open(14, "Link each card to /act", factory.LabelProposal, atOncePlan())
	f.open(15, "Draw the decision graph", factory.LabelProposal, factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: issuePlan.Name}, Hash: atOnceHashes[15], IssuePlan: issuePlan}})
	f.open(19, "Count the retries", factory.LabelAsking, factory.Marker{Kind: factory.KindForks, Forks: []formalize.Fork{{ID: "F1", Question: "Does a retry reset the count?", Options: yesNo}, {ID: "F2", Question: "Does a timeout count as a retry?", Options: yesNo}}})
	f.open(20, "Bound the queue", factory.LabelHumanReview, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped})
	f.open(21, "Check the leases", factory.LabelProposal, factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: "leases", Statements: said}, Hash: atOnceHashes[21]}})

	team := newAccessTeam(t)
	f.owner = team.token(t, team.key, "RS256", team.claims(nil))
	gh := github.Client{Repo: "o/r", GH: filepath.Join(dir, "gh")}
	f.s = &Server{Repos: []*Repo{{Name: "o/r", GitHub: gh}}, Branch: "main", Every: every, Access: team.access(), Post: f.post, Log: f.logf}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
	})
	f.s.Start(ctx)
	f.soon("the dashboard's first read of GitHub", func() (bool, string) {
		rec := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
		return rec.Code == http.StatusOK, fmt.Sprintf("GET /api/state.json answered %d", rec.Code)
	})
	return f
}

// open puts open issue n on the fake GitHub, opened by @gitdek two hours
// before t0, with the factory's post carrying m n minutes before t0, and
// labeled for the factory and with label.
func (f *atOnceFake) open(n int, title, label string, m factory.Marker) {
	opened := f.t0.Add(-2 * time.Hour).Format(time.RFC3339)
	at := f.t0.Add(-time.Duration(n) * time.Minute)
	f.mu.Lock()
	f.issues = append(f.issues, &github.Issue{Number: n, Title: title, User: github.User{Login: "gitdek"}, State: "open", URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n), CreatedAt: opened, UpdatedAt: opened})
	f.mu.Unlock()
	f.update(n, label, atOnceComment(n, int64(n), "invariant-code-factory[bot]", at, atOnceMarker(f.t, m)))
}

// update adds comments to issue n on the fake GitHub, labels it for the
// factory and with label, and marks it updated when its last comment was
// made, as GitHub does. The fake's gh lists it all from then on.
func (f *atOnceFake) update(n int, label string, comments ...github.Comment) {
	f.t.Helper()
	f.mu.Lock()
	for _, is := range f.issues {
		if is.Number == n {
			is.Labels = []github.Label{{Name: factory.LabelTrigger}, {Name: label}}
			is.UpdatedAt = comments[len(comments)-1].CreatedAt
		}
	}
	f.comments[n] = append(f.comments[n], comments...)
	f.mu.Unlock()
	f.write()
}

// write puts the fake's issues and comments where its gh reads them, each
// file whole, and every issue's comments before the issues, so a read that
// finds an issue updated finds its new comments too.
func (f *atOnceFake) write() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, is := range f.issues {
		atOnceWrite(f.t, filepath.Join(f.dir, fmt.Sprintf("comments-%d.json", is.Number)), f.comments[is.Number])
	}
	atOnceWrite(f.t, filepath.Join(f.dir, "issues.json"), f.issues)
}

// post is GitHub taking a comment the dashboard posts on issue n, or
// refusing it when the issue is marked to refuse its next one.
func (f *atOnceFake) post(_ context.Context, repo string, n int, body string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse[n] {
		delete(f.refuse, n)
		return "", errors.New("gh: Service Unavailable (HTTP 503)")
	}
	f.next++
	f.ids[n] = f.next
	f.posts = append(f.posts, fmt.Sprintf("%s#%d %s", repo, n, body))
	return atOnceURL(n, f.next), nil
}

// refuseNext has GitHub refuse the next comment the dashboard posts on
// issue n.
func (f *atOnceFake) refuseNext(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse[n] = true
}

// id is the ID of the last comment GitHub took on issue n.
func (f *atOnceFake) id(n int) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ids[n]
}

// taken is every comment GitHub has taken from the dashboard, as o/r#n and
// its body.
func (f *atOnceFake) taken() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

// logf keeps what the dashboard logs, to show when a test fails.
func (f *atOnceFake) logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logged = append(f.logged, fmt.Sprintf(format, args...))
}

// tail is the last few things the dashboard logged.
func (f *atOnceFake) tail() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logged[max(len(f.logged)-4, 0):], "\n")
}

// soon waits up to half a minute for ok, and fails the test with what it
// last saw, and what the dashboard last logged, if ok never holds.
func (f *atOnceFake) soon(what string, ok func() (bool, string)) {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		done, saw := ok()
		if done {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("waited half a minute for %s; last saw %s. The dashboard last logged:\n%s", what, saw, f.tail())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// listings is how many times the dashboard has listed o/r's issues: once, as
// each refresh starts.
func (f *atOnceFake) listings() int {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Count(string(b), "/issues?")
}

// refreshed waits for n refreshes that start after the from-th listing to
// finish, which they have once another starts after them.
func (f *atOnceFake) refreshed(from, n int) {
	f.t.Helper()
	f.soon(fmt.Sprintf("%d whole refreshes", n), func() (bool, string) {
		started := f.listings() - from
		return started > n, fmt.Sprintf("%d started", started)
	})
}

// act sends body to path at /act as the owner's page does, signed in
// through Cloudflare Access.
func (f *atOnceFake) act(path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://invariant.example.com"+path, strings.NewReader(body))
	r.Host = "invariant.example.com"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Invariant", "act")
	r.Header.Set("Origin", "https://invariant.example.com")
	r.Header.Set("Cf-Access-Jwt-Assertion", f.owner)
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}

// agent posts a command on issue n through the agent's door, with its
// token.
func (f *atOnceFake) agent(n int, command string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/act/api/comment", strings.NewReader(atOnceCommand(f.t, n, command)))
	r.Header.Set("Authorization", "Bearer door-token")
	w := httptest.NewRecorder()
	f.s.AgentHandler("door-token").ServeHTTP(w, r)
	return w
}

// atOnceIssue is what a page reads of an issue in /api/state.json: its
// stage, what it waits for, if anything, and the command posted on it that
// shows at once, if any.
type atOnceIssue struct {
	Number  int    `json:"number"`
	Stage   string `json:"stage"`
	Waiting *struct {
		Kind  string    `json:"kind"`
		Hash  string    `json:"hash"`
		Since time.Time `json:"since"`
	} `json:"waiting"`
	Acted *struct {
		Command string    `json:"command"`
		Text    string    `json:"text"`
		URL     string    `json:"url"`
		At      time.Time `json:"at"`
	} `json:"acted"`
}

// state is o/r's issues as every page reads them from /api/state.json now,
// the public page included, by number: as a page reads each, and whole.
func (f *atOnceFake) state() (map[int]atOnceIssue, map[int]map[string]any) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
	if rec.Code != http.StatusOK {
		f.t.Fatalf("GET /api/state.json: %d %s", rec.Code, rec.Body.String())
	}
	var snap struct {
		Issues []json.RawMessage `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		f.t.Fatal(err)
	}
	issues, whole := map[int]atOnceIssue{}, map[int]map[string]any{}
	for _, raw := range snap.Issues {
		var is atOnceIssue
		var all map[string]any
		if err := json.Unmarshal(raw, &is); err != nil {
			f.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &all); err != nil {
			f.t.Fatal(err)
		}
		issues[is.Number], whole[is.Number] = is, all
	}
	return issues, whole
}

// atOnceActed checks that issue n shows as acted on, when: waiting on no
// one, queued, and with command shown posted, saying text, linking to url,
// at a time between from and to.
func atOnceActed(t *testing.T, when string, issues map[int]atOnceIssue, n int, command, text, url string, from, to time.Time) {
	t.Helper()
	is, ok := issues[n]
	if !ok {
		t.Errorf("%s, /api/state.json doesn't show #%d", when, n)
		return
	}
	if is.Waiting != nil {
		t.Errorf("%s, #%d still waits on a person, for its %s, after GitHub took %q", when, n, is.Waiting.Kind, command)
	}
	if is.Stage != "queued" {
		t.Errorf("%s, #%d is %s; want queued, as an issue whose person has acted", when, n, is.Stage)
	}
	a := is.Acted
	if a == nil {
		t.Errorf("%s, #%d doesn't show what was posted on it, %q", when, n, command)
		return
	}
	if a.Command != command || a.Text != text || a.URL != url {
		t.Errorf("%s, #%d shows %q posted, saying %q, linking to %s; want %q, saying %q, linking to %s", when, n, a.Command, a.Text, a.URL, command, text, url)
	}
	if a.At.Before(from.Add(-time.Second)) || a.At.After(to.Add(time.Second)) {
		t.Errorf("%s, #%d shows it posted at %s; want when GitHub took it, between %s and %s", when, n, a.At.Format(time.RFC3339Nano), from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano))
	}
}

// atOnceAsItWas checks that issue n, read whole, waits for exactly what it
// waited for before, at the stage it was at, with nothing shown posted.
func atOnceAsItWas(t *testing.T, when string, whole, before map[int]map[string]any, n int) {
	t.Helper()
	is, was := whole[n], before[n]
	if is == nil || is["waiting"] == nil || !reflect.DeepEqual(is["waiting"], was["waiting"]) || is["stage"] != was["stage"] || is["acted"] != nil {
		t.Errorf("%s, #%d is %s; want it as it was, %s, with nothing shown posted", when, n, atOnceGist(is), atOnceGist(was))
	}
}

// A ratify posted from /act shows at once. The dashboard has read GitHub
// once, and doesn't again during the test, and the next /api/state.json, as
// the page that posted it, a reload, another tab and the public page all
// read it, shows the plumbing plan's issue waiting on no one, queued, and
// acted on: the command, "Ratified. The factory builds it next.", the
// comment GitHub made, and when. The other issues wait as they did. Answers
// to every open question, a retry, and a revise through the agent's door,
// shown without the agent's note, each show at once too, with their own
// words, while an answer to one of two questions leaves its issue waiting.
// And /act refuses the same ratify again, as a tab that still shows the card
// would send it, since its issue no longer waits for it.
func TestACommandPostedFromActShowsAtOnce(t *testing.T) {
	f := atOnceStart(t, time.Hour)
	issues, before := f.state()
	for _, n := range []int{14, 15, 19, 20, 21} {
		if is := issues[n]; is.Waiting == nil || is.Acted != nil {
			t.Fatalf("before anything is posted, #%d is %s; want it waiting, with nothing shown posted", n, atOnceGist(before[n]))
		}
	}

	ratify := "/invariant ratify " + atOnceShort(14)
	from := time.Now()
	if w := f.act("/act/api/comment", atOnceCommand(t, 14, ratify)); w.Code != http.StatusOK {
		t.Fatalf("ratifying #14 from /act: %d %s", w.Code, w.Body.String())
	}
	to := time.Now()
	url := atOnceURL(14, f.id(14))
	for _, when := range []string{"the next time a page reads it", "the time after that"} {
		issues, whole := f.state()
		atOnceActed(t, when, issues, 14, ratify, "Ratified. The factory builds it next.", url, from, to)
		for _, n := range []int{15, 19, 20, 21} {
			atOnceAsItWas(t, when, whole, before, n)
		}
	}

	// An answer to one of #19's two questions leaves it waiting for both, as
	// the factory reads it.
	if w := f.agent(19, "/invariant choose F1 A"); w.Code != http.StatusOK {
		t.Fatalf("answering one of #19's questions through the agent's door: %d %s", w.Code, w.Body.String())
	}
	_, whole := f.state()
	atOnceAsItWas(t, "after an answer to one of its two questions", whole, before, 19)

	// Answers to both of #19's questions, a retry of #20's build, and a
	// revise of #21's proposal through the agent's door each show at once,
	// with their own words.
	shows := func(n int, command, text string, door bool) {
		t.Helper()
		from := time.Now()
		var w *httptest.ResponseRecorder
		if door {
			w = f.agent(n, command)
		} else {
			w = f.act("/act/api/comment", atOnceCommand(t, n, command))
		}
		to := time.Now()
		if w.Code != http.StatusOK {
			t.Fatalf("posting %q on #%d: %d %s", command, n, w.Code, w.Body.String())
		}
		issues, _ := f.state()
		atOnceActed(t, "at once", issues, n, command, text, atOnceURL(n, f.id(n)), from, to)
	}
	shows(19, "/invariant choose F1 A\n/invariant choose F2 B", "Answered. The factory drafts again next.", false)
	shows(20, "/invariant retry", "Asked for a retry. The factory tries again next.", false)
	shows(21, "/invariant revise", "Asked for a new draft. The factory drafts again next.", true)
	if got := f.taken(); !strings.HasPrefix(got[len(got)-1], "o/r#21 /invariant revise\n\n") {
		t.Errorf("the agent's revise went to GitHub as %q; want the command, then the agent's note", got[len(got)-1])
	}

	issues, whole = f.state()
	atOnceAsItWas(t, "after every other post", whole, before, 15)
	atOnceActed(t, "after every other post", issues, 14, ratify, "Ratified. The factory builds it next.", url, from, to)

	// A tab that still shows #14's card sends its ratify again. /act refuses
	// it, since #14 no longer waits for it, and nothing more goes to GitHub.
	posted := len(f.taken())
	if w := f.act("/act/api/comment", atOnceCommand(t, 14, ratify)); w.Code != http.StatusBadRequest {
		t.Errorf("ratifying #14 again, from a tab that still shows its card: %d %s; want 400, since #14 no longer waits for it", w.Code, w.Body.String())
	}
	if got := f.taken(); len(got) != posted {
		t.Errorf("ratifying #14 again posted %q", got[posted:])
	}
}

// A ratify GitHub refuses is answered 502, saying GitHub didn't take it, as
// now, and /api/state.json still shows its issue exactly as it was. When
// GitHub stops a Ratify all partway through, taking #14's ratify and
// refusing #15's, #14 shows as acted on at once and #15 waits as it did.
// Posted again and taken, #15's ratify of a plan of issues shows, saying
// "Ratified. The factory opens its first issue next."
func TestAPostGitHubRefusesLeavesTheIssueWaiting(t *testing.T) {
	f := atOnceStart(t, time.Hour)
	_, before := f.state()
	f.refuseNext(14)
	w := f.act("/act/api/comment", atOnceCommand(t, 14, "/invariant ratify "+atOnceShort(14)))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "GitHub didn't take it") {
		t.Errorf("a ratify GitHub refused: %d %q; want 502, saying GitHub didn't take it", w.Code, w.Body.String())
	}
	_, whole := f.state()
	if !reflect.DeepEqual(whole[14], before[14]) {
		t.Errorf("after GitHub refused its ratify, #14 is\n%s\nwant it exactly as it was:\n%s", atOnceJSON(whole[14]), atOnceJSON(before[14]))
	}

	// A Ratify all of #14 and #15, which GitHub stops at #15.
	f.refuseNext(15)
	batch, err := json.Marshal(map[string]any{"ratify": []map[string]any{{"repo": "o/r", "issue": 14, "hash": atOnceShort(14)}, {"repo": "o/r", "issue": 15, "hash": atOnceShort(15)}}})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Now()
	w = f.act("/act/api/ratify", string(batch))
	to := time.Now()
	if w.Code != http.StatusBadGateway {
		t.Errorf("a Ratify all that GitHub stopped at #15: %d %s; want 502", w.Code, w.Body.String())
	}
	issues, whole := f.state()
	atOnceActed(t, "after a Ratify all that GitHub stopped at #15", issues, 14, "/invariant ratify "+atOnceShort(14), "Ratified. The factory builds it next.", atOnceURL(14, f.id(14)), from, to)
	atOnceAsItWas(t, "after a Ratify all that GitHub stopped at it", whole, before, 15)

	// #15's ratify, posted again and taken.
	ratify := "/invariant ratify " + atOnceShort(15)
	from = time.Now()
	if w := f.act("/act/api/comment", atOnceCommand(t, 15, ratify)); w.Code != http.StatusOK {
		t.Fatalf("ratifying #15 again: %d %s", w.Code, w.Body.String())
	}
	to = time.Now()
	issues, _ = f.state()
	atOnceActed(t, "once GitHub took it", issues, 15, ratify, "Ratified. The factory opens its first issue next.", atOnceURL(15, f.id(15)), from, to)
}

// A ratify posted from /act stays shown as acted on through every refresh
// that doesn't find its comment on GitHub. The refresh that reads it, with
// the factory's reply that it's building, shows #14 as the factory's reading
// says: building, waiting on no one, and acted on no more. Forgotten, the
// command never shows again: when the factory asks for the same ratify
// again, as it would if a revise drafted the same plan, #14 waits for it.
func TestTheRefreshThatReadsTheCommentTakesOver(t *testing.T) {
	f := atOnceStart(t, 50*time.Millisecond)
	ratify := "/invariant ratify " + atOnceShort(14)
	from := time.Now()
	if w := f.act("/act/api/comment", atOnceCommand(t, 14, ratify)); w.Code != http.StatusOK {
		t.Fatalf("ratifying #14 from /act: %d %s", w.Code, w.Body.String())
	}
	to := time.Now()
	id := f.id(14)
	f.refreshed(f.listings(), 2)
	issues, _ := f.state()
	atOnceActed(t, "after two refreshes that don't find its comment", issues, 14, ratify, "Ratified. The factory builds it next.", atOnceURL(14, id), from, to)

	// GitHub lists the comment now, and the factory's reply that it has
	// committed the ratification and is building.
	said := f.t0.Add(10 * time.Second)
	replied := f.t0.Add(20 * time.Second)
	f.update(14, factory.LabelBuilding, atOnceComment(14, id, "gitdek", said, ratify), atOnceComment(14, 9501, "invariant-code-factory[bot]", replied, atOnceMarker(t, factory.Marker{Kind: factory.KindRatified, Hash: atOnceHashes[14]})))
	var got atOnceIssue
	f.soon("#14 to show as building, as the factory's reading says once a refresh reads the comment", func() (bool, string) {
		issues, _ := f.state()
		got = issues[14]
		return got.Stage == "building", "#14 " + got.Stage
	})
	if got.Waiting != nil || got.Acted != nil {
		t.Errorf("the refresh that read #14's comment shows it waiting for %+v, with %+v shown posted; want it waiting on no one, and the command forgotten", got.Waiting, got.Acted)
	}
	f.refreshed(f.listings(), 2)
	issues, _ = f.state()
	if is := issues[14]; is.Stage != "building" || is.Acted != nil {
		t.Errorf("two refreshes later, #14 is %s, with %+v shown posted; want it building, with nothing shown posted", is.Stage, is.Acted)
	}

	// The factory asks for the same ratify again, as it would if a revise
	// drafted the same plan.
	revised := f.t0.Add(30 * time.Second)
	again := f.t0.Add(40 * time.Second)
	f.update(14, factory.LabelProposal, atOnceComment(14, 9502, "gitdek", revised, "/invariant revise"), atOnceComment(14, 9503, "invariant-code-factory[bot]", again, atOnceMarker(t, atOncePlan())))
	f.soon("#14 to wait for its proposal again, with nothing shown posted, since a command forgotten once a refresh has read its comment never shows again", func() (bool, string) {
		issues, _ := f.state()
		got = issues[14]
		return got.Waiting != nil && got.Waiting.Since.Equal(again) && got.Acted == nil, fmt.Sprintf("#14 %s, waiting for %+v, with %+v shown posted", got.Stage, got.Waiting, got.Acted)
	})
	if got.Waiting.Kind != "proposal" || got.Waiting.Hash != atOnceShort(14) {
		t.Errorf("#14 waits for its %s %s; want the ratify of its proposal, %s", got.Waiting.Kind, got.Waiting.Hash, atOnceShort(14))
	}
}

// On /act, Needs you draws each issue acted on from the snapshot, so a
// reload or another tab shows what was posted too: the page's script reads
// each issue's acted, and the command it holds.
func TestActDrawsWhatWasPosted(t *testing.T) {
	b, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{".acted", ".command"} {
		if !strings.Contains(string(b), field) {
			t.Errorf("the page's script never reads %s, so /act can't show what was posted from the snapshot", field)
		}
	}
}
