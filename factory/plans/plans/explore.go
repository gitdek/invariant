package plans

import "fmt"

// The model's bounds.
const (
	N          = 3
	People     = 2
	Watchers   = 2
	MaxCrashes = 2
)

// State is PlanRun's variables at the bounds. Issue k+1 is the one for step k+1, and
// Status[k] is 0 for an issue not created. 0 in Ratifier and Leader is NoOne.
type State struct {
	Ratifier int8
	StopMark int8
	Count    int8
	Status   [N]int8
	Author   [N]int8
	Recorded [N]bool
	Done     bool
	Leader   int8
	Crashes  int8
}

func Init() State {
	return State{StopMark: -1}
}

func plan(s State) *Plan {
	pl := New(N)
	pl.Ratifier = int(s.Ratifier)
	pl.StopMark = int(s.StopMark)
	pl.Count = int(s.Count)
	for k := 0; k < N; k++ {
		pl.Status[k] = int(s.Status[k])
		pl.Author[k] = int(s.Author[k])
		pl.Recorded[k] = s.Recorded[k]
	}
	pl.Done = s.Done
	pl.Leader = int(s.Leader)
	return pl
}

func state(pl *Plan, s State) State {
	n := State{
		Ratifier: int8(pl.Ratifier),
		StopMark: int8(pl.StopMark),
		Count:    int8(pl.Count),
		Done:     pl.Done,
		Leader:   int8(pl.Leader),
		Crashes:  s.Crashes,
	}
	for k := 0; k < N; k++ {
		n.Status[k] = int8(pl.Status[k])
		n.Author[k] = int8(pl.Author[k])
		n.Recorded[k] = pl.Recorded[k]
	}
	return n
}

func mv(name string) map[string]any { return map[string]any{"$mv": name} }

func person(p int8) map[string]any {
	if p == 0 {
		return mv("NoOne")
	}
	return mv(fmt.Sprintf("p%d", p))
}

func watcher(w int8) map[string]any {
	if w == 0 {
		return mv("NoOne")
	}
	return mv(fmt.Sprintf("w%d", w))
}

func Try(s State, tried func(step string, args []any, next State)) {
	run := func(op func(pl *Plan) bool) State {
		pl := plan(s)
		if !op(pl) {
			return s
		}
		return state(pl, s)
	}
	for p := 1; p <= People; p++ {
		arg := []any{person(int8(p))}
		tried("Ratify", arg, run(func(pl *Plan) bool { return pl.Ratify(p) }))
		tried("Stop", arg, run(func(pl *Plan) bool { return pl.Stop() }))
	}
	for w := 1; w <= Watchers; w++ {
		arg := []any{watcher(int8(w))}
		tried("Acquire", arg, run(func(pl *Plan) bool { return pl.Acquire(w) }))
		next := s
		if s.Crashes < MaxCrashes {
			next = run(func(pl *Plan) bool { return pl.Crash(w) })
			if next != s {
				next.Crashes++
			}
		}
		tried("Crash", arg, next)
		tried("Create", arg, run(func(pl *Plan) bool { return pl.Create(w) }))
		for i := 1; i <= N; i++ {
			tried("Record", arg, run(func(pl *Plan) bool { return pl.Record(w, i) }))
		}
		tried("Finish", arg, run(func(pl *Plan) bool { return pl.Finish(w) }))
	}
	for i := 1; i <= N; i++ {
		arg := []any{i}
		tried("Fail", arg, run(func(pl *Plan) bool { return pl.Fail(i) }))
		tried("Merge", arg, run(func(pl *Plan) bool { return pl.Merge(i) }))
	}
	tried("Rest", []any{}, s)
}

var statuses = map[int8]string{Open: "open", Failed: "failed", Merged: "merged"}

func Abstract(s State) map[string]any {
	issues := []any{}
	for k := 0; k < int(s.Count); k++ {
		issues = append(issues, map[string]any{
			"step":   k + 1,
			"author": person(s.Author[k]),
			"status": statuses[s.Status[k]],
		})
	}
	recorded := []any{}
	for k := 0; k < N; k++ {
		if s.Recorded[k] {
			recorded = append(recorded, k+1)
		}
	}
	return map[string]any{
		"ratifier": person(s.Ratifier),
		"stopMark": int(s.StopMark),
		"issues":   map[string]any{"$seq": issues},
		"recorded": map[string]any{"$set": recorded},
		"done":     s.Done,
		"leader":   watcher(s.Leader),
		"crashes":  int(s.Crashes),
	}
}
