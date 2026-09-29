package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// These tests check that conformance checks its batches at once (#132). The
// sandbox has no Docker, so a stand-in takes TLC's place.

// acceptCounter is the model the tests check against: a counter that goes up
// by one. Its bound is in a module of its own, so a batch's directory needs
// every module in dir, not only the one its module extends.
const acceptCounter = `---- MODULE Counter ----
EXTENDS Naturals, Limit
VARIABLE n
vars == <<n>>
Init == n \in 0..Max
Inc == n < Max /\ n' = n + 1
Next == Inc
====
`

// acceptLimit is the module that holds the counter's bound.
const acceptLimit = `---- MODULE Limit ----
Max == 100000
====
`

// acceptModules is the model's modules, by name.
var acceptModules = map[string]string{"Counter": acceptCounter, "Limit": acceptLimit}

// acceptAtOnce is how many CPUs the tests have Go use, and so the most
// batches Check may check at once.
const acceptAtOnce = 3

// acceptHold is the least time the stand-in keeps a batch it holds open.
const acceptHold = 50 * time.Millisecond

// acceptTLC stands in for TLC. As TLC does, it reads the module it's asked to
// check from the directory it's given, and it fails when the model's modules
// aren't files there beside it, since TLC's container sees only that
// directory. It checks Counter as TLC would: a run may start anywhere, a step
// is a Next step when it adds one, and a state's steps were all tried when Inc
// was tried in it.
//
// It holds batches open, as a slow TLC run would. A batch of kind hold, steps
// or tried, runs until two of its kind have run at once, and for acceptHold
// at least, so that more at once than Check may run would show. The batch
// that fails at n = early runs until the one that fails at n = late has
// finished, and for acceptHold after that. No batch runs past the deadline.
type acceptTLC struct {
	hold        string
	early, late int
	deadline    time.Time

	mu        sync.Mutex
	now, most map[string]int
	runs      []acceptRun
	failed    []int
	lateAt    time.Time
}

// acceptRun is one batch the stand-in checked: its kind, the directory it ran
// in, and what it checked, each step as "from->to" or each state as its n.
type acceptRun struct {
	kind  string
	dir   string
	items []string
}

// newAcceptTLC is a stand-in that holds batches of kind hold open, and the
// batch that fails at n = early until the one that fails at n = late has
// finished, for ten seconds at most. An n of -1 is none.
func newAcceptTLC(hold string, early, late int) *acceptTLC {
	return &acceptTLC{hold: hold, early: early, late: late, deadline: time.Now().Add(10 * time.Second), now: map[string]int{}, most: map[string]int{}}
}

// Check checks module, in dir, against cfg.
func (f *acceptTLC) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	text, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		return tlc.Result{Outcome: tlc.Failed, Message: "Cannot find source file for module " + module + " in " + dir}, nil
	}
	for name, want := range acceptModules {
		path := filepath.Join(dir, name+".tla")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return tlc.Result{Outcome: tlc.Failed, Message: "Cannot find source file for module " + name + " in " + dir}, nil
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			return tlc.Result{Outcome: tlc.Failed, Message: "Module " + name + " in " + dir + " isn't the model's"}, nil
		}
	}
	switch cfg.Specification {
	case "Invariant_CSpec":
		return f.batch(ctx, "steps", dir, string(text))
	case "Invariant_TSpec":
		return f.batch(ctx, "tried", dir, string(text))
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}

// batch checks one batch of kind, steps or tried, and holds it open as the
// stand-in was asked to.
func (f *acceptTLC) batch(ctx context.Context, kind, dir, text string) (tlc.Result, error) {
	start := time.Now()
	f.mu.Lock()
	f.now[kind]++
	f.most[kind] = max(f.most[kind], f.now[kind])
	f.mu.Unlock()
	run := acceptRun{kind: kind, dir: dir}
	res, bad := acceptVerdict(kind, text, &run)
	for f.held(kind, bad, start) {
		if err := ctx.Err(); err != nil {
			f.finish(run, -1)
			return tlc.Result{}, err
		}
		time.Sleep(time.Millisecond)
	}
	f.finish(run, bad)
	return res, nil
}

// held is whether a batch of kind that started at start, and fails at n =
// bad, is still running.
func (f *acceptTLC) held(kind string, bad int, start time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case time.Now().After(f.deadline):
		return false
	case kind == f.hold:
		return f.most[kind] < 2 || time.Since(start) < acceptHold
	case bad >= 0 && bad == f.early:
		return f.lateAt.IsZero() || time.Since(f.lateAt) < acceptHold
	}
	return false
}

// finish records a batch that has finished, failing at n = bad, or at none
// when bad is -1.
func (f *acceptTLC) finish(run acceptRun, bad int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now[run.kind]--
	f.runs = append(f.runs, run)
	if bad < 0 {
		return
	}
	f.failed = append(f.failed, bad)
	if bad == f.late && f.lateAt.IsZero() {
		f.lateAt = time.Now()
	}
}

// finishedBefore is whether the batch that fails at n = a finished before the
// one that fails at n = b.
func (f *acceptTLC) finishedBefore(a, b int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.failed {
		switch n {
		case a:
			return true
		case b:
			return false
		}
	}
	return false
}

// acceptVerdict is what TLC finds in a batch of kind, from its module's text,
// and the n it fails at, or -1. It adds what the batch checks to run.
func acceptVerdict(kind, text string, run *acceptRun) (tlc.Result, int) {
	unread := tlc.Result{Outcome: tlc.Failed, Message: "the stand-in can't read the " + kind + " batch's module"}
	states := acceptValues(acceptTuple(text, "Invariant_States"))
	if len(states) == 0 {
		return unread, -1
	}
	res, bad := tlc.Result{Outcome: tlc.Passed}, -1
	if kind == "tried" {
		sets := acceptSet.FindAllString(acceptTuple(text, "Invariant_Tried"), -1)
		if len(sets) != len(states) {
			return unread, -1
		}
		for i, set := range sets {
			run.items = append(run.items, strconv.Itoa(states[i]))
			if bad < 0 && !strings.Contains(set, `<<"Inc">>`) {
				bad = states[i]
				res = tlc.Result{Outcome: tlc.Violated, Invariant: "Invariant_EveryStepTried", Trace: acceptState("invariant_i", strconv.Itoa(i+1), "n", strconv.Itoa(bad))}
				res.Printed = []string{`<<"invariant-untried", {<<"Inc">>}>>`}
			}
		}
		return res, bad
	}
	_, steps, _ := strings.Cut(text, "Invariant_Steps == {")
	steps, _, _ = strings.Cut(steps, "}")
	for _, p := range acceptPair.FindAllStringSubmatch(steps, -1) {
		i, _ := strconv.Atoi(p[1])
		j, _ := strconv.Atoi(p[2])
		if i < 1 || j < 1 || i > len(states) || j > len(states) {
			return unread, -1
		}
		from, to := states[i-1], states[j-1]
		run.items = append(run.items, fmt.Sprintf("%d->%d", from, to))
		if bad < 0 && to != from+1 {
			bad = from
			res = tlc.Result{Outcome: tlc.Deadlock, Trace: acceptState("invariant_done", "FALSE", "invariant_source", p[1], "invariant_target", p[2], "n", strconv.Itoa(from))}
		}
	}
	if len(run.items) == 0 {
		return unread, -1
	}
	return res, bad
}

// acceptN reads n from a state's record.
var acceptN = regexp.MustCompile(`\[n \|-> (-?\d+)\]`)

// acceptPair reads a step: a pair of indexes into its batch's states.
var acceptPair = regexp.MustCompile(`<<(\d+), (\d+)>>`)

// acceptSet reads the set of steps tried in one state.
var acceptSet = regexp.MustCompile(`\{[^{}]*\}`)

// acceptTuple is what's between the brackets of name == <<...>> in a
// module's text.
func acceptTuple(text, name string) string {
	_, rest, ok := strings.Cut(text, name+" == <<")
	if !ok {
		return ""
	}
	depth := 1
	for i := 0; i+1 < len(rest); i++ {
		switch rest[i : i+2] {
		case "<<":
			depth++
			i++
		case ">>":
			depth--
			if depth == 0 {
				return rest[:i]
			}
			i++
		}
	}
	return ""
}

// acceptValues is n in each record of a tuple's text, in order.
func acceptValues(tuple string) []int {
	var out []int
	for _, m := range acceptN.FindAllStringSubmatch(tuple, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// acceptState is a trace of the one state TLC reports, from name and value
// pairs.
func acceptState(pairs ...string) []tlc.State {
	s := tlc.State{Index: 1, Action: "Initial predicate"}
	for i := 0; i+1 < len(pairs); i += 2 {
		s.Vars = append(s.Vars, tlc.Var{Name: pairs[i], Value: pairs[i+1]})
	}
	return []tlc.State{s}
}

// acceptDir is a directory staged as verify stages the one it gives Check:
// the model's modules, and nothing else.
func acceptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range acceptModules {
		if err := os.WriteFile(filepath.Join(dir, name+".tla"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// acceptCPUs has Go use acceptAtOnce CPUs until the test ends, so how many
// batches Check runs at once doesn't depend on the machine.
func acceptCPUs(t *testing.T) {
	prev := runtime.GOMAXPROCS(acceptAtOnce)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })
}

// acceptCheck has Check test what a driver recorded against Counter, with the
// stand-in in TLC's place.
func acceptCheck(t *testing.T, f *acceptTLC, dir string, raw []byte, model *Model) Result {
	t.Helper()
	r, err := Check(context.Background(), f, dir, "Counter", map[string]string{}, []string{"n"}, 0, raw, model)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// acceptUpTo is a run that counts from 0 up to n-1.
func acceptUpTo(n int) []int {
	run := make([]int, n)
	for i := range run {
		run[i] = i
	}
	return run
}

// acceptRuns is what a driver that records runs writes, each run being the
// values of n it went through.
func acceptRuns(t *testing.T, runs ...[]int) []byte {
	t.Helper()
	traces := [][]map[string]any{}
	for _, run := range runs {
		trace := []map[string]any{}
		for _, n := range run {
			trace = append(trace, map[string]any{"n": n})
		}
		traces = append(traces, trace)
	}
	b, err := json.Marshal(map[string]any{"traces": traces})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// acceptAttempts is what a driver that records attempts writes. It reached
// every n from 0 to states-1, starting at 0, and it tried Inc in every state
// but those in untried: to the next state, or as a refusal in the last.
func acceptAttempts(t *testing.T, states int, untried ...int) []byte {
	t.Helper()
	skip := map[int]bool{}
	for _, n := range untried {
		skip[n] = true
	}
	list := []map[string]any{}
	tries := [][]any{}
	for n := 0; n < states; n++ {
		list = append(list, map[string]any{"n": n})
		switch {
		case skip[n]:
		case n == states-1:
			tries = append(tries, []any{n, "Inc", []any{}, n})
		default:
			tries = append(tries, []any{n, "Inc", []any{}, n + 1})
		}
	}
	b, err := json.Marshal(map[string]any{"states": list, "init": []int{0}, "attempts": tries})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// acceptSpread checks how the batches of kind ran: n of them, more than one
// at once and never more than acceptAtOnce, each in a directory of its own
// inside dir.
func acceptSpread(t *testing.T, f *acceptTLC, kind, dir string, n int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if most := f.most[kind]; most < 2 || most > acceptAtOnce {
		t.Errorf("at most %d %s batches ran at once; want more than one, and no more than the %d CPUs Go may use", most, kind, acceptAtOnce)
	}
	ran := 0
	seen := map[string]bool{}
	for _, run := range f.runs {
		if run.kind != kind {
			continue
		}
		ran++
		rel, err := filepath.Rel(dir, run.dir)
		rel = filepath.ToSlash(rel)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			t.Errorf("a %s batch ran in %s; want a directory of its own inside %s", kind, run.dir, dir)
		}
		if seen[run.dir] {
			t.Errorf("two %s batches ran in %s; want each in a directory of its own", kind, run.dir)
		}
		seen[run.dir] = true
	}
	if ran != n {
		t.Errorf("%d %s batches ran; want %d", ran, kind, n)
	}
}

// acceptEachOnce checks that the batches of kind checked each of want once,
// and nothing else.
func acceptEachOnce(t *testing.T, f *acceptTLC, kind string, want []string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	count := map[string]int{}
	for _, run := range f.runs {
		if run.kind == kind {
			for _, item := range run.items {
				count[item]++
			}
		}
	}
	wrong := 0
	for _, w := range want {
		if count[w] != 1 {
			wrong++
			if wrong <= 3 {
				t.Errorf("%s was checked %d times; want once", w, count[w])
			}
		}
	}
	if wrong > 0 || len(count) != len(want) {
		t.Errorf("the %s batches checked %d things, %d of them not once; want each of %d once", kind, len(count), wrong, len(want))
	}
}

// Check checks more than one batch of steps at once, and never more than one
// per CPU Go may use, each in a directory of its own inside dir that holds the
// model's modules. It checks every step once, and a run whose every step is a
// Next step passes, as it does now.
func TestStepBatchesRunAtOnceEachInADirectoryOfItsOwn(t *testing.T) {
	acceptCPUs(t)
	dir := acceptDir(t)
	f := newAcceptTLC("steps", -1, -1)
	steps := 4*stepBatch + stepBatch/2
	r := acceptCheck(t, f, dir, acceptRuns(t, acceptUpTo(steps+1)), nil)
	if !r.Passed || r.BadStep != nil || r.Message != "" {
		t.Fatalf("result %+v; want every step a Next step", r)
	}
	if r.Runs != 1 || r.Steps != steps || r.States != steps+1 || r.Transitions != steps {
		t.Errorf("result %+v; want one run of %d steps through %d states", r, steps, steps+1)
	}
	acceptSpread(t, f, "steps", dir, 5)
	want := make([]string, steps)
	for n := range want {
		want[n] = fmt.Sprintf("%d->%d", n, n+1)
	}
	acceptEachOnce(t, f, "steps", want)
}

// checkTried does the same with its batches of states: more than one at once,
// never more than one per CPU Go may use, each in a directory of its own
// inside dir that holds the model's modules, and every state checked once.
func TestStateBatchesRunAtOnceEachInADirectoryOfItsOwn(t *testing.T) {
	acceptCPUs(t)
	dir := acceptDir(t)
	f := newAcceptTLC("tried", -1, -1)
	states := 4*triedBatch + triedBatch/2
	r := acceptCheck(t, f, dir, acceptAttempts(t, states), &Model{Steps: []tla.Step{{Name: "Inc"}}})
	if !r.Passed || r.Tried == nil || !r.Tried.Passed || r.Tried.Message != "" {
		t.Fatalf("result %+v, tried %+v; want every step a Next step, and every step tried", r, r.Tried)
	}
	if r.Tried.Attempts != states || r.Tried.States != states {
		t.Errorf("tried %+v; want %d attempts in %d states", r.Tried, states, states)
	}
	acceptSpread(t, f, "tried", dir, 5)
	want := make([]string, states)
	for n := range want {
		want[n] = strconv.Itoa(n)
	}
	acceptEachOnce(t, f, "tried", want)
}

// With a step the model doesn't allow in the first batch and another in the
// third, Check reports the first batch's step, even when the third finishes
// first. The first batch runs on after the third fails, so stopping it then
// would show too.
func TestTheEarliestBadStepIsReportedWhenALaterBatchFailsFirst(t *testing.T) {
	acceptCPUs(t)
	dir := acceptDir(t)
	early, late := 10, 2*stepBatch+500
	f := newAcceptTLC("", early, late)
	// One run counts up through three batches of steps. Two more each take a
	// step back: one sorts into the first batch, and one into the third.
	raw := acceptRuns(t, acceptUpTo(3*stepBatch), []int{early, early - 5}, []int{late, late - 10})
	r := acceptCheck(t, f, dir, raw, nil)
	want := Step{From: fmt.Sprintf("n = %d", early), To: fmt.Sprintf("n = %d", early-5)}
	if r.Passed || r.BadStep == nil || *r.BadStep != want {
		t.Fatalf("result %+v, bad step %+v; want the first batch's step, %+v", r, r.BadStep, want)
	}
	if !f.finishedBefore(late, early) {
		t.Errorf("the batch with the step from n = %d never finished before the one with the step from n = %d: the batches weren't checked at once", late, early)
	}
}

// With Inc untried in a state of the first batch of states and in one of the
// third, checkTried reports the first batch's state, even when the third
// finishes first. The first batch runs on after the third fails, so stopping
// it then would show too.
func TestTheEarliestUntriedStepIsReportedWhenALaterBatchFailsFirst(t *testing.T) {
	acceptCPUs(t)
	dir := acceptDir(t)
	early, late := 10, 2*triedBatch+500
	f := newAcceptTLC("", early, late)
	r := acceptCheck(t, f, dir, acceptAttempts(t, 3*triedBatch, early, late), &Model{Steps: []tla.Step{{Name: "Inc"}}})
	if !r.Passed || r.Tried == nil {
		t.Fatalf("result %+v; want every step a Next step, and the steps tried checked", r)
	}
	if in := fmt.Sprintf("n = %d", early); r.Tried.Passed || r.Tried.In != in || r.Tried.Untried != `{<<"Inc">>}` {
		t.Fatalf("tried %+v; want Inc untried in the first batch's state, %s", r.Tried, in)
	}
	if !f.finishedBefore(late, early) {
		t.Errorf("the batch with n = %d untried never finished before the one with n = %d: the batches weren't checked at once", late, early)
	}
}
