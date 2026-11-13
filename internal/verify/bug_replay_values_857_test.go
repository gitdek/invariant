package verify

import (
	"encoding/json"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// An execution adapter writes a replay's records in JSON of its own, and
// the gate compares them with the path TLC checked (#857). The comparison
// is of values: a set is the same set in any order, an empty argument list
// is the same written as [] or as null, and an integer is the same number
// however it's padded. Anything else is a different value, a sequence in
// another order among them, and a malformed value equals nothing, itself
// included.
func TestReplayRecordsAreComparedByValue857(t *testing.T) {
	atom := func(kind, text string) tlc.ReplayValue { return tlc.ReplayValue{Kind: kind, Atom: text} }
	of := func(kind string, elements ...tlc.ReplayValue) tlc.ReplayValue {
		return tlc.ReplayValue{Kind: kind, Elements: elements}
	}
	a, b, c := atom("model", "a"), atom("model", "b"), atom("integer", "3")
	for _, tc := range []struct {
		name string
		x, y tlc.ReplayValue
		same bool
	}{
		{"a set in another order", of("set", a, b, c), of("set", c, b, a), true},
		{"a set that repeats an element", of("set", a, b), of("set", b, a, b), true},
		{"an empty set, with and without its list", of("set"), tlc.ReplayValue{Kind: "set", Elements: []tlc.ReplayValue{}}, true},
		{"a set inside a sequence", of("sequence", of("set", a, b), c), of("sequence", of("set", b, a), c), true},
		{"a function's entries in another order",
			tlc.ReplayValue{Kind: "function", Entries: []tlc.ReplayEntry{{Key: a, Value: c}, {Key: b, Value: c}}},
			tlc.ReplayValue{Kind: "function", Entries: []tlc.ReplayEntry{{Key: b, Value: c}, {Key: a, Value: c}}}, true},
		{"an integer with a leading zero", atom("integer", "3"), atom("integer", "03"), true},
		{"a sequence in another order", of("sequence", a, b), of("sequence", b, a), false},
		{"a set and a sequence of the same elements", of("set", a, b), of("sequence", a, b), false},
		{"a set with one more element", of("set", a, b), of("set", a, b, c), false},
		{"a string and a model value of the same text", atom("string", "a"), a, false},
		{"two malformed values", atom("integer", "three"), atom("integer", "three"), false},
		{"a value and a malformed one", a, tlc.ReplayValue{Kind: "model", Atom: "a", Elements: []tlc.ReplayValue{b}}, false},
	} {
		if got := sameReplayValue(tc.x, tc.y); got != tc.same {
			t.Errorf("%s: the same value: %v; want %v", tc.name, got, tc.same)
		}
		if got := sameReplayValue(tc.y, tc.x); got != tc.same {
			t.Errorf("%s, the other way round: the same value: %v; want %v", tc.name, got, tc.same)
		}
		state := func(v tlc.ReplayValue) tlc.ReplayState { return tlc.ReplayState{"held": v, "n": c} }
		if got := sameReplayState(state(tc.x), state(tc.y)); got != tc.same {
			t.Errorf("%s, in a state: the same state: %v; want %v", tc.name, got, tc.same)
		}
	}
	if sameReplayState(tlc.ReplayState{"n": c}, tlc.ReplayState{"n": c, "held": a}) || sameReplayState(tlc.ReplayState{"n": c}, tlc.ReplayState{"m": c}) {
		t.Error("states with other variables are the same state")
	}

	// A call as the path has it, and as an adapter's JSON gives it back.
	path := BugReplayCall{Index: 0, Action: "Reset", Before: tlc.ReplayState{"held": of("set", a, b)}, After: tlc.ReplayState{"held": of("set")}, Dispatched: true, Outcome: "returned"}
	var wire BugReplayCall
	if err := json.Unmarshal([]byte(`{"index":0,"action":"Reset","args":[],"before":{"held":{"kind":"set","elements":[{"kind":"model","atom":"b"},{"kind":"model","atom":"a"}]}},"after":{"held":{"kind":"set","elements":[]}},"dispatched":true,"outcome":"returned"}`), &wire); err != nil {
		t.Fatal(err)
	}
	if path.Args != nil || wire.Args == nil {
		t.Fatal("the fixture doesn't spell an empty argument list two ways")
	}
	if !sameReplayCalls([]BugReplayCall{path}, []BugReplayCall{wire}) || !sameReplayValues(path.Args, wire.Args) {
		t.Error("a call with no arguments and a set in another order isn't the call the path names")
	}
	for name, change := range map[string]func(*BugReplayCall){
		"another action":      func(c *BugReplayCall) { c.Action = "Clear" },
		"another index":       func(c *BugReplayCall) { c.Index = 1 },
		"an argument":         func(c *BugReplayCall) { c.Args = []tlc.ReplayValue{a} },
		"another state after": func(c *BugReplayCall) { c.After = tlc.ReplayState{"held": of("set", a)} },
		"no dispatch":         func(c *BugReplayCall) { c.Dispatched = false },
		"another outcome":     func(c *BugReplayCall) { c.Outcome = "refused" },
		"an error":            func(c *BugReplayCall) { c.Error = "boom" },
	} {
		other := wire
		change(&other)
		if sameReplayCalls([]BugReplayCall{path}, []BugReplayCall{other}) {
			t.Errorf("a call with %s is taken for the call the path names", name)
		}
	}
	if sameReplayCalls([]BugReplayCall{path}, nil) || sameReplayCalls([]BugReplayCall{path}, []BugReplayCall{wire, wire}) {
		t.Error("a list with other calls is taken for the same list")
	}
}
