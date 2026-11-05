package statechart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxViewBytes = 1 << 20
const maxExpressionDepth = 64

type view struct {
	state, step []viewRule
}

type viewRule struct {
	id, label string
	hide      bool
	when      expression
}

type expression struct {
	op       string
	value    bool
	terms    []term
	children []expression
}

type term struct {
	lookup, name string
	text         string
	boolean      bool
	isBool       bool
}

func parseView(ctx context.Context, data []byte) (*view, error) {
	bad := func(err error) (*view, error) {
		return nil, &Refusal{Code: "invalid_view", Message: err.Error()}
	}
	if len(data) > maxViewBytes || !utf8.Valid(data) {
		return bad(fmt.Errorf("view must be UTF-8 JSON of at most %d bytes", maxViewBytes))
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	x, err := readJSON(ctx, d, 0)
	if err != nil {
		return bad(err)
	}
	if _, err := d.Token(); err != io.EOF {
		return bad(fmt.Errorf("view has trailing content"))
	}
	m, ok := x.(map[string]any)
	if !ok || len(m) != 3 || m["version"] != json.Number("1") || m["state"] == nil || m["step"] == nil {
		return bad(fmt.Errorf("view requires exactly version:1, state and step"))
	}
	v := &view{}
	for _, kind := range []string{"state", "step"} {
		items, ok := m[kind].([]any)
		if !ok || len(items) == 0 {
			return bad(fmt.Errorf("%s must be a nonempty rule array", kind))
		}
		seen := map[string]bool{}
		for _, item := range items {
			r, err := parseRule(item, kind == "step")
			if err != nil {
				return nil, &Refusal{Code: "invalid_view", Message: kind + ": " + err.Error(), Rules: nonempty(r.id)}
			}
			if seen[r.id] {
				return nil, &Refusal{Code: "invalid_view", Message: "duplicate " + kind + " rule ID", Rules: []string{r.id}}
			}
			seen[r.id] = true
			if kind == "state" {
				v.state = append(v.state, r)
			} else {
				v.step = append(v.step, r)
			}
		}
	}
	return v, nil
}

// Decode every key, including keys in rules that will never match. The usual
// struct decoder accepts duplicate keys and would erase that ambiguity.
func readJSON(ctx context.Context, d *json.Decoder, depth int) (any, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if depth > 2*maxExpressionDepth+8 {
		return nil, fmt.Errorf("JSON nesting exceeds view limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be a string")
			}
			if _, exists := m[k]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", k)
			}
			m[k], err = readJSON(ctx, d, depth+1)
			if err != nil {
				return nil, err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("unclosed JSON object")
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			x, err := readJSON(ctx, d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, x)
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("unclosed JSON array")
		}
		return a, nil
	default:
		return t, nil
	}
}

func parseRule(x any, step bool) (r viewRule, err error) {
	m, ok := x.(map[string]any)
	if !ok {
		return r, fmt.Errorf("rule must be an object")
	}
	r.id, _ = m["id"].(string)
	r.label, _ = m["label"].(string)
	if strings.TrimSpace(r.id) == "" || strings.TrimSpace(r.label) == "" {
		return r, fmt.Errorf("rule requires nonblank literal id and label")
	}
	for k := range m {
		if k != "id" && k != "label" && k != "when" && !(step && k == "hide") {
			return r, fmt.Errorf("unsupported rule field %q", k)
		}
	}
	if h, exists := m["hide"]; exists {
		var ok bool
		r.hide, ok = h.(bool)
		if !ok {
			return r, fmt.Errorf("hide must be boolean")
		}
	}
	r.when, err = parseExpression(m["when"], step, 0)
	return r, err
}

func parseExpression(x any, step bool, depth int) (e expression, err error) {
	if depth > maxExpressionDepth {
		return e, fmt.Errorf("expression nesting exceeds %d", maxExpressionDepth)
	}
	if b, ok := x.(bool); ok {
		return expression{value: b}, nil
	}
	m, ok := x.(map[string]any)
	if !ok || len(m) != 1 {
		return e, fmt.Errorf("when requires a boolean or one boolean operator")
	}
	for op, value := range m {
		e.op = op
		switch op {
		case "eq":
			a, ok := value.([]any)
			if !ok || len(a) != 2 {
				return e, fmt.Errorf("eq requires two terms")
			}
			for _, x := range a {
				t, err := parseTerm(x, step)
				if err != nil {
					return e, err
				}
				e.terms = append(e.terms, t)
			}
			if e.terms[0].isBool != e.terms[1].isBool {
				return e, fmt.Errorf("eq requires like types")
			}
		case "not", "all", "any":
			a := []any{value}
			if op != "not" {
				a, ok = value.([]any)
				if !ok {
					return e, fmt.Errorf("%s requires a boolean expression array", op)
				}
			}
			for _, x := range a {
				child, err := parseExpression(x, step, depth+1)
				if err != nil {
					return e, err
				}
				e.children = append(e.children, child)
			}
		default:
			return e, fmt.Errorf("unsupported operator %q", op)
		}
	}
	return e, nil
}

func parseTerm(x any, step bool) (t term, err error) {
	switch v := x.(type) {
	case string:
		return term{text: v}, nil
	case bool:
		return term{boolean: v, isBool: true}, nil
	case map[string]any:
		if len(v) == 1 {
			for k, x := range v {
				if k == "action" && step && x == true {
					return term{lookup: k}, nil
				}
				name, ok := x.(string)
				if ok && strings.TrimSpace(name) != "" && ((!step && k == "var") || (step && (k == "from" || k == "to"))) {
					return term{lookup: k, name: name}, nil
				}
			}
		}
	}
	return t, fmt.Errorf("term requires a string, boolean or a lookup available to this mapping")
}

type environment struct {
	vars, from, to map[string]string
	action         string
}

func (t term) evaluate(env environment) (term, error) {
	if t.lookup == "" {
		return t, nil
	}
	if t.lookup == "action" {
		return term{text: env.action}, nil
	}
	values := env.vars
	if t.lookup == "from" {
		values = env.from
	} else if t.lookup == "to" {
		values = env.to
	}
	x, ok := values[t.name]
	if !ok {
		return term{}, fmt.Errorf("missing %s variable %q", t.lookup, t.name)
	}
	return term{text: x}, nil
}

func (e expression) evaluate(ctx context.Context, env environment) (bool, error) {
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	switch e.op {
	case "":
		return e.value, nil
	case "eq":
		a, err := e.terms[0].evaluate(env)
		if err != nil {
			return false, err
		}
		b, err := e.terms[1].evaluate(env)
		return a == b, err
	default:
		result := e.op == "all"
		for _, child := range e.children {
			b, err := child.evaluate(ctx, env)
			if err != nil {
				return false, err
			}
			switch e.op {
			case "all":
				result = result && b
			case "any":
				result = result || b
			case "not":
				result = !b
			}
		}
		return result, nil
	}
}

func match(ctx context.Context, rules []viewRule, env environment) (*viewRule, error) {
	for i := range rules {
		r := &rules[i]
		ok, err := r.when.evaluate(ctx, env)
		if err != nil {
			return nil, &Refusal{Code: "evaluation", Message: err.Error(), Rules: []string{r.id}}
		}
		if ok {
			return r, nil
		}
	}
	return nil, nil
}

func nonempty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
