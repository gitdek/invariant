package plumbing

import (
	"slices"
	"strings"
	"testing"
)

func TestGoRuntimeParallelParentResume(t *testing.T) {
	source := `package fixture
import "testing"
func TestReady(t *testing.T) {
 t.Log("authentic progress before parallel parents pause")
 t.Run("one/two", func(t *testing.T) {
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
  t.Parallel()
 })
 t.Parallel()
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
	runtimeCompleted567(t, expected, good, err, "passed", "parallel child")
	// Both siblings resume before either finishes. Their interleaving remains
	// valid with a slash-bearing actual parent and two levels of parallelism.
	left, right := "TestReady/one/two/left", "TestReady/one/two/right"
	if runtimeIndex567(t, control.Events, left, "continue") > runtimeIndex567(t, control.Events, right, "pass") || runtimeIndex567(t, control.Events, right, "continue") > runtimeIndex567(t, control.Events, left, "pass") {
		t.Fatal("real control did not interleave parallel siblings")
	}
	for _, pair := range []struct{ parent, child string }{
		{"TestReady", "TestReady/one/two"},
		{"TestReady/one/two", left},
	} {
		for _, action := range []string{"continue", "pass"} {
			t.Run(pair.parent+" after child "+action, func(t *testing.T) {
				bad := runtimeCopyStream567(control)
				at := runtimeIndex567(t, bad.Events, pair.parent, "continue")
				resume := bad.Events[at]
				bad.Events = slices.Delete(bad.Events, at, at+1)
				after := runtimeIndex567(t, bad.Events, pair.child, action)
				bad.Events = slices.Insert(bad.Events, after+1, resume)
				runtimeReframe567(&bad)
				e, err := CollectGoAcceptance(expected, bad, nil)
				runtimeUnfinished567(t, expected, e, err, true)
				r, _ := runtimeResult567(expected, e)
				if e.Complete || err == nil || !strings.Contains(err.Error(), "parent resume") || !strings.Contains(r.Reason, "authentic progress") {
					t.Fatalf("invalid parent ordering qualified or lost its explanation: %+v %v", e, err)
				}
			})
		}
	}
	good, err = CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, good, err, "passed", "parallel child")
}
