package verify

import (
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A trusted contract says what its operation may raise with Exsures, and
// declaring no Exsures for an exception says the operation never raises it
// under its precondition. The runner records each exception type that escapes
// a backing test's calls into the bound implementation, and a contract whose
// tests saw one its Exsures doesn't cover, by class or superclass, is
// unsupported. Each case runs the real embedded runner on the host's Python.

const praState = `def save(task):
    task['saved'] = True
    return task


class StoreError(Exception):
    pass


def reject(task):
    raise StoreError('rejected')


def settle(n):
    if n == 0:
        raise LookupError('bottom')
    try:
        return settle(n - 1)
    except LookupError:
        return n
`

const praTests = ptcTestSource + `

import base64
import binascii

from store.state import StoreError, reject, settle


class RaiseTests(unittest.TestCase):
    def test_rejects_malformed(self):
        with self.assertRaises(json.JSONDecodeError):
            json.loads('{')

    def test_rejects_padding(self):
        with self.assertRaises(binascii.Error):
            base64.b64decode('a')

    def test_pops_missing(self):
        with self.assertRaises(KeyError):
            {}.pop('x')

    def test_pops_from_empty(self):
        with self.assertRaises(KeyError):
            {}.popitem()

    def test_rejects_task(self):
        with self.assertRaises(StoreError):
            reject({})

    def test_settles(self):
        self.assertEqual(settle(2), 1)
`

const praContracts = ptcContractSource + `
from store.state import StoreError

def decode(text: str) -> bytes:
    Requires(True)
    ...

def pop(d: dict, key: object) -> object:
    Requires(True)
    ...

def popitem(d: dict) -> object:
    Requires(True)
    Exsures(LookupError, True)
    ...

def reject(task: dict) -> None:
    Requires(True)
    Exsures(StoreError, True)
    ...

def settle(n: int) -> int:
    Requires(n >= 0)
    ...
`

func TestPythonApplicationContractTestsNeedWhatTheyRaiseDeclared(t *testing.T) {
	root, p := ptcFixture(t)
	pa310Write(t, root, "store/state.py", praState)
	pa310Write(t, root, ptcTests, praTests)
	pa310Write(t, root, ptcContracts, praContracts)
	app := func(symbol string) project.ComponentOperation {
		return project.ComponentOperation{Kind: "application", Source: "store/state.py", Symbol: symbol}
	}
	p.Manifest.Components.TrustedContracts = []project.ComponentContract{
		ptcContract("json.loads", prvStd("json", "loads"), "loads", "RaiseTests.test_rejects_malformed"),
		ptcContract("base64.b64decode", prvStd("base64", "b64decode"), "decode", "RaiseTests.test_rejects_padding"),
		ptcContract("dict.pop", prvStd("builtins", "dict.pop"), "pop", "RaiseTests.test_pops_missing"),
		ptcContract("dict.popitem", prvStd("builtins", "dict.popitem"), "popitem", "RaiseTests.test_pops_from_empty"),
		ptcContract("store.reject", app("reject"), "reject", "RaiseTests.test_rejects_task"),
		ptcContract("store.settle", app("settle"), "settle", "RaiseTests.test_settles"),
	}
	pa310Save(t, p)
	r := ptcRun(t, ptcSession(t, p, ptcLibraries(t, ptcTinyver), "1.0"))
	// The inventory read each contract's Exsures from its own bytes.
	exsures := map[string][]string{}
	for _, c := range r.Components.Inventory.TrustedContracts {
		exsures[c.ID] = c.Exsures
	}
	if !slices.Equal(exsures["json.loads"], []string{"builtins.ValueError"}) || !slices.Equal(exsures["store.reject"], []string{"store.state.StoreError"}) || exsures["dict.pop"] != nil {
		t.Fatalf("the inventory's Exsures: %v", exsures)
	}
	want := map[string][]PythonApplicationRaise{
		// A subclass of a declared exception is declared, whether a Python
		// function or a C one raised it, and an application's own exception
		// resolves once its module is loaded.
		"RaiseTests-test_rejects_malformed": {{Exception: "json.decoder.JSONDecodeError", Calls: 1, Declared: true}},
		"RaiseTests-test_pops_from_empty":   {{Exception: "builtins.KeyError", Calls: 1, Declared: true}},
		"RaiseTests-test_rejects_task":      {{Exception: "store.state.StoreError", Calls: 1, Declared: true}},
		// What the contract doesn't declare is undeclared.
		"RaiseTests-test_rejects_padding": {{Exception: "binascii.Error", Calls: 1, Declared: false}},
		"RaiseTests-test_pops_missing":    {{Exception: "builtins.KeyError", Calls: 1, Declared: false}},
		// An exception the implementation raises to itself and catches
		// never escapes it.
		"RaiseTests-test_settles": {},
	}
	for id, raised := range want {
		row := ptcSelected(t, ptcExecution(t, r, id))
		if !slices.Equal(row.BoundaryRaised, raised) || row.Calls() == 0 {
			t.Errorf("%s: raised %+v in %d calls, want %+v", id, row.BoundaryRaised, row.Calls(), raised)
		}
	}
	if row := ptcSelected(t, ptcExecution(t, r, "RaiseTests-test_settles")); row.Calls() != 3 {
		t.Fatalf("settle's recursion: %+v", row)
	}
	for _, id := range []string{"json.loads", "dict.popitem", "store.reject", "store.settle"} {
		if c := ptcStanding(t, r, id); c.Support != "tested" {
			t.Errorf("%s: %+v; diagnostics %+v", id, c, r.Components.ContractDiagnostics)
		}
	}
	for id, why := range map[string]string{
		"base64.b64decode": "RaiseTests.test_rejects_padding@" + ptcTests + ": base64.b64decode raised binascii.Error, which its contract's Exsures doesn't declare",
		"dict.pop":         "RaiseTests.test_pops_missing@" + ptcTests + ": builtins.dict.pop raised builtins.KeyError, which its contract's Exsures doesn't declare",
	} {
		if c := ptcStanding(t, r, id); c.Support != "untested" || !strings.Contains(ptcDiagnostic(r, UnsupportedContract, id), why) {
			t.Errorf("%s: %+v, %q", id, c, ptcDiagnostic(r, UnsupportedContract, id))
		}
	}
}
