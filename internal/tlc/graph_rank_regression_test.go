package tlc

import (
	"strings"
	"testing"
)

// realLifecycleDOT is what the pinned TLC, 1.7.4, wrote for ask3d's statue
// lifecycle model at MaxTasks = 2, with -dump dot,actionlabels. It ends its
// cluster with {rank = same; ...} layout groups, which the pinned fixtures
// leave out, and the real pinned run in CI's integration shards refused them.
const realLifecycleDOT = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-6505866907456613820 [label="status = <<>>",style = filled]
-6505866907456613820 -> 9005678570397026243 [label="Submit",color="black",fontcolor="black"];
9005678570397026243 [label="status = <<\"queued\">>"];
9005678570397026243 -> 3830319370671926376 [label="Finish",color="black",fontcolor="black"];
3830319370671926376 [label="status = <<\"succeeded\">>"];
3830319370671926376 -> 6192949454594225428 [label="Submit",color="black",fontcolor="black"];
6192949454594225428 [label="status = <<\"succeeded\", \"queued\">>"];
6192949454594225428 -> 1572005980506375662 [label="Finish",color="black",fontcolor="black"];
1572005980506375662 [label="status = <<\"succeeded\", \"succeeded\">>"];
1572005980506375662 -> 1572005980506375662 [label="Idle",color="black",fontcolor="black"];
{rank = same; -6505866907456613820;}
{rank = same; 9005678570397026243;}
{rank = same; 3830319370671926376;}
{rank = same; 6192949454594225428;}
{rank = same; 1572005980506375662;}
}
}`

func TestParseGraphDOTReadsTLCRankGroups(t *testing.T) {
	g, err := ParseGraphDOT(realLifecycleDOT)
	if err != nil {
		t.Fatalf("real TLC dump refused: %v", err)
	}
	if len(g.States) != 5 || len(g.Steps) != 5 || len(g.Initial) != 1 {
		t.Fatalf("got %d states, %d steps, %d initial; want 5, 5 and 1", len(g.States), len(g.Steps), len(g.Initial))
	}
	if g.Initial[0] != "-6505866907456613820" || g.States[0].Values["status"] != "<<>>" {
		t.Fatalf("wrong initial state: %v, %+v", g.Initial, g.States[0])
	}
	actions := map[string]int{}
	for _, s := range g.Steps {
		actions[s.Action]++
	}
	if actions["Submit"] != 2 || actions["Finish"] != 2 || actions["Idle"] != 1 {
		t.Fatalf("wrong steps: %+v", g.Steps)
	}
}

func TestParseGraphDOTRefusesMalformedRankGroups(t *testing.T) {
	const group = "{rank = same; -6505866907456613820;}"
	for name, replace := range map[string]string{
		"an unlisted state": "{rank = same; 42;}",
		"an empty group":    "{rank = same;}",
		"another rank":      "{rank = min; -6505866907456613820;}",
		"no separator":      "{rank = same; -6505866907456613820}",
		"a label":           "{rank = same; \"x\";}",
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(realLifecycleDOT, group, replace, 1)
			if bad == realLifecycleDOT {
				t.Fatal("the real dump lost its first rank group")
			}
			if g, err := ParseGraphDOT(bad); err == nil {
				t.Fatalf("malformed rank group parsed: %+v", g)
			}
		})
	}
	// A rank group outside the cluster isn't TLC's.
	outside := strings.Replace(realLifecycleDOT, "strict digraph DiskGraph {\n", "strict digraph DiskGraph {\n"+group+"\n", 1)
	if g, err := ParseGraphDOT(outside); err == nil {
		t.Fatalf("rank group outside the cluster parsed: %+v", g)
	}
}
