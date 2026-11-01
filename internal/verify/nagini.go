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
// Nagini also reads the modules a marked file imports, and takes their
// contracts without proving their bodies, so the gate refuses, the same way,
// a marked file that imports a module of its package other than another
// marked file proved in the same run, and one whose proof would rest on a
// marked file the gate refuses (#383). It refuses a marked file, and so
// every core that imports it, where Python may load something else in its
// place: a compiled module of its name, its cached bytecode or a package,
// as D-0260 refuses a compiled module in place. Each file then runs in a
// directory of its own, holding only that file and the marked files it
// imports, so Nagini can read no other module of the project, and the gate
// says so of a proof that rests on a file Nagini didn't prove. The image is
// acquired only once a file needs Nagini.
func runNagini(ctx context.Context, acquire func(context.Context) (string, error), pkg string, proved, plain []string) (*Code, error) {
	stage, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(pkg, stage); err != nil {
		return nil, err
	}
	held, err := naginiHolds(pkg, proved)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*naginiFile, len(proved))
	for _, f := range proved {
		// The copy holds the very bytes Nagini reads.
		src, err := os.ReadFile(filepath.Join(stage, f))
		if err != nil {
			return nil, err
		}
		refused, imports, err := naginiRead(f, src)
		if err != nil {
			return nil, err
		}
		m := &naginiFile{src: src, refused: refused}
		m.fns, m.contracts = pythonFunctions(string(src))
		if what := held.shadowed(strings.TrimSuffix(f, ".py")); what != "" {
			m.refused = append(m.refused, fmt.Sprintf("%s: refused before Nagini runs: Python may load the package's %s, in place of %s, so what runs needn't be what Nagini proves", f, what, f))
		}
		for _, imp := range imports {
			switch file, why := held.resolve(imp); {
			case why != "":
				m.refused = append(m.refused, fmt.Sprintf("%s:%d: refused before Nagini runs: the line %s", f, imp.Line, why))
			case file != "":
				m.imports = append(m.imports, naginiEdge{line: imp.Line, module: imp.Module, file: file})
			}
		}
		files[f] = m
	}
	// A proof that would rest on a marked file the gate refuses is refused
	// too, and so is every proof on the way to it.
	for changed := true; changed; {
		changed = false
		for _, f := range proved {
			m := files[f]
			if len(m.refused) > 0 {
				continue
			}
			for _, e := range m.imports {
				if len(files[e.file].refused) > 0 {
					m.refused = append(m.refused, fmt.Sprintf("%s:%d: refused before Nagini runs: the line imports %s, the package's %s, which the gate refuses, so the proof would rest on contracts nothing proves", f, e.line, e.module, e.file))
				}
			}
			changed = changed || len(m.refused) > 0
		}
	}
	code := &Code{Verifier: "Nagini", Passed: true}
	image := ""
	failed := map[string]bool{}
	for _, f := range proved {
		m := files[f]
		if len(m.refused) > 0 {
			code.Passed = false
			code.Errors = append(code.Errors, m.refused...)
			code.Unverified = append(code.Unverified, m.fns...)
			continue
		}
		if image == "" {
			if image, err = acquire(ctx); err != nil {
				return nil, err
			}
		}
		code.Functions = append(code.Functions, m.fns...)
		code.Contracts = append(code.Contracts, m.contracts...)
		out, exit, err := naginiProve(ctx, image, f, files)
		if err != nil {
			return nil, err
		}
		if exit != 0 || !strings.Contains(out, "Verification successful") {
			code.Passed = false
			failed[f] = true
			code.Errors = append(code.Errors, naginiErrors(f, out)...)
		}
	}
	// Nagini proved each file against the contracts of the marked files it
	// imports, so a proof stands only where Nagini proved those too.
	for _, f := range proved {
		if len(files[f].refused) > 0 || failed[f] {
			continue
		}
		for _, g := range naginiReach(f, files) {
			if failed[g] {
				code.Errors = append(code.Errors, fmt.Sprintf("%s: its proof rests on %s's contracts, which Nagini didn't prove", f, g))
			}
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

// naginiFile is a marked file as the gate read it: its bytes, its functions
// and those with contracts, why the gate refuses it, if it does, and the
// marked files of its package it imports.
type naginiFile struct {
	src            []byte
	fns, contracts []string
	refused        []string
	imports        []naginiEdge
}

// naginiEdge is one import of another marked file of the package, by the
// line that holds it and the module it names.
type naginiEdge struct {
	line         int
	module, file string
}

// naginiReach is every other marked file whose contracts Nagini reads when
// it proves f, through the files f imports and the ones they import, in
// order.
func naginiReach(f string, files map[string]*naginiFile) []string {
	seen := map[string]bool{f: true}
	var out []string
	for queue := []string{f}; len(queue) > 0; queue = queue[1:] {
		for _, e := range files[queue[0]].imports {
			if !seen[e.file] {
				seen[e.file] = true
				out = append(out, e.file)
				queue = append(queue, e.file)
			}
		}
	}
	sort.Strings(out)
	return out
}

// naginiProve runs the pinned Nagini on f in a directory holding only f and
// the marked files it reaches, each with the bytes the gate read, and
// returns what Nagini said and its exit code.
func naginiProve(ctx context.Context, image, f string, files map[string]*naginiFile) (string, int, error) {
	work, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(work)
	for _, g := range append([]string{f}, naginiReach(f, files)...) {
		if err := os.WriteFile(filepath.Join(work, g), files[g].src, 0o644); err != nil {
			return "", 0, err
		}
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

// naginiPackage is what a package directory holds, by name, beside the
// marked files the gate proves in the run. Nagini type-checks with mypy,
// which looks for a module in Nagini's working directory before it looks in
// the standard library or the image's own packages: for a directory name/,
// a package or a namespace, a stub package name-stubs/, a stub name.pyi,
// and a module name.py. Python, where the code runs, looks for a package
// name/, then an extension, then name.py, and runs the bytecode
// __pycache__ caches for name.py when it validates, or always, when it's
// unchecked.
type naginiPackage struct {
	dir     string
	name    string
	names   []string        // what the directory holds, in order
	entries map[string]bool // whether each name the directory holds is a directory
	cached  []string        // what its __pycache__ holds
	proved  map[string]bool
}

func naginiHolds(pkg string, proved []string) (naginiPackage, error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return naginiPackage{}, err
	}
	abs, err := filepath.Abs(pkg)
	if err != nil {
		return naginiPackage{}, err
	}
	p := naginiPackage{dir: pkg, name: filepath.Base(abs), entries: map[string]bool{}, proved: map[string]bool{}}
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
	for _, f := range proved {
		p.proved[f] = true
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
// which the gate can't read or never proves: a compiled module of that
// name, the bytecode __pycache__ holds for it, or a package of that name,
// a directory holding an __init__ module. A namespace directory doesn't
// shadow the file, so it's left alone. The gate refuses each, as D-0260
// refuses a compiled module where an in-place proof's module would be.
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

// naginiOwn is why the gate refuses a relative import and an import of the
// package by its own name: the pinned Nagini resolves neither.
const naginiOwn = "which the pinned Nagini can't resolve: it proves each marked file as a top-level module of its own, so it finds another marked file only by that file's own name, as import helper finds helper.py"

// resolve says which marked file of the package an import of a marked file
// reaches, or why the gate refuses it (#383). An import of a module the
// package doesn't hold reaches neither: the standard library, Nagini's own
// contracts, or what the pinned image installs. A top-level name the
// standard library or nagini_contracts provides is theirs whatever the
// package holds, since that's the module Python loads, and it's the one
// Nagini reads too, since the package's file of that name never joins the
// directory Nagini runs in.
func (p naginiPackage) resolve(imp naginiImport) (file, why string) {
	if imp.Level > 0 {
		return "", fmt.Sprintf("imports from %s, relative to its package, %s", strings.Repeat(".", imp.Level)+imp.Module, naginiOwn)
	}
	top, _, _ := strings.Cut(imp.Module, ".")
	if top == "nagini_contracts" || slices.Contains(strings.Fields(pythonStdlibModules), top) {
		return "", ""
	}
	if n := p.compiled(top); n != "" {
		return "", fmt.Sprintf("imports %s, which Python may load from the package's %s, a compiled module the gate can't read", imp.Module, n)
	}
	module := top + ".py"
	dir, found := p.entries[module]
	isModule := found && !dir
	// What mypy would read in the module's place, or where there's no
	// module, in the order it looks: a package or a namespace, a stub
	// package and a stub.
	for _, other := range []struct {
		name string
		dir  bool
		what string
	}{
		{top, true, "directory " + top + ", whose modules the gate never proves"},
		{top + "-stubs", true, "directory " + top + "-stubs, a stub package the gate never proves"},
		{top + ".pyi", false, top + ".pyi, a stub the gate never proves"},
	} {
		if dir, ok := p.entries[other.name]; ok && dir == other.dir {
			return "", fmt.Sprintf("imports %s, which Nagini would read from the package's %s, and take its contracts", imp.Module, other.what)
		}
	}
	switch {
	case isModule && p.proved[module]:
		return module, ""
	case isModule && naginiTestFile(module):
		return "", fmt.Sprintf("imports %s, the package's %s, a test, which the gate never proves, so Nagini would take its contracts without proving its bodies", imp.Module, module)
	case isModule:
		return "", fmt.Sprintf("imports %s, the package's %s, which isn't marked %s, so Nagini would take its contracts without proving its bodies", imp.Module, module, NaginiHeader)
	case top == p.name:
		return "", fmt.Sprintf("imports %s, its own package by name, %s", imp.Module, naginiOwn)
	}
	return "", ""
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
