// +gobra

package followup

// Classify applies the agent's inference to the plan's door and execution facts.
// A one-way door, an unexecuted change or an inference requiring owner input
// always leaves the decision proposed.
// @ requires true
// @ ensures d.Status == (twoWay && executed && !needsOwner ? Decided : Proposed)
// @ ensures d.DecidedByAgent == (twoWay && executed && !needsOwner)
// @ ensures d.NeedsOwner == needsOwner
// @ ensures d.PlanSource && d.ChangeSource && d.ReviewSource
func Classify(twoWay, executed, needsOwner bool) (d Draft) {
	if twoWay && executed && !needsOwner {
		return Draft{Status: Decided, DecidedByAgent: true, NeedsOwner: needsOwner,
			PlanSource: true, ChangeSource: true, ReviewSource: true}
	}
	return Draft{Status: Proposed, NeedsOwner: needsOwner,
		PlanSource: true, ChangeSource: true, ReviewSource: true}
}

// Bundle describes the atomic publication produced by the decision command.
// Citation slots correspond to the text identified by the merged change.
// Integrations execute this publication inside the one follow-up pull request.
// The workflow retains its identity; publication contents are output, not history
// kept by the workflow. Repairs retain this scope and decision identity.
type Bundle struct {
	Command         string
	Journal         string
	LogRow          string
	SpecCitations   []string
	ReadmeCitations []string
	RolledUp        bool
	DecisionsFile   bool
	SpecFile        bool
	ReadmeFile      bool
}

// RecordDecision constructs the publication for invariant decisions decide.
// Counts come from the merged change, with no fixed limit on modeled text size.
// @ requires 0 <= specCount && specCount <= MaxSize
// @ requires 0 <= readmeCount && readmeCount <= MaxSize
// @ ensures bundle.Command == "invariant decisions decide"
// @ ensures bundle.Journal == "newDecision" && bundle.LogRow == "newDecision"
// @ ensures bundle.RolledUp && bundle.DecisionsFile && bundle.SpecFile && bundle.ReadmeFile
// @ ensures len(bundle.SpecCitations) == specCount && len(bundle.ReadmeCitations) == readmeCount
// @ ensures forall i int :: { &bundle.SpecCitations[i] } 0 <= i && i < len(bundle.SpecCitations) ==> acc(&bundle.SpecCitations[i])
// @ ensures forall i int :: { &bundle.ReadmeCitations[i] } 0 <= i && i < len(bundle.ReadmeCitations) ==> acc(&bundle.ReadmeCitations[i])
// @ ensures forall i int :: { bundle.SpecCitations[i] } 0 <= i && i < len(bundle.SpecCitations) ==> bundle.SpecCitations[i] == "newDecision"
// @ ensures forall i int :: { bundle.ReadmeCitations[i] } 0 <= i && i < len(bundle.ReadmeCitations) ==> bundle.ReadmeCitations[i] == "newDecision"
func RecordDecision(specCount, readmeCount int) (bundle Bundle) {
	spec := make([]string, specCount)
	i := 0
	// @ invariant 0 <= i && i <= len(spec)
	// @ invariant len(spec) == specCount && specCount <= MaxSize
	// @ invariant forall j int :: { &spec[j] } 0 <= j && j < len(spec) ==> acc(&spec[j])
	// @ invariant forall j int :: { spec[j] } 0 <= j && j < i ==> spec[j] == "newDecision"
	for i < len(spec) {
		spec[i] = "newDecision"
		i++
	}
	readme := make([]string, readmeCount)
	i = 0
	// @ invariant 0 <= i && i <= len(readme)
	// @ invariant len(readme) == readmeCount && readmeCount <= MaxSize
	// @ invariant forall j int :: { &readme[j] } 0 <= j && j < len(readme) ==> acc(&readme[j])
	// @ invariant forall j int :: { readme[j] } 0 <= j && j < i ==> readme[j] == "newDecision"
	for i < len(readme) {
		readme[i] = "newDecision"
		i++
	}
	return Bundle{Command: "invariant decisions decide", Journal: "newDecision", LogRow: "newDecision",
		SpecCitations: spec, ReadmeCitations: readme, RolledUp: true,
		DecisionsFile: true, SpecFile: true, ReadmeFile: true}
}
