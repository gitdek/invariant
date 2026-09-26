// Package conformance tests code against its model. A driver runs the real
// code: it calls its operations at random and records each state in the
// spec's vocabulary. TLC then checks that every run starts in a state Init
// allows and that every step between two different states is a Next step.
// Refusals are fine, since a step that changes nothing is always allowed.
// What the model forbids, TLC catches.
//
// It's testing, not proof: it covers the runs the driver made, no more.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/tlc"
)

// Result is what conformance testing established.
type Result struct {
	Passed      bool   `json:"passed"`
	Runs        int    `json:"runs"`
	Steps       int    `json:"steps"`                // steps recorded, including refusals
	States      int    `json:"states"`               // distinct states the code visited
	ModelStates int64  `json:"model_states"`         // distinct states TLC found in the model
	Transitions int    `json:"transitions"`          // distinct steps that changed the state
	Exhaustive  bool   `json:"exhaustive,omitempty"` // the driver claims to explore every state the code can reach
	BadStart    string `json:"bad_start,omitempty"`
	BadStep     *Step  `json:"bad_step,omitempty"`
	Message     string `json:"message,omitempty"`
}

// Step is one step the code took that the model doesn't allow.
type Step struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Traces is what a conformance driver writes: each run is the sequence of
// states the code went through, each state a map from the spec's variables to
// values in the encoding Encode reads.
type Traces struct {
	Traces [][]map[string]any `json:"traces"`
}

// Check tests the traces against module, staged in dir, with TLC.
func Check(ctx context.Context, runner tlc.Runner, dir, module string, bounds map[string]string, vars []string, modelStates int64, raw []byte) (Result, error) {
	r := Result{ModelStates: modelStates}
	var t Traces
	if err := json.Unmarshal(raw, &t); err != nil {
		r.Message = "the driver's traces aren't valid JSON: " + err.Error()
		return r, nil
	}
	r.Runs = len(t.Traces)
	if r.Runs == 0 {
		r.Message = "the driver recorded no runs"
		return r, nil
	}
	index := map[string]int{}
	var states, pretty []string
	starts := map[int]bool{}
	steps := map[[2]int]bool{}
	for _, run := range t.Traces {
		prev := -1
		for n, s := range run {
			text, readable, err := encodeState(s, vars)
			if err != nil {
				r.Message = fmt.Sprintf("a recorded state can't be read: %v", err)
				return r, nil
			}
			i, seen := index[text]
			if !seen {
				i = len(states)
				index[text] = i
				states = append(states, text)
				pretty = append(pretty, readable)
			}
			if n == 0 {
				starts[i] = true
			} else {
				r.Steps++
				if i != prev {
					steps[[2]int{prev, i}] = true
				}
			}
			prev = i
		}
	}
	r.States, r.Transitions = len(states), len(steps)

	values := modelValues(bounds)
	cfgConstants := map[string]string{}
	for k, v := range bounds {
		cfgConstants[k] = v
	}
	for _, mv := range values {
		cfgConstants[mv] = mv
	}
	// TLC's standard module defines :> and @@, which the recorded functions use.
	header := fmt.Sprintf("EXTENDS %s, TLC\n", module)
	if len(values) > 0 {
		header += "CONSTANTS " + strings.Join(values, ", ") + "\n"
	}

	// Every run must start where Init allows.
	var startSet []string
	for i := range starts {
		startSet = append(startSet, states[i])
	}
	sort.Strings(startSet)
	initModule := "---- MODULE Invariant_ConformanceInit ----\n" + header +
		"Invariant_Starts == {" + strings.Join(startSet, ",\n  ") + "}\n" +
		"Invariant_IInit == \\E s \\in Invariant_Starts : " + is("s", vars, false) + "\n" +
		"Invariant_ISpec == Invariant_IInit /\\ [][UNCHANGED vars]_vars\n====\n"
	if err := os.WriteFile(filepath.Join(dir, "Invariant_ConformanceInit.tla"), []byte(initModule), 0o644); err != nil {
		return r, err
	}
	res, err := runner.Check(ctx, dir, "Invariant_ConformanceInit", tlc.Config{Specification: "Invariant_ISpec", Constants: cfgConstants, Invariants: []string{"Init"}})
	if err != nil {
		return r, err
	}
	switch {
	case res.Outcome == tlc.Violated && len(res.Trace) > 0:
		r.BadStart = stateText(res.Trace[len(res.Trace)-1], vars)
		r.Message = "a run starts in a state Init doesn't allow"
		return r, nil
	case res.Outcome != tlc.Passed:
		r.Message = "TLC couldn't check the starting states: " + res.Message
		return r, nil
	}

	// Every step that changed the state must be a Next step. TLC checks the
	// steps in batches, each carrying only the states it uses, so memory
	// stays flat however many steps the code took.
	var all [][2]int
	for p := range steps {
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool { return all[i][0] < all[j][0] || all[i][0] == all[j][0] && all[i][1] < all[j][1] })
	for _, batch := range batches(all, stepBatch) {
		local, pairs := renumber(batch)
		used := make([]string, len(local))
		for i, s := range local {
			used[i] = states[s]
		}
		stepModule := "---- MODULE Invariant_Conformance ----\n" + header +
			"VARIABLES invariant_source, invariant_target, invariant_done\n" +
			"Invariant_States == <<" + strings.Join(used, ",\n  ") + ">>\n" +
			"Invariant_Steps == {" + strings.Join(pairs, ", ") + "}\n" +
			"Invariant_CInit == \\E p \\in Invariant_Steps :\n" +
			"    /\\ " + is("Invariant_States[p[1]]", vars, false) + "\n" +
			"    /\\ invariant_source = p[1]\n    /\\ invariant_target = p[2]\n    /\\ invariant_done = FALSE\n" +
			"Invariant_CNext ==\n" +
			"    \\/ /\\ ~invariant_done\n       /\\ Next\n" +
			"       /\\ " + is("Invariant_States[invariant_target]", vars, true) + "\n" +
			"       /\\ invariant_done' = TRUE\n       /\\ UNCHANGED <<invariant_source, invariant_target>>\n" +
			"    \\/ /\\ invariant_done\n       /\\ UNCHANGED <<vars, invariant_source, invariant_target, invariant_done>>\n" +
			"Invariant_CSpec == Invariant_CInit /\\ [][Invariant_CNext]_<<vars, invariant_source, invariant_target, invariant_done>>\n====\n"
		if err := os.WriteFile(filepath.Join(dir, "Invariant_Conformance.tla"), []byte(stepModule), 0o644); err != nil {
			return r, err
		}
		res, err := runner.Check(ctx, dir, "Invariant_Conformance", tlc.Config{Specification: "Invariant_CSpec", Constants: cfgConstants})
		if err != nil {
			return r, err
		}
		switch {
		case res.Outcome == tlc.Deadlock && len(res.Trace) > 0:
			// The stuck state is the start of a step no Next step can make; it
			// carries the step's two ends as indexes into the recorded states.
			stuck := res.Trace[len(res.Trace)-1]
			step := &Step{From: stateText(stuck, vars)}
			for _, v := range stuck.Vars {
				i, err := strconv.Atoi(v.Value)
				if err != nil || i < 1 || i > len(local) {
					continue
				}
				switch v.Name {
				case "invariant_source":
					step.From = pretty[local[i-1]]
				case "invariant_target":
					step.To = pretty[local[i-1]]
				}
			}
			r.BadStep = step
			r.Message = "the code took a step the model doesn't allow"
			return r, nil
		case res.Outcome != tlc.Passed:
			r.Message = "TLC couldn't check the steps: " + res.Message
			return r, nil
		}
	}
	r.Passed = true
	return r, nil
}

// stepBatch is how many steps TLC checks at once. A batch's module holds
// only the states its steps use, so its size doesn't grow with the model.
const stepBatch = 2000

// batches splits steps into runs of at most n.
func batches(steps [][2]int, n int) [][][2]int {
	var out [][][2]int
	for len(steps) > n {
		out = append(out, steps[:n])
		steps = steps[n:]
	}
	if len(steps) > 0 {
		out = append(out, steps)
	}
	return out
}

// renumber gives the states a batch uses indexes of their own, from 1:
// local[i-1] is the recorded state behind local index i, and pairs are the
// batch's steps as TLA+ tuples of local indexes.
func renumber(batch [][2]int) (local []int, pairs []string) {
	index := map[int]int{}
	id := func(s int) int {
		if i, ok := index[s]; ok {
			return i
		}
		local = append(local, s)
		index[s] = len(local)
		return len(local)
	}
	for _, p := range batch {
		pairs = append(pairs, fmt.Sprintf("<<%d, %d>>", id(p[0]), id(p[1])))
	}
	return local, pairs
}

// is states that the variables equal the fields of record expression s, or,
// when primed, that the next values do.
func is(s string, vars []string, primed bool) string {
	parts := make([]string, len(vars))
	for i, v := range vars {
		p := ""
		if primed {
			p = "'"
		}
		parts[i] = fmt.Sprintf("%s%s = (%s).%s", v, p, s, v)
	}
	return strings.Join(parts, " /\\ ")
}

// encodeState renders a recorded state as a TLA+ record, and readably as
// name = value pairs.
func encodeState(s map[string]any, vars []string) (record, readable string, err error) {
	if len(s) != len(vars) {
		return "", "", fmt.Errorf("want exactly the spec's variables %v, got %d fields", vars, len(s))
	}
	fields := make([]string, len(vars))
	pairs := make([]string, len(vars))
	for i, v := range vars {
		value, ok := s[v]
		if !ok {
			return "", "", fmt.Errorf("variable %s is missing", v)
		}
		text, err := Encode(value)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", v, err)
		}
		fields[i] = v + " |-> " + text
		pairs[i] = v + " = " + text
	}
	return "[" + strings.Join(fields, ", ") + "]", strings.Join(pairs, ", "), nil
}

func stateText(s tlc.State, vars []string) string {
	want := map[string]bool{}
	for _, v := range vars {
		want[v] = true
	}
	var parts []string
	for _, v := range s.Vars {
		if want[v.Name] {
			parts = append(parts, v.Name+" = "+v.Value)
		}
	}
	return strings.Join(parts, ", ")
}

// Encode turns a decoded JSON value into canonical TLA+ text. Arrays are
// ambiguous in TLA+, so they must say what they are:
//
//	"text", 42, true                  strings, integers, booleans
//	{"$set": [...]}                   a set
//	{"$seq": [...]}                   a sequence
//	{"$fn": [[key, value], ...]}      a function
//	{"$mv": "r1"}                     a model value from the bounds
//	{"field": value, ...}             a record
//
// Sets, functions and records render in a canonical order, so equal values
// always have equal text.
func Encode(v any) (string, error) {
	switch x := v.(type) {
	case string:
		b, _ := json.Marshal(x)
		return string(b), nil
	case bool:
		if x {
			return "TRUE", nil
		}
		return "FALSE", nil
	case float64:
		if x != float64(int64(x)) {
			return "", fmt.Errorf("%v isn't an integer; TLA+ has no fractions", x)
		}
		return strconv.FormatInt(int64(x), 10), nil
	case []any:
		return "", fmt.Errorf(`a bare array is ambiguous: write {"$set": [...]} or {"$seq": [...]}`)
	case map[string]any:
		if len(x) == 1 {
			for tag, inner := range x {
				switch tag {
				case "$set":
					return setText(inner)
				case "$seq":
					items, err := encodeAll(inner)
					return "<<" + strings.Join(items, ", ") + ">>", err
				case "$fn":
					return fnText(inner)
				case "$mv":
					name, ok := inner.(string)
					if !ok || !identifier.MatchString(name) {
						return "", fmt.Errorf("a model value must be an identifier, got %v", inner)
					}
					return name, nil
				}
			}
		}
		names := make([]string, 0, len(x))
		for k := range x {
			if !identifier.MatchString(k) {
				return "", fmt.Errorf("record field %q isn't an identifier", k)
			}
			names = append(names, k)
		}
		sort.Strings(names)
		fields := make([]string, len(names))
		for i, k := range names {
			text, err := Encode(x[k])
			if err != nil {
				return "", err
			}
			fields[i] = k + " |-> " + text
		}
		return "[" + strings.Join(fields, ", ") + "]", nil
	default:
		return "", fmt.Errorf("can't encode %T", v)
	}
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func encodeAll(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("want an array, got %T", v)
	}
	out := make([]string, len(items))
	for i, item := range items {
		text, err := Encode(item)
		if err != nil {
			return nil, err
		}
		out[i] = text
	}
	return out, nil
}

func setText(v any) (string, error) {
	items, err := encodeAll(v)
	if err != nil {
		return "", err
	}
	sort.Strings(items)
	var unique []string
	for i, it := range items {
		if i == 0 || it != items[i-1] {
			unique = append(unique, it)
		}
	}
	return "{" + strings.Join(unique, ", ") + "}", nil
}

func fnText(v any) (string, error) {
	pairs, ok := v.([]any)
	if !ok {
		return "", fmt.Errorf("$fn wants an array of [key, value] pairs")
	}
	if len(pairs) == 0 {
		return "[x \\in {} |-> 0]", nil
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		kv, ok := p.([]any)
		if !ok || len(kv) != 2 {
			return "", fmt.Errorf("$fn wants [key, value] pairs")
		}
		k, err := Encode(kv[0])
		if err != nil {
			return "", err
		}
		val, err := Encode(kv[1])
		if err != nil {
			return "", err
		}
		parts[i] = k + " :> " + val
	}
	sort.Strings(parts)
	return "(" + strings.Join(parts, " @@ ") + ")", nil
}

var boundIdentifier = regexp.MustCompile(`"[^"]*"|[A-Za-z_][A-Za-z0-9_]*`)

// modelValues names the model values the bounds introduce, such as r1 in
// RM = {r1, r2, r3}.
func modelValues(bounds map[string]string) []string {
	seen := map[string]bool{}
	for _, v := range bounds {
		for _, tok := range boundIdentifier.FindAllString(v, -1) {
			if !strings.HasPrefix(tok, `"`) && tok != "TRUE" && tok != "FALSE" {
				seen[tok] = true
			}
		}
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
