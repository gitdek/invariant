package tla

import (
	"fmt"
	"regexp"
	"strings"
)

// A Step is one of the named steps a model's Next takes. In
// `\E r \in RM : TMRcvPrepared(r)` the step is TMRcvPrepared, its argument
// is r, and it sits under the binder r \in RM: one step for each r in RM.
type Step struct {
	Name    string
	Args    []string // the arguments Next passes it, as TLA+ expressions
	Binders []Binder // the \E it sits under, outermost first
}

// A Binder is one variable an \E binds, and the set it ranges over.
type Binder struct {
	Var    string
	Domain string // a TLA+ expression
}

// Steps reads the named steps from a model's Next. Next must be a
// disjunction of named steps, each call optionally under \E: bulleted lists
// of \/, infix \/, parentheses and \E, nested any way (D-0085). Anything
// else, such as a conjunction or an IF, is an error, so a driver's steps can
// always be named the way Next names them.
func Steps(src string) ([]Step, error) {
	def, err := Definition(src, "Next")
	if err != nil {
		return nil, err
	}
	toks := tokenize(def)
	// Drop the head, `Next ==`.
	for i, t := range toks {
		if t.text == "==" {
			toks = toks[i+1:]
			break
		}
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("Next is empty")
	}
	var steps []Step
	if err := disjuncts(toks, nil, &steps); err != nil {
		return nil, fmt.Errorf("Next must be a disjunction of named steps: %w", err)
	}
	return steps, nil
}

// token is one TLA+ token, and where it starts. first says it's the first
// token on its line, which is what makes a \/ a bullet.
type token struct {
	text      string
	line, col int
	first     bool
}

// tokenPattern keeps multi-character operators such as .. and :> whole, so
// an expression joined back from its tokens means what it meant.
var tokenPattern = regexp.MustCompile(`\\\*.*|"(?:[^"\\]|\\.)*"|<<|>>|\\/|/\\|\\[A-Za-z]+|[A-Za-z_][A-Za-z0-9_]*|[0-9]+|[(){}\[\],]|[-+*/<>=:|@#.~&^$!?%]+|\S`)

// tokenize splits text into tokens, dropping comments. Block comments are
// blanked out first, byte for byte, so every token keeps its column.
func tokenize(text string) []token {
	text = blockComment.ReplaceAllStringFunc(strings.ReplaceAll(text, "\r\n", "\n"), func(c string) string {
		b := []byte(c)
		for i := range b {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
	var out []token
	for n, line := range strings.Split(text, "\n") {
		first := true
		for _, loc := range tokenPattern.FindAllStringIndex(line, -1) {
			t := line[loc[0]:loc[1]]
			if strings.HasPrefix(t, `\*`) {
				break
			}
			out = append(out, token{text: t, line: n, col: loc[0], first: first})
			first = false
		}
	}
	return out
}

// disjuncts adds the steps in toks, a disjunction under binders, to steps.
func disjuncts(toks []token, binders []Binder, steps *[]Step) error {
	toks = unwrap(toks)
	if len(toks) == 0 {
		return fmt.Errorf("an empty disjunct")
	}
	// A bulleted list: items start at each \/ in the first bullet's column
	// that's the first token on its line.
	if toks[0].text == `\/` {
		col := toks[0].col
		start := 1
		for i := 1; i <= len(toks); i++ {
			if i < len(toks) && !(toks[i].text == `\/` && toks[i].first && toks[i].col == col && depthAt(toks, i) == 0) {
				continue
			}
			if err := disjuncts(toks[start:i], binders, steps); err != nil {
				return err
			}
			start = i + 1
		}
		return nil
	}
	// Infix \/ at depth zero splits the rest, before any \E, since an \E
	// reaches as far right as it can.
	if toks[0].text != `\E` {
		if parts := splitTop(toks, `\/`); len(parts) > 1 {
			for _, p := range parts {
				if err := disjuncts(p, binders, steps); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if toks[0].text == `\E` {
		colon := indexTop(toks, ":")
		if colon < 0 {
			return fmt.Errorf(`an \E with no ":"`)
		}
		bound := append([]Binder(nil), binders...)
		for _, group := range splitTop(toks[1:colon], ",") {
			in := indexTop(group, `\in`)
			if in != 1 || !isIdentifier(group[0].text) || len(group) < 3 {
				return fmt.Errorf(`an \E binds %q; want "x \in S"`, joinTokens(group))
			}
			bound = append(bound, Binder{Var: group[0].text, Domain: joinTokens(group[2:])})
		}
		return disjuncts(toks[colon+1:], bound, steps)
	}
	// A single named step: Name, or Name(args).
	if !isIdentifier(toks[0].text) || reserved[toks[0].text] {
		return fmt.Errorf("%q isn't a named step", joinTokens(toks))
	}
	s := Step{Name: toks[0].text, Binders: binders}
	if len(toks) > 1 {
		if toks[1].text != "(" || toks[len(toks)-1].text != ")" || closing(toks, 1) != len(toks)-1 {
			return fmt.Errorf("%q isn't a named step", joinTokens(toks))
		}
		for _, arg := range splitTop(toks[2:len(toks)-1], ",") {
			if len(arg) == 0 {
				return fmt.Errorf("%q has an empty argument", joinTokens(toks))
			}
			s.Args = append(s.Args, joinTokens(arg))
		}
	}
	*steps = append(*steps, s)
	return nil
}

// reserved are words that start an expression that isn't a named step.
var reserved = map[string]bool{"IF": true, "LET": true, "CASE": true, "UNCHANGED": true, "TRUE": true, "FALSE": true, "ENABLED": true}

// unwrap strips parentheses around the whole of toks.
func unwrap(toks []token) []token {
	for len(toks) >= 2 && toks[0].text == "(" && closing(toks, 0) == len(toks)-1 {
		toks = toks[1 : len(toks)-1]
	}
	return toks
}

var opens = map[string]string{"(": ")", "[": "]", "{": "}", "<<": ">>"}

// closing is the index of the bracket that closes the one at i, or -1.
func closing(toks []token, i int) int {
	depth := 0
	for j := i; j < len(toks); j++ {
		switch t := toks[j].text; {
		case opens[t] != "":
			depth++
		case t == ")" || t == "]" || t == "}" || t == ">>":
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// depthAt is how deep in brackets toks[i] sits.
func depthAt(toks []token, i int) int {
	depth := 0
	for j := 0; j < i; j++ {
		switch t := toks[j].text; {
		case opens[t] != "":
			depth++
		case t == ")" || t == "]" || t == "}" || t == ">>":
			depth--
		}
	}
	return depth
}

// splitTop splits toks at sep where it sits outside every bracket.
func splitTop(toks []token, sep string) [][]token {
	var parts [][]token
	depth, start := 0, 0
	for i, t := range toks {
		switch {
		case opens[t.text] != "":
			depth++
		case t.text == ")" || t.text == "]" || t.text == "}" || t.text == ">>":
			depth--
		case depth == 0 && t.text == sep:
			parts = append(parts, toks[start:i])
			start = i + 1
		}
	}
	return append(parts, toks[start:])
}

// indexTop is the index of the first sep outside every bracket, or -1.
func indexTop(toks []token, sep string) int {
	depth := 0
	for i, t := range toks {
		switch {
		case opens[t.text] != "":
			depth++
		case t.text == ")" || t.text == "]" || t.text == "}" || t.text == ">>":
			depth--
		case depth == 0 && t.text == sep:
			return i
		}
	}
	return -1
}

func joinTokens(toks []token) string {
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.text
	}
	return strings.Join(parts, " ")
}

var identifierOnly = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isIdentifier(s string) bool { return identifierOnly.MatchString(s) }
