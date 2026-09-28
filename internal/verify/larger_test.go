package verify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// One size larger is each number plus one, and each set of numbered model
// values with the next one added. Anything else stays as it is.
func TestLargerBounds(t *testing.T) {
	larger, sizes := largerBounds(map[string]string{
		"Capacity": "2", "Producers": "{p1, p2}", "Heads": "{h1,h2}",
		"Actors": "{alice, mallory}", "NoP": "NoP", "Limit": " 3 ", "Mixed": "{p1, q2}",
	})
	want := map[string]string{
		"Capacity": "3", "Producers": "{p1, p2, p3}", "Heads": "{h1, h2, h3}",
		"Actors": "{alice, mallory}", "NoP": "NoP", "Limit": "4", "Mixed": "{p1, q2}",
	}
	for k, v := range want {
		if larger[k] != v {
			t.Errorf("%s: %q, want %q", k, larger[k], v)
		}
	}
	if len(sizes) != 4 || sizes["Capacity"] != 3 || sizes["Producers"] != 3 || sizes["Heads"] != 3 || sizes["Limit"] != 4 {
		t.Errorf("sizes %v", sizes)
	}
}

// The explorer's bounds change only when it names every one of them, once.
func TestLargerExplorer(t *testing.T) {
	src := []byte("package x\n\nconst (\n\tProducers = 2\n\tCapacity  = 2 // the buffer's\n\tMaxLines  = 2\n)\n\nconst MaxLog = Producers * MaxLines\n")
	sizes := map[string]int{"Producers": 3, "Capacity": 3, "MaxLines": 3}
	if !declaresBounds(src, sizes) {
		t.Fatal("the explorer names its bounds")
	}
	got := string(largerExplorer(src, sizes))
	for _, want := range []string{"\tProducers = 3\n", "\tCapacity  = 3 // the buffer's\n", "\tMaxLines  = 3\n", "MaxLog = Producers * MaxLines"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if declaresBounds(src, map[string]int{"Capacity": 3, "Consumers": 2}) {
		t.Error("an explorer that doesn't name a bound can't be made larger")
	}
	if declaresBounds([]byte("const (\n\tNP = 2\n\tNQ = 2\n)\n"), map[string]int{"Producers": 3}) {
		t.Error("constants named otherwise don't count")
	}
}

// The code agrees one size larger only when it reaches exactly the model's
// states there.
func TestCompareLarger(t *testing.T) {
	b := map[string]string{"Capacity": "3"}
	model := &tlc.Result{Outcome: tlc.Passed, DistinctStates: 111, Depth: 9}
	if l := compareLarger(b, model, &Exploration{OK: true, States: 111, Depth: 9}, ""); !l.Passed || !l.Required {
		t.Errorf("same states: %+v", l)
	}
	if l := compareLarger(b, model, &Exploration{OK: true, States: 87, Depth: 9}, ""); l.Passed || !l.Required || !strings.Contains(l.Message, "87 states") {
		t.Errorf("bounded code fails: %+v", l)
	}
	if l := compareLarger(b, nil, nil, "TLC didn't finish"); l.Passed || l.Required || l.Message != "TLC didn't finish" {
		t.Errorf("no model is neither claimed nor failed: %+v", l)
	}
	if l := compareLarger(b, &tlc.Result{Outcome: tlc.Violated}, &Exploration{OK: true}, ""); l.Passed || l.Required {
		t.Errorf("a problem in the model one size larger isn't the code's to fail: %+v", l)
	}
}

// Bounds are found, and made one size larger, in Go, TypeScript and Python.
func TestBoundsInEveryLanguage(t *testing.T) {
	sizes := map[string]int{"Capacity": 3, "Clients": 4}
	for lang, src := range map[string]string{
		"go":         "const (\n\tCapacity = 2\n\tClients  = 3 // c1, c2, c3\n)\n",
		"typescript": "const Capacity = 2;\nexport const Clients: number = 3; // c1, c2, c3\n",
		"python":     "Capacity = 2\nClients = 3  # c1, c2, c3\n",
	} {
		if !declaresBounds([]byte(src), sizes) {
			t.Errorf("%s: the bounds weren't found in\n%s", lang, src)
			continue
		}
		got := string(largerExplorer([]byte(src), sizes))
		if !strings.Contains(got, "Capacity = 3") || !strings.Contains(got, "Clients = 4") && !strings.Contains(got, "Clients: number = 4") && !strings.Contains(got, "Clients  = 4") {
			t.Errorf("%s: one size larger is\n%s", lang, got)
		}
	}
	// A name that only starts like a bound isn't one.
	if declaresBounds([]byte("const CapacityMax = 2;\nconst Clients = 3;\n"), sizes) {
		t.Error("CapacityMax was taken for Capacity")
	}
}

// The environment's bounds are the numbers the code doesn't take as
// parameters; sets of model values stay as they are (D-0085).
func TestEnvironmentLarger(t *testing.T) {
	got := environmentLarger(map[string]string{"Apis": "{a1, a2}", "Capacity": "2", "MaxCalls": "5", "MaxTime": " 3 ", "MaxWaiting": "2"}, []string{"Capacity", "MaxWaiting"})
	want := map[string]string{"MaxCalls": "6", "MaxTime": "4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environmentLarger = %v; want %v", got, want)
	}
	if got := environmentLarger(map[string]string{"RM": "{r1, r2, r3}"}, nil); len(got) != 0 {
		t.Errorf("two-phase commit has no numeric bounds, got %v", got)
	}
}
