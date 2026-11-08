package factory

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
	"github.com/gitdek/invariant/internal/synth"
)

// These tests replace agents, GitHub and container operations, not the
// formalizer, builder, native adapters, collectors, scope or publication.
// Go runs a real local test process with an empty dependency graph. Python
// and Node processes emit their normal private lifecycle protocols from the
// staged production value; the real host collectors interpret those bytes.
// Real-framework producer correctness remains covered by the existing offline
// suites. No factory result or native acceptance adapter is replaced here.
type fixture742 struct {
	t                        *testing.T
	r                        *rig
	clone                    Clone
	hub                      *hub742
	config                   repository.Config
	base                     string
	plan                     *plumbing.Plan
	draft                    string
	product, old, fixed      string
	runner                   *containers742
	planner                  *planner742
	build                    *build742
	writer, reviewer         *agent742
	extra                    map[string]string
	remove                   []string
	poison, repair, fallback bool
	beforeReview             func()
}

func hash742(s string) string {
	v := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(v[:])
}
func json742(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func write742(t *testing.T, dir, file, s string)    { t.Helper(); repair281Write(t, dir, file, s) }
func read742(t *testing.T, dir, file string) string { t.Helper(); return repair281Read(t, dir, file) }

// Workflows reads the requested committed tree, including when the caller
// supplies a full SHA. It cannot accidentally find a candidate-only gate.
type hub742 struct {
	*draftGitHub
	t            *testing.T
	beforeCreate func()
	mergeHeads   []string
	creates      int
}

func (g *hub742) Workflows(_ context.Context, ref string) (map[string]string, error) {
	ref = strings.TrimPrefix(ref, "origin/")
	files := strings.Fields(git(g.t, g.origin, "ls-tree", "-r", "--name-only", ref, "--", ".github/workflows"))
	out := map[string]string{}
	for _, f := range files {
		out[f] = git(g.t, g.origin, "show", ref+":"+f) + "\n"
	}
	return out, nil
}
func (*hub742) MergeCommits(context.Context) (bool, error) { return true, nil }
func (g *hub742) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	g.creates++
	if g.beforeCreate != nil {
		f := g.beforeCreate
		g.beforeCreate = nil
		f()
	}
	return g.draftGitHub.CreatePullRequest(ctx, pr)
}
func (g *hub742) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.mergeHeads = append(g.mergeHeads, sha)
	return g.draftGitHub.Merge(ctx, n, sha, method)
}

func new742(t *testing.T, framework string, monorepo bool, goCache string) *fixture742 {
	t.Helper()
	r := newRig(t)
	x := &fixture742{t: t, r: r, clone: r.f.Repo.(Clone), extra: map[string]string{}}
	x.hub = &hub742{draftGitHub: &draftGitHub{fakeGitHub: r.gh}, t: t}
	r.f.Repository = "fixture/configured"
	// Watch hides concrete client methods behind this exact wrapper. Both
	// successful flows and gate refusals must work through that boundary.
	r.f.GitHub = leasedGitHub{x.hub, r.f}
	r.f.Language = "python" // A configured TypeScript or mixed request must not be forced to Go.
	language, dir, file, selector, source := "python", "worker", "test_value.py", "test_value.py::test_value", "import product\ndef test_value():\n    assert product.VALUE == 7\n"
	x.product, x.old, x.fixed = "worker/product.py", "VALUE = 0\n", "VALUE = 7\n"
	if framework == "go" {
		language, dir, file, selector, source = "go", "api", "value_accept_test.go", "TestValue", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value != 7 { t.Fatal(Value) } }\n"
		x.product, x.old, x.fixed = "api/product.go", "package fixture\n\nconst Value = 0\n", "package fixture\n\nconst Value = 7\n"
	}
	if framework == "vitest" || framework == "jest" {
		language, dir, file, selector = "typescript", "web", "value.test.ts", "value becomes seven [one].+"
		pkg := framework
		if framework == "jest" {
			pkg = "@jest/globals"
		}
		source = "import {test, expect} from '" + pkg + "';\nimport {value} from './product';\ntest('" + selector + "', () => { expect(value).toBe(7); });\n"
		x.product, x.old, x.fixed = "web/product.ts", "export const value = 0;\n", "export const value = 7;\n"
	}
	part := part742(t, x.clone.Dir, language, dir, framework)
	x.config = repository.Config{Version: 1, Parts: []repository.Part{part}, AgentRules: []string{"AGENTS.md"}, CurrentState: []string{"STATE.md"}, TrustedPaths: []string{"policy/"}, SharedPaths: []repository.SharedPath{{Path: "policy/", Parts: []string{dir}}, {Path: ".github/", Parts: []string{dir}}}}
	if monorepo {
		otherLang, otherDir, otherFramework := "typescript", "web", "vitest"
		if language == "typescript" {
			otherLang, otherDir, otherFramework = "go", "api", "go"
		}
		x.config.Parts = append(x.config.Parts, part742(t, x.clone.Dir, otherLang, otherDir, otherFramework))
		if otherLang == "go" {
			write742(t, x.clone.Dir, "api/product.go", "package fixture\nconst Value = 0\n")
		} else {
			write742(t, x.clone.Dir, "web/product.ts", "export const value = 0;\n")
		}
	}
	workflow, err := setup.Workflow(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	for f, s := range map[string]string{
		repository.Path: json742(t, x.config), setup.WorkflowPath: workflow,
		x.product: x.old, "AGENTS.md": "Captured root guidance742.\n", dir + "/AGENTS.md": "Captured nested guidance742.\n",
		"STATE.md": "Captured documentation policy742: keep named changes within the plan.\n", "policy/guard.txt": "trusted742\n",
	} {
		write742(t, x.clone.Dir, f, s)
	}
	x.commitBase()
	_, err = repository.Load(t.Context(), x.clone.Dir, x.base)
	if err != nil {
		t.Fatal(err)
	}
	x.plan = &plumbing.Plan{Name: "configured value", Summary: "Return seven in the selected part.", Files: []string{x.product}, Tests: []plumbing.Test{{Name: "seven", Part: dir, Framework: framework, File: dir + "/" + file, Selector: selector, Says: "The value is seven."}}, Sources: map[string]string{dir + "/" + file: source}}
	x.bindDraft()
	// Only compiled Go artifacts are shared by subtests. Snapshots, locked
	// dependency caches, process roots and all mutable fixture state stay private.
	x.runner = &containers742{x: x, artifacts: map[string]plumbing.DependencyArtifact{}, goCache: goCache, helperRoot: t.TempDir(), installs: map[string]int{}, opens: map[string]int{}}
	sb := plumbing.Sandbox{Timeout: 3 * time.Minute, ShardTimeout: 2 * time.Minute, Repository: &plumbing.RepositoryChecks{Dependencies: plumbing.DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: x.runner}}}
	// The runtime template deliberately has no Snapshot. The factory must
	// bind its own committed authority for this request, not a caller's
	// worktree or whatever a previous issue left on the shared runner.
	x.planner = &planner742{x: x, real: formalize.Formalizer{Backend: &agent742{x: x, role: "planner"}, Binary: "unused", CheckRuns: 4, Timeout: 4 * time.Minute, Sandbox: sb}}
	x.writer, x.reviewer = &agent742{x: x, role: "builder"}, &agent742{x: x, role: "reviewer"}
	x.build = &build742{Builder: plumbing.Builder{Backend: x.writer, Reviewer: x.reviewer, Binary: "unused", TestRuns: 6, Timeout: 4 * time.Minute, FallbackEffort: "medium", Sandbox: sb}}
	r.f.Formalizer, r.f.Plumbing = x.planner, x.build
	return x
}

func part742(t *testing.T, root, language, dir, framework string) repository.Part {
	t.Helper()
	p := repository.Part{Name: dir, Directory: dir, Language: language, Image: "fixture/" + language + "@" + hash742(language), Inputs: []string{}, Lockfiles: []string{}, Commands: map[string]repository.Command{}}
	for _, phase := range []string{"format", "lint", "build", "test"} {
		p.Commands[phase] = repository.Command{Argv: []string{"fixture-check", phase}}
	}
	switch language {
	case "go":
		p.Inputs, p.Lockfiles = []string{dir + "/go.mod"}, []string{dir + "/go.sum"}
		p.Commands["install"] = repository.Command{Argv: []string{"go", "mod", "download"}}
		p.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}", "-count=1"}}
		write742(t, root, dir+"/go.mod", "module example.invalid/fixture742\n\ngo 1.27.1\n")
		write742(t, root, dir+"/go.sum", "")
	case "python":
		p.Lockfiles = []string{dir + "/requirements.lock"}
		p.Commands["install"] = repository.Command{Argv: []string{"python3", "-m", "pip", "install", "--require-hashes", "--only-binary=:all:", "-r", "requirements.lock"}}
		p.Commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "-q", "{test}"}}
		write742(t, root, dir+"/requirements.lock", "pytest==8.3.5 --hash=sha256:"+strings.Repeat("a", 64)+"\n")
	case "typescript":
		p.Inputs, p.Lockfiles = []string{dir + "/package.json"}, []string{dir + "/package-lock.json"}
		p.Commands["install"] = repository.Command{Argv: []string{"npm", "ci"}}
		argv := []string{"vitest", "run", "-t", "{test}"}
		if framework == "jest" {
			argv = []string{"jest", "--runInBand", "-t", "{test}"}
		}
		p.Commands["test_one"] = repository.Command{Argv: argv}
		write742(t, root, dir+"/package.json", `{"name":"fixture","version":"1.0.0","dependencies":{"`+framework+`":"1.0.0"}}`)
		write742(t, root, dir+"/package-lock.json", `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"fixture","version":"1.0.0","dependencies":{"`+framework+`":"1.0.0"}},"node_modules/`+framework+`":{"version":"1.0.0","resolved":"https://registry.npmjs.org/`+framework+`/-/`+framework+`-1.0.0.tgz","integrity":"sha512-`+strings.Repeat("A", 86)+`=="}}}`)
	}
	files := map[string][]byte{}
	for _, f := range append(slices.Clone(p.Inputs), p.Lockfiles...) {
		files[f] = []byte(read742(t, root, f))
	}
	if err := plumbing.CheckDependencyInstall(p, files); err != nil {
		t.Fatal(err)
	}
	return p
}

func (x *fixture742) commitBase() {
	x.t.Helper()
	var err error
	x.base, err = x.clone.Commit(x.t.Context(), x.clone.Dir, ".", "fixture base742")
	if err != nil {
		x.t.Fatal(err)
	}
	if err = x.clone.Push(x.t.Context(), x.clone.Dir, "main"); err != nil {
		x.t.Fatal(err)
	}
	if err = x.clone.Fetch(x.t.Context()); err != nil {
		x.t.Fatal(err)
	}
}
func (x *fixture742) bindDraft() {
	x.t.Helper()
	x.draft = json742(x.t, map[string]any{"name": x.plan.Name, "summary": x.plan.Summary, "files": x.plan.Files, "tests": x.plan.Tests})
	ws := x.t.TempDir()
	write742(x.t, ws, "plan.json", x.draft)
	for f, s := range x.plan.Sources {
		write742(x.t, ws, "tests/"+f, s)
	}
	snap, err := repository.Load(x.t.Context(), x.clone.Dir, x.base)
	if err != nil {
		x.t.Fatal(err)
	}
	p, err := plumbing.ReadRepositoryDraft(x.t.Context(), ws, snap)
	if err != nil {
		x.t.Fatal(err)
	}
	x.plan = p // Expected identity only; never injected into a production runner.
}
func (x *fixture742) start() Post {
	x.t.Helper()
	x.r.gh.open(1, "gitdek", "Return seven", "Kind: plumbing\n\n/invariant solve")
	x.r.poll()
	p := x.r.expect(1, KindProposal, LabelProposal)
	if p.Marker.Proposal == nil || p.Marker.Proposal.Plan == nil || p.Marker.Proposal.Hash != x.plan.Hash() {
		x.t.Fatalf("proposal did not preserve captured identity: %+v", p.Marker)
	}
	if len(x.planner.requests) != 1 || len(x.planner.checks) != 1 || x.planner.checks[0].Problem != "" || x.planner.checks[0].Run.Passed() {
		x.t.Fatal("real planning tool did not independently fail the pinned test on base")
	}
	for _, r := range x.planner.checks[0].Run.TestResults {
		if r.Phase == "accept" && (r.Status != "failed" || r.Kind != "test") {
			x.t.Fatalf("base failure was not a completed test: %+v", r)
		}
	}
	if x.plan.Tests[0].Framework != "go" && x.planner.requests[0].Language == "go" {
		x.t.Fatal("factory forced configured request language to Go")
	}
	return p
}
func (x *fixture742) ratify(p Post) {
	x.t.Helper()
	x.r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	x.r.poll()
}
func (x *fixture742) published(held bool) Post {
	x.t.Helper()
	p := x.r.gh.last(1)
	if p.Marker.Kind != KindPR || len(x.r.gh.prs) != 1 {
		x.t.Fatalf("expected one published PR: %s", p.Comment.Body)
	}
	pr := x.r.gh.prs[p.Marker.PR]
	if pr == nil || pr.Draft != held || x.build.result == nil || !x.build.result.Built() {
		x.t.Fatalf("publication did not follow real checks and review: PR=%+v result=%+v", pr, x.build.result)
	}
	if held && !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
		x.t.Fatal("trusted draft lacks human handoff")
	}
	l, err := plumbing.ReadLock([]byte(git(x.t, x.r.origin, "show", p.Marker.Branch+":"+plumbing.LockPath(1))))
	if err != nil || l.Plan.Hash() != x.plan.Hash() || l.Plan.Execution == nil || *l.Plan.Execution != *x.plan.Execution {
		x.t.Fatalf("ratification authority changed: %+v %v", l, err)
	}
	for f, s := range x.plan.Sources {
		if got := git(x.t, x.r.origin, "show", p.Marker.Branch+":"+f); got != strings.TrimSpace(s) {
			x.t.Fatal("published acceptance differs from pinned bytes")
		}
	}
	sc, err := x.clone.Scope(x.t.Context(), "origin/main", "origin/"+p.Marker.Branch, 1)
	if err != nil || !sc.OK() || (len(sc.Trusted) > 0) != held {
		x.t.Fatalf("published scope/hold mismatch: %+v %v", sc, err)
	}
	if git(x.t, x.r.origin, "show", p.Marker.Branch+":"+x.product) != strings.TrimSpace(x.fixed) {
		x.t.Fatal("production change was not published")
	}
	if len(x.build.result.Final.TestResults) != 1 || x.build.result.Final.TestResults[0].Status != "passed" {
		x.t.Fatal("ready result lacks exact native runtime pass")
	}
	x.checkContainers()
	return p
}
func (x *fixture742) checkContainers() {
	x.t.Helper()
	if x.runner.closed != x.runner.opened {
		x.t.Fatalf("leaked check environments: %d opened %d closed", x.runner.opened, x.runner.closed)
	}
	part := x.plan.Tests[0].Part
	if x.runner.installs[part] != 1 || x.runner.inspects < 2 || x.runner.opens[part] < 4 {
		x.t.Fatalf("dependency preparation/cache reuse or fresh checks missing: installs=%v inspections=%d opens=%v", x.runner.installs, x.runner.inspects, x.runner.opens)
	}
}

// The wrappers observe real orchestration and do not set result fields.
type planner742 struct {
	x        *fixture742
	real     formalize.Formalizer
	requests []formalize.Request
	checks   []*plumbing.Checked
}

func (p *planner742) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	p.requests = append(p.requests, req)
	return p.real.Formalize(ctx, req, out)
}

type build742 struct {
	plumbing.Builder
	result *plumbing.BuildResult
	calls  int
}

func (b *build742) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	b.calls++
	r, err := b.Builder.Build(ctx, root, n, out)
	b.result = r
	return r, err
}

type agent742 struct {
	x              *fixture742
	role           string
	jobs           []synth.Job
	checksAtReview int
}

func (a *agent742) Name() string { return "fixture742 " + a.role }
func client742(job synth.Job) *plumbing.CheckClient {
	c := &plumbing.CheckClient{}
	for i := 0; i+1 < len(job.GateServer); i++ {
		switch job.GateServer[i] {
		case "-check-queue":
			c.Address = job.GateServer[i+1]
		case "-check-build":
			c.Build = job.GateServer[i+1]
		}
	}
	return c
}
func captured742(t *testing.T, job synth.Job) {
	t.Helper()
	entries, err := os.ReadDir(job.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "repository-context-") {
			text := read742(t, job.Workspace, entry.Name()+"/context.md")
			for _, want := range []string{"Captured root guidance742", "Captured nested guidance742", "Captured documentation policy742"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s lost %q", job.Workspace, want)
				}
			}
			if strings.Contains(text, "poisoned context742") {
				t.Fatal("candidate replaced captured guidance")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("configured agent has no captured repository context")
	}
}
func poisonContext742(t *testing.T, job synth.Job) {
	t.Helper()
	entries, _ := os.ReadDir(job.Workspace)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "repository-context-") {
			write742(t, job.Workspace, e.Name()+"/context.md", "poisoned context742\n")
		}
	}
}
func (a *agent742) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	x, t := a.x, a.x.t
	a.jobs = append(a.jobs, job)
	round := len(a.jobs)
	captured742(t, job)
	if a.role == "planner" {
		write742(t, job.Workspace, "plan.json", x.draft)
		for f, s := range x.plan.Sources {
			write742(t, job.Workspace, "tests/"+f, s)
		}
		write742(t, job.Workspace, "repo/"+x.product, x.fixed)
		write742(t, job.Workspace, "repo/"+repository.Path, `{"candidate":"not authority"}`)
		c := client742(job)
		if c.Address == "" || c.Build == "" {
			return synth.Usage{}, errors.New("planner has no captured check registration")
		}
		checked, err := plumbing.Check(ctx, job.Workspace, filepath.Join(job.Workspace, "repo"), plumbing.Sandbox{Checks: c})
		x.planner.checks = append(x.planner.checks, checked)
		if err != nil || checked == nil || checked.Problem != "" {
			return synth.Usage{}, fmt.Errorf("real planning check: %v %+v", err, checked)
		}
		poisonContext742(t, job)
		return synth.Usage{Summary: "Checked native plan."}, nil
	}
	if a.role == "reviewer" {
		if x.runner.opened-a.checksAtReview < 4 || x.runner.opened != x.runner.closed {
			t.Fatal("review ran without fresh completed tool and final checks")
		}
		a.checksAtReview = x.runner.opened
		if !job.ReadOnly {
			t.Fatal("review can write")
		}
		if read742(t, filepath.Join(job.Workspace, "base"), x.product) != x.old {
			t.Fatal("review lost original checkout")
		}
		for f, s := range x.plan.Sources {
			if read742(t, filepath.Join(job.Workspace, "repo"), f) != s {
				t.Fatal("review lost pinned source")
			}
		}
		if x.beforeReview != nil {
			f := x.beforeReview
			x.beforeReview = nil
			f()
		}
		if x.repair && round == 1 {
			return synth.Usage{Summary: "product: add the promised review note742.\nVERDICT: changes needed"}, nil
		}
		if x.repair && !strings.Contains(read742(t, filepath.Join(job.Workspace, "repo"), x.product), "review note742") {
			t.Fatal("review did not see repair")
		}
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	if x.fallback && round == 1 {
		poisonContext742(t, job)
		return synth.Usage{}, synth.ErrThinkingLoop
	}
	if round > 1 && job.Workspace != a.jobs[0].Workspace {
		t.Fatal("fallback/repair replaced builder workspace")
	}
	code := x.fixed
	if x.repair && len(x.reviewer.jobs) > 0 {
		if !repair281Sees(job, "review note742") {
			t.Fatal("repair lost review finding")
		}
		prefix := "// "
		if strings.HasSuffix(x.product, ".py") {
			prefix = "# "
		}
		code += prefix + "review note742\n"
		x.fixed = code
	}
	write742(t, job.Workspace, x.product, code)
	for f, s := range x.extra {
		write742(t, job.Workspace, f, s)
	}
	for _, f := range x.remove {
		if err := os.Remove(filepath.Join(job.Workspace, f)); err != nil {
			t.Fatal(err)
		}
	}
	if x.poison {
		write742(t, job.Workspace, "unplanned742.txt", "must not publish\n")
		write742(t, job.Workspace, repository.Path, `{"version":999,"parts":[]}`)
		write742(t, job.Workspace, plumbing.LockPath(1), `{"plan":{"files":["unplanned742.txt"]}}`)
		write742(t, job.Workspace, "AGENTS.md", "poisoned context742\n")
	}
	c := client742(job)
	if c.Address == "" || c.Build == "" {
		return synth.Usage{}, errors.New("builder has no captured check registration")
	}
	forged := *x.plan
	forged.Tests = nil
	forged.Sources = nil
	forged.Execution = &plumbing.Execution{Base: x.base, Digest: hash742("forged")}
	result, err := (plumbing.Sandbox{Checks: c}).Run(ctx, job.Workspace, &forged)
	if err != nil || !result.Passed() {
		return synth.Usage{}, fmt.Errorf("real builder check: %v\n%s", err, result.Summary())
	}
	poisonContext742(t, job)
	return synth.Usage{Summary: "Built and checked."}, nil
}

// Installation is the sole network-capable boundary. The in-memory immutable
// artifacts model image evidence; DependencyPreparer still admits inputs,
// computes keys, verifies the staged bytes, writes its cache and inspects reuse.
type containers742 struct {
	x                        *fixture742
	artifacts                map[string]plumbing.DependencyArtifact
	goCache, helperRoot      string
	installs, opens          map[string]int
	inspects, opened, closed int
	commands                 []string
}

func (r *containers742) Install(_ context.Context, q plumbing.DependencyRequest) (plumbing.DependencyArtifact, error) {
	if !q.Network || !q.Pull || !q.Init || q.Token == "" || len(q.Inputs) == 0 {
		return plumbing.DependencyArtifact{}, errors.New("installation lost locked input/network policy")
	}
	var part repository.Part
	for _, p := range r.x.config.Parts {
		if p.Directory == q.Directory {
			part = p
		}
	}
	if part.Name == "" || q.Image != part.Image {
		return plumbing.DependencyArtifact{}, errors.New("installation used uncaptured owner/image")
	}
	for f, h := range q.Inputs {
		data, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(f)))
		if err != nil {
			return plumbing.DependencyArtifact{}, fmt.Errorf("read installation input %s: %w", f, err)
		}
		if hash742(string(data)) != h {
			return plumbing.DependencyArtifact{}, errors.New("installation input digest differs")
		}
	}
	for _, p := range []string{r.x.product, repository.Path, "AGENTS.md"} {
		if _, e := os.Stat(filepath.Join(q.SourceDir, p)); !errors.Is(e, os.ErrNotExist) {
			return plumbing.DependencyArtifact{}, errors.New("installer received nondependency source")
		}
	}
	switch part.Language {
	case "go":
		if !slices.Equal(q.Argv, []string{"go", "mod", "download"}) || q.Env["GOTOOLCHAIN"] != "local" {
			return plumbing.DependencyArtifact{}, errors.New("unlocked Go install")
		}
	case "python":
		for _, flag := range []string{"--require-hashes", "--only-binary=:all:", "requirements.lock"} {
			if !slices.Contains(q.Argv, flag) {
				return plumbing.DependencyArtifact{}, fmt.Errorf("missing pip flag %s", flag)
			}
		}
	case "typescript":
		for _, flag := range []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"} {
			if !slices.Contains(q.Argv, flag) {
				return plumbing.DependencyArtifact{}, fmt.Errorf("missing npm flag %s", flag)
			}
		}
	}
	r.installs[part.Name]++
	a := plumbing.DependencyArtifact{Image: hash742("image" + q.Key), Base: q.Image, BaseImage: hash742("base" + q.Image), Platform: q.Platform, Key: q.Key, Inputs: q.Inputs, Complete: true}
	r.artifacts[a.Image] = a
	return a, nil
}
func (r *containers742) Inspect(_ context.Context, image, platform string) (plumbing.DependencyArtifact, error) {
	r.inspects++
	a, ok := r.artifacts[image]
	if !ok || a.Platform != platform {
		return a, errors.New("unknown prepared image")
	}
	return a, nil
}
func (*containers742) Cleanup(context.Context, string) error { return nil }
func (*containers742) Run(context.Context, plumbing.DependencyRequest) ([]byte, error) {
	return nil, errors.New("checks must use the persistent offline environment")
}
func (r *containers742) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	if q.Network || q.Pull || q.Token == "" || !q.Init {
		return nil, errors.New("checks must be offline with private resources")
	}
	var part repository.Part
	for _, p := range r.x.config.Parts {
		if p.Directory == q.Directory {
			part = p
		}
	}
	if _, ok := r.artifacts[q.Image]; !ok {
		return nil, errors.New("check skipped locked preparation")
	}
	for key, want := range map[string]string{"GOPROXY": "off", "GOTOOLCHAIN": "local", "PIP_NO_INDEX": "1", "NPM_CONFIG_OFFLINE": "true"} {
		applies := part.Language == "go" && strings.HasPrefix(key, "GO") || part.Language == "python" && strings.HasPrefix(key, "PIP") || part.Language == "typescript" && strings.HasPrefix(key, "NPM")
		if applies && q.Env[key] != want {
			return nil, fmt.Errorf("offline check lost %s", key)
		}
	}
	r.opened++
	r.opens[part.Name]++
	helpers, err := os.MkdirTemp(r.helperRoot, "check-")
	if err != nil {
		return nil, fmt.Errorf("create check helpers: %w", err)
	}
	e := &environment742{runner: r, request: q, part: part, helpers: helpers}
	if part.Language == "go" {
		exe, err := exec.LookPath("go")
		if err != nil {
			return nil, err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return nil, err
		}
		test := r.x.plan.Tests[0]
		selection, err := (plumbing.GoRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
		if err != nil {
			return nil, err
		}
		in := plumbing.RepositoryAcceptanceInvocation{Source: r.x.plan.Sources[test.File], Argv: selection.Argv, Binding: plumbing.RepositoryAcceptanceBinding{InvocationID: "local-transport-setup742", Part: test.Part, Framework: test.Framework, File: test.File, Selector: test.Selector, Package: selection.Package, SourceDigest: hash742(r.x.plan.Sources[test.File]), ExecutionDigest: r.x.plan.Execution.Digest}}
		e.local, err = plumbing.OpenLocalGoProcessTransport(ctx, plumbing.LocalGoProcessOptions{SourceDir: q.SourceDir, GoExecutable: exe, CacheDir: r.goCache, Part: part, Invocation: in})
		if err != nil {
			return nil, err
		}
	}
	return e, nil
}

type environment742 struct {
	runner  *containers742
	request plumbing.DependencyRequest
	part    repository.Part
	helpers string
	local   plumbing.RepositoryProcessTransport
	closed  bool
}

func (e *environment742) Run(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 2 && argv[0] == "fixture-check" {
		if !slices.Contains([]string{"format", "lint", "build", "test"}, argv[1]) {
			return nil, errors.New("unexpected check phase")
		}
		e.runner.commands = append(e.runner.commands, e.part.Name+"/"+argv[1])
		return nil, nil
	}
	if len(argv) == 3 && argv[0] == "node" && argv[1] == "-e" && strings.Contains(argv[2], "node_modules") {
		return []byte(strings.Repeat("b", 64)), nil
	}
	if e.local != nil {
		return e.local.Run(ctx, argv)
	}
	return nil, fmt.Errorf("unexpected container command %q", argv)
}
func (e *environment742) Close(ctx context.Context) error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.runner.closed++
	if e.local != nil {
		return e.local.Close(ctx)
	}
	return nil
}
func (e *environment742) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if e.local != nil {
		return e.local.ReadFile(ctx, name)
	}
	return os.ReadFile(filepath.Join(e.request.SourceDir, e.part.Directory, filepath.FromSlash(name)))
}
func (e *environment742) Stage(ctx context.Context, name string, b []byte) (string, error) {
	if e.local != nil {
		return e.local.Stage(ctx, name, b)
	}
	path := filepath.Join(e.helpers, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create staged helper directory: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("stage helper %s: %w", name, err)
	}
	return path, nil
}
func (e *environment742) Prepare(ctx context.Context, s plumbing.RepositoryProcessSpec) (plumbing.RepositoryPreparedProcess, error) {
	if e.local != nil {
		return e.local.Prepare(ctx, s)
	}
	var request struct {
		Binding plumbing.RepositoryAcceptanceBinding
		File    string
		Args    []string
	}
	if len(s.Argv) < 3 || json.Unmarshal([]byte(s.Argv[len(s.Argv)-1]), &request) != nil {
		return nil, errors.New("missing native process request")
	}
	x := e.runner.x
	test := x.plan.Tests[0]
	b := request.Binding
	if b.Part != test.Part || b.Framework != test.Framework || b.File != test.File || b.Selector != test.Selector || b.ExecutionDigest != x.plan.Execution.Digest || b.SourceDigest != hash742(x.plan.Sources[test.File]) || b.InvocationID == "" {
		return nil, errors.New("native process lost pinned identity/authority")
	}
	data, err := e.ReadFile(ctx, request.File)
	if err != nil || string(data) != x.plan.Sources[test.File] {
		return nil, errors.New("native process did not receive pinned source")
	}
	product, err := os.ReadFile(filepath.Join(e.request.SourceDir, filepath.FromSlash(x.product)))
	if err != nil {
		return nil, err
	}
	passed := strings.Contains(string(product), "= 7")
	if !passed && string(product) != x.old {
		return nil, fmt.Errorf("unexpected production bytes %q", product)
	}
	out := plumbing.RepositoryProcessOutput{Started: true, Exited: true, Drained: true}
	if !passed {
		out.ExitCode = 1
	}
	if b.Framework == "pytest" {
		if s.EventEnv != "INVARIANT_PYTEST_EVENTS" || s.Argv[0] != "python3" || !slices.Contains(request.Args, test.Selector) {
			return nil, errors.New("pytest invocation changed")
		}
		out.Events = pytestWire742(x.t, b, passed)
	} else {
		if s.EventEnv != "INVARIANT_NODE_EVENTS" || s.Argv[0] != "node" || request.Args[0] != b.Framework {
			return nil, errors.New("Node invocation changed")
		}
		key, err := hex.DecodeString(s.Env["INVARIANT_NODE_AUTHENTICATION"])
		if err != nil || len(key) != 32 {
			return nil, errors.New("Node process has no fresh host control key")
		}
		out.Events = nodeWire742(x.t, b, passed, key)
	}
	return process742{out: out}, nil
}

type process742 struct {
	out plumbing.RepositoryProcessOutput
}

func (p process742) Start(context.Context) (plumbing.RepositoryProcess, error) { return p, nil }
func (p process742) Close(context.Context) error                               { return nil }
func (p process742) Wait(context.Context) (plumbing.RepositoryProcessOutput, error) {
	return p.out, nil
}
func (p process742) Snapshot() plumbing.RepositoryProcessOutput { return p.out }
func pytestWire742(t *testing.T, b plumbing.RepositoryAcceptanceBinding, passed bool) []byte {
	status, actual, code := "passed", "passed", 0
	if !passed {
		status, actual, code = "failed", "assertion", 1
	}
	events := []map[string]any{
		{"Event": "begin"}, {"Event": "session_start"}, {"Event": "collection", "IDs": []string{b.Selector}},
		{"Event": "protocol_start", "Node": b.Selector}, {"Event": "phase", "Node": b.Selector, "When": "setup", "Actual": "passed", "Outcome": "passed"},
		{"Event": "body_start", "Node": b.Selector}, {"Event": "body_end", "Node": b.Selector, "Actual": actual},
		{"Event": "phase", "Node": b.Selector, "When": "call", "Actual": actual, "Outcome": status, "Reason": "observed staged value"},
		{"Event": "phase", "Node": b.Selector, "When": "teardown", "Actual": "passed", "Outcome": "passed"},
		{"Event": "protocol_end", "Node": b.Selector}, {"Event": "session_end", "Code": code}, {"Event": "finish", "Code": code},
	}
	var wire []byte
	for i, v := range events {
		v["Seq"], v["Binding"] = i+1, b
		wire = append(wire, []byte(json742(t, v)+"\n")...)
	}
	return wire
}

// The controller's signature covers the canonical event fields in the
// transport protocol. This fake is the controller, never a candidate process.
type nodeEvent742 struct {
	Binding plumbing.RepositoryAcceptanceBinding
	Actor   string
	Seq     int
	Event   string
	IDs     []string
	Outcome string
	Reason  string
	Code    int
	MAC     string `json:",omitempty"`
}

func nodeWire742(t *testing.T, b plumbing.RepositoryAcceptanceBinding, passed bool, key []byte) []byte {
	status, outcome, code := "passed", "passed", 0
	if !passed {
		status, outcome, code = "failed", "assertion", 1
	}
	events := []nodeEvent742{{Actor: "parent", Event: "begin"}, {Actor: "worker", Event: "collection", IDs: []string{b.Selector}}, {Actor: "worker", Event: "body_start"}, {Actor: "worker", Event: "body_end", Outcome: outcome, Reason: "observed staged value"}, {Actor: "worker", Event: "hooks", Outcome: "passed"}, {Actor: "worker", Event: "suite_end", Outcome: "passed"}, {Actor: "parent", Event: "session_end", Outcome: status, Code: code}, {Actor: "parent", Event: "finish", Code: code}}
	seq := map[string]int{}
	var wire []byte
	for _, v := range events {
		seq[v.Actor]++
		v.Binding, v.Seq = b, seq[v.Actor]
		if v.Actor == "parent" {
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(json742(t, v)))
			v.MAC = hex.EncodeToString(mac.Sum(nil))
		}
		wire = append(wire, []byte(json742(t, v)+"\n")...)
	}
	return wire
}

// Reuse the test process's compiled Go objects, including the standard
// library, across the two Go flows and their subtests. The local transport
// still creates fresh source, helper, home, module and process directories;
// -count=1 still executes every pinned test on its own candidate.
func compiledCache742(t *testing.T) string {
	t.Helper()
	cache := os.Getenv("GOCACHE")
	if cache == "" {
		out, err := exec.Command("go", "env", "GOCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		cache = strings.TrimSpace(string(out))
	}
	if !filepath.IsAbs(cache) {
		t.Fatalf("Go compiler cache must be an absolute path: %q", cache)
	}
	root := t.TempDir()
	if err := os.Symlink(cache, filepath.Join(root, "build")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConfiguredFactoryNativeFlow742(t *testing.T) {
	goCache := compiledCache742(t)
	for _, framework := range []string{"go", "pytest", "vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			x := new742(t, framework, false, goCache)
			p := x.start()
			x.r.gh.say(1, "gitdek", "/invariant ratify ffffffffffff")
			x.r.poll()
			if x.build.calls != 0 || len(x.r.gh.prs) != 0 {
				t.Fatal("wrong hash started a build")
			}
			x.ratify(p)
			pr := x.published(false)
			if x.build.calls != 1 || len(x.writer.jobs) != 1 || len(x.reviewer.jobs) != 1 {
				t.Fatal("unexpected build/review count")
			}
			head := x.r.gh.prs[pr.Marker.PR].Head.SHA
			x.r.gh.ci(pr.Marker.PR, "success")
			x.r.poll()
			x.r.expect(1, KindMerged, LabelMerged)
			if !slices.Equal(x.hub.mergeHeads, []string{head}) {
				t.Fatalf("merge was not pinned to checked head: %v", x.hub.mergeHeads)
			}
			if x.planner.real.Sandbox.Repository.Snapshot != nil || x.build.Builder.Sandbox.Repository.Snapshot != nil {
				t.Fatal("request authority mutated the shared runner template")
			}
		})
	}
}

func TestConfiguredFactoryMonorepo742(t *testing.T) {
	goCache := compiledCache742(t)
	for _, framework := range []string{"go", "vitest"} {
		t.Run(framework, func(t *testing.T) {
			x := new742(t, framework, true, goCache)
			p := x.start()
			x.ratify(p)
			x.published(false)
			selected := x.plan.Tests[0].Part
			for _, part := range x.config.Parts {
				if part.Name != selected && (x.runner.installs[part.Name] != 0 || x.runner.opens[part.Name] != 0) {
					t.Fatalf("untouched part prepared or checked: %s", part.Name)
				}
			}
			for _, command := range x.runner.commands {
				if !strings.HasPrefix(command, selected+"/") {
					t.Fatalf("unrelated suite ran: %s", command)
				}
			}
		})
	}
}

func TestConfiguredFactoryRevisionAndRepairs742(t *testing.T) {
	goCache := t.TempDir()
	x := new742(t, "pytest", false, goCache)
	x.repair, x.fallback = true, true
	p := x.start()
	// Compatible movement can change current guidance, but a revision/build
	// of this proposal still reads its captured guidance and native identity.
	write742(t, x.clone.Dir, "AGENTS.md", "later guidance742 must not replace capture\n")
	x.commitBase()
	x.r.gh.say(1, "gitdek", "Please keep the same behavior.\n\n/invariant revise")
	x.r.poll()
	p = x.r.expect(1, KindProposal, LabelProposal)
	if p.Marker.Proposal.Hash != x.plan.Hash() || len(x.planner.requests) != 2 || x.planner.requests[1].Previous == nil {
		t.Fatal("revision lost captured proposal")
	}
	x.ratify(p)
	x.published(false)
	if len(x.writer.jobs) != 3 || len(x.reviewer.jobs) != 2 || x.build.result.Fallback == nil {
		t.Fatalf("fallback and checked repair did not complete: builds=%d reviews=%d result=%+v", len(x.writer.jobs), len(x.reviewer.jobs), x.build.result)
	}
	if x.writer.jobs[1].Effort != "medium" {
		t.Fatal("fallback did not use configured effort")
	}
}

func TestConfiguredFactoryScopeAndTrust742(t *testing.T) {
	goCache := t.TempDir()
	for _, mode := range []string{"candidate poisoning", "out of plan head", "contract", "workflow", "root rule", "nested rule deletion", "declared trust deletion"} {
		t.Run(mode, func(t *testing.T) {
			x := new742(t, "pytest", false, goCache)
			held := false
			switch mode {
			case "candidate poisoning":
				x.poison = true
			case "contract":
				held = true
				x.plan.Files = append(x.plan.Files, repository.Path)
				c := x.config
				c.TrustedPaths = []string{}
				x.extra[repository.Path] = json742(t, c)
			case "workflow":
				held = true
				x.plan.Files = append(x.plan.Files, setup.WorkflowPath)
				x.extra[setup.WorkflowPath] = "name: candidate gate742\n"
			case "root rule":
				held = true
				x.plan.Files = append(x.plan.Files, "AGENTS.md")
				x.extra["AGENTS.md"] = "candidate rules742\n"
			case "nested rule deletion":
				held = true
				x.plan.Files = append(x.plan.Files, "worker/AGENTS.md")
				x.remove = []string{"worker/AGENTS.md"}
			case "declared trust deletion":
				held = true
				x.plan.Files = append(x.plan.Files, "policy/guard.txt")
				x.remove = []string{"policy/guard.txt"}
			}
			x.bindDraft()
			p := x.start()
			// A transient publication interruption recovers the recorded real build.
			if held {
				x.r.gh.failPRs = 1
			}
			x.r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
			if held {
				_ = x.r.f.Poll(t.Context())
				x.restart()
				x.r.poll()
			} else {
				x.r.poll()
			}
			pr := x.published(held)
			if x.build.calls != 1 {
				t.Fatal("publication recovery rebuilt a finished candidate")
			}
			if mode == "candidate poisoning" {
				paths := git(t, x.r.origin, "ls-tree", "-r", "--name-only", pr.Marker.Branch)
				if strings.Contains(paths, "unplanned742.txt") || git(t, x.r.origin, "show", pr.Marker.Branch+":"+repository.Path) != json742(t, x.config) {
					t.Fatal("candidate policy/out-of-plan edit escaped staging")
				}
			}
			if mode == "out of plan head" {
				x.changeBranch(pr.Marker.Branch, func(root string) { write742(t, root, "unplanned742.txt", "unauthorized\n") })
			}
			x.r.gh.ci(pr.Marker.PR, "success")
			x.r.poll()
			if held {
				x.r.expect(1, KindFailed, LabelHumanReview)
				x.r.gh.say(1, "gitdek", "/invariant retry")
				x.r.poll()
				x.restart()
				x.r.poll()
				if !x.r.gh.prs[pr.Marker.PR].Draft || len(x.hub.calls) != 0 || len(x.hub.mergeHeads) != 0 || x.build.calls != 1 {
					t.Fatal("trusted recovery/retry escaped hold or rebuilt")
				}
			} else if mode == "out of plan head" {
				x.r.expect(1, KindFailed, LabelHumanReview)
				if len(x.hub.mergeHeads) != 0 {
					t.Fatal("out-of-plan head merged")
				}
			} else {
				x.r.expect(1, KindMerged, LabelMerged)
			}
		})
	}
}

func (x *fixture742) restart() {
	x.t.Helper()
	old := x.r.f
	x.r.f = &Factory{Repository: old.Repository, GitHub: old.GitHub, Repo: old.Repo, Formalizer: old.Formalizer, Builder: old.Builder, Plumbing: old.Plumbing, Base: old.Base, Projects: old.Projects, Work: old.Work, Check: old.Check, Language: old.Language, Agent: old.Agent, Agents: old.Agents, Self: old.Self, Log: old.Log, Now: old.Now}
	x.r.f.GitHub = leasedGitHub{x.hub, x.r.f}
}
func (x *fixture742) changeBranch(branch string, change func(string)) {
	x.t.Helper()
	wt, err := x.clone.Worktree(x.t.Context(), "edit742", "origin/"+branch)
	if err != nil {
		x.t.Fatal(err)
	}
	defer x.clone.RemoveWorktree(context.Background(), wt)
	change(wt)
	if _, err = x.clone.Commit(x.t.Context(), wt, ".", "fixture changes branch"); err != nil {
		x.t.Fatal(err)
	}
	if err = x.clone.Push(x.t.Context(), wt, branch); err != nil {
		x.t.Fatal(err)
	}
	if err = x.clone.Fetch(x.t.Context()); err != nil {
		x.t.Fatal(err)
	}
}

type push742 struct {
	Repo
	denied bool
	calls  int
}

func (p *push742) PushCommit(ctx context.Context, code, branch string) error {
	p.calls++
	if p.denied {
		return errors.New("remote: refusing to allow a GitHub App to create or update workflow `.github/workflows/gate.yml` without `workflows` permission")
	}
	return p.Repo.PushCommit(ctx, code, branch)
}
func TestConfiguredFactoryCredentialRecovery742(t *testing.T) {
	goCache := t.TempDir()
	x := new742(t, "pytest", false, goCache)
	x.plan.Files = append(x.plan.Files, setup.WorkflowPath)
	x.extra[setup.WorkflowPath] = read742(t, x.clone.Dir, setup.WorkflowPath) + "# reviewed workflow change742\n"
	x.bindDraft()
	p := x.start()
	push := &push742{Repo: x.clone, denied: true}
	x.r.f.Repo = push
	x.r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	_ = x.r.f.Poll(t.Context())
	if push.calls != 1 || x.build.calls != 1 || x.build.result == nil || !x.build.result.Built() || len(x.r.gh.prs) != 0 {
		t.Fatal("credential refusal lost or bypassed the successful build")
	}
	post := x.r.gh.last(1)
	visible := strings.ToLower(post.Comment.Body)
	for _, word := range []string{"workflow", "permission", "credential", "retry"} {
		if !strings.Contains(visible, word) {
			t.Errorf("handoff lacks %s", word)
		}
	}
	if !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
		t.Fatal("credential refusal needs a person")
	}
	posts := len(x.r.gh.posts(1))
	x.restart()
	x.r.poll()
	x.r.poll()
	x.r.gh.say(1, "mallory", "/invariant retry")
	x.r.poll()
	if push.calls != 1 || x.build.calls != 1 || len(x.r.gh.posts(1)) != posts {
		t.Fatal("poll, recovery or reader retry repeated a refused write")
	}
	push.denied = false
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	x.r.poll()
	x.published(true)
	if push.calls != 2 || x.build.calls != 1 {
		t.Fatal("explicit credential retry must resume recorded publication, not rebuild")
	}
}

// Loading a completed build is the publication entry point even if authority
// validation refuses before a push or PR write. Counting only posts or writes
// would miss a watcher that retries this work silently on every poll.
type publication742 struct {
	Repo
	loads, pushes int
}

func (r *publication742) Load(ctx context.Context, commit, dir string) error {
	if err := r.Repo.Load(ctx, commit, dir); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err == nil {
		var result builtResult
		if json.Unmarshal(data, &result) == nil && result.Plumbing != nil {
			r.loads++
		}
	}
	return nil
}

func (r *publication742) PushCommit(ctx context.Context, code, branch string) error {
	r.pushes++
	return r.Repo.PushCommit(ctx, code, branch)
}

func (x *fixture742) publicationHeld(change string, probe *publication742) {
	t := x.t
	t.Helper()
	if x.build.calls != 1 || x.build.result == nil || !x.build.result.Built() {
		t.Fatal("publication fixture must retain one successfully checked and reviewed build")
	}
	if len(x.hub.mergeHeads) != 0 || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
		t.Fatalf("publication failed to hold %s for a person", change)
	}
	for _, pr := range x.r.gh.prs {
		if !pr.Draft {
			t.Fatal("changed base authority published ready")
		}
	}
	post := x.r.gh.last(1)
	visible := strings.ToLower(strings.Split(post.Comment.Body, "<!-- invariant:")[0])
	wantPath := repository.Path
	if strings.Contains(change, "gate") {
		wantPath = "gate"
	}
	if post.Marker.Kind == KindRatified || !strings.Contains(visible, wantPath) || !strings.Contains(visible, "/invariant retry") {
		t.Fatalf("publication hold must visibly name %s and the writer retry: %s", change, post.Comment.Body)
	}
	// Permit natural wording, but require the reason, not just a generic
	// human-review label or a mention of the repository setup documentation.
	reasons := map[string][]string{
		"contract removed": {"removed", "missing", "no longer", "not found", "absent"},
		"commands changed": {"changed", "differs", "different", "match"},
		"gate removed":     {"removed", "missing", "no longer", "not found", "no workflow", "no committed", "absent"},
		"obsolete gate":    {"obsolete", "upgrade", "capab", "check-repository"},
		"gate pin changed": {"changed", "differs", "different", "match"},
	}
	named := false
	for _, reason := range reasons[change] {
		named = named || strings.Contains(visible, reason)
	}
	if !named {
		t.Fatalf("publication hold did not explain %s: %s", change, post.Comment.Body)
	}
	loads, pushes, posts, prs := probe.loads, probe.pushes, len(x.r.gh.posts(1)), len(x.r.gh.prs)
	creates, readies := x.hub.creates, len(x.hub.calls)
	stillHeld := func() {
		t.Helper()
		if probe.loads != loads || probe.pushes != pushes || x.hub.creates != creates || len(x.hub.calls) != readies || len(x.r.gh.posts(1)) != posts || len(x.r.gh.prs) != prs || x.build.calls != 1 || len(x.hub.mergeHeads) != 0 || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
			t.Fatalf("held publication retried without a writer: result loads %d -> %d, pushes %d -> %d, posts %d -> %d, PRs %d -> %d, builds %d", loads, probe.loads, pushes, probe.pushes, posts, len(x.r.gh.posts(1)), prs, len(x.r.gh.prs), x.build.calls)
		}
	}
	x.restart()
	effects434Poll(t, x.r)
	effects434Poll(t, x.r)
	stillHeld()
	x.r.gh.say(1, "mallory", "/invariant retry")
	effects434Poll(t, x.r)
	stillHeld()
	// Repairing the base alone is not permission to publish. Restore exactly
	// the original authority, with a new compatible commit, before retrying.
	x.changeBranch("main", func(root string) {
		file := repository.Path
		if strings.Contains(change, "gate") {
			file = setup.WorkflowPath
		}
		data, err := x.clone.Show(t.Context(), x.plan.Execution.Base, file)
		if err != nil {
			t.Fatal(err)
		}
		write742(t, root, file, string(data))
	})
	x.restart()
	effects434Poll(t, x.r)
	stillHeld()
	x.r.gh.say(1, "gitdek", "/invariant retry")
	x.r.poll()
	x.r.poll()
	x.published(false)
	if probe.loads <= loads || x.build.calls != 1 {
		t.Fatal("authorized retry must resume the recorded build's publication without rebuilding")
	}
}

func TestConfiguredFactoryRevalidatesAuthority742(t *testing.T) {
	goCache := t.TempDir()
	for _, stage := range []string{"ratification", "build", "publication", "merge", "last merge refresh"} {
		for _, change := range []string{"compatible", "contract removed", "commands changed", "gate removed", "obsolete gate", "gate pin changed"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				x := new742(t, "pytest", false, goCache)
				p := x.start()
				var publication *publication742
				move := func() {
					x.changeBranch("main", func(root string) {
						switch change {
						case "compatible":
							write742(t, root, "unrelated.txt", "compatible movement742\n")
						case "contract removed":
							if err := os.Remove(filepath.Join(root, repository.Path)); err != nil {
								t.Fatal(err)
							}
						case "commands changed":
							c := x.config
							c.Parts = slices.Clone(c.Parts)
							c.Parts[0].Image = "fixture/python@" + hash742("changed authority")
							write742(t, root, repository.Path, json742(t, c))
						case "gate removed":
							if err := os.Remove(filepath.Join(root, setup.WorkflowPath)); err != nil {
								t.Fatal(err)
							}
						case "gate pin changed":
							workflow, err := setup.Workflow(strings.Repeat("b", 40))
							if err != nil {
								t.Fatal(err)
							}
							write742(t, root, setup.WorkflowPath, workflow)
						case "obsolete gate":
							write742(t, root, setup.WorkflowPath, "name: old\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo obsolete\n")
						}
					})
				}
				switch stage {
				case "ratification":
					move()
				case "build":
					x.r.f.Repo = &afterRatification742{Repo: x.clone, move: move}
				case "publication":
					publication = &publication742{Repo: x.clone}
					x.r.f.Repo = publication
					x.beforeReview = move
				}
				x.r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
				_ = x.r.f.Poll(t.Context())
				if stage == "merge" || stage == "last merge refresh" {
					pr := x.published(false)
					if stage == "merge" {
						move()
					} else {
						x.r.f.Repo = &moveBaseTrust695{Repo: x.clone, move: move}
					}
					x.r.gh.ci(pr.Marker.PR, "success")
					_ = x.r.f.Poll(t.Context())
				}
				if change == "compatible" {
					if stage == "merge" || stage == "last merge refresh" {
						x.r.expect(1, KindMerged, LabelMerged)
					} else {
						x.published(false)
					}
					return
				}
				if stage == "publication" {
					x.publicationHeld(change, publication)
					return
				}
				x.restart()
				_ = x.r.f.Poll(t.Context())
				_ = x.r.f.Poll(t.Context())
				if len(x.hub.mergeHeads) != 0 || !slices.Contains(x.r.gh.labelsOf(1), LabelHumanReview) {
					t.Fatalf("%s failed to hold changed %s for a person", stage, change)
				}
				if stage == "ratification" || stage == "build" {
					if x.build.calls != 0 {
						t.Fatal("invalid authority reached builder")
					}
				}
				before := len(x.r.gh.posts(1))
				_ = x.r.f.Poll(t.Context())
				if len(x.r.gh.posts(1)) != before {
					t.Fatal("setup hold repeated on every poll")
				}
			})
		}
	}
}

type afterRatification742 struct {
	Repo
	move func()
}

func (r *afterRatification742) Push(ctx context.Context, root, branch string) error {
	err := r.Repo.Push(ctx, root, branch)
	if err == nil && r.move != nil {
		f := r.move
		r.move = nil
		f()
	}
	return err
}

// Keep the compile-time contract of the process fixture explicit.
var _ plumbing.RepositoryProcessTransport = (*environment742)(nil)
var _ plumbing.DependencyRunner = (*containers742)(nil)
