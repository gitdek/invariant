package conformance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

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
	Attempts []attemptFields  `json:"attempts"`
}

// attemptFields preserves the legacy decoder while refusing ambiguous JSON
// in the opt-in fifth field, before decoding objects could replace a key's
// earlier value with its later one.
type attemptFields []any

func (a *attemptFields) UnmarshalJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var values []any
	if err := d.Decode(&values); err != nil {
		return err
	}
	if len(values) == 5 {
		var fields []json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if err := distinctMetadataKeys(fields[4]); err != nil {
			return err
		}
	}
	*a = values
	return nil
}

func distinctMetadataKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func() error
	read = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		bracket, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		seen := map[string]bool{}
		for d.More() {
			if bracket == '{' {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name := key.(string) // The already parsed JSON has object keys here.
				if seen[name] {
					return fmt.Errorf("declared outcome metadata repeats field %q", name)
				}
				seen[name] = true
			}
			if err := read(); err != nil {
				return err
			}
		}
		_, err = d.Token() // The object's or array's closing delimiter.
		return err
	}
	return read()
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

// attempt is one step a driver tried, from one state to another.
type attempt struct {
	from, to int
	step     string
	args     []string // as TLA+
	outcome  string   // empty for legacy attempts; accepted or refused for declared steps
}

// declaredOutcome validates the opt-in fifth field independently of the
// collector. Exception attributes are evidence, not TLA+ values: fractions
// and null are valid here, as are integers larger than machine integers.
func declaredOutcome(value any) (string, string) {
	m, ok := value.(map[string]any)
	if !ok {
		return "", "declared outcome metadata must be an object"
	}
	outcome, ok := m["outcome"].(string)
	if !ok || outcome != "accepted" && outcome != "refused" {
		return "", "declared outcome must be accepted or refused"
	}
	if outcome == "accepted" {
		if len(m) != 1 {
			return "", "an accepted attempt must have only its outcome"
		}
		return outcome, ""
	}
	refusal, ok := m["refusal"].(map[string]any)
	if len(m) != 2 || !ok || len(refusal) != 2 {
		return "", "a refused attempt must have exactly its outcome and refusal descriptor"
	}
	typ, ok := refusal["type"].(string)
	if !ok || !strings.Contains(typ, ".") || strings.IndexFunc(typ, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", "a refusal type must be a module-qualified exception name"
	}
	for _, part := range strings.Split(typ, ".") {
		if part == "" {
			return "", "a refusal type must be a module-qualified exception name"
		}
	}
	attrs, ok := refusal["attributes"].(map[string]any)
	if !ok {
		return "", "refusal attributes must be an object of finite JSON scalars"
	}
	for name, value := range attrs {
		switch x := value.(type) {
		case nil, bool, string:
		case json.Number:
			if !strings.ContainsAny(string(x), ".eE") {
				continue // JSON decoded the integer exactly, without a size limit.
			}
			f, err := x.Float64()
			if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				continue
			}
			return "", fmt.Sprintf("refusal attribute %q must be a finite JSON scalar", name)
		default:
			return "", fmt.Sprintf("refusal attribute %q must be a finite JSON scalar", name)
		}
	}
	return outcome, ""
}

func actionCall(a attempt) string {
	if len(a.args) == 0 {
		return a.step
	}
	return a.step + "(" + strings.Join(a.args, ", ") + ")"
}

// strictAttempts validates every declared action before starting TLC. Its
// index contains original attempt positions, not copies of their arguments.
func strictAttempts(ctx context.Context, rec recorded, model *Model) (string, error) {
	if len(rec.strict) == 0 {
		return "", nil
	}
	if model == nil || len(model.Steps) == 0 {
		return "declared attempts require the model's named steps", nil
	}
	if model.Problem != "" {
		return "declared attempts cannot be checked: " + model.Problem, nil
	}
	arity := stepArities(model.Steps)
	for _, id := range rec.strict {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		a, err := rec.store.attempt(rec.attempts[id])
		if err != nil {
			return "", err
		}
		n, ok := arity[a.step]
		if !ok || !identifier.MatchString(a.step) || n < 0 || n != len(a.args) {
			pretty, err := rec.store.pretty(a.from)
			if err != nil {
				return "", err
			}
			if !ok || !identifier.MatchString(a.step) {
				return fmt.Sprintf("declared attempt %s in %s names no step of the model's Next", actionCall(a), pretty), nil
			}
			return fmt.Sprintf("declared attempt %s in %s has %d arguments; Next must name the step with a consistent arity of %d", actionCall(a), pretty, len(a.args), n), nil
		}
	}
	return "", nil
}

func stepArities(steps []tla.Step) map[string]int {
	arity := map[string]int{}
	for _, s := range steps {
		if n, seen := arity[s.Name]; seen && n != len(s.Args) {
			arity[s.Name] = -1
		} else {
			arity[s.Name] = len(s.Args)
		}
	}
	return arity
}

const triedBatch = 1000

type triedEntry struct {
	state, next int
	text        diskText
}

type triedKey struct {
	state int
	hash  [32]byte
}

// triedEntries keeps each state's distinct action instances on disk. Even a
// single state's set need not fit in memory; sorting compares disk text and
// generation copies one instance at a time.
func triedEntries(ctx context.Context, rec recorded, arity map[string]int) ([]triedEntry, string, error) {
	var entries []triedEntry
	index := map[triedKey]int{}
	s := rec.store
	for _, ref := range rec.attempts {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		a, err := s.attempt(ref)
		if err != nil {
			return nil, "", err
		}
		n, ok := arity[a.step]
		switch {
		case !ok:
			return nil, fmt.Sprintf("the driver tried %s, a step the model's Next doesn't name", a.step), nil
		case n >= 0 && n != len(a.args):
			return nil, fmt.Sprintf("the driver tried %s with %d arguments, and Next passes it %d", a.step, len(a.args), n), nil
		}
		text := instance(a.step, a.args)
		key := triedKey{state: a.from, hash: sha256.Sum256([]byte(text))}
		seen := false
		for link := index[key]; link != 0; link = entries[link-1].next {
			equal, err := s.equal(entries[link-1].text, text)
			if err != nil {
				return nil, "", err
			}
			if equal {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		ref, err := s.put(text)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, triedEntry{state: a.from, text: ref, next: index[key]})
		index[key] = len(entries)
	}
	err := sortEntries(ctx, entries, func(a, b triedEntry) (int, error) {
		if a.state < b.state {
			return -1, nil
		}
		if a.state > b.state {
			return 1, nil
		}
		return s.compare(a.text, b.text)
	})
	return entries, "", err
}

// checkTried checks every reached state at both bounds. State windows are
// lazy, and their variable-size tried sets are streamed directly to a module.
func checkTried(ctx context.Context, runner Runner, dir, root, module, header string, constants map[string]string, vars []string, m Model, rec recorded) (Tried, error) {
	s := rec.store
	t := Tried{Attempts: len(rec.attempts), States: len(s.states)}
	if m.Problem != "" {
		t.Message = m.Problem
		return t, nil
	}
	if len(m.Steps) == 0 {
		t.Message = "the model names no steps, so the driver's attempts can't be checked"
		return t, nil
	}
	tried, problem, err := triedEntries(ctx, rec, stepArities(m.Steps))
	if err != nil {
		return t, err
	}
	if problem != "" {
		t.Message = problem
		return t, nil
	}
	ranges := make([]int, len(s.states)+1)
	pos := 0
	for i := range s.states {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		ranges[i] = pos
		for pos < len(tried) && tried[pos].state == i {
			if err := ctx.Err(); err != nil {
				return t, err
			}
			pos++
		}
	}
	ranges[len(s.states)] = pos
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
	b, res, err := atOnce(ctx, batchCount(len(s.states), triedBatch), func(ctx context.Context, b int) (tlc.Result, error) {
		return checkBatch(ctx, runner, dir, root, fmt.Sprintf("tried-%d", b+1), "Invariant_Tried",
			tlc.Config{Specification: "Invariant_TSpec", Constants: constants, Invariants: []string{"Invariant_EveryStepTried"}}, func(w *moduleWriter) error {
				start, end := b*triedBatch, min((b+1)*triedBatch, len(s.states))
				w.text("---- MODULE Invariant_Tried ----\n" + header + "VARIABLE invariant_i\n" + larger + "\nInvariant_States == <<")
				for i := start; i < end; i++ {
					if i > start {
						w.text(",\n  ")
					}
					w.value(s, s.states[i].text)
					if w.err != nil {
						return w.err
					}
				}
				w.text(">>\nInvariant_Tried == <<")
				for i := start; i < end; i++ {
					if i > start {
						w.text(",\n  ")
					}
					w.text("{")
					for j := ranges[i]; j < ranges[i+1]; j++ {
						if j > ranges[i] {
							w.text(", ")
						}
						w.value(s, tried[j].text)
						if w.err != nil {
							return w.err
						}
					}
					w.text("}")
				}
				w.text(">>\nInvariant_TInit == \\E i \\in DOMAIN Invariant_States : invariant_i = i /\\ " + is("Invariant_States[i]", vars, false) + "\n" +
					"Invariant_TSpec == Invariant_TInit /\\ [][UNCHANGED <<vars, invariant_i>>]_<<vars, invariant_i>>\n" +
					"Invariant_Untried ==\n  " + untried + "\n" +
					"Invariant_EveryStepTried == Invariant_Untried = {} \\/ (PrintT(<<\"invariant-untried\", Invariant_Untried>>) /\\ FALSE)\n====\n")
				return nil
			})
	})
	if err != nil {
		return t, err
	}
	switch {
	case res.Outcome == tlc.Violated && res.Invariant == "Invariant_EveryStepTried":
		t.Untried = printedUntried(res.Printed)
		start := b * triedBatch
		if i := stateIndex(res.Trace); i >= 1 && start+i-1 < len(s.states) {
			t.In, err = s.pretty(start + i - 1)
			if err != nil {
				return t, err
			}
		}
		t.Message = "the driver never tried a step that more than a bound rules out"
		return t, nil
	case res.Outcome != tlc.Passed:
		t.Message = "TLC couldn't check the driver's attempts: " + res.Message
		return t, nil
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

// printedUntried finds the untried steps TLC printed, as TLA+. TLC prints a
// tuple that fits on a line as <<a, b>>, and one it wraps as << a, b >>.
func printedUntried(printed []string) string {
	const head = `<<"invariant-untried",`
	tight := strings.NewReplacer("<< ", "<<", " >>", ">>")
	for _, p := range printed {
		p = tight.Replace(strings.Join(strings.Fields(p), " "))
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
