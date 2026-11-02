package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A refusal's type can't also be a control-flow class, and its float
// attributes match by sign as well as by ==, so the declared value a row
// records is the one the application raised.
func TestDeclaredRefusalMatchesExactly(t *testing.T) {
	const probe = `import sys
sys.path.insert(0, sys.argv[1])
from invariant_observe import Refusal
class Mixed(Exception, KeyboardInterrupt):
    pass
try:
    Refusal(Mixed)
except ValueError:
    pass
else:
    raise SystemExit("a control-flow subclass was accepted")
class Refused(Exception):
    def __init__(self, code):
        self.code = code
zero, negative = Refusal(Refused, code=0.0), Refusal(Refused, code=-0.0)
assert zero.matches(Refused(0.0)) and not zero.matches(Refused(-0.0)), "-0.0 matched 0.0"
assert negative.matches(Refused(-0.0)) and not negative.matches(Refused(0.0)), "0.0 matched -0.0"
class Later(Exception):
    pass
later = Refusal(Later)
assert later.matches(Later())
Later.__bases__ = (Exception, KeyboardInterrupt)
assert not later.matches(Later()), "a control-flow base added after declaring matched"
`
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "invariant_observe.py"), pythonApplicationObserver, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-c", probe, dir).CombinedOutput(); err != nil {
		t.Fatalf("exact refusal matching: %v\n%s", err, out)
	}
}
