//go:build integration

package verify

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The issue's own case (#928). Add(n) takes any amount that fits, and the
// application refuses one that doesn't, with a declared refusal. At the
// ratified bounds the driver passes 1 and 2. One size larger it also passes
// True, which Python takes for 1, so the run reaches the model's four states
// in its two levels, and which the model's <= can't take at all. The pinned
// TLC ends the declared-outcome check with its code for that error and its
// general status.
const larger928Model = `---- MODULE Counter ----
EXTENDS Naturals, Local
CONSTANT Limit
VARIABLE count
vars == <<count>>
Init == count = 0
Add(n) == n <= Limit - count /\ count' = count + n
Done == count = Limit /\ UNCHANGED count
Next == (\E n \in 1..Limit : Add(n)) \/ Done
Spec == Init /\ [][Next]_vars
Safe == count <= Limit /\ Helper
Full == count = Limit
Overflow == count' = Limit + 1
====
`

const larger928Source = `counts = {'value': 0}


class Refused(Exception):
    def __init__(self, code):
        self.code = code


def increment(value):
    counts['value'] = value


def add(n, limit):
    if n > limit - counts['value']:
        raise Refused(409)
    increment(counts['value'] + n)


def done(limit):
    if counts['value'] != limit:
        raise Refused(409)
    return counts['value']
`

const larger928Driver = `from invariant_observe import Refusal, Step, explore, module
from counter import core as app

Limit = 2


def start():
    app.counts['value'] = 0
    return module(app.increment, fields={'count': 'counts.value'})


def add(life, state, n):
    life.step(app.add, n, Limit)


def done(life, state):
    life.step(app.done, Limit)


AMOUNTS = [[n] for n in range(1, Limit + 1)] + ([[True]] if Limit > 2 else [])


def drive():
    explore(start, [Step('Add', add, AMOUNTS, refusal=Refusal(app.Refused, code=409)),
                    Step('Done', done, refusal=Refusal(app.Refused, code=409))])


if __name__ == '__main__':
    drive()
`

func TestPythonApplicationLargerStandardOperatorTLC928(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := refusal392Fixture(t, true, larger928Source, larger928Driver, larger928Model)
	r, err := Run(ctx, f.p.Dir, "", tc)
	if err != nil || r == nil {
		t.Fatalf("gate: %v", err)
	}
	_, base := pa342Explored(t, r)
	if !base.passed() || len(r.Application.Larger) != 1 || r.Larger == nil {
		t.Fatalf("missing passing base or larger result: %s", Feedback(r))
	}
	l := r.Application.Larger[0]
	if l.Result.States != 4 || l.Result.Depth != 2 || l.Result.WantStates != 4 || l.Result.WantDepth != 2 {
		t.Fatalf("the fixture must match TLC's count and depth one size larger: %+v", l.Result)
	}
	if r.Passed || !l.Result.Required || l.Result.Passed || !r.Larger.Required || r.Larger.Passed {
		t.Fatalf("an evaluation error inside a standard operator claimed nothing instead of failing: passed=%v %+v", r.Passed, l.Result)
	}
	c := l.Conformance
	if c == nil || c.OperationalError || c.passed() {
		t.Fatalf("larger conformance: %+v", c)
	}
	found := false
	for _, check := range c.Checks {
		found = found || check.Module == "Invariant_StrictAttempts" && check.Result.Outcome == tlc.Failed && check.Error == "" &&
			check.Result.Version != "" && check.Result.ExitCode == 255 && slices.Contains(check.Result.Codes, 2266)
	}
	if !found {
		t.Fatalf("pinned TLC didn't end the declared-outcome check with its code 2266 and status 255: %+v", c.Checks)
	}
	want := "invocation " + l.Key.ID + ": conformance one size larger: TLC can't check what the run recorded: The first argument of <= should be an integer, but instead it is: TRUE"
	for _, message := range []string{l.Result.Message, r.Larger.Message} {
		if !strings.Contains(message, want) || strings.ContainsAny(message, "\r\n") {
			t.Fatalf("want the invocation and TLC's reason on one line, got %q", message)
		}
	}
}
