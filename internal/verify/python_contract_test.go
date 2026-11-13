package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// contractOf reads the contract of f in a module that imports Nagini's
// names, with the reader the gate uses.
func contractOf(t *testing.T, source string) *PythonContract {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("needs python3")
	}
	d, err := analyzePythonProof([]byte(source), "f")
	if err != nil {
		t.Fatal(err)
	}
	if d.Contract == nil {
		t.Fatal("the reader gave no contract")
	}
	return d.Contract
}

func contractCall(t *testing.T, record string) PythonApplicationCall {
	t.Helper()
	var call PythonApplicationCall
	if err := json.Unmarshal([]byte(record), &call); err != nil {
		t.Fatalf("%s: %v", record, err)
	}
	return call
}

func findings(found []PythonContractFinding) string {
	var lines []string
	for _, f := range found {
		lines = append(lines, strings.TrimSpace(fmt.Sprintf("%s: %s %s", f.Verdict, f.Clause, f.Why)))
	}
	return strings.Join(lines, "\n")
}

const contractHeader = "from typing import Dict, List, Optional\nfrom nagini_contracts.contracts import *\n\nLIMIT = 3\nKINDS = ('statue', 'engine')\n\n\n"

// The gate evaluates a proved function's contract on a recorded call
// (#838): entry clauses and parameter types on the arguments as they were
// on entry, exit clauses on the result, Old on the entry values, and a
// list or dictionary argument as it was after the call.
func TestContractOnARecordedCall838(t *testing.T) {
	c := contractOf(t, contractHeader+`def f(kind: str, busy: bool, queue: List[int], limit: Optional[int] = None) -> int:
    """Says whether a job may start."""
    Requires(Acc(list_pred(queue)) and len(queue) <= LIMIT)
    Requires(kind in KINDS)
    Requires(Implies(limit is not None, isinstance(limit, int)))
    Ensures(Acc(list_pred(queue)))
    Ensures(Result() == (409 if busy else 0))
    Ensures(len(queue) == Old(len(queue)) + (0 if busy else 1))
    Exsures(Exception, busy)
    Decreases(None)
    if busy:
        return 409
    queue.append(1)
    return 0
`)
	checked, partly, proofOnly := c.Classes()
	if len(checked) != 5 || len(partly) != 1 || len(proofOnly) != 2 {
		t.Fatalf("clauses: checked %q, partly %q, proof-only %q", checked, partly, proofOnly)
	}
	for _, x := range []struct {
		name, record string
		evaluated    int
		want         string
	}{
		{"a call that meets it",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":false,"queue":[1],"limit":null},"after":{"queue":[1,1]},"result":0}`, 10, ""},
		{"a refused call that meets it",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"engine","busy":true,"queue":[],"limit":5},"after":{"queue":[]},"result":409}`, 10, ""},
		{"a bad precondition",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"boat","busy":false,"queue":[1],"limit":null},"after":{"queue":[1,1]},"result":0}`, 10,
			"violated: Requires(kind in KINDS)"},
		{"a queue past the limit",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":true,"queue":[1,2,3,4],"limit":null},"after":{"queue":[1,2,3,4]},"result":409}`, 10,
			"violated: Requires(Acc(list_pred(queue)) and len(queue) <= LIMIT)"},
		{"a wrong result",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":true,"queue":[],"limit":null},"after":{"queue":[]},"result":0}`, 10,
			"violated: Ensures(Result() == (409 if busy else 0))"},
		{"an argument left as it was",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":false,"queue":[1],"limit":null},"after":{"queue":[1]},"result":0}`, 10,
			"violated: Ensures(len(queue) == Old(len(queue)) + (0 if busy else 1))"},
		{"an argument of another type",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":7,"busy":false,"queue":[1],"limit":null},"result":0}`, 3,
			"violated: kind: str kind was 7"},
		{"a list holding another type",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":false,"queue":[1,"two"],"limit":null},"result":0}`, 3,
			`violated: queue: List[int] queue was [1, "two"]`},
		{"a float where an int belongs",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":false,"queue":[],"limit":2.5},"result":0}`, 3,
			"violated: limit: Union[int, None] limit was 2.5"},
		{"a result of another type",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":true,"queue":[],"limit":null},"after":{"queue":[]},"result":"409"}`, 10,
			"violated: Ensures(Result() == (409 if busy else 0))\nviolated: returns int it returned \"409\""},
		{"a declared exception whose condition holds",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":true,"queue":[],"limit":null},"after":{"queue":[]},"result":null,"raised":{"type":"builtins.RuntimeError","builtins":["BaseException","Exception","RuntimeError"]}}`, 9, ""},
		{"a declared exception whose condition doesn't",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":false,"queue":[],"limit":null},"after":{"queue":[]},"result":null,"raised":{"type":"builtins.RuntimeError","builtins":["BaseException","Exception","RuntimeError"]}}`, 9,
			"violated: Exsures(Exception, busy)"},
		{"an exception the contract doesn't declare",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"kind":"statue","busy":true,"queue":[],"limit":null},"after":{"queue":[]},"result":null,"raised":{"type":"builtins.KeyboardInterrupt","builtins":["BaseException","KeyboardInterrupt"]}}`, 8,
			"violated: raises only what its Exsures declare it raised builtins.KeyboardInterrupt"},
	} {
		t.Run(x.name, func(t *testing.T) {
			evaluated, found := c.Check(contractCall(t, x.record))
			if got := findings(found); got != x.want || evaluated != x.evaluated {
				t.Errorf("evaluated %d clauses, want %d; findings:\n%s\nwant:\n%s", evaluated, x.evaluated, got, x.want)
			}
		})
	}
}

// What the gate can't evaluate on a record is unresolved, never taken as
// holding (#838): a permission anywhere but beside what it's stated with, a
// clause that reads something no record holds, an annotation it can't read,
// and a string Go's JSON decoder may have changed.
func TestContractClausesTheGateCannotEvaluate838(t *testing.T) {
	record := `{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"n":1,"name":"a","queue":[]},"result":1}`
	for _, x := range []struct{ name, source, record, want string }{
		{"a permission under a not", "def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(not Acc(list_pred(queue)))\n    return n\n", record,
			"unresolved: Requires(not Acc(list_pred(queue))) states the permission Acc where the gate can't take it as holding"},
		{"a permission in an or", "def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(n > 5 or Acc(list_pred(queue)))\n    return n\n", record,
			"unresolved: Requires(n > 5 or Acc(list_pred(queue))) states the permission Acc where the gate can't take it as holding"},
		{"a permission as a condition", "def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(Implies(Acc(list_pred(queue)), n > 5))\n    return n\n", record,
			"unresolved: Requires(Implies(Acc(list_pred(queue)), n > 5)) states the permission Acc where the gate can't take it as holding"},
		{"Result() on entry", "def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(Result() == 1)\n    return n\n", record,
			"unresolved: Requires(Result() == 1) reads Result() where the call returns nothing to read"},
		{"identity of two values", "def f(n: int, name: str, queue: List[int]) -> int:\n    Ensures(Result() is n)\n    return n\n", record,
			"unresolved: Ensures(Result() is n) compares identities with is"},
		{"a contract after the body starts", "def f(n: int, name: str, queue: List[int]) -> int:\n    m = n\n    Requires(n > 0)\n    return m\n", record,
			"unresolved: Requires(n > 0) isn't at the head of the function"},
		{"an annotation the gate can't read", "from typing import Sequence\n\ndef f(n: int, name: str, queue: Sequence[int]) -> int:\n    return n\n", record,
			"unresolved: queue: Sequence[int] the gate can't tell what type the annotation Sequence[int] names"},
		{"an argument the record doesn't hold", "def f(n: int, name: str, queue: List[int], extra: int) -> int:\n    return n\n", record,
			"unresolved: extra: int the record holds no argument for extra"},
		{"a string the record may not have kept", "def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(len(name) == 1)\n    return n\n",
			`{"target":"t","source_sha256":"s","caller":"app/core.py","arguments":{"n":1,"name":"\ud800","queue":[]},"result":1}`,
			"unresolved: Requires(len(name) == 1) a string holds U+FFFD"},
	} {
		t.Run(x.name, func(t *testing.T) {
			c := contractOf(t, contractHeader+x.source)
			_, found := c.Check(contractCall(t, x.record))
			if got := findings(found); !strings.HasPrefix(got, x.want) || strings.Count(got, "\n") != 0 {
				t.Errorf("findings:\n%s\nwant one starting:\n%s", got, x.want)
			}
		})
	}
	// A clause untaken on a call isn't evaluated on it, as Python doesn't.
	c := contractOf(t, contractHeader+"def f(n: int, name: str, queue: List[int]) -> int:\n    Requires(Implies(n > 5, Result() == 1))\n    return n\n")
	if evaluated, found := c.Check(contractCall(t, record)); len(found) != 0 || evaluated != 5 {
		t.Errorf("an untaken consequent was evaluated: %d clauses, %s", evaluated, findings(found))
	}
}

// contractOracle writes one function whose head holds generated clauses,
// and what Python itself makes of each on generated arguments: true, false,
// or an error. Implies is lazy, Old reads the entry values, Result() the
// result, and a permission holds, as the gate reads them.
const contractOracle = `
import json, random, sys

rng = random.Random(int(sys.argv[1]))
INTS = ["a", "b", "c"]
STRS = ["s", "t"]
BOOLS = ["p", "q"]
LISTS = ["xs", "ys"]

def lit_int():
    return str(rng.choice([0, 1, 2, 3, 5, 7, 10, 255, 2 ** 70, 10 ** 30]))

def int_expr(d, exit):
    k = rng.randrange(13 if d > 0 else 4)
    if k == 0:
        n = lit_int()
        return n, n
    if k == 1:
        n = rng.choice(INTS)
        return n, n
    if k == 2:
        n = rng.choice(BOOLS)
        return n, n
    if k == 3:
        n = rng.choice(LISTS + STRS + ["d"])
        return "len(%s)" % n, "len(%s)" % n
    if k == 4 and exit:
        return "Result()", "result__"
    if k in (4, 5):
        op = rng.choice(["+", "-", "*"])
        (l, lo), (r, ro) = int_expr(d - 1, exit), int_expr(d - 1, exit)
        return "(%s %s %s)" % (l, op, r), "(%s %s %s)" % (lo, op, ro)
    if k == 6:
        op, n = rng.choice(["//", "%"]), rng.choice(["1", "2", "3", "7", "1000"])
        l, lo = int_expr(d - 1, exit)
        return "(%s %s %s)" % (l, op, n), "(%s %s %s)" % (lo, op, n)
    if k == 7:
        op = rng.choice(["-", "+"])
        l, lo = int_expr(d - 1, exit)
        return "(%s%s)" % (op, l), "(%s%s)" % (op, lo)
    if k == 8:
        l, lo = int_expr(d - 1, exit)
        return "abs(%s)" % l, "abs(%s)" % lo
    if k == 9:
        (c, co), (l, lo), (r, ro) = bool_expr(d - 1, exit, False), int_expr(d - 1, exit), int_expr(d - 1, exit)
        return "(%s if %s else %s)" % (l, c, r), "(%s if %s else %s)" % (lo, co, ro)
    if k == 10 and exit:
        l, lo = int_expr(d - 1, False)
        return "Old(%s)" % l, old(lo)
    if k == 11:
        (l, lo), (r, ro) = int_expr(d - 1, exit), int_expr(d - 1, exit)
        op = rng.choice(["and", "or"])
        return "(%s %s %s)" % (l, op, r), "(%s %s %s)" % (lo, op, ro)
    n = rng.choice(INTS)
    return n, n

def old(text):
    # In the oracle, an entry value is the name with __old after it.
    out, i = [], 0
    names = sorted(INTS + STRS + BOOLS + LISTS + ["d", "n"], key=len, reverse=True)
    while i < len(text):
        for name in names:
            before = text[i - 1] if i else " "
            after = text[i + len(name)] if i + len(name) < len(text) else " "
            if text.startswith(name, i) and not (before.isalnum() or before == "_") and not (after.isalnum() or after == "_"):
                out.append(name + "__old")
                i += len(name)
                break
        else:
            out.append(text[i])
            i += 1
    return "".join(out)

def str_expr(d):
    k = rng.randrange(5 if d > 0 else 2)
    if k == 0:
        return repr(rng.choice(["", "a", "ab", "statue", "é", "\U0001F600x"]))
    if k == 1:
        return rng.choice(STRS)
    if k == 2:
        return "(%s + %s)" % (str_expr(d - 1), str_expr(d - 1))
    if k == 3:
        return "(%s * %s)" % (str_expr(d - 1), rng.choice(["0", "1", "2", "3"]))
    return "(%s * %s)" % (rng.choice(["2", "3"]), str_expr(d - 1))

def list_expr(d):
    k = rng.randrange(4 if d > 0 else 1)
    if k == 0:
        return rng.choice(LISTS)
    if k == 1:
        return "(%s + %s)" % (list_expr(d - 1), list_expr(d - 1))
    if k == 2:
        return "(%s * %s)" % (list_expr(d - 1), rng.choice(["0", "1", "2"]))
    return rng.choice(LISTS)

def any_expr(d, exit):
    k = rng.randrange(6)
    if k == 0:
        return int_expr(d, exit)
    if k == 1:
        e = str_expr(d)
        return e, e
    if k == 2:
        e = list_expr(d)
        return e, e
    if k == 3:
        return "n", "n"
    if k == 4:
        return "d", "d"
    return bool_expr(d, exit, False)

def bool_expr(d, exit, positive):
    k = rng.randrange(19 if d > 0 else 6)
    if k == 0:
        n = rng.choice(BOOLS)
        return n, n
    if k == 1:
        return rng.choice([("True", "True"), ("False", "False")])
    if k == 2:
        op = rng.choice(["is", "is not"])
        return "(n %s None)" % op, "(n %s None)" % op
    if k == 3:
        n, t = rng.choice(INTS + STRS + BOOLS + LISTS + ["d", "n"]), rng.choice(["int", "bool", "str", "list", "dict", "tuple", "object", "bytes", "set"])
        return "isinstance(%s, %s)" % (n, t), "isinstance(%s, %s)" % (n, t)
    if k == 4:
        n, v = rng.choice(BOOLS), rng.choice(["True", "False"])
        return "(%s is %s)" % (n, v), "(%s is %s)" % (n, v)
    if k == 5 and positive:
        return rng.choice(["Acc(list_pred(xs))", "list_pred(ys)", "Acc(dict_pred(d), 1/2)"]), "True"
    if k in (5, 6):
        ops = [rng.choice(["==", "!=", "<", "<=", ">", ">="]) for _ in range(rng.choice([1, 1, 2]))]
        parts = [int_expr(d - 1, exit) for _ in range(len(ops) + 1)]
        text, oracle = parts[0]
        for op, (e, eo) in zip(ops, parts[1:]):
            text, oracle = "%s %s %s" % (text, op, e), "%s %s %s" % (oracle, op, eo)
        return "(%s)" % text, "(%s)" % oracle
    if k == 7:
        op = rng.choice(["==", "!=", "<", "<=", ">", ">="])
        l, r = str_expr(d - 1), str_expr(d - 1)
        return "(%s %s %s)" % (l, op, r), "(%s %s %s)" % (l, op, r)
    if k == 8:
        op = rng.choice(["==", "!="])
        (l, lo), (r, ro) = any_expr(d - 1, exit), any_expr(d - 1, exit)
        return "(%s %s %s)" % (l, op, r), "(%s %s %s)" % (lo, op, ro)
    if k == 9:
        op = rng.choice(["in", "not in"])
        l, lo = any_expr(d - 1, exit)
        items = [any_expr(0, exit) for _ in range(rng.randrange(1, 4))]
        return "(%s %s (%s,))" % (l, op, ", ".join(i[0] for i in items)), "(%s %s (%s,))" % (lo, op, ", ".join(i[1] for i in items))
    if k == 10:
        op, c = rng.choice(["in", "not in"]), rng.choice(LISTS + STRS + ["d"])
        l, lo = any_expr(d - 1, exit)
        return "(%s %s %s)" % (l, op, c), "(%s %s %s)" % (lo, op, c)
    if k == 11:
        l, lo = bool_expr(d - 1, exit, False)
        return "(not %s)" % l, "(not %s)" % lo
    if k == 12:
        parts = [bool_expr(d - 1, exit, positive) for _ in range(rng.choice([2, 2, 3]))]
        return "(%s)" % " and ".join(x[0] for x in parts), "(%s)" % " and ".join(x[1] for x in parts)
    if k == 13:
        parts = [bool_expr(d - 1, exit, False) for _ in range(rng.choice([2, 2, 3]))]
        return "(%s)" % " or ".join(x[0] for x in parts), "(%s)" % " or ".join(x[1] for x in parts)
    if k == 14:
        (l, lo), (r, ro) = bool_expr(d - 1, exit, False), bool_expr(d - 1, exit, positive)
        return "Implies(%s, %s)" % (l, r), "(True if not %s else %s)" % (lo, ro)
    if k == 15:
        (c, co), (l, lo), (r, ro) = bool_expr(d - 1, exit, False), bool_expr(d - 1, exit, positive), bool_expr(d - 1, exit, positive)
        return "(%s if %s else %s)" % (l, c, r), "(%s if %s else %s)" % (lo, co, ro)
    if k == 16:
        # What Python can fail on: the gate must say unresolved, not guess.
        bad = rng.choice(["(a < s)", "(s + a == s)", "(len(a) == 1)", "(abs(s) == 1)", "(a in s)", "(xs in d)", "((-s) == s)",
                          "(s % 5 == 0)", "(a // 0 == 1)", "(a % 0 == 1)", "(n < 1)", "(xs < 3)", "(d + d == d)", "(s - t == s)",
                          "(len(n) == 0)", "((xs,) in d)"])
        return bad, bad
    if k == 17:
        e, eo = any_expr(d - 1, exit)
        return e, eo
    l, lo = list_expr(d - 1), list_expr(d - 1)
    op = rng.choice(["==", "!="])
    return "(%s %s %s)" % (l, op, lo), "(%s %s %s)" % (l, op, lo)

def value(kind):
    if kind == "int":
        return rng.choice([0, 1, -1, 2, 3, 7, -7, 255, 2 ** 70, -(2 ** 70), 10 ** 30 + 1])
    if kind == "str":
        return rng.choice(["", "a", "ab", "statue", "é", "\U0001F600x", "aa", "b"])
    if kind == "bool":
        return rng.choice([True, False])
    if kind == "list":
        return [value("int") for _ in range(rng.randrange(4))]
    return {k: value("int") for k in rng.sample(["", "a", "ab", "statue", "k"], rng.randrange(4))}

count = int(sys.argv[2])
clauses = []
for i in range(count):
    exit = i % 2 == 1
    text, oracle = bool_expr(rng.randrange(1, 5), exit, True)
    clauses.append((("Ensures" if exit else "Requires"), text, oracle))
source = "from typing import Dict, List, Optional\nfrom nagini_contracts.contracts import *\n\n\n"
source += "def f(a: int, b: int, c: int, s: str, t: str, p: bool, q: bool, xs: List[int], ys: List[int], d: Dict[str, int], n: Optional[int]) -> object:\n"
for kind, text, _ in clauses:
    source += "    %s(%s)\n" % (kind, text)
source += "    return 0\n"
cases = []
for _ in range(int(sys.argv[3])):
    entry = {"a": value("int"), "b": value("int"), "c": value("int"), "s": value("str"), "t": value("str"), "p": value("bool"), "q": value("bool"),
             "xs": value("list"), "ys": value("list"), "d": value("dict"), "n": rng.choice([None, value("int")])}
    after = {"xs": rng.choice([entry["xs"], value("list")]), "ys": entry["ys"], "d": rng.choice([entry["d"], value("dict")])}
    result = rng.choice([value("int"), value("str"), value("bool"), None, value("list")])
    truths = []
    for kind, _, oracle in clauses:
        names = dict(entry)
        if kind == "Ensures":
            names.update(after)
        names.update({k + "__old": v for k, v in entry.items()})
        names["result__"] = result
        try:
            truths.append(bool(eval(oracle, {"__builtins__": {"len": len, "abs": abs, "isinstance": isinstance, "int": int, "bool": bool, "str": str,
                                                              "list": list, "dict": dict, "tuple": tuple, "object": object, "bytes": bytes, "set": set,
                                                              "True": True, "False": False, "None": None}}, names)))
        except Exception as error:
            truths.append("error: " + type(error).__name__)
    cases.append({"arguments": entry, "after": after, "result": result, "truths": truths})
print(json.dumps({"source": source, "clauses": [c[1] for c in clauses], "cases": cases}))
`

// contractSeeds is how many generated contracts the comparison with Python
// runs: six, or INVARIANT_CONTRACT_SEEDS of them.
func contractSeeds() int {
	n := 6
	fmt.Sscan(os.Getenv("INVARIANT_CONTRACT_SEEDS"), &n)
	return n
}

// The gate's evaluator and Python agree on every clause a contract may
// hold: generated clauses, read by the gate's own reader, are true, false
// or an error in Python exactly where the gate says they hold, are violated
// or are unresolved, on generated arguments, exact integers and strings
// beyond the basic plane among them (#838).
func TestContractEvaluatorAgreesWithPython838(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("needs python3")
	}
	for seed := 1; seed <= contractSeeds(); seed++ {
		out, err := exec.Command("python3", "-I", "-c", contractOracle, fmt.Sprint(seed), "220", "40").Output()
		if err != nil {
			t.Fatalf("the oracle: %v", err)
		}
		var oracle struct {
			Source  string   `json:"source"`
			Clauses []string `json:"clauses"`
			Cases   []struct {
				Arguments map[string]any `json:"arguments"`
				After     map[string]any `json:"after"`
				Result    any            `json:"result"`
				Truths    []any          `json:"truths"`
			} `json:"cases"`
		}
		d := json.NewDecoder(bytes.NewReader(out))
		d.UseNumber()
		if err := d.Decode(&oracle); err != nil {
			t.Fatal(err)
		}
		c := contractOf(t, oracle.Source)
		if len(c.Obligations) != len(oracle.Clauses) {
			t.Fatalf("the reader gave %d clauses of %d", len(c.Obligations), len(oracle.Clauses))
		}
		agreed, errors := 0, 0
		for _, x := range oracle.Cases {
			entry, exit := map[string]any{}, map[string]any{}
			for name, v := range x.Arguments {
				entry[name], exit[name] = pythonContractValue(v), pythonContractValue(v)
			}
			for name, v := range x.After {
				exit[name] = pythonContractValue(v)
			}
			for i, o := range c.Obligations {
				env := pythonContractEnv{names: entry, entry: entry}
				if o.Kind == "ensures" {
					env = pythonContractEnv{names: exit, entry: entry, result: pythonContractValue(x.Result), returned: true}
				}
				got := any(nil)
				v, err := env.eval(o.Expr, true)
				if err == nil {
					got, err = pythonContractTruth(v)
				}
				if err != nil {
					got = "error"
				}
				want := x.Truths[i]
				if s, ok := want.(string); ok && strings.HasPrefix(s, "error") {
					want = "error"
					errors++
				}
				if got != want {
					t.Fatalf("seed %d: %s\n  on %v, after %v, result %v\n  Python: %v\n  the gate: %v (%v)", seed, o.Text, x.Arguments, x.After, x.Result, x.Truths[i], got, err)
				}
				agreed++
			}
		}
		if testing.Verbose() {
			t.Logf("seed %d: %d evaluations agree, %d of them errors in Python and unresolved here", seed, agreed, errors)
		}
		if agreed < 8000 || errors < 100 {
			t.Fatalf("seed %d: compared %d clause evaluations, %d of them errors: too few to mean anything", seed, agreed, errors)
		}
	}
}
