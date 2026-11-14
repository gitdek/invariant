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

// foreign is every entry of the directory that isn't the founding's own,
// by its path, a directory with a slash after it. The founding's own are
// its files, each at its path with the bytes it would write now, the
// directories on the way to them, and the checkout's .git. Everything else
// is foreign, whatever git makes of it: a file with other contents, a file
// git ignores, a directory whose name differs only by case, a repository
// inside this one, a link. It's decided by reading the directory, and no
// link is followed. A directory that isn't the founding's is named and not
// entered.
func foreign(dir string, files []file) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	own, through := map[string]file{}, map[string]bool{}
	for _, f := range files {
		own[f.path] = f
		for d := path.Dir(f.path); d != "."; d = path.Dir(d) {
			through[d] = true
		}
	}
	var found []string
	var read func(in string) error
	read = func(in string) error {
		entries, err := fs.ReadDir(root.FS(), in)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := path.Join(in, entry.Name())
			f, mine := own[name]
			switch {
			case name == ".git":
			case entry.IsDir() && through[name]:
				if err := read(name); err != nil {
					return err
				}
			case entry.IsDir():
				found = append(found, name+"/")
			case !mine || !entry.Type().IsRegular():
				found = append(found, name)
			default:
				info, err := entry.Info()
				if err != nil {
					return err
				}
				data, err := root.ReadFile(name)
				if err != nil {
					return err
				}
				if !bytes.Equal(data, f.data) || f.mode&0o111 != 0 && info.Mode()&0o111 == 0 {
					found = append(found, name)
				}
			}
		}
		return nil
	}
	return found, read(".")
}

// place writes each of the founding's files that isn't in the checkout yet,
// and returns the ones it wrote. It replaces nothing: a file is created only
// where none is, and only inside the checkout.
func place(dir string, files []file) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var wrote []string
	for _, f := range files {
		if _, err := root.Lstat(f.path); err == nil {
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

// checks is the checks file of a project that holds the founding's files
// and nothing else, complete: what init would draft of them, with the three
// things a person completed by hand. Go's format check is the starter's
// script, since gofmt -l doesn't fail on differences. The current state is
// the starter's documents, which the draft finds only where a rule names
// them. And tools/, which holds the format check, is trusted.
//
// Nothing is written to find out, so a founding knows its checks before it
// creates anything. A draft that leaves anything else unresolved is an
// error: a starter is complete, or it isn't written.
func checks(files []file) ([]byte, error) {
	held := map[string][]byte{}
	for _, f := range files {
		held[f.path] = f.data
	}
	draft, _, err := setup.DraftRepositoryOf(held)
	if err != nil {
		return nil, err
	}
	var c struct {
		repository.Config
		Unresolved []string `json:"unresolved"`
	}
	if err := json.Unmarshal(draft, &c); err != nil {
		return nil, err
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
		if _, there := held[name]; there {
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
	left := slices.DeleteFunc(c.Unresolved, func(issue string) bool {
		return settled != "" && strings.HasPrefix(issue, settled)
	})
	if len(left) > 0 {
		return nil, fmt.Errorf("the starter's own checks leave requirements unresolved, so nothing was created:\n- %s", strings.Join(left, "\n- "))
	}
	contract, err := json.MarshalIndent(c.Config, "", "  ")
	return append(contract, '\n'), err
}
