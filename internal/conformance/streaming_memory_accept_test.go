package conformance

// These measurements include parsing and every checking phase, in fresh test
// processes with fixed GOMAXPROCS/GOGC. The parent writes files incrementally;
// neither the child input nor the semantic TLC stand-in holds a recording.
// The 192 MiB ceiling permits indexes and active windows. Even one copy of
// the smaller recording's payload is 256 MiB; doubling it must still fit.
// This is a workload bound, not a bound on the size of an individual value.

import (
	"bufio"
	"context"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

const s559PadBytes = 8192
const s559HeapCeiling = 192 << 20

func TestStreamingTracePeakHeap(t *testing.T)           { s559Memory(t, "traces") }
func TestStreamingAttemptStatePeakHeap(t *testing.T)    { s559Memory(t, "states") }
func TestStreamingAttemptContentsPeakHeap(t *testing.T) { s559Memory(t, "contents") }

func s559Memory(t *testing.T, kind string) {
	t.Helper()
	if os.Getenv("INVARIANT_559_MEASURE") == kind {
		n, err := strconv.Atoi(os.Getenv("INVARIANT_559_COUNT"))
		if err != nil {
			t.Fatal(err)
		}
		runtime.GOMAXPROCS(1)
		debug.SetGCPercent(100)
		debug.SetMemoryLimit(1<<63 - 1)
		f, err := os.Open(os.Getenv("INVARIANT_559_INPUT"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		dir := t.TempDir()
		model := s559LargeModel(kind, n)
		s559Write(t, filepath.Join(dir, "Counter.tla"), model)
		s559Write(t, filepath.Join(dir, "Limit.tla"), "---- MODULE Limit ----\nMax == "+strconv.Itoa(n-1)+"\n====\n")
		steps, err := tla.Steps(model)
		if err != nil {
			t.Fatal(err)
		}
		var m *Model
		if kind != "traces" {
			m = &Model{Steps: steps}
		}
		fake := &s559LargeTLC{kind: kind, n: n, pad: strings.Repeat("p", s559PadBytes), model: model, seen: make(map[string][]uint8)}
		// Only fixed-size coverage entries survive a runner call.
		for _, phase := range []string{"init", "steps", "strict", "tried"} {
			fake.seen[phase] = make([]uint8, n)
		}
		var peak atomic.Uint64
		sample := func() {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			for old := peak.Load(); ms.HeapAlloc > old; old = peak.Load() {
				if peak.CompareAndSwap(old, ms.HeapAlloc) {
					break
				}
			}
		}
		fake.sample = sample
		done, joined := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(joined)
			tick := time.NewTicker(2 * time.Millisecond)
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
		result, err := CheckReader(ctx, fake, dir, "Counter", nil, []string{"n", "pad"}, int64(n), &s559MeasuredReader{r: f, sample: sample}, m)
		sample()
		close(done)
		<-joined
		if err != nil {
			t.Fatal(err)
		}
		states, runs, transitions := n, 0, n-1
		if kind == "traces" {
			runs, transitions = n/2, n/2
		}
		if kind == "contents" {
			states, transitions = 1, 0
		}
		stepsCount := n
		if kind == "traces" {
			stepsCount = n / 2
		}
		want := Result{Passed: true, Runs: runs, Steps: stepsCount, States: states, Transitions: transitions, ModelStates: int64(n)}
		if kind != "traces" {
			want.Tried = &Tried{Passed: true, Attempts: n, States: states}
		}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("result=%+v tried=%+v; want %+v tried=%+v", result, result.Tried, want, want.Tried)
		}
		fake.coverage(t)
		if got := peak.Load(); got > s559HeapCeiling {
			t.Fatalf("%s/%d peak heap=%d MiB; ceiling=%d MiB", kind, n, got>>20, s559HeapCeiling>>20)
		}
		t.Logf("%s/%d peak heap=%d MiB", kind, n, peak.Load()>>20)
		return
	}
	for _, n := range []int{32768, 65536} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.json")
			s559LargeRecording(t, path, kind, n)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^"+strings.Split(t.Name(), "/")[0]+"$", "-test.count=1", "-test.timeout=4m30s")
			cmd.Env = append(os.Environ(), "INVARIANT_559_MEASURE="+kind, "INVARIANT_559_COUNT="+strconv.Itoa(n), "INVARIANT_559_INPUT="+path, "GOMAXPROCS=1", "GOGC=100", "GOMEMLIMIT=off")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated measurement: %v\n%s", err, out)
			}
		})
	}
}

type s559MeasuredReader struct {
	r      io.Reader
	sample func()
}

func (r *s559MeasuredReader) Read(p []byte) (int, error) { r.sample(); return r.r.Read(p) }

func s559Write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func s559LargeRecording(t *testing.T, path, kind string, n int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 64<<10)
	pad := strings.Repeat("p", s559PadBytes)
	state := func(i int) { fmt.Fprintf(w, `{"n":%d,"pad":"%s"}`, i, pad) }
	if kind == "traces" {
		fmt.Fprint(w, `{"traces":[`)
		for i := 0; i < n; i += 2 {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprint(w, "[")
			state(i)
			fmt.Fprint(w, ",")
			state(i + 1)
			fmt.Fprint(w, "]")
		}
		fmt.Fprint(w, "]}")
	} else {
		// Forward references are intentional: attempts appear before states.
		fmt.Fprint(w, `{"attempts":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			if kind == "contents" {
				name, outcome := "Read", `{"outcome":"accepted"}`
				if i%2 == 1 {
					name, outcome = "Denied", `{"outcome":"refused","refusal":{"type":"counter.Refused","attributes":{"detail":"`+pad+`"}}}`
				}
				fmt.Fprintf(w, `[0,"%s",[{"$seq":[%d,"%s"]}],0,%s]`, name, i/2, pad, outcome)
			} else if i == n-1 {
				fmt.Fprintf(w, `[%d,"Inc",[],%d,{"outcome":"refused","refusal":{"type":"counter.Refused","attributes":{}}}]`, i, i)
			} else {
				fmt.Fprintf(w, `[%d,"Inc",[],%d,{"outcome":"accepted"}]`, i, i+1)
			}
		}
		fmt.Fprint(w, `],"init":[`)
		count := n
		if kind == "contents" {
			count = 1
		}
		for i := 0; i < count; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprint(w, i)
		}
		fmt.Fprint(w, `],"states":[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			state(i)
		}
		fmt.Fprint(w, "]}")
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func s559LargeModel(kind string, n int) string {
	head := "---- MODULE Counter ----\nEXTENDS Naturals, Sequences, Limit\nVARIABLES n, pad\nvars == <<n,pad>>\nInit == n \\in 0..Max\n"
	if kind == "contents" {
		return head + "Pad == " + strconv.Quote(strings.Repeat("p", s559PadBytes)) + "\nRead(a) == UNCHANGED vars\nDenied(a) == FALSE /\\ UNCHANGED vars\nNext == \\E k \\in 0.." + strconv.Itoa(n/2-1) + " : Read(<<k,Pad>>) \\/ Denied(<<k,Pad>>)\n====\n"
	}
	return head + "Inc == n < Max /\\ n' = n+1 /\\ UNCHANGED pad\nNext == Inc\n====\n"
}

// A small lexer for the data expressions emitted in these checking modules.
// It never reads an unbounded line: one tried-action set can be hundreds of
// MiB. Strings and values survive only while their one record is checked.
type s559Lex struct {
	r     *bufio.Reader
	saved string
	err   error
}

func (l *s559Lex) token() string {
	if l.saved != "" {
		s := l.saved
		l.saved = ""
		return s
	}
	for {
		b, e := l.r.ReadByte()
		if e != nil {
			l.err = e
			return ""
		}
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		var s strings.Builder
		s.WriteByte(b)
		if b == '"' {
			escaped := false
			for {
				c, e := l.r.ReadByte()
				if e != nil {
					l.err = e
					return ""
				}
				s.WriteByte(c)
				if c == '"' && !escaped {
					return s.String()
				}
				if c == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
			}
		}
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || b >= '0' && b <= '9' || b == '-' {
			for {
				p, e := l.r.Peek(1)
				if e != nil {
					break
				}
				c := p[0]
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
					break
				}
				l.r.ReadByte()
				s.WriteByte(c)
			}
			return s.String()
		}
		if b == '<' || b == '>' || b == '=' {
			p, e := l.r.Peek(1)
			if e == nil && p[0] == b {
				l.r.ReadByte()
				s.WriteByte(b)
			}
		}
		if b == '|' {
			p, e := l.r.Peek(2)
			if e == nil && string(p) == "->" {
				l.r.Discard(2)
				s.WriteString("->")
			}
		}
		return s.String()
	}
}
func (l *s559Lex) expect(w string) error {
	if g := l.token(); g != w {
		return fmt.Errorf("module token %q, want %q (%v)", g, w, l.err)
	}
	return nil
}
func (l *s559Lex) find(name string) error {
	for {
		s := l.token()
		if s == name {
			return l.expect("==")
		}
		if s == "" {
			return fmt.Errorf("missing %s: %v", name, l.err)
		}
	}
}
func (l *s559Lex) integer() (int, error) { s := l.token(); n, e := strconv.Atoi(s); return n, e }
func (l *s559Lex) state(pad string) (int, error) {
	if e := l.expect("["); e != nil {
		return 0, e
	}
	n, haveN, havePad := 0, false, false
	for {
		field := l.token()
		if e := l.expect("|->"); e != nil {
			return 0, e
		}
		switch field {
		case "n":
			var e error
			n, e = l.integer()
			if e != nil {
				return 0, e
			}
			haveN = true
		case "pad":
			s, e := strconv.Unquote(l.token())
			if e != nil || s != pad {
				return 0, fmt.Errorf("lost state payload: %v", e)
			}
			havePad = true
		default:
			return 0, fmt.Errorf("unexpected field %q", field)
		}
		sep := l.token()
		if sep == "]" {
			break
		}
		if sep != "," {
			return 0, fmt.Errorf("bad record separator %q", sep)
		}
	}
	if !haveN || !havePad {
		return 0, fmt.Errorf("incomplete state")
	}
	return n, nil
}
func (l *s559Lex) states(open, close, pad string) ([]int, error) {
	if e := l.expect(open); e != nil {
		return nil, e
	}
	var ids []int
	for {
		s := l.token()
		if s == close {
			return ids, nil
		}
		l.saved = s
		n, e := l.state(pad)
		if e != nil {
			return nil, e
		}
		ids = append(ids, n)
		if len(ids) > 4000 {
			return nil, fmt.Errorf("unbounded state window")
		}
		s = l.token()
		if s == close {
			return ids, nil
		}
		if s != "," {
			return nil, fmt.Errorf("state separator %q", s)
		}
	}
}
func (l *s559Lex) argument(pad string) (int, error) {
	if e := l.expect("<<"); e != nil {
		return 0, e
	}
	n, e := l.integer()
	if e != nil {
		return 0, e
	}
	if e = l.expect(","); e != nil {
		return 0, e
	}
	s, e := strconv.Unquote(l.token())
	if e != nil || s != pad {
		return 0, fmt.Errorf("lost attempt payload")
	}
	return n, l.expect(">>")
}

type s559LargeTLC struct {
	kind              string
	n                 int
	pad, model        string
	sample            func()
	mu                sync.Mutex
	seen              map[string][]uint8
	active, maxActive int
	stepOrder         int
	strictOrder       int
	lastInit          string
}

func (f *s559LargeTLC) mark(phase string, i int) error {
	if i < 0 || i >= len(f.seen[phase]) {
		return fmt.Errorf("%s unexpected item %d", phase, i)
	}
	f.seen[phase][i]++
	if f.seen[phase][i] != 1 {
		return fmt.Errorf("%s repeats item %d", phase, i)
	}
	if phase == "steps" {
		want := f.stepOrder
		if f.kind == "traces" {
			want *= 2
		}
		if i != want {
			return fmt.Errorf("transition order: got source %d, want %d", i, want)
		}
		f.stepOrder++
	}
	if phase == "strict" {
		if i != f.strictOrder {
			return fmt.Errorf("strict attempt order: got %d, want %d", i, f.strictOrder)
		}
		f.strictOrder++
	}
	if phase == "init" {
		key := strconv.Itoa(i)
		if f.lastInit != "" && key <= f.lastInit {
			return fmt.Errorf("starts lost canonical order: %s after %s", key, f.lastInit)
		}
		f.lastInit = key
	}
	return nil
}
func (f *s559LargeTLC) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock() // measured concurrency is one
	f.active++
	defer func() { f.active-- }()
	f.maxActive = max(f.maxActive, f.active)
	f.sample()
	for name, want := range map[string]string{"Counter.tla": f.model, "Limit.tla": "---- MODULE Limit ----\nMax == " + strconv.Itoa(f.n-1) + "\n====\n"} {
		info, e := os.Lstat(filepath.Join(dir, name))
		if e != nil || !info.Mode().IsRegular() {
			return tlc.Result{}, fmt.Errorf("model is not a copied regular file: %s", name)
		}
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || string(raw) != want {
			return tlc.Result{}, fmt.Errorf("model copy differs: %s", name)
		}
	}
	file, err := os.Open(filepath.Join(dir, module+".tla"))
	if err != nil {
		return tlc.Result{}, err
	}
	defer file.Close()
	l := &s559Lex{r: bufio.NewReaderSize(file, 64<<10)}
	var tails []string
	switch cfg.Specification {
	case "Invariant_ISpec":
		if !reflect.DeepEqual(cfg.Invariants, []string{"Init"}) {
			return tlc.Result{}, fmt.Errorf("Init isn't checked")
		}
		if err = l.find("Invariant_Starts"); err != nil {
			break
		}
		var ids []int
		ids, err = l.states("{", "}", f.pad)
		if err != nil {
			break
		}
		if len(ids) > 1000 || len(ids) == 0 {
			err = fmt.Errorf("Init window=%d", len(ids))
			break
		}
		for _, id := range ids {
			if f.kind == "traces" && id%2 != 0 {
				err = fmt.Errorf("non-start %d", id)
				break
			}
			if err = f.mark("init", id); err != nil {
				break
			}
		}
		tails = []string{"Invariant_IInit", "Invariant_Starts", "(s).n", "(s).pad", "Invariant_ISpec"}
	case "Invariant_CSpec":
		if err = l.find("Invariant_States"); err != nil {
			break
		}
		var ids []int
		ids, err = l.states("<<", ">>", f.pad)
		if err != nil {
			break
		}
		if err = l.find("Invariant_Steps"); err != nil {
			break
		}
		if err = l.expect("{"); err != nil {
			break
		}
		used := make([]bool, len(ids))
		count := 0
		for err == nil {
			s := l.token()
			if s == "}" {
				break
			}
			l.saved = s
			if err = l.expect("<<"); err != nil {
				break
			}
			var a, b int
			a, err = l.integer()
			if err != nil {
				break
			}
			if err = l.expect(","); err != nil {
				break
			}
			b, err = l.integer()
			if err != nil {
				break
			}
			if err = l.expect(">>"); err != nil {
				break
			}
			if a < 1 || b < 1 || a > len(ids) || b > len(ids) || ids[b-1] != ids[a-1]+1 {
				err = fmt.Errorf("invalid local Next pair %d,%d", a, b)
				break
			}
			used[a-1], used[b-1] = true, true
			count++
			err = f.mark("steps", ids[a-1])
			if err != nil {
				break
			}
			s = l.token()
			if s == "}" {
				break
			}
			if s != "," {
				err = fmt.Errorf("pair separator %q", s)
			}
		}
		if err == nil && (count == 0 || count > 2000) {
			err = fmt.Errorf("step window=%d", count)
		}
		for _, u := range used {
			if !u {
				err = fmt.Errorf("step module contains an unused endpoint")
			}
		}
		tails = []string{"/\\ Next", "n' = (Invariant_States[invariant_target]).n", "pad' = (Invariant_States[invariant_target]).pad", "invariant_source = p[1]", "invariant_target = p[2]", "invariant_done' = TRUE"}
	case "Invariant_ASpec":
		if err = l.find("Invariant_Attempts"); err != nil {
			break
		}
		if err = l.expect("<<"); err != nil {
			break
		}
		count := 0
		for err == nil {
			s := l.token()
			if s == ">>" {
				break
			}
			l.saved = s
			if err = l.expect("<<"); err != nil {
				break
			}
			var from, to, k int
			from, err = l.state(f.pad)
			if err != nil {
				break
			}
			if err = l.expect(","); err != nil {
				break
			}
			to, err = l.state(f.pad)
			if err != nil {
				break
			}
			if err = l.expect(","); err != nil {
				break
			}
			var name string
			name, err = strconv.Unquote(l.token())
			if err != nil {
				break
			}
			if err = l.expect(","); err != nil {
				break
			}
			if err = l.expect("<<"); err != nil {
				break
			}
			if f.kind == "contents" {
				k, err = l.argument(f.pad)
				if err != nil {
					break
				}
			}
			if err = l.expect(">>"); err != nil {
				break
			}
			if err = l.expect(","); err != nil {
				break
			}
			refused := l.token()
			if err = l.expect(">>"); err != nil {
				break
			}
			id := from
			wantName, wantRefused, wantTo := "Inc", "FALSE", from+1
			if from == f.n-1 {
				wantRefused, wantTo = "TRUE", from
			}
			if f.kind == "contents" {
				id = 2 * k
				wantName, wantRefused, wantTo = "Read", "FALSE", 0
				if name == "Denied" {
					id++
					wantName, wantRefused = "Denied", "TRUE"
				}
				if from != 0 {
					err = fmt.Errorf("wrong attempt source")
				}
			}
			if name != wantName || refused != wantRefused || to != wantTo {
				err = fmt.Errorf("wrong strict obligation %s %d->%d %s", name, from, to, refused)
				break
			}
			count++
			if err == nil {
				err = f.mark("strict", id)
			}
			if err != nil {
				break
			}
			s = l.token()
			if s == ">>" {
				break
			}
			if s != "," {
				err = fmt.Errorf("strict separator %q", s)
			}
		}
		if err == nil && (count == 0 || count > 2000) {
			err = fmt.Errorf("strict window=%d", count)
		}
		tails = []string{"Invariant_Action == CASE", "~ENABLED (Invariant_Action /\\ UNCHANGED <<invariant_i, invariant_done>>)", "ELSE /\\ Invariant_Action", "n' = (Invariant_Attempts[invariant_i][2]).n", "invariant_done' = TRUE"}
		if f.kind == "contents" {
			tails = append(tails, "Read(Invariant_Attempts[invariant_i][4][1])", "Denied(Invariant_Attempts[invariant_i][4][1])")
		} else {
			tails = append(tails, `"Inc" -> Inc`)
		}
	case "Invariant_TSpec":
		if !reflect.DeepEqual(cfg.Invariants, []string{"Invariant_EveryStepTried"}) {
			return tlc.Result{}, fmt.Errorf("missing tried invariant")
		}
		if err = l.find("Invariant_States"); err != nil {
			break
		}
		var ids []int
		ids, err = l.states("<<", ">>", f.pad)
		if err != nil {
			break
		}
		if len(ids) > 1000 || len(ids) == 0 {
			err = fmt.Errorf("tried window=%d", len(ids))
			break
		}
		if err = l.find("Invariant_Tried"); err != nil {
			break
		}
		if err = l.expect("<<"); err != nil {
			break
		}
		for i, id := range ids {
			if i > 0 {
				if err = l.expect(","); err != nil {
					break
				}
			}
			if err = l.expect("{"); err != nil {
				break
			}
			count := 0
			for err == nil {
				s := l.token()
				if s == "}" {
					break
				}
				l.saved = s
				if err = l.expect("<<"); err != nil {
					break
				}
				var name string
				name, err = strconv.Unquote(l.token())
				if err != nil {
					break
				}
				item := id
				if f.kind == "contents" {
					if err = l.expect(","); err != nil {
						break
					}
					var k int
					k, err = l.argument(f.pad)
					item = 2 * k
					if name == "Denied" {
						item++
					} else if name != "Read" {
						err = fmt.Errorf("unknown tried action")
					}
					if id != 0 {
						err = fmt.Errorf("tried actions in wrong state")
					}
				} else if name != "Inc" {
					err = fmt.Errorf("unknown tried action")
				}
				if err != nil {
					break
				}
				if err = l.expect(">>"); err != nil {
					break
				}
				count++
				if err = f.mark("tried", item); err != nil {
					break
				}
				s = l.token()
				if s == "}" {
					break
				}
				if s != "," {
					err = fmt.Errorf("tried separator %q", s)
				}
			}
			if err != nil {
				break
			}
			want := 1
			if f.kind == "contents" {
				want = f.n
			}
			if count != want {
				err = fmt.Errorf("state %d tried %d actions, want %d", id, count, want)
				break
			}
		}
		if err == nil {
			err = l.expect(">>")
		}
		tails = []string{"Invariant_EveryStepTried == Invariant_Untried = {}", "invariant_x \\notin invariant_t", "~ENABLED", "ENABLED (Invariant_Larger!"}
	default:
		err = fmt.Errorf("unknown checking specification %q", cfg.Specification)
	}
	if err != nil {
		return tlc.Result{}, err
	}
	// The remaining predicates are small; cap reading so a malformed module
	// cannot move a whole recording into the stand-in's heap.
	tail, err := io.ReadAll(io.LimitReader(l.r, 64<<10))
	if err != nil {
		return tlc.Result{}, err
	}
	if len(tail) == 64<<10 {
		return tlc.Result{}, fmt.Errorf("oversized predicate tail")
	}
	normalized := strings.Join(strings.Fields(string(tail)), " ")
	for _, want := range tails {
		if !strings.Contains(normalized, strings.Join(strings.Fields(want), " ")) {
			return tlc.Result{}, fmt.Errorf("%s lacks obligation %q", module, want)
		}
	}
	f.sample()
	if err = ctx.Err(); err != nil {
		return tlc.Result{}, err
	}
	return tlc.Result{Outcome: tlc.Passed}, nil
}
func (f *s559LargeTLC) coverage(t *testing.T) {
	t.Helper()
	for phase, seen := range f.seen {
		for i, got := range seen {
			want := uint8(1)
			switch f.kind {
			case "traces":
				if phase == "strict" || phase == "tried" || i%2 == 1 {
					want = 0
				}
			case "states":
				if phase == "steps" && i == f.n-1 {
					want = 0
				}
			case "contents":
				if phase == "steps" || phase == "init" && i != 0 {
					want = 0
				}
			}
			if got != want {
				t.Fatalf("%s/%s obligation %d checked %d times, want %d", f.kind, phase, i, got, want)
			}
		}
	}
}
