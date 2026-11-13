package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// What the pinned Nagini proves where Python raises, or reads apart from
// Python, is refused before it runs (#872), with the arithmetic. It finds two
// equal ints or strings identical, where Python compares objects, so a
// function that returns `count` whenever `total is count + 1` fails was
// proved to return `count + 1`, and returns `count` from 256 on. It gives
// formatting no precondition: an f-string, `str.format`, `str()`, `print()`
// and `__str__` never fail to it. Nor do `bytes()` of an int, a range's
// `len`, or hashing a list. What both read alike stays: a test against None
// or of a list, a dict or a set, and making a string of a string or a
// boolean.
func TestPythonApplicationProofRefusesWhatNaginiModelsApart872(t *testing.T) {
	t.Parallel()
	head := "from nagini_contracts.contracts import *\nfrom typing import Dict, List, Optional, Set\n\nSTEP = 1\n\n\n" +
		"def admit(name: str, count: int, ok: bool, data: bytes, limit: Optional[int], xs: List[int], seen: Set[int], table: Dict[str, int]) -> int:\n    Requires(count >= 0)\n"
	const (
		identity = "tests identity with is or is not on other than None, a list, a dict or a set"
		field    = "holds an f-string with a field the gate can't tell is a string or a boolean"
		format   = "reads format, a string's formatting method"
		hashes   = "hashes a list, a dict or a set"
	)
	// Each body's first line is the module's ninth.
	for _, c := range []struct{ name, body, want string }{
		// The audit's case: proved at every size, wrong from 256 on.
		{"an identity test on computed ints", "    Ensures(Result() == count + STEP)\n    total = count + STEP\n    if total is count + STEP:\n        return total\n    return count\n", "line 11 " + identity},
		{"is not on a string", "    if name is not name + 'x':\n        return count\n    return count\n", "line 9 " + identity},
		{"an identity test on a tuple", "    pair = (count, count)\n    if pair is (count, count):\n        return count\n    return count\n", "line 10 " + identity},
		{"an identity test in a contract", "    Requires(count is count)\n    return count\n", "line 9 " + identity},
		{"an identity test in a chain", "    if 0 <= count is count:\n        return count\n    return count\n", "line 9 " + identity},
		{"an identity test against a boolean", "    if ok is True:\n        return count\n    return count\n", "line 9 " + identity},
		{"an identity test of what the gate can't tell is a list", "    if name is xs[0]:\n        return count\n    return count\n", "line 9 " + identity},
		{"a test against None", "    if limit is None:\n        return count\n    if None is not limit:\n        return count\n    return count\n", ""},
		// A list is its own object to Nagini and to Python alike.
		{"an identity test of a list", "    ys = [count]\n    if xs is ys or ys is not xs:\n        return count\n    return count\n", ""},
		{"an identity test of a dict", "    if table is not table:\n        return count\n    return count\n", ""},
		{"equality", "    total = count + STEP\n    if total == count + STEP:\n        return total\n    return count\n", ""},

		{"an f-string with a format code", "    r = f'{count:q}'\n    return count\n", "line 9 " + field},
		{"an f-string of an int", "    r = f'{count}'\n    return count\n", "line 9 " + field},
		{"an f-string of an Optional", "    r = f'{limit}'\n    return count\n", "line 9 " + field},
		{"an f-string with a conversion", "    r = f'{name!r}'\n    return count\n", "line 9 " + field},
		{"an f-string of a string with a format code", "    r = f'{name:>4}'\n    return count\n", "line 9 " + field},
		{"an f-string of an int in a raise", "    if count > 3:\n        raise ValueError(f'too many: {count}')\n    return count\n", "line 10 " + field},
		{"an f-string of a string and a boolean", "    r = f'site {name} busy {ok}'\n    return count\n", ""},
		{"an f-string with no field", "    r = f'plain'\n    return count\n", ""},
		{"str.format", "    r = '{} {}'.format(count)\n    return count\n", "line 9 " + format},
		{"format on a string the gate can't see", "    r = name.format(count)\n    return count\n", "line 9 " + format},
		{"str() of an int", "    r = name + str(count)\n    return count\n", "line 9 makes a string with str() of what the gate can't tell is a string or a boolean"},
		{"str() of an Optional", "    r = str(limit)\n    return count\n", "line 9 makes a string with str()"},
		{"str() of a list", "    r = str(xs)\n    return count\n", "line 9 makes a string with str()"},
		{"str() of a string and of a boolean", "    r = str(name) + str(ok) + str()\n    return count\n", ""},
		{"an int's __str__", "    r = count.__str__()\n    return count\n", "line 9 reads __str__, which makes a string as str() does"},
		{"an int's __repr__", "    r = count.__repr__()\n    return count\n", "line 9 reads __repr__, which makes a string as str() does"},
		{"print() of an int", "    print(name, count)\n    return count\n", "line 9 makes a string with print()"},
		{"print() of a string and a boolean", "    print('admitting', name, ok)\n    return count\n", ""},

		{"bytes() of an int", "    b = bytes(count)\n    return count\n", "line 9 makes bytes() of what the gate can't tell is bytes"},
		{"bytearray() of an int", "    b = bytearray(count)\n    return count\n", "line 9 makes bytearray() of what the gate can't tell is bytes"},
		{"bytes() of bytes, and of nothing", "    b = bytes(data) + bytes()\n    return count\n", ""},
		{"the len of a range", "    n = len(range(count))\n    return count\n", "line 9 takes the len of a range"},
		{"the len of a range a local holds", "    r = range(count)\n    n = len(r)\n    return count\n", "line 10 takes the len of a range"},
		{"a loop over a range, and a list's len", "    total = len(xs)\n    for i in range(count):\n        total = total + 1\n    return total\n", ""},

		{"a list added to a set", "    seen.add(xs)\n    return count\n", "line 9 " + hashes},
		{"a list looked up in a dict", "    v = table.get(xs)\n    return count\n", "line 9 " + hashes},
		{"a list as a dict's key", "    table[xs] = 1\n    return count\n", "line 9 " + hashes},
		{"a list in a set display", "    s = {count, xs}\n    return count\n", "line 9 " + hashes},
		{"a list as a dict display's key", "    d = {xs: 1}\n    return count\n", "line 9 " + hashes},
		{"a list in a set comprehension", "    s = {[x] for x in xs}\n    return count\n", "line 9 " + hashes},
		{"a list tested for membership in a set", "    if xs in seen:\n        return count\n    return count\n", "line 9 " + hashes},
		{"hash() of a list", "    h = hash(xs)\n    return count\n", "line 9 " + hashes},
		{"ints and strings where Python hashes", "    seen.add(count)\n    v = table.get(name)\n    table[name] = hash(count)\n    s = {count, 1}\n    xs[0] = 1\n    if count in seen or count in xs:\n        return count\n    return count\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := analyzePythonProof([]byte(head+c.body), "admit")
			if err != nil {
				t.Fatal(err)
			}
			found := slices.ContainsFunc(d.Arithmetic, func(p pythonProofDynamic) bool {
				return strings.Contains(fmt.Sprintf("line %d %s", p.Line, p.What), c.want)
			})
			switch {
			case c.want == "" && len(d.Arithmetic) != 0:
				t.Errorf("refused: %+v", d.Arithmetic)
			case c.want != "" && !found:
				t.Errorf("problems %+v, want %q", d.Arithmetic, c.want)
			}
		})
	}
}

// What those refusals say of Python, from the host's python3: each thing the
// pinned Nagini proves can't fail does, and what the gate lets through
// doesn't.
func TestPythonRaisesWhereNaginiProvesNothingDoes872(t *testing.T) {
	t.Parallel()
	if out, err := exec.Command("python3", "-c", "import sys; print(sys.version_info >= (3, 12))").Output(); err != nil || strings.TrimSpace(string(out)) != "True" {
		t.Skip("needs Python 3.12 or newer on the host: an int's conversion to a string is limited from 3.11")
	}
	script := `
def raises(kind, f):
    try:
        f()
    except kind:
        return True
    return False

def probe(name, count, big, xs):
    total = count + 1
    other, again = name + "x", name + "x"
    seen, table = set(), {}
    def store():
        table[xs] = 1
    return {
        "two equal ints past the small-int cache are two objects": total is not count + 1,
        "two equal strings are two objects": other is not again,
        "two lists are two objects, and one list is one": [count] is not [count] and xs is xs,
        "an f-string's format code": raises(ValueError, lambda: f"{count:q}"),
        "an f-string of an int of 4,301 digits": raises(ValueError, lambda: f"{big}"),
        "format with an argument missing": raises(IndexError, lambda: "{} {}".format(count)),
        "format's format code": raises(ValueError, lambda: "{:q}".format(count)),
        "str() of an int of 4,301 digits": raises(ValueError, lambda: str(big)),
        "__str__ and __repr__ of one": raises(ValueError, big.__str__) and raises(ValueError, big.__repr__),
        "print() of one": raises(ValueError, lambda: print(big)),
        "a string and a boolean format": f"{name} {True}" == "ab True" and str(name) + str(False) == "abFalse",
        "bytes() of a negative count": raises(ValueError, lambda: bytes(-count)),
        "bytearray() of a negative count": raises(ValueError, lambda: bytearray(-count)),
        "bytes() past what Python can index": raises(OverflowError, lambda: bytes(2 ** 63)),
        "a range's len past what Python can index": raises(OverflowError, lambda: len(range(2 ** 63))),
        "hash() of a list": raises(TypeError, lambda: hash(xs)),
        "a list in a set display": raises(TypeError, lambda: {xs}),
        "a list as a dict display's key": raises(TypeError, lambda: {xs: 1}),
        "a list added to a set": raises(TypeError, lambda: seen.add(xs)),
        "a list looked up in a dict": raises(TypeError, lambda: table.get(xs)),
        "a list as a dict's key": raises(TypeError, store),
        "a list tested for membership in a set": raises(TypeError, lambda: xs in seen),
    }

failed = [what for what, held in probe("ab", 1000, 10 ** 4300, [1]).items() if not held]
print("\n".join(failed))
`
	out, err := exec.Command("python3", "-I", "-c", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("Python doesn't do what the refusals say it does: %v\n%s", err, out)
	}
}

// What those refusals say of the pinned Nagini, from the pinned Nagini. It is
// given each file whole, past the gate's own reader, which would refuse them.
// It proves every function of the first, each of which Python contradicts or
// fails on, except the last three, where the two agree: they're why a list, a
// dict and a set keep their identity tests. And it proves neither function of
// the second. When a later Nagini changes any of this, the refusals change
// with it.
func TestPythonApplicationProofRealNaginiModelsThemApart872(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_PROOF_REAL") != "1" {
		t.Skip("set INVARIANT_PYTHON_APPLICATION_PROOF_REAL=1 to run the pinned Nagini")
	}
	t.Parallel()
	ctx := context.Background()
	image, err := toolchain.NaginiImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head := "from nagini_contracts.contracts import *\nfrom typing import Dict, List, Set\n\n\n"
	proved := head +
		// Python returns count from 256 on, and 0 for any name.
		"def equal_ints(count: int) -> int:\n    Requires(count >= 0)\n    Ensures(Result() == count + 1)\n    total = count + 1\n    if total is count + 1:\n        return total\n    return count\n\n\n" +
		"def equal_strings(name: str) -> int:\n    Ensures(Result() == 1)\n    other = name + 'x'\n    again = name + 'x'\n    if other is again:\n        return 1\n    return 0\n\n\n" +
		// Python raises in each of these, for some argument or for all.
		"def format_code(count: int) -> str:\n    return f'{count:q}'\n\n\n" +
		"def format_unseen(fmt: str, count: int) -> str:\n    return fmt.format(count)\n\n\n" +
		"def string_of_an_int(count: int) -> str:\n    return str(count)\n\n\n" +
		"def its_own_str(count: int) -> str:\n    return count.__str__() + count.__repr__()\n\n\n" +
		"def printed(count: int) -> int:\n    print(count)\n    return count\n\n\n" +
		"def zero_bytes(count: int) -> int:\n    b = bytes(count)\n    return count\n\n\n" +
		// A bytearray of a negative count has a length of -1 to Nagini, and
		// from that it proves anything.
		"def negative_count(count: int) -> int:\n    Ensures(Result() == 42)\n    b = bytearray(count)\n    if count < 0:\n        return 0\n    return 42\n\n\n" +
		"def range_len(count: int) -> int:\n    Requires(count >= 0)\n    Ensures(Result() == count)\n    return len(range(count))\n\n\n" +
		"def hashed(xs: List[int]) -> int:\n    Requires(Acc(list_pred(xs)))\n    Ensures(Acc(list_pred(xs)))\n    return hash(xs)\n\n\n" +
		"def element(seen: Set[List[int]], xs: List[int]) -> int:\n    Requires(Acc(set_pred(seen)))\n    Ensures(Acc(set_pred(seen)))\n    seen.add(xs)\n    return 1\n\n\n" +
		"def key(xs: List[int]) -> int:\n    d = {xs: 1}\n    return 1\n\n\n" +
		// Where the two agree.
		"def two_lists(count: int) -> int:\n    Ensures(Result() == 1)\n    ys = [count]\n    zs = [count]\n    if ys is zs:\n        return 0\n    return 1\n\n\n" +
		"def one_list(count: int) -> int:\n    Ensures(Result() == 1)\n    ys = [count]\n    zs = ys\n    if ys is zs:\n        return 1\n    return 0\n\n\n" +
		"def another_dict(task: Dict[str, int]) -> Dict[str, int]:\n    Requires(Acc(dict_pred(task)))\n    Ensures(Acc(dict_pred(task)))\n    Ensures(Result() is not task)\n    out = {}  # type: Dict[str, int]\n    return out\n"
	out, exit, err := naginiProve(ctx, image, "proved.py", []byte(proved))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Nagini: %s", strings.TrimSpace(out))
	if exit != 0 || !strings.Contains(out, "Verification successful") {
		t.Fatalf("the pinned Nagini no longer proves all of these, so a refusal of #872's may have lost its reason: exit %d\n%s", exit, out)
	}

	refuted := head +
		"def two_lists_are_one(count: int) -> int:\n    Ensures(Result() == 0)\n    ys = [count]\n    zs = [count]\n    if ys is zs:\n        return 0\n    return 1\n\n\n" +
		"def the_dict_it_was_given(task: Dict[str, int]) -> Dict[str, int]:\n    Ensures(Result() is not task)\n    return task\n"
	out, exit, err = naginiProve(ctx, image, "refuted.py", []byte(refuted))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Nagini: %s", strings.TrimSpace(out))
	for _, name := range []string{"two_lists_are_one", "the_dict_it_was_given"} {
		if exit == 0 || !strings.Contains(out, "Postcondition of "+name+" might not hold") {
			t.Fatalf("the pinned Nagini proves %s, so an identity test of a list or a dict isn't Python's to it: exit %d\n%s", name, exit, out)
		}
	}
}

// A module constant the proof copies is bound once. A match capture and an
// except clause's name rebind one as plain strings, which a scan for stored
// names missed, so the proof copied a literal the module no longer held
// (#872). Each is a binding now, and the input can't be built.
func TestPythonApplicationProofSeesCapturedRebindings872(t *testing.T) {
	t.Parallel()
	for name, rebinding := range map[string]string{
		"a match capture":        "match 4:\n    case LIMIT:\n        pass\n",
		"a match star capture":   "match [4]:\n    case [*LIMIT]:\n        pass\n",
		"a match mapping's rest": "match {}:\n    case {**LIMIT}:\n        pass\n",
		"a match capture as":     "match 4:\n    case int() as LIMIT:\n        pass\n",
		"an except clause":       "try:\n    raise ValueError()\nexcept ValueError as LIMIT:\n    pass\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := strings.Replace(ppModule, "LIMIT = 3\n", "LIMIT = 3\n"+rebinding, 1)
			if module == ppModule {
				t.Fatal("the fixture's constant moved")
			}
			_, p := ppFixture(t, module)
			n := &ppNagini{output: ppVerified}
			s := ppSession(t, p, n)
			if _, err := s.Prove(context.Background(), ppAdmitID()); err == nil || !strings.Contains(err.Error(), "LIMIT is bound more than once") {
				t.Fatalf("got %v, want the second binding of LIMIT refused", err)
			}
			if n.calls != 0 {
				t.Fatal("Nagini ran on an input that can't be built")
			}
		})
	}
}
