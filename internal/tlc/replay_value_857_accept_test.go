package tlc

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ReplayValue is a tagged lossless value: Kind is bool, integer, string,
// model, set, sequence, record or function; Atom holds scalar text, Elements
// holds set/sequence members, and Entries holds typed Key/Value pairs.
// TLA renders only validated data, never unchecked source expressions.
// Set members and record/function entries canonicalize; sequences do not.
// ParseValue remains the existing display API with its existing tests intact.
func TestReplayValuesPreserveTypesAndExactIntegers857(t *testing.T) {
	cases := []struct{ source, kind, atom string }{
		{`r1`, "model", "r1"}, {`"r1"`, "string", "r1"}, {`TRUE`, "bool", "TRUE"},
		{`9007199254740993`, "integer", "9007199254740993"}, {`-9223372036854775809`, "integer", "-9223372036854775809"},
		{`{r1, "r1"}`, "set", ""}, {`<<r1, "r1">>`, "sequence", ""},
		{`[a |-> <<1, 2>>, b |-> {r1}]`, "record", ""},
		{`(r1 :> 1 @@ "r1" :> 2 @@ <<1, 2>> :> 3)`, "function", ""},
	}
	for _, tc := range cases {
		v, err := ParseReplayValue(tc.source)
		if err != nil {
			t.Fatal(err)
		}
		if v.Kind != tc.kind || v.Atom != tc.atom {
			t.Errorf("%s loses type/value: %+v", tc.source, v)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var round ReplayValue
		if err = json.Unmarshal(b, &round); err != nil {
			t.Fatal(err)
		}
		text, err := round.TLA()
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseReplayValue(text)
		if err != nil || !reflect.DeepEqual(v, back) {
			t.Errorf("%s changes across JSON/TLA: %s %+v %v", tc.source, text, back, err)
		}
	}
	for _, pair := range [][2]string{{`{r1, "r1"}`, `{"r1", r1}`}, {`1..3`, `{3, 1, 2}`}, {`(r1 :> 1 @@ "r1" :> 2)`, `("r1" :> 2 @@ r1 :> 1)`}} {
		a, e1 := ParseReplayValue(pair[0])
		b, e2 := ParseReplayValue(pair[1])
		if e1 != nil || e2 != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("equal TLA values have different encoding: %v %v %v %v", a, b, e1, e2)
		}
	}
	a, _ := ParseReplayValue(`<<1,2>>`)
	b, _ := ParseReplayValue(`<<2,1>>`)
	if reflect.DeepEqual(a, b) {
		t.Fatal("sequence order lost")
	}
	for _, bad := range []string{`(r1 :> 1 @@ r1 :> 2)`, `[a |-> 1, a |-> 2]`, `<<1`, `1.5`, `1 /\ TRUE`, `"unterminated`} {
		if _, err := ParseReplayValue(bad); err == nil {
			t.Errorf("accepted malformed/ambiguous replay value %q", bad)
		}
	}
}
