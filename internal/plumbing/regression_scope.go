package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// RegressionScope is captured from the original checkout, or CI's diff merge
// base. Neither candidate bytes nor a previous repair can grant permission.
// Readers must reject non-regular files, including linked ancestors.
type RegressionScope struct {
	base     []string
	existing map[string]bool
	packages map[string]bool
	config   repository.Config
	parts    map[string]*regressionDiscovery
	read     func(string) ([]byte, error)
}

func goRegressionScope(plan *Plan, base []string) *RegressionScope {
	s := &RegressionScope{base: slices.Clone(base), existing: map[string]bool{}, packages: map[string]bool{}, parts: map[string]*regressionDiscovery{}}
	for _, file := range base {
		s.existing[file] = true
	}
	for _, file := range append(slices.Clone(plan.Files), plan.TestFiles()...) {
		s.existing[file] = true
	}
	for _, dir := range plan.GoPackages(base) {
		s.packages[dir] = true
	}
	return s
}

// NewRegressionScope reads discovery declarations without executing repository
// policy. Unsupported declarations close only the implicit non-Go allowance;
// explicitly planned files still have their ordinary checks.
func NewRegressionScope(plan *Plan, base []string, read func(string) ([]byte, error)) (*RegressionScope, error) {
	s := goRegressionScope(plan, base)
	s.read = read
	if !slices.Contains(base, repository.Path) {
		return s, nil
	}
	data, err := read(repository.Path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.config); err != nil {
		return nil, fmt.Errorf("captured regression policy: %w", err)
	}
	selected := map[string]bool{}
	for _, file := range append(slices.Clone(plan.Files), plan.TestFiles()...) {
		if owner := s.owner(file); owner != nil {
			selected[owner.Name] = true
		}
		for _, part := range s.config.Parts {
			if file == repository.Path || slices.Contains(s.config.AgentRules, file) || slices.Contains(s.config.CurrentState, file) || slices.Contains(part.Inputs, file) || slices.Contains(part.Lockfiles, file) {
				selected[part.Name] = true
			}
		}
		for _, shared := range s.config.SharedPaths {
			if file == shared.Path || strings.HasSuffix(shared.Path, "/") && strings.HasPrefix(file, shared.Path) {
				for _, name := range shared.Parts {
					selected[name] = true
				}
			}
		}
	}
	for _, part := range s.config.Parts {
		if selected[part.Name] && (part.Language == "python" || part.Language == "typescript") {
			s.parts[part.Name] = captureRegressionDiscovery(part, base, read)
		}
	}
	return s, nil
}

func (s *RegressionScope) owner(file string) *repository.Part {
	var owner *repository.Part
	for i := range s.config.Parts {
		part := &s.config.Parts[i]
		if withinRegressionDir(file, part.Directory) && (owner == nil || owner.Directory == "." || len(part.Directory) > len(owner.Directory)) {
			owner = part
		}
	}
	return owner
}

func withinRegressionDir(file, dir string) bool {
	return dir == "." || file == dir || strings.HasPrefix(file, dir+"/")
}

// Files intersects novelty, ownership, conventional names and captured suite
// discovery. Callers additionally check the candidate's regular-file identity.
func (s *RegressionScope) Files(candidates []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, file := range candidates {
		if s.existing[file] || seen[file] || regressionPolicyFile(file) {
			continue
		}
		if c, err := clean(file); err != nil || c != file || strings.ContainsAny(file, "\\\x00") || strings.HasPrefix(file, "decisions/") || strings.HasPrefix(file, ".invariant/") {
			continue
		}
		allowed := strings.HasSuffix(file, "_test.go") && s.packages[path.Dir(file)]
		if part := s.owner(file); part != nil {
			if discovery := s.parts[part.Name]; discovery != nil {
				rel := file
				if part.Directory != "." {
					rel = strings.TrimPrefix(file, part.Directory+"/")
				}
				allowed = allowed || discovery.contains(rel)
			}
		}
		if allowed {
			seen[file] = true
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out
}

// CheckPolicy prevents even explicitly named policy edits from hiding implicit
// additions. A plan changing discovery can name its new tests explicitly.
func (s *RegressionScope) CheckPolicy(candidates []string, read func(string) ([]byte, error)) error {
	// A Go test file the build added can't run code as a test binary
	// starts. The sandbox judges the plan's acceptance tests and a
	// package's existing tests without the added files, but CI's own go
	// test runs every file together, where a TestMain or an init decides
	// how the others go (#869).
	for _, file := range s.Files(candidates) {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := read(file)
		if err != nil {
			return fmt.Errorf("%s: an added test file can't be read: %w", file, err)
		}
		if name := startsTestBinary(data); name != "" {
			return fmt.Errorf("%s declares %s: a test file the build adds can't run code as the test binary starts, since it would decide how the plan's acceptance tests and the package's own tests run. Put the setup inside the tests that need it", file, name)
		}
	}
	active := map[string]bool{}
	for _, file := range s.Files(candidates) {
		if owner := s.owner(file); owner != nil {
			active[owner.Name] = true
		}
	}
	if len(active) == 0 {
		return nil
	}
	names := map[string]bool{repository.Path: true}
	for _, part := range s.config.Parts {
		if !active[part.Name] {
			continue
		}
		for _, file := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
			names[file] = true
		}
		if d := s.parts[part.Name]; d != nil && d.config != "" {
			names[path.Join(part.Directory, d.config)] = true
		}
		for _, inventory := range [][]string{candidates, s.base} {
			for _, file := range inventory {
				// Ancestor configuration can also change automatic discovery.
				if regressionPolicyFile(file) && (withinRegressionDir(file, part.Directory) || withinRegressionDir(part.Directory, path.Dir(file))) {
					names[file] = true
				}
			}
		}
	}
	for _, file := range mapKeys(names) {
		before, beforeErr := s.read(file)
		after, afterErr := read(file)
		if beforeErr != nil && afterErr != nil && !slices.Contains(s.base, file) && !slices.Contains(candidates, file) {
			continue
		}
		if beforeErr != nil || afterErr != nil || !bytes.Equal(before, after) {
			return fmt.Errorf("%s: discovery policy must stay captured while adding implicit regressions", file)
		}
	}
	return nil
}

// startsTestBinary names the function in a Go test file that runs as its
// test binary starts, TestMain or init, or "" when it has none. A file that
// doesn't parse names none: the compiler reports it.
func startsTestBinary(source []byte) string {
	file, err := parser.ParseFile(token.NewFileSet(), "", source, parser.SkipObjectResolution)
	if err != nil {
		return ""
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && (fn.Name.Name == "TestMain" || fn.Name.Name == "init") {
			return fn.Name.Name
		}
	}
	return ""
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func regressionPolicyFile(file string) bool {
	name := path.Base(file)
	switch name {
	case "conftest.py", "__init__.py", "pytest.ini", ".pytest.ini", "pytest.toml", ".pytest.toml", "pyproject.toml", "setup.cfg", "tox.ini", "package.json":
		return true
	}
	return strings.HasPrefix(name, "vitest.config.") || strings.HasPrefix(name, "vitest.workspace.") || strings.HasPrefix(name, "vite.config.") || strings.HasPrefix(name, "jest.config.") || strings.HasPrefix(name, "tsconfig.") || strings.HasPrefix(name, "babel.config.") || strings.HasPrefix(name, ".babelrc") || name == ".env" || strings.HasPrefix(name, ".env.")
}

func checkoutRegressionScope(ctx context.Context, original string, plan *Plan) (*RegressionScope, error) {
	base, err := checkoutNames(ctx, original)
	if err != nil {
		return nil, err
	}
	return NewRegressionScope(plan, base, func(file string) ([]byte, error) { return regular.ReadFile(original, file) })
}
