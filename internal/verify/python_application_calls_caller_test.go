package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// callerFixture is the statue-shaped fixture with its core, its driver and
// any files beside them replaced: admission is observed, and proved in place.
func callerFixture(t *testing.T, source, driver string, extra map[string]string) pa342Fixture {
	t.Helper()
	f := hold393Fixture(t, true, source, driver)
	pa342Write(t, f.root, calls793Source, calls793Admission)
	for name, body := range extra {
		pa342Write(t, f.root, name, body)
	}
	for _, name := range []string{"__init__.py", "contracts.py"} {
		body, err := naginiRuntime.ReadFile("naginiruntime/nagini_contracts/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pa342Write(t, f.root, "nagini_contracts/"+name, string(body))
	}
	f.p.Manifest.Existing = append(f.p.Manifest.Existing, calls793Source)
	f.p.Manifest.Components.Checks[0].Observe = []project.ComponentRef{pa342Ref(calls793Source, "admission")}
	target := pa342Ref(calls793Source, "admission")
	f.p.Manifest.Components.Checks = append(f.p.Manifest.Components.Checks, project.ComponentCheck{
		Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}},
	})
	pa342Save(t, f)
	return f
}

// callers is the files recorded as having made admission's calls, on
// attempts that aren't replays.
func callers(r *Report) map[string]int {
	made := map[string]int{}
	for _, x := range r.Application.Executions {
		for _, row := range x.Observations {
			if row.Replay == nil || *row.Replay {
				continue
			}
			for _, call := range row.Calls {
				made[call.Caller]++
			}
		}
	}
	return made
}

// A proof qualifies by observed calls only when the application's own code
// makes them (#793). The review of #807 found two ways a driver could make
// them itself, inside an entry, and have the gate report the function
// proved in an application that never calls it: an entry of its own beside
// the application, and a callback handed to the application's entry. Each
// call now names the staged file whose code made it, and one made outside
// the application's existing sources counts for nothing.
func TestObservedCallsCountOnlyFromTheApplication793(t *testing.T) {
	application := "from .admission import admission\n" + hold393Source
	application = strings.Replace(application, "if counts['active'] != 0:\n            raise Refused(409)",
		"status = admission(True, 'statue', 'engine', counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)", 1)
	if application == "from .admission import admission\n"+hold393Source {
		t.Fatal("the fixture's busy check moved")
	}
	hooked := strings.Replace(hold393Source, "async def submit(value, *, hold='forwarded'):\n    assert value == 1 and hold == 'forwarded'\n",
		"async def submit(value, *, hold='forwarded', hook=None):\n    assert value == 1 and hold == 'forwarded'\n    if hook is not None:\n        hook()\n", 1)
	if hooked == hold393Source {
		t.Fatal("the fixture's entry moved")
	}
	wrapperDriver := strings.Replace(hold393Driver, "from counter import core as app\n", "from counter import core as app\nfrom tests import wrapper\n", 1)
	wrapperDriver = strings.Replace(wrapperDriver, "life.hold(app.submit,", "life.hold(wrapper.submit,", 1)
	callbackDriver := strings.Replace(hold393Driver, "from counter import core as app\n", "from counter import core as app\nfrom counter.admission import admission\n", 1)
	callbackDriver = strings.Replace(callbackDriver, "life.hold(app.submit, value, hold='forwarded',",
		"life.hold(app.submit, value, hold='forwarded', hook=lambda: admission(True, 'statue', 'engine', state['active'] != 0),", 1)
	if wrapperDriver == hold393Driver || callbackDriver == hold393Driver {
		t.Fatal("the fixture's driver moved")
	}
	for _, c := range []struct {
		name, source, driver string
		extra                map[string]string
		caller               string
		proved               bool
	}{
		{"the application calls it", application, hold393Driver, nil, pa342Source, true},
		{"an entry beside the application calls it", hold393Source, wrapperDriver, map[string]string{
			"tests/wrapper.py": "from counter import core\nfrom counter.admission import admission\n\nasync def submit(value, *, hold='forwarded'):\n    admission(True, 'statue', 'engine', core.counts['active'] != 0)\n    await core.submit(value, hold=hold)\n",
		}, "tests/wrapper.py", false},
		{"the driver's callback calls it", hooked, callbackDriver, nil, "verification/counter/driver.py", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := callerFixture(t, c.source, c.driver, c.extra)
			tc, _ := hold393GateFake(t)
			gp359Verifier(t, false)
			if err := Check(f.p.Dir); err != nil {
				t.Fatalf("check: %v", err)
			}
			r := pa342Unchanged(t, f, tc)
			made := callers(r)
			if len(made) != 1 || made[c.caller] == 0 {
				t.Fatalf("calls were made by %v; want only %s", made, c.caller)
			}
			proof := calls793Proof(t, r)
			problems := strings.Join(PythonApplicationProofProblems(r.Components, r.Application), "\n")
			if c.proved {
				if !r.Passed || proof.Status != "proved" || problems != "" {
					t.Fatalf("the application's own calls didn't qualify the proof: passed=%v status=%s\n%s", r.Passed, proof.Status, problems)
				}
				return
			}
			if r.Passed || proof.Status == "proved" {
				t.Fatalf("a proof qualified by calls the application never made: passed=%v status=%s", r.Passed, proof.Status)
			}
			if !strings.Contains(problems, "any completed call by the application's own code") {
				t.Errorf("the problem doesn't say whose calls are missing:\n%s", problems)
			}
		})
	}
}

// A caller is a file the run staged. A record naming any other is refused,
// as a record naming a foreign target is, so an edited receipt can't move a
// call into the application.
func TestObservedCallCallersAreStaged793(t *testing.T) {
	declaration := project.Component{ID: "python:counter.admission#admission@counter/admission.py", SourceSHA256: "sha256:abc"}
	files := map[string]string{"counter/core.py": "sha256:1", "notes.txt": "sha256:2"}
	replay, called := false, false
	for caller, ok := range map[string]bool{"": true, "counter/core.py": true, "counter/other.py": false, "notes.txt": false, "../outside.py": false} {
		row := PythonApplicationObservation{Kind: "transition", Entry: "python:counter.core#submit@counter/core.py", Replay: &replay, Called: &called,
			Calls: []PythonApplicationCall{{Target: declaration.ID, SourceSHA256: declaration.SourceSHA256, Caller: caller, Arguments: map[string]any{}, Result: nil}}}
		err := pythonApplicationValidateCalls(row, []project.Component{declaration}, files, true)
		if ok != (err == nil) {
			t.Errorf("caller %q: %v", caller, err)
		}
	}
	for caller, own := range map[string]bool{"": false, "counter/core.py": true, "counter/pkg/deep.py": true, "counter": false, "counterfeit/core.py": false,
		"verification/counter/driver.py": false, "tests/wrapper.py": false} {
		call := PythonApplicationCall{Caller: caller}
		if got := pythonApplicationOwnCall(call, []string{"counter/core.py", "counter/pkg", "verification/counter/driver.py"}, "verification/counter/driver.py"); got != own {
			t.Errorf("caller %q counted as the application's own = %v", caller, got)
		}
	}
}

// Calls an entry's worker threads make complete in an order that varies,
// while the calls made don't. A replay is compared with its attempt by the
// calls made, so such an application still explores.
func TestObservedCallsOnWorkerThreadsReplay793(t *testing.T) {
	source := "from .admission import admission\nimport random, time\n" + hold393Source
	source = strings.Replace(source, "def reset():", "def audit_call(n):\n    time.sleep(random.random() / 200)\n    admission(True, 'statue', 'engine', n == 1)\ndef reset():", 1)
	source = strings.Replace(source, "if counts['active'] != 0:\n            raise Refused(409)",
		"status = admission(True, 'statue', 'engine', counts['active'] != 0)\n        if status != 0:\n            raise Refused(status)", 1)
	started := strings.Replace(source, "    threading.Thread(target=prepare).start()\n",
		"    threading.Thread(target=prepare).start()\n    threading.Thread(target=audit_call, args=(0,)).start()\n    threading.Thread(target=audit_call, args=(1,)).start()\n", 1)
	if started == source {
		t.Fatal("the fixture's worker moved")
	}
	for i := range 3 {
		f := callerFixture(t, started, hold393Driver, nil)
		s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
		x, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
		if err != nil || x == nil || !x.Success {
			t.Fatalf("run %d: worker threads' calls made the exploration fail: %v", i, err)
		}
	}
}

// An argument the collector can't copy fails the run with the function, the
// parameter and what's allowed, not a message about read-back.
func TestObservedCallArgumentsSayWhatIsAllowed793(t *testing.T) {
	f := observedCallsFixture(t, "def decision(pair):\n    return 0\n", "        decision((1, 2))")
	s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
	x, _ := s.Run(pa321Context(t), pa318Driver(t, s).Key)
	if x == nil || x.Success {
		t.Fatal("a tuple argument was recorded")
	}
	if got := strings.Join(x.Diagnostics, "\n"); !strings.Contains(got, "observed function decision: argument pair must be a JSON scalar, or a list or a dictionary with string keys of them") {
		t.Errorf("the diagnostic doesn't name the function, the parameter and what's allowed:\n%s", got)
	}
}
