package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

func (a GoRepositoryAcceptanceAdapter) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, environment RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	fail := func(kind, reason string, err error) (RepositoryAcceptanceDiscovery, error) {
		if ctx.Err() != nil {
			kind = "setup"
			err = errors.Join(err, ctx.Err())
		}
		if err != nil {
			reason += ": " + err.Error()
		}
		return RepositoryAcceptanceDiscovery{}, &RepositoryAcceptanceDiscoveryError{Kind: kind, Reason: reason, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return fail("setup", "Go discovery was interrupted", err)
	}
	binding := in.Binding
	test := Test{Name: "Go acceptance", Part: binding.Part, Framework: binding.Framework, File: binding.File, Selector: binding.Selector}
	selection, err := a.Select(ctx, part, test)
	if err != nil {
		return fail("refused", "Go discovery cannot validate the captured selection", err)
	}
	if binding.InvocationID == "" || !executionDigest.MatchString(binding.ExecutionDigest) || binding.SourceDigest != dependencyHash([]byte(in.Source)) || binding.Package != selection.Package || !slices.Equal(in.Argv, selection.Argv) {
		return fail("refused", "Go discovery binding or arguments differ from the captured invocation", nil)
	}
	reader, ok := environment.(RepositoryAcceptanceFileEnvironment)
	if !ok {
		return fail("setup", "prepared environment cannot read acceptance sources", nil)
	}
	file, err := goAcceptanceFile(part, binding.File)
	if err != nil {
		return fail("refused", "invalid Go acceptance source path", err)
	}
	source, err := reader.ReadFile(ctx, file)
	if err != nil {
		return fail("refused", "cannot read pinned Go source", err)
	}
	if string(source) != in.Source {
		return fail("refused", "prepared Go source differs from the verbatim pinned bytes", nil)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), file, source, 0); err != nil {
		return fail("refused", "malformed pinned Go source", err)
	}
	// Only build policy belongs to package loading. Compilation retains all the
	// selected policy, but -c prevents initialization, TestMain and tests running.
	argv := []string{"go", "list", "-json"}
	argv = append(argv, goDiscoveryBuildFlags(in.Argv)...)
	argv = append(argv, selection.Package)
	out, err := environment.Run(ctx, argv)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return fail("setup", "Go package discovery failed: "+strings.TrimSpace(string(out)), err)
	}
	var pkg struct {
		Dir, ImportPath, Name     string
		TestGoFiles, XTestGoFiles []string
		Incomplete                bool
		Error                     *struct{ Err string }
		Module                    *struct {
			Path string
			Main bool
		}
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		return fail("setup", "Go package discovery returned invalid package metadata", err)
	}
	if pkg.Incomplete || pkg.Error != nil || !path.IsAbs(pkg.Dir) || !nativeText(pkg.ImportPath) || pkg.Name == "" || pkg.Module == nil || !pkg.Module.Main || pkg.Module.Path == "" {
		return fail("setup", "Go package discovery did not identify one package in the owning module", nil)
	}
	name := strings.Split(binding.Selector, "/")[0]
	found, matches := false, 0
	seen := map[string]bool{}
	for external, files := range [][]string{pkg.TestGoFiles, pkg.XTestGoFiles} {
		packageName := pkg.Name
		if external == 1 {
			packageName += "_test"
		}
		for _, base := range files {
			if !repositoryAcceptancePath(base) || path.Base(base) != base || !strings.HasSuffix(base, "_test.go") || seen[base] {
				return fail("setup", "Go package discovery returned invalid or duplicate test files", nil)
			}
			seen[base] = true
			rel := path.Join(path.Dir(file), base)
			data, err := reader.ReadFile(ctx, rel)
			if err != nil {
				return fail("refused", "cannot read included Go test source "+rel, err)
			}
			if rel == file && string(data) != in.Source {
				return fail("refused", "included Go source differs from the pinned bytes", nil)
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), rel, data, 0)
			if err != nil {
				return fail("refused", "malformed included Go test source "+rel, err)
			}
			if parsed.Name.Name != packageName {
				return fail("refused", "Go test source has a different package declaration", nil)
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name.Name != name {
					continue
				}
				matches++
				if !goTestingDeclaration(parsed, fn) {
					return fail("refused", "selected Go declaration must be func "+name+"(*testing.T) with no type parameters or results", nil)
				}
				found = found || rel == file
			}
		}
	}
	if !found || matches != 1 {
		return fail("refused", "pinned file must contain the only included top-level declaration of "+name+" across ordinary and external tests", nil)
	}
	argv = append([]string{"go", "test", "-c", "-o", "/dev/null"}, in.Argv[2:]...)
	out, err = environment.Run(ctx, argv)
	binding.Package = pkg.ImportPath
	discovery := RepositoryAcceptanceDiscovery{Binding: binding, TestName: name}
	if err := errors.Join(err, ctx.Err()); err != nil {
		// Direct discovery still returns no selection on failure. Only the
		// configured process lifecycle can investigate this completed attempt.
		if ctx.Err() == nil && repositoryCommandFailed(err) {
			err = &goCompilationFailure{discovery: discovery, err: err}
		}
		return fail("setup", "Go test compilation did not complete: "+strings.TrimSpace(string(out)), err)
	}
	return discovery, nil
}

// This only consumes arguments that Select has validated, preserving spellings.
func goDiscoveryBuildFlags(argv []string) []string {
	var flags []string
	for i := 2; i < len(argv); i++ {
		flag, _, equal := strings.Cut(argv[i], "=")
		switch flag {
		case "-tags", "-p", "-mod", "-race", "-trimpath":
			flags = append(flags, argv[i])
			if !equal && flag != "-race" && flag != "-trimpath" {
				i++
				flags = append(flags, argv[i])
			}
		}
	}
	return flags
}

func goTestingDeclaration(file *ast.File, fn *ast.FuncDecl) bool {
	if fn.Type.TypeParams.NumFields() != 0 || fn.Type.Results.NumFields() != 0 || fn.Type.Params.NumFields() != 1 || fn.Body == nil {
		return false
	}
	ptr, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil || importPath != "testing" {
			continue
		}
		alias := "testing"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "." {
			id, ok := ptr.X.(*ast.Ident)
			return ok && id.Name == "T"
		}
		if typ, ok := ptr.X.(*ast.SelectorExpr); ok && typ.Sel.Name == "T" {
			id, ok := typ.X.(*ast.Ident)
			return ok && alias != "_" && id.Name == alias
		}
	}
	return false
}
