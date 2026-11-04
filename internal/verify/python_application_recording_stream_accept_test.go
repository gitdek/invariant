package verify

// The conversion seam is internal, shared by the gate and the explicit byte
// compatibility wrappers:
//
//   writePythonApplicationRecording(context.Context, io.Writer,
//       PythonApplicationExecutionResult) error
//
// It writes one sampled-traces or explored-attempts document incrementally.
// It owns and removes any deduplication scratch storage, never closes the
// caller's writer, preserves conversion/validation semantics, and propagates
// context, storage and writer errors. The normal gate spools its output to an
// owned file and consumes it through CheckReader; direct Check may collect
// bytes. No new execution/collector authority or public API is introduced.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Extend the existing command-boundary fake, keeping its actual local Python
// runner, authenticated collector, environment and source-binding checks.
// Observation files are not conformance transports: only a complete traces or
// attempts JSON document counts, outside the checker's private scratch area.
func app569Fake(t *testing.T, strictFailure bool) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := refusal392GateFake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(filepath.Dir(docker), "docker.py")
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	needle := "    record({'kind':'obligation','module':module,'body':body,'cfg':cfg})\n"
	extra := `    if module.startswith('Invariant_'):
        found=[]
        for base,dirs,files in os.walk(os.environ['TMPDIR']):
            dirs[:]=[d for d in dirs if not d.startswith('invariant-conformance-')]
            for name in files:
                filename=os.path.join(base,name)
                try:
                    if os.path.getsize(filename)>1024*1024: continue
                    with open(filename) as f: value=json.load(f)
                    if isinstance(value,dict) and ('traces' in value or ('states' in value and 'attempts' in value)):
                        found.append(value)
                except (OSError,ValueError,UnicodeError): pass
        record({'kind':'owned-recording','module':module,'recordings':found})
    if module=='Invariant_StrictAttempts':
        # The first declared refusal in this fixture is Submit(2) from 0.
        rows=re.findall(r'<<\[count \|-> (\d+).*?\], \[count \|-> (\d+).*?\], "(Submit|Read)", <<(\d+)>>, (TRUE|FALSE)>>',body,re.S)
        assert len(rows)==8, ('declared outcomes lost', rows)
        assert any(row[4]=='TRUE' for row in rows), 'refusals lost'
        assert '~ENABLED' in body and 'Submit(' in body and 'Read(' in body
        if os.environ.get('APP569_STRICT_FAILURE')=='1':
            i=next(i for i,row in enumerate(rows,1) if row[4]=='TRUE')
            msg(2114,'Deadlock reached.')
            msg(2217,'1: <Initial predicate>\n/\\ count = 0\n/\\ invariant_i = %d'%i)
            sys.exit(11)
`
	if strings.Count(string(raw), needle) != 1 {
		t.Fatal("existing fake's obligation boundary changed")
	}
	gate569Write(t, filename, strings.Replace(string(raw), needle, needle+extra, 1))
	t.Setenv("APP569_STRICT_FAILURE", "0")
	if strictFailure {
		t.Setenv("APP569_STRICT_FAILURE", "1")
	}
	return tc, log
}

func app569Fixture(t *testing.T, explored bool) pa342Fixture {
	t.Helper()
	source := strings.ReplaceAll(refusal392Source, "409", "9007199254740993") + "\npayload = {'big': 9007199254740993, 'items': [1, 2]}\n\ndef same():\n    increment(counts['value'])\n"
	driver := strings.ReplaceAll(refusal392Driver, "409", "9007199254740993")
	if !explored {
		driver = `from invariant_observe import module
from counter import core as app

def drive():
    app.counts['value'] = 0
    with module(app.increment, fields={'count':'counts.value', 'payload':'payload'}) as life:
        life.step(app.submit, 1)
        life.step(app.same)
if __name__ == '__main__':
    drive()
`
	} else {
		driver = strings.ReplaceAll(driver, "fields={'count':'counts.value'}", "fields={'count':'counts.value', 'payload':'payload'}")
	}
	model := strings.ReplaceAll(refusal392Model, "VARIABLE count", "VARIABLES count, payload")
	model = strings.ReplaceAll(model, "vars == <<count>>", "vars == <<count,payload>>")
	model = strings.ReplaceAll(model, "Init == count = 0", "Init == count = 0 /\\ payload = [big |-> 9007199254740993, items |-> <<1, 2>>]")
	model = strings.ReplaceAll(model, "count' = k\n", "count' = k /\\ UNCHANGED payload\n")
	model = strings.ReplaceAll(model, "UNCHANGED count", "UNCHANGED <<count,payload>>")
	f := refusal392Fixture(t, true, source, driver, model)
	f.p.Manifest.Exhaustive = explored
	// No named bound in this driver: the larger-run rule remains inapplicable.
	pa342Save(t, f)
	return f
}

func app569Wire(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPythonApplicationRecordingTransport(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, explored := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			if !explored && fail {
				continue
			}
			t.Run(fmt.Sprintf("explored=%v/strict-failure=%v", explored, fail), func(t *testing.T) {
				tc, log := app569Fake(t, fail)
				f := app569Fixture(t, explored)
				before := pa342Tree(t, f.root)
				tmp := t.TempDir()
				t.Setenv("TMPDIR", tmp)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				r, err := Run(ctx, f.p.Dir, "", tc, Options{TLCParallel: 1})
				if err != nil || r == nil {
					t.Fatalf("application gate: %+v %v", r, err)
				}
				x, c := pa342Explored(t, r)
				if r.Passed == fail || !x.Success || !x.Complete || c.Result.Passed == fail || c.Result.States != 2 || c.Result.Transitions != 1 {
					t.Fatalf("application verdict/counts: report=%v execution=%+v conformance=%+v", r.Passed, x, c)
				}
				if c.Key != x.Key || !reflect.DeepEqual(c.Invocation, x.Declared) || len(x.Provenance.Files) == 0 || x.Provenance.Fingerprint == "" || x.Fingerprint == "" || x.Provenance.RunnerSHA256 == "" || x.Provenance.ObserverSHA256 == "" {
					t.Fatal("invocation, hashes or provenance lost")
				}
				row := pa342Row(t, r)
				if row.EveryStep != (explored && !fail) || row.ExhaustiveStates != (explored && !fail) {
					t.Fatalf("conformance qualifications changed: %+v", row)
				}
				if explored {
					if x.Exploration == nil || x.Exploration.Attempts != 10 || x.Exploration.States != 2 || c.Result.Steps != 10 || c.Result.Runs != x.Exploration.Runs {
						t.Fatalf("exploration counts: %+v %+v", x.Exploration, c)
					}
					replay, refused := 0, 0
					for _, row := range x.Observations {
						if row.Replay != nil && *row.Replay {
							replay++
						}
						if row.Outcome == "refused" {
							refused++
							if row.Refusal == nil {
								t.Fatal("raw refusal lost")
							}
						}
					}
					if replay == 0 || refused == 0 {
						t.Fatal("raw replay or refusal observations discarded")
					}
					if !fail && (c.Result.Tried == nil || !c.Result.Tried.Passed || c.Result.Tried.Attempts != 10) {
						t.Fatalf("tried: %+v", c.Result.Tried)
					}
					if fail && (c.Result.BadStep == nil || !strings.Contains(c.Result.Message, "declared refused attempt Submit(2)")) {
						t.Fatalf("strict refusal diagnostic: %+v", c.Result)
					}
				} else if c.Result.Runs != 1 || c.Result.Steps != 2 || c.Result.Exhaustive || c.Result.Tried != nil || len(x.Observations) != 3 {
					t.Fatalf("sampled counts/claim: %+v", c.Result)
				}
				rawBefore := app569Wire(t, x.Observations)
				for _, row := range x.Observations {
					state := row.State
					if row.Kind == "transition" {
						state = row.After
					}
					if state == nil {
						continue
					}
					payload, ok := state["payload"].(map[string]any)
					if !ok {
						t.Fatal("raw module object lost")
					}
					if payload["big"] != json.Number("9007199254740993") {
						t.Fatalf("integer rounded: %v", payload)
					}
					if _, ok := payload["items"].([]any); !ok {
						t.Fatal("raw module list replaced with model encoding")
					}
				}
				var encoded bytes.Buffer
				if err := writePythonApplicationRecording(ctx, &encoded, *x); err != nil {
					t.Fatal(err)
				}
				var compat []byte
				if explored {
					compat, err = pythonApplicationAttempts(*x)
				} else {
					compat, err = pythonApplicationTraces(*x)
				}
				if err != nil || !reflect.DeepEqual(app569JSON(t, encoded.Bytes()), app569JSON(t, compat)) {
					t.Fatalf("stream/byte conversion mismatch: %v", err)
				}
				if !reflect.DeepEqual(rawBefore, app569Wire(t, x.Observations)) {
					t.Fatal("conversion changed retained observations")
				}
				data, _ := json.Marshal(r)
				var back Report
				if err := json.Unmarshal(data, &back); err != nil || Fingerprint(back) != r.Fingerprint {
					t.Fatalf("receipt JSON fingerprint: %v", err)
				}
				if !reflect.DeepEqual(app569Wire(t, r.Application), app569Wire(t, back.Application)) {
					t.Fatal("receipt round trip lost raw application evidence")
				}
				logs, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				owned := 0
				for _, line := range bytes.Split(logs, []byte{'\n'}) {
					var row struct {
						Kind       string            `json:"kind"`
						Recordings []json.RawMessage `json:"recordings"`
					}
					if json.Unmarshal(line, &row) == nil && row.Kind == "owned-recording" {
						match := false
						for _, raw := range row.Recordings {
							if reflect.DeepEqual(app569JSON(t, raw), app569JSON(t, compat)) {
								match = true
							}
						}
						if !match {
							t.Error("application conformance had no live owned transport matching its authenticated observations")
						}
						owned++
					}
				}
				if owned == 0 {
					t.Fatal("application never reached conformance")
				}
				if !strings.Contains(string(logs), "9007199254740993") || !strings.Contains(string(logs), "items |-> <<1, 2>>") {
					t.Fatal("model conversion changed exact integers or module arrays")
				}
				gate569Clean(t, tmp)
				// Explicit compatibility clients still receive bytes; a semantic
				// conformance failure must not erase successfully collected rows.
				b, e, err := (Python{Image: tc.PythonImage}).Check(ctx, f.p)
				if err != nil || !b.Passed || len(e.Traces) == 0 {
					t.Fatalf("direct application Check lost bytes: %+v %v", b, err)
				}
				if !reflect.DeepEqual(before, pa342Tree(t, f.root)) {
					t.Fatal("gate changed application inputs")
				}
				gate569Clean(t, tmp)
			})
		}
	}
}

func app569JSON(t *testing.T, b []byte) any {
	t.Helper()
	var out any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// These large observations deliberately share immutable input values. They
// represent already retained receipt evidence; the measurement starts after
// creating them and subtracts their live heap. A second converted tree would
// replicate the 1,024-element module list per state (at least 512 MiB at the
// smaller size), and a whole encoded document exceeds 256 MiB. Both must fit
// the same 96 MiB additional conversion ceiling. This is not total gate heap.
// This synthetic converter stress exceeds the collector's per-execution event
// limit; the end-to-end application cases above use authenticated executions.
func app569Observations(n int, explored bool) PythonApplicationExecutionResult {
	pad := make([]any, 1024)
	for i := range pad {
		pad[i] = "ppppp"
	}
	states := make([]map[string]any, n)
	for i := range states {
		states[i] = map[string]any{"n": json.Number(strconv.Itoa(i)), "payload": map[string]any{"big": json.Number("9007199254740993"), "items": pad}}
	}
	x := PythonApplicationExecutionResult{Success: true, Complete: true, ObservationMode: "sampled"}
	x.Observations = append(x.Observations, PythonApplicationObservation{Form: "module", Kind: "initial", Run: 1, State: states[0]})
	no := false
	for i := 1; i < n; i++ {
		x.Observations = append(x.Observations, PythonApplicationObservation{Form: "module", Kind: "transition", Run: 1, Before: states[i-1], After: states[i], Step: "Inc", Replay: &no})
	}
	if explored {
		x.ObservationMode = "explored"
		x.Exploration = &PythonApplicationExploration{States: n, Attempts: n - 1, Steps: []PythonApplicationExplorationStep{{Name: "Inc", Args: [][]any{{}}}}}
	}
	return x
}

type app569FailWriter struct{ err error }

func (w app569FailWriter) Write([]byte) (int, error) { return 0, w.err }

type app569CancelWriter struct {
	cancel context.CancelFunc
	bytes  int
}

func (w *app569CancelWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	if w.bytes > 16384 {
		w.cancel()
	}
	return len(p), nil
}

func TestPythonApplicationConversionPeakHeap(t *testing.T) {
	if os.Getenv("APP569_MEASURE") == "1" {
		n, _ := strconv.Atoi(os.Getenv("APP569_N"))
		explored := os.Getenv("APP569_EXPLORED") == "true"
		runtime.GOMAXPROCS(1)
		debug.SetGCPercent(100)
		debug.SetMemoryLimit(1<<63 - 1)
		x := app569Observations(n, explored)
		output := filepath.Join(t.TempDir(), "converted.json")
		f, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		runtime.GC()
		var baseline runtime.MemStats
		runtime.ReadMemStats(&baseline)
		var peak atomic.Uint64
		peak.Store(baseline.HeapAlloc)
		done, joined := make(chan struct{}), make(chan struct{})
		sample := func() {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			for old := peak.Load(); m.HeapAlloc > old; old = peak.Load() {
				if peak.CompareAndSwap(old, m.HeapAlloc) {
					break
				}
			}
		}
		go func() {
			defer close(joined)
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					sample()
				}
			}
		}()
		err = writePythonApplicationRecording(context.Background(), f, x)
		sample()
		close(done)
		<-joined
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("conversion: %v %v", err, closeErr)
		}
		runtime.KeepAlive(x)
		if delta := peak.Load() - baseline.HeapAlloc; delta > 96<<20 {
			t.Errorf("n=%d explored=%v additional peak=%d MiB exceeds 96 MiB", n, explored, delta>>20)
		}
		t.Logf("n=%d explored=%v retained baseline=%d bytes additional peak=%d bytes", n, explored, baseline.HeapAlloc, peak.Load()-baseline.HeapAlloc)
		gate569Clean(t, tmp)
		// Decode one state/attempt at a time after measurement. Counting bytes
		// alone could let a truncated stream masquerade as bounded conversion.
		app569Inspect(t, output, n, explored)
		return
	}
	for _, explored := range []bool{false, true} {
		for _, n := range []int{32768, 65536} {
			t.Run(fmt.Sprintf("explored=%v/%d", explored, n), func(t *testing.T) {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, exe, "-test.run=^TestPythonApplicationConversionPeakHeap$", "-test.v", "-test.count=1", "-test.timeout=3m30s")
				cmd.Env = append(os.Environ(), "APP569_MEASURE=1", "APP569_N="+strconv.Itoa(n), "APP569_EXPLORED="+strconv.FormatBool(explored), "GOMAXPROCS=1", "GOGC=100", "GOMEMLIMIT=off")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("isolated conversion: %v\n%s", err, out)
				}
				t.Logf("%s", out)
			})
		}
	}
	t.Run("errors and validation", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		for _, explored := range []bool{false, true} {
			x := app569Observations(8, explored)
			sentinel := errors.New("fixture writer failed")
			if err := writePythonApplicationRecording(context.Background(), app569FailWriter{sentinel}, x); !errors.Is(err, sentinel) {
				t.Fatalf("lost writer failure: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			w := &app569CancelWriter{cancel: cancel}
			err := writePythonApplicationRecording(ctx, w, x)
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost mid-conversion cancellation: %v", err)
			}
			for _, bad := range []string{"incomplete", "unsuccessful", "no observations", "wrong mode", "invalid run", "invalid number"} {
				y := app569Observations(2, explored)
				switch bad {
				case "incomplete":
					y.Complete = false
				case "unsuccessful":
					y.Success = false
				case "no observations":
					y.Observations = nil
				case "wrong mode":
					y.ObservationMode = "foreign"
				case "invalid run":
					if explored {
						continue
					}
					y.Observations[0].Run = 2
				case "invalid number":
					y.Observations[0].State["n"] = json.Number("1.5")
				}
				if err := writePythonApplicationRecording(context.Background(), io.Discard, y); err == nil {
					t.Fatalf("accepted %s (explored=%v)", bad, explored)
				}
			}
			gate569Clean(t, tmp)
		}
	})
	t.Run("interleaved sampled lifecycles", func(t *testing.T) {
		state := func(n string) map[string]any { return map[string]any{"n": json.Number(n)} }
		x := PythonApplicationExecutionResult{Success: true, Complete: true, ObservationMode: "sampled", Observations: []PythonApplicationObservation{
			{Form: "module", Kind: "initial", Run: 1, State: state("0")},
			{Form: "module", Kind: "initial", Run: 2, State: state("10")},
			{Form: "module", Kind: "transition", Run: 1, Before: state("0"), After: state("1")},
			{Form: "module", Kind: "transition", Run: 2, Before: state("10"), After: state("11")},
		}}
		var output bytes.Buffer
		if err := writePythonApplicationRecording(context.Background(), &output, x); err != nil {
			t.Fatal(err)
		}
		want := `{"traces":[[{"n":0},{"n":1}],[{"n":10},{"n":11}]]}`
		if !reflect.DeepEqual(app569JSON(t, output.Bytes()), app569JSON(t, []byte(want))) {
			t.Fatalf("sampled run ordering changed: %s", output.Bytes())
		}
	})
}

func app569Inspect(t *testing.T, path string, n int, explored bool) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	token := func(want json.Token) {
		t.Helper()
		got, e := d.Token()
		if e != nil || got != want {
			t.Fatalf("token %v %v, want %v", got, e, want)
		}
	}
	stateIndex := 0
	state := func() {
		var v map[string]any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		if v["n"] != json.Number(strconv.Itoa(stateIndex)) {
			t.Fatalf("state %d: %v", stateIndex, v["n"])
		}
		p := v["payload"].(map[string]any)
		if p["big"] != json.Number("9007199254740993") {
			t.Fatal("integer rounded")
		}
		if len(p["items"].(map[string]any)["$seq"].([]any)) != 1024 {
			t.Fatal("module sequence lost")
		}
		stateIndex++
	}
	token(json.Delim('{'))
	seen := map[string]bool{}
	attempts := 0
	for d.More() {
		key, e := d.Token()
		if e != nil {
			t.Fatal(e)
		}
		name := key.(string)
		if seen[name] {
			t.Fatal("duplicate field")
		}
		seen[name] = true
		switch name {
		case "states":
			token(json.Delim('['))
			for d.More() {
				state()
			}
			token(json.Delim(']'))
		case "traces":
			token(json.Delim('['))
			token(json.Delim('['))
			for d.More() {
				state()
			}
			token(json.Delim(']'))
			token(json.Delim(']'))
		case "init":
			var ids []int
			if err := d.Decode(&ids); err != nil || !reflect.DeepEqual(ids, []int{0}) {
				t.Fatalf("initial IDs: %v %v", ids, err)
			}
		case "attempts":
			token(json.Delim('['))
			for d.More() {
				var a []any
				if err := d.Decode(&a); err != nil {
					t.Fatal(err)
				}
				if len(a) != 4 || a[0] != json.Number(strconv.Itoa(attempts)) || a[1] != "Inc" || len(a[2].([]any)) != 0 || a[3] != json.Number(strconv.Itoa(attempts+1)) {
					t.Fatalf("attempt %d: %v", attempts, a)
				}
				attempts++
			}
			token(json.Delim(']'))
		default:
			t.Fatalf("unexpected recording field %s", name)
		}
	}
	token(json.Delim('}'))
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("trailing output: %v", err)
	}
	if stateIndex != n || explored && (attempts != n-1 || !seen["states"] || !seen["init"]) || !explored && !seen["traces"] {
		t.Fatalf("truncated recording states=%d attempts=%d keys=%v", stateIndex, attempts, seen)
	}
}
