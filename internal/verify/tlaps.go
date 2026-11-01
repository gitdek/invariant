package verify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

// TLAPS proves a ratified invariant of the model at every size, with the TLA+
// Proof System (#360). The proof is untrusted search: a coding agent writes
// it, and the gate alone accepts it. The gate writes the only theorem that
// counts itself, Spec => []Inv, from the ratified model and invariant, and a
// proof may only supply an inductive invariant and three first-order lemmas
// whose statements the gate fixes. The temporal step is the gate's own, by
// PTL, which LS4 decides.

// TLAPSProof is the TLA+ Proof System's result for one ratified invariant the
// manifest selects. It's evidence about the model alone, at every value of
// its constants, kept apart from TLC's check within the bounds and from the
// code's evidence: it says nothing about the code.
type TLAPSProof struct {
	Invariant string `json:"invariant"`
	// Theorem is the theorem the gate wrote, as tlapm checked it.
	Theorem string `json:"theorem"`
	Proved  bool   `json:"proved"`
	// Obligations is how many proof obligations tlapm generated. A proof
	// counts only when it proved or found trivial every one of them.
	Obligations int `json:"obligations"`
	// Provers counts the obligations each backend proved. Which one gets
	// there first depends on timing, so the fingerprint leaves it out.
	Provers map[string]int `json:"provers,omitempty"`
	// Model and Proof are the SHA-256 of the model module and of the
	// project's proof, as tlapm checked them. Init and Next aren't pinned,
	// so the proof is bound to the model's exact text.
	Model string `json:"model"`
	Proof string `json:"proof"`
	// Libraries are the TLAPS standard modules the proof extends, and Cites
	// the facts of theirs it uses. Those modules only state them: their
	// proofs are in companion modules tlapm doesn't check here.
	Libraries []string `json:"libraries,omitempty"`
	Cites     []string `json:"cites,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// TLAPSTool records the TLAPS sandbox a report's proofs ran in.
type TLAPSTool struct {
	Release string `json:"release"`
	// Version is the tlapm commit the sandbox reports, which must be the
	// pinned one.
	Version string `json:"version"`
	Tarball string `json:"tarball_sha256"`
	Recipe  string `json:"recipe"`
	// Backends is each backend's version, as tlapm reports it.
	Backends map[string]string `json:"backends,omitempty"`
	// Options is tlapm's command line, without the module.
	Options []string `json:"options"`
}

// tlapsOptions run tlapm so that a proof counts only as it stands: strict
// (an omitted or missing proof is an error), with the standard library ahead
// of any same-named module beside the model, with no fingerprints from an
// earlier run, and with the toolbox's machine-readable output.
var tlapsOptions = []string{"--strict", "--prefer-stdlib", "--nofp", "--cache-dir", "/tmp/tlacache", "--threads", "4", "--stretch", "3", "--toolbox", "0", "0"}

// tlapsTimeout is how long one proof may take, as long as Gobra gets.
const tlapsTimeout = 20 * time.Minute

// ErrTLAPSApplication refuses TLAPS proofs for existing Python applications,
// whose gate doesn't run them yet.
var ErrTLAPSApplication = errors.New("TLAPS proofs aren't supported for existing Python applications yet")

//go:embed tlaps_stdlib.txt
var tlapsStdlib string

// tlapsLibraries are the TLAPS standard modules a proof may extend, and the
// facts each one states, from the pinned tarball's stdlib.
var tlapsLibraries = func() map[string]map[string]bool {
	out := map[string]map[string]bool{
		"Naturals": {}, "Integers": {}, "Sequences": {}, "FiniteSets": {}, "Bags": {}, "Functions": {}, "Folds": {},
	}
	for _, line := range strings.Split(strings.TrimSpace(tlapsStdlib), "\n") {
		if module, name, ok := strings.Cut(line, " "); ok {
			if out[module] == nil {
				out[module] = map[string]bool{}
			}
			out[module][name] = true
		}
	}
	return out
}()

// tlapsTemporalRules are TLAPS's own temporal proof rules, which its module
// states without proof. The gate takes the only temporal step itself, so a
// proof has no use for them.
var tlapsTemporalRules = map[string]bool{
	"RuleINV1": true, "RuleINV2": true, "RuleInvImplication": true, "RuleSF1": true, "RuleSF2": true,
	"RuleStepSimulation": true, "RuleTLA1": true, "RuleTLA2": true, "RuleWF1": true, "RuleWF2": true,
}

// modelModules are the standard modules a model may extend for TLAPS: tlapm
// knows them, and they state nothing a proof could misuse.
var modelModules = map[string]bool{"Naturals": true, "Integers": true, "Reals": true, "Sequences": true, "FiniteSets": true, "Bags": true}

// soundness names the constructs tlapm's open soundness issues touch, which
// neither a model nor a proof may use (#360): a term tlapm doesn't translate
// under a binder proves false theorems (tlaplus/tlapm#309), overlapping CASE
// arms do under SMT (#304), and so do negative exponents (#324).
var soundness = map[string]string{
	"ENABLED":  "ENABLED, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	`\cdot`:    `\cdot, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)`,
	"[]":       "a temporal operator, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	"<>":       "a temporal operator, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	"~>":       "a temporal operator, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	"-+->":     "a temporal operator, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	`\EE`:      "temporal quantification, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	`\AA`:      "temporal quantification, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	"TEMPORAL": "a temporal declaration, which tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)",
	"CASE":     "a CASE expression, whose overlapping arms SMT proves unsoundly (tlaplus/tlapm#304)",
	"^":        "an exponent, which SMT proves unsoundly when it's negative (tlaplus/tlapm#324)",
}

// tlaToken is one token of TLA+ source.
type tlaToken struct {
	text   string
	str    bool // a string literal
	label  bool // a proof step label, such as <1>2.
	line   int  // 1-based
	offset int  // the byte offset where it starts
	first  bool // the first token on its line
}

// tlaSymbols are TLA+'s multi-character symbols, longest first, so each is
// read whole.
var tlaSymbols = []string{
	"-+->", "<=>", "|->", "\\/", "/\\", "==", "=>", "=<", "<=", ">=", "/=", "<<", ">>", "[]", "<>", "~>", "->", "<-",
	"..", "::", ":=", ":>", "@@", "|-", "-|", "|=", "=|", "<:", "++", "--", "**", "//", "^^", "%%", "$$", "##", "&&", "||",
}

var stepLabel = regexp.MustCompile(`^<(\d+|\*|\+)>[A-Za-z0-9_]*\.*`)

// lexTLA splits TLA+ source into tokens as tlapm reads them: a string is one
// token, and line comments and nested block comments are dropped, so a word
// in a comment or a string never counts and one outside always does.
func lexTLA(src string) ([]tlaToken, error) {
	var out []tlaToken
	line, lineStart := 1, true
	emit := func(t tlaToken) {
		t.line, t.first = line, lineStart
		out = append(out, t)
		lineStart = false
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line, lineStart = line+1, true
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
		case strings.HasPrefix(src[i:], `\*`):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "(*"):
			depth, start := 0, line
			for i < len(src) {
				switch {
				case strings.HasPrefix(src[i:], "(*"):
					depth, i = depth+1, i+2
				case strings.HasPrefix(src[i:], "*)"):
					depth, i = depth-1, i+2
				default:
					if src[i] == '\n' {
						line++
					}
					i++
				}
				if depth == 0 {
					break
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("line %d: a comment is never closed", start)
			}
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\n' {
					return nil, fmt.Errorf("line %d: a string runs past the end of its line", line)
				}
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("line %d: a string is never closed", line)
			}
			emit(tlaToken{text: src[i : j+1], str: true, offset: i})
			i = j + 1
		case lineStart && c == '<' && stepLabel.MatchString(src[i:]):
			l := stepLabel.FindString(src[i:])
			emit(tlaToken{text: l, label: true, offset: i})
			i += len(l)
		case c == '-' && strings.HasPrefix(src[i:], "----"), c == '=' && strings.HasPrefix(src[i:], "===="):
			j := i
			for j < len(src) && src[j] == c {
				j++
			}
			emit(tlaToken{text: src[i:j], offset: i})
			i = j
		case c == '\\' && i+1 < len(src) && isLetter(src[i+1]):
			j := i + 1
			for j < len(src) && isLetter(src[j]) {
				j++
			}
			emit(tlaToken{text: src[i:j], offset: i})
			i = j
		case isLetter(c) || c == '_' || isDigit(c):
			j := i
			for j < len(src) && (isLetter(src[j]) || isDigit(src[j]) || src[j] == '_') {
				j++
			}
			emit(tlaToken{text: src[i:j], offset: i})
			i = j
		default:
			n := 1
			for _, s := range tlaSymbols {
				if strings.HasPrefix(src[i:], s) {
					n = len(s)
					break
				}
			}
			emit(tlaToken{text: src[i : i+n], offset: i})
			i += n
		}
	}
	return out, nil
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// unsound says why tok is a construct a TLAPS proof can't rest on, if it is.
func unsound(tok tlaToken) string {
	if tok.str || tok.label {
		return ""
	}
	if why, ok := soundness[tok.text]; ok {
		return why
	}
	if strings.HasPrefix(tok.text, "^") {
		return soundness["^"]
	}
	if strings.HasPrefix(tok.text, "WF_") || strings.HasPrefix(tok.text, "SF_") {
		return "a fairness condition, a temporal formula tlapm abstracts unsoundly under a binder (tlaplus/tlapm#309)"
	}
	return ""
}

// setEnumBinders begin an expression whose own ":" ends a quantifier or
// CHOOSE, so a "\in" within it is that binder's bound, not a dropped set
// element.
var setEnumBinders = map[string]bool{`\E`: true, `\A`: true, `\EE`: true, `\AA`: true, `\exists`: true, `\forall`: true, "CHOOSE": true}

// tlapsSetEnum finds a set enumeration one of whose elements is a bare
// membership L \in R, and returns the line of that membership. tlapm's
// parser reads such an element as a bound and drops it, so it reads
// {e \in S} as the empty set and proves false theorems about it
// (tlaplus/tlapm#235). An element that carries its own top-level ":" (a
// comprehension {x \in S : p} or {e : x \in S}, or a quantifier) or that
// parenthesises the membership is read correctly, and is left alone.
func tlapsSetEnum(toks []tlaToken) (int, bool) {
	for i, t := range toks {
		if t.text != "{" || t.str || t.label {
			continue
		}
		// Walk this brace's own tokens (nest 0), splitting its elements at
		// nest-0 commas. An element is dropped when it holds a nest-0 "\in"
		// but no nest-0 ":" and doesn't begin with a binder.
		nest, line := 0, 0
		var hasColon, hasIn, startsBinder, first bool
		first = true
		dropped := func() (int, bool) { return line, hasIn && !hasColon && !startsBinder }
		for j := i + 1; j < len(toks); j++ {
			tok := toks[j]
			if nest == 0 && tok.text == "}" && !tok.str {
				if ln, bad := dropped(); bad {
					return ln, true
				}
				break
			}
			if nest == 0 && !tok.str && !tok.label {
				switch tok.text {
				case ",":
					if ln, bad := dropped(); bad {
						return ln, true
					}
					hasColon, hasIn, startsBinder, first = false, false, false, true
				case ":":
					hasColon = true
				case `\in`:
					if !hasIn {
						hasIn, line = true, tok.line
					}
				default:
					if first && setEnumBinders[tok.text] {
						startsBinder = true
					}
				}
			}
			switch tok.text {
			case "(", "[", "{", "<<":
				nest++
			case ")", "]", ">>", "}":
				nest--
			}
			if !(nest == 0 && tok.text == ",") {
				first = false
			}
		}
	}
	return 0, false
}

// tlapsModel checks the model a TLAPS proof rests on, and returns the
// standard modules it extends. Its spec must be the gate's, Init /\
// [][Next]_vars, and nothing else in it may use a construct tlapm's open
// soundness issues touch, add an assumption, or state a fact without a
// proof the gate checks.
func tlapsModel(src, specName string) error {
	spec, err := tla.Definition(src, specName)
	if err != nil {
		return err
	}
	if want := specName + " == Init /\\ [][Next]_vars"; tla.Canonical(spec) != want {
		return fmt.Errorf("the spec must be %s for a TLAPS proof", want)
	}
	start := strings.Index(src, spec)
	toks, err := lexTLA(src)
	if err != nil {
		return fmt.Errorf("the model: %v", err)
	}
	if line, bad := tlapsSetEnum(toks); bad {
		return fmt.Errorf("the model writes a set enumeration whose element is a membership at line %d, which tlapm reads as the empty set (tlaplus/tlapm#235)", line)
	}
	for i, tok := range toks {
		if tok.offset >= start && tok.offset < start+len(spec) || tok.str {
			continue
		}
		if why := unsound(tok); why != "" {
			return fmt.Errorf("the model uses %s at line %d", why, tok.line)
		}
		switch tok.text {
		case "AXIOM", "ASSUME", "ASSUMPTION":
			return fmt.Errorf("the model has an assumption at line %d, which no one ratified, so a proof can't rest on it", tok.line)
		case "THEOREM", "LEMMA", "PROPOSITION", "COROLLARY":
			return fmt.Errorf("the model states a fact at line %d, whose proof the gate doesn't check", tok.line)
		case "INSTANCE", "RECURSIVE":
			return fmt.Errorf("the model uses %s at line %d, which TLAPS proofs don't support yet", tok.text, tok.line)
		case "EXTENDS":
			for j := i + 1; j < len(toks); j++ {
				if toks[j].text == "," {
					continue
				}
				if j > i+1 && toks[j-1].text != "," {
					break
				}
				if !modelModules[toks[j].text] {
					return fmt.Errorf("the model extends %s, and a TLAPS proof may rest only on standard modules", toks[j].text)
				}
			}
		}
	}
	return nil
}

// tlapsBody is a proof the project holds, checked and placed in the module
// the gate writes.
type tlapsBody struct {
	module    string // the whole module tlapm checks
	name      string // its name
	theorem   string // the theorem the gate wrote
	libraries []string
	cites     []string
}

// tlapsBodyLines is how many lines the gate puts before a proof's own
// first line, so tlapm's line numbers map back to the proof.
const tlapsBodyLines = 2

// tlapsModule checks the proof of invariant inv in body and writes the module
// tlapm checks around it: the proof extends the ratified model and TLAPS,
// and the gate's theorem follows it. A proof may begin with an EXTENDS of
// TLAPS's standard modules, and must define IndInv_<inv> and prove three
// lemmas, whose statements are the gate's:
//
//	<inv>_Init    == Init => IndInv_<inv>
//	<inv>_Step    == IndInv_<inv> /\ [Next]_vars => IndInv_<inv>'
//	<inv>_Implies == IndInv_<inv> => <inv>
//
// Everything else in it is the proof's own definitions and lemmas, each
// proved: no axiom, assumption, constant, variable, instance, other module,
// omitted proof, or construct tlapm's open soundness issues touch.
func tlapsModule(model, specName, inv, body string) (tlapsBody, error) {
	toks, err := lexTLA(body)
	if err != nil {
		return tlapsBody{}, err
	}
	if line, bad := tlapsSetEnum(toks); bad {
		return tlapsBody{}, fmt.Errorf("line %d: the proof writes a set enumeration whose element is a membership, which tlapm reads as the empty set (tlaplus/tlapm#235)", line)
	}
	extends, rest := []string{}, toks
	if len(toks) > 0 && toks[0].text == "EXTENDS" {
		i := 1
		for i < len(toks) {
			extends = append(extends, toks[i].text)
			if i++; i < len(toks) && toks[i].text == "," {
				i++
				continue
			}
			break
		}
		rest = toks[i:]
	}
	b := tlapsBody{name: "TLAPS_" + inv}
	for _, m := range extends {
		if _, ok := tlapsLibraries[m]; !ok || m == "TLAPS" {
			return tlapsBody{}, fmt.Errorf("the proof extends %s, and may extend only TLAPS's standard modules", m)
		}
		if !slices.Contains(b.libraries, m) {
			b.libraries = append(b.libraries, m)
		}
	}
	// defined collects every name the proof introduces, so a local one that
	// happens to match a library fact isn't recorded as citing it. A plain
	// definition is "<name> ==" with the name first on its line; a named
	// lemma or theorem is "<kind> <name> ==". Both are gathered up front, so
	// the citation scan sees them whatever order they appear in.
	defKinds := map[string]bool{"LEMMA": true, "THEOREM": true, "PROPOSITION": true, "COROLLARY": true}
	defined := map[string]bool{}
	for i, tok := range rest {
		if tok.str || tok.label {
			continue
		}
		switch {
		case tok.text == "==" && i > 0 && rest[i-1].first:
			defined[rest[i-1].text] = true
		case defKinds[tok.text] && tok.first && i+2 < len(rest) && rest[i+2].text == "==":
			defined[rest[i+1].text] = true
		}
	}
	for i, tok := range rest {
		prev := ""
		if i > 0 {
			prev = rest[i-1].text
		}
		if why := unsound(tok); why != "" && !(tok.text == "CASE" && i > 0 && rest[i-1].label) {
			return tlapsBody{}, fmt.Errorf("line %d: the proof uses %s", tok.line, why)
		}
		if tok.str || tok.label {
			continue
		}
		switch tok.text {
		case "AXIOM", "ASSUMPTION":
			return tlapsBody{}, fmt.Errorf("line %d: the proof adds an assumption, %s", tok.line, tok.text)
		case "ASSUME":
			// ASSUME ... PROVE states a lemma or a step, which is proved.
			// Anywhere else it's an assumption.
			after := map[string]bool{"LEMMA": true, "THEOREM": true, "PROPOSITION": true, "COROLLARY": true, "SUFFICES": true}
			lemma := prev == "==" && i >= 3 && after[rest[i-3].text]
			if !after[prev] && !lemma && !(i > 0 && rest[i-1].label) {
				return tlapsBody{}, fmt.Errorf("line %d: the proof adds an assumption, ASSUME", tok.line)
			}
		case "CONSTANT", "CONSTANTS", "VARIABLE", "VARIABLES", "PARAMETER", "PARAMETERS":
			if prev != "NEW" {
				return tlapsBody{}, fmt.Errorf("line %d: the proof declares %s, which the ratified model doesn't have", tok.line, strings.ToLower(tok.text))
			}
		case "OMITTED":
			return tlapsBody{}, fmt.Errorf("line %d: the proof omits a proof", tok.line)
		case "INSTANCE", "LOCAL", "RECURSIVE", "MODULE", "EXTENDS":
			return tlapsBody{}, fmt.Errorf("line %d: the proof uses %s, which a proof the gate accepts can't", tok.line, tok.text)
		case "PTL", "LS4":
			// The temporal backend names the atoms it decides by 20 bits of
			// a hash it never checks for collision, which proves false
			// theorems (tlaplus/tlapm#310). The gate takes the only temporal
			// step itself, on a fixed obligation whose atoms are the ratified
			// model's and invariant's, so a proof never drives that backend.
			return tlapsBody{}, fmt.Errorf("line %d: the proof names the %s backend, which the gate alone takes (tlaplus/tlapm#310)", tok.line, tok.text)
		}
		switch {
		case strings.HasPrefix(tok.text, "----") || strings.HasPrefix(tok.text, "===="):
			return tlapsBody{}, fmt.Errorf("line %d: the proof is the body of the module the gate writes, so it has no module header or end", tok.line)
		case tlapsTemporalRules[tok.text]:
			return tlapsBody{}, fmt.Errorf("line %d: the proof cites %s, a temporal rule TLAPS states without proof; the gate takes the only temporal step itself", tok.line, tok.text)
		}
		for _, m := range append([]string{"TLAPS"}, b.libraries...) {
			if fact := m + " " + tok.text; tlapsLibraries[m][tok.text] && !defined[tok.text] && !slices.Contains(b.cites, fact) {
				b.cites = append(b.cites, fact)
			}
		}
	}
	ind := "IndInv_" + inv
	if !defined[ind] {
		return tlapsBody{}, fmt.Errorf("the proof doesn't define %s, the inductive invariant its lemmas use", ind)
	}
	for _, l := range []struct{ name, statement string }{
		{inv + "_Init", "Init => " + ind},
		{inv + "_Step", ind + " /\\ [Next]_vars => " + ind + "'"},
		{inv + "_Implies", ind + " => " + inv},
	} {
		if err := tlapsLemma(rest, l.name, l.statement); err != nil {
			return tlapsBody{}, err
		}
	}
	// The proof keeps its own lines: its EXTENDS clause becomes blank, so
	// line n of the proof is line n+2 of the module.
	text := []byte(body)
	if n := len(toks) - len(rest); n > 0 {
		last := toks[n-1]
		for i := toks[0].offset; i < last.offset+len(last.text); i++ {
			if text[i] != '\n' {
				text[i] = ' '
			}
		}
	}
	imports := append([]string{model, "TLAPS"}, b.libraries...)
	b.theorem = fmt.Sprintf("THEOREM %s_AtEverySize == %s => []%s", inv, specName, inv)
	b.module = fmt.Sprintf("---- MODULE %s ----\nEXTENDS %s\n%s\n%s\nBY %s_Init, %s_Step, %s_Implies, PTL DEF %s\n====\n",
		b.name, strings.Join(imports, ", "), strings.TrimRight(string(text), "\n"), b.theorem, inv, inv, inv, specName)
	sort.Strings(b.cites)
	return b, nil
}

// proofStart are the words a lemma's proof can begin with, besides a step
// label.
var proofStart = map[string]bool{"BY": true, "OBVIOUS": true, "PROOF": true}

// tlapsLemma checks that toks state the lemma name exactly as the gate does,
// and give it a proof.
func tlapsLemma(toks []tlaToken, name, statement string) error {
	want, _ := lexTLA(statement)
	kinds := map[string]bool{"LEMMA": true, "THEOREM": true, "PROPOSITION": true, "COROLLARY": true}
	for i := 0; i+2 < len(toks); i++ {
		if !kinds[toks[i].text] || toks[i+1].text != name || toks[i+2].text != "==" {
			continue
		}
		// The statement runs to its proof, or, when it has none, to the next
		// unit: another lemma, a USE or HIDE, or a definition.
		j := i + 3
		var got []string
		for j < len(toks) && !toks[j].label && !proofStart[toks[j].text] && !kinds[toks[j].text] &&
			toks[j].text != "USE" && toks[j].text != "HIDE" && !(toks[j].first && j+1 < len(toks) && toks[j+1].text == "==") {
			got = append(got, toks[j].text)
			j++
		}
		var w []string
		for _, t := range want {
			w = append(w, t.text)
		}
		if !slices.Equal(got, w) {
			return fmt.Errorf("lemma %s states %q; the gate's statement is %q", name, strings.Join(got, " "), statement)
		}
		if j == len(toks) || !toks[j].label && !proofStart[toks[j].text] {
			return fmt.Errorf("lemma %s has no proof", name)
		}
		return nil
	}
	return fmt.Errorf("the proof doesn't prove lemma %s == %s", name, statement)
}

// tlapsRun is what tlapm reported for one module.
type tlapsRun struct {
	exit        int
	count       int            // the obligations it generated
	status      map[int]string // each obligation's final status
	prover      map[int]string // and the backend that settled it
	errors      []string       // what it reported as errors
	versionLine string         // the version it reports
	output      string
}

// parseTLAPS reads tlapm's toolbox output.
func parseTLAPS(out string) tlapsRun {
	r := tlapsRun{status: map[int]string{}, prover: map[int]string{}, count: -1, output: out}
	var block map[string]string
	scan := bufio.NewScanner(strings.NewReader(out))
	scan.Buffer(make([]byte, 1<<20), 1<<24)
	for scan.Scan() {
		line := scan.Text()
		switch {
		case line == "@!!BEGIN":
			block = map[string]string{}
		case line == "@!!END":
			switch block["type"] {
			case "obligationsnumber":
				r.count, _ = strconv.Atoi(block["count"])
			case "obligation":
				if id, err := strconv.Atoi(block["id"]); err == nil {
					r.status[id] = block["status"]
					if block["prover"] != "" {
						r.prover[id] = block["prover"]
					}
				}
			case "error":
				// A parse or backend error. Strict mode also exits non-zero,
				// but the block alone fails the proof, whatever the
				// obligations say.
				if msg := strings.TrimSpace(block["msg"]); msg != "" {
					r.errors = append(r.errors, msg)
				} else {
					r.errors = append(r.errors, "tlapm reported an error")
				}
			}
			block = nil
		case block != nil && strings.HasPrefix(line, "@!!"):
			if k, v, ok := strings.Cut(line[3:], ":"); ok {
				block[k] = v
			}
		case strings.HasPrefix(line, `\* TLAPM version `):
			r.versionLine = strings.TrimSpace(strings.TrimPrefix(line, `\* TLAPM version `))
		case strings.HasPrefix(line, "[ERROR]"), strings.HasPrefix(line, "Missing proof"), strings.HasPrefix(line, "Omitted proof"):
			r.errors = append(r.errors, strings.TrimSpace(line))
		}
	}
	return r
}

// proved says whether tlapm proved everything it generated, with nothing
// missing, omitted or failed.
func (r tlapsRun) proved() (bool, string) {
	switch {
	case r.exit != 0 && len(r.errors) == 0:
		return false, fmt.Sprintf("tlapm exited with status %d", r.exit)
	case len(r.errors) > 0:
		return false, strings.Join(r.errors, "; ")
	case r.count <= 0:
		return false, "tlapm generated no obligations"
	case len(r.status) != r.count:
		return false, fmt.Sprintf("tlapm reported %d of its %d obligations", len(r.status), r.count)
	}
	var failed []string
	for id, s := range r.status {
		if s != "proved" && s != "trivial" {
			failed = append(failed, fmt.Sprintf("obligation %d %s", id, s))
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return false, strings.Join(failed, "; ")
	}
	return true, ""
}

// tlapsConfig is the pinned sandbox's own report of its tools, read once per
// image.
var tlapsConfig sync.Map

// tlapsTool reads the sandbox's configuration, and refuses one that isn't
// the pinned tlapm.
func tlapsTool(ctx context.Context, image string) (*TLAPSTool, error) {
	if t, ok := tlapsConfig.Load(image); ok {
		return t.(*TLAPSTool), nil
	}
	cmd := toolchain.Docker(ctx, "run", "--rm", "--platform", "linux/amd64", "--network", "none", image, "--config")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("reading the TLAPS sandbox's configuration: %w\n%s", err, out.String())
	}
	t := &TLAPSTool{Release: toolchain.TLAPSRelease, Tarball: "sha256:" + toolchain.TLAPSTarballSHA256, Recipe: toolchain.TLAPSRecipe(),
		Backends: map[string]string{}, Options: append([]string(nil), tlapsOptions...)}
	versions := regexp.MustCompile(`^(version|Isabelle version|zenon version|Z3 version|LS4 version) == "(.*)"$`)
	for _, line := range strings.Split(out.String(), "\n") {
		m := versions.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		switch m[1] {
		case "version":
			t.Version = m[2]
		case "Isabelle version":
			t.Backends["isabelle"] = m[2]
		case "zenon version":
			t.Backends["zenon"] = strings.TrimPrefix(m[2], "zenon version ")
		case "Z3 version":
			t.Backends["z3"] = strings.TrimPrefix(m[2], "Z3 version ")
		case "LS4 version":
			t.Backends["ls4"] = m[2]
		}
	}
	if t.Version != toolchain.TLAPSVersion {
		return nil, fmt.Errorf("the TLAPS sandbox reports tlapm %q, not the pinned %s", t.Version, toolchain.TLAPSVersion)
	}
	tlapsConfig.Store(image, t)
	return t, nil
}

// proveTLAPS proves each invariant the manifest selects, in its own staged
// copy of the model, one at a time.
func proveTLAPS(ctx context.Context, work string, p *project.Project, src string) ([]TLAPSProof, *TLAPSTool, error) {
	model := sha(src)
	var proofs []TLAPSProof
	modelErr := tlapsModel(src, p.SpecName())
	image, tool := "", (*TLAPSTool)(nil)
	for _, inv := range p.Manifest.TLAPS {
		out := TLAPSProof{Invariant: inv, Model: model}
		proofs = append(proofs, out)
		at := &proofs[len(proofs)-1]
		body, err := os.ReadFile(p.TLAPSProofPath(inv))
		if err != nil {
			at.Message = fmt.Sprintf("no proof at %s", filepath.ToSlash(filepath.Join(".invariant", "proofs", inv+".tla")))
			continue
		}
		at.Proof = sha(string(body))
		if modelErr != nil {
			at.Message = modelErr.Error()
			continue
		}
		b, err := tlapsModule(p.ModuleName(), p.SpecName(), inv, string(body))
		if err != nil {
			at.Message = err.Error()
			continue
		}
		at.Theorem, at.Libraries, at.Cites = b.theorem, b.libraries, b.cites
		if image == "" {
			if image, err = toolchain.TLAPSImage(ctx); err != nil {
				return nil, nil, err
			}
			if tool, err = tlapsTool(ctx, image); err != nil {
				return nil, nil, err
			}
		}
		d, err := stage(work, "tlaps-"+inv, p, src, b.module)
		if err != nil {
			return nil, nil, err
		}
		run, err := runTLAPS(ctx, image, d, b.name)
		if err != nil {
			return nil, nil, err
		}
		at.Obligations = max(run.count, 0)
		at.Proved, at.Message = run.proved()
		at.Message = tlapsLines(at.Message)
		if at.Proved {
			at.Provers = map[string]int{}
			for id, status := range run.status {
				prover := run.prover[id]
				if status == "trivial" {
					prover = "trivial"
				}
				at.Provers[prover]++
			}
		}
	}
	return proofs, tool, nil
}

// tlapsLines maps tlapm's line numbers in the module the gate wrote back to
// the proof's own.
func tlapsLines(msg string) string {
	return regexp.MustCompile(`line (\d+)`).ReplaceAllStringFunc(msg, func(s string) string {
		n, _ := strconv.Atoi(strings.TrimPrefix(s, "line "))
		if n > tlapsBodyLines {
			return fmt.Sprintf("line %d", n-tlapsBodyLines)
		}
		return s
	})
}

// runTLAPS runs tlapm on module, staged in dir, offline, read-only and
// within tlapsTimeout.
func runTLAPS(ctx context.Context, image, dir, module string) (tlapsRun, error) {
	ctx, cancel := context.WithTimeout(ctx, tlapsTimeout)
	defer cancel()
	args := []string{"run", "--rm", "--platform", "linux/amd64", "--network", "none", "--tmpfs", "/tmp", "-v", dir + ":/work:ro", "-w", "/work", image}
	args = append(append(args, tlapsOptions...), module+".tla")
	cmd := toolchain.Docker(ctx, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		r := parseTLAPS(out.String())
		r.exit, r.errors = -1, append(r.errors, fmt.Sprintf("tlapm didn't finish within %s", tlapsTimeout))
		return r, nil
	}
	r := parseTLAPS(out.String())
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		r.exit = exit.ExitCode()
	case err != nil:
		return r, fmt.Errorf("running tlapm: %w\n%s", err, out.String())
	}
	if r.versionLine != "" && r.versionLine != toolchain.TLAPSVersion {
		r.errors = append(r.errors, fmt.Sprintf("tlapm reports version %s, not the pinned %s", r.versionLine, toolchain.TLAPSVersion))
	}
	return r, nil
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}
