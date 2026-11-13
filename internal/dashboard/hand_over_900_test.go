package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// /act hands an issue the factory hasn't taken to the factory with the one
// command that fits it (#900). An issue whose text names one change, in a
// Kind:, Project: or Code: line, is handed over with /invariant solve. One
// that names none is a PRD, and is planned. One that says it waits, in a
// Waits: line, is listed with its reason, and neither command is posted
// for it. Before, every one of them was offered /invariant plan, which has
// the factory restate one change as a plan of issues.
func TestActHandsAnIssueOverWithTheCommandThatFitsIt900(t *testing.T) {
	f := atOnceStart(t, 50*time.Millisecond)
	opened := f.t0.Add(-time.Hour)
	issue := func(n int, title, body string) *github.Issue {
		at := opened.Format(time.RFC3339)
		return &github.Issue{Number: n, Title: title, Body: body, User: github.User{Login: "gitdek"}, State: "open",
			URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n), CreatedAt: at, UpdatedAt: at, AuthorAssociation: "OWNER"}
	}
	f.mu.Lock()
	f.issues = append(f.issues,
		issue(30, "Check every mesh a person uploads", "A long request with no line that names a change.\n\n```\nKind: plumbing\nWaits: nothing, this is a quotation\n```\n"),
		issue(31, "Let a path hold brackets", "Kind: plumbing\n\nA repository path may hold [ and ].\n"),
		issue(32, "Check the repair loop", "Code: hooks/use-auto-repair.ts\n\nCheck the hook as it is.\n"),
		issue(33, "Amend the rate limiter", "Project: examples/04-api-rate-limiter\n\nA burst never exceeds its limit.\n"),
		issue(34, "Compare states one size larger", "Kind: plumbing\nWaits: for #884, which changes the same check.\n\nCompare the states, not the counts.\n"),
		issue(35, "Pin a statement's layout", "waits : a decision, since it changes what a lock records\n\nA long request.\n"))
	f.mu.Unlock()
	f.write()

	list := func() map[int]Untaken {
		r := httptest.NewRequest("GET", "https://invariant.example.com/act/api/untaken.json", nil)
		r.Header.Set("Cf-Access-Jwt-Assertion", f.owner)
		w := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(w, r)
		var untaken []Untaken
		json.Unmarshal(w.Body.Bytes(), &untaken)
		out := map[int]Untaken{}
		for _, u := range untaken {
			out[u.Number] = u
		}
		return out
	}
	type offer struct{ hand, waits string }
	want := map[int]offer{
		30: {"plan", ""},
		31: {"solve", ""},
		32: {"solve", ""},
		33: {"solve", ""},
		34: {"solve", "for #884, which changes the same check."},
		35: {"plan", "a decision, since it changes what a lock records"},
	}
	offers := func() map[int]offer {
		out := map[int]offer{}
		for n, u := range list() {
			out[n] = offer{u.Hand, u.Waits}
		}
		return out
	}
	f.soon("/act to list the six issues", func() (bool, string) {
		got := offers()
		return reflect.DeepEqual(got, want), fmt.Sprintf("%+v", got)
	})

	post := func(n int, verb string) *httptest.ResponseRecorder {
		return f.act("/act/api/comment", fmt.Sprintf(`{"repo":"o/r","issue":%d,"body":"/invariant %s"}`, n, verb))
	}
	for _, tc := range []struct {
		n    int
		verb string
		says string
	}{
		{31, "plan", "is one change, not a PRD"},
		{32, "plan", "is one change, not a PRD"},
		{33, "plan", "is one change, not a PRD"},
		{30, "solve", "names no Kind, Project or Code"},
		{34, "solve", "says it waits, so it can't be handed over from here: for #884, which changes the same check."},
		{34, "plan", "says it waits, so it can't be planned from here"},
		{35, "plan", "says it waits"},
		{35, "solve", "says it waits"},
		{99, "solve", "isn't an open issue of a writer's that the factory hasn't taken, so it can't be handed over from here"},
		{99, "plan", "isn't an open issue of a writer's that the factory hasn't taken, so it can't be planned from here"},
	} {
		if w := post(tc.n, tc.verb); w.Code != 400 || !strings.Contains(w.Body.String(), tc.says) {
			t.Errorf("/invariant %s on #%d: %d %q, want 400 and %q", tc.verb, tc.n, w.Code, strings.TrimSpace(w.Body.String()), tc.says)
		}
	}
	if len(f.taken()) != 0 {
		t.Fatalf("a refused command was posted: %q", f.taken())
	}
	// The command that fits is posted as the owner, and the issue is offered
	// no more.
	for _, tc := range []struct {
		n    int
		verb string
	}{{31, "solve"}, {32, "solve"}, {33, "solve"}, {30, "plan"}} {
		if w := post(tc.n, tc.verb); w.Code != 200 {
			t.Fatalf("/invariant %s on #%d: %d %s", tc.verb, tc.n, w.Code, w.Body.String())
		}
		if _, still := list()[tc.n]; still {
			t.Errorf("/act still lists #%d once it's handed over", tc.n)
		}
	}
	got := f.taken()
	if want := []string{"o/r#31 /invariant solve", "o/r#32 /invariant solve", "o/r#33 /invariant solve", "o/r#30 /invariant plan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("posted %q, want %q", got, want)
	}
	if left := offers(); !reflect.DeepEqual(left, map[int]offer{34: want[34], 35: want[35]}) {
		t.Errorf("what's left is %+v, want the two that wait", left)
	}
}

func TestWaitsLine900(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"its own line", "Kind: plumbing\nWaits: for #884, which changes the same check.\n", "for #884, which changes the same check."},
		{"any case, and spaces", "  WAITS :  a decision  \n", "a decision"},
		{"the first of two", "Waits: one\nWaits: two\n", "one"},
		{"none", "Kind: plumbing\n\nIt waits for nothing.\n", ""},
		{"a line that only starts with the word", "Waitress: no\n", ""},
		{"inside a fenced block", "```\nWaits: a quotation\n```\n", ""},
		{"after a fenced block", "```\nWaits: a quotation\n```\nWaits: the real one\n", "the real one"},
		{"an empty reason", "Waits:\n", ""},
	} {
		if got := waitsLine(tc.body); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
