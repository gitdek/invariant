# Decisions

Decisions → recorded in → the decision record → rolled up into → the spec.

- **The decision is the unit.** Every time someone picks an option, it becomes an entry in [`log.md`](log.md).
- **Ceremony scales with the door.** A two-way door, meaning one that's cheap to reverse, gets one line: what, who, when. A one-way door, meaning one with a real cost to undo, also gets a full record: the options considered, why, and what would reopen it.
- **Provenance.** Every entry names the conversation or document it came from and who ratified it. Source documents live in [`sources/`](sources/).
- **The spec is derived.** [`SPEC.md`](../SPEC.md) contains only what ratified entries support. To change the spec, change the record first, then roll it up.
