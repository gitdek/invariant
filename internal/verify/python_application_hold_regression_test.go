package verify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Exercise the Go protocol directly, including histories that need not be
// emitted by a conforming collector. The authenticated transport tests also
// exercise these rules against real execution rows.
func TestPythonApplicationHeldCallExplorerAccounting(t *testing.T) {
	declare := func(t *testing.T) *pythonApplicationExplorer {
		t.Helper()
		x := &pythonApplicationExplorer{}
		if err := x.declare([]byte(`{"type":"exploration","steps":[{"name":"Submit","args":[[]],"refusal":{"type":"app.Busy","attributes":{}}},{"name":"Read","args":[[]]},{"name":"Finish","args":[[]]}]}`)); err != nil {
			t.Fatal(err)
		}
		return x
	}
	state := func(value string) map[string]any {
		return map[string]any{"count": json.Number(value)}
	}
	initial := func(run int) PythonApplicationObservation {
		start := 0
		return PythonApplicationObservation{Kind: "initial", Form: "module", Run: run, Instance: run, Start: &start, State: state("0")}
	}
	transition := func(run int, step, operation, before, after string, called, replay bool) PythonApplicationObservation {
		row := PythonApplicationObservation{
			Kind: "transition", Form: "module", Run: run, Instance: run,
			Target: "selected", SourceSHA256: "source", Entry: "surrounding", EntrySHA256: "source",
			Step: step, Operation: operation, Args: []any{}, Before: state(before), After: state(after), Called: &called, Replay: &replay,
		}
		if operation == "release" {
			row.Entry = row.Target
		}
		if step == "Submit" {
			row.Outcome = "accepted"
			if !called {
				row.Outcome = "refused"
				row.Refusal = &PythonApplicationRefusal{Type: "app.Busy", Attributes: map[string]any{}}
			}
		}
		return row
	}
	observe := func(t *testing.T, x *pythonApplicationExplorer, row PythonApplicationObservation) {
		t.Helper()
		if err := x.observe(row); err != nil {
			t.Fatalf("run %d %s %s: %v", row.Run, row.Kind, row.Step, err)
		}
	}
	t.Run("read and replayed refusal preserve the held call", func(t *testing.T) {
		x := declare(t)
		for run := 1; run <= 4; run++ {
			observe(t, x, initial(run))
			observe(t, x, transition(run, "Submit", "hold", "0", "1", true, run > 1))
			if run >= 2 {
				observe(t, x, transition(run, "Read", "", "1", "1", false, run > 2))
			}
			if run >= 3 {
				observe(t, x, transition(run, "Submit", "hold", "1", "1", false, run > 3))
			}
			if x.held == nil || x.held.Run != run || x.held.Outcome != "accepted" {
				t.Fatal("intervening step replaced or lost the original selected start")
			}
			if run == 4 {
				observe(t, x, transition(run, "Finish", "release", "1", "2", false, false))
				if x.held != nil {
					t.Fatal("release retained the completed call")
				}
			}
		}
	})
	t.Run("new run cannot release the previous run's held call", func(t *testing.T) {
		x := declare(t)
		observe(t, x, initial(1))
		observe(t, x, transition(1, "Submit", "hold", "0", "1", true, false))
		observe(t, x, initial(2))
		if err := x.observe(transition(2, "Finish", "release", "0", "2", false, false)); err == nil {
			t.Fatal("release changed state using a held call from another run")
		}
	})
	t.Run("replay binds a hold even when its state is unchanged", func(t *testing.T) {
		x := declare(t)
		observe(t, x, initial(1))
		observe(t, x, transition(1, "Submit", "hold", "0", "0", true, false))
		observe(t, x, initial(2))
		if err := x.observe(transition(2, "Submit", "", "0", "0", true, true)); err == nil {
			t.Fatal("replay replaced a hold with an ordinary completed call")
		}
	})
}

func TestPythonApplicationHeldCallFailureBetweenOperations(t *testing.T) {
	// No step watchdog is running while the driver's take examines its
	// state. A caught handle error must still terminate paused workers.
	driver := strings.Replace(hold393Driver, "def read(life, state):", `def read(life, state):
    if state['active']:
        try:
            life.state
        except AttributeError:
            return`, 1)
	var elapsed time.Duration
	x := hold393Execution(t, false, hold393Source, driver, false, &elapsed)
	if x.Success || x.Complete || x.Exploration != nil || len(x.Observations) != 0 || len(x.Diagnostics) == 0 {
		t.Fatalf("caught handle misuse retained held evidence: %+v", x)
	}
	if elapsed > 25*time.Second {
		t.Fatal("a paused worker blocked shutdown after a failure between operations")
	}
}

func TestPythonApplicationHeldCallReleaseRejectsSecondStart(t *testing.T) {
	// The original target has completed by the time a descendant starts
	// its call. Ownership remains with release until every worker drains.
	source := strings.Replace(hold393Source, "def settle(parent):\n    parent.join()", `def settle(parent):
    parent.join()
    if audit['hang']:
        increment(1)`, 1)
	driver := strings.Replace(hold393Driver, "    life.release(", "    app.audit['hang'] = bool(state['active'])\n    life.release(", 1)
	x := hold393Execution(t, false, source, driver, false)
	if x.Success || x.Complete || x.Exploration != nil || len(x.Observations) != 0 {
		t.Fatalf("release accepted another selected call: %+v", x)
	}
	diagnostic := strings.Join(x.Diagnostics, "\n")
	if !strings.Contains(diagnostic, "second call") || !strings.Contains(diagnostic, "held") {
		t.Fatalf("release lost the held-call failure: %s", diagnostic)
	}
}
