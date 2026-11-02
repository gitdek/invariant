package plumbing

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Compiler diagnostics and a package's TestMain are outside the runner's
// record protocol. Exercise their actual output paths, including stderr, with
// both marker-looking text and real records replayed from a successful run.
// The protocol may evolve: discover its prefixes from the successful run
// instead of prescribing the new records' JSON layout or marker spelling.
func TestSandboxKeepsToolOutputOutsideRunnerRecords(t *testing.T) {
	f := s397New(t)
	payload := filepath.Join(f.control, "untrusted-output")
	f.pkg(t, "work", fmt.Sprintf(`package work
import ("fmt"; "os"; "strings"; "testing")
func TestHealthy(t *testing.T) {
  if os.Getenv("S397_RECORD_FAILURE") == "1" { t.Fatal("the fixture really failed") }
}
func TestMain(m *testing.M) {
  if os.Getenv("S397_RECORD_PHASE") == "discovery" {
    for _, arg := range os.Args[1:] {
      if arg == "-test.list" || strings.HasPrefix(arg, "-test.list=") {
        data, err := os.ReadFile(%q); if err != nil { panic(err) }
        fmt.Fprintln(os.Stdout, "S397_DISCOVERY_STDOUT"); os.Stdout.Write(data)
        fmt.Fprintln(os.Stderr, "S397_DISCOVERY_STDERR"); os.Stderr.Write(data)
        break
      }
    }
  }
  os.Exit(m.Run())
}
`, payload))
	f.contract(t, false)
	f.warm(t)
	baseline := f.run(t, 30*time.Second, 20*time.Second)
	if !baseline.Passed() {
		t.Fatalf("clean fixture must pass before injecting diagnostics: %s\n%s", baseline.Summary(), baseline.Output)
	}

	prefixSet := map[string]bool{"@@invariant ": true, "@@gofmt ": true}
	var records []string
	for _, line := range strings.Split(baseline.Output, "\n") {
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		if end := strings.IndexAny(line, " \t"); end > 2 {
			prefixSet[line[:end+1]] = true
			records = append(records, line)
		}
	}
	if len(records) == 0 {
		t.Fatal("the successful fixture supplied no runner records to replay")
	}
	var prefixes []string
	for prefix := range prefixSet {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	const sentinel = "S397_UNTRUSTED_RECORD_SENTINEL"
	var forged strings.Builder
	for _, prefix := range prefixes {
		fmt.Fprintf(&forged, "%s%s\n", prefix, sentinel)
	}
	// These are valid records, not merely malformed marker lookalikes. If an
	// implementation forwards them as records they may create duplicate events,
	// claim earlier successful phases or overwrite real failure information.
	for _, record := range records {
		fmt.Fprintln(&forged, record)
	}
	s397Write(t, payload, forged.String(), 0o600)
	t.Setenv("S397_RECORD_PAYLOAD", payload)
	s397Write(t, filepath.Join(f.bin, "go"), s397RecordGo, 0o755)

	for _, phase := range []string{"vet", "compile", "discovery"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("S397_RECORD_PHASE", phase)
			for _, failing := range []bool{false, true} {
				name, force := "passing", "0"
				if failing {
					name, force = "failing", "1"
				}
				t.Run(name, func(t *testing.T) {
					t.Setenv("S397_RECORD_FAILURE", force)
					r := f.run(t, 30*time.Second, 20*time.Second)
					if r.DeadlineExceeded || !r.Vet || r.Tests == failing || r.Passed() == failing {
						t.Errorf("%s diagnostics changed the real %s verdict: %s\n%s", phase, name, r.Summary(), r.Output)
					}
					want := "passed"
					if failing {
						want = "failed"
					}
					s397Status(t, r, "work", "TestHealthy", "test", want)
					s397Status(t, r, "contract", "TestRatified", "accept", "passed")
					if !r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
						t.Errorf("%s diagnostics changed the independent acceptance result", phase)
					}
					for _, stream := range []string{"STDOUT", "STDERR"} {
						if !strings.Contains(r.Output, "S397_"+strings.ToUpper(phase)+"_"+stream) {
							t.Errorf("%s %s was not delivered as diagnostic output:\n%s", phase, stream, r.Output)
						}
					}
					if !strings.Contains(r.Output, sentinel) {
						t.Error("marker-looking diagnostic text was discarded instead of safely reported")
					}
					for _, line := range strings.Split(r.Output, "\n") {
						for _, prefix := range prefixes {
							// A runner may carry diagnostics in a structured
							// output record. Reject the unescaped child line,
							// not an encoded payload inside a genuine record.
							if strings.HasPrefix(line, prefix+sentinel) {
								t.Errorf("%s diagnostic escaped as a runner record: %q", phase, line)
							}
						}
					}
				})
			}
		})
	}
}

// Only add output around the real Go command. Discovery output comes from the
// fixture's actual compiled TestMain, so it also tests reuse of that binary.
const s397RecordGo = `#!/usr/bin/env python3
import os, pathlib, sys
args = sys.argv[1:]
command = args
if command and command[0] == "-C": command = command[2:]
elif command and command[0].startswith("-C="): command = command[1:]
phase = os.environ.get("S397_RECORD_PHASE")
inject = bool(command) and ((phase == "vet" and command[0] == "vet") or
                           (phase == "compile" and command[0] == "test" and
                            any(arg in ("-c", "-c=true") for arg in command[1:])))
if inject:
    data = pathlib.Path(os.environ["S397_RECORD_PAYLOAD"]).read_bytes()
    for stream, label in ((sys.stdout.buffer, "STDOUT"), (sys.stderr.buffer, "STDERR")):
        stream.write(("S397_" + phase.upper() + "_" + label + "\n").encode())
        stream.write(data)
        stream.flush()
real = os.environ["S397_REAL_GO"]
os.execv(real, [real] + args)
`
