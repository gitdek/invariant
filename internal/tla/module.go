package tla

import (
	"fmt"
	"regexp"
	"strings"
)

// A module is read here the way SANY reads it, and a module this file can't
// read exactly is refused, so that the text a pin covers is the text TLC
// checks (#812). SANY ignores everything before a module's header and after
// its closing line, and it starts a new definition wherever its grammar
// allows one: indented, after a comment, or on the line of the one before.
// So a module is read only between its header, which must come first, and
// its closing line, and nothing after that line is read; every line at the
// left margin must start a unit this file knows; and a unit may not hold
// the start of another.

// Modules a model may extend without supplying them: the ones TLC ships whose
// operators mean one thing wherever they're extended. Naturals and Integers
// share their operators, and TestSANYReadsAsTheReaderDoes holds that TLC
// evaluates them alike. Reals, RealTime, Randomization and Toolbox are left
// out: TLC can't evaluate the first, the second declares a variable, and the
// third draws at random.
var standard = map[string]bool{"Naturals": true, "Integers": true, "Sequences": true, "FiniteSets": true, "Bags": true, "TLC": true}

// Module names TLC resolves itself or attaches its own Java code to, in the
// pinned release. A file of the model's with one of these names would be
// loaded in place of TLC's module, or have its operators replaced by TLC's
// code, so none may have one.
var shipped = map[string]bool{
	"AnySet": true, "Bags": true, "BuiltInModuleHelper": true, "FiniteSets": true, "Integers": true, "Naturals": true,
	"Randomization": true, "RealTime": true, "Reals": true, "Sequences": true, "Strings": true, "TLC": true,
	"TLCExt": true, "Toolbox": true, "TransitiveClosure": true,
}

// Words that start a unit of a module. One anywhere but the start of a line
// at the left margin would start a unit this file didn't see.
var unitWords = map[string]bool{
	"EXTENDS": true, "CONSTANT": true, "CONSTANTS": true, "VARIABLE": true, "VARIABLES": true, "RECURSIVE": true,
	"ASSUME": true, "ASSUMPTION": true, "AXIOM": true, "THEOREM": true, "LEMMA": true, "PROPOSITION": true,
	"COROLLARY": true, "INSTANCE": true, "LOCAL": true, "USE": true, "HIDE": true, "MODULE": true,
}

// keywords are TLA+'s reserved words, none of which can be a definition's
// name.
var keywords = func() map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.Fields(`ACTION ASSUME ASSUMPTION AXIOM BOOLEAN BY CASE CHOOSE CONSTANT CONSTANTS COROLLARY DEF
		DEFINE DEFS DOMAIN ELSE ENABLED EXCEPT EXTENDS FALSE HAVE HIDE IF IN INSTANCE LAMBDA LEMMA LET LOCAL MODULE NEW
		OBVIOUS OMITTED ONLY OTHER PICK PROOF PROPOSITION PROVE QED RECURSIVE STATE STRING SUBSET SUFFICES TAKE TEMPORAL
		THEN THEOREM TRUE UNCHANGED UNION USE VARIABLE VARIABLES WITH WITNESS`) {
		words[w] = true
	}
	return words
}()

type unitKind byte

const (
	definitionUnit unitKind = iota
	extendsUnit
	declarationUnit // CONSTANT(S), VARIABLE(S) and RECURSIVE
	assumptionUnit
	separatorUnit
)

// A unit is one top-level part of a module: a definition, a declaration, an
// assumption or a separator line.
type unit struct {
	kind  unitKind
	word  string // the keyword a unit that isn't a definition starts with
	name  string // the name a definition defines
	eq    int    // where on its first line a definition's == is
	first int    // its first line
	last  int    // its last line that holds code
	next  int    // the line the unit after it starts on
}

// A Module is one TLA+ module, read the way SANY reads it.
type Module struct {
	Name    string
	Extends []string // the modules it extends, in order

	file   string   // what to call it in a message: empty for a model's main module
	header int      // the header's line
	lines  []string // the module's lines, as written
	plain  []string // the lines with every comment blanked
	code   []string // the lines with every string's contents blanked as well
	units  []unit
	defs   map[string]int // a definition's name to its unit
}

var headerLine = regexp.MustCompile(`^-{4,}[ \t]*MODULE[ \t]+([A-Za-z0-9_]+)[ \t]*-{4,}[ \t]*$`)

// at starts a message about a line of the module. Lines count from one.
func (m *Module) at(line int) string {
	if m.file != "" {
		return fmt.Sprintf("%s line %d", m.file, line+1)
	}
	return fmt.Sprintf("line %d", line+1)
}

// ParseModule reads one module. It refuses whatever SANY would read
// differently from the text a pin is taken from.
func ParseModule(src string) (*Module, error) { return parseModule(src, "") }

func parseModule(src, file string) (*Module, error) {
	text := strings.ReplaceAll(src, "\r\n", "\n")
	m := &Module{file: file, lines: strings.Split(text, "\n"), defs: map[string]int{}}
	for i, line := range m.lines {
		for j := 0; j < len(line); j++ {
			if c := line[j]; (c < 0x20 && c != '\t') || c == 0x7f {
				return nil, fmt.Errorf("%s: a control character (%#02x). A model holds printable text, tabs and line ends only: TLC ends a comment at a lone carriage return, and what follows it would be read as code", m.at(i), c)
			}
		}
	}
	if err := m.readHeader(); err != nil {
		return nil, err
	}

	// Everything after the header is TLA+ to SANY, up to the first ==== that
	// is code, where it stops reading. Whatever follows that line is nothing
	// to SANY, and nothing here: the Toolbox keeps a file's history there.
	body := strings.Join(m.lines[m.header+1:], "\n")
	spans, end, err := scan(body, true)
	if err != nil {
		e := err.(*scanError)
		return nil, fmt.Errorf("%s: %s", m.at(m.header+1+strings.Count(body[:e.offset], "\n")), e.msg)
	}
	if end == len(body) {
		return nil, fmt.Errorf("the module has no closing ==== line")
	}
	closing := m.header + 1 + strings.Count(body[:end], "\n")
	if start := strings.LastIndexByte(body[:end], '\n') + 1; start != end || strings.Trim(strings.TrimRight(m.lines[closing], " \t"), "=") != "" {
		return nil, fmt.Errorf("%s: ==== closes the module, so it must be a line of its own at the left margin. TLC ignores whatever follows it", m.at(closing))
	}
	plain, code := blanked(body[:end], spans)
	blank := make([]string, m.header+1)
	m.plain = append(blank, strings.Split(plain, "\n")...)
	m.code = append(append([]string(nil), blank...), strings.Split(code, "\n")...)

	for i := m.header + 1; i < closing; i++ {
		line := m.code[i]
		for j := 0; j < len(line); j++ {
			if line[j] >= 0x80 {
				return nil, fmt.Errorf("%s: a character outside ASCII in the module's code. The pinned TLC reads none there; write the operator in ASCII, as == or \\in", m.at(i))
			}
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(m.units) == 0 || m.units[len(m.units)-1].kind == separatorUnit {
				return nil, fmt.Errorf("%s: an indented line that continues nothing. A definition or a declaration starts at the left margin", m.at(i))
			}
			m.units[len(m.units)-1].last = i
			continue
		}
		u, err := m.startUnit(i)
		if err != nil {
			return nil, err
		}
		if n := len(m.units); n > 0 {
			m.units[n-1].next = i
		}
		m.units = append(m.units, u)
	}
	if n := len(m.units); n > 0 {
		m.units[n-1].next = closing
	}
	for i := range m.units {
		u := &m.units[i]
		if err := m.checkUnit(u); err != nil {
			return nil, err
		}
		switch u.kind {
		case extendsUnit:
			if i != 0 {
				return nil, fmt.Errorf("%s: EXTENDS comes first in a module, right after its header", m.at(u.first))
			}
			for _, name := range m.items(u) {
				if !isIdentifier(name) {
					return nil, fmt.Errorf("%s: EXTENDS names modules, separated by commas; %q isn't a module's name", m.at(u.first), name)
				}
				m.Extends = append(m.Extends, name)
			}
		case definitionUnit:
			if first, twice := m.defs[u.name]; twice {
				return nil, fmt.Errorf("%s: %s is defined twice, here and on line %d", m.at(u.first), u.name, m.units[first].first+1)
			}
			m.defs[u.name] = i
		}
	}
	return m, nil
}

// readHeader finds the module's header, which must be its first line that
// isn't blank, and takes the module's name from it.
func (m *Module) readHeader() error {
	for m.header < len(m.lines) && strings.TrimSpace(m.lines[m.header]) == "" {
		m.header++
	}
	if m.header == len(m.lines) {
		return fmt.Errorf("no MODULE header")
	}
	head := headerLine.FindStringSubmatch(m.lines[m.header])
	if head == nil {
		if moduleHeader.MatchString(strings.Join(m.lines, "\n")) {
			return fmt.Errorf("%s: a module starts with its header, ---- MODULE Name ----, on a line of its own. TLC ignores whatever comes before the header, so nothing may", m.at(m.header))
		}
		return fmt.Errorf("no MODULE header")
	}
	m.Name = head[1]
	if !isIdentifier(m.Name) {
		return fmt.Errorf("%s: %q isn't a module name", m.at(m.header), m.Name)
	}
	return nil
}

// startUnit reads the start of the unit on line i, which holds code at the
// left margin.
func (m *Module) startUnit(i int) (unit, error) {
	line := m.code[i]
	u := unit{first: i, last: i}
	if strings.Trim(strings.TrimRight(line, " \t"), "-") == "" && len(strings.TrimRight(line, " \t")) >= 4 {
		u.kind = separatorUnit
		return u, nil
	}
	n := 0
	for n < len(line) && isWordByte(line[n]) {
		n++
	}
	word := line[:n]
	switch word {
	case "EXTENDS":
		u.kind, u.word = extendsUnit, word
		return u, nil
	case "CONSTANT", "CONSTANTS", "VARIABLE", "VARIABLES", "RECURSIVE":
		u.kind, u.word = declarationUnit, word
		return u, nil
	case "ASSUME", "ASSUMPTION", "AXIOM":
		u.kind, u.word = assumptionUnit, word
		return u, nil
	case "INSTANCE", "LOCAL":
		return u, fmt.Errorf("%s: %s. The gate can't pin what an INSTANCE brings into a module. Define the operators in this module, or put them in a module beside it and EXTEND that", m.at(i), word)
	case "THEOREM", "LEMMA", "PROPOSITION", "COROLLARY", "USE", "HIDE":
		return u, fmt.Errorf("%s: %s. TLC doesn't check a theorem, and the gate doesn't read one in a model. A proof goes in the project's proofs, beside the model", m.at(i), word)
	}
	// A definition: Name ==, Name(parameters) == or, for a function,
	// Name[x \in S] ==, its head on one line.
	rest := n
	for rest < len(line) && (line[rest] == ' ' || line[rest] == '\t') {
		rest++
	}
	if rest < len(line) && (line[rest] == '(' || line[rest] == '[') {
		open, shut := line[rest], byte(')')
		if open == '[' {
			shut = ']'
		}
		depth := 0
		for ; rest < len(line); rest++ {
			if line[rest] == open {
				depth++
			} else if line[rest] == shut {
				if depth--; depth == 0 {
					rest++
					break
				}
			}
		}
		for rest < len(line) && (line[rest] == ' ' || line[rest] == '\t') {
			rest++
		}
	}
	if !isIdentifier(word) || keywords[word] || !strings.HasPrefix(line[rest:], "==") {
		return u, fmt.Errorf("%s: this line starts at the left margin and isn't one the gate reads: a definition as Name == …, Name(x) == … or Name[x \\in S] == …, an EXTENDS, CONSTANT, VARIABLE, RECURSIVE or ASSUME, or a ---- separator. Indent a line that continues the one above it", m.at(i))
	}
	u.kind, u.name, u.eq = definitionUnit, word, rest
	return u, nil
}

// checkUnit refuses a unit that holds the start of another: SANY starts a
// definition at any == outside a LET, and a declaration at its keyword,
// wherever on a line they are.
func (m *Module) checkUnit(u *unit) error {
	if u.kind == separatorUnit {
		return nil
	}
	text := strings.Join(m.code[u.first:u.last+1], "\n")
	line := func(offset int) int { return u.first + strings.Count(text[:offset], "\n") }
	what := "the " + u.word + " that starts on line " + fmt.Sprint(u.first+1)
	if u.kind == definitionUnit {
		what = "the definition of " + u.name
	}
	depth, lets := 0, []int(nil)
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '=':
			j := i
			for j < len(text) && text[j] == '=' {
				j++
			}
			own := u.kind == definitionUnit && i == u.eq
			switch {
			case j-i == 2 && depth == 0 && !own:
				return fmt.Errorf("%s: a second definition inside %s. TLC reads a definition wherever one starts, so each must start a line of its own at the left margin, and a definition local to another goes in its LET", m.at(line(i)), what)
			case j-i > 2:
				return fmt.Errorf("%s: %s isn't an operator; a definition is written with ==", m.at(line(i)), text[i:j])
			}
			i = j
		case c == '-':
			j := i
			for j < len(text) && text[j] == '-' {
				j++
			}
			if j-i >= 4 {
				return fmt.Errorf("%s: a ---- separator inside %s. A separator is a line of its own at the left margin", m.at(line(i)), what)
			}
			i = j
		case isWordByte(c):
			j := i
			for j < len(text) && isWordByte(text[j]) {
				j++
			}
			word := text[i:j]
			switch {
			case unitWords[word] && !(i == 0 && word == u.word):
				return fmt.Errorf("%s: %s inside %s. It starts a part of a module of its own, so it must start a line at the left margin", m.at(line(i)), word, what)
			case word == "LET" || word == "IN":
				// SANY accepts a reserved word as a record's field, where it
				// opens and closes nothing.
				before, after := strings.TrimRight(text[:i], " \t\n"), strings.TrimLeft(text[j:], " \t\n")
				if (strings.HasSuffix(before, ".") && !strings.HasSuffix(before, "..")) || strings.HasPrefix(after, "|->") || strings.HasPrefix(after, ":") {
					return fmt.Errorf("%s: %s as a record's field. Name the field something that isn't a reserved word", m.at(line(i)), word)
				}
				if word == "LET" {
					depth, lets = depth+1, append(lets, i)
				} else if depth--; depth < 0 {
					return fmt.Errorf("%s: an IN with no LET before it in %s", m.at(line(i)), what)
				} else {
					lets = lets[:depth]
				}
			}
			i = j
		default:
			i++
		}
	}
	if depth != 0 {
		return fmt.Errorf("%s: a LET with no IN in %s", m.at(line(lets[len(lets)-1])), what)
	}
	return nil
}

// items is what a declaration lists after its keyword, between commas.
func (m *Module) items(u *unit) []string {
	text := strings.Join(m.code[u.first:u.last+1], " ")
	var items []string
	for _, item := range strings.Split(strings.TrimPrefix(text, u.word), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// text is a unit as written, from its first line through its last line that
// holds code and the indented or blank lines after that, less the space at
// its end.
func (m *Module) text(u *unit) string {
	end := u.last + 1
	for end < u.next && (strings.TrimSpace(m.lines[end]) == "" || m.lines[end][0] == ' ' || m.lines[end][0] == '\t') {
		end++
	}
	return strings.TrimRight(strings.Join(m.lines[u.first:end], "\n"), " \t\n")
}

// definitions names the module's definitions, in source order.
func (m *Module) definitions() []string {
	var names []string
	for _, u := range m.units {
		if u.kind == definitionUnit {
			names = append(names, u.name)
		}
	}
	return names
}

// variables names what the module's VARIABLE declarations declare.
func (m *Module) variables() []string {
	var names []string
	for i := range m.units {
		if u := &m.units[i]; u.kind == declarationUnit && strings.HasPrefix(u.word, "VARIABLE") {
			names = append(names, m.items(u)...)
		}
	}
	return names
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
