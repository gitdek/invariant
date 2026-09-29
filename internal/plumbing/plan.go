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
	Files   []string `json:"files"`   // every file the build may write, from the repository's root
	Tests   []Test   `json:"tests"`   // the acceptance tests
	// Sources is each acceptance test file's text, by path. The tests are
	// new files the ratification adds, which the build can't change.
	Sources map[string]string `json:"sources"`
	// Trusted is the files the change touches in the trusted base, which
	// only a person merges. The factory sets it; the hash leaves it out.
	Trusted []string `json:"trusted,omitempty"`
}

// Test is one acceptance test: a Go test function in one of the plan's test
// files.
type Test struct {
	Name string `json:"name"` // the function, as TestGraphShowsEveryDecision
	File string `json:"file"` // its file, from the repository's root
	Says string `json:"says"` // what it shows, in plain words
}

// TrustedBase is what only a person merges (D-0105): the gate and the
// verifiers, the scope and ratification checks, CI and its decision check,
// the factory's own code, its proved rules and its CLI, the dependencies,
// the page that posts the owner's commands, the rules agents work by, and
// the configuration that starts their tools in a session (D-0132).
var TrustedBase = []string{
	".github/", "cmd/", "factory/", "go.mod", "go.sum", "AGENTS.md", "CLAUDE.md",
	".claude/", ".codex/", ".mcp.json",
	"internal/conformance/", "internal/decisions/", "internal/factory/", "internal/formalize/",
	"internal/github/", "internal/gobra/", "internal/mcp/", "internal/plumbing/", "internal/project/",
	"internal/receipt/", "internal/regular/", "internal/scope/", "internal/setup/", "internal/synth/", "internal/tla/",
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
	sort.Strings(c.Files)
	sort.Slice(c.Tests, func(i, j int) bool {
		if c.Tests[i].File != c.Tests[j].File {
			return c.Tests[i].File < c.Tests[j].File
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
	names := map[string]bool{}
	for i, t := range p.Tests {
		c, err := clean(t.File)
		if err != nil {
			return err
		}
		switch {
		case !strings.HasSuffix(c, "_test.go"):
			return fmt.Errorf("acceptance test file %s must be a Go test file, ending in _test.go", c)
		case files[c]:
			return fmt.Errorf("%s is both a file the build writes and an acceptance test file; the build can't change the tests", c)
		case !testName.MatchString(t.Name):
			return fmt.Errorf("acceptance test %q must be named like TestGraphShowsEveryDecision", t.Name)
		case strings.TrimSpace(t.Says) == "":
			return fmt.Errorf("acceptance test %s doesn't say what it shows", t.Name)
		case names[path.Dir(c)+"."+t.Name]:
			return fmt.Errorf("two acceptance tests in %s are named %s", path.Dir(c), t.Name)
		}
		names[path.Dir(c)+"."+t.Name], p.Tests[i].File = true, c
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
		if err := declares(f, src, p.Tests); err != nil {
			return err
		}
	}
	if len(p.Sources) != len(p.TestFiles()) {
		return errors.New("the plan's test sources hold a file no acceptance test is in")
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
	if p.Sources != nil || p.Trusted != nil {
		return nil, errors.New("plan.json: leave sources and trusted out; the factory reads the tests from tests/ and works out what's trusted")
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
	return &p, p.Validate()
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
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{LockPath(l.Ratified.Issue): string(b) + "\n"}
	for f, src := range l.Plan.Sources {
		files[f] = src
	}
	for f, text := range files {
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
	if err := l.Plan.Validate(); err != nil {
		return nil, err
	}
	if h := l.Plan.Hash(); h != l.Ratified.Proposal {
		return nil, fmt.Errorf("the plan hashes to %s, not the %s ratified", h, l.Ratified.Proposal)
	}
	return &l, nil
}
