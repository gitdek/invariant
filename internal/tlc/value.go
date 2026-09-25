package tlc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseValue turns a TLA+ value as TLC prints it into plain data:
//
//	{a, b}                 set       → []any
//	<<a, b>>               tuple     → []any
//	[f |-> v, g |-> w]     record    → map[string]any
//	(k :> v @@ k2 :> v2)   function  → map[string]any, keyed by the domain value
//	"text", 42, TRUE       strings, integers and booleans
//	r1                     model value → string
func ParseValue(text string) (any, error) {
	p := &valueParser{s: text}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.space(); p.i != len(p.s) {
		return nil, p.errorf("unexpected %q", p.s[p.i:])
	}
	return v, nil
}

type valueParser struct {
	s string
	i int
}

func (p *valueParser) value() (any, error) {
	p.space()
	switch {
	case p.eat("<<"):
		return p.sequence(">>")
	case p.eat("{"):
		return p.sequence("}")
	case p.eat("["):
		return p.record()
	case p.eat("("):
		return p.function()
	case p.i < len(p.s) && p.s[p.i] == '"':
		return p.str()
	case p.i < len(p.s) && (p.s[p.i] == '-' || p.s[p.i] >= '0' && p.s[p.i] <= '9'):
		return p.integer()
	}
	switch id := p.ident(); id {
	case "":
		return nil, p.errorf("expected a value")
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	default:
		return id, nil
	}
}

func (p *valueParser) sequence(end string) (any, error) {
	out := []any{}
	if p.space(); p.eat(end) {
		return out, nil
	}
	for {
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if p.space(); p.eat(end) {
			return out, nil
		}
		if !p.eat(",") {
			return nil, p.errorf("expected , or %s", end)
		}
	}
}

func (p *valueParser) record() (any, error) {
	out := map[string]any{}
	if p.space(); p.eat("]") {
		return out, nil
	}
	for {
		p.space()
		field := p.ident()
		if p.space(); field == "" || !p.eat("|->") {
			return nil, p.errorf("expected field |-> value")
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[field] = v
		if p.space(); p.eat("]") {
			return out, nil
		}
		if !p.eat(",") {
			return nil, p.errorf("expected , or ]")
		}
	}
}

func (p *valueParser) function() (any, error) {
	out := map[string]any{}
	for {
		k, err := p.value()
		if err != nil {
			return nil, err
		}
		if p.space(); !p.eat(":>") {
			return nil, p.errorf("expected :>")
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[key(k)] = v
		if p.space(); p.eat(")") {
			return out, nil
		}
		if !p.eat("@@") {
			return nil, p.errorf("expected @@ or )")
		}
	}
}

func (p *valueParser) str() (any, error) {
	var b strings.Builder
	for p.i++; p.i < len(p.s); p.i++ {
		switch c := p.s[p.i]; c {
		case '"':
			p.i++
			return b.String(), nil
		case '\\':
			if p.i+1 < len(p.s) {
				p.i++
				b.WriteByte(p.s[p.i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return nil, p.errorf("unterminated string")
}

func (p *valueParser) integer() (any, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	n, err := strconv.ParseInt(p.s[start:p.i], 10, 64)
	if err != nil {
		return nil, p.errorf("bad integer %q", p.s[start:p.i])
	}
	return n, nil
}

func (p *valueParser) ident() string {
	start := p.i
	for p.i < len(p.s) {
		c := p.s[p.i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || p.i > start && c >= '0' && c <= '9') {
			break
		}
		p.i++
	}
	return p.s[start:p.i]
}

func (p *valueParser) eat(tok string) bool {
	if strings.HasPrefix(p.s[p.i:], tok) {
		p.i += len(tok)
		return true
	}
	return false
}

func (p *valueParser) space() {
	for p.i < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.s[p.i])) {
		p.i++
	}
}

func (p *valueParser) errorf(format string, args ...any) error {
	return fmt.Errorf("TLA+ value at offset %d: %s", p.i, fmt.Sprintf(format, args...))
}

// key renders a function's domain value as a map key.
func key(v any) string {
	switch k := v.(type) {
	case string:
		return k
	case int64:
		return strconv.FormatInt(k, 10)
	default:
		b, _ := json.Marshal(k)
		return string(b)
	}
}
