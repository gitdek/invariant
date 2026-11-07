package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

// DraftRepository reads checkout evidence without executing commands or writing
// files. The unresolved field deliberately makes incomplete drafts invalid to
// the enabled-configuration loader; that loader's schema remains unchanged.
func DraftRepository(dir string) ([]byte, []string, error) {
	d, err := discoverRepository(dir)
	if err != nil {
		return nil, nil, err
	}
	defer d.root.Close()
	if err := d.draft(); err != nil {
		return nil, nil, err
	}
	slices.Sort(d.unresolved)
	d.unresolved = slices.Compact(d.unresolved)
	b, err := json.MarshalIndent(struct {
		repository.Config
		Unresolved []string `json:"unresolved,omitempty"`
	}{d.config, d.unresolved}, "", "  ")
	return append(b, '\n'), slices.Clone(d.unresolved), err
}

func (d *repositoryDraft) draft() error {
	d.config = repository.Config{Version: 1, Parts: []repository.Part{}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	for _, dir := range d.directories {
		for _, language := range []string{"go", "python", "typescript"} {
			if !d.project(dir, language) {
				continue
			}
			p := repository.Part{Name: language + ":" + dir, Directory: dir, Language: language, Inputs: []string{}, Lockfiles: []string{}, Commands: map[string]repository.Command{}}
			for _, field := range commandFields {
				p.Commands[field] = repository.Command{}
			}
			if err := d.part(&p); err != nil {
				return err
			}
			d.config.Parts = append(d.config.Parts, p)
		}
	}
	if len(d.config.Parts) == 0 {
		d.unresolved = append(d.unresolved, "parts: no supported project detected; declare the repository's parts, ownership, toolchains and checks")
	}
	if err := d.documents(); err != nil {
		return err
	}
	d.ownership()
	return nil
}

var commandFields = []string{"install", "format", "lint", "build", "test", "test_one"}

func (d *repositoryDraft) issue(p *repository.Part, field, reason string) {
	d.unresolved = append(d.unresolved, fmt.Sprintf("%s (%s) %s: %s", p.Directory, p.Language, field, reason))
}

func (d *repositoryDraft) part(p *repository.Part) error {
	files := map[string][]byte{}
	for _, name := range d.names {
		if !dependencyEvidence(p.Language, path.Base(name)) || !encloses(path.Dir(name), p.Directory) {
			continue
		}
		// Parent manifests belong to their own parts. Parent manager settings,
		// however, can change preparation even in an otherwise independent part.
		if path.Dir(name) != p.Directory && !inheritedEvidence(path.Base(name)) {
			continue
		}
		b, err := d.read(name)
		if err != nil {
			return err
		}
		files[name] = b
		p.Inputs = append(p.Inputs, name)
	}
	set := func(field string, argv ...string) { p.Commands[field] = repository.Command{Argv: argv} }
	switch p.Language {
	case "go":
		if name := path.Join(p.Directory, "go.sum"); d.files[name] {
			p.Lockfiles = append(p.Lockfiles, name)
			p.Inputs = without(p.Inputs, name)
		}
		set("install", "go", "mod", "download")
		d.goCommands(p, files)
	case "python":
		// A dedicated lock takes precedence over source requirements.txt.
		for _, lock := range []string{"requirements.lock", "requirements.txt"} {
			name := path.Join(p.Directory, lock)
			if !d.files[name] {
				continue
			}
			p.Lockfiles = []string{name}
			set("install", "python3", "-m", "pip", "install", "--require-hashes", "--only-binary=:all:", "-r", lock)
			probe := *p
			if plumbing.CheckDependencyInstall(probe, map[string][]byte{name: files[name]}) == nil {
				p.Inputs = without(p.Inputs, name)
			} else {
				p.Lockfiles = []string{}
			}
			break
		}
		if err := d.pythonCommands(p, files); err != nil {
			return err
		}
	case "typescript":
		if name := path.Join(p.Directory, "package-lock.json"); d.files[name] {
			p.Lockfiles = append(p.Lockfiles, name)
			p.Inputs = without(p.Inputs, name)
		}
		set("install", "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund")
		if err := d.nodeCommands(p, files); err != nil {
			return err
		}
	}
	unsupported := []string{}
	for _, name := range p.Inputs {
		if unsupportedManager(path.Base(name)) {
			unsupported = append(unsupported, name)
		}
	}
	if err := plumbing.CheckDependencyInstall(*p, files); err != nil || len(unsupported) > 0 {
		p.Commands["install"] = repository.Command{}
		// Admission may encounter several invalid map entries in any order.
		// Keep diagnostics deterministic while naming all retained evidence.
		names := append(slices.Clone(p.Inputs), p.Lockfiles...)
		slices.Sort(names)
		reason := "supply supported locked dependencies and review " + strings.Join(names, ", ")
		switch p.Language {
		case "go":
			reason += "; go.mod needs complete go.sum hashes for dependencies, without replace, exclude or go.work declarations"
		case "python":
			reason += "; use a requirements.txt or requirements.lock with exact versions and SHA-256 hashes, without includes or other package managers"
		case "typescript":
			reason += "; npm ci needs a matching package-lock.json v2/v3 with a complete integrity-locked graph and no other package manager configuration"
		}
		d.issue(p, "install", reason)
	}
	for _, field := range commandFields {
		if field != "install" && len(p.Commands[field].Argv) == 0 {
			d.issue(p, field, "declare a supported check command and its scope; format checks must exit unsuccessfully on differences")
		}
	}
	slices.Sort(p.Inputs)
	p.Inputs = slices.Compact(p.Inputs)
	return nil
}

func without(values []string, value string) []string {
	return slices.DeleteFunc(values, func(s string) bool { return s == value })
}

// CheckInitPaths checks both destinations before init writes either file.
func CheckInitPaths(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be an unlinked directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, destination := range []string{WorkflowPath, repository.Path} {
		components := strings.Split(destination, "/")
		for i := range components {
			name := strings.Join(components[:i+1], "/")
			info, err := root.Lstat(name)
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil {
				return err
			}
			if i < len(components)-1 && !info.IsDir() || i == len(components)-1 && !info.Mode().IsRegular() {
				return fmt.Errorf("%s: init needs unlinked directories and regular destination files", name)
			}
		}
	}
	return nil
}

// WriteRepository keeps any existing regular configuration byte for byte. It
// has no force option: completing or replacing a draft is a person's work.
func WriteRepository(dir string) (kept bool, unresolved []string, err error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return false, nil, err
	}
	if !info.IsDir() {
		return false, nil, fmt.Errorf("%s must be an unlinked directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, nil, err
	}
	defer root.Close()
	for _, name := range []string{".invariant", repository.Path} {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return false, nil, err
		}
		if name == ".invariant" && !info.IsDir() || name == repository.Path && !info.Mode().IsRegular() {
			return false, nil, fmt.Errorf("%s must be an unlinked %s", name, map[bool]string{true: "directory", false: "regular file"}[name == ".invariant"])
		}
		if name == repository.Path {
			return true, nil, nil
		}
	}
	b, unresolved, err := DraftRepository(dir)
	if err != nil {
		return false, nil, err
	}
	if err := root.Mkdir(".invariant", 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return false, nil, err
	}
	info, err = root.Lstat(".invariant")
	if err != nil || !info.IsDir() {
		return false, nil, fmt.Errorf(".invariant must be an unlinked directory: %v", err)
	}
	f, err := root.OpenFile(repository.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, nil, err
	}
	_, err = f.Write(b)
	return false, unresolved, errors.Join(err, f.Close())
}
