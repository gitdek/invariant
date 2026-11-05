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

func TestTLCMemoryFramesOverrideEvidence(t *testing.T) {
	state := stall470Frame(2217, "1: <Initial predicate>\n/\\ n = 0")
	for _, diagnostic := range []struct {
		code int
		body string
	}{
		{1001, "Java ran out of memory. Running Java with a larger memory allocation pool (heap) may fix this."},
		{1002, "There are too many possible initial states. Java ran out of memory."},
		{1003, "Java ran out of memory during liveness checking."},
	} {
		for _, c := range []struct {
			name, prefix, finish string
			exit, removed        int
		}{
			{"invariant", stall470Frame(2110, "Invariant Safe is violated.") + state, "exit 1\n", 1, 0},
			{"property", stall470Frame(2112, "Action property OtherIsNext is violated.") + state + stall470Frame(2218, "2: Stuttering"), "exit 1\n", 1, 0},
			{"success", stall470Frame(2193, "Model checking completed.") + stall470Frame(2199, "17 states generated, 9 distinct states found."), "exit 0\n", 0, 0},
			{"stall", stall470Frame(2110, "Invariant Safe is violated.") + state, "exec sleep 30\n", 0, 1},
		} {
			t.Run(fmt.Sprintf("%d/%s", diagnostic.code, c.name), func(t *testing.T) {
				f := stall470Setup(t, "cat \"$root/transcript\"\n"+c.finish)
				frame := fmt.Sprintf("@!@!@STARTMSG %d:1 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", diagnostic.code, diagnostic.body, diagnostic.code)
				if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(c.prefix+frame), 0o600); err != nil {
					t.Fatal(err)
				}
				r, err := f.runner(750*time.Millisecond).Check(stall470Context(t), f.dir, "Memory", stall470Config())
				exit := c.exit
				if c.removed != 0 {
					// The cancelled Docker client's exit status is platform dependent.
					exit = r.ExitCode
				}
				want := tlc.Result{Outcome: tlc.Failed, Message: "TLC check Memory exhausted Java memory", ExitCode: exit}
				if err != nil || !reflect.DeepEqual(r, want) {
					t.Fatalf("memory frame retained evidence or lost its diagnostic: %+v, %v; want %+v", r, err, want)
				}
				f.events(t, 1, c.removed)
			})
		}
	}
}

func TestTLCMemoryFrameRejectsGraphDump(t *testing.T) {
	f := stall470Setup(t, "cat \"$root/transcript\"\nprintf 'digraph G {}\\n' > \"$dump\"\n")
	out := stall470Frame(2193, "Model checking completed.") + stall470Frame(1001, "Java ran out of memory.")
	if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	dot, err := f.runner(time.Second).Dump(stall470Context(t), f.dir, "Memory", stall470Config())
	if dot != "" || err == nil || !strings.Contains(err.Error(), "TLC check Memory exhausted Java memory") {
		t.Fatalf("memory frame accepted unfinished graph: %q, %v", dot, err)
	}
	f.events(t, 1, 0)
}

func TestTLCNonMemoryFramesKeepDiagnostics(t *testing.T) {
	for _, c := range []struct {
		code int
		body string
	}{
		{1000, "Java ran out of stack space."},
		{1004, "Error reading the disk (StatePoolReader)."},
	} {
		t.Run(fmt.Sprint(c.code), func(t *testing.T) {
			f := stall470Setup(t, "cat \"$root/transcript\"\nexit 1\n")
			out := stall470Frame(c.code, c.body)
			if err := os.WriteFile(filepath.Join(f.root, "transcript"), []byte(out), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := f.runner(time.Second).Check(stall470Context(t), f.dir, "Other", stall470Config())
			if err != nil || !reflect.DeepEqual(r, tlc.Parse(out, 1)) {
				t.Fatalf("unrelated failure changed: %+v, %v", r, err)
			}
			f.events(t, 1, 0)
		})
	}
}
