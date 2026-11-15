package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

// These use the same application fixtures with the actual collector locally
// and, in the existing real lane, the pinned Python image and executor.
func TestPythonApplicationBugReplayRuntime935(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" && os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") != "1" {
			continue
		}
		t.Run(fmt.Sprintf("real=%v", real), func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
				for _, mode := range []string{"fixed", "replay", "reuse", "both", "alternative"} {
					t.Run(fmt.Sprintf("explicit=%v/%s", explicit, mode), func(t *testing.T) {
						t.Parallel()
						root, p := pa935Fixture(t, explicit, mode, "")
						before := pa315Tree(t, root)
						selected := 0
						s := pa315Prepare(t, p, pa318Options(t, real, &selected))
						for _, second := range []bool{false, true} {
							q := pa935Request(t, p, second)
							x := pa935Run(t, s, q)
							last := x.BugReplay.Result.Calls[q.Path.Offending]
							broken := mode == "both" || mode == "replay" && !second || mode == "reuse" && second
							if last.After.Equal(q.Path.States[4]) != broken || (last.Outcome == "refused") != (mode != "alternative" && !broken) {
								t.Fatalf("wrong real entry outcome: %+v", last)
							}
						}
						if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
							t.Fatal("replay changed source or selected another image")
						}
					})
				}
			}
			for _, mode := range []string{"missing", "exception", "cleanup", "cancel"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					pa935RuntimeFailure(t, real, mode)
				})
			}
		})
	}
}

func pa935RuntimeFailure(t *testing.T, real bool, mode string) {
	t.Helper()
	driver := pa935Driver
	if mode == "missing" {
		driver = strings.Replace(driver, "    if replay(start, steps + bugs):\n        return\n", "", 1)
	}
	root, p := pa935Fixture(t, true, "fixed", driver)
	if mode == "exception" || mode == "cancel" {
		b, err := os.ReadFile(filepath.Join(root, pa315Target))
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(b), "raise Refused(409)", "raise Refused(410)", 1)
		if mode == "cancel" {
			body = strings.Replace(string(b), "def recover():", "def recover():\n    import os, time\n    from pathlib import Path\n    Path(os.environ['APP_SIGNAL']).write_text('entered')\n    time.sleep(60)", 1)
		}
		pa310Write(t, root, pa315Target, body)
	}
	opts := pa318Options(t, real, new(int))
	execute := opts.Execute
	if execute == nil {
		execute = RunPythonApplicationCommand
	}
	var ctx context.Context
	var cancel context.CancelFunc
	var output string
	var reached bool
	opts.Execute = func(cctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		output = c.OutputDir
		if mode == "cancel" {
			signal := filepath.Join(c.OutputDir, "entry-ready")
			c.Env["APP_SIGNAL"] = signal
			if real {
				c.Env["APP_SIGNAL"] = "/output/entry-ready"
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					if _, err := os.Stat(signal); err == nil {
						reached = true
						cancel()
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
			}()
			x, err := execute(cctx, c)
			cancel()
			<-done
			return x, err
		}
		x, err := execute(cctx, c)
		if mode == "cleanup" {
			err = errors.Join(err, errors.New("fixture cleanup failed after real execution"))
		}
		return x, err
	}
	s := pa315Prepare(t, p, opts)
	ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	x, err := s.RunBugReplay(ctx, pa318Driver(t, s).Key, pa935Request(t, p, false))
	pa935Rejected(t, x, err)
	if mode == "cancel" && (!reached || !errors.Is(err, context.Canceled)) {
		t.Fatalf("did not cancel a running application entry: reached=%v err=%v", reached, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed replay retained private output: %v", err)
	}
}

func TestPythonApplicationBugReplayStartsAndArguments935(t *testing.T) {
	t.Parallel()
	driver := strings.Replace(pa935Driver, "def deliver(life, state, identity, name):", "def other_start():\n    life = start()\n    app.state['phase'] = 9\n    return life\ndef deliver(life, state, identity, name):", 1)
	driver = strings.Replace(driver, "replay(start, steps + bugs)", "replay([other_start, start], steps + bugs)", 1)
	// Enumeration lists cannot limit the requested arguments.
	driver = strings.ReplaceAll(driver, "[[9007199254740993, 'job']]", "[[0, 'unrelated']]")
	_, p := pa935Fixture(t, true, "fixed", driver)
	s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
	x := pa935Run(t, s, pa935Request(t, p, false))
	if len(x.Observations) != 6 || x.Observations[0].Sequence != 0 || x.Observations[1].Run != 2 || x.BugReplay.Result.Outcome != "refused" {
		t.Fatalf("starts were omitted or counted as calls: %+v", x)
	}
}

func TestPythonApplicationBugReplayUnicodeReadback935(t *testing.T) {
	t.Parallel()
	const note = "<task>&\"\n😀\u2028"
	driver := strings.Replace(pa935Driver, "'reused':'state.reused'", "'reused':'state.reused', 'note':'note'", 1)
	root, p := pa935Fixture(t, true, "fixed", driver)
	b, err := os.ReadFile(filepath.Join(root, pa315Target))
	if err != nil {
		t.Fatal(err)
	}
	literal, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	pa310Write(t, root, pa315Target, "note = "+string(literal)+"\n"+string(b))
	value := tlc.ReplayValue{Kind: "string", Atom: note}
	text, err := value.TLA()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(pa935Model, "VARIABLE phase, deliveries, reused", "VARIABLE phase, deliveries, reused, note", 1)
	body = strings.ReplaceAll(body, "<<phase, deliveries, reused>>", "<<phase, deliveries, reused, note>>")
	body = strings.Replace(body, "Init == phase = 0 /\\ deliveries = 0 /\\ reused = FALSE", "Init == phase = 0 /\\ deliveries = 0 /\\ reused = FALSE /\\ note = "+text, 1)
	body = strings.ReplaceAll(body, "UNCHANGED reused", "UNCHANGED <<reused, note>>")
	body = strings.ReplaceAll(body, "UNCHANGED <<deliveries, reused>>", "UNCHANGED <<deliveries, reused, note>>")
	body = strings.ReplaceAll(body, "UNCHANGED <<phase, reused>>", "UNCHANGED <<phase, reused, note>>")
	body = strings.ReplaceAll(body, "UNCHANGED phase", "UNCHANGED <<phase, note>>")
	path := pa935Request(t, p, false).Path
	for _, state := range path.States {
		state["note"] = value
	}
	q := pa935Rebind(t, p, path, body)
	x := pa935Run(t, pa315Prepare(t, p, pa318Options(t, false, new(int))), q)
	if x.BugReplay.Result.Initial["note"].Atom != note || x.BugReplay.Result.Calls[3].After["note"].Atom != note {
		t.Fatal("JSON escaping changed replay read-back")
	}
}

func TestPythonApplicationBugReplayMissingPaths935(t *testing.T) {
	t.Parallel()
	_, p := pa935Fixture(t, true, "fixed", "")
	v := p.Manifest.Components.Checks[0].Invocations[1]
	v.ID = "second"
	p.Manifest.Components.Checks[0].Invocations = append(p.Manifest.Components.Checks[0].Invocations, v)
	pa310Save(t, p)
	opts := pa318Options(t, false, new(int))
	opts.Execute = func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error) {
		t.Fatal("missing paths must not execute a substitute path")
		return PythonApplicationProcess{}, nil
	}
	s := pa315Prepare(t, p, opts)
	r := &Report{Application: &PythonApplicationReport{}}
	for _, name := range []string{"ReplayCompletedTask", "ReuseRecoveredName"} {
		r.Bugs = append(r.Bugs, Bug{Name: name, Caught: true, CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "replay-unavailable", Message: "replay-ambiguous-edge", Counterexample: &tlc.Result{Message: "retained counterexample"}}})
	}
	if err := runPythonApplicationBugReplays(context.Background(), s, r, tlc.Runner{}); err != nil {
		t.Fatal(err)
	}
	if len(r.Application.BugReplays) != 4 || len(r.Application.Executions) != 0 || activateBugReplays(r) {
		t.Fatalf("missing obligations disappeared: %+v", r.Application)
	}
	for _, row := range r.Application.BugReplays {
		if row.Bug == "" || row.Evidence.Passed || row.Evidence.Request != nil || row.Evidence.Counterexample == nil || !strings.Contains(row.Evidence.Message, row.Bug) || !strings.Contains(row.Evidence.Message, row.Key.ID) {
			t.Fatalf("unnamed or fabricated obligation: %+v", row)
		}
	}
}
