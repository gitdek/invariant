// Package conformance tests code against its model. A driver runs the real
// code: it calls its operations at random and records each state in the
// spec's vocabulary. TLC then checks that every run starts in a state Init
// allows and that every step between two different states is a Next step.
// Legacy attempts allow any step that changes nothing. A declared attempt
// instead checks the exact named action, or its disabledness for a refusal.
// What the model forbids, TLC catches.
//
// It's testing, not proof: it covers the runs the driver made, no more.
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	// Tried is whether the driver tried every step (D-0082, D-0085). It's
	// nil for a driver that records only its runs, whose steps aren't checked.
	Tried *Tried `json:"tried,omitempty"`
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

// Runner model-checks module, a file in dir, against cfg. tlc.Runner is one,
// and a stand-in can take its place in tests.
type Runner interface {
	Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error)
}

// Check is the byte-input compatibility wrapper for CheckReader.
func Check(ctx context.Context, runner Runner, dir, module string, bounds map[string]string, vars []string, modelStates int64, raw []byte, model *Model) (Result, error) {
	return CheckReader(ctx, runner, dir, module, bounds, vars, modelStates, bytes.NewReader(raw), model)
}

// CheckReader tests a recording against the staged model. Variable-size
// recording contents live in invocation-private temporary storage; only
// fixed-size indexes and active checking windows remain in memory.
// The caller owns the reader and its files. Every exit joins started checks
// and removes this invocation's storage and copied checking modules.
func CheckReader(ctx context.Context, runner Runner, dir, module string, bounds map[string]string, vars []string, modelStates int64, input io.Reader, model *Model) (r Result, err error) {
	r.ModelStates = modelStates
	s, err := newRecordingStore(ctx)
	if err != nil {
		return r, err
	}
	root := ""
	defer func() {
		err = errors.Join(err, ctx.Err(), s.close())
		if root != "" {
			err = errors.Join(err, os.RemoveAll(root))
		}
		if err != nil {
			r = Result{ModelStates: modelStates}
		}
	}()
	rec, problem, err := decodeReader(ctx, s, input, vars)
	if err != nil {
		return r, err
	}
	if problem != "" {
		r.Message = problem
		return r, nil
	}
	problem, err = strictAttempts(ctx, rec, model)
	if err != nil {
		return r, err
	}
	if problem != "" {
		r.Message = problem
		return r, nil
	}
	r.Runs, r.Steps = rec.runs, rec.recorded
	r.States, r.Transitions = len(s.states), len(rec.steps)

	values := modelValues(bounds)
	constants := map[string]string{}
	for k, v := range bounds {
		constants[k] = v
	}
	for _, mv := range values {
		constants[mv] = mv
	}
	header := fmt.Sprintf("EXTENDS %s, TLC\n", module)
	if len(values) > 0 {
		header += "CONSTANTS " + strings.Join(values, ", ") + "\n"
	}
	// Keep batch paths inside the caller's staging directory, but never use
	// or overwrite a path that another invocation or the caller owns.
	root, err = batchRoot(ctx, dir)
	if err != nil {
		return r, err
	}

	_, res, err := checkRanges(ctx, runner, "init", len(rec.starts), initBatch, func(ctx context.Context, c tlc.ConformanceCheck) (tlc.Result, error) {
		return checkBatch(ctx, runner, dir, root, c, "Invariant_ConformanceInit",
			tlc.Config{Specification: "Invariant_ISpec", Constants: constants, Invariants: []string{"Init"}}, func(w *moduleWriter) error {
				w.text("---- MODULE Invariant_ConformanceInit ----\n" + header + "Invariant_Starts == {")
				start := c.Range.Start
				for i := start; i < start+c.Range.Count; i++ {
					if i > start {
						w.text(",\n  ")
					}
					w.value(s, s.states[rec.starts[i]].text)
					if w.err != nil {
						return w.err
					}
				}
				w.text("}\nInvariant_IInit == \\E s \\in Invariant_Starts : " + is("s", vars, false) + "\n" +
					"Invariant_ISpec == Invariant_IInit /\\ [][UNCHANGED vars]_vars\n====\n")
				return nil
			})
	})
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

	window, res, err := checkRanges(ctx, runner, "steps", len(rec.steps), stepBatch, func(ctx context.Context, c tlc.ConformanceCheck) (tlc.Result, error) {
		start := c.Range.Start
		local, pairs := renumber(rec.steps[start : start+c.Range.Count])
		return checkBatch(ctx, runner, dir, root, c, "Invariant_Conformance",
			tlc.Config{Specification: "Invariant_CSpec", Constants: constants}, func(w *moduleWriter) error {
				w.text("---- MODULE Invariant_Conformance ----\n" + header +
					"VARIABLES invariant_source, invariant_target, invariant_done\nInvariant_States == <<")
				for i, id := range local {
					if i > 0 {
						w.text(",\n  ")
					}
					w.value(s, s.states[id].text)
					if w.err != nil {
						return w.err
					}
				}
				w.text(">>\nInvariant_Steps == {" + strings.Join(pairs, ", ") + "}\n" +
					"Invariant_CInit == \\E p \\in Invariant_Steps :\n" +
					"    /\\ " + is("Invariant_States[p[1]]", vars, false) + "\n" +
					"    /\\ invariant_source = p[1]\n    /\\ invariant_target = p[2]\n    /\\ invariant_done = FALSE\n" +
					"Invariant_CNext ==\n" +
					"    \\/ /\\ ~invariant_done\n       /\\ Next\n" +
					"       /\\ " + is("Invariant_States[invariant_target]", vars, true) + "\n" +
					"       /\\ invariant_done' = TRUE\n       /\\ UNCHANGED <<invariant_source, invariant_target>>\n" +
					"    \\/ /\\ invariant_done\n       /\\ UNCHANGED <<vars, invariant_source, invariant_target, invariant_done>>\n" +
					"Invariant_CSpec == Invariant_CInit /\\ [][Invariant_CNext]_<<vars, invariant_source, invariant_target, invariant_done>>\n====\n")
				return nil
			})
	})
	if err != nil {
		return r, err
	}
	switch {
	case res.Outcome == tlc.Deadlock && len(res.Trace) > 0:
		start := window.Start
		local, _ := renumber(rec.steps[start : start+window.Count])
		stuck := res.Trace[len(res.Trace)-1]
		step := &Step{From: stateText(stuck, vars)}
		for _, v := range stuck.Vars {
			i, e := strconv.Atoi(v.Value)
			if e != nil || i < 1 || i > len(local) {
				continue
			}
			if v.Name == "invariant_source" || v.Name == "invariant_target" {
				pretty, e := s.pretty(local[i-1])
				if e != nil {
					return r, e
				}
				if v.Name == "invariant_source" {
					step.From = pretty
				} else {
					step.To = pretty
				}
			}
		}
		r.BadStep = step
		r.Message = "the code took a step the model doesn't allow"
		for _, id := range rec.strict {
			e := rec.attempts[id]
			from, err := s.pretty(e.from)
			if err != nil {
				return r, err
			}
			if from != step.From {
				continue
			}
			to, err := s.pretty(e.to)
			if err != nil {
				return r, err
			}
			if to != step.To {
				continue
			}
			a, err := s.attempt(e)
			if err != nil {
				return r, err
			}
			r.Message = fmt.Sprintf("declared %s attempt %s from %s to %s is not a model Next step", a.outcome, actionCall(a), step.From, step.To)
			break
		}
		return r, nil
	case res.Outcome != tlc.Passed:
		r.Message = "TLC couldn't check the steps: " + res.Message
		return r, nil
	}
	if len(rec.strict) > 0 {
		bad, message, err := checkStrictAttempts(ctx, runner, dir, root, header, constants, vars, rec)
		if err != nil {
			return r, err
		}
		if message != "" {
			r.BadStep, r.Message = bad, message
			return r, nil
		}
	}
	if rec.hasAttempts && model != nil {
		tried, err := checkTried(ctx, runner, dir, root, module, header, constants, vars, *model, rec)
		if err != nil {
			return r, err
		}
		r.Tried = &tried
	}
	r.Passed = true
	return r, nil
}

const initBatch = 1000

func checkBatch(ctx context.Context, runner Runner, source, root string, c tlc.ConformanceCheck, module string, cfg tlc.Config, emit func(*moduleWriter) error) (result tlc.Result, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	dir, err := batchDir(ctx, source, root, c.Batch)
	if err != nil {
		return result, err
	}
	defer func() {
		if cleanup := os.RemoveAll(dir); cleanup != nil {
			err = errors.Join(err, cleanup)
		}
	}()
	if err := writeModule(ctx, dir, module, emit); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if capable, ok := runner.(tlc.ConformanceRunner); ok {
		return capable.CheckConformance(ctx, dir, module, cfg, c)
	}
	return runner.Check(ctx, dir, module, cfg)
}

// Declared outcomes retain their original order, including multiple actions
// with the same state pair. The done bit prevents TLC from excusing an
// accepted read-only action as stuttering; a refusal requires disabledness.
func checkStrictAttempts(ctx context.Context, runner Runner, dir, root, header string, constants map[string]string, vars []string, rec recorded) (*Step, string, error) {
	s := rec.store
	window, res, err := checkRanges(ctx, runner, "strict", len(rec.strict), stepBatch, func(ctx context.Context, c tlc.ConformanceCheck) (tlc.Result, error) {
		return checkBatch(ctx, runner, dir, root, c, "Invariant_StrictAttempts",
			tlc.Config{Specification: "Invariant_ASpec", Constants: constants}, func(w *moduleWriter) error {
				w.text("---- MODULE Invariant_StrictAttempts ----\n" + header + "VARIABLES invariant_i, invariant_done\nInvariant_Attempts == <<")
				start := c.Range.Start
				actions := map[string]string{}
				for i := start; i < start+c.Range.Count; i++ {
					if err := ctx.Err(); err != nil {
						return err
					}
					a, err := s.attempt(rec.attempts[rec.strict[i]])
					if err != nil {
						return err
					}
					name, _ := json.Marshal(a.step)
					refused := "FALSE"
					if a.outcome == "refused" {
						refused = "TRUE"
					}
					if i > start {
						w.text(",\n  ")
					}
					w.text("<<")
					w.value(s, s.states[a.from].text)
					w.text(", ")
					w.value(s, s.states[a.to].text)
					w.text(", " + string(name) + ", <<" + strings.Join(a.args, ", ") + ">>, " + refused + ">>")
					if w.err != nil {
						return w.err
					}
					args := make([]string, len(a.args))
					for j := range args {
						args[j] = fmt.Sprintf("Invariant_Attempts[invariant_i][4][%d]", j+1)
					}
					actions[a.step] = "Invariant_Attempts[invariant_i][3] = " + string(name) + " -> " + actionCall(attempt{step: a.step, args: args})
				}
				cases := make([]string, 0, len(actions))
				for _, expression := range actions {
					cases = append(cases, expression)
				}
				sort.Strings(cases)
				w.text(">>\nInvariant_Action == CASE " + strings.Join(cases, "\n  [] ") + "\n  [] OTHER -> FALSE\n" +
					"Invariant_AInit == \\E i \\in DOMAIN Invariant_Attempts :\n" +
					"    /\\ invariant_i = i\n    /\\ invariant_done = FALSE\n" +
					"    /\\ " + is("Invariant_Attempts[i][1]", vars, false) + "\n" +
					"Invariant_ANext ==\n" +
					"    \\/ /\\ ~invariant_done\n" +
					"       /\\ IF Invariant_Attempts[invariant_i][5]\n" +
					"             THEN /\\ ~ENABLED (Invariant_Action /\\ UNCHANGED <<invariant_i, invariant_done>>)\n" +
					"                  /\\ UNCHANGED vars\n" +
					"             ELSE /\\ Invariant_Action\n" +
					"                  /\\ " + is("Invariant_Attempts[invariant_i][2]", vars, true) + "\n" +
					"       /\\ invariant_done' = TRUE\n       /\\ UNCHANGED invariant_i\n" +
					"    \\/ /\\ invariant_done\n       /\\ UNCHANGED <<vars, invariant_i, invariant_done>>\n" +
					"Invariant_ASpec == Invariant_AInit /\\ [][Invariant_ANext]_<<vars, invariant_i, invariant_done>>\n====\n")
				return nil
			})
	})
	if err == nil && res.Outcome == tlc.Passed {
		return nil, "", nil
	}
	if cause := ctx.Err(); cause != nil {
		return nil, "", cause
	}
	b := window.Start / stepBatch
	i, index := stateIndex(res.Trace), window.Start
	if i >= 1 && i <= window.Count && index+i-1 < len(rec.strict) {
		index += i - 1
	}
	a, readErr := s.attempt(rec.attempts[rec.strict[index]])
	if readErr != nil {
		return nil, "", errors.Join(err, readErr)
	}
	from, readErr := s.pretty(a.from)
	if readErr != nil {
		return nil, "", errors.Join(err, readErr)
	}
	to, readErr := s.pretty(a.to)
	if readErr != nil {
		return nil, "", errors.Join(err, readErr)
	}
	description := fmt.Sprintf("declared %s attempt %s from %s to %s", a.outcome, actionCall(a), from, to)
	if err != nil {
		return nil, "", fmt.Errorf("TLC couldn't check %s (batch %d): %w", description, b+1, err)
	}
	if res.Outcome == tlc.Deadlock && i >= 1 && i <= window.Count && window.Start+i-1 < len(rec.strict) {
		return &Step{From: from, To: to}, description + " disagrees with the named model action", nil
	}
	return nil, fmt.Sprintf("TLC couldn't check %s (batch %d): %s", description, b+1, res.Message), nil
}

// stepBatch bounds the steps and endpoints in one checking window.
// An individual endpoint or attempt has no fixed byte ceiling.
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

// atOnce checks batches 0 to n-1 with check, as many at once as
// runtime.GOMAXPROCS(0) says, up to maxAtOnce: they don't depend on each
// other, and most of each TLC run is starting its JVM. What it finds is what checking them in
// order would: the earliest batch that doesn't pass and what check found
// there, or n and a passing result. Once a batch fails, those after it don't
// start and those running are stopped through their context, but every batch
// before it runs to its end, and atOnce returns only once every batch it
// started has. Operational errors survive an earlier semantic failure; only
// cancellation caused by stopping a later batch is ignored.
func atOnce(ctx context.Context, n int, check func(ctx context.Context, b int) (tlc.Result, error)) (int, tlc.Result, error) {
	window, result, err := checkRanges(ctx, nil, "batch", n, 1, func(ctx context.Context, c tlc.ConformanceCheck) (tlc.Result, error) {
		return check(ctx, c.Range.Start)
	})
	if ctx.Err() != nil {
		result = tlc.Result{}
	}
	return window.Start, result, err
}

var errLaterBatch = errors.New("an earlier conformance batch failed")

// A canceled write can also fail while flushing or closing its file. Those
// joined storage errors must not be mistaken for scheduler cancellation.
func batchCancellationOnly(err error) bool {
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		for _, child := range children {
			if !batchCancellationOnly(child) {
				return false
			}
		}
		return len(children) > 0
	case interface{ Unwrap() error }:
		return batchCancellationOnly(e.Unwrap())
	default:
		return err == context.Canceled || err == errLaterBatch
	}
}

// maxAtOnce is the most batches checked at once, however many CPUs there
// are. Each is a TLC JVM whose heap may grow to a quarter of the machine's
// memory: on a Mac with 18 cores, 18 at once ran out of memory, and TLC was
// killed partway through a batch (#142). Four at once checked
// factory/protocol there in 258 seconds, against 371 one at a time.
const maxAtOnce = 4

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
	return renderState(s, vars, true)
}

func encodeRecord(s map[string]any, vars []string) (string, error) {
	record, _, err := renderState(s, vars, false)
	return record, err
}

func renderState(s map[string]any, vars []string, pretty bool) (record, readable string, err error) {
	if len(s) != len(vars) {
		return "", "", fmt.Errorf("want exactly the spec's variables %v, got %d fields", vars, len(s))
	}
	fields := make([]string, len(vars))
	var pairs []string
	if pretty {
		pairs = make([]string, len(vars))
	}
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
		if pretty {
			pairs[i] = v + " = " + text
		}
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
	case json.Number:
		if !regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`).MatchString(string(x)) {
			return "", fmt.Errorf("%s isn't an integer; TLA+ has no fractions", x)
		}
		return string(x), nil
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
