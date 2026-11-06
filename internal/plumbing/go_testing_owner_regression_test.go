package plumbing

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoTestingHandoffRequiresOwner548(t *testing.T) {
	ctx := context538(t)
	f := fixtureFor538(t, t.TempDir(), `package fixture
import ("testing"; "os"; "fmt")
func TestReady(t *testing.T) {
 for _, key := range []string{"INVARIANT_GO_TESTING_EVENTS", "INVARIANT_GO_TESTING_OWNER"} {
  if _, ok := os.LookupEnv(key); ok { t.Fatalf("handoff not consumed: %s", key) }
 }
 dir, err := os.Getwd()
 if err != nil || os.Getenv("PWD") != dir { t.Fatal("package PWD changed") }
 if _, err := os.ReadFile("ready_accept_test.go"); err != nil { t.Fatal(err) }
 fmt.Fprintf(os.Stdout, "OWNER_PID548/%d\n", os.Getpid())
 fmt.Fprintln(os.Stderr, "OWNER_STDERR548")
 t.Log("owner test completed 548")
}
`)
	f.part.Commands["test_one"] = command538([]string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v"})
	launched := 0
	f.started = func(_ context.Context, argv []string, pid int) {
		if runtimeTestStart567(argv) {
			launched = pid
		}
	}
	run, err := f.start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := run.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	terminal538(t, stream, "TestReady", "pass")
	if launched <= 0 || !strings.Contains(string(stream.Output.Stdout), fmt.Sprintf("OWNER_PID548/%d\n", launched)) {
		t.Fatalf("launcher did not exec the test in its owned process: pid=%d %+v", launched, stream.Output)
	}
	spec := f.prepares[len(f.prepares)-1]
	if len(spec.Argv) < 5 || spec.Argv[0] != "/bin/sh" || spec.Argv[2] != goTestingLauncher {
		t.Fatalf("producer did not use the host launcher: %q", spec.Argv)
	}
	// Reuse the actual instrumented binary without the launcher, as a helper
	// does. Neither an available writer nor an invalid path may be opened by a
	// process with a missing, malformed or different owner's handoff.
	for _, owner := range []string{"", "not-a-pid", "1"} {
		for _, endpoint := range []string{"inherited writer", "unavailable path"} {
			t.Run(owner+"/"+endpoint, func(t *testing.T) {
				direct := RepositoryProcessSpec{
					Argv: spec.Argv[4:], Directory: spec.Directory, EventEnv: goTestingEventEnv,
					Env: map[string]string{"INVARIANT_GO_TESTING_OWNER": owner},
				}
				if endpoint == "unavailable path" {
					direct.EventEnv = ""
					direct.Env[goTestingEventEnv] = filepath.Join(t.TempDir(), "missing-events")
				}
				prepared, err := run.environment.Prepare(ctx, direct)
				if err != nil {
					t.Fatal(err)
				}
				process, err := prepared.Start(ctx)
				if err != nil {
					t.Fatal(err)
				}
				out, err := process.Wait(ctx)
				if err != nil || !out.Started || !out.Exited || !out.Drained || out.ExitCode != 0 || len(out.Events) != 0 {
					t.Fatalf("nonowner used the endpoint or changed ordinary testing: %+v %v", out, err)
				}
				if !strings.Contains(string(out.Stdout), "owner test completed 548") || strings.Contains(string(out.Stdout), "OWNER_STDERR548") || string(out.Stderr) != "OWNER_STDERR548\n" {
					t.Fatalf("nonowner lost ordinary diagnostics: %+v", out)
				}
				evidence, err := CollectGoAcceptance(run.Invocation, GoAcceptanceStream{Invocation: run.Invocation, Output: out}, nil)
				if err == nil || evidence.Complete {
					t.Fatalf("nonowner diagnostics qualified as host evidence: %+v %v", evidence, err)
				}
			})
		}
	}
	assertProcessesGone538(t, f.processes)
	cleanup, done := goProcessCleanupContext(context.Background())
	defer done()
	if err := run.Close(cleanup); err != nil {
		t.Fatal(err)
	}
}
