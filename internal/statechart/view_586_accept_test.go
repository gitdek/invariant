package statechart_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// The evaluator refuses views larger than 1 MiB and expressions nested more
// than 64 levels. These bound view evaluation, not graph evidence: supported
// graphs are never sampled, and no arrow-count limit is enforced. JSON must
// be one object without duplicate keys (at any depth), unknown fields,
// trailing content, unsupported versions or ambiguous expression variants.
// Validation includes rules the ordered evaluation will never reach.

const reader586AllView = `{"version":1,"state":[{"id":"state","when":true,"label":"Work"}],"step":[{"id":"step","when":true,"label":"Advance"}]}`

func TestReader586ClosedViewLanguage(t *testing.T) {
	for name, view := range map[string]string{
		"trailing JSON":               reader586AllView + ` {}`,
		"unknown field":               strings.Replace(reader586AllView, `"version":1`, `"version":1,"imports":["os"]`, 1),
		"duplicate version":           strings.Replace(reader586AllView, `"version":1`, `"version":1,"version":1`, 1),
		"duplicate nested key":        strings.Replace(reader586AllView, `"label":"Work"`, `"label":"Work","label":"Work"`, 1),
		"unsupported version":         strings.Replace(reader586AllView, `"version":1`, `"version":2`, 1),
		"missing version":             strings.Replace(reader586AllView, `"version":1,`, "", 1),
		"empty state mapping":         `{"version":1,"state":[],"step":[{"id":"s","when":true,"label":"Step"}]}`,
		"empty label":                 strings.Replace(reader586AllView, `"label":"Work"`, `"label":"   "`, 1),
		"nonliteral label":            strings.Replace(reader586AllView, `"label":"Work"`, `"label":{"var":"phase"}`, 1),
		"state cannot hide":           strings.Replace(reader586AllView, `"label":"Work"`, `"label":"Work","hide":true`, 1),
		"hide must be bool":           strings.Replace(reader586AllView, `"label":"Advance"`, `"label":"Advance","hide":"yes"`, 1),
		"missing when":                strings.Replace(reader586AllView, `"when":true,`, "", 1),
		"script":                      strings.Replace(reader586AllView, `"when":true`, `"when":"open('/tmp/reader586')"`, 1),
		"call":                        strings.Replace(reader586AllView, `"when":true`, `"when":{"call":"exec","args":["touch marker"]}`, 1),
		"environment":                 strings.Replace(reader586AllView, `"when":true`, `"when":{"env":"HOME"}`, 1),
		"mutation":                    strings.Replace(reader586AllView, `"when":true`, `"when":{"set":{"phase":"done"}}`, 1),
		"loop":                        strings.Replace(reader586AllView, `"when":true`, `"when":{"while":true}`, 1),
		"time":                        strings.Replace(reader586AllView, `"when":true`, `"when":{"now":true}`, 1),
		"random":                      strings.Replace(reader586AllView, `"when":true`, `"when":{"random":true}`, 1),
		"state sees action":           strings.Replace(reader586AllView, `"when":true`, `"when":{"eq":[{"action":true},"Start"]}`, 1),
		"ambiguous operator":          strings.Replace(reader586AllView, `"when":true`, `"when":{"all":[],"any":[]}`, 1),
		"wrong equality arity":        strings.Replace(reader586AllView, `"when":true`, `"when":{"eq":["a"]}`, 1),
		"mixed equality types":        strings.Replace(reader586AllView, `"when":true`, `"when":{"eq":[true,"true"]}`, 1),
		"nonboolean conjunction":      strings.Replace(reader586AllView, `"when":true`, `"when":{"all":[true,"yes"]}`, 1),
		"duplicate rule ID":           strings.Replace(reader586AllView, `"label":"Work"}`, `"label":"Work"},{"id":"state","when":false,"label":"Other"}`, 1),
		"unreachable malicious rule":  strings.Replace(reader586AllView, `"label":"Work"}`, `"label":"Work"},{"id":"unused","when":{"import":"os"},"label":"Other"}`, 1),
		"view cannot supply evidence": strings.Replace(reader586AllView, `"version":1`, `"version":1,"invariants":{"Safe":true}`, 1),
	} {
		t.Run(name, func(t *testing.T) { reader586Refusal(t, reader586Input(), view, "invalid_view") })
	}
	deep := "true"
	for range 65 {
		deep = `{"not":` + deep + `}`
	}
	reader586Refusal(t, reader586Input(), strings.Replace(reader586AllView, `"when":true`, `"when":`+deep, 1), "invalid_view")
	reader586Refusal(t, reader586Input(), strings.Replace(reader586AllView, "Work", strings.Repeat("x", 1<<20), 1), "invalid_view")

	// Read literals as data, including text that looks like host code. No host
	// capability is in the evaluator's language, and labels aren't interpolated.
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	literal := fmt.Sprintf("$(touch %s); import os; ${READER586_ENV}", marker)
	t.Setenv("READER586_ENV", "must never substitute")
	view := strings.Replace(reader586AllView, `"Work"`, string(reader586JSON(t, literal)), 1)
	out := reader586Compute(t, reader586Input(), view)
	if len(out.States) != 1 || out.States[0].Label != literal {
		t.Fatalf("label interpreted: %+v", out.States)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("view performed I/O: %v", err)
	}

	// The useful boolean grammar, both transition endpoints, and first-match
	// semantics work together; a later matching fallback is not ambiguity.
	view = `{"version":1,"state":[{"id":"s","when":{"not":false},"label":"All"}],"step":[
	 {"id":"starting","when":{"all":[{"eq":[{"action":true},"Start"]},{"eq":[{"from":"phase"},"\"idle\""]},{"eq":[{"to":"phase"},"\"busy\""]}]},"label":"Begin"},
	 {"id":"remaining","when":{"any":[false,true]},"label":"Other"}]}`
	out = reader586Compute(t, reader586Input(), view)
	var begins int
	for _, a := range out.Arrows {
		if a.Label == "Begin" {
			begins += a.Count
		}
	}
	if begins != 3 {
		t.Fatalf("endpoint expressions or ordered matching lost starts: %+v", out.Arrows)
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("reader canceled by caller")
	cancel(cause)
	got, err := statechart.Compute(ctx, reader586Input(), []byte(reader586AllView))
	if got != nil || !errors.Is(err, cause) {
		t.Fatalf("canceled compute: %+v %v", got, err)
	}
}

func reader586Large(n int) (statechart.Input, string) {
	in := reader586Small(nil, []string{"1"}, nil)
	var view bytes.Buffer
	view.WriteString(`{"version":1,"state":[`)
	for i := 1; i <= n; i++ {
		id := fmt.Sprint(i)
		in.Graph.Graph.States = append(in.Graph.Graph.States, tlc.GraphState{ID: id, Label: "phase = " + id, Values: map[string]string{"phase": id}})
		in.Invariants[id] = map[string]bool{"Safe": true}
		next := i + 1
		if next > n {
			next = n
		}
		in.Graph.Graph.Steps = append(in.Graph.Graph.Steps, tlc.GraphStep{From: id, Action: "Next", To: fmt.Sprint(next)})
		if i > 1 {
			view.WriteByte(',')
		}
		fmt.Fprintf(&view, `{"id":"state-%d","when":{"eq":[{"var":"phase"},"%d"]},"label":"Stage %d"}`, i, i, i)
	}
	view.WriteString(`],"step":[{"id":"next","when":true,"label":"Continue"}]}`)
	in.Graph.Run.Depth = n
	reader586Counts(&in)
	return in, view.String()
}

func TestReader586SizeAndArrowTarget(t *testing.T) {
	in, view := reader586Large(500)
	out := reader586Compute(t, in, view)
	reader586Accounting(t, in, out)
	if len(out.States) != 500 || len(out.Arrows) != 500 || out.Report.Counts["tlc_steps"] != 500 || out.Report.Counts["chart_arrows"] != 500 || out.Report.Counts["hidden_steps"] != 0 || !out.Report.OverArrowTarget || out.Report.ArrowTarget != 20 {
		t.Fatalf("size/arrow advice lost evidence: states=%d arrows=%d report=%+v", len(out.States), len(out.Arrows), out.Report)
	}
	in, view = reader586Large(501)
	r := reader586Refusal(t, in, view, "too_large")
	if r.Actual != 501 || r.Limit != 500 || !strings.Contains(r.Error(), "501") || !strings.Contains(r.Error(), "500") {
		t.Fatalf("size refusal is not explicit: %+v", r)
	}
}
