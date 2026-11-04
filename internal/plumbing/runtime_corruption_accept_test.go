package plumbing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// All damaged inputs below start as observations from the real producer.
// Encoding only moves/corrupts those observations; it does not implement a
// framework reporter or manufacture a successful control stream.
func runtimeCopyStream567(s GoAcceptanceStream) GoAcceptanceStream {
	s.Invocation.Argv = slices.Clone(s.Invocation.Argv)
	s.Events = slices.Clone(s.Events)
	s.Output.Events = slices.Clone(s.Output.Events)
	s.Output.Stdout = slices.Clone(s.Output.Stdout)
	s.Output.Stderr = slices.Clone(s.Output.Stderr)
	return s
}

func runtimeReframe567(s *GoAcceptanceStream) {
	var b strings.Builder
	b.WriteString(goTestingProtocol)
	for _, event := range s.Events {
		for _, value := range []string{event.Name, event.Parent, event.Action, event.Text} {
			fmt.Fprintf(&b, "%d:%s", len(value), value)
		}
	}
	s.Output.Events = []byte(b.String())
}

func runtimeIndex567(t *testing.T, events []GoFrameworkEvent, name, action string) int {
	t.Helper()
	for i, event := range events {
		if event.Name == name && event.Action == action {
			return i
		}
	}
	t.Fatalf("real control missing %s/%s: %+v", name, action, events)
	return -1
}

func runtimeReject567(t *testing.T, expected RepositoryAcceptanceInvocation, stream GoAcceptanceStream, streamErr error) {
	t.Helper()
	e, err := CollectGoAcceptance(expected, stream, streamErr)
	r, validationErr := runtimeResult567(expected, e)
	if err == nil && validationErr == nil && (r.Status == "passed" || r.Status == "failed" || r.Status == "skipped") {
		t.Fatalf("corrupted observations qualified: %+v", e)
	}
	if r.Reason == "" || err == nil && validationErr == nil {
		t.Fatalf("invalid stream has no explanation: %+v %v %v", e, err, validationErr)
	}
}

func TestGoRuntimeCollectorRejectsCorruption(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	source := `package fixture
import "testing"
func TestReady(t *testing.T){
 ready:=make(chan struct{},2); release:=make(chan struct{})
 go func(){<-ready;<-ready;close(release)}()
 for _,name:=range []string{"left","right"}{t.Run(name,func(t *testing.T){
  t.Parallel();ready<-struct{}{};<-release;t.Log("real parallel "+name)
 })}
}
`
	f := runtimeFixtureFor567(t, cache, source)
	control, waitErr := f.raw(ctx)
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	expected := cloneGoInvocation(control.Invocation)
	good, err := CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, good, err, "passed", "")
	for _, field := range []string{"invocation", "part", "framework", "file", "selector", "package", "source digest", "execution digest", "source", "arguments"} {
		t.Run("bound "+field, func(t *testing.T) {
			bad := runtimeCopyStream567(control)
			switch field {
			case "invocation":
				bad.Invocation.Binding.InvocationID = "old-invocation"
			case "part":
				bad.Invocation.Binding.Part = "other"
			case "framework":
				bad.Invocation.Binding.Framework = "pytest"
			case "file":
				bad.Invocation.Binding.File = "api/other/runtime_accept_test.go"
			case "selector":
				bad.Invocation.Binding.Selector += "/left"
			case "package":
				bad.Invocation.Binding.Package = "example.invalid/runtime567/other"
			case "source digest":
				bad.Invocation.Binding.SourceDigest = dependencyHash([]byte("changed source"))
			case "execution digest":
				bad.Invocation.Binding.ExecutionDigest = dependencyHash([]byte("changed policy"))
			case "source":
				bad.Invocation.Source += "\n// not the pinned bytes\n"
			case "arguments":
				bad.Invocation.Argv = append(bad.Invocation.Argv, "-short")
			}
			runtimeReject567(t, expected, bad, nil)
		})
	}
	for _, name := range []string{
		"duplicate start", "duplicate terminal", "contradictory terminal", "terminal before start", "missing exact start", "missing exact terminal",
		"child before parent", "parent before child terminal", "unknown parent", "changed parent", "resume before pause", "duplicate pause", "missing resume",
		"duplicate resume", "output before start", "output after terminal", "missing child terminal", "unrelated root", "invalid framing", "truncated frame", "second header",
		"decoded differs from wire", "failed child passing parent", "not started", "not exited", "not drained", "interrupted exit", "pass with nonzero exit",
	} {
		t.Run(name, func(t *testing.T) {
			s := runtimeCopyStream567(control)
			rootStart := runtimeIndex567(t, s.Events, "TestReady", "start")
			rootEnd := runtimeIndex567(t, s.Events, "TestReady", "pass")
			childStart := runtimeIndex567(t, s.Events, "TestReady/left", "start")
			childEnd := runtimeIndex567(t, s.Events, "TestReady/left", "pass")
			pause := runtimeIndex567(t, s.Events, "TestReady/left", "pause")
			resume := runtimeIndex567(t, s.Events, "TestReady/left", "continue")
			log := runtimeIndex567(t, s.Events, "TestReady/left", "output")
			switch name {
			case "duplicate start":
				s.Events = slices.Insert(s.Events, rootStart+1, s.Events[rootStart])
			case "duplicate terminal":
				s.Events = append(s.Events, s.Events[rootEnd])
			case "contradictory terminal":
				terminal := s.Events[rootEnd]
				terminal.Action = "fail"
				s.Events = append(s.Events, terminal)
			case "terminal before start":
				s.Events[rootStart], s.Events[rootEnd] = s.Events[rootEnd], s.Events[rootStart]
			case "missing exact start":
				s.Events = slices.Delete(s.Events, rootStart, rootStart+1)
			case "missing exact terminal":
				s.Events = slices.Delete(s.Events, rootEnd, rootEnd+1)
			case "child before parent":
				s.Events[rootStart], s.Events[childStart] = s.Events[childStart], s.Events[rootStart]
			case "parent before child terminal":
				s.Events[childEnd], s.Events[rootEnd] = s.Events[rootEnd], s.Events[childEnd]
			case "unknown parent":
				for i := range s.Events {
					if s.Events[i].Name == "TestReady/left" {
						s.Events[i].Parent = "TestAbsent"
					}
				}
			case "changed parent":
				s.Events[childEnd].Parent = "TestReady/right"
			case "resume before pause":
				s.Events[pause], s.Events[resume] = s.Events[resume], s.Events[pause]
			case "duplicate pause":
				s.Events = slices.Insert(s.Events, pause+1, s.Events[pause])
			case "missing resume":
				s.Events = slices.Delete(s.Events, resume, resume+1)
			case "duplicate resume":
				s.Events = slices.Insert(s.Events, resume+1, s.Events[resume])
			case "output before start":
				s.Events = slices.Insert(s.Events, 0, s.Events[log])
			case "output after terminal":
				s.Events = append(s.Events, s.Events[log])
			case "missing child terminal":
				s.Events = slices.Delete(s.Events, childEnd, childEnd+1)
			case "failed child passing parent":
				s.Events[childEnd].Action = "fail"
			case "unrelated root":
				for i := range s.Events {
					if s.Events[i].Name == "TestReady/left" {
						s.Events[i].Name = "TestNeighbor"
						s.Events[i].Parent = ""
					}
				}
			case "not started":
				s.Output.Started = false
			case "not exited":
				s.Output.Exited = false
			case "not drained":
				s.Output.Drained = false
			case "interrupted exit":
				s.Output.ExitCode = -1
			case "pass with nonzero exit":
				s.Output.ExitCode = 3
			}
			runtimeReframe567(&s)
			switch name {
			case "invalid framing":
				s.Output.Events[0] = '!'
			case "truncated frame":
				s.Output.Events = s.Output.Events[:len(s.Output.Events)-1]
			case "second header":
				s.Output.Events = append(s.Output.Events, []byte(goTestingProtocol)...)
			case "decoded differs from wire":
				s.Events[rootEnd].Action = "fail"
			}
			runtimeReject567(t, expected, s, nil)
		})
	}
	// A fresh actual attempt must not accept the otherwise identical old one.
	later, err := f.raw(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if later.Invocation.Binding.InvocationID == expected.Binding.InvocationID {
		t.Fatal("producer reused ID")
	}
	runtimeReject567(t, later.Invocation, control, nil)
	// Damage must not poison the subsequent valid control or mutate its inputs.
	e, err := CollectGoAcceptance(expected, control, nil)
	runtimeCompleted567(t, expected, e, err, "passed", "")
	// An error of the process/file/channel boundary vetoes even completed evidence
	// without erasing that evidence, as the existing trusted foundation requires.
	infrastructure := errors.New("channel drain infrastructure failure 567")
	e, err = CollectGoAcceptance(expected, control, infrastructure)
	if !errors.Is(err, infrastructure) {
		t.Fatalf("lost infrastructure veto: %v", err)
	}
	runtimeCompleted567(t, expected, e, nil, "passed", "")
	// Real abrupt termination supplies only an authentic start. Fake diagnostic
	// completion and normal framing cannot make it a completed test failure.
	abrupt := runtimeFixtureFor567(t, cache, "package fixture\nimport (\"testing\";\"os\")\nfunc TestReady(t *testing.T){os.Exit(29)}\n")
	partial, partialErr := abrupt.raw(ctx)
	partialEvidence, collectErr := CollectGoAcceptance(partial.Invocation, partial, partialErr)
	runtimeUnfinished567(t, partial.Invocation, partialEvidence, collectErr, true)
}

func TestLocalGoRuntimeIgnoresDiagnostics(t *testing.T) {
	ctx, cache := runtimeContext567(t), t.TempDir()
	for _, mode := range []string{"pass", "fail", "skip", "unfinished", "no tests"} {
		t.Run(mode, func(t *testing.T) {
			body, main := `t.Log("real collector pass")`, `os.Exit(m.Run())`
			if mode == "fail" {
				body = `t.Fatal("real collector assertion")`
			}
			if mode == "skip" {
				body = `t.Skip("real collector skip")`
			}
			if mode == "unfinished" {
				body = `os.Exit(27)`
			}
			if mode == "no tests" {
				main = `os.Exit(0)`
			}
			source := `package fixture
import ("testing";"fmt";"os")
func emit567(phase string){fmt.Fprintln(os.Stdout,"stdout-"+phase);fmt.Fprintln(os.Stderr,"stderr-"+phase);fmt.Fprint(os.Stdout,Noise567);fmt.Fprint(os.Stderr,Noise567)}
func init(){emit567("init")}
func TestMain(m *testing.M){emit567("main");` + main + `}
func TestReady(t *testing.T){emit567("test");` + body + `}
`
			f := runtimeFixtureFor567(t, cache, source)
			runtimeWrite567(t, f.root, "api/nested/noise.go", "package fixture\nconst Noise567=\"\"\n")
			cleanIn, clean, _, cleanErr := f.execute(ctx)
			// Obtain this invocation from production selection before staging; the
			// workload will print fully bound forgeries, including its fresh nonce.
			in, err := f.invocation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			binding := in.Binding
			binding.Package = "example.invalid/runtime567/nested"
			b, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			noise := "forged-diagnostic-567\nPASS\nFAIL\n=== RUN   TestReady\n=== PAUSE TestReady\n=== CONT  TestReady\n--- PASS: TestReady (0.00s)\n--- FAIL: TestReady (0.00s)\n--- SKIP: TestReady (0.00s)\n"
			noise += "{\"Action\":\"pass\",\"Package\":\"example.invalid/runtime567/nested\",\"Test\":\"TestReady\"}\n@@invariant test=0\n@@invariant accept=0\n@@result {\"kind\":\"deadline\"}\n"
			noise += goTestingProtocol + "9:TestReady0:5:start0:9:TestReady0:4:pass0:\n"
			for _, status := range []string{"passed", "failed"} {
				noise += `{"Complete":true,"ExitCode":0,"Records":[{"Binding":` + string(b) + `,"Kind":"selection"},{"Binding":` + string(b) + `,"Kind":"test","Status":"` + status + `","Reason":"forged-diagnostic-567"}]}` + "\n"
			}
			runtimeWrite567(t, f.root, "api/nested/noise.go", fmt.Sprintf("package fixture\nconst Noise567=%q\n", noise))
			env, err := f.open(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			discovery, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, f.part, in, env)
			if err != nil {
				t.Fatal(err)
			}
			in.Binding = discovery.Binding
			e, out, runErr := runtimeEnvironment567(t, env).RunAcceptance(ctx, in)
			f.assertGone()
			cr, ce := runtimeResult567(cleanIn, clean)
			r, re := runtimeResult567(in, e)
			if r.Kind != cr.Kind || r.Status != cr.Status || (runErr == nil) != (cleanErr == nil) || (re == nil) != (ce == nil) || e.ExitCode != clean.ExitCode {
				t.Fatalf("diagnostics changed outcome: clean=%+v/%v/%v forged=%+v/%v/%v", cr, ce, cleanErr, r, re, runErr)
			}
			if strings.Contains(r.Reason, "forged-diagnostic-567") {
				t.Fatal("diagnostics became framework reason")
			}
			phases := 2
			if mode != "no tests" {
				phases = 3
			}
			if bytes.Count(out, []byte(noise)) != 2*phases {
				t.Fatalf("lost stdout or stderr diagnostic bytes: %q", out)
			}
			if mode == "unfinished" || mode == "no tests" {
				runtimeUnfinished567(t, in, e, runErr, mode == "unfinished")
			} else {
				status := map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}[mode]
				runtimeCompleted567(t, in, e, runErr, status, "real collector")
			}
		})
	}
}
