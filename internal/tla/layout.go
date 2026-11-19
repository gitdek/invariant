package tla

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// A pin hashes a statement's canonical text, which collapses whitespace, and
// TLA+ reads a bulleted list of /\ or \/ by the columns its items start in.
// So the same tokens laid out differently can mean different things:
//
//	Inv ==
//	    /\ FALSE => FALSE
//	    /\ FALSE
//
// is (FALSE => FALSE) /\ FALSE, and Inv == /\ FALSE => FALSE /\ FALSE is
// FALSE => (FALSE /\ FALSE). A layout pin covers what the pin leaves out
// (#814): where each token is.

// Layout is a definition's layout: its text as written, with each comment
// blanked in place, the space at the end of each line trimmed, and the lines
// left blank dropped. Every token keeps its line, among the lines that hold
// one, and its column, so a comment can change without changing it, and a
// token can't move without changing it.
func Layout(def string) string {
	lines := layoutLines(def)
	text := make([]string, len(lines))
	for i, l := range lines {
		text[i] = l.Text
	}
	return strings.Join(text, "\n")
}

// A LayoutLine is one line of a definition's layout: its text, and its line
// in the definition as written, counting from zero.
type LayoutLine struct {
	Text string
	Line int
}

func layoutLines(def string) []LayoutLine {
	spans, _, _ := scan(def, false)
	plain, _ := blanked(def, spans)
	var lines []LayoutLine
	for i, line := range strings.Split(plain, "\n") {
		if line = strings.TrimRight(line, " \t"); line != "" {
			lines = append(lines, LayoutLine{Text: line, Line: i})
		}
	}
	return lines
}

// LayoutHash is a statement's layout pin: the SHA-256 of the layout of every
// definition in its closure, in the closure's order, as PinHash hashes their
// canonical texts. Two models with the same pin and the same layout pin give
// TLC the same tokens in the same places.
func (m *Model) LayoutHash(name string, stop map[string]bool) (string, error) {
	names, err := m.Closure(name, stop)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(names))
	for i, n := range names {
		def, err := m.Definition(n)
		if err != nil {
			return "", err
		}
		parts[i] = Layout(def)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// LayoutHash is Model.LayoutHash for a model with no modules beside it.
func LayoutHash(src, name string, stop map[string]bool) (string, error) {
	m, err := Read(src, nil)
	if err != nil {
		return "", err
	}
	return m.LayoutHash(name, stop)
}

// A Move is where a statement's layout first differs from the layout it was
// ratified with: the definition, the file that holds it ("" for the main
// module), the line, counting from one, and that line's layout now and as it
// was. A line one side doesn't have is empty on that side.
type Move struct {
	Definition string `json:"definition"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line"`
	Now        string `json:"now"`
	Ratified   string `json:"ratified"`
}

// String says where the move is, for a person or an agent to put it back:
// the definition, its file when it isn't the main module's, the line, and
// that line now and as ratified, each in backquotes, as written.
func (mv Move) String() string {
	where := "the definition of " + mv.Definition
	if mv.File != "" {
		where += " in " + mv.File
	}
	switch {
	case mv.Ratified == "":
		return fmt.Sprintf("%s, at line %d: `%s`, a line the ratified layout doesn't have", where, mv.Line, mv.Now)
	case mv.Now == "":
		return fmt.Sprintf("%s, which ends at line %d, where the ratified layout goes on with `%s`", where, mv.Line, mv.Ratified)
	}
	return fmt.Sprintf("%s, at line %d: `%s`, where the ratified layout has `%s`", where, mv.Line, mv.Now, mv.Ratified)
}

// FirstMove compares the layout of a statement's closure in m with its
// layout in ratified, the model as it was ratified, and returns where the
// first definition that differs, in the closure's order, first differs. It
// returns nil when every definition in the closure is laid out as ratified.
// A definition ratified doesn't have counts as moved at its first line.
func (m *Model) FirstMove(ratified *Model, name string, stop map[string]bool) (*Move, error) {
	names, err := m.Closure(name, stop)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		def, err := m.Definition(n)
		if err != nil {
			return nil, err
		}
		mod := m.owner[n]
		first := mod.units[mod.defs[n]].first
		now := layoutLines(def)
		var was []LayoutLine
		if old, err := ratified.Definition(n); err == nil {
			was = layoutLines(old)
		}
		for i := 0; i < len(now) || i < len(was); i++ {
			mv := Move{Definition: n, File: mod.file}
			switch {
			case i < len(now) && i < len(was) && now[i].Text == was[i].Text:
				continue
			case i < len(now):
				mv.Line, mv.Now = first+now[i].Line+1, now[i].Text
			default:
				mv.Line = first + now[len(now)-1].Line + 1
			}
			if i < len(was) {
				mv.Ratified = was[i].Text
			}
			return &mv, nil
		}
	}
	return nil, nil
}
