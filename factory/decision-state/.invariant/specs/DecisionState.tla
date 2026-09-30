---- MODULE DecisionState ----
\* How a decision's state follows from its journal (#177, part 2). A
\* decision's journal is a list of lines, each recording one thing that
\* happened to it: its first line records the decision, and later lines
\* ratify or supersede it. People and agents write lines, and every
\* checkout folds the same lines, in order, into the decision's state.
EXTENDS Naturals, Sequences

CONSTANTS People, Agents, MaxLines

VARIABLES lines, status, door, who

vars == <<lines, status, door, who>>

Writers == People \cup Agents
Doors == {"one-way", "two-way"}
Statuses == {"none", "proposed", "decided", "ratified", "superseded"}
Ops == {"decide", "ratify", "supersede"}

Line == [op : Ops, by : Writers, door : Doors \cup {"-"}, status : Statuses \cup {"-"}]

TypeOK ==
    /\ lines \in Seq(Line)
    /\ Len(lines) <= MaxLines
    /\ status \in Statuses
    /\ door \in Doors \cup {"-"}
    /\ who \in Writers \cup {"-"}

\* Only a person ratifies: a ratified decision was ratified by a person.
OnlyPeopleRatify == status = "ratified" => who \in People

\* A one-way door is never decided: it's proposed until a person ratifies it.
OneWayNeverDecided == door = "one-way" => status # "decided"

\* A journal's first line records its decision, and no later line does.
FirstLineOnly == \A i \in 1..Len(lines) : (lines[i].op = "decide") <=> (i = 1)

\* A decision that's ratified or superseded isn't ratified again.
RatifiedOnce ==
    \A i, j \in 1..Len(lines) :
        (i < j /\ lines[j].op = "ratify") =>
            /\ lines[i].op # "ratify"
            /\ lines[i].op # "supersede"
            /\ ~(lines[i].op = "decide" /\ lines[i].status = "ratified")

\* The state is the fold of the lines: folding them again, from nothing, gives
\* the same state, so every checkout with the same lines agrees.
Fold(ls) ==
    LET step[i \in 0..Len(ls)] ==
        IF i = 0 THEN [status |-> "none", door |-> "-", who |-> "-"]
        ELSE LET s == step[i - 1] IN
             LET l == ls[i] IN
             CASE l.op = "decide" -> [status |-> l.status, door |-> l.door, who |-> l.by]
               [] l.op = "ratify" -> [status |-> "ratified", door |-> s.door, who |-> l.by]
               [] l.op = "supersede" -> [status |-> "superseded", door |-> s.door, who |-> s.who]
    IN step[Len(ls)]
Agreed == Fold(lines) = [status |-> status, door |-> door, who |-> who]

\* Witnesses.
AgentProposedPersonRatified ==
    /\ Len(lines) >= 2
    /\ lines[1].by \in Agents /\ lines[1].door = "one-way"
    /\ status = "ratified"
TwoWayDecidedByAgent == door = "two-way" /\ status = "decided" /\ who \in Agents
Superseded == status = "superseded"

Init ==
    /\ lines = <<>>
    /\ status = "none"
    /\ door = "-"
    /\ who = "-"

Add(l) == lines' = Append(lines, l)

\* A writer records a decision: people decide, propose or ratify it at once;
\* an agent decides a two-way door or proposes, and never ratifies, and a
\* one-way door is only proposed, unless a person ratifies it as they record
\* it.
Decide(w, d, s) ==
    /\ lines = <<>>
    /\ Len(lines) < MaxLines
    /\ s \in {"proposed", "decided", "ratified"}
    /\ s = "ratified" => w \in People
    /\ d = "one-way" => s # "decided"
    /\ Add([op |-> "decide", by |-> w, door |-> d, status |-> s])
    /\ status' = s
    /\ door' = d
    /\ who' = w

\* A person ratifies a decision that's proposed or decided.
Ratify(p) ==
    /\ Len(lines) < MaxLines
    /\ status \in {"proposed", "decided"}
    /\ Add([op |-> "ratify", by |-> p, door |-> "-", status |-> "-"])
    /\ status' = "ratified"
    /\ who' = p
    /\ UNCHANGED door

\* A later decision supersedes this one.
Supersede(w) ==
    /\ Len(lines) < MaxLines
    /\ status \in {"proposed", "decided", "ratified"}
    /\ Add([op |-> "supersede", by |-> w, door |-> "-", status |-> "-"])
    /\ status' = "superseded"
    /\ UNCHANGED <<door, who>>

Done ==
    /\ (status = "superseded" \/ Len(lines) = MaxLines)
    /\ UNCHANGED vars

Next ==
    \/ \E w \in Writers, d \in Doors, s \in Statuses : Decide(w, d, s)
    \/ \E p \in People : Ratify(p)
    \/ \E w \in Writers : Supersede(w)
    \/ Done

\* A known bug: an agent's ratify line is taken.
AgentRatifies ==
    \E a \in Agents :
        /\ Len(lines) < MaxLines
        /\ status \in {"proposed", "decided"}
        /\ Add([op |-> "ratify", by |-> a, door |-> "-", status |-> "-"])
        /\ status' = "ratified"
        /\ who' = a
        /\ UNCHANGED door

\* A known bug: an agent decides a one-way door, where it may only propose.
AgentDecidesOneWay ==
    \E a \in Agents :
        /\ lines = <<>>
        /\ Add([op |-> "decide", by |-> a, door |-> "one-way", status |-> "decided"])
        /\ status' = "decided"
        /\ door' = "one-way"
        /\ who' = a

\* A known bug: a ratified or superseded decision is ratified again.
RatifyAgain ==
    \E p \in People :
        /\ Len(lines) < MaxLines
        /\ status \in {"ratified", "superseded"}
        /\ Add([op |-> "ratify", by |-> p, door |-> "-", status |-> "-"])
        /\ status' = "ratified"
        /\ who' = p
        /\ UNCHANGED door

\* A known bug: a later line records the decision again, and replaces it.
DecideAgain ==
    \E w \in Writers :
        /\ Len(lines) >= 1 /\ Len(lines) < MaxLines
        /\ Add([op |-> "decide", by |-> w, door |-> "two-way", status |-> "decided"])
        /\ status' = "decided"
        /\ door' = "two-way"
        /\ who' = w

Spec == Init /\ [][Next]_vars
====
