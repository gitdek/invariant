package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// /act opens a PRD for the factory to plan (#201): what the plan must carry
// out, and /invariant plan, with no Project:, Code: or language, since each
// issue the plan opens names its own.
func TestActOpensAPRDToPlan(t *testing.T) {
	team := newAccessTeam(t)
	var opened []github.NewIssue
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: team.access(), Log: t.Logf,
		Open: func(_ context.Context, repo string, is github.NewIssue) (int, string, error) {
			opened = append(opened, is)
			return 30, "https://github.com/o/r/issues/30", nil
		}}
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	send := func(n newIssue) int {
		b, _ := json.Marshal(n)
		return togetherSend(s.Handler(), owner, "/act/api/issue", string(b), nil).Code
	}
	prd := newIssue{Repo: "o/r", Title: "Prove the decision journal in parts", Body: "Prove the journal, part by part.", Plan: true}
	if code := send(prd); code != 200 {
		t.Fatalf("opening a PRD: %d", code)
	}
	if len(opened) != 1 || opened[0].Body != "Prove the journal, part by part.\n\n/invariant plan\n" || len(opened[0].Labels) != 0 {
		t.Fatalf("opened %+v", opened)
	}
	for name, change := range map[string]func(*newIssue){
		"a project":  func(n *newIssue) { n.Project = "factory/journal" },
		"code":       func(n *newIssue) { n.Code = []string{"internal/decisions"} },
		"a language": func(n *newIssue) { n.Language = "go" },
	} {
		n := prd
		change(&n)
		if code := send(n); code != 400 {
			t.Errorf("a PRD with %s: %d, want 400", name, code)
		}
	}
	if len(opened) != 1 {
		t.Errorf("a refused PRD was opened: %+v", opened)
	}
}

// /act offers to plan each open issue a writer opened that the factory
// hasn't taken, and only those (#201): not a stranger's, not one a writer
// has already handed the factory in a comment, and not one the factory is
// working on. Only a person Cloudflare Access signed in can see the list.
// Planning one posts /invariant plan as the owner, and it's offered no
// more, before GitHub lists the comment and after.
func TestActPlansAnIssueTheFactoryHasntTaken(t *testing.T) {
	f := atOnceStart(t, 50*time.Millisecond)
	opened := f.t0.Add(-time.Hour)
	plain := func(n int, title, login, association string) *github.Issue {
		at := opened.Format(time.RFC3339)
		return &github.Issue{Number: n, Title: title, User: github.User{Login: login}, State: "open", URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n),
			CreatedAt: at, UpdatedAt: at, AuthorAssociation: association}
	}
	solve := atOnceComment(32, 3200, "gitdek", opened, "/invariant solve")
	solve.AuthorAssociation = "OWNER"
	f.mu.Lock()
	f.issues = append(f.issues,
		plain(30, "Prove the decision journal in parts", "gitdek", "OWNER"),
		plain(31, "Please add a dark mode", "mallory", "NONE"),
		plain(32, "Keep the logs for a week", "gitdek", "OWNER"))
	f.comments[32] = []github.Comment{solve}
	f.mu.Unlock()
	f.write()

	list := func(tok string) (int, []string) {
		r := httptest.NewRequest("GET", "https://invariant.example.com/act/api/untaken.json", nil)
		if tok != "" {
			r.Header.Set("Cf-Access-Jwt-Assertion", tok)
		}
		w := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(w, r)
		var untaken []Untaken
		json.Unmarshal(w.Body.Bytes(), &untaken)
		var out []string
		for _, u := range untaken {
			out = append(out, fmt.Sprintf("%s#%d %s", u.Repo, u.Number, u.Title))
		}
		return w.Code, out
	}
	want := []string{"o/r#30 Prove the decision journal in parts"}
	f.soon("/act to offer to plan #30", func() (bool, string) {
		code, got := list(f.owner)
		return code == 200 && slices.Equal(got, want), fmt.Sprintf("%d %q", code, got)
	})
	if code, _ := list(""); code != 403 {
		t.Errorf("the list without signing in: %d, want 403", code)
	}

	plan := func(n int) *httptest.ResponseRecorder {
		return f.act("/act/api/comment", fmt.Sprintf(`{"repo":"o/r","issue":%d,"body":"/invariant plan"}`, n))
	}
	for _, n := range []int{31, 32, 14, 99} {
		if w := plan(n); w.Code != 400 {
			t.Errorf("planning #%d: %d, want 400", n, w.Code)
		}
	}
	if len(f.taken()) != 0 {
		t.Fatalf("a refused plan was posted: %q", f.taken())
	}
	from := f.listings()
	if w := plan(30); w.Code != 200 {
		t.Fatalf("planning #30: %d %s", w.Code, w.Body.String())
	}
	if got := f.taken(); len(got) != 1 || got[0] != "o/r#30 /invariant plan" {
		t.Fatalf("posted %q", got)
	}
	if _, got := list(f.owner); len(got) != 0 {
		t.Errorf("/act still offers to plan %q once it's planned", got)
	}
	// Refreshes that read GitHub before it lists the comment leave it out,
	// and so do the ones after.
	f.refreshed(from, 2)
	if _, got := list(f.owner); len(got) != 0 {
		t.Errorf("/act offers to plan %q again before GitHub lists the comment", got)
	}
	planned := atOnceComment(30, f.id(30), "gitdek", time.Now().UTC(), "/invariant plan")
	planned.AuthorAssociation = "OWNER"
	f.mu.Lock()
	f.comments[30] = []github.Comment{planned}
	for _, is := range f.issues {
		if is.Number == 30 {
			is.UpdatedAt = planned.CreatedAt
		}
	}
	f.mu.Unlock()
	f.write()
	f.s.mu.Lock()
	f.s.planned = nil
	f.s.mu.Unlock()
	f.refreshed(f.listings(), 2)
	if _, got := list(f.owner); len(got) != 0 {
		t.Errorf("/act offers to plan %q again once GitHub lists the comment", got)
	}
	if w := plan(30); w.Code != 400 || !strings.Contains(w.Body.String(), "#30") {
		t.Errorf("planning #30 twice: %d %s", w.Code, w.Body.String())
	}
}
