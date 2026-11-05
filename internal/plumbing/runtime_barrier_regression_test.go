package plumbing

import (
	"slices"
	"strings"
	"testing"
)

func TestGoRuntimeParallelSiblingBarrier(t *testing.T) {
	source := `package fixture
import "testing"
func TestReady(t *testing.T) {
 t.Log("authentic progress before sibling barrier")
 ready := make(chan struct{}, 2)
 release := make(chan struct{})
 go func(){ <-ready; <-ready; close(release) }()
 for _, name := range []string{"left", "right"} {
  t.Run(name, func(t *testing.T) {
   t.Parallel()
   ready <- struct{}{}; <-release
   t.Log("parallel child " + name)
  })
 }
 t.Run("serial", func(t *testing.T){t.Log("serial child completed")})
}
`
	f := runtimeFixtureFor567(t, t.TempDir(), source)
	command := f.part.Commands["test_one"]
	command.Argv = append(command.Argv, "-parallel=2")
	f.part.Commands["test_one"] = command
	control, err := f.raw(runtimeContext567(t))
	if err != nil {
		t.Fatal(err)
	}
	expected := cloneGoInvocation(control.Invocation)
	good, err := CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, good, err, "passed", "serial child completed")
	left, right := "TestReady/left", "TestReady/right"
	if runtimeIndex567(t, control.Events, left, "continue") > runtimeIndex567(t, control.Events, right, "pass") || runtimeIndex567(t, control.Events, right, "continue") > runtimeIndex567(t, control.Events, left, "pass") {
		t.Fatal("real control did not interleave parallel siblings")
	}
	for _, before := range []struct{ name, action string }{
		{right, "pause"},
		{"TestReady/serial", "pass"},
		{right, "start"},
	} {
		t.Run("resume before "+before.name+" "+before.action, func(t *testing.T) {
			bad := runtimeCopyStream567(control)
			at := runtimeIndex567(t, bad.Events, left, "continue")
			resume := bad.Events[at]
			bad.Events = slices.Delete(bad.Events, at, at+1)
			at = runtimeIndex567(t, bad.Events, before.name, before.action)
			bad.Events = slices.Insert(bad.Events, at, resume)
			runtimeReframe567(&bad)
			e, err := CollectGoAcceptance(expected, bad, nil)
			runtimeUnfinished567(t, expected, e, err, true)
			r, _ := runtimeResult567(expected, e)
			if e.Complete || err == nil || !strings.Contains(err.Error(), "parent barrier") || !strings.Contains(r.Reason, "authentic progress") {
				t.Fatalf("invalid sibling ordering qualified or lost its explanation: %+v %v", e, err)
			}
		})
	}
	good, err = CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, good, err, "passed", "parallel child")
}
