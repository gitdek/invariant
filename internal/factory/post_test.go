package factory

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/verify"
)

// A post stays under GitHub's limit, however big the proposal: the marker
// is gzipped, and a post that's still too long drops its collapsed detail.
func TestPostsFitInAComment(t *testing.T) {
	model := strings.Repeat("Next == \\/ Write(p) \\/ Ship \\/ ShipFail\n", 3000) // about 130 KB of TLA+
	m := Marker{Kind: KindProposal, Proposal: &formalize.Proposal{Hash: "sha256:abc", Draft: formalize.Draft{Name: "big"}, ModuleText: model}}
	body := "Here's what I propose.\n\n<details>\n<summary>What changes, in TLA+</summary>\n\n```tla\n" + model + "```\n\n</details>\n\nTo ratify exactly this, comment `/invariant ratify abc`."
	out := post("proposal for ratification", body, m)
	if len(out) > maxComment {
		t.Fatalf("the post is %d characters", len(out))
	}
	if strings.Contains(out, "<details>") || !strings.Contains(out, "To ratify exactly this") || !strings.Contains(out, "GitHub limits") {
		t.Errorf("the post didn't drop its detail and keep the rest:\n%s", out[:min(len(out), 400)])
	}
	got, ok := DecodeMarker(out)
	if !ok || got.Proposal == nil || got.Proposal.ModuleText != model {
		t.Fatal("the marker didn't survive")
	}
}

// Markers from before they were gzipped still read, and a marker that
// would unzip to something huge doesn't.
func TestMarkersOldAndHostile(t *testing.T) {
	old := "<!-- invariant:" + base64.StdEncoding.EncodeToString([]byte(`{"kind":"pr","pr":7}`)) + " -->"
	if m, ok := DecodeMarker("◉ **Invariant** · pull request\n\n" + old); !ok || m.Kind != KindPR || m.PR != 7 {
		t.Errorf("an old marker: %+v %v", m, ok)
	}
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write([]byte(`{"kind":"pr","note":"`))
	w.Write(bytes.Repeat([]byte("a"), maxMarker+10))
	w.Write([]byte(`"}`))
	w.Close()
	if _, ok := DecodeMarker("<!-- invariant:z:" + base64.StdEncoding.EncodeToString(z.Bytes()) + " -->"); ok {
		t.Error("a marker that unzips past the limit was read")
	}
}

// A proposal with properties says they held, and under what.
func TestCheckedSaysPropertiesHeld(t *testing.T) {
	r := &verify.Report{Design: verify.Design{DistinctStates: 5188},
		Witnesses:  []verify.Witness{{Name: "Merged", Reached: true}},
		Properties: []verify.Property{{Name: "CommandsAnswered", Holds: true}, {Name: "Settled", Holds: true}},
		Fairness:   []verify.Fair{{Name: "FairAct", InNext: true}},
		Bugs:       []verify.Bug{{Name: "PostWithoutLooking", Caught: true}}}
	want := "TLC explored 5,188 states and found no violation and no deadlock, the witness was reached, all 2 properties held under the fairness, and the known bug `PostWithoutLooking` was caught."
	if got := checked(r); got != want {
		t.Errorf("checked =\n%s\nwant\n%s", got, want)
	}
}
