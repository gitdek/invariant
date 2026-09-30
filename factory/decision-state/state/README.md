# Decision state

`Journal` owns a fixed-capacity journal and its accumulated decision state.
Choose storage with `New(capacity)`, then call `Decide`, `Ratify`, or
`Supersede`. Each operation returns whether it ran. A refusal leaves every
line and every state field unchanged. `Current`, `Len`, and `Read` return
values without exposing the owned slice.

To read an existing journal, decode its records into `[]Line`, preserving
their order, and call `Fold(lines)`. Reject the journal if the returned
accepted count differs from `len(lines)`; that count is the zero-based index
of the first refused line. The returned journal contains only the accepted
prefix. `Apply` provides the same validation for a single incoming line,
including the placeholder door and status on ratify and supersede records.

Writers have a kind (`Person` or `Agent`) and a positive ID within that kind.
The reader's identity registry must assign the kind and map each identity to
a stable ID. `NoDoor` and `NoStatus` encode the journal field `"-"`; `None`
is the distinct initial decision status `"none"`.

The verified system is in `types.go`, `steps.go`, and `fold.go`. Its contracts
use fixed-capacity slices, quantified slot permissions, a plain replay loop,
and nonrecursive pure views. The constructor requires a capacity between
zero and the machine safety limit `MaxCapacity`.

`explore.go` is the model-checking environment. It contains all TLC bounds,
reconstructs an independent journal for each attempt, and converts state to
the TLA+ vocabulary. It attempts disabled actions too. `MaxLines` is marked
as a constructor parameter in the manifest.

This standalone factory checkout does not contain the `invariant decisions`
command. Its journal reader can integrate the core through `Fold` or `Apply`.
