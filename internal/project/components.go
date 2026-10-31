package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ComponentsConfig declares package directories and the checks intended for
// their code. It says nothing about what a verifier or test has established.
type ComponentsConfig struct {
	Scope  []string         `json:"scope"`
	Checks []ComponentCheck `json:"checks,omitempty"`
}

// ComponentRef selects one declaration in a source file, relative to the
// application for existing Python and to the project otherwise.
type ComponentRef struct {
	Source string `json:"source"`
	Symbol string `json:"symbol"`
}

type ComponentCheck struct {
	Target      ComponentRef          `json:"target"`
	Proof       *ComponentProof       `json:"proof,omitempty"`
	Tests       []ComponentRef        `json:"tests,omitempty"`
	Conformance []ComponentRef        `json:"conformance,omitempty"`
	Assumptions []ComponentAssumption `json:"assumptions,omitempty"`
	Invocations []ComponentInvocation `json:"invocations,omitempty"`
}

type ComponentProof struct {
	Contracts []ComponentRef `json:"contracts"`
}

// ComponentInventory keeps source identity separate from intended checks.
// Legacy projects have no inferred scope, components or checks.
type ComponentInventory struct {
	Mode       string                   `json:"mode"`
	Scope      []string                 `json:"scope,omitempty"`
	Files      []ComponentFile          `json:"files,omitempty"`
	Components []Component              `json:"components,omitempty"`
	Checks     []ResolvedComponentCheck `json:"checks,omitempty"`
	Provenance *ComponentProvenance     `json:"provenance,omitempty"`
}

type ComponentFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Component struct {
	ID             string `json:"id"`
	Language       string `json:"language"`
	Package        string `json:"package"`
	Symbol         string `json:"symbol"`
	Source         string `json:"source"`
	SourceSHA256   string `json:"source_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
}

type ResolvedComponentCheck struct {
	Target      string                        `json:"target"`
	Proof       *ResolvedComponentProof       `json:"proof,omitempty"`
	Tests       []string                      `json:"tests,omitempty"`
	Conformance []string                      `json:"conformance,omitempty"`
	Assumptions []ResolvedComponentAssumption `json:"assumptions,omitempty"`
	Invocations []ResolvedComponentInvocation `json:"invocations,omitempty"`
}

type ResolvedComponentProof struct {
	Contracts []string `json:"contracts"`
}

// ResolveComponents inventories non-test Go or Python sources in each scoped
// directory and resolves every selected reference. Python uses an isolated
// syntax parser; no project code runs and no verification evidence is supplied.
func ResolveComponents(p *Project) (*ComponentInventory, error) {
	root := p.Dir
	if p.Manifest.Language == "python" && len(p.Manifest.Existing) > 0 {
		app, err := ResolvePythonApplication(p)
		if err != nil {
			return nil, err
		}
		root = app.Root
	}
	cfg := p.Manifest.Components
	if cfg == nil {
		return &ComponentInventory{Mode: "legacy"}, nil
	}
	language := p.Manifest.Language
	if language == "" {
		language = "go"
	}
	if language != "go" && language != "python" {
		return nil, fmt.Errorf("components: language %q is not supported; component discovery supports Go and Python", language)
	}
	if len(cfg.Scope) == 0 {
		return nil, fmt.Errorf("components: scope must name at least one %s source directory", language)
	}
	inv := &ComponentInventory{Mode: "explicit", Scope: append([]string(nil), cfg.Scope...)}
	sort.Strings(inv.Scope)
	r := componentResolver{
		language: language,
		root:     root, files: map[string]componentSource{}, scoped: map[ComponentRef]Component{},
		inputs: map[string]componentInput{}, declarations: map[string]Component{},
		provenanceFiles: map[string]ComponentFile{}, modules: map[string]ComponentFile{},
	}
	for i, dir := range inv.Scope {
		if i > 0 && dir == inv.Scope[i-1] {
			return nil, fmt.Errorf("components: duplicate scope %q", dir)
		}
		full, err := componentPath(root, dir, true)
		if err != nil {
			return nil, fmt.Errorf("components scope %q: %w", dir, err)
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil, fmt.Errorf("components scope %q: %w", dir, err)
		}
		count := 0
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !r.sourceFile(name) || r.testFile(name) {
				continue
			}
			source := path.Join(dir, name)
			f, err := r.read(source)
			if err != nil {
				return nil, err
			}
			count++
			inv.Files = append(inv.Files, f.file)
			inv.Components = append(inv.Components, f.components...)
			for _, c := range f.components {
				r.scoped[ComponentRef{Source: c.Source, Symbol: c.Symbol}] = c
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("components scope %q: no non-test %s source files", dir, language)
		}
	}
	targets := map[string]bool{}
	for _, check := range cfg.Checks {
		target, err := r.resolve(check.Target, "target")
		if err != nil {
			return nil, err
		}
		if targets[target.ID] {
			return nil, fmt.Errorf("components: duplicate target %s", target.ID)
		}
		targets[target.ID] = true
		if check.Proof == nil && len(check.Tests) == 0 && len(check.Conformance) == 0 && len(check.Assumptions) == 0 {
			return nil, fmt.Errorf("components target %s: check must select proof, tests, conformance or assumptions", target.ID)
		}
		resolved := ResolvedComponentCheck{Target: target.ID}
		if check.Proof != nil {
			if len(check.Proof.Contracts) == 0 {
				return nil, fmt.Errorf("components target %s: proof must select at least one contract", target.ID)
			}
			contracts, err := r.associations(check.Proof.Contracts, "contract")
			if err != nil {
				return nil, err
			}
			resolved.Proof = &ResolvedComponentProof{Contracts: contracts}
		}
		if resolved.Tests, err = r.associations(check.Tests, "test"); err != nil {
			return nil, err
		}
		if resolved.Conformance, err = r.associations(check.Conformance, "conformance"); err != nil {
			return nil, err
		}
		if resolved.Assumptions, err = r.assumptions(check.Assumptions); err != nil {
			return nil, fmt.Errorf("components target %s: %w", target.ID, err)
		}
		if resolved.Invocations, err = r.invocations(check.Invocations, resolved); err != nil {
			return nil, fmt.Errorf("components target %s: %w", target.ID, err)
		}
		inv.Checks = append(inv.Checks, resolved)
	}
	inv.Provenance = r.provenance()
	sort.Slice(inv.Files, func(i, j int) bool { return inv.Files[i].Path < inv.Files[j].Path })
	sort.Slice(inv.Components, func(i, j int) bool { return inv.Components[i].ID < inv.Components[j].ID })
	sort.Slice(inv.Checks, func(i, j int) bool { return inv.Checks[i].Target < inv.Checks[j].Target })
	return inv, nil
}

type componentSource struct {
	file       ComponentFile
	components []Component
}

type componentResolver struct {
	language        string
	root            string
	files           map[string]componentSource
	scoped          map[ComponentRef]Component
	inputs          map[string]componentInput
	declarations    map[string]Component
	provenanceFiles map[string]ComponentFile
	modules         map[string]ComponentFile
}

func (r *componentResolver) associations(refs []ComponentRef, kind string) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	for _, ref := range refs {
		c, err := r.resolve(ref, kind)
		if err != nil {
			return nil, err
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("components: duplicate %s selection %s", kind, c.ID)
		}
		seen[c.ID] = true
		ids = append(ids, c.ID)
		if kind == "test" || kind == "conformance" {
			r.declarations[c.ID] = c
			r.provenanceFiles[c.Source] = ComponentFile{Path: c.Source, SHA256: c.SourceSHA256}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (r *componentResolver) resolve(ref ComponentRef, kind string) (Component, error) {
	fail := func(why string) (Component, error) {
		return Component{}, fmt.Errorf("components %s %q in %q: %s", kind, ref.Symbol, ref.Source, why)
	}
	if ref.Symbol == "" {
		return fail("symbol is required")
	}
	if !r.sourceFile(ref.Source) {
		return fail("source must be a " + r.language + " file")
	}
	if kind == "test" && !r.testFile(ref.Source) {
		return fail("test references must name a test source file")
	}
	if kind == "target" || kind == "contract" {
		c, ok := r.scoped[ref]
		if !ok {
			return fail("selection must resolve to exactly one declaration in scope")
		}
		if kind == "contract" && c.ContractSHA256 == "" {
			if r.language == "python" {
				return fail("declaration has no supported inline Nagini contract")
			}
			return fail("declaration has no inline Gobra contract")
		}
		return c, nil
	}
	f, err := r.read(ref.Source)
	if err != nil {
		return fail(err.Error())
	}
	for _, c := range f.components {
		if c.Symbol == ref.Symbol {
			return c, nil
		}
	}
	return fail("selection must resolve to exactly one declaration")
}

func (r *componentResolver) read(source string) (componentSource, error) {
	if f, ok := r.files[source]; ok {
		return f, nil
	}
	input, err := r.input(source)
	if err != nil {
		return componentSource{}, fmt.Errorf("components source %q: %w", source, err)
	}
	if r.language == "python" {
		out, err := componentPython(source, input)
		if err != nil {
			return componentSource{}, fmt.Errorf("components source %q: %w", source, err)
		}
		r.files[source] = out
		return out, nil
	}
	b := input.bytes
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, source, b, parser.ParseComments)
	if err != nil {
		return componentSource{}, fmt.Errorf("components source %q: %w", source, err)
	}
	pkg, err := r.componentPackage(path.Dir(source))
	if err != nil {
		return componentSource{}, fmt.Errorf("components source %q: %w", source, err)
	}
	out := componentSource{file: input.file}
	ordinals, seen := map[string]int{}, map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		symbol := fn.Name.Name
		if fn.Recv != nil {
			if len(fn.Recv.List) != 1 {
				return componentSource{}, fmt.Errorf("components source %q: invalid receiver for %s", source, symbol)
			}
			receiver := componentReceiver(fn.Recv.List[0].Type)
			if receiver == "" {
				return componentSource{}, fmt.Errorf("components source %q: unsupported receiver for %s", source, symbol)
			}
			symbol = receiver + "." + symbol
		}
		if fn.Name.Name == "_" || fn.Name.Name == "init" && fn.Recv == nil {
			ordinals[symbol]++
			symbol += "[" + strconv.Itoa(ordinals[symbol]) + "]"
		}
		if seen[symbol] {
			return componentSource{}, fmt.Errorf("components source %q: duplicate declaration %s", source, symbol)
		}
		seen[symbol] = true
		out.components = append(out.components, Component{
			ID:       "go:" + pkg + "#" + symbol + "@" + source,
			Language: "go", Package: pkg, Symbol: symbol, Source: source,
			SourceSHA256: out.file.SHA256, ContractSHA256: componentContract(fset, fn.Doc, b),
		})
	}
	r.files[source] = out
	return out, nil
}

func (r *componentResolver) sourceFile(name string) bool {
	if r.language == "python" {
		return strings.HasSuffix(name, ".py")
	}
	return strings.HasSuffix(name, ".go")
}

func (r *componentResolver) testFile(name string) bool {
	if r.language == "python" {
		name = path.Base(name)
		return strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") || strings.HasSuffix(name, "_test.py")
	}
	return strings.HasSuffix(name, "_test.go")
}

func componentReceiver(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if name := componentReceiver(t.X); name != "" {
			return "(*" + name + ")"
		}
	case *ast.IndexExpr:
		return componentReceiver(t.X)
	case *ast.IndexListExpr:
		return componentReceiver(t.X)
	case *ast.ParenExpr:
		return componentReceiver(t.X)
	}
	return ""
}

func componentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func componentContract(fset *token.FileSet, doc *ast.CommentGroup, src []byte) string {
	if doc == nil {
		return ""
	}
	annotated := false
	for _, c := range doc.List {
		if strings.HasPrefix(c.Text, "//@") || strings.HasPrefix(c.Text, "// @") || strings.HasPrefix(c.Text, "/*@") {
			annotated = true
		}
	}
	if !annotated {
		return ""
	}
	start := fset.Position(doc.Pos()).Offset
	last := doc.List[len(doc.List)-1]
	end := fset.Position(last.Pos()).Offset
	// Read the original bytes: ast.Comment.Text drops carriage returns.
	if strings.HasPrefix(last.Text, "//") {
		if n := bytes.IndexByte(src[end:], '\n'); n >= 0 {
			end += n + 1
		} else {
			end = len(src)
		}
	} else {
		end += bytes.Index(src[end:], []byte("*/")) + 2
		newline := end
		for newline < len(src) && (src[newline] == ' ' || src[newline] == '\t' || src[newline] == '\r') {
			newline++
		}
		if newline < len(src) && src[newline] == '\n' {
			end = newline + 1
		}
	}
	return componentHash(src[start:end])
}

// componentPath rejects aliases at every step below the project root, before
// opening any source, association or module file.
func componentPath(root, rel string, directory bool) (string, error) {
	if rel == "" || path.Clean(rel) != rel || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, `\`) {
		return "", fmt.Errorf("%q must be a clean project-relative path", rel)
	}
	full := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%q must not contain symbolic links", rel)
		}
		if i < len(parts)-1 || directory {
			if !info.IsDir() {
				return "", fmt.Errorf("%q must name a directory", rel)
			}
		} else if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%q must name a regular file", rel)
		}
	}
	return full, nil
}

// componentPackage uses the nearest module inside the project, including a
// nested module. Package clauses alone cannot distinguish import identities.
func (r *componentResolver) componentPackage(dir string) (string, error) {
	for parent := dir; ; parent = path.Dir(parent) {
		name := path.Join(parent, "go.mod")
		input, err := r.input(name)
		if err == nil {
			module, err := componentModule(input.bytes)
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			r.modules[name] = input.file
			rel, err := filepath.Rel(filepath.FromSlash(parent), filepath.FromSlash(dir))
			if err != nil {
				return "", err
			}
			if rel == "." {
				return module, nil
			}
			return module + "/" + filepath.ToSlash(rel), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent == "." {
			return "", fmt.Errorf("no enclosing go.mod inside the project for %q", dir)
		}
	}
}

func componentModule(src []byte) (string, error) {
	module, block := "", ""
	found := false
	for _, line := range strings.Split(string(src), "\n") {
		// A module import path cannot contain //, even inside quotes.
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		value := line
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			if block != "module" {
				continue
			}
		} else {
			end := strings.IndexAny(line, " \t(")
			if end < 0 {
				end = len(line)
			}
			directive := line[:end]
			value = strings.TrimSpace(line[end:])
			if value == "(" {
				block = directive
				continue
			}
			if directive != "module" {
				continue
			}
		}
		if found || value == "" {
			return "", fmt.Errorf("want exactly one module import path")
		}
		found = true
		module = value
		if value[0] == '"' || value[0] == '`' {
			var err error
			module, err = strconv.Unquote(value)
			if err != nil {
				return "", fmt.Errorf("invalid module import path: %w", err)
			}
		}
	}
	if block != "" {
		return "", fmt.Errorf("unterminated %s block", block)
	}
	if module == "" || path.IsAbs(module) || path.Clean(module) != module || module == "." || module == ".." || strings.HasPrefix(module, "../") || strings.ContainsAny(module, "\\ \t\r\n#@()") {
		return "", fmt.Errorf("missing or invalid module import path %q", module)
	}
	return module, nil
}
