# Decisions

Decisions → recorded in → the decision journal → rolled up into → the spec.

- **The decision is the unit.** Every time someone picks an option, it becomes a decision in the journal, [`journal/`](journal/): one file per decision, one line per write to it, each chained to the line before by hash. `invariant decisions decide` writes it, and [`log.md`](log.md)'s table is the journal's view.
- **Ceremony scales with the door.** A two-way door, meaning one that's cheap to reverse, gets one line: what, who, when. A one-way door, meaning one with a real cost to undo, also gets a full record: the options considered, why, and what would reopen it.
- **Provenance.** Every entry names the conversation or document it came from and who ratified it. Source documents live in [`sources/`](sources/).
- **The spec is derived.** [`SPEC.md`](../SPEC.md) contains only what ratified entries support. To change the spec, change the record first, then roll it up.
- **It's a graph.** Decisions refine, cite, reopen and supersede each other, and every mention of a decision in the docs and the code is an edge to it. `invariant decisions dependents D-NNNN` lists everything that rests on a decision: what reopening it would touch. CI checks that every cited decision exists and that the SPEC rests on none that's superseded.
