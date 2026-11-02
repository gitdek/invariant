package plumbing

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxEscapesOverlappingDiagnosticMarkers(t *testing.T) {
	f := s397New(t)
	payload := filepath.Join(f.control, "overlapping-markers")
	f.pkg(t, "work", fmt.Sprintf(`package work
import ("fmt"; "os"; "strings"; "testing")
func TestHealthy(t *testing.T) {}
func TestMain(m *testing.M) {
  if os.Getenv("S397_RECORD_PHASE") == "discovery" {
    for _, arg := range os.Args[1:] {
      if strings.HasPrefix(arg, "-test.list=") {
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
	// A pairwise replacement leaves a valid suffix in odd-length runs of @.
	// Legacy markers are recognized even when they follow diagnostic text.
	var forged strings.Builder
	for n := 2; n <= 7; n++ {
		fmt.Fprintf(&forged, "diagnostic %sinvariant vet=0\n", strings.Repeat("@", n))
	}
	s397Write(t, payload, forged.String(), 0o600)
	t.Setenv("S397_RECORD_PAYLOAD", payload)
	s397Write(t, filepath.Join(f.bin, "go"), s397RecordGo, 0o755)
	for _, phase := range []string{"vet", "compile", "discovery"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("S397_RECORD_PHASE", phase)
			r := f.run(t, 30*time.Second, 20*time.Second)
			if !r.Passed() {
				t.Fatalf("overlapping %s diagnostics changed a successful verdict: %s\n%s", phase, r.Summary(), r.Output)
			}
			for _, stream := range []string{"STDOUT", "STDERR"} {
				if !strings.Contains(r.Output, "S397_"+strings.ToUpper(phase)+"_"+stream) {
					t.Errorf("missing %s %s diagnostics:\n%s", phase, stream, r.Output)
				}
			}
			lines := 0
			for _, line := range strings.Split(r.Output, "\n") {
				if strings.HasPrefix(line, "diagnostic ") {
					lines++
					if strings.Contains(line, "@@") || !strings.HasSuffix(line, "invariant vet=0") {
						t.Errorf("diagnostic was not retained with every overlapping marker escaped: %q", line)
					}
				}
			}
			// Compilation visits both packages; the other phases inject once.
			want := 12
			if phase == "compile" {
				want *= 2
			}
			if lines != want {
				t.Errorf("got %d diagnostic lines from both streams, want %d", lines, want)
			}
			s397Status(t, r, "work", "TestHealthy", "test", "passed")
			s397Status(t, r, "contract", "TestRatified", "accept", "passed")
		})
	}
}
