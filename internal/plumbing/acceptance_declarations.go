package plumbing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type goDeclarationPolicy int

const (
	goDeclarationLegacy goDeclarationPolicy = iota
	goDeclarationNative
)

type goDeclaration struct {
	count          int
	legacy, native bool
}

type goDeclarations struct {
	file  string
	names map[string]*goDeclaration
}

// analyzeGoDeclarations records both structural policies in one walk. Legacy
// accepts any pointer to a selector named T, without resolving its qualifier or
// rejecting results, type parameters, grouped parameters or duplicate names.
// Native requires the testing import, a strict signature and a unique function.
func analyzeGoDeclarations(file, src string) (*goDeclarations, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	testingName := ""
	for _, imp := range f.Imports {
		if imp.Path.Value == `"testing"` {
			testingName = "testing"
			if imp.Name != nil {
				testingName = imp.Name.Name
			}
		}
	}
	a := &goDeclarations{file: file, names: map[string]*goDeclaration{}}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		declaration := a.names[fn.Name.Name]
		if declaration == nil {
			declaration = &goDeclaration{}
			a.names[fn.Name.Name] = declaration
		}
		declaration.count++
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
			continue
		}
		star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		native := false
		if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "T" {
			declaration.legacy = true
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == testingName {
				native = true
			}
		} else if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "T" && testingName == "." {
			native = true
		}
		if native && len(fn.Type.Params.List[0].Names) <= 1 && fn.Type.Results == nil && fn.Type.TypeParams == nil {
			declaration.native = true
		}
	}
	return a, nil
}

func (a *goDeclarations) require(name string, policy goDeclarationPolicy) error {
	d := a.names[name]
	switch policy {
	case goDeclarationLegacy:
		if d == nil || !d.legacy {
			return fmt.Errorf("acceptance test file %s doesn't declare func %s(t *testing.T)", a.file, name)
		}
	case goDeclarationNative:
		if d == nil || d.count != 1 || !d.native {
			return fmt.Errorf("acceptance file %s must declare exactly one func %s(t *testing.T)", a.file, name)
		}
	default:
		return fmt.Errorf("unknown Go declaration policy %d", policy)
	}
	return nil
}

// declares retains the legacy policy and diagnostics for existing plans.
func declares(file, src string, tests []Test) error {
	a, err := analyzeGoDeclarations(file, src)
	if err != nil {
		return fmt.Errorf("acceptance test file %s doesn't parse: %v", file, err)
	}
	for _, t := range tests {
		if t.File == file {
			if err := a.require(t.Name, goDeclarationLegacy); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeGoDeclarations(file, src string, tests []Test) error {
	a, err := analyzeGoDeclarations(file, src)
	if err != nil {
		return fmt.Errorf("acceptance file %s: %w", file, err)
	}
	for _, t := range tests {
		if t.File != file {
			continue
		}
		name := t.Name
		if t.Native() {
			name = strings.Split(t.Selector, "/")[0]
		}
		if err := a.require(name, goDeclarationNative); err != nil {
			return err
		}
	}
	return nil
}

func acceptanceDeclarations(file, src string, tests []Test) error {
	for _, t := range tests {
		if t.File == file && t.Native() {
			definition, ok := lookupAcceptanceFramework(t.Framework)
			if !ok {
				return fmt.Errorf("unsupported acceptance framework %q", t.Framework)
			}
			return definition.checkDeclarations(file, src, tests)
		}
	}
	return declares(file, src, tests)
}
