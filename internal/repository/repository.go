// Package repository reads a repository's check declarations from a committed
// base. It selects checks, but never runs them or enables external plumbing.
package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Path is the repository contract, separate from modeled-project manifests.
const Path = ".invariant/repository.json"

var (
	ErrMissing = errors.New("repository contract is absent")
	ErrInvalid = errors.New("invalid repository contract")
)

// Config is version 1 of the repository contract. Paths are relative to the
// repository root, including dependency inputs belonging to nested parts.
type Config struct {
	Version      int          `json:"version"`
	Parts        []Part       `json:"parts"`
	AgentRules   []string     `json:"agent_rules"`
	CurrentState []string     `json:"current_state"`
	TrustedPaths []string     `json:"trusted_paths"`
	SharedPaths  []SharedPath `json:"shared_paths"`
}

// Part declares the checks for one directory and its dependency inputs.
type Part struct {
	Name      string             `json:"name"`
	Directory string             `json:"directory"`
	Language  string             `json:"language"`
	Image     string             `json:"image"`
	Lockfiles []string           `json:"lockfiles"`
	Inputs    []string           `json:"inputs"`
	Commands  map[string]Command `json:"commands"`
}

// Command is either an argument array or a reason the check does not apply.
// Arguments are data for a later runner, in the part's directory.
type Command struct {
	Argv          []string `json:"argv,omitempty"`
	NotApplicable string   `json:"not_applicable,omitempty"`
}

// SharedPath adds named owners to an exact file or a directory prefix.
type SharedPath struct {
	Path  string   `json:"path"`
	Parts []string `json:"parts"`
}

var imagePattern = regexp.MustCompile(`^(?:[A-Za-z0-9.-]+(?::[0-9]+)?/)?[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?@sha256:[0-9a-f]{64}$`)

func decodeConfig(data []byte) (Config, error) {
	var c Config
	if !utf8.Valid(data) {
		return c, errors.New("configuration must be valid UTF-8")
	}
	var parts, shared []json.RawMessage
	_, err := decodeObject(data, "configuration", map[string]any{
		"version": &c.Version, "parts": &parts,
		"agent_rules": &c.AgentRules, "current_state": &c.CurrentState,
		"trusted_paths": &c.TrustedPaths, "shared_paths": &shared,
	})
	if err != nil {
		return c, err
	}
	if parts == nil || shared == nil {
		return c, errors.New("parts and shared_paths must be arrays, not null")
	}
	c.Parts = make([]Part, len(parts))
	for i, raw := range parts {
		p := &c.Parts[i]
		where := fmt.Sprintf("parts[%d]", i)
		var commands json.RawMessage
		if _, err := decodeObject(raw, where, map[string]any{
			"name": &p.Name, "directory": &p.Directory, "language": &p.Language,
			"image": &p.Image, "lockfiles": &p.Lockfiles, "inputs": &p.Inputs,
			"commands": &commands,
		}); err != nil {
			return c, err
		}
		p.Commands, err = decodeCommands(commands, where+".commands")
		if err != nil {
			return c, err
		}
	}
	c.SharedPaths = make([]SharedPath, len(shared))
	for i, raw := range shared {
		s := &c.SharedPaths[i]
		if _, err := decodeObject(raw, fmt.Sprintf("shared_paths[%d]", i), map[string]any{
			"path": &s.Path, "parts": &s.Parts,
		}); err != nil {
			return c, err
		}
	}
	return c, nil
}

func decodeCommands(data []byte, where string) (map[string]Command, error) {
	names := []string{"install", "format", "lint", "build", "test", "test_one"}
	raw := make([]json.RawMessage, len(names))
	fields := map[string]any{}
	for i, name := range names {
		fields[name] = &raw[i]
	}
	if _, err := decodeObject(data, where, fields); err != nil {
		return nil, err
	}
	commands := make(map[string]Command, len(names))
	for i, name := range names {
		var c Command
		label := where + "." + name
		seen, err := decodeObject(raw[i], label, map[string]any{
			"argv": &c.Argv, "not_applicable": &c.NotApplicable,
		}, "argv", "not_applicable")
		if err != nil {
			return nil, err
		}
		if seen["argv"] == seen["not_applicable"] {
			return nil, fmt.Errorf("%s needs exactly one of argv or not_applicable", label)
		}
		if seen["not_applicable"] {
			if strings.TrimSpace(c.NotApplicable) == "" {
				return nil, fmt.Errorf("%s needs a nonblank not_applicable reason", label)
			}
		} else {
			if len(c.Argv) == 0 || strings.TrimSpace(c.Argv[0]) == "" {
				return nil, fmt.Errorf("%s needs a nonempty argv with a nonblank executable", label)
			}
			placeholders := 0
			for _, arg := range c.Argv {
				if strings.ContainsRune(arg, '\x00') {
					return nil, fmt.Errorf("%s argv contains a NUL", label)
				}
				placeholders += strings.Count(arg, "{test}")
			}
			if name == "test_one" && (placeholders != 1 || strings.Contains(c.Argv[0], "{test}")) {
				return nil, fmt.Errorf("%s argv needs exactly one {test} placeholder in a selector argument", label)
			}
		}
		commands[name] = c
	}
	return commands, nil
}

// decodeObject checks exact field names and their presence before interpreting
// values. encoding/json alone accepts duplicate keys and case aliases.
func decodeObject(data []byte, where string, fields map[string]any, optional ...string) (map[string]bool, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("%s must be an object", where)
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%s needs a field name", where)
		}
		target, ok := fields[key]
		if !ok {
			return nil, fmt.Errorf("%s: unknown field %q", where, key)
		}
		if seen[key] {
			return nil, fmt.Errorf("%s: duplicate field %q", where, key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", where, key, err)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("%s.%s must not be null", where, key)
		}
		if _, ok := target.(*[]string); ok {
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", where, key, err)
			}
			for _, value := range values {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return nil, fmt.Errorf("%s.%s needs strings, not null", where, key)
				}
			}
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", where, key, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s: trailing JSON", where)
	}
	required := make([]string, 0, len(fields))
	for key := range fields {
		required = append(required, key)
	}
	sort.Strings(required)
	for _, key := range required {
		if !seen[key] && !slices.Contains(optional, key) {
			return nil, fmt.Errorf("%s: missing %s", where, key)
		}
	}
	return seen, nil
}

func (s *Snapshot) validate() error {
	c := s.config
	if c.Version != 1 {
		return fmt.Errorf("unsupported version %d; want 1", c.Version)
	}
	if len(c.Parts) == 0 {
		return errors.New("parts must name at least one part")
	}
	for _, list := range []struct {
		name  string
		paths []string
	}{
		{"agent_rules", c.AgentRules}, {"current_state", c.CurrentState},
		{"trusted_paths", c.TrustedPaths},
	} {
		check := s.requireFile
		if list.name == "trusted_paths" {
			check = s.checkSelector
		}
		if err := validatePaths(list.name, list.paths, check); err != nil {
			return err
		}
	}
	names, directories := map[string]bool{}, map[string]bool{}
	for _, p := range c.Parts {
		if strings.TrimSpace(p.Name) == "" || names[p.Name] {
			return fmt.Errorf("part name %q is blank or duplicated", p.Name)
		}
		names[p.Name] = true
		if directories[p.Directory] {
			return fmt.Errorf("part directory %q has two owners", p.Directory)
		}
		directories[p.Directory] = true
		if err := s.requireDirectory(p.Directory); err != nil {
			return fmt.Errorf("part %q directory: %w", p.Name, err)
		}
		switch p.Language {
		case "go", "python", "typescript":
		default:
			return fmt.Errorf("part %q has unsupported language %q", p.Name, p.Language)
		}
		if !imagePattern.MatchString(p.Image) {
			return fmt.Errorf("part %q image must name an image pinned by @sha256 and 64 lowercase hex digits", p.Name)
		}
		if err := validatePaths("part "+p.Name+" lockfiles", p.Lockfiles, s.requireFile); err != nil {
			return err
		}
		if err := validatePaths("part "+p.Name+" inputs", p.Inputs, s.requireFile); err != nil {
			return err
		}
	}
	for i, shared := range c.SharedPaths {
		if err := s.checkSelector(shared.Path); err != nil {
			return fmt.Errorf("shared_paths: %w", err)
		}
		if len(shared.Parts) == 0 {
			return fmt.Errorf("shared path %q has no parts", shared.Path)
		}
		seen := map[string]bool{}
		for _, name := range shared.Parts {
			if !names[name] || seen[name] {
				return fmt.Errorf("shared path %q names unknown or duplicate part %q", shared.Path, name)
			}
			seen[name] = true
		}
		for _, earlier := range c.SharedPaths[:i] {
			if selectorMatches(shared.Path, earlier.Path) || selectorMatches(earlier.Path, shared.Path) {
				return fmt.Errorf("shared paths %q and %q overlap", earlier.Path, shared.Path)
			}
		}
	}
	return nil
}

func validatePaths(label string, paths []string, check func(string) error) error {
	if paths == nil {
		return fmt.Errorf("%s must be an explicit array, not null", label)
	}
	seen := map[string]bool{}
	for _, name := range paths {
		if seen[name] {
			return fmt.Errorf("%s repeats %q", label, name)
		}
		seen[name] = true
		if err := check(name); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	return nil
}
