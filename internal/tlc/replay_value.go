package tlc

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// ReplayValue is lossless executable data. The display parser deliberately
// remains separate: a model value is not a string, and a set is not a sequence.
type ReplayValue struct {
	Kind     string        `json:"kind"`
	Atom     string        `json:"atom,omitempty"`
	Elements []ReplayValue `json:"elements,omitempty"`
	Entries  []ReplayEntry `json:"entries,omitempty"`
}

type ReplayEntry struct {
	Key   ReplayValue `json:"key"`
	Value ReplayValue `json:"value"`
}

func ParseReplayValue(text string) (ReplayValue, error) {
	p := replayValueParser{valueParser: valueParser{s: text}}
	v, err := p.value(0)
	if err != nil {
		return ReplayValue{}, err
	}
	p.space()
	if p.i != len(p.s) {
		return ReplayValue{}, p.errorf("unexpected %q", p.s[p.i:])
	}
	return v.canonical(0)
}

type replayValueParser struct{ valueParser }

func (p *replayValueParser) value(depth int) (ReplayValue, error) {
	if depth > 128 {
		return ReplayValue{}, p.errorf("value nesting is too deep")
	}
	p.space()
	v := ReplayValue{}
	switch {
	case p.eat("<<"):
		v.Kind = "sequence"
		x, err := p.elements(">>", depth)
		v.Elements = x
		return v, err
	case p.eat("{"):
		v.Kind = "set"
		x, err := p.elements("}", depth)
		v.Elements = x
		return v, err
	case p.eat("["):
		v.Kind = "record"
		p.space()
		if p.eat("]") {
			return v, nil
		}
		for {
			p.space()
			k := p.ident()
			p.space()
			if k == "" || !p.eat("|->") {
				return v, p.errorf("expected field |-> value")
			}
			x, err := p.value(depth + 1)
			if err != nil {
				return v, err
			}
			v.Entries = append(v.Entries, ReplayEntry{Key: ReplayValue{Kind: "string", Atom: k}, Value: x})
			p.space()
			if p.eat("]") {
				return v, nil
			}
			if !p.eat(",") {
				return v, p.errorf("expected , or ]")
			}
		}
	case p.eat("("):
		v.Kind = "function"
		for {
			k, err := p.value(depth + 1)
			if err != nil {
				return v, err
			}
			p.space()
			if !p.eat(":>") {
				return v, p.errorf("expected :>")
			}
			x, err := p.value(depth + 1)
			if err != nil {
				return v, err
			}
			v.Entries = append(v.Entries, ReplayEntry{Key: k, Value: x})
			p.space()
			if p.eat(")") {
				return v, nil
			}
			if !p.eat("@@") {
				return v, p.errorf("expected @@ or )")
			}
		}
	case p.eat("\""):
		v.Kind = "string"
		var b strings.Builder
		for p.i < len(p.s) {
			c := p.s[p.i]
			p.i++
			if c == '"' {
				v.Atom = b.String()
				return v, nil
			}
			if c < 32 {
				return v, p.errorf("unescaped control in string")
			}
			if c == '\\' {
				if p.i == len(p.s) {
					break
				}
				c = p.s[p.i]
				p.i++
				switch c {
				case 'n':
					c = '\n'
				case 't':
					c = '\t'
				case 'r':
					c = '\r'
				case 'f':
					c = '\f'
				case '"', '\\':
				default:
					return v, p.errorf("unknown string escape")
				}
			}
			b.WriteByte(c)
		}
		return v, p.errorf("unterminated string")
	case p.i < len(p.s) && (p.s[p.i] == '-' || p.s[p.i] >= '0' && p.s[p.i] <= '9'):
		v.Kind = "integer"
		n, err := p.number()
		if err != nil {
			return v, err
		}
		v.Atom = n.String()
		p.space()
		if !p.eat("..") {
			return v, nil
		}
		p.space()
		high, err := p.number()
		if err != nil {
			return v, err
		}
		v = ReplayValue{Kind: "set"}
		delta := new(big.Int).Sub(high, n)
		if delta.Sign() < 0 {
			return v, nil
		}
		if !delta.IsInt64() || delta.Int64() >= maxInterval {
			return v, p.errorf("interval is too large")
		}
		for n.Cmp(high) <= 0 {
			v.Elements = append(v.Elements, ReplayValue{Kind: "integer", Atom: n.String()})
			n.Add(n, big.NewInt(1))
		}
		return v, nil
	default:
		id := p.ident()
		if id == "" {
			return v, p.errorf("expected a value")
		}
		v.Kind, v.Atom = "model", id
		if id == "TRUE" || id == "FALSE" {
			v.Kind = "bool"
		}
		return v, nil
	}
}

func (p *replayValueParser) number() (*big.Int, error) {
	start := p.i
	p.eat("-")
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	n, ok := new(big.Int).SetString(p.s[start:p.i], 10)
	if !ok {
		return nil, p.errorf("expected an integer")
	}
	return n, nil
}

func (p *replayValueParser) elements(end string, depth int) ([]ReplayValue, error) {
	var out []ReplayValue
	p.space()
	if p.eat(end) {
		return out, nil
	}
	for {
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.space()
		if p.eat(end) {
			return out, nil
		}
		if !p.eat(",") {
			return nil, p.errorf("expected , or %s", end)
		}
	}
}

func replayValueKey(v ReplayValue) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (v ReplayValue) canonical(depth int) (ReplayValue, error) {
	bad := func() (ReplayValue, error) {
		return ReplayValue{}, fmt.Errorf("malformed replay %s value", v.Kind)
	}
	if depth > 128 {
		return bad()
	}
	switch v.Kind {
	case "bool", "integer", "model", "string":
		if len(v.Elements) != 0 || len(v.Entries) != 0 {
			return bad()
		}
		switch v.Kind {
		case "bool":
			if v.Atom != "TRUE" && v.Atom != "FALSE" {
				return bad()
			}
		case "integer":
			if v.Atom == "" {
				return bad()
			}
			for i, c := range v.Atom {
				if !(c >= '0' && c <= '9' || i == 0 && c == '-') {
					return bad()
				}
			}
			n, ok := new(big.Int).SetString(v.Atom, 10)
			if !ok {
				return bad()
			}
			v.Atom = n.String()
		case "model":
			if !isIdent(v.Atom) || v.Atom == "TRUE" || v.Atom == "FALSE" {
				return bad()
			}
		case "string":
			if !utf8.ValidString(v.Atom) {
				return bad()
			}
			for _, c := range v.Atom {
				if c < 32 && !strings.ContainsRune("\n\t\r\f", c) {
					return bad()
				}
			}
		}
		v.Elements, v.Entries = nil, nil
	case "set", "sequence":
		if v.Atom != "" || len(v.Entries) != 0 {
			return bad()
		}
		x := make([]ReplayValue, 0, len(v.Elements))
		for _, e := range v.Elements {
			c, err := e.canonical(depth + 1)
			if err != nil {
				return bad()
			}
			x = append(x, c)
		}
		if v.Kind == "set" {
			sort.Slice(x, func(i, j int) bool { return replayValueKey(x[i]) < replayValueKey(x[j]) })
			n := 0
			for _, e := range x {
				if n == 0 || replayValueKey(x[n-1]) != replayValueKey(e) {
					x[n] = e
					n++
				}
			}
			x = x[:n]
		}
		v.Elements, v.Entries = nil, nil
		if len(x) > 0 {
			v.Elements = x
		}
	case "record", "function":
		if v.Atom != "" || len(v.Elements) != 0 || v.Kind == "function" && len(v.Entries) == 0 {
			return bad()
		}
		x := make([]ReplayEntry, 0, len(v.Entries))
		seen := map[string]bool{}
		for _, e := range v.Entries {
			k, err := e.Key.canonical(depth + 1)
			if err != nil {
				return bad()
			}
			c, err := e.Value.canonical(depth + 1)
			if err != nil {
				return bad()
			}
			if v.Kind == "record" && (k.Kind != "string" || !isIdent(k.Atom)) {
				return bad()
			}
			id := replayValueKey(k)
			if seen[id] {
				return bad()
			}
			seen[id] = true
			x = append(x, ReplayEntry{k, c})
		}
		sort.Slice(x, func(i, j int) bool { return replayValueKey(x[i].Key) < replayValueKey(x[j].Key) })
		v.Entries, v.Elements = nil, nil
		if len(x) > 0 {
			v.Entries = x
		}
	default:
		return bad()
	}
	return v, nil
}

// Canonical is the value as every equal value is written: a set's elements
// and a function's entries sorted and without repeats, a sequence in its
// own order. A malformed value has none.
func (v ReplayValue) Canonical() (ReplayValue, error) {
	return v.canonical(0)
}

// Equal says whether two values are the same value, however each is
// spelled: a set in any order, an integer however it's padded. A malformed
// value equals nothing, itself included.
func (v ReplayValue) Equal(o ReplayValue) bool {
	x, err := v.canonical(0)
	if err != nil {
		return false
	}
	y, err := o.canonical(0)
	return err == nil && reflect.DeepEqual(x, y)
}

// Equal says whether two states give the same variables the same values.
func (s ReplayState) Equal(o ReplayState) bool {
	if len(s) != len(o) {
		return false
	}
	for name, value := range s {
		other, ok := o[name]
		if !ok || !value.Equal(other) {
			return false
		}
	}
	return true
}

// TLA accepts only validated data, never an arbitrary expression from a result.
func (v ReplayValue) TLA() (string, error) {
	c, err := v.canonical(0)
	if err != nil {
		return "", err
	}
	return c.render(), nil
}

func (v ReplayValue) render() string {
	switch v.Kind {
	case "bool", "integer", "model":
		return v.Atom
	case "string":
		return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t", "\f", "\\f").Replace(v.Atom) + "\""
	case "set", "sequence":
		x := make([]string, len(v.Elements))
		for i, e := range v.Elements {
			x[i] = e.render()
		}
		if v.Kind == "set" {
			return "{" + strings.Join(x, ", ") + "}"
		}
		return "<<" + strings.Join(x, ", ") + ">>"
	default:
		x := make([]string, len(v.Entries))
		for i, e := range v.Entries {
			if v.Kind == "record" {
				x[i] = e.Key.Atom + " |-> " + e.Value.render()
			} else {
				x[i] = e.Key.render() + " :> " + e.Value.render()
			}
		}
		if v.Kind == "record" {
			return "[" + strings.Join(x, ", ") + "]"
		}
		return "(" + strings.Join(x, " @@ ") + ")"
	}
}
