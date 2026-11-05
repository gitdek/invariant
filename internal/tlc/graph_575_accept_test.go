package tlc_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

// API contract for the reader graph foundation:
//
//   ParseGraphDOT(string) (*Graph, error)
//   Runner.DumpGraph(context.Context, string, string, Config) (*GraphResult, error)
//
// Graph has States []GraphState, Initial []string and Steps []GraphStep.
// GraphState has ID, Label string and Values map[string]string. Values retain
// the TLA+ text after the assignment, including continuation indentation and
// escapes inside strings; only the assignment and variable-separating newline
// are excluded. Label is the whole decoded DOT label, without normalization.
// GraphStep has From, Action and To string. IDs are TLC fingerprints as text,
// not display indexes. Every edge occurrence survives, including duplicates.
// Parsing establishes structural validity, not completed-run evidence; only
// DumpGraph supplies the latter.
//
// GraphResult has Graph Graph, Run Result, StateCount and StepCount int,
// Module string, Config Config, and Tool GraphTool. GraphTool has Image and
// JarSHA256 string (lowercase hex, no prefix). Config is a detached snapshot.

// This is the TLC 2.19 DOT-format fixture already used by the dashboard,
// copied verbatim so the new API has to support that existing dialect.
const graph575PinnedDOT = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-39061 [label="/\\ buf = <<>>\n/\\ n = 0",style = filled]
-39061 -> 1090 [label="Write",color="black",fontcolor="black"];
1090 [label="/\\ buf = <<\"a\">>\n/\\ n = 1"];
1090 -> 2207 [label="Write",color="black",fontcolor="black"];
2207 [label="/\\ buf = <<\"a\", \"b\">>\n/\\ n = 2"];
2207 -> 2207 [label="Stay",color="black",fontcolor="black"];
1090 -> -39061 [label="Ship",color="black",fontcolor="black"];
}
}`

var graph575IDs = []string{"-9223372036854775807", "9007199254740993", "734291"}

const graph575Payload = `[message |-> "a  b \"quoted\" \\path \n",
  sequence |-> <<1, TRUE, [ok |-> FALSE]>>,
  members |-> {m1, "m1"},
  mapping |-> (m1 :> <<>> @@ m2 :> <<"x">>)]`

var graph575Edges = []tlc.GraphStep{
	{From: graph575IDs[0], Action: "Stay", To: graph575IDs[0]},
	{From: graph575IDs[0], Action: "Advance", To: graph575IDs[2]},
	{From: graph575IDs[0], Action: "Alternate", To: graph575IDs[2]},
	{From: graph575IDs[1], Action: "Advance", To: graph575IDs[2]},
	{From: graph575IDs[2], Action: "Stay", To: graph575IDs[2]},
	{From: graph575IDs[2], Action: "Reset", To: graph575IDs[0]},
	{From: graph575IDs[0], Action: "Advance", To: graph575IDs[2]},
}

func graph575Quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

// Adversarial values and multiplicities in the same pinned DOT dialect.
func graph575DOT() string {
	var b strings.Builder
	b.WriteString("strict digraph DiskGraph {\nnodesep=0.35;\nsubgraph cluster_graph {\ncolor=\"white\";\n")
	for i, id := range graph575IDs {
		label := fmt.Sprintf("/\\ n = %d\n/\\ payload = %s", i, graph575Payload)
		init := ""
		if i < 2 {
			init = ",style = filled"
		}
		fmt.Fprintf(&b, "%s [label=%s%s];\n", id, graph575Quote(label), init)
	}
	for _, e := range graph575Edges {
		fmt.Fprintf(&b, "%s -> %s [label=%s,color=\"black\",fontcolor=\"black\"];\n", e.From, e.To, graph575Quote(e.Action))
	}
	b.WriteString("}\n}\n")
	return b.String()
}

func graph575AssertGraph(t *testing.T, g *tlc.Graph) {
	t.Helper()
	if g == nil || len(g.States) != 3 || len(g.Initial) != 2 || len(g.Steps) != len(graph575Edges) {
		t.Fatalf("lost graph data: %+v", g)
	}
	states := map[string]tlc.GraphState{}
	for _, s := range g.States {
		if _, exists := states[s.ID]; exists {
			t.Fatalf("duplicate state identity %q", s.ID)
		}
		states[s.ID] = s
	}
	for i, id := range graph575IDs {
		s, ok := states[id]
		wantValues := map[string]string{"n": fmt.Sprint(i), "payload": graph575Payload}
		wantLabel := fmt.Sprintf("/\\ n = %d\n/\\ payload = %s", i, graph575Payload)
		if !ok || !reflect.DeepEqual(s.Values, wantValues) || s.Label != wantLabel {
			t.Errorf("state %s lost its identity or exact values: %+v; want %q", id, s, wantValues)
		}
	}
	initial := map[string]int{}
	for _, id := range g.Initial {
		initial[id]++
	}
	if !reflect.DeepEqual(initial, map[string]int{graph575IDs[0]: 1, graph575IDs[1]: 1}) {
		t.Errorf("initial states = %v", initial)
	}
	counts := func(edges []tlc.GraphStep) map[tlc.GraphStep]int {
		m := map[tlc.GraphStep]int{}
		for _, e := range edges {
			m[e]++
		}
		return m
	}
	if got, want := counts(g.Steps), counts(graph575Edges); !reflect.DeepEqual(got, want) {
		t.Errorf("steps = %v; want %v", got, want)
	}
}

func TestGraph575PreservesDumpData(t *testing.T) {
	g, err := tlc.ParseGraphDOT(graph575PinnedDOT)
	if err != nil || g == nil {
		t.Fatalf("pinned-format fixture: %+v, %v", g, err)
	}
	if len(g.States) != 3 || len(g.Initial) != 1 || g.Initial[0] != "-39061" || len(g.Steps) != 4 {
		t.Fatalf("pinned fixture lost data: %+v", g)
	}
	for _, s := range g.States {
		if s.ID == "2207" && s.Values["buf"] != `<<"a", "b">>` {
			t.Fatalf("tuple value changed: %+v", s)
		}
	}
	g, err = tlc.ParseGraphDOT(graph575DOT())
	if err != nil {
		t.Fatal(err)
	}
	graph575AssertGraph(t, g)

	t.Run("single variable and long label", func(t *testing.T) {
		value := `"` + strings.Repeat("a  b", 32768) + `"`
		dot := "strict digraph DiskGraph {\nsubgraph cluster_graph {\n1 [label=" + graph575Quote("text = "+value) + ",style = filled]\n1 -> 1 [label=\"Hold\"];\n}\n}\n"
		g, err := tlc.ParseGraphDOT(dot)
		if err != nil || g == nil || len(g.States) != 1 || g.States[0].Values["text"] != value || g.States[0].Label != "text = "+value {
			t.Fatalf("long single-variable label was lost: graph=%v, error=%v", g != nil, err)
		}
	})
	t.Run("identity is not the display label", func(t *testing.T) {
		dot := "strict digraph DiskGraph {\nsubgraph cluster_graph {\n1 [label=\"n = 0\",style = filled]\n1 -> 2 [label=\"Same\"];\n2 [label=\"n = 0\"];\n}\n}\n"
		g, err := tlc.ParseGraphDOT(dot)
		if err != nil || g == nil || len(g.States) != 2 || len(g.Steps) != 1 || g.Steps[0].To != "2" {
			t.Fatalf("concrete identities collapsed: %+v, %v", g, err)
		}
	})
}

func TestGraph575RefusesMalformedDOT(t *testing.T) {
	valid := graph575DOT()
	for name, dot := range map[string]string{
		"empty":               "",
		"no states":           "strict digraph DiskGraph {\nsubgraph cluster_graph {\n}\n}\n",
		"missing outer close": strings.TrimSuffix(valid, "}\n"),
		"missing all closes":  strings.TrimSuffix(valid, "}\n}\n"),
		"truncated label":     "strict digraph DiskGraph {\n1 [label=\"n = 0",
		"dangling endpoint":   strings.Replace(valid, graph575IDs[0]+" -> "+graph575IDs[2], graph575IDs[0]+" -> 999", 1),
		"no initial state":    strings.ReplaceAll(valid, ",style = filled", ""),
		"unlabeled step":      strings.Replace(valid, `[label="Stay",color="black",fontcolor="black"]`, "", 1),
		"empty action":        strings.Replace(valid, `label="Stay"`, `label=""`, 1),
		"malformed edge":      strings.Replace(valid, " -> ", " --> ", 1),
		"garbage record":      strings.Replace(valid, "color=\"white\";", "color=\"white\";\nTHIS IS NOT DOT;", 1),
		"trailing garbage":    valid + "unconsumed garbage\n",
		"conflicting state":   strings.Replace(valid, "}\n}\n", graph575IDs[0]+" [label=\"n = 99\"];\n}\n}\n", 1),
		"duplicate variable":  strings.Replace(valid, `/\\ n = 0\n`, `/\\ n = 0\n/\\ n = 1\n`, 1),
		"missing value":       strings.Replace(valid, `/\\ n = 0\n`, `/\\ n = \n`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			g, err := tlc.ParseGraphDOT(dot)
			if err == nil || g != nil {
				t.Fatalf("invalid dump supplied a graph: %+v, %v", g, err)
			}
		})
	}
}
