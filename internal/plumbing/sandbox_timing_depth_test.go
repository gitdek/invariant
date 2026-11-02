package plumbing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxIgnoresDeeplyNestedTimingHints(t *testing.T) {
	f := s397New(t)
	visits := filepath.Join(f.observe, "healthy-runs")
	f.pkg(t, "work", fmt.Sprintf(`package work
import ("os"; "testing")
func TestHealthy(t *testing.T) {
  out, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { t.Fatal(err) }
  defer out.Close()
  if _, err := out.WriteString("run\n"); err != nil { t.Fatal(err) }
}
`, visits))
	f.contract(t, false)
	f.warm(t)
	// Small enough for the cache size limit, but deeper than Python's JSON
	// decoder permits. Invalid ordering hints must not abort either phase.
	s397Write(t, filepath.Join(f.cache, s397TimingFile), strings.Repeat("[", 1100)+strings.Repeat("]", 1100), 0o600)
	r := f.run(t, 30*time.Second, 20*time.Second)
	if !r.Passed() {
		t.Fatalf("deeply nested timing hint changed the verdict: %s\n%s", r.Summary(), r.Output)
	}
	s397Status(t, r, "work", "TestHealthy", "test", "passed")
	s397Status(t, r, "contract", "TestRatified", "test", "passed")
	s397Status(t, r, "contract", "TestRatified", "accept", "passed")
	for file, want := range map[string]int{visits: 1, filepath.Join(f.observe, "contract-runs"): 2} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != strings.Repeat("run\n", want) {
			t.Errorf("executions in %s = %q, want %d runs", file, data, want)
		}
	}
}
