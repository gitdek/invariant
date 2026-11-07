package setup

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

type repositoryDraft struct {
	root        *os.Root
	files       map[string]bool
	names       []string
	directories []string
	contents    map[string][]byte
	config      repository.Config
	unresolved  []string
}

func discoverRepository(dir string) (*repositoryDraft, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s must be an unlinked directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	d := &repositoryDraft{root: root, files: map[string]bool{}, contents: map[string][]byte{}}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(entry.Name(), ".git") && !entry.IsDir() {
			return nil
		}
		if entry.IsDir() {
			if name != "." && excludedDirectory(entry.Name()) {
				return fs.SkipDir
			}
			// Even a nonconventional environment name carries this marker.
			if _, err := root.Lstat(path.Join(name, "pyvenv.cfg")); err == nil {
				return fs.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if name != "." && !repositoryPath(name) {
			d.unresolved = append(d.unresolved, fmt.Sprintf("paths: rename unsafe checkout path %q before declaring ownership", name))
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			d.directories = append(d.directories, name)
		} else if entry.Type().IsRegular() {
			d.files[name] = true
			d.names = append(d.names, name)
		} else {
			d.unresolved = append(d.unresolved, fmt.Sprintf("%s paths: linked or nonregular evidence was skipped; review its ownership without following links", name))
		}
		return nil
	})
	if err != nil {
		root.Close()
		return nil, err
	}
	return d, nil
}

func excludedDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv", "env", ".env", "__pycache__", "__pypackages__", ".tox", ".nox", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".cache", "dist", "build", "coverage", ".next", ".nuxt", ".output", "out", "target", ".turbo", ".parcel-cache", ".yarn":
		return true
	}
	return strings.HasSuffix(name, ".egg-info") || strings.HasSuffix(name, ".dist-info")
}

func repositoryPath(name string) bool {
	if name == "" || name == "." || !utf8.ValidString(name) || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:*?[]") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}

func encloses(dir, name string) bool {
	return dir == "." || name == dir || strings.HasPrefix(name, dir+"/")
}

func (d *repositoryDraft) read(name string) ([]byte, error) {
	if b, ok := d.contents[name]; ok {
		return b, nil
	}
	if !d.files[name] {
		return nil, fmt.Errorf("%s is not discovered regular evidence", name)
	}
	for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
		info, err := d.root.Lstat(dir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%s is no longer an unlinked directory: %v", dir, err)
		}
	}
	info, err := d.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is no longer regular evidence", name)
	}
	f, err := d.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while reading evidence", name)
	}
	b, err := io.ReadAll(f)
	if err == nil {
		d.contents[name] = b
	}
	return b, err
}

func (d *repositoryDraft) project(dir, language string) bool {
	var markers []string
	switch language {
	case "go":
		markers = []string{"go.mod"}
	case "python":
		markers = []string{"pyproject.toml", "requirements.txt", "requirements.lock", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock", "uv.lock", "poetry.lock"}
	case "typescript":
		markers = []string{"package.json"}
	}
	for _, marker := range markers {
		if d.files[path.Join(dir, marker)] {
			return true
		}
	}
	return false
}

func dependencyEvidence(language, name string) bool {
	if inheritedEvidence(name) {
		switch language {
		case "go":
			return slices.Contains([]string{"go.work", "go.work.sum", ".go-version", ".tool-versions"}, name)
		case "python":
			return slices.Contains([]string{"pip.conf", "pip.ini", ".python-version", ".tool-versions"}, name)
		case "typescript":
			return slices.Contains([]string{".npmrc", ".yarnrc", ".yarnrc.yml", ".node-version", ".nvmrc", ".tool-versions"}, name)
		}
	}
	switch language {
	case "go":
		return name == "go.mod" || name == "go.sum"
	case "python":
		return slices.Contains([]string{"pyproject.toml", "requirements.txt", "requirements.lock", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock", "uv.lock", "poetry.lock"}, name)
	case "typescript":
		return slices.Contains([]string{"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}, name)
	}
	return false
}

func inheritedEvidence(name string) bool {
	return slices.Contains([]string{"go.work", "go.work.sum", ".npmrc", ".yarnrc", ".yarnrc.yml", "pip.conf", "pip.ini", ".python-version", ".node-version", ".nvmrc", ".go-version", ".tool-versions"}, name)
}

func unsupportedManager(name string) bool {
	return slices.Contains([]string{"uv.lock", "poetry.lock", "Pipfile", "Pipfile.lock", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}, name)
}

func agentRule(name string) bool {
	switch strings.ToLower(path.Base(name)) {
	case "agents.md", "agents.override.md", "claude.md", "claude.local.md", ".mcp.json":
		return true
	}
	for _, component := range strings.Split(name, "/") {
		if strings.EqualFold(component, ".codex") || strings.EqualFold(component, ".claude") {
			return true
		}
	}
	return false
}

var documentLink = regexp.MustCompile("\\[[^]\\n]*\\]\\(([^)\\n]+)\\)|`([^`\\n]+)`")

func (d *repositoryDraft) documents() error {
	for _, name := range d.names {
		if !agentRule(name) {
			continue
		}
		d.config.AgentRules = append(d.config.AgentRules, name)
		if !strings.EqualFold(path.Ext(name), ".md") {
			continue
		}
		b, err := d.read(name)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "current state") && !strings.Contains(lower, "current-state") && !strings.Contains(lower, "source of truth") && !strings.Contains(lower, "build against") {
				continue
			}
			for _, match := range documentLink.FindAllStringSubmatch(line, -1) {
				ref := match[1]
				if ref == "" {
					ref = match[2]
				}
				ref, _, _ = strings.Cut(ref, "#")
				if strings.Contains(ref, "://") || path.Ext(ref) == "" {
					continue
				}
				target := path.Join(path.Dir(name), ref)
				if path.IsAbs(ref) || !repositoryPath(target) || !d.files[target] {
					d.unresolved = append(d.unresolved, fmt.Sprintf("%s current_state: declare a confined regular document for %q", name, ref))
					continue
				}
				d.config.CurrentState = append(d.config.CurrentState, target)
			}
		}
	}
	slices.Sort(d.config.CurrentState)
	d.config.CurrentState = slices.Compact(d.config.CurrentState)
	return nil
}

func (d *repositoryDraft) ownership() {
	owners := map[string][]string{}
	for i, p := range d.config.Parts {
		for _, other := range d.config.Parts[:i] {
			if p.Directory == other.Directory {
				d.issue(&p, "ownership", "co-located languages need one unambiguous directory owner; review and separate the part declarations")
			}
		}
		for _, name := range append(slices.Clone(p.Inputs), p.Lockfiles...) {
			owners[name] = append(owners[name], p.Name)
		}
	}
	for _, name := range d.names {
		if name == repository.Path || slices.Contains(d.config.AgentRules, name) || slices.Contains(d.config.CurrentState, name) {
			continue
		}
		if repository.CI(name) {
			for _, p := range d.config.Parts {
				owners[name] = append(owners[name], p.Name)
			}
		}
		if len(owners[name]) > 0 {
			slices.Sort(owners[name])
			owners[name] = slices.Compact(owners[name])
			if len(owners[name]) > 1 || repository.CI(name) {
				d.config.SharedPaths = append(d.config.SharedPaths, repository.SharedPath{Path: name, Parts: owners[name]})
			}
			continue
		}
		owned := false
		for _, p := range d.config.Parts {
			owned = owned || encloses(p.Directory, name)
		}
		if !owned {
			d.unresolved = append(d.unresolved, fmt.Sprintf("%s ownership: declare an owning part or explicit shared owners", name))
		}
	}
}

// Capture literal local TypeScript extends chains, including inputs shared by
// sibling parts. Package resolution and executable configuration stay unknown.
func (d *repositoryDraft) tsconfig(p *repository.Part, name string, active map[string]bool) error {
	if active[name] {
		d.issue(p, "inputs", "cyclic tsconfig extends at "+name+"; resolve the configuration chain")
		return nil
	}
	active[name] = true
	defer delete(active, name)
	p.Inputs = append(p.Inputs, name)
	b, err := d.read(name)
	if err != nil {
		return err
	}
	var config struct {
		Extends any `json:"extends"`
	}
	if err := plumbing.DecodeDependencyJSON(b, &config); err != nil {
		d.issue(p, "inputs", name+": use a statically understood JSON tsconfig or complete its inputs manually")
		return nil
	}
	if config.Extends == nil {
		return nil
	}
	ref, ok := config.Extends.(string)
	target := path.Join(path.Dir(name), ref)
	if ok && !d.files[target] && path.Ext(target) == "" {
		target += ".json"
	}
	if !ok || !strings.HasPrefix(ref, ".") || !repositoryPath(target) || !d.files[target] {
		d.issue(p, "inputs", name+": resolve extends to confined regular checkout evidence")
		return nil
	}
	return d.tsconfig(p, target, active)
}
