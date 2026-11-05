package verify_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestReader586ReplayValuesPinnedTLC(t *testing.T) {
	if os.Getenv("INVARIANT_STATECHART_REAL") != "1" {
		t.Skip("set INVARIANT_STATECHART_REAL=1 to run pinned TLC replay value regressions")
	}
	reader586ReplayValuesReal(t)
}

func reader586ReplayValuesReal(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	for _, test := range []struct{ name, value string }{
		{"interval", "1..2"},
		{"model values", "RM"},
		{"structured model values", `[nodes |-> RM, byNode |-> [n \in RM |-> <<n, "a  b">>], span |-> -2..2]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := fmt.Sprintf(`---- MODULE ReplayValues ----
EXTENDS Integers, Sequences, TLC
CONSTANT RM
VARIABLE x
Init == x = %s
Next == UNCHANGED x
Spec == Init /\ [][Next]_x
Keep == x = %s
Reject == x # %s
====
`, test.value, test.value, test.value)
			dir := t.TempDir()
			source := filepath.Join(dir, "ReplayValues.tla")
			if err := os.WriteFile(source, []byte(model), 0600); err != nil {
				t.Fatal(err)
			}
			lock := project.Lock{Bounds: map[string]string{"RM": "{z, a}"}, Statements: []project.Statement{
				{Name: "Spec", Kind: project.Spec, Says: "Keep the initial value."},
				{Name: "Keep", Kind: project.Invariant, Says: "The value stays unchanged."},
				{Name: "Reject", Kind: project.Invariant, Says: "The value differs from the initial value."},
			}}
			for i := range lock.Statements {
				s := &lock.Statements[i]
				s.SHA256, err = tla.PinHash(model, s.Name, project.Model)
				if err != nil {
					t.Fatal(err)
				}
			}
			g, err := runner.DumpGraph(ctx, dir, "ReplayValues", tlc.Config{Specification: "Spec", Constants: lock.Bounds})
			if err != nil || g == nil {
				t.Fatalf("complete graph: %v %v", g, err)
			}
			if g.StateCount != 1 || g.StepCount != 1 {
				t.Fatalf("expected one snapshot and unchanged step: %+v", g)
			}
			state := g.Graph.States[0]
			if test.name == "interval" && strings.TrimSpace(state.Values["x"]) != "1..2" {
				t.Fatalf("fixture no longer exercises TLC's printed interval: %v", state.Values)
			}
			matrix, err := statechart.EvaluateRules(ctx, runner, dir, *g, lock)
			want := map[string]map[string]bool{state.ID: {"Keep": true, "Reject": false}}
			if err != nil || !reflect.DeepEqual(matrix, want) {
				t.Fatalf("TLC replay of %v: %v %v", state.Values, matrix, err)
			}
			data, err := os.ReadFile(source)
			if err != nil || string(data) != model {
				t.Fatal("replay changed the model")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "ReplayValues.tla" {
				t.Fatalf("replay artifacts remain: %v %v", entries, err)
			}
		})
	}
}
