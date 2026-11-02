package conformance

// #392 extends only explicitly marked attempts. Four-field legacy attempts
// and sampled traces retain D-0085/D-0200's existing Next/stuttering rule.
// Strict fifth-field metadata is described in verify's acceptance contract.
// Accepted attempts must satisfy the named action(args) between those exact
// states, even if identical; refused attempts must have identical states and
// ~ENABLED action(args) there, not merely ~ENABLED <<action(args)>>_vars.
// An enabled read-only action must therefore not be reported as a refusal.
// Check both named actions and arguments; a different action satisfying Next
// cannot excuse a bad acceptance. Unknown action names, wrong arity, malformed
// outcomes and changed-state refusals must fail, never downgrade to legacy.
// Failure/timeout/error of any added TLC obligation cannot earn success or
// every-step/every-state coverage. Preserve bounded batching, exact arguments,
// diagnostics identifying the offending action/arguments/state, and existing
// check provenance. Do not deduplicate distinct strict outcomes by state pair.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

const refusal392Model = `---- MODULE Slot ----
EXTENDS Naturals
VARIABLE count
vars == <<count>>
Init == count = 0
Submit(k) == count = 0 /\ k = 1 /\ count' = k
Read(k) == count = k /\ UNCHANGED count
Legacy == UNCHANGED count
Next == (\E k \in 1..2 : Submit(k) \/ Read(k)) \/ Legacy
====
`

func refusal392Attempts() [][]any {
	yes := map[string]any{"outcome": "accepted"}
	no := func(code int) any {
		return map[string]any{"outcome": "refused", "refusal": map[string]any{"type": "counter.core.Refused", "attributes": map[string]any{"status_code": code}}}
	}
	return [][]any{
		{0, "Submit", []any{1}, 1, yes}, {0, "Submit", []any{2}, 0, no(409)},
		{0, "Read", []any{1}, 0, no(404)}, {0, "Read", []any{2}, 0, no(404)}, {0, "Legacy", []any{}, 0},
		{1, "Submit", []any{1}, 1, no(409)}, {1, "Submit", []any{2}, 1, no(409)},
		{1, "Read", []any{1}, 1, yes}, {1, "Read", []any{2}, 1, no(404)}, {1, "Legacy", []any{}, 1},
	}
}

func refusal392Check(t *testing.T, runner Runner, list [][]any) Result {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Slot.tla"), []byte(refusal392Model), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"states": []any{map[string]any{"count": 0}, map[string]any{"count": 1}}, "init": []int{0}, "attempts": list})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := tla.Steps(refusal392Model)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Check(context.Background(), runner, dir, "Slot", nil, []string{"count"}, 2, raw, &Model{Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func refusal392Passed(r Result) bool { return r.Passed && r.Tried != nil && r.Tried.Passed }

type refusal392Runner struct {
	mu     sync.Mutex
	bodies []string
	fail   bool
}

func (r *refusal392Runner) Check(_ context.Context, dir, module string, _ tlc.Config) (tlc.Result, error) {
	b, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		return tlc.Result{}, err
	}
	r.mu.Lock()
	r.bodies = append(r.bodies, string(b))
	r.mu.Unlock()
	if r.fail && module != "Invariant_ConformanceInit" {
		return tlc.Result{Outcome: tlc.Violated, Message: "fixture obligation failed"}, nil
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}

func TestDeclaredAttemptConformance(t *testing.T) {
	runner := &refusal392Runner{}
	r := refusal392Check(t, runner, refusal392Attempts())
	if !refusal392Passed(r) || r.Steps != 10 || r.States != 2 {
		t.Fatalf("strict attempts not admitted: %+v", r)
	}
	body := strings.Join(runner.bodies, "\n")
	// Semantic ingredients, without pinning module names or expression layout.
	for _, s := range []string{"Submit(", "Read(", "ENABLED"} {
		if !strings.Contains(body, s) {
			t.Errorf("TLC never received %s", s)
		}
	}
	failed := refusal392Check(t, &refusal392Runner{fail: true}, refusal392Attempts())
	if refusal392Passed(failed) {
		t.Fatal("TLC failure accepted")
	}
	for _, c := range []struct {
		name   string
		mutate func([][]any)
	}{
		{"unknown outcome", func(a [][]any) { a[0][4] = map[string]any{"outcome": "unknown"} }},
		{"null metadata", func(a [][]any) { a[0][4] = nil }},
		{"missing outcome", func(a [][]any) { a[0][4] = map[string]any{} }},
		{"accepted with exception", func(a [][]any) {
			a[0][4] = map[string]any{"outcome": "accepted", "refusal": map[string]any{"type": "x.Error", "attributes": map[string]any{}}}
		}},
		{"refused without exception", func(a [][]any) { a[1][4] = map[string]any{"outcome": "refused"} }},
		{"refused changes state", func(a [][]any) { a[1][3] = 1 }},
		{"non scalar attribute", func(a [][]any) {
			a[1][4] = map[string]any{"outcome": "refused", "refusal": map[string]any{"type": "x.Error", "attributes": map[string]any{"code": []any{409}}}}
		}},
		{"unknown named action", func(a [][]any) { a[0][1] = "Invented" }},
		{"wrong argument count", func(a [][]any) { a[0][2] = []any{} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := refusal392Attempts()
			c.mutate(a)
			if x := refusal392Check(t, &refusal392Runner{}, a); refusal392Passed(x) {
				t.Fatalf("malformed strict attempt accepted: %+v", x)
			}
		})
	}
	// Legacy records are still valid even when their stutter isn't that named
	// action, and even when another action accounts for a changed state.
	old := refusal392Attempts()
	for i := range old {
		old[i] = old[i][:4]
	}
	old[0][2] = []any{2}
	// Still try both Submit arguments: legacy conformance accepts these two
	// identical state changes via Next, even though Submit(2) is disabled.
	old[1] = []any{0, "Submit", []any{1}, 1}
	if x := refusal392Check(t, &refusal392Runner{}, old); !refusal392Passed(x) {
		t.Fatalf("legacy behavior changed: %+v", x)
	}
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real TLC", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			real := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
			if x := refusal392Check(t, real, refusal392Attempts()); !refusal392Passed(x) {
				t.Fatalf("valid strict attempts: %+v", x)
			}
			for _, c := range []struct {
				name   string
				mutate func([][]any)
			}{
				{"enabled read-only refusal", func(a [][]any) { a[7][4] = a[8][4] }},
				{"disabled normal read-only return", func(a [][]any) { a[2][4] = map[string]any{"outcome": "accepted"} }},
				{"wrong argument despite legal Next", func(a [][]any) {
					a[0][2] = []any{2}
					a[1] = []any{0, "Submit", []any{1}, 1, map[string]any{"outcome": "accepted"}}
				}},
				{"wrong name despite legal Next", func(a [][]any) {
					a[0][1] = "Read"
					a[2] = []any{0, "Submit", []any{1}, 1, map[string]any{"outcome": "accepted"}}
				}},
			} {
				t.Run(c.name, func(t *testing.T) {
					a := refusal392Attempts()
					c.mutate(a)
					x := refusal392Check(t, real, a)
					if refusal392Passed(x) {
						t.Fatalf("TLC accepted wrong outcome: %+v", x)
					}
				})
			}
			if x := refusal392Check(t, real, old); !refusal392Passed(x) {
				t.Fatalf("real legacy changed: %+v", x)
			}
		})
	}
}
