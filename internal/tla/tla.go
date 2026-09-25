// Package tla reads the parts of a TLA+ module that Invariant pins: top-level
// operator definitions, and what each depends on.
package tla

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	lineComment  = regexp.MustCompile(`\\\*.*`)
	blockComment = regexp.MustCompile(`(?s)\(\*.*?\*\)`)
	whitespace   = regexp.MustCompile(`\s+`)
	moduleHeader = regexp.MustCompile(`-{4,}\s*MODULE\s+(\w+)\s*-{4,}`)
)

// ModuleName returns the name in the module's header line.
func ModuleName(src string) (string, error) {
	m := moduleHeader.FindStringSubmatch(src)
	if m == nil {
		return "", fmt.Errorf("no MODULE header")
	}
	return m[1], nil
}

// Definition returns the text of the top-level definition of name: the line
// that starts `name ==` (or `name(args) ==`) at column 0, plus every
// following line that is blank or indented.
func Definition(src, name string) (string, error) {
	head := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `(\s*\([^)]*\))?\s*==`)
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !head.MatchString(line) {
			continue
		}
		j := i + 1
		for j < len(lines) && (strings.TrimSpace(lines[j]) == "" || lines[j][0] == ' ' || lines[j][0] == '\t') {
			j++
		}
		return strings.TrimRight(strings.Join(lines[i:j], "\n"), " \t\n"), nil
	}
	return "", fmt.Errorf("no top-level definition of %s", name)
}

// Canonical strips comments and collapses whitespace, so reformatting a
// definition leaves its hash alone but any change to its meaning does not.
func Canonical(def string) string {
	def = blockComment.ReplaceAllString(def, " ")
	def = lineComment.ReplaceAllString(def, " ")
	return strings.TrimSpace(whitespace.ReplaceAllString(def, " "))
}

// Hash is the pin for a definition: the SHA-256 of its canonical text.
func Hash(def string) string {
	sum := sha256.Sum256([]byte(Canonical(def)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

var (
	definitionHead = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(\s*\([^)]*\))?\s*==`)
	identifier     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	stringLiteral  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	declaration    = regexp.MustCompile(`^(CONSTANTS?|VARIABLES?)\b`)
	subscript      = regexp.MustCompile(`(\]|>>|\bWF|\bSF)_`)
)

// Definitions names the module's top-level definitions, in source order.
func Definitions(src string) []string {
	var names []string
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if m := definitionHead.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

// Closure returns name and every top-level definition it depends on,
// directly or indirectly, sorted. It doesn't follow the names in stop,
// which belong to the factory's model.
func Closure(src, name string, stop map[string]bool) ([]string, error) {
	defined := map[string]bool{}
	for _, d := range Definitions(src) {
		defined[d] = true
	}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(n string) error {
		if seen[n] {
			return nil
		}
		seen[n] = true
		def, err := Definition(src, n)
		if err != nil {
			return err
		}
		body := stringLiteral.ReplaceAllString(Canonical(def), " ")
		// In [A]_v, <<A>>_v, WF_v(A) and SF_v(A), the underscore is syntax, not
		// part of a name.
		body = subscript.ReplaceAllString(body, "$1 ")
		for _, ref := range identifier.FindAllString(body, -1) {
			if defined[ref] && !stop[ref] && ref != n {
				if err := visit(ref); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(name); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// PinHash is the pin for a statement: the SHA-256 of the canonical text of
// its closure. Pinning the closure means a statement can't be weakened by
// redefining something it depends on. For a statement with no dependencies
// it equals Hash of its definition.
func PinHash(src, name string, stop map[string]bool) (string, error) {
	names, err := Closure(src, name, stop)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(names))
	for i, n := range names {
		def, err := Definition(src, n)
		if err != nil {
			return "", err
		}
		parts[i] = Canonical(def)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Variables names the module's variables, from its VARIABLE declarations.
func Variables(src string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var names []string
	for i := 0; i < len(lines); i++ {
		m := declaration.FindStringSubmatch(lines[i])
		if m == nil || !strings.HasPrefix(m[1], "VARIABLE") {
			continue
		}
		text := lineComment.ReplaceAllString(strings.TrimPrefix(lines[i], m[1]), " ")
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], " ") && strings.TrimSpace(lines[i+1]) != "" {
			i++
			text += " " + lineComment.ReplaceAllString(lines[i], " ")
		}
		for _, n := range strings.Split(text, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// ModelMarker opens the part of a skeleton the factory writes.
const ModelMarker = `\* ---------------------------------------------------------------------------
\* The model. The factory writes Init, one operator per action, and Next
\* here. Everything else in this module is pinned and must not change.
\* ---------------------------------------------------------------------------`

// Skeleton reduces a module to what's pinned: its header, its CONSTANT and
// VARIABLE declarations, and the definitions in keep, in source order, each
// with the comment lines directly above it. The definition named last (the
// spec, which refers to the model) goes after ModelMarker, where the factory
// adds the model.
func Skeleton(src string, keep map[string]bool, last string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var header, decls, defs []string
	var lastDef string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case moduleHeader.MatchString(line) && header == nil:
			header = []string{line}
		case declaration.MatchString(line):
			decls = append(decls, line)
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], " ") && strings.TrimSpace(lines[i+1]) != "" {
				i++
				decls = append(decls, lines[i])
			}
		default:
			m := definitionHead.FindStringSubmatch(line)
			if m == nil || !keep[m[1]] {
				continue
			}
			def, err := Definition(src, m[1])
			if err != nil {
				return "", err
			}
			start := i
			for start > 0 && strings.HasPrefix(lines[start-1], `\*`) {
				start--
			}
			text := strings.Join(append(append([]string{}, lines[start:i]...), def), "\n")
			if m[1] == last {
				lastDef = def
			} else {
				defs = append(defs, text)
			}
		}
	}
	if header == nil {
		return "", fmt.Errorf("no MODULE header")
	}
	if lastDef == "" {
		return "", fmt.Errorf("no definition of %s", last)
	}
	var b strings.Builder
	b.WriteString(header[0] + "\n")
	b.WriteString(strings.Join(decls, "\n") + "\n\n")
	b.WriteString(strings.Join(defs, "\n\n") + "\n\n")
	b.WriteString(ModelMarker + "\n\n")
	b.WriteString(lastDef + "\n")
	b.WriteString(strings.Repeat("=", 77) + "\n")
	return b.String(), nil
}
