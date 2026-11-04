package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

type s559ApplicationInit struct {
	mu    sync.Mutex
	fail  bool
	calls int
	seen  map[int]bool
}

var s559ApplicationN = regexp.MustCompile(`\[n \|-> ([0-9]+)\]`)

func (f *s559ApplicationInit) Check(_ context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if module != "Invariant_ConformanceInit" || cfg.Specification != "Invariant_ISpec" || len(cfg.Invariants) != 1 || cfg.Invariants[0] != "Init" {
		return tlc.Result{}, fmt.Errorf("unexpected obligation %s %+v", module, cfg)
	}
	raw, e := os.ReadFile(filepath.Join(dir, module+".tla"))
	if e != nil {
		return tlc.Result{}, e
	}
	rows := s559ApplicationN.FindAllStringSubmatch(string(raw), -1)
	if len(rows) == 0 || len(rows) > 1000 {
		return tlc.Result{}, fmt.Errorf("Init batch has %d states; want 1..1000", len(rows))
	}
	f.calls++
	if f.fail && f.calls > 2 {
		return tlc.Result{}, fmt.Errorf("a later actual invocation ran after failure")
	}
	bad := ""
	for _, row := range rows {
		n, e := strconv.Atoi(row[1])
		if e != nil {
			return tlc.Result{}, e
		}
		if f.seen[n] {
			return tlc.Result{}, fmt.Errorf("state %d checked twice", n)
		}
		f.seen[n] = true
		if f.fail && n > 11004 && bad == "" {
			bad = row[1]
		}
	}
	if bad != "" {
		return tlc.Result{Outcome: tlc.Violated, Trace: []tlc.State{{Vars: []tlc.Var{{Name: "n", Value: bad}}}}}, nil
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}

func TestStreamingInitApplicationOrder(t *testing.T) {
	old := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(old)
	var b strings.Builder
	b.WriteString(`{"traces":[`)
	for i := 0; i < 3505; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `[{"n":%d}]`, 10000+i)
	}
	b.WriteString("]}")
	raw := []byte(b.String())
	for _, fail := range []bool{false, true} {
		first := ""
		for repeat := 0; repeat < 3; repeat++ {
			dir := t.TempDir()
			limit := 13504
			if fail {
				limit = 11004
			}
			if e := os.WriteFile(filepath.Join(dir, "Counter.tla"), []byte(fmt.Sprintf("---- MODULE Counter ----\nEXTENDS Naturals\nVARIABLE n\nvars == <<n>>\nInit == n \\in 10000..%d\nNext == n' = n+1\n====\n", limit)), 0600); e != nil {
				t.Fatal(e)
			}
			fake := &s559ApplicationInit{fail: fail, seen: map[int]bool{}}
			runner := &pythonApplicationModelRunner{root: dir, runner: fake}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			r, e := conformance.Check(ctx, runner, dir, "Counter", nil, []string{"n"}, 3505, raw, nil)
			cancel()
			if e != nil {
				t.Fatal(e)
			}
			wantCalls := 4
			if fail {
				wantCalls = 2
			}
			if fake.calls != wantCalls || len(runner.results) != wantCalls {
				t.Fatalf("unstable actual prefix: %d calls, %d records, want %d", fake.calls, len(runner.results), wantCalls)
			}
			if r.Runs != 3505 || r.States != 3505 || r.Steps != 0 || r.Transitions != 0 || r.ModelStates != 3505 || r.Passed == fail {
				t.Fatalf("Init result: %+v", r)
			}
			if fail {
				if r.BadStart != "n = 11005" || r.Message != "a run starts in a state Init doesn't allow" {
					t.Fatalf("wrong earliest Init failure: %+v", r)
				}
			} else {
				if len(fake.seen) != 3505 {
					t.Fatalf("checked %d starts, want every one", len(fake.seen))
				}
				for n := 10000; n < 13505; n++ {
					if !fake.seen[n] {
						t.Fatalf("missing start %d", n)
					}
				}
			}
			fp := pythonApplicationFingerprint(runner.results)
			if repeat > 0 && fp != first {
				t.Fatal("Init scheduling changed the portable evidence fingerprint")
			}
			first = fp
		}
	}
}
