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

func TestTLCWrappedMemoryCauseOnSameLine(t *testing.T) {
	for _, reason := range []string{"Java heap space", "GC overhead limit exceeded"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s/%q", reason, newline), func(t *testing.T) {
				f := stall470Setup(t, "cat \"$root/transcript\"\nexit 1\n")
				out := stall470Read(t, "testdata/violated.txt") + stall470Frame(1000,
					"The exception was a java.util.concurrent.ExecutionException: java.lang.OutOfMemoryError: "+reason)
				out = strings.ReplaceAll(out, "\n", newline)
				if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(out), 0o600); err != nil {
					t.Fatal(err)
				}
				r, err := f.runner(5*time.Second).Check(stall470Context(t), f.dir, "Memory", stall470Config())
				want := tlc.Result{Outcome: tlc.Failed, ExitCode: 1, Message: "TLC check Memory exhausted Java memory"}
				if reason == "GC overhead limit exceeded" {
					want.Message += " (GC overhead limit exceeded)"
				}
				if err != nil || !reflect.DeepEqual(r, want) {
					t.Fatalf("wrapped cause retained evidence or lost its diagnostic: %+v, %v; want %+v", r, err, want)
				}
				f.events(t, 1, 0)
			})
		}
	}
}

func TestTLCWrappedMemoryTextNeedsExceptionCause(t *testing.T) {
	const header = "The exception was a java.util.concurrent.ExecutionException"
	const cause = ": java.lang.OutOfMemoryError: GC overhead limit exceeded"
	for _, tc := range []struct {
		name, out string
	}{
		{"printed text", header + "\n" + cause + "\n"},
		{"missing wrapper", stall470Frame(1000, cause)},
		{"unrelated exception message", stall470Frame(1000, "The exception was a java.lang.IllegalArgumentException\n"+cause)},
		{"unrelated wrapped cause", stall470Frame(1000, header+"\n: java.lang.IllegalStateException"+cause)},
		{"similar exception name", stall470Frame(1000, header+"\n: java.lang.OutOfMemoryErrorValue: GC overhead limit exceeded")},
		{"quoted name", stall470Frame(1000, header+"\n: \"java.lang.OutOfMemoryError: GC overhead limit exceeded\"")},
		{"separate frames", stall470Frame(1000, header) + stall470Frame(1000, cause)},
		{"other message", stall470Frame(1004, header+"\n"+cause)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stall470Setup(t, "cat \"$root/transcript\"\n")
			out := stall470Read(t, "testdata/passed.txt") + tc.out
			if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(out), 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := tlc.NewBudget(8, 16<<30, 2)
			if err != nil {
				t.Fatal(err)
			}
			runner := f.runner(5 * time.Second)
			runner.Budget = b
			r, err := runner.Check(stall470Context(t), f.dir, "Memory", stall470Config())
			if want := tlc.Parse(out, 0); err != nil || !reflect.DeepEqual(r, want) {
				t.Fatalf("ordinary output changed: %+v, %v; want %+v", r, err, want)
			}
			if attempts := b.Attempts(); len(attempts) != 1 || attempts[0].Outcome != r.Outcome || attempts[0].MemoryBytes != 2<<30 {
				t.Fatalf("ordinary output earned a memory retry: %+v", attempts)
			}
			f.events(t, 1, 0)
		})
	}
}
