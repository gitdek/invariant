package project

import (
	"strings"
	"testing"
)

// The gate writes each bound into TLC's config as CONSTANT Name = value, and
// TLC reads the config a token at a time. A value that holds a line end, or
// a second value, would add a directive: "2\nCONSTRAINT Calm" made TLC check
// only the states an operator of the factory's allowed, and a proposal shows
// it as `Capacity = 2 CONSTRAINT Calm`. A bound is one name and one value.
func TestABoundIsOneValue(t *testing.T) {
	for _, value := range []string{
		"2", "0", "-1", "r1", "NoOne", "_x9", `"working"`, `""`, `"a  b \"quoted\" \\path"`, "{}", "{r1, r2, r3}", "{alice,invbot}",
		`{"", "same-origin", "none"}`, `{"localhost:3000", "app.example:3000", ""}`, "<<1, 2>>", "<<>>", " { a , b } ",
		`{{{"tokenTag", c0}, {"phaseTag", "none"}}, {{"tokenTag", c1}, {"phaseTag", "open"}}}`,
	} {
		if err := validBounds(map[string]string{"Capacity": value}); err != nil {
			t.Errorf("the bound %q is refused: %v", value, err)
		}
	}
	for value, want := range map[string]string{
		"":                           "it's empty",
		"2\nCONSTRAINT Calm":         "more than one value",
		"2 CONSTRAINT Calm":          "more than one value",
		"2\rCHECK_DEADLOCK FALSE":    "more than one value",
		"2\tx":                       "more than one value",
		"{a} {b}":                    "more than one value",
		"a b":                        "more than one value",
		"{a, b":                      "isn't closed by }",
		"{a b}":                      "isn't closed by }",
		"<<1, 2":                     "isn't closed by >>",
		`"open`:                      "a string never closes",
		"\"a\nb\"":                   "a control character",
		"CONSTRAINT":                 "starts a directive",
		"{INVARIANT}":                "starts a directive",
		"1..3":                       "more than one value",
		"-":                          "isn't a number",
		"[a |-> 1]":                  "can't start a value",
		"{a, }":                      "can't start a value",
		"2 \\* a comment":            "more than one value",
		"(* c *) 2":                  "can't start a value",
		"{r1, r2}\nINVARIANT TypeOK": "more than one value",
	} {
		err := validBounds(map[string]string{"Capacity": value})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "Capacity") {
			t.Errorf("the bound %q: %v; want a refusal saying %q", value, err, want)
		}
	}
	for _, name := range []string{"", "2x", "Max Lines", "A\nCONSTRAINT Calm", "a-b", "CONSTRAINT", "INVARIANT", "x=1"} {
		if err := validBounds(map[string]string{name: "2"}); err == nil || !strings.Contains(err.Error(), "isn't a constant's name") {
			t.Errorf("the bound named %q: %v", name, err)
		}
	}
	// A lock is refused as a whole, and the gate's config never holds it.
	lock := Lock{Bounds: map[string]string{"Capacity": "2\nCONSTRAINT Calm"}, Statements: []Statement{{Name: "Spec", Kind: Spec, Says: "s", SHA256: "sha256:x"}}}
	if err := Validate(Manifest{Name: "queue", Module: "Q.tla", Code: "q"}, lock); err == nil || !strings.Contains(err.Error(), "isn't one value") {
		t.Errorf("a lock with a bound that adds a directive: %v", err)
	}
}
