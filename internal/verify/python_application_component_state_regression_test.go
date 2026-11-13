package verify

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPythonApplicationComponentStateBatchFailure(t *testing.T) {
	t.Parallel()
	for _, explored := range []bool{false, true} {
		for _, attributed := range []bool{false, true} {
			t.Run(fmt.Sprintf("explored=%t/attributed=%t", explored, attributed), func(t *testing.T) {
				r, inv := PythonModuleReceiptFixture837(explored, []map[string]any{
					{"status": []any{}}, {"status": []any{"queued"}}, {"status": []any{"done"}},
				})
				later := r.Application.Executions[0]
				later.Key.ID, later.Declared.ID = "later", "later"
				later.Observations = slices.Clone(later.Observations)
				for i := range later.Observations {
					later.Observations[i].Key = later.Key
				}
				inv.Checks[0].Invocations = append(inv.Checks[0].Invocations, later.Declared)
				r.Application.Executions = append(r.Application.Executions, later)
				check := r.Application.Conformance[0]
				check.Key, check.Invocation = later.Key, later.Declared
				r.Application.Conformance = append(r.Application.Conformance, check)
				if attributed {
					if err := r.attributePythonApplication(inv); err != nil {
						t.Fatal(err)
					}
				}
				// A valid changed prefix must not replace prior evidence when
				// another invocation's last observation cannot be encoded.
				r.Application.Executions[0].Observations[1].Before = map[string]any{"status": []any{"changed"}}
				last := &later.Observations[len(later.Observations)-1]
				last.After = map[string]any{"status": []any{"done", nil}}
				before := moduleReceipt837JSON(t, r)
				err := r.attributePythonApplication(inv)
				if err == nil {
					t.Fatal("late invalid state was accepted")
				}
				for _, want := range []string{later.Key.Target, later.Key.Check, "invocation later", fmt.Sprintf("run %d step %d", last.Run, last.Sequence), "after", "status"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("diagnostic %q omits %q", err, want)
					}
				}
				if after := moduleReceipt837JSON(t, r); after != before {
					t.Fatal("failed batch changed the report")
				}
			})
		}
	}
}

func TestPythonApplicationComponentNonModuleTags(t *testing.T) {
	t.Parallel()
	for _, explored := range []bool{false, true} {
		t.Run(fmt.Sprintf("explored=%t", explored), func(t *testing.T) {
			state := map[string]any{
				"sequence": map[string]any{"$seq": []any{"b", "a", "b"}},
				"empty":    map[string]any{"$seq": []any{}},
				"set":      map[string]any{"$set": []any{"b", "a", "b"}},
				"function": map[string]any{"$fn": []any{[]any{json.Number("1"), "one"}}},
				"model":    map[string]any{"$mv": "p1"},
			}
			want := map[string]string{
				"sequence": `<<"b", "a", "b">>`, "empty": `<<>>`, "set": `{"a", "b"}`,
				"function": `(1 :> "one")`, "model": "p1",
			}
			r, inv := PythonModuleReceiptFixture837(explored, []map[string]any{state, state})
			for i := range r.Application.Executions[0].Observations {
				r.Application.Executions[0].Observations[i].Form = ""
			}
			raw := moduleReceipt837JSON(t, r.Application)
			if err := r.attributePythonApplication(inv); err != nil {
				t.Fatal(err)
			}
			for _, row := range r.Components.Conformance[0].Result.Observations {
				if !reflect.DeepEqual(row.From, want) || !reflect.DeepEqual(row.To, want) {
					t.Fatalf("non-module tags changed: %+v", row)
				}
			}
			if moduleReceipt837JSON(t, r.Application) != raw {
				t.Fatal("attribution changed raw evidence")
			}
		})
	}
}
