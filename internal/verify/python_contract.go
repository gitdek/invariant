package verify

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// A function proved in place states what it asks of a call and what it
// promises back, and Nagini proves its body keeps the promise. Its callers
// are ordinary code nothing proves, and where the application runs, the
// stand-in for nagini_contracts makes every Requires and Ensures a no-op.
// So the gate checks the contract itself, on each call of the function it
// records (#838): the reader that builds the proof's input also reads the
// contract into the trees below, and the gate evaluates them on the recorded
// arguments and result, with Python's meaning for the few expressions a
// contract may hold and exact integers. Nothing of the application runs.

// PythonContract is what a selected function's contract asks of a call:
// each parameter's type as its annotation names it, the result's, and each
// Requires, Ensures, Exsures and Decreases at its head.
type PythonContract struct {
	Parameters  []PythonContractParameter  `json:"parameters"`
	Returns     PythonContractType         `json:"returns"`
	Obligations []PythonContractObligation `json:"obligations"`
}

type PythonContractParameter struct {
	Name string             `json:"name"`
	Type PythonContractType `json:"type"`
}

// PythonContractType is a builtin type, by K: int, bool, str, none, object,
// bytes, float, complex, range, or list, set, dict and tuple with what they
// hold, a union of types, or unknown with the annotation's text.
type PythonContractType struct {
	K     string               `json:"k"`
	Of    *PythonContractType  `json:"of,omitempty"`
	Key   *PythonContractType  `json:"key,omitempty"`
	Items []PythonContractType `json:"items,omitempty"`
	Text  string               `json:"text,omitempty"`
}

// PythonContractObligation is one contract statement, as written and as the
// tree the gate evaluates. Decreases has none. Exsures names its exception.
type PythonContractObligation struct {
	Kind      string              `json:"kind"`
	Line      int                 `json:"line"`
	Text      string              `json:"text"`
	Exception string              `json:"exception,omitempty"`
	Expr      *PythonContractExpr `json:"expr,omitempty"`
}

// PythonContractExpr is one node of a contract's condition, by K: name (a
// parameter), int, bool, str, none, tuple, binop, unary, and, or, if,
// compare, call, type (isinstance's second argument), and unresolved, which
// says why the gate can't evaluate what stands there.
type PythonContractExpr struct {
	K     string               `json:"k"`
	ID    string               `json:"id,omitempty"`
	V     string               `json:"v,omitempty"`
	Op    string               `json:"op,omitempty"`
	F     string               `json:"f,omitempty"`
	Why   string               `json:"why,omitempty"`
	L     *PythonContractExpr  `json:"l,omitempty"`
	R     *PythonContractExpr  `json:"r,omitempty"`
	E     *PythonContractExpr  `json:"e,omitempty"`
	Test  *PythonContractExpr  `json:"test,omitempty"`
	Then  *PythonContractExpr  `json:"then,omitempty"`
	Else  *PythonContractExpr  `json:"else,omitempty"`
	Ops   []string             `json:"ops,omitempty"`
	Items []PythonContractExpr `json:"items,omitempty"`
	Rest  []PythonContractExpr `json:"rest,omitempty"`
	Args  []PythonContractExpr `json:"args,omitempty"`
}

// pythonContractProofOnly are the permissions and predicates a contract may
// state. They say what the function may read and write, which Python doesn't
// track, so where the application runs they hold by definition. Nagini
// proves them, and the gate lists them apart from what it evaluated.
var pythonContractProofOnly = []string{"Acc", "Rd", "Wildcard", "list_pred", "dict_pred", "set_pred", "MayCreate", "Low", "LowVal"}

// PythonContractFinding is one clause a recorded call didn't meet, or one
// the gate couldn't evaluate on it.
type PythonContractFinding struct {
	// Clause is the contract statement as written, or a parameter with its
	// type, as "limit: int".
	Clause  string `json:"clause"`
	Line    int    `json:"line,omitempty"`
	Verdict string `json:"verdict"` // violated or unresolved
	Why     string `json:"why,omitempty"`
}

// Classes says how the gate treats each clause of the contract: evaluated
// on every recorded call, evaluated apart from the permissions it also
// states, or proof-only, which Nagini proves and no run can check.
func (c *PythonContract) Classes() (checked, partly, proofOnly []string) {
	for _, o := range c.Obligations {
		switch executable, permissions := o.Expr.parts(); {
		case o.Expr == nil || !executable:
			proofOnly = append(proofOnly, o.Text)
		case permissions:
			partly = append(partly, o.Text)
		default:
			checked = append(checked, o.Text)
		}
	}
	return checked, partly, proofOnly
}

// parts says whether the tree holds anything the gate evaluates, and
// whether it holds a permission.
func (e *PythonContractExpr) parts() (executable, permissions bool) {
	if e == nil {
		return false, false
	}
	if e.K == "call" && slices.Contains(pythonContractProofOnly, e.F) {
		return false, true
	}
	children := []*PythonContractExpr{e.L, e.R, e.E, e.Test, e.Then, e.Else}
	for _, list := range [][]PythonContractExpr{e.Items, e.Rest, e.Args} {
		for i := range list {
			children = append(children, &list[i])
		}
	}
	// A conjunction of permissions alone states nothing to evaluate.
	executable = e.K != "and"
	for _, child := range children {
		if child == nil {
			continue
		}
		x, p := child.parts()
		executable, permissions = executable || x, permissions || p
	}
	return executable, permissions
}

// Check evaluates the contract on one recorded call. evaluated counts the
// clauses it could tell held or didn't. A call whose arguments aren't of
// the types the function's annotations name is reported for those alone:
// the proof assumes them, and the rest of the contract means nothing on
// other values.
func (c *PythonContract) Check(call PythonApplicationCall) (evaluated int, findings []PythonContractFinding) {
	entry := map[string]any{}
	for name, v := range call.Arguments {
		entry[name] = pythonContractValue(v)
	}
	exit := map[string]any{}
	for name, v := range entry {
		exit[name] = v
	}
	for name, v := range call.After {
		exit[name] = pythonContractValue(v)
	}
	typed := true
	for _, p := range c.Parameters {
		clause := p.Name + ": " + p.Type.String()
		v, recorded := entry[p.Name]
		if kind, ok := call.Unrecorded[p.Name]; ok {
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "unresolved", Why: fmt.Sprintf("the record can't hold %s, a %s", p.Name, kind)})
			typed = false
			continue
		}
		if !recorded {
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "unresolved", Why: "the record holds no argument for " + p.Name})
			typed = false
			continue
		}
		switch ok, known := p.Type.holds(v); {
		case !known:
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "unresolved", Why: "the gate can't tell what type the annotation " + p.Type.String() + " names"})
			typed = false
		case !ok:
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "violated", Why: fmt.Sprintf("%s was %s", p.Name, pythonContractShow(v))})
			typed = false
		default:
			evaluated++
		}
	}
	if !typed {
		return evaluated, findings
	}
	judge := func(o PythonContractObligation, env pythonContractEnv) {
		v, err := env.eval(o.Expr, true)
		holds := false
		if err == nil {
			holds, err = pythonContractTruth(v)
		}
		switch {
		case err != nil:
			findings = append(findings, PythonContractFinding{Clause: o.Text, Line: o.Line, Verdict: "unresolved", Why: err.Error()})
		case !holds:
			evaluated++
			findings = append(findings, PythonContractFinding{Clause: o.Text, Line: o.Line, Verdict: "violated"})
		default:
			evaluated++
		}
	}
	result := pythonContractValue(call.Result)
	// Nothing about the result can be checked where the record can't hold
	// it; what the call was asked still can.
	kind, lost := call.Unrecorded["return"]
	if lost {
		findings = append(findings, PythonContractFinding{Clause: "returns " + c.Returns.String(), Verdict: "unresolved", Why: "the record can't hold the result, a " + kind})
	}
	declared := false
	for _, o := range c.Obligations {
		if executable, _ := o.Expr.parts(); o.Expr == nil || (!executable && o.Kind != "exsures") {
			continue
		}
		switch o.Kind {
		case "requires":
			judge(o, pythonContractEnv{names: entry, entry: entry})
		case "ensures":
			if call.Raised == nil && !lost {
				judge(o, pythonContractEnv{names: exit, entry: entry, result: result, returned: true})
			}
		case "exsures":
			if call.Raised != nil && slices.Contains(call.Raised.Builtins, o.Exception) {
				declared = true
				if executable, _ := o.Expr.parts(); executable {
					judge(o, pythonContractEnv{names: exit, entry: entry})
				}
			}
		}
	}
	switch {
	case call.Raised != nil && !declared:
		findings = append(findings, PythonContractFinding{Clause: "raises only what its Exsures declare", Verdict: "violated",
			Why: "it raised " + call.Raised.Type})
		evaluated++
	case call.Raised != nil:
		evaluated++
	case lost:
	case c.Returns.K == "unknown" && c.Returns.Text == "":
		// No annotation states what it returns.
	default:
		clause := "returns " + c.Returns.String()
		switch ok, known := c.Returns.holds(result); {
		case !known:
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "unresolved", Why: "the gate can't tell what type the annotation " + c.Returns.String() + " names"})
		case !ok:
			findings = append(findings, PythonContractFinding{Clause: clause, Verdict: "violated", Why: "it returned " + pythonContractShow(result)})
			evaluated++
		default:
			evaluated++
		}
	}
	return evaluated, findings
}

// String writes a type as an annotation would.
func (t PythonContractType) String() string {
	switch t.K {
	case "none":
		return "None"
	case "unknown":
		return t.Text
	case "union":
		parts := make([]string, len(t.Items))
		for i, item := range t.Items {
			parts[i] = item.String()
		}
		return "Union[" + strings.Join(parts, ", ") + "]"
	case "list", "set":
		if t.Of != nil {
			return strings.ToUpper(t.K[:1]) + t.K[1:] + "[" + t.Of.String() + "]"
		}
	case "dict":
		if t.Key != nil && t.Of != nil {
			return "Dict[" + t.Key.String() + ", " + t.Of.String() + "]"
		}
	case "tuple":
		if t.Of != nil {
			return "Tuple[" + t.Of.String() + ", ...]"
		}
		if t.Items != nil {
			parts := make([]string, len(t.Items))
			for i, item := range t.Items {
				parts[i] = item.String()
			}
			return "Tuple[" + strings.Join(parts, ", ") + "]"
		}
	}
	return t.K
}

// holds says whether a recorded value is of the type, and whether the gate
// can tell. The collector records only None, bools, ints, floats, strings,
// lists and dictionaries with string keys, each of exactly that type, so a
// recorded value is never a tuple, a set, bytes or a range.
func (t PythonContractType) holds(v any) (ok, known bool) {
	switch t.K {
	case "object":
		return true, true
	case "none":
		return v == nil, true
	case "bool":
		_, ok := v.(bool)
		return ok, true
	case "int":
		switch v.(type) {
		case bool, *big.Int:
			return true, true
		}
		return false, true
	case "float":
		switch v.(type) {
		case bool, *big.Int, pythonFloat:
			return true, true
		}
		return false, true
	case "str":
		_, ok := v.(string)
		return ok, true
	case "list":
		list, ok := v.([]any)
		if !ok {
			return false, true
		}
		for _, item := range list {
			if t.Of == nil {
				break
			}
			if ok, known := t.Of.holds(item); !ok || !known {
				return ok, known
			}
		}
		return true, true
	case "dict":
		dict, ok := v.(map[string]any)
		if !ok {
			return false, true
		}
		for _, key := range sortedKeys(dict) {
			if t.Key != nil {
				if ok, known := t.Key.holds(key); !ok || !known {
					return ok, known
				}
			}
			if t.Of != nil {
				if ok, known := t.Of.holds(dict[key]); !ok || !known {
					return ok, known
				}
			}
		}
		return true, true
	case "tuple", "set", "bytes", "range", "complex":
		return false, true
	case "union":
		known := true
		for _, item := range t.Items {
			ok, k := item.holds(v)
			if ok && k {
				return true, true
			}
			known = known && k
		}
		return false, known
	}
	return false, false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pythonFloat is a recorded float, by its JSON text. No contract a proof
// takes holds one, so the gate tells it apart and evaluates nothing on it.
type pythonFloat string

// pythonTuple is a tuple a contract writes out. A recorded value is never one.
type pythonTuple []any

var pythonContractInteger = regexp.MustCompile(`^-?[0-9]+$`)

// pythonContractValue reads a recorded JSON value as the Python value it
// was copied from.
func pythonContractValue(v any) any {
	switch v := v.(type) {
	case json.Number:
		if pythonContractInteger.MatchString(string(v)) {
			n, _ := new(big.Int).SetString(string(v), 10)
			return n
		}
		return pythonFloat(v)
	case float64:
		// A value decoded without UseNumber: exact only as an integer.
		if v == float64(int64(v)) && v > -1<<53 && v < 1<<53 {
			return big.NewInt(int64(v))
		}
		return pythonFloat(fmt.Sprint(v))
	case int:
		return big.NewInt(int64(v))
	case int64:
		return big.NewInt(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = pythonContractValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = pythonContractValue(item)
		}
		return out
	}
	return v
}

// pythonContractShow writes a value as Python's repr would, cut short.
func pythonContractShow(v any) string {
	var b strings.Builder
	var write func(v any)
	write = func(v any) {
		if b.Len() > 200 {
			return
		}
		switch v := v.(type) {
		case nil:
			b.WriteString("None")
		case bool:
			if v {
				b.WriteString("True")
			} else {
				b.WriteString("False")
			}
		case *big.Int:
			b.WriteString(v.String())
		case pythonFloat:
			b.WriteString(string(v))
		case string:
			quoted, _ := json.Marshal(v)
			b.Write(quoted)
		case []any, pythonTuple:
			open, shut, items := "[", "]", []any(nil)
			if t, ok := v.(pythonTuple); ok {
				open, shut, items = "(", ")", t
			} else {
				items = v.([]any)
			}
			b.WriteString(open)
			for i, item := range items {
				if i > 0 {
					b.WriteString(", ")
				}
				write(item)
			}
			b.WriteString(shut)
		case map[string]any:
			b.WriteString("{")
			for i, key := range sortedKeys(v) {
				if i > 0 {
					b.WriteString(", ")
				}
				write(key)
				b.WriteString(": ")
				write(v[key])
			}
			b.WriteString("}")
		default:
			fmt.Fprintf(&b, "%v", v)
		}
	}
	write(v)
	if b.Len() > 200 {
		return b.String()[:200] + "…"
	}
	return b.String()
}

// pythonContractEnv is what a clause reads: its names' values, the values
// on entry, which Old reads, and the result where the call returned one.
type pythonContractEnv struct {
	names, entry map[string]any
	result       any
	returned     bool
}

func unresolvedf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// pythonContractInt reads a value as the int Python's arithmetic takes it
// for: an int, or a bool.
func pythonContractInt(v any) (*big.Int, bool) {
	switch v := v.(type) {
	case *big.Int:
		return v, true
	case bool:
		if v {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	}
	return nil, false
}

// pythonContractString refuses a string Go's JSON decoder may have changed:
// it reads an unpaired surrogate as U+FFFD, so one holding U+FFFD may not
// be the string the application passed.
func pythonContractString(v any) (string, bool, error) {
	s, ok := v.(string)
	if ok && strings.ContainsRune(s, utf8.RuneError) {
		return "", true, unresolvedf("a string holds U+FFFD, which may stand for a character the record couldn't keep")
	}
	return s, ok, nil
}

// pythonContractTruth is Python's truth of a value.
func pythonContractTruth(v any) (bool, error) {
	switch v := v.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case *big.Int:
		return v.Sign() != 0, nil
	case string:
		return v != "", nil
	case []any:
		return len(v) > 0, nil
	case pythonTuple:
		return len(v) > 0, nil
	case map[string]any:
		return len(v) > 0, nil
	}
	return false, unresolvedf("takes the truth of %s", pythonContractShow(v))
}

// pythonContractEqual is Python's ==, on the values a record can hold.
func pythonContractEqual(a, b any) (bool, error) {
	if x, ok := pythonContractInt(a); ok {
		if y, ok := pythonContractInt(b); ok {
			return x.Cmp(y) == 0, nil
		}
	}
	for _, v := range []any{a, b} {
		if _, ok := v.(pythonFloat); ok {
			return false, unresolvedf("compares the float %s", pythonContractShow(v))
		}
	}
	switch x := a.(type) {
	case nil:
		return b == nil, nil
	case string:
		if _, _, err := pythonContractString(x); err != nil {
			return false, err
		}
		y, ok, err := pythonContractString(b)
		return ok && x == y, err
	case []any:
		y, ok := b.([]any)
		return pythonContractEqualAll(x, y, ok)
	case pythonTuple:
		y, ok := b.(pythonTuple)
		return pythonContractEqualAll(x, y, ok)
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false, nil
		}
		for _, key := range sortedKeys(x) {
			other, ok := y[key]
			if !ok {
				return false, nil
			}
			if same, err := pythonContractEqual(x[key], other); err != nil || !same {
				return false, err
			}
		}
		return true, nil
	}
	if _, _, err := pythonContractString(b); err != nil {
		return false, err
	}
	return false, nil
}

func pythonContractEqualAll(x, y []any, same bool) (bool, error) {
	if !same || len(x) != len(y) {
		return false, nil
	}
	for i := range x {
		if equal, err := pythonContractEqual(x[i], y[i]); err != nil || !equal {
			return false, err
		}
	}
	return true, nil
}

// pythonContractOrder is Python's ordering of two ints or two strings, as
// -1, 0 or 1. Python orders strings by code point, as their UTF-8 does.
func pythonContractOrder(a, b any) (int, error) {
	if x, ok := pythonContractInt(a); ok {
		if y, ok := pythonContractInt(b); ok {
			return x.Cmp(y), nil
		}
	}
	x, xs, err := pythonContractString(a)
	if err != nil {
		return 0, err
	}
	y, ys, err := pythonContractString(b)
	if err != nil {
		return 0, err
	}
	if xs && ys {
		return strings.Compare(x, y), nil
	}
	// Two lists, or two tuples, order by the first items that differ, and
	// by length where none does.
	left, kind := pythonContractItems(a)
	right, other := pythonContractItems(b)
	if kind == "" || kind != other {
		return 0, unresolvedf("orders %s and %s, which Python doesn't order", pythonContractShow(a), pythonContractShow(b))
	}
	for i := 0; i < len(left) && i < len(right); i++ {
		same, err := pythonContractEqual(left[i], right[i])
		if err != nil {
			return 0, err
		}
		if !same {
			return pythonContractOrder(left[i], right[i])
		}
	}
	return len(left) - len(right), nil
}

// pythonContractItems is a list's or a tuple's items, and which it is.
func pythonContractItems(v any) ([]any, string) {
	switch v := v.(type) {
	case []any:
		return v, "list"
	case pythonTuple:
		return v, "tuple"
	}
	return nil, ""
}

// pythonContractContains is Python's in.
func pythonContractContains(item, container any) (bool, error) {
	if items, kind := pythonContractItems(container); kind != "" {
		for _, x := range items {
			if same, err := pythonContractEqual(item, x); err != nil || same {
				return same, err
			}
		}
		return false, nil
	}
	switch c := container.(type) {
	case string:
		whole, _, err := pythonContractString(c)
		if err != nil {
			return false, err
		}
		part, ok, err := pythonContractString(item)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, unresolvedf("tests %s for membership in a string", pythonContractShow(item))
		}
		return strings.Contains(whole, part), nil
	case map[string]any:
		switch key := item.(type) {
		case string:
			if _, _, err := pythonContractString(key); err != nil {
				return false, err
			}
			_, ok := c[key]
			return ok, nil
		}
		// Python hashes what it looks up, and a list or a dictionary has no
		// hash. Nothing else a record or a contract holds is a string key.
		if pythonContractHashable(item) {
			return false, nil
		}
		return false, unresolvedf("tests %s for membership in a dictionary", pythonContractShow(item))
	}
	return false, unresolvedf("tests membership in %s", pythonContractShow(container))
}

func pythonContractHashable(v any) bool {
	switch v := v.(type) {
	case nil, bool, *big.Int, string:
		return true
	case pythonTuple:
		return !slices.ContainsFunc(v, func(item any) bool { return !pythonContractHashable(item) })
	}
	return false
}

// pythonContractRepeat is the longest sequence the gate builds for x * n.
const pythonContractRepeat = 1 << 16

func (env pythonContractEnv) eval(e *PythonContractExpr, positive bool) (any, error) {
	if e == nil {
		return nil, unresolvedf("holds nothing to evaluate")
	}
	switch e.K {
	case "unresolved":
		return nil, unresolvedf("%s", e.Why)
	case "name":
		v, ok := env.names[e.ID]
		if !ok {
			return nil, unresolvedf("reads %s, which the record doesn't hold", e.ID)
		}
		return v, nil
	case "none":
		return nil, nil
	case "bool":
		return e.V == "true", nil
	case "int":
		n, ok := new(big.Int).SetString(e.V, 10)
		if !ok {
			return nil, unresolvedf("holds an integer the gate can't read")
		}
		return n, nil
	case "str":
		return e.V, nil
	case "tuple":
		out := make(pythonTuple, len(e.Items))
		for i := range e.Items {
			v, err := env.eval(&e.Items[i], false)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case "and", "or":
		var last any
		for i := range e.Items {
			// A permission holds beside what an and states with it.
			v, err := env.eval(&e.Items[i], positive && e.K == "and")
			if err != nil {
				return nil, err
			}
			truth, err := pythonContractTruth(v)
			if err != nil {
				return nil, err
			}
			last = v
			if truth == (e.K == "or") {
				return v, nil
			}
		}
		return last, nil
	case "unary":
		v, err := env.eval(e.E, false)
		if err != nil {
			return nil, err
		}
		if e.Op == "not" {
			truth, err := pythonContractTruth(v)
			return !truth, err
		}
		n, ok := pythonContractInt(v)
		if !ok {
			return nil, unresolvedf("negates %s", pythonContractShow(v))
		}
		if e.Op == "-" {
			return new(big.Int).Neg(n), nil
		}
		return new(big.Int).Set(n), nil
	case "if":
		v, err := env.eval(e.Test, false)
		if err != nil {
			return nil, err
		}
		truth, err := pythonContractTruth(v)
		if err != nil {
			return nil, err
		}
		if truth {
			return env.eval(e.Then, positive)
		}
		return env.eval(e.Else, positive)
	case "binop":
		l, err := env.eval(e.L, false)
		if err != nil {
			return nil, err
		}
		r, err := env.eval(e.R, false)
		if err != nil {
			return nil, err
		}
		return pythonContractArithmetic(e.Op, l, r)
	case "compare":
		if len(e.Ops) != len(e.Rest) || len(e.Ops) == 0 {
			return nil, unresolvedf("holds a comparison the gate can't read")
		}
		left, err := env.eval(e.L, false)
		if err != nil {
			return nil, err
		}
		for i, op := range e.Ops {
			right, err := env.eval(&e.Rest[i], false)
			if err != nil {
				return nil, err
			}
			holds, err := pythonContractCompare(op, left, right)
			if err != nil || !holds {
				return false, err
			}
			left = right
		}
		return true, nil
	case "call":
		return env.call(e, positive)
	}
	return nil, unresolvedf("holds what the gate doesn't evaluate")
}

func pythonContractCompare(op string, left, right any) (bool, error) {
	switch op {
	case "==", "!=":
		same, err := pythonContractEqual(left, right)
		return same == (op == "=="), err
	case "<", "<=", ">", ">=":
		order, err := pythonContractOrder(left, right)
		if err != nil {
			return false, err
		}
		return map[string]bool{"<": order < 0, "<=": order <= 0, ">": order > 0, ">=": order >= 0}[op], nil
	case "in", "not in":
		holds, err := pythonContractContains(left, right)
		return holds == (op == "in"), err
	case "is", "is not":
		// None, True and False are each one object, and a record holds a
		// bool only for a bool, so identity with one of them is equality of
		// type and value. No record says whether two other values are one.
		same := false
		switch {
		case left == nil || right == nil:
			same = left == nil && right == nil
		default:
			x, xb := left.(bool)
			y, yb := right.(bool)
			if !xb && !yb {
				return false, unresolvedf("compares identities with is, which the record doesn't hold, other than with None, True or False")
			}
			same = xb && yb && x == y
		}
		return same == (op == "is"), nil
	}
	return false, unresolvedf("holds the comparison %s, which the gate doesn't evaluate", op)
}

func pythonContractArithmetic(op string, l, r any) (any, error) {
	x, xi := pythonContractInt(l)
	y, yi := pythonContractInt(r)
	if xi && yi {
		switch op {
		case "+":
			return new(big.Int).Add(x, y), nil
		case "-":
			return new(big.Int).Sub(x, y), nil
		case "*":
			return new(big.Int).Mul(x, y), nil
		case "//", "%":
			if y.Sign() == 0 {
				return nil, unresolvedf("divides by zero")
			}
			// Python floors the quotient, and the remainder takes the
			// divisor's sign. DivMod gives a remainder that's never negative.
			q, m := new(big.Int).DivMod(x, y, new(big.Int))
			if y.Sign() < 0 && m.Sign() != 0 {
				q.Sub(q, big.NewInt(1))
				m.Add(m, y)
			}
			if op == "//" {
				return q, nil
			}
			return m, nil
		}
	}
	switch op {
	case "+":
		switch a := l.(type) {
		case string:
			if b, ok := r.(string); ok {
				return a + b, nil
			}
		case []any:
			if b, ok := r.([]any); ok {
				return append(slices.Clone(a), b...), nil
			}
		case pythonTuple:
			if b, ok := r.(pythonTuple); ok {
				return append(slices.Clone(a), b...), nil
			}
		}
	case "*":
		seq, count := l, y
		if xi {
			seq, count = r, x
		}
		if count == nil {
			break
		}
		// Python fails on a count it can't index by, a negative one too.
		if !count.IsInt64() {
			return nil, unresolvedf("repeats a sequence by a count Python can't index by")
		}
		n := 0
		if count.Sign() > 0 {
			if count.Int64() > pythonContractRepeat {
				return nil, unresolvedf("repeats a sequence more often than the gate builds")
			}
			n = int(count.Int64())
		}
		switch a := seq.(type) {
		case string:
			if n*len(a) > pythonContractRepeat {
				return nil, unresolvedf("repeats a sequence more often than the gate builds")
			}
			return strings.Repeat(a, n), nil
		}
		if items, kind := pythonContractItems(seq); kind != "" {
			if n*len(items) > pythonContractRepeat {
				return nil, unresolvedf("repeats a sequence more often than the gate builds")
			}
			out := make([]any, 0, n*len(items))
			for range n {
				out = append(out, items...)
			}
			if kind == "tuple" {
				return pythonTuple(out), nil
			}
			return out, nil
		}
	}
	return nil, unresolvedf("applies %s to %s and %s", op, pythonContractShow(l), pythonContractShow(r))
}

func (env pythonContractEnv) call(e *PythonContractExpr, positive bool) (any, error) {
	if slices.Contains(pythonContractProofOnly, e.F) {
		if !positive {
			return nil, unresolvedf("states the permission %s where the gate can't take it as holding: under a not or an or, in a comparison, or as a condition", e.F)
		}
		return true, nil
	}
	arity := map[string]int{"Result": 0, "Old": 1, "len": 1, "abs": 1, "Implies": 2, "isinstance": 2}
	if n, ok := arity[e.F]; !ok || len(e.Args) != n {
		return nil, unresolvedf("calls %s, which the gate doesn't evaluate", e.F)
	}
	switch e.F {
	case "Result":
		if !env.returned {
			return nil, unresolvedf("reads Result() where the call returns nothing to read")
		}
		return env.result, nil
	case "Old":
		return pythonContractEnv{names: env.entry, entry: env.entry}.eval(&e.Args[0], false)
	case "Implies":
		v, err := env.eval(&e.Args[0], false)
		if err != nil {
			return nil, err
		}
		truth, err := pythonContractTruth(v)
		if err != nil || !truth {
			return true, err
		}
		return env.eval(&e.Args[1], positive)
	case "isinstance":
		v, err := env.eval(&e.Args[0], false)
		if err != nil {
			return nil, err
		}
		if e.Args[1].K != "type" {
			return nil, unresolvedf("tests isinstance against what the gate can't read")
		}
		switch e.Args[1].ID {
		case "object":
			return true, nil
		case "int":
			_, ok := pythonContractInt(v)
			return ok, nil
		case "bool":
			_, ok := v.(bool)
			return ok, nil
		case "str":
			_, ok := v.(string)
			return ok, nil
		case "list":
			_, ok := v.([]any)
			return ok, nil
		case "dict":
			_, ok := v.(map[string]any)
			return ok, nil
		case "tuple":
			_, ok := v.(pythonTuple)
			return ok, nil
		case "bytes", "set":
			return false, nil
		}
		return nil, unresolvedf("tests isinstance against %s, which the gate doesn't evaluate", e.Args[1].ID)
	}
	v, err := env.eval(&e.Args[0], false)
	if err != nil {
		return nil, err
	}
	if e.F == "abs" {
		n, ok := pythonContractInt(v)
		if !ok {
			return nil, unresolvedf("takes the abs of %s", pythonContractShow(v))
		}
		return new(big.Int).Abs(n), nil
	}
	switch v := v.(type) {
	case string:
		s, _, err := pythonContractString(v)
		return big.NewInt(int64(utf8.RuneCountInString(s))), err
	case []any:
		return big.NewInt(int64(len(v))), nil
	case pythonTuple:
		return big.NewInt(int64(len(v))), nil
	case map[string]any:
		return big.NewInt(int64(len(v))), nil
	}
	return nil, unresolvedf("takes the len of %s", pythonContractShow(v))
}
