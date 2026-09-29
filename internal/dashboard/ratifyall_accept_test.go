package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
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

// togetherNow is when the dashboard reads the fixture's issues.
var togetherNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// togetherHashes are the hashes of the proposals o/r's issues wait for, by
// issue.
var togetherHashes = map[int]string{14: "sha256:3f2a9c1b7d4e5f60", 15: "sha256:8c6e1d2f4a7b9c01", 16: "sha256:5b9d3e7a1c2f4e82", 17: "sha256:e4c8a2f6b1d93a73", 18: "sha256:a7f3c9e5d2b16b24"}

// togetherShort is the short hash a person ratifies issue n's proposal by.
func togetherShort(n int) string {
	return strings.TrimPrefix(togetherHashes[n], "sha256:")[:12]
}

// togetherMarker is a post of the factory's that carries m, as the
// dashboard reads it among an issue's comments.
func togetherMarker(t *testing.T, m factory.Marker) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return "**Invariant** update\n\n<!-- invariant:" + base64.StdEncoding.EncodeToString(b) + " -->\n"
}

// togetherThreads is o/r's open issues as GitHub lists them, each with the
// factory's post that says what it waits for: a plumbing plan (#14), a plan
// of issues (#15), new statements (#16), an amendment that changes
// statements (#17), a rebuild at its lock's hash (#18), and a question
// (#19).
func togetherThreads(t *testing.T) ([]github.Issue, map[int][]github.Comment) {
	t.Helper()
	var issues []github.Issue
	comments := map[int][]github.Comment{}
	post := func(n int, title, label string, m factory.Marker) {
		at := togetherNow.Add(-time.Duration(n) * time.Hour).Format(time.RFC3339)
		issues = append(issues, github.Issue{Number: n, Title: title, State: "open", CreatedAt: at, UpdatedAt: at, Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: label}}})
		comments[n] = []github.Comment{{User: github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}, CreatedAt: at, Body: togetherMarker(t, m)}}
	}
	propose := func(n int, p *formalize.Proposal) factory.Marker {
		p.Hash = togetherHashes[n]
		return factory.Marker{Kind: factory.KindProposal, Proposal: p}
	}
	said := []project.Statement{{Name: "OneLeaseHolder", Kind: "invariant", Says: "At most one worker holds a lease."}}
	plan := &plumbing.Plan{Name: "a link to each card on /act", Summary: "Link each card in Needs you to its card on /act.", Files: []string{"internal/dashboard/web/app.js"}, Tests: []plumbing.Test{{Name: "TestTheCardLinks", File: "internal/dashboard/link_accept_test.go", Says: "The card links to its card on /act."}}}
	issuePlan := &formalize.IssuePlan{Name: "a decision graph on the dashboard", Summary: "Serve the graph, then prove its layout settles.", Issues: []formalize.PlannedIssue{{Title: "Serve the decision graph", Body: "The dashboard serves the graph.\n\nKind: plumbing"}, {Title: "Prove the layout settles", Body: "The layout settles.\n\nProject: invariant/layout"}}}
	post(14, "Link each card to /act", factory.LabelProposal, propose(14, &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name, Language: "go"}, Plan: plan}))
	post(15, "Draw the decision graph", factory.LabelProposal, propose(15, &formalize.Proposal{Draft: formalize.Draft{Name: issuePlan.Name}, IssuePlan: issuePlan}))
	post(16, "Check the leases", factory.LabelProposal, propose(16, &formalize.Proposal{Draft: formalize.Draft{Name: "leases", Statements: said}}))
	post(17, "Refuse calls once too many wait", factory.LabelProposal, propose(17, &formalize.Proposal{Draft: formalize.Draft{Name: "rate limiter", Statements: said}, Target: &formalize.Target{Dir: "examples/04-api-rate-limiter", Previous: "#3", Amends: "sha256:0badc0ffee000000"}}))
	post(18, "Rebuild the log buffer", factory.LabelProposal, propose(18, &formalize.Proposal{Draft: formalize.Draft{Name: "log buffer", Statements: said}, Target: &formalize.Target{Dir: "examples/03-log-buffer", Previous: "#1", Amends: togetherHashes[18]}}))
	post(19, "Count the retries", factory.LabelAsking, factory.Marker{Kind: factory.KindForks, Forks: []formalize.Fork{{ID: "F1", Question: "Does a retry reset the count?", Options: []formalize.Option{{ID: "A", Says: "Yes"}, {ID: "B", Says: "No"}}}}})
	return issues, comments
}

// togetherWaiting is what each of issues waits for, by number.
func togetherWaiting(issues []Issue) map[int]*Waiting {
	out := map[int]*Waiting{}
	for _, is := range issues {
		out[is.Number] = is.Waiting
	}
	return out
}

// togetherServer is a dashboard for o/r behind team's Access, whose issues
// are togetherThreads' as a refresh leaves them. posted lists every comment
// it has posted to GitHub, as repo#n and the comment's body.
func togetherServer(t *testing.T, team *accessTeam) (*Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: team.access(), Log: t.Logf}
	s.Post = func(_ context.Context, repo string, issue int, body string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, fmt.Sprintf("%s#%d %s", repo, issue, body))
		return fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", repo, issue, len(bodies)), nil
	}
	issues, comments := togetherThreads(t)
	for _, is := range issues {
		l := Lane(is, comments[is.Number], "", togetherNow)
		l.Repo = "o/r"
		s.issues = append(s.issues, l)
	}
	posted := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
	return s, posted
}

// togetherBatch is what the page at /act sends once the owner confirms
// Ratify all: each issue it listed, with the hash it showed.
func togetherBatch(t *testing.T, numbers ...int) string {
	t.Helper()
	var ratify []map[string]any
	for _, n := range numbers {
		ratify = append(ratify, map[string]any{"repo": "o/r", "issue": n, "hash": togetherShort(n)})
	}
	b, err := json.Marshal(map[string]any{"ratify": ratify})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// togetherSend posts body to path as the page at /act does, signed in with
// tok, with headers changed.
func togetherSend(h http.Handler, tok, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	target := "https://invariant.example.com" + path
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.Host = "invariant.example.com"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Invariant", "act")
	r.Header.Set("Origin", "https://invariant.example.com")
	if tok != "" {
		r.Header.Set("Cf-Access-Jwt-Assertion", tok)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// On the public page, the card of an issue waiting for ratification links
// to that issue's card on /act, which opens at it once Cloudflare Access
// signs the owner in. The public page still posts nothing, and a dashboard
// that doesn't serve /act doesn't link to it.
func TestAPublicCardLinksToItsCardOnAct(t *testing.T) {
	team := newAccessTeam(t)
	issues, comments := togetherThreads(t)
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: team.access(), Log: t.Logf}
	r := s.Repos[0]
	r.src.issues = issues
	r.src.comments = map[int]cachedComments{}
	for _, is := range issues {
		r.src.comments[is.Number] = cachedComments{updated: is.UpdatedAt, comments: comments[is.Number]}
	}
	// The snapshot the public page draws, and the issues /act checks
	// against, as a refresh leaves them.
	snap := s.assemble(togetherNow)
	s.issues = snap.Issues
	h := s.Handler()
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	follow := func(link, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", link, nil)
		if tok != "" {
			req.Header.Set("Cf-Access-Jwt-Assertion", tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, n := range []int{14, 16, 19} {
		w := togetherWaiting(snap.Issues)[n]
		if w == nil {
			t.Fatalf("#%d isn't in Needs you", n)
		}
		link, err := url.Parse(w.ActURL)
		if err != nil || link.Path != "/act" {
			t.Fatalf("#%d's card links to %q; want its own card on /act", n, w.ActURL)
		}
		open := fmt.Sprintf(`data-open="o/r#%d"`, n)
		page := follow(w.ActURL, owner)
		if body := page.Body.String(); page.Code != http.StatusOK || !strings.Contains(body, `data-act="owner@example.com"`) || !strings.Contains(body, open) {
			t.Errorf("following #%d's link, signed in: %d, and the page must be marked for acting and with %s", n, page.Code, open)
		}
		if refused := follow(w.ActURL, ""); refused.Code != http.StatusForbidden {
			t.Errorf("following #%d's link without signing in: %d, want 403", n, refused.Code)
		}
	}
	// The public page only reads: a post anywhere outside /act is refused.
	for _, p := range []string{"/", "/index.html", "/api/state.json"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", p, strings.NewReader("{}")))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d, want 405", p, rec.Code)
		}
	}
	// A dashboard without /act links no card there.
	s.Access = nil
	for _, is := range s.assemble(togetherNow).Issues {
		if is.Waiting != nil && is.Waiting.ActURL != "" {
			t.Errorf("with no /act, #%d's card links to %s", is.Number, is.Waiting.ActURL)
		}
	}
}

// On /act, with a plumbing plan and a plan of issues waiting, one
// confirmation posts /invariant ratify <hash> on each plan's issue, with its
// own hash, as the owner, and on no other issue. The server checks every
// line as it checks a single command, so a batch with a hash its issue isn't
// waiting for posts nothing, and only the owner's page sends one: the
// agent's door takes no batch.
func TestOneConfirmationRatifiesEveryPlan(t *testing.T) {
	team := newAccessTeam(t)
	s, posted := togetherServer(t, team)
	h := s.Handler()
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	// Each plan's card can say what the plan is.
	waiting := togetherWaiting(s.issues)
	w14, w15 := waiting[14], waiting[15]
	if w14 == nil || w15 == nil || w14.Plan == nil || w15.IssuePlan == nil {
		t.Fatalf("the plans' cards can't say what each plan is: %+v and %+v", w14, w15)
	}
	if got := strings.Join(w15.IssuePlan.Issues, "; "); got != "Serve the decision graph; Prove the layout settles" {
		t.Errorf("the plan of issues' card lists %q; want each issue's title, in order", got)
	}

	batch := togetherBatch(t, 14, 15)
	stale := strings.Replace(batch, togetherShort(15), "000000000000", 1)
	if w := togetherSend(h, "", "/act/api/ratify", batch, nil); w.Code != http.StatusForbidden {
		t.Errorf("a batch from someone not signed in: %d, want 403", w.Code)
	}
	if w := togetherSend(h, owner, "/act/api/ratify", batch, map[string]string{"Origin": "https://evil.example.com"}); w.Code != http.StatusForbidden {
		t.Errorf("a batch from another site: %d, want 403", w.Code)
	}
	if w := togetherSend(h, owner, "/act/api/ratify", stale, nil); w.Code != http.StatusBadRequest {
		t.Errorf("a batch with a hash #15 isn't waiting for, as after a revise: %d, want 400", w.Code)
	}
	door := httptest.NewRequest("POST", "/act/api/ratify", strings.NewReader(batch))
	door.Header.Set("Authorization", "Bearer door-token")
	rec := httptest.NewRecorder()
	s.AgentHandler("door-token").ServeHTTP(rec, door)
	if rec.Code == http.StatusOK {
		t.Error("the agent's door took a batch of ratifications")
	}
	if got := posted(); len(got) != 0 {
		t.Fatalf("a refused batch posted %q", got)
	}

	if w := togetherSend(h, owner, "/act/api/ratify", batch, nil); w.Code != http.StatusOK {
		t.Fatalf("one confirmation of both plans: %d %s", w.Code, w.Body.String())
	}
	want := []string{"o/r#14 /invariant ratify " + togetherShort(14), "o/r#15 /invariant ratify " + togetherShort(15)}
	got := posted()
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("one confirmation posted\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A proposal that changes statements, new or amended, is left out of that
// batch: only a plan, of either kind, or a rebuild that changes no statement
// is marked to ratify with others. A batch that names one is refused whole,
// with nothing posted, and it still ratifies on its own, from its own card.
func TestAProposalThatChangesStatementsRatifiesAlone(t *testing.T) {
	team := newAccessTeam(t)
	s, posted := togetherServer(t, team)
	h := s.Handler()
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	what := map[int]string{14: "plumbing plan", 15: "plan of issues", 16: "proposal of new statements", 17: "amendment that changes statements", 18: "rebuild at its lock's hash", 19: "question"}
	waiting := togetherWaiting(s.issues)
	for _, n := range []int{14, 15, 16, 17, 18, 19} {
		w := waiting[n]
		if w == nil {
			t.Fatalf("#%d, a %s, isn't waiting on anyone", n, what[n])
		}
		if want := n == 14 || n == 15 || n == 18; w.Together != want {
			t.Errorf("#%d, a %s: marked to ratify together %v, want %v", n, what[n], w.Together, want)
		}
	}
	for _, n := range []int{16, 17} {
		if w := togetherSend(h, owner, "/act/api/ratify", togetherBatch(t, 14, 15, n), nil); w.Code != http.StatusBadRequest {
			t.Errorf("a batch with #%d, a %s, in it: %d, want 400", n, what[n], w.Code)
		}
		if w := togetherSend(h, owner, "/act/api/ratify", togetherBatch(t, n), nil); w.Code != http.StatusBadRequest {
			t.Errorf("a batch of #%d, a %s, alone: %d, want 400", n, what[n], w.Code)
		}
	}
	if got := posted(); len(got) != 0 {
		t.Fatalf("a batch with statements in it posted %q", got)
	}
	// It still ratifies on its own, from its own card.
	line := "/invariant ratify " + togetherShort(16)
	one, err := json.Marshal(map[string]any{"repo": "o/r", "issue": 16, "body": line})
	if err != nil {
		t.Fatal(err)
	}
	if w := togetherSend(h, owner, "/act/api/comment", string(one), nil); w.Code != http.StatusOK {
		t.Fatalf("ratifying #16 on its own: %d %s", w.Code, w.Body.String())
	}
	want := "o/r#16 " + line
	if got := posted(); len(got) != 1 || got[0] != want {
		t.Errorf("ratifying #16 on its own posted %q, want %q", got, want)
	}
}
