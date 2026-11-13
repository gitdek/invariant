// Package tla reads the parts of a TLA+ model that Invariant pins: top-level
// operator definitions, and what each depends on. It reads a module the way
// SANY, TLC's parser, reads it, and refuses a module it can't read exactly,
// so that the text a pin covers is the text TLC checks (#812).
package tla

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var moduleHeader = regexp.MustCompile(`-{4,}\s*MODULE\s+(\w+)\s*-{4,}`)

// A Model is a main module and the modules beside it that it extends, read
// as SANY reads them: one space of definitions.
type Model struct {
	Main    *Module
	modules []*Module          // each module after the ones it extends; Main last
	owner   map[string]*Module // a definition's name to the module that holds it
}

// Read reads a model: its main module's text, and the sibling modules beside
// it by file name, such as Helper.tla. A module may extend the standard
// modules TLC ships and the siblings supplied here, which are read the same
// way; their definitions join the main module's, as they do for SANY.
func Read(src string, includes map[string]string) (*Model, error) {
	return cachedRead(src, includes, true)
}

// alone reads one module by itself, without the modules it extends: enough
// to say what the module itself defines and declares, and never enough to
// take a pin.
func alone(src string) (*Model, error) { return cachedRead(src, nil, false) }

func cachedRead(src string, includes map[string]string, whole bool) (*Model, error) {
	key := cacheKey(src, includes, whole)
	cache.Lock()
	hit, ok := cache.models[key]
	cache.Unlock()
	if ok {
		return hit.model, hit.err
	}
	model, err := read(src, includes, whole)
	cache.Lock()
	if len(cache.models) >= 32 {
		cache.models = map[[32]byte]cached{}
	}
	cache.models[key] = cached{model, err}
	cache.Unlock()
	return model, err
}

// A gate run reads the same model once for each statement and dependency,
// so the last few models read are kept. A Model is never changed once read.
type cached struct {
	model *Model
	err   error
}

var cache = struct {
	sync.Mutex
	models map[[32]byte]cached
}{models: map[[32]byte]cached{}}

func cacheKey(src string, includes map[string]string, whole bool) [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%v:%d:%s", whole, len(src), src)
	names := make([]string, 0, len(includes))
	for name := range includes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(h, "%d:%s%d:%s", len(name), name, len(includes[name]), includes[name])
	}
	var key [32]byte
	h.Sum(key[:0])
	return key
}

func read(src string, includes map[string]string, whole bool) (*Model, error) {
	main, err := ParseModule(src)
	if err != nil {
		return nil, err
	}
	if !whole {
		m := &Model{Main: main, modules: []*Module{main}, owner: map[string]*Module{}}
		for _, name := range main.definitions() {
			m.owner[name] = main
		}
		return m, nil
	}
	if shipped[main.Name] {
		return nil, fmt.Errorf("a model can't be named %s: TLC has a module of that name, and would run its own code in place of this module's definitions", main.Name)
	}
	for name := range includes {
		stem := strings.TrimSuffix(name, ".tla")
		if shipped[stem] {
			return nil, fmt.Errorf("%s: TLC has a module named %s, and would load this file in its place. Give the module another name", name, stem)
		}
	}
	m := &Model{Main: main, owner: map[string]*Module{}}
	loaded, loading := map[string]bool{main.Name: true}, map[string]bool{main.Name: true}
	var load func(*Module) error
	load = func(mod *Module) error {
		for _, name := range mod.Extends {
			if standard[name] {
				continue
			}
			if loading[name] {
				return fmt.Errorf("%s extends %s, which extends it in turn", mod.Name, name)
			}
			if loaded[name] {
				continue
			}
			text, ok := includes[name+".tla"]
			switch {
			case !ok && shipped[name]:
				return fmt.Errorf("%s: EXTENDS %s. A model the gate pins may extend Naturals, Integers, Sequences, FiniteSets, Bags and TLC, and modules beside it", mod.at(mod.units[0].first), name)
			case !ok:
				return fmt.Errorf("%s: EXTENDS %s, and there's no %s.tla beside the model. A model may extend Naturals, Integers, Sequences, FiniteSets, Bags and TLC, and modules beside it", mod.at(mod.units[0].first), name, name)
			}
			sibling, err := parseModule(text, name+".tla")
			if err != nil {
				return err
			}
			if sibling.Name != name {
				return fmt.Errorf("%s.tla holds module %s; a module's file is named for it", name, sibling.Name)
			}
			loading[name] = true
			if err := load(sibling); err != nil {
				return err
			}
			loading[name], loaded[name] = false, true
			m.modules = append(m.modules, sibling)
		}
		return nil
	}
	if err := load(main); err != nil {
		return nil, err
	}
	m.modules = append(m.modules, main)
	for _, mod := range m.modules {
		for _, name := range mod.definitions() {
			if other, twice := m.owner[name]; twice {
				return nil, fmt.Errorf("%s is defined in both %s and %s", name, other.Name, mod.Name)
			}
			m.owner[name] = mod
		}
	}
	return m, nil
}

// Name is the main module's name.
func (m *Model) Name() string { return m.Main.Name }

// Definitions names the model's top-level definitions: those of the modules
// it extends, then its own, each in source order.
func (m *Model) Definitions() []string {
	var names []string
	for _, mod := range m.modules {
		names = append(names, mod.definitions()...)
	}
	return names
}

// Definition returns the text of the top-level definition of name: its line,
// Name == or Name(args) ==, and every line after it up to the next
// definition or declaration.
func (m *Model) Definition(name string) (string, error) {
	mod, ok := m.owner[name]
	if !ok {
		return "", fmt.Errorf("no top-level definition of %s", name)
	}
	return mod.text(&mod.units[mod.defs[name]]), nil
}

// Variables names the model's variables, from its VARIABLE declarations.
func (m *Model) Variables() []string {
	var names []string
	for _, mod := range m.modules {
		names = append(names, mod.variables()...)
	}
	return names
}

var (
	identifier    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	stringLiteral = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	subscript     = regexp.MustCompile(`(\]|>>|\bWF|\bSF)_`)
)

// Closure returns name and every top-level definition it depends on,
// directly or indirectly, sorted. It doesn't follow the names in stop,
// which belong to the factory's model.
func (m *Model) Closure(name string, stop map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(n string) error {
		if seen[n] {
			return nil
		}
		seen[n] = true
		def, err := m.Definition(n)
		if err != nil {
			return err
		}
		body := stringLiteral.ReplaceAllString(Canonical(def), " ")
		// In [A]_v, <<A>>_v, WF_v(A) and SF_v(A), the underscore is syntax, not
		// part of a name.
		body = subscript.ReplaceAllString(body, "$1 ")
		for _, ref := range identifier.FindAllString(body, -1) {
			if m.owner[ref] != nil && !stop[ref] && ref != n {
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
// redefining something it depends on, in the model or in a module it
// extends. For a statement with no dependencies it equals Hash of its
// definition.
func (m *Model) PinHash(name string, stop map[string]bool) (string, error) {
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
		parts[i] = Canonical(def)
		if division.MatchString(stringLiteral.ReplaceAllString(parts[i], " ")) && !m.extends("Integers") {
			return "", fmt.Errorf(`%s uses \div, and the model doesn't extend Integers. TLC rounds \div toward zero under Naturals and down under Integers when one side is negative, so what a pin covers may use \div only in a model that extends Integers, where it means one thing`, n)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// division is the one operator TLC evaluates differently by which of the
// modules that define it a model extends: its code for Naturals leaves a
// quotient of zero alone where Integers rounds it down to -1.
// TestSANYReadsAsTheReaderDoes holds that, and that the others agree.
var division = regexp.MustCompile(`\\div\b`)

// extends says whether the model, or a module it extends, extends name.
func (m *Model) extends(name string) bool {
	for _, mod := range m.modules {
		for _, e := range mod.Extends {
			if e == name {
				return true
			}
		}
	}
	return false
}

// ModuleName returns the name in the module's header line, which must be the
// first line that isn't blank. It reads nothing after the header.
func ModuleName(src string) (string, error) {
	m := &Module{lines: strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")}
	if err := m.readHeader(); err != nil {
		return "", err
	}
	return m.Name, nil
}

// Definition returns the text of the top-level definition of name in one
// module, read alone. See Model.Definition.
func Definition(src, name string) (string, error) {
	m, err := alone(src)
	if err != nil {
		return "", err
	}
	return m.Definition(name)
}

// Hash is the pin for a definition: the SHA-256 of its canonical text.
func Hash(def string) string {
	sum := sha256.Sum256([]byte(Canonical(def)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Definitions names the top-level definitions of one module, read alone, in
// source order. It names none for a module the gate refuses, which
// Definition, Closure and PinHash report.
func Definitions(src string) []string {
	m, err := alone(src)
	if err != nil {
		return nil
	}
	return m.Definitions()
}

// Closure is Model.Closure for a model with no modules beside it. A model
// that extends one is refused: its closure would miss what the module
// beside it defines.
func Closure(src, name string, stop map[string]bool) ([]string, error) {
	m, err := Read(src, nil)
	if err != nil {
		return nil, err
	}
	return m.Closure(name, stop)
}

// PinHash is Model.PinHash for a model with no modules beside it. A model
// that extends one is refused: its pin would miss what the module beside it
// defines.
func PinHash(src, name string, stop map[string]bool) (string, error) {
	m, err := Read(src, nil)
	if err != nil {
		return "", err
	}
	return m.PinHash(name, stop)
}

// PinHashWith is PinHash for a model with modules beside it: includes holds
// them by file name, as Read takes them.
func PinHashWith(src string, includes map[string]string, name string, stop map[string]bool) (string, error) {
	m, err := Read(src, includes)
	if err != nil {
		return "", err
	}
	return m.PinHash(name, stop)
}

// Variables names the variables one module declares, from its VARIABLE
// declarations. It names none for a module the gate refuses.
func Variables(src string) []string {
	m, err := alone(src)
	if err != nil {
		return nil
	}
	return m.Variables()
}

// ModelMarker opens the part of a skeleton the factory writes.
const ModelMarker = `\* ---------------------------------------------------------------------------
\* The model. The factory writes Init, one operator per action, and Next
\* here. Everything else in this module is pinned and must not change.
\* ---------------------------------------------------------------------------`

// Skeleton reduces a module to what's pinned: its header, its CONSTANT and
// VARIABLE declarations, and the definitions in keep, in source order, each
// with the comment lines directly above it. The definitions named in last,
// the spec first, which refer to the model, go after ModelMarker, where the
// factory adds the model, in that order.
func Skeleton(src string, keep map[string]bool, last []string) (string, error) {
	m, err := ParseModule(src)
	if err != nil {
		return "", err
	}
	isLast := map[string]bool{}
	for _, n := range last {
		isLast[n] = true
	}
	var decls, defs []string
	lastDefs := map[string][2]string{} // each definition, alone and with its comment
	for i := range m.units {
		u := &m.units[i]
		switch {
		case u.kind == declarationUnit && u.word != "RECURSIVE":
			end := u.last + 1
			for end < u.next && strings.HasPrefix(m.lines[end], " ") && strings.TrimSpace(m.lines[end]) != "" {
				end++
			}
			decls = append(decls, m.lines[u.first:end]...)
		case u.kind == definitionUnit && keep[u.name]:
			def := m.text(u)
			start := u.first
			for start > m.header+1 && strings.HasPrefix(m.lines[start-1], `\*`) {
				start--
			}
			text := strings.Join(append(append([]string{}, m.lines[start:u.first]...), def), "\n")
			if isLast[u.name] {
				lastDefs[u.name] = [2]string{def, text}
			} else {
				defs = append(defs, text)
			}
		}
	}
	var after []string
	for i, n := range last {
		def, ok := lastDefs[n]
		if !ok {
			return "", fmt.Errorf("no definition of %s", n)
		}
		// The spec follows the marker directly, as the prompt describes.
		if i == 0 {
			after = append(after, def[0])
		} else {
			after = append(after, def[1])
		}
	}
	var b strings.Builder
	b.WriteString(m.lines[m.header] + "\n")
	b.WriteString(strings.Join(decls, "\n") + "\n\n")
	b.WriteString(strings.Join(defs, "\n\n") + "\n\n")
	b.WriteString(ModelMarker + "\n\n")
	b.WriteString(strings.Join(after, "\n\n") + "\n")
	b.WriteString(strings.Repeat("=", 77) + "\n")
	return b.String(), nil
}
