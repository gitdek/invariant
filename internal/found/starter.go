package found

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// file is one file a founding puts in the checkout.
type file struct {
	path string // relative to the checkout, with slashes
	data []byte
	mode fs.FileMode
}

// The Go starter's files are templates, named so that none of them is a
// module or a Go file of Invariant's own.
//
//go:embed starter/go/*.tmpl
var starters embed.FS

// specPath is where a project's spec goes, and formatCheck the script that
// fails when a Go file isn't formatted.
const (
	specPath    = "docs/PRD.md"
	formatCheck = "tools/check-format"
)

// goStarter is the Go starter: where each file goes, the template it's
// written from, and its mode.
var goStarter = []struct {
	path, template string
	mode           fs.FileMode
}{
	{".gitignore", "gitignore", 0o644},
	{"AGENTS.md", "AGENTS.md", 0o644},
	{"README.md", "README.md", 0o644},
	{"SPEC.md", "SPEC.md", 0o644},
	{"doc.go", "doc.go", 0o644},
	{"go.mod", "go.mod", 0o644},
	{formatCheck, "check-format", 0o755},
}

// starter is the Go starter for owner/name: a module with a package
// comment, the format check, and the documents that say what is built. Its
// documents name the spec only when the project has one.
func starter(owner, name string, spec bool) ([]file, error) {
	about := struct {
		Name, Module, Package string
		Spec                  bool
	}{name, "github.com/" + owner + "/" + name, packageName(name), spec}
	var files []file
	for _, s := range goStarter {
		t, err := template.ParseFS(starters, "starter/go/"+s.template+".tmpl")
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, about); err != nil {
			return nil, err
		}
		files = append(files, file{s.path, b.Bytes(), s.mode})
	}
	return files, nil
}

// packageName is a Go package name for a repository's name: its letters and
// digits, in lower case. A name that leaves none, that starts with a digit,
// or that Go keeps for itself gets pkg before it.
func packageName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	p := b.String()
	if p == "" || p[0] <= '9' || token.IsKeyword(p) || p == "main" || p == "init" {
		p = "pkg" + p
	}
	return p
}

// devices are the file names Windows keeps for itself. Go refuses a module
// path with one as an element, whatever follows its first dot, so a
// starter for a repository named for one wouldn't build.
var devices = []string{"con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
	"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9"}

// device is the name Windows keeps that a repository's name stands for, as
// Go reads a module path's element, or "" when it stands for none.
func device(name string) string {
	short, _, _ := strings.Cut(name, ".")
	if slices.Contains(devices, strings.ToLower(short)) {
		return short
	}
	return ""
}

// blocked is the first of the files that can't go where it's meant to: one
// that's there already with other contents, or a path with something other
// than a directory on the way to it. A file that's there with the same
// contents is kept, so a founding that stopped can be run again.
func blocked(dir string, files []file, again string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range files {
		if _, err := present(root, dir, f, again); err != nil {
			return err
		}
	}
	return nil
}

// clear refuses a path with something other than a directory on the way to
// it, whatever the file that goes there will hold.
func clear(dir, name, again string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return way(root, dir, name, again)
}

// way is the error of a path whose way isn't clear: one of the directories
// it goes through is there as a file or a link.
func way(root *os.Root, dir, name, again string) error {
	parts := strings.Split(name, "/")
	for i := range parts[:len(parts)-1] {
		through := path.Join(parts[:i+1]...)
		info, err := root.Lstat(through)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is in the way of %s: invariant new needs a directory there, and never replaces what it finds. Move it aside, then run it again:\n\n%s", filepath.Join(dir, filepath.FromSlash(through)), name, again)
		}
	}
	return nil
}

// present says whether the file is in the checkout already, with the same
// contents. Its error names what's there in its place, and the command to
// run once that's out of the way.
func present(root *os.Root, dir string, f file, again string) (bool, error) {
	if err := way(root, dir, f.path, again); err != nil {
		return false, err
	}
	info, err := root.Lstat(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	differs := fmt.Errorf("%s is already there, and isn't the file invariant new would write. It never overwrites a file: move this one aside, then run it again:\n\n%s", filepath.Join(dir, filepath.FromSlash(f.path)), again)
	if !info.Mode().IsRegular() || f.mode&0o111 != 0 && info.Mode()&0o111 == 0 {
		return false, differs
	}
	old, err := root.ReadFile(f.path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(old, f.data) {
		return false, differs
	}
	return true, nil
}

// place writes each file that isn't in the checkout yet, and returns the
// ones it wrote. It replaces nothing: a file is created only where none is,
// and only inside the checkout.
func place(dir string, files []file, again string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var wrote []string
	for _, f := range files {
		there, err := present(root, dir, f, again)
		if err != nil {
			return wrote, err
		}
		if there {
			continue
		}
		if parent := path.Dir(f.path); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return wrote, err
			}
		}
		w, err := root.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
		if err != nil {
			return wrote, err
		}
		_, err = w.Write(f.data)
		if err := errors.Join(err, w.Close()); err != nil {
			return wrote, err
		}
		wrote = append(wrote, f.path)
	}
	return wrote, nil
}

// checks is the checks file of a checkout as it would be with the founding's
// files in it, complete: what init would draft there, with the three things
// a person completed by hand. Go's format check is the starter's script,
// since gofmt -l doesn't fail on differences. The current state is the
// starter's documents, which the draft finds only where a rule names them.
// And tools/, which holds the format check, is trusted.
//
// Nothing is written to find out, so a founding knows its checks before it
// creates anything. base is the checkout as it is, or "" when there's none
// yet. left is what the draft leaves unresolved besides: a person's to
// settle, so a founding stops on it. named is every file the checks name:
// the agent rules, the current-state documents and each part's dependency
// inputs and lockfiles. The loader refuses checks that name a file their
// commit doesn't hold.
func checks(base string, files []file) (contract []byte, named, left []string, err error) {
	added := map[string][]byte{}
	for _, f := range files {
		added[f.path] = f.data
	}
	draft, _, err := setup.DraftRepositoryWith(base, added)
	if err != nil {
		return nil, nil, nil, err
	}
	var c struct {
		repository.Config
		Unresolved []string `json:"unresolved"`
	}
	if err := json.Unmarshal(draft, &c); err != nil {
		return nil, nil, nil, err
	}
	// Only the root's Go part is the starter's. settled is how the draft
	// words the one requirement this completes, for that part.
	var settled string
	for _, p := range c.Parts {
		if p.Directory == "." && p.Language == "go" && p.Commands != nil {
			p.Commands["format"] = repository.Command{Argv: []string{"./" + formatCheck}}
			settled = p.Directory + " (" + p.Language + ") format: "
		}
	}
	documents := []string{}
	for _, name := range []string{"SPEC.md", "README.md", specPath} {
		if _, adding := added[name]; adding || base != "" && regular(filepath.Join(base, filepath.FromSlash(name))) {
			documents = append(documents, name)
		}
	}
	for _, name := range c.CurrentState {
		if !slices.Contains(documents, name) {
			documents = append(documents, name)
		}
	}
	c.CurrentState = documents
	if trusted := path.Dir(formatCheck) + "/"; !slices.Contains(c.TrustedPaths, trusted) {
		c.TrustedPaths = append(c.TrustedPaths, trusted)
	}
	left = slices.DeleteFunc(c.Unresolved, func(issue string) bool {
		return settled != "" && strings.HasPrefix(issue, settled)
	})
	named = slices.Concat(c.AgentRules, c.CurrentState)
	for _, p := range c.Parts {
		named = slices.Concat(named, p.Inputs, p.Lockfiles)
	}
	contract, err = json.MarshalIndent(c.Config, "", "  ")
	return append(contract, '\n'), named, left, err
}

// regular says whether name is a regular file.
func regular(name string) bool {
	info, err := os.Lstat(name)
	return err == nil && info.Mode().IsRegular()
}
