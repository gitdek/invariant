package statechart_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// TLC wraps a long value over several lines, as it prints the two-phase
// commit's messages. The replay module must carry it on its conjunct's line:
// a continuation line left of the junction bullets would end the list, and
// SANY refused the module (#636).
func TestReplayJoinsWrappedValuesOntoOneLine(t *testing.T) {
	in, dir := reader586EvalFixture(t)
	wrapped := "{ [type |-> \"Abort\"],\n  [type |-> \"Prepared\", rm |-> \"r1\"] }"
	for i := range in.Graph.Graph.States {
		in.Graph.Graph.States[i].Values["worker"] = wrapped
	}
	var source []byte
	stop := errors.New("captured the replay module")
	runner := replayFunc(func(ctx context.Context, dir, module string, cfg tlc.Config) (*tlc.GraphResult, error) {
		var err error
		source, err = os.ReadFile(filepath.Join(dir, module+".tla"))
		if err != nil {
			t.Fatal(err)
		}
		return nil, stop
	})
	if _, err := statechart.EvaluateRules(t.Context(), runner, dir, in.Graph, in.Lock); !errors.Is(err, stop) {
		t.Fatalf("the replay didn't reach TLC: %v", err)
	}
	want := `worker = ({ [type |-> "Abort"], [type |-> "Prepared", rm |-> "r1"] })`
	if !strings.Contains(string(source), want) {
		t.Fatalf("the wrapped value isn't on its conjunct's line:\n%s", source)
	}
	// Every line inside Init's disjunction starts with its own bullet.
	inInit := false
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasSuffix(line, "Init ==") {
			inInit = true
			continue
		}
		if !inInit {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, `\/ /\`) && !strings.HasPrefix(trimmed, `/\`) {
			t.Fatalf("a replay line continues a value outside its junction bullet: %q\n%s", line, source)
		}
	}
}
