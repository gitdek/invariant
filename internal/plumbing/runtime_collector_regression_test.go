package plumbing

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestGoRuntimeConcurrentChildren(t *testing.T) {
	ctx := runtimeContext567(t)
	source := `package fixture
import ("testing"; "sync")
func TestReady(t *testing.T) {
 t.Parallel()
 var done sync.WaitGroup
 ready := make(chan struct{}, 2)
 release := make(chan struct{})
 go func(){ <-ready; <-ready; close(release) }()
 for _, name := range []string{"left", "right"} {
  done.Go(func(){t.Run(name, func(t *testing.T){
   ready <- struct{}{}; <-release
   t.Run("one/two", func(t *testing.T){t.Log("concurrent actual child " + name)})
  })})
 }
 done.Wait()
}
`
	f := runtimeFixtureFor567(t, t.TempDir(), source)
	in, evidence, _, err := f.execute(ctx)
	r := runtimeCompleted567(t, in, evidence, err, "passed", "concurrent actual child left")
	if !strings.Contains(r.Reason, "concurrent actual child right") {
		t.Fatal("lost interleaved sibling output")
	}
}

func TestGoRuntimePreservesDamagedStreamPrefix(t *testing.T) {
	f := runtimeFixtureFor567(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Log(\"authentic prefix\")}\n")
	stream, err := f.raw(runtimeContext567(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{"truncated terminal", "noncanonical terminal", "decoded terminal"} {
		t.Run(damage, func(t *testing.T) {
			bad := runtimeCopyStream567(stream)
			switch damage {
			case "truncated terminal":
				bad.Output.Events = bad.Output.Events[:len(bad.Output.Events)-1]
			case "noncanonical terminal":
				wire := string(bad.Output.Events)
				at := strings.LastIndex(wire, "9:TestReady")
				bad.Output.Events = []byte(wire[:at] + "+" + wire[at:])
			case "decoded terminal":
				bad.Events[len(bad.Events)-1].Action = "fail"
			}
			e, collectErr := CollectGoAcceptance(stream.Invocation, bad, nil)
			runtimeUnfinished567(t, stream.Invocation, e, collectErr, true)
			r, _ := runtimeResult567(stream.Invocation, e)
			if e.Complete || !strings.Contains(r.Reason, "authentic prefix") {
				t.Fatalf("damaged suffix erased authentic progress: %+v", e)
			}
		})
	}
	// Cancellation is an infrastructure veto even when a complete stream was
	// delivered; the retained terminal remains useful to the caller's report.
	veto := errors.New("late infrastructure veto")
	e, err := CollectGoAcceptance(stream.Invocation, stream, veto)
	if !errors.Is(err, veto) {
		t.Fatal(err)
	}
	runtimeCompleted567(t, stream.Invocation, e, nil, "passed", "authentic prefix")
}

func TestLocalGoRuntimeRetainsArgumentAuthority(t *testing.T) {
	ctx := runtimeContext567(t)
	f := runtimeFixtureFor567(t, t.TempDir(), "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T){t.Log(\"retained command\")}\n")
	original, err := f.invocation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained := cloneGoInvocation(original)
	env, err := f.open(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, f.part, retained, env)
	if err != nil {
		t.Fatal(err)
	}
	retained.Binding = discovery.Binding
	// Both arrays belonged to the caller at Open. Neither can change the local
	// selection or the authority used by rediscovery and instrumentation.
	original.Argv[0] = "changed"
	command := f.part.Commands["test_one"]
	command.Argv[slices.Index(command.Argv, "-count=1")] = "-count=2"
	e, _, err := runtimeEnvironment567(t, env).RunAcceptance(ctx, retained)
	runtimeCompleted567(t, retained, e, err, "passed", "retained command")
	f.assertGone()
}
