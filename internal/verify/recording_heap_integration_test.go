//go:build integration

package verify

// #569 exercises the normal Run path, not a manually substituted Language or
// Reader. Only external commands are substituted. Fixture generation happens
// in the parent; the measured process never holds its input as a byte slice.
// At GOMAXPROCS=1/GOGC=100 and TLCParallel=1, both 256 and 512 MiB payloads
// must fit the same 192 MiB heap ceiling. This is a workload ceiling, not a
// promise about unbounded individual values, index counts, or Python receipts.
//
// The ceiling bounds the heap the gate keeps: the most live heap any
// collection marks during the run. The peak including garbage the collector
// hasn't freed yet depends on when it runs, so identical runs measured 57 and
// 212 MiB. That peak is reported, not judged (#697).
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const gate569Ceiling = 192 << 20

func gate569Memory(t *testing.T, refused bool) {
	t.Helper()
	if os.Getenv("INVARIANT_569_MEASURE") == "1" {
		n, _ := strconv.Atoi(os.Getenv("INVARIANT_569_COUNT"))
		route := os.Getenv("INVARIANT_569_ROUTE")
		bad := os.Getenv("INVARIANT_569_BAD") == "true"
		kind := "traces"
		if route == "go" {
			kind = "attempts"
		}
		if refused {
			kind = "refused"
		}
		runtime.GOMAXPROCS(1)
		debug.SetGCPercent(100)
		debug.SetMemoryLimit(1<<63 - 1)
		p, tc, events, tmp := gate569Setup(t, route, "ok", os.Getenv("INVARIANT_569_INPUT"), n, kind, bad)
		fdCount := func() int64 {
			for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
				if entries, err := os.ReadDir(dir); err == nil {
					return int64(len(entries))
				}
			}
			return -1
		}
		fdBase := fdCount()
		var fdPeak atomic.Int64
		var ticks atomic.Uint64
		var kept, peak atomic.Uint64
		raise := func(top *atomic.Uint64, v uint64) {
			for old := top.Load(); v > old; old = top.Load() {
				if top.CompareAndSwap(old, v) {
					break
				}
			}
		}
		// Each collection publishes the live heap it marked, and sampling
		// every millisecond sees each one long before the next replaces it.
		var start runtime.MemStats
		runtime.ReadMemStats(&start)
		live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		sample := func() {
			metrics.Read(live)
			raise(&kept, live[0].Value.Uint64())
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			raise(&peak, m.HeapAlloc)
			if ticks.Add(1)%50 == 0 {
				n := fdCount()
				for old := fdPeak.Load(); n > old; old = fdPeak.Load() {
					if fdPeak.CompareAndSwap(old, n) {
						break
					}
				}
			}
		}
		done, joined := make(chan struct{}), make(chan struct{})
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
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		r, err := Run(ctx, p.Dir, "", tc, Options{TLCParallel: 1})
		close(done)
		<-joined
		sample()
		var end runtime.MemStats
		runtime.ReadMemStats(&end)
		collections := end.NumGC - start.NumGC
		if err != nil {
			t.Fatal(err)
		}
		gate569Result(t, r, n, kind, route, bad)
		gate569Coverage(t, events, route, kind, n, bad)
		gate569Clean(t, tmp)
		if bad {
			if _, err := os.Stat(filepath.Join(events, "late-bad-seen")); err != nil {
				t.Fatal("recording tail was not checked")
			}
		}
		// A run the collector never visited can't say what the gate kept.
		if collections == 0 {
			t.Fatalf("%s/%s n=%d finished without a collection", route, kind, n)
		}
		if got := kept.Load(); got > gate569Ceiling {
			t.Errorf("%s/%s n=%d kept=%d MiB exceeds fixed %d MiB ceiling", route, kind, n, got>>20, gate569Ceiling>>20)
		}
		if fdBase >= 0 && fdPeak.Load() > fdBase+24 {
			t.Errorf("recording descriptors accumulated: baseline=%d peak=%d", fdBase, fdPeak.Load())
		}
		t.Logf("%s/%s n=%d bad=%v kept=%d bytes peak=%d bytes collections=%d", route, kind, n, bad, kept.Load(), peak.Load(), collections)
		return
	}
	routes := []string{"go", "typescript"}
	if refused {
		routes = []string{"typescript"}
	}
	for _, route := range routes {
		for _, n := range []int{32768, 65536} {
			kind := "traces"
			if route == "go" {
				kind = "attempts"
			}
			if refused {
				kind = "refused"
			}
			path := filepath.Join(t.TempDir(), "recording.json")
			gate569Record(t, path, kind, n, gate569Padding)
			badCases := []bool{false, true}
			if refused {
				badCases = []bool{false}
			}
			for _, bad := range badCases {
				t.Run(fmt.Sprintf("%s/%d/bad=%v", route, n, bad), func(t *testing.T) {
					t.Logf("%s", gate569Measured(t, strings.Split(t.Name(), "/")[0], route, n, bad, path))
				})
			}
		}
	}
}

// gate569Measured runs one workload in a process of its own, under the
// measured settings, and returns its output.
func gate569Measured(t *testing.T, test, route string, n int, bad bool, path string) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+test+"$", "-test.v", "-test.count=1", "-test.timeout=4m30s")
	cmd.Env = append(os.Environ(), "INVARIANT_569_MEASURE=1", "INVARIANT_569_COUNT="+strconv.Itoa(n), "INVARIANT_569_ROUTE="+route, "INVARIANT_569_BAD="+strconv.FormatBool(bad), "INVARIANT_569_INPUT="+path, "GOMAXPROCS=1", "GOGC=100", "GOMEMLIMIT=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("measured gate: %v\n%s", err, out)
	}
	return out
}

func TestGateRecordingPeakHeap(t *testing.T)        { gate569Memory(t, false) }
func TestGateRefusedRecordingPeakHeap(t *testing.T) { gate569Memory(t, true) }

var gate697Measure = regexp.MustCompile(`go/attempts n=65536 bad=true kept=(\d+) bytes peak=(\d+) bytes`)

// Identical runs of the 65,536-entry late-bad Go case, each in a process of
// its own, all stay under the ceiling. The spread of what the gate kept, and
// of the peak including garbage, is reported (#697).
func TestGateRecordingKeptHeapIsStable697(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	gate569Record(t, path, "attempts", 65536, gate569Padding)
	var kept, peak []uint64
	for range 3 {
		out := gate569Measured(t, "TestGateRecordingPeakHeap", "go", 65536, true, path)
		m := gate697Measure.FindSubmatch(out)
		if m == nil {
			t.Fatalf("measured gate reported no heap:\n%s", out)
		}
		k, _ := strconv.ParseUint(string(m[1]), 10, 64)
		p, _ := strconv.ParseUint(string(m[2]), 10, 64)
		kept, peak = append(kept, k), append(peak, p)
	}
	mib := func(v []uint64) string {
		lo, hi := slices.Min(v), slices.Max(v)
		return fmt.Sprintf("%d to %d MiB (spread %d MiB)", lo>>20, hi>>20, (hi-lo)>>20)
	}
	t.Logf("%d runs: kept %s against the %d MiB ceiling; peak including garbage %s", len(kept), mib(kept), gate569Ceiling>>20, mib(peak))
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
