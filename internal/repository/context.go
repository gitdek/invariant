package repository

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// Context is a detached description of a captured base for an agent. It does
// not supply execution or scope authority; those still use the Snapshot.
type Context struct {
	Base         string
	Config       Config
	Paths        []string
	Rules        []ContextFile
	CurrentState []ContextFile
}

// ContextFile retains the original path and bytes, including the captured
// target of an agent-rule link when it resolves inside the repository.
type ContextFile struct {
	Path   string
	Target string
	Text   string
}

// Context reads committed objects, never the caller's working-tree copies.
// All nested rules are labeled by path so planning can consider any part;
// their directory scope is not promoted to a repository-wide instruction.
func (s *Snapshot) Context(ctx context.Context) (*Context, error) {
	c := &Context{Base: s.Commit(), Config: s.Config()}
	rules := slices.Clone(c.Config.AgentRules)
	for name, entry := range s.entries {
		if entry.directory() {
			continue
		}
		c.Paths = append(c.Paths, name)
		switch strings.ToLower(path.Base(name)) {
		case "agents.md", "agents.override.md", "claude.md", "claude.local.md":
			rules = append(rules, name)
		}
	}
	sort.Strings(c.Paths)
	sort.Strings(rules)
	for _, name := range slices.Compact(rules) {
		file := ContextFile{Path: name}
		target := name
		if s.entries[name].mode == "120000" {
			var ok bool
			var err error
			target, ok, err = s.resolve(ctx, name)
			if err != nil {
				return nil, err
			}
			if !ok || !s.entries[target].regular() {
				file.Text = "Captured rule link has no readable regular target inside the repository."
				c.Rules = append(c.Rules, file)
				continue
			}
			file.Target = target
		}
		data, err := s.ReadFileContext(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("captured agent rule %s: %w", name, err)
		}
		file.Text = string(data)
		c.Rules = append(c.Rules, file)
	}
	for _, name := range c.Config.CurrentState {
		data, err := s.ReadFileContext(ctx, name)
		if err != nil {
			return nil, err
		}
		c.CurrentState = append(c.CurrentState, ContextFile{Path: name, Text: string(data)})
	}
	return c, ctx.Err()
}
