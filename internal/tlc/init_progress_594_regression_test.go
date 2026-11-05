package tlc_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// initProgress594 puts the progress TLC can report while it computes initial
// states, message 2269, before its summary of them, where TLC prints it (#594).
func initProgress594(t *testing.T, out string) string {
	t.Helper()
	at := strings.Index(out, "@!@!@STARTMSG 2190:0 @!@!@")
	if at < 0 {
		t.Fatal("the transcript has no summary of its initial states")
	}
	return out[:at] + graph575Frame(2269, "Computed 2 initial states...") + out[at:]
}

func TestParseTreatsInitialStateProgressAsInformational594(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "passed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// A failed run reports problems, so the message must not be one.
	for exit, outcome := range map[int]tlc.Outcome{0: tlc.Passed, 1: tlc.Failed} {
		plain := tlc.Parse(string(b), exit)
		if plain.Outcome != outcome {
			t.Fatalf("exit %d: %+v", exit, plain)
		}
		if with := tlc.Parse(initProgress594(t, string(b)), exit); !reflect.DeepEqual(with, plain) {
			t.Errorf("exit %d: the progress message changed the result: %+v, want %+v", exit, with, plain)
		}
	}
}

func TestGraphCompletionTreatsInitialStateProgressAsInformational594(t *testing.T) {
	f := graph575Setup(t)
	plain, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config)
	if err != nil {
		t.Fatal(err)
	}
	f = graph575Setup(t)
	graph575Write(t, filepath.Join(f.root, "transcript"), initProgress594(t, graph575Transcript(false)))
	with, err := f.runner.DumpGraph(graph575Context(t), f.dir, "Graph575", f.config)
	if err != nil {
		t.Fatalf("the progress message failed graph completion: %v", err)
	}
	if !reflect.DeepEqual(with.Run, plain.Run) || with.StateCount != plain.StateCount || with.StepCount != plain.StepCount {
		t.Fatalf("the progress message changed the evidence: %+v, want %+v", with, plain)
	}
}
