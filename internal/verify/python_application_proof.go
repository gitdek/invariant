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

// pythonStdlibModules are the pinned runtime's sys.stdlib_module_names, and
// pythonBuiltins its builtins, at toolchain.PythonVersion's minor version.
// They tell a missing contract on the standard library from one on a
// dependency's code, the same on every host.
var (
	//go:embed python_stdlib_modules.txt
	pythonStdlibModules string
	//go:embed python_builtins.txt
	pythonBuiltins string
)

// naginiExports are what the pinned Nagini's nagini_contracts.contracts
// exports, so a star import of it binds exactly these.
var naginiExports = strings.Fields(`ARP Acc AllLow Assert Assume ContractOnly Declassify Decreases Ensures Exists
Exsures Fold Forall Forall2 Forall3 Forall4 Forall5 Forall6 Ghost GhostReturns Implies Inline Invariant Let Low
LowEvent LowExit LowVal MayCreate MaySet Old Opaque PByteSeq PMultiset PSeq PSet Predicate PreservesLow Previous
Pure RD_PRED RaisedException Rd Refute Requires Result ResultT Reveal TerminatesSif ToByteSeq ToMS ToSeq Unfold
Unfolding Wildcard bytearray_pred dict_pred isNaN list_pred set_pred`)

// naginiNative are the builtins Nagini models itself, as part of the
// verifier: the types it knows, and the functions and containers its own
// theory defines. Every other operation beyond the selected function needs
// a trusted contract, I/O such as print included, which Nagini would
// otherwise accept with no contract at all.
var naginiNative = map[string]bool{"int": true, "float": true, "bool": true, "str": true, "object": true,
	"list": true, "dict": true, "set": true, "tuple": true, "len": true, "isinstance": true, "abs": true,
	"min": true, "max": true, "range": true}

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
// module: the input Nagini got, the recipe and image it ran in and how it
// ran, its output with the session's paths removed, and the result in the
// form proof attribution takes. An attempt that reached an operation no
// trusted contract binds doesn't run, since no complete input exists; one
// that ran reports exactly what Nagini said. It grants no gate success.
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
	// which copies whole lines, can't copy it alone.
	Shared bool `json:"shared"`
}

type pythonProofName struct {
	Attributes []string `json:"attributes"`
	Bare       bool     `json:"bare"`
	Deep       bool     `json:"deep"`
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
	Unsupported  []string                        `json:"unsupported"`
	Bindings     map[string][]pythonProofBinding `json:"bindings"`
	Star         []pythonProofBinding            `json:"star"`
	Declarations []string                        `json:"declarations"`
	Globals      []string                        `json:"globals"`
	Future       []pythonProofBinding            `json:"future"`
	source       []byte
}

// analyzePythonProof reads one top-level function with the host's Python in
// isolated mode, which parses the source and never imports or runs it.
func analyzePythonProof(source []byte, symbol string) (*pythonProofDeclaration, error) {
	request, err := json.Marshal(map[string]string{"source": base64.StdEncoding.EncodeToString(source), "symbol": symbol})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", pythonApplicationProofAnalyzer)
	cmd.Stdin = bytes.NewReader(request)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("isolated Python 3 syntax reader: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var d pythonProofDeclaration
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("Python syntax reader output: %w", err)
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

// pythonProofOperation is an operation the selected function reaches beyond
// its boundary, and the name and form it reaches it by.
type pythonProofOperation struct {
	op       project.ResolvedComponentOperation
	name     string // the name the function reads
	attr     string // for a module the function reads attributes of
	binding  pythonProofBinding
	dotted   string
	contract *project.ResolvedComponentContract
}

// ProofInput builds Nagini's input for the proof check on target, the
// canonical ID of a selected function, without running any tool beyond the
// host's isolated Python parser.
func (s *PythonApplicationSession) ProofInput(target string) (*PythonApplicationProofInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("Python application session is closed")
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	in, _, err := s.proofInput(target)
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
	in, check, err := s.proofInput(target)
	if err != nil {
		return nil, err
	}
	proof := &PythonApplicationProof{Input: *in, Recipe: toolchain.NaginiRecipe(), Exit: -1}
	result := ComponentProofResult{Target: target, Verifier: "Nagini", Contracts: slices.Clone(check.Proof.Contracts),
		Inputs: s.proofInputs(in, check), FileInputs: slices.Clone(in.Sources), Assumed: copyComponentContractUses(in.Assumed),
		Unaccounted: copyComponentUnaccounted(in.Unaccounted),
		Tool:        &ComponentProofTool{Name: "Nagini", Version: NaginiVersion, Pins: map[string]string{"recipe": proof.Recipe, "platform": "linux/amd64", "input": in.Fingerprint}}}
	var missing []string
	for _, u := range in.Unaccounted {
		if u.Kind == MissingContract {
			missing = append(missing, "no trusted contract binds "+unaccountedText(u)+", which the function reaches")
		}
	}
	if len(missing) > 0 {
		result.Errors = missing
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
	// The image Nagini runs in is the recipe's, by the digest of what the
	// recipe built. A session's double runs no image, and records the tag
	// the recipe would have.
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
	result.Tool.Pins["image"] = image
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

func (s *PythonApplicationSession) proofInput(target string) (*PythonApplicationProofInput, project.ResolvedComponentCheck, error) {
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
	b := &pythonProofBuilder{s: s, target: decl, d: d, in: &PythonApplicationProofInput{Target: decl, Main: PythonApplicationProofMain}}
	if err := b.build(); err != nil {
		return nil, check, fmt.Errorf("in-place proof of %s: %w", target, err)
	}
	return b.in, check, nil
}

// proofSource reads a declaration's source as the session staged it.
func (s *PythonApplicationSession) proofSource(c project.Component) ([]byte, error) {
	f, ok := s.snapshot[c.Source]
	if !ok || f.hash != c.SourceSHA256 {
		return nil, fmt.Errorf("stale declaration %s", c.ID)
	}
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(c.Source)))
	if err != nil {
		return nil, err
	}
	if pythonApplicationHash(b) != c.SourceSHA256 {
		return nil, fmt.Errorf("stale declaration %s", c.ID)
	}
	return b, nil
}

type pythonProofBuilder struct {
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
	for _, f := range d.Future {
		if f.Shared {
			return b.unsupported("line %d holds more than one statement, and the input copies whole lines", f.Line)
		}
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
	return b.write()
}

// bind classifies one name the target reads, and returns the operations it
// stands for beyond the target's boundary.
func (b *pythonProofBuilder) bind(name string, use pythonProofName) ([]*pythonProofOperation, error) {
	d := b.d
	if name == b.target.Symbol {
		// A recursive call uses the function's own contract, which the
		// input holds.
		return nil, nil
	}
	rows := d.Bindings[name]
	if len(rows) > 1 {
		return nil, b.unsupported("%s is bound more than once at its module's top level", name)
	}
	if len(rows) == 0 {
		naginiStar := slices.ContainsFunc(d.Star, func(s pythonProofBinding) bool { return s.Module == "nagini_contracts.contracts" && s.Level == 0 })
		if naginiStar && slices.Contains(naginiExports, name) {
			return nil, b.nagini(name, pythonProofBinding{Kind: "star"})
		}
		if slices.ContainsFunc(d.Star, func(s pythonProofBinding) bool { return s.Module != "nagini_contracts.contracts" || s.Level != 0 }) {
			return nil, b.unsupported("%s may come from a star import", name)
		}
		if naginiNative[name] {
			b.boundary(PythonApplicationProofBoundary{Name: name, Kind: "native"})
			return nil, nil
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
		return y.Name == x.Name && y.Kind == x.Kind && y.Contract == x.Contract
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

func (b *pythonProofBuilder) applicationSource(module string, level int) (string, error) {
	if level == 0 {
		return b.applicationModule(module), nil
	}
	if module == "" {
		return "", b.unsupported("the function imports a module with a relative import of a package")
	}
	base := path.Dir(b.target.Source)
	for i := 1; i < level; i++ {
		base = path.Dir(base)
	}
	if base == "." || strings.HasPrefix(base, "..") {
		base = ""
	}
	name := path.Join(base, strings.ReplaceAll(module, ".", "/"))
	for _, candidate := range []string{name + ".py", name + "/__init__.py"} {
		if _, ok := b.s.snapshot[candidate]; ok {
			return candidate, nil
		}
	}
	return "", b.unsupported("the relative import of %s resolves to no application source", module)
}

// boundaries binds every operation the target reaches to its trusted
// contract's abstraction, or records it as missing. A dependency
// operation no contract binds names the resolved wheel its module's name
// matches, or, with none, is an assumption no declaration accounts for.
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
		key := o.op.Key()
		c, ok := byKey[key]
		if !ok {
			b.missing(o)
			continue
		}
		o.contract = &c
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
		typing, err := b.abstraction(c, ad)
		if err != nil {
			return err
		}
		b.sources[decl.Source] = true
		op := c.Operation
		b.boundary(PythonApplicationProofBoundary{Name: o.name, Kind: "contract", Contract: c.ID, Operation: &op})
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

// missing records an operation no trusted contract binds.
func (b *pythonProofBuilder) missing(o *pythonProofOperation) {
	op := o.op
	if op.Kind == project.OperationDependency {
		if d := b.s.provenance.Dependencies; d != nil {
			want := normalizePythonDistribution(strings.Split(op.Module, ".")[0])
			for _, a := range d.Artifacts {
				if normalizePythonDistribution(a.Name) == want {
					op.Distribution = want
				}
			}
		}
		if op.Distribution == "" {
			b.unaccounted(ComponentUnaccounted{Kind: UnaccountedAssumption,
				Detail: "the function reaches " + o.dotted + ", which no trusted contract binds and no resolved wheel provides by that name"})
			return
		}
	}
	b.boundary(PythonApplicationProofBoundary{Name: o.name, Kind: "missing", Operation: &op})
	b.unaccounted(ComponentUnaccounted{Kind: MissingContract, Operation: &op})
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
// typing names and the builtins Nagini models. So no assumption hides
// behind another. It returns the typing imports the abstraction needs.
func (b *pythonProofBuilder) abstraction(c project.ResolvedComponentContract, d *pythonProofDeclaration) ([]pythonProofBinding, error) {
	fail := func(format string, args ...any) error {
		return b.unsupported("trusted contract %s's abstraction %s", c.ID, fmt.Sprintf(format, args...))
	}
	if d.Async {
		return nil, fail("is an async function")
	}
	if len(d.Unsupported) > 0 {
		return nil, fail("holds %s", strings.Join(d.Unsupported, ", "))
	}
	for _, decorator := range d.Decorators {
		if decorator != "ContractOnly" {
			return nil, fail("has the decorator %s", decorator)
		}
	}
	contracts := 0
	for _, s := range d.Body {
		switch s.Kind {
		case "contract":
			contracts++
		case "docstring", "ellipsis", "pass":
		default:
			return nil, fail("holds a statement that isn't a contract, on line %d", s.Line)
		}
	}
	if contracts == 0 {
		return nil, fail("holds no contract")
	}
	var typing []pythonProofBinding
	naginiStar := slices.ContainsFunc(d.Star, func(s pythonProofBinding) bool { return s.Module == "nagini_contracts.contracts" && s.Level == 0 })
	for name, use := range d.Free {
		rows := d.Bindings[name]
		switch {
		case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Module == "nagini_contracts.contracts" && rows[0].Level == 0,
			len(rows) == 0 && naginiStar && slices.Contains(naginiExports, name):
			if name == "Assume" {
				return nil, fail("calls Assume")
			}
		case len(rows) == 1 && rows[0].Kind == "from" && rows[0].Module == "typing" && rows[0].Level == 0,
			len(rows) == 1 && rows[0].Kind == "import" && rows[0].Module == "typing" && rows[0].Asname == "":
			if rows[0].Shared {
				return nil, fail("reads %s from line %d, which holds more than one statement", name, rows[0].Line)
			}
			if !slices.ContainsFunc(typing, func(t pythonProofBinding) bool { return t.Start == rows[0].Start }) {
				typing = append(typing, rows[0])
			}
		case len(rows) == 0 && naginiNative[name] && !use.Deep:
		default:
			return nil, fail("reads %s, which is neither its parameter, Nagini's, typing's nor a builtin Nagini models", name)
		}
	}
	slices.SortFunc(typing, func(x, y pythonProofBinding) int { return x.Start - y.Start })
	return typing, nil
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
			t.copyRenamed("signature", stub.decl.Source, d.source, def, d.HeaderEnd, header, stub.contract.ID)
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
// span holds exactly the original bytes, the target among them, and every
// signature renames only the function.
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

func (t *pythonProofText) copyRenamed(kind, source string, src []byte, from, to int, text []byte, contract string) {
	t.source(t.add(kind, text, contract), source, src, from, to)
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
