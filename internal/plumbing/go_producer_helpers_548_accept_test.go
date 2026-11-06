package plumbing

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This dependency deliberately has no testing import, including transitively.
// Its module path sorts before testing, so its init re-executes the binary
// before the binary's testing package has initialized. It never reads or
// changes the producer's event handoff.
const earlyHelperSource548 = `package early
import ("fmt"; "os"; "os/exec")
const Enabled = HELPERS_ENABLED548
func Role() string { return os.Getenv("HELPER_ROLE548") }
func init() {
 if role := Role(); role != "" {
  fmt.Fprintln(os.Stdout, "HELPER_OUT548/"+role)
  fmt.Fprintln(os.Stderr, "HELPER_ERR548/"+role)
 } else {
  Run("init")
 }
}
func Run(phase string) {
 if !Enabled || Role() != "" { return }
 for _, tc := range []struct{ name, selector string; exit int }{
  {"empty", "^$", 0},
  {"pass", "^TestHelper548$", 0},
  {"fail", "^TestHelperFailure548$", 1},
 } {
  cmd := exec.Command(os.Args[0], "-test.run="+tc.selector, "-test.count=1", "-test.v")
  cmd.Env = append(os.Environ(), "HELPER_ROLE548="+phase+"/"+tc.name)
  cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
  err := cmd.Run()
  if tc.exit == 0 {
   if err != nil { panic(fmt.Sprintf("helper %s/%s: %v", phase, tc.name, err)) }
  } else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != tc.exit {
   panic(fmt.Sprintf("helper %s/%s: want exit %d, got %v", phase, tc.name, tc.exit, err))
  }
  fmt.Fprintln(os.Stdout, "HELPER_JOINED548/"+phase+"/"+tc.name)
 }
}
`

const helperAcceptanceSource548 = `package fixture
import ("testing"; "fmt"; "os"; early "example.invalid/producer538/early")
func TestMain(m *testing.M) {
 early.Run("TestMain-before")
 code := m.Run()
 early.Run("TestMain-after")
 if role := early.Role(); role != "" { fmt.Fprintln(os.Stderr, "HELPER_MAIN_DONE548/"+role) }
 os.Exit(code)
}
func TestReady(t *testing.T) {
 early.Run("body")
 t.Log("HOST_LOG548")
 t.Cleanup(func(){ t.Log("HOST_CLEANUP548") })
 t.Run("parallel", func(t *testing.T){ t.Parallel(); t.Log("HOST_PARALLEL548") })
 switch Value {
 case 1: t.Error("HOST_FAILURE548")
 case 2: t.Skip("HOST_SKIP548")
 }
}
func TestHelper548(t *testing.T) {
 if early.Role() == "" { t.Fatal("unselected helper ran as parent") }
 t.Log("HELPER_LOG548/"+early.Role())
 t.Cleanup(func(){ fmt.Fprintln(os.Stdout, "HELPER_CLEANUP548/"+early.Role()) })
 t.Run("parallel", func(t *testing.T){ t.Parallel(); t.Log("HELPER_PARALLEL548/"+early.Role()) })
}
func TestHelperFailure548(t *testing.T) {
 if early.Role() == "" { t.Fatal("unselected failing helper ran as parent") }
 t.Fatal("HELPER_FAILURE548/"+early.Role())
}
`

func helperFixture548(t *testing.T, cache string, outcome int) *fixture538 {
	t.Helper()
	f := fixtureFor538(t, cache, helperAcceptanceSource548)
	f.part.Commands["test_one"] = command538([]string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v"})
	write538(t, f.root, "api/nested/value.go", fmt.Sprintf("package fixture\nconst Value = %d\n", outcome))
	setHelpers548(t, f, false)
	return f
}

func setHelpers548(t *testing.T, f *fixture538, enabled bool) {
	t.Helper()
	write538(t, f.root, "api/early/early.go", strings.ReplaceAll(earlyHelperSource548, "HELPERS_ENABLED548", strconv.FormatBool(enabled)))
}

func helperDiagnostics548(t *testing.T, out RepositoryProcessOutput, separate bool) {
	t.Helper()
	stdout, stderr := string(out.Stdout), string(out.Stderr)
	combined := stdout + stderr
	check := func(marker string, inStderr bool) {
		t.Helper()
		marker += "\n"
		if strings.Count(combined, marker) != 1 {
			t.Fatalf("missing or repeated helper diagnostic %q: stdout=%q stderr=%q", marker, stdout, stderr)
		}
		if separate {
			own, other := stdout, stderr
			if inStderr {
				own, other = stderr, stdout
			}
			if strings.Count(own, marker) != 1 || strings.Contains(other, marker) {
				t.Fatalf("helper diagnostic changed channels: %q; stdout=%q stderr=%q", marker, stdout, stderr)
			}
		}
	}
	for _, phase := range []string{"init", "TestMain-before", "body", "TestMain-after"} {
		for _, kind := range []string{"empty", "pass", "fail"} {
			role := phase + "/" + kind
			check("HELPER_OUT548/"+role, false)
			check("HELPER_ERR548/"+role, true)
			check("HELPER_MAIN_DONE548/"+role, true)
			check("HELPER_JOINED548/"+role, false)
		}
		for _, prefix := range []string{"HELPER_LOG548/", "HELPER_PARALLEL548/", "HELPER_CLEANUP548/"} {
			check(prefix+phase+"/pass", false)
		}
		check("HELPER_FAILURE548/"+phase+"/fail", false)
	}
}

func parentLifecycle548(t *testing.T, stream GoAcceptanceStream, terminal string, exit int) {
	t.Helper()
	out := stream.Output
	if !out.Started || !out.Exited || !out.Drained || out.ExitCode != exit {
		t.Fatalf("parent process changed: %+v", out)
	}
	if !slices.Equal(actions538(stream, "TestReady"), []string{"start", terminal}) ||
		!slices.Equal(actions538(stream, "TestReady/parallel"), []string{"start", "pause", "continue", "pass"}) {
		t.Fatalf("parent lifecycle changed: %+v", stream.Events)
	}
	for _, event := range stream.Events {
		wantParent := ""
		if event.Name == "TestReady/parallel" {
			wantParent = "TestReady"
		} else if event.Name != "TestReady" {
			t.Fatalf("helper entered the host lifecycle: %+v", stream.Events)
		}
		if event.Parent != wantParent || strings.Contains(event.Text, "HELPER_") {
			t.Fatalf("helper changed parent events: %+v", stream.Events)
		}
	}
	for _, marker := range []string{"HOST_LOG548", "HOST_CLEANUP548", "HOST_PARALLEL548"} {
		if !strings.Contains(text538(stream), marker) {
			t.Fatalf("parent lost its authentic log %s: %+v", marker, stream.Events)
		}
	}
	evidence, err := CollectGoAcceptance(stream.Invocation, stream, nil)
	status := map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}[terminal]
	docker591Complete(t, stream.Invocation, evidence, err, status, "")
	if evidence.ExitCode != exit {
		t.Fatalf("collector changed parent exit: %+v", evidence)
	}
}

func TestGoProducerKeepsHelpersOutOfLifecycle548(t *testing.T) {
	cache := t.TempDir()
	for _, tc := range []struct {
		terminal string
		value    int
		exit     int
	}{{"pass", 0, 0}, {"fail", 1, 1}, {"skip", 2, 0}} {
		t.Run(tc.terminal, func(t *testing.T) {
			ctx := context538(t)
			f := helperFixture548(t, cache, tc.value)
			clean := f.execute(ctx)
			parentLifecycle548(t, clean, tc.terminal, tc.exit)
			setHelpers548(t, f, true)

			// Actual uninstrumented go test is the exit/diagnostic control. Its
			// driver combines the binary's stderr into stdout; the producer must
			// instead preserve the two channels separately.
			ordinary, err := control538(t, f, ctx, clean.Invocation)
			if err != nil || !ordinary.Started || !ordinary.Exited || !ordinary.Drained || ordinary.ExitCode != tc.exit {
				t.Fatalf("ordinary Go control: %v %+v", err, ordinary)
			}
			helperDiagnostics548(t, ordinary, false)
			for _, marker := range []string{"=== RUN   TestReady\n", "=== RUN   TestReady/parallel\n", "=== PAUSE TestReady/parallel\n", "=== CONT  TestReady/parallel\n", "--- " + strings.ToUpper(tc.terminal) + ": TestReady (", "--- PASS: TestReady/parallel ("} {
				if strings.Count(string(ordinary.Stdout)+string(ordinary.Stderr), marker) != 1 {
					t.Fatalf("ordinary Go parent lifecycle lacks %q: %s%s", marker, ordinary.Stdout, ordinary.Stderr)
				}
			}

			got := f.execute(ctx)
			parentLifecycle548(t, got, tc.terminal, tc.exit)
			helperDiagnostics548(t, got.Output, true)
			if !slices.Equal(clean.Events, got.Events) || clean.Output.ExitCode != got.Output.ExitCode {
				t.Fatalf("helpers changed the parent's events or exit: clean=%+v with helpers=%+v", clean, got)
			}
			checkBinding538(t, f, got, "example.invalid/producer538/nested")
		})
	}
}

func TestDockerGoProducerKeepsHelpersOutOfLifecycle548(t *testing.T) {
	b := newDocker591(t)
	// Only the Docker CLI is simulated. OpenChecks, source staging, discovery,
	// compilation, process framing, testing hooks and collection are production.
	t.Run("prepared transport", func(t *testing.T) {
		ctx := context538(t)
		f := helperFixture548(t, t.TempDir(), 0)
		execute := func() GoAcceptanceStream {
			t.Helper()
			var request DependencyRequest
			run, err := StartGoAcceptance(ctx, f.part, f.test, f.source, dependencyHash([]byte("captured execution policy 538")), func(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error) {
				request = DependencyRequest{
					Token: "invariant-deps-548-" + in.Binding.InvocationID, Image: dependencyHash([]byte("prepared image 591")),
					Platform: "linux/amd64", Directory: f.part.Directory, SourceDir: f.root,
					Env: dependencyEnv("go", false), Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024,
				}
				env, err := (&DockerDependencyRunner{}).OpenChecks(ctx, request)
				if env != nil {
					t.Cleanup(func() { _ = docker591Close(t, env) })
				}
				if err != nil {
					return nil, err
				}
				transport, ok := env.(RepositoryProcessTransport)
				if !ok {
					t.Fatal("prepared Go environment has no process transport")
				}
				return transport, nil
			})
			if run != nil {
				t.Cleanup(func() {
					cleanup, done := goProcessCleanupContext(context.Background())
					defer done()
					_ = run.Close(cleanup)
				})
			}
			if err != nil {
				t.Fatal("prepared producer startup: ", err)
			}
			stream, err := run.Wait(ctx)
			if err != nil {
				t.Fatalf("helpers caused prepared producer infrastructure error: %v; %+v", err, stream)
			}
			cleanup, done := goProcessCleanupContext(context.Background())
			defer done()
			if err := run.Close(cleanup); err != nil {
				t.Fatal("prepared producer cleanup: ", err)
			}
			b.policy(request, stream.Invocation)
			b.clean()
			return stream
		}
		clean := execute()
		parentLifecycle548(t, clean, "pass", 0)
		setHelpers548(t, f, true)
		got := execute()
		parentLifecycle548(t, got, "pass", 0)
		helperDiagnostics548(t, got.Output, true)
		if !slices.Equal(clean.Events, got.Events) || clean.Output.ExitCode != got.Output.ExitCode {
			t.Fatalf("prepared helpers changed parent events or exit: clean=%+v with helpers=%+v", clean, got)
		}
		checkBinding538(t, f, got, "example.invalid/producer538/nested")
	})
	b.clean()
}
