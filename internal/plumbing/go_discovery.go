package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

type goAcceptancePackage struct {
	Dir, ImportPath, Name string

	TestGoFiles, XTestGoFiles []string

	Module *struct {
		Path, Dir string
		Main      bool
	}

	Error *struct{ Err string }
}

func (GoRepositoryAcceptanceAdapter) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, environment RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	fail := func(kind, reason string, err error) (RepositoryAcceptanceDiscovery, error) {
		if canceled := ctx.Err(); canceled != nil {
			kind, err = "setup", errors.Join(err, canceled)
		}
		if err != nil {
			reason += ": " + err.Error()
		}
		return RepositoryAcceptanceDiscovery{}, &RepositoryAcceptanceDiscoveryError{Kind: kind, Reason: reason, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return fail("setup", "Go discovery interrupted", err)
	}
	binding := in.Binding
	test := Test{Name: "Go discovery", Part: binding.Part, Framework: binding.Framework, File: binding.File, Selector: binding.Selector}
	selection, build, err := goAcceptanceCommand(part, test)
	if err != nil {
		return fail("refused", "invalid captured Go selection", err)
	}
	if binding.InvocationID == "" || !executionDigest.MatchString(binding.ExecutionDigest) || binding.SourceDigest != dependencyHash([]byte(in.Source)) || selection.Package != binding.Package || !slices.Equal(selection.Argv, in.Argv) {
		return fail("refused", "Go discovery binding, source or arguments differ from captured selection", nil)
	}
	reader, ok := environment.(RepositoryAcceptanceFileEnvironment)
	if !ok {
		return fail("setup", "prepared environment cannot read Go discovery sources", nil)
	}
	file, err := goAcceptanceFile(part, binding.File)
	if err != nil {
		return fail("refused", "invalid pinned Go file", err)
	}
	source, err := reader.ReadFile(ctx, file)
	if err != nil {
		return fail("refused", "cannot read pinned Go source", err)
	}
	if string(source) != in.Source {
		return fail("refused", "prepared Go source differs from the verbatim pinned bytes", nil)
	}
	if err := acceptanceDeclarations(binding.File, in.Source, []Test{test}); err != nil {
		return fail("refused", "invalid pinned Go testing declaration", err)
	}
	list := append([]string{"go", "list", "-e", "-json"}, build...)
	list = append(list, selection.Package)
	out, err := environment.Run(ctx, list)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fail("setup", "Go package discovery failed: "+strings.TrimSpace(string(out)), err)
	}
	var pkg goAcceptancePackage
	decoder := json.NewDecoder(bytes.NewReader(out))
	if err := decoder.Decode(&pkg); err != nil {
		return fail("setup", "invalid Go package discovery response", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fail("setup", "Go discovery must resolve exactly one package", err)
	}
	if pkg.Error != nil {
		return fail("setup", "Go package discovery: "+pkg.Error.Err, nil)
	}
	dir := path.Dir(file)
	if pkg.Module == nil || !pkg.Module.Main || pkg.Module.Path == "" || !path.IsAbs(pkg.Module.Dir) || pkg.Dir != path.Join(pkg.Module.Dir, dir) || pkg.Name == "" {
		return fail("setup", "Go discovery needs a package in the owning part's module", nil)
	}
	importPath := pkg.Module.Path
	if dir != "." {
		importPath += "/" + dir
	}
	if pkg.ImportPath != importPath || !nativeText(importPath) {
		return fail("setup", "Go discovery returned inconsistent package identity", nil)
	}
	files := append(slices.Clone(pkg.TestGoFiles), pkg.XTestGoFiles...)
	if !slices.Contains(files, path.Base(file)) {
		return fail("refused", "pinned Go file is excluded from the package's ordinary and external tests", nil)
	}
	top := strings.Split(binding.Selector, "/")[0]
	count := 0
	seen := map[string]bool{}
	for _, name := range files {
		if !goAcceptancePath(name) || path.Base(name) != name || !strings.HasSuffix(name, "_test.go") || seen[name] {
			return fail("refused", "Go discovery returned an unsafe or duplicate test source", nil)
		}
		seen[name] = true
		name = path.Join(dir, name)
		data, err := reader.ReadFile(ctx, name)
		if err != nil {
			return fail("refused", "cannot read included Go test source "+name, err)
		}
		if name == file && string(data) != in.Source {
			return fail("refused", "pinned Go source changed during discovery", nil)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.SkipObjectResolution)
		if err != nil {
			return fail("refused", "malformed included Go test source "+name, err)
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == top {
				count++
			}
		}
	}
	if count != 1 {
		return fail("refused", fmt.Sprintf("Go test %s has %d declarations across ordinary and external tests", top, count), nil)
	}
	// Compilation checks the actual package and testing types without starting
	// init, TestMain or any test. Compiler failures remain setup in this slice.
	compile := append([]string{"go", "test", "-c", "-o", "/dev/null"}, build...)
	compile = append(compile, selection.Package)
	out, err = environment.Run(ctx, compile)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fail("setup", "Go test compilation failed: "+strings.TrimSpace(string(out)), err)
	}
	binding.Package = pkg.ImportPath
	return RepositoryAcceptanceDiscovery{Binding: binding, TestName: top}, nil
}
