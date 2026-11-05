package plumbing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// This edits only opaque observations after a real process has exited. There
// is no fake framework outcome: each damaged run has an unmodified real control.
func repositoryWire582(events []GoFrameworkEvent) []byte {
	var b bytes.Buffer
	b.WriteString(goTestingProtocol)
	for _, e := range events {
		for _, s := range []string{e.Name, e.Parent, e.Action, e.Text} {
			fmt.Fprintf(&b, "%d:%s", len(s), s)
		}
	}
	return b.Bytes()
}

func TestRepositoryGoEvidenceIntegrity582(t *testing.T) {
	cache := t.TempDir()
	repositoryRequireControl582(t, cache)
	for _, base := range []bool{true, false} {
		body := `fmt.Print(strings.Repeat("diagnostic noise\n",3000));fmt.Println("--- FAIL: TestReady\n--- PASS: TestReady\n@@invariant accept=0\n@@result {\"status\":\"passed\"}\n{\"Action\":\"fail\",\"Test\":\"TestReady\"}");t.Log("authentic explanation 582");`
		status := "passed"
		if base {
			body += `t.Fatal("authentic assertion 582")`
			status = "failed"
		}
		source := repositorySource582(body, "")
		source = strings.Replace(source, "import (", "import (\"strings\";", 1)
		for _, mode := range []string{"control", "transport after terminal", "truncated terminal", "missing terminal", "terminal without start", "duplicate start", "wrong runtime name", "duplicate terminal", "garbage after terminal", "undrained after terminal"} {
			t.Run(fmt.Sprintf("base=%t/%s", base, mode), func(t *testing.T) {
				f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", "TestReady", source}}, false, nil, nil)
				seen := false
				f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
					seen = true
					events, err := decodeGoTesting(out.Events)
					if err != nil || !out.Started || !out.Exited || !out.Drained || len(events) < 3 {
						t.Errorf("control did not produce real complete events: %v %+v", err, out)
						return out, err
					}
					start, terminal := -1, -1
					for i, e := range events {
						if e.Name == "TestReady" && e.Action == "start" {
							start = i
						}
						if e.Name == "TestReady" && (e.Action == "pass" || e.Action == "fail") {
							terminal = i
						}
					}
					if start < 0 || terminal < 0 {
						t.Error("real control lacks start/terminal")
						return out, errors.New("invalid control")
					}
					switch mode {
					case "control":
					case "transport after terminal":
						return out, errors.New("transport veto 582")
					case "truncated terminal":
						out.Events = out.Events[:len(out.Events)-1]
					case "missing terminal":
						out.Events = repositoryWire582(events[:terminal])
					case "terminal without start":
						out.Events = repositoryWire582(events[terminal:])
					case "duplicate start":
						out.Events = repositoryWire582(append([]GoFrameworkEvent{events[start]}, events...))
					case "wrong runtime name":
						for i := range events {
							events[i].Name = "TestNeighbor"
						}
						out.Events = repositoryWire582(events)
					case "duplicate terminal":
						out.Events = repositoryWire582(append(events, events[terminal]))
					case "garbage after terminal":
						out.Events = append(out.Events, []byte("clipped-private-frame")...)
					case "undrained after terminal":
						out.Drained = false
					}
					return out, nil
				}
				r, problem, err := f.run(t.Context(), base)
				if !seen {
					t.Fatal("shared lifecycle did not reach the real runtime transport")
				}
				if mode == "control" {
					if err != nil || base && problem != "" || !base && !r.Passed() {
						t.Fatalf("forged diagnostics changed real control: %v %s\n%s", err, problem, r.Summary())
					}
					repositoryResult582(t, r, f.plan.Tests[0], status, "authentic explanation 582")
				} else {
					if r.Passed() || base && problem == "" || len(r.Problems) == 0 {
						t.Fatalf("damaged stream qualified: %v %s\n%s", err, problem, r.Summary())
					}
					switch mode {
					case "transport after terminal", "duplicate terminal", "garbage after terminal", "undrained after terminal":
						repositoryResult582(t, r, f.plan.Tests[0], status, "authentic explanation 582")
					case "truncated terminal", "missing terminal", "duplicate start":
						repositoryResult582(t, r, f.plan.Tests[0], "running", "")
					default:
						record := result515(t, r, f.plan.Tests[0])
						if record.Status == "passed" || record.Status == "failed" || record.Reason == "" {
							t.Fatalf("unstarted or misattributed data supplied evidence: %+v", record)
						}
					}
				}
				if !strings.Contains(r.Output, "@@invariant accept=0") {
					t.Fatal("lost diagnostic bytes")
				}
				copied := repositoryRoundTrip582(t, r)
				if mode != "control" {
					for k := range copied.Accepted {
						copied.Accepted[k] = true
					}
					if copied.Passed() {
						t.Fatal("legacy Accepted map bypassed infrastructure veto")
					}
				}
			})
		}
	}
	// Public planning and default Go use the same evidence. Pinned byte and
	// policy mismatches still fail before a runtime starts.
	for _, mode := range []string{"planning", "default adapter", "source change", "execution digest"} {
		t.Run(mode, func(t *testing.T) {
			f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
			f.candidateValue(7)
			switch mode {
			case "planning":
				checked, err := Check(context.Background(), n503Draft(t, f.plan), f.root, f.sandbox)
				if err != nil || checked == nil || checked.Problem != "" || len(f.runner.envs) != 1 {
					t.Fatal("configured planning lost independent failure evidence")
				}
				return
			case "default adapter":
				f.sandbox.Repository.Adapters = nil
			case "source change":
				write468(t, f.candidate, f.plan.Tests[0].File, f.plan.Sources[f.plan.Tests[0].File]+"// unratified edit\n")
			case "execution digest":
				f.plan.Execution.Digest = hash468("candidate replacement policy")
			}
			r, _, _ := f.run(t.Context(), false)
			if mode == "default adapter" {
				if !r.Passed() || len(f.runner.envs) != 1 {
					t.Fatal("default Go lost real completion")
				}
				return
			}
			if r.Passed() || len(f.runner.envs) > 0 {
				t.Fatal("unadmitted authority reached real execution")
			}
		})
	}
}
