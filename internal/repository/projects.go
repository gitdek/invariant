package repository

import (
	"fmt"
	"strings"
)

const DefaultProjectsDirectory = "invariant"

// Projects supplies the execution default without changing the serialized
// contract: old native execution digests must retain their original identity.
func (c Config) Projects() string {
	if c.ProjectsDirectory == "" {
		return DefaultProjectsDirectory
	}
	return c.ProjectsDirectory
}

// IsProjectPath matches the reserved directory and its descendants only.
func (c Config) IsProjectPath(name string) bool {
	dir := c.Projects()
	return name == dir || strings.HasPrefix(name, dir+"/")
}

// ValidateProjectsDirectory checks a declaration, including an explicit empty
// value. The directory may not exist yet, but cannot reserve repository policy.
func ValidateProjectsDirectory(name string) error {
	if err := validatePath(name, false, false); err != nil {
		return fmt.Errorf("projects_directory: %w", err)
	}
	// Init uses the directory in both flat ESLint and legacy ignore patterns.
	if strings.ContainsAny(name, "[]:{}()!#") {
		return fmt.Errorf("projects_directory %q must be a literal repository directory", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("projects_directory %q must not start with -; the gate refuses leading-dash project paths", name)
	}
	for _, component := range strings.Split(name, "/") {
		switch strings.ToLower(component) {
		case ".invariant", ".github", ".circleci", ".gitlab-ci.yml":
			return fmt.Errorf("projects_directory %q conflicts with repository policy", name)
		}
	}
	return nil
}

// CheckProjects checks the directory's relationship to application declarations.
// Tree and filesystem readers separately check its existing path components.
func (c Config) CheckProjects() error {
	if err := ValidateProjectsDirectory(c.Projects()); err != nil {
		return err
	}
	for _, part := range c.Parts {
		if c.IsProjectPath(part.Directory) {
			return fmt.Errorf("projects_directory %q contains application part %q", c.Projects(), part.Name)
		}
		for _, names := range [][]string{part.Inputs, part.Lockfiles} {
			for _, name := range names {
				if c.IsProjectPath(name) {
					return fmt.Errorf("projects_directory %q contains part %q dependency input %q", c.Projects(), part.Name, name)
				}
			}
		}
	}
	return nil
}
