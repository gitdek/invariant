package plumbing

// #538's host-only production API. These are raw framework observations, NOT
// RepositoryAcceptanceEvidence and NOT an implementation of RunAcceptance.
// Nothing registers a default adapter or enables configured planning admission.
//
// StartGoAcceptance(ctx, part, test, source, executionDigest, prepare) returns
// (*GoAcceptanceRun, error). prepare has signature func(context.Context,
// RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error).
// StartGoAcceptance calls the existing Select before prepare, creates a fresh
// repositoryInvocation, and calls the existing Discover against the prepared
// environment before preparing/launching the producer. The captured source is
// staged verbatim, including CRLF. Only discovery changes Binding.Package.
// A returned run owns any acquired environment even when accompanied by error.
// It exposes Invocation and Process (nil before runtime launch), Wait(ctx)
// (GoAcceptanceStream, error), and Close(ctx) error. Close uses the caller's
// bounded cleanup context, retains prior infrastructure errors, and is safe to
// repeat. The environment owns all prepared/process handles until that Close.
// Before Run or Wait returns, on success, failure or cancellation, the producer
// and transport stop and reap every descendant in the whole process group,
// including compilers and children that closed their diagnostic/event pipes.
// StartGoAcceptance also quiesces any acquired process tree before returning an
// error; a successful asynchronous Start may of course leave its run active.
// Close retains resource ownership and bounded uncanceled cleanup, but must not
// be the first point at which a completed/interrupted run's descendants stop.
//
// GoAcceptanceStream contains Invocation RepositoryAcceptanceInvocation,
// Events []GoFrameworkEvent, and Output RepositoryProcessOutput. An event has
// Name, Parent, Action, Text strings. Actions are start/pause/continue/output/
// pass/fail/skip, emitted directly by testing's lifecycle and logging hooks,
// including when -v=false. Names are runtime T.Name() values, Parent is the
// actual runtime parent (empty for top level); slice order is stream order.
// Text retains framework logs, skips, assertion and panic explanations.
// A decoder may decode the independent protocol, but must not infer outcomes
// from stdout/stderr, go test/test2json output, process exits or runner markers.
// Instrument the pinned testing implementation privately with host-owned Go
// code, preserving acceptance, ordinary production/dependency bytes, build
// constraints and normal/external test behavior; no candidate hooks/overlays.
// Host-only instrumentation must retain every supported captured flag.
// Toolchain-specific hooks validate that they can safely instrument the pinned
// testing sources; otherwise preparation fails, with no textual fallback.
//
// RepositoryProcessTransport embeds RepositoryAcceptanceFileEnvironment and
// adds Stage(ctx, cleanPrivateName string, data []byte) (environmentPath string,
// error) and Prepare(ctx, RepositoryProcessSpec) (RepositoryPreparedProcess,
// error). Stage is private helper storage, not a candidate-source write API.
// RepositoryProcessSpec has Argv []string, Env map[string]string and EventEnv
// string: a nonempty EventEnv asks the transport to supply a writable endpoint
// in that environment variable, separate from stdout/stderr. OS creation and
// environment path/endpoint mapping belong to the transport. A future Docker
// transport must run this identical producer and the later identical collector.
// RepositoryPreparedProcess has Start(ctx) (RepositoryProcess, error) and
// Close(ctx) error; RepositoryProcess has Wait(ctx) (RepositoryProcessOutput,
// error), Snapshot() RepositoryProcessOutput and Close(ctx) error.
// RepositoryProcessOutput has Events, Stdout, Stderr []byte, ExitCode int, and
// Started, Exited, Drained bool. Snapshots are independent copies. The transport
// buffers/drains all three channels concurrently as opaque bytes; no reporter.
// Wait distinguishes ordinary exit (ExitCode, nil error) from infrastructure,
// cancellation or unfinished drain errors; it retains partial output on error.
//
// OpenLocalGoProcessTransport(ctx, LocalGoProcessOptions) returns
// (RepositoryProcessTransport, error). Options are SourceDir, GoExecutable,
// CacheDir strings, Part repository.Part, Invocation, and an optional host-only
// OnStart func(context.Context, []string, int) observer of actual argv and PID
// after each successful OS start (including Run); it cannot supply output.
// It copies safely into
// private storage, stages pinned source with existing staging rules, runs from
// the part directory and uses only the explicitly resolved local Go toolchain.
// CacheDir is a host-owned isolated temporary cache; HOME, GOCACHE, GOMODCACHE,
// GOPATH and temporary storage must never fall back to user locations. Use
// dependencyEnv's offline policy, GOTOOLCHAIN=local, GOWORK=off, GOENV=off,
// host-controlled GOFLAGS, and no inherited hooks or network/install/downloads.
// Standard-library-only fixtures need no dependency installation; this local
// path cannot bypass the existing verified-dependency policy to obtain others.
// Context applies to all reads/copies/validation/processes, with no fresh run
// or test budget: the existing twelve-minute run and one ten-minute test phase,
// shorter caller/preparer limits and cleanup reserve remain the caller's
// authority, with no scheduler added. No mandatory methods are added to
// existing environments.
// Existing #527 fixtures stay discovery-only; this distinct fixture delegates
// all event production and all process management to production code.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type fixture538 struct {
	t                               *testing.T
	root, cache, executable, source string
	part                            repository.Part
	test                            Test
	calls                           [][]string
	prepares                        []RepositoryProcessSpec
	environments                    []RepositoryProcessTransport
	runs                            []*GoAcceptanceRun
	before                          func(context.Context, string) error
	afterStart                      func(RepositoryProcess)
	partial                         string
	prepareCount                    int
	opening                         func(RepositoryAcceptanceInvocation)
	started                         func(context.Context, []string, int)
	processes                       []processTree538
}

func context538(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func write538(t *testing.T, root, name, source string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureFor538(t *testing.T, cache, source string) *fixture538 {
	t.Helper()
	exe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("default producer acceptance needs actual Go: ", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture538{t: t, root: t.TempDir(), cache: cache, executable: exe, source: source}
	f.part = repository.Part{Name: "api", Directory: "api", Language: "go", Commands: map[string]repository.Command{
		"test_one": {Argv: []string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v=false"}},
	}}
	f.test = Test{Name: "same display label", Says: "selected runtime behavior", Part: "api", Framework: "go", File: "api/nested/ready_accept_test.go", Selector: "TestReady"}
	write538(t, f.root, "api/go.mod", "module example.invalid/producer538\n\ngo 1.23\n")
	write538(t, f.root, "api/go.sum", "")
	write538(t, f.root, "api/nested/value.go", "package fixture\nconst Value = 7\n")
	write538(t, f.root, f.test.File, strings.ReplaceAll(source, "\r\n", "\n"))
	t.Cleanup(func() {
		for _, r := range f.runs {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Close(ctx) // Individual ownership/error tests assert the result.
			cancel()
		}
	})
	return f
}

var injected538 = errors.New("injected transport failure after acquiring a real resource")

func (f *fixture538) gate(ctx context.Context, phase string) error {
	if f.before != nil {
		return f.before(ctx, phase)
	}
	return ctx.Err()
}

type transport538 struct {
	RepositoryProcessTransport
	f *fixture538
}

func (e *transport538) Run(ctx context.Context, argv []string) ([]byte, error) {
	e.f.calls = append(e.f.calls, slices.Clone(argv))
	phase := "run"
	if len(argv) > 1 {
		phase = argv[1]
	}
	if err := e.f.gate(ctx, phase); err != nil {
		return nil, err
	}
	first := len(e.f.processes)
	out, err := e.RepositoryProcessTransport.Run(ctx, argv)
	assertProcessesGone538(e.f.t, e.f.processes[first:])
	return out, err
}
func (e *transport538) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := e.f.gate(ctx, "read"); err != nil {
		return nil, err
	}
	return e.RepositoryProcessTransport.ReadFile(ctx, name)
}
func (e *transport538) Stage(ctx context.Context, name string, b []byte) (string, error) {
	if err := e.f.gate(ctx, "helper"); err != nil {
		return "", err
	}
	return e.RepositoryProcessTransport.Stage(ctx, name, b)
}
func (e *transport538) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	if err := e.f.gate(ctx, "prepare process"); err != nil {
		return nil, err
	}
	copy := spec
	copy.Argv = slices.Clone(spec.Argv)
	e.f.prepares = append(e.f.prepares, copy)
	p, err := e.RepositoryProcessTransport.Prepare(ctx, spec)
	if p != nil {
		p = &prepared538{RepositoryPreparedProcess: p, f: e.f}
	}
	if err == nil && e.f.partial == "prepare process" {
		err = injected538
	}
	return p, err
}
func (e *transport538) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cleanup received canceled context: %w", err)
	}
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > SandboxCleanupAllowance+time.Second {
		return errors.New("cleanup is unbounded")
	}
	err := e.RepositoryProcessTransport.Close(ctx)
	assertProcessesGone538(e.f.t, e.f.processes)
	if e.f.partial == "close" {
		return errors.Join(err, injected538)
	}
	return err
}

type prepared538 struct {
	RepositoryPreparedProcess
	f *fixture538
}

func (p *prepared538) Start(ctx context.Context) (RepositoryProcess, error) {
	if err := p.f.gate(ctx, "start"); err != nil {
		return nil, err
	}
	child, err := p.RepositoryPreparedProcess.Start(ctx)
	if child != nil && p.f.afterStart != nil {
		p.f.afterStart(child)
	}
	if err == nil && p.f.partial == "start" {
		err = injected538
	}
	return child, err
}

func (f *fixture538) open(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error) {
	f.prepareCount++
	if err := f.gate(ctx, "open"); err != nil {
		return nil, err
	}
	if f.opening != nil {
		f.opening(in)
	}
	e, err := OpenLocalGoProcessTransport(ctx, LocalGoProcessOptions{SourceDir: f.root, Part: f.part, Invocation: in, GoExecutable: f.executable, CacheDir: f.cache, OnStart: f.observeStart})
	if e != nil {
		e = &transport538{RepositoryProcessTransport: e, f: f}
		f.environments = append(f.environments, e)
	}
	if err == nil && f.partial == "open" {
		err = injected538
	}
	return e, err
}
func (f *fixture538) start(ctx context.Context) (*GoAcceptanceRun, error) {
	r, err := StartGoAcceptance(ctx, f.part, f.test, f.source, dependencyHash([]byte("captured execution policy 538")), f.open)
	if r != nil {
		f.runs = append(f.runs, r)
	}
	if err != nil {
		assertProcessesGone538(f.t, f.processes)
	}
	return r, err
}
func (f *fixture538) execute(ctx context.Context) GoAcceptanceStream {
	f.t.Helper()
	r, err := f.start(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if r == nil || r.Process == nil {
		f.t.Fatal("producer never launched")
	}
	stream, err := r.Wait(ctx)
	assertProcessesGone538(f.t, f.processes)
	if err != nil {
		f.t.Fatalf("producer infrastructure: %v; %+v", err, stream)
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(cleanup); err != nil {
		f.t.Fatal(err)
	}
	if !stream.Output.Started || !stream.Output.Exited || !stream.Output.Drained {
		f.t.Fatalf("unfinished transport: %+v", stream.Output)
	}
	return stream
}

func actions538(s GoAcceptanceStream, name string) []string {
	var out []string
	for _, e := range s.Events {
		if e.Name == name && e.Action != "output" {
			out = append(out, e.Action)
		}
	}
	return out
}
func terminal538(t *testing.T, s GoAcceptanceStream, name, terminal string) {
	t.Helper()
	got := actions538(s, name)
	if len(got) < 2 || got[0] != "start" || got[len(got)-1] != terminal {
		t.Fatalf("%s lifecycle %v, want start ... %s; %+v", name, got, terminal, s)
	}
	starts, ends := 0, 0
	for _, a := range got {
		if a == "start" {
			starts++
		}
		if a == "pass" || a == "fail" || a == "skip" {
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("duplicate/missing lifecycle: %s %v", name, got)
	}
}
func text538(s GoAcceptanceStream) string {
	var b strings.Builder
	for _, e := range s.Events {
		b.WriteString(e.Text)
	}
	return b.String()
}
func checkBinding538(t *testing.T, f *fixture538, s GoAcceptanceStream, pkg string) {
	t.Helper()
	b := s.Invocation.Binding
	if b.InvocationID == "" || b.Part != f.test.Part || b.Framework != "go" || b.File != f.test.File || b.Selector != f.test.Selector || b.Package != pkg || b.SourceDigest != dependencyHash([]byte(f.source)) || b.ExecutionDigest != dependencyHash([]byte("captured execution policy 538")) || s.Invocation.Source != f.source {
		t.Fatalf("lost bound identity/source: %+v", s.Invocation)
	}
	selected, err := (GoRepositoryAcceptanceAdapter{}).Select(context538(t), f.part, f.test)
	if err != nil || !slices.Equal(s.Invocation.Argv, selected.Argv) {
		t.Fatalf("lost captured arguments: %+v %v", s.Invocation, err)
	}
}

func TestGoProducerChangesOnlyProductionOutcome(t *testing.T) {
	ctx := context538(t)
	source := "package fixture\r\nimport (\"testing\"; h \"example.invalid/producer538/helper\")\r\nfunc TestReady(t *testing.T) { t.Log(\"assertion context 538\"); if Value != h.Want { t.Fatalf(\"actual Value=%d want=%d\",Value,h.Want) } }\r\n"
	f := fixtureFor538(t, t.TempDir(), source)
	write538(t, f.root, "api/helper/helper.go", "package helper\nconst Want = 42\n")
	first := f.execute(ctx)
	terminal538(t, first, "TestReady", "fail")
	if !strings.Contains(text538(first), "actual Value=7 want=42") || first.Output.ExitCode == 0 {
		t.Fatal("no genuine assertion failure with explanation")
	}
	write538(t, f.root, "api/nested/value.go", "package fixture\nconst Value = 42\n")
	second := f.execute(ctx)
	terminal538(t, second, "TestReady", "pass")
	if second.Output.ExitCode != 0 || !strings.Contains(text538(second), "assertion context 538") {
		t.Fatal("no actual pass/log with verbosity false")
	}
	for _, s := range []GoAcceptanceStream{first, second} {
		checkBinding538(t, f, s, "example.invalid/producer538/nested")
	}
	if first.Invocation.Binding.InvocationID == second.Invocation.Binding.InvocationID {
		t.Fatal("invocation identity reused")
	}
	data, err := os.ReadFile(filepath.Join(f.root, f.test.File))
	if err != nil || string(data) != strings.ReplaceAll(source, "\r\n", "\n") {
		t.Fatal("producer rewrote caller acceptance bytes")
	}
	if len(f.calls) < 4 {
		t.Fatal("real discovery and compilation were not used")
	}
	for _, e := range f.environments {
		if _, ok := e.(RepositoryAcceptanceEnvironment); ok {
			t.Fatal("raw producer enabled an evidence collector")
		}
	}
}

func TestGoProducerSelectsExactRuntimeCases(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	source := `package fixture
import "testing"
func TestReady(t *testing.T) {
 for _,name := range []string{"case[1].+$", "case1x", "case[1].+$suffix", "case[2].+$"} {
  t.Run(name,func(t *testing.T) {
   t.Run("$(touch_PWN);BACKTICKS538",func(t *testing.T){t.Parallel();t.Log("selected exact child")})
   t.Run("$(touch_PWN);BACKTICKS538prefix",func(t *testing.T){t.Fatal("neighbor")})
  })
 }
}
func TestReadyPrefix(t *testing.T){t.Fatal("prefix neighbor")}
`
	source = strings.ReplaceAll(source, "BACKTICKS538", "`literal`")
	f := fixtureFor538(t, cache, source)
	f.test.Selector = "TestReady/case[1].+$/$(touch_PWN);`literal`"
	s := f.execute(ctx)
	for _, name := range []string{"TestReady", "TestReady/case[1].+$", f.test.Selector} {
		terminal538(t, s, name, "pass")
	}
	if !slices.Contains(actions538(s, f.test.Selector), "pause") || !slices.Contains(actions538(s, f.test.Selector), "continue") {
		t.Fatal("parallel lifecycle missing")
	}
	for _, e := range s.Events {
		if e.Name != "TestReady" && e.Name != "TestReady/case[1].+$" && e.Name != f.test.Selector {
			t.Fatalf("neighbor ran: %+v", e)
		}
		want := ""
		if n := strings.LastIndex(e.Name, "/"); n >= 0 {
			want = e.Name[:n]
		}
		if e.Parent != want {
			t.Fatalf("wrong runtime parent: %+v", e)
		}
	}
	f.test.Selector = "TestReady/case[9].+$/missing"
	missing := f.execute(ctx)
	terminal538(t, missing, "TestReady", "pass")
	if len(actions538(missing, f.test.Selector)) != 0 {
		t.Fatal("discovered parent manufactured an absent child")
	}
	// Several parallel children must all finish before their parent finishes.
	f.source = "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){ for _,n:=range []string{\"a\",\"b\"}{t.Run(n,func(t *testing.T){t.Parallel();t.Log(n)})} }\n"
	write538(t, f.root, f.test.File, f.source)
	f.test.Selector = "TestReady"
	parallel := f.execute(ctx)
	end := map[string]int{}
	for i, e := range parallel.Events {
		if e.Action == "pass" {
			end[e.Name] = i
		}
	}
	for _, n := range []string{"TestReady/a", "TestReady/b"} {
		terminal538(t, parallel, n, "pass")
		if end[n] >= end["TestReady"] {
			t.Fatal("parent finished before parallel child")
		}
	}
	// Same display label/name in distinct captured owning modules/parts and an
	// external package must keep its own import path, bytes and working directory.
	for _, dir := range []string{"api", "api/child"} {
		g := fixtureFor538(t, cache, "package fixture\n")
		g.part.Name, g.part.Directory = dir, dir
		g.test.Part = dir
		module := "example.invalid/" + strings.ReplaceAll(dir, "/", "_") + "538"
		g.test.File = dir + "/nested/ready_accept_test.go"
		g.source = "//go:build included538\n\npackage fixture_test\nimport (\"testing\"; f \"" + module + "/nested\")\nfunc TestReady(t *testing.T){if f.Value!=7{t.Fatal(f.Value)}}\n"
		g.part.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run={test}", "-tags=included538", "-v=false"}}
		write538(t, g.root, dir+"/go.mod", "module "+module+"\n\ngo 1.23\n")
		write538(t, g.root, dir+"/nested/value.go", "package fixture\nconst Value=7\n")
		write538(t, g.root, g.test.File, g.source)
		write538(t, g.root, dir+"/other/ready_test.go", "package other\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"other package\")}\n")
		write538(t, g.root, dir+"/foreign/go.mod", "module example.invalid/foreign538\n\ngo 1.23\n")
		write538(t, g.root, dir+"/foreign/ready_test.go", "package foreign\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Fatal(\"other module\")}\n")
		got := g.execute(ctx)
		terminal538(t, got, "TestReady", "pass")
		checkBinding538(t, g, got, module+"/nested")
	}
}

func TestGoProducerRefusesBeforeRuntime(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	source := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){}\n"
	// Reuse policy cases, not the discovery-only execution fixture.
	for _, argv := range badCommands527() {
		f := fixtureFor538(t, cache, source)
		f.part.Commands["test_one"] = repository.Command{Argv: argv}
		r, err := f.start(ctx)
		if err == nil || f.prepareCount != 0 || r != nil && r.Process != nil {
			t.Fatalf("unsupported command reached preparation: %q %v", argv, err)
		}
	}
	for _, kind := range []string{"wrong file", "excluded tag", "duplicate", "external duplicate", "wrong module", "malformed source", "compile", "dependency", "changed bytes", "symlink", "hard link"} {
		t.Run(kind, func(t *testing.T) {
			f := fixtureFor538(t, cache, source)
			switch kind {
			case "wrong file":
				f.source = "package fixture\n"
				write538(t, f.root, "api/nested/other_test.go", source)
			case "excluded tag":
				f.source = "//go:build excluded538\n\n" + source
				write538(t, f.root, "api/nested/other_test.go", source)
			case "duplicate":
				write538(t, f.root, "api/nested/other_test.go", source)
			case "external duplicate":
				write538(t, f.root, "api/nested/other_test.go", strings.Replace(source, "package fixture", "package fixture_test", 1))
			case "wrong module":
				write538(t, f.root, "api/nested/go.mod", "module example.invalid/foreign538\n\ngo 1.23\n")
			case "malformed source":
				f.source = "package fixture\nfunc TestReady(\n"
			case "compile":
				f.source = strings.Replace(source, "{}", "{missingSymbol()}", 1)
			case "dependency":
				f.source = strings.Replace(source, "import \"testing\"", "import (\"testing\"; _ \"unavailable.invalid/dependency\")", 1)
			}
			write538(t, f.root, f.test.File, f.source)
			switch kind {
			case "changed bytes":
				write538(t, f.root, f.test.File, f.source+"// candidate edit\n")
			case "symlink", "hard link":
				target := filepath.Join(t.TempDir(), "test.go")
				write538(t, filepath.Dir(target), filepath.Base(target), source)
				name := filepath.Join(f.root, f.test.File)
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(target, name)
				} else {
					err = os.Link(target, name)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err := f.start(ctx)
			if err == nil || strings.TrimSpace(err.Error()) == "" || len(f.prepares) != 0 || r != nil && r.Process != nil {
				t.Fatalf("%s invoked producer without successful discovery: %v %+v", kind, err, r)
			}
		})
	}
	// Host-only instrumentation does not admit candidate hook/overlay commands.
	for _, flag := range []string{"-overlay=evil.json", "-toolexec=evil", "-exec=evil"} {
		f := fixtureFor538(t, cache, source)
		c := f.part.Commands["test_one"]
		c.Argv = append(c.Argv, flag)
		f.part.Commands["test_one"] = c
		_, err := f.start(ctx)
		if err == nil || f.prepareCount != 0 {
			t.Fatal("candidate instrumentation became authority")
		}
	}
	// No start and no I/O on an already canceled call.
	f := fixtureFor538(t, cache, source)
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err := f.start(canceled)
	if !errors.Is(err, context.Canceled) || f.prepareCount != 0 {
		t.Fatal("canceled validation performed preparation")
	}
}

// The control runner is actual uninstrumented Go, using exactly the local
// transport's isolated environment. It never constructs a framework event.
func control538(t *testing.T, f *fixture538, ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryProcessOutput, error) {
	t.Helper()
	var processes []processTree538
	env, err := OpenLocalGoProcessTransport(ctx, LocalGoProcessOptions{SourceDir: f.root, Part: f.part, Invocation: in, GoExecutable: f.executable, CacheDir: f.cache, OnStart: func(_ context.Context, _ []string, pid int) {
		processes = append(processes, observeProcessTree538(t, pid))
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := env.Close(c); err != nil {
			t.Error(err)
		}
	}()
	p, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: slices.Clone(in.Argv)})
	if err != nil {
		return RepositoryProcessOutput{}, err
	}
	child, err := p.Start(ctx)
	if err != nil {
		return RepositoryProcessOutput{}, err
	}
	out, waitErr := child.Wait(ctx)
	assertProcessesGone538(t, processes)
	return out, waitErr
}
