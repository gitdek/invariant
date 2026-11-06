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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// Execution binds native acceptance to captured policy. Base is provenance;
// only Digest participates in the plan's hash.
type Execution struct {
	Base   string `json:"base,omitempty"`
	Digest string `json:"digest"`
}

// UnmarshalJSON distinguishes absent metadata from an attempted native record.
// Empty, null, partial and misspelled fields must never become legacy tests.
func (t *Test) UnmarshalJSON(data []byte) error {
	var out Test
	fields := map[string]*string{"name": &out.Name, "file": &out.File, "says": &out.Says,
		"part": &out.Part, "framework": &out.Framework, "selector": &out.Selector}
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("acceptance test must be an object")
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || fields[name] == nil || seen[name] {
			return fmt.Errorf("unknown or duplicate acceptance field %q", key)
		}
		seen[name] = true
		var value *string
		if err := dec.Decode(&value); err != nil {
			return err
		}
		if value == nil {
			return fmt.Errorf("acceptance field %s must not be null", name)
		}
		*fields[name] = *value
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing acceptance JSON")
	}
	if seen["part"] || seen["framework"] || seen["selector"] {
		if strings.TrimSpace(out.Part) == "" || strings.TrimSpace(out.Framework) == "" || strings.TrimSpace(out.Selector) == "" {
			return errors.New("native acceptance requires part, framework and selector")
		}
	}
	*t = out
	return nil
}

// Native reports explicit metadata; no fields are inferred for legacy Go.
func (t Test) Native() bool { return t.Part != "" || t.Framework != "" || t.Selector != "" }

func (p *Plan) Native() bool {
	if p != nil {
		for _, t := range p.Tests {
			if t.Native() {
				return true
			}
		}
	}
	return false
}

func (t Test) identity() string {
	b, _ := json.Marshal([]string{t.Part, t.Framework, t.File, t.Selector})
	return string(b)
}

// Identity is the complete selection, suitable for diagnostics and prompts.
func (t Test) Identity() string {
	if !t.Native() {
		return t.File + ":" + t.Name
	}
	return fmt.Sprintf("part=%q framework=%q file=%q selector=%q", t.Part, t.Framework, t.File, t.Selector)
}

// SourceLanguage is a presentation hint, never an execution choice.
func (p *Plan) SourceLanguage(file string) string {
	for _, t := range p.Tests {
		if t.File == file {
			switch t.Framework {
			case "pytest":
				return "python"
			case "vitest", "jest":
				switch path.Ext(file) {
				case ".js", ".jsx", ".mjs", ".cjs":
					return "javascript"
				}
				return "typescript"
			}
		}
	}
	return "go"
}

// nativeText is text a person can read as ratified: no control, format,
// private-use or line-separating character can hide or reorder what a
// proposal shows.
func nativeText(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.In(r, unicode.C, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

func (t Test) validateNative() error {
	if !nativeText(t.Part) || !nativeText(t.Selector) || !nativeText(t.Name) {
		return errors.New("native acceptance needs a label, part and exact selector")
	}
	if c, err := clean(t.File); err != nil || c != t.File || strings.ContainsAny(t.File, `\*?[]:`) || !nativeText(t.File) {
		return fmt.Errorf("native acceptance file %q must be a clean repository path", t.File)
	}
	valid := false
	switch t.Framework {
	case "go":
		valid = strings.HasSuffix(t.File, "_test.go") && testName.MatchString(strings.Split(t.Selector, "/")[0])
		for _, segment := range strings.Split(t.Selector, "/") {
			valid = valid && strings.TrimSpace(segment) != ""
		}
	case "pytest":
		base := path.Base(t.File)
		valid = strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py"))
		_, node, found := strings.Cut(t.Selector, "::")
		// The final node must select a function or a concrete parameter case,
		// not a file or class. Discovery belongs to the execution adapter.
		declaration, _, _ := strings.Cut(node, "[")
		segments := strings.Split(declaration, "::")
		valid = valid && found && strings.HasPrefix(segments[len(segments)-1], "test")
		for _, segment := range segments {
			valid = valid && segment != ""
		}
	case "vitest", "jest":
		ext := path.Ext(t.File)
		switch ext {
		case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
			stem := strings.TrimSuffix(t.File, ext)
			valid = strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || strings.Contains("/"+t.File, "/__tests__/")
		}
	default:
		return fmt.Errorf("unsupported acceptance framework %q", t.Framework)
	}
	if !valid {
		return fmt.Errorf("invalid native acceptance file or selector: %s", t.Identity())
	}
	return nil
}

func acceptanceDeclarations(file, src string, tests []Test) error {
	var selected []Test
	native := false
	for _, t := range tests {
		if t.File != file {
			continue
		}
		if t.Native() {
			if !utf8.ValidString(src) {
				return fmt.Errorf("native acceptance file %s must be UTF-8", file)
			}
			if t.Framework != "go" {
				return nil
			}
			native = true
			t.Name = strings.Split(t.Selector, "/")[0]
		}
		selected = append(selected, t)
	}
	if !native {
		return declares(file, src, selected)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("acceptance file %s: %w", file, err)
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
	have := map[string]int{}
	valid := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		have[fn.Name.Name]++
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) > 1 || fn.Type.Results != nil || fn.Type.TypeParams != nil {
			continue
		}
		if star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "T" {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == testingName {
					valid[fn.Name.Name] = true
				}
			} else if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "T" && testingName == "." {
				valid[fn.Name.Name] = true
			}
		}
	}
	for _, t := range selected {
		if have[t.Name] != 1 || !valid[t.Name] {
			return fmt.Errorf("acceptance file %s must declare exactly one func %s(t *testing.T)", file, t.Name)
		}
	}
	return nil
}

var executionDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var executionBase = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (p *Plan) validateExecution(required bool) error {
	if !p.Native() {
		if p.Execution != nil {
			return errors.New("legacy plans cannot supply native execution metadata")
		}
		return nil
	}
	if p.Execution == nil {
		if required {
			return errors.New("native acceptance needs captured execution authority")
		}
		return nil
	}
	if !executionBase.MatchString(p.Execution.Base) || !executionDigest.MatchString(p.Execution.Digest) {
		return errors.New("invalid captured execution base or digest")
	}
	return nil
}

// ReadRepositoryDraft binds a structurally valid draft to the caller's captured
// policy. Neither plan.json nor a candidate repository supplies that policy.
func ReadRepositoryDraft(ctx context.Context, ws string, snapshot *repository.Snapshot) (*Plan, error) {
	p, err := ReadDraft(ws)
	if err != nil || !p.Native() {
		return p, err
	}
	digest, err := p.repositoryDigest(ctx, snapshot)
	if err != nil {
		return p, err
	}
	for _, file := range p.TestFiles() {
		if _, err := snapshot.ReadFileContext(ctx, file); err == nil {
			return p, fmt.Errorf("acceptance file %s already exists in the captured base", file)
		} else if !errors.Is(err, repository.ErrFileMissing) {
			return p, err
		}
	}
	p.Execution = &Execution{Base: snapshot.Commit(), Digest: digest}
	p.Trusted = nil
	for file := range p.Touches(0) {
		trusted, err := snapshot.IsTrusted(file)
		if err != nil {
			return p, err
		}
		if trusted {
			p.Trusted = append(p.Trusted, file)
		}
	}
	sort.Strings(p.Trusted)
	return p, nil
}

// ValidateRepository verifies content authority without changing provenance.
// Legacy records remain usable without a contract or inferred metadata.
func (p *Plan) ValidateRepository(ctx context.Context, snapshot *repository.Snapshot) error {
	if err := p.validateExecution(true); err != nil {
		return err
	}
	if !p.Native() {
		return nil
	}
	digest, err := p.repositoryDigest(ctx, snapshot)
	if err != nil {
		return err
	}
	if digest != p.Execution.Digest {
		return errors.New("the captured repository execution contract differs from the plan's")
	}
	return nil
}

func (p *Plan) repositoryDigest(ctx context.Context, snapshot *repository.Snapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if snapshot == nil {
		return "", errors.New("native acceptance needs the caller's captured repository snapshot")
	}
	config := snapshot.Config()
	paths := append(append([]string(nil), p.Files...), p.TestFiles()...)
	if _, err := snapshot.PartsFor(paths); err != nil {
		return "", err
	}
	var identities []string
	owners := map[string]bool{}
	for _, t := range p.Tests {
		if !t.Native() {
			continue
		}
		if err := t.validateNative(); err != nil {
			return "", err
		}
		owner, ok, err := snapshot.DirectoryOwner(t.File)
		if err != nil {
			return "", err
		}
		if !ok || owner.Name != t.Part {
			return "", fmt.Errorf("acceptance file %s must execute in its deepest owning part %q", t.File, t.Part)
		}
		owners[owner.Name] = true
		language := map[string]string{"go": "go", "pytest": "python", "vitest": "typescript", "jest": "typescript"}[t.Framework]
		if owner.Language != language || len(owner.Commands["test_one"].Argv) == 0 || owner.Commands["test_one"].NotApplicable != "" {
			return "", fmt.Errorf("part %q needs language %s and an applicable test_one for %s", owner.Name, language, t.Identity())
		}
		if t.Framework == "pytest" {
			rel := t.File
			if owner.Directory != "." {
				rel = strings.TrimPrefix(rel, owner.Directory+"/")
			}
			file, _, _ := strings.Cut(t.Selector, "::")
			if file != rel {
				return "", fmt.Errorf("pytest selector must start with part-relative file %s::", rel)
			}
		}
		identities = append(identities, t.identity())
	}
	// Canonicalize set-like declarations, keeping argv order and exact bytes.
	// The whole configuration is policy. Of the declared bytes, only the
	// lockfiles and inputs of parts running native tests can change what a
	// selector means, so the repository's rules, documentation and other
	// parts' dependencies never invalidate a ratified identity.
	sort.Slice(config.Parts, func(i, j int) bool { return config.Parts[i].Name < config.Parts[j].Name })
	inputNames := map[string]bool{}
	for _, names := range [][]string{config.AgentRules, config.CurrentState, config.TrustedPaths} {
		sort.Strings(names)
	}
	for _, part := range config.Parts {
		sort.Strings(part.Lockfiles)
		sort.Strings(part.Inputs)
		if !owners[part.Name] {
			continue
		}
		for _, name := range append(append([]string(nil), part.Lockfiles...), part.Inputs...) {
			inputNames[name] = true
		}
	}
	for _, shared := range config.SharedPaths {
		sort.Strings(shared.Parts)
	}
	sort.Slice(config.SharedPaths, func(i, j int) bool { return config.SharedPaths[i].Path < config.SharedPaths[j].Path })
	inputs := map[string][]byte{}
	for name := range inputNames {
		data, err := snapshot.ReadFileContext(ctx, name)
		if err != nil {
			return "", err
		}
		inputs[name] = data
	}
	sort.Strings(paths)
	sort.Strings(identities)
	data, err := json.Marshal(struct {
		Version int
		Config  repository.Config
		Inputs  map[string][]byte
		Paths   []string
		Tests   []string
	}{1, config, inputs, paths, identities})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return dependencyHash(data), nil
}

func nativeSourceEntries(ws string, sources map[string]string) error {
	return filepath.WalkDir(filepath.Join(ws, "tests"), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(ws, "tests"), name)
		if err != nil {
			return err
		}
		if _, ok := sources[filepath.ToSlash(rel)]; !ok || !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected acceptance source entry %s", rel)
		}
		return nil
	})
}

// writeNativeFile refuses existing different bytes and links, including on
// ancestors. Exclusive creation never overwrites a ratified source or follows
// a substituted leaf. The root confines writes even if an ancestor changes.
func writeNativeFile(dir, file, text string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for parent := path.Dir(file); parent != "."; parent = path.Dir(parent) {
		info, err := root.Lstat(filepath.FromSlash(parent))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("native plan path %s requires a regular directory at %s", file, parent)
		}
	}
	if err := root.MkdirAll(filepath.FromSlash(path.Dir(file)), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(filepath.FromSlash(file), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		data, err := regular.ReadFile(dir, file)
		if err != nil {
			return err
		}
		if string(data) != text {
			return fmt.Errorf("native plan file %s already exists with different bytes", file)
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, text)
	return errors.Join(err, f.Close())
}
