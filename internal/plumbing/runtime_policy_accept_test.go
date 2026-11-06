package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func runtimeNoVerdict567(t *testing.T, e RepositoryAcceptanceEvidence, err error) {
	t.Helper()
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("refusal/setup has no error: %+v", e)
	}
	for _, r := range e.Records {
		if r.Kind == "compile" || r.Kind == "test" && (r.Status == "passed" || r.Status == "failed" || r.Status == "skipped") {
			t.Fatalf("setup/refusal supplied terminal test or compiler evidence: %+v", e)
		}
	}
}

func runtimeHasTestStart567(f *runtimeFixture567) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, argv := range f.starts {
		if len(argv) > 0 && strings.HasSuffix(argv[0], "acceptance.test") {
			return true
		}
	}
	return false
}

func runtimeFlag567(argv []string, name string) string {
	for i, a := range argv {
		if value, ok := strings.CutPrefix(a, name+"="); ok {
			return value
		}
		if a == name {
			if name == "-short" || name == "-v" || name == "-race" || name == "-trimpath" {
				return "true"
			}
			if i+1 < len(argv) {
				return argv[i+1]
			}
		}
	}
	return ""
}

func TestLocalGoRuntimeAdmissionAndPolicy(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	plain := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Log(\"admitted real runtime\")}\n"
	control := runtimeFixtureFor567(t, cache, plain)
	in, e, _, err := control.execute(ctx)
	runtimeCompleted567(t, in, e, err, "passed", "admitted real runtime")
	for _, argv := range [][]string{
		{"sh", "-c", "go test -run {test}"}, {"/usr/bin/go", "test", "-run={test}"},
		{"go", "test", "./...", "-run={test}", "-run=TestOther"},
		{"go", "test", "./...", "-run={test}", "-count=2"},
		{"go", "test", "./...", "-run={test}", "-args", "-test.run=TestOther"},
		{"go", "test", "./...", "-run={test}", "-overlay=foreign.json"},
		{"go", "test", "./...", "-run={test}", "-toolexec=foreign"},
		{"go", "test", "./...", "-run={test}", "-json"},
	} {
		f := runtimeFixtureFor567(t, cache, plain)
		f.part.Commands["test_one"] = repository.Command{Argv: argv}
		opened := false
		run, err := StartGoAcceptance(ctx, f.part, f.test, f.source, dependencyHash([]byte("captured policy")), func(c context.Context, in RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error) {
			opened = true
			return f.open(c, in)
		})
		if run != nil {
			f.runs = append(f.runs, run)
		}
		if err == nil || opened {
			t.Fatalf("unsupported command reached preparation: %q %v", argv, err)
		}
	}
	for _, kind := range []string{"absent declaration", "wrong file", "excluded source", "compiler", "offline import", "nested module"} {
		t.Run(kind, func(t *testing.T) {
			f := runtimeFixtureFor567(t, cache, plain)
			switch kind {
			case "absent declaration":
				f.test.Selector = "TestAbsent"
			case "wrong file":
				f.source = "package fixture\n"
				runtimeWrite567(t, f.root, "api/nested/elsewhere_test.go", plain)
			case "excluded source":
				f.source = "//go:build excluded567\n\n" + plain
			case "compiler":
				f.source = strings.Replace(plain, `t.Log("admitted real runtime")`, `Undefined567()`, 1)
			case "offline import":
				f.source = strings.Replace(plain, `import "testing"`, `import ("testing"; _ "unavailable.invalid/missing567")`, 1)
			case "nested module":
				runtimeWrite567(t, f.root, "api/nested/go.mod", "module example.invalid/foreign567\n\ngo 1.27.1\n")
			}
			runtimeWrite567(t, f.root, f.test.File, f.source)
			in, err := f.invocation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			env, err := f.open(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			discovery, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, f.part, in, env)
			if err == nil || discovery.Binding.Package != "" {
				t.Fatalf("real failed discovery selected a package: %+v %v", discovery, err)
			}
			var detail *RepositoryAcceptanceDiscoveryError
			if !errors.As(err, &detail) || (detail.Kind != "setup" && detail.Kind != "refused") {
				t.Fatalf("lost setup/refusal: %v", err)
			}
			// Even a caller that requests collection after failed discovery cannot
			// turn its preliminary package or compiler exit into test evidence.
			e, _, runErr := runtimeEnvironment567(t, env).RunAcceptance(ctx, in)
			runtimeNoVerdict567(t, e, runErr)
			if runtimeHasTestStart567(f) {
				t.Fatal("failed discovery launched runtime")
			}
			f.assertGone()
		})
	}
	for _, field := range []string{"invocation", "part", "framework", "file", "selector", "package", "source digest", "execution digest", "source", "argv"} {
		t.Run("local bound "+field, func(t *testing.T) {
			f := runtimeFixtureFor567(t, cache, plain)
			env, in, err := f.local(ctx)
			if err != nil {
				t.Fatal(err)
			}
			bad := cloneGoInvocation(in)
			switch field {
			case "invocation":
				bad.Binding.InvocationID = "different-attempt"
			case "part":
				bad.Binding.Part = "other"
			case "framework":
				bad.Binding.Framework = "jest"
			case "file":
				bad.Binding.File = "api/other_test.go"
			case "selector":
				bad.Binding.Selector = "TestReady/other"
			case "package":
				bad.Binding.Package = "example.invalid/runtime567/other"
			case "source digest":
				bad.Binding.SourceDigest = dependencyHash([]byte("changed"))
			case "execution digest":
				bad.Binding.ExecutionDigest = dependencyHash([]byte("changed"))
			case "source":
				bad.Source += "\n// changed\n"
			case "argv":
				bad.Argv = append(bad.Argv, "-short")
			}
			e, _, err := runtimeEnvironment567(t, env).RunAcceptance(ctx, bad)
			runtimeNoVerdict567(t, e, err)
			if runtimeHasTestStart567(f) {
				t.Fatal("changed authority started a test")
			}
		})
	}
	t.Run("single use", func(t *testing.T) {
		f := runtimeFixtureFor567(t, cache, plain)
		env, in, err := f.local(ctx)
		if err != nil {
			t.Fatal(err)
		}
		a := runtimeEnvironment567(t, env)
		// Mutating the caller's map after preparation cannot replace the captured
		// command which this environment retains for its discovered invocation.
		f.part.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "-run=TestOther"}}
		e, _, err := a.RunAcceptance(ctx, in)
		runtimeCompleted567(t, in, e, err, "passed", "")
		e, _, err = a.RunAcceptance(ctx, in)
		runtimeNoVerdict567(t, e, err)
	})
	t.Run("real helper storage failure", func(t *testing.T) {
		f := runtimeFixtureFor567(t, cache, plain)
		env, in, err := f.local(ctx)
		if err != nil {
			t.Fatal(err)
		}
		probe, err := env.Stage(ctx, "storage-probe", []byte("private storage"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Dir(probe)); err != nil {
			t.Fatal(err)
		}
		e, _, err := runtimeEnvironment567(t, env).RunAcceptance(ctx, in)
		runtimeNoVerdict567(t, e, err)
		if runtimeHasTestStart567(f) {
			t.Fatal("failed helper setup launched runtime")
		}
	})
	flags := [][]string{
		{"-tags", "included567,extra567", "-timeout=0", "-parallel", "2", "-p=1", "-cpu", "2", "-shuffle=off", "-mod", "readonly", "-short=false", "-v=false", "-race=false", "-trimpath=false"},
		{"-tags=included567,extra567", "-timeout", "13s", "-parallel=3", "-p", "2", "-cpu=1", "-shuffle", "+37", "-mod=vendor", "-count", "1", "-short", "-v", "-race=false", "-trimpath"},
	}
	for index, captured := range flags {
		t.Run(fmt.Sprintf("captured flags %d", index), func(t *testing.T) {
			src := fmt.Sprintf(`//go:build included567 && extra567

package fixture
import ("testing";"flag";"runtime";"path/filepath";"os")
var calls int
func TestReady(t *testing.T){
 calls++;if calls!=1{t.Fatal("count changed")}
 if testing.Short()!=%t||testing.Verbose()!=%t{t.Fatal("boolean flags changed")}
 if Race567{t.Fatal("race=false changed")}
 _,file,_,_:=runtime.Caller(0);if filepath.IsAbs(file)==%t{t.Fatal("trimpath changed")}
 for key,want:=range map[string]string{"test.timeout":%q,"test.parallel":%q,"test.cpu":%q,"test.count":"1"}{if flag.Lookup(key).Value.String()!=want{t.Fatalf("%%s changed",key)}}
 if runtime.GOMAXPROCS(0)!=%s{t.Fatal("cpu changed")}
 for key,want:=range map[string]string{"GOTOOLCHAIN":"local","GOWORK":"off","GOENV":"off","GOPROXY":"off","GOSUMDB":"off"}{if os.Getenv(key)!=want{t.Fatalf("unsafe %%s=%%q",key,os.Getenv(key))}}
 for _,key:=range []string{"HOME","GOCACHE","GOMODCACHE","GOPATH","TMPDIR"}{if !filepath.IsAbs(os.Getenv(key)){t.Fatalf("nonprivate %%s",key)}}
 b,err:=os.ReadFile("runtime_accept_test.go");if err!=nil||len(b)==0{t.Fatal("not package cwd")}
 wd,err:=os.Getwd();if err!=nil||os.Getenv("PWD")!=wd{t.Fatal("PWD changed")}
 t.Log("all captured flags and offline policy reached runtime")
}
`, runtimeFlag567(captured, "-short") == "true", runtimeFlag567(captured, "-v") == "true", runtimeFlag567(captured, "-trimpath") == "true", map[bool]string{true: "0s", false: "13s"}[runtimeFlag567(captured, "-timeout") == "0"], runtimeFlag567(captured, "-parallel"), runtimeFlag567(captured, "-cpu"), runtimeFlag567(captured, "-cpu"))
			f := runtimeFixtureFor567(t, cache, src)
			runtimeWrite567(t, f.root, "api/nested/race_on.go", "//go:build race\n\npackage fixture\nconst Race567=true\n")
			runtimeWrite567(t, f.root, "api/nested/race_off.go", "//go:build !race\n\npackage fixture\nconst Race567=false\n")
			argv := append([]string{"go", "test", "./...", "-run={test}"}, captured...)
			f.part.Commands["test_one"] = repository.Command{Argv: argv}
			in, e, out, err := f.execute(ctx)
			runtimeCompleted567(t, in, e, err, "passed", "all captured flags")
			selection, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, f.part, f.test)
			if err != nil || !slices.Equal(in.Argv, selection.Argv) {
				t.Fatal("lost literal command policy")
			}
			listed, compiled := false, false
			for _, call := range f.starts {
				if len(call) < 2 || call[0] != "go" {
					continue
				}
				if (call[1] == "list" || call[1] == "test") && slices.Contains(call, "./nested") {
					if !slices.Equal(goDiscoveryBuildFlags(call), goDiscoveryBuildFlags(in.Argv)) {
						t.Fatalf("real command dropped build policy: %q", call)
					}
					listed = listed || call[1] == "list"
					compiled = compiled || call[1] == "test"
				}
			}
			if !listed || !compiled {
				t.Fatal("did not run real loader/compiler")
			}
			if runtimeFlag567(captured, "-shuffle") == "+37" && !strings.Contains(string(out), "-test.shuffle 37") {
				t.Fatal("shuffle not executed")
			}
		})
	}
}

func TestLocalGoRuntimeCancellationAndCleanup(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	plain := "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Log(\"earlier actual completion\")}\n"
	warm := runtimeFixtureFor567(t, cache, plain)
	earlier, completed, _, err := warm.execute(ctx)
	runtimeCompleted567(t, earlier, completed, err, "passed", "earlier actual completion")
	for _, phase := range []string{"list", "discovery compile", "helper reader", "instrumented compile"} {
		t.Run(phase, func(t *testing.T) {
			f := runtimeFixtureFor567(t, cache, plain)
			limited, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			deadline, _ := limited.Deadline()
			observed := false
			f.started = func(c context.Context, argv []string, pid int) {
				d, ok := c.Deadline()
				if !ok || d.After(deadline) {
					t.Error("preparation renewed allowance")
				}
				if len(argv) < 2 || argv[0] != "go" {
					return
				}
				match := phase == "list" && argv[1] == "list" || phase == "discovery compile" && argv[1] == "test" && slices.Contains(argv, "/dev/null") || phase == "helper reader" && argv[1] == "run" || phase == "instrumented compile" && argv[1] == "test" && !slices.Contains(argv, "/dev/null")
				if match && !observed {
					observed = true
					cancel()
				}
			}
			start := time.Now()
			run, err := StartGoAcceptance(limited, f.part, f.test, f.source, dependencyHash([]byte("captured policy")), f.open)
			if run != nil {
				f.runs = append(f.runs, run)
			}
			if !observed || !errors.Is(err, context.Canceled) || time.Since(start) > 10*time.Second {
				t.Fatalf("real %s cancellation failed: seen=%v err=%v", phase, observed, err)
			}
			if run != nil && run.Process != nil && run.Process.Snapshot().Started {
				t.Fatal("canceled preparation started runtime")
			}
			f.assertGone()
		})
	}
	// These helpers are actual descendants of the instrumented Go process. They
	// retain its process group, including a grandchild which never writes stdout.
	source := `package fixture
import ("testing";"os";"os/exec";"strconv";"time";"fmt")
func init(){
 role:=os.Getenv("RUNTIME567_ROLE")
 if role=="grandchild"{for{time.Sleep(time.Hour)}}
 if role=="child"{
  os.Setenv("RUNTIME567_ROLE","grandchild")
  c:=exec.Command(os.Args[0]);c.Env=os.Environ();if err:=c.Start();err!=nil{panic(err)}
  if err:=os.WriteFile("grandchild567.pid",[]byte(strconv.Itoa(c.Process.Pid)),0600);err!=nil{panic(err)}
  for{time.Sleep(time.Hour)}
 }
}
func TestReady(t *testing.T){
 t.Run("completed",func(t *testing.T){t.Log("authentic completed child before cancellation")})
 os.Setenv("RUNTIME567_ROLE","child")
 c:=exec.Command(os.Args[0]);c.Env=os.Environ();if err:=c.Start();err!=nil{t.Fatal(err)}
 if err:=os.WriteFile("child567.pid",[]byte(strconv.Itoa(c.Process.Pid)),0600);err!=nil{t.Fatal(err)}
 fmt.Fprintln(os.Stdout,"real runtime waiting 567");fmt.Fprintln(os.Stderr,"real runtime stderr 567")
 if err:=os.WriteFile("runtime567.pid",[]byte(strconv.Itoa(os.Getpid())),0600);err!=nil{t.Fatal(err)}
 for{time.Sleep(time.Hour)}
}
`
	for _, selector := range []string{"TestReady", "TestReady/completed"} {
		t.Run(selector, func(t *testing.T) {
			f := runtimeFixtureFor567(t, cache, source)
			f.test.Selector = selector
			env, in, err := f.local(ctx)
			if err != nil {
				t.Fatal(err)
			}
			out, err := env.Run(ctx, []string{"go", "list", "-f", "{{.Dir}}", "./nested"})
			if err != nil {
				t.Fatal(err)
			}
			pkg := strings.TrimSpace(string(out))
			probe, err := env.Stage(ctx, "owned-resource", []byte("owned"))
			if err != nil {
				t.Fatal(err)
			}
			limited, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			deadline, _ := limited.Deadline()
			f.started = func(c context.Context, _ []string, _ int) {
				d, ok := c.Deadline()
				if !ok || d.After(deadline) {
					t.Error("RunAcceptance renewed deadline")
				}
			}
			type answer struct {
				e   RepositoryAcceptanceEvidence
				out []byte
				err error
			}
			done := make(chan answer, 1)
			a := runtimeEnvironment567(t, env)
			go func() { e, out, err := a.RunAcceptance(limited, in); done <- answer{e, out, err} }()
			var descendants []int
			readyUntil := time.Now().Add(15 * time.Second)
			for _, name := range []string{"child567.pid", "grandchild567.pid", "runtime567.pid"} {
				for {
					// A loaded machine can show the file before its writer fills it.
					b, readErr := os.ReadFile(filepath.Join(pkg, name))
					if readErr == nil && len(b) > 0 {
						pid, err := strconv.Atoi(string(b))
						if err != nil || pid <= 0 {
							t.Fatalf("bad real descendant PID: %q", b)
						}
						descendants = append(descendants, pid)
						break
					}
					if time.Now().After(readyUntil) {
						cancel()
						t.Fatal("real runtime did not reach child readiness")
					}
					select {
					case got := <-done:
						t.Fatalf("runtime stopped before readiness: %+v %v", got.e, got.err)
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			start := time.Now()
			cancel()
			var got answer
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("RunAcceptance cancellation unbounded")
			}
			if !errors.Is(got.err, context.Canceled) || time.Since(start) > 5*time.Second {
				t.Fatalf("lost cancellation: %v", got.err)
			}
			if !strings.Contains(string(got.out), "real runtime waiting 567") || !strings.Contains(string(got.out), "real runtime stderr 567") {
				t.Fatal("lost partial diagnostics")
			}
			if selector == "TestReady" {
				runtimeUnfinished567(t, in, got.e, got.err, true)
			} else {
				found := false
				for _, r := range got.e.Records {
					if r.Kind == "test" && r.Status == "passed" && r.Binding == in.Binding && strings.Contains(r.Reason, "authentic completed child") {
						found = true
					}
				}
				if !found {
					t.Fatalf("lost authentic terminal before later interruption: %+v", got.e)
				}
				_, validationErr := runtimeResult567(in, got.e)
				if validationErr == nil {
					t.Fatal("unfinished ancestor qualified selected child")
				}
			}
			f.assertGone()
			for _, pid := range descendants {
				if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
					t.Errorf("descendant %d survived: %v", pid, err)
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
			first := env.Close(cleanup)
			second := env.Close(cleanup)
			stop()
			if first != nil && second == nil {
				t.Fatal("repeated cleanup lost infrastructure cause")
			}
			if _, err := os.Stat(probe); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("helper survived cleanup")
			}
			if _, err := os.Stat(pkg); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("private candidate survived cleanup")
			}
			runtimeCompleted567(t, earlier, completed, nil, "passed", "earlier actual completion")
		})
	}
	// A consumed shared deadline cannot be restarted for another invocation.
	shared, stop := context.WithTimeout(ctx, 80*time.Millisecond)
	defer stop()
	<-shared.Done()
	f := runtimeFixtureFor567(t, cache, plain)
	env, in, err := f.local(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	before := len(f.starts)
	f.mu.Unlock()
	e, _, err := runtimeEnvironment567(t, env).RunAcceptance(shared, in)
	runtimeNoVerdict567(t, e, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost original shared deadline: %v", err)
	}
	f.mu.Lock()
	after := len(f.starts)
	f.mu.Unlock()
	if before != after {
		t.Fatal("expired shared allowance started another process")
	}
}
