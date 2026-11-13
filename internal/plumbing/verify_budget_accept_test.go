package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// Observe the configuration delivered to the container, rather than pinning a
// new Go helper or the names of constants. For selection and Builder wiring,
// Docker is a reporting stand-in; the two duration tests below execute the
// real embedded runner and real compiled Go tests through s397Docker.
const v498Docker = `#!/usr/bin/env python3
import glob, json, os, sys, time
a = sys.argv[1:]
if not a or a[0] != "run":
    sys.exit(0)
src = next(a[i+1].rsplit(":", 1)[0] for i, v in enumerate(a[:-1])
           if v in ("-v", "--volume") and a[i+1].endswith(":/src"))
configs = glob.glob(src + "/.invariant-runner-*/config.json")
assert len(configs) == 1, configs
with open(configs[0]) as f:
    config = json.load(f)
config["observed_at"] = time.time()
with open(os.environ["V498_RECORD"], "a") as f:
    f.write(json.dumps(config) + "\n")
if os.environ.get("V498_HANG") == "1":
    time.sleep(60)
print("@@invariant vet=0")
print("@@invariant test=0")
for test in config.get("acceptance") or []:
    print("--- PASS: " + test["name"] + " (0.00s)")
print("@@invariant accept=0")
`

type v498Observation struct {
	Deadline float64  `json:"deadline"`
	Observed float64  `json:"observed_at"`
	Tests    float64  `json:"shard_timeout"`
	Packages []string `json:"packages"`
}

func v498Watchdog(t *testing.T) context.Context {
	t.Helper()
	// A watchdog without a Deadline lets us observe the production deadline.
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(90*time.Second, cancel)
	t.Cleanup(func() { timer.Stop(); cancel() })
	return ctx
}

func v498Spy(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("the sandbox runner needs python3: ", err)
	}
	bin := t.TempDir()
	s397Write(t, filepath.Join(bin, "docker"), v498Docker, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	record := filepath.Join(t.TempDir(), "checks.jsonl")
	t.Setenv("V498_RECORD", record)
	return record
}

func v498Read(t *testing.T, path string) []v498Observation {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var observations []v498Observation
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var o v498Observation
		if err := json.Unmarshal([]byte(line), &o); err != nil {
			t.Fatal(err)
		}
		observations = append(observations, o)
	}
	return observations
}

func v498Budgets(t *testing.T, o v498Observation, started time.Time, run, tests time.Duration) {
	t.Helper()
	if got := time.Duration(o.Tests * float64(time.Second)); got != tests {
		t.Errorf("shared test budget = %s, want %s", got, tests)
	}
	deadline := time.Unix(0, int64(o.Deadline*float64(time.Second)))
	// The deadline starts before preparation and is not renewed after it.
	if deadline.Before(started.Add(run-time.Second)) || deadline.After(started.Add(run+time.Second)) {
		t.Errorf("whole-run deadline is %s after entry, want %s", deadline.Sub(started), run)
	}
}

func TestVerifyCheckSelectsLongerBudgetsPrecisely(t *testing.T) {
	record := v498Spy(t)
	root := t.TempDir()
	for _, dir := range []string{"internal/verify", "internal/verify/pythongate", "internal/verifyish", "internal/plumbing"} {
		s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
	}
	s397Write(t, filepath.Join(root, "go.mod"), "module github.com/gitdek/invariant\n\ngo 1.27.1\n", 0o644)
	s397Write(t, filepath.Join(root, "internal/verify/assets/driver.py"), "# embedded fixture\n", 0o644)
	s397Write(t, filepath.Join(root, "internal/verify/added_accept_test.go"), "package fixture\n", 0o644)
	ctx := v498Watchdog(t)
	for _, tc := range []struct {
		name       string
		files      []string
		acceptance bool
		overall    time.Duration
		phase      time.Duration
		wantRun    time.Duration
		wantTests  time.Duration
		wantPkg    string
	}{
		{name: "verify", files: []string{"internal/verify/code.go"}, wantRun: 60 * time.Minute, wantTests: 55 * time.Minute, wantPkg: "./internal/verify"},
		{name: "embedded_asset", files: []string{"internal/verify/assets/driver.py"}, wantRun: 60 * time.Minute, wantTests: 55 * time.Minute, wantPkg: "./internal/verify"},
		{name: "acceptance_owns_verify", files: []string{"SPEC.md"}, acceptance: true, wantRun: 60 * time.Minute, wantTests: 55 * time.Minute, wantPkg: "./internal/verify"},
		{name: "mixed", files: []string{"internal/plumbing/code.go", "internal/verify/code.go"}, wantRun: 60 * time.Minute, wantTests: 55 * time.Minute, wantPkg: "./internal/verify"},
		{name: "other_package", files: []string{"internal/plumbing/code.go"}, wantRun: 12 * time.Minute, wantTests: 10 * time.Minute, wantPkg: "./internal/plumbing"},
		{name: "prefix_is_not_package", files: []string{"internal/verifyish/code.go"}, wantRun: 12 * time.Minute, wantTests: 10 * time.Minute, wantPkg: "./internal/verifyish"},
		{name: "separate_child_package", files: []string{"internal/verify/pythongate/code.go"}, wantRun: 12 * time.Minute, wantTests: 10 * time.Minute, wantPkg: "./internal/verify/pythongate"},
		{name: "shorter_overrides", files: []string{"internal/verify/code.go"}, overall: 24 * time.Minute, phase: 19 * time.Minute, wantRun: 24 * time.Minute, wantTests: 19 * time.Minute, wantPkg: "./internal/verify"},
		{name: "cannot_extend_verify", files: []string{"internal/verify/code.go"}, overall: 2 * time.Hour, phase: 2 * time.Hour, wantRun: 60 * time.Minute, wantTests: 55 * time.Minute, wantPkg: "./internal/verify"},
		{name: "cannot_extend_other", files: []string{"internal/plumbing/code.go"}, overall: time.Hour, phase: time.Hour, wantRun: 12 * time.Minute, wantTests: 10 * time.Minute, wantPkg: "./internal/plumbing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{Files: tc.files}
			if tc.acceptance {
				p.Tests = []Test{{Name: "TestBudgetFixture", File: "internal/verify/added_accept_test.go"}}
			}
			s := Sandbox{Image: "fixture", ModCache: t.TempDir(), Timeout: tc.overall, ShardTimeout: tc.phase}
			started := time.Now()
			r, err := s.Run(ctx, root, p)
			if err != nil || !r.Passed() {
				t.Fatalf("configuration fixture: %+v, %v", r, err)
			}
			all := v498Read(t, record)
			o := all[len(all)-1]
			v498Budgets(t, o, started, tc.wantRun, tc.wantTests)
			if !strings.Contains(" "+strings.Join(o.Packages, " ")+" ", " "+tc.wantPkg+" ") {
				t.Errorf("selected packages = %q, missing %s", o.Packages, tc.wantPkg)
			}
		})
	}
	t.Run("parent_deadline_still_bounds_verify", func(t *testing.T) {
		started := time.Now()
		parent, cancel := context.WithDeadline(ctx, started.Add(18*time.Minute))
		defer cancel()
		s := Sandbox{Image: "fixture", ModCache: t.TempDir()}
		r, err := s.Run(parent, root, &Plan{Files: []string{"internal/verify/code.go"}})
		if err != nil || !r.Passed() {
			t.Fatalf("parent-bounded check: %+v, %v", r, err)
		}
		all := v498Read(t, record)
		v498Budgets(t, all[len(all)-1], started, 18*time.Minute, 55*time.Minute)
	})
}

// Scale the configured test-phase budget only at the Docker boundary, so that
// internal/verify's 55 minutes are twenty seconds of fixture execution and
// the ordinary ten minutes under four: a fixture test of eleven seconds
// finishes only under the longer budget, and eight of twenty-one outlast
// it. Preparation retains its normal time; the production Go deadline is
// observed before any scaling. This exercises the actual runner's queue,
// subprocess timeout, records and Go Result parsing without adding a
// production clock or sleeping for an hour. The tests keep the names #498
// gave them, when the budget was twenty minutes and a second stood for one.
func v498TimedRun(t *testing.T, slow bool) (Result, v498Observation, time.Time) {
	t.Helper()
	f := s397New(t)
	s397Write(t, filepath.Join(f.root, "go.mod"), "module github.com/gitdek/invariant\n\ngo 1.27.1\n", 0o644)
	f.plan.Files = []string{"internal/verify/code.go"}
	s397Write(t, filepath.Join(f.root, "internal/verify/code.go"), "package verify\n", 0o644)
	var source strings.Builder
	source.WriteString("package verify\nimport (\"testing\"; \"time\")\n")
	if slow {
		// All eight workers stay occupied, leaving the ninth test unstarted.
		for i := 0; i < 8; i++ {
			fmt.Fprintf(&source, "func TestSlow%02d(t *testing.T) { time.Sleep(21*time.Second) }\n", i)
		}
		source.WriteString("func TestZZUnstarted(t *testing.T) {}\n")
	} else {
		source.WriteString("func TestElevenMinutes(t *testing.T) { time.Sleep(11*time.Second) }\n")
	}
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(f.root, "internal/verify/code_test.go"), string(formatted), 0o644)
	f.warm(t)
	record := filepath.Join(f.control, "budgets.jsonl")
	insert := fmt.Sprintf(`
import glob, time
configs = glob.glob(src + "/.invariant-runner-*/config.json")
assert len(configs) == 1, configs
with open(configs[0]) as source:
    config = json.load(source)
config["observed_at"] = time.time()
with open(%q, "a") as out:
    out.write(json.dumps(config) + "\n")
config["shard_timeout"] /= 165
with open(configs[0], "w") as out:
    json.dump(config, out)
`, record)
	needle := "src = mounts[\"/src\"]\n"
	if strings.Count(s397Docker, needle) != 1 {
		t.Fatal("local Docker fixture cannot locate its staged source")
	}
	s397Write(t, filepath.Join(f.bin, "docker"), strings.Replace(s397Docker, needle, needle+insert, 1), 0o755)
	started := time.Now()
	r, err := (Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache}).Run(v498Watchdog(t), f.root, f.plan)
	if err != nil {
		t.Fatalf("real runner: %v\n%s", err, r.Output)
	}
	all := v498Read(t, record)
	if len(all) != 1 {
		t.Fatalf("got %d checks, want one", len(all))
	}
	return r, all[0], started
}

func TestVerifyCheckAllowsElevenMinutes(t *testing.T) {
	r, observed, started := v498TimedRun(t, false)
	v498Budgets(t, observed, started, 60*time.Minute, 55*time.Minute)
	if !r.Passed() {
		t.Fatalf("eleven-minute equivalent check was cut off: %s\n%s", r.Summary(), r.Output)
	}
	for _, test := range r.TestResults {
		if test.Package == "github.com/gitdek/invariant/internal/verify" && test.Name == "TestElevenMinutes" && test.Phase == "test" && test.Status == "passed" {
			return
		}
	}
	t.Fatalf("missing completed eleven-minute test evidence: %+v", r.TestResults)
}

func TestVerifyCheckCutsOffTwentyOneMinutesWithNames(t *testing.T) {
	r, observed, started := v498TimedRun(t, true)
	v498Budgets(t, observed, started, 60*time.Minute, 55*time.Minute)
	if r.Passed() || !r.TimedOut || r.DeadlineExceeded || r.Interrupted {
		t.Fatalf("want shared test-phase timeout before whole-run deadline: %+v\n%s", r, r.Output)
	}
	want := map[string]bool{"TestZZUnstarted": true}
	for i := 0; i < 8; i++ {
		want[fmt.Sprintf("TestSlow%02d", i)] = true
	}
	for _, test := range r.TestResults {
		if test.Package != "github.com/gitdek/invariant/internal/verify" || test.Phase != "test" || !want[test.Name] {
			continue
		}
		if test.Name == "TestZZUnstarted" && test.Status != "not_run" {
			t.Errorf("queued test has status %q, want not_run", test.Status)
		}
		if strings.HasPrefix(test.Name, "TestSlow") && test.Status != "running" && test.Status != "failed" {
			t.Errorf("cut-off test %s has status %q", test.Name, test.Status)
		}
		if !strings.Contains(r.Summary(), test.Package+"/"+test.Name) {
			t.Errorf("summary lost unfinished test %s/%s: %s", test.Package, test.Name, r.Summary())
		}
		delete(want, test.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing unfinished test records: %v\n%s", want, r.Summary())
	}
}

type v498Agent struct {
	plan    *Plan
	sandbox Sandbox
	jobs    []synth.Job
	starts  []time.Time
	results []Result
}

func (*v498Agent) Name() string { return "verify-budget-fixture" }

func (a *v498Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	a.jobs = append(a.jobs, job)
	path := filepath.Join(job.Workspace, a.plan.Files[0])
	if err := os.WriteFile(path, []byte("package fixture\n\nfunc Added() {}\n"), 0o644); err != nil {
		return synth.Usage{}, err
	}
	sandbox := a.sandbox
	for i, arg := range job.GateServer {
		if i+1 < len(job.GateServer) && arg == "-check-queue" {
			sandbox.Checks = &CheckClient{Address: job.GateServer[i+1]}
		}
	}
	for i, arg := range job.GateServer {
		if i+1 < len(job.GateServer) && arg == "-check-build" && sandbox.Checks != nil {
			sandbox.Checks.Build = job.GateServer[i+1]
		}
	}
	a.starts = append(a.starts, time.Now())
	r, err := sandbox.Run(ctx, job.Workspace, a.plan)
	a.results = append(a.results, r)
	if err != nil {
		return synth.Usage{}, err
	}
	if ctx.Err() != nil {
		return synth.Usage{}, ctx.Err()
	}
	if !r.Passed() {
		return synth.Usage{}, fmt.Errorf("agent's test call failed: %s", r.Summary())
	}
	return synth.Usage{Outcome: "success"}, nil
}

func v498Builder(t *testing.T, dir string, queue *CheckQueue, budget time.Duration) (Builder, *v498Agent, string) {
	t.Helper()
	root := t.TempDir()
	p := &Plan{Name: "Check a fixture", Summary: "Add a fixture function.", Files: []string{dir + "/code.go"},
		Tests:   []Test{{Name: "TestBudgetFixture", File: dir + "/fixture_accept_test.go", Says: "The fixture succeeds."}},
		Sources: map[string]string{dir + "/fixture_accept_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestBudgetFixture(t *testing.T) {}\n"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	lock := Lock{Ratified: Ratification{By: "gitdek", At: "2026-11-03T15:00:00Z", Issue: 498,
		Comment: "https://github.com/gitdek/invariant/issues/498#issuecomment-1", Proposal: p.Hash()}, Plan: *p}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
	s397Write(t, filepath.Join(root, "go.mod"), "module github.com/gitdek/invariant\n\ngo 1.27.1\n", 0o644)
	s := Sandbox{Image: "fixture", ModCache: t.TempDir()}
	a := &v498Agent{plan: p, sandbox: s}
	return Builder{Backend: a, Reviewer: &q458Reviewer{}, Binary: "invariant", TestRuns: 4, Timeout: budget, Checks: queue, Sandbox: s}, a, root
}

func TestVerifyBuildSharesBudgetsWithoutRenewingExecutionTime(t *testing.T) {
	record := v498Spy(t)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	ctx := v498Watchdog(t)
	for _, tc := range []struct {
		name   string
		dir    string
		queued bool
		budget time.Duration
	}{
		{"direct_verify", "internal/verify", false, 90 * time.Minute},
		{"queued_verify", "internal/verify", true, 90 * time.Minute},
		{"direct_uncut_verify", "internal/verify", false, 0},
		{"queued_uncut_verify", "internal/verify", true, 0},
		{"ordinary_direct", "internal/plumbing", false, 90 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q *CheckQueue
			if tc.queued {
				q = queue
			}
			if err := os.WriteFile(record, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			b, a, root := v498Builder(t, tc.dir, q, tc.budget)
			r, err := b.Build(ctx, root, 498, t.TempDir())
			if err != nil || r == nil || !r.Built() {
				t.Fatalf("fixture build: %+v, %v", r, err)
			}
			observations := v498Read(t, record)
			if len(observations) != 2 || len(a.jobs) != 1 || len(a.results) != 1 || !a.results[0].Passed() {
				t.Fatalf("want one agent test call and one final check: observations=%+v, jobs=%d, results=%+v", observations, len(a.jobs), a.results)
			}
			run, phase := 60*time.Minute, 55*time.Minute
			if tc.dir == "internal/plumbing" {
				run, phase = 12*time.Minute, 10*time.Minute
			}
			for i, o := range observations {
				// The stand-in observes immediately after preparation. Allow
				// five seconds for filesystem work on a loaded test host.
				remaining := time.Duration((o.Deadline - o.Observed) * float64(time.Second))
				if o.Tests != phase.Seconds() || remaining > run+time.Second || remaining < run-5*time.Second {
					t.Errorf("check %d got test budget %.0fs and %s left, want %s inside %s", i+1, o.Tests, remaining, phase, run)
				}
			}
			tool := a.jobs[0].ToolTimeout
			if tc.queued {
				want := tc.budget
				if want <= 0 {
					want = -1
				}
				if tool != want {
					t.Errorf("queued tool timer = %s, want existing build allowance %s", tool, want)
				}
			} else if tc.dir == "internal/plumbing" {
				if tool != 0 {
					t.Errorf("ordinary direct build changed its default tool timer: %s", tool)
				}
			} else if tool >= 0 && tool < run+SandboxStartupAllowance+SandboxCleanupAllowance {
				t.Errorf("direct verify tool timer %s cannot deliver a %s check with startup and cleanup reserves", tool, run)
			}
		})
	}
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprintf("execution_allowance_queued_%t", queued), func(t *testing.T) {
			t.Setenv("V498_HANG", "1")
			var q *CheckQueue
			if queued {
				q = queue
			}
			b, _, root := v498Builder(t, "internal/verify", q, 1500*time.Millisecond)
			started := time.Now()
			r, err := b.Build(ctx, root, 498, t.TempDir())
			if !errors.Is(err, context.DeadlineExceeded) || r == nil || r.Built() {
				t.Fatalf("active check must consume the build allowance: %+v, %v", r, err)
			}
			if elapsed := time.Since(started); elapsed > 8*time.Second {
				t.Fatalf("short build allowance was renewed by the longer check: %s", elapsed)
			}
		})
	}
}
