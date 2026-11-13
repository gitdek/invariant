package tla

import (
	"fmt"
	"strings"
)

// The gate pins text it finds itself, and TLC then checks the model with its
// own parser, SANY. A pin means something only where the two read the same
// text, so this file reads a module's strings and comments the way SANY's
// lexer does (#812): a string runs to its closing quote on the same line; a
// \* comment runs to the end of its line; a (* *) comment runs to its
// matching close, and nests. Inside a string a comment marker is text, and
// inside a comment a quote and the other comment's markers are text.
// TestSANYReadsAsTheReaderDoes holds each of these against the pinned TLC.

type spanKind byte

const (
	codeSpan spanKind = iota
	stringSpan
	commentSpan
)

// A span is a run of a text that is all code, one string with its quotes, or
// one comment.
type span struct {
	start, end int
	kind       spanKind
}

// escapes are the characters SANY accepts after a backslash in a string.
const escapes = `"\tnfr`

// scan splits text into spans. A strict scan is of a module's body: it
// refuses a string that doesn't close on its line or holds an escape SANY
// doesn't know, and a comment that never closes, each of which SANY refuses
// too, and it stops where SANY stops reading, at the first ==== that is
// code, returning where that is. A scan that isn't strict is of a fragment,
// such as one definition: it ends an open string at the end of its line and
// an open comment at the end of the text, and returns the text's length.
func scan(text string, strict bool) ([]span, int, error) {
	var spans []span
	add := func(start, end int, kind spanKind) {
		if start < end {
			spans = append(spans, span{start, end, kind})
		}
	}
	code, i, n := 0, 0, len(text)
	for i < n {
		switch {
		case text[i] == '"':
			j, closed := i+1, false
			for j < n && text[j] != '\n' {
				if text[j] == '\\' && j+1 < n && text[j+1] != '\n' {
					if strict && !strings.ContainsRune(escapes, rune(text[j+1])) {
						return nil, 0, &scanError{j, fmt.Sprintf(`a string holds \%c; TLC accepts only \", \\, \t, \n, \f and \r after a backslash`, text[j+1])}
					}
					j += 2
					continue
				}
				if text[j] == '"' {
					closed = true
					break
				}
				j++
			}
			if closed {
				j++
			} else if strict {
				return nil, 0, &scanError{i, "a string must close on the line it opens on"}
			}
			add(code, i, codeSpan)
			add(i, j, stringSpan)
			i, code = j, j
		case text[i] == '(' && i+1 < n && text[i+1] == '*':
			j, depth := i+2, 1
			for j < n && depth > 0 {
				switch {
				case text[j] == '(' && j+1 < n && text[j+1] == '*':
					depth, j = depth+1, j+2
				case text[j] == '*' && j+1 < n && text[j+1] == ')':
					depth, j = depth-1, j+2
				default:
					j++
				}
			}
			if depth > 0 && strict {
				return nil, 0, &scanError{i, "a (* comment never closes"}
			}
			add(code, i, codeSpan)
			add(i, j, commentSpan)
			i, code = j, j
		case text[i] == '\\' && i+1 < n && text[i+1] == '*':
			j := i
			for j < n && text[j] != '\n' {
				j++
			}
			add(code, i, codeSpan)
			add(i, j, commentSpan)
			i, code = j, j
		case strict && strings.HasPrefix(text[i:], "===="):
			add(code, i, codeSpan)
			return spans, i, nil
		default:
			i++
		}
	}
	add(code, n, codeSpan)
	return spans, n, nil
}

// A scanError is what a strict scan refuses, and where.
type scanError struct {
	offset int
	msg    string
}

func (e *scanError) Error() string { return e.msg }

// blanked returns two copies of text, each as long as text: plain has every
// comment blanked, and code has every string's contents blanked as well, its
// quotes left in place. Blanking writes spaces and keeps newlines, so every
// token keeps its line and its column.
func blanked(text string, spans []span) (plain, code string) {
	p, c := []byte(text), []byte(text)
	blank := func(b []byte, from, to int) {
		for i := from; i < to; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for _, s := range spans {
		switch s.kind {
		case commentSpan:
			blank(p, s.start, s.end)
			blank(c, s.start, s.end)
		case stringSpan:
			end := s.end
			if end-s.start >= 2 && text[end-1] == '"' {
				end--
			}
			blank(c, s.start+1, end)
		}
	}
	return string(p), string(c)
}

// withoutComments blanks the comments of a fragment, keeping every other
// byte where it was.
func withoutComments(text string) string {
	spans, _, _ := scan(text, false)
	plain, _ := blanked(text, spans)
	return plain
}

// Canonical strips comments and collapses whitespace, so reformatting a
// definition leaves its hash alone but any change to its tokens does not. A
// string is kept as written: its spaces are part of what it says, and a
// comment marker inside it is text.
func Canonical(def string) string {
	spans, _, _ := scan(def, false)
	var b strings.Builder
	gap := false
	for _, s := range spans {
		switch s.kind {
		case commentSpan:
			gap = true
		case stringSpan:
			if gap && b.Len() > 0 {
				b.WriteByte(' ')
			}
			gap = false
			b.WriteString(def[s.start:s.end])
		default:
			for i := s.start; i < s.end; i++ {
				switch c := def[i]; c {
				case ' ', '\t', '\n', '\f', '\r':
					gap = true
				default:
					if gap && b.Len() > 0 {
						b.WriteByte(' ')
					}
					gap = false
					b.WriteByte(c)
				}
			}
		}
	}
	return b.String()
}
