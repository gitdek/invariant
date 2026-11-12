package conformance_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

// The runner stands in only for TLC. Production decoding, canonical equality,
// obligation generation and counting run for both public recording APIs.
type coverage802Runner struct{}

func (coverage802Runner) Check(_ context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	b, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		return tlc.Result{}, err
	}
	switch cfg.Specification {
	case "Invariant_ISpec":
		if len(cfg.Invariants) != 1 || cfg.Invariants[0] != "Init" || !strings.Contains(string(b), "Invariant_Starts") {
			return tlc.Result{}, fmt.Errorf("missing Init obligation")
		}
	case "Invariant_CSpec":
		if !strings.Contains(string(b), "/\\ Next") {
			return tlc.Result{}, fmt.Errorf("missing Next obligation")
		}
	default:
		return tlc.Result{}, fmt.Errorf("unexpected obligation %q", cfg.Specification)
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}

func TestSampledCoverageCountsOnlyReferencedCanonicalStates802(t *testing.T) {
	// Distinct JSON entries need not be distinct model states. The third
	// canonical state in each table is never an initial state or an endpoint.
	for _, tc := range []struct {
		name, raw                  string
		states, steps, transitions int
	}{
		{"trace repeats", `{"traces":[[{"v":{"$set":[2,1,1]}},{"v":{"$set":[1,2]}},{"v":{"$set":[3]}}],[{"v":{"$set":[2,1]}},{"v":{"$set":[3]}}]]}`, 2, 3, 1},
		{"unused table entry", `{"states":[{"v":{"$set":[2,1,1]}},{"v":{"$set":[1,2]}},{"v":{"$set":[3]}},{"v":{"$set":[99]}}],"init":[0,1],"attempts":[[1,"Move",[],2],[0,"Move",[],2],[2,"Stay",[],2]],"coverage":100}`, 2, 3, 1},
		{"initial state only", `{"states":[{"v":{"$set":[1,2]}},{"v":{"$set":[99]}}],"init":[0],"attempts":[]}`, 1, 0, 0},
		{"unchanged attempt endpoint", `{"states":[{"v":{"$set":[1]}},{"v":{"$set":[2]}},{"v":{"$set":[99]}}],"init":[0],"attempts":[[1,"Stay",[],1]]}`, 2, 1, 0},
	} {
		for _, stream := range []bool{false, true} {
			dir := t.TempDir()
			var got conformance.Result
			var err error
			if stream {
				got, err = conformance.CheckReader(t.Context(), coverage802Runner{}, dir, "Coverage", nil, []string{"v"}, 5, strings.NewReader(tc.raw), nil)
			} else {
				got, err = conformance.Check(t.Context(), coverage802Runner{}, dir, "Coverage", nil, []string{"v"}, 5, []byte(tc.raw), nil)
			}
			if err != nil || !got.Passed {
				t.Fatalf("%s stream=%v: check failed: %+v, %v", tc.name, stream, got, err)
			}
			if got.States != tc.states || got.ModelStates != 5 || got.Steps != tc.steps || got.Transitions != tc.transitions {
				t.Errorf("%s stream=%v: reached/count evidence %+v; want %d distinct referenced states, %d steps and %d transitions out of 5 model states", tc.name, stream, got, tc.states, tc.steps, tc.transitions)
			}
		}
	}
}
