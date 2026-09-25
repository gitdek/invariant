package conformance

import (
	"encoding/json"
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
