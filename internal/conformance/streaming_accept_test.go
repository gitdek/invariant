package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

type s559Runner func(context.Context, string, string, tlc.Config) (tlc.Result, error)

func (f s559Runner) Check(c context.Context, d, m string, cfg tlc.Config) (tlc.Result, error) {
	return f(c, d, m, cfg)
}

type s559Short struct{ r io.Reader }

func (r s559Short) Read(p []byte) (int, error) { return r.r.Read(p[:min(7, len(p))]) }
func s559Pass(context.Context, string, string, tlc.Config) (tlc.Result, error) {
	return tlc.Result{Outcome: tlc.Passed}, nil
}
func s559NoTools(context.Context, string, string, tlc.Config) (tlc.Result, error) {
	return tlc.Result{}, errors.New("invalid recording reached TLC")
}

var s559Rows = regexp.MustCompile(`<<\[n \|-> (-?[0-9]+)\], \[n \|-> (-?[0-9]+)\], "([A-Za-z]+)", <<([^<>]*)>>, (TRUE|FALSE)>>`)

// This small Counter stand-in computes verdicts from the actual obligations:
// Init permits n>=0, Next increments by one; Read(k) is enabled exactly when
// n=k and leaves n unchanged. It never accepts a named action on Next alone.
func s559Semantic(_ context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	raw, e := os.ReadFile(filepath.Join(dir, module+".tla"))
	if e != nil {
		return tlc.Result{}, e
	}
	text := string(raw)
	switch cfg.Specification {
	case "Invariant_ISpec":
		if !reflect.DeepEqual(cfg.Invariants, []string{"Init"}) {
			return tlc.Result{}, errors.New("missing Init invariant")
		}
		for _, n := range acceptValues(text) {
			if n < 0 {
				return tlc.Result{Outcome: tlc.Violated, Trace: acceptState("n", strconv.Itoa(n))}, nil
			}
		}
	case "Invariant_CSpec":
		if !strings.Contains(text, "/\\ Next") || !strings.Contains(text, "n' = (Invariant_States[invariant_target]).n") {
			return tlc.Result{}, errors.New("missing Next obligation")
		}
		run := acceptRun{}
		r, _ := acceptVerdict("steps", text, &run)
		return r, nil
	case "Invariant_ASpec":
		for _, want := range []string{"~ENABLED (Invariant_Action", "ELSE /\\ Invariant_Action", "invariant_done' = TRUE", "n' = (Invariant_Attempts[invariant_i][2]).n"} {
			if !strings.Contains(text, want) {
				return tlc.Result{}, fmt.Errorf("missing strict obligation %q", want)
			}
		}
		rows := s559Rows.FindAllStringSubmatch(text, -1)
		if len(rows) == 0 || len(rows) > 2000 {
			return tlc.Result{}, errors.New("invalid strict window")
		}
		for i, row := range rows {
			from, _ := strconv.Atoi(row[1])
			to, _ := strconv.Atoi(row[2])
			k, _ := strconv.Atoi(row[4])
			enabled, accepted := false, false
			switch row[3] {
			case "Inc":
				enabled, accepted = true, to == from+1
			case "Read":
				enabled, accepted = from == k, from == k && to == from
			default:
				return tlc.Result{}, fmt.Errorf("unknown action %s", row[3])
			}
			if !strings.Contains(text, `"`+row[3]+`" -> `+row[3]) {
				return tlc.Result{}, errors.New("named action not dispatched")
			}
			ok := accepted
			if row[5] == "TRUE" {
				ok = from == to && !enabled
			}
			if !ok {
				return tlc.Result{Outcome: tlc.Deadlock, Trace: acceptState("invariant_i", strconv.Itoa(i+1), "n", row[1])}, nil
			}
		}
	case "Invariant_TSpec":
		// Read's finite argument domain is 0..1; Inc has no arguments.
		ids := acceptValues(acceptTuple(text, "Invariant_States"))
		sets := acceptSet.FindAllString(acceptTuple(text, "Invariant_Tried"), -1)
		if len(ids) != len(sets) || len(ids) == 0 || len(ids) > 1000 {
			return tlc.Result{}, errors.New("invalid tried window")
		}
		needs := []string{`<<"Inc">>`}
		if strings.Contains(text, "Invariant_Larger!Read(") {
			needs = []string{`<<"Read", 0>>`, `<<"Read", 1>>`}
		}
		for i, set := range sets {
			for _, need := range needs {
				if !strings.Contains(set, need) {
					return tlc.Result{Outcome: tlc.Violated, Invariant: "Invariant_EveryStepTried", Trace: acceptState("invariant_i", strconv.Itoa(i+1), "n", strconv.Itoa(ids[i])), Printed: []string{`<<"invariant-untried", {` + need + `}>>`}}, nil
				}
			}
		}
	default:
		return tlc.Result{}, fmt.Errorf("unknown spec %s", cfg.Specification)
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}

func s559ReadModel() *Model {
	return &Model{Steps: []tla.Step{{Name: "Read", Args: []string{"k"}, Binders: []tla.Binder{{Var: "k", Domain: "0..1"}}}}}
}
func s559IncModel() *Model { return &Model{Steps: []tla.Step{{Name: "Inc"}}} }

func TestStreamingConformanceCompatibility(t *testing.T) {
	cases := []struct {
		name, raw string
		model     *Model
		want      Result
	}{
		{"passing and stuttering", `{"traces":[[{"n":0},{"n":1},{"n":1}],[],[{"n":0},{"n":1}]]}`, nil, Result{Passed: true, Runs: 3, Steps: 3, States: 2, Transitions: 1}},
		{"bad start", `{"traces":[[{"n":-1},{"n":0}]]}`, nil, Result{Runs: 1, Steps: 1, States: 2, Transitions: 1, BadStart: "n = -1", Message: "a run starts in a state Init doesn't allow"}},
		{"bad step", `{"traces":[[{"n":4},{"n":2}]]}`, nil, Result{Runs: 1, Steps: 1, States: 2, Transitions: 1, BadStep: &Step{From: "n = 4", To: "n = 2"}, Message: "the code took a step the model doesn't allow"}},
		{"untried", `{"attempts":[[0,"Inc",[],1]],"init":[0],"states":[{"n":0},{"n":1}]}`, s559IncModel(), Result{Passed: true, Steps: 1, States: 2, Transitions: 1, Tried: &Tried{Attempts: 1, States: 2, In: "n = 1", Untried: `{<<"Inc">>}`, Message: "the driver never tried a step that more than a bound rules out"}}},
		{"legacy named stutter", `{"states":[{"n":1}],"init":[0],"attempts":[[0,"Read",[0],0],[0,"Read",[1],0]]}`, s559ReadModel(), Result{Passed: true, Steps: 2, States: 1, Tried: &Tried{Passed: true, Attempts: 2, States: 1}}},
		{"strict same-pair actions and outcomes", `{"states":[{"n":1}],"init":[0],"attempts":[[0,"Read",[1],0,{"outcome":"accepted"}],[0,"Read",[0],0,{"outcome":"refused","refusal":{"type":"x.Error","attributes":{"n":null,"large":90071992547409931234567890,"fraction":1.25}}}]]}`, s559ReadModel(), Result{Passed: true, Steps: 2, States: 1, Tried: &Tried{Passed: true, Attempts: 2, States: 1}}},
		{"duplicate original state IDs", `{"states":[{"n":1},{"n":0},{"n":1}],"init":[1],"attempts":[[1,"Inc",[],2],[0,"Inc",[],0]]}`, s559IncModel(), Result{Passed: true, Steps: 2, States: 2, Transitions: 1, Tried: &Tried{Passed: true, Attempts: 2, States: 2}}},
		{"wrong named action despite Next", `{"states":[{"n":0},{"n":1}],"init":[0],"attempts":[[0,"Inc",[],1,{"outcome":"accepted"}],[0,"Read",[1],1,{"outcome":"accepted"}]]}`, &Model{Steps: []tla.Step{{Name: "Inc"}, {Name: "Read", Args: []string{"k"}}}}, Result{Steps: 2, States: 2, Transitions: 1, BadStep: &Step{From: "n = 0", To: "n = 1"}, Message: "declared accepted attempt Read(1) from n = 0 to n = 1 disagrees with the named model action"}},
		{"wrong argument on unchanged acceptance", `{"states":[{"n":1}],"init":[0],"attempts":[[0,"Read",[1],0,{"outcome":"accepted"}],[0,"Read",[0],0,{"outcome":"accepted"}]]}`, s559ReadModel(), Result{Steps: 2, States: 1, BadStep: &Step{From: "n = 1", To: "n = 1"}, Message: "declared accepted attempt Read(0) from n = 1 to n = 1 disagrees with the named model action"}},
		{"enabled read-only refusal", `{"states":[{"n":1}],"init":[0],"attempts":[[0,"Read",[1],0,{"outcome":"accepted"}],[0,"Read",[1],0,{"outcome":"refused","refusal":{"type":"x.Error","attributes":{}}}]]}`, s559ReadModel(), Result{Steps: 2, States: 1, BadStep: &Step{From: "n = 1", To: "n = 1"}, Message: "declared refused attempt Read(1) from n = 1 to n = 1 disagrees with the named model action"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.want.ModelStates = 17
			for _, stream := range []bool{true, false} {
				dir := acceptDir(t)
				var got Result
				var err error
				if stream {
					got, err = CheckReader(context.Background(), s559Runner(s559Semantic), dir, "Counter", nil, []string{"n"}, 17, s559Short{strings.NewReader(tc.raw)}, tc.model)
				} else {
					got, err = Check(context.Background(), s559Runner(s559Semantic), dir, "Counter", nil, []string{"n"}, 17, []byte(tc.raw), tc.model)
				}
				if err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("stream=%v: %+v tried=%+v, %v; want %+v tried=%+v", stream, got, got.Tried, err, tc.want, tc.want.Tried)
				}
			}
		})
	}
	for _, tc := range []struct{ a, b, want string }{
		{`{"$set":["b","a","a"]}`, `{"$set":["a","b"]}`, `{"a", "b"}`},
		{`{"$fn":[[2,"b"],[1,"a"]]}`, `{"$fn":[[1,"a"],[2,"b"]]}`, `(1 :> "a" @@ 2 :> "b")`},
		{`{"z":9007199254740993,"a":{"$seq":[-9007199254740993,true]}}`, `{"a":{"$seq":[-9007199254740993,true]},"z":9007199254740993}`, `[a |-> <<-9007199254740993, TRUE>>, z |-> 9007199254740993]`},
		{`{"z":1,"z":2}`, `{"z":2}`, `[z |-> 2]`},
	} {
		t.Run("canonical/"+tc.a, func(t *testing.T) {
			want := tc.want
			runner := s559Runner(func(_ context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
				raw, e := os.ReadFile(filepath.Join(dir, module+".tla"))
				if e != nil {
					return tlc.Result{}, e
				}
				if cfg.Specification != "Invariant_ISpec" || !strings.Contains(string(raw), "[v |-> "+want+"]") {
					return tlc.Result{}, errors.New("canonical equality or exact value lost")
				}
				return tlc.Result{Outcome: tlc.Passed}, nil
			})
			raw := `{"traces":[[{"v":` + tc.a + `},{"v":` + tc.b + `}]]}`
			r, e := CheckReader(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"v"}, 1, s559Short{strings.NewReader(raw)}, nil)
			if e != nil || !reflect.DeepEqual(r, Result{Passed: true, Runs: 1, Steps: 1, States: 1, ModelStates: 1}) {
				t.Fatalf("canonical stream: %+v %v", r, e)
			}
		})
	}
}

func TestStreamingConformanceMalformedInput(t *testing.T) {
	// These strings pin the base's refusal text and precedence, including a
	// later JSON/metadata error taking precedence over an earlier bad state.
	for _, tc := range []struct{ name, raw, want string }{
		{"empty", "", "the driver's output isn't valid JSON: EOF"},
		{"truncated", `{"traces":[`, "the driver's output isn't valid JSON: unexpected EOF"},
		{"state wire type", `{"states":[3],"init":[0],"attempts":[]}`, "the driver's output isn't valid JSON: json: cannot unmarshal number into .states.0 of type map[string]interface {}"},
		{"init wire type", `{"states":[{"n":0}],"init":[0.5],"attempts":[]}`, "the driver's output isn't valid JSON: json: cannot unmarshal number 0.5 into .init.0 of type int"},
		{"trace wire type", `{"traces":[1]}`, "the driver's output isn't valid JSON: json: cannot unmarshal number into .traces.0 of type []map[string]interface {}"},
		{"overwritten wire error", `{"init":["x"],"init":[0],"states":[{"n":0}],"attempts":[]}`, "the driver's output isn't valid JSON: json: cannot unmarshal string into .init.0 of type int"},
		{"syntax before metadata validation", `{"states":[{"n":0}],"attempts":[[0,"Read",[],0,{"outcome":"accepted","outcome":"accepted"}]],"unknown":x}`, "the driver's output isn't valid JSON: invalid character 'x' looking for beginning of value"},
		{"metadata before trailing data", `{"states":[{"n":0}],"attempts":[[0,"Read",[],0,{"outcome":"accepted","outcome":"accepted"}]]} {}`, `the driver's output isn't valid JSON: declared outcome metadata repeats field "outcome"`},
		{"trailing", `{"traces":[[{"n":null}]]} {}`, "the driver's output isn't valid JSON: trailing data"},
		{"no runs", `{}`, "the driver recorded no runs"},
		{"no start", `{"states":[{"n":0}],"init":[],"attempts":[]}`, "the driver recorded no state to start in"},
		{"missing start", `{"states":[{"n":0}],"init":[2],"attempts":[]}`, "the driver starts in state 2, which it didn't record"},
		{"bad state first", `{"attempts":[[9,"Read",[],9]],"init":[8],"states":[{"n":null}]}`, "a recorded state can't be read: n: can't encode <nil>"},
		{"fraction", `{"traces":[[{"n":1.25}]]}`, "a recorded state can't be read: n: 1.25 isn't an integer; TLA+ has no fractions"},
		{"out of range", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[],3]]}`, "an attempt is [0 Read [] 3]; want [from, step, [args], to], with states the driver recorded"},
		{"fractional reference", `{"states":[{"n":0}],"init":[0],"attempts":[[0.0,"Read",[],0]]}`, "an attempt is [0.0 Read [] 0]; want [from, step, [args], to], with states the driver recorded"},
		{"arity of row", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",1]]}`, "an attempt is [0 Read 1]; want [from, step, [args], to] with optional declared outcome metadata"},
		{"bad argument", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[null],0]]}`, "an argument of Read can't be read: can't encode <nil>"},
		{"null metadata", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[0],0,null]]}`, "attempt Read(0) in n = 0: declared outcome metadata must be an object"},
		{"changed refusal", `{"states":[{"n":0},{"n":1}],"init":[0],"attempts":[[0,"Read",[0],1,{"outcome":"refused","refusal":{"type":"x.Error","attributes":{}}}]]}`, "refused attempt Read(0) changed state from n = 0 to n = 1"},
		{"unknown action", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Other",[],0,{"outcome":"accepted"}]]}`, "declared attempt Other in n = 0 names no step of the model's Next"},
		{"wrong arity", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[],0,{"outcome":"accepted"}]]}`, "declared attempt Read in n = 0 has 0 arguments; Next must name the step with a consistent arity of 1"},
		{"duplicate outcome before bad state", `{"states":[{"n":null}],"init":[9],"attempts":[[0,"Read",[null],0,{"outcome":"accepted","outc\u006fme":"accepted"}]]}`, `the driver's output isn't valid JSON: declared outcome metadata repeats field "outcome"`},
		{"duplicate refusal", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[0],0,{"outcome":"refused","refusal":{"type":"x.One","type":"x.Two","attributes":{}}}]]}`, `the driver's output isn't valid JSON: declared outcome metadata repeats field "type"`},
		{"duplicate attributes", `{"states":[{"n":0}],"init":[0],"attempts":[[0,"Read",[0],0,{"outcome":"refused","refusal":{"type":"x.Error","attributes":{"code":1,"code":2}}}]]}`, `the driver's output isn't valid JSON: declared outcome metadata repeats field "code"`},
		{"reused map on repeated states", `{"states":[{"n":0,"extra":1}],"states":[{"n":1}],"init":[0],"attempts":[]}`, "a recorded state can't be read: want exactly the spec's variables [n], got 2 fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := CheckReader(context.Background(), s559Runner(s559NoTools), t.TempDir(), "Counter", nil, []string{"n"}, 7, s559Short{strings.NewReader(tc.raw)}, s559ReadModel())
			if e != nil || !reflect.DeepEqual(r, Result{ModelStates: 7, Message: tc.want}) {
				t.Fatalf("got %+v, %v; want exact refusal %q", r, e, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, raw           string
		states, runs, steps int
		attempts            bool
	}{
		{"absent", `{"states":[{"n":null}],"init":[99],"traces":[[{"n":0}]]}`, 1, 1, 0, false},
		{"null", `{"attempts":null,"states":[{"n":null}],"traces":[[{"n":0}]]}`, 1, 1, 0, false},
		{"empty overrides traces", `{"traces":[[{"n":null}]],"attempts":[],"init":[0],"states":[{"n":0}]}`, 1, 0, 0, true},
		{"last null", `{"attempts":[],"attempts":null,"traces":[[{"n":0}]]}`, 1, 1, 0, false},
		{"last nonnull", `{"attempts":null,"attempts":[],"states":[{"n":0}],"init":[0]}`, 1, 0, 0, true},
		{"repeated states and init", `{"states":[{"n":-1},{"n":9}],"states":[{"n":0}],"init":[9],"init":[0],"attempts":[]}`, 1, 0, 0, true},
		{"repeated traces", `{"traces":[[{"n":-1},{"n":7}]],"traces":[[{"n":0}]]}`, 1, 1, 0, false},
		{"legacy duplicates", `{"attempts":[[0,"Read",[{"code":500,"code":409}],0]],"states":[{"n":9,"n":0}],"init":[0]}`, 1, 0, 1, true},
		{"case insensitive fields", `{"ATTEMPTS":[],"STATES":[{"n":0}],"INIT":[0],"ignored":{"x":[1,2]}}`, 1, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := CheckReader(context.Background(), s559Runner(s559Pass), t.TempDir(), "Counter", nil, []string{"n"}, 7, s559Short{strings.NewReader(tc.raw)}, &Model{})
			want := Result{Passed: true, States: tc.states, Runs: tc.runs, Steps: tc.steps, ModelStates: 7}
			if tc.attempts {
				want.Tried = &Tried{Attempts: tc.steps, States: tc.states, Message: "the model names no steps, so the driver's attempts can't be checked"}
			}
			if e != nil || !reflect.DeepEqual(r, want) {
				t.Fatalf("JSON compatibility: %+v, %v; want %+v", r, e, want)
			}
		})
	}
}

func TestStreamingConformanceEarliestFailure(t *testing.T) {
	previous := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(previous)
	for _, kind := range []string{"steps", "tried"} {
		t.Run(kind, func(t *testing.T) {
			early, late := 10, 4500
			var raw []byte
			var model *Model
			if kind == "steps" {
				raw = acceptRuns(t, acceptUpTo(6000), []int{early, early - 5}, []int{late, late - 10})
			} else {
				late = 2500
				raw = acceptAttempts(t, 3000, early, late)
				model = s559IncModel()
			}
			f := newAcceptTLC("", early, late)
			r, e := CheckReader(context.Background(), f, acceptDir(t), "Counter", nil, []string{"n"}, 0, strings.NewReader(string(raw)), model)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "steps" {
				if r.Passed || r.BadStep == nil || *r.BadStep != (Step{From: "n = 10", To: "n = 5"}) {
					t.Fatalf("wrong failure: %+v %+v", r, r.BadStep)
				}
			} else {
				if !r.Passed || r.Tried == nil || r.Tried.Passed || r.Tried.In != "n = 10" || r.Tried.Untried != `{<<"Inc">>}` {
					t.Fatalf("wrong tried failure: %+v %+v", r, r.Tried)
				}
			}
			if !f.finishedBefore(late, early) {
				t.Fatal("fixture never completed the later failing batch first")
			}
		})
	}
	t.Run("strict original order", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(`{"states":[{"n":0}],"init":[0],"attempts":[`)
		for i := 0; i < 6001; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			k := 0
			if i == 10 {
				k = 7
			}
			if i == 4500 {
				k = 9
			}
			fmt.Fprintf(&b, `[0,"Read",[%d],0,{"outcome":"accepted"}]`, k)
		}
		b.WriteString("]}")
		later := make(chan struct{})
		var once sync.Once
		runner := s559Runner(func(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
			if module == "Invariant_Tried" {
				return tlc.Result{}, errors.New("strict failure earned tried coverage")
			}
			if module == "Invariant_StrictAttempts" {
				raw, e := os.ReadFile(filepath.Join(dir, module+".tla"))
				if e != nil {
					return tlc.Result{}, e
				}
				if strings.Contains(string(raw), "<<9>>, FALSE>>") {
					defer once.Do(func() { close(later) })
				}
				if strings.Contains(string(raw), "<<7>>, FALSE>>") {
					select {
					case <-later:
					case <-ctx.Done():
						return tlc.Result{}, ctx.Err()
					case <-time.After(5 * time.Second):
						return tlc.Result{}, errors.New("later strict batch did not run")
					}
				}
			}
			return s559Semantic(ctx, dir, module, cfg)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, e := CheckReader(ctx, runner, acceptDir(t), "Counter", nil, []string{"n"}, 1, strings.NewReader(b.String()), s559ReadModel())
		want := "declared accepted attempt Read(7) from n = 0 to n = 0 disagrees with the named model action"
		if e != nil || r.Passed || r.Tried != nil || r.Message != want || r.BadStep == nil || *r.BadStep != (Step{From: "n = 0", To: "n = 0"}) {
			t.Fatalf("strict diagnostic: %+v %v", r, e)
		}
	})
}

type s559ErrorReader struct {
	prefix *strings.Reader
	err    error
}

func (r *s559ErrorReader) Read(p []byte) (int, error) {
	n, e := r.prefix.Read(p)
	if e == io.EOF {
		return 0, r.err
	}
	return n, e
}

type s559CancelReader struct {
	r      io.Reader
	cancel context.CancelFunc
	calls  int
}

func (r *s559CancelReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 4 {
		r.cancel()
	}
	return r.r.Read(p[:min(len(p), 19)])
}

func s559NoEvidence(t *testing.T, r Result, e error) {
	t.Helper()
	if e == nil || r.Passed || r.Tried != nil {
		t.Fatalf("operational failure gained evidence: %+v tried=%+v, %v", r, r.Tried, e)
	}
}
func s559Empty(t *testing.T, dir string) {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatalf("temporary store not removed: %v, %v", entries, e)
	}
}
func s559Private(dir string) error {
	count := 0
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == dir {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("temporary store is not private: %s %v", p, info.Mode())
		}
		if info.Mode().IsRegular() {
			count++
		}
		return nil
	})
	if e == nil && count == 0 {
		return errors.New("no disk-backed private store exists while checking")
	}
	return e
}

func TestStreamingConformanceResources(t *testing.T) {
	modelDir := acceptDir(t)
	recording := filepath.Join(modelDir, "caller.json")
	s559Write(t, recording, `{"traces":[[{"n":0},{"n":1}]]}`)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	check := func(ctx context.Context, runner Runner, reader io.Reader, model *Model) (Result, error) {
		return CheckReader(ctx, runner, modelDir, "Counter", nil, []string{"n"}, 2, reader, model)
	}
	t.Run("read errors", func(t *testing.T) {
		for _, prefix := range []string{`{"traces":[`, `{"traces":[[{"n":0}]]}`} {
			sentinel := errors.New("recording read failed")
			r, e := check(context.Background(), s559Runner(s559NoTools), &s559ErrorReader{strings.NewReader(prefix), sentinel}, nil)
			s559NoEvidence(t, r, e)
			if !errors.Is(e, sentinel) {
				t.Fatalf("lost read cause: %v", e)
			}
			s559Empty(t, tmp)
		}
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r, e := check(ctx, s559Runner(s559NoTools), strings.NewReader(`{}`), nil)
		s559NoEvidence(t, r, e)
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		s559Empty(t, tmp)
	})
	t.Run("cancel decoding", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader := &s559CancelReader{r: strings.NewReader(string(acceptRuns(t, acceptUpTo(2000)))), cancel: cancel}
		r, e := check(ctx, s559Runner(s559NoTools), reader, nil)
		s559NoEvidence(t, r, e)
		if !errors.Is(e, context.Canceled) || reader.calls > 5 {
			t.Fatalf("decoding continued after cancel: reads=%d %v", reader.calls, e)
		}
		s559Empty(t, tmp)
	})
	for _, mode := range []string{"success", "semantic refusal", "tool error", "tried tool error"} {
		t.Run(mode, func(t *testing.T) {
			runner := s559Runner(func(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
				if e := s559Private(tmp); e != nil {
					return tlc.Result{}, e
				}
				if mode == "tool error" || mode == "tried tool error" && module == "Invariant_Tried" {
					return tlc.Result{}, errors.New("tool unavailable")
				}
				if mode == "semantic refusal" {
					return tlc.Result{Outcome: tlc.Violated, Trace: acceptState("n", "-1")}, nil
				}
				return s559Semantic(ctx, dir, module, cfg)
			})
			raw := string(acceptAttempts(t, 2))
			r, e := check(context.Background(), runner, strings.NewReader(raw), s559IncModel())
			if mode == "tried tool error" {
				if e == nil || r.Tried != nil && r.Tried.Passed {
					t.Fatalf("failed tool earned tried coverage: %+v %v", r, e)
				}
			} else if mode == "tool error" {
				s559NoEvidence(t, r, e)
			} else if e != nil || r.Passed != (mode == "success") {
				t.Fatalf("%s: %+v %v", mode, r, e)
			}
			s559Empty(t, tmp)
		})
	}
	t.Run("store creation failure", func(t *testing.T) {
		blocked := filepath.Join(tmp, "file")
		s559Write(t, blocked, "x")
		t.Setenv("TMPDIR", blocked)
		t.Setenv("TMP", blocked)
		t.Setenv("TEMP", blocked)
		r, e := check(context.Background(), s559Runner(s559NoTools), strings.NewReader(`{"traces":[[{"n":0}]]}`), nil)
		s559NoEvidence(t, r, e)
		if e = os.Remove(blocked); e != nil {
			t.Fatal(e)
		}
		s559Empty(t, tmp)
	})
	t.Run("store read failure", func(t *testing.T) {
		// Use more payload than the measured heap ceiling, so a valid bounded
		// cache cannot hide every subsequent disk read after the first Init.
		dir := filepath.Join(modelDir, "disk-failure")
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
		input := filepath.Join(dir, "recording.json")
		s559LargeRecording(t, input, "traces", 32768)
		s559Write(t, filepath.Join(dir, "Counter.tla"), s559LargeModel("traces", 32768))
		s559Write(t, filepath.Join(dir, "Limit.tla"), "---- MODULE Limit ----\nMax == 32767\n====\n")
		file, e := os.Open(input)
		if e != nil {
			t.Fatal(e)
		}
		defer file.Close()
		var cut atomic.Bool
		runner := s559Runner(func(context.Context, string, string, tlc.Config) (tlc.Result, error) {
			if cut.CompareAndSwap(false, true) {
				if e := s559Private(tmp); e != nil {
					return tlc.Result{}, e
				}
				e := filepath.WalkDir(tmp, func(p string, d fs.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if d.Type().IsRegular() {
						return os.Truncate(p, 0)
					}
					return nil
				})
				if e != nil {
					return tlc.Result{}, e
				}
			}
			return tlc.Result{Outcome: tlc.Passed}, nil
		})
		r, e := CheckReader(context.Background(), runner, dir, "Counter", nil, []string{"n", "pad"}, 32768, file, nil)
		s559NoEvidence(t, r, e)
		s559Empty(t, tmp)
	})
	t.Run("cancellation joins started batches", func(t *testing.T) {
		old := runtime.GOMAXPROCS(3)
		defer runtime.GOMAXPROCS(old)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered := make(chan struct{}, 3)
		release := make(chan struct{})
		var active, started atomic.Int32
		runner := s559Runner(func(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
			if module == "Invariant_ConformanceInit" {
				return s559Semantic(ctx, dir, module, cfg)
			}
			active.Add(1)
			started.Add(1)
			defer active.Add(-1)
			entered <- struct{}{}
			<-ctx.Done()
			<-release
			return tlc.Result{}, ctx.Err()
		})
		type answer struct {
			r Result
			e error
		}
		out := make(chan answer, 1)
		raw := string(acceptRuns(t, acceptUpTo(20001)))
		go func() { r, e := check(ctx, runner, strings.NewReader(raw), nil); out <- answer{r, e} }()
		for i := 0; i < 3; i++ {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				cancel()
				close(release)
				t.Fatal("bounded concurrent checks did not start")
			}
		}
		cancel()
		select {
		case <-out:
			close(release)
			t.Fatal("returned before joining started checks")
		case <-time.After(30 * time.Millisecond):
		}
		close(release)
		select {
		case a := <-out:
			s559NoEvidence(t, a.r, a.e)
			if !errors.Is(a.e, context.Canceled) {
				t.Fatal(a.e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation failed to join")
		}
		if active.Load() != 0 || started.Load() != 3 {
			t.Fatalf("continued scheduling or leaked workers: active=%d started=%d", active.Load(), started.Load())
		}
		s559Empty(t, tmp)
	})
	t.Run("independent invocation stores", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		results := make(chan error, 2)
		runner := s559Runner(func(context.Context, string, string, tlc.Config) (tlc.Result, error) {
			entered <- struct{}{}
			<-release
			return tlc.Result{Outcome: tlc.Passed}, nil
		})
		for i := 0; i < 2; i++ {
			dir := filepath.Join(modelDir, fmt.Sprintf("invocation-%d", i))
			if e := os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			s559Write(t, filepath.Join(dir, "Counter.tla"), acceptCounter)
			s559Write(t, filepath.Join(dir, "Limit.tla"), acceptLimit)
			go func() {
				r, e := CheckReader(context.Background(), runner, dir, "Counter", nil, []string{"n"}, 1, strings.NewReader(`{"traces":[[{"n":0}]]}`), nil)
				if e == nil && !r.Passed {
					e = fmt.Errorf("result %+v", r)
				}
				results <- e
			}()
		}
		for i := 0; i < 2; i++ {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("concurrent check did not reach TLC")
			}
		}
		entries, e := os.ReadDir(tmp)
		if e != nil || len(entries) < 2 {
			close(release)
			t.Fatalf("invocations shared storage: %v %v", entries, e)
		}
		private := s559Private(tmp)
		close(release)
		for i := 0; i < 2; i++ {
			if e := <-results; e != nil {
				t.Error(e)
			}
		}
		if private != nil {
			t.Error(private)
		}
		s559Empty(t, tmp)
	})
	if raw, e := os.ReadFile(recording); e != nil || string(raw) != `{"traces":[[{"n":0},{"n":1}]]}` {
		t.Fatalf("caller recording changed: %s %v", raw, e)
	}
	for name, want := range acceptModules {
		if raw, e := os.ReadFile(filepath.Join(modelDir, name+".tla")); e != nil || string(raw) != want {
			t.Fatalf("caller model changed: %s %v", name, e)
		}
	}
}
