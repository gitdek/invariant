package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPythonApplicationExecutionRealRunnerSetupFailure(t *testing.T) {
	root, p := pa315Fixture(t, false)
	pa310Write(t, root, pa315TestSource, `import unittest
class TaskTests(unittest.TestCase):
    def setUp(self):
        if self._testMethodName == 'test_pass':
            self.fail('setup assertion diagnostic')
    def test_pass(self):
        self.fail('test body must not run')
    def test_z(self):
        pass
`)
	pa315Select(t, p, "TaskTests.test_pass", "tests.test_tasks")
	before := pa315Tree(t, root)
	opts := pa315Options(new(int))
	opts.Execute = pa315LocalPython(t, t.TempDir())
	s := pa315Prepare(t, p, opts)
	r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
	if r == nil || r.Success || r.ExitCode != 1 || len(r.Tests) != 2 {
		t.Fatalf("setup failure lost observations: %+v %v", r, err)
	}
	first := r.Tests[0]
	diagnostic := strings.Join(first.Diagnostics, "\n")
	if first.ID != "tests.test_tasks.TaskTests.test_pass" || first.Check != pa315Unit(t, s).Key.Check ||
		first.SourceSHA256 != before[pa315TestSource] || first.Outcome != "failed" || first.Exercised ||
		!strings.Contains(diagnostic, "setup assertion diagnostic") || strings.Contains(diagnostic, "test body must not run") {
		t.Fatalf("setup assertion was not retained accurately: %+v", first)
	}
	if next := r.Tests[1]; next.ID != "tests.test_tasks.TaskTests.test_z" || next.Outcome != "passed" || !next.Exercised || next.Check != "" {
		t.Fatalf("later observation lost or misattributed: %+v", next)
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("setup failure changed original bytes")
	}
}

func TestPythonApplicationExecutionOversizedEventPrefix(t *testing.T) {
	for _, which := range []string{"large diagnostic", "complete prefix"} {
		t.Run(which, func(t *testing.T) {
			root, p := pa315Fixture(t, false)
			hash := pa315Tree(t, root)[pa315TestSource]
			opts := pa315Options(new(int))
			var sizeErr error
			opts.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				first := pa315Event(c, hash, "passed")
				var events []byte
				if which == "large diagnostic" {
					last := pa315Event(c, hash, "failed")
					last["id"], last["check"] = "tests.test_tasks.TaskTests.test_fail", ""
					last["diagnostics"] = []string{strings.Repeat("x", pythonApplicationEventsLimit)}
					events = pa315Stream(t, c, first, last, map[string]any{"type": "end", "count": 2})
				} else {
					// Even a complete successful protocol in the prefix must not
					// conceal unvalidated bytes beyond the read limit.
					events = pa315Stream(t, c, first, map[string]any{"type": "end", "count": 1})
					events = append(events, []byte(strings.Repeat("x", pythonApplicationEventsLimit))...)
				}
				if err := os.WriteFile(filepath.Join(c.OutputDir, "events.jsonl"), events, 0600); err != nil {
					t.Fatal(err)
				}
				prefix, err := readPythonApplicationEvents(c.OutputDir)
				if err == nil || !strings.Contains(err.Error(), "exceed") || len(prefix) != pythonApplicationEventsLimit {
					t.Fatalf("oversized events did not retain bounded bytes: %d, %v", len(prefix), err)
				}
				sizeErr = err
				return PythonApplicationProcess{Events: prefix}, err
			}
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
			if r == nil || r.Success || r.Complete || !errors.Is(err, sizeErr) || len(r.Tests) != 1 ||
				r.Tests[0].Outcome != "passed" || !r.Tests[0].Exercised || r.Tests[0].Check != pa315Unit(t, s).Key.Check {
				t.Fatalf("size limit lost valid prefix or granted success: %+v %v", r, err)
			}
		})
	}
}
