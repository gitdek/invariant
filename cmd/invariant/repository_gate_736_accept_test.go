package main

// CI entry-point contract for #736:
//
// checkRepositoryCmd(ctx, args) is dispatched by main. Its testable command
// body is runRepositoryCheck(ctx, args, plumbing.Sandbox, factory.Comments,
// io.Writer) int. The supplied sandbox injects only execution boundaries and
// budgets: the command must capture its own repository authority. A nil
// Comments uses github.Client for -repo. All diagnostics go to the writer.
// Production uses the normal Docker preparer and default native adapters.
//
// Flags are -C DIR (default .), -base REF (default HEAD, full checks),
// -issue N (positive for a factory PR; omitted for ordinary CI), -repo OWNER/NAME,
// and at most one candidate commit argument (default HEAD). An explicit base
// enables ordinary PR ownership selection; an empty/unreliable selection uses
// all parts. An invalid authority ref is an error, not a fallback. Factory
// validation never falls back to ordinary checks. Git inputs are resolved and
// privately staged from commits, not borrowed from a mutable working tree.
//
// checkRepositorySetup(ctx, setup.GitHub, repo, checkout, base string) error
// is the watcher's preflight after clone/fetch and before agent/tool startup;
// it loads committed configuration and calls setup.CheckRepository. Existing
// setup.Check and its modeled-only requirements remain compatible.
// These fixtures fake external dependency/framework processes, never config
// loading, ownership, plan hashing, scope, ratification or sandbox execution.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

const gateRepo736 = "fixture/repository"
const gateIssue736 = 736

func gateHash736(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func gateWrite736(t *testing.T, root, file, body string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gateGit736(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	var diagnostics bytes.Buffer
	c.Stderr = &diagnostics
	b, err := c.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s\n%s", args, err, b, diagnostics.String())
	}
	return strings.TrimSpace(string(b))
}

func gateCommit736(t *testing.T, root string) string {
	t.Helper()
	gateGit736(t, root, "add", "--all")
	gateGit736(t, root, "commit", "-qm", "fixture", "--allow-empty")
	return gateGit736(t, root, "rev-parse", "HEAD")
}

func gateJSON736(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type gateFixture736 struct {
	t          *testing.T
	root, base string
	config     repository.Config
	lock       *plumbing.Lock
	comments   *gateComments736
}

func gateFixtureFor736(t *testing.T, frameworks ...string) *gateFixture736 {
	t.Helper()
	f := &gateFixture736{t: t, root: t.TempDir(), comments: &gateComments736{permission: "write"}}
	f.config = repository.Config{Version: 1, Parts: []repository.Part{}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	gateGit736(t, f.root, "init", "-q", "-b", "main")
	for _, framework := range frameworks {
		dir := map[string]string{"go": "api", "pytest": "worker", "vitest": "web", "jest": "admin"}[framework]
		language := map[string]string{"go": "go", "pytest": "python", "vitest": "typescript", "jest": "typescript"}[framework]
		p := repository.Part{Name: dir, Directory: dir, Language: language, Image: "registry.example/" + language + "@" + gateHash736(language), Inputs: []string{}, Lockfiles: []string{}, Commands: map[string]repository.Command{}}
		for _, phase := range []string{"format", "lint", "build"} {
			p.Commands[phase] = repository.Command{Argv: []string{"check-" + language, phase, "literal argument; $(never-a-shell)"}}
		}
		switch framework {
		case "go":
			p.Inputs, p.Lockfiles = []string{dir + "/go.mod"}, []string{dir + "/go.sum"}
			p.Commands["install"] = repository.Command{Argv: []string{"go", "mod", "download"}}
			p.Commands["test"] = repository.Command{Argv: []string{"go", "test", "./..."}}
			p.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}"}}
			gateWrite736(t, f.root, dir+"/go.mod", "module example.invalid/app\n\ngo 1.27.1\n\nrequire example.invalid/dep v1.2.3\n")
			gateWrite736(t, f.root, dir+"/go.sum", "example.invalid/dep v1.2.3 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nexample.invalid/dep v1.2.3/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=\n")
			gateWrite736(t, f.root, dir+"/main.go", "package fixture\nconst Value = 0\n")
		case "pytest":
			p.Lockfiles = []string{dir + "/requirements.lock"}
			p.Commands["install"] = repository.Command{Argv: []string{"python3", "-m", "pip", "install", "--require-hashes", "--only-binary=:all:", "-r", "requirements.lock"}}
			p.Commands["test"] = repository.Command{Argv: []string{"python3", "-m", "pytest"}}
			p.Commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "{test}"}}
			gateWrite736(t, f.root, dir+"/requirements.lock", "packaging==24.2 --hash=sha256:"+strings.Repeat("c", 64)+"\n")
			gateWrite736(t, f.root, dir+"/main.py", "VALUE = 0\n")
		default:
			p.Inputs, p.Lockfiles = []string{dir + "/package.json"}, []string{dir + "/package-lock.json"}
			p.Commands["install"] = repository.Command{Argv: []string{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}}
			p.Commands["test"] = repository.Command{Argv: []string{framework, "run"}}
			p.Commands["test_one"] = repository.Command{Argv: []string{framework, "run", "-t", "{test}"}}
			if framework == "jest" {
				p.Commands["test"] = repository.Command{Argv: []string{"jest", "--runInBand"}}
				p.Commands["test_one"] = repository.Command{Argv: []string{"jest", "--runInBand", "--testNamePattern", "{test}"}}
			}
			gateWrite736(t, f.root, dir+"/package.json", `{"name":"fixture","version":"1.0.0","dependencies":{"is-number":"7.0.0"}}`)
			gateWrite736(t, f.root, dir+"/package-lock.json", `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"fixture","version":"1.0.0","dependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":"sha512-`+strings.Repeat("A", 86)+`=="}}}`)
			gateWrite736(t, f.root, dir+"/main.ts", "export const value = 0;\n")
		}
		f.config.Parts = append(f.config.Parts, p)
	}
	gateWrite736(t, f.root, "README.md", "fixture\n")
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	f.base = gateCommit736(t, f.root)
	return f
}

func (f *gateFixture736) native(parts []string, extra ...string) {
	f.t.Helper()
	p := plumbing.Plan{Name: "CI fixture", Summary: "Require native results for the committed candidate.", Files: append([]string{}, extra...), Tests: []plumbing.Test{}, Sources: map[string]string{}}
	for _, part := range f.config.Parts {
		if !slices.Contains(parts, part.Name) {
			continue
		}
		framework, file, selector, source, production, fixed := "go", "ready_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { if Value != 7 { t.Fatal(Value) } }\n", "main.go", "package fixture\nconst Value = 7\n"
		if part.Language == "python" {
			framework, file, selector, source, production, fixed = "pytest", "test_ready.py", "test_ready.py::test_ready", "import main\ndef test_ready():\n    assert main.VALUE == 7\n", "main.py", "VALUE = 7\n"
		}
		if part.Language == "typescript" {
			framework = part.Commands["test"].Argv[0]
			module := "vitest"
			if framework == "jest" {
				module = "@jest/globals"
			}
			file, selector, source, production, fixed = "ready.test.ts", "ready [one].+", "import {test,expect} from '"+module+"';\nimport {value} from './main';\ntest('ready [one].+',()=>expect(value).toBe(7));\n", "main.ts", "export const value = 7;\n"
		}
		file = part.Directory + "/" + file
		p.Files = append(p.Files, part.Directory+"/"+production)
		p.Tests = append(p.Tests, plumbing.Test{Name: "ready", File: file, Says: "The value is seven.", Part: part.Name, Framework: framework, Selector: selector})
		p.Sources[file] = source
		gateWrite736(f.t, f.root, part.Directory+"/"+production, fixed)
	}
	draft := f.t.TempDir()
	gateWrite736(f.t, draft, "plan.json", gateJSON736(f.t, struct {
		Name    string          `json:"name"`
		Summary string          `json:"summary"`
		Files   []string        `json:"files"`
		Tests   []plumbing.Test `json:"tests"`
	}{p.Name, p.Summary, p.Files, p.Tests}))
	for file, src := range p.Sources {
		gateWrite736(f.t, draft, "tests/"+file, src)
	}
	snapshot, err := repository.Load(f.t.Context(), f.root, f.base)
	if err != nil {
		f.t.Fatal(err)
	}
	bound, err := plumbing.ReadRepositoryDraft(f.t.Context(), draft, snapshot)
	if err != nil {
		f.t.Fatal(err)
	}
	f.lock = &plumbing.Lock{Plan: *bound, Ratified: plumbing.Ratification{By: "writer", Issue: gateIssue736, Comment: "https://github.com/" + gateRepo736 + "/issues/736#issuecomment-17", Proposal: bound.Hash()}}
	if err := f.lock.Write(f.root); err != nil {
		f.t.Fatal(err)
	}
	f.comments.comment = github.Comment{ID: 17, Body: "/invariant ratify " + bound.Hash(), User: github.User{Login: "writer", Type: "User"}, URL: f.lock.Ratified.Comment, IssueURL: "https://api.github.com/repos/" + gateRepo736 + "/issues/736"}
	gateCommit736(f.t, f.root)
}

type gateComments736 struct {
	comment    github.Comment
	permission string
	calls      int
	fail       bool
}

func (g *gateComments736) Comment(context.Context, int64) (github.Comment, error) {
	g.calls++
	if g.fail {
		return github.Comment{}, errors.New("ratification unavailable")
	}
	return g.comment, nil
}
func (g *gateComments736) Permission(context.Context, string) (string, error) {
	return g.permission, nil
}

type gateRunner736 struct {
	t            *testing.T
	config       repository.Config
	plan         *plumbing.Plan
	images       map[string]plumbing.DependencyArtifact
	sources      map[string]string
	events       []string
	commands     []string
	accepts      []plumbing.RepositoryAcceptanceInvocation
	open, closed int
	failure      string
	acceptStatus string
	cancel       context.CancelFunc
}

func (f *gateFixture736) runner() *gateRunner736 {
	r := &gateRunner736{t: f.t, config: f.config, images: map[string]plumbing.DependencyArtifact{}, sources: map[string]string{}, acceptStatus: "passed"}
	if f.lock != nil {
		r.plan = &f.lock.Plan
	}
	for _, part := range f.config.Parts {
		file := part.Directory + "/main" + map[string]string{"go": ".go", "python": ".py", "typescript": ".ts"}[part.Language]
		if data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(file))); err == nil {
			r.sources[file] = string(data)
		}
	}
	return r
}

func (r *gateRunner736) Install(ctx context.Context, q plumbing.DependencyRequest) (plumbing.DependencyArtifact, error) {
	r.events = append(r.events, "install:"+q.Directory)
	if _, ok := ctx.Deadline(); !ok {
		r.t.Error("dependency preparation lost its deadline")
	}
	if !q.Network || !q.Pull || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 {
		r.t.Errorf("bad install isolation: %+v", q)
	}
	for name, digest := range q.Inputs {
		b, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(name)))
		if err != nil || gateHash736(string(b)) != digest {
			r.t.Fatalf("preparation did not stage captured input %s: %v", name, err)
		}
	}
	if r.failure == "install" {
		return plumbing.DependencyArtifact{}, errors.New("install failed736")
	}
	a := plumbing.DependencyArtifact{Image: gateHash736(q.Key), Base: q.Image, BaseImage: gateHash736("base"), Platform: q.Platform, Key: q.Key, Inputs: q.Inputs, Complete: true}
	r.images[a.Image] = a
	return a, nil
}
func (r *gateRunner736) Inspect(_ context.Context, image, platform string) (plumbing.DependencyArtifact, error) {
	a, ok := r.images[image]
	if !ok || a.Platform != platform {
		return a, errors.New("unknown dependency image")
	}
	r.events = append(r.events, "inspect:"+image)
	return a, nil
}
func (r *gateRunner736) Cleanup(ctx context.Context, _ string) error { return ctx.Err() }
func (*gateRunner736) Run(context.Context, plumbing.DependencyRequest) ([]byte, error) {
	return nil, errors.New("CI bypassed persistent sandbox checks")
}
func (r *gateRunner736) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	if _, ok := r.images[q.Image]; !ok {
		return nil, errors.New("checks preceded verified dependency preparation")
	}
	if q.Network || q.Pull || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 {
		return nil, errors.New("checks lost offline isolation")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("checks lost deadline")
	}
	for file, want := range r.sources {
		if strings.HasPrefix(file, q.Directory+"/") {
			got, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(file)))
			if err != nil || string(got) != want {
				return nil, fmt.Errorf("CI did not stage the candidate's %s: %v", file, err)
			}
		}
	}
	r.events = append(r.events, "open:"+q.Directory)
	r.open++
	return &gateEnvironment736{r: r, q: q}, nil
}

type gateEnvironment736 struct {
	r *gateRunner736
	q plumbing.DependencyRequest
}

func (e *gateEnvironment736) ReadFile(_ context.Context, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(e.q.SourceDir, e.q.Directory, filepath.FromSlash(name)))
}
func (e *gateEnvironment736) Run(ctx context.Context, argv []string) ([]byte, error) {
	r := e.r
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("command lost deadline")
	}
	// Only the Go adapter asks the external toolchain for package discovery
	// and compilation. These are fake process replies, not a fake adapter.
	if len(argv) > 2 && argv[0] == "go" && argv[1] == "list" {
		var files []string
		if r.plan != nil {
			for _, test := range r.plan.Tests {
				if test.Part == e.q.Directory {
					files = append(files, filepath.Base(test.File))
				}
			}
		}
		return []byte(gateJSON736(r.t, map[string]any{"Dir": "/invariant/work/" + e.q.Directory, "Name": "fixture", "ImportPath": "example.invalid/app", "Module": map[string]any{"Path": "example.invalid/app", "Main": true}, "TestGoFiles": files})), nil
	}
	if len(argv) > 2 && argv[0] == "go" && argv[1] == "test" && slices.Contains(argv, "-c") {
		return nil, nil
	}
	phase := ""
	for _, p := range r.config.Parts {
		if p.Directory == e.q.Directory {
			for _, name := range []string{"format", "lint", "build", "test"} {
				if slices.Equal(argv, p.Commands[name].Argv) {
					phase = name
				}
			}
		}
	}
	if phase == "" {
		return nil, fmt.Errorf("candidate supplied undeclared argv: %q", argv)
	}
	r.commands = append(r.commands, e.q.Directory+"/"+phase)
	r.events = append(r.events, e.q.Directory+"/"+phase)
	if r.failure == "cancel" && phase == "lint" {
		r.cancel()
		return []byte("cancel detail736"), ctx.Err()
	}
	if r.failure == phase {
		return []byte(phase + " detail736"), errors.New("exit status 7")
	}
	return []byte(phase + " completed736\n"), nil
}
func (e *gateEnvironment736) RunAcceptance(_ context.Context, in plumbing.RepositoryAcceptanceInvocation) (plumbing.RepositoryAcceptanceEvidence, []byte, error) {
	r := e.r
	if r.plan == nil {
		return plumbing.RepositoryAcceptanceEvidence{}, nil, errors.New("ordinary checks invented acceptance")
	}
	var test plumbing.Test
	var part repository.Part
	for _, v := range r.plan.Tests {
		if v.File == in.Binding.File {
			test = v
		}
	}
	for _, v := range r.config.Parts {
		if v.Name == test.Part {
			part = v
		}
	}
	adapters := map[string]plumbing.RepositoryAcceptanceAdapter{"go": plumbing.GoRepositoryAcceptanceAdapter{}, "pytest": plumbing.PytestRepositoryAcceptanceAdapter{}, "vitest": plumbing.VitestRepositoryAcceptanceAdapter{}, "jest": plumbing.JestRepositoryAcceptanceAdapter{}}
	want, err := adapters[test.Framework].Select(context.Background(), part, test)
	if err != nil || !slices.Equal(want.Argv, in.Argv) || in.Binding.Selector != test.Selector || in.Binding.Part != test.Part || in.Binding.Framework != test.Framework || in.Source != r.plan.Sources[test.File] || in.Binding.SourceDigest != gateHash736(in.Source) || in.Binding.ExecutionDigest != r.plan.Execution.Digest || in.Binding.InvocationID == "" {
		return plumbing.RepositoryAcceptanceEvidence{}, nil, fmt.Errorf("lost native identity or adapter arguments: %+v %v", in, err)
	}
	if b, err := os.ReadFile(filepath.Join(e.q.SourceDir, filepath.FromSlash(test.File))); err != nil || string(b) != in.Source {
		return plumbing.RepositoryAcceptanceEvidence{}, nil, errors.New("pinned source not staged")
	}
	if e.q.Network || e.q.Pull {
		return plumbing.RepositoryAcceptanceEvidence{}, nil, errors.New("native test was online")
	}
	r.accepts = append(r.accepts, in)
	r.events = append(r.events, "accept:"+test.Part)
	evidence := plumbing.RepositoryAcceptanceEvidence{Complete: true, Records: []plumbing.RepositoryAcceptanceRecord{
		{Binding: in.Binding, Kind: "selection"}, {Binding: in.Binding, Kind: "test", Status: r.acceptStatus, Reason: "framework detail736"},
	}}
	if r.acceptStatus == "failed" {
		evidence.ExitCode = 1
	}
	if r.failure == "missing evidence" {
		evidence.Records = nil
	}
	return evidence, []byte("framework diagnostic736\n"), nil
}
func (e *gateEnvironment736) Close(ctx context.Context) error {
	e.r.closed++
	d, ok := ctx.Deadline()
	if !ok || ctx.Err() != nil || time.Until(d) > plumbing.SandboxCleanupAllowance+time.Second {
		e.r.t.Error("cleanup needs a fresh bounded allowance")
	}
	if e.r.failure == "cleanup" {
		return errors.New("cleanup detail736")
	}
	return nil
}

func (f *gateFixture736) run(ctx context.Context, r *gateRunner736, args ...string) (int, string) {
	f.t.Helper()
	sb := plumbing.Sandbox{Timeout: time.Minute, ShardTimeout: 30 * time.Second, Repository: &plumbing.RepositoryChecks{Dependencies: plumbing.DependencyPreparer{CacheDir: f.t.TempDir(), Platform: "linux/amd64", Runner: r}}}
	var out bytes.Buffer
	code := runRepositoryCheck(ctx, append([]string{"-C", f.root, "-repo", gateRepo736}, args...), sb, f.comments, &out)
	if r.open != r.closed {
		f.t.Errorf("command returned before closing environments: %d opened, %d closed", r.open, r.closed)
	}
	return code, out.String()
}

func (f *gateFixture736) factoryArgs() []string {
	return []string{"-base", f.base, "-issue", "736", "HEAD"}
}

func TestRepositoryGate736FactoryLanguages(t *testing.T) {
	for _, frameworks := range [][]string{{"go"}, {"pytest"}, {"vitest"}, {"jest"}, {"go", "pytest", "vitest", "jest"}} {
		t.Run(strings.Join(frameworks, "+"), func(t *testing.T) {
			f := gateFixtureFor736(t, frameworks...)
			var parts []string
			for _, p := range f.config.Parts {
				parts = append(parts, p.Name)
			}
			f.native(parts)
			r := f.runner()
			code, out := f.run(t.Context(), r, f.factoryArgs()...)
			if code != 0 {
				t.Fatalf("valid native CI failed %d:\n%s", code, out)
			}
			if f.comments.calls == 0 || len(r.accepts) != len(parts) {
				t.Fatalf("ratification or native execution bypassed: comments=%d tests=%d", f.comments.calls, len(r.accepts))
			}
			if len(r.commands) != 4*len(parts) {
				t.Fatalf("declared phases missing: %v", r.commands)
			}
			for _, part := range parts {
				if slices.Index(r.events, "install:"+part) < 0 || slices.Index(r.events, "install:"+part) > slices.Index(r.events, "accept:"+part) {
					t.Fatal(r.events)
				}
				var phases []string
				for _, command := range r.commands {
					if strings.HasPrefix(command, part+"/") {
						phases = append(phases, command)
					}
				}
				if !slices.Equal(phases, []string{part + "/format", part + "/lint", part + "/build", part + "/test"}) || slices.Index(r.events, part+"/test") > slices.Index(r.events, "accept:"+part) {
					t.Fatalf("lost declared phase order: %v", r.events)
				}
			}
		})
	}
	for _, part := range []string{"api", "worker", "web", "admin"} {
		t.Run("confined-"+part, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest", "vitest", "jest")
			f.native([]string{part})
			regression := map[string]string{"api": "extra_test.go", "worker": "test_extra.py", "web": "extra.test.ts", "admin": "extra.test.ts"}[part]
			body := map[string]string{"api": "package fixture\nimport \"testing\"\nfunc TestExtra(t *testing.T) {}\n", "worker": "def test_extra(): assert True\n", "web": "test('extra',()=>{});\n", "admin": "test('extra',()=>{});\n"}[part]
			gateWrite736(t, f.root, part+"/"+regression, body)
			gateCommit736(t, f.root)
			r := f.runner()
			code, out := f.run(t.Context(), r, f.factoryArgs()...)
			if code != 0 || len(r.commands) != 4 || len(r.accepts) != 1 {
				t.Fatalf("confined part or admitted regression failed: %d %v\n%s", code, r.commands, out)
			}
			for _, event := range r.events {
				if strings.HasPrefix(event, "install:") && event != "install:"+part {
					t.Fatalf("untouched part installed: %v", r.events)
				}
			}
		})
	}
}

func TestRepositoryGate736Failures(t *testing.T) {
	for _, failure := range []string{"format", "lint", "build", "test", "native", "native skip", "native unfinished", "missing evidence", "install", "cleanup", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest")
			f.native([]string{"api", "worker"})
			r := f.runner()
			r.failure = failure
			if failure == "native" {
				r.acceptStatus = "failed"
			}
			if failure == "native skip" {
				r.acceptStatus = "skipped"
			}
			if failure == "native unfinished" {
				r.acceptStatus = "running"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.cancel = cancel
			code, out := f.run(ctx, r, f.factoryArgs()...)
			if code == 0 {
				t.Fatalf("%s failure passed CI:\n%s", failure, out)
			}
			if !strings.Contains(out, "api") {
				t.Fatalf("failed part missing:\n%s", out)
			}
			if !strings.HasPrefix(failure, "native") && failure != "missing evidence" && failure != "cleanup" && !strings.Contains(out, "not_run") {
				t.Fatalf("unfinished phases missing:\n%s", out)
			}
			if slices.Contains([]string{"format", "lint", "build", "test"}, failure) && (!strings.Contains(out, failure) || !strings.Contains(out, "detail736")) {
				t.Fatalf("failure details lost:\n%s", out)
			}
		})
	}
	f := gateFixtureFor736(t, "pytest")
	f.config.Parts[0].Commands["format"] = repository.Command{NotApplicable: "Formatting is checked by lint736."}
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	f.base = gateCommit736(t, f.root)
	f.native([]string{"worker"})
	r := f.runner()
	r.failure = "lint"
	code, out := f.run(t.Context(), r, f.factoryArgs()...)
	if code == 0 || !strings.Contains(out, "skipped") || !strings.Contains(out, "Formatting is checked by lint736.") || !strings.Contains(out, "not_run") {
		t.Fatalf("skipped and unstarted reasons lost: %d\n%s", code, out)
	}
}

func TestRepositoryGate736Authority(t *testing.T) {
	for _, mode := range []string{"missing plan", "missing ratification", "wrong hash", "wrong comment", "wrong issue", "reader ratified", "unavailable ratification", "altered test", "removed test", "linked test", "out of plan", "untouched part regression", "delete unplanned", "bad execution", "base moved", "delete config", "weaken config"} {
		t.Run(mode, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest")
			f.native([]string{"api"})
			switch mode {
			case "missing plan":
				if err := os.Remove(filepath.Join(f.root, plumbing.LockPath(736))); err != nil {
					t.Fatal(err)
				}
			case "missing ratification":
				f.lock.Ratified = plumbing.Ratification{}
			case "wrong hash":
				f.lock.Ratified.Proposal = gateHash736("other")
			case "wrong comment":
				f.comments.comment.Body = "/invariant ratify " + gateHash736("other")
			case "wrong issue":
				f.lock.Ratified.Issue = 737
			case "reader ratified":
				f.comments.permission = "read"
			case "unavailable ratification":
				f.comments.fail = true
			case "altered test":
				gateWrite736(t, f.root, f.lock.Plan.Tests[0].File, "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
			case "removed test":
				if err := os.Remove(filepath.Join(f.root, f.lock.Plan.Tests[0].File)); err != nil {
					t.Fatal(err)
				}
			case "linked test":
				name := filepath.Join(f.root, f.lock.Plan.Tests[0].File)
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("main.go", name); err != nil {
					t.Fatal(err)
				}
			case "out of plan":
				gateWrite736(t, f.root, "worker/main.py", "VALUE = 7\n")
			case "untouched part regression":
				gateWrite736(t, f.root, "worker/test_unplanned.py", "def test_unplanned(): assert True\n")
			case "delete unplanned":
				if err := os.Remove(filepath.Join(f.root, "worker/main.py")); err != nil {
					t.Fatal(err)
				}
			case "bad execution":
				f.lock.Plan.Execution.Digest = gateHash736("forged authority")
				f.lock.Ratified.Proposal = f.lock.Plan.Hash()
				f.comments.comment.Body = "/invariant ratify " + f.lock.Plan.Hash()
			case "delete config":
				if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
					t.Fatal(err)
				}
			case "weaken config":
				gateWrite736(t, f.root, repository.Path, `{"version":1,"parts":[]}`)
			case "base moved":
				head := gateGit736(t, f.root, "rev-parse", "HEAD")
				gateGit736(t, f.root, "checkout", "--detach", f.base)
				f.config.Parts[0].Commands["lint"] = repository.Command{NotApplicable: "new base policy"}
				gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
				f.base = gateCommit736(t, f.root)
				gateGit736(t, f.root, "checkout", "--detach", head)
			}
			if slices.Contains([]string{"missing ratification", "wrong hash", "wrong issue", "bad execution"}, mode) {
				gateWrite736(t, f.root, plumbing.LockPath(736), gateJSON736(t, f.lock))
			}
			gateCommit736(t, f.root)
			r := f.runner()
			code, out := f.run(t.Context(), r, f.factoryArgs()...)
			if code == 0 {
				t.Fatalf("%s bypassed CI:\n%s", mode, out)
			}
			if len(r.events) != 0 {
				t.Fatalf("invalid authority reached external execution: %v\n%s", r.events, out)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatal("refusal has no explanation")
			}
		})
	}
	// Compatible movement of the current base does not invalidate the captured
	// execution digest or substitute its file inventory for the original one.
	f := gateFixtureFor736(t, "go", "pytest")
	f.native([]string{"api"})
	head := gateGit736(t, f.root, "rev-parse", "HEAD")
	gateGit736(t, f.root, "checkout", "--detach", f.base)
	gateWrite736(t, f.root, "README.md", "unrelated current-base change\n")
	f.base = gateCommit736(t, f.root)
	gateGit736(t, f.root, "checkout", "--detach", head)
	r := f.runner()
	if code, out := f.run(t.Context(), r, f.factoryArgs()...); code != 0 || len(r.accepts) != 1 {
		t.Fatalf("compatible current-base movement refused: %d\n%s", code, out)
	}
	// A named configuration change is permitted for a person to merge, but
	// still executes the original commands and selects all original parts.
	for _, deleted := range []bool{false, true} {
		f := gateFixtureFor736(t, "go", "pytest")
		f.native([]string{"api"}, repository.Path)
		if deleted {
			if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
				t.Fatal(err)
			}
		} else {
			gateWrite736(t, f.root, repository.Path, `{"version":1,"parts":[]}`)
		}
		gateCommit736(t, f.root)
		r := f.runner()
		r.failure = "lint"
		code, out := f.run(t.Context(), r, f.factoryArgs()...)
		if code == 0 || !slices.Contains(r.commands, "api/lint") || !strings.Contains(out, "worker") {
			t.Fatalf("planned config edit replaced base authority: %d %v\n%s", code, r.commands, out)
		}
	}
}

func TestRepositoryGate736OrdinaryCI(t *testing.T) {
	for _, mode := range []string{"selected", "push", "empty diff", "unowned", "shared", "dirty config", "deleted config"} {
		t.Run(mode, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest", "vitest", "jest")
			if mode == "shared" {
				f.config.SharedPaths = []repository.SharedPath{{Path: "shared/settings.txt", Parts: []string{"api", "web"}}}
				gateWrite736(t, f.root, "shared/settings.txt", "base\n")
				gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
				f.base = gateCommit736(t, f.root)
			}
			if mode != "empty diff" {
				gateWrite736(t, f.root, "worker/main.py", "VALUE = 7\n")
			}
			if mode == "unowned" {
				gateWrite736(t, f.root, "README.md", "changed\n")
			}
			if mode == "shared" {
				gateWrite736(t, f.root, "shared/settings.txt", "changed\n")
			}
			if mode == "deleted config" {
				if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
					t.Fatal(err)
				}
			}
			gateCommit736(t, f.root)
			if mode == "dirty config" {
				gateWrite736(t, f.root, repository.Path, `{"version":999}`)
			}
			args := []string{"-base", f.base}
			if mode == "push" {
				args = nil
			}
			r := f.runner()
			code, out := f.run(t.Context(), r, args...)
			if code != 0 {
				t.Fatalf("ordinary %s failed: %d\n%s", mode, code, out)
			}
			want := []string{"worker"}
			if mode == "push" || mode == "empty diff" || mode == "unowned" || mode == "deleted config" {
				want = []string{"admin", "api", "web", "worker"}
			}
			if mode == "shared" {
				want = []string{"api", "web", "worker"}
			}
			var got []string
			for _, event := range r.events {
				if strings.HasPrefix(event, "install:") {
					got = append(got, strings.TrimPrefix(event, "install:"))
				}
			}
			slices.Sort(got)
			slices.Sort(want)
			if !reflect.DeepEqual(got, want) || len(r.commands) != 4*len(want) || len(r.accepts) != 0 || f.comments.calls != 0 {
				t.Fatalf("wrong ordinary selection: got %v want %v commands=%v\n%s", got, want, r.commands, out)
			}
		})
	}
	for _, mode := range []string{"invalid contract", "missing contract", "invalid base"} {
		f := gateFixtureFor736(t, "go")
		if mode == "invalid contract" {
			gateWrite736(t, f.root, repository.Path, `{"version":999}`)
			f.base = gateCommit736(t, f.root)
		}
		if mode == "missing contract" {
			if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
				t.Fatal(err)
			}
			f.base = gateCommit736(t, f.root)
		}
		if mode == "invalid base" {
			f.base = "missing-authority-ref736"
		}
		r := f.runner()
		code, out := f.run(t.Context(), r, "-base", f.base)
		if (code == 0) != (mode == "missing contract") || len(r.events) != 0 {
			t.Fatalf("%s: %d %v\n%s", mode, code, r.events, out)
		}
	}
}

type gateSetupGitHub736 struct{ workflow string }

func (g gateSetupGitHub736) Workflows(context.Context, string) (map[string]string, error) {
	return map[string]string{setup.WorkflowPath: g.workflow}, nil
}
func (gateSetupGitHub736) MergeCommits(context.Context) (bool, error) { return true, nil }

func TestRepositoryGate736CLIAndUpgrade(t *testing.T) {
	// Reexecute this test binary to enter main without letting os.Exit end
	// the acceptance runner. This proves dispatch, including the no-contract
	// path, without building another copy of Invariant or contacting Docker.
	if os.Getenv("INVARIANT_GATE736_CHILD") == "1" {
		os.Args = []string{"invariant", "check-repository", "-C", os.Getenv("INVARIANT_GATE736_ROOT")}
		main()
		return
	}
	f := gateFixtureFor736(t, "go")
	if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
		t.Fatal(err)
	}
	gateCommit736(t, f.root)
	child := exec.Command(os.Args[0], "-test.run=^TestRepositoryGate736CLIAndUpgrade$")
	child.Env = append(os.Environ(), "INVARIANT_GATE736_CHILD=1", "INVARIANT_GATE736_ROOT="+f.root)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("modeled-only check-repository command is unavailable: %v\n%s", err, out)
	}
	if !strings.Contains(usage, "check-repository") {
		t.Fatal("CLI help omits repository checks")
	}
	old := "name: gate\non: [push, pull_request]\njobs:\n  gate:\n    name: invariant/gate\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo old gate\n"
	f = gateFixtureFor736(t, "go")
	if err := checkRepositorySetup(t.Context(), gateSetupGitHub736{old}, gateRepo736, f.root, f.base); err == nil || !strings.Contains(strings.ToLower(err.Error()), "upgrade") {
		t.Fatalf("CLI preflight accepted obsolete configured gate: %v", err)
	}
	gateWrite736(t, f.root, repository.Path, "malformed working tree\n")
	if err := checkRepositorySetup(t.Context(), gateSetupGitHub736{old}, gateRepo736, f.root, f.base); err == nil || !strings.Contains(strings.ToLower(err.Error()), "upgrade") {
		t.Fatalf("preflight trusted working-tree contract: %v", err)
	}
	if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
		t.Fatal(err)
	}
	f.base = gateCommit736(t, f.root)
	if err := checkRepositorySetup(t.Context(), gateSetupGitHub736{old}, gateRepo736, f.root, f.base); err != nil {
		t.Fatalf("modeled-only setup regressed: %v", err)
	}
	gateWrite736(t, f.root, repository.Path, `{"version":999}`)
	f.base = gateCommit736(t, f.root)
	if err := checkRepositorySetup(t.Context(), gateSetupGitHub736{old}, gateRepo736, f.root, f.base); err == nil {
		t.Fatal("malformed committed configuration became modeled-only setup")
	}
	root := t.TempDir()
	gateWrite736(t, root, "go.mod", "module example.invalid/app\n\ngo 1.27.1\n")
	code, out := initPrints(t, "-repo", gateRepo736, "-invariant", initPin, root)
	if code != 0 {
		t.Fatal(out)
	}
	for _, want := range []string{"check-repository", "upgrade", "review", "commit", "external plumbing", "disabled"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("init guidance lacks %q:\n%s", want, out)
		}
	}
	gateWrite736(t, root, setup.WorkflowPath, old)
	code, out = initPrints(t, "-repo", gateRepo736, "-invariant", initPin, root)
	if code == 0 {
		t.Fatal("init silently replaced customized gate")
	}
	b, err := os.ReadFile(filepath.Join(root, setup.WorkflowPath))
	if err != nil || string(b) != old {
		t.Fatal("customized gate was changed")
	}
}
