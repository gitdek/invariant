package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// A driver that records its attempts writes every state it reached, the ones
// it started in, and every step it tried in each: the step's name and
// arguments as the model's Next names them, and the state it reached. A
// refusal reaches the state it started in (D-0085).
//
//	{"states": [state, ...], "init": [0], "attempts": [[from, "MakeCall", [{"$mv": "a1"}], to], ...]}
type attempts struct {
	States   []map[string]any `json:"states"`
	Init     []int            `json:"init"`
	Attempts [][]any          `json:"attempts"`
}

// Model is what checking a driver's attempts needs from the model.
type Model struct {
	Steps []tla.Step // the named steps its Next takes
	// Larger is the environment's numeric bounds, each one larger: a step a
	// driver leaves untried must be one the model rules out at the ratified
	// bounds and allows with these.
	Larger map[string]string
	// Problem is why the model's steps can't be read, when they can't.
	Problem string
}

// Tried is whether a driver tried every step: in every state it reached,
// every step the model names, except where only a bound of the environment
// rules the step out (D-0082, D-0085).
type Tried struct {
	Passed   bool   `json:"passed"`
	Attempts int    `json:"attempts"`
	States   int    `json:"states"`
	Untried  string `json:"untried,omitempty"` // steps the driver never tried where more than a bound rules them out, as TLA+
	In       string `json:"in,omitempty"`      // the state it never tried them in
	Message  string `json:"message,omitempty"`
}

// recorded is what a driver wrote, in either form: its states, the ones its
// runs start in, and the steps that changed the state.
type recorded struct {
	states, pretty []string
	starts         map[int]bool
	steps          map[[2]int]bool
	runs, recorded int       // runs made, and steps recorded, refusals included
	attempts       []attempt // nil for a driver that records only runs
}

// attempt is one step a driver tried, from one state to another.
type attempt struct {
	from, to int
	step     string
	args     []string // as TLA+
}

// decode reads a driver's output, as runs or as attempts. A problem is why
// it can't be read.
func decode(raw []byte, vars []string) (rec recorded, problem string) {
	rec.starts, rec.steps = map[int]bool{}, map[[2]int]bool{}
	index := map[string]int{}
	add := func(s map[string]any) (int, string) {
		text, readable, err := encodeState(s, vars)
		if err != nil {
			return 0, fmt.Sprintf("a recorded state can't be read: %v", err)
		}
		i, seen := index[text]
		if !seen {
			i = len(rec.states)
			index[text] = i
			rec.states = append(rec.states, text)
			rec.pretty = append(rec.pretty, readable)
		}
		return i, ""
	}

	var out struct {
		Traces [][]map[string]any `json:"traces"`
		attempts
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return rec, "the driver's output isn't valid JSON: " + err.Error()
	}
	if out.Attempts == nil {
		rec.runs = len(out.Traces)
		if rec.runs == 0 {
			return rec, "the driver recorded no runs"
		}
		for _, run := range out.Traces {
			prev := -1
			for n, s := range run {
				i, problem := add(s)
				if problem != "" {
					return rec, problem
				}
				if n == 0 {
					rec.starts[i] = true
				} else {
					rec.recorded++
					if i != prev {
						rec.steps[[2]int{prev, i}] = true
					}
				}
				prev = i
			}
		}
		return rec, ""
	}

	// Attempts. The driver's own state numbers map onto distinct states.
	ids := make([]int, len(out.States))
	for n, s := range out.States {
		i, problem := add(s)
		if problem != "" {
			return rec, problem
		}
		ids[n] = i
	}
	state := func(v any) (int, bool) {
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) || f < 0 || int(f) >= len(ids) {
			return 0, false
		}
		return ids[int(f)], true
	}
	for _, n := range out.Init {
		if n < 0 || n >= len(ids) {
			return rec, fmt.Sprintf("the driver starts in state %d, which it didn't record", n)
		}
		rec.starts[ids[n]] = true
	}
	if len(rec.starts) == 0 {
		return rec, "the driver recorded no state to start in"
	}
	rec.attempts = []attempt{}
	for _, a := range out.Attempts {
		if len(a) != 4 {
			return rec, fmt.Sprintf("an attempt is %v; want [from, step, [args], to]", a)
		}
		from, okFrom := state(a[0])
		name, okName := a[1].(string)
		args, okArgs := a[2].([]any)
		to, okTo := state(a[3])
		if !okFrom || !okName || !okArgs || !okTo {
			return rec, fmt.Sprintf("an attempt is %v; want [from, step, [args], to], with states the driver recorded", a)
		}
		t := attempt{from: from, to: to, step: name}
		for _, arg := range args {
			text, err := Encode(arg)
			if err != nil {
				return rec, fmt.Sprintf("an argument of %s can't be read: %v", name, err)
			}
			t.args = append(t.args, text)
		}
		rec.attempts = append(rec.attempts, t)
		rec.recorded++
		if from != to {
			rec.steps[[2]int{from, to}] = true
		}
	}
	return rec, ""
}

// triedBatch is how many states one TLC run looks at. A batch's module holds
// only its own states, so memory stays flat however many the driver reached.
const triedBatch = 1000

// checkTried has TLC find, in each state the driver reached, a step it never
// tried that more than a bound rules out. The module extends the model at
// the ratified bounds and instantiates it with the environment's bounds one
// larger, so TLC evaluates each step both ways (D-0085).
func checkTried(ctx context.Context, runner tlc.Runner, dir, module, header string, constants map[string]string, vars []string, m Model, rec recorded) (Tried, error) {
	t := Tried{Attempts: len(rec.attempts), States: len(rec.states)}
	if m.Problem != "" {
		t.Message = m.Problem
		return t, nil
	}
	if len(m.Steps) == 0 {
		t.Message = "the model names no steps, so the driver's attempts can't be checked"
		return t, nil
	}
	arity := map[string]int{}
	for _, s := range m.Steps {
		if n, seen := arity[s.Name]; seen && n != len(s.Args) {
			arity[s.Name] = -1 // Next calls it with different numbers of arguments
			continue
		}
		arity[s.Name] = len(s.Args)
	}
	tried := make([]map[string]bool, len(rec.states))
	for _, a := range rec.attempts {
		n, ok := arity[a.step]
		switch {
		case !ok:
			t.Message = fmt.Sprintf("the driver tried %s, a step the model's Next doesn't name", a.step)
			return t, nil
		case n >= 0 && n != len(a.args):
			t.Message = fmt.Sprintf("the driver tried %s with %d arguments, and Next passes it %d", a.step, len(a.args), n)
			return t, nil
		}
		if tried[a.from] == nil {
			tried[a.from] = map[string]bool{}
		}
		tried[a.from][instance(a.step, a.args)] = true
	}

	larger := "Invariant_Larger == INSTANCE " + module
	if len(m.Larger) > 0 {
		names := make([]string, 0, len(m.Larger))
		for name := range m.Larger {
			names = append(names, name)
		}
		sort.Strings(names)
		subs := make([]string, len(names))
		for i, name := range names {
			subs[i] = name + " <- " + m.Larger[name]
		}
		larger += " WITH " + strings.Join(subs, ", ")
	}
	untried := untriedText(m.Steps)
	for start := 0; start < len(rec.states); start += triedBatch {
		end := min(start+triedBatch, len(rec.states))
		sets := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			names := make([]string, 0, len(tried[i]))
			for x := range tried[i] {
				names = append(names, x)
			}
			sort.Strings(names)
			sets = append(sets, "{"+strings.Join(names, ", ")+"}")
		}
		text := "---- MODULE Invariant_Tried ----\n" + header +
			"VARIABLE invariant_i\n" +
			larger + "\n" +
			"Invariant_States == <<" + strings.Join(rec.states[start:end], ",\n  ") + ">>\n" +
			"Invariant_Tried == <<" + strings.Join(sets, ",\n  ") + ">>\n" +
			"Invariant_TInit == \\E i \\in DOMAIN Invariant_States : invariant_i = i /\\ " + is("Invariant_States[i]", vars, false) + "\n" +
			"Invariant_TSpec == Invariant_TInit /\\ [][UNCHANGED <<vars, invariant_i>>]_<<vars, invariant_i>>\n" +
			"Invariant_Untried ==\n  " + untried + "\n" +
			"Invariant_EveryStepTried == Invariant_Untried = {} \\/ (PrintT(<<\"invariant-untried\", Invariant_Untried>>) /\\ FALSE)\n====\n"
		if err := os.WriteFile(filepath.Join(dir, "Invariant_Tried.tla"), []byte(text), 0o644); err != nil {
			return t, err
		}
		res, err := runner.Check(ctx, dir, "Invariant_Tried", tlc.Config{Specification: "Invariant_TSpec", Constants: constants, Invariants: []string{"Invariant_EveryStepTried"}})
		if err != nil {
			return t, err
		}
		switch {
		case res.Outcome == tlc.Violated && res.Invariant == "Invariant_EveryStepTried":
			t.Untried = printedUntried(res.Printed)
			if i := stateIndex(res.Trace); i >= 1 && start+i-1 < len(rec.pretty) {
				t.In = rec.pretty[start+i-1]
			}
			t.Message = "the driver never tried a step that more than a bound rules out"
			return t, nil
		case res.Outcome != tlc.Passed:
			t.Message = "TLC couldn't check the driver's attempts: " + res.Message
			return t, nil
		}
	}
	t.Passed = true
	return t, nil
}

// instance is a step with its arguments, as the TLA+ tuple TLC compares.
func instance(step string, args []string) string {
	name, _ := json.Marshal(step)
	return "<<" + strings.Join(append([]string{string(name)}, args...), ", ") + ">>"
}

// untriedText is the TLA+ for the steps in the current state the driver never
// tried, leaving out those only the environment's bounds rule out.
func untriedText(steps []tla.Step) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		name, _ := json.Marshal(s.Name)
		set := "{<<" + strings.Join(append([]string{string(name)}, s.Args...), ", ") + ">>"
		if n := len(s.Binders); n > 0 {
			set += " : " + s.Binders[n-1].Var + ` \in ` + s.Binders[n-1].Domain
			set += "}"
			for j := n - 2; j >= 0; j-- {
				set = "UNION {" + set + " : " + s.Binders[j].Var + ` \in ` + s.Binders[j].Domain + "}"
			}
		} else {
			set += "}"
		}
		call := s.Name
		if len(s.Args) > 0 {
			xs := make([]string, len(s.Args))
			for k := range xs {
				xs[k] = "invariant_x[" + strconv.Itoa(k+2) + "]"
			}
			call += "(" + strings.Join(xs, ", ") + ")"
		}
		parts[i] = fmt.Sprintf(`{invariant_x \in %s : invariant_x \notin invariant_t /\ ~(~ENABLED (%s /\ UNCHANGED invariant_i) /\ ENABLED (Invariant_Larger!%s /\ UNCHANGED invariant_i))}`, set, call, call)
	}
	return "LET invariant_t == Invariant_Tried[invariant_i] IN\n    " + strings.Join(parts, "\n    \\cup ")
}

// printedUntried finds the untried steps TLC printed, as TLA+.
func printedUntried(printed []string) string {
	const head = `<<"invariant-untried",`
	for _, p := range printed {
		p = strings.Join(strings.Fields(p), " ")
		if strings.HasPrefix(p, head) && strings.HasSuffix(p, ">>") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(p, head), ">>"))
		}
	}
	return ""
}

// stateIndex is invariant_i in the state TLC reported, or 0.
func stateIndex(trace []tlc.State) int {
	if len(trace) == 0 {
		return 0
	}
	for _, v := range trace[len(trace)-1].Vars {
		if v.Name == "invariant_i" {
			n, _ := strconv.Atoi(v.Value)
			return n
		}
	}
	return 0
}
