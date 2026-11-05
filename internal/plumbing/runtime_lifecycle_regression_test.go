package plumbing

import (
	"slices"
	"testing"
)

// testing writes a finished subtest's late output, as through a saved Output
// writer, to its nearest unfinished ancestor. The real run passes, and the
// output reaches the parent's evidence (#567's review).
func TestGoRuntimeLateSubtestOutputGoesToParent(t *testing.T) {
	ctx := runtimeContext567(t)
	source := `package fixture
import ("fmt";"io";"testing")
func TestReady(t *testing.T){
 var saved io.Writer
 t.Run("child",func(t *testing.T){saved=t.Output()})
 fmt.Fprintln(saved,"late child output 567")
}
`
	f := runtimeFixtureFor567(t, t.TempDir(), source)
	control, waitErr := f.raw(ctx)
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	expected := cloneGoInvocation(control.Invocation)
	late := runtimeIndex567(t, control.Events, "TestReady/child", "output")
	if late < runtimeIndex567(t, control.Events, "TestReady/child", "pass") {
		t.Fatalf("the child's output didn't come after its terminal: %+v", control.Events)
	}
	e, err := CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, e, err, "passed", "late child output 567")
	// Output still can't follow the end of every test that could own it.
	s := runtimeCopyStream567(control)
	s.Events = append(s.Events, s.Events[late])
	runtimeReframe567(&s)
	runtimeReject567(t, expected, s, nil)
}

// A test calls Parallel inside its function, and its function's return is
// what releases its parallel children, so a stream where a parent pauses
// after its child resumed and finished is impossible (#567's review).
func TestGoRuntimeRejectsPauseAfterChildRelease(t *testing.T) {
	ctx := runtimeContext567(t)
	source := `package fixture
import "testing"
func TestReady(t *testing.T){
 t.Parallel()
 t.Run("child",func(t *testing.T){t.Parallel();t.Log("real parallel child 567")})
}
`
	f := runtimeFixtureFor567(t, t.TempDir(), source)
	control, waitErr := f.raw(ctx)
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	expected := cloneGoInvocation(control.Invocation)
	good, err := CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, good, err, "passed", "")
	s := runtimeCopyStream567(control)
	pause := runtimeIndex567(t, s.Events, "TestReady", "pause")
	resume := runtimeIndex567(t, s.Events, "TestReady", "continue")
	if resume != pause+1 {
		t.Fatalf("the parent's pause and resume aren't adjacent in the real stream: %+v", s.Events)
	}
	pair := slices.Clone(s.Events[pause : resume+1])
	s.Events = slices.Delete(s.Events, pause, resume+1)
	end := runtimeIndex567(t, s.Events, "TestReady", "pass")
	s.Events = slices.Insert(s.Events, end, pair...)
	runtimeReframe567(&s)
	runtimeReject567(t, expected, s, nil)
}
