package tlc_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

// Use the existing Docker-boundary fixture. These reports are TLC's output,
// not a JVM invocation, so the tests need neither Docker nor Java.
func tlc552Wrapped(cause string) string {
	return "@!@!@STARTMSG 1000:1 @!@!@\n" +
		"TLC threw an unexpected exception.\n" +
		"This was probably caused by an error in the spec or model.\n" +
		"See the User Output or TLC Console for clues to what happened.\n" +
		"The exception was a java.util.concurrent.ExecutionException\n" +
		": " + cause + "\n" +
		"@!@!@ENDMSG 1000 @!@!@\n"
}

func tlc552Write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tlc552Budget(t *testing.T) *tlc.Budget {
	t.Helper()
	b, err := tlc.NewBudget(8, 16<<30, 2)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tlc552Message(module, reason string) string {
	msg := "TLC check " + module + " exhausted Java memory"
	if reason == "GC overhead limit exceeded" {
		msg += " (GC overhead limit exceeded)"
	}
	return msg
}

func TestTLC552WrappedMemoryReportsAndModelText(t *testing.T) {
	for _, reason := range []string{"GC overhead limit exceeded", "Java heap space"} {
		for _, newline := range []string{"LF", "CRLF"} {
			t.Run(reason+"/"+newline, func(t *testing.T) {
				f := stall470Setup(t, "cat \"$root/transcript\"\nexit 1\n")
				out := stall470Read(t, "testdata/passed.txt") + stall470Read(t, "testdata/violated.txt") +
					tlc552Wrapped("java.lang.OutOfMemoryError: "+reason)
				if newline == "CRLF" {
					out = strings.ReplaceAll(out, "\n", "\r\n")
				}
				tlc552Write(t, filepath.Join(f.root, "transcript"), out)
				// A standalone runner reports exhaustion but cannot expand.
				r, err := f.runner(5*time.Second).Check(stall470Context(t), f.dir, "Wrapped", stall470Config())
				want := tlc.Result{Outcome: tlc.Failed, ExitCode: 1, Message: tlc552Message("Wrapped", reason)}
				if err != nil || !reflect.DeepEqual(r, want) {
					t.Fatalf("wrapped cause was not a memory failure without model evidence: got %+v, %v; want %+v", r, err, want)
				}
				f.events(t, 1, 0)
			})
		}
	}

	passed := stall470Read(t, "testdata/passed.txt")
	violated := stall470Read(t, "testdata/violated.txt")
	for _, tc := range []struct {
		name, out string
		exit      int
	}{
		{"SANY module name", strings.ReplaceAll(passed, "TwoPhase", "OutOfMemoryError"), 0},
		{"action name", strings.ReplaceAll(violated, "RMPrepare line", "OutOfMemoryError line"), 12},
		{"trace values", strings.NewReplacer(
			`"working"`, `": java.lang.OutOfMemoryError: GC overhead limit exceeded"`,
			`"prepared"`, `"The exception was a java.util.concurrent.ExecutionException\n: java.lang.OutOfMemoryError: Java heap space"`,
			`"init"`, `"OutOfMemoryError"`,
			"tmPrepared", "OutOfMemoryError",
		).Replace(violated), 12},
		{"other wrapped cause", tlc552Wrapped("java.lang.IllegalStateException: invalid configuration"), 1},
		{"other exception mentioning the name", tlc552Wrapped("java.lang.IllegalArgumentException: OutOfMemoryError is a model value"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stall470Setup(t, fmt.Sprintf("cat \"$root/transcript\"\nexit %d\n", tc.exit))
			tlc552Write(t, filepath.Join(f.root, "transcript"), tc.out)
			b := tlc552Budget(t)
			runner := f.runner(5 * time.Second)
			runner.Budget = b
			r, err := runner.Check(stall470Context(t), f.dir, "OutOfMemoryError", stall470Config())
			if want := tlc.Parse(tc.out, tc.exit); err != nil || !reflect.DeepEqual(r, want) {
				t.Fatalf("model text or unrelated failure changed: got %+v, %v; want %+v", r, err, want)
			}
			if attempts := b.Attempts(); len(attempts) != 1 || attempts[0].Outcome != r.Outcome || attempts[0].MemoryBytes != 2<<30 {
				t.Fatalf("ordinary output earned a memory retry: %+v", attempts)
			}
			f.events(t, 1, 0)
		})
	}
}

func TestTLC552WrappedMemoryRetryHistory(t *testing.T) {
	for _, reason := range []string{"GC overhead limit exceeded", "Java heap space"} {
		for _, recover := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/recover=%t", reason, recover), func(t *testing.T) {
				body := `if [ "$attempt" = 1 ]; then
    cat "$root/unfinished" "$root/diagnostic"
    exit 1
fi
`
				if recover {
					body += "cat \"$root/pass\"\n"
				} else {
					body += "cat \"$root/unfinished\" \"$root/diagnostic\"\nexit 1\n"
				}
				f := stall470Setup(t, body)
				tlc552Write(t, filepath.Join(f.root, "diagnostic"), tlc552Wrapped("java.lang.OutOfMemoryError: "+reason))
				tlc552Write(t, filepath.Join(f.root, "unfinished"), stall470Read(t, "testdata/violated.txt"))
				b := tlc552Budget(t)
				runner := f.runner(5 * time.Second)
				runner.Budget = b
				r, err := runner.Check(stall470Context(t), f.dir, "Wrapped", stall470Config())
				want := tlc.Result{Outcome: tlc.Failed, ExitCode: 1, Message: tlc552Message("Wrapped", reason)}
				if recover {
					want = tlc.Parse(stall470Read(t, filepath.Join(f.root, "pass")), 0)
				}
				if err != nil || !reflect.DeepEqual(r, want) {
					t.Errorf("retry result retained unfinished evidence or lost its outcome: got %+v, %v; want %+v", r, err, want)
				}
				attempts := b.Attempts()
				if len(attempts) != 2 {
					t.Fatalf("want exactly one memory retry and both history entries, got %+v", attempts)
				}
				for i, a := range attempts {
					memory, heap := uint64(2<<30), uint64(1536<<20)
					outcome, message := tlc.Failed, tlc552Message("Wrapped", reason)
					if i == 1 {
						memory, heap = 12<<30, 9<<30
						outcome, message = want.Outcome, want.Message
					}
					if a.Check == "" || a.Check != attempts[0].Check || a.Module != "Wrapped" || a.Attempt != i+1 ||
						a.MemoryBytes != memory || a.HeapBytes != heap || a.Outcome != outcome || a.Message != message {
						t.Errorf("attempt %d lost its identity, allocation or outcome: %+v", i+1, a)
					}
					args := stall470Read(t, filepath.Join(f.root, fmt.Sprintf("args-%d", i+1)))
					if !strings.Contains(args, fmt.Sprintf("--memory\n%dm\n--memory-swap\n%dm\n", memory>>20, memory>>20)) ||
						!strings.Contains(args, fmt.Sprintf("-Xmx%dm\n", heap>>20)) {
						t.Errorf("actual container limits differ from recorded allocation: %s", args)
					}
				}
				if first, second := stall470Read(t, filepath.Join(f.root, "config-1")), stall470Read(t, filepath.Join(f.root, "config-2")); first != second {
					t.Fatalf("memory retry changed configuration: %q versus %q", first, second)
				}
				f.events(t, 2, 0)
			})
		}
	}
}

func TestTLC552WrappedMemoryRetryRunsAlone(t *testing.T) {
	f := stall470Setup(t, `if [ "$attempt" = 1 ]; then
    cat "$root/diagnostic"
    exit 1
fi
if [ "$attempt" = 2 ]; then
    touch "$root/retry-started"
    while [ ! -f "$root/release-retry" ]; do sleep 0.01; done
fi
cat "$root/pass"
`)
	tlc552Write(t, filepath.Join(f.root, "diagnostic"), tlc552Wrapped("java.lang.OutOfMemoryError: GC overhead limit exceeded"))
	b := tlc552Budget(t)
	runner := f.runner(5 * time.Second)
	runner.Budget = b
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	type result struct {
		r   tlc.Result
		err error
	}
	heavy, peer := make(chan result, 1), make(chan result, 1)
	peerDir := t.TempDir()
	go func() {
		r, err := runner.Check(ctx, f.dir, "Wrapped", stall470Config())
		heavy <- result{r, err}
	}()
	// Always join the fake processes before their directories and PATH go away.
	peerStarted := false
	t.Cleanup(func() {
		cancel()
		<-heavy
		if peerStarted {
			<-peer
		}
	})
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(f.root, "retry-started")); err == nil {
			break
		}
		select {
		case got := <-heavy:
			heavy <- got // leave the completed result for cleanup
			t.Fatalf("wrapped failure did not start its larger retry: %+v, %v", got.r, got.err)
		case <-ctx.Done():
			t.Fatal("memory retry did not start within the fixture deadline")
		case <-ticker.C:
		}
	}
	peerStarted = true
	entered := make(chan struct{})
	go func() {
		close(entered)
		r, err := runner.Check(ctx, peerDir, "Peer", stall470Config())
		peer <- result{r, err}
	}()
	<-entered
	select {
	case got := <-peer:
		peer <- got
		t.Fatalf("ordinary check ran during the exclusive retry: %+v, %v", got.r, got.err)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(f.root, "args-3")); !os.IsNotExist(err) {
		t.Fatalf("another container started during the memory retry: %v", err)
	}
	tlc552Write(t, filepath.Join(f.root, "release-retry"), "")
	for _, done := range []chan result{heavy, peer} {
		select {
		case got := <-done:
			done <- got
			stall470Passed(t, got.r, got.err)
		case <-ctx.Done():
			t.Fatal("finishing the exclusive retry did not release the budget")
		}
	}
	f.events(t, 3, 0)
}
