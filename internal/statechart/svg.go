package statechart

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// SVG renders the complete checked ordinary chart and its report as standalone
// SVG. It performs no I/O and never mutates the receiver, including its maps.
// Callers own evidence checking and must not mutate the result during a call.
// Invalid structure or unrepresentable text returns an error and no SVG.
func (c *Result) SVG() (string, error) {
	if err := validateSVG(c); err != nil {
		return "", err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("statechart SVG: cannot preserve result: %w", err)
	}
	// JSON would replace invalid UTF-8 and escape XML-forbidden controls. Check
	// original strings, including map keys and metadata-only temporal values.
	if err := validateSVGText(reflect.ValueOf(*c)); err != nil {
		return "", err
	}
	l := layoutSVG(c, svgReport(c))
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="chart-title" aria-describedby="chart-description" viewBox="0 0 %g %g" width="%g" height="%g" font-family="monospace" font-size="14" fill="#17212b">`, l.width, l.height, l.width, l.height)
	b.WriteString(`<title id="chart-title">Bounded reader statechart</title><desc id="chart-description">Every checked chart state, initial and visible directed arrow, followed by bounds, rule wording, state qualifications, hidden occurrences, independent counts, provenance and temporal trace facts. This bounded model view adds no proof claim.</desc>`)
	b.WriteString(`<metadata id="statechart-data">`)
	b.WriteString(svgEscape(string(data)))
	b.WriteString(`</metadata><defs><marker id="chart-arrowhead" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="10" markerHeight="10" markerUnits="userSpaceOnUse" orient="auto"><path d="M 0 0 L 10 5 L 0 10 Z" fill="#34495e"/></marker></defs>`)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%g" height="%g" fill="#ffffff"/>`, l.width, l.height)
	writeSVGText(&b, measureSVGText("Bounded reader statechart\nAll initial states and visible arrows; qualifications and evidence below."), svgMargin, svgMargin)
	for i, a := range c.Arrows {
		route := l.arrows[i]
		right := l.nodes[l.index[a.From]].x + l.nodes[l.index[a.From]].width
		fmt.Fprintf(&b, `<g id="arrow-%d" data-from="%s" data-to="%s" data-label="%s"><title>%s</title>`, i+1, svgEscape(a.From), svgEscape(a.To), svgEscape(a.Label), svgEscape(a.Label))
		fmt.Fprintf(&b, `<desc>State %d to state %d; %d concrete step occurrences.</desc>`, l.index[a.From]+1, l.index[a.To]+1, a.Count)
		writeSVGPath(&b, fmt.Sprintf("M %g %g L %g %g L %g %g L %g %g", right, route.fromY, route.laneX, route.fromY, route.laneX, route.toY, right, route.toY))
		fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="3" fill="#34495e"/>`, right, route.fromY)
		box := svgBox{right + svgPadding, route.fromY - route.text.height/2 - svgPadding/2, route.text.width + svgPadding, route.text.height + svgPadding}
		writeSVGRect(&b, box, "#ffffff", "none")
		writeSVGText(&b, route.text, box.x+svgPadding/2, box.y+svgPadding/2)
		b.WriteString(`</g>`)
	}
	for i, s := range c.States {
		n := l.nodes[i]
		fmt.Fprintf(&b, `<g id="state-%d" data-state-id="%s"><title>%s</title>`, i+1, svgEscape(s.ID), svgEscape(s.Label))
		fill := "#f0f6fa"
		if len(s.ViolatedRules) > 0 {
			fill = "#fff0e8"
		}
		writeSVGRect(&b, n.svgBox, fill, "#34495e")
		writeSVGText(&b, n.text, n.x+svgPadding, n.y+(n.height-n.text.height)/2)
		b.WriteString(`</g>`)
	}
	for i, id := range c.Initial {
		n := l.nodes[l.index[id]]
		y := n.y + n.height/2
		fmt.Fprintf(&b, `<g id="initial-%d" data-initial="%s"><title>Initial state %d</title>`, i+1, svgEscape(id), l.index[id]+1)
		fmt.Fprintf(&b, `<circle cx="40" cy="%g" r="5" fill="#34495e"/>`, y)
		writeSVGPath(&b, fmt.Sprintf("M 40 %g L %g %g", y, n.x, y))
		writeSVGText(&b, measureSVGText("Initial"), 8, y-svgLineHeight-svgPadding/2)
		b.WriteString(`</g>`)
	}
	b.WriteString(`<g id="chart-report">`)
	y := l.reportY
	for _, t := range l.report {
		writeSVGText(&b, t, svgMargin, y)
		y += t.height + svgPadding
	}
	b.WriteString(`</g></svg>`)
	return b.String(), nil
}

func svgEscape(s string) string {
	var b strings.Builder
	// All dynamic strings have already been checked for XML 1.0 characters.
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func writeSVGText(b *strings.Builder, t svgText, x, y float64) {
	b.WriteString(`<text xml:space="preserve">`)
	for i, line := range t.lines {
		if i > 0 {
			b.WriteString("&#xA;")
		}
		fmt.Fprintf(b, `<tspan x="%g" y="%g">%s</tspan>`, x, y+svgFontSize+float64(i)*svgLineHeight, svgEscape(line))
	}
	b.WriteString(`</text>`)
}

func writeSVGRect(b *strings.Builder, r svgBox, fill, stroke string) {
	fmt.Fprintf(b, `<rect x="%g" y="%g" width="%g" height="%g" rx="4" fill="%s" stroke="%s" stroke-width="2"/>`, r.x, r.y, r.width, r.height, fill, stroke)
}

func writeSVGPath(b *strings.Builder, d string) {
	fmt.Fprintf(b, `<path d="%s" fill="none" stroke="#34495e" stroke-width="2" stroke-linejoin="round" marker-end="url(#chart-arrowhead)"/>`, d)
}

func validateSVG(c *Result) error {
	bad := func(s string) error { return fmt.Errorf("statechart SVG: %s", s) }
	if c == nil {
		return bad("nil result")
	}
	if c.Version != 1 {
		return bad("unsupported result version")
	}
	if len(c.States) == 0 || len(c.States) > MaxStates || len(c.Initial) == 0 {
		return bad("requires states within supported capacity and at least one initial")
	}
	states := map[string]bool{}
	owners := map[string]string{}
	initialMembers := map[string]bool{}
	for _, s := range c.States {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Label) == "" || states[s.ID] || len(s.Members) == 0 {
			return bad("empty or duplicate state identity, label or membership")
		}
		states[s.ID] = true
		for _, id := range s.Members {
			if strings.TrimSpace(id) == "" || owners[id] != "" {
				return bad("empty or repeated concrete state membership")
			}
			owners[id] = s.ID
		}
		for _, id := range s.InitialMembers {
			if owners[id] != s.ID || initialMembers[id] {
				return bad("unknown or repeated initial member")
			}
			initialMembers[id] = true
		}
	}
	if len(owners) > MaxStates {
		return bad("concrete membership exceeds supported capacity")
	}
	initial := map[string]bool{}
	for _, id := range c.Initial {
		if !states[id] || initial[id] {
			return bad("unknown or duplicate initial state")
		}
		initial[id] = true
	}
	for _, s := range c.States {
		if initial[s.ID] != (len(s.InitialMembers) > 0) {
			return bad("initial states disagree with concrete initial membership")
		}
	}
	type key struct{ from, label, to string }
	seen := map[key]bool{}
	counts := [2]int64{}
	for group, arrows := range [][]Arrow{c.Arrows, c.Report.Hidden} {
		for _, a := range arrows {
			k := key{a.From, a.Label, a.To}
			if !states[a.From] || !states[a.To] || strings.TrimSpace(a.Label) == "" || seen[k] {
				return bad("unknown arrow endpoint, empty label or duplicate arrow bucket")
			}
			seen[k] = true
			if a.Count <= 0 || a.Count != len(a.Members) || (len(a.HidingRules) > 0) != (group == 1) {
				return bad("arrow count, membership or hiding rules disagree")
			}
			for _, step := range a.Members {
				if owners[step.From] != a.From || owners[step.To] != a.To || strings.TrimSpace(step.Action) == "" {
					return bad("arrow occurrence disagrees with chart membership")
				}
			}
			counts[group] += int64(a.Count)
		}
	}
	if err := validateHiding(c, c.Report.Hidden); err != nil {
		return fmt.Errorf("statechart SVG: %w", err)
	}
	want := map[string]int64{
		"chart_states": int64(len(c.States)), "chart_arrows": int64(len(c.Arrows)),
		"chart_arrows_before_hiding": int64(len(seen)), "tlc_states": int64(len(owners)),
		"tlc_initial_states": int64(len(initialMembers)), "shown_steps": counts[0],
		"hidden_steps": counts[1], "tlc_steps": counts[0] + counts[1],
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		n, ok := c.Report.Counts[name]
		if !ok || n != want[name] {
			return bad("report count disagrees with chart: " + name)
		}
	}
	if c.Report.Counts["tlc_generated_states"] < want["tlc_steps"]+want["tlc_initial_states"] {
		return bad("inconsistent generated state count")
	}
	if c.Report.ArrowTarget != ArrowTarget || c.Report.OverArrowTarget != (len(c.Arrows) > c.Report.ArrowTarget) {
		return bad("inconsistent advisory arrow target")
	}
	return nil
}

// Result's dynamic values come from JSON-decoded temporal traces. Marshal has
// already rejected cycles and non-JSON numbers; pointers in those values are
// not part of a computed Result. Reflection also checks metadata-only strings.
func validateSVGText(v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if !utf8.ValidString(s) {
			return fmt.Errorf("statechart SVG: text is not UTF-8")
		}
		for _, r := range s {
			if r != '\t' && r != '\n' && r != '\r' && (r < 0x20 || r == 0xfffe || r == 0xffff) {
				return fmt.Errorf("statechart SVG: XML 1.0 cannot represent U+%04X", r)
			}
		}
	case reflect.Interface:
		if !v.IsNil() {
			return validateSVGText(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if err := validateSVGText(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := validateSVGText(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if err := validateSVGText(it.Key()); err != nil {
				return err
			}
			if err := validateSVGText(it.Value()); err != nil {
				return err
			}
		}
	case reflect.Pointer:
		return fmt.Errorf("statechart SVG: temporal values must be JSON data")
	}
	return nil
}

func svgReport(c *Result) []string {
	r := c.Report
	out := []string{
		"Bounded model report",
		"This chart describes the complete explored model within the supplied bounds. It adds no proof claim.",
		"State violations are invariant facts; temporal violations remain trace facts, not state qualifications.",
		"Unavailable actions occur elsewhere in the graph but at no member of that state, before hiding.",
		fmt.Sprintf("Visible arrows: %d. Advisory target: %d. Over advisory target: %t. All arrows are drawn.", len(c.Arrows), r.ArrowTarget, r.OverArrowTarget),
		"Checked bounds (configuration constants)",
	}
	if len(r.Source.Config.Constants) == 0 {
		out = append(out, "No constants supplied.")
	}
	for _, name := range slices.Sorted(maps.Keys(r.Source.Config.Constants)) {
		out = append(out, name+" = "+r.Source.Config.Constants[name])
	}
	out = append(out, "Locked rules (verbatim wording)")
	for _, rule := range r.Rules {
		out = append(out, rule.Name+" ("+rule.Kind+")", rule.Says, "SHA-256: "+rule.SHA256)
		if rule.Expect != "" {
			out = append(out, "Expected violation: "+rule.Expect)
		}
	}
	index := map[string]int{}
	out = append(out, "State qualifications")
	for i, s := range c.States {
		index[s.ID] = i + 1
		out = append(out, fmt.Sprintf("State %d: ", i+1)+s.Label,
			"Concrete members: "+svgList(s.Members), "Initial members: "+svgList(s.InitialMembers),
			"Violated invariant rules: "+svgList(s.ViolatedRules), "Unavailable model actions: "+svgList(s.UnavailableActions))
	}
	for group, arrows := range [][]Arrow{c.Arrows, r.Hidden} {
		name := "Visible"
		if group == 1 {
			name = "Hidden"
		}
		out = append(out, name+" arrow occurrences (duplicates retained)")
		if len(arrows) == 0 {
			out = append(out, "None.")
		}
		for i, a := range arrows {
			out = append(out, fmt.Sprintf("%s arrow %d: state %d -> state %d; %d concrete occurrences", name, i+1, index[a.From], index[a.To], a.Count), a.Label)
			if group == 1 {
				out = append(out, "Hiding view rules: "+svgList(a.HidingRules))
			}
			for j, step := range a.Members {
				out = append(out, fmt.Sprintf("Occurrence %d: ", j+1)+step.From+" -- "+step.Action+" --> "+step.To)
			}
		}
	}
	out = append(out, "Independent counts and their meanings")
	keys := map[string]bool{}
	for k := range r.Counts {
		keys[k] = true
	}
	for k := range r.CountMeanings {
		keys[k] = true
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		value := "not supplied"
		if n, ok := r.Counts[k]; ok {
			value = fmt.Sprint(n)
		}
		out = append(out, k+" = "+value, r.CountMeanings[k])
	}
	out = append(out, "Provenance", "Module: "+r.Source.Module, "TLC: "+r.Source.TLC,
		"Specification: "+r.Source.Config.Specification,
		"Graph-run invariant checks: "+svgList(r.Source.Config.Invariants),
		"Graph-run temporal property checks: "+svgList(r.Source.Config.Properties),
		"Java image: "+r.Source.Tool.Image, "TLC jar SHA-256: "+r.Source.Tool.JarSHA256,
		"Temporal counterexamples (trace facts only)")
	if len(r.Temporal) == 0 {
		out = append(out, "No temporal counterexamples supplied.")
	}
	for i, trace := range r.Temporal {
		out = append(out, fmt.Sprintf("Trace %d; check: ", i+1)+trace.Check,
			"Violated property: "+trace.Violated, "Module: "+trace.Module, "TLC: "+trace.TLC,
			fmt.Sprintf("Loop back to trace state: %d (0 means none). Stutters at the last state: %t.", trace.Loop, trace.Stutters))
		for _, s := range trace.States {
			out = append(out, fmt.Sprintf("Trace state %d; action: ", s.Index)+s.Action, "Changed variables: "+svgList(s.Changed))
			for _, k := range slices.Sorted(maps.Keys(s.TLA)) {
				out = append(out, k+" = "+s.TLA[k])
			}
			// The enclosing result has already been marshaled successfully.
			data, _ := json.Marshal(s.Vars)
			out = append(out, "Trace values (JSON): "+string(data))
		}
	}
	return out
}

func svgList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
