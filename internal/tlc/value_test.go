package tlc

import (
	"reflect"
	"strings"
	"testing"
)

// TLC prints a set it built from an integer range as low..high. ParseValue
// reads it as that set of integers, wherever a value can appear, as ask3d#4's
// trace needs (#777).
func TestParseValueReadsIntervals(t *testing.T) {
	for _, c := range []struct {
		in   string
		want any
	}{
		{"1..3", []any{int64(1), int64(2), int64(3)}},
		{"1..1", []any{int64(1)}},
		{"1..0", []any{}},
		{"-2..0", []any{int64(-2), int64(-1), int64(0)}},
		{"[localItems |-> 1..1, loads |-> 0]", map[string]any{"localItems": []any{int64(1)}, "loads": int64(0)}},
		{"<<1..2, {}>>", []any{[]any{int64(1), int64(2)}, []any{}}},
		{"(a :> 1..0 @@ b :> 2..2)", map[string]any{"a": []any{}, "b": []any{int64(2)}}},
		{"{1..1, 0..0}", []any{[]any{int64(1)}, []any{int64(0)}}},
		{"7", int64(7)},
	} {
		got, err := ParseValue(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseValue(%q) = %#v, %v; want %#v", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ in, says string }{
		{"1..", "expected an integer after .."},
		{"1..x", "expected an integer after .."},
		{"0..65536", "holds more than 65536 integers"},
		{"-9223372036854775808..9223372036854775807", "holds more than"},
	} {
		if _, err := ParseValue(c.in); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("ParseValue(%q) = %v; want an error saying %q", c.in, err, c.says)
		}
	}
}
