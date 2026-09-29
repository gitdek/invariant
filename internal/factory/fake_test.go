package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/verify"
)

// fakeGitHub is an in-memory repository on GitHub. Pull requests point at
// branches in a real git origin, so their heads are real commits.
type fakeGitHub struct {
	t        *testing.T
	origin   string
	me       string
	issues   map[int]*github.Issue
	comments map[int][]github.Comment
	events   map[int][]github.Event
	perms    map[string]string
	prs      map[int]*github.PullRequest
	checks   map[string][]github.CheckRun
	refused  map[int64]bool // check runs whose jobs GitHub never started
	// noActions is an App without the Actions permission: it can't read jobs.
	noActions bool
	merged    []int
	deleted   []string
	bodies    map[int]string
	nextID    int64
	nextPR    int
	failPRs   int // pull requests to refuse, as GitHub can
}

func newFakeGitHub(t *testing.T, origin string) *fakeGitHub {
	return &fakeGitHub{t: t, origin: origin, me: "gitdek", issues: map[int]*github.Issue{}, comments: map[int][]github.Comment{},
		events: map[int][]github.Event{}, perms: map[string]string{"gitdek": "admin", "mallory": "read"},
		prs: map[int]*github.PullRequest{}, checks: map[string][]github.CheckRun{}, refused: map[int64]bool{}, bodies: map[int]string{}, nextID: 1000, nextPR: 100}
}

func (g *fakeGitHub) open(n int, by, title, body string, labels ...string) {
	issue := &github.Issue{Number: n, Title: title, Body: body, User: github.User{Login: by}, State: "open",
		URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n), CreatedAt: "2026-09-25T10:00:00Z"}
	for _, l := range labels {
		issue.Labels = append(issue.Labels, github.Label{Name: l})
		g.events[n] = append(g.events[n], github.Event{Event: "labeled", Actor: github.User{Login: by}, Label: &github.Label{Name: l}})
	}
	g.issues[n] = issue
}

// say adds a comment. Logins ending in [bot] are bots.
func (g *fakeGitHub) say(issue int, by, body string) github.Comment {
	g.nextID++
	kind := "User"
	if strings.HasSuffix(by, "[bot]") {
		kind = "Bot"
	}
	c := github.Comment{ID: g.nextID, Body: body, User: github.User{Login: by, Type: kind},
		URL:      fmt.Sprintf("https://github.com/o/r/issues/%d#issuecomment-%d", issue, g.nextID),
		IssueURL: fmt.Sprintf("https://api.github.com/repos/o/r/issues/%d", issue), CreatedAt: "2026-09-25T11:00:00Z"}
	g.comments[issue] = append(g.comments[issue], c)
	return c
}

// posts are the factory's comments on an issue.
func (g *fakeGitHub) posts(issue int) []Post {
	var out []Post
	for _, c := range g.comments[issue] {
		if m, ok := DecodeMarker(c.Body); ok {
			out = append(out, Post{c, m})
		}
	}
	return out
}

func (g *fakeGitHub) last(issue int) Post {
	ps := g.posts(issue)
	if len(ps) == 0 {
		g.t.Fatalf("#%d has no factory posts", issue)
	}
	return ps[len(ps)-1]
}

func (g *fakeGitHub) labelsOf(issue int) []string {
	var out []string
	for _, l := range g.issues[issue].Labels {
		out = append(out, l.Name)
	}
	return out
}

func (g *fakeGitHub) OpenIssues(context.Context) ([]github.Issue, error) {
	var out []github.Issue
	for _, i := range g.issues {
		if i.State == "open" {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (g *fakeGitHub) Comments(_ context.Context, n int) ([]github.Comment, error) {
	return append([]github.Comment(nil), g.comments[n]...), nil
}

func (g *fakeGitHub) Events(_ context.Context, n int) ([]github.Event, error) {
	return g.events[n], nil
}

func (g *fakeGitHub) Permission(_ context.Context, login string) (string, error) {
	if p, ok := g.perms[login]; ok {
		return p, nil
	}
	return "none", nil
}

func (g *fakeGitHub) PostComment(_ context.Context, n int, body string) (github.Comment, error) {
	return g.say(n, g.me, body), nil
}

func (g *fakeGitHub) EnsureLabel(context.Context, string, string, string) error { return nil }

func (g *fakeGitHub) AddLabels(_ context.Context, n int, labels ...string) error {
	if i, ok := g.issues[n]; ok {
		for _, l := range labels {
			if !i.HasLabel(l) {
				i.Labels = append(i.Labels, github.Label{Name: l})
			}
		}
	}
	return nil
}

func (g *fakeGitHub) RemoveLabel(_ context.Context, n int, label string) error {
	if i, ok := g.issues[n]; ok {
		var keep []github.Label
		for _, l := range i.Labels {
			if l.Name != label {
				keep = append(keep, l)
			}
		}
		i.Labels = keep
	}
	return nil
}

func (g *fakeGitHub) CreatePullRequest(_ context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	if g.failPRs > 0 {
		g.failPRs--
		return github.PullRequest{}, errors.New("GitHub is having a bad day")
	}
	g.nextPR++
	out := &github.PullRequest{Number: g.nextPR, State: "open", Draft: pr.Draft, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", g.nextPR),
		Head: github.Ref{Ref: pr.Head, SHA: g.head(pr.Head)}, Base: github.Ref{Ref: pr.Base}}
	g.prs[out.Number] = out
	g.bodies[out.Number] = pr.Body
	return *out, nil
}

func (g *fakeGitHub) OpenPullRequest(_ context.Context, branch string) (github.PullRequest, bool, error) {
	for _, pr := range g.prs {
		if pr.State == "open" && pr.Head.Ref == branch {
			pr.Head.SHA = g.head(branch)
			return *pr, true, nil
		}
	}
	return github.PullRequest{}, false, nil
}

func (g *fakeGitHub) head(branch string) string {
	out, err := exec.Command("git", "-C", g.origin, "rev-parse", "refs/heads/"+branch).Output()
	if err != nil {
		g.t.Fatalf("branch %s isn't in origin: %v", branch, err)
	}
	return strings.TrimSpace(string(out))
}

func (g *fakeGitHub) PullRequest(_ context.Context, n int) (github.PullRequest, error) {
	pr, ok := g.prs[n]
	if !ok {
		return github.PullRequest{}, github.ErrNotFound
	}
	if pr.State == "open" {
		pr.Head.SHA = g.head(pr.Head.Ref)
	}
	return *pr, nil
}

func (g *fakeGitHub) CheckRuns(_ context.Context, sha, name string) ([]github.CheckRun, error) {
	var out []github.CheckRun
	for _, r := range g.checks[sha] {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out, nil
}

// ci records a completed invariant/gate run on a pull request's head.
func (g *fakeGitHub) ci(pr int, conclusion string) {
	sha := g.head(g.prs[pr].Head.Ref)
	g.nextID++
	g.checks[sha] = append(g.checks[sha], github.CheckRun{ID: g.nextID, Name: "invariant/gate", Status: "completed",
		Conclusion: conclusion, URL: fmt.Sprintf("https://github.com/o/r/actions/runs/%d", g.nextID), HeadSHA: sha})
}

// refuse records a gate run on a pull request's head that GitHub failed
// without starting, as it does when the account's Actions minutes run out.
func (g *fakeGitHub) refuse(pr int) {
	g.ci(pr, "failure")
	g.refused[g.nextID] = true
}

func (g *fakeGitHub) Job(_ context.Context, id int64) (github.Job, error) {
	if g.noActions {
		return github.Job{}, fmt.Errorf("GET actions/jobs/%d: %w", id, github.ErrNoPermission)
	}
	job := github.Job{ID: id, Name: "invariant/gate", Status: "completed"}
	if !g.refused[id] {
		job.RunnerID = 7
		job.Steps = append(job.Steps, struct {
			Name string `json:"name"`
		}{Name: "Verify every project"})
	}
	return job, nil
}

func (g *fakeGitHub) Merge(_ context.Context, n int, sha, method string) (string, error) {
	pr := g.prs[n]
	if pr.Head.SHA != sha {
		return "", fmt.Errorf("head moved")
	}
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "abc123merge"
	g.merged = append(g.merged, n)
	return "abc123merge", nil
}

func (g *fakeGitHub) DeleteBranch(_ context.Context, branch string) error {
	g.deleted = append(g.deleted, branch)
	return nil
}

// scriptedFormalizer answers with forks until the forks are decided, then
// with a real, pinned proposal.
type scriptedFormalizer struct {
	t        *testing.T
	requests []formalize.Request
	forks    []formalize.Fork
	fail     string
	// amend drafts an amendment of the current project, when the issue
	// names one.
	amend func(c *formalize.Current) *formalize.Proposal
	// revised is what a draft says people's later comments changed.
	revised []formalize.Revision
	// plan is what a plumbing issue's draft plans (D-0105).
	plan *plumbing.Plan
}

const bufferModule = `---- MODULE BoundedBuffer ----
EXTENDS Naturals, Sequences
CONSTANTS Cap, Msgs
VARIABLES buf

vars == <<buf>>

WithinCap == Len(buf) <= Cap

TypeOK == buf \in Seq(Msgs)

CanFill == Len(buf) = Cap

Init == buf = <<>>

Put(m) == Len(buf) < Cap /\ buf' = Append(buf, m)

Take == Len(buf) > 0 /\ buf' = Tail(buf)

Next == (\E m \in Msgs : Put(m)) \/ Take

PutWhenFull == \E m \in Msgs : buf' = Append(buf, m)

Spec == Init /\ [][Next]_vars
====
`

func bufferProposal(t *testing.T) *formalize.Proposal {
	p := &formalize.Proposal{ModuleText: bufferModule, Draft: formalize.Draft{
		Name: "bounded buffer", Slug: "bounded-buffer", Module: "BoundedBuffer", Package: "buffer",
		Bounds: map[string]string{"Cap": "2", "Msgs": "{m1, m2}"},
		Statements: []project.Statement{
			{Name: "Spec", Kind: project.Spec, Says: "The system starts in Init, and every step is a Next step."},
			{Name: "WithinCap", Kind: project.Invariant, Says: "The buffer never holds more than its capacity."},
			{Name: "TypeOK", Kind: project.Invariant, Says: "The buffer always holds a sequence of messages."},
			{Name: "CanFill", Kind: project.Witness, Says: "The buffer can fill up."},
			{Name: "PutWhenFull", Kind: project.Bug, Says: "A producer adds a message to a full buffer.", Expect: "WithinCap"},
		},
	}}
	if err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *scriptedFormalizer) Formalize(_ context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	s.requests = append(s.requests, req)
	if s.fail != "" {
		return &formalize.Result{Problem: s.fail}, nil
	}
	if req.Plumbing != "" {
		if s.plan == nil {
			return &formalize.Result{Problem: "no plan scripted"}, nil
		}
		if _, err := os.Stat(filepath.Join(req.Plumbing, "README.md")); err != nil {
			s.t.Errorf("the planner should get the base branch to read: %v", err)
		}
		plan := *s.plan
		if err := plan.Validate(); err != nil {
			s.t.Fatal(err)
		}
		return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name, Language: "go"}, Plan: &plan, Hash: plan.Hash()},
			Usage: synth.Usage{CostUSD: 0.10}}, nil
	}
	if len(req.Answers) < len(s.forks) {
		return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Forks: s.forks}}, Usage: synth.Usage{CostUSD: 0.10}}, nil
	}
	report := &verify.Report{ModelOnly: true, Passed: true, Design: verify.Design{Passed: true, Outcome: "passed", DistinctStates: 7, Depth: 3},
		Witnesses: []verify.Witness{{Name: "CanFill", Reached: true, Steps: 2}}, Bugs: []verify.Bug{{Name: "PutWhenFull", Caught: true}}}
	if c := req.Current; c != nil && s.amend != nil {
		p := s.amend(c)
		if err := p.Amend(c); err != nil {
			return &formalize.Result{Problem: err.Error()}, nil
		}
		return &formalize.Result{Proposal: p, Report: report, Changes: formalize.Diff(c, p)}, nil
	}
	p := bufferProposal(s.t)
	p.Language, p.Revised = req.Language, s.revised
	return &formalize.Result{Proposal: p, Report: report, Usage: synth.Usage{CostUSD: 0.10}}, nil
}

// fakeBuilder writes a Go package into the ratified project, the way
// synthesis would, and reports the gate result it's told to.
type fakeBuilder struct {
	pass    bool
	stop    bool   // the agent stops before it finishes
	crash   bool   // the factory itself stops partway through the build
	review  string // what a second agent says of the driver, if it read one
	account string // what the building agent says when it's done
	runs    []bool // the agent's gate runs, when not just the final one
	built   []string
	amended []bool
}

func (b *fakeBuilder) Build(_ context.Context, dir, out string, amend bool) (*synth.Result, error) {
	b.built = append(b.built, dir)
	b.amended = append(b.amended, amend)
	if b.stop {
		return nil, errors.New("the agent stopped before it finished")
	}
	if b.crash {
		panic("the factory stopped partway through the build")
	}
	result := filepath.Join(out, "result")
	if err := copyResult(dir, result); err != nil {
		return nil, err
	}
	code := "// +gobra\n\npackage buffer\n\nconst Cap = 2\n"
	if amend {
		existing, err := os.ReadFile(filepath.Join(dir, "buffer", "buffer.go"))
		if err != nil {
			return nil, fmt.Errorf("an amendment must start from the existing code: %w", err)
		}
		code = string(existing) + "\n// Amended.\n"
		os.Remove(filepath.Join(result, "buffer", "old.go")) // the agent dropped a file
	}
	if err := os.MkdirAll(filepath.Join(result, "buffer"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(result, "buffer", "buffer.go"), []byte(code), 0o644); err != nil {
		return nil, err
	}
	final := &verify.Report{Project: "bounded buffer", Passed: b.pass, Assurance: "proved",
		Design: verify.Design{Passed: true, Outcome: "passed", DistinctStates: 7, Depth: 3}, Build: verify.Build{Passed: b.pass}}
	res := &synth.Result{Project: "bounded buffer", Final: final, Usage: synth.Usage{Backend: "fake", Turns: 3, CostUSD: 0.25, Summary: b.account},
		GateRuns: []synth.GateRun{{Run: 1, Passed: b.pass}}}
	if b.runs != nil {
		res.GateRuns = nil
		for i, passed := range b.runs {
			res.GateRuns = append(res.GateRuns, synth.GateRun{Run: i + 1, Passed: passed})
		}
	}
	if b.review != "" && b.pass {
		res.Review = &synth.Review{Usage: synth.Usage{CostUSD: 0.05}, Text: b.review}
	}
	return res, nil
}

// gitRepos makes an origin with one project already under examples, and the
// factory's clone of it.
func gitRepos(t *testing.T) (origin string, clone Clone) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "--quiet", "--bare", "-b", "main", origin)
	git(t, root, "init", "--quiet", "-b", "main", seed)
	for name, text := range map[string]string{
		"README.md": "# repo\n",
		"examples/02-twophase-commit/.invariant/invariant.json": `{"name": "two-phase commit"}`,
		"examples/02-twophase-commit/.invariant/ratified.lock":  `{"decision": "D-0027"}`,
	} {
		path := filepath.Join(seed, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(text), 0o644)
	}
	git(t, seed, "add", "-A")
	git(t, seed, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed")
	git(t, seed, "push", "--quiet", origin, "main")
	clone = Clone{Dir: filepath.Join(root, "clone"), Remote: origin, Name: "Joseph", Email: "7275925+gitdek@users.noreply.github.com"}
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return origin, clone
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (g *fakeGitHub) prBody(n int) string { return g.bodies[n] }
