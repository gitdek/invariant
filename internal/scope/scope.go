// Package scope checks that a factory pull request stays in bounds (D-0014):
// it changes one project and nothing else, adds no dependencies (Go modules,
// npm packages or Python requirements), uses no cgo, leaves CI configuration
// alone, and changes no ratified lock except by adding a new, ratified
// project.
package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// Result is what a pull request changes, and anything out of bounds.
type Result struct {
	Project  string   `json:"project"` // the one project directory the changes are in
	New      bool     `json:"new"`     // whether the pull request adds the project
	Files    []string `json:"files"`
	Problems []string `json:"problems,omitempty"`
}

// OK says whether the pull request stays in bounds.
func (r Result) OK() bool { return len(r.Problems) == 0 }

const (
	manifestFile = ".invariant/invariant.json"
	lockFile     = ".invariant/ratified.lock"
)

// Check compares head with base in the git repository at dir, the way a pull
// request of head into base would change it. issue is the issue the pull
// request answers; only its ratification may amend an existing project.
func Check(ctx context.Context, dir, base, head string, issue int) (Result, error) {
	g := git{ctx: ctx, dir: dir}
	out, err := g.run("diff", "--name-only", "--no-renames", base+"..."+head)
	if err != nil {
		return Result{}, err
	}
	var r Result
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f != "" {
			r.Files = append(r.Files, f)
		}
	}
	if len(r.Files) == 0 {
		r.Problems = append(r.Problems, "it changes nothing")
		return r, nil
	}
	projects := map[string]bool{}
	for _, f := range r.Files {
		if f == ".github" || strings.HasPrefix(f, ".github/") {
			r.Problems = append(r.Problems, "it edits CI configuration: "+f)
		}
		p, ok := g.projectOf(head, f)
		if !ok {
			r.Problems = append(r.Problems, "it changes "+f+", which isn't in a project")
			continue
		}
		projects[p] = true
	}
	if len(projects) != 1 {
		if len(projects) > 1 {
			r.Problems = append(r.Problems, "it changes more than one project: "+strings.Join(keys(projects), ", "))
		}
		return r, nil
	}
	r.Project = keys(projects)[0]
	r.New = !g.exists(base, join(r.Project, manifestFile))

	if r.New {
		var lock project.Lock
		if b, err := g.show(head, join(r.Project, lockFile)); err != nil || json.Unmarshal([]byte(b), &lock) != nil {
			r.Problems = append(r.Problems, "it adds a project without a readable ratified lock")
		} else if lock.Ratified == nil {
			r.Problems = append(r.Problems, "it adds a project whose lock has no ratification record")
		}
	} else if changed(r.Files, join(r.Project, lockFile)) {
		// An amendment (D-0045): the new lock must carry this issue's
		// ratification, amending exactly the lock on the base branch.
		r.Problems = append(r.Problems, amendment(g, base, head, join(r.Project, lockFile), issue)...)
	}

	before, _ := g.show(base, join(r.Project, "go.mod"))
	after, _ := g.show(head, join(r.Project, "go.mod"))
	had := requires(before)
	for _, m := range sorted(requires(after)) {
		if !had[m] {
			r.Problems = append(r.Problems, "it adds a module dependency: "+m)
		}
	}
	for _, f := range r.Files {
		switch name := path.Base(f); {
		case name == "package.json":
			after, err := g.show(head, f)
			if err != nil {
				continue // deleted
			}
			before, _ := g.show(base, f)
			had := npmDependencies(before)
			for _, dep := range sorted(npmDependencies(after)) {
				if !had[dep] {
					r.Problems = append(r.Problems, "it adds a package dependency: "+dep)
				}
			}
		case pythonDependencyFile(name):
			if g.exists(head, f) {
				r.Problems = append(r.Problems, "it adds or changes a Python dependency file: "+f)
			}
		}
	}
	for _, f := range r.Files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		src, err := g.show(head, f)
		if err != nil {
			continue // deleted
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s doesn't parse: %v", f, err))
			continue
		}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "C" {
				r.Problems = append(r.Problems, f+" uses cgo")
			}
		}
	}
	return r, nil
}

// amendment checks a change to an existing project's lock.
func amendment(g git, base, head, lockPath string, issue int) []string {
	var before, after project.Lock
	b, err := g.show(base, lockPath)
	if err != nil || json.Unmarshal([]byte(b), &before) != nil {
		return []string{"it changes a lock that can't be read on the base branch"}
	}
	a, err := g.show(head, lockPath)
	if err != nil || json.Unmarshal([]byte(a), &after) != nil {
		return []string{"it leaves a project's lock unreadable"}
	}
	r := after.Ratified
	switch {
	case r == nil:
		return []string{"it changes the ratified lock of an existing project without a ratification"}
	case issue == 0 || r.Issue != issue:
		return []string{fmt.Sprintf("it changes an existing project's lock with a ratification from #%d, not this pull request's issue", r.Issue)}
	case r.Amends != project.ProposalHash(before.Bounds, before.Statements):
		return []string{"its amendment was drafted against a lock that has since changed"}
	case project.ProposalHash(after.Bounds, after.Statements) != r.Proposal:
		return []string{"its lock isn't the proposal that was ratified"}
	}
	return nil
}

// npmDependencies lists every package a package.json depends on, of any
// kind.
func npmDependencies(text string) map[string]bool {
	out := map[string]bool{}
	var pkg map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &pkg) != nil {
		return out
	}
	for _, field := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		var deps map[string]any
		if json.Unmarshal(pkg[field], &deps) == nil {
			for name := range deps {
				out[name] = true
			}
		}
	}
	var bundled []string
	if json.Unmarshal(pkg["bundleDependencies"], &bundled) == nil {
		for _, name := range bundled {
			out[name] = true
		}
	}
	return out
}

// pythonDependencyFile says whether a file is where Python projects declare
// what to install. The factory's Python uses the standard library only.
func pythonDependencyFile(name string) bool {
	switch name {
	case "pyproject.toml", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock", "poetry.lock":
		return true
	}
	return strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt")
}

// requires lists the modules a go.mod requires.
func requires(gomod string) map[string]bool {
	out := map[string]bool{}
	block := false
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case block && line == ")":
			block = false
		case block && line != "":
			out[strings.Fields(line)[0]] = true
		case line == "require (":
			block = true
		case strings.HasPrefix(line, "require "):
			if f := strings.Fields(line); len(f) >= 2 {
				out[f[1]] = true
			}
		}
	}
	return out
}

type git struct {
	ctx context.Context
	dir string
}

func (g git) run(args ...string) (string, error) {
	cmd := exec.CommandContext(g.ctx, "git", append([]string{"-C", g.dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g git) show(ref, file string) (string, error) { return g.run("show", ref+":"+file) }

func (g git) exists(ref, file string) bool {
	_, err := g.run("cat-file", "-e", ref+":"+file)
	return err == nil
}

// projectOf finds the project a file belongs to at ref: the nearest
// directory above it with a manifest.
func (g git) projectOf(ref, file string) (string, bool) {
	for d := path.Dir(file); ; d = path.Dir(d) {
		if g.exists(ref, join(d, manifestFile)) {
			return d, true
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

func join(dir, file string) string {
	if dir == "." {
		return file
	}
	return dir + "/" + file
}

func changed(files []string, f string) bool {
	for _, x := range files {
		if x == f {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string { return sorted(m) }

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
