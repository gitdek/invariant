package verify

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPythonHarnessFailureBeforeTerminatingCleanup(t *testing.T) {
	t.Parallel()
	// The selected worker is already paused when the expectation fails.
	// Cleanup must terminate the runner, but cannot erase that first cause.
	driver := strings.Replace(hold393Driver, "expected_after=expected)", "expected_after={'count':-1})", 1)
	x := hold393Execution(t, false, hold393Source, driver, false)
	if x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil || x.ObservationMode != "" {
		t.Fatalf("failed cleanup retained evidence: %+v", x)
	}
	if x.HarnessError != "expected state differs from application read-back" {
		t.Fatalf("cleanup replaced first cause: %q; %q", x.HarnessError, x.Diagnostics)
	}
	for _, want := range []string{x.HarnessError, "module observation timed out or left unfinished work", "Traceback"} {
		if !strings.Contains(strings.Join(x.Diagnostics, "\n"), want) {
			t.Fatalf("missing diagnostic %q: %q", want, x.Diagnostics)
		}
	}
}

func TestPythonHarnessWrappedFailureBeforeTerminatingCleanup(t *testing.T) {
	t.Parallel()
	// A second operation in one take fails while the first call is held.
	// The outer wrapper must survive the same terminating cleanup as its cause.
	driver := strings.Replace(hold393Driver, "def finish(life, state):", `    try:
        life.release()
    except Exception as error:
        raise RuntimeError('wrapper before terminating cleanup') from error
def finish(life, state):`, 1)
	x := hold393Execution(t, false, hold393Source, driver, false)
	if x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil {
		t.Fatalf("failed cleanup retained evidence: %+v", x)
	}
	if !strings.Contains(x.HarnessError, "at most once") {
		t.Fatalf("cleanup replaced first cause: %q; %q", x.HarnessError, x.Diagnostics)
	}
	for _, want := range []string{x.HarnessError, "wrapper before terminating cleanup", "module observation timed out or left unfinished work"} {
		if !strings.Contains(strings.Join(x.Diagnostics, "\n"), want) {
			t.Fatalf("missing diagnostic %q: %q", want, x.Diagnostics)
		}
	}
}

func TestPythonHarnessDiagnosticDoesNotAuthorizeObservations(t *testing.T) {
	t.Parallel()
	for _, sealed := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "unsealed"}[sealed], func(t *testing.T) {
			_, p := pa318Fixture(t, false, pa318Functional, false)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				process, err := execute(ctx, c)
				if err != nil {
					t.Fatalf("valid runner failed: %v; %s", err, process.Stderr)
				}
				message, _ := json.Marshal(c.Root + "/cause\n<script>literal</script>")
				process.Stderr += "\nINVARIANT_HARNESS_ERROR " + string(message) + "\n"
				if !sealed {
					// Leave the real observations intact, but remove their seal.
					i := strings.LastIndex(string(process.Events[:len(process.Events)-1]), "\n")
					process.Events = process.Events[:i+1]
				}
				return process, nil
			}
			s := pa315Prepare(t, p, opts)
			x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			if sealed {
				if err != nil || !x.Success || !x.Complete || x.HarnessError != "" || len(x.Observations) == 0 {
					t.Fatalf("diagnostic text changed valid evidence: %+v; %v", x, err)
				}
			} else {
				pa318Reject(t, x, err)
				if x.HarnessError != "/src/cause\n<script>literal</script>" {
					t.Fatalf("diagnostic was not decoded and normalized: %q", x.HarnessError)
				}
			}
			fingerprint := pythonApplicationExecutionFingerprint(*x)
			x.HarnessError, x.Diagnostics = "different diagnostic", []string{"different context"}
			if pythonApplicationExecutionFingerprint(*x) != fingerprint {
				t.Fatal("diagnostics changed execution fingerprint")
			}
			x.ExitCode++
			if pythonApplicationExecutionFingerprint(*x) == fingerprint {
				t.Fatal("execution outcome no longer affects fingerprint")
			}
		})
	}
}

func TestPythonHarnessFailureDuringRefusalMatching(t *testing.T) {
	t.Parallel()
	source := strings.Replace(hold393Source, "self.status_code = status_code", "self._status_code = status_code", 1)
	source = strings.Replace(source, "def reset():", `    @property
    def status_code(self):
        try:
            increment(1)
        except Exception:
            pass
        return 410
def reset():`, 1)
	x := hold393Execution(t, false, source, hold393Driver, false)
	if x.Success || x.Complete || len(x.Observations) != 0 || x.Exploration != nil {
		t.Fatalf("a refusal getter's caught collector error earned evidence: %+v", x)
	}
	if !strings.Contains(x.HarnessError, "selected call") || strings.Contains(strings.Join(x.Diagnostics, "\n"), "application call raised an undeclared or invalid refusal") {
		t.Fatalf("collector failure became a refusal mismatch: %q; %q", x.HarnessError, x.Diagnostics)
	}
}

func TestPythonHarnessFirstDiagnosticRecord(t *testing.T) {
	t.Parallel()
	const cause = "first\n<script>literal</script> | `tick`"
	encoded, err := json.Marshal(cause)
	if err != nil {
		t.Fatal(err)
	}
	stderr := "INVARIANT_HARNESS_ERROR not JSON\nINVARIANT_HARNESS_ERROR " + string(encoded) +
		"\n" + strings.Repeat("later traceback\n", 30) + "INVARIANT_HARNESS_ERROR \"poisoned\"\n"
	if got := pythonApplicationHarnessError(stderr); got != cause {
		t.Fatalf("first diagnostic replaced: %q", got)
	}
	var old PythonApplicationExecutionResult
	if err := json.Unmarshal([]byte(`{"complete":false,"success":false,"diagnostics":["old failure"]}`), &old); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(old)
	if err != nil || strings.Contains(string(b), "harness_error") {
		t.Fatalf("old receipt invented a harness error: %s; %v", b, err)
	}
}

func TestPythonHarnessReceiptPreservesIndependentExploredArguments(t *testing.T) {
	t.Parallel()
	// A larger failure's receipt also retains the successful ordinary run,
	// including the empty argument lists on its accepted explored attempts.
	for _, raw := range []string{
		`{"kind":"transition","args":[],"outcome":"refused","refusal":{"type":"app.Refused","attributes":{"status_code":409}}}`,
		`{"kind":"transition","args":[9007199254740993]}`,
		`{"kind":"transition"}`,
	} {
		var before, after PythonApplicationObservation
		if err := json.Unmarshal([]byte(raw), &before); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("observation changed on round trip: before=%#v after=%#v", before, after)
		}
	}
}
