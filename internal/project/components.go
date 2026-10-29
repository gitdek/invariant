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
// their functions. It is configuration, not evidence that a check ran.
type ComponentsConfig struct {
	Scope  []string         `json:"scope"`
	Checks []ComponentCheck `json:"checks,omitempty"`
}

// ComponentRef selects a declaration in a project-relative Go source file.
// Methods name their receiver: Type.Method or (*Type).Method.
type ComponentRef struct {
	Source string `json:"source"`
	Symbol string `json:"symbol"`
}

type ComponentCheck struct {
	Target      ComponentRef    `json:"target"`
	Proof       *ComponentProof `json:"proof,omitempty"`
	Tests       []ComponentRef  `json:"tests,omitempty"`
	Conformance []ComponentRef  `json:"conformance,omitempty"`
}

type ComponentProof struct {
	Contracts []ComponentRef `json:"contracts"`
}

// ComponentInventory records sources and resolved check intent. Neither the
// inventory nor any component carries assurance or verification evidence.
type ComponentInventory struct {
	Mode       string                   `json:"mode"`
	Scope      []string                 `json:"scope,omitempty"`
	Files      []ComponentFile          `json:"files,omitempty"`
	Components []Component              `json:"components,omitempty"`
	Checks     []ResolvedComponentCheck `json:"checks,omitempty"`
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
	Target      string                  `json:"target"`
	Proof       *ResolvedComponentProof `json:"proof,omitempty"`
	Tests       []string                `json:"tests,omitempty"`
	Conformance []string                `json:"conformance,omitempty"`
}

type ResolvedComponentProof struct {
	Contracts []string `json:"contracts"`
}

// ResolveComponents inventories exactly the declared directories, without
// build-tag filtering or recursion. Legacy manifests require no source reads.
func ResolveComponents(p *Project) (*ComponentInventory, error) {
	cfg := p.Manifest.Components
	if cfg == nil {
		return &ComponentInventory{Mode: "legacy"}, nil
	}
	if lang := p.Manifest.Language; lang != "" && lang != "go" {
		return nil, fmt.Errorf("components: language %q is not supported; component discovery is Go-only", lang)
	}
	if len(cfg.Scope) == 0 {
		return nil, fmt.Errorf("components: scope must name at least one Go package directory")
	}
	inv := &ComponentInventory{Mode: "explicit", Scope: append([]string(nil), cfg.Scope...)}
	sort.Strings(inv.Scope)
	r := componentResolver{dir: p.Dir, scope: map[string]bool{}, sources: map[string]componentSource{}, packages: map[string]string{}}
	for _, dir := range inv.Scope {
		if r.scope[dir] {
			return nil, fmt.Errorf("components: duplicate scope %q", dir)
		}
		r.scope[dir] = true
		full, err := componentPath(p.Dir, dir, true)
		if err != nil {
			return nil, fmt.Errorf("components scope %q: %w", dir, err)
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil, fmt.Errorf("components scope %q: %w", dir, err)
		}
		files := 0
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			src, err := r.readSource(path.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			inv.Files = append(inv.Files, src.file)
			inv.Components = append(inv.Components, src.components...)
			files++
		}
		if files == 0 {
			return nil, fmt.Errorf("components scope %q: no non-test Go source files", dir)
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
		if check.Proof == nil && len(check.Tests) == 0 && len(check.Conformance) == 0 {
			return nil, fmt.Errorf("components target %s: no proof, tests or conformance selected", target.ID)
		}
		resolved := ResolvedComponentCheck{Target: target.ID}
		if check.Proof != nil {
			if len(check.Proof.Contracts) == 0 {
				return nil, fmt.Errorf("components target %s: proof needs at least one contract", target.ID)
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
		inv.Checks = append(inv.Checks, resolved)
	}
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
	dir      string
	scope    map[string]bool
	sources  map[string]componentSource
	packages map[string]string
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
			return nil, fmt.Errorf("components: duplicate %s %s", kind, c.ID)
		}
		seen[c.ID] = true
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func (r *componentResolver) resolve(ref ComponentRef, kind string) (Component, error) {
	fail := func(reason string) (Component, error) {
		return Component{}, fmt.Errorf("components %s %q in %q: %s", kind, ref.Symbol, ref.Source, reason)
	}
	if ref.Symbol == "" {
		return fail("symbol is required")
	}
	if kind == "target" || kind == "contract" {
		if !r.scope[path.Dir(ref.Source)] || strings.HasSuffix(ref.Source, "_test.go") {
			return fail("must select non-test source inside component scope")
		}
	}
	if kind == "test" && !strings.HasSuffix(ref.Source, "_test.go") {
		return fail("must select a function in a Go _test.go file")
	}
	src, err := r.readSource(ref.Source)
	if err != nil {
		return fail(err.Error())
	}
	var found Component
	count := 0
	for _, c := range src.components {
		if c.Symbol == ref.Symbol {
			found = c
			count++
		}
	}
	if count != 1 {
		return fail(fmt.Sprintf("want exactly one declaration, found %d", count))
	}
	if kind == "contract" && found.ContractSHA256 == "" {
		return fail("declaration has no attached Gobra contract")
	}
	return found, nil
}

func (r *componentResolver) readSource(source string) (componentSource, error) {
	if src, ok := r.sources[source]; ok {
		return src, nil
	}
	var src componentSource
	full, err := componentPath(r.dir, source, false)
	if err != nil {
		return src, fmt.Errorf("components source %q: %w", source, err)
	}
	if !strings.HasSuffix(source, ".go") {
		return src, fmt.Errorf("components source %q: only Go source files are supported", source)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return src, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, source, b, parser.ParseComments)
	if err != nil {
		return src, fmt.Errorf("components source %q: %w", source, err)
	}
	pkg, err := r.packagePath(path.Dir(source))
	if err != nil {
		return src, fmt.Errorf("components source %q: %w", source, err)
	}
	src.file = ComponentFile{Path: source, SHA256: componentHash(b)}
	initializers := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		symbol := fn.Name.Name
		if fn.Recv != nil {
			if len(fn.Recv.List) != 1 {
				return src, fmt.Errorf("components source %q: invalid receiver for %s", source, symbol)
			}
			receiver := componentReceiver(fn.Recv.List[0].Type)
			if receiver == "" {
				return src, fmt.Errorf("components source %q: unsupported receiver for %s", source, symbol)
			}
			symbol = receiver + "." + symbol
		} else if symbol == "init" {
			initializers++
			symbol = fmt.Sprintf("init[%d]", initializers)
		}
		src.components = append(src.components, Component{
			ID:       "go:" + pkg + "#" + symbol + "@" + source,
			Language: "go", Package: pkg, Symbol: symbol, Source: source,
			SourceSHA256: src.file.SHA256, ContractSHA256: componentContractHash(b, fset, fn.Doc),
		})
	}
	r.sources[source] = src
	return src, nil
}

func componentReceiver(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		if name := componentReceiver(e.X); name != "" {
			return "(*" + name + ")"
		}
	case *ast.IndexExpr:
		return componentReceiver(e.X)
	case *ast.IndexListExpr:
		return componentReceiver(e.X)
	case *ast.ParenExpr:
		return componentReceiver(e.X)
	}
	return ""
}

func componentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func componentContractHash(src []byte, fset *token.FileSet, doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	annotated := false
	for _, c := range doc.List {
		if strings.HasPrefix(c.Text, "//@") || strings.HasPrefix(c.Text, "// @") {
			annotated = true
		}
	}
	if !annotated {
		return ""
	}
	start := fset.Position(doc.Pos()).Offset
	last := doc.List[len(doc.List)-1]
	end := fset.Position(last.Pos()).Offset
	// Read the final comment's end from the bytes: the parser strips CRs
	// from comments, so its End position need not include all their bytes.
	if strings.HasPrefix(last.Text, "//") {
		if newline := bytes.IndexByte(src[end:], '\n'); newline >= 0 {
			end += newline + 1
		} else {
			end = len(src)
		}
	} else {
		end += bytes.Index(src[end:], []byte("*/")) + 2
		for i := end; i < len(src); i++ {
			if src[i] == '\n' {
				end = i + 1
				break
			}
			if src[i] != ' ' && src[i] != '\t' && src[i] != '\r' {
				break
			}
		}
	}
	return componentHash(src[start:end])
}

// componentPath rejects aliases as well as escapes, including a link in an
// intermediate directory. The project root itself is the caller's boundary.
func componentPath(root, name string, directory bool) (string, error) {
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || filepath.VolumeName(name) != "" ||
		name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("%q must be a clean project-relative path", name)
	}
	full := root
	parts := strings.Split(name, "/")
	for i, part := range parts {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%q must not contain a symbolic link", name)
		}
		if i < len(parts)-1 || directory {
			if !info.IsDir() {
				return "", fmt.Errorf("%q must name a directory", name)
			}
		} else if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%q must name a regular file", name)
		}
	}
	return full, nil
}

// packagePath uses the nearest module inside the project, including nested
// modules. Package clauses and checkout locations never enter an identity.
func (r *componentResolver) packagePath(dir string) (string, error) {
	if pkg, ok := r.packages[dir]; ok {
		return pkg, nil
	}
	for parent := dir; ; parent = path.Dir(parent) {
		name := path.Join(parent, "go.mod")
		full, err := componentPath(r.dir, name, false)
		if err == nil {
			b, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			module, err := componentModulePath(b)
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			rel, err := filepath.Rel(filepath.FromSlash(parent), filepath.FromSlash(dir))
			if err != nil {
				return "", err
			}
			pkg := module
			if rel != "." {
				pkg += "/" + filepath.ToSlash(rel)
			}
			r.packages[dir] = pkg
			return pkg, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent == "." {
			return "", fmt.Errorf("no go.mod between %q and the project root", dir)
		}
	}
}

func componentModulePath(src []byte) (string, error) {
	module, block := "", ""
	declared := false
	for _, line := range strings.Split(string(src), "\n") {
		fields, err := componentModuleFields(line)
		if err != nil {
			return "", err
		}
		if len(fields) == 0 {
			continue
		}
		if block != "" {
			if len(fields) == 1 && fields[0] == ")" {
				block = ""
				continue
			}
			if block != "module" {
				continue
			}
		} else {
			verb := fields[0]
			fields = fields[1:]
			if verb == "module" {
				if declared {
					return "", fmt.Errorf("repeated module declaration")
				}
				declared = true
			}
			if len(fields) == 1 && fields[0] == "(" {
				block = verb
				continue
			}
			if verb != "module" {
				continue
			}
		}
		if module != "" || len(fields) != 1 {
			return "", fmt.Errorf("want exactly one module path")
		}
		module = fields[0]
		if strings.HasPrefix(module, `"`) || strings.HasPrefix(module, "`") {
			var err error
			module, err = strconv.Unquote(module)
			if err != nil {
				return "", fmt.Errorf("invalid module path: %w", err)
			}
		}
		if module == "" || module == "." || module == ".." || path.IsAbs(module) || path.Clean(module) != module ||
			strings.HasPrefix(module, "../") || strings.ContainsAny(module, "\\ \t\r\n\"`#@()") {
			return "", fmt.Errorf("invalid module path %q", module)
		}
	}
	if block != "" {
		return "", fmt.Errorf("unclosed %s block", block)
	}
	if module == "" {
		return "", fmt.Errorf("missing module path")
	}
	return module, nil
}

// Only the module declaration supplies identity. Split go.mod lines without
// treating comment markers inside quoted strings as comments, and recognize
// blocks so a dependency named module cannot become the package's module.
func componentModuleFields(line string) ([]string, error) {
	var fields []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if strings.HasPrefix(line, "//") {
			break
		}
		n := 1
		switch line[0] {
		case '"', '`':
			quoted, err := strconv.QuotedPrefix(line)
			if err != nil {
				return nil, fmt.Errorf("invalid go.mod string: %w", err)
			}
			n = len(quoted)
		case '(', ')':
		default:
			for n < len(line) && !strings.ContainsRune(" \t\r()\"`", rune(line[n])) && !strings.HasPrefix(line[n:], "//") {
				n++
			}
		}
		fields = append(fields, line[:n])
		line = line[n:]
	}
	return fields, nil
}
