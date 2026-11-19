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
	// ErrFileMissing identifies a safe file path absent from the captured tree.
	ErrFileMissing = errors.New("repository file is absent")
)

// Config is version 1 of the repository contract. Paths are relative to the
// repository root, including dependency inputs belonging to nested parts.
type Config struct {
	Version           int          `json:"version"`
	ProjectsDirectory string       `json:"projects_directory,omitempty"`
	Parts             []Part       `json:"parts"`
	AgentRules        []string     `json:"agent_rules"`
	CurrentState      []string     `json:"current_state"`
	TrustedPaths      []string     `json:"trusted_paths"`
	SharedPaths       []SharedPath `json:"shared_paths"`
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
	// MacOS, when set, declares that the part builds on macOS too. Omitted,
	// it stays omitted when the contract is serialized, so the execution
	// digests of contracts without it don't change.
	MacOS *MacOS `json:"macos,omitempty"`
	// TestMinutes, when set, is the part's test budget in the factory's
	// checks: from DefaultTestMinutes to MaxTestMinutes (#487). Omitted, the
	// part has the default, and the field stays omitted when the contract
	// is serialized, as MacOS does.
	TestMinutes int `json:"test_minutes,omitempty"`
}

const (
	// DefaultTestMinutes is a part's test budget when it declares none: the
	// ten minutes every check had before parts could declare one.
	DefaultTestMinutes = 10
	// MaxTestMinutes is the most a part can declare: the 55 minutes inside
	// an hour that Invariant's own checks of internal/verify have (D-0460).
	// Two such checks, the agent's last test run and the factory's own,
	// fit inside a watcher's 150-minute run with time for the agent and its
	// review.
	MaxTestMinutes = 55
)

// MacOS is the checks CI's gate runs for a Go part on a hosted macOS
// runner, in the part's directory, as a job of its own (#929). Linux skips
// the files only macOS builds, such as cgo with Objective-C, so the Linux
// job never checks them. Each check is a plain argument array: no shell, no
// placeholder, and no reason in place of a command.
type MacOS struct {
	Build Command `json:"build"`
	Vet   Command `json:"vet"`
	Test  Command `json:"test"`
	// Checks run after the tests, in order, such as a self-test that needs
	// no permission.
	Checks []MacOSCheck `json:"checks,omitempty"`
}

// MacOSCheck is one check a part's macOS job runs, by its name.
type MacOSCheck struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
}

// Steps are the checks in the order the macOS job runs them: build, vet,
// test, then each other check as declared.
func (m MacOS) Steps() []MacOSCheck {
	steps := []MacOSCheck{
		{Name: "build", Argv: slices.Clone(m.Build.Argv)},
		{Name: "vet", Argv: slices.Clone(m.Vet.Argv)},
		{Name: "test", Argv: slices.Clone(m.Test.Argv)},
	}
	for _, c := range m.Checks {
		steps = append(steps, MacOSCheck{Name: c.Name, Argv: slices.Clone(c.Argv)})
	}
	return steps
}

// MacOSParts are the parts that declare macOS checks, sorted by name.
func (c Config) MacOSParts() []Part {
	var parts []Part
	for _, p := range c.Parts {
		if p.MacOS != nil {
			parts = append(parts, clonePart(p))
		}
	}
	slices.SortFunc(parts, func(a, b Part) int { return strings.Compare(a.Name, b.Name) })
	return parts
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
	seen, err := decodeObject(data, "configuration", map[string]any{
		"version": &c.Version, "parts": &parts,
		"projects_directory": &c.ProjectsDirectory,
		"agent_rules":        &c.AgentRules, "current_state": &c.CurrentState,
		"trusted_paths": &c.TrustedPaths, "shared_paths": &shared,
	}, "projects_directory")
	if err != nil {
		return c, err
	}
	if seen["projects_directory"] {
		if err := ValidateProjectsDirectory(c.ProjectsDirectory); err != nil {
			return c, err
		}
	}
	if parts == nil || shared == nil {
		return c, errors.New("parts and shared_paths must be arrays, not null")
	}
	c.Parts = make([]Part, len(parts))
	for i, raw := range parts {
		p := &c.Parts[i]
		where := fmt.Sprintf("parts[%d]", i)
		var commands, macos json.RawMessage
		seen, err := decodeObject(raw, where, map[string]any{
			"name": &p.Name, "directory": &p.Directory, "language": &p.Language,
			"image": &p.Image, "lockfiles": &p.Lockfiles, "inputs": &p.Inputs,
			"commands": &commands, "macos": &macos, "test_minutes": &p.TestMinutes,
		}, "macos", "test_minutes")
		if err != nil {
			return c, err
		}
		if seen["test_minutes"] && (p.TestMinutes < DefaultTestMinutes || p.TestMinutes > MaxTestMinutes) {
			return c, fmt.Errorf("part %q declares test_minutes %d, but a part's test budget is from the default %d minutes to the most a check allows, %d", p.Name, p.TestMinutes, DefaultTestMinutes, MaxTestMinutes)
		}
		p.Commands, err = decodeCommands(commands, where+".commands")
		if err != nil {
			return c, err
		}
		if seen["macos"] {
			if p.MacOS, err = decodeMacOS(macos, where+".macos"); err != nil {
				return c, err
			}
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

// macOSCheckName is a name for one of a part's other macOS checks, as the
// job's output and a person read it.
var macOSCheckName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// decodeMacOS reads a part's macOS checks. Each is a plain argument array:
// unlike a Linux command, it can't give a reason in its place, and it holds
// no {test} placeholder, since the job runs whole checks only.
func decodeMacOS(data []byte, where string) (*MacOS, error) {
	var build, vet, test json.RawMessage
	var checks []json.RawMessage
	if _, err := decodeObject(data, where, map[string]any{
		"build": &build, "vet": &vet, "test": &test, "checks": &checks,
	}, "checks"); err != nil {
		return nil, err
	}
	m := &MacOS{}
	for _, c := range []struct {
		name string
		raw  json.RawMessage
		to   *Command
	}{{"build", build, &m.Build}, {"vet", vet, &m.Vet}, {"test", test, &m.Test}} {
		label := where + "." + c.name
		var argv []string
		var reason string
		seen, err := decodeObject(c.raw, label, map[string]any{"argv": &argv, "not_applicable": &reason}, "argv", "not_applicable")
		if err != nil {
			return nil, err
		}
		if seen["not_applicable"] {
			return nil, fmt.Errorf("%s needs an argv: a part that builds on macOS is built, vetted and tested there, so not_applicable doesn't apply", label)
		}
		if !seen["argv"] {
			return nil, fmt.Errorf("%s: missing argv", label)
		}
		if err := plainArgv(argv, label+".argv"); err != nil {
			return nil, err
		}
		c.to.Argv = argv
	}
	names := map[string]bool{"build": true, "vet": true, "test": true}
	for i, raw := range checks {
		label := fmt.Sprintf("%s.checks[%d]", where, i)
		var c MacOSCheck
		if _, err := decodeObject(raw, label, map[string]any{"name": &c.Name, "argv": &c.Argv}); err != nil {
			return nil, err
		}
		if !macOSCheckName.MatchString(c.Name) {
			return nil, fmt.Errorf("%s.name %q must be lowercase letters, digits and dashes, at most 64, starting with a letter or a digit", label, c.Name)
		}
		if names[c.Name] {
			return nil, fmt.Errorf("%s.name %q names another of the part's macOS checks", label, c.Name)
		}
		names[c.Name] = true
		if err := plainArgv(c.Argv, label+".argv"); err != nil {
			return nil, err
		}
		m.Checks = append(m.Checks, c)
	}
	return m, nil
}

// plainArgv refuses anything but an argument array run as it is: an empty
// array, a blank executable, a {test} placeholder, and a control character,
// NUL and newline included, in any argument.
func plainArgv(argv []string, label string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("%s needs a nonempty argv with a nonblank executable", label)
	}
	for _, arg := range argv {
		if strings.IndexFunc(arg, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("%s holds a control character, and a plain argument array doesn't", label)
		}
		if strings.Contains(arg, "{test}") {
			return fmt.Errorf("%s holds a {test} placeholder: the macOS job runs whole checks, and fills in nothing", label)
		}
	}
	return nil
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
	if err := c.CheckProjects(); err != nil {
		return err
	}
	if err := s.checkPath(c.Projects()+"/", false, true); err != nil {
		return fmt.Errorf("projects_directory: %w", err)
	}
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
		if p.MacOS != nil && p.Language != "go" {
			return fmt.Errorf("part %q declares macos checks, which only a Go part can: the macOS job sets up Go alone", p.Name)
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
