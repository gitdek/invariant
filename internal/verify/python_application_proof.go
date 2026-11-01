package verify

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// A selected Python function is proved in its real module (#353, #294's step
// 5): a developer writes Nagini's inline contract calls in the function and
// selects it as a check's proof, with no core.py and no marker on the file.
// The session builds Nagini's input around the function's original bytes:
// the module's imports of Nagini's contracts and typing and its literal
// constants, copied as they are, and every other name the function reads
// bound only to a trusted contract's abstraction, which Nagini assumes and
// never verifies. Nothing else in the module, its neighbors included, goes
// in. Every span the input copies is mapped to the original bytes it came
// from and checked against them before Nagini runs, so a rewritten or
// detached implementation can't stand for the application's. The pinned
// Nagini runs in its own image, where no application requirement is
// installed. The gate doesn't use this yet (#294's step 6 connects it).

//go:embed python_application_proof.py
var pythonApplicationProofAnalyzer string

// pythonStdlibModules are the pinned runtime's sys.stdlib_module_names,
// pythonBuiltins its builtins and pythonBuiltinExceptions its builtin
// exception classes, at toolchain.PythonVersion's minor version. They tell a
// missing contract on the standard library from one on a dependency's code,
// the same on every host.
var (
	//go:embed python_stdlib_modules.txt
	pythonStdlibModules string
	//go:embed python_builtins.txt
	pythonBuiltins string
	//go:embed python_builtin_exceptions.txt
	pythonBuiltinExceptions string
)

// naginiNames are the names the pinned Nagini reads as its own wherever they
// appear, whatever a module binds them to: the contract functions and their
// wrappers, the IO, obligation and thread names, the builtins it translates
// itself, its predicates and the decorators it matches by name, taken from
// its sources (call.py's dispatch, constants.py and the analyzer). A
// function may read one only as Nagini's own export of it, typing's, or the
// real builtin.
//
//go:embed nagini_names.txt
var naginiNamesList string

// naginiCalls are the ones its call translation dispatches on by name alone
// (call.py), even when the function binds the name itself, so the function
// may bind none of them.
//
//go:embed nagini_calls.txt
var naginiCallsList string

var naginiNames, naginiCalls = strings.Fields(naginiNamesList), strings.Fields(naginiCallsList)

// naginiExports are what the pinned Nagini's nagini_contracts.contracts
// exports, so a star import of it binds exactly these.
var naginiExports = strings.Fields(`ARP Acc AllLow Assert Assume ContractOnly Declassify Decreases Ensures Exists
Exsures Fold Forall Forall2 Forall3 Forall4 Forall5 Forall6 Ghost GhostReturns Implies Inline Invariant Let Low
LowEvent LowExit LowVal MayCreate MaySet Old Opaque PByteSeq PMultiset PSeq PSet Predicate PreservesLow Previous
Pure RD_PRED RaisedException Rd Refute Requires Result ResultT Reveal TerminatesSif ToByteSeq ToMS ToSeq Unfold
Unfolding Wildcard bytearray_pred dict_pred isNaN list_pred set_pred`)

// naginiNative are the builtins Nagini models itself, as part of the
// verifier, with the operations of the containers and strings they make: the
// types it knows, and the functions its own theory defines. Every other
// operation beyond the selected function needs a trusted contract, I/O such
// as print included, which Nagini would otherwise accept with no contract at
// all. naginiExceptions are the only builtin exception classes the pinned
// Nagini models (its builtins.json).
var (
	naginiNative = map[string]bool{"int": true, "float": true, "bool": true, "str": true, "object": true,
		"list": true, "dict": true, "set": true, "tuple": true, "len": true, "isinstance": true, "abs": true,
		"min": true, "max": true, "range": true}
	naginiExceptions = map[string]bool{"Exception": true, "ConnectionRefusedError": true}
)

// NaginiVersion is the Nagini the recipe pins.
const NaginiVersion = "1.3.1"

// PythonApplicationProofMain is the verifier input's own module, which holds
// the selected function.
const PythonApplicationProofMain = "invariant_target.py"

// PythonApplicationProofFile is one file of a verifier input, by its name in
// Nagini's working directory, with its exact contents.
type PythonApplicationProofFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Content string `json:"content"`
}

// PythonApplicationProofSpan maps lines of a verifier input to the original
// bytes they copy. A copied span's SHA256 equals its SourceSHA256. A
// signature names the original abstraction's header with the function
// renamed for the operation it stands for, so its hashes differ. Spans the
// gate writes itself have no source: an import binding a name to a trusted
// contract's abstraction, the ContractOnly marker that makes Nagini assume
// the abstraction rather than verify it, and a boundary module's import of
// Nagini's contracts.
type PythonApplicationProofSpan struct {
	Kind          string `json:"kind"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	EndLine       int    `json:"end_line"`
	SHA256        string `json:"sha256"`
	Source        string `json:"source,omitempty"`
	SourceLine    int    `json:"source_line,omitempty"`
	SourceEndLine int    `json:"source_end_line,omitempty"`
	SourceSHA256  string `json:"source_sha256,omitempty"`
	Contract      string `json:"contract,omitempty"`
	start, end    int
	from, to      int
	// A signature's original name runs from name to nameEnd, and the input
	// holds rename in its place.
	name, nameEnd int
	rename        string
}

// PythonApplicationProofBoundary says how the verifier input binds one name
// the selected function reads: Nagini's own contract call or a typing name,
// copied with its import; a builtin Nagini models natively; a literal
// constant, copied; or a trusted contract's abstraction for the operation
// the name stands for.
type PythonApplicationProofBoundary struct {
	Name      string                              `json:"name"`
	Kind      string                              `json:"kind"`
	Contract  string                              `json:"contract,omitempty"`
	Operation *project.ResolvedComponentOperation `json:"operation,omitempty"`
}

// PythonApplicationProofInput is Nagini's input for one selected function,
// with the original files it was built from, the declarations of its module
// it leaves out, how it binds every name the function reads, the trusted
// contracts it assumes and anything it reaches that no contract accounts for.
type PythonApplicationProofInput struct {
	Target      project.Component                `json:"target"`
	Main        string                           `json:"main"`
	Files       []PythonApplicationProofFile     `json:"files"`
	Spans       []PythonApplicationProofSpan     `json:"spans"`
	Boundaries  []PythonApplicationProofBoundary `json:"boundaries"`
	Excluded    []string                         `json:"excluded,omitempty"`
	Sources     []project.ComponentFile          `json:"sources"`
	Assumed     []ComponentContractUse           `json:"assumed,omitempty"`
	Unaccounted []ComponentUnaccounted           `json:"unaccounted,omitempty"`
	Fingerprint string                           `json:"fingerprint"`
}

// PythonApplicationProof is one attempt to prove a selected function in its
// module: the input Nagini got, the recipe and the local image it ran in and
// how it ran, its output with the session's paths removed, and the result in
// the form proof attribution takes, whose pins name the recipe, never a
// local image. An attempt that reaches an operation no trusted contract
// binds doesn't run, since no complete input exists; one that ran reports
// exactly what Nagini said. It grants no gate success.
type PythonApplicationProof struct {
	Result  ComponentProofResult        `json:"result"`
	Input   PythonApplicationProofInput `json:"input"`
	Recipe  string                      `json:"recipe"`
	Image   string                      `json:"image,omitempty"`
	Command []string                    `json:"command,omitempty"`
	Ran     bool                        `json:"ran"`
	Exit    int                         `json:"exit"`
	Output  string                      `json:"output,omitempty"`
}

// PythonApplicationNaginiCommand is one Nagini run: the pinned image, the
// directory holding the verifier input, mounted as Nagini's working
// directory, and the input's own module.
type PythonApplicationNaginiCommand struct {
	Image, Dir, Main string
}

// RunPythonApplicationNagini runs the pinned Nagini on one verifier input,
// offline, in its own linux/amd64 image.
func RunPythonApplicationNagini(ctx context.Context, c PythonApplicationNaginiCommand) (PythonApplicationProcess, error) {
	args := pythonApplicationNaginiArgs(c, c.Dir)
	cmd := toolchain.Docker(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	p := PythonApplicationProcess{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1}
	if cmd.ProcessState != nil {
		p.ExitCode = cmd.ProcessState.ExitCode()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	return p, err
}

func pythonApplicationNaginiArgs(c PythonApplicationNaginiCommand, dir string) []string {
	return []string{"run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "-v", dir + ":/work", "-w", "/work", c.Image, c.Main}
}

// pythonApplicationDistributionsScript names the distribution each top-level
// module of a libraries directory belongs to, from each installed
// distribution's RECORD, which lists every file it installed.
const pythonApplicationDistributionsScript = `import json, os, re, sys
lib = sys.argv[1]
out = {}
for entry in sorted(os.listdir(lib)) if os.path.isdir(lib) else []:
    if not entry.endswith(".dist-info"):
        continue
    name = None
    try:
        with open(os.path.join(lib, entry, "METADATA"), encoding="utf-8") as f:
            for line in f:
                if not line.strip():
                    break
                if line.lower().startswith("name:"):
                    name = line.split(":", 1)[1].strip()
                    break
        with open(os.path.join(lib, entry, "RECORD"), encoding="utf-8") as f:
            paths = [line.split(",", 1)[0] for line in f]
    except OSError:
        continue
    if not name:
        continue
    name = re.sub(r"[-_.]+", "-", name).lower()
    for p in paths:
        first = p.split("/", 1)[0]
        if not first or first.startswith("..") or first.endswith((".dist-info", ".data")):
            continue
        if "/" in p:
            top = first
        elif first.endswith(".py"):
            top = first[:-3]
        elif first.endswith((".so", ".pyd")):
            top = first.split(".", 1)[0]
        else:
            continue
        out.setdefault(top, set()).add(name)
print(json.dumps({k: sorted(v) for k, v in sorted(out.items())}))
`

// pythonApplicationDistributions maps each top-level module the session's
// dependency image installed to its distributions, read in that image,
// offline.
func pythonApplicationDistributions(ctx context.Context, image string) (map[string][]string, error) {
	b, err := toolchain.Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "/usr/local/bin/python", image,
		"-I", "-S", "-c", pythonApplicationDistributionsScript, toolchain.PythonLibrary).Output()
	if err != nil {
		return nil, fmt.Errorf("reading the dependency image's distributions: %w", err)
	}
	var out map[string][]string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("the dependency image's distributions: %w", err)
	}
	return out, nil
}

type pythonProofLines struct {
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
}

type pythonProofBinding struct {
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Line    int    `json:"line"`
	Module  string `json:"module"`
	Level   int    `json:"level"`
	Name    string `json:"name"`
	Asname  string `json:"asname"`
	Literal bool   `json:"literal"`
	// Shared says the statement shares a line with another, so the gate,
	// which copies whole lines, can't copy it alone. Annotation names what an
	// annotated constant's annotation reads, "." for an attribute.
	Shared     bool     `json:"shared"`
	Annotation []string `json:"annotation"`
}

type pythonProofName struct {
	Attributes []string `json:"attributes"`
	Bare       bool     `json:"bare"`
	Deep       bool     `json:"deep"`
}

// pythonProofTry is one try statement of the function: what its handlers
// catch, "*" for a bare except and "?" for what the reader can't name, and
// what its body reads.
type pythonProofTry struct {
	Line     int                        `json:"line"`
	Handlers []string                   `json:"handlers"`
	Reads    map[string]pythonProofName `json:"reads"`
}

type pythonProofDynamic struct {
	Line int    `json:"line"`
	What string `json:"what"`
}

// pythonProofDeclaration is the analyzer's account of one top-level function.
type pythonProofDeclaration struct {
	Start        int                             `json:"start"`
	End          int                             `json:"end"`
	Line         int                             `json:"line"`
	EndLine      int                             `json:"end_line"`
	DefLine      int                             `json:"def_line"`
	Async        bool                            `json:"async"`
	Decorators   []string                        `json:"decorators"`
	HeaderEnd    int                             `json:"header_end"`
	NameStart    int                             `json:"name_start"`
	NameEnd      int                             `json:"name_end"`
	Body         []pythonProofLines              `json:"body"`
	Free         map[string]pythonProofName      `json:"free"`
	Bound        []string                        `json:"bound"`
	Tries        []pythonProofTry                `json:"tries"`
	Unsupported  []string                        `json:"unsupported"`
	Bindings     map[string][]pythonProofBinding `json:"bindings"`
	Star         []pythonProofBinding            `json:"star"`
	Declarations []string                        `json:"declarations"`
	Globals      []string                        `json:"globals"`
	Future       []pythonProofBinding            `json:"future"`
	Dynamic      []pythonProofDynamic            `json:"dynamic"`
	source       []byte
}

func runPythonProofAnalyzer(request any, out any) error {
	b, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", pythonApplicationProofAnalyzer)
	cmd.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	reply, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("isolated Python 3 syntax reader: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(reply, out); err != nil {
		return fmt.Errorf("Python syntax reader output: %w", err)
	}
	return nil
}

// analyzePythonProof reads one top-level function with the host's Python in
// isolated mode, which parses the source and never imports or runs it.
func analyzePythonProof(source []byte, symbol string) (*pythonProofDeclaration, error) {
	var d pythonProofDeclaration
	if err := runPythonProofAnalyzer(map[string]string{"source": base64.StdEncoding.EncodeToString(source), "symbol": symbol}, &d); err != nil {
		return nil, err
	}
	inside := func(start, end int) bool { return 0 <= start && start <= end && end <= len(source) }
	ok := inside(d.Start, d.End) && inside(d.Start, d.HeaderEnd) && d.HeaderEnd <= d.End && inside(d.NameStart, d.NameEnd) &&
		string(source[d.NameStart:d.NameEnd]) == symbol
	for _, b := range d.Body {
		ok = ok && inside(b.Start, b.End) && d.Start <= b.Start && b.End <= d.End
	}
	for _, rows := range d.Bindings {
		for _, b := range rows {
			ok = ok && inside(b.Start, b.End)
		}
	}
	for _, b := range append(slices.Clone(d.Star), d.Future...) {
		ok = ok && inside(b.Start, b.End)
	}
	if !ok {
		return nil, fmt.Errorf("Python syntax reader: invalid spans for %s", symbol)
	}
	d.source = source
	return &d, nil
}

type pythonProofWrite struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	What string `json:"what"`
}

// pythonProofOperation is an operation the selected function reaches beyond
// its boundary, and the name and form it reaches it by.
type pythonProofOperation struct {
	op      project.ResolvedComponentOperation
	name    string // the name the function reads
	attr    string // for a module the function reads attributes of
	binding pythonProofBinding
	dotted  string
}

// ProofInput builds Nagini's input for the proof check on target, the
// canonical ID of a selected function. It runs no tool beyond the host's
// isolated Python parser, but for one look at the dependency image's
// installed distributions when the function reaches a dependency no
// trusted contract binds.
func (s *PythonApplicationSession) ProofInput(ctx context.Context, target string) (*PythonApplicationProofInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("Python application session is closed")
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	in, _, err := s.proofInput(ctx, target)
	return in, err
}

// Prove builds Nagini's input for the proof check on target, checks every
// copied span against the original bytes again, and runs the pinned Nagini
// on it, unless the function reaches an operation no trusted contract binds.
// An unsupported selection, or context the input can't preserve, is an
// error; a failed proof is a result.
func (s *PythonApplicationSession) Prove(ctx context.Context, target string) (*PythonApplicationProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("Python application session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	in, check, err := s.proofInput(ctx, target)
	if err != nil {
		return nil, err
	}
	proof := &PythonApplicationProof{Input: *in, Recipe: toolchain.NaginiRecipe(), Exit: -1}
	result := ComponentProofResult{Target: target, Verifier: "Nagini", Contracts: slices.Clone(check.Proof.Contracts),
		Inputs: s.proofInputs(in, check), FileInputs: slices.Clone(in.Sources), Assumed: copyComponentContractUses(in.Assumed),
		Unaccounted: copyComponentUnaccounted(in.Unaccounted),
		Tool:        &ComponentProofTool{Name: "Nagini", Version: NaginiVersion, Pins: map[string]string{"recipe": proof.Recipe, "platform": "linux/amd64", "input": in.Fingerprint}}}
	// An input that binds nothing to a name the function reaches is
	// incomplete, so Nagini doesn't run on it.
	var incomplete []string
	through := map[string][]string{}
	for _, b := range in.Boundaries {
		if b.Kind == "missing" && b.Operation != nil {
			key := b.Operation.Key()
			through[key] = append(through[key], b.Name)
		}
	}
	for _, u := range in.Unaccounted {
		switch {
		case u.Kind == MissingContract && u.Operation != nil:
			incomplete = append(incomplete, "no trusted contract binds "+unaccountedText(u)+", which the function reaches through "+strings.Join(through[u.Operation.Key()], " and "))
		case u.Kind == UnaccountedAssumption && strings.Contains(u.Detail, "no trusted contract binds"):
			incomplete = append(incomplete, u.Detail)
		}
	}
	if len(incomplete) > 0 {
		result.Errors = incomplete
		proof.Result = result
		return proof, nil
	}
	work, err := os.MkdirTemp("", "invariant-nagini-input-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := writePythonProofInput(work, in); err != nil {
		return nil, err
	}
	// The image Nagini runs in is the recipe's, by the local ID of what the
	// recipe built, which the attempt records and the result's pins don't:
	// the recipe names it the same on every machine. A session's double runs
	// no image, and records the tag the recipe would have.
	image := "invariant-nagini:" + strings.TrimPrefix(proof.Recipe, "sha256:")[:12]
	run := s.nagini
	if run == nil {
		run = RunPythonApplicationNagini
		tag, err := toolchain.NaginiImage(ctx)
		if err != nil {
			return nil, err
		}
		b, err := toolchain.Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
		if image = strings.TrimSpace(string(b)); err != nil || !pythonApplicationDigest.MatchString(image) {
			return nil, fmt.Errorf("the Nagini image %s has no digest: %v", tag, err)
		}
	}
	command := PythonApplicationNaginiCommand{Image: image, Dir: work, Main: in.Main}
	proof.Image, proof.Command = image, pythonApplicationNaginiArgs(command, "/input")
	process, err := run(ctx, command)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("running the pinned Nagini: %w", err)
	}
	// What Nagini read is what was recorded: nothing changed the input.
	if err := checkPythonProofFiles(work, in); err != nil {
		return nil, err
	}
	proof.Ran, proof.Exit = true, process.ExitCode
	output := strings.ReplaceAll(process.Stdout+process.Stderr, work, "/work")
	proof.Output = output
	result.Passed = process.ExitCode == 0 && strings.Contains(output, "Verification successful")
	if result.Passed {
		result.Verified = []string{target}
	} else {
		result.Errors = mapNaginiErrors(in, naginiErrors(in.Main, output))
	}
	proof.Result = result
	return proof, nil
}

// proofInputs are the declarations the attempt read, as the inventory holds
// them: the target, its selected contracts and each assumed abstraction.
func (s *PythonApplicationSession) proofInputs(in *PythonApplicationProofInput, check project.ResolvedComponentCheck) []project.Component {
	ids := append([]string{in.Target.ID}, check.Proof.Contracts...)
	for _, use := range in.Assumed {
		ids = append(ids, use.Declaration.ID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make([]project.Component, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.declarations[id])
	}
	return out
}

// pythonApplicationProofCheck is the inventory's proof check on target, which
// a session admits only in the form in-place proof supports: the selected
// function's own inline contract, on a function at its module's top level,
// with no declared invocation, since the gate runs its own pinned Nagini.
func pythonApplicationProofCheck(check project.ResolvedComponentCheck, target project.Component) error {
	if check.Proof == nil {
		return fmt.Errorf("%s selects no proof", check.Target)
	}
	if len(check.Proof.Contracts) != 1 || check.Proof.Contracts[0] != check.Target {
		return fmt.Errorf("in-place proof of %s selects the function's own inline contract, and no other declaration's", check.Target)
	}
	if !pythonApplicationName.MatchString(target.Symbol) || strings.Contains(target.Symbol, ".") {
		return fmt.Errorf("in-place proof selects a function at its module's top level, not %s", target.Symbol)
	}
	for _, v := range check.Invocations {
		if v.Kind == "proof" {
			return fmt.Errorf("in-place proof of %s runs the gate's pinned Nagini, so it declares no proof invocation", check.Target)
		}
	}
	return nil
}

func (s *PythonApplicationSession) proofInput(ctx context.Context, target string) (*PythonApplicationProofInput, project.ResolvedComponentCheck, error) {
	var check project.ResolvedComponentCheck
	found := false
	if s.inventory != nil {
		for _, c := range s.inventory.Checks {
			if c.Target == target && c.Proof != nil {
				check, found = c, true
			}
		}
	}
	decl, ok := s.declarations[target]
	if !found || !ok || s.legacy {
		return nil, check, fmt.Errorf("%s has no selected in-place proof", target)
	}
	if err := pythonApplicationProofCheck(check, decl); err != nil {
		return nil, check, err
	}
	source, err := s.proofSource(decl)
	if err != nil {
		return nil, check, err
	}
	d, err := analyzePythonProof(source, decl.Symbol)
	if err != nil {
		return nil, check, fmt.Errorf("in-place proof of %s: %w", target, err)
	}
	b := &pythonProofBuilder{ctx: ctx, s: s, target: decl, d: d, in: &PythonApplicationProofInput{Target: decl, Main: PythonApplicationProofMain},
		reached: map[string]map[string]string{}, raises: map[string][]string{}}
	if err := b.build(); err != nil {
		return nil, check, fmt.Errorf("in-place proof of %s: %w", target, err)
	}
	return b.in, check, nil
}

// proofSource reads a declaration's source as the session staged it.
func (s *PythonApplicationSession) proofSource(c project.Component) ([]byte, error) {
	return s.stagedSource(c.Source, c.SourceSHA256, c.ID)
}

func (s *PythonApplicationSession) stagedSource(source, hash, what string) ([]byte, error) {
	f, ok := s.snapshot[source]
	if !ok || (hash != "" && f.hash != hash) {
		return nil, fmt.Errorf("stale declaration %s", what)
	}
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(source)))
	if err != nil {
		return nil, err
	}
	if pythonApplicationHash(b) != f.hash {
		return nil, fmt.Errorf("stale declaration %s", what)
	}
	return b, nil
}

// moduleDistributions maps the dependency image's top-level modules to the
// distributions that installed them, read once per session.
func (s *PythonApplicationSession) moduleDistributions(ctx context.Context) (map[string][]string, error) {
	if s.modules != nil {
		return s.modules, nil
	}
	modules := map[string][]string{}
	if s.provenance.Dependencies != nil {
		read := s.distributions
		if read == nil {
			read = pythonApplicationDistributions
		}
		var err error
		if modules, err = read(ctx, s.image); err != nil {
			return nil, err
		}
	}
	s.modules = modules
	return modules, nil
}

// moduleWrites finds every place a staged source writes one of module's
// attributes, which no binding in the module itself shows.
func (s *PythonApplicationSession) moduleWrites(module string) ([]pythonProofWrite, error) {
	type file struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Tests  bool   `json:"tests,omitempty"`
	}
	verification, err := filepath.Rel(s.root, s.project.Dir)
	if err != nil {
		return nil, err
	}
	var files []file
	names := make([]string, 0, len(s.snapshot))
	for name := range s.snapshot {
		if strings.HasSuffix(name, ".py") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := s.stagedSource(name, "", name)
		if err != nil {
			return nil, err
		}
		files = append(files, file{Path: name, Source: base64.StdEncoding.EncodeToString(b), Tests: !s.production(name, filepath.ToSlash(verification))})
	}
	var reply struct {
		Writes []pythonProofWrite `json:"writes"`
	}
	if err := runPythonProofAnalyzer(map[string]any{"writes": true, "module": module, "files": files}, &reply); err != nil {
		return nil, err
	}
	return reply.Writes, nil
}

// production says whether a staged source is the application's production
// code, which is what a proof covers. Test files, by the project's rule, are
// not, and neither is the verification project's own code, its driver and
// helpers among it, unless the application selects it. The scan for writes
// still reads what production code imports of either.
func (s *PythonApplicationSession) production(name, verification string) bool {
	base := path.Base(name)
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return false
	}
	if !strings.HasPrefix(name, verification+"/") {
		return true
	}
	m := s.project.Manifest
	selected := slices.ContainsFunc(m.Existing, func(e string) bool { return name == e || strings.HasPrefix(name, e+"/") })
	if m.Components != nil {
		selected = selected || slices.Contains(m.Components.Scope, path.Dir(name))
	}
	return selected
}

type pythonProofBuilder struct {
	ctx    context.Context
	s      *PythonApplicationSession
	target project.Component
	d      *pythonProofDeclaration
	in     *PythonApplicationProofInput
	// The main module's lines: copied imports and constants, the gate's
	// own bindings, then the target.
	copied   []pythonProofBinding
	bindings []string
	modules  []*pythonProofModule
	sources  map[string]bool
	// reached names the trusted contract each name, and each module's
	// attribute, stands for, and raises what each contract declares its
	// operation raises.
	reached map[string]map[string]string
	raises  map[string][]string
}

// pythonProofModule is one boundary module: the abstractions the target
// reaches through one name.
type pythonProofModule struct {
	file  string
	stubs []pythonProofStub
}

type pythonProofStub struct {
	name     string
	contract project.ResolvedComponentContract
	decl     project.Component
	d        *pythonProofDeclaration
	typing   []pythonProofBinding
}

func (b *pythonProofBuilder) unsupported(format string, args ...any) error {
	return fmt.Errorf("unsupported: "+format, args...)
}

func (b *pythonProofBuilder) build() error {
	d := b.d
	if d.Async {
		return b.unsupported("%s is an async function", b.target.Symbol)
	}
	if len(d.Unsupported) > 0 {
		return b.unsupported("%s holds %s", b.target.Symbol, strings.Join(d.Unsupported, ", "))
	}
	for _, decorator := range d.Decorators {
		if decorator != "Pure" {
			return b.unsupported("%s's decorator %s", b.target.Symbol, decorator)
		}
	}
	// What callers and the function's own recursion reach is the module's
	// binding of its name, so that must be the def and nothing else.
	if rows := d.Bindings[b.target.Symbol]; len(rows) != 1 || rows[0].Kind != "function" || slices.Contains(d.Globals, b.target.Symbol) {
		return b.unsupported("the module binds %s other than by its one def, so callers may not reach the function the input proves", b.target.Symbol)
	}
	for _, f := range d.Future {
		if f.Shared {
			return b.unsupported("line %d holds more than one statement, and the input copies whole lines", f.Line)
		}
	}
	if len(d.Dynamic) > 0 {
		return b.unsupported("line %d writes the module's names with %s, which no binding shows", d.Dynamic[0].Line, d.Dynamic[0].What)
	}
	for _, name := range d.Bound {
		if slices.Contains(naginiCalls, name) {
			return b.unsupported("%s binds %s, which Nagini reads as its own", b.target.Symbol, name)
		}
	}
	// A star import could rebind what the input copies, whatever the
	// function reads.
	for _, s := range d.Star {
		if s.Module != "nagini_contracts.contracts" || s.Level != 0 {
			return b.unsupported("line %d's star import of %s%s could rebind what the input copies from the module", s.Line, strings.Repeat(".", s.Level), s.Module)
		}
	}
	module := strings.TrimSuffix(b.target.Package, ".__init__")
	if err := b.submodules(module); err != nil {
		return err
	}
	writes, err := b.s.moduleWrites(module)
	if err != nil {
		return err
	}
	if len(writes) > 0 {
		w := writes[0]
		if w.Line == 0 {
			return b.unsupported("%s %s", w.Path, w.What)
		}
		return b.unsupported("%s:%d %s", w.Path, w.Line, w.What)
	}
	b.sources = map[string]bool{b.target.Source: true}
	if b.s.inventory != nil {
		for _, c := range b.s.inventory.Components {
			if c.Source == b.target.Source && c.ID != b.target.ID {
				b.in.Excluded = append(b.in.Excluded, c.ID)
			}
		}
	}
	sort.Strings(b.in.Excluded)
	names := make([]string, 0, len(d.Free))
	for name := range d.Free {
		names = append(names, name)
	}
	sort.Strings(names)
	var operations []*pythonProofOperation
	for _, name := range names {
		ops, err := b.bind(name, d.Free[name])
		if err != nil {
			return err
		}
		operations = append(operations, ops...)
	}
	if err := b.boundaries(operations); err != nil {
		return err
	}
	if err := b.handlers(); err != nil {
		return err
	}
	return b.write()
}

// submodules refuses a package's own module when one of its submodules has
// a name the function reads, or the function's own: importing the submodule
// rebinds that name in the package.
func (b *pythonProofBuilder) submodules(module string) error {
	dir, ok := strings.CutSuffix(b.target.Source, "__init__.py")
	if !ok {
		return nil
	}
	names := make([]string, 0, len(b.s.snapshot))
	for name := range b.s.snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rest, ok := strings.CutPrefix(name, dir)
		if !ok || rest == "__init__.py" {
			continue
		}
		child := strings.TrimSuffix(strings.SplitN(rest, "/", 2)[0], ".py")
		if _, read := b.d.Free[child]; read || child == b.target.Symbol {
			return b.unsupported("%s is in a submodule of %s, and importing it rebinds %s.%s", name, module, module, child)
		}
	}
	return nil
}

// naginiStar says the module star-imports Nagini's contracts.
func (d *pythonProofDeclaration) naginiStar() bool {
	return slices.ContainsFunc(d.Star, func(s pythonProofBinding) bool { return s.Module == "nagini_contracts.contracts" && s.Level == 0 })
}

// bind classifies one name the target reads, and returns the operations it
// stands for beyond the target's boundary.
func (b *pythonProofBuilder) bind(name string, use pythonProofName) ([]*pythonProofOperation, error) {
	d := b.d
	if name == b.target.Symbol {
		// A recursive call uses the function's own contract, which the
		// input holds; build checked the module binds the name to it alone.
		return nil, nil
	}
	rows := d.Bindings[name]
	if len(rows) > 1 {
		return nil, b.unsupported("%s is bound more than once at its module's top level", name)
	}
	// Nagini reads some names as its own wherever they appear, so the
	// function may read one only as what Nagini takes it for.
	if slices.Contains(naginiNames, name) {
		switch {
		case len(rows) == 0 && d.naginiStar() && slices.Contains(naginiExports, name):
		case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Level == 0 && rows[0].Name == name && rows[0].Asname == "" &&
			(rows[0].Module == "nagini_contracts.contracts" || rows[0].Module == "typing"):
		case len(rows) == 0 && naginiNative[name]:
		case len(rows) == 0 && slices.Contains(strings.Fields(pythonBuiltins), name):
			return nil, b.unsupported("the function uses the builtin %s, which in-place proof doesn't take yet", name)
		default:
			return nil, b.unsupported("the function reads %s, which Nagini reads as its own whatever the module binds it to", name)
		}
	}
	if len(rows) == 0 {
		if d.naginiStar() && slices.Contains(naginiExports, name) {
			return nil, b.nagini(name, pythonProofBinding{Kind: "star"})
		}
		if naginiNative[name] || naginiExceptions[name] {
			// Nagini models the builtin itself, not what it holds, as
			// str.maketrans: that needs a contract, as other builtins do.
			if use.Deep || len(use.Attributes) > 0 {
				return nil, b.unsupported("the function reads attributes of the builtin %s", name)
			}
			b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "native"})
			return nil, nil
		}
		if slices.Contains(strings.Fields(pythonBuiltinExceptions), name) {
			return nil, b.unsupported("the function reads %s, an exception class the pinned Nagini doesn't model: it models Exception and ConnectionRefusedError", name)
		}
		if !slices.Contains(strings.Fields(pythonBuiltins), name) {
			return nil, b.unsupported("%s isn't bound in its module", name)
		}
		if use.Deep || len(use.Attributes) > 0 {
			return nil, b.unsupported("the function reads attributes of the builtin %s", name)
		}
		op := project.ResolvedComponentOperation{Kind: project.OperationStandardLibrary, Module: "builtins", Symbol: name}
		return []*pythonProofOperation{{op: op, name: name, dotted: "builtins." + name}}, nil
	}
	row := rows[0]
	switch row.Kind {
	case "from":
		switch {
		case row.Module == "nagini_contracts.contracts" && row.Level == 0:
			if row.Name != name {
				return nil, b.unsupported("the module imports Nagini's %s as %s, and Nagini reads its names only as themselves", row.Name, name)
			}
			return nil, b.nagini(name, row)
		case row.Module == "typing" && row.Level == 0:
			b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "typing"})
			return nil, b.copy(row)
		}
		if use.Deep || len(use.Attributes) > 0 {
			return nil, b.unsupported("the function reads attributes of %s, and only calls it reaches by name are supported", name)
		}
		op, err := b.operation(row.Module, row.Level, row.Name)
		if err != nil {
			return nil, err
		}
		return []*pythonProofOperation{{op: op, name: name, binding: row, dotted: row.Module + "." + row.Name}}, nil
	case "import":
		if row.Module == "typing" && row.Asname == "" {
			b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "typing"})
			return nil, b.copy(row)
		}
		if strings.HasPrefix(row.Module, "nagini_contracts") {
			return nil, b.unsupported("%s imports Nagini's contracts as a module; contract calls are unqualified", name)
		}
		if use.Bare || use.Deep || len(use.Attributes) == 0 {
			return nil, b.unsupported("the function uses the module %s other than to call its functions", name)
		}
		if row.Asname == "" && strings.Contains(row.Module, ".") {
			return nil, b.unsupported("the function reads %s through the dotted import %s", name, row.Module)
		}
		var ops []*pythonProofOperation
		for _, attr := range use.Attributes {
			op, err := b.operation(row.Module, 0, attr)
			if err != nil {
				return nil, err
			}
			ops = append(ops, &pythonProofOperation{op: op, name: name, attr: attr, binding: row, dotted: row.Module + "." + attr})
		}
		return ops, nil
	case "assign":
		if !row.Literal {
			return nil, b.unsupported("%s's value isn't an immutable literal", name)
		}
		if slices.Contains(d.Globals, name) {
			return nil, b.unsupported("a function in the module rebinds %s with a global statement", name)
		}
		if use.Deep || len(use.Attributes) > 0 {
			return nil, b.unsupported("the function reads attributes of the constant %s", name)
		}
		// An annotation is copied with its constant, so what it reads must
		// be in the input too.
		for _, a := range row.Annotation {
			switch rows := d.Bindings[a]; {
			case a == ".":
				return nil, b.unsupported("%s's annotation reads an attribute", name)
			case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Module == "typing" && rows[0].Level == 0:
				if err := b.copy(rows[0]); err != nil {
					return nil, err
				}
			case len(rows) == 0 && naginiNative[a]:
			default:
				return nil, b.unsupported("%s's annotation reads %s, which the input can't bind", name, a)
			}
		}
		b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "constant"})
		return nil, b.copy(row)
	case "function":
		if use.Deep || len(use.Attributes) > 0 {
			return nil, b.unsupported("the function reads attributes of %s", name)
		}
		op := project.ResolvedComponentOperation{Kind: project.OperationApplication, Declaration: "python:" + b.target.Package + "#" + name + "@" + b.target.Source}
		return []*pythonProofOperation{{op: op, name: name, dotted: b.target.Package + "." + name}}, nil
	case "class":
		return nil, b.unsupported("%s is a class, and the function's boundary only takes functions", name)
	}
	return nil, b.unsupported("%s is bound conditionally or by more than a name", name)
}

// nagini binds one of Nagini's contract names through the module's own
// import of them. Assume makes Nagini take a fact nothing proves, so it is
// an assumption no declaration accounts for.
func (b *pythonProofBuilder) nagini(name string, row pythonProofBinding) error {
	if name == "ContractOnly" {
		return b.unsupported("%s is ContractOnly, which Nagini doesn't verify", b.target.Symbol)
	}
	if row.Kind == "star" {
		for _, s := range b.d.Star {
			if s.Module == "nagini_contracts.contracts" && s.Level == 0 {
				row = s
			}
		}
	}
	if err := b.copy(row); err != nil {
		return err
	}
	b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "nagini"})
	if name == "Assume" {
		b.unaccounted(ComponentUnaccounted{Kind: UnaccountedAssumption, Detail: "the function calls Assume, so Nagini takes a fact that nothing proves"})
	}
	return nil
}

// copy keeps a module statement for the main module, once, in its original
// order. The gate copies whole lines, so a statement sharing its line with
// another can't be copied without it.
func (b *pythonProofBuilder) copy(row pythonProofBinding) error {
	if row.Shared {
		return b.unsupported("line %d holds more than one statement, and the input copies whole lines", row.Line)
	}
	for _, c := range b.copied {
		if c.Start == row.Start {
			return nil
		}
	}
	b.copied = append(b.copied, row)
	return nil
}

func (b *pythonProofBuilder) boundary(x PythonApplicationProofBoundary) {
	if !slices.ContainsFunc(b.in.Boundaries, func(y PythonApplicationProofBoundary) bool {
		return y.Name == x.Name && y.Kind == x.Kind && y.Contract == x.Contract && (y.Operation == nil) == (x.Operation == nil) && (y.Operation == nil || *y.Operation == *x.Operation)
	}) {
		b.in.Boundaries = append(b.in.Boundaries, x)
	}
}

// operation resolves what a module and name stand for: an application
// declaration, when the module is one of the application's own sources, or
// else a standard-library or dependency operation by its dotted name.
func (b *pythonProofBuilder) operation(module string, level int, name string) (project.ResolvedComponentOperation, error) {
	if level > 0 || b.applicationModule(module) != "" {
		source, err := b.applicationSource(module, level)
		if err != nil {
			return project.ResolvedComponentOperation{}, err
		}
		pkg := strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".")
		declarations, err := project.PythonDeclarations(b.s.root, source)
		if err != nil {
			return project.ResolvedComponentOperation{}, err
		}
		if !slices.ContainsFunc(declarations, func(c project.Component) bool { return c.Symbol == name }) {
			return project.ResolvedComponentOperation{}, b.unsupported("%s doesn't declare %s, which the function imports from it", source, name)
		}
		return project.ResolvedComponentOperation{Kind: project.OperationApplication, Declaration: "python:" + pkg + "#" + name + "@" + source}, nil
	}
	top := strings.Split(module, ".")[0]
	if slices.Contains(strings.Fields(pythonStdlibModules), top) {
		return project.ResolvedComponentOperation{Kind: project.OperationStandardLibrary, Module: module, Symbol: name}, nil
	}
	return project.ResolvedComponentOperation{Kind: project.OperationDependency, Module: module, Symbol: name}, nil
}

// applicationModule is the application source an absolute module names, or
// empty when it names none.
func (b *pythonProofBuilder) applicationModule(module string) string {
	if module == "" {
		return ""
	}
	base := strings.ReplaceAll(module, ".", "/")
	for _, candidate := range []string{base + ".py", base + "/__init__.py"} {
		if _, ok := b.s.snapshot[candidate]; ok {
			return candidate
		}
	}
	return ""
}

// applicationSource is the source a relative or absolute import of module
// names. A relative import can't reach above the top-level package, as at
// runtime.
func (b *pythonProofBuilder) applicationSource(module string, level int) (string, error) {
	if level == 0 {
		return b.applicationModule(module), nil
	}
	if module == "" {
		return "", b.unsupported("the function imports a module with a relative import of a package")
	}
	parts := strings.Split(path.Dir(b.target.Source), "/")
	if path.Dir(b.target.Source) == "." {
		parts = nil
	}
	if level-1 >= len(parts) {
		return "", b.unsupported("the relative import of %s reaches above the top-level package", module)
	}
	name := path.Join(path.Join(parts[:len(parts)-(level-1)]...), strings.ReplaceAll(module, ".", "/"))
	for _, candidate := range []string{name + ".py", name + "/__init__.py"} {
		if _, ok := b.s.snapshot[candidate]; ok {
			return candidate, nil
		}
	}
	return "", b.unsupported("the relative import of %s resolves to no application source", module)
}

// boundaries binds every operation the target reaches to its trusted
// contract's abstraction, or records it as missing.
func (b *pythonProofBuilder) boundaries(operations []*pythonProofOperation) error {
	byKey := map[string]project.ResolvedComponentContract{}
	if b.s.inventory != nil {
		for _, c := range b.s.inventory.TrustedContracts {
			byKey[c.Operation.Key()] = c
		}
	}
	modules := map[string]*pythonProofModule{}
	var order []string
	assumed := map[string]bool{}
	for _, o := range operations {
		c, ok := byKey[o.op.Key()]
		if !ok {
			if err := b.missing(o); err != nil {
				return err
			}
			continue
		}
		decl, found := b.s.declarations[c.Contract]
		if !found {
			return fmt.Errorf("trusted contract %s has no declaration in the session", c.ID)
		}
		src, err := b.s.proofSource(decl)
		if err != nil {
			return err
		}
		ad, err := analyzePythonProof(src, decl.Symbol)
		if err != nil {
			return fmt.Errorf("trusted contract %s's abstraction: %w", c.ID, err)
		}
		typing, raises, err := b.abstraction(c, ad)
		if err != nil {
			return err
		}
		if c.Operation.Kind == project.OperationApplication {
			if err := b.applicationOperation(c); err != nil {
				return err
			}
		}
		b.sources[decl.Source] = true
		op := c.Operation
		b.boundary(PythonApplicationProofBoundary{Name: o.name, Kind: "contract", Contract: c.ID, Operation: &op})
		if b.reached[o.name] == nil {
			b.reached[o.name] = map[string]string{}
		}
		b.reached[o.name][o.attr] = c.ID
		b.raises[c.ID] = raises
		if !assumed[c.ID] {
			assumed[c.ID] = true
			b.in.Assumed = append(b.in.Assumed, ComponentContractUse{Contract: c.ID, Declaration: decl})
		}
		group := o.name
		m := modules[group]
		if m == nil {
			m = &pythonProofModule{file: fmt.Sprintf("invariant_boundary_%d.py", len(order))}
			modules[group] = m
			order = append(order, group)
		}
		stub := o.op.Symbol
		switch {
		case o.op.Kind == project.OperationApplication:
			stub = o.binding.Name
			if stub == "" {
				stub = o.name
			}
		case o.attr != "":
			stub = o.attr
		}
		if slices.Contains(naginiNames, stub) {
			return b.unsupported("trusted contract %s's operation is named %s, which Nagini reads as its own", c.ID, stub)
		}
		if i := slices.IndexFunc(m.stubs, func(x pythonProofStub) bool { return x.name == stub }); i >= 0 && m.stubs[i].contract.ID != c.ID {
			return b.unsupported("%s stands for two trusted contracts", o.name)
		} else if i < 0 {
			m.stubs = append(m.stubs, pythonProofStub{name: stub, contract: c, decl: decl, d: ad, typing: typing})
		}
	}
	for _, group := range order {
		m := modules[group]
		b.modules = append(b.modules, m)
		module := strings.TrimSuffix(m.file, ".py")
		var line string
		row := b.d.Bindings[group]
		switch {
		case len(row) == 1 && row[0].Kind == "import":
			line = "import " + module + " as " + group
		default:
			stub := m.stubs[0].name
			line = "from " + module + " import " + stub
			if stub != group {
				line += " as " + group
			}
		}
		b.bindings = append(b.bindings, line)
	}
	slices.SortFunc(b.in.Assumed, func(x, y ComponentContractUse) int { return strings.Compare(x.Contract, y.Contract) })
	return nil
}

// applicationOperation checks an application operation a trusted contract
// stands for is a function the abstraction can stand for: not async, whose
// call returns a coroutine, and not a class.
func (b *pythonProofBuilder) applicationOperation(c project.ResolvedComponentContract) error {
	decl, ok := b.s.declarations[c.Operation.Declaration]
	if !ok {
		return fmt.Errorf("trusted contract %s's operation has no declaration in the session", c.ID)
	}
	src, err := b.s.proofSource(decl)
	if err != nil {
		return err
	}
	od, err := analyzePythonProof(src, decl.Symbol)
	if err != nil {
		return b.unsupported("trusted contract %s's operation %s: %v", c.ID, decl.ID, err)
	}
	if od.Async {
		return b.unsupported("trusted contract %s's operation %s is an async function, whose call returns a coroutine", c.ID, decl.ID)
	}
	return nil
}

// missing records an operation no trusted contract binds. A dependency's
// names the distribution the dependency image installed its module from; a
// module none or several installed is an assumption no declaration accounts
// for.
func (b *pythonProofBuilder) missing(o *pythonProofOperation) error {
	op := o.op
	if op.Kind == project.OperationDependency {
		modules, err := b.s.moduleDistributions(b.ctx)
		if err != nil {
			return err
		}
		dists := modules[strings.Split(op.Module, ".")[0]]
		if len(dists) != 1 {
			why := "no installed distribution provides its module"
			if len(dists) > 1 {
				why = "its module comes from several installed distributions, " + strings.Join(dists, ", ")
			}
			b.boundary(PythonApplicationProofBoundary{Name: o.name, Kind: "missing"})
			b.unaccounted(ComponentUnaccounted{Kind: UnaccountedAssumption, Detail: "the function reaches " + o.dotted + ", which no trusted contract binds, and " + why})
			return nil
		}
		op.Distribution = normalizePythonDistribution(dists[0])
	}
	b.boundary(PythonApplicationProofBoundary{Name: o.name, Kind: "missing", Operation: &op})
	b.unaccounted(ComponentUnaccounted{Kind: MissingContract, Operation: &op})
	return nil
}

// unaccounted records a boundary once, however many names reach it.
func (b *pythonProofBuilder) unaccounted(u ComponentUnaccounted) {
	for _, x := range b.in.Unaccounted {
		if x.Kind == u.Kind && x.Detail == u.Detail && (x.Operation == nil) == (u.Operation == nil) && (x.Operation == nil || *x.Operation == *u.Operation) {
			return
		}
	}
	b.in.Unaccounted = append(b.in.Unaccounted, u)
}

func normalizePythonDistribution(name string) string {
	return pythonApplicationWheelName.ReplaceAllString(strings.ToLower(name), "-")
}

// abstraction checks a trusted contract's declaration is a contract and
// nothing more: an unadorned function at its module's top level whose body
// holds only Nagini's contract calls, besides a docstring, ... or pass, and
// which reads nothing but its own parameters, Nagini's contract names,
// typing names, the builtins Nagini models and the exception classes it
// models. So no assumption hides behind another. It returns the typing
// imports the abstraction needs, and the exceptions its Exsures say the
// operation raises; it raises no other.
func (b *pythonProofBuilder) abstraction(c project.ResolvedComponentContract, d *pythonProofDeclaration) ([]pythonProofBinding, []string, error) {
	fail := func(format string, args ...any) error {
		return b.unsupported("trusted contract %s's abstraction %s", c.ID, fmt.Sprintf(format, args...))
	}
	if d.Async {
		return nil, nil, fail("is an async function")
	}
	if len(d.Unsupported) > 0 {
		return nil, nil, fail("holds %s", strings.Join(d.Unsupported, ", "))
	}
	for _, decorator := range d.Decorators {
		if decorator != "ContractOnly" {
			return nil, nil, fail("has the decorator %s", decorator)
		}
	}
	contracts := 0
	for _, s := range d.Body {
		switch s.Kind {
		case "contract":
			contracts++
		case "docstring", "ellipsis", "pass":
		default:
			return nil, nil, fail("holds a statement that isn't a contract, on line %d", s.Line)
		}
	}
	if contracts == 0 {
		return nil, nil, fail("holds no contract")
	}
	// What the operation raises is what the inventory read from the
	// contract's own Exsures (D-0221), the bytes its stub copies to Nagini,
	// which models only these classes.
	var raises []string
	for _, path := range c.Exsures {
		name, builtin := strings.CutPrefix(path, "builtins.")
		if !builtin || !naginiExceptions[name] {
			return nil, nil, fail("declares that its operation raises %s, which the pinned Nagini doesn't model: it models Exception and ConnectionRefusedError", path)
		}
		raises = append(raises, name)
	}
	for _, name := range d.Bound {
		if slices.Contains(naginiCalls, name) {
			return nil, nil, fail("binds %s, which Nagini reads as its own", name)
		}
	}
	var typing []pythonProofBinding
	naginiStar := d.naginiStar()
	// The stub holds none of the module's other imports, so a name that may
	// come from another star import may not mean in the stub what it means
	// where the contract's tests read it.
	otherStar := slices.ContainsFunc(d.Star, func(s pythonProofBinding) bool { return s.Module != "nagini_contracts.contracts" || s.Level != 0 })
	for name, use := range d.Free {
		rows := d.Bindings[name]
		if len(rows) == 0 && otherStar {
			return nil, nil, fail("reads %s, which may come from a star import", name)
		}
		switch {
		case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Module == "nagini_contracts.contracts" && rows[0].Level == 0 && rows[0].Name == name,
			len(rows) == 0 && naginiStar && slices.Contains(naginiExports, name):
			if name == "Assume" {
				return nil, nil, fail("calls Assume")
			}
		case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Module == "typing" && rows[0].Level == 0 && (rows[0].Name == name || !slices.Contains(naginiNames, name)),
			len(rows) == 1 && rows[0].Kind == "import" && rows[0].Module == "typing" && rows[0].Asname == "":
			if rows[0].Shared {
				return nil, nil, fail("reads %s from line %d, which holds more than one statement", name, rows[0].Line)
			}
			if !slices.ContainsFunc(typing, func(t pythonProofBinding) bool { return t.Start == rows[0].Start }) {
				typing = append(typing, rows[0])
			}
		case len(rows) == 0 && (naginiNative[name] || naginiExceptions[name]) && !use.Deep:
		default:
			return nil, nil, fail("reads %s, which is neither its parameter, Nagini's, typing's, a builtin Nagini models nor an exception it models", name)
		}
	}
	slices.SortFunc(typing, func(x, y pythonProofBinding) int { return x.Start - y.Start })
	sort.Strings(raises)
	return typing, raises, nil
}

// handlers refuses an exception handler that can catch an exception from a
// boundary operation that its trusted contract doesn't declare. Nagini takes
// a contract with no Exsures for an exception to mean its operation never
// raises it, so such a handler is code the proof thinks is never reached.
// A bare except catches everything, and the exceptions a handler names must
// be ones Nagini models.
func (b *pythonProofBuilder) handlers() error {
	for _, t := range b.d.Tries {
		var contracts []string
		names := make([]string, 0, len(t.Reads))
		for name := range t.Reads {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			use := t.Reads[name]
			if id, ok := b.reached[name][""]; ok && use.Bare {
				contracts = append(contracts, id)
			}
			for _, attr := range use.Attributes {
				if id, ok := b.reached[name][attr]; ok {
					contracts = append(contracts, id)
				}
			}
		}
		if len(contracts) == 0 {
			continue
		}
		for _, h := range t.Handlers {
			switch {
			case h == "*":
				return b.unsupported("the bare except on line %d's try can catch any exception from %s, beyond what its trusted contract declares", t.Line, contracts[0])
			case h == "?" || !naginiExceptions[h]:
				return b.unsupported("a handler of line %d's try catches what Nagini doesn't model: it models Exception and ConnectionRefusedError", t.Line)
			}
			for _, id := range contracts {
				covered := slices.ContainsFunc(b.raises[id], func(d string) bool { return h == d || (h == "ConnectionRefusedError" && d == "Exception") })
				if !covered {
					return b.unsupported("the handler for %s on line %d's try can catch an exception from %s that its trusted contract doesn't declare with Exsures", h, t.Line, id)
				}
			}
		}
	}
	return nil
}

// write assembles the main module and each boundary module, line by line,
// recording the span each part takes and where it came from.
func (b *pythonProofBuilder) write() error {
	src := b.d.source
	var main pythonProofText
	main.file = b.in.Main
	// A __future__ import changes how the module compiles, and comes first.
	for _, row := range b.d.Future {
		main.copy("future", b.target.Source, src, row.Start, row.End, "")
	}
	slices.SortFunc(b.copied, func(x, y pythonProofBinding) int { return x.Start - y.Start })
	for _, row := range b.copied {
		kind := "constant"
		switch {
		case row.Module == "nagini_contracts.contracts":
			kind = "nagini"
		case row.Module == "typing":
			kind = "typing"
		}
		main.copy(kind, b.target.Source, src, row.Start, row.End, "")
	}
	for _, line := range b.bindings {
		main.write("binding", line+"\n", "")
	}
	main.write("blank", "\n\n", "")
	main.copy("target", b.target.Source, src, b.d.Start, b.d.End, "")
	files := []pythonProofText{main}
	for _, m := range b.modules {
		var t pythonProofText
		t.file = m.file
		t.write("preamble", "from nagini_contracts.contracts import *\n", "")
		var typing []pythonProofBinding
		for _, stub := range m.stubs {
			for _, row := range stub.typing {
				if !slices.ContainsFunc(typing, func(x pythonProofBinding) bool {
					return x.Start == row.Start && x.Line == row.Line && x.Module == row.Module && x.Name == row.Name
				}) {
					typing = append(typing, row)
					t.copy("typing", stub.decl.Source, stub.d.source, row.Start, row.End, stub.contract.ID)
				}
			}
		}
		for _, stub := range m.stubs {
			d := stub.d
			t.write("blank", "\n\n", "")
			t.write("marker", "@ContractOnly\n", stub.contract.ID)
			// The header starts at the def line: an abstraction's own
			// ContractOnly marker is the one the gate writes. Only the name
			// changes, to the operation's.
			def := bytes.LastIndexByte(d.source[:d.NameStart], '\n') + 1
			header := append([]byte{}, d.source[def:d.NameStart]...)
			header = append(header, stub.name...)
			header = append(header, d.source[d.NameEnd:d.HeaderEnd]...)
			s := t.copyRenamed("signature", stub.decl.Source, d.source, def, d.HeaderEnd, header, stub.contract.ID)
			s.name, s.nameEnd, s.rename = d.NameStart, d.NameEnd, stub.name
			copied := -1
			for _, s := range d.Body {
				// Contract calls sharing a line are copied with it, once.
				if s.Kind == "contract" && s.Start > copied {
					t.copy("contract", stub.decl.Source, d.source, s.Start, s.End, stub.contract.ID)
					copied = s.End - 1
				}
			}
		}
		files = append(files, t)
	}
	for _, t := range files {
		b.in.Files = append(b.in.Files, PythonApplicationProofFile{Path: t.file, SHA256: pythonApplicationHash(t.buf.Bytes()), Content: t.buf.String()})
		b.in.Spans = append(b.in.Spans, t.spans...)
	}
	sources := make([]string, 0, len(b.sources))
	for name := range b.sources {
		sources = append(sources, name)
	}
	sort.Strings(sources)
	for _, name := range sources {
		b.in.Sources = append(b.in.Sources, project.ComponentFile{Path: name, SHA256: b.s.snapshot[name].hash})
	}
	slices.SortFunc(b.in.Boundaries, func(x, y PythonApplicationProofBoundary) int {
		return strings.Compare(x.Name+"\x00"+x.Kind+"\x00"+x.Contract, y.Name+"\x00"+y.Kind+"\x00"+y.Contract)
	})
	if err := b.check(); err != nil {
		return err
	}
	b.in.Fingerprint = pythonApplicationFingerprint(struct {
		Target     project.Component
		Files      []PythonApplicationProofFile
		Spans      []PythonApplicationProofSpan
		Boundaries []PythonApplicationProofBoundary
		Sources    []project.ComponentFile
	}{b.in.Target, b.in.Files, b.in.Spans, b.in.Boundaries, b.in.Sources})
	return nil
}

// check confirms the input says what the application says: every copied
// span holds exactly the original bytes, the target among them, every
// signature renames only the function, and no copied line continues past
// what was copied.
func (b *pythonProofBuilder) check() error {
	contents := map[string][]byte{}
	for _, f := range b.in.Files {
		contents[f.Path] = []byte(f.Content)
	}
	read := func(source string) ([]byte, error) {
		if source == b.target.Source {
			return b.d.source, nil
		}
		for _, m := range b.modules {
			for _, stub := range m.stubs {
				if stub.decl.Source == source {
					return stub.d.source, nil
				}
			}
		}
		return nil, fmt.Errorf("no original source %s", source)
	}
	for _, s := range b.in.Spans {
		if s.Source == "" {
			continue
		}
		original, err := read(s.Source)
		if err != nil {
			return err
		}
		if s.from >= 0 && s.to <= len(original) && s.from <= s.to &&
			bytes.HasSuffix(bytes.TrimRight(original[s.from:s.to], "\r\n"), []byte(`\`)) {
			return b.unsupported("%s's line %d ends in a line continuation, which would join the input's next line", s.Source, s.SourceEndLine)
		}
	}
	return checkPythonProofSpans(b.in, contents, read)
}

// checkPythonProofSpans checks each span of in against the generated
// contents and the original sources.
func checkPythonProofSpans(in *PythonApplicationProofInput, contents map[string][]byte, read func(string) ([]byte, error)) error {
	target := 0
	for _, s := range in.Spans {
		generated, ok := contents[s.File]
		if !ok || s.start < 0 || s.end > len(generated) || s.start > s.end || pythonApplicationHash(generated[s.start:s.end]) != s.SHA256 {
			return fmt.Errorf("the verifier input's %s span in %s doesn't hold what it records", s.Kind, s.File)
		}
		if s.Source == "" {
			continue
		}
		original, err := read(s.Source)
		if err != nil {
			return err
		}
		if s.from < 0 || s.to > len(original) || s.from > s.to || pythonApplicationHash(original[s.from:s.to]) != s.SourceSHA256 {
			return fmt.Errorf("the verifier input's %s span doesn't match %s", s.Kind, s.Source)
		}
		switch s.Kind {
		case "signature":
			// A signature is the original header with only the function's
			// name replaced.
			if s.name < s.from || s.nameEnd < s.name || s.to < s.nameEnd {
				return fmt.Errorf("the verifier input's signature in %s doesn't name what it renames", s.File)
			}
			want := append(append(append([]byte{}, original[s.from:s.name]...), s.rename...), original[s.nameEnd:s.to]...)
			if !bytes.Equal(generated[s.start:s.end], want) {
				return fmt.Errorf("the verifier input's signature in %s changes more than the function's name", s.File)
			}
		default:
			if s.SHA256 != s.SourceSHA256 {
				return fmt.Errorf("the verifier input's %s span in %s changed what it copies from %s", s.Kind, s.File, s.Source)
			}
		}
		if s.Kind == "target" {
			target++
			if s.File != in.Main || s.Source != in.Target.Source {
				return fmt.Errorf("the verifier input's target isn't the selected function's source")
			}
		}
	}
	if target != 1 {
		return fmt.Errorf("the verifier input holds the selected function %d times", target)
	}
	return nil
}

// pythonProofText is one generated file as it is written.
type pythonProofText struct {
	file  string
	buf   bytes.Buffer
	spans []PythonApplicationProofSpan
}

func (t *pythonProofText) line() int {
	return bytes.Count(t.buf.Bytes(), []byte("\n")) + 1
}

func (t *pythonProofText) add(kind string, text []byte, contract string) *PythonApplicationProofSpan {
	if len(text) > 0 && text[len(text)-1] != '\n' {
		text = append(append([]byte{}, text...), '\n')
	}
	start, line := t.buf.Len(), t.line()
	t.buf.Write(text)
	s := PythonApplicationProofSpan{Kind: kind, File: t.file, Line: line, EndLine: t.line() - 1,
		SHA256: pythonApplicationHash(t.buf.Bytes()[start:]), Contract: contract, start: start, end: t.buf.Len()}
	t.spans = append(t.spans, s)
	return &t.spans[len(t.spans)-1]
}

func (t *pythonProofText) write(kind, text, contract string) {
	if kind == "blank" {
		t.buf.WriteString(text)
		return
	}
	t.add(kind, []byte(text), contract)
}

func (t *pythonProofText) copy(kind, source string, src []byte, from, to int, contract string) {
	text := src[from:to]
	if len(text) > 0 && text[len(text)-1] != '\n' {
		// A last line with no newline is copied as it is, and the newline
		// that ends it in the input is the gate's.
		s := t.add(kind, text, contract)
		s.end--
		s.SHA256 = pythonApplicationHash(text)
		t.source(s, source, src, from, to)
		return
	}
	t.source(t.add(kind, text, contract), source, src, from, to)
}

func (t *pythonProofText) copyRenamed(kind, source string, src []byte, from, to int, text []byte, contract string) *PythonApplicationProofSpan {
	s := t.add(kind, text, contract)
	t.source(s, source, src, from, to)
	return s
}

func (t *pythonProofText) source(s *PythonApplicationProofSpan, source string, src []byte, from, to int) {
	s.Source, s.from, s.to = source, from, to
	s.SourceLine = bytes.Count(src[:from], []byte("\n")) + 1
	s.SourceEndLine = s.SourceLine + bytes.Count(bytes.TrimSuffix(src[from:to], []byte("\n")), []byte("\n"))
	s.SourceSHA256 = pythonApplicationHash(src[from:to])
}

// writePythonProofInput writes every file of an input into dir.
func writePythonProofInput(dir string, in *PythonApplicationProofInput) error {
	for _, f := range in.Files {
		if f.Path != path.Base(f.Path) || !strings.HasSuffix(f.Path, ".py") {
			return fmt.Errorf("invalid verifier input file %q", f.Path)
		}
		if err := os.WriteFile(filepath.Join(dir, f.Path), []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return checkPythonProofFiles(dir, in)
}

// checkPythonProofFiles checks each of the input's files in dir holds the
// bytes the input records.
func checkPythonProofFiles(dir string, in *PythonApplicationProofInput) error {
	for _, f := range in.Files {
		b, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil {
			return err
		}
		if pythonApplicationHash(b) != f.SHA256 || string(b) != f.Content {
			return fmt.Errorf("the verifier input's %s changed", f.Path)
		}
	}
	return nil
}

var naginiLocation = regexp.MustCompile(`^([\w./-]+\.py):(\d+):(\d+): (.*)$`)

// mapNaginiErrors names each error's place in the original source where the
// input copies it, keeping the verifier input's place beside it.
func mapNaginiErrors(in *PythonApplicationProofInput, errs []string) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		m := naginiLocation.FindStringSubmatch(e)
		if m == nil {
			out = append(out, e)
			continue
		}
		line, _ := strconv.Atoi(m[2])
		mapped := e
		for _, s := range in.Spans {
			if s.File == m[1] && s.Source != "" && s.Line <= line && line <= s.EndLine {
				mapped = fmt.Sprintf("%s:%d:%s: %s (verifier input %s:%s)", s.Source, s.SourceLine+line-s.Line, m[3], m[4], m[1], m[2])
				break
			}
		}
		out = append(out, mapped)
	}
	return out
}
