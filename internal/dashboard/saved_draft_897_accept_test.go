package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/synth"
)

// Public contract added by #897. The locator contains no presentation version
// and can be constructed by the watcher without calling the dashboard:
// /draft/<owner>/<repository>/<issue>/<full commit>/<full digest without sha256:>.
// It resolves only authenticated saved references, never caller-selected blobs.
// waiting.savedDraft contains reference, locator, reader, download and problem.
// A healthy reader names /reader/<64 lowercase hex>.html; download names the
// same key with .json and returns the EXACT original result.json bytes.
// waiting.proposal retains the complete proposal for questions as well as
// selected models; waiting.answers is the current marker's observations, not
// an edit to the original preview choices. Failed resolution keeps savedDraft
// with a visible problem and disables ratify/choose in the UI AND command gate.
type saved897Presentation struct {
	Reference factory.DraftReference `json:"reference"`
	Locator   string                 `json:"locator"`
	Reader    *ReaderPresentation    `json:"reader"`
	Download  string                 `json:"download"`
	Problem   string                 `json:"problem"`
}

type saved897Waiting struct {
	Waiting
	SavedDraft *saved897Presentation `json:"savedDraft"`
	Answers    []formalize.Answer    `json:"answers"`
}

const saved897Author = "factory[bot]"
const saved897Literal = `<script>window.saved897=true</script> & λ`
const saved897Source = "saved897 ORIGINAL source"

func saved897Proposal(t *testing.T, large bool, limit int, previous *formalize.Proposal) *formalize.Result {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	var draft map[string]any
	if err := json.Unmarshal([]byte(reader603Draft), &draft); err != nil {
		t.Fatal(err)
	}
	draft["bounds"] = map[string]string{"Limit": fmt.Sprint(limit)}
	draft["name"] = "Saved lifecycle " + saved897Literal
	preview639Write(t, ws, draft)
	if large {
		reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+saved897Source+" "+strings.Repeat("x", (4<<20)+1024)+"\n====", 1))
	}
	ctx := (formalize.Request{Previous: previous}).CheckContext()
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, ctx)
	if err != nil || p == nil || report == nil || !report.Passed {
		t.Fatalf("checked proposal fixture: %v", err)
	}
	return &formalize.Result{Proposal: p, Report: report, CheckContext: &ctx,
		Usage: synth.Usage{Backend: "fixture " + saved897Literal, CostUSD: 0.125}}
}

func saved897Questions(t *testing.T, previous *formalize.Proposal) *formalize.Result {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	choices := map[string]string{"F0": "B"}
	draft := preview639Draft(t, 2, choices)
	draft["forks"].([]any)[0].(map[string]any)["question"] = "Exact question  <word> & two  spaces. " + saved897Literal
	preview639Option(draft, 0, 0)["says"] = "Exact option A  <limit> & result. " + saved897Literal
	preview639Input(draft, 0, 0)["model"].(map[string]any)["includes"] = map[string]string{
		"Helper603.tla": strings.Replace(reader603Include, "====", "\\* "+saved897Source+" "+strings.Repeat("x", (4<<20)+1024)+"\n====", 1),
	}
	preview639Write(t, ws, draft)
	ctx := (formalize.Request{Previous: previous, Answers: []formalize.Answer{{Fork: "F0", Option: "B"}}}).CheckContext()
	p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, ctx)
	if err != nil || p == nil {
		t.Fatalf("checked question fixture: %v", err)
	}
	return &formalize.Result{Proposal: p, CheckContext: &ctx, Usage: synth.Usage{Backend: "question fixture"}}
}

type saved897Fixture struct {
	s       *Server
	r       *Repo
	ref     factory.DraftReference
	result  *formalize.Result
	payload []byte
	marker  factory.Marker
	issues  []github.Issue
}

func saved897Endpoint(t *testing.T, r *Repo, suffix string, body []byte) string {
	t.Helper()
	file := filepath.Join(filepath.Dir(r.GitHub.GH), strings.TrimPrefix(reader631Hash([]byte("repos/"+r.Name+"/"+suffix)), "sha256:"))
	reader631Write(t, file, body)
	return file
}

func saved897Locator(ref factory.DraftReference) string {
	return fmt.Sprintf("/draft/%s/%d/%s/%s", ref.Repository, ref.Issue, ref.Commit, strings.TrimPrefix(ref.SHA256, "sha256:"))
}

func saved897FixtureFor(t *testing.T, result *formalize.Result) *saved897Fixture {
	t.Helper()
	r := reader631Repo(t, "owner/saved", strings.Repeat("a", 40), 631, nil, nil)
	r.FactoryLogin = saved897Author
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir(), Branch: "main"}
	f := &saved897Fixture{s: s, r: r}
	f.install(t, result, 897, "draft-issue", strings.Repeat("b", 40))
	return f
}

func (f *saved897Fixture) install(t *testing.T, result *formalize.Result, issue int, step, commit string) {
	t.Helper()
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Neither normalization nor remarshal may change a byte of the download.
	f.payload = append(append([]byte(" \n"), b...), '\n', '\t')
	f.result = result
	f.ref = factory.DraftReference{Version: 1, Repository: f.r.Name, Issue: issue, Step: step, Commit: commit,
		SHA256: reader631Hash(f.payload), ProposalHash: result.Proposal.Hash}
	f.marker = factory.Marker{Kind: factory.KindProposal, ProposalRef: reader631JSON(t, f.ref)}
	label := factory.LabelProposal
	if len(result.Proposal.Forks) > 0 {
		f.marker.Kind, label = factory.KindForks, factory.LabelAsking
		for _, q := range result.Proposal.Forks {
			f.marker.Forks = append(f.marker.Forks, formalize.Fork{ID: q.ID})
		}
	}
	const at = "2026-11-12T12:00:00Z"
	f.issues = []github.Issue{{Number: issue, Title: "Read saved evidence", State: "open", CreatedAt: at, UpdatedAt: at,
		Labels: []github.Label{{Name: label}}, User: github.User{Login: "owner"}, AuthorAssociation: "OWNER"}}
	f.writeIssues(t)
	f.comment(t, f.marker, saved897Author)
	f.record(t, "complete checked draft")
	// Use the real github.Client and #631's strict direct-GET subprocess
	// fixture. Only these exact run and contents endpoints can return bytes;
	// no main/branch/URL/path fallback can answer a result read.
	saved897Endpoint(t, f.r, "commits?sha=main&per_page=100", []byte("[]"))
	saved897Endpoint(t, f.r, "actions/runs?branch=main&per_page=30", []byte(`{"workflow_runs":[]}`))
	saved897Endpoint(t, f.r, "git/trees/main?recursive=1", []byte(`{"tree":[]}`))
	// Assert that this is genuine valid shared-reference evidence before the
	// dashboard receives it. The dashboard must still do its own reads/checks.
	_, got, err := f.marker.ResolveDraft(t.Context(), f.r.Name, issue,
		func(context.Context, int, string) (factory.RunState, string, error) {
			return factory.RunDone, commit, nil
		},
		func(_ context.Context, sha, file string) ([]byte, error) {
			if sha != commit || file != "result.json" {
				return nil, fmt.Errorf("unexpected fixture read")
			}
			return f.payload, nil
		})
	if err != nil || got == nil || !sameReaderJSON(got, result) {
		t.Fatalf("invalid saved reference fixture: %v", err)
	}
}

func (f *saved897Fixture) writeIssues(t *testing.T) {
	t.Helper()
	saved897Endpoint(t, f.r, "issues?state=all&per_page=100", reader631JSON(t, f.issues))
}

func (f *saved897Fixture) comment(t *testing.T, m factory.Marker, author string) {
	t.Helper()
	c := github.Comment{ID: 897, User: github.User{Login: author}, CreatedAt: "2026-11-12T12:00:01Z",
		URL: "https://github.com/" + f.r.Name + "/issues/897#issuecomment-897", Body: marker(t, m)}
	if len(c.Body) > 5000 {
		t.Fatal("reference fixture is not compact")
	}
	saved897Endpoint(t, f.r, fmt.Sprintf("issues/%d/comments?per_page=100", f.ref.Issue), reader631JSON(t, []github.Comment{c}))
}

func (f *saved897Fixture) record(t *testing.T, message string) {
	t.Helper()
	saved897Endpoint(t, f.r, fmt.Sprintf("git/ref/invariant/runs/%d/%s", f.ref.Issue, f.ref.Step), reader631JSON(t, map[string]any{"object": map[string]string{"sha": f.ref.Commit}}))
	saved897Endpoint(t, f.r, "git/commits/"+f.ref.Commit, reader631JSON(t, map[string]string{"message": message}))
	saved897Endpoint(t, f.r, "contents/result.json?ref="+f.ref.Commit, f.payload)
}

func (f *saved897Fixture) refresh(t *testing.T) {
	t.Helper()
	if err := f.s.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (f *saved897Fixture) restart(t *testing.T, login string) *Server {
	t.Helper()
	s := &Server{Repos: []*Repo{{Name: f.r.Name, FactoryLogin: login, GitHub: f.r.GitHub}}, Cache: f.s.Cache, Branch: "main"}
	s.prepare()
	return s
}

func saved897State(t *testing.T, s *Server, issue int) (Snapshot, *saved897Waiting) {
	t.Helper()
	w := reader631GET(s, "GET", "/api/state.json", "")
	if w.Code != 200 {
		t.Fatalf("state: %d %s", w.Code, w.Body.String())
	}
	var snap Snapshot
	var wire struct {
		Issues []struct {
			Number  int
			Waiting *saved897Waiting
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, is := range wire.Issues {
		if is.Number == issue {
			return snap, is.Waiting
		}
	}
	t.Fatalf("state omits issue %d", issue)
	return snap, nil
}

func saved897Healthy(t *testing.T, s *Server, ref factory.DraftReference, result *formalize.Result) *saved897Waiting {
	t.Helper()
	_, w := saved897State(t, s, ref.Issue)
	if w == nil || w.SavedDraft == nil || w.SavedDraft.Problem != "" || w.SavedDraft.Reader == nil {
		t.Fatalf("compact factory marker did not load complete saved draft evidence: %+v", w)
	}
	d := w.SavedDraft
	if d.Reference != ref || d.Locator != saved897Locator(ref) || !sameReaderJSON(w.Proposal, result.Proposal) {
		t.Fatal("waiting state lost the reference or complete original proposal")
	}
	if !regexp.MustCompile(`^/reader/[0-9a-f]{64}\.html$`).MatchString(d.Reader.Page) || d.Reader.Fingerprint == "" || d.Download != strings.TrimSuffix(d.Reader.Page, ".html")+".json" {
		t.Fatalf("saved draft lacks immutable HTML/JSON identities: %+v", d)
	}
	return w
}

func saved897Response(t *testing.T, s *Server, url, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	w := reader631GET(s, "GET", url, "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), contentType) || w.Header().Get("ETag") == "" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("immutable response %s: %d %v", url, w.Code, w.Header())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("saved evidence lost script-free headers")
	}
	for _, method := range []string{"GET", "HEAD"} {
		c := reader631GET(s, method, url, w.Header().Get("ETag"))
		if c.Code != 304 || c.Body.Len() != 0 {
			t.Fatal("conditional saved response is not bodyless")
		}
	}
	h := reader631GET(s, "HEAD", url, "")
	if h.Code != 200 || h.Body.Len() != 0 || h.Header().Get("ETag") != w.Header().Get("ETag") || h.Header().Get("Content-Length") != fmt.Sprint(w.Body.Len()) {
		t.Fatal("HEAD changed representation metadata or wrote a body")
	}
	if x := reader631GET(s, "POST", url, ""); x.Code != 405 {
		t.Fatal("evidence accepted a write method")
	}
	return w
}

func saved897Evidence(t *testing.T, s *Server, d *saved897Presentation, payload []byte) string {
	t.Helper()
	r := reader631GET(s, "GET", d.Locator, "")
	if r.Code != 302 || r.Header().Get("Location") != d.Reader.Page || !strings.Contains(r.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("deterministic locator did not resolve authenticated evidence: %d %v", r.Code, r.Header())
	}
	h := reader631GET(s, "HEAD", d.Locator, "")
	if h.Code != 302 || h.Body.Len() != 0 || h.Header().Get("Location") != d.Reader.Page {
		t.Fatal("locator HEAD differs")
	}
	if x := reader631GET(s, "DELETE", d.Locator, ""); x.Code != 405 {
		t.Fatal("locator accepted mutation")
	}
	page := saved897Response(t, s, d.Reader.Page, "text/html")
	download := saved897Response(t, s, d.Download, "application/json")
	if !bytes.Equal(download.Body.Bytes(), payload) || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download is not the exact complete saved result.json attachment")
	}
	if download.Header().Get("ETag") == page.Header().Get("ETag") {
		t.Fatal("different representations share an ETag")
	}
	if r := reader631GET(s, "GET", d.Download, page.Header().Get("ETag")); r.Code != 200 {
		t.Fatal("HTML validator incorrectly matches JSON")
	}
	body := page.Body.String()
	if strings.Contains(strings.ToLower(body), "<script") || strings.Contains(body, "/invariant ratify") || strings.Contains(body, "data-act-") || strings.Contains(body, "<form") {
		t.Fatal("evidence page offers actions or active content")
	}
	plain := html.UnescapeString(body)
	for _, word := range []string{d.Reference.Repository, fmt.Sprint(d.Reference.Issue), d.Reference.Commit, d.Reference.SHA256, d.Download} {
		if !strings.Contains(plain, word) {
			t.Fatalf("saved evidence page omits %q", word)
		}
	}
	// The complete saved record must also be readable as text, not just linked.
	found := false
	for _, m := range regexp.MustCompile(`(?s)<pre(?:\s[^>]*)?>(.*?)</pre>`).FindAllStringSubmatch(body, -1) {
		var a, b any
		if json.Unmarshal([]byte(html.UnescapeString(m[1])), &a) == nil && json.Unmarshal(payload, &b) == nil && reflect.DeepEqual(a, b) {
			found = true
		}
	}
	if !found {
		t.Fatal("HTML omits the complete readable result record")
	}
	return plain
}

func saved897Images(t *testing.T, s *Server, w *Waiting, p *formalize.Proposal) {
	t.Helper()
	if w.Reader == nil || w.Reader.Status != "chart" || w.Posters == nil {
		t.Fatal("saved proposal lost native chart/poster presentation")
	}
	image := saved897Response(t, s, w.Reader.SVG, "image/svg+xml")
	want, err := p.Reader.Chart.SVG()
	if p.Reader.Comparison != nil {
		want, err = p.Reader.Comparison.SVG()
	}
	if err != nil || image.Body.String() != want {
		t.Fatal("saved chart differs from native complete evidence renderer")
	}
	for _, url := range []string{w.Posters.Animated, w.Posters.Still} {
		r := saved897Response(t, s, url, "image/svg+xml")
		if !strings.HasPrefix(strings.TrimSpace(r.Body.String()), "<svg") || strings.Contains(r.Body.String(), "<script") {
			t.Fatal("invalid native poster")
		}
	}
}

func TestSavedDraft897ProposalRoundTrip(t *testing.T) {
	result := saved897Proposal(t, true, 2, nil)
	f := saved897FixtureFor(t, result)
	if len(f.payload) <= 4<<20 {
		t.Fatal("fixture must exceed the expanded marker limit")
	}
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	if w.Hash != strings.TrimPrefix(result.Proposal.Hash, "sha256:")[:12] || !slices.Contains(w.Takes, factory.Ratify) {
		t.Fatal("saved proposal changed ratification identity")
	}
	text := saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	for _, word := range []string{saved897Literal, result.Proposal.Statements[1].Says, "Limit", result.Report.Toolchain.JavaImage, result.Proposal.Reader.Chart.Report.Source.Tool.JarSHA256} {
		if !strings.Contains(text, word) {
			t.Fatalf("proposal page omits exact evidence %q", word)
		}
	}
	saved897Images(t, f.s, &w.Waiting, result.Proposal)
	work := cache730Work(t, f.s)
	for i := 0; i < 3; i++ {
		f.refresh(t)
	}
	if cache730Work(t, f.s) != work {
		t.Fatal("unchanged saved draft repeated validation/render/persistence work")
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.r.GitHub.GH), "calls"))
	for _, endpoint := range []string{"git/ref/invariant/runs/897/draft-issue", "git/commits/" + f.ref.Commit, "contents/result.json?ref=" + f.ref.Commit} {
		if !strings.Contains(string(log), "repos/"+f.r.Name+"/"+endpoint+"\n") {
			t.Fatalf("dashboard did not read %s", endpoint)
		}
	}
	cold := f.restart(t, saved897Author)
	again := saved897Healthy(t, cold, f.ref, result)
	if !reflect.DeepEqual(w.SavedDraft, again.SavedDraft) {
		t.Fatal("healthy restart changed immutable saved identity")
	}
	saved897Evidence(t, cold, again.SavedDraft, f.payload)
	saved897Images(t, cold, &again.Waiting, result.Proposal)
	snap, _ := saved897State(t, f.s, 897)
	if err := (act{Repo: f.r.Name, Issue: 897, Body: "/invariant ratify " + w.Hash}).check(snap.Issues, nil); err != nil {
		t.Fatal("existing authorized ratification path refused resolved evidence", err)
	}
	saved897Cards(t, f.s, snap, w.SavedDraft.Locator, false)
}

func TestSavedDraft897QuestionSnapshots(t *testing.T) {
	base := saved897Proposal(t, false, 2, nil)
	result := saved897Questions(t, base.Proposal)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	if len(f.payload) <= 4<<20 || len(w.Forks) != 2 || w.Hash != "" || slices.Contains(w.Takes, factory.Ratify) || result.Proposal.Baseline == nil {
		t.Fatal("question fixture or waiting authority is incomplete")
	}
	text := saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	if !strings.Contains(strings.ToLower(text), "unratified") || !strings.Contains(strings.ToLower(text), "model-only") {
		t.Fatal("alternatives lost their qualifications")
	}
	for i, q := range w.Forks {
		if !strings.Contains(text, q.Question) {
			t.Fatal("question wording missing from complete page")
		}
		for j, o := range q.Options {
			want := result.Proposal.Forks[i].Options[j]
			if o.Reader == nil || !sameReaderJSON(o.Preview, want.Preview) || !strings.Contains(text, o.Says) || !strings.Contains(text, o.Reader.Page) {
				t.Fatal("question page lost an option, exact context or page link")
			}
			preview639Image(t, f.s, preview639Choice{ID: o.ID, Says: o.Says, Reader: o.Reader, Preview: savedPtr639(preview639Saved{Fork: o.Preview.Fork, Option: o.Preview.Option, Choices: o.Preview.Choices, Model: o.Preview.Model, Receipt: o.Preview.Receipt, Problem: o.Preview.Problem, Fingerprint: o.Preview.Fingerprint})})
		}
	}
	old := *w.SavedDraft
	f.marker.Forks = []formalize.Fork{{ID: "F2"}}
	f.marker.Answers = []formalize.Answer{{Fork: "F1", Option: "A", By: "owner", Comment: "https://example.test/answer"}}
	f.comment(t, f.marker, saved897Author)
	f.issues[0].UpdatedAt = "2026-11-12T12:00:30Z"
	f.writeIssues(t)
	f.refresh(t)
	snap, partial := saved897State(t, f.s, 897)
	if partial == nil || len(partial.Forks) != 1 || partial.Forks[0].ID != "F2" || !sameReaderJSON(partial.Answers, f.marker.Answers) || !sameReaderJSON(partial.Proposal, result.Proposal) {
		t.Fatal("partial choice rewrote original snapshots or lost current answers")
	}
	if !reflect.DeepEqual(partial.SavedDraft, &old) {
		t.Fatal("partial answer changed original immutable evidence")
	}
	if err := (act{Repo: f.r.Name, Issue: 897, Body: "/invariant choose F2 B"}).check(snap.Issues, nil); err != nil {
		t.Fatal(err)
	}
	if err := (act{Repo: f.r.Name, Issue: 897, Body: "/invariant choose F1 A"}).check(snap.Issues, nil); err == nil {
		t.Fatal("answered question remains selectable")
	}
	saved897Evidence(t, f.s, &old, f.payload)
	cold := f.restart(t, saved897Author)
	saved897Healthy(t, cold, f.ref, result)
	saved897Evidence(t, cold, &old, f.payload)
	saved897Cards(t, f.s, snap, old.Locator, false)
	// Answering the final visible fork removes the card, but not the complete
	// question or any of the option pages it has already published.
	comments := []github.Comment{
		{ID: 897, User: github.User{Login: saved897Author}, CreatedAt: "2026-11-12T12:00:01Z", Body: marker(t, f.marker)},
		{ID: 899, User: github.User{Login: "owner"}, AuthorAssociation: "OWNER", CreatedAt: "2026-11-12T12:00:40Z", Body: "/invariant choose F2 B"},
	}
	saved897Endpoint(t, f.r, "issues/897/comments?per_page=100", reader631JSON(t, comments))
	f.issues[0].UpdatedAt = "2026-11-12T12:00:45Z"
	f.writeIssues(t)
	f.refresh(t)
	_, answered := saved897State(t, f.s, 897)
	if answered != nil {
		t.Fatal("answered question still has a Needs you card")
	}
	cold = f.restart(t, saved897Author)
	saved897Evidence(t, cold, &old, f.payload)
	for _, q := range w.Forks {
		for _, o := range q.Options {
			saved897Response(t, cold, o.Reader.Page, "text/html")
			saved897Response(t, cold, o.Reader.SVG, "image/svg+xml")
		}
	}
}

func TestSavedDraft897ComparisonsAndLimitations(t *testing.T) {
	base := saved897Proposal(t, false, 1, nil)
	for _, limit := range []int{3, 501} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var previous *formalize.Proposal
			if limit == 3 {
				previous = base.Proposal
			}
			result := saved897Proposal(t, false, limit, previous)
			f := saved897FixtureFor(t, result)
			f.s.prepare()
			f.refresh(t)
			w := saved897Healthy(t, f.s, f.ref, result)
			text := saved897Evidence(t, f.s, w.SavedDraft, f.payload)
			if limit == 3 {
				c := result.Proposal.Reader.Comparison
				if c == nil {
					t.Fatal("fixture lacks captured comparison")
				}
				for _, word := range append(c.Qualifications(), c.Baseline.Name, c.Candidate.Name) {
					if !strings.Contains(text, word) {
						t.Fatalf("saved comparison omits %q", word)
					}
				}
				saved897Images(t, f.s, &w.Waiting, result.Proposal)
			} else {
				if w.Reader == nil || w.Reader.Status != "too_large" || w.Reader.SVG != "" || w.Posters != nil || !strings.Contains(text, "502") || !strings.Contains(text, "500") {
					t.Fatal("too_large lost checked count or fabricated a chart")
				}
				if x := reader631GET(f.s, "GET", strings.TrimSuffix(w.SavedDraft.Reader.Page, ".html")+".svg", ""); x.Code == 200 {
					t.Fatal("limited saved draft invented an SVG")
				}
			}
		})
	}
}

func saved897Unavailable(t *testing.T, s *Server, issue int) *saved897Waiting {
	t.Helper()
	snap, w := saved897State(t, s, issue)
	if w == nil || w.SavedDraft == nil || w.SavedDraft.Problem == "" {
		t.Fatal("unresolved reference disappeared or was treated as a legacy chart-free proposal")
	}
	if slices.Contains(w.Takes, factory.Ratify) || slices.Contains(w.Takes, factory.Choose) || w.Hash != "" || w.Together {
		t.Fatal("unresolved evidence offers ratification or choices")
	}
	for _, body := range []string{"/invariant ratify 0123456789ab", "/invariant choose F1 A"} {
		if err := (act{Repo: snap.Repo, Issue: issue, Body: body}).check(snap.Issues, nil); err == nil {
			t.Fatal("command gate accepted unresolved evidence", body)
		}
	}
	return w
}

func saved897NonSuccess(t *testing.T, s *Server, url string) {
	t.Helper()
	w := reader631GET(s, "GET", url, "")
	if w.Code < 400 || w.Body.Len() == 0 || w.Header().Get("Location") != "" {
		t.Fatalf("missing/invalid saved evidence is not an explicit non-success: %s %d", url, w.Code)
	}
	h := reader631GET(s, "HEAD", url, "")
	if h.Code != w.Code || h.Body.Len() != 0 {
		t.Fatal("failed saved-evidence HEAD must also be bodyless")
	}
}

func TestSavedDraft897AuthorityAndRecovery(t *testing.T) {
	result := saved897Proposal(t, false, 2, nil)
	// Positive control is itself new behavior, so no negative-only case can
	// qualify this acceptance function by passing on the old dashboard.
	good := saved897FixtureFor(t, result)
	good.s.prepare()
	good.refresh(t)
	saved897Healthy(t, good.s, good.ref, result)
	for _, kind := range []string{"repository", "issue", "commit", "digest", "proposal hash", "step", "version", "recorded run", "run commit", "payload", "rehashed payload"} {
		t.Run(kind, func(t *testing.T) {
			f := saved897FixtureFor(t, result)
			ref := f.ref
			switch kind {
			case "repository":
				ref.Repository = "owner/elsewhere"
			case "issue":
				ref.Issue++
			case "commit":
				ref.Commit = strings.Repeat("c", 40)
			case "digest":
				ref.SHA256 = "sha256:" + strings.Repeat("d", 64)
			case "proposal hash":
				ref.ProposalHash = "sha256:" + strings.Repeat("e", 64)
			case "step":
				ref.Step = "../draft-issue"
			case "version":
				ref.Version = 9
			case "recorded run":
				f.record(t, "invariant: recorded draft fixture")
			case "run commit":
				saved897Endpoint(t, f.r, "git/ref/invariant/runs/897/draft-issue", []byte(`{"object":{"sha":"`+strings.Repeat("c", 40)+`"}}`))
			case "payload", "rehashed payload":
				bad := cache730Clone(t, result)
				bad.Proposal.Includes["Helper603.tla"] += "\n\\* changed without a check\n"
				b := reader631JSON(t, bad)
				saved897Endpoint(t, f.r, "contents/result.json?ref="+ref.Commit, b)
				if kind == "rehashed payload" {
					ref.SHA256 = reader631Hash(b)
				}
			}
			f.marker.ProposalRef = reader631JSON(t, ref)
			f.comment(t, f.marker, saved897Author)
			f.s.prepare()
			f.refresh(t)
			saved897Unavailable(t, f.s, 897)
			saved897NonSuccess(t, f.s, saved897Locator(ref))
		})
	}
	for _, author := range []string{"copier", "unknown factory"} {
		t.Run(author, func(t *testing.T) {
			f := saved897FixtureFor(t, result)
			if author == "copier" {
				f.comment(t, f.marker, "person")
			} else {
				f.r.FactoryLogin = ""
			}
			f.s.Work = t.TempDir()
			reader631Write(t, filepath.Join(f.s.Work, "897", "draft-issue", "result.json"), f.payload)
			f.s.prepare()
			f.refresh(t)
			_, w := saved897State(t, f.s, 897)
			if w != nil && (w.Hash != "" || len(w.Forks) != 0 || w.SavedDraft != nil && w.SavedDraft.Reader != nil) {
				t.Fatal("copied marker or local run established authority")
			}
			saved897NonSuccess(t, f.s, saved897Locator(f.ref))
			log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.r.GitHub.GH), "calls"))
			if strings.Contains(string(log), "contents/result.json") || strings.Contains(string(log), "git/ref/invariant/runs") {
				t.Fatal("untrusted marker caused saved-run reads")
			}
		})
	}
	for _, u := range []string{
		strings.Replace(saved897Locator(good.ref), "owner/saved", "stranger/private", 1),
		strings.Replace(saved897Locator(good.ref), good.ref.Commit, strings.Repeat("f", 40), 1),
		strings.Replace(saved897Locator(good.ref), strings.TrimPrefix(good.ref.SHA256, "sha256:"), "0123456789ab", 1),
	} {
		saved897NonSuccess(t, good.s, u)
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(good.r.GitHub.GH), "calls"))
	if strings.Contains(string(log), "repos/stranger/") || strings.Contains(string(log), "contents/result.json?ref="+strings.Repeat("f", 40)) {
		t.Fatal("locator turned the dashboard into a Git file server")
	}

	f := saved897FixtureFor(t, result)
	file := saved897Endpoint(t, f.r, "contents/result.json?ref="+f.ref.Commit, f.payload)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	f.s.Work = t.TempDir()
	reader631Write(t, filepath.Join(f.s.Work, "897", "draft-issue", "result.json"), f.payload)
	f.s.prepare()
	f.refresh(t)
	saved897Unavailable(t, f.s, 897)
	saved897NonSuccess(t, f.s, saved897Locator(f.ref))
	snap, _ := saved897State(t, f.s, 897)
	saved897Cards(t, f.s, snap, "", true)
	// Restore only the pinned bytes: issue UpdatedAt and comment ID/body stay
	// identical, and no local run or new branch can satisfy this retry.
	reader631Write(t, file, f.payload)
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	saved897Evidence(t, f.s, w.SavedDraft, f.payload)
}

func TestSavedDraft897DurableLocatorsAndFailures(t *testing.T) {
	result := saved897Proposal(t, false, 2, nil)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	old, oldPayload, oldRef := *w.SavedDraft, bytes.Clone(f.payload), f.ref
	before := reader631GET(f.s, "GET", "/api/state.json", "").Body.String()
	disk, err := os.ReadFile(f.s.snapshotFile())
	if err != nil {
		t.Fatal(err)
	}
	oldPage := reader631GET(f.s, "GET", old.Reader.Page, "").Body.String()
	next := saved897Proposal(t, false, 3, result.Proposal)
	f.install(t, next, 897, "draft-898", strings.Repeat("c", 64))
	f.issues[0].UpdatedAt = "2026-11-12T12:01:00Z"
	f.writeIssues(t)
	if err := os.Mkdir(f.s.snapshotFile()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.s.refresh(t.Context()); err == nil {
		t.Fatal("blocked snapshot replacement succeeded")
	}
	if reader631GET(f.s, "GET", "/api/state.json", "").Body.String() != before {
		t.Fatal("failed snapshot replacement changed the public read")
	}
	kept, err := os.ReadFile(f.s.snapshotFile())
	if err != nil || !bytes.Equal(kept, disk) {
		t.Fatal("failed snapshot replacement changed durable read")
	}
	saved897Evidence(t, f.s, &old, oldPayload)
	if err := os.RemoveAll(f.s.snapshotFile() + ".tmp"); err != nil {
		t.Fatal(err)
	}

	// A destination that cannot hold any cache file exercises evidence saving
	// failures too, without chmod assumptions that differ for root in CI.
	cache := f.s.Cache
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	reader631Write(t, blocked, []byte("blocked"))
	f.s.Cache = blocked
	if err := f.s.refresh(t.Context()); err == nil {
		t.Fatal("unwritable cache replaced the durable snapshot")
	}
	saved897Evidence(t, f.s, &old, oldPayload)
	f.s.Cache = cache
	f.refresh(t)
	newW := saved897Healthy(t, f.s, f.ref, next)
	if newW.SavedDraft.Locator == old.Locator || newW.SavedDraft.Reader.Page == old.Reader.Page {
		t.Fatal("revision reused old immutable identity")
	}
	saved897Evidence(t, f.s, &old, oldPayload)
	if reader631GET(f.s, "GET", old.Reader.Page, "").Body.String() != oldPage {
		t.Fatal("revision retargeted old rendered evidence")
	}

	// After ratification there is no current Needs you card to act as an index.
	f.marker.Kind, f.marker.Hash = factory.KindRatified, next.Proposal.Hash
	f.comment(t, f.marker, saved897Author)
	f.issues[0].UpdatedAt = "2026-11-12T12:02:00Z"
	f.writeIssues(t)
	f.refresh(t)
	_, after := saved897State(t, f.s, 897)
	if after != nil {
		t.Fatal("ratified fixture is still waiting")
	}
	// Generated comparison/preview pages can be evicted. Their saved source
	// authority and original record must have a longer lifetime than the card.
	for _, directory := range []string{"comparisons", "previews"} {
		if err := os.RemoveAll(filepath.Join(cache, directory)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []factory.DraftReference{oldRef, f.ref} {
		for _, endpoint := range []string{"contents/result.json?ref=" + ref.Commit, "git/commits/" + ref.Commit, fmt.Sprintf("git/ref/invariant/runs/%d/%s", ref.Issue, ref.Step)} {
			file := filepath.Join(filepath.Dir(f.r.GitHub.GH), strings.TrimPrefix(reader631Hash([]byte("repos/"+f.r.Name+"/"+endpoint)), "sha256:"))
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	cold := f.restart(t, saved897Author)
	saved897Evidence(t, cold, &old, oldPayload)
	saved897Evidence(t, cold, newW.SavedDraft, f.payload)
	if reader631GET(cold, "GET", old.Reader.Page, "").Body.String() != oldPage {
		t.Fatal("offline restart moved the old link to new evidence")
	}
	for _, login := range []string{"", "other-factory[bot]"} {
		wrong := f.restart(t, login)
		saved897NonSuccess(t, wrong, old.Locator)
	}
}

// Mutate retained source bytes, including JSON strings or base64 byte fields,
// without knowing the implementation's private record layout. Leave reference
// digests and factory comment bytes intact. Gzip snapshot files are covered too.
func saved897Damage(t *testing.T, cache string) int {
	t.Helper()
	changed := 0
	var alter func(any) (any, bool)
	alter = func(v any) (any, bool) {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, saved897Source) {
				return strings.ReplaceAll(x, saved897Source, "saved897 ALTERED source"), true
			}
			if b, err := base64.StdEncoding.DecodeString(x); err == nil && bytes.Contains(b, []byte(saved897Source)) {
				return base64.StdEncoding.EncodeToString(bytes.ReplaceAll(b, []byte(saved897Source), []byte("saved897 ALTERED source"))), true
			}
		case map[string]any:
			found := false
			for k, a := range x {
				b, ok := alter(a)
				if ok {
					x[k], found = b, true
				}
			}
			return x, found
		case []any:
			found := false
			for i, a := range x {
				b, ok := alter(a)
				if ok {
					x[i], found = b, true
				}
			}
			return x, found
		}
		return v, false
	}
	err := filepath.WalkDir(cache, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		zipped := bytes.HasPrefix(b, []byte{0x1f, 0x8b})
		if zipped {
			b, err = gunzip(b)
			if err != nil {
				return err
			}
		}
		if bytes.Contains(b, []byte(saved897Source)) {
			b = bytes.ReplaceAll(b, []byte(saved897Source), []byte("saved897 ALTERED source"))
		} else {
			var value any
			if json.Unmarshal(b, &value) != nil {
				return nil
			}
			next, ok := alter(value)
			if !ok {
				return nil
			}
			b, err = json.Marshal(next)
			if err != nil {
				return err
			}
		}
		if zipped {
			b, err = gzipped(b)
			if err != nil {
				return err
			}
		}
		changed++
		return os.WriteFile(path, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestSavedDraft897RestartRejectsDamage(t *testing.T) {
	bad := saved897Proposal(t, true, 2, nil)
	good := saved897Proposal(t, false, 3, nil)
	f := saved897FixtureFor(t, bad)
	sibling := &saved897Fixture{s: f.s, r: f.r}
	sibling.install(t, good, 898, "draft-issue", strings.Repeat("d", 40))
	f.issues = append(f.issues, sibling.issues...)
	f.writeIssues(t)
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, bad)
	healthy := saved897Healthy(t, f.s, sibling.ref, good)
	if saved897Damage(t, f.s.Cache) == 0 {
		t.Fatal("fixture did not damage any retained saved source")
	}
	file := saved897Endpoint(t, f.r, "contents/result.json?ref="+f.ref.Commit, f.payload)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	cold := f.restart(t, saved897Author)
	saved897Unavailable(t, cold, 897)
	saved897Healthy(t, cold, sibling.ref, good)
	saved897Evidence(t, cold, healthy.SavedDraft, sibling.payload)
	saved897NonSuccess(t, cold, w.SavedDraft.Locator)
	saved897NonSuccess(t, cold, w.SavedDraft.Download)
	// Repeated restart must not turn altered bytes into fresh authority.
	again := &saved897Fixture{s: cold, r: cold.Repos[0]}
	cold = again.restart(t, saved897Author)
	saved897Unavailable(t, cold, 897)
	saved897NonSuccess(t, cold, w.SavedDraft.Locator)
	reader631Write(t, file, f.payload)
	if err := cold.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered := saved897Healthy(t, cold, f.ref, bad)
	if !reflect.DeepEqual(recovered.SavedDraft, w.SavedDraft) {
		t.Fatal("repair did not restore original validated identity")
	}
	saved897Evidence(t, cold, recovered.SavedDraft, f.payload)
	foreign := &Server{Repos: []*Repo{{Name: "owner/other", FactoryLogin: saved897Author, GitHub: f.r.GitHub}}, Cache: f.s.Cache, Branch: "main"}
	foreign.prepare()
	saved897NonSuccess(t, foreign, w.SavedDraft.Locator)
}

// Exercise both actual card renderers when the repository's browser opt-in is
// available. The mandatory marker/loading/HTTP/command assertions above run
// without a browser; no acceptance test skips or depends on installing one.
func saved897Cards(t *testing.T, s *Server, snap Snapshot, locator string, unavailable bool) {
	t.Helper()
	browser := os.Getenv("INVARIANT_DASHBOARD_BROWSER")
	if browser == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
			if p, err := exec.LookPath(name); err == nil {
				browser = p
				break
			}
		}
	}
	if browser == "" {
		t.Log("card browser check available through INVARIANT_DASHBOARD_BROWSER")
		return
	}
	team := newAccessTeam(t)
	s.Access = team.access()
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	script := fmt.Sprintf("const fixture=%s; const expected=%s; const unavailable=%t;\n", reader631JSON(t, snap), reader631JSON(t, locator), unavailable) + saved897Browser
	handler := s.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/saved897-check.js" {
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, script)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/act" {
			r.Header.Set("Cf-Access-Jwt-Assertion", owner)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for k, v := range recorder.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(recorder.Code)
			fmt.Fprint(w, strings.Replace(recorder.Body.String(), `<script src="/app.js`, `<script src="/saved897-check.js"></script><script src="/app.js`, 1))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, page := range []string{"/", "/act"} {
		out, err := browserDOM(t, browser, server.URL+page, 20000)
		if err != nil || !strings.Contains(string(out), `data-saved897="passed"`) {
			t.Fatalf("saved evidence card on %s: %v\n%s", page, err, out)
		}
	}
}

const saved897Browser = `
(() => {
  localStorage.clear();
  const json = (v) => new Response(JSON.stringify(v), {headers: {"Content-Type":"application/json"}});
  window.fetch = (url) => {
    if (String(url).includes("/api/state.json")) return Promise.resolve(json(structuredClone(fixture)));
    if (String(url).includes("/api/live.json")) return Promise.resolve(json({steps:[]}));
    return Promise.reject(new Error("unexpected request: " + url));
  };
  const assert = (ok, why) => { if (!ok) throw new Error(why); };
  async function check() {
    let card;
    for (let i=0;i<1500;i++) {
      card = document.querySelector('#inbox [data-issue="owner/saved#897"]');
      if (card) break;
      await new Promise((r) => setTimeout(r,10));
    }
    assert(card, "missing Needs you card");
    assert(!window.saved897, "hostile literal executed");
    if (unavailable) {
      const problem = fixture.issues.find((i)=>i.number===897).waiting.savedDraft.problem;
      assert(card.textContent.includes(problem), "missing diagnostic");
      const controls = [...card.querySelectorAll('button:not([disabled])')];
      assert(!controls.some((b)=>/\/invariant (ratify|choose)/.test(b.dataset.cmd||b.dataset.actBody||b.dataset.actConfirm||'') || b.hasAttribute('data-act-pick')), "unresolved choice/ratify control");
    } else {
      assert([...card.querySelectorAll('a')].some((a)=>a.getAttribute('href')===expected), "card does not link the same saved locator");
      const w = fixture.issues.find((i)=>i.number===897).waiting;
      const pages = w.kind==='forks' ? w.forks.flatMap((q)=>q.options.map((o)=>o.reader.page)) : [w.reader.page];
      for (const page of pages) assert([...card.querySelectorAll('a')].some((a)=>a.getAttribute('href')===page), "saved link replaced existing presentation");
    }
  }
  addEventListener('load',()=>check().then(()=>{document.body.dataset.saved897='passed';},(e)=>{document.body.dataset.saved897='failed: '+e.message;}));
})();
`
