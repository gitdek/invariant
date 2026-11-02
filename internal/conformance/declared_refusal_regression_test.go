package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

type declaredAttemptRunner func(context.Context, string, string, tlc.Config) (tlc.Result, error)

func (f declaredAttemptRunner) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	return f(ctx, dir, module, cfg)
}

func TestDeclaredAttemptMetadataFailsBeforeTools(t *testing.T) {
	for _, metadata := range []string{
		`{"outcome":"refused","outcome":"accepted"}`,
		`{"outcome":"accepted","outcome":"accepted"}`,
		`{"outcome":"accepted","outc\u006fme":"accepted"}`,
		`{"outcome":"refused","refusal":{"type":"x.Wrong","type":"x.Error","attributes":{}}}`,
		`{"outcome":"refused","refusal":{"type":"x.Error","attributes":{"code":500,"code":409}}}`,
		`{"outcome":"accepted","extra":true}`,
		`{"outcome":"accepted","refusal":null}`,
		`{"outcome":"refused","refusal":null}`,
		`{"outcome":"refused","refusal":{"type":"x.Error","attributes":null}}`,
		`{"outcome":"refused","refusal":{"type":"Error","attributes":{}}}`,
		`{"outcome":"refused","refusal":{"type":"x..Error","attributes":{}}}`,
		`{"outcome":"refused","refusal":{"type":"x.Error","attributes":{},"extra":true}}`,
		`{"outcome":"refused","refusal":{"type":"x.Error","attributes":{"x":{}}}}`,
		`{"outcome":"refused","refusal":{"type":"x.Error","attributes":{"x":1e999}}}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			raw := []byte(`{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[],0,` + metadata + `]]}`)
			runner := declaredAttemptRunner(func(context.Context, string, string, tlc.Config) (tlc.Result, error) {
				t.Fatal("malformed metadata reached TLC")
				return tlc.Result{}, nil
			})
			r, err := Check(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"n"}, 1, raw, &Model{Steps: []tla.Step{{Name: "Read"}}})
			if err != nil || r.Passed || r.Message == "" || r.Tried != nil {
				t.Fatalf("malformed metadata gained evidence: %+v, %v", r, err)
			}
		})
	}
	// Exception evidence accepts all finite JSON scalars, independently of
	// TLA+'s more restrictive state/argument encoding, without rounding ints.
	raw := []byte(`{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[],0,{"outcome":"refused","refusal":{"type":"x.outer.<locals>.Error","attributes":{"none":null,"bool":true,"text":"x","int":90071992547409931234567890,"float":1.25}}}]]}`)
	if _, problem := decode(raw, []string{"n"}); problem != "" {
		t.Fatal(problem)
	}
	// Opting out retains the legacy JSON decoder's handling of repeated keys
	// in argument records; this strict validation concerns metadata alone.
	legacy := []byte(`{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[{"code":500,"code":409}],0]]}`)
	if _, problem := decode(legacy, []string{"n"}); problem != "" {
		t.Fatalf("legacy argument decoding changed: %s", problem)
	}
}

func TestDeclaredAttemptsRequireKnownUnambiguousModelActions(t *testing.T) {
	raw := []byte(`{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[],0,{"outcome":"accepted"}]]}`)
	for _, model := range []*Model{
		nil,
		{},
		{Steps: []tla.Step{{Name: "Other"}}},
		{Steps: []tla.Step{{Name: "Read", Args: []string{"n"}}}},
		{Steps: []tla.Step{{Name: "Read"}, {Name: "Read", Args: []string{"n"}}, {Name: "Read"}}},
		{Steps: []tla.Step{{Name: "Read"}}, Problem: "Next is not readable"},
	} {
		runner := declaredAttemptRunner(func(context.Context, string, string, tlc.Config) (tlc.Result, error) {
			t.Fatal("invalid action reached TLC")
			return tlc.Result{}, nil
		})
		r, err := Check(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"n"}, 1, raw, model)
		if err != nil || r.Passed || r.Message == "" || r.Tried != nil {
			t.Fatalf("uncheckable strict attempt gained evidence: %+v, %v", r, err)
		}
	}
}

func TestDeclaredAttemptBatchesKeepEveryOutcomeAndExactArgument(t *testing.T) {
	const exact = "9007199254740993"
	var attempts [][]any
	for i := 0; i < 2*stepBatch+1; i++ {
		metadata := map[string]any{"outcome": "accepted"}
		if i%2 == 1 {
			metadata = map[string]any{"outcome": "refused", "refusal": map[string]any{"type": "x.Error", "attributes": map[string]any{}}}
		}
		attempts = append(attempts, []any{0, "Read", []any{json.Number(exact)}, 0, metadata})
	}
	raw, err := json.Marshal(map[string]any{"states": []any{map[string]any{"n": 0}}, "init": []int{0}, "attempts": attempts})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []string
	runner := declaredAttemptRunner(func(_ context.Context, dir, module string, _ tlc.Config) (tlc.Result, error) {
		if module == "Invariant_StrictAttempts" {
			body, err := os.ReadFile(filepath.Join(dir, module+".tla"))
			if err != nil {
				return tlc.Result{}, err
			}
			mu.Lock()
			bodies = append(bodies, string(body))
			mu.Unlock()
		}
		return tlc.Result{Outcome: tlc.Passed}, nil
	})
	r, err := Check(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"n"}, 1, raw, &Model{Steps: []tla.Step{{Name: "Read", Args: []string{"n"}}}})
	if err != nil || !r.Passed || r.Tried == nil || !r.Tried.Passed || len(bodies) != 3 {
		t.Fatalf("strict batches: %+v, %v, %d batches", r, err, len(bodies))
	}
	accepted, refused := 0, 0
	for _, body := range bodies {
		yes := strings.Count(body, "<<"+exact+">>, FALSE>>")
		no := strings.Count(body, "<<"+exact+">>, TRUE>>")
		if yes+no > stepBatch || yes+no == 0 {
			t.Fatalf("unbounded or empty strict batch: %d attempts", yes+no)
		}
		accepted += yes
		refused += no
	}
	if accepted != stepBatch+1 || refused != stepBatch {
		t.Fatalf("same-pair outcomes lost or arguments rounded: %d accepted, %d refused", accepted, refused)
	}
}

func TestDeclaredAttemptTLCFailuresCannotEarnCoverage(t *testing.T) {
	raw := []byte(`{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[1],0,{"outcome":"accepted"}]]}`)
	for _, outcome := range []tlc.Outcome{tlc.Deadlock, tlc.Violated, tlc.Failed, "timed_out", "runner error"} {
		t.Run(string(outcome), func(t *testing.T) {
			runner := declaredAttemptRunner(func(_ context.Context, _, module string, _ tlc.Config) (tlc.Result, error) {
				if module == "Invariant_Tried" {
					t.Fatal("failed strict attempts reached coverage checks")
				}
				if module != "Invariant_StrictAttempts" {
					return tlc.Result{Outcome: tlc.Passed}, nil
				}
				if outcome == "runner error" {
					return tlc.Result{}, context.DeadlineExceeded
				}
				return tlc.Result{Outcome: outcome, Message: "fixture failure", Trace: []tlc.State{{Vars: []tlc.Var{{Name: "invariant_i", Value: "1"}}}}}, nil
			})
			r, err := Check(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"n"}, 1, raw, &Model{Steps: []tla.Step{{Name: "Read", Args: []string{"n"}}}})
			if r.Passed || r.Tried != nil {
				t.Fatalf("failed strict attempt gained coverage: %+v, %v", r, err)
			}
			diagnostic := r.Message + fmt.Sprint(err)
			if !strings.Contains(diagnostic, "Read(1)") || !strings.Contains(diagnostic, "n = 0") {
				t.Fatalf("diagnostic lost named action, exact argument or state: %s", diagnostic)
			}
		})
	}
}
