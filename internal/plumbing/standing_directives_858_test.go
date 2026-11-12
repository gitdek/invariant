package plumbing

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A plan's acceptance tests call an API that isn't there yet, so the check
// reads the compiler's errors from source. It can't read what a compiler
// directive does. In an acceptance file that stays a refusal. In a test file
// the plan didn't write, the compiler is asked: the package's tests are
// compiled without the acceptance files, and a package that compiles has had
// its directives checked (#858). These run the built-in check and the real
// Go compiler through the local Docker stand-in, as #733's tests do.
func TestBuiltinGoLeavesStandingDirectivesToTheCompiler858(t *testing.T) {
	const file = "pending/pinned_accept_test.go"
	const calls = `package pending
import "testing"
func TestNeedsChange(t *testing.T) { if Future858() != 7 { t.Fatal("wrong result") } }
`
	embeds := func(name string) string {
		return "package pending\n\nimport (\n\t_ \"embed\"\n\t\"testing\"\n)\n\n//go:embed " + name + "\nvar data858 string\n\n" +
			"func TestStanding858(t *testing.T) {\n\tif data858 == \"\" {\n\t\tt.Fatal(\"nothing embedded\")\n\t}\n}\n"
	}
	fixture := func(t *testing.T, standing string) *s397Fixture {
		t.Helper()
		f := s397New(t)
		f.pkg(t, "pending", "")
		if standing != "" {
			s397Write(t, filepath.Join(f.root, "pending", "standing_test.go"), standing, 0o644)
		}
		s397Write(t, filepath.Join(f.root, "pending", "data.txt"), "embedded\n", 0o644)
		return f
	}

	t.Run("a standing test file embeds a file", func(t *testing.T) {
		f := fixture(t, embeds("data.txt"))
		f.accept(t, "pending", "TestNeedsChange", calls)
		c := builtin733Check(t, f, builtin733Draft(t, f))
		if c.Problem != "" {
			t.Fatalf("a directive the compiler accepts in a standing test file refused the plan:\n%s", c.Feedback())
		}
		s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")

		// The build adds the API, and the standing test still runs beside
		// the acceptance test.
		s397Write(t, filepath.Join(f.root, "pending", "code.go"), "package pending\n\nfunc Future858() int { return 7 }\n", 0o644)
		s397Write(t, filepath.Join(f.root, file), f.plan.Sources[file], 0o644)
		r := f.run(t, 90*time.Second, 30*time.Second)
		if !r.Passed() {
			t.Fatalf("the build's change did not pass: %s\n%s", r.Summary(), r.Output)
		}
		s397Status(t, r, "pending", "TestNeedsChange", "accept", "passed")
		s397Status(t, r, "pending", "TestStanding858", "test", "passed")
	})

	t.Run("an acceptance file holds the directive", func(t *testing.T) {
		f := fixture(t, "")
		f.accept(t, "pending", "TestNeedsChange", `package pending
import (_ "embed"; "testing")
//go:embed data.txt
var data858 string
func TestNeedsChange(t *testing.T) { if Future858() != 7 || data858 == "" { t.Fatal("wrong result") } }
`)
		c := builtin733Check(t, f, builtin733Draft(t, f))
		builtin733Refused(t, c, file, "test compiler directive is outside API attribution", "//go:embed data.txt")
		if strings.Contains(c.Feedback(), "without the acceptance files") {
			t.Errorf("the compiler was asked about an acceptance file:\n%s", c.Feedback())
		}
	})

	t.Run("a standing directive doesn't compile", func(t *testing.T) {
		f := fixture(t, embeds("absent.txt"))
		f.accept(t, "pending", "TestNeedsChange", calls)
		c := builtin733Check(t, f, builtin733Draft(t, f))
		builtin733Refused(t, c, "pending/standing_test.go", "//go:embed absent.txt",
			"don't compile without the acceptance files", "no matching files found")
	})

	t.Run("a standing test function has no body", func(t *testing.T) {
		f := fixture(t, "package pending\n\nimport \"testing\"\n\nfunc bodiless858() int\n\n"+
			"func TestStanding858(t *testing.T) {\n\tif bodiless858() != 0 {\n\t\tt.Fatal(\"wrong result\")\n\t}\n}\n")
		f.accept(t, "pending", "TestNeedsChange", calls)
		c := builtin733Check(t, f, builtin733Draft(t, f))
		builtin733Refused(t, c, "pending/standing_test.go", "test function has no body",
			"don't compile without the acceptance files", "missing function body")
	})
}
