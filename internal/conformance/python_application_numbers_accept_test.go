package conformance_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

type pa327NumberRunner struct {
	mu      sync.Mutex
	modules []string
}

func (r *pa327NumberRunner) Check(_ context.Context, dir, module string, _ tlc.Config) (tlc.Result, error) {
	b, e := os.ReadFile(filepath.Join(dir, module+".tla"))
	if e != nil {
		return tlc.Result{}, e
	}
	r.mu.Lock()
	r.modules = append(r.modules, string(b))
	r.mu.Unlock()
	return tlc.Result{Outcome: tlc.Passed}, nil
}
func TestPythonApplicationGateExactNumbers(t *testing.T) {
	// Python JSON integers must not silently round through float64 before the
	// TLC checker sees them. This is an encoding/transport test, not a claim
	// that TLC implements arbitrary precision arithmetic in a model.
	for _, n := range []string{"0", "-7", "9007199254740993", "-9007199254740993"} {
		got, e := conformance.Encode(json.Number(n))
		if e != nil || got != n {
			t.Errorf("exact number %s became %q: %v", n, got, e)
		}
	}
	for _, value := range []any{json.Number("1.25"), json.Number("NaN"), nil} {
		if got, e := conformance.Encode(value); e == nil {
			t.Errorf("unsupported scalar %v encoded as %q", value, got)
		}
	}
	r := &pa327NumberRunner{}
	raw := []byte(`{"traces":[[{"n":9007199254740993},{"n":9007199254740994}],[{"n":-9007199254740993},{"n":-9007199254740992}]]}`)
	result, e := conformance.Check(context.Background(), r, t.TempDir(), "Counter", map[string]string{"Limit": "2"}, []string{"n"}, 4, raw, nil)
	if e != nil || !result.Passed || result.Runs != 2 || result.Steps != 2 || result.States != 4 {
		t.Fatalf("exact trace transport: %+v %v", result, e)
	}
	text := strings.Join(r.modules, "\n")
	for _, n := range []string{"9007199254740993", "9007199254740994", "-9007199254740993", "-9007199254740992"} {
		if !strings.Contains(text, n) {
			t.Errorf("TLC never received exact %s", n)
		}
	}
	bad, e := conformance.Check(context.Background(), &pa327NumberRunner{}, t.TempDir(), "Counter", nil, []string{"n"}, 1, []byte(`{"traces":[[{"n":1.25},{"n":2}]]}`), nil)
	if e == nil && (bad.Passed || bad.Message == "") {
		t.Fatal("fraction silently coerced or missing diagnostic")
	}
}
