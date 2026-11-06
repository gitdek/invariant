// Package plumbing is how the factory takes a change that isn't a state
// machine (D-0105). Its proposal is a plan: what changes, the files the
// change may touch, and the acceptance tests that show it works. People
// ratify the plan by its hash, as they ratify statements, and the tests are
// pinned by it: the build must make them pass without changing them. The
// code is tested, not proved, and a person merges anything in the trusted
// base.
package plumbing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
)

// Plan is a plumbing change's proposal.
type Plan struct {
	Name    string   `json:"name"`    // the change, in a few plain words
	Summary string   `json:"summary"` // what changes and why, in plain words
	Files   []string `json:"files"`   // named writable files; captured conventions may also admit new regression tests
	Tests   []Test   `json:"tests"`   // the acceptance tests
	// Sources is each acceptance test file's text, by path. The tests are
	// new files the ratification adds, which the build can't change.
	Sources map[string]string `json:"sources"`
	// Trusted is the files the change touches in the trusted base, which
	// only a person merges. The factory sets it; the hash leaves it out.
	Trusted []string `json:"trusted,omitempty"`
	// Execution is supplied by the trusted caller, never by a draft.
	Execution *Execution `json:"execution,omitempty"`
}

// Test is one acceptance test. Without native metadata Name is a Go function;
// with it Name is a display label and Selector identifies the exact test.
type Test struct {
	Name string `json:"name"` // the legacy function name, or a native display label
	File string `json:"file"` // its file, from the repository's root
	Says string `json:"says"` // what it shows, in plain words

	Part      string `json:"part,omitempty"`
	Framework string `json:"framework,omitempty"`
	Selector  string `json:"selector,omitempty"`
}

// TrustedBase is what only a person merges (D-0105): the gate and the
// verifiers, the scope and ratification checks, CI and its decision check,
// the factory's own code, its proved rules and its CLI, the dependencies,
// the page that posts the owner's commands, the rules agents work by, and
// the configuration that starts their tools in a session (D-0132).
var TrustedBase = []string{
	".github/", "cmd/", "factory/", "go.mod", "go.sum", "AGENTS.md", "CLAUDE.md",
	".claude/", ".codex/", ".mcp.json", ".invariant/repository.json",
	"internal/conformance/", "internal/decisions/", "internal/factory/", "internal/formalize/",
	"internal/github/", "internal/gobra/", "internal/mcp/", "internal/plumbing/", "internal/project/",
	"internal/receipt/", "internal/regular/", "internal/repository/", "internal/scope/", "internal/setup/", "internal/synth/", "internal/tla/",
	"internal/tlc/", "internal/toolchain/", "internal/verify/",
	"internal/dashboard/act.go", "internal/dashboard/http.go",
}

// Trusted says whether a file is in the trusted base.
func Trusted(file string) bool {
	for _, t := range TrustedBase {
		if file == t || (strings.HasSuffix(t, "/") && strings.HasPrefix(file, t)) {
			return true
		}
	}
	return false
}

// LockDir is where a ratified plan is recorded in the repository, one file
// per issue.
const LockDir = ".invariant/plans"

// LockPath is where issue n's ratified plan is recorded.
func LockPath(n int) string { return fmt.Sprintf("%s/issue-%d.json", LockDir, n) }

var (
	testName = regexp.MustCompile(`^Test[A-Z0-9_][A-Za-z0-9_]*$`)
	lockFile = regexp.MustCompile(`^\.invariant/plans/issue-([0-9]+)\.json$`)
)

// LockIssue is the issue a path records the plan of, or 0 if it isn't a
// plan's record.
func LockIssue(p string) int {
	m := lockFile.FindStringSubmatch(p)
	if m == nil {
		return 0
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	return n
}

// Hash identifies the plan: its summary, files, tests and their text. A
// person's ratification names it.
func (p *Plan) Hash() string {
	c := Plan{Summary: p.Summary, Files: append([]string(nil), p.Files...), Tests: append([]Test(nil), p.Tests...), Sources: p.Sources}
	if p.Execution != nil {
		c.Execution = &Execution{Digest: p.Execution.Digest}
	}
	sort.Strings(c.Files)
	sort.Slice(c.Tests, func(i, j int) bool {
		if c.Tests[i].File != c.Tests[j].File {
			return c.Tests[i].File < c.Tests[j].File
		}
		if c.Tests[i].Part != c.Tests[j].Part {
			return c.Tests[i].Part < c.Tests[j].Part
		}
		if c.Tests[i].Framework != c.Tests[j].Framework {
			return c.Tests[i].Framework < c.Tests[j].Framework
		}
		if c.Tests[i].Selector != c.Tests[j].Selector {
			return c.Tests[i].Selector < c.Tests[j].Selector
		}
		return c.Tests[i].Name < c.Tests[j].Name
	})
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestFiles is every acceptance test file, in order.
func (p *Plan) TestFiles() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range p.Tests {
		if !seen[t.File] {
			seen[t.File] = true
			out = append(out, t.File)
		}
	}
	sort.Strings(out)
	return out
}

// Touches is every file the change may add or change: the files the build
// writes, the tests the ratification adds, and the plan's record.
func (p *Plan) Touches(n int) map[string]bool {
	out := map[string]bool{LockPath(n): true}
	for _, f := range p.Files {
		out[f] = true
	}
	for _, f := range p.TestFiles() {
		out[f] = true
	}
	return out
}

// GoPackages finds the packages the plan touches using the original
// checkout's Go files and the Go files the plan names. An embedded asset
// belongs to its nearest enclosing package; new unnamed files cannot
// introduce another package into the plan's scope.
func (p *Plan) GoPackages(baseFiles []string) []string {
	return p.packagesIn(append(append([]string(nil), baseFiles...), p.goTouches()...))
}

// goTouches is what the plan's Go packages come from: its files and its Go
// acceptance tests. A Python or JavaScript acceptance test is never an
// embedded asset, so it can't widen the Go regression allowance.
func (p *Plan) goTouches() []string {
	out := append([]string(nil), p.Files...)
	for _, t := range p.Tests {
		if t.Framework == "" || t.Framework == "go" {
			out = append(out, t.File)
		}
	}
	return out
}

// packagesIn finds the packages the plan touches with files as the whole
// inventory: each file the plan names counts for the nearest directory
// where files holds Go files. Given a candidate's files, a package the
// plan deletes is no package at all (#298).
func (p *Plan) packagesIn(files []string) []string {
	have := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			have[path.Dir(f)] = true
		}
	}
	seen := map[string]bool{}
	for _, f := range p.goTouches() {
		for dir := path.Dir(f); ; dir = path.Dir(dir) {
			if have[dir] {
				seen[dir] = true
				break
			}
			if dir == "." || dir == "/" {
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// RegressionFiles retains the unconfigured Go allowance. Configured callers
// use NewRegressionScope with the same original inventory and a policy reader.
func (p *Plan) RegressionFiles(baseFiles, candidates []string) []string {
	return goRegressionScope(p, baseFiles).Files(candidates)
}

// clean checks a path from the repository's root: relative, inside it, and
// not somewhere a plan can't reach.
func clean(p string) (string, error) {
	c := path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
	switch {
	case c == "." || c == "" || path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../"):
		return "", fmt.Errorf("%q isn't a path inside the repository", p)
	case c == ".git" || strings.HasPrefix(c, ".git/"):
		return "", fmt.Errorf("%q is git's own", p)
	case strings.HasPrefix(c, ".github/"):
		return "", fmt.Errorf("%q is CI's: the factory's App can't change it, so a person does", p)
	case strings.HasPrefix(c, LockDir+"/"):
		return "", fmt.Errorf("%q is where ratified plans are recorded", p)
	}
	return c, nil
}

// Validate checks a plan as its draft left it, sorts its files, and sets
// what it touches in the trusted base.
func (p *Plan) Validate() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Summary) == "" {
		return errors.New("the plan needs a name and a summary")
	}
	if len(p.Files) == 0 {
		return errors.New("the plan names no files for the build to write")
	}
	files := map[string]bool{}
	for i, f := range p.Files {
		c, err := clean(f)
		if err != nil {
			return err
		}
		if files[c] {
			return fmt.Errorf("the plan names %s twice", c)
		}
		files[c], p.Files[i] = true, c
	}
	sort.Strings(p.Files)
	if len(p.Tests) == 0 {
		return errors.New("the plan has no acceptance tests")
	}
	native := 0
	for _, t := range p.Tests {
		if t.Native() {
			native++
		}
	}
	if native > 0 && native < len(p.Tests) {
		return errors.New("a plan's acceptance tests must all be native or all legacy Go; nothing infers a part for a legacy record")
	}
	names := map[string]bool{}
	identities := map[string]bool{}
	frameworks := map[string]string{}
	goFiles := map[string]string{}
	for i, t := range p.Tests {
		c, err := clean(t.File)
		if err != nil {
			return err
		}
		if t.Native() {
			if err := t.validateNative(); err != nil {
				return err
			}
			if identities[t.identity()] {
				return fmt.Errorf("duplicate acceptance identity: %s", t.Identity())
			}
			identities[t.identity()] = true
		}
		if framework, seen := frameworks[c]; seen && framework != t.Framework {
			return fmt.Errorf("acceptance file %s has conflicting frameworks", c)
		}
		frameworks[c] = t.Framework
		switch {
		case !t.Native() && !strings.HasSuffix(c, "_test.go"):
			return fmt.Errorf("acceptance test file %s must be a Go test file, ending in _test.go", c)
		case files[c]:
			return fmt.Errorf("%s is both a file the build writes and an acceptance test file; the build can't change the tests", c)
		case !t.Native() && !testName.MatchString(t.Name):
			return fmt.Errorf("acceptance test %q must be named like TestGraphShowsEveryDecision", t.Name)
		case strings.TrimSpace(t.Says) == "":
			return fmt.Errorf("acceptance test %s doesn't say what it shows", t.Name)
		case !t.Native() && names[path.Dir(c)+"."+t.Name]:
			return fmt.Errorf("two acceptance tests in %s are named %s", path.Dir(c), t.Name)
		}
		if !t.Native() {
			names[path.Dir(c)+"."+t.Name] = true
		}
		if !t.Native() || t.Framework == "go" {
			name := t.Name
			if t.Native() {
				name = strings.Split(t.Selector, "/")[0]
			}
			key := path.Dir(c) + "." + name
			if previous := goFiles[key]; previous != "" && previous != c {
				return fmt.Errorf("ambiguous Go acceptance function %s in %s and %s", name, previous, c)
			}
			goFiles[key] = c
		}
		p.Tests[i].File = c
	}
	for f := range p.Sources {
		if c, err := clean(f); err != nil || c != f {
			return fmt.Errorf("the plan's test sources hold %q, which isn't a clean path", f)
		}
	}
	for _, f := range p.TestFiles() {
		src, ok := p.Sources[f]
		if !ok {
			return fmt.Errorf("the plan has no text for acceptance test file %s", f)
		}
		if err := acceptanceDeclarations(f, src, p.Tests); err != nil {
			return err
		}
	}
	if len(p.Sources) != len(p.TestFiles()) {
		return errors.New("the plan's test sources hold a file no acceptance test is in")
	}
	if err := p.validateExecution(false); err != nil {
		return err
	}
	p.Trusted = nil
	for f := range p.Touches(0) {
		if Trusted(f) {
			p.Trusted = append(p.Trusted, f)
		}
	}
	sort.Strings(p.Trusted)
	return nil
}

// declares checks that a test file parses as Go and declares every
// acceptance test the plan puts in it, each as func TestX(t *testing.T).
func declares(file, src string, tests []Test) error {
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("acceptance test file %s doesn't parse: %v", file, err)
	}
	have := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
			continue
		}
		if star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr); ok {
			if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "T" {
				have[fn.Name.Name] = true
			}
		}
	}
	for _, t := range tests {
		if t.File == file && !have[t.Name] {
			return fmt.Errorf("acceptance test file %s doesn't declare func %s(t *testing.T)", file, t.Name)
		}
	}
	return nil
}

// ReadDraft reads the plan an agent left in its workspace: plan.json, and
// each acceptance test file under tests/, at its path in the repository.
func ReadDraft(ws string) (*Plan, error) {
	b, err := regular.ReadFile(ws, "plan.json")
	if err != nil {
		return nil, fmt.Errorf("the draft has no plan.json: %w", err)
	}
	var p Plan
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("plan.json: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(string(b))).Decode(&fields); err != nil {
		return nil, fmt.Errorf("plan.json: %w", err)
	}
	if _, supplied := fields["execution"]; supplied || p.Execution != nil || p.Sources != nil || p.Trusted != nil {
		return nil, errors.New("plan.json: leave sources, trusted and execution out; the factory reads the tests and captures authority")
	}
	p.Sources = map[string]string{}
	for _, t := range p.Tests {
		c, err := clean(t.File)
		if err != nil {
			return nil, err
		}
		if _, done := p.Sources[c]; done {
			continue
		}
		src, err := regular.ReadFile(ws, "tests/"+c)
		if err != nil {
			return nil, fmt.Errorf("acceptance test %s is in %s, but tests/%s can't be read: %w", t.Name, c, c, err)
		}
		p.Sources[c] = string(src)
	}
	if err := p.Validate(); err != nil {
		return &p, err
	}
	if p.Native() {
		if err := nativeSourceEntries(ws, p.Sources); err != nil {
			return &p, err
		}
	}
	return &p, nil
}

// Lock is a ratified plan as the repository records it: who ratified which
// plan, where, and the plan itself.
type Lock struct {
	Ratified Ratification `json:"ratified"`
	Plan     Plan         `json:"plan"`
}

// Ratification is a person's ratification of a plan, as a comment on its
// issue.
type Ratification struct {
	By       string `json:"by"`
	At       string `json:"at"`
	Issue    int    `json:"issue"`
	Comment  string `json:"comment"`  // the ratifying comment's URL
	Proposal string `json:"proposal"` // the plan's hash
}

// Write records the ratified plan in a checkout at root: its record, and
// each acceptance test file at its path.
func (l Lock) Write(root string) error {
	if l.Plan.Native() {
		if err := l.Verify(); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{LockPath(l.Ratified.Issue): string(b) + "\n"}
	for f, src := range l.Plan.Sources {
		files[f] = src
	}
	for f, text := range files {
		// A ratified source is created exclusively. The record itself is
		// rewritten when the same plan is ratified again.
		if l.Plan.Native() && f != LockPath(l.Ratified.Issue) {
			if err := writeNativeFile(root, f, text); err != nil {
				return err
			}
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ReadLock reads a ratified plan's record, and checks that the plan in it
// is the one ratified.
func ReadLock(b []byte) (*Lock, error) {
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	if err := l.Verify(); err != nil {
		return nil, err
	}
	return &l, nil
}

// Verify checks that a lock holds the plan that was ratified: a plan fit
// to be ratified, hashing to the ratified proposal.
func (l *Lock) Verify() error {
	if err := l.Plan.ratifiable(); err != nil {
		return err
	}
	if h := l.Plan.Hash(); h != l.Ratified.Proposal {
		return fmt.Errorf("the plan hashes to %s, not the %s ratified", h, l.Ratified.Proposal)
	}
	return nil
}

// ratifiable checks what any ratified plan must hold, wherever it's read:
// a valid plan, with the captured execution a native plan needs.
func (p *Plan) ratifiable() error {
	if err := p.Validate(); err != nil {
		return err
	}
	return p.validateExecution(true)
}
