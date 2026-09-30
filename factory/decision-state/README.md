# Decision state

Built by hand for [#193](https://github.com/gitdek/invariant/issues/193): Prove how a decision's state follows from its journal (#177, part 2)

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/193#issuecomment-5899178020). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/DecisionState.tla`](.invariant/specs/DecisionState.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | A decision's journal starts empty, and every step appends a line: its first records the decision, and later ones ratify or supersede it. |
| `TypeOK` | invariant | The journal is a list of lines, and the decision's status, door and who have the kinds of values they should. |
| `OnlyPeopleRatify` | invariant | Only a person ratifies: a ratified decision was ratified by a person. |
| `OneWayNeverDecided` | invariant | A one-way door is never decided: it's proposed until a person ratifies it. |
| `FirstLineOnly` | invariant | A journal's first line records its decision, and no later line does. |
| `RatifiedOnce` | invariant | A decision that's ratified or superseded isn't ratified again. |
| `Agreed` | invariant | The decision's state is the fold of its lines, in order, so every checkout that holds the same lines agrees on it. |
| `AgentProposedPersonRatified` | witness | An agent can propose a one-way door, and a person can ratify it. |
| `TwoWayDecidedByAgent` | witness | An agent can decide a two-way door. |
| `Superseded` | witness | A decision can be superseded. |
| `AgentRatifies` | bug | An agent's ratify line is taken. |
| `AgentDecidesOneWay` | bug | An agent decides a one-way door, where it may only propose one. |
| `RatifyAgain` | bug | A ratified or superseded decision is ratified again. |
| `DecideAgain` | bug | A later line records the decision again, and replaces it. |
