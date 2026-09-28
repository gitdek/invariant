package protocol

import "fmt"

// The model's bounds, each set of model values as its size.
const (
	Actors    = 3
	Writers   = 2
	Bots      = 1
	Questions = 2
	Proposals = 2
	Heads     = 2
)

// Values of DirectedBy: NoActor, or the actor's index plus one. alice is the
// director: invbot writes but is a bot, and mallory has no write access.
const (
	ByAlice   = 1
	ByMallory = 2
	ByInvbot  = 3
)

// Proposals, as locks.
const (
	P1 = 1
	P2 = 2
)

// Head commits.
const (
	H1 = 1
	H2 = 2
)

// State mirrors the spec's variables one to one. Open[i] is question f(i+1);
// Gate[i] is CI's result on head h(i+1).
type State struct {
	Kind         int8
	Open         [Questions]bool
	Proposal     int8
	Amends       int8
	Base         int8
	Ratified     int8
	RatifiedBase int8
	Head         int8
	Gate         [Heads]int8
	PrLock       int8
	Scope        int8
	MergedBy     int8
	MergedHead   int8
	DirectedBy   int8
	Stops        int8
	Failure      int8
}

var (
	kindNames     = []string{"none", "asked", "proposed", "stuck", "unsupported", "ratified", "pr_open", "failed", "merged", "closed"}
	gateNames     = []string{"none", "pending", "pass", "fail"}
	scopeNames    = []string{"one", "many"}
	mergedByNames = []string{"nobody", "factory", "other"}
	failureNames  = []string{"none", "stopped", "limit", "gate", "ci", "unmergeable"}
)

// actor is the a-th of the model's actors. The first Writers-Bots are
// directors, and the last Bots are bots with write access.
func actor(a int) Actor {
	bot := a >= Actors-Bots
	return Actor{ID: a + 1, Writer: a < Writers-Bots || bot, Bot: bot}
}

func actorName(a int) string {
	switch a {
	case 0:
		return "alice"
	case 1:
		return "mallory"
	case 2:
		return "invbot"
	}
	return fmt.Sprintf("a%d", a+1)
}

func mv(name string) map[string]any {
	return map[string]any{"$mv": name}
}

func lockValue(l int) map[string]any {
	if l == NoP {
		return mv("NoP")
	}
	return mv(fmt.Sprintf("p%d", l))
}

func headValue(h int) map[string]any {
	if h == NoHead {
		return mv("NoHead")
	}
	return mv(fmt.Sprintf("h%d", h))
}

func directedValue(d int) map[string]any {
	if d == NoActor {
		return mv("NoActor")
	}
	return mv(actorName(d - 1))
}

func openSet(open [Questions]bool) map[string]any {
	set := []any{}
	for q := 0; q < Questions; q++ {
		if open[q] {
			set = append(set, mv(fmt.Sprintf("f%d", q+1)))
		}
	}
	return map[string]any{"$set": set}
}

// draftChoice is one of the model's Drafts.
type draftChoice struct {
	kind int
	open [Questions]bool
	p    int
}

// drafts lists every draft the factory can post.
func drafts() []draftChoice {
	var out []draftChoice
	for m := 1; m < 1<<Questions; m++ {
		c := draftChoice{kind: KindAsked, p: NoP}
		for q := 0; q < Questions; q++ {
			c.open[q] = m&(1<<q) != 0
		}
		out = append(out, c)
	}
	for p := 1; p <= Proposals; p++ {
		out = append(out, draftChoice{kind: KindProposed, p: p})
	}
	out = append(out, draftChoice{kind: KindStuck, p: NoP}, draftChoice{kind: KindUnsupported, p: NoP})
	return out
}

// draft is c as the code takes it, with questions of its own.
func (c draftChoice) draft() Draft {
	d := Draft{Kind: c.kind, Open: make([]bool, Questions), P: c.p}
	for q := 0; q < Questions; q++ {
		d.Open[q] = c.open[q]
		if c.open[q] {
			d.NOpen++
		}
	}
	return d
}

func (c draftChoice) value() map[string]any {
	return map[string]any{"kind": kindNames[c.kind], "open": openSet(c.open), "p": lockValue(c.p)}
}

// toIssue makes the code's value from s.
func toIssue(s State) *Issue {
	i := NewIssue(Questions)
	for q := 0; q < Questions; q++ {
		i.Open[q] = s.Open[q]
		if s.Open[q] {
			i.NOpen++
		}
	}
	i.Kind = int(s.Kind)
	i.Proposal = int(s.Proposal)
	i.Amends = int(s.Amends)
	i.Base = int(s.Base)
	i.Ratified = int(s.Ratified)
	i.RatifiedBase = int(s.RatifiedBase)
	i.Head = int(s.Head)
	if s.Head != NoHead {
		i.HeadGate = int(s.Gate[s.Head-1])
	}
	i.PrLock = int(s.PrLock)
	i.Scope = int(s.Scope)
	i.MergedBy = int(s.MergedBy)
	i.MergedHead = int(s.MergedHead)
	i.DirectedBy = int(s.DirectedBy)
	i.Stops = int(s.Stops)
	i.Failure = int(s.Failure)
	return i
}

// readBack is s after the code's step to i. CI's results on heads other than
// the current one are the explorer's own: a pull request forgotten clears
// them all.
func readBack(s State, i *Issue) State {
	t := s
	t.Kind = int8(i.Kind)
	for q := 0; q < Questions; q++ {
		t.Open[q] = i.Open[q]
	}
	t.Proposal = int8(i.Proposal)
	t.Amends = int8(i.Amends)
	t.Base = int8(i.Base)
	t.Ratified = int8(i.Ratified)
	t.RatifiedBase = int8(i.RatifiedBase)
	t.Head = int8(i.Head)
	if i.Head == NoHead {
		t.Gate = [Heads]int8{}
	} else {
		t.Gate[i.Head-1] = int8(i.HeadGate)
	}
	t.PrLock = int8(i.PrLock)
	t.Scope = int8(i.Scope)
	t.MergedBy = int8(i.MergedBy)
	t.MergedHead = int8(i.MergedHead)
	t.DirectedBy = int8(i.DirectedBy)
	t.Stops = int8(i.Stops)
	t.Failure = int8(i.Failure)
	return t
}

// Init is the model's initial state.
func Init() State {
	return readBack(State{}, NewIssue(Questions))
}

// attempt tries every step Next names in s, with every argument, and says
// whether the code ran it.
func attempt(s State, tried func(step string, args []any, next State, ran bool)) {
	do := func(step string, args []any, op func(i *Issue) bool) {
		i := toIssue(s)
		if op(i) {
			tried(step, args, readBack(s, i), true)
		} else {
			tried(step, args, s, false)
		}
	}
	ds := drafts()
	for a := 0; a < Actors; a++ {
		act, av := actor(a), mv(actorName(a))
		for _, c := range ds {
			do("Solve", []any{av, c.value()}, func(i *Issue) bool { return i.Solve(act, c.draft()) })
			do("Revise", []any{av, c.value()}, func(i *Issue) bool { return i.Revise(act, c.draft()) })
		}
		do("Retry", []any{av}, func(i *Issue) bool { return i.Retry(act) })
		for q := 0; q < Questions; q++ {
			qv := mv(fmt.Sprintf("f%d", q+1))
			for _, c := range ds {
				do("Choose", []any{av, qv, c.value()}, func(i *Issue) bool { return i.Choose(act, q, c.draft()) })
			}
		}
		for p := 1; p <= Proposals; p++ {
			do("Ratify", []any{av, lockValue(p)}, func(i *Issue) bool { return i.Ratify(act, p) })
		}
	}
	for h := 1; h <= Heads; h++ {
		do("Build", []any{headValue(h)}, func(i *Issue) bool { return i.Build(h) })
		do("BuildFailsGate", []any{headValue(h)}, func(i *Issue) bool { return i.BuildFailsGate(h) })
	}
	do("StopBuild", []any{}, (*Issue).StopBuild)
	do("RefuseBuild", []any{}, (*Issue).RefuseBuild)
	for h := 1; h <= Heads; h++ {
		for l := 0; l <= Proposals; l++ {
			for sc := ScopeOne; sc <= ScopeMany; sc++ {
				do("Push", []any{headValue(h), lockValue(l), scopeNames[sc]}, func(i *Issue) bool { return i.Push(h, l, sc) })
			}
		}
	}
	for _, r := range []int{GatePass, GateFail} {
		do("CIGate", []any{gateNames[r]}, func(i *Issue) bool { return i.CIGate(r) })
	}
	do("NoticeFail", []any{}, (*Issue).NoticeFail)
	do("NoticeUnmergeable", []any{}, (*Issue).NoticeUnmergeable)
	do("Merge", []any{}, (*Issue).Merge)
	do("OthersMerge", []any{}, (*Issue).OthersMerge)
	do("OthersClose", []any{}, (*Issue).OthersClose)
	for l := 0; l <= Proposals; l++ {
		do("BaseMoves", []any{lockValue(l)}, func(i *Issue) bool { return i.BaseMoves(l) })
	}
	do("Finished", []any{}, (*Issue).Finished)
}

// Try tries every step Next names in s, with every argument, and reports the
// state each reaches: s itself where the code refuses.
func Try(s State, tried func(step string, args []any, next State)) {
	attempt(s, func(step string, args []any, next State, _ bool) {
		tried(step, args, next)
	})
}

// Successors returns every state one step of Next reaches from s.
func Successors(s State) []State {
	var out []State
	attempt(s, func(_ string, _ []any, next State, ran bool) {
		if ran {
			out = append(out, next)
		}
	})
	return out
}

// Abstract is s in the spec's vocabulary.
func Abstract(s State) map[string]any {
	gate := []any{}
	for h := 0; h < Heads; h++ {
		gate = append(gate, []any{headValue(h + 1), gateNames[s.Gate[h]]})
	}
	return map[string]any{
		"state":        kindNames[s.Kind],
		"open":         openSet(s.Open),
		"proposal":     lockValue(int(s.Proposal)),
		"amends":       lockValue(int(s.Amends)),
		"base":         lockValue(int(s.Base)),
		"ratified":     lockValue(int(s.Ratified)),
		"ratifiedBase": lockValue(int(s.RatifiedBase)),
		"head":         headValue(int(s.Head)),
		"gate":         map[string]any{"$fn": gate},
		"prLock":       lockValue(int(s.PrLock)),
		"scope":        scopeNames[s.Scope],
		"mergedBy":     mergedByNames[s.MergedBy],
		"mergedHead":   headValue(int(s.MergedHead)),
		"directedBy":   directedValue(int(s.DirectedBy)),
		"stops":        int(s.Stops),
		"failure":      failureNames[s.Failure],
	}
}
