//go:build integration

package conformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// A queue of Capacity items, filled by at most MaxPuts puts. The check that
// the queue has room is a rule the code keeps; the count of puts is the
// environment's bound.
const queueModel = `---- MODULE Queue ----
EXTENDS Naturals, Sequences
CONSTANTS Items, Capacity, MaxPuts
VARIABLES q, puts
vars == <<q, puts>>
Init == q = <<>> /\ puts = 0
Put(i) == puts < MaxPuts /\ Len(q) < Capacity /\ q' = Append(q, i) /\ puts' = puts + 1
Take == q # <<>> /\ q' = Tail(q) /\ UNCHANGED puts
Next == (\E i \in Items : Put(i)) \/ Take
====
`

var queueBounds = map[string]string{"Items": "{i1, i2}", "Capacity": "1", "MaxPuts": "2"}

// Three states: empty and fresh, full, and empty with the puts used up.
var queueStates = []map[string]any{
	{"q": map[string]any{"$seq": []any{}}, "puts": 0},
	{"q": map[string]any{"$seq": []any{map[string]any{"$mv": "i1"}}}, "puts": 1},
	{"q": map[string]any{"$seq": []any{}}, "puts": 2},
}

// attemptsJSON writes a driver's output whose every attempt is a refusal,
// so only what it tried is at stake: tried[i] lists the steps tried in
// state i, as "Take" or "Put i1".
func attemptsJSON(t *testing.T, tried [][]string) []byte {
	t.Helper()
	var list [][]any
	for from, steps := range tried {
		for _, s := range steps {
			name, arg, _ := strings.Cut(s, " ")
			args := []any{}
			if arg != "" {
				args = append(args, map[string]any{"$mv": arg})
			}
			list = append(list, []any{from, name, args, from})
		}
	}
	b, err := json.Marshal(map[string]any{"states": queueStates, "init": []int{0}, "attempts": list})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func checkQueue(t *testing.T, raw []byte, larger map[string]string) Result {
	t.Helper()
	tc, err := toolchain.Ensure(context.Background())
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Queue.tla"), []byte(queueModel), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, err := tla.Steps(queueModel)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	r, err := Check(context.Background(), runner, dir, "Queue", queueBounds, []string{"q", "puts"}, 5, raw, &Model{Steps: steps, Larger: larger})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// MaxPuts is the environment's; Capacity is a size the code takes.
var environment = map[string]string{"MaxPuts": "3"}

func TestADriverThatTriesEveryStepPasses(t *testing.T) {
	// In the last state only MaxPuts rules Put out, so leaving it untried is fine.
	r := checkQueue(t, attemptsJSON(t, [][]string{{"Put i1", "Put i2", "Take"}, {"Put i1", "Put i2", "Take"}, {"Take"}}), environment)
	if !r.Passed || r.Tried == nil || !r.Tried.Passed || r.Tried.Attempts != 7 || r.Tried.States != 3 {
		t.Fatalf("result %+v, tried %+v", r, r.Tried)
	}
}

// The way copythis-ad#33's driver skipped: the model rules Take out in an
// empty queue at every size, so the driver must try it and see it refused.
func TestSkippingAStepTheModelRulesOutFails(t *testing.T) {
	r := checkQueue(t, attemptsJSON(t, [][]string{{"Put i1", "Put i2"}, {"Put i1", "Put i2", "Take"}, {"Take"}}), environment)
	if r.Tried == nil || r.Tried.Passed || r.Tried.Untried != `{<<"Take">>}` || !strings.Contains(r.Tried.In, "puts = 0") {
		t.Fatalf("tried %+v", r.Tried)
	}
}

func TestSkippingAStepTheModelAllowsFails(t *testing.T) {
	r := checkQueue(t, attemptsJSON(t, [][]string{{"Put i2", "Take"}, {"Put i1", "Put i2", "Take"}, {"Take"}}), environment)
	if r.Tried == nil || r.Tried.Passed || r.Tried.Untried != `{<<"Put", i1>>}` {
		t.Fatalf("tried %+v", r.Tried)
	}
}

// Refusing a put into a full queue is a rule the code keeps, so a driver that
// stops at Capacity fails, unless Capacity were the environment's bound.
func TestStoppingAtACapacityTheCodeEnforcesFails(t *testing.T) {
	stops := attemptsJSON(t, [][]string{{"Put i1", "Put i2", "Take"}, {"Take"}, {"Take"}})
	r := checkQueue(t, stops, environment)
	if r.Tried == nil || r.Tried.Passed || r.Tried.Untried != `{<<"Put", i1>>, <<"Put", i2>>}` {
		t.Fatalf("tried %+v", r.Tried)
	}
	r = checkQueue(t, stops, map[string]string{"MaxPuts": "3", "Capacity": "2"})
	if r.Tried == nil || !r.Tried.Passed {
		t.Fatalf("with Capacity as the environment's bound, the stop is a bound's: %+v", r.Tried)
	}
}

func TestAStepTheModelDoesntNameFails(t *testing.T) {
	r := checkQueue(t, attemptsJSON(t, [][]string{{"Put i1", "Put i2", "Take", "Get"}, {}, {}}), environment)
	if r.Tried == nil || r.Tried.Passed || !strings.Contains(r.Tried.Message, "Get, a step the model's Next doesn't name") {
		t.Fatalf("tried %+v", r.Tried)
	}
}

// A driver that records only runs isn't checked for its steps.
func TestADriverThatRecordsRunsIsNotChecked(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"traces": [][]map[string]any{{queueStates[0]}}})
	r := checkQueue(t, raw, environment)
	if !r.Passed || r.Tried != nil {
		t.Fatalf("result %+v, tried %+v", r, r.Tried)
	}
}
