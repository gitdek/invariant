package verify

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// naginiCore is a core marked # +nagini as the factory writes one, a class
// whose methods state permissions, with line put on line 15, in a method.
func naginiCore(line string) string {
	return "# +nagini\nfrom typing import List\n\nfrom nagini_contracts.contracts import *\n\n\n" +
		"class Box:\n" +
		"    def __init__(self, n: int) -> None:\n" +
		"        self.v = n  # type: int\n" +
		"        Ensures(Acc(self.v))\n" +
		"\n" +
		"    def step(self, n: int, xs: List[int]) -> int:\n" +
		"        Requires(Acc(self.v, 1/2))\n" +
		"        Ensures(Acc(self.v, 1/2))\n" +
		line +
		"        return n\n"
}

// #380: the pinned Nagini proves a core whole, and models some of its
// arithmetic apart from CPython: for n = -2, it proves 7 // n == -3 and
// 7 % n == 1, where Python gives -4 and -1; it proves -4 | 1 != -3 and
// -1 ^ 1 != -2; it raises nothing for [0] * n past what CPython can index,
// or for n + 0.5 and n / 3 with n too large for a float, all of which
// Python raises OverflowError for. The gate refuses each in a marked file,
// before Nagini runs, by the rule an in-place proof applies (#359): // and %
// only by a positive int literal, no bitwise operator, no / outside Acc's
// permission fraction, no float, multiplication only of what the gate can
// tell are both ints or by an int literal, and none of the methods behind
// them called by name, which Nagini models as it does the operator. That
// holds in a contract and a lambda as in a body, and in the module's and
// its classes' own statements, which Nagini reads too.
func TestNaginiRefusesArithmeticItModelsApartFromPython(t *testing.T) {
	const (
		floorDivision = "holds a floor division by other than a positive integer literal, which the pinned Nagini divides as Viper does, apart from Python for a negative divisor"
		modulo        = "holds a modulo by other than a positive integer literal, which the pinned Nagini divides as Viper does"
		bitwise       = "holds a bitwise operator, which the pinned Nagini models apart from Python on negative ints"
		repeated      = "multiplies what the gate can't tell are both ints by other than an int literal, so it may repeat a sequence more often than Python can index"
		floats        = "deals in floats, which the pinned Nagini leaves uninterpreted, while Python's arithmetic fails on an int too large for a float"
		division      = "divides with /, which makes a float and fails on an int too large for one where Nagini's model doesn't, and a file marked # +nagini may divide only in Acc's permission fraction"
		method        = "the method behind an operator, which the gate checks only written as the operator, since the pinned Nagini can model its arithmetic apart from Python"
	)
	for _, c := range []struct{ name, src, want string }{
		// The issue's table, one construct at a time.
		{"7 // n", naginiCore("        r = 7 // n\n"), "core.py:15: refused before Nagini runs: the line " + floorDivision},
		{"-7 // n", naginiCore("        r = -7 // n\n"), "core.py:15: refused before Nagini runs: the line " + floorDivision},
		{"7 % n", naginiCore("        r = 7 % n\n"), "core.py:15: refused before Nagini runs: the line " + modulo},
		{"-7 % n", naginiCore("        r = -7 % n\n"), "core.py:15: refused before Nagini runs: the line " + modulo},
		{"-4 | 1", naginiCore("        r = -4 | 1\n"), "core.py:15: refused before Nagini runs: the line " + bitwise},
		{"-1 ^ 1", naginiCore("        r = -1 ^ 1\n"), "core.py:15: refused before Nagini runs: the line " + bitwise},
		{"[0] * n", naginiCore("        r = [0] * n\n"), "core.py:15: refused before Nagini runs: the line " + repeated},
		{"n + 0.5", naginiCore("        r = n + 0.5\n"), "core.py:15: refused before Nagini runs: the line " + floats},
		{"n / 3", naginiCore("        r = n / 3\n"), "core.py:15: refused before Nagini runs: the line " + division},
		// The rest of the rule.
		{"n & 3", naginiCore("        r = n & 3\n"), "core.py:15: refused before Nagini runs: the line " + bitwise},
		{"~n", naginiCore("        r = ~n\n"), "core.py:15: refused before Nagini runs: the line " + bitwise},
		{"n // -2", naginiCore("        r = n // -2\n"), "core.py:15: refused before Nagini runs: the line " + floorDivision},
		{"float()", naginiCore("        r = float(n)\n"), "core.py:15: refused before Nagini runs: the line " + floats},
		{"a float annotation", naginiCore("        r: float = 1\n"), "core.py:15: refused before Nagini runs: the line " + floats},
		{"an augmented floor division", naginiCore("        n //= -2\n"), "core.py:15: refused before Nagini runs: the line " + floorDivision},
		{"an augmented or", naginiCore("        n |= 1\n"), "core.py:15: refused before Nagini runs: the line " + bitwise},
		{"an augmented division", naginiCore("        n /= 2\n"), "core.py:15: refused before Nagini runs: the line " + division},
		{"__floordiv__ by name", naginiCore("        r = (7).__floordiv__(n)\n"), "core.py:15: refused before Nagini runs: the line reads __floordiv__, " + method},
		{"__or__ by name", naginiCore("        r = n.__or__(1)\n"), "core.py:15: refused before Nagini runs: the line reads __or__, " + method},
		{"a contract's floor division", naginiCore("        Ensures(Result() // n >= 0)\n"), "core.py:15: refused before Nagini runs: the line " + floorDivision},
		{"a lambda's modulo", naginiCore("        Ensures(Forall(int, lambda i: Implies(0 <= i, i % n >= 0)))\n"), "core.py:15: refused before Nagini runs: the line " + modulo},
		{"a fraction by a name", naginiCore("        Requires(Acc(self.v, 1/n))\n"), "core.py:15: refused before Nagini runs: the line " + division},
		{"a fraction past a whole permission", naginiCore("        Requires(Acc(self.v, 3/2))\n"), "core.py:15: refused before Nagini runs: the line " + division},
		{"a fraction of a bool", naginiCore("        Requires(Acc(self.v, True/2))\n"), "core.py:15: refused before Nagini runs: the line " + division},
		{"a fraction outside Acc", naginiCore("        Requires(Rd(self.v) and 1/2 > 0)\n"), "core.py:15: refused before Nagini runs: the line " + division},
		// Nagini reads the module's own statements and its classes' too.
		{"a module constant", "# +nagini\nfrom nagini_contracts.contracts import *\n\nDIV = 7 // -2\n\n\ndef div() -> int:\n    return DIV\n",
			"core.py:4: refused before Nagini runs: the line " + floorDivision},
		{"a float constant", "# +nagini\nfrom nagini_contracts.contracts import *\n\nRATE = 0.5\n", "core.py:4: refused before Nagini runs: the line " + floats},
		{"a class attribute", "# +nagini\nfrom nagini_contracts.contracts import *\n\n\nclass Mask:\n    BITS = 3 & 1\n",
			"core.py:6: refused before Nagini runs: the line " + bitwise},
		{"a default", "# +nagini\nfrom nagini_contracts.contracts import *\n\n\ndef f(n: int = 7 // -2) -> int:\n    return n\n",
			"core.py:5: refused before Nagini runs: the line " + floorDivision},
	} {
		t.Run(c.name, func(t *testing.T) {
			refused, err := naginiRefusals("core.py", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(refused) != 1 || !strings.HasPrefix(refused[0], c.want) {
				t.Fatalf("refused %q, want one refusal starting %q", refused, c.want)
			}
		})
	}
}

// Acc's permission fraction is Nagini's permission, not Python's division,
// and arithmetic Nagini models as Python does stays: ints multiplied by
// ints or by an int literal, // and % by a positive int literal, unary
// minus, shifts and **. A nested function's parameters type its own
// arithmetic.
func TestNaginiAdmitsPermissionFractionsAndArithmeticItModelsAsPythonDoes(t *testing.T) {
	src := naginiCore("        Requires(Acc(list_pred(xs), 1/4) and Acc(self.v, 2/3) and Acc(self.v, 1/1))\n" +
		"        r = n * n + n * 2 + 3 * n + len(xs) // 2 + n % 3 + -n + (n << 1) + (n >> 1) + n ** 2\n" +
		"        s = [0] * 3 + xs * 2 + xs + xs\n" +
		"        r += 1\n" +
		"        r //= 2\n" +
		"        r *= n\n")
	src += "\n\ndef scale(n: int) -> int:\n    def by(a: int, b: int) -> int:\n        return a * b\n    return by(n, n)\n"
	refused, err := naginiRefusals("core.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 0 {
		t.Fatalf("refused %q", refused)
	}
}

// Today's proved cores, whose only / is Acc's permission fraction, pass.
func TestNaginiAdmitsTodaysCores(t *testing.T) {
	cores, err := filepath.Glob("testdata/examples/*/*/core.py")
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, core := range cores {
		src, err := os.ReadFile(core)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLine(string(src), NaginiHeader) {
			continue
		}
		marked++
		if !strings.Contains(string(src), "1/2)") {
			t.Errorf("%s holds no permission fraction, so it shows nothing about one", core)
		}
		refused, err := naginiRefusals(filepath.Base(core), src)
		if err != nil {
			t.Fatal(err)
		}
		if len(refused) != 0 {
			t.Errorf("%s refused: %q", core, refused)
		}
	}
	if marked < 2 {
		t.Fatalf("found %d marked cores in testdata, want both two-phase commits", marked)
	}
}

// A refused file never reaches Nagini, nor its image: the code fails with
// the refusal, and its functions are named as ones Nagini never saw, beside
// the plain files', whose arithmetic the environment may use freely.
func TestNaginiNeverRunsOnARefusedFile(t *testing.T) {
	pkg := t.TempDir()
	pa310Write(t, pkg, "core.py", naginiCore("        r = 7 // n\n"))
	pa310Write(t, pkg, "explore.py", "def node(sent, n):\n    return sent | {n // -2}\n")
	proved, plain, err := naginiSources(pkg)
	if err != nil {
		t.Fatal(err)
	}
	code, err := runNagini(context.Background(), func(context.Context) (string, error) {
		t.Fatal("the gate acquired Nagini's image for a file it refuses")
		return "", nil
	}, pkg, proved, plain)
	if err != nil {
		t.Fatal(err)
	}
	if code.Passed || len(code.Functions) != 0 || len(code.Contracts) != 0 {
		t.Fatalf("code = %+v, want a failure with nothing verified", code)
	}
	if len(code.Errors) != 1 || !strings.HasPrefix(code.Errors[0], "core.py:15: refused before Nagini runs: the line holds a floor division") {
		t.Fatalf("errors = %q", code.Errors)
	}
	if want := []string{"Box.__init__", "Box.step", "node"}; !reflect.DeepEqual(code.Unverified, want) {
		t.Fatalf("unverified = %q, want %q", code.Unverified, want)
	}
}

// A marked file the host's Python can't parse is refused, since the gate
// can't check its arithmetic.
func TestNaginiRefusesAFileItCantRead(t *testing.T) {
	refused, err := naginiRefusals("core.py", []byte("# +nagini\ndef f(:\n    return 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 || !strings.HasPrefix(refused[0], "core.py: refused before Nagini runs: the host's python3 can't parse it, so the gate can't check its arithmetic: ") {
		t.Fatalf("refused %q", refused)
	}
}
