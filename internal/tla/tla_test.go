package tla

import (
	"strings"
	"testing"
)

const module = `---- MODULE Demo ----
VARIABLE x

\* A pinned statement.
TypeOK == x \in 0..3

Safe ==
    /\ x >= 0   \* never negative
    /\ x <= 3

SafeAndMore == Safe /\ x # 2

Next(n) ==
    x' = n
====
`

func TestModuleName(t *testing.T) {
	name, err := ModuleName(module)
	if err != nil || name != "Demo" {
		t.Fatalf("ModuleName = %q, %v; want Demo", name, err)
	}
}

func TestDefinition(t *testing.T) {
	cases := map[string]string{
		"TypeOK": `TypeOK == x \in 0..3`,
		"Safe":   "Safe ==\n    /\\ x >= 0   \\* never negative\n    /\\ x <= 3",
		"Next":   "Next(n) ==\n    x' = n",
	}
	for name, want := range cases {
		got, err := Definition(module, name)
		if err != nil || got != want {
			t.Errorf("Definition(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := Definition(module, "Missing"); err == nil {
		t.Error("Definition(Missing) succeeded; want an error")
	}
}

func TestHashIgnoresFormattingButNotMeaning(t *testing.T) {
	def, _ := Definition(module, "Safe")
	reformatted := "Safe == /\\ x >= 0 /\\ x <= 3 (* same meaning *)"
	if Hash(def) != Hash(reformatted) {
		t.Errorf("reformatting changed the hash:\n%s\n%s", Canonical(def), Canonical(reformatted))
	}
	weakened := "Safe == /\\ x >= 0 /\\ x <= 4"
	if Hash(def) == Hash(weakened) {
		t.Error("changing the bound did not change the hash")
	}
	if !strings.HasPrefix(Hash(def), "sha256:") {
		t.Errorf("Hash = %q; want a sha256: prefix", Hash(def))
	}
}

func TestMutate(t *testing.T) {
	got, err := Mutate(module, "x <= 3", "x <= 4")
	if err != nil || !strings.Contains(got, "x <= 4") {
		t.Fatalf("Mutate = %v; want the replacement applied", err)
	}
	if _, err := Mutate(module, "x' = 7", "x' = 8"); err == nil {
		t.Error("Mutate with no match succeeded; want an error")
	}
	if _, err := Mutate(module, "x", "y"); err == nil {
		t.Error("Mutate with many matches succeeded; want an error")
	}
}
