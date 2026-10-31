package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// #353, #294's step 5: a selected function is proved in its real module.
// These tests build Nagini's input with the host's isolated Python parser and
// run the session with a Nagini double, and step 4's real runner on host
// Python for backing tests where they need them. With
// INVARIANT_PYTHON_APPLICATION_PROOF_REAL=1, TestPythonApplicationProofReal
// runs the pinned Nagini in its linux/amd64 image on a function whose
// boundary is a real installed wheel, with its backing tests run in the
// application's real dependency image:
//
//	INVARIANT_PYTHON_APPLICATION_PROOF_REAL=1 go test ./internal/verify -run '^TestPythonApplicationProof' -count=1 -timeout 60m

const ppSource = "tasks/admission.py"

// ppModule's admit is the selected function, and report an unselected
// neighbor in the same module that Nagini can't read: given the whole file,
// it stops on the async function.
const ppModule = `from nagini_contracts.contracts import *
import tinyver
from store.state import save
from .helper import next_state

LIMIT = 3


def admit(text: str, admitted: int) -> int:
    Requires(admitted >= 0)
    Requires(admitted < LIMIT)
    Ensures(Result() >= admitted)
    Ensures(Implies(Result() > admitted, len(text) > 0))
    if len(text) > 0:
        version = tinyver.parse(text)
        return admitted + 1
    return admitted


async def report(text: str) -> str:
    return f"admitted {text}: {next_state(text, text)}"
`

const ppTarget = `def admit(text: str, admitted: int) -> int:
    Requires(admitted >= 0)
    Requires(admitted < LIMIT)
    Ensures(Result() >= admitted)
    Ensures(Implies(Result() > admitted, len(text) > 0))
    if len(text) > 0:
        version = tinyver.parse(text)
        return admitted + 1
    return admitted
`

// ppAbstraction is the trusted contract tinyver.parse stands for, under its
// own name, which the input renames to the operation's.
const ppAbstraction = `

def version_of(text: str) -> object:
    """The version a non-empty text names."""
    Requires(len(text) > 0)
    ...
`

func ppAdmitID() string { return pa310ID(ppSource, "admit") }

// ppFixture is step 4's fixture with trusted contracts on json.loads, the
// installed tinyver's parse and the application's store.save, and a proof
// check on admit, which reaches only tinyver.parse.
func ppFixture(t *testing.T, module string) (string, *project.Project) {
	t.Helper()
	root, p := ptcFixture(t)
	pa310Write(t, root, ppSource, module)
	b, err := os.ReadFile(filepath.Join(root, ptcContracts))
	if err != nil {
		t.Fatal(err)
	}
	pa310Write(t, root, ptcContracts, string(b)+ppAbstraction)
	contracts := p.Manifest.Components.TrustedContracts
	for i, c := range contracts {
		if c.ID == "tinyver.parse" {
			contracts[i].Contract = pa310Ref(ptcContracts, "version_of")
		}
	}
	p.Manifest.Existing = append(p.Manifest.Existing, ppSource)
	p.Manifest.Components.Checks = append(p.Manifest.Components.Checks, project.ComponentCheck{Target: pa310Ref(ppSource, "admit"),
		Proof: &project.ComponentProof{Contracts: []project.ComponentRef{pa310Ref(ppSource, "admit")}}})
	pa310Save(t, p)
	return root, p
}

// ppNagini stands in for the pinned Nagini: it reads the input as Nagini
// would and answers with the output it's given.
type ppNagini struct {
	calls    int
	output   string
	exit     int
	main     string
	commands []PythonApplicationNaginiCommand
	during   func(dir string)
}

func (n *ppNagini) run(_ context.Context, c PythonApplicationNaginiCommand) (PythonApplicationProcess, error) {
	n.calls++
	n.commands = append(n.commands, c)
	b, err := os.ReadFile(filepath.Join(c.Dir, c.Main))
	if err != nil {
		return PythonApplicationProcess{}, err
	}
	n.main = string(b)
	if n.during != nil {
		n.during(c.Dir)
	}
	return PythonApplicationProcess{Stdout: n.output, ExitCode: n.exit}, nil
}

const ppVerified = "Verification successful\nVerification took 21.10 seconds.\n"

// ppDistributions stands in for the dependency image's installed
// distributions, as their RECORDs say: tinyver's module and, where a test
// adds them, others'.
func ppDistributions(extra map[string][]string) func(context.Context, string) (map[string][]string, error) {
	return func(context.Context, string) (map[string][]string, error) {
		out := map[string][]string{"tinyver": {"tinyver"}}
		for k, v := range extra {
			out[k] = v
		}
		return out, nil
	}
}

func ppSession(t *testing.T, p *project.Project, n *ppNagini) *PythonApplicationSession {
	t.Helper()
	return pa315Prepare(t, p, PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0"), Nagini: n.run, Distributions: ppDistributions(nil)})
}

func ppFile(t *testing.T, in *PythonApplicationProofInput, name string) string {
	t.Helper()
	for _, f := range in.Files {
		if f.Path == name {
			return f.Content
		}
	}
	t.Fatalf("no verifier input file %s in %+v", name, in.Files)
	return ""
}

func ppBoundaries(in *PythonApplicationProofInput) map[string]PythonApplicationProofBoundary {
	out := map[string]PythonApplicationProofBoundary{}
	for _, b := range in.Boundaries {
		out[b.Name] = b
	}
	return out
}

func TestPythonApplicationProofInputKeepsTheFunctionAsWritten(t *testing.T) {
	root, p := ppFixture(t, ppModule)
	before := pa315Tree(t, root)
	s := ppSession(t, p, &ppNagini{})
	in, err := s.ProofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	main := ppFile(t, in, PythonApplicationProofMain)
	want := "from nagini_contracts.contracts import *\nLIMIT = 3\nimport invariant_boundary_0 as tinyver\n\n\n" + ppTarget
	if main != want {
		t.Fatalf("the verifier input's module:\n%s\nwant:\n%s", main, want)
	}
	for _, left := range []string{"async def report", "next_state", "save", "import tinyver\n"} {
		if strings.Contains(main, left) {
			t.Fatalf("the input kept %q, which the function doesn't reach:\n%s", left, main)
		}
	}
	boundary := ppFile(t, in, "invariant_boundary_0.py")
	if want := "from nagini_contracts.contracts import *\n\n\n@ContractOnly\ndef parse(text: str) -> object:\n    Requires(len(text) > 0)\n"; boundary != want {
		t.Fatalf("the abstraction's module:\n%s\nwant:\n%s", boundary, want)
	}
	// Every span maps to the original bytes it copies, the function's
	// among them, exactly once.
	var target *PythonApplicationProofSpan
	for i, s := range in.Spans {
		if s.Kind == "target" {
			if target != nil {
				t.Fatal("the function is in the input twice")
			}
			target = &in.Spans[i]
		}
		if s.Source != "" && s.Kind != "signature" && s.SHA256 != s.SourceSHA256 {
			t.Fatalf("a copied span changed its bytes: %+v", s)
		}
	}
	if target == nil || target.File != PythonApplicationProofMain || target.Line != 6 || target.EndLine != 14 ||
		target.Source != ppSource || target.SourceLine != 9 || target.SourceEndLine != 17 || target.SHA256 != pa310Hash([]byte(ppTarget)) {
		t.Fatalf("the function's mapping: %+v", target)
	}
	signature := slices.IndexFunc(in.Spans, func(s PythonApplicationProofSpan) bool { return s.Kind == "signature" })
	if signature < 0 || in.Spans[signature].Contract != "tinyver.parse" || in.Spans[signature].Source != ptcContracts || in.Spans[signature].SHA256 == in.Spans[signature].SourceSHA256 {
		t.Fatalf("the renamed abstraction's mapping: %+v", in.Spans)
	}
	got := ppBoundaries(in)
	for name, kind := range map[string]string{"Requires": "nagini", "Ensures": "nagini", "Implies": "nagini", "Result": "nagini",
		"LIMIT": "constant", "len": "native", "int": "native", "str": "native", "tinyver": "contract"} {
		if got[name].Kind != kind {
			t.Fatalf("%s is bound as %+v, want %s", name, got[name], kind)
		}
	}
	if len(got) != 9 {
		t.Fatalf("boundaries: %+v", in.Boundaries)
	}
	if op := got["tinyver"].Operation; got["tinyver"].Contract != "tinyver.parse" || op == nil || op.Kind != "dependency" || op.Distribution != "tinyver" || op.Symbol != "parse" {
		t.Fatalf("tinyver's boundary: %+v %+v", got["tinyver"], op)
	}
	// Only the contract the function reaches is assumed: json.loads and
	// store.save stay declarations.
	if len(in.Assumed) != 1 || in.Assumed[0].Contract != "tinyver.parse" || in.Assumed[0].Declaration.ID != pa310ID(ptcContracts, "version_of") {
		t.Fatalf("assumed: %+v", in.Assumed)
	}
	if !reflect.DeepEqual(in.Excluded, []string{pa310ID(ppSource, "report")}) || len(in.Unaccounted) != 0 {
		t.Fatalf("excluded %v, unaccounted %+v", in.Excluded, in.Unaccounted)
	}
	if len(in.Sources) != 2 || in.Sources[0].Path != ppSource || in.Sources[1].Path != ptcContracts || in.Sources[0].SHA256 != pa310Hash([]byte(ppModule)) {
		t.Fatalf("sources: %+v", in.Sources)
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("building the input changed the application")
	}
	// The same application, staged again elsewhere, gets the same input.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := copyTree(root, moved); err != nil {
		t.Fatal(err)
	}
	q, err := project.Load(filepath.Join(moved, pa315Project))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ppSession(t, q, &ppNagini{}).ProofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint != in.Fingerprint || !reflect.DeepEqual(again.Files, in.Files) || !reflect.DeepEqual(again.Spans, in.Spans) {
		t.Fatal("a relocated application built another input")
	}
}

func TestPythonApplicationProofReportsWhatNoContractBinds(t *testing.T) {
	for _, c := range []struct {
		name, from, to string
		drop           string
		kind           string
		op             project.ResolvedComponentOperation
	}{
		{name: "a dependency", drop: "tinyver.parse", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "dependency", Distribution: "tinyver", Module: "tinyver", Symbol: "parse"}},
		{name: "the standard library", from: "version = tinyver.parse(text)", to: "version = json.dumps(text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}},
		{name: "a builtin", from: "version = tinyver.parse(text)", to: "print(text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "standard_library", Module: "builtins", Symbol: "print"}},
		{name: "the application", from: "version = tinyver.parse(text)", to: "version = next_state(text, text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "application", Declaration: pa310ID("tasks/helper.py", "next_state")}},
		{name: "a module no wheel provides", from: "version = tinyver.parse(text)", to: "version = yamlish.load(text)", kind: UnaccountedAssumption},
		{name: "two names for one operation", from: "version = tinyver.parse(text)", to: "version = json.dumps(text) + dumps(text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}},
		// A module named other than its distribution is the distribution's,
		// as its RECORD says, as yaml is PyYAML's.
		{name: "a module named other than its distribution", from: "version = tinyver.parse(text)", to: "version = yaml.safe_load(text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "dependency", Distribution: "pyyaml", Module: "yaml", Symbol: "safe_load"}},
		// The comprehension's own print is its own, so the call after it
		// reaches the builtin.
		{name: "a builtin a comprehension's variable shares a name with", from: "version = tinyver.parse(text)",
			to: "shown = [print for print in [text]]\n        print(text)", kind: MissingContract,
			op: project.ResolvedComponentOperation{Kind: "standard_library", Module: "builtins", Symbol: "print"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			module := strings.Replace(ppModule, "import tinyver\n", "import tinyver\nimport json\nimport yamlish\nimport yaml\nfrom json import dumps\n", 1)
			if c.from != "" {
				module = strings.Replace(module, c.from, c.to, 1)
			}
			_, p := ppFixture(t, module)
			if c.drop != "" {
				p.Manifest.Components.TrustedContracts = slices.DeleteFunc(p.Manifest.Components.TrustedContracts, func(x project.ComponentContract) bool { return x.ID == c.drop })
				pa310Save(t, p)
			}
			n := &ppNagini{output: ppVerified}
			s := pa315Prepare(t, p, PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0"), Nagini: n.run,
				Distributions: ppDistributions(map[string][]string{"yaml": {"pyyaml"}})})
			proof, err := s.Prove(context.Background(), ppAdmitID())
			if err != nil {
				t.Fatal(err)
			}
			u := proof.Result.Unaccounted
			if len(u) != 1 || u[0].Kind != c.kind || (c.kind == MissingContract && (u[0].Operation == nil || *u[0].Operation != c.op)) {
				t.Fatalf("unaccounted: %+v", u)
			}
			// Nothing binds a name the function reaches, so the input is
			// incomplete and Nagini doesn't run.
			if proof.Ran || n.calls != 0 || proof.Result.Passed || len(proof.Result.Verified) != 0 || len(proof.Result.Errors) != 1 {
				t.Fatalf("an incomplete input ran or passed: %+v (calls %d)", proof.Result, n.calls)
			}
			if c.kind == UnaccountedAssumption {
				if !strings.Contains(u[0].Detail, "yamlish.load") || !strings.Contains(proof.Result.Errors[0], "yamlish") {
					t.Fatalf("detail %q, errors %q", u[0].Detail, proof.Result.Errors)
				}
				return
			}
			if !strings.Contains(proof.Result.Errors[0], "no trusted contract binds") {
				t.Fatalf("errors: %q", proof.Result.Errors)
			}
			// The attempt is attributable, and proves nothing.
			r := &Report{Project: "task lifecycle", Bounds: map[string]string{"Limit": "2"}, Toolchain: Toolchain{PythonImage: toolchain.PythonImage, PythonDependencies: s.Provenance().Dependencies}}
			if err := r.AttributeComponentProofs(s.inventory, []ComponentProofResult{proof.Result}); err != nil {
				t.Fatal(err)
			}
			for _, row := range r.Components.Proofs {
				if row.Target == ppAdmitID() && row.Status != "unchecked" {
					t.Fatalf("an attempt with a missing contract proved %s", row.Target)
				}
			}
			if !slices.ContainsFunc(r.Components.ContractDiagnostics, func(d ComponentContractDiagnostic) bool { return d.Kind == MissingContract && d.Target == ppAdmitID() }) {
				t.Fatalf("diagnostics: %+v", r.Components.ContractDiagnostics)
			}
		})
	}
}

func TestPythonApplicationProofRefusesWhatItCantPreserve(t *testing.T) {
	body := func(from, to string) func(string) string {
		return func(m string) string { return strings.Replace(m, from, to, 1) }
	}
	for _, c := range []struct {
		name   string
		module func(string) string
		want   string
	}{
		{"an async function", body("def admit(", "async def admit("), "async"},
		{"a decorator", body("def admit(", "@staticmethod\ndef admit("), "decorator"},
		{"ContractOnly", body("def admit(", "@ContractOnly\ndef admit("), "decorator"},
		{"a nested function", body("    if len(text) > 0:", "    def inner() -> int:\n        return 1\n    if len(text) > 0:"), "nested function"},
		{"a computed constant", body("LIMIT = 3", "LIMIT = int('3')"), "immutable literal"},
		{"a mutable constant", body("LIMIT = 3", "LIMIT = [3]"), "immutable literal"},
		{"a constant rebound elsewhere", body("LIMIT = 3\n", "LIMIT = 3\n\n\ndef reset() -> None:\n    global LIMIT\n    LIMIT = 0\n"), "global statement"},
		{"a name bound twice", body("LIMIT = 3\n", "LIMIT = 3\nLIMIT = 4\n"), "more than once"},
		{"a constant changed in place", body("LIMIT = 3\n", "LIMIT = 3\nLIMIT += 1\n"), "more than once"},
		{"another star import", body("import tinyver\n", "import tinyver\nfrom os.path import *\n"), "star import"},
		{"a class", body("LIMIT = 3\n", "LIMIT = 3\n\n\nclass Box:\n    pass\n"), ""},
		{"a deep attribute", body("version = tinyver.parse(text)", "version = tinyver.sub.parse(text)"), "other than to call"},
		{"a module used bare", body("version = tinyver.parse(text)", "version = tinyver"), "other than to call"},
		{"varargs", body("def admit(text: str, admitted: int)", "def admit(text: str, admitted: int, *rest: int)"), "*args"},
		{"an unbound name", body("version = tinyver.parse(text)", "version = undefined_thing(text)"), "isn't bound"},
		{"a conditional binding", body("LIMIT = 3\n", "if True:\n    LIMIT = 3\n"), "conditionally"},
		{"a shared line", body("LIMIT = 3\n", "LIMIT = 3; HOOK = print('installed')\n"), "more than one statement"},
		{"another encoding", func(m string) string { return "# -*- coding: latin-1 -*-\n" + m }, "latin-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			module := c.module(ppModule)
			if c.name == "a class" {
				module = strings.Replace(module, "if len(text) > 0:", "if isinstance(text, Box):\n        return admitted\n    if len(text) > 0:", 1)
				c.want = "class"
			}
			_, p := ppFixture(t, module)
			n := &ppNagini{output: ppVerified}
			s := ppSession(t, p, n)
			if _, err := s.Prove(context.Background(), ppAdmitID()); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
			if n.calls != 0 {
				t.Fatal("Nagini ran on an input that can't be built")
			}
		})
	}
	// An abstraction is a contract and nothing more.
	for _, c := range []struct{ name, abstraction, want string }{
		{"a body", "\n\ndef version_of(text: str) -> object:\n    Requires(len(text) > 0)\n    return text\n", "isn't a contract"},
		{"another name", "\n\ndef version_of(text: str) -> object:\n    Requires(len(text) > LIMIT)\n    ...\n", "reads LIMIT"},
		{"Assume", "\n\ndef version_of(text: str) -> object:\n    Requires(len(text) > 0)\n    Assume(len(text) > 1)\n", "isn't a contract"},
	} {
		t.Run("abstraction with "+c.name, func(t *testing.T) {
			root, p := ppFixture(t, ppModule)
			b, _ := os.ReadFile(filepath.Join(root, ptcContracts))
			pa310Write(t, root, ptcContracts, strings.Replace(string(b), ppAbstraction, c.abstraction, 1))
			s := ppSession(t, p, &ppNagini{output: ppVerified})
			if _, err := s.Prove(context.Background(), ppAdmitID()); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
	// A session refuses a selection in-place proof doesn't support before
	// any tool runs.
	for _, c := range []struct {
		name   string
		change func(*project.Project, string)
		want   string
	}{
		{"another contract declaration", func(p *project.Project, root string) {
			pa310Write(t, root, ppSource, ppModule+"\n\ndef bounded(n: int) -> int:\n    Requires(n >= 0)\n    return n\n")
			check := &p.Manifest.Components.Checks[len(p.Manifest.Components.Checks)-1]
			check.Proof.Contracts = append(check.Proof.Contracts, pa310Ref(ppSource, "bounded"))
		}, "own inline contract"},
		{"a proof invocation", func(p *project.Project, root string) {
			check := &p.Manifest.Components.Checks[len(p.Manifest.Components.Checks)-1]
			check.Invocations = []project.ComponentInvocation{{ID: "nagini", Kind: "proof", Check: check.Target, Command: "python", Args: []string{"-m", "nagini"}, WorkingDir: "."}}
		}, "pinned Nagini"},
		{"a method", func(p *project.Project, root string) {
			pa310Write(t, root, ppSource, ppModule+"\n\nclass Desk:\n    def admit(self, n: int) -> int:\n        Requires(n >= 0)\n        return n\n")
			check := &p.Manifest.Components.Checks[len(p.Manifest.Components.Checks)-1]
			check.Target = pa310Ref(ppSource, "Desk.admit")
			check.Proof.Contracts = []project.ComponentRef{check.Target}
		}, "top level"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := ppFixture(t, ppModule)
			c.change(p, root)
			pa310Save(t, p)
			assertNoTools := pa315TrapTools(t)
			_, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0")})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
			assertNoTools()
		})
	}
}

func TestPythonApplicationProofCopiesASharedContractLineOnce(t *testing.T) {
	root, p := ppFixture(t, ppModule)
	b, _ := os.ReadFile(filepath.Join(root, ptcContracts))
	pa310Write(t, root, ptcContracts, strings.Replace(string(b), ppAbstraction,
		"\n\ndef version_of(text: str) -> object:\n    Requires(len(text) > 0); Ensures(Result() is not None)\n", 1))
	in, err := ppSession(t, p, &ppNagini{}).ProofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	if got := ppFile(t, in, "invariant_boundary_0.py"); strings.Count(got, "Requires(len(text) > 0); Ensures(Result() is not None)\n") != 1 {
		t.Fatalf("the abstraction's module:\n%s", got)
	}
}

func TestPythonApplicationProofChecksItsInputAgainstTheApplication(t *testing.T) {
	_, p := ppFixture(t, ppModule)
	s := ppSession(t, p, &ppNagini{output: ppVerified})
	in, _, err := s.proofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.proofSource(in.Target)
	if err != nil {
		t.Fatal(err)
	}
	abstraction, err := s.proofSource(s.declarations[pa310ID(ptcContracts, "version_of")])
	if err != nil {
		t.Fatal(err)
	}
	read := func(source string) ([]byte, error) {
		switch source {
		case ppSource:
			return original, nil
		case ptcContracts:
			return abstraction, nil
		}
		return nil, errors.New("unknown source")
	}
	contents := func(in *PythonApplicationProofInput) map[string][]byte {
		out := map[string][]byte{}
		for _, f := range in.Files {
			out[f.Path] = []byte(f.Content)
		}
		return out
	}
	if err := checkPythonProofSpans(in, contents(in), read); err != nil {
		t.Fatalf("a faithful input was refused: %v", err)
	}
	// A rewritten implementation in the input isn't the application's.
	files := contents(in)
	files[PythonApplicationProofMain] = []byte(strings.Replace(string(files[PythonApplicationProofMain]), "return admitted + 1", "return admitted + 2", 1))
	if err := checkPythonProofSpans(in, files, read); err == nil {
		t.Fatal("a rewritten function passed for the application's")
	}
	// A span recorded as a copy that holds other bytes is refused, even when
	// its own hash matches.
	changed := *in
	changed.Spans = slices.Clone(in.Spans)
	for i, s := range changed.Spans {
		if s.Kind == "target" {
			changed.Spans[i].SourceSHA256 = pa310Hash([]byte("def admit(): pass\n"))
		}
	}
	if err := checkPythonProofSpans(&changed, contents(in), read); err == nil {
		t.Fatal("a target detached from its source passed")
	}
	// The original changing after the session staged it makes the input stale.
	if err := os.WriteFile(filepath.Join(s.root, ppSource), []byte(strings.Replace(ppModule, "admitted + 1", "admitted + 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProofInput(context.Background(), ppAdmitID()); err == nil {
		t.Fatal("an input was built from a changed source")
	}
	// What Nagini reads must be what the input records.
	_, p = ppFixture(t, ppModule)
	n := &ppNagini{output: ppVerified, during: func(dir string) {
		os.WriteFile(filepath.Join(dir, PythonApplicationProofMain), []byte("def admit(text, admitted):\n    return admitted\n"), 0o644)
	}}
	if _, err := ppSession(t, p, n).Prove(context.Background(), ppAdmitID()); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("an input that changed under Nagini was accepted: %v", err)
	}
}

func TestPythonApplicationProofRunsTheVerifierOnTheInput(t *testing.T) {
	_, p := ppFixture(t, ppModule)
	n := &ppNagini{output: ppVerified}
	s := ppSession(t, p, n)
	proof, err := s.Prove(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	r := proof.Result
	if !proof.Ran || n.calls != 1 || !r.Passed || !reflect.DeepEqual(r.Verified, []string{ppAdmitID()}) || r.Verifier != "Nagini" || len(r.Errors) != 0 {
		t.Fatalf("a verified input: %+v", proof)
	}
	if n.main != ppFile(t, &proof.Input, PythonApplicationProofMain) || n.commands[0].Main != PythonApplicationProofMain {
		t.Fatal("Nagini didn't read the recorded input")
	}
	// The pins name the recipe, the same on every machine, and never a local
	// image, which the attempt records beside them.
	tag := "invariant-nagini:" + strings.TrimPrefix(toolchain.NaginiRecipe(), "sha256:")[:12]
	if r.Tool == nil || r.Tool.Name != "Nagini" || r.Tool.Version != NaginiVersion || r.Tool.Pins["recipe"] != toolchain.NaginiRecipe() ||
		r.Tool.Pins["platform"] != "linux/amd64" || r.Tool.Pins["input"] != proof.Input.Fingerprint || len(r.Tool.Pins) != 3 ||
		proof.Image != tag || proof.Recipe != toolchain.NaginiRecipe() {
		t.Fatalf("the tool's identity: %+v, image %q", r.Tool, proof.Image)
	}
	if !slices.Contains(proof.Command, "/input:/work") || !slices.Contains(proof.Command, "none") || slices.ContainsFunc(proof.Command, func(a string) bool { return strings.Contains(a, os.TempDir()) }) {
		t.Fatalf("the command: %v", proof.Command)
	}
	ids := []string{}
	for _, c := range r.Inputs {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{ppAdmitID(), pa310ID(ptcContracts, "version_of")}) || !reflect.DeepEqual(r.FileInputs, proof.Input.Sources) {
		t.Fatalf("inputs %v, files %+v", ids, r.FileInputs)
	}
	b, err := json.Marshal(proof)
	if err != nil || strings.Contains(string(b), s.root) {
		t.Fatalf("the attempt names a session path: %v", err)
	}
	// A failure reports Nagini's errors where the application has them.
	n.output = "Verification failed\nErrors:\nPostcondition of admit might not hold. Assertion (Result() >= admitted) might not hold. (invariant_target.py@9.12--9.32).\nVerification took 3.20 seconds.\n"
	n.exit = 1
	proof, err = s.Prove(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	want := "tasks/admission.py:12:12: Postcondition of admit might not hold. Assertion (Result() >= admitted) might not hold. (verifier input invariant_target.py:9)"
	if proof.Result.Passed || len(proof.Result.Verified) != 0 || !reflect.DeepEqual(proof.Result.Errors, []string{want}) {
		t.Fatalf("errors: %q", proof.Result.Errors)
	}
	// Assume is an assumption nothing accounts for: Nagini runs, and the
	// attempt covers nothing.
	_, p = ppFixture(t, strings.Replace(ppModule, "    return admitted\n\n", "    Assume(admitted < 2)\n    return admitted\n\n", 1))
	n = &ppNagini{output: ppVerified}
	proof, err = ppSession(t, p, n).Prove(context.Background(), ppAdmitID())
	if err != nil || n.calls != 1 || len(proof.Result.Unaccounted) != 1 || proof.Result.Unaccounted[0].Kind != UnaccountedAssumption {
		t.Fatalf("Assume: %v %+v", err, proof)
	}
}

func TestPythonApplicationProofAttributesWithItsBackingTests(t *testing.T) {
	_, p := ppFixture(t, ppModule)
	libraries := ptcLibraries(t, ptcTinyver)
	n := &ppNagini{output: ppVerified}
	s := pa315Prepare(t, p, PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0"), Execute: pa315LocalPython(t, libraries), Nagini: n.run})
	r := ptcRun(t, s)
	proof, err := s.Prove(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentProofs(s.inventory, []ComponentProofResult{proof.Result}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, use string }{{"tinyver.parse", "assumed"}, {"json.loads", "declared"}, {"store.save", "declared"}} {
		if got := ptcStanding(t, r, c.id); got.Support != "tested" || got.Use != c.use {
			t.Fatalf("%s: %+v; diagnostics %+v", c.id, got, r.Components.ContractDiagnostics)
		}
	}
	if got := ptcStanding(t, r, "tinyver.parse"); !reflect.DeepEqual(got.Relying, []string{ppAdmitID()}) {
		t.Fatalf("relying: %+v", got)
	}
	for _, row := range r.Components.Proofs {
		if want := map[bool]string{true: "proved", false: "unchecked"}[row.Target == ppAdmitID()]; row.Status != want {
			t.Fatalf("%s: %s, want %s", row.Target, row.Status, want)
		}
	}
	if len(r.Components.ContractDiagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", r.Components.ContractDiagnostics)
	}
}

func TestPythonApplicationProofTheGateStillRefuses(t *testing.T) {
	_, p := ppFixture(t, ppModule)
	assertNoTools := pa315TrapTools(t)
	if err := Check(p.Dir); err == nil || !strings.Contains(err.Error(), "doesn't prove Python in place yet") {
		t.Fatalf("Check: %v", err)
	}
	if r, err := Run(context.Background(), p.Dir, "", toolchain.Toolchain{}); err == nil || r != nil {
		t.Fatalf("Run: %+v %v", r, err)
	}
	if b, _, err := (Python{}).Check(context.Background(), p); err == nil || b.Passed {
		t.Fatalf("Python.Check: %v", err)
	}
	assertNoTools()
}

// ppContract adds a trusted contract on op, its abstraction and a backing
// test, as #353's review did.
func ppContract(t *testing.T, root string, p *project.Project, id string, op project.ComponentOperation, abstraction, symbol, test, body string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, ptcContracts))
	pa310Write(t, root, ptcContracts, string(b)+abstraction)
	b, _ = os.ReadFile(filepath.Join(root, ptcTests))
	pa310Write(t, root, ptcTests, string(b)+body)
	p.Manifest.Components.TrustedContracts = append(p.Manifest.Components.TrustedContracts, ptcContract(id, op, symbol, test))
	pa310Save(t, p)
}

// Each hole #353's independent review found let an attempt pass, proved,
// while the real function breaks its contract; the pinned Nagini confirmed
// each. Each is refused now before any verifier runs.
func TestPythonApplicationProofRefusesTheReviewedHoles(t *testing.T) {
	recursive := `def admit(text: str, admitted: int) -> int:
    Requires(admitted >= 0)
    Ensures(Result() == admitted)
    if admitted > 0:
        return admit(text, admitted - 1) + 1
    return 0
`
	replace := func(from, to string) func(string) string {
		return func(m string) string { return strings.Replace(m, from, to, 1) }
	}
	dumps := func(t *testing.T, root string, p *project.Project) {
		ppContract(t, root, p, "json.dumps", project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"},
			"\n\ndef dumps_of(value: object) -> str:\n    Ensures(len(Result()) >= 0)\n    ...\n", "dumps_of", "DumpsTests.test_dumps",
			"\n\nclass DumpsTests(unittest.TestCase):\n    def test_dumps(self):\n        self.assertEqual(json.dumps(1), '1')\n")
	}
	application := func(symbol, abstraction string) func(*testing.T, string, *project.Project) {
		return func(t *testing.T, root string, p *project.Project) {
			ppContract(t, root, p, "admission."+symbol, project.ComponentOperation{Kind: "application", Source: ppSource, Symbol: symbol},
				abstraction, symbol+"_of", "Of"+symbol+".test_of", "\n\nclass Of"+symbol+"(unittest.TestCase):\n    def test_of(self):\n        pass\n")
		}
	}
	for _, c := range []struct {
		name     string
		module   func(string) string
		contract func(*testing.T, string, *project.Project)
		other    map[string]string
		want     string
	}{
		// The function's own name, which callers and its recursion reach.
		{name: "the function rebound", module: func(m string) string {
			return strings.Replace(m, ppTarget, recursive, 1) + "\n\ndef _fast(text: str, admitted: int) -> int:\n    return 42\n\n\nadmit = _fast\n"
		}, want: "other than by its one def"},
		{name: "the function rebound conditionally", module: func(m string) string { return m + "\n\nif LIMIT > 2:\n    admit = report\n" }, want: "other than by its one def"},
		{name: "the function rebound by a global statement", module: func(m string) string {
			return m + "\n\ndef swap() -> None:\n    global admit\n    admit = report\n"
		}, want: "other than by its one def"},
		{name: "the function wrapped by assignment", module: func(m string) string {
			return m + "\n\ndef traced(f):\n    return f\n\n\nadmit = traced(admit)\n"
		}, want: "other than by its one def"},
		// Names Nagini reads as its own whatever the module binds them to.
		{name: "Assume imported from json", module: func(m string) string {
			m = strings.Replace(m, "import tinyver\n", "import tinyver\nfrom json import dumps as Assume\n", 1)
			m = strings.Replace(m, "    Ensures(Result() >= admitted)\n", "    Ensures(Result() >= admitted)\n    Ensures(Result() == admitted + 100)\n", 1)
			return strings.Replace(m, "    if len(text) > 0:\n", "    Assume(False)\n    if len(text) > 0:\n", 1)
		}, contract: dumps, want: "reads Assume, which Nagini reads as its own"},
		{name: "Assume imported from json beside Nagini's own names", module: func(string) string {
			return "from nagini_contracts.contracts import Requires, Ensures, Result\nfrom json import dumps as Assume\n\n\n" +
				"def admit(text: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Ensures(Result() == admitted + 100)\n    Assume(False)\n    return admitted\n\n\n" +
				"async def report(text: str) -> str:\n    return text\n"
		}, contract: dumps, want: "reads Assume, which Nagini reads as its own"},
		{name: "the module's own len", module: func(string) string {
			return "from nagini_contracts.contracts import *\n\n\ndef len(s: str) -> int:\n    return 0\n\n\n" +
				"def admit(text: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Ensures(Result() == 5)\n    return len('hello')\n\n\n" +
				"async def report(text: str) -> str:\n    return text\n"
		}, contract: application("len", "\n\ndef len_of(s: str) -> int:\n    Ensures(Result() == 0)\n    ...\n"), want: "reads len, which Nagini reads as its own"},
		{name: "the module's own range", module: func(m string) string {
			m = strings.Replace(m, "LIMIT = 3\n", "LIMIT = 3\n\n\ndef range(n: int) -> int:\n    return n\n", 1)
			return strings.Replace(m, "        return admitted + 1\n", "        return admitted + 1 + range(0)\n", 1)
		}, contract: application("range", "\n\ndef range_of(n: int) -> int:\n    Ensures(Result() == n)\n    ...\n"), want: "reads range, which Nagini reads as its own"},
		{name: "a parameter named len", module: replace("def admit(text: str, admitted: int)", "def admit(text: str, admitted: int, len: int = 0)"), want: "binds len, which Nagini reads as its own"},
		{name: "Nagini's Requires imported under another name", module: func(m string) string {
			m = strings.Replace(m, "from nagini_contracts.contracts import *\n", "from nagini_contracts.contracts import *\nfrom nagini_contracts.contracts import Requires as Pre\n", 1)
			return strings.Replace(m, "    Requires(admitted >= 0)\n", "    Pre(admitted >= 0)\n", 1)
		}, want: ""},
		// What a trusted contract says its operation raises.
		{name: "a bare except around a boundary call", module: func(string) string {
			return "from nagini_contracts.contracts import *\nimport json\n\n\n" +
				"def admit(text: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Requires(len(text) > 0)\n    Ensures(Result() == admitted + 1)\n" +
				"    try:\n        json.loads(text)\n    except:\n        return admitted\n    return admitted + 1\n\n\n" +
				"async def report(text: str) -> str:\n    return text\n"
		}, want: "bare except"},
		{name: "an except Exception the contract doesn't declare", module: func(string) string {
			return "from nagini_contracts.contracts import *\nimport json\n\n\n" +
				"def admit(text: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Requires(len(text) > 0)\n    Ensures(Result() == admitted + 1)\n" +
				"    try:\n        json.loads(text)\n    except Exception:\n        return admitted\n    return admitted + 1\n\n\n" +
				"async def report(text: str) -> str:\n    return text\n"
		}, want: "doesn't declare with Exsures"},
		{name: "an exception Nagini doesn't model", module: replace("    return admitted\n\n\nasync", "    raise ValueError('empty')\n\n\nasync"), want: "an exception class the pinned Nagini doesn't model"},
		// Writes the module's bindings don't show.
		{name: "globals()", module: replace("LIMIT = 3\n", "LIMIT = 3\nglobals()['LIMIT'] = 100\n"), want: "with globals"},
		{name: "setattr on sys.modules", module: replace("LIMIT = 3\n", "import sys\nLIMIT = 3\nsetattr(sys.modules[__name__], 'LIMIT', 100)\n"), want: "writes the module's names with"},
		{name: "another module writing a constant", module: func(m string) string { return m },
			other: map[string]string{"tasks/config.py": "from tasks import admission\n\nadmission.LIMIT = 100\n"}, want: "tasks/config.py:3 writes tasks.admission.LIMIT"},
		{name: "another module writing the function", module: func(m string) string { return m },
			other: map[string]string{"tasks/config.py": "import tasks.admission as adm\n\nadm.admit = None\n"}, want: "writes tasks.admission.admit"},
		{name: "another module writing through a relative import", module: func(m string) string { return m },
			other: map[string]string{"tasks/config.py": "from . import admission\n\ndel admission.LIMIT\n"}, want: "writes tasks.admission.LIMIT"},
		// Operations an abstraction can't stand for, and imports that fail.
		{name: "an async application operation", module: func(m string) string {
			m = strings.Replace(m, "        version = tinyver.parse(text)\n", "        version = tinyver.parse(text)\n        pending = fetch(text)\n", 1)
			return m + "\n\nasync def fetch(text: str) -> int:\n    return len(text)\n"
		}, contract: application("fetch", "\n\ndef fetch_of(text: str) -> int:\n    Ensures(Result() == len(text))\n    ...\n"), want: "async function, whose call returns a coroutine"},
		{name: "a relative import above the top-level package", module: func(m string) string {
			m = strings.Replace(m, "from .helper import next_state\n", "from ..toplevel import next_state\n", 1)
			return strings.Replace(m, "        version = tinyver.parse(text)\n", "        version = next_state(text, text)\n", 1)
		}, other: map[string]string{"toplevel.py": "def next_state(a, b):\n    return a\n"}, want: "above the top-level package"},
		// Lines the input can't copy as they are.
		{name: "an annotation the input can't bind", module: replace("LIMIT = 3\n", "LIMIT: Final = 3\n"), want: "annotation reads Final"},
		{name: "a line continuation", module: replace("LIMIT = 3\n", "LIMIT = 3 \\\n\nHOOK = 1\n"), want: "line continuation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := ppFixture(t, c.module(ppModule))
			for name, body := range c.other {
				pa310Write(t, root, name, body)
			}
			if c.contract != nil {
				c.contract(t, root, p)
			}
			n := &ppNagini{output: ppVerified}
			proof, err := ppSession(t, p, n).Prove(context.Background(), ppAdmitID())
			if c.want == "" {
				// Nagini reads Pre as nothing it knows, and the input
				// refuses the alias rather than run.
				c.want = "imports Nagini's Requires as Pre"
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, %+v; want an error naming %q", err, proof, c.want)
			}
			if n.calls != 0 {
				t.Fatal("Nagini ran on an input that can't stand for the function")
			}
		})
	}
}

// A trusted contract says what its operation raises with Exsures, with an
// exception Nagini models, and a handler may catch only what the contract
// declares.
func TestPythonApplicationProofTakesDeclaredExceptions(t *testing.T) {
	module := "from nagini_contracts.contracts import *\nimport json\nfrom typing import Final\n\nLIMIT: Final = 3\n\n\n" +
		"def admit(text: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Requires(admitted < LIMIT)\n    Requires(len(text) > 0)\n" +
		"    Ensures(Result() >= admitted)\n    Exsures(Exception, True)\n" +
		"    try:\n        json.loads(text)\n    except Exception:\n        return admitted\n    return admitted + 1\n\n\n" +
		"async def report(text: str) -> str:\n    return text\n"
	root, p := ppFixture(t, module)
	b, _ := os.ReadFile(filepath.Join(root, ptcContracts))
	pa310Write(t, root, ptcContracts, strings.Replace(string(b), "def loads(text: str) -> object:\n    Requires(len(text) > 0)\n",
		"def loads(text: str) -> object:\n    Requires(len(text) > 0)\n    Exsures(Exception, True)\n", 1))
	in, err := ppSession(t, p, &ppNagini{}).ProofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	main := ppFile(t, in, PythonApplicationProofMain)
	for _, want := range []string{"from typing import Final\n", "LIMIT: Final = 3\n", "import invariant_boundary_0 as json\n"} {
		if !strings.Contains(main, want) {
			t.Fatalf("the input lacks %q:\n%s", want, main)
		}
	}
	if got := ppFile(t, in, "invariant_boundary_0.py"); !strings.Contains(got, "    Exsures(Exception, True)\n") {
		t.Fatalf("the contract's Exsures didn't reach its stub:\n%s", got)
	}
	if bs := ppBoundaries(in); bs["Exception"].Kind != "native" || bs["json"].Contract != "json.loads" {
		t.Fatalf("boundaries: %+v", in.Boundaries)
	}
	// A contract may declare only exceptions Nagini models.
	pa310Write(t, root, ptcContracts, strings.Replace(string(b), "def loads(text: str) -> object:\n    Requires(len(text) > 0)\n",
		"def loads(text: str) -> object:\n    Requires(len(text) > 0)\n    Exsures(ValueError, True)\n", 1))
	if _, err := ppSession(t, p, &ppNagini{}).ProofInput(context.Background(), ppAdmitID()); err == nil || !strings.Contains(err.Error(), "raises ValueError, which the pinned Nagini doesn't model") {
		t.Fatalf("an unmodeled exception in a contract: %v", err)
	}
}

func TestPythonApplicationProofSignatureRenamesOnlyTheFunction(t *testing.T) {
	_, p := ppFixture(t, ppModule)
	s := ppSession(t, p, &ppNagini{})
	in, _, err := s.proofInput(context.Background(), ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	original, _ := s.proofSource(in.Target)
	abstraction, _ := s.proofSource(s.declarations[pa310ID(ptcContracts, "version_of")])
	read := func(source string) ([]byte, error) {
		if source == ppSource {
			return original, nil
		}
		return abstraction, nil
	}
	contents := map[string][]byte{}
	for _, f := range in.Files {
		contents[f.Path] = []byte(f.Content)
	}
	// A boundary module whose header changes its parameters, even with its
	// recorded hash to match, doesn't rename only the function.
	i := slices.IndexFunc(in.Spans, func(s PythonApplicationProofSpan) bool { return s.Kind == "signature" })
	s2 := in.Spans[i]
	file := contents[s2.File]
	changed := append(append(append([]byte{}, file[:s2.start]...), []byte("def parse(text: str, extra: int) -> object:\n")...), file[s2.end:]...)
	contents[s2.File] = changed
	in.Spans[i].end = s2.start + len("def parse(text: str, extra: int) -> object:\n")
	in.Spans[i].SHA256 = pa310Hash([]byte("def parse(text: str, extra: int) -> object:\n"))
	for j := range in.Spans {
		if j != i && in.Spans[j].File == s2.File && in.Spans[j].start >= s2.end {
			d := in.Spans[i].end - s2.end
			in.Spans[j].start += d
			in.Spans[j].end += d
		}
	}
	if err := checkPythonProofSpans(in, contents, read); err == nil || !strings.Contains(err.Error(), "changes more than the function's name") {
		t.Fatalf("a changed signature: %v", err)
	}
}

func TestPythonApplicationProofReadsDistributionsFromRecords(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := ptcLibraries(t, ptcTinyver)
	pa310Write(t, dir, "yaml/__init__.py", "")
	pa310Write(t, dir, "_yaml/__init__.py", "")
	pa310Write(t, dir, "PyYAML-6.0.2.dist-info/METADATA", "Metadata-Version: 2.1\nName: PyYAML\nVersion: 6.0.2\n")
	pa310Write(t, dir, "PyYAML-6.0.2.dist-info/RECORD", "yaml/__init__.py,sha256=x,1\n_yaml/__init__.py,sha256=y,1\nPyYAML-6.0.2.dist-info/METADATA,,\nPyYAML-6.0.2.dist-info/RECORD,,\n")
	out, err := exec.Command(python, "-I", "-S", "-c", pythonApplicationDistributionsScript, dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"_yaml": {"pyyaml"}, "tinyver": {"tinyver"}, "yaml": {"pyyaml"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("distributions %v, want %v", got, want)
	}
}

// The standard library's and the builtins' names are the pinned runtime's,
// so a missing contract is named the same on every host. Where python3 is
// the pinned image's minor version (INVARIANT_PYTHON_PINNED=1, as in CI's
// second run of the Python tests), they must be its own.
func TestPythonApplicationProofKnowsThePinnedRuntime(t *testing.T) {
	for name, list := range map[string]string{"standard library": pythonStdlibModules, "builtins": pythonBuiltins, "builtin exceptions": pythonBuiltinExceptions,
		"Nagini's names": naginiNamesList, "Nagini's calls": naginiCallsList} {
		if fields := strings.Fields(list); len(fields) < 50 || !slices.IsSorted(fields) {
			t.Fatalf("the %s list: %d names, sorted %v", name, len(fields), slices.IsSorted(fields))
		}
	}
	for _, name := range naginiCalls {
		if !slices.Contains(naginiNames, name) {
			t.Fatalf("Nagini's call %s isn't among its names", name)
		}
	}
	if os.Getenv("INVARIANT_PYTHON_PINNED") != "1" {
		t.Log("INVARIANT_PYTHON_PINNED isn't set, so python3 may be any version here")
		return
	}
	out, err := exec.Command("python3", "-I", "-c", "import builtins, sys\nprint(' '.join(sorted(sys.stdlib_module_names)))\nprint(' '.join(sorted(n for n in dir(builtins) if not n.startswith('_'))))\n"+
		"print(' '.join(sorted(n for n in dir(builtins) if not n.startswith('_') and isinstance(getattr(builtins, n), type) and issubclass(getattr(builtins, n), BaseException))))").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 || !slices.Equal(strings.Fields(lines[0]), strings.Fields(pythonStdlibModules)) || !slices.Equal(strings.Fields(lines[1]), strings.Fields(pythonBuiltins)) ||
		!slices.Equal(strings.Fields(lines[2]), strings.Fields(pythonBuiltinExceptions)) {
		t.Fatalf("the pinned runtime's names changed: refresh python_stdlib_modules.txt, python_builtins.txt and python_builtin_exceptions.txt from toolchain.PythonImage")
	}
}

func TestPythonApplicationProofKeepsMarkedFiles(t *testing.T) {
	// The legacy adapter still proves whole marked files of a core.
	dir := t.TempDir()
	pa310Write(t, dir, "core.py", "# +nagini\nfrom nagini_contracts.contracts import *\n\ndef inc(n: int) -> int:\n    Requires(n >= 0)\n    Ensures(Result() > n)\n    return n + 1\n")
	pa310Write(t, dir, "plain.py", "def other():\n    return 1\n")
	proved, plain, err := naginiSources(dir)
	if err != nil || !reflect.DeepEqual(proved, []string{"core.py"}) || !reflect.DeepEqual(plain, []string{"plain.py"}) {
		t.Fatalf("marked files: %v %v %v", proved, plain, err)
	}
}

// ppRealModule's admit reaches packaging's is_normalized_name, whose result
// says its argument isn't empty: the second postcondition needs the contract.
const ppRealModule = `from nagini_contracts.contracts import *
from packaging.utils import is_normalized_name
import json

LIMIT = 3


def admit(name: str, admitted: int) -> int:
    Requires(admitted >= 0)
    Requires(admitted < LIMIT)
    Ensures(Result() >= admitted)
    Ensures(Implies(Result() > admitted, len(name) > 0))
    if is_normalized_name(name):
        return admitted + 1
    return admitted


async def report(name: str) -> str:
    return f"admitted {name}: {json.dumps(name)}"
`

const ppRealContracts = `from nagini_contracts.contracts import *


def loads(text: str) -> object:
    Requires(len(text) > 0)
    ...


def normalized(name: str) -> bool:
    Ensures(Implies(Result(), len(name) > 0))
    ...
`

const ppRealTests = `import json
import unittest

from packaging.utils import is_normalized_name


class LoadsTests(unittest.TestCase):
    def test_parses(self):
        self.assertEqual(json.loads('{"task": "pending"}'), {"task": "pending"})


class NormalizedTests(unittest.TestCase):
    def test_accepts_normalized(self):
        self.assertTrue(is_normalized_name('task-runner'))

    def test_rejects_empty_and_unnormalized(self):
        self.assertFalse(is_normalized_name(''))
        self.assertFalse(is_normalized_name('Task_Runner'))
`

func ppRealFixture(t *testing.T, module string, contracts bool) (string, *project.Project) {
	t.Helper()
	root, p := pa315Fixture(t, true)
	pa310Write(t, root, ppSource, module)
	pa310Write(t, root, ptcContracts, ppRealContracts)
	pa310Write(t, root, ptcTests, ppRealTests)
	p.Manifest.Existing = append(p.Manifest.Existing, ppSource)
	p.Manifest.Components.Checks = append(p.Manifest.Components.Checks, project.ComponentCheck{Target: pa310Ref(ppSource, "admit"),
		Proof: &project.ComponentProof{Contracts: []project.ComponentRef{pa310Ref(ppSource, "admit")}}})
	p.Manifest.Components.TrustedContracts = []project.ComponentContract{
		ptcContract("json.loads", project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"}, "loads", "LoadsTests.test_parses"),
	}
	if contracts {
		p.Manifest.Components.TrustedContracts = append(p.Manifest.Components.TrustedContracts,
			ptcContract("packaging.is_normalized_name", project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.utils", Symbol: "is_normalized_name"},
				"normalized", "NormalizedTests.test_accepts_normalized", "NormalizedTests.test_rejects_empty_and_unnormalized"))
	}
	pa310Save(t, p)
	return root, p
}

func TestPythonApplicationProofReal(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_PROOF_REAL") != "1" {
		t.Skip("set INVARIANT_PYTHON_APPLICATION_PROOF_REAL=1 to run the pinned Nagini and the real dependency image")
	}
	ctx := context.Background()
	root, p := ppRealFixture(t, ppRealModule, true)
	before := pa315Tree(t, root)
	s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
	// The dependency boundary's backing tests run as step 4 runs them, in
	// the application's own image with packaging installed from its wheel.
	r := ptcRun(t, s)
	if got := ptcStanding(t, r, "packaging.is_normalized_name"); got.Support != "tested" || got.Binding == nil || got.Binding.Wheel == nil || got.Binding.Wheel.Name != "packaging" {
		t.Fatalf("the dependency boundary isn't tested: %+v; diagnostics %+v", got, r.Components.ContractDiagnostics)
	}
	proof, err := s.Prove(ctx, ppAdmitID())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Nagini: %s", strings.TrimSpace(proof.Output))
	if !proof.Ran || !proof.Result.Passed || !reflect.DeepEqual(proof.Result.Verified, []string{ppAdmitID()}) || len(proof.Result.Unaccounted) != 0 {
		t.Fatalf("the selected function wasn't proved: %+v", proof)
	}
	if len(proof.Result.Assumed) != 1 || proof.Result.Assumed[0].Contract != "packaging.is_normalized_name" {
		t.Fatalf("assumed: %+v", proof.Result.Assumed)
	}
	if !pythonApplicationDigest.MatchString(proof.Image) || proof.Result.Tool.Pins["image"] != "" {
		t.Fatalf("the Nagini image: %q", proof.Image)
	}
	if err := r.AttributeComponentProofs(s.inventory, []ComponentProofResult{proof.Result}); err != nil {
		t.Fatal(err)
	}
	if got := ptcStanding(t, r, "packaging.is_normalized_name"); got.Use != "assumed" || got.Support != "tested" {
		t.Fatalf("the used contract: %+v", got)
	}
	if got := ptcStanding(t, r, "json.loads"); got.Use != "declared" {
		t.Fatalf("an unused contract: %+v", got)
	}
	for _, row := range r.Components.Proofs {
		if row.Target == ppAdmitID() && row.Status != "proved" {
			t.Fatalf("the function's proof wasn't attributed: %+v", row)
		}
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("proving the function changed the application")
	}
	// Given the whole module, Nagini stops on the neighbor; the input never
	// shows it.
	if strings.Contains(proof.Input.Files[0].Content, "async def report") {
		t.Fatal("the neighbor reached Nagini")
	}

	t.Run("a function-body violation", func(t *testing.T) {
		_, p := ppRealFixture(t, strings.Replace(ppRealModule, "        return admitted + 1\n", "        return admitted - 1\n", 1), true)
		proof, err := pa315Prepare(t, p, PythonApplicationSessionOptions{}).Prove(ctx, ppAdmitID())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Nagini: %s", strings.TrimSpace(proof.Output))
		if !proof.Ran || proof.Result.Passed || len(proof.Result.Errors) == 0 || !strings.HasPrefix(proof.Result.Errors[0], ppSource+":") {
			t.Fatalf("a violation passed, or wasn't placed in the application: %+v", proof.Result)
		}
	})
	t.Run("an omitted trusted contract", func(t *testing.T) {
		_, p := ppRealFixture(t, ppRealModule, false)
		proof, err := pa315Prepare(t, p, PythonApplicationSessionOptions{}).Prove(ctx, ppAdmitID())
		if err != nil {
			t.Fatal(err)
		}
		u := proof.Result.Unaccounted
		if proof.Ran || proof.Result.Passed || len(u) != 1 || u[0].Kind != MissingContract || u[0].Operation == nil ||
			*u[0].Operation != (project.ResolvedComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.utils", Symbol: "is_normalized_name"}) {
			t.Fatalf("an omitted contract: %+v", proof.Result)
		}
	})
	// A contract that declares what its operation raises reaches Nagini:
	// a handler for it is code Nagini checks, so a postcondition that
	// forgets the handler's path fails.
	for _, c := range []struct {
		name, ensures string
		passes        bool
	}{
		{"a handler the contract declares", "Result() >= admitted", true},
		{"a postcondition the handler breaks", "Result() == admitted + 1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			module := "from nagini_contracts.contracts import *\nimport json\n\n\n" +
				"def admit(name: str, admitted: int) -> int:\n    Requires(admitted >= 0)\n    Requires(len(name) > 0)\n    Ensures(" + c.ensures + ")\n" +
				"    try:\n        json.loads(name)\n    except Exception:\n        return admitted\n    return admitted + 1\n\n\n" +
				"async def report(name: str) -> str:\n    return name\n"
			root, p := ppRealFixture(t, module, false)
			pa310Write(t, root, ptcContracts, strings.Replace(ppRealContracts, "    Requires(len(text) > 0)\n", "    Requires(len(text) > 0)\n    Exsures(Exception, True)\n", 1))
			proof, err := pa315Prepare(t, p, PythonApplicationSessionOptions{}).Prove(ctx, ppAdmitID())
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Nagini: %s", strings.TrimSpace(proof.Output))
			if !proof.Ran || proof.Result.Passed != c.passes || len(proof.Result.Assumed) != 1 || proof.Result.Assumed[0].Contract != "json.loads" {
				t.Fatalf("passed %v, want %v: %+v", proof.Result.Passed, c.passes, proof.Result)
			}
		})
	}
	// The names the input treats as Nagini's are the pinned image's own.
	t.Run("Nagini's names", func(t *testing.T) {
		image, err := toolchain.NaginiImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		script := `import inspect, re
import nagini_translation.translators.call as call
import nagini_translation.lib.constants as c
import nagini_translation.analyzer as a
import nagini_contracts.contracts as k, nagini_contracts.io_contracts as io, nagini_contracts.obligations as ob, nagini_contracts.thread as th
calls = set(call.CONTRACT_WRAPPER_FUNCS) | set(call.CONTRACT_FUNCS) | set(call.IO_CONTRACT_FUNCS) | set(call.OBLIGATION_CONTRACT_FUNCS)
calls |= set(c.BUILTINS) | set(c.THREADING) | set(c.BUILTIN_PREDICATES)
calls |= set(re.findall(r"'(IOExists\d*)'", inspect.getsource(call))) | {'IOExists'}
names = set(calls) | set(re.findall(r"has_decorator\([^,]+, '([A-Za-z_]+)'\)", inspect.getsource(a)))
names |= {n for n in re.findall(r"\.id\s*==\s*'([A-Za-z_]+)'", inspect.getsource(a)) if n != '_'}
for m in (k, io, ob, th):
    names |= set(getattr(m, '__all__', None) or [n for n in dir(m) if not n.startswith('_')])
print(' '.join(sorted(names)))
print(' '.join(sorted(calls)))
print(' '.join(sorted(k.__all__)))
`
		out, err := toolchain.Docker(ctx, "run", "--rm", "--network", "none", "--platform", "linux/amd64", "--entrypoint", "python", image, "-I", "-c", script).Output()
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 3 || !slices.Equal(strings.Fields(lines[0]), naginiNames) || !slices.Equal(strings.Fields(lines[1]), naginiCalls) || !slices.Equal(strings.Fields(lines[2]), naginiExports) {
			t.Fatalf("the pinned Nagini's names changed: refresh nagini_names.txt, nagini_calls.txt and naginiExports from its sources")
		}
	})
}
