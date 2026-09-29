package formalize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// issuesDraft is a plan of issues as a planning agent leaves it in
// issues.json: a plumbing issue, then a modeled one that names its project.
const issuesDraft = `{
  "name": "a decision graph on the dashboard",
  "summary": "Serve the graph, then prove its layout settles.",
  "issues": [
    {"title": "Serve the decision graph as JSON", "body": "The dashboard serves every decision and its edges at /api/graph.json.\n\nKind: plumbing"},
    {"title": "Prove how the graph's layout settles", "body": "The layout moves each node toward its neighbors until nothing moves.\n\nProject: examples/09-graph-layout"}
  ]
}`

// prdPlan is the plan in issuesDraft.
func prdPlan() *IssuePlan {
	issues := []PlannedIssue{
		{Title: "Serve the decision graph as JSON", Body: "The dashboard serves every decision and its edges at /api/graph.json.\n\nKind: plumbing"},
		{Title: "Prove how the graph's layout settles", Body: "The layout moves each node toward its neighbors until nothing moves.\n\nProject: examples/09-graph-layout"},
	}
	return &IssuePlan{Name: "a decision graph on the dashboard", Summary: "Serve the graph, then prove its layout settles.", Issues: issues}
}

// prdRequest is an issue a writer asked to plan, with the base branch
// exported for the planner to read. Its PRD is in the repository.
func prdRequest(t *testing.T) Request {
	t.Helper()
	root := t.TempDir()
	for name, text := range map[string]string{"README.md": "# repo\n", "docs/PRD.md": "# PRD\n\nDraw the decision graph on the dashboard.\n"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Request{Repo: "o/r", Issue: 40, Title: "Draw the decision graph", Body: "The PRD is docs/PRD.md.", Author: "gitdek", PRD: root}
}

// prdAgent is a planning agent that leaves the files in draft, by name, in
// its workspace. It keeps the job it was given, and notes which of the
// request's and the repository's files it found there.
type prdAgent struct {
	draft map[string]string
	job   synth.Job
	found []string
}

func (a *prdAgent) Name() string { return "scripted" }

func (a *prdAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	a.job = job
	for _, f := range []string{"request.md", "repo/README.md", "repo/docs/PRD.md"} {
		if _, err := os.Stat(filepath.Join(job.Workspace, filepath.FromSlash(f))); err == nil {
			a.found = append(a.found, f)
		}
	}
	for name, text := range a.draft {
		if err := os.WriteFile(filepath.Join(job.Workspace, name), []byte(text), 0o644); err != nil {
			return synth.Usage{}, err
		}
	}
	return synth.Usage{CostUSD: 0.20}, nil
}

// A request with a PRD gets a plan of issues instead of statements. The
// agent reads the request and the repository, the PRD with it, checks its
// plan with invariant mcp -issues, and leaves issues.json. The formalizer
// reads the plan back, checks it, and pins it by its hash.
func TestAPRDIsDraftedAsAPlanOfIssues(t *testing.T) {
	agent := &prdAgent{draft: map[string]string{"issues.json": issuesDraft}}
	f := Formalizer{Backend: agent, CheckRuns: 2, Timeout: time.Minute}
	r, err := f.Formalize(context.Background(), prdRequest(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r.Problem != "" || r.Proposal == nil || r.Proposal.IssuePlan == nil {
		t.Fatalf("the draft should be a plan of issues: %+v", r)
	}
	p, got, want := r.Proposal, r.Proposal.IssuePlan, prdPlan()
	if got.Name != want.Name || got.Summary != want.Summary || len(got.Issues) != len(want.Issues) {
		t.Fatalf("the plan read back is %+v; want %+v", got, want)
	}
	for i, w := range want.Issues {
		if got.Issues[i].Title != w.Title || got.Issues[i].Body != w.Body {
			t.Errorf("issue %d is %+v; want %+v", i+1, got.Issues[i], w)
		}
	}
	if p.Hash != want.Hash() || !p.Ratifiable() || p.Plan != nil || len(p.Statements) != 0 {
		t.Errorf("the proposal should be the plan of issues alone, pinned by its hash %s: %+v", want.Hash(), p)
	}
	if held := strings.Join(agent.found, " "); held != "request.md repo/README.md repo/docs/PRD.md" {
		t.Errorf("the agent's workspace held %q; want the request and the repository, PRD and all", held)
	}
	for _, s := range []string{"issues.json", "Project:", "Kind: plumbing", "10"} {
		if !strings.Contains(agent.job.Prompt, s) {
			t.Errorf("the planning agent's prompt doesn't mention %q", s)
		}
	}
	if s := agent.job.GateServer; len(s) < 3 || s[1] != "mcp" || !slices.Contains(s, "-issues") || !slices.Contains(agent.job.Tools, "check") {
		t.Errorf("the planning agent should get invariant mcp -issues's check tool: server %v, tools %v", s, agent.job.Tools)
	}
}

// A PRD's draft asks what it can't settle, with forks in proposal.json, as
// any draft does. A plan that breaks the rules comes back as a problem, with
// nothing to ratify.
func TestAPRDsDraftAsksOrFailsAsADraftDoes(t *testing.T) {
	forks := `{"forks": [{"id": "F1", "question": "Does the graph show superseded decisions?", "options": [{"id": "A", "says": "Yes, greyed out."}, {"id": "B", "says": "No, only the ones in force."}]}]}`
	f := Formalizer{Backend: &prdAgent{draft: map[string]string{"proposal.json": forks}}, CheckRuns: 2, Timeout: time.Minute}
	r, err := f.Formalize(context.Background(), prdRequest(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r.Problem != "" || r.Proposal == nil || len(r.Proposal.Forks) != 1 || r.Proposal.IssuePlan != nil || r.Proposal.Ratifiable() {
		t.Fatalf("a draft with a fork should ask it: %+v", r)
	}

	var issues []string
	for i := 1; i <= MaxIssues+1; i++ {
		issues = append(issues, fmt.Sprintf(`{"title": "Step %d", "body": "More plumbing.\n\nKind: plumbing"}`, i))
	}
	tooMany := `{"name": "too much", "summary": "One issue too many.", "issues": [` + strings.Join(issues, ", ") + `]}`
	f.Backend = &prdAgent{draft: map[string]string{"issues.json": tooMany}}
	r, err = f.Formalize(context.Background(), prdRequest(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r.Problem == "" || (r.Proposal != nil && r.Proposal.Ratifiable()) {
		t.Fatalf("a plan of %d issues should come back as a problem, with nothing to ratify: %+v", MaxIssues+1, r)
	}
}

// A plan has a name, a summary, and one to 10 issues. Each has a title and
// a complete body that marks it either modeled, with a Project: line naming
// a directory in the repository, or plumbing, with a Kind: plumbing line and
// no code to check, and never both. No body carries an /invariant command:
// the factory takes each issue itself.
func TestAPlanOfIssuesMustHoldTogether(t *testing.T) {
	if err := prdPlan().Validate(); err != nil {
		t.Fatalf("a plumbing issue and a modeled one: %v", err)
	}
	more := func(p *IssuePlan, n int) {
		for len(p.Issues) < n {
			p.Issues = append(p.Issues, PlannedIssue{Title: fmt.Sprintf("Step %d", len(p.Issues)+1), Body: "More plumbing.\n\nKind: plumbing"})
		}
	}
	full := prdPlan()
	more(full, MaxIssues)
	if err := full.Validate(); err != nil || MaxIssues != 10 {
		t.Fatalf("a plan of %d issues: %v", MaxIssues, err)
	}
	for _, tc := range []struct {
		name string
		edit func(p *IssuePlan)
		want string
	}{
		{"no name", func(p *IssuePlan) { p.Name = "" }, ""},
		{"no summary", func(p *IssuePlan) { p.Summary = " " }, ""},
		{"no issues", func(p *IssuePlan) { p.Issues = nil }, ""},
		{"eleven issues", func(p *IssuePlan) { more(p, MaxIssues+1) }, "10"},
		{"an issue with no title", func(p *IssuePlan) { p.Issues[0].Title = " " }, ""},
		{"an issue with no body", func(p *IssuePlan) { p.Issues[1].Body = "" }, ""},
		{"an issue neither modeled nor plumbing", func(p *IssuePlan) { p.Issues[0].Body = "The dashboard serves the graph." }, ""},
		{"an issue both modeled and plumbing", func(p *IssuePlan) { p.Issues[1].Body += "\nKind: plumbing" }, ""},
		{"a project outside the repository", func(p *IssuePlan) { p.Issues[1].Body = "A layout.\n\nProject: ../elsewhere" }, ""},
		{"a project in CI's own directory", func(p *IssuePlan) { p.Issues[1].Body = "A workflow.\n\nProject: .github/workflows" }, ""},
		{"plumbing that names code to check", func(p *IssuePlan) { p.Issues[0].Body += "\nCode: src/lib" }, ""},
		{"a body with a command", func(p *IssuePlan) { p.Issues[0].Body += "\n\n/invariant solve" }, ""},
	} {
		p := prdPlan()
		tc.edit(p)
		if err := p.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want an error that says %q", tc.name, err, tc.want)
		}
	}
}

// A plan's hash covers everything a person ratifies: its name, its summary,
// and each issue's title and body, in order.
func TestAPlanOfIssuesHashesWhatIsRatified(t *testing.T) {
	hash := prdPlan().Hash()
	if !strings.HasPrefix(hash, "sha256:") || len(hash) != len("sha256:")+64 || prdPlan().Hash() != hash {
		t.Fatalf("hash %q isn't one stable sha256", hash)
	}
	for _, tc := range []struct {
		what string
		edit func(p *IssuePlan)
	}{
		{"its name", func(p *IssuePlan) { p.Name = "a graph" }},
		{"its summary", func(p *IssuePlan) { p.Summary = "Draw the graph." }},
		{"an issue's title", func(p *IssuePlan) { p.Issues[0].Title = "Serve the graph" }},
		{"an issue's body", func(p *IssuePlan) { p.Issues[1].Body += "\nIt settles in a second." }},
		{"the issues' order", func(p *IssuePlan) { p.Issues[0], p.Issues[1] = p.Issues[1], p.Issues[0] }},
	} {
		p := prdPlan()
		tc.edit(p)
		if p.Hash() == hash {
			t.Errorf("changing %s leaves the plan's hash the same", tc.what)
		}
	}
}
