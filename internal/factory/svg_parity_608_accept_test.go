package factory

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Exercise the existing published Mermaid path without changing its aliases,
// entity escaping, Markdown tables, commands, limits or saved proposal format.
// Both formats must describe the same checked topology, not just equal counts.
func TestSVG608MatchesPublishedMermaid(t *testing.T) {
	in := statechart.Input{
		Graph: tlc.GraphResult{
			Graph: tlc.Graph{
				States: []tlc.GraphState{
					{ID: "a", Label: "phase = 0", Values: map[string]string{"phase": "0"}},
					{ID: "b", Label: "phase = 1", Values: map[string]string{"phase": "1"}},
				},
				Initial: []string{"a", "b"},
				Steps: []tlc.GraphStep{
					{From: "a", Action: "Go", To: "b"}, {From: "a", Action: "Go", To: "b"},
					{From: "a", Action: "Retry", To: "b"}, {From: "b", Action: "Reset", To: "a"},
					{From: "b", Action: "Wait", To: "b"}, {From: "a", Action: "Poll", To: "a"},
				},
			},
			Module: "Parity608", StateCount: 2, StepCount: 6,
			Config: tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "2"}},
			Tool:   tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
			Run:    tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: 2, DistinctStates: 2, StatesGenerated: 8},
		},
		Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{
			{Name: "Spec", Kind: project.Spec, Says: "Start, retry or return.", SHA256: strings.Repeat("c", 64)},
		}},
		Invariants: map[string]map[string]bool{"a": {}, "b": {}},
	}
	view := map[string]any{"version": 1, "state": []any{
		map[string]any{"id": "ready", "when": map[string]any{"eq": []any{map[string]any{"var": "phase"}, "0"}}, "label": "Ready <&> \"quoted\" #quot;"},
		map[string]any{"id": "work", "when": true, "label": "Working\nSecond line"},
	}}
	var steps []any
	for _, action := range []string{"Go", "Retry", "Reset", "Wait", "Poll"} {
		label := action
		if action == "Go" {
			label = "Begin & <continue>\n[*] --> forged #amp;"
		}
		steps = append(steps, map[string]any{"id": action, "when": map[string]any{"eq": []any{map[string]any{"action": true}, action}}, "label": label, "hide": action == "Poll"})
	}
	view["step"] = steps
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	c, err := statechart.Compute(t.Context(), in, b)
	if err != nil {
		t.Fatal(err)
	}
	renderer, ok := any(c).(interface{ SVG() (string, error) })
	if !ok {
		t.Fatal("checked charts have no shared SVG renderer alongside Mermaid")
	}
	svg, err := renderer.SVG()
	if err != nil || svg == "" {
		t.Fatalf("SVG: %v", err)
	}

	type topology struct{ states, initials, arrows map[string]int }
	newTopology := func() topology { return topology{map[string]int{}, map[string]int{}, map[string]int{}} }
	want, drawn, published := newTopology(), newTopology(), newTopology()
	labels := map[string]string{}
	for _, s := range c.States {
		labels[s.ID] = s.Label
		want.states[s.Label]++
	}
	for _, id := range c.Initial {
		want.initials[labels[id]]++
	}
	key := func(from, label, to string) string { return from + "\x00" + label + "\x00" + to }
	for _, a := range c.Arrows {
		want.arrows[key(labels[a.From], a.Label, labels[a.To])]++
	}
	d := xml.NewDecoder(strings.NewReader(svg))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		e, ok := tok.(xml.StartElement)
		if !ok || e.Name.Local != "g" {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attr {
			attrs[a.Name.Local] = a.Value
		}
		if id, ok := attrs["data-state-id"]; ok {
			drawn.states[labels[id]]++
		}
		if id, ok := attrs["data-initial"]; ok {
			drawn.initials[labels[id]]++
		}
		if from, ok := attrs["data-from"]; ok {
			drawn.arrows[key(labels[from], attrs["data-label"], labels[attrs["data-to"]])]++
		}
	}
	body := readerComment(&formalize.Proposal{Bounds: in.Lock.Bounds, Reader: &formalize.Reader{Version: 1, Chart: c}})
	fence := regexp.MustCompile("(?s)```mermaid\\n(.*?)\\n```").FindStringSubmatch(body)
	if len(fence) != 2 || !strings.HasPrefix(fence[1], "stateDiagram-v2\n") {
		t.Fatal("existing Mermaid publication disappeared")
	}
	decode := func(s string) string {
		return regexp.MustCompile(`#([0-9]+|quot|amp|lt|gt|apos);`).ReplaceAllStringFunc(s, func(entity string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(entity, "#"), ";")
			if n, err := strconv.Atoi(name); err == nil {
				return string(rune(n))
			}
			return map[string]string{"quot": "\"", "amp": "&", "lt": "<", "gt": ">", "apos": "'"}[name]
		})
	}
	aliases := map[string]string{}
	for _, s := range regexp.MustCompile(`(?m)^\s*state "([^"\n]*)" as ([A-Za-z][A-Za-z0-9_]*)\s*$`).FindAllStringSubmatch(fence[1], -1) {
		aliases[s[2]] = decode(s[1])
		published.states[aliases[s[2]]]++
	}
	for _, a := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*|\[\*\])\s*-->\s*([A-Za-z][A-Za-z0-9_]*)(?:: ([^\n]*))?\s*$`).FindAllStringSubmatch(fence[1], -1) {
		if a[1] == "[*]" {
			published.initials[aliases[a[2]]]++
		} else {
			published.arrows[key(aliases[a[1]], decode(a[3]), aliases[a[2]])]++
		}
	}
	if !reflect.DeepEqual(drawn, want) {
		t.Fatalf("SVG differs from checked topology: %+v want %+v", drawn, want)
	}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("Mermaid differs from checked topology: %+v want %+v", published, want)
	}
}
