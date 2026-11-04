package tlc_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestTLCMemoryAndStallRetryLimits(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		passed         bool
		memory         []uint64
	}{
		{"stall then memory", "stall memory pass", true, []uint64{2048, 2048, 12288}},
		{"memory then stall", "memory stall pass", true, []uint64{2048, 12288, 12288}},
		{"escalation cannot renew stall retry", "stall memory stall pass", false, []uint64{2048, 2048, 12288}},
		{"two stalls after escalation", "memory stall stall pass", false, []uint64{2048, 12288, 12288}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "case \"$attempt\" in\n"
			for i, mode := range strings.Fields(tc.sequence) {
				body += fmt.Sprintf("%d) ", i+1)
				switch mode {
				case "stall":
					body += "printf 'unfinished attempt\\n'; exec sleep 30"
				case "memory":
					body += "cat \"$root/pass\"; printf 'java.lang.OutOfMemoryError: Java heap space\\n'; exit 1"
				default:
					body += "cat \"$root/pass\"; exit 0"
				}
				body += ";;\n"
			}
			body += "*) exit 99;;\nesac\n"
			f := stall470Setup(t, body)
			b, err := tlc.NewBudget(8, 16<<30, 1)
			if err != nil {
				t.Fatal(err)
			}
			runner := f.runner(750 * time.Millisecond)
			runner.Budget = b
			r, err := runner.Check(stall470Context(t), f.dir, "Recovery", stall470Config())
			if err != nil || (r.Outcome == tlc.Passed) != tc.passed {
				t.Fatalf("retry result: %+v, %v", r, err)
			}
			attempts := b.Attempts()
			if len(attempts) != len(tc.memory) {
				t.Fatalf("retry limit changed: %+v", attempts)
			}
			for i, a := range attempts {
				if a.Check != attempts[0].Check || a.Attempt != i+1 || a.MemoryBytes != tc.memory[i]<<20 || a.HeapBytes != tc.memory[i]*3/4<<20 {
					t.Fatalf("attempt %d lost identity or allocation: %+v", i+1, a)
				}
				if i < len(attempts)-1 && (a.Outcome != tlc.Failed || a.Message == "") {
					t.Fatalf("unfinished attempt recorded evidence: %+v", a)
				}
				args := stall470Read(t, filepath.Join(f.root, fmt.Sprintf("args-%d", i+1)))
				if !strings.Contains(args, fmt.Sprintf("--memory\n%dm\n--memory-swap\n%dm\n", tc.memory[i], tc.memory[i])) ||
					!strings.Contains(args, fmt.Sprintf("-Xmx%dm\n", tc.memory[i]*3/4)) {
					t.Fatalf("recorded limits differ from launched container: %s", args)
				}
			}
			if tc.passed {
				want := tlc.Parse(stall470Read(t, filepath.Join(f.root, "pass")), 0)
				if !reflect.DeepEqual(r, want) {
					t.Fatalf("recovered evidence = %+v, want %+v", r, want)
				}
			} else if r.Outcome != tlc.Failed || !strings.Contains(r.Message, "stopped making progress") || r.DistinctStates != 0 {
				t.Fatalf("second stall supplied evidence: %+v", r)
			}
			// The snapshot is detached from the budget's stored history.
			attempts[0].Message = "changed by reader"
			if b.Attempts()[0].Message == attempts[0].Message {
				t.Fatal("resource history aliases its snapshot")
			}
		})
	}
}

func TestTLCMemoryRetryRoundedAndUnknownCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cpus   int
		memory uint64
		want   []uint64
	}{
		{"fractional reserve", 8, (4097 << 20) + 1, []uint64{2048, 3072}},
		{"unknown CPU", 0, 16 << 30, []uint64{1024}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stall470Setup(t, "printf 'GC overhead limit exceeded\\n'\nexit 1\n")
			b, err := tlc.NewBudget(tc.cpus, tc.memory, 1)
			if err != nil {
				t.Fatal(err)
			}
			runner := f.runner(time.Second)
			runner.Budget = b
			r, err := runner.Check(stall470Context(t), f.dir, "Memory", stall470Config())
			if err != nil || r.Outcome != tlc.Failed {
				t.Fatalf("repeated exhaustion: %+v, %v", r, err)
			}
			var memory []uint64
			for _, a := range b.Attempts() {
				memory = append(memory, a.MemoryBytes>>20)
			}
			if !reflect.DeepEqual(memory, tc.want) {
				t.Fatalf("allocations = %v MiB, want %v", memory, tc.want)
			}
		})
	}
}

func TestTLCMemoryRetryWaitsForCleanup(t *testing.T) {
	f := stall470Setup(t, `if [ "$attempt" = 1 ]; then
    printf 'Java heap space\n'
    exec sleep 30
fi
cat "$root/pass"
`)
	// Memory exhaustion can be followed by silence. Hold removal to prove
	// the exclusive retry cannot acquire the allocation before cleanup ends.
	script := stall470Read(t, filepath.Join(f.root, "docker"))
	script = strings.Replace(script, "    exit 0\nfi\n", "    while [ ! -f \"$root/release-rm\" ]; do sleep 0.01; done\n    exit 0\nfi\n", 1)
	if err := os.WriteFile(filepath.Join(f.root, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(filepath.Join(f.root, "release-rm"), nil, 0600) })
	b, err := tlc.NewBudget(8, 16<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	runner := f.runner(750 * time.Millisecond)
	runner.Budget = b
	ctx := stall470Context(t)
	done := make(chan error, 1)
	go func() {
		r, err := runner.Check(ctx, f.dir, "Memory", stall470Config())
		if err == nil && r.Outcome != tlc.Passed {
			err = fmt.Errorf("retry: %+v", r)
		}
		done <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("finished before cleanup: %v", err)
		case <-ctx.Done():
			t.Fatal("cleanup never started")
		case <-ticker.C:
			events, _ := os.ReadFile(filepath.Join(f.root, "events"))
			if !strings.Contains(string(events), "\nrm ") {
				continue
			}
			if _, err := os.Stat(filepath.Join(f.root, "args-2")); !os.IsNotExist(err) || len(b.Attempts()) != 0 {
				t.Fatal("memory retry preceded cleanup completion")
			}
			if err := os.WriteFile(filepath.Join(f.root, "release-rm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("retry did not finish after cleanup")
			}
			f.events(t, 2, 1)
			return
		}
	}
}
