package plumbing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGoTestingHandoffOwnership548(t *testing.T) {
	ctx := context538(t)
	source := `package fixture
import ("testing"; "os")
func TestReady(t *testing.T) {
 if os.Getenv("PROBE548") == "" {
  for _, key := range []string{"INVARIANT_GO_TESTING_EVENTS", "INVARIANT_GO_TESTING_OWNER"} {
   if _, ok := os.LookupEnv(key); ok { t.Fatalf("unconsumed handoff %s", key) }
  }
 }
 t.Logf("owner PID548=%d", os.Getpid())
}
`
	f := fixtureFor538(t, t.TempDir(), source)
	pid := 0
	f.started = func(_ context.Context, argv []string, started int) {
		if len(argv) > 1 && strings.HasSuffix(argv[0], "/launch") {
			pid = started
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
	if pid == 0 || !strings.Contains(text538(stream), fmt.Sprintf("owner PID548=%d", pid)) {
		t.Fatalf("launcher replaced the transport-owned process: pid=%d events=%+v", pid, stream.Events)
	}
	spec := f.prepares[len(f.prepares)-1]
	if len(spec.Argv) < 2 {
		t.Fatalf("missing launcher command: %+v", spec)
	}
	launcher, binary := spec.Argv[0], spec.Argv[1]
	for _, owner := range []string{"", "0", "not-a-pid"} {
		t.Run("unowned "+owner, func(t *testing.T) {
			// A non-owner must not even try to open the inherited endpoint.
			// A valid transport pipe must also receive no header or records.
			for _, endpoint := range []string{"missing", "pipe"} {
				probe := RepositoryProcessSpec{
					Argv: slices.Clone(spec.Argv[1:]), Directory: spec.Directory,
					Env: map[string]string{"PROBE548": "1", goTestingOwnerEnv: owner, goTestingEventEnv: filepath.Join(filepath.Dir(binary), "absent-endpoint")},
				}
				if endpoint == "pipe" {
					probe.EventEnv = goTestingEventEnv
				}
				prepared, err := run.environment.Prepare(ctx, probe)
				if err != nil {
					t.Fatal(err)
				}
				process, err := prepared.Start(ctx)
				if err != nil {
					t.Fatal(err)
				}
				out, err := process.Wait(ctx)
				if err != nil || !out.Started || !out.Exited || !out.Drained || out.ExitCode != 0 || len(out.Events) != 0 {
					t.Fatalf("unowned %s endpoint changed ordinary testing: %+v %v", endpoint, out, err)
				}
			}
		})
	}
	// Exec failure must leave the event channel empty, never supplying evidence
	// just because the transport started and reaped the launcher successfully.
	prepared, err := run.environment.Prepare(ctx, RepositoryProcessSpec{Argv: []string{launcher, binary + ".missing"}, EventEnv: goTestingEventEnv, Directory: spec.Directory})
	if err != nil {
		t.Fatal(err)
	}
	process, err := prepared.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := process.Wait(ctx)
	if err != nil || !out.Started || !out.Exited || !out.Drained || out.ExitCode == 0 || len(out.Events) != 0 || len(out.Stderr) == 0 {
		t.Fatalf("launcher exec failure changed process/evidence behavior: %+v %v", out, err)
	}
	assertProcessesGone538(t, f.processes)
}

func TestGoTestingLauncherCancellation548(t *testing.T) {
	ctx, cancel := context.WithCancel(context538(t))
	defer cancel()
	f := fixtureFor538(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n")
	observed := false
	f.started = func(c context.Context, argv []string, _ int) {
		if len(argv) > 2 && argv[0] == "go" && argv[1] == "build" && strings.HasSuffix(argv[len(argv)-1], "/launch.go") {
			deadline, ok := c.Deadline()
			original, _ := ctx.Deadline()
			if !ok || deadline.After(original) {
				t.Error("launcher compilation renewed the shared allowance")
			}
			observed = true
			cancel()
		}
	}
	run, err := f.start(ctx)
	if !observed || !errors.Is(err, context.Canceled) || run == nil || run.Process != nil {
		t.Fatalf("canceled launcher compilation reached runtime: observed=%v run=%+v err=%v", observed, run, err)
	}
	assertProcessesGone538(t, f.processes)
}
