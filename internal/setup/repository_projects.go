package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// InitProjects checks the directory before init writes either destination and
// supplies advice for existing contracts as well as new drafts. Reading an
// unfinished contract here does not endorse it as executable authority.
func InitProjects(dir, requested string) (string, []string, error) {
	if err := repository.ValidateProjectsDirectory(requested); err != nil {
		return "", nil, err
	}
	c := repository.Config{ProjectsDirectory: requested}
	data, err := regular.ReadFile(dir, repository.Path)
	if err == nil {
		// Unknown draft fields stay untouched. An absent directory in an
		// existing contract means the default, regardless of the CLI flag.
		c = repository.Config{}
		if err := json.Unmarshal(data, &c); err != nil {
			return "", nil, fmt.Errorf("read projects_directory from retained contract: %w", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return "", nil, err
		}
		if value, ok := fields["projects_directory"]; ok {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return "", nil, errors.New("projects_directory must not be null")
			}
			if err := repository.ValidateProjectsDirectory(c.ProjectsDirectory); err != nil {
				return "", nil, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if err := c.CheckProjects(); err != nil {
		return "", nil, err
	}
	projects := c.Projects()
	if err := checkProjectsPath(dir, projects); err != nil {
		return "", nil, err
	}
	d, err := discoverRepository(dir, projects)
	if err != nil {
		return "", nil, err
	}
	defer d.root.Close()
	var directories []string
	for _, part := range c.Parts {
		if part.Language == "typescript" && encloses(part.Directory, projects) {
			directories = append(directories, part.Directory)
		}
	}
	for _, name := range d.directories {
		if encloses(name, projects) && d.project(name, "typescript") {
			directories = append(directories, name)
		}
	}
	slices.Sort(directories)
	var guidance []string
	for _, name := range slices.Compact(directories) {
		relative := projects
		if name != "." {
			relative = strings.TrimPrefix(projects, name+"/")
		}
		guidance = append(guidance, fmt.Sprintf("TypeScript part %q: add %q to the exclude array in %s; add a global ESLint ignores entry %q in the ESLint configuration at %s (or that pattern in its legacy .eslintignore). Paths are relative to this part; adjust for configuration files in other directories.", name, relative+"/**", path.Join(name, "tsconfig.json"), relative+"/**", name))
	}
	return projects, guidance, nil
}

func checkProjectsPath(dir, projects string) error {
	if err := repository.ValidateProjectsDirectory(projects); err != nil {
		return err
	}
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
	components := strings.Split(projects, "/")
	for i := range components {
		name := strings.Join(components[:i+1], "/")
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("projects_directory: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("projects_directory %q needs an unlinked directory at %s", projects, name)
		}
	}
	return nil
}
