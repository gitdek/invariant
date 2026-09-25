// Package tla reads the parts of a TLA+ module that Invariant pins and
// mutates: top-level operator definitions.
package tla

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
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

// Mutate replaces the single occurrence of from with to. It fails unless from
// occurs exactly once, so a mutant can't silently stop applying after the
// model is edited.
func Mutate(src, from, to string) (string, error) {
	switch n := strings.Count(src, from); n {
	case 1:
		return strings.Replace(src, from, to, 1), nil
	case 0:
		return "", fmt.Errorf("%q does not occur in the module", from)
	default:
		return "", fmt.Errorf("%q occurs %d times in the module; it must occur exactly once", from, n)
	}
}
