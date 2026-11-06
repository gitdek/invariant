package plumbing

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestNativeMixedAdmission633(t *testing.T) {
	nodeControl633(t)
	// Exercise the shared lifecycle using its established trusted environment
	// contract. These stand-ins are not tests of real framework evidence;
	// TestNativeOfflineMixedBuilder633 exercises all four real producers.
	parts := []repository.Part{part484("go", "api"), part617("worker", "python@"+hash468("633")), nodePart633("vitest", "web", "node@"+hash468("633")), nodePart633("jest", "admin", "node@"+hash468("633"))}
	parts[0].Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}"}}
	tests := []Test{
		{Name: "same", Part: "api", Framework: "go", File: "api/ready_accept_test.go", Selector: "TestReady", Says: "Returns seven."},
		{Name: "same", Part: "worker", Framework: "pytest", File: "worker/test_ready.py", Selector: "test_ready.py::test_ready", Says: "Returns seven."},
		{Name: "same", Part: "web", Framework: "vitest", File: "web/ready.test.ts", Selector: "TestReady", Says: "Returns seven."},
		{Name: "same", Part: "admin", Framework: "jest", File: "admin/ready.test.ts", Selector: "TestReady", Says: "Returns seven."},
	}
	p := &Plan{Name: "four frameworks", Summary: "Require each exact selector independently.", Files: []string{"api/main.go", "worker/main.py", "web/main.ts", "admin/main.ts"}, Tests: tests, Sources: map[string]string{
		"api/ready_accept_test.go": "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"seven\")}\n",
		"worker/test_ready.py":     "def test_ready(): assert False\n",
		"web/ready.test.ts":        "test('TestReady',()=>{});\n", "admin/ready.test.ts": "test('TestReady',()=>{});\n",
	}}
	adapters := map[string]RepositoryAcceptanceAdapter{}
	for i, test := range tests {
		a := (Sandbox{Repository: &RepositoryChecks{}}).repositoryAdapter(test.Framework)
		if a == nil {
			t.Fatalf("missing %s default", test.Framework)
		}
		if _, err := a.Select(t.Context(), parts[i], test); err != nil {
			t.Fatal(err)
		}
		// Select-only forwarding deliberately uses #515's evidence contract
		// without pretending to perform Go discovery or a Node framework run.
		adapters[test.Framework] = &adapter515{selectHook: a.Select}
	}
	root, candidate, snapshot := fixture484(t, parts, nil)
	p = n503Read(t, p, snapshot)
	runner := &runner515{runner484: newRunner484(t)}
	s := sandbox484(t, root, snapshot, runner.runner484)
	s.Repository.Dependencies.Runner = runner
	s.Repository.Adapters = adapters
	runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(in, "test", "failed", "selected assertion"), nil, nil
	}
	c, err := Check(t.Context(), n503Draft(t, p), root, s)
	if err != nil || c == nil || c.Problem != "" || len(runner.invocations) != 4 {
		t.Fatalf("mixed independent failures did not qualify: %v %+v", err, c)
	}
	runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		return evidence515(in, "test", "passed", "selected call completed"), nil, nil
	}
	if c, _ := Check(t.Context(), n503Draft(t, p), root, s); c == nil || c.Problem == "" {
		t.Fatal("passing native selectors qualified a draft")
	}
	r, err := s.Run(t.Context(), candidate, p)
	if err != nil || !r.Passed() {
		t.Fatalf("four complete native results refused: %v %s", err, r.Summary())
	}
	for _, mode := range []string{"missing", "duplicate", "wrong identity", "compile", "skip", "unfinished", "command failure"} {
		bad := r
		bad.TestResults = slices.Clone(r.TestResults)
		bad.CommandResults = slices.Clone(r.CommandResults)
		switch mode {
		case "missing":
			bad.TestResults = bad.TestResults[:len(bad.TestResults)-1]
		case "duplicate":
			bad.TestResults = append(bad.TestResults, bad.TestResults[0])
		case "wrong identity":
			bad.TestResults[0].Framework = "jest"
		case "compile":
			bad.TestResults[0].Kind = "compile"
		case "skip":
			bad.TestResults[0].Status = "skipped"
		case "unfinished":
			bad.TestResults[0].Status = "running"
		case "command failure":
			bad.CommandResults[0].Status = "failed"
		}
		if bad.Passed() {
			t.Fatalf("aggregate result accepted %s with all legacy Accepted booleans still true", mode)
		}
	}
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	registration, err := q.register(t.Context(), s, p)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.finish()
	client := &CheckClient{Address: q.listener.Addr().String(), Build: registration.id}
	forged := cloneRepositoryPlan(p)
	forged.Tests[2].Selector = "forged"
	forged.Execution.Digest = hash468("forged")
	r, err = (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(t.Context(), candidate, forged)
	if err != nil || !r.Passed() || result617(t, r, p.Tests[2]).Selector != "TestReady" {
		t.Fatalf("queue replaced retained native authority: %v %+v", err, r)
	}
	t.Run("legacy hashes and locks", TestNativeExtensionPreservesPreChangeGoHashesAndCommittedLocks)
	closed484(t, runner.runner484)
}

func mixedCases633() []nativeCase633 {
	cases := []nativeCase633{
		{"go", "api", "ready_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){if Value!=7{t.Fatal(Value)}}\n"},
		{"pytest", "worker", "test_ready.py", "test_ready.py::test_ready", "import product\ndef test_ready(): assert product.VALUE == 7\n"},
	}
	for _, framework := range []string{"vitest", "jest"} {
		part := "web"
		if framework == "jest" {
			part = "admin"
		}
		for _, file := range []string{"ready.test.ts", "other.test.ts"} {
			cases = append(cases, nativeCase633{framework, part, file, "TestReady", nodeImports633(framework) + "import {value} from './product';test('TestReady',()=>expect(value).toBe(7));\n"})
		}
	}
	return cases
}

type mixedBuilder633 struct {
	t       *testing.T
	f       *nativeFixture633
	mode    string
	tool    Result
	reviews int
}

func (*mixedBuilder633) Name() string { return "mixed acceptance builder fixture" }
func (a *mixedBuilder633) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		a.reviews++
		if read484(a.t, filepath.Join(job.Workspace, "base"), "web/product.ts") != "export const value = 0;\n" {
			return synth.Usage{}, errors.New("review lost original checkout")
		}
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	client := &CheckClient{}
	for i, flag := range job.GateServer {
		if i+1 >= len(job.GateServer) {
			continue
		}
		switch flag {
		case "-check-queue":
			client.Address = job.GateServer[i+1]
		case "-check-build":
			client.Build = job.GateServer[i+1]
		}
	}
	if client.Address == "" || client.Build == "" {
		return synth.Usage{}, errors.New("builder omitted captured registration")
	}
	copy := *a.f
	copy.candidate = job.Workspace
	fix633(a.t, &copy)
	write468(a.t, job.Workspace, repository.Path, `{"version":999,"parts":[]}`)
	write468(a.t, job.Workspace, LockPath(633), `{"plan":{"tests":[]}}`)
	write468(a.t, job.Workspace, "web/unplanned.test.ts", "throw new Error('unplanned code must not enter execution');\n")
	forged := cloneRepositoryPlan(a.f.plan)
	forged.Tests = nil
	forged.Sources = map[string]string{}
	forged.Execution.Digest = hash468("child authority")
	// Pinned sources are restaged from the original checkout. Changing a child
	// test or its claimed plan cannot conceal a failing production candidate.
	file := a.f.plan.Tests[2].File
	write468(a.t, job.Workspace, "web/product.ts", "export const value=0;\n")
	write468(a.t, job.Workspace, file, "test('forged',()=>{});\n")
	bad, _ := (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(ctx, job.Workspace, forged)
	if bad.Passed() {
		return synth.Usage{}, errors.New("builder tool accepted changed pinned native source")
	}
	fix633(a.t, &copy)
	write468(a.t, job.Workspace, file, a.f.plan.Sources[file])
	var err error
	a.tool, err = (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(ctx, job.Workspace, forged)
	if err != nil || !a.tool.Passed() {
		return synth.Usage{}, fmt.Errorf("retained mixed check failed: %v\n%s", err, a.tool.Summary())
	}
	switch a.mode {
	case "regress after tool":
		write468(a.t, job.Workspace, "web/product.ts", "export const value=0;\n")
	case "tamper after tool":
		write468(a.t, job.Workspace, "web/product.ts", "export const value=0;\n")
		write468(a.t, job.Workspace, file, "test('forged',()=>{});\n")
	}
	return synth.Usage{Summary: "candidate checked"}, nil
}

func TestNativeOfflineMixedBuilder633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	if os.Getenv("INVARIANT_NATIVE_MIXED_REAL") != "1" {
		t.Log("also set INVARIANT_NATIVE_MIXED_REAL=1 with the local pinned Go image and INVARIANT_PYTEST_ACCEPTANCE_IMAGE to run the real four-framework matrix")
		return
	}
	image := os.Getenv("INVARIANT_PYTEST_ACCEPTANCE_IMAGE")
	if !dependencyBase.MatchString(image) {
		t.Fatal("enabled mixed suite needs a digest-pinned local Python 3.13/pytest 8.3.5 image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for _, image := range []string{image, toolchain.GoImage} {
		if _, err := inspectDependencyImage(ctx, image, tools.platform); err != nil {
			t.Fatal("enabled mixed suite prerequisite missing: ", err)
		}
	}
	out, err := dependencyDocker(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--platform", tools.platform, "--entrypoint", "python3", image, "-I", "-c", "import sys,pytest;assert sys.version_info[:2]==(3,13);assert pytest.__version__=='8.3.5'")
	if err != nil {
		t.Fatalf("enabled mixed suite needs real pinned pytest: %v %s", err, out)
	}
	for _, shared := range []bool{false, true} {
		for _, mode := range []string{"pass", "regress after tool", "tamper after tool"} {
			t.Run(fmt.Sprintf("shared=%v/%s", shared, mode), func(t *testing.T) {
				f := fixture633(t, tools, mixedCases633(), nil, nil)
				check633(t, f, true)
				lock := Lock{Plan: *f.plan, Ratified: Ratification{Issue: 633, Proposal: f.plan.Hash()}}
				if err := lock.Write(f.root); err != nil {
					t.Fatal(err)
				}
				git468(t, f.root, "add", "--all")
				git468(t, f.root, "commit", "-qm", "ratified native fixture")
				agent := &mixedBuilder633{t: t, f: f, mode: mode}
				b := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 3, Timeout: 10 * time.Minute, Sandbox: f.sandbox}
				if shared {
					q, err := NewCheckQueue(1)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = q.Close() })
					b.Checks = q
				}
				r, err := b.Build(t.Context(), f.root, 633, t.TempDir())
				if r == nil || !agent.tool.Passed() || r.AgentError != "" || mode == "pass" && err != nil {
					t.Fatalf("mixed builder failed: %v %+v", err, r)
				}
				want := mode == "pass"
				if r.Built() != want || r.Passed != want || r.Approved != want || (agent.reviews == 1) != want {
					t.Fatalf("fresh checks did not gate readiness: %+v reviews=%d", r, agent.reviews)
				}
				if f.runner.opens < 8 {
					t.Fatal("tool and final checks did not run independent environments")
				}
				for _, test := range f.plan.Tests {
					if read484(t, f.root, test.File) != f.plan.Sources[test.File] {
						t.Fatal("build changed pinned acceptance")
					}
				}
			})
		}
	}
}

func TestNodeOfflineLifetime633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		f := fixture633(t, tools, append(nodeCases633(framework), nativeCase633{framework, "other", "ready.test.ts", "outer ready [one].+ $(literal); | ^ $", nodeSource633(framework, "./product")}), nil, nil)
		check633(t, f, true)
		if len(f.runner.deadlines) < 2 {
			t.Fatal("missing independently observed selector deadlines")
		}
		for _, d := range f.runner.deadlines[1:] {
			if d.After(f.runner.deadlines[0]) {
				t.Fatal("later part renewed the shared allowance")
			}
		}
		g := fixture633(t, tools, nodeCases633(framework), nil, nil)
		ctx, cancel := context.WithCancel(t.Context())
		g.runner.afterWait = cancel
		c, err := Check(ctx, n503Draft(t, g.plan), g.root, g.sandbox)
		if err != nil || c == nil || c.Problem == "" || !c.Run.Interrupted {
			t.Fatalf("cancellation after real failure qualified: %v %+v", err, c)
		}
		cancel()
		h := fixture633(t, tools, nodeCases633(framework), nil, nil)
		h.runner.afterClose = func(context.Context) error { return errors.New("fixture cleanup failure633") }
		c = check633(t, h, false)
		if !strings.Contains(c.Problem, "cleanup failure633") || result617(t, c.Run, h.plan.Tests[0]).Status != "failed" {
			t.Fatal("cleanup veto lost completed evidence or its reason")
		}
		// A hanging test is not a completed assertion failure. Keep the captured
		// per-test limit short without renewing the enclosing host allowance.
		source := nodeImports633(framework) + "test('hang',async()=>{await new Promise(()=>{});});\n"
		hang := fixture633(t, tools, []nativeCase633{{framework, "web", "hang.test.ts", "hang", source}}, nil, func(c *repository.Config) {
			a := c.Parts[0].Commands["test_one"].Argv
			c.Parts[0].Commands["test_one"] = repository.Command{Argv: append(a, "--testTimeout", "100")}
		})
		check633(t, hang, false)
	}
}

func TestNodeOfflineFreshTransport633(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	f := fixture633(t, tools, nodeCases633("vitest"), nil, nil)
	// Use the same verified preparation and Node environment as acceptance.
	session, err := f.sandbox.Repository.Dependencies.Prepare(t.Context(), f.sandbox.Repository.Snapshot, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	prepared := session.parts["web"]
	q := cloneDependencyRequest(prepared.request)
	q.Image = prepared.artifact.Image
	q.SourceDir = f.candidate
	q.Network = false
	q.Pull = false
	q.Argv = nil
	q.Env = dependencyEnv("typescript", false)
	q.Token, err = dependencyToken()
	if err != nil {
		t.Fatal(err)
	}
	env, err := f.runner.OpenChecks(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer close484(t, env)
	transport := env.(RepositoryProcessTransport)
	wire := []byte(dockerProcessProtocol)
	for _, frame := range []struct {
		kind byte
		text string
	}{{'s', ""}, {'v', "forged633"}, {'x', "0"}, {'d', ""}} {
		var header [5]byte
		header[0] = frame.kind
		binary.BigEndian.PutUint32(header[1:], uint32(len(frame.text)))
		wire = append(wire, header[:]...)
		wire = append(wire, frame.text...)
	}
	replacement := "process.stdout.write(Buffer.from('" + hex.EncodeToString(wire) + "','hex'));\n"
	quoted, _ := json.Marshal(replacement)
	// Replace every private Node transport script left by the first invocation.
	// These bytes would forge a successful second invocation without its body.
	first := `const fs=require('node:fs'),path=require('node:path');let count=0;
function visit(dir){for(const e of fs.readdirSync(dir,{withFileTypes:true})){const p=path.join(dir,e.name);if(e.isDirectory())visit(p);else if(/\.(cjs|mjs|js)$/.test(e.name)){fs.renameSync(p,p+'.old633');fs.writeFileSync(p,` + string(quoted) + `);count++;}}}
for(const name of fs.readdirSync('/invariant'))if(name.startsWith('process-'))visit('/invariant/'+name);
if(!count)throw new Error('fixture did not replace a Node helper');fs.writeFileSync(process.env.EVENT633,'first633');console.log('replaced633');`
	run := func(script string) (RepositoryProcessOutput, error) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		p, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"node", "-e", script}, EventEnv: "EVENT633"})
		if err != nil {
			return RepositoryProcessOutput{}, err
		}
		r, err := p.Start(ctx)
		if r == nil {
			return RepositoryProcessOutput{}, err
		}
		out, wait := r.Wait(ctx)
		return out, errors.Join(err, wait)
	}
	one, err := run(first)
	if err != nil || !one.Drained || one.ExitCode != 0 || string(one.Events) != "first633" || !bytes.Contains(one.Stdout, []byte("replaced633")) {
		t.Fatalf("workload did not replace actual transport: %v %+v", err, one)
	}
	two, err := run(`require('node:fs').writeFileSync(process.env.EVENT633,'authentic633');console.log('second633');process.exit(7);`)
	if bytes.Contains(two.Events, []byte("forged633")) {
		t.Fatalf("later invocation used workload-left helper: %v %+v", err, two)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatal("timeout is not a helper-tampering refusal")
		}
		if two.Drained && two.ExitCode == 0 {
			t.Fatal("refusal retained apparent success")
		}
		return
	}
	if !two.Exited || !two.Drained || two.ExitCode != 7 || string(two.Events) != "authentic633" || !bytes.Contains(two.Stdout, []byte("second633")) {
		t.Fatalf("later workload did not run under authentic helper: %+v", two)
	}
}
