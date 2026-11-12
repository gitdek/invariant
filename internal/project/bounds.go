package project

import (
	"fmt"
	"sort"
	"strings"
)

// The gate writes TLC's config itself, from the lock's bounds, one line a
// bound: CONSTANT Name = value (D-0017). TLC reads that file a token at a
// time, so a value that held more than one value, or a line end, would add a
// directive of its own, such as a CONSTRAINT naming an operator the factory
// wrote, and TLC would check fewer states than the bounds say. A proposal
// shows a bound as inline code, where a line end reads as a space. So a
// bound's name is one identifier, and its value is one value: a number, a
// model value, a string, or a set or tuple of values.

// configWords are the words that start a directive in a TLC config. None
// can be a bound's name or a model value.
var configWords = map[string]bool{
	"CONSTANT": true, "CONSTANTS": true, "CONSTRAINT": true, "CONSTRAINTS": true, "ACTION_CONSTRAINT": true, "ACTION_CONSTRAINTS": true,
	"INVARIANT": true, "INVARIANTS": true, "PROPERTY": true, "PROPERTIES": true, "SPECIFICATION": true, "INIT": true, "NEXT": true,
	"SYMMETRY": true, "VIEW": true, "ALIAS": true, "POSTCONDITION": true, "CHECK_DEADLOCK": true, "TYPE": true, "TYPE_CONSTRAINT": true,
}

// validBounds refuses a bound the gate can't write into TLC's config as one
// constant.
func validBounds(bounds map[string]string) error {
	names := make([]string, 0, len(bounds))
	for name := range bounds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !boundWord(name) || configWords[name] {
			return fmt.Errorf("%s: the bound %q isn't a constant's name: a name is letters, digits and underscores, starting with a letter", lockFile, name)
		}
		value := bounds[name]
		rest, err := boundValue(strings.TrimLeft(value, " "))
		if err == nil && strings.TrimLeft(rest, " ") != "" {
			err = fmt.Errorf("it holds more than one value")
		}
		if err != nil {
			return fmt.Errorf("%s: the bound %s = %q isn't one value: %v. A bound is a number, a model value, a string, or a set {…} or tuple <<…>> of them, on one line", lockFile, name, value, err)
		}
	}
	return nil
}

// boundWord says whether s is a name: letters, digits and underscores,
// starting with a letter or an underscore.
func boundWord(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return s != ""
}

// boundValue reads one value from the start of s and returns what follows
// it.
func boundValue(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("it's empty")
	}
	for _, pair := range [][2]string{{"{", "}"}, {"<<", ">>"}} {
		if !strings.HasPrefix(s, pair[0]) {
			continue
		}
		s = strings.TrimLeft(s[len(pair[0]):], " ")
		if strings.HasPrefix(s, pair[1]) {
			return s[len(pair[1]):], nil
		}
		for {
			rest, err := boundValue(s)
			if err != nil {
				return "", err
			}
			s = strings.TrimLeft(rest, " ")
			switch {
			case strings.HasPrefix(s, pair[1]):
				return s[len(pair[1]):], nil
			case strings.HasPrefix(s, ","):
				s = strings.TrimLeft(s[1:], " ")
			default:
				return "", fmt.Errorf("%s isn't closed by %s", pair[0], pair[1])
			}
		}
	}
	switch c := s[0]; {
	case c == '"':
		for i := 1; i < len(s); i++ {
			switch {
			case s[i] < 0x20 || s[i] == 0x7f:
				return "", fmt.Errorf("a string holds a control character")
			case s[i] == '\\' && i+1 < len(s):
				i++
			case s[i] == '"':
				return s[i+1:], nil
			}
		}
		return "", fmt.Errorf("a string never closes")
	case c == '-' || (c >= '0' && c <= '9'):
		i := 1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if c == '-' && i == 1 {
			return "", fmt.Errorf("- isn't a number")
		}
		return s[i:], nil
	default:
		i := 0
		for i < len(s) && (s[i] == '_' || (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		if i == 0 {
			return "", fmt.Errorf("%q can't start a value", s[:1])
		}
		if configWords[s[:i]] {
			return "", fmt.Errorf("%s starts a directive in TLC's config", s[:i])
		}
		return s[i:], nil
	}
}
