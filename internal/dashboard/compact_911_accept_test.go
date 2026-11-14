package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
)

func TestCompact911FactoryLinkReturnsFullDashboardRecord(t *testing.T) {
	result := saved897Proposal(t, true, 2, nil)
	var record map[string]any
	if err := json.Unmarshal(reader631JSON(t, result), &record); err != nil {
		t.Fatal(err)
	}
	record["publication"] = map[string]any{"agent": "codex"}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Downloads must retain these original bytes, not reserialize the result.
	payload := append(append([]byte(" \n"), encoded...), '\n', '\t')
	repo := compact911DashboardRepo{commit: strings.Repeat("c", 40), payload: payload}
	gh := &compact911DashboardGitHub{issue: github.Issue{Number: 911, State: "open", Title: "Complete compact decision", Body: "/invariant solve", User: github.User{Login: "owner"}, CreatedAt: "2026-11-12T12:00:00Z"}}
	watch := &factory.Factory{Repository: "owner/saved", Self: saved897Author, GitHub: gh, Repo: repo, Base: "main", Projects: "examples", Work: t.TempDir()}
	const dashboardURL = "https://dashboard.example"
	if field := reflect.ValueOf(watch).Elem().FieldByName("DashboardURL"); field.IsValid() {
		field.SetString(dashboardURL)
	}
	if err := watch.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 1 {
		t.Fatalf("factory produced %d comments, want one", len(gh.comments))
	}
	comment := gh.comments[0]
	m, ok := factory.DecodeMarker(comment.Body)
	if !ok || m.Kind != factory.KindProposal || m.ProposalRef == nil || len(comment.Body) > 60000 {
		t.Fatal("factory refused valid saved evidence instead of publishing a compact proposal")
	}
	var ref factory.DraftReference
	if err := json.Unmarshal(m.ProposalRef, &ref); err != nil {
		t.Fatal(err)
	}
	wantURL := dashboardURL + saved897Locator(ref)
	found := false
	for _, link := range regexp.MustCompile(`\]\((https?://[^\s)]+)\)`).FindAllStringSubmatch(comment.Body, -1) {
		if link[1] == wantURL {
			found = true
		}
	}
	if !found {
		t.Fatalf("factory comment has no exact saved locator %s", wantURL)
	}
	fixture := saved897FixtureFor(t, result)
	fixture.ref, fixture.payload, fixture.marker = ref, payload, m
	fixture.issues = []github.Issue{gh.issue}
	fixture.writeIssues(t)
	fixture.record(t, "complete checked draft")
	saved897Endpoint(t, fixture.r, "issues/911/comments?per_page=100", reader631JSON(t, gh.comments))
	fixture.refresh(t)
	_, waiting := saved897State(t, fixture.s, 911)
	if waiting.SavedDraft == nil || waiting.SavedDraft.Problem != "" || waiting.SavedDraft.Reference != ref {
		t.Fatal("dashboard did not hydrate the factory-produced reference")
	}
	u, err := url.Parse(wantURL)
	if err != nil {
		t.Fatal(err)
	}
	redirect := httptest.NewRecorder()
	fixture.s.Handler().ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, u.RequestURI(), nil))
	if redirect.Code != http.StatusFound || redirect.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("factory locator did not resolve: %d %s", redirect.Code, redirect.Body.String())
	}
	page := redirect.Header().Get("Location")
	if page != waiting.SavedDraft.Reader.Page {
		t.Fatal("locator redirected to another presentation")
	}
	html := saved897Response(t, fixture.s, page, "text/html")
	if !strings.Contains(html.Body.String(), strings.TrimPrefix(page, "/reader/")) && !strings.Contains(html.Body.String(), ".json") {
		t.Fatal("saved evidence page does not expose the complete JSON attachment")
	}
	download := strings.TrimSuffix(page, ".html") + ".json"
	response := saved897Response(t, fixture.s, download, "application/json")
	if !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatal("following the factory link lost full saved bytes, sources or reader evidence")
	}
	if reader631Hash(response.Body.Bytes()) != ref.SHA256 {
		t.Fatal("download does not match the comment's digest")
	}
	if _, got, err := m.ResolveDraft(t.Context(), "owner/saved", 911, repo.Run, repo.Show); err != nil || !sameReaderJSON(got.Proposal, result.Proposal) {
		t.Fatalf("shared resolution changed the proposal: %v", err)
	}
	// The same public locator survives a dashboard restart without network.
	restarted := fixture.restart(t, saved897Author)
	redirect = httptest.NewRecorder()
	restarted.Handler().ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, u.RequestURI(), nil))
	if redirect.Code != http.StatusFound {
		t.Fatal("offline restart lost a factory-produced evidence locator")
	}
	response = saved897Response(t, restarted, strings.TrimSuffix(redirect.Header().Get("Location"), ".html")+".json", "application/json")
	if !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatal("restart retargeted the comment's saved record")
	}
}

type compact911DashboardRepo struct {
	factory.Repo
	commit  string
	payload []byte
}

func (r compact911DashboardRepo) Run(_ context.Context, n int, step string) (factory.RunState, string, error) {
	if n == 911 && step == "draft-issue" {
		return factory.RunDone, r.commit, nil
	}
	return factory.RunNone, "", nil
}
func (r compact911DashboardRepo) Show(_ context.Context, ref, path string) ([]byte, error) {
	if ref != r.commit || path != "result.json" {
		return nil, fmt.Errorf("unexpected saved-record read %s:%s", ref, path)
	}
	return append([]byte(nil), r.payload...), nil
}

type compact911DashboardGitHub struct {
	factory.GitHub
	issue    github.Issue
	comments []github.Comment
}

func (g *compact911DashboardGitHub) OpenIssues(context.Context) ([]github.Issue, error) {
	return []github.Issue{g.issue}, nil
}
func (g *compact911DashboardGitHub) ClosedIssues(context.Context, string) ([]github.Issue, error) {
	return nil, nil
}
func (g *compact911DashboardGitHub) MergedPullRequests(context.Context) ([]github.PullRequest, error) {
	return nil, nil
}
func (g *compact911DashboardGitHub) Issue(context.Context, int) (github.Issue, error) {
	return g.issue, nil
}
func (g *compact911DashboardGitHub) Comments(context.Context, int) ([]github.Comment, error) {
	return g.comments, nil
}
func (g *compact911DashboardGitHub) Events(context.Context, int) ([]github.Event, error) {
	return nil, nil
}
func (g *compact911DashboardGitHub) Permission(context.Context, string) (string, error) {
	return "admin", nil
}
func (g *compact911DashboardGitHub) AddLabels(_ context.Context, _ int, labels ...string) error {
	for _, l := range labels {
		g.issue.Labels = append(g.issue.Labels, github.Label{Name: l})
	}
	return nil
}
func (g *compact911DashboardGitHub) RemoveLabel(_ context.Context, _ int, label string) error {
	var keep []github.Label
	for _, l := range g.issue.Labels {
		if l.Name != label {
			keep = append(keep, l)
		}
	}
	g.issue.Labels = keep
	return nil
}
func (g *compact911DashboardGitHub) PostComment(_ context.Context, n int, body string) (github.Comment, error) {
	c := github.Comment{ID: 911, User: github.User{Login: saved897Author, Type: "Bot"}, Body: body, URL: fmt.Sprintf("https://github.com/owner/saved/issues/%d#issuecomment-911", n), CreatedAt: "2026-11-12T12:00:01Z"}
	g.comments = append(g.comments, c)
	return c, nil
}
