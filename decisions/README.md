# Decisions

Decisions → recorded in → the decision journal → rolled up into → the spec.

- **The decision is the unit.** Every time someone picks an option, it becomes a decision in the journal, [`journal/`](journal/): one file per decision, one line per write to it, each chained to the line before by hash. `invariant decisions decide` writes it, and [`log.md`](log.md)'s table is the journal's view.
- **Ceremony scales with the door.** A two-way door, meaning one that's cheap to reverse, gets one line: what, who, when. A one-way door, meaning one with a real cost to undo, also gets a full record: the options considered, why, and what would reopen it.
- **Provenance.** Every entry names the conversation or document it came from and who ratified it. Source documents live in [`sources/`](sources/).
- **The spec is derived.** [`SPEC.md`](../SPEC.md) contains only what ratified entries support. To change the spec, change the record first, then roll it up.
- **A ratified plan is a record too.** When a person ratifies a plan on an issue, the file that ratification commits, `.invariant/plans/issue-N.json`, says what was decided, who ratified it, when, and for exactly which proposal. The graph reads each one as a node named `P-` and its issue's number, and a SPEC line can rest on a plan by linking its file. Nobody restates a ratified plan as a decision by hand. A call the plan doesn't state, and every one-way door, still goes in the journal.
- **It's a graph.** Decisions refine, cite, reopen and supersede each other, and every mention of a decision in the docs and the code is an edge to it. `invariant decisions dependents D-NNNN` lists everything that rests on a decision, and `dependents P-NNNN` everything that rests on a plan: what reopening it would touch. CI checks that every cited decision and plan exists and that the SPEC rests on none that's superseded.
