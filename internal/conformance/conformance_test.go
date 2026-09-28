package conformance

import (
	"encoding/json"
	"github.com/gitdek/invariant/internal/tla"
	"reflect"
	"strings"
	"testing"
)

func TestEncode(t *testing.T) {
	cases := map[string]string{
		`"working"`: `"working"`,
		`3`:         `3`,
		`true`:      `TRUE`,
		`{"$set": [{"$mv": "r2"}, {"$mv": "r1"}, {"$mv": "r1"}]}`: `{r1, r2}`,
		`{"$seq": [1, 2]}`: `<<1, 2>>`,
		`{"$fn": [[{"$mv": "r2"}, "aborted"], [{"$mv": "r1"}, "working"]]}`: `(r1 :> "working" @@ r2 :> "aborted")`,
		`{"type": "Prepared", "rm": {"$mv": "r1"}}`:                         `[rm |-> r1, type |-> "Prepared"]`,
		`{"$set": []}`: `{}`,
	}
	for in, want := range cases {
		var v any
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatal(err)
		}
		got, err := Encode(v)
		if err != nil || got != want {
			t.Errorf("Encode(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`[1, 2]`, `1.5`, `{"$mv": "not an id"}`, `{"bad field": 1}`} {
		var v any
		json.Unmarshal([]byte(bad), &v)
		if _, err := Encode(v); err == nil {
			t.Errorf("Encode(%s) succeeded; want an error", bad)
		}
	}
}

func TestEncodeIsCanonical(t *testing.T) {
	a, _ := Encode(map[string]any{"$set": []any{"b", "a"}})
	b, _ := Encode(map[string]any{"$set": []any{"a", "b"}})
	if a != b {
		t.Errorf("equal sets encode differently: %s vs %s", a, b)
	}
}

func TestModelValues(t *testing.T) {
	got := modelValues(map[string]string{"RM": "{r1, r2, r3}", "N": "3", "Names": `{"a", "b"}`})
	if want := []string{"r1", "r2", "r3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("modelValues = %v; want %v", got, want)
	}
}

func TestEncodeState(t *testing.T) {
	vars := []string{"x", "y"}
	got, readable, err := encodeState(map[string]any{"y": "b", "x": float64(1)}, vars)
	if err != nil || got != `[x |-> 1, y |-> "b"]` || readable != `x = 1, y = "b"` {
		t.Errorf("encodeState = %q, %q, %v", got, readable, err)
	}
	if _, _, err := encodeState(map[string]any{"x": float64(1)}, vars); err == nil || !strings.Contains(err.Error(), "variables") {
		t.Errorf("a state missing a variable should fail: %v", err)
	}
}

func TestBatches(t *testing.T) {
	steps := [][2]int{{0, 5}, {5, 9}, {9, 0}, {2, 3}, {3, 2}}
	got := batches(steps, 2)
	if len(got) != 3 || len(got[0]) != 2 || len(got[2]) != 1 {
		t.Fatalf("batches = %v", got)
	}
	local, pairs := renumber(got[0])
	// State 5 appears in both steps and gets one local index.
	if !reflect.DeepEqual(local, []int{0, 5, 9}) || !reflect.DeepEqual(pairs, []string{"<<1, 2>>", "<<2, 3>>"}) {
		t.Errorf("local = %v, pairs = %v", local, pairs)
	}
	if len(batches(nil, 2)) != 0 {
		t.Error("no steps, no batches")
	}
}

func TestDecodeReadsAttempts(t *testing.T) {
	raw := `{"states": [{"n": 0}, {"n": 1}, {"n": 0}], "init": [0],
		"attempts": [[0, "Up", [], 1], [1, "Up", [], 1], [1, "Down", [{"$mv": "a1"}], 2]]}`
	rec, problem := decode([]byte(raw), []string{"n"})
	if problem != "" {
		t.Fatal(problem)
	}
	// States 0 and 2 are the same state, so the step back lands on 0.
	if len(rec.states) != 2 || !rec.starts[0] || rec.recorded != 3 || len(rec.attempts) != 3 {
		t.Fatalf("recorded %+v", rec)
	}
	if !rec.steps[[2]int{0, 1}] || !rec.steps[[2]int{1, 0}] || len(rec.steps) != 2 {
		t.Errorf("steps %v; a refusal isn't a step", rec.steps)
	}
	if a := rec.attempts[2]; a.step != "Down" || len(a.args) != 1 || a.args[0] != "a1" || a.from != 1 || a.to != 0 {
		t.Errorf("attempt %+v", a)
	}
	for _, bad := range []string{
		`{"states": [{"n": 0}], "init": [1], "attempts": []}`,
		`{"states": [{"n": 0}], "init": [0], "attempts": [[0, "Up", [], 3]]}`,
		`{"states": [{"n": 0}], "init": [0], "attempts": [[0, "Up", 1]]}`,
		`{"states": [{"n": 0}], "init": [], "attempts": []}`,
	} {
		if _, problem := decode([]byte(bad), []string{"n"}); problem == "" {
			t.Errorf("decode(%s) found no problem", bad)
		}
	}
	// Runs, as drivers wrote them before attempts.
	rec, problem = decode([]byte(`{"traces": [[{"n": 0}, {"n": 1}, {"n": 1}]]}`), []string{"n"})
	if problem != "" || rec.attempts != nil || rec.runs != 1 || rec.recorded != 2 || len(rec.steps) != 1 {
		t.Errorf("runs: %+v, %s", rec, problem)
	}
}

func TestUntriedText(t *testing.T) {
	steps := []tla.Step{
		{Name: "Tick"},
		{Name: "Push", Args: []string{"h", "l"}, Binders: []tla.Binder{{Var: "h", Domain: "Heads"}, {Var: "l", Domain: "Locks"}}},
	}
	text := untriedText(steps)
	for _, want := range []string{
		`{invariant_x \in {<<"Tick">>} : invariant_x \notin invariant_t /\ ~(~ENABLED (Tick /\ UNCHANGED invariant_i) /\ ENABLED (Invariant_Larger!Tick /\ UNCHANGED invariant_i))}`,
		`UNION {{<<"Push", h, l>> : l \in Locks} : h \in Heads}`,
		`ENABLED (Invariant_Larger!Push(invariant_x[2], invariant_x[3]) /\ UNCHANGED invariant_i)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("untried text lacks %q:\n%s", want, text)
		}
	}
	if got := instance("Push", []string{"h1", "l2"}); got != `<<"Push", h1, l2>>` {
		t.Errorf("instance = %s", got)
	}
	printed := []string{"something else", "<<\"invariant-untried\",\n  {<<\"Push\", h1, l2>>}>>"}
	if got := printedUntried(printed); got != `{<<"Push", h1, l2>>}` {
		t.Errorf("printedUntried = %q", got)
	}
}
