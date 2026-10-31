package factory

import (
	"context"
	"testing"
	"time"
)

// Every agent run a step starts ends once: when the step moves on to a new
// run or to something else, and otherwise when the step itself ends. A step
// with no run left ends without another.
func TestFactoryFinishesEachRunItsStepMovesOnFrom(t *testing.T) {
	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	now := start
	f := &Factory{Work: t.TempDir(), Now: func() time.Time { return now }}
	var heard []Step
	f.Finished = func(s Step) { heard = append(heard, s) }
	f.Activity = func(steps []Step) {
		for _, s := range steps {
			if s.Ended != nil {
				t.Errorf("Activity heard a finished step: %+v", s)
			}
		}
	}
	if started, err := f.start(context.Background(), 1, 1); err != nil || !started {
		t.Fatalf("the step didn't start: %v", err)
	}
	f.doing(1, "formalizing")
	f.runsAgent(1, "codex", "/work/issue-1/formalize-first")
	now = now.Add(time.Minute)
	f.runsAgent(1, "codex", "/work/issue-1/formalize-second")
	now = now.Add(time.Minute)
	f.doing(1, "ratifying")
	now = now.Add(time.Minute)
	f.end(1, now)
	want := []struct {
		run   string
		ended time.Time
	}{{"formalize-first", start.Add(time.Minute)}, {"formalize-second", start.Add(2 * time.Minute)}}
	if len(heard) != len(want) {
		t.Fatalf("Finished heard %+v; want the two runs, once each", heard)
	}
	for i, w := range want {
		if s := heard[i]; s.Issue != 1 || s.Doing != "formalizing" || s.Agent != "codex" || s.Run != w.run || !s.Since.Equal(start) || s.Ended == nil || !s.Ended.Equal(w.ended) {
			t.Errorf("finished run %d is %+v; want %s from %s until %s", i, s, w.run, start, w.ended)
		}
	}
	if steps := f.Steps(); len(steps) != 0 {
		t.Errorf("steps %+v remain once the step ended", steps)
	}
}
