package project

import (
	"path/filepath"
	"strings"
	"testing"
)

// A manifest selects ratified invariants for TLAPS by name, each once
// (#360). Selecting one changes no statement, so it isn't in the lock.
func TestTLAPSSelectsRatifiedInvariants(t *testing.T) {
	lock := Lock{
		Bounds: map[string]string{"Size": "2"},
		Statements: []Statement{
			{Name: "Spec", Kind: Spec},
			{Name: "NoSharedConnection", Kind: Invariant},
			{Name: "TypeOK", Kind: Invariant},
			{Name: "PoolFull", Kind: Witness},
		},
	}
	manifest := Manifest{Module: ".invariant/specs/ConnectionPool.tla", Code: "pool", Language: "go", TLAPS: []string{"NoSharedConnection", "TypeOK"}}
	if err := Validate(manifest, lock); err != nil {
		t.Fatalf("two ratified invariants: %v", err)
	}
	for _, tc := range []struct {
		tlaps []string
		want  string
	}{
		{[]string{"PoolFull"}, "isn't a ratified invariant"},
		{[]string{"Unknown"}, "isn't a ratified invariant"},
		{[]string{"TypeOK", "TypeOK"}, "twice"},
	} {
		manifest.TLAPS = tc.tlaps
		if err := Validate(manifest, lock); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("tlaps %v: %v, want an error naming %q", tc.tlaps, err, tc.want)
		}
	}
	p := &Project{Dir: "/p"}
	if got, want := p.TLAPSProofPath("TypeOK"), filepath.Join("/p", ".invariant", "proofs", "TypeOK.tla"); got != want {
		t.Errorf("TLAPSProofPath = %q, want %q", got, want)
	}
}
