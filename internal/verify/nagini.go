package verify

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/toolchain"
)

// NaginiHeader marks a Python file for Nagini. Files without it are plain
// Python: they run, but nothing proves them, and the receipt says so.
const NaginiHeader = "# +nagini"

// naginiRuntime holds the stand-in for nagini_contracts that Python runs
// with, so a proved core runs unchanged.
//
//go:embed all:naginiruntime
var naginiRuntime embed.FS

// naginiSources splits a package's Python files into those Nagini proves and
// the rest. Tests are left out.
func naginiSources(pkg string) (proved, plain []string, err error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".py") || naginiTestFile(n) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(pkg, n))
		if err != nil {
			return nil, nil, err
		}
		if hasLine(string(b), NaginiHeader) {
			proved = append(proved, n)
		} else {
			plain = append(plain, n)
		}
	}
	sort.Strings(proved)
	sort.Strings(plain)
	return proved, plain, nil
}

// naginiTestFile says whether a Python file's name makes it a test, which
// the gate runs and never proves.
func naginiTestFile(name string) bool {
	return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")
}

func hasLine(src, want string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// runNagini proves each marked file, with no network, in the image acquire
// gives, and names the functions it proved and the ones it never saw. Nagini
// proves a file whole, so the gate first reads each one for arithmetic the
// pinned Nagini models apart from Python, by the rule an in-place proof
// applies (#359), and refuses one that holds any before Nagini runs: the
// code fails, saying where and why, and Nagini never sees the file (#380).
//
// A proved core can rest only on itself, the standard library and the
// modules Nagini models, so the gate refuses, the same way, a marked file
// that imports anything its package holds, marked or not (#383): Nagini
// takes an imported module's contracts without proving its bodies, and where
// the code runs, Python binds the name to whatever its path finds first,
// which needn't be the module Nagini read. It refuses a marked file where
// Python may load something else in its place too: a compiled module of its
// name, its cached bytecode or a package, as D-0260 refuses a compiled
// module in place. Each file then runs in a directory holding only that
// file, so Nagini can read no module of the project. The image is acquired
// only once a file needs Nagini.
func runNagini(ctx context.Context, acquire func(context.Context) (string, error), pkg string, proved, plain []string) (*Code, error) {
	stage, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(pkg, stage); err != nil {
		return nil, err
	}
	held, err := naginiHolds(pkg)
	if err != nil {
		return nil, err
	}
	code := &Code{Verifier: "Nagini", Passed: true}
	image := ""
	for _, f := range proved {
		// The copy holds the very bytes Nagini reads.
		src, err := os.ReadFile(filepath.Join(stage, f))
		if err != nil {
			return nil, err
		}
		fns, contracts := pythonFunctions(string(src))
		refused, imports, err := naginiRead(f, src)
		if err != nil {
			return nil, err
		}
		if what := held.shadowed(strings.TrimSuffix(f, ".py")); what != "" {
			refused = append(refused, fmt.Sprintf("%s: refused before Nagini runs: Python may load the package's %s, in place of %s, so what runs needn't be what Nagini proves", f, what, f))
		}
		for _, imp := range imports {
			if why := held.resolve(imp); why != "" {
				refused = append(refused, fmt.Sprintf("%s:%d: refused before Nagini runs: the line %s", f, imp.Line, why))
			}
		}
		if len(refused) > 0 {
			code.Passed = false
			code.Errors = append(code.Errors, refused...)
			code.Unverified = append(code.Unverified, fns...)
			continue
		}
		if image == "" {
			if image, err = acquire(ctx); err != nil {
				return nil, err
			}
		}
		code.Functions = append(code.Functions, fns...)
		code.Contracts = append(code.Contracts, contracts...)
		out, exit, err := naginiProve(ctx, image, f, src)
		if err != nil {
			return nil, err
		}
		if exit != 0 || !strings.Contains(out, "Verification successful") {
			code.Passed = false
			code.Errors = append(code.Errors, naginiErrors(f, out)...)
		}
	}
	for _, f := range plain {
		src, err := os.ReadFile(filepath.Join(pkg, f))
		if err != nil {
			return nil, err
		}
		fns, _ := pythonFunctions(string(src))
		code.Unverified = append(code.Unverified, fns...)
	}
	return code, nil
}

// naginiProve runs the pinned Nagini on f in a directory holding only f,
// with the bytes the gate read, and returns what Nagini said and its exit
// code.
func naginiProve(ctx context.Context, image, f string, src []byte) (string, int, error) {
	work, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(work)
	if err := os.WriteFile(filepath.Join(work, f), src, 0o644); err != nil {
		return "", 0, err
	}
	var out bytes.Buffer
	cmd := toolchain.Docker(ctx, "run", "--rm", "--network", "none", "--platform", "linux/amd64",
		"-v", work+":/work", "-w", "/work", image, f)
	cmd.Stdout, cmd.Stderr = &out, &out
	exit := 0
	if err := cmd.Run(); err != nil {
		var e *exec.ExitError
		if !errors.As(err, &e) {
			return "", 0, err
		}
		exit = e.ExitCode()
	}
	return out.String(), exit, nil
}

// naginiImport is one import a marked file holds, as the analyzer reads it:
// the module it names and the levels a relative import climbs.
type naginiImport struct {
	Line   int    `json:"line"`
	Module string `json:"module"`
	Level  int    `json:"level"`
}

// naginiPackage is what a package directory holds, by name. Nagini
// type-checks with mypy, which looks for a module in its working directory
// before the standard library or the image's own packages: for a directory
// name/, a package or a namespace, a stub package name-stubs/, a stub
// name.pyi and a module name.py. Python, where the code runs, looks for a
// package name/, then an extension, then name.py, and runs the bytecode
// __pycache__ caches for name.py when it validates, or always, when it's
// unchecked.
type naginiPackage struct {
	dir     string
	name    string
	names   []string        // what the directory holds, in order
	entries map[string]bool // whether each name the directory holds is a directory
	cached  []string        // what its __pycache__ holds
}

func naginiHolds(pkg string) (naginiPackage, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return naginiPackage{}, err
	}
	abs, err := filepath.Abs(pkg)
	if err != nil {
		return naginiPackage{}, err
	}
	p := naginiPackage{dir: pkg, name: filepath.Base(abs), entries: map[string]bool{}}
	for _, e := range entries {
		p.names = append(p.names, e.Name())
		p.entries[e.Name()] = e.IsDir()
	}
	if p.entries["__pycache__"] {
		cached, err := os.ReadDir(filepath.Join(pkg, "__pycache__"))
		if err != nil {
			return naginiPackage{}, err
		}
		for _, e := range cached {
			p.cached = append(p.cached, e.Name())
		}
	}
	return p, nil
}

// compiled is a compiled module the package holds by a module's name, an
// extension or sourceless bytecode, by the rule D-0260's scan reads one by:
// a compiled module's name runs to its first dot, so core.abi3.so and
// core.cpython-313-x86_64-linux-gnu.so are both core.
func (p naginiPackage) compiled(module string) string {
	for _, n := range p.names {
		if first, _, _ := strings.Cut(n, "."); first == module && !p.entries[n] && pythonCompiled(n) {
			return n
		}
	}
	return ""
}

// shadowed says what Python may load in place of the package's module.py,
// which the gate can't read or never proves: an extension of that name,
// which Python's finder takes before the source; sourceless bytecode of that
// name, which it takes only after the source, and the gate refuses anyway,
// as D-0260 refuses a compiled module where an in-place proof's module would
// be; the bytecode __pycache__ holds for it; or a package of that name, a
// directory holding an __init__ module, which Python takes before the file.
// A namespace directory doesn't shadow the file, so it's left alone.
func (p naginiPackage) shadowed(module string) string {
	if n := p.compiled(module); n != "" {
		return n + ", a compiled module the gate can't read"
	}
	for _, n := range p.cached {
		if first, _, _ := strings.Cut(n, "."); first == module && strings.HasSuffix(n, ".pyc") {
			return "__pycache__/" + n + ", bytecode the gate can't read"
		}
	}
	if p.entries[module] {
		inside, err := os.ReadDir(filepath.Join(p.dir, module))
		// A directory the gate can't list may be a package.
		if err != nil || slices.ContainsFunc(inside, func(e fs.DirEntry) bool {
			first, _, _ := strings.Cut(e.Name(), ".")
			return first == "__init__" && !e.IsDir() && (strings.HasSuffix(e.Name(), ".py") || pythonCompiled(e.Name()))
		}) {
			return "directory " + module + ", a package the gate never proves"
		}
	}
	return ""
}

// naginiOnly is why the gate refuses every import of what a marked file's
// package holds (#383).
const naginiOnly = "and a proved core can rest only on itself, the standard library and the modules Nagini models"

// resolve says why the gate refuses an import a marked file holds, or
// nothing for an import of what the package doesn't hold: the standard
// library, Nagini's own contracts, or what Nagini's image installs, which
// Nagini resolves or fails on, since the directory it runs in holds nothing
// else. Every import of what the package holds is refused, in any form,
// marked or not, relative or by the package's own name: Nagini would take
// that module's contracts without proving its bodies, and where the code
// runs Python binds the name to whatever its path finds first, the project
// root before the package (#383). A top-level name the standard library or
// nagini_contracts provides is theirs whatever the package holds, since
// that's the module Python loads, and the one Nagini reads.
func (p naginiPackage) resolve(imp naginiImport) string {
	if imp.Level > 0 {
		return fmt.Sprintf("imports from %s, relative to its package, %s", strings.Repeat(".", imp.Level)+imp.Module, naginiOnly)
	}
	top, _, _ := strings.Cut(imp.Module, ".")
	if top == "nagini_contracts" || slices.Contains(strings.Fields(pythonStdlibModules), top) {
		return ""
	}
	if held := p.holds(top); held != "" {
		return fmt.Sprintf("imports %s, which the package holds as %s, %s", imp.Module, held, naginiOnly)
	}
	if top == p.name {
		return fmt.Sprintf("imports %s, its own package by name, %s", imp.Module, naginiOnly)
	}
	return ""
}

// holds names what the package holds under a top-level module's name, as
// Python or mypy would find it: a module, a stub, a directory, a stub
// package or a compiled module.
func (p naginiPackage) holds(top string) string {
	for _, c := range []struct {
		name string
		dir  bool
		what string
	}{
		{top + ".py", false, top + ".py"},
		{top + ".pyi", false, top + ".pyi, a stub"},
		{top, true, "the directory " + top},
		{top + "-stubs", true, "the directory " + top + "-stubs, a stub package"},
	} {
		if dir, ok := p.entries[c.name]; ok && dir == c.dir {
			return c.what
		}
	}
	if n := p.compiled(top); n != "" {
		return n + ", a compiled module"
	}
	return ""
}

// naginiRead reads a marked file with the host's isolated Python, which
// parses it and never runs it, and returns, as file:line: why, each place
// it holds arithmetic the pinned Nagini models apart from Python (#380),
// and every import it holds, wherever it stands (#383).
func naginiRead(file string, src []byte) ([]string, []naginiImport, error) {
	var reply struct {
		Arithmetic []pythonProofDynamic `json:"arithmetic"`
		Imports    []naginiImport       `json:"imports"`
		Unreadable string               `json:"unreadable"`
	}
	request := map[string]string{"marked": base64.StdEncoding.EncodeToString(src),
		"standin": base64.StdEncoding.EncodeToString(naginiStandInSource)}
	if err := runPythonProofAnalyzer(request, &reply); err != nil {
		return nil, nil, err
	}
	if reply.Unreadable != "" {
		return []string{fmt.Sprintf("%s: refused before Nagini runs: the host's python3 can't parse it, so the gate can't check its arithmetic: %s", file, reply.Unreadable)}, nil, nil
	}
	refused := make([]string, 0, len(reply.Arithmetic))
	for _, p := range reply.Arithmetic {
		refused = append(refused, fmt.Sprintf("%s:%d: refused before Nagini runs: the line %s", file, p.Line, p.What))
	}
	return refused, reply.Imports, nil
}

// naginiRefusals is naginiRead's refusals for a marked file's arithmetic:
// in any function or method, and in the module's and its classes' own
// statements, which Nagini reads too. That's the rule an in-place proof
// applies (#359), except that Acc may take a permission fraction, a
// positive int literal over one at least as large, as Acc(x, 1/2), which
// Nagini reads as a permission and never as Python's division. A file the
// host's Python can't parse is refused too, since the gate couldn't check
// it.
func naginiRefusals(file string, src []byte) ([]string, error) {
	refused, _, err := naginiRead(file, src)
	return refused, err
}

// naginiError is one of Nagini's errors: the message, then where it is, as
// "(core.py@78.12--78.38)."
var naginiError = regexp.MustCompile(`^(.*?)\s*\(([\w./-]+\.py)@(\d+)\.(\d+)--\d+\.\d+\)\.?$`)

// naginiErrors reads the errors Nagini lists after "Errors:", one per line,
// as file:line:col: message. The branch conditions it adds under an error
// are left out.
func naginiErrors(file, out string) []string {
	var errs []string
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Errors:" {
			continue
		}
		branch := false
		for _, e := range lines[i+1:] {
			if strings.TrimSpace(e) == "" || strings.HasPrefix(e, "Verification took") {
				break
			}
			if strings.HasPrefix(e, "Branch conditions:") {
				branch = true
				continue
			}
			if branch && (e[0] == ' ' || e[0] == '\t') {
				continue
			}
			branch = false
			e = strings.TrimSpace(e)
			if m := naginiError.FindStringSubmatch(e); m != nil {
				e = m[2] + ":" + m[3] + ":" + m[4] + ": " + m[1]
			}
			errs = append(errs, e)
		}
	}
	if len(errs) == 0 {
		errs = []string{file + ": Nagini didn't verify it:\n" + lastLines(out, 15)}
	}
	return errs
}

var (
	pyClass = regexp.MustCompile(`^class (\w+)`)
	pyDef   = regexp.MustCompile(`^(\s*)def (\w+)\(`)
)

// pythonFunctions names a module's top-level functions and its classes'
// methods (Class.method), and those whose bodies state a contract. Functions
// nested inside others belong to them and aren't listed.
func pythonFunctions(src string) (all, withContract []string) {
	lines := strings.Split(src, "\n")
	class := ""
	for i, line := range lines {
		if m := pyClass.FindStringSubmatch(line); m != nil {
			class = m[1]
			continue
		}
		m := pyDef.FindStringSubmatch(line)
		if m == nil {
			if line != "" && line[0] != ' ' && line[0] != '#' && line[0] != '\t' {
				class = ""
			}
			continue
		}
		name := m[2]
		switch {
		case m[1] == "":
			class = ""
		case class != "" && len(m[1]) == 4:
			name = class + "." + name
		default:
			continue
		}
		all = append(all, name)
		for _, body := range lines[i+1:] {
			if pyDef.MatchString(body) || pyClass.MatchString(body) {
				break
			}
			if strings.Contains(body, "Requires(") || strings.Contains(body, "Ensures(") {
				withContract = append(withContract, name)
				break
			}
		}
	}
	return all, withContract
}

// writeNaginiRuntime writes the nagini_contracts stand-in under dir.
func writeNaginiRuntime(dir string) error {
	return fs.WalkDir(naginiRuntime, "naginiruntime", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("naginiruntime", path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := naginiRuntime.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
