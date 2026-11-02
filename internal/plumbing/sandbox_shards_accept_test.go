package plumbing

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The operational API added by #397 is deliberately small:
//
// Sandbox.Timeout and Sandbox.ShardTimeout are time.Duration overrides which
// can shorten, but never extend, the production limits (zero uses the default).
// Timeout covers the entire Run, starting before checkout preparation.
// ShardTimeout bounds the whole shared-queue test phase, starting when its
// eight workers start; it does not restart for each test they pull. Every
// invocation is bounded by the time left in that phase and in the whole run.
// Result.DeadlineExceeded identifies the sandbox's own deadline. That deadline
// returns a failed Result, nil, so the existing test tool delivers the report.
// Result.TestResults contains records with Package (Go import path), Name
// (including subtest suffixes), Phase ("test" or "accept"), Status ("passed",
// "failed", "skipped", "running", or "not_run"), and Shard (1..8 in the test
// phase). A shard timeout may leave its interrupted test "running" while the
// shard is reported as failed. Results are keyed by phase, package and name.
// Summary preserves failure/deadline identities even when Output is tailed.
//
// These tests execute the real sandbox entry script and real Go fixture tests.
// Only Docker is replaced, by a local process-group boundary. They require
// neither Docker nor a network and never execute repository code on the host
// outside the same boundary already used for ordinary Go unit tests.

type s397Fixture struct {
	root, observe, control, cache, mod, bin, realGo string
	plan                                            *Plan
}

func s397Write(t *testing.T, name, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func s397New(t *testing.T) *s397Fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the sandbox and its local Docker stand-in use POSIX processes")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("the pinned sandbox runner and this fixture need python3: ", err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("GOCACHE")
	if cache == "" || cache == "off" {
		out, err := exec.Command(goBin, "env", "GOCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		cache = strings.TrimSpace(string(out))
	}
	// Every fixture is a separate build: reuse compiler objects, but not the
	// elapsed-time history that may reorder another fixture's test queue.
	cache = s397IsolatedCache(t, cache)
	f := &s397Fixture{root: t.TempDir(), observe: t.TempDir(), control: t.TempDir(),
		cache: cache, mod: t.TempDir(), bin: t.TempDir(), realGo: goBin,
		plan: &Plan{Name: "shard fixture", Summary: "Run every fixture test and report its result.", Sources: map[string]string{}}}
	s397Write(t, filepath.Join(f.root, "go.mod"), "module example.com/shards397\n\ngo 1.27.1\n", 0o644)
	s397Write(t, filepath.Join(f.bin, "docker"), s397Docker, 0o755)
	s397Write(t, filepath.Join(f.bin, "go"), s397Go, 0o755)
	t.Setenv("S397_CONTROL", f.control)
	t.Setenv("S397_REAL_GO", f.realGo)
	t.Setenv("PATH", f.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *s397Fixture) pkg(t *testing.T, dir, tests string) {
	t.Helper()
	s397Write(t, filepath.Join(f.root, dir, "code.go"), "package "+dir+"\n", 0o644)
	f.plan.Files = append(f.plan.Files, dir+"/code.go")
	if tests != "" {
		formatted, err := format.Source([]byte(tests))
		if err != nil {
			t.Fatal(err)
		}
		s397Write(t, filepath.Join(f.root, dir, "code_test.go"), string(formatted), 0o644)
	}
}

func (f *s397Fixture) accept(t *testing.T, dir, name, body string) {
	t.Helper()
	file := dir + "/pinned_accept_test.go"
	formatted, err := format.Source([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	body = string(formatted)
	s397Write(t, filepath.Join(f.root, file), body, 0o644)
	f.plan.Tests = append(f.plan.Tests, Test{Name: name, File: file, Says: "The fixture's pinned check passes."})
	f.plan.Sources[file] = body
}

func (f *s397Fixture) contract(t *testing.T, blockSecond bool) {
	t.Helper()
	f.accept(t, "contract", "TestRatified", fmt.Sprintf(`package contract
import ("os"; "strings"; "testing"; "time")
func TestRatified(t *testing.T) {
  p := %q
  before, _ := os.ReadFile(p)
  out, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { t.Fatal(err) }; if _, err = out.WriteString("run\n"); err != nil { t.Fatal(err) }; out.Close()
  if %t && strings.Count(string(before), "run\n") > 0 { for { time.Sleep(time.Second) } }
}
`, filepath.Join(f.observe, "contract-runs"), blockSecond))
}

// Audit real compiler invocations and real test processes. TestMain records
// discovery as well as execution, so rebuilding for go test -list or for the
// acceptance phase cannot hide behind a warm Go build cache. Relative testdata
// also makes executing a compiled binary from the package directory observable.
func (f *s397Fixture) audit(t *testing.T, pkgs ...string) {
	t.Helper()
	s397Write(t, filepath.Join(f.control, "audit"), "enabled", 0o644)
	for _, pkg := range pkgs {
		src := fmt.Sprintf(`package %s
import ("crypto/sha256"; "encoding/hex"; "encoding/json"; "fmt"; "io"; "os"; "path/filepath"; "strings"; "testing")
func TestMain(m *testing.M) {
  if os.Getenv("S397_AUDIT") == "1" {
    fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
    data, err := os.ReadFile("testdata/package.txt"); if err != nil { fail(err) }
    if string(data) != %q { fail(fmt.Errorf("test binary ran outside its package directory")) }
    exe, err := os.Executable(); if err != nil { fail(err) }
    binary, err := os.Open(exe); if err != nil { fail(err) }
    hash := sha256.New(); if _, err := io.Copy(hash, binary); err != nil { fail(err) }; binary.Close()
    phase := "run"
    for _, arg := range os.Args[1:] { if arg == "-test.list" || strings.HasPrefix(arg, "-test.list=") { phase = "list" } }
    event, err := json.Marshal(map[string]string{"kind": "binary", "package": %q, "phase": phase, "executable": exe, "digest": hex.EncodeToString(hash.Sum(nil))}); if err != nil { fail(err) }
    log, err := os.OpenFile(filepath.Join(os.Getenv("S397_CONTROL"), "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); if err != nil { fail(err) }
    if _, err := log.Write(append(event, '\n')); err != nil { fail(err) }; log.Close()
  }
  os.Exit(m.Run())
}
`, pkg, pkg, "example.com/shards397/"+pkg)
		formatted, err := format.Source([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		s397Write(t, filepath.Join(f.root, pkg, "audit_test.go"), string(formatted), 0o644)
		s397Write(t, filepath.Join(f.root, pkg, "testdata", "package.txt"), pkg, 0o644)
	}
}

func (f *s397Fixture) assertBuiltOnce(t *testing.T, pkgs ...string) {
	t.Helper()
	log, err := os.Open(filepath.Join(f.control, "events.jsonl"))
	if err != nil {
		t.Fatal("missing real build and binary observations: ", err)
	}
	defer log.Close()
	type event struct {
		Kind        string   `json:"kind"`
		ID          int      `json:"id"`
		Packages    []string `json:"packages"`
		CompileOnly bool     `json:"compile_only"`
		Code        int      `json:"code"`
		Package     string   `json:"package"`
		Phase       string   `json:"phase"`
		Executable  string   `json:"executable"`
		Digest      string   `json:"digest"`
	}
	builds, completed := map[string]int{}, map[string]bool{}
	active := map[int]bool{}
	binaries := map[string]event{}
	started := false
	decoder := json.NewDecoder(log)
	for {
		var got event
		if err := decoder.Decode(&got); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal("build observation: ", err)
		}
		switch got.Kind {
		case "build_start":
			if len(active) != 0 {
				t.Errorf("package compilations overlapped: %v started with %v still active", got.Packages, active)
			}
			if started {
				t.Errorf("compilation began after test execution started: %v", got.Packages)
			}
			if !got.CompileOnly {
				t.Errorf("go test compiled and executed %v instead of preparing reusable test binaries", got.Packages)
			}
			active[got.ID] = true
			for _, pkg := range got.Packages {
				builds[pkg]++
			}
		case "build_done":
			if !active[got.ID] || got.Code != 0 {
				t.Errorf("compilation did not finish successfully: %+v", got)
			}
			delete(active, got.ID)
			for _, pkg := range got.Packages {
				completed[pkg] = got.Code == 0
			}
		case "binary":
			if !completed[got.Package] {
				t.Errorf("%s binary ran before its compilation finished", got.Package)
			}
			if got.Phase == "run" {
				started = true
				if len(active) != 0 {
					t.Errorf("%s test execution overlapped compilation", got.Package)
				}
				for _, pkg := range pkgs {
					if !completed["example.com/shards397/"+pkg] {
						t.Errorf("%s tests ran before %s's binary was built", got.Package, pkg)
					}
				}
			}
			if prior, ok := binaries[got.Package]; ok && (prior.Executable != got.Executable || prior.Digest != got.Digest) {
				t.Errorf("%s did not reuse the same binary for discovery, shards and acceptance: %+v then %+v", got.Package, prior, got)
			}
			binaries[got.Package] = got
		default:
			t.Errorf("unknown build observation: %+v", got)
		}
	}
	if !started || len(active) != 0 {
		t.Errorf("incomplete build/run observations: tests started=%v, active builds=%v", started, active)
	}
	for pkg, count := range builds {
		if count != 1 {
			t.Errorf("%s compiled %d times, want once across discovery, shards and acceptance", pkg, count)
		}
	}
	for _, pkg := range pkgs {
		name := "example.com/shards397/" + pkg
		if builds[name] != 1 || !completed[name] || binaries[name].Executable == "" {
			t.Errorf("%s must build one reusable test binary: builds=%d, completed=%v, binary=%+v", name, builds[name], completed[name], binaries[name])
		}
	}
}

// Warm compilation outside the measured short fixture deadline, using the
// same cache and flags the sandbox uses. This runs no test bodies.
func (f *s397Fixture) warm(t *testing.T, pkgs ...string) {
	t.Helper()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := append([]string{"test", "-run", "^$", "-count=1", "-vet=off"}, pkgs...)
	cmd := exec.CommandContext(ctx, f.realGo, args...)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "GOCACHE="+f.cache, "GOMODCACHE="+f.mod, "GOTOOLCHAIN=local", "GOPROXY=off", "CGO_ENABLED=0", "GOFLAGS=-mod=readonly -p=1", "GOMAXPROCS=2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v\n%s", err, out)
	}
}

func (f *s397Fixture) run(t *testing.T, timeout, shardTimeout time.Duration) Result {
	t.Helper()
	s := Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache, Timeout: timeout, ShardTimeout: shardTimeout}
	start := time.Now()
	r, err := s.Run(context.Background(), f.root, f.plan)
	if err != nil {
		t.Fatalf("Sandbox.Run must deliver a check result: %v\n%s", err, r.Output)
	}
	if elapsed := time.Since(start); elapsed > timeout+5*time.Second {
		t.Fatalf("fixture deadline %s returned after %s", timeout, elapsed)
	}
	return r
}

func s397Status(t *testing.T, r Result, pkg, name, phase, want string) int {
	t.Helper()
	found, shard := 0, 0
	for _, got := range r.TestResults {
		if got.Package == "example.com/shards397/"+pkg && got.Name == name && got.Phase == phase {
			found++
			shard = got.Shard
			if got.Status != want {
				t.Errorf("%s %s/%s: status %q, want %q", phase, pkg, name, got.Status, want)
			}
		}
	}
	if found != 1 {
		t.Fatalf("%s %s/%s has %d final records, want one; records: %+v\n%s", phase, pkg, name, found, r.TestResults, r.Output)
	}
	return shard
}

func s397Summary(t *testing.T, r Result, words ...string) {
	t.Helper()
	summary := strings.ToLower(r.Summary())
	for _, word := range words {
		if !strings.Contains(summary, strings.ToLower(word)) {
			t.Errorf("the delivered summary lacks %q:\n%s", word, r.Summary())
		}
	}
}

func TestSandboxShardsEveryTopLevelTestExactlyOnce(t *testing.T) {
	f := s397New(t)
	var src strings.Builder
	fmt.Fprintf(&src, `package alpha
import ("fmt"; "os"; "path/filepath"; "strconv"; "testing"; "time")
func visit(name string) {
  dir := %q
  out, err := os.OpenFile(filepath.Join(dir, "alpha-"+name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
  if err != nil { panic(err) }; fmt.Fprint(out, os.Getpid()); out.Close()
  if err := os.WriteFile(filepath.Join(dir, "worker-"+strconv.Itoa(os.Getpid())), nil, 0600); err != nil { panic(err) }
  deadline := time.Now().Add(20*time.Second)
  for { workers, _ := filepath.Glob(filepath.Join(dir, "worker-*")); if len(workers) >= 8 { break }; if time.Now().After(deadline) { panic("eight test shards never ran together") }; time.Sleep(10*time.Millisecond) }
}
func Example() { visit("Example"); fmt.Println("hello")
// Output: hello
}
func FuzzSeed(f *testing.F) { f.Add(1); f.Fuzz(func(t *testing.T, n int) { visit("FuzzSeed") }) }
func TestChildren(t *testing.T) { visit("TestChildren"); t.Run("first", func(t *testing.T) { visit("child-first") }); t.Run("second", func(t *testing.T) { visit("child-second") }) }
`, f.observe)
	alpha := []string{"Example", "FuzzSeed", "TestChildren", "TestNewlyAdded"}
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf("TestCase%02d", i)
		alpha = append(alpha, name)
		fmt.Fprintf(&src, "func %s(t *testing.T) { visit(%q) }\n", name, name)
	}
	src.WriteString("func TestNewlyAdded(t *testing.T) { visit(\"TestNewlyAdded\") }\n")
	f.pkg(t, "alpha", src.String())
	f.pkg(t, "beta", fmt.Sprintf(`package beta
import ("fmt"; "os"; "path/filepath"; "testing")
func record(t *testing.T, name string) { p := filepath.Join(%q, "beta-"+name); f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); if err != nil { t.Fatal(err) }; fmt.Fprint(f, os.Getpid()); f.Close() }
func TestCase00(t *testing.T) { record(t, "TestCase00") }
func TestCase01(t *testing.T) { record(t, "TestCase01") }
`, f.observe))
	f.pkg(t, "empty", "")
	f.contract(t, false)
	f.audit(t, "alpha", "beta", "contract")
	f.warm(t)
	r := f.run(t, 60*time.Second, 40*time.Second)
	if !r.Passed() {
		t.Fatalf("fixture failed: %s\n%s", r.Summary(), r.Output)
	}
	f.assertBuiltOnce(t, "alpha", "beta", "contract")
	sort.Strings(alpha)
	all := append([]string(nil), alpha...)
	all = append(all, "beta:TestCase00", "beta:TestCase01", "contract:TestRatified")
	workers := map[int]bool{}
	processes := map[string]bool{}
	for _, entry := range all {
		pkg, name := "alpha", entry
		if p := strings.SplitN(entry, ":", 2); len(p) == 2 {
			pkg, name = p[0], p[1]
		}
		shard := s397Status(t, r, pkg, name, "test", "passed")
		if shard < 1 || shard > 8 {
			t.Errorf("%s/%s has worker %d, want one of eight queue workers", pkg, name, shard)
		}
		workers[shard] = true
		if pkg == "contract" {
			continue
		}
		pid, err := os.ReadFile(filepath.Join(f.observe, pkg+"-"+name))
		if err != nil {
			t.Fatal(err)
		}
		if n, err := strconv.Atoi(string(pid)); err != nil || n <= 0 {
			t.Fatalf("%s/%s did not record a real process ID: %q", pkg, name, pid)
		}
		if processes[string(pid)] {
			t.Errorf("top-level test %s/%s reused another test's process %s", pkg, name, pid)
		}
		processes[string(pid)] = true
	}
	if len(workers) != 8 {
		t.Errorf("ran with %d workers, want eight concurrent queue workers", len(workers))
	}
	for _, name := range []string{"first", "second"} {
		s397Status(t, r, "alpha", "TestChildren/"+name, "test", "passed")
		if _, err := os.Stat(filepath.Join(f.observe, "alpha-child-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	s397Status(t, r, "contract", "TestRatified", "accept", "passed")
	count, err := os.ReadFile(filepath.Join(f.observe, "contract-runs"))
	if err != nil || string(count) != "run\nrun\n" {
		t.Errorf("acceptance runs = %q, %v; want once in a shard and once in the pinned acceptance phase", count, err)
	}
	var args []string
	b, err := os.ReadFile(filepath.Join(f.control, "args.json"))
	if err != nil || json.Unmarshal(b, &args) != nil {
		t.Fatalf("Docker argument record: %s, %v", b, err)
	}
	for key, value := range map[string]string{"--memory": "4g", "--pids-limit": "1024", "--network": "none", "--hostname": "localhost"} {
		found := false
		for i := 0; i+1 < len(args); i++ {
			found = found || (args[i] == key && args[i+1] == value)
		}
		if !found {
			t.Errorf("container arguments %q lack %s %s", args, key, value)
		}
	}
	env := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-e" || args[i] == "--env" {
			key, value, _ := strings.Cut(args[i+1], "=")
			env[key] = value
		}
	}
	if env["GOMAXPROCS"] != "2" {
		t.Errorf("container GOMAXPROCS = %q, want 2", env["GOMAXPROCS"])
	}
	for _, flag := range []string{"-mod=readonly", "-p=1"} {
		found := false
		for _, got := range strings.Fields(env["GOFLAGS"]) {
			found = found || got == flag
		}
		if !found {
			t.Errorf("container GOFLAGS = %q, missing %s", env["GOFLAGS"], flag)
		}
	}
}

func TestSandboxNamesFailuresFromEveryShard(t *testing.T) {
	for _, kind := range []string{"early_failure", "failure", "panic", "shard_timeout", "build_error"} {
		t.Run(kind, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "good", "package good\nimport \"testing\"\nfunc TestHealthy(t *testing.T) {}\n")
			body := "t.Fatal(\"fixture failure\")"
			imports := "\"testing\""
			if kind == "panic" {
				body = "panic(\"fixture panic\")"
			}
			if kind == "shard_timeout" {
				body, imports = "time.Sleep(60*time.Second)", "\"testing\"; \"time\""
			}
			var src strings.Builder
			fmt.Fprintf(&src, "package bad\nimport (%s)\n", imports)
			if kind != "build_error" && kind != "early_failure" {
				// A test late in discovery must fail the run just as an early one does.
				for i := 0; i < 7; i++ {
					fmt.Fprintf(&src, "func TestA%02d(t *testing.T) {}\n", i)
				}
			}
			fmt.Fprintf(&src, "func TestBroken(t *testing.T) { %s }\n", body)
			f.pkg(t, "bad", src.String())
			f.contract(t, false)
			if kind == "build_error" {
				s397Write(t, filepath.Join(f.root, "bad", "code.go"), "package bad\nvar Broken = missingSymbol397\n", 0o644)
				f.warm(t, "./good", "./contract")
			} else {
				f.warm(t)
			}
			shardTimeout := 20 * time.Second
			if kind == "shard_timeout" {
				shardTimeout = time.Second
			}
			r := f.run(t, 35*time.Second, shardTimeout)
			if r.Passed() || r.Tests || r.DeadlineExceeded {
				t.Errorf("%s: Passed=%v Tests=%v DeadlineExceeded=%v\n%s", kind, r.Passed(), r.Tests, r.DeadlineExceeded, r.Output)
			}
			s397Status(t, r, "good", "TestHealthy", "test", "passed")
			if !r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
				t.Errorf("a failed shard prevented independent acceptance checks: %s", r.Output)
			}
			s397Summary(t, r, "bad")
			if kind != "build_error" {
				s397Summary(t, r, "TestBroken", "shard")
			}
			switch kind {
			case "early_failure", "failure":
				if shard := s397Status(t, r, "bad", "TestBroken", "test", "failed"); shard < 1 || shard > 8 {
					t.Errorf("failed test belongs to worker %d, want one of eight queue workers", shard)
				}
			case "shard_timeout":
				summary := strings.ToLower(r.Summary())
				if !strings.Contains(summary, "timeout") && !strings.Contains(summary, "timed out") {
					t.Errorf("summary must identify the shard timeout: %s", r.Summary())
				}
			case "build_error":
				s397Summary(t, r, "build")
			}
		})
	}
}

// A fixed round-robin strands Test08Release behind Test00Busy. A shared
// queue lets any free worker run it, while the busy worker is still waiting.
// This checks balancing without assuming a worker ID or racing elapsed times.
func TestSandboxWorkersPullFromSharedQueue(t *testing.T) {
	f := s397New(t)
	var src strings.Builder
	fmt.Fprintf(&src, `package alpha
import ("os"; "path/filepath"; "testing"; "time")
var observations = %q
func Test00Busy(t *testing.T) {
  os.WriteFile(filepath.Join(observations, "busy"), nil, 0600)
  until := time.Now().Add(10*time.Second)
  for {
    if _, err := os.Stat(filepath.Join(observations, "released")); err == nil { return }
    if time.Now().After(until) { t.Fatal("queued work was stranded behind a busy worker") }
    time.Sleep(10*time.Millisecond)
  }
}
`, f.observe)
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&src, "func Test%02dQuick(t *testing.T) {}\n", i)
	}
	src.WriteString(`func Test08Release(t *testing.T) {
  until := time.Now().Add(10*time.Second)
  for {
    if _, err := os.Stat(filepath.Join(observations, "busy")); err == nil { break }
    if time.Now().After(until) { t.Fatal("busy test never started") }
    time.Sleep(10*time.Millisecond)
  }
  if err := os.WriteFile(filepath.Join(observations, "released"), nil, 0600); err != nil { t.Fatal(err) }
}
`)
	f.pkg(t, "alpha", src.String())
	f.contract(t, false)
	f.warm(t)
	r := f.run(t, 35*time.Second, 20*time.Second)
	if !r.Passed() {
		t.Fatalf("a free worker must take the next test while another worker is busy:\n%s\n%s", r.Summary(), r.Output)
	}
	busy := s397Status(t, r, "alpha", "Test00Busy", "test", "passed")
	release := s397Status(t, r, "alpha", "Test08Release", "test", "passed")
	if busy == release {
		t.Errorf("waiting test and its queued release both used worker %d", busy)
	}
}

func TestSandboxQueueBudgetDoesNotRestartPerTest(t *testing.T) {
	f := s397New(t)
	var src strings.Builder
	src.WriteString("package alpha\nimport (\"testing\"; \"time\")\n")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&src, "func TestWork%03d(t *testing.T) { time.Sleep(200*time.Millisecond) }\n", i)
	}
	f.pkg(t, "alpha", src.String())
	f.contract(t, false)
	f.warm(t)
	r := f.run(t, 30*time.Second, time.Second)
	if r.Passed() || r.Tests || r.DeadlineExceeded {
		t.Fatalf("the shared one-second test budget must expire before the whole-run deadline: %+v\n%s", r, r.Output)
	}
	passed, pending := 0, 0
	for _, result := range r.TestResults {
		if result.Package == "example.com/shards397/alpha" && result.Phase == "test" {
			if result.Status == "passed" {
				passed++
			}
			if result.Status == "not_run" {
				pending++
			}
		}
	}
	if passed == 0 || pending == 0 {
		t.Errorf("want completed and unstarted tests after the shared budget expires; passed=%d pending=%d", passed, pending)
	}
	if !r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
		t.Errorf("the touched-test phase used up the acceptance phase's remaining whole-run budget:\n%s", r.Output)
	}
	summary := strings.ToLower(r.Summary())
	if !strings.Contains(summary, "timeout") && !strings.Contains(summary, "timed out") {
		t.Errorf("the summary must identify the touched-test phase timeout: %s", r.Summary())
	}
}

func TestSandboxDeadlineReturnsPartialResults(t *testing.T) {
	for _, phase := range []string{"tests", "vet", "acceptance"} {
		t.Run(phase, func(t *testing.T) {
			f := s397New(t)
			var src strings.Builder
			fmt.Fprintf(&src, `package work
import ("os"; "testing"; "time")
func Test00Passed(t *testing.T) {}
func Test01Failed(t *testing.T) { if %t { t.Fatal("before deadline") } }
func Test02Running(t *testing.T) { if %t { for { os.WriteFile(%q, []byte(time.Now().String()), 0600); time.Sleep(30*time.Millisecond) } } }
`, phase == "tests", phase == "tests", filepath.Join(f.observe, "heartbeat"))
			for i := 3; i <= 9; i++ {
				fmt.Fprintf(&src, "func Test%02dRunning(t *testing.T) { if %t { for { time.Sleep(time.Second) } } }\n", i, phase == "tests")
			}
			src.WriteString("func Test10Queued(t *testing.T) {}\n")
			f.pkg(t, "work", src.String())
			f.contract(t, phase == "acceptance")
			f.warm(t)
			if phase == "vet" {
				t.Setenv("S397_HANG_VET", "1")
			}
			r := f.run(t, 8*time.Second, 30*time.Second)
			if !r.DeadlineExceeded || r.Passed() {
				t.Fatalf("deadline must return a failed partial result: %+v\n%s", r, r.Output)
			}
			s397Summary(t, r, "deadline")
			if r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
				t.Error("a test which passed in a shard cannot satisfy an unfinished acceptance phase")
			}
			if _, err := os.Stat(filepath.Join(f.control, "removed")); err != nil {
				t.Fatal("the timed-out container was not removed: ", err)
			}
			switch phase {
			case "tests":
				s397Status(t, r, "work", "Test00Passed", "test", "passed")
				s397Status(t, r, "work", "Test01Failed", "test", "failed")
				s397Status(t, r, "work", "Test02Running", "test", "running")
				s397Status(t, r, "work", "Test10Queued", "test", "not_run")
				s397Summary(t, r, "work", "Test00Passed", "passed", "Test01Failed", "failed", "Test02Running", "running", "Test10Queued")
				before, err := os.ReadFile(filepath.Join(f.observe, "heartbeat"))
				if err != nil {
					t.Fatal("hanging fixture never started: ", err)
				}
				time.Sleep(150 * time.Millisecond)
				after, _ := os.ReadFile(filepath.Join(f.observe, "heartbeat"))
				if string(before) != string(after) {
					t.Error("a fixture process kept running after the sandbox returned")
				}
			case "vet":
				if r.Vet {
					t.Error("vet never finished")
				}
				s397Summary(t, r, "vet")
			case "acceptance":
				if !r.Tests || !r.Vet {
					t.Errorf("completed checks were lost: %s", r.Output)
				}
				s397Status(t, r, "contract", "TestRatified", "accept", "running")
				s397Summary(t, r, "accept", "contract", "TestRatified", "running")
			}
		})
	}
}

func TestSandboxKeepsAcceptanceResultsByPackage(t *testing.T) {
	f := s397New(t)
	for _, pkg := range []string{"left", "right"} {
		f.pkg(t, pkg, "")
		body := ""
		if pkg == "right" {
			body = "t.Fatal(\"right failed\")"
		}
		f.accept(t, pkg, "TestSameName", "package "+pkg+"\nimport \"testing\"\nfunc TestSameName(t *testing.T) { "+body+" }\n")
	}
	f.warm(t)
	r := f.run(t, 30*time.Second, 20*time.Second)
	if r.Passed() || !r.Accepted["left/pinned_accept_test.go:TestSameName"] || r.Accepted["right/pinned_accept_test.go:TestSameName"] {
		t.Fatalf("package-qualified acceptance results: %+v\n%s", r.Accepted, r.Output)
	}
	s397Status(t, r, "left", "TestSameName", "accept", "passed")
	s397Status(t, r, "right", "TestSameName", "accept", "failed")
	s397Summary(t, r, "right", "TestSameName")
}

// Every go test invocation compiles a package, even when Go can reuse cached
// compiler output. Record its semantic package targets and completion around
// the real command; accept either flag spelling and package patterns, without
// depending on how the sandbox formats its shell script.
const s397Go = `#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
a = sys.argv[1:]
real = os.environ["S397_REAL_GO"]
control = pathlib.Path(os.environ["S397_CONTROL"])
prefix = []
if a and a[0] == "-C":
    prefix, a = a[:2], a[2:]
elif a and a[0].startswith("-C="):
    prefix, a = a[:1], a[1:]
if a and a[0] == "vet" and os.environ.get("S397_HANG_VET") == "1":
    (control / "vet-started").write_text("started")
    os.execvp("sleep", ["sleep", "60"])
if not a or a[0] != "test" or os.environ.get("S397_AUDIT") != "1":
    os.execv(real, [real] + sys.argv[1:])
value_flags = {
    "-o", "-p", "-run", "-list", "-timeout", "-count", "-parallel", "-cpu", "-shuffle",
    "-vet", "-exec", "-bench", "-benchtime", "-fuzz", "-fuzztime", "-fuzzminimizetime",
    "-asmflags", "-gcflags", "-ldflags", "-gccgoflags", "-tags", "-mod", "-modfile",
    "-overlay", "-pkgdir", "-toolexec", "-buildmode", "-compiler", "-covermode",
    "-coverpkg", "-coverprofile", "-blockprofile", "-blockprofilerate", "-cpuprofile",
    "-memprofile", "-memprofilerate", "-mutexprofile", "-mutexprofilefraction",
    "-outputdir", "-trace"
}
patterns, compile_only, i = [], False, 1
while i < len(a):
    arg = a[i]
    if arg == "-args": break
    if arg.startswith("-"):
        flag, equal, value = arg.partition("=")
        if flag == "-c": compile_only = not equal or value == "true"
        i += 2 if flag in value_flags and not equal else 1
    else:
        patterns.append(arg); i += 1
listed = subprocess.run([real] + prefix + ["list", "-f", "{{.ImportPath}}"] + patterns,
                        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
if listed.returncode:
    sys.stderr.write(listed.stderr); sys.exit(listed.returncode)
packages = listed.stdout.split()
def record(kind, **extra):
    event = dict(kind=kind, id=os.getpid(), packages=packages, **extra)
    fd = os.open(control / "events.jsonl", os.O_CREAT | os.O_APPEND | os.O_WRONLY, 0o600)
    try: os.write(fd, (json.dumps(event) + "\n").encode())
    finally: os.close(fd)
record("build_start", compile_only=compile_only)
code = subprocess.run([real] + sys.argv[1:]).returncode
record("build_done", code=code)
sys.exit(code if code >= 0 else 128-code)
`

// The stand-in honors the existing sh -c entry contract, real environment and
// mounts, and Docker's important cancellation property: rm kills every process
// belonging to the container. It never supplies fabricated test output.
const s397Docker = `#!/usr/bin/env python3
import json, os, pathlib, signal, subprocess, sys
a = sys.argv[1:]
control = pathlib.Path(os.environ["S397_CONTROL"])
if a and a[0] == "rm":
    (control / "removed").write_text(" ".join(a))
    try:
        os.killpg(int((control / "group").read_text()), signal.SIGKILL)
    except (ProcessLookupError, FileNotFoundError):
        pass
    sys.exit(0)
if not a or a[0] != "run":
    sys.exit(2)
(control / "args.json").write_text(json.dumps(a))
env = dict(os.environ)
if (control / "audit").exists(): env["S397_AUDIT"] = "1"
mounts = {}
i = 1
while i < len(a):
    if a[i] in ("-e", "--env"):
        k, _, v = a[i+1].partition("="); env[k] = v; i += 2
    elif a[i] in ("-v", "--volume"):
        parts = a[i+1].split(":"); mounts[parts[1]] = parts[0]; i += 2
    else:
        i += 1
if a[-3:-1] != ["sh", "-c"]:
    sys.exit("sandbox lost its sh -c entry contract")
src = mounts["/src"]
for k, v in list(env.items()):
    if v in mounts: env[k] = mounts[v]
# Paths are shell quoted because the copied checkout may have spaces.
import shlex
script = a[-1].replace("cd /src", "cd " + shlex.quote(src))
p = subprocess.Popen(["sh", "-c", script], cwd=src, env=env, start_new_session=True)
(control / "group").write_text(str(p.pid))
try:
    code = p.wait()
finally:
    try: os.killpg(p.pid, signal.SIGKILL)
    except ProcessLookupError: pass
sys.exit(code if code >= 0 else 128-code)
`
