package repository

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// DirectoryOwner returns an independent copy of the deepest enclosing part.
// Safe new or deleted paths use the same rule; a safe path outside all part
// directories returns false, even if it has shared or dependency check owners.
func (s *Snapshot) DirectoryOwner(name string) (Part, bool, error) {
	if err := s.checkPath(name, false, false); err != nil {
		return Part{}, false, err
	}
	deepest := -1
	for i, part := range s.config.Parts {
		if part.Directory == "." || strings.HasPrefix(name, part.Directory+"/") {
			if deepest < 0 || s.config.Parts[deepest].Directory == "." || len(part.Directory) > len(s.config.Parts[deepest].Directory) {
				deepest = i
			}
		}
	}
	if deepest < 0 {
		return Part{}, false, nil
	}
	return clonePart(s.config.Parts[deepest]), true, nil
}

// PartsFor selects the checks affected by every touched file using the captured
// configuration. A new or deleted file has the same owners as an existing file
// at that path. An unsafe or unowned path invalidates the entire selection.
func (s *Snapshot) PartsFor(paths []string) ([]Part, error) {
	selected := make(map[string]bool)
	for _, name := range paths {
		owner, owned, err := s.DirectoryOwner(name)
		if err != nil {
			return nil, err
		}
		if name == Path || slices.Contains(s.config.AgentRules, name) || slices.Contains(s.config.CurrentState, name) {
			for _, part := range s.config.Parts {
				selected[part.Name] = true
			}
			continue
		}

		if owned {
			selected[owner.Name] = true
		}
		for _, part := range s.config.Parts {
			if slices.Contains(part.Lockfiles, name) || slices.Contains(part.Inputs, name) {
				selected[part.Name] = true
				owned = true
			}
		}
		for _, shared := range s.config.SharedPaths {
			if selectorMatches(name, shared.Path) {
				for _, owner := range shared.Parts {
					selected[owner] = true
				}
				owned = true
			}
		}
		if !owned {
			return nil, fmt.Errorf("%s: touched path %q has no owning part", Path, name)
		}
	}

	var parts []Part
	for _, part := range s.config.Parts {
		if selected[part.Name] {
			parts = append(parts, clonePart(part))
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
	return parts, nil
}

// IsTrusted reports whether the captured policy protects a file as part of the
// trusted base. Current-state documents are not trusted unless also declared
// through a trusted category. What a trusted symbolic link in the base names
// is trusted too.
func (s *Snapshot) IsTrusted(name string) (bool, error) {
	if err := s.checkPath(name, false, false); err != nil {
		return false, err
	}
	return s.trustedByRule(name) || linkedFrom(s.linked, name), nil
}

// trustedByRule applies the policy to a path by its name alone.
func (s *Snapshot) trustedByRule(name string) bool {
	if name == Path || CI(name) || agentPath(name) || slices.Contains(s.config.AgentRules, name) {
		return true
	}
	for _, part := range s.config.Parts {
		if slices.Contains(part.Lockfiles, name) || slices.Contains(part.Inputs, name) {
			return true
		}
	}
	for _, selector := range s.config.TrustedPaths {
		if selectorMatches(name, selector) {
			return true
		}
	}
	return false
}

// CI protects the gate workflow and the repository's other CI configuration.
func CI(name string) bool {
	return name == ".github" || strings.HasPrefix(name, ".github/") ||
		name == ".gitlab-ci.yml" || name == ".circleci" || strings.HasPrefix(name, ".circleci/")
}

// agentPath says whether a file supplies a coding agent's instructions or
// tools, in any directory: AGENTS.md and the AGENTS.override.md Codex reads
// in its place, CLAUDE.md and CLAUDE.local.md, .mcp.json, and anything under
// .codex/ or .claude/. Names match whatever their case, since a
// case-insensitive file system, as macOS's, serves app/agents.md to an agent
// that asks for app/AGENTS.md.
func agentPath(name string) bool {
	switch strings.ToLower(path.Base(name)) {
	case "agents.md", "agents.override.md", "claude.md", "claude.local.md", ".mcp.json":
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if p := strings.ToLower(part); p == ".codex" || p == ".claude" {
			return true
		}
	}
	return false
}

func selectorMatches(name, selector string) bool {
	if strings.HasSuffix(selector, "/") {
		return strings.HasPrefix(name, selector)
	}
	return name == selector
}

func cloneConfig(config Config) Config {
	config.Parts = slices.Clone(config.Parts)
	for i, part := range config.Parts {
		config.Parts[i] = clonePart(part)
	}
	config.AgentRules = slices.Clone(config.AgentRules)
	config.CurrentState = slices.Clone(config.CurrentState)
	config.TrustedPaths = slices.Clone(config.TrustedPaths)
	config.SharedPaths = slices.Clone(config.SharedPaths)
	for i, shared := range config.SharedPaths {
		config.SharedPaths[i].Parts = slices.Clone(shared.Parts)
	}
	return config
}

func clonePart(part Part) Part {
	part.Lockfiles = slices.Clone(part.Lockfiles)
	part.Inputs = slices.Clone(part.Inputs)
	if part.Commands != nil {
		commands := make(map[string]Command, len(part.Commands))
		for name, command := range part.Commands {
			command.Argv = slices.Clone(command.Argv)
			commands[name] = command
		}
		part.Commands = commands
	}
	return part
}
