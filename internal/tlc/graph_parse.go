package tlc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParseGraphDOT reads the pinned TLC dot,actionlabels dialect. It validates
// the whole document, but only DumpGraph can establish a completed search.
func ParseGraphDOT(dot string) (*Graph, error) {
	p := graphParser{text: dot, states: map[string]bool{}}
	for _, word := range []string{"strict", "digraph", "DiskGraph", "{"} {
		if err := p.expect(word); err != nil {
			return nil, err
		}
	}
	if err := p.body(false); err != nil {
		return nil, err
	}
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	if t.kind != "eof" {
		return nil, p.invalid("trailing content")
	}
	g := &p.graph
	if len(g.States) == 0 || len(g.Initial) == 0 {
		return nil, p.invalid("no initial state")
	}
	for _, e := range g.Steps {
		if !p.states[e.From] || !p.states[e.To] {
			return nil, p.invalid("step refers to an undeclared state")
		}
	}
	return g, nil
}

type graphToken struct{ kind, text string }

type graphParser struct {
	text    string
	pos     int
	graph   Graph
	states  map[string]bool
	cluster bool
}

func (p *graphParser) invalid(reason string) error {
	return fmt.Errorf("invalid TLC DOT at byte %d: %s", p.pos, reason)
}

// Tokenize instead of scanning lines: TLC labels can exceed Scanner's limit,
// and punctuation and escaped newlines inside labels are data, not records.
func (p *graphParser) next() (graphToken, error) {
	for p.pos < len(p.text) && strings.ContainsRune(" \t\r\n", rune(p.text[p.pos])) {
		p.pos++
	}
	if p.pos == len(p.text) {
		return graphToken{kind: "eof"}, nil
	}
	c := p.text[p.pos]
	p.pos++
	if strings.ContainsRune("{}[]=,;", rune(c)) {
		return graphToken{kind: string(c), text: string(c)}, nil
	}
	if c == '-' && p.pos < len(p.text) && p.text[p.pos] == '>' {
		p.pos++
		return graphToken{kind: "->", text: "->"}, nil
	}
	if c == '"' {
		var b strings.Builder
		for p.pos < len(p.text) {
			c = p.text[p.pos]
			p.pos++
			switch c {
			case '"':
				return graphToken{kind: "string", text: b.String()}, nil
			case '\\':
				if p.pos == len(p.text) {
					return graphToken{}, p.invalid("unfinished label escape")
				}
				c = p.text[p.pos]
				p.pos++
				switch c {
				case 'n':
					b.WriteByte('\n')
				case '\\', '"':
					b.WriteByte(c)
				default:
					return graphToken{}, p.invalid("unsupported label escape")
				}
			case '\n', '\r', 0:
				return graphToken{}, p.invalid("unescaped control character in label")
			default:
				b.WriteByte(c)
			}
		}
		return graphToken{}, p.invalid("unfinished label")
	}
	start := p.pos - 1
	for p.pos < len(p.text) {
		c = p.text[p.pos]
		if strings.ContainsRune(" \t\r\n{}[]=,;\"", rune(c)) || strings.HasPrefix(p.text[p.pos:], "->") {
			break
		}
		p.pos++
	}
	word := p.text[start:p.pos]
	if !isIdent(word) && !graphDOTNumber.MatchString(word) {
		return graphToken{}, p.invalid("invalid unquoted token")
	}
	return graphToken{kind: "word", text: word}, nil
}

func (p *graphParser) expect(want string) error {
	t, err := p.next()
	if err != nil {
		return err
	}
	if t.text != want || t.kind == "string" || t.kind == "eof" {
		return p.invalid("expected " + want)
	}
	return nil
}

var (
	graphID        = regexp.MustCompile(`^(?:0|-?[1-9][0-9]*)$`)
	graphDOTNumber = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)
)

func graphFingerprint(t graphToken) bool {
	if t.kind != "word" || !graphID.MatchString(t.text) {
		return false
	}
	_, err := strconv.ParseInt(t.text, 10, 64)
	return err == nil
}

func (p *graphParser) body(inCluster bool) error {
	for {
		t, err := p.next()
		if err != nil {
			return err
		}
		switch t.kind {
		case "}":
			return nil
		case ";":
			continue
		case "word":
		default:
			return p.invalid("expected a graph record or closing brace")
		}
		if t.text == "subgraph" {
			if inCluster || p.cluster {
				return p.invalid("unexpected subgraph")
			}
			p.cluster = true
			if err := p.expect("cluster_graph"); err != nil {
				return err
			}
			if err := p.expect("{"); err != nil {
				return err
			}
			if err := p.body(true); err != nil {
				return err
			}
			continue
		}
		if t.text == "nodesep" || t.text == "color" {
			if err := p.expect("="); err != nil {
				return err
			}
			v, err := p.next()
			if err != nil {
				return err
			}
			if v.kind != "word" && v.kind != "string" || v.text == "" {
				return p.invalid("missing graph attribute value")
			}
			if err := p.expect(";"); err != nil {
				return err
			}
			continue
		}
		if !graphFingerprint(t) {
			return p.invalid("expected a TLC fingerprint")
		}
		from := t.text
		t, err = p.next()
		if err != nil {
			return err
		}
		to := ""
		if t.kind == "->" {
			t, err = p.next()
			if err != nil {
				return err
			}
			if !graphFingerprint(t) {
				return p.invalid("expected a step target")
			}
			to = t.text
			t, err = p.next()
			if err != nil {
				return err
			}
		}
		if t.kind != "[" {
			return p.invalid("missing state or step attributes")
		}
		attrs, err := p.attributes()
		if err != nil {
			return err
		}
		label, ok := attrs["label"]
		if !ok || label.kind != "string" || strings.TrimSpace(label.text) == "" {
			return p.invalid("missing state or action label")
		}
		if to != "" {
			p.graph.Steps = append(p.graph.Steps, GraphStep{From: from, Action: label.text, To: to})
			continue
		}
		if p.states[from] {
			return p.invalid("duplicate state " + from)
		}
		values, err := graphValues(label.text)
		if err != nil {
			return p.invalid(err.Error())
		}
		if len(p.graph.States) > 0 {
			first := p.graph.States[0].Values
			if len(first) != len(values) {
				return p.invalid("inconsistent state variables")
			}
			for name := range first {
				if _, ok := values[name]; !ok {
					return p.invalid("inconsistent state variables")
				}
			}
		}
		p.states[from] = true
		p.graph.States = append(p.graph.States, GraphState{ID: from, Label: label.text, Values: values})
		if style, ok := attrs["style"]; ok {
			if style.text != "filled" {
				return p.invalid("unsupported state style")
			}
			p.graph.Initial = append(p.graph.Initial, from)
		}
	}
}

func (p *graphParser) attributes() (map[string]graphToken, error) {
	attrs := map[string]graphToken{}
	for {
		key, err := p.next()
		if err != nil {
			return nil, err
		}
		if key.kind != "word" || !isIdent(key.text) {
			return nil, p.invalid("expected an attribute name")
		}
		if _, ok := attrs[key.text]; ok {
			return nil, p.invalid("duplicate attribute " + key.text)
		}
		if err := p.expect("="); err != nil {
			return nil, err
		}
		value, err := p.next()
		if err != nil {
			return nil, err
		}
		if value.kind != "word" && value.kind != "string" {
			return nil, p.invalid("missing attribute value")
		}
		attrs[key.text] = value
		end, err := p.next()
		if err != nil {
			return nil, err
		}
		if end.kind == "]" {
			return attrs, nil
		}
		if end.kind != "," {
			return nil, p.invalid("expected comma or closing bracket")
		}
	}
}

func graphValues(label string) (map[string]string, error) {
	values := map[string]string{}
	var name string
	for i, line := range strings.Split(label, "\n") {
		assignment, conjunction := strings.CutPrefix(line, `/\ `)
		if i == 0 || conjunction {
			variable, value, ok := strings.Cut(assignment, " = ")
			if !ok || !isIdent(variable) || strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("invalid state assignment")
			}
			if _, exists := values[variable]; exists {
				return nil, fmt.Errorf("duplicate variable %s", variable)
			}
			name = variable
			values[name] = value
		} else {
			if line == "" || line[0] != ' ' && line[0] != '\t' {
				return nil, fmt.Errorf("invalid state continuation")
			}
			values[name] += "\n" + line
		}
	}
	return values, nil
}

var (
	graphFrameHead = regexp.MustCompile(`^@!@!@STARTMSG ([0-9]+):([0-9]+) @!@!@\n`)
	graphNumber    = `([0-9]+|[1-9][0-9]{0,2}(?:,[0-9]{3})+)`
	graphStats     = regexp.MustCompile(`^` + graphNumber + ` states generated, ` + graphNumber + ` distinct states found, ` + graphNumber + ` states left on queue\.$`)
	graphInit      = regexp.MustCompile(`^Finished computing initial states: ` + graphNumber + ` distinct states? generated at [^\n]+\.$`)
	graphInitAll   = regexp.MustCompile(`^Finished computing initial states: ` + graphNumber + ` states generated, with ` + graphNumber + ` of them distinct at [^\n]+\.$`)
	graphDepth     = regexp.MustCompile(`^The depth of the complete state graph search is ([0-9]+)\.$`)
	graphFinished  = regexp.MustCompile(`^Finished in [^\n]+ at \([^\n]+\)$`)
)

// Parse is deliberately permissive for diagnostics. Graph evidence needs
// balanced frames, a complete successful run, and exact count reconciliation.
func graphCompletion(out string, g *Graph) (Result, error) {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	remaining := out
	stage := 0
	var initial, distinctInitial, generated, distinct, queued, searchDepth int64
	bad := func() (Result, error) {
		return Result{}, fmt.Errorf("incomplete or inconsistent TLC completion evidence")
	}
	for {
		i := strings.Index(remaining, "@!@!@")
		if i < 0 {
			break
		}
		remaining = remaining[i:]
		head := graphFrameHead.FindStringSubmatch(remaining)
		if head == nil {
			return bad()
		}
		code, err := strconv.Atoi(head[1])
		if err != nil || head[2] != "0" || stage == 6 {
			return bad()
		}
		remaining = remaining[len(head[0]):]
		end := strings.Index(remaining, "@!@!@")
		marker := "@!@!@ENDMSG " + head[1] + " @!@!@"
		if end < 0 || !strings.HasPrefix(remaining[end:], marker) {
			return bad()
		}
		body := strings.TrimSpace(remaining[:end])
		remaining = remaining[end+len(marker):]
		if remaining != "" && remaining[0] != '\n' {
			return bad()
		}
		numbers := func(re *regexp.Regexp, targets ...*int64) bool {
			m := re.FindStringSubmatch(body)
			if m == nil {
				return false
			}
			for i, target := range targets {
				n, err := strconv.ParseInt(strings.ReplaceAll(m[i+1], ",", ""), 10, 64)
				if err != nil {
					return false
				}
				*target = n
			}
			return true
		}
		switch code {
		case codeVersion:
			if stage != 0 || !strings.HasPrefix(body, "TLC2 Version ") || len(body) == len("TLC2 Version ") {
				return bad()
			}
			stage = 1
		case codeInitDone, 2191: // EC.TLC_INIT_GENERATED2 includes repeated initial generation.
			if stage != 1 {
				return bad()
			}
			if code == codeInitDone {
				if !numbers(graphInit, &initial) {
					return bad()
				}
				distinctInitial = initial
			} else if !numbers(graphInitAll, &initial, &distinctInitial) {
				return bad()
			}
			stage = 2
		case codeSuccess:
			if stage != 2 || !strings.HasPrefix(body, "Model checking completed. No error has been found.") {
				return bad()
			}
			stage = 3
		case codeStats:
			if stage != 3 || !numbers(graphStats, &generated, &distinct, &queued) {
				return bad()
			}
			stage = 4
		case codeDepth:
			if stage != 4 || !numbers(graphDepth, &searchDepth) {
				return bad()
			}
			stage = 5
		case codeFinished:
			if stage != 5 || !graphFinished.MatchString(body) {
				return bad()
			}
			stage = 6
		default:
			if !informational[code] || code == codeTraceState || code == codeBehavior || code == codeCounterexample {
				return bad()
			}
		}
	}
	if stage != 6 || queued != 0 || distinct != int64(len(g.States)) ||
		distinctInitial != int64(len(g.Initial)) || initial < distinctInitial ||
		generated < distinct || generated < initial || generated-initial != int64(len(g.Steps)) ||
		searchDepth < 1 || searchDepth > distinct {
		return bad()
	}
	run := Parse(out, 0)
	if run.Outcome != Passed || run.StatesGenerated != generated || run.DistinctStates != distinct || int64(run.Depth) != searchDepth {
		return bad()
	}
	return run, nil
}
