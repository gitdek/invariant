package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The image is an explicit opt-in prerequisite, never built/pulled by tests.
// /opt/invariant-633/node contains package.json, package-lock.json and their
// installed node_modules: Node 22.14.0, Vitest 2.1.9, Jest 29.7.0,
// TypeScript 5.7.3 and ts-jest 29.2.5. It must contain no Python interpreter.
// Preparation alone is substituted by sealing those already-local bytes;
// selection, observers, transport, framework execution and collection are real.
type offline633 struct{ image, platform, manifest, lock string }

func nodeOffline633(t *testing.T) (offline633, bool) {
	t.Helper()
	nodeControl633(t)
	if os.Getenv("INVARIANT_NODE_ACCEPTANCE_REAL") != "1" && os.Getenv("INVARIANT_NATIVE_MIXED_REAL") != "1" {
		t.Log("real fixtures: INVARIANT_NODE_ACCEPTANCE_REAL=1, INVARIANT_NODE_ACCEPTANCE_IMAGE=<repository>@sha256:<digest>, INVARIANT_NODE_ACCEPTANCE_PLATFORM=linux/amd64 or linux/arm64; see docs/factory.md")
		return offline633{}, false
	}
	f := offline633{image: os.Getenv("INVARIANT_NODE_ACCEPTANCE_IMAGE"), platform: os.Getenv("INVARIANT_NODE_ACCEPTANCE_PLATFORM")}
	if !dependencyBase.MatchString(f.image) || !dependencyPlatform.MatchString(f.platform) {
		t.Fatal("enabled Node suite requires a digest-pinned local image and explicit Linux platform")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("enabled Node suite requires Docker: ", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := inspectDependencyImage(ctx, f.image, f.platform); err != nil {
		t.Fatal("enabled Node suite requires the daemon and image already available: ", err)
	}
	script := `const fs=require('node:fs'), cp=require('node:child_process'), assert=require('node:assert/strict');
assert.equal(process.versions.node,'22.14.0');
const root='/opt/invariant-633/node/';
for(const [name,version] of Object.entries({'vitest':'2.1.9','jest':'29.7.0','typescript':'5.7.3','ts-jest':'29.2.5'})) assert.equal(JSON.parse(fs.readFileSync(root+'node_modules/'+name+'/package.json')).version,version);
for(const name of ['python','python3']) assert(cp.spawnSync(name,['--version']).error, 'Node fixture must not rely on Python');
console.log(JSON.stringify({manifest:fs.readFileSync(root+'package.json','utf8'),lock:fs.readFileSync(root+'package-lock.json','utf8')}));`
	out, err := dependencyDocker(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--platform", f.platform, "--cap-drop", "ALL", "--entrypoint", "node", f.image, "-e", script)
	if err != nil {
		t.Fatalf("pinned offline Node prerequisites missing: %v %s", err, out)
	}
	var inputs struct{ Manifest, Lock string }
	if err := json.Unmarshal(out, &inputs); err != nil || inputs.Manifest == "" || inputs.Lock == "" {
		t.Fatalf("missing locked framework fixture: %v %s", err, out)
	}
	f.manifest, f.lock = inputs.Manifest, inputs.Lock
	p := nodePart633("vitest", "web", f.image)
	p.Inputs, p.Lockfiles = []string{"web/package.json"}, []string{"web/package-lock.json"}
	if err := npmDependencyLock(p, map[string][]byte{"web/package.json": []byte(f.manifest), "web/package-lock.json": []byte(f.lock)}); err != nil {
		t.Fatal("offline image must carry a supported complete npm lock: ", err)
	}
	return f, true
}

type nativeCase633 struct{ framework, part, file, selector, source string }
type nativeFixture633 struct {
	root, candidate string
	plan            *Plan
	sandbox         Sandbox
	runner          *nativeRunner633
}
type nativeRunner633 struct {
	*DockerDependencyRunner
	t                                *testing.T
	tools                            offline633
	mu                               sync.Mutex
	opens, closes, active, maxActive int
	deadlines                        []time.Time
	streams                          []RepositoryProcessOutput
	damage                           func(RepositoryProcessOutput) RepositoryProcessOutput
	afterWait                        func()
	afterClose                       func(context.Context) error
}

func (r *nativeRunner633) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	if q.Env["NPM_CONFIG_IGNORE_SCRIPTS"] == "true" {
		if !slices.Contains(q.Argv, "ci") {
			return DependencyArtifact{}, errors.New("Node fixture must retain npm lock preparation policy")
		}
		for file, want := range map[string]string{"package.json": r.tools.manifest, "package-lock.json": r.tools.lock} {
			data, err := os.ReadFile(filepath.Join(q.SourceDir, q.Directory, file))
			if err != nil || string(data) != want {
				return DependencyArtifact{}, fmt.Errorf("prepared image's locked input %s differs from capture", file)
			}
		}
	} else if len(q.Argv) != 0 {
		return DependencyArtifact{}, errors.New("Go/Python fixture must need no installation")
	}
	// Only this fixture substitutes dependency acquisition; it does not install
	// or execute npm and never substitutes acceptance evidence or a framework.
	q.Argv, q.Network, q.Pull = nil, false, false
	a, err := r.DockerDependencyRunner.Install(ctx, q)
	if a.Image != "" {
		r.t.Cleanup(func() {
			ctx, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			defer done()
			if _, err := dependencyDocker(ctx, "image", "rm", a.Image); err != nil {
				r.t.Error(err)
			}
		})
	}
	return a, err
}

func (r *nativeRunner633) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	if q.Network || q.Pull || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 {
		return nil, errors.New("native checks lost offline isolation")
	}
	e, err := r.DockerDependencyRunner.OpenChecks(ctx, q)
	if err != nil {
		return nil, err
	}
	transport, ok := e.(RepositoryProcessTransport)
	if !ok {
		close484(r.t, e)
		return nil, errors.New("prepared part has no process transport")
	}
	node := q.Env["NPM_CONFIG_OFFLINE"] == "true"
	if node {
		if q.Env["PIP_NO_INDEX"] != "" || q.Env["GOTOOLCHAIN"] != "" {
			close484(r.t, e)
			return nil, errors.New("Node check inherited another language runtime")
		}
		if out, err := e.Run(ctx, []string{"cp", "-a", "/opt/invariant-633/node/node_modules", "."}); err != nil {
			close484(r.t, e)
			return nil, fmt.Errorf("copy preinstalled locked fixture dependencies: %w %s", err, out)
		}
	}
	r.mu.Lock()
	r.opens++
	r.active++
	r.maxActive = max(r.maxActive, r.active)
	r.mu.Unlock()
	return &nativeEnvironment633{RepositoryProcessTransport: transport, runner: r, node: node}, nil
}

type nativeEnvironment633 struct {
	RepositoryProcessTransport
	runner *nativeRunner633
	node   bool
	once   sync.Once
	err    error
}

func (e *nativeEnvironment633) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	if e.node {
		for _, arg := range spec.Argv {
			if slices.Contains([]string{"python", "python3", "pip", "npm", "npx", "go"}, arg) {
				return nil, fmt.Errorf("Node runtime attempted dependency acquisition or another interpreter: %q", spec.Argv)
			}
		}
	}
	if spec.EventEnv != "" {
		d, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("acceptance process has no inherited deadline")
		}
		e.runner.mu.Lock()
		e.runner.deadlines = append(e.runner.deadlines, d)
		e.runner.mu.Unlock()
	}
	p, err := e.RepositoryProcessTransport.Prepare(ctx, spec)
	if p == nil {
		return nil, err
	}
	return &nativePrepared633{RepositoryPreparedProcess: p, runner: e.runner}, err
}
func (e *nativeEnvironment633) Close(ctx context.Context) error {
	e.once.Do(func() {
		if end, ok := ctx.Deadline(); ctx.Err() != nil || !ok || time.Until(end) > SandboxCleanupAllowance+time.Second {
			e.runner.t.Error("cleanup lost its bounded uncanceled reserve")
		}
		e.err = e.RepositoryProcessTransport.Close(ctx)
		if e.runner.afterClose != nil {
			e.err = errors.Join(e.err, e.runner.afterClose(ctx))
		}
		e.runner.mu.Lock()
		e.runner.closes++
		e.runner.active--
		e.runner.mu.Unlock()
	})
	return e.err
}

type nativePrepared633 struct {
	RepositoryPreparedProcess
	runner *nativeRunner633
}

func (p *nativePrepared633) Start(ctx context.Context) (RepositoryProcess, error) {
	r, err := p.RepositoryPreparedProcess.Start(ctx)
	if r == nil {
		return nil, err
	}
	return &nativeProcess633{RepositoryProcess: r, runner: p.runner}, err
}

type nativeProcess633 struct {
	RepositoryProcess
	runner *nativeRunner633
}

func (p *nativeProcess633) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	out, err := p.RepositoryProcess.Wait(ctx)
	if len(out.Events) > 0 {
		copy := out
		copy.Events, copy.Stdout, copy.Stderr = bytes.Clone(out.Events), bytes.Clone(out.Stdout), bytes.Clone(out.Stderr)
		p.runner.mu.Lock()
		p.runner.streams = append(p.runner.streams, copy)
		p.runner.mu.Unlock()
		if p.runner.damage != nil {
			out = p.runner.damage(out)
		}
		if p.runner.afterWait != nil {
			p.runner.afterWait()
		}
	}
	return out, err
}

func fixture633(t *testing.T, tools offline633, cases []nativeCase633, extra map[string]string, change func(*repository.Config)) *nativeFixture633 {
	t.Helper()
	f := &nativeFixture633{root: t.TempDir(), candidate: t.TempDir()}
	cfg := repository.Config{Version: 1, Parts: []repository.Part{}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	p := &Plan{Name: "native acceptance", Summary: "Return seven from each selected production API.", Sources: map[string]string{}}
	seen := map[string]bool{}
	for _, c := range cases {
		if !seen[c.part] {
			seen[c.part] = true
			part := nodePart633(c.framework, c.part, tools.image)
			file, source := c.part+"/product.ts", "export const value = 0;\n"
			switch c.framework {
			case "go":
				part.Language, part.Image = "go", toolchain.GoImage
				part.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}", "-count=1"}}
				part.Inputs = []string{c.part + "/go.mod"}
				write468(t, f.root, c.part+"/go.mod", "module fixture633/"+c.part+"\ngo 1.27.1\n")
				file, source = c.part+"/product.go", "package fixture\nconst Value = 0\n"
			case "pytest":
				part = part617(c.part, os.Getenv("INVARIANT_PYTEST_ACCEPTANCE_IMAGE"))
				file, source = c.part+"/product.py", "VALUE = 0\n"
			default:
				part.Inputs, part.Lockfiles = []string{c.part + "/package.json"}, []string{c.part + "/package-lock.json"}
				part.Commands["install"] = repository.Command{Argv: []string{"npm", "ci"}}
				write468(t, f.root, c.part+"/package.json", tools.manifest)
				write468(t, f.root, c.part+"/package-lock.json", tools.lock)
				if c.framework == "jest" {
					part.Commands["test_one"] = repository.Command{Argv: []string{"jest", "--runInBand", "--config", "jest.config.cjs", "--testNamePattern", "{test}"}}
					write468(t, f.root, c.part+"/jest.config.cjs", "module.exports={testEnvironment:'node',transform:{'^.+\\\\.tsx?$':['ts-jest',{tsconfig:{target:'ES2022',module:'commonjs',jsx:'preserve'},diagnostics:true}]}};\n")
				}
			}
			cfg.Parts = append(cfg.Parts, part)
			p.Files = append(p.Files, file)
			write468(t, f.root, file, source)
		}
		file := c.part + "/" + c.file
		p.Tests = append(p.Tests, Test{Name: "same display label", Part: c.part, Framework: c.framework, File: file, Selector: c.selector, Says: "The selected API returns seven."})
		if old, ok := p.Sources[file]; ok && old != c.source {
			t.Fatal("conflicting fixture source")
		}
		p.Sources[file] = c.source
	}
	if change != nil {
		change(&cfg)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, f.root, repository.Path, string(data))
	for name, source := range extra {
		write468(t, f.root, name, source)
	}
	git468(t, f.root, "init", "-q")
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-qm", "captured native base")
	snapshot, err := repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	f.plan = n503Read(t, p, snapshot)
	if err := CopyCheckout(t.Context(), f.root, f.candidate); err != nil {
		t.Fatal(err)
	}
	f.runner = &nativeRunner633{DockerDependencyRunner: &DockerDependencyRunner{}, t: t, tools: tools}
	f.sandbox = Sandbox{Original: f.root, Timeout: 6 * time.Minute, Repository: &RepositoryChecks{Snapshot: snapshot, Dependencies: DependencyPreparer{CacheDir: t.TempDir(), Platform: tools.platform, Runner: f.runner}}}
	t.Cleanup(func() {
		f.runner.mu.Lock()
		defer f.runner.mu.Unlock()
		if f.runner.active != 0 || f.runner.opens != f.runner.closes || f.runner.maxActive > 1 {
			t.Errorf("environments leaked or overlapped: %+v", f.runner)
		}
	})
	return f
}

func nodeImports633(framework string) string {
	module := "vitest"
	if framework == "jest" {
		module = "@jest/globals"
	}
	return "import { describe, test, it, expect, beforeAll, beforeEach, afterAll, afterEach } from '" + module + "';\n"
}
func nodeSource633(framework, relative string) string {
	return nodeImports633(framework) + "import { value } from '" + relative + "';\nlet calls=0; describe('outer',()=>{ test('ready [one].+ $(literal); | ^ $',()=>{ expect(++calls).toBe(1); expect(value).toBe(7); }); test('neighbor',()=>{ throw new Error('unselected body ran'); }); });\n"
}
func nodeCases633(framework string) []nativeCase633 {
	return []nativeCase633{{framework, "web", "ready.test.ts", "outer ready [one].+ $(literal); | ^ $", nodeSource633(framework, "./product")}}
}
func check633(t *testing.T, f *nativeFixture633, eligible bool) *Checked {
	t.Helper()
	ws := n503Draft(t, f.plan)
	for _, file := range f.plan.Files {
		write468(t, ws, "repo/"+file, "planner edits cannot replace the committed base\n")
	}
	c, err := Check(t.Context(), ws, f.root, f.sandbox)
	if err != nil || c == nil || (c.Problem == "") != eligible {
		t.Fatalf("base eligibility want=%v: %v %+v", eligible, err, c)
	}
	if c.Plan == nil || c.Plan.Hash() != f.plan.Hash() {
		t.Fatal("draft lost pinned authority")
	}
	if eligible {
		for _, test := range f.plan.Tests {
			r := result617(t, c.Run, test)
			if r.Status != "failed" || r.Kind != "test" && r.Kind != "compile" {
				t.Fatalf("qualified without attributable failure: %+v", r)
			}
		}
	}
	return c
}
func fix633(t *testing.T, f *nativeFixture633) {
	t.Helper()
	for _, file := range f.plan.Files {
		source := "export const value = 7;\nexport function future(): number { return 7; }\n"
		if strings.HasSuffix(file, ".go") {
			source = "package fixture\nconst Value = 7\n"
		}
		if strings.HasSuffix(file, ".py") {
			source = "VALUE = 7\n"
		}
		write468(t, f.candidate, file, source)
	}
}
func pass633(t *testing.T, f *nativeFixture633) Result {
	t.Helper()
	r, err := f.sandbox.Run(t.Context(), f.candidate, f.plan)
	if err != nil || !r.Passed() {
		t.Fatalf("corrected production did not pass: %v\n%s", err, r.Summary())
	}
	for _, test := range f.plan.Tests {
		v := result617(t, r, test)
		if v.Kind != "test" || v.Status != "passed" {
			t.Fatalf("candidate lacked a runtime pass: %+v", v)
		}
	}
	return r
}

func TestNodeOfflineExactResults633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			cases := nodeCases633(framework)
			cases = append(cases,
				nativeCase633{framework, "web", "nested/ready.test.ts", cases[0].selector, nodeSource633(framework, "../product")},
				nativeCase633{framework, "other", "ready.test.ts", cases[0].selector, cases[0].source},
				nativeCase633{framework, "web", "parameter.spec.tsx", "outer case 7", nodeImports633(framework) + "import {value} from './product'; describe('outer',()=>{ it.each([7,19])('case %s',(n)=>{expect(n).toBe(7);expect(value).toBe(n);}); });\n"})
			extra := map[string]string{}
			for _, part := range []string{"web", "other"} {
				extra[part+"/decoy/ready.test.ts"] = nodeImports633(framework) + "describe('outer',()=>{test('ready [one].+ $(literal); | ^ $',()=>expect(1).toBe(2));});\n"
			}
			f := fixture633(t, tools, cases, extra, nil)
			c := check633(t, f, true)
			for _, test := range f.plan.Tests {
				if result617(t, c.Run, test).Kind != "test" {
					t.Fatal("assertion failure was not a test result")
				}
			}
			fix633(t, f)
			pass633(t, f)
			if len(f.runner.streams) < 2*len(cases) {
				t.Fatal("selectors did not run independently")
			}
			for _, file := range f.plan.TestFiles() {
				if _, err := os.Stat(filepath.Join(f.root, file)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("base execution wrote acceptance into original checkout")
				}
			}
			both := nodeImports633(framework) + "import {value} from './product'; test('fails',()=>expect(value).toBe(7)); test('passes',()=>expect(1).toBe(1));\n"
			g := fixture633(t, tools, []nativeCase633{{framework, "web", "both.test.ts", "fails", both}, {framework, "web", "both.test.ts", "passes", both}}, nil, nil)
			mixed := check633(t, g, false)
			if result617(t, mixed.Run, g.plan.Tests[0]).Status != "failed" || result617(t, mixed.Run, g.plan.Tests[1]).Status != "passed" {
				t.Fatal("neighbor outcome substituted for an exact selector")
			}
		})
	}
}

func TestNodeOfflineProposedAPI633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			source := nodeImports633(framework) + "import {future} from './product'; test('future API',()=>{ expect(future()).toBe(7); });\n"
			f := fixture633(t, tools, []nativeCase633{{framework, "web", "future.test.ts", "future API", source}}, nil, nil)
			c := check633(t, f, true)
			v := result617(t, c.Run, f.plan.Tests[0])
			if v.Kind != "compile" || v.Reason == "" {
				t.Fatalf("missing proposed API lacks conservative attribution: %+v", v)
			}
			fix633(t, f)
			pass633(t, f)
		})
	}
}

func TestNodeOfflineRefusals633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		imports := nodeImports633(framework)
		expectedFailure := "fails"
		if framework == "jest" {
			expectedFailure = "failing"
		}
		cases := []struct{ name, body, selector, extraFile, extraSource string }{
			{"absent", "test('neighbor',()=>expect(1).toBe(2));", "selected", "", ""},
			{"skip", "test.skip('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"todo", "test.todo('selected');", "selected", "", ""},
			{"expected failure", "test." + expectedFailure + "('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"expected pass", "test." + expectedFailure + "('selected',()=>expect(1).toBe(1));", "selected", "", ""},
			{"duplicate", "test('selected',()=>expect(1).toBe(2));test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"ambiguous each", "test.each([1,2])('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"parameter group", "test.each([1,2])('case %s',()=>expect(1).toBe(2));", "case %s", "", ""},
			{"before hook", "beforeEach(()=>{throw new Error('setup broke')});test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"after hook", "afterEach(()=>{throw new Error('teardown broke')});test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"after suite", "afterAll(()=>{throw new Error('suite teardown broke')});test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"ordinary exception", "test('selected',()=>{throw new Error('unrelated tool error')});", "selected", "", ""},
			{"no tests", "console.log('1 failed');", "selected", "", ""},
			{"worker death", "test('selected',()=>{process.exit(7)});", "selected", "", ""},
			{"zero exit without call", "process.exit(0);test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"bad transform", "const broken: = ; test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"broken configuration", "test('selected',()=>expect(1).toBe(2));", "selected", framework + ".config.cjs", "throw new Error('configuration failure633');\n"},
			{"unavailable runner", "test('selected',()=>expect(1).toBe(2));", "selected", framework + ".config.cjs", map[string]string{"vitest": "module.exports={test:{runner:'./missing-runner633.mjs'}};\n", "jest": "module.exports={testRunner:'./missing-runner633.cjs'};\n"}[framework]},
			{"missing distribution", "import missing from 'missing-package-633';test('selected',()=>expect(missing()).toBe(7));", "selected", "", ""},
			{"neighbor proposed API", "import {future} from './product';test('neighbor',()=>expect(future()).toBe(7));test('selected',()=>expect(1).toBe(2));", "selected", "", ""},
			{"unreachable proposed API", "import {future} from './product';test('selected',()=>{return;future();});", "selected", "", ""},
			{"conditional proposed API", "import {future} from './product';test('selected',()=>{if(false){future();}expect(1).toBe(2);});", "selected", "", ""},
			{"broken production import", "import {value} from './product';test('selected',()=>expect(value).toBe(7));", "selected", "product.ts", "import './missing633';export const value=0;\n"},
		}
		for _, tc := range cases {
			t.Run(framework+"/"+tc.name, func(t *testing.T) {
				extra := map[string]string{}
				if tc.extraFile != "" {
					extra["web/"+tc.extraFile] = tc.extraSource
				}
				f := fixture633(t, tools, []nativeCase633{{framework, "web", "refused.test.ts", tc.selector, imports + tc.body + "\n"}}, extra, nil)
				c := check633(t, f, false)
				v := result617(t, c.Run, f.plan.Tests[0])
				if v.Status == "failed" && (v.Kind == "test" || v.Kind == "compile") {
					t.Fatalf("refusal manufactured attributable failure: %+v", v)
				}
				if v.Reason == "" {
					t.Fatal("refusal lost reason")
				}
				if r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan); r.Passed() {
					t.Fatal("invalid selection qualified a candidate")
				}
			})
		}
		t.Run(framework+"/missing locked runtime", func(t *testing.T) {
			f := fixture633(t, tools, nodeCases633(framework), nil, func(c *repository.Config) {
				c.Parts[0].Commands["build"] = repository.Command{Argv: []string{"node", "-e", "require('node:fs').rmSync('node_modules/" + framework + "',{recursive:true,force:true})"}}
			})
			check633(t, f, false)
			if r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan); r.Passed() {
				t.Fatal("missing prepared framework qualified")
			}
		})
	}
}

func TestNodeOfflineEvidenceAuthority633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		f := fixture633(t, tools, nodeCases633(framework), map[string]string{"web/results.json": `{"success":true,"numFailedTests":0}`, "web/fake-reporter.cjs": "module.exports=class { onRunComplete(){console.log('PASS outer ready')} };\n"}, nil)
		check633(t, f, true)
		if len(f.runner.streams) == 0 {
			t.Fatal("real framework supplied no private stream")
		}
		stale := bytes.Clone(f.runner.streams[len(f.runner.streams)-1].Events)
		fix633(t, f)
		pass633(t, f)
		for _, mode := range []string{"missing", "console", "stale", "duplicate", "truncated", "wrong framework", "wrong file", "wrong part", "wrong title", "not exited", "not drained", "conflicting exit"} {
			t.Run(framework+"/"+mode, func(t *testing.T) {
				f.runner.damage = func(out RepositoryProcessOutput) RepositoryProcessOutput {
					switch mode {
					case "missing":
						out.Events = nil
					case "console":
						out.Stdout = append(out.Stdout, out.Events...)
						out.Events = nil
					case "stale":
						out.Events = stale
					case "duplicate":
						out.Events = append(bytes.Clone(out.Events), out.Events...)
					case "truncated":
						out.Events = out.Events[:len(out.Events)/2]
					case "not exited":
						out.Exited = false
					case "not drained":
						out.Drained = false
					case "conflicting exit":
						out.ExitCode = 19
					default:
						from, to := framework, "wrong-framework633"
						if mode == "wrong file" {
							from, to = "web/ready.test.ts", "web/neighbor.test.ts"
						}
						if mode == "wrong part" {
							from, to = `"web"`, `"other"`
						}
						if mode == "wrong title" {
							from, to = "outer ready", "neighbor ready"
						}
						changed := bytes.ReplaceAll(out.Events, []byte(from), []byte(to))
						if bytes.Equal(changed, out.Events) {
							t.Fatal("fixture did not alter the bound identity")
						}
						out.Events = changed
					}
					return out
				}
				if r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan); r.Passed() {
					t.Fatalf("%s evidence qualified", mode)
				}
				check633(t, f, false)
			})
		}
		f.runner.damage = nil
		for _, mode := range []string{"acceptance", "lock", "manifest"} {
			t.Run(framework+"/tampered "+mode, func(t *testing.T) {
				g := fixture633(t, tools, nodeCases633(framework), nil, nil)
				fix633(t, g)
				file := g.plan.Tests[0].File
				if mode == "lock" {
					file = "web/package-lock.json"
				}
				if mode == "manifest" {
					file = "web/package.json"
				}
				write468(t, g.candidate, file, "tampered source or dependency authority\n")
				if r, _ := g.sandbox.Run(t.Context(), g.candidate, g.plan); r.Passed() || g.runner.opens != 0 {
					t.Fatal("tampered bytes executed or qualified")
				}
			})
		}
	}
}
