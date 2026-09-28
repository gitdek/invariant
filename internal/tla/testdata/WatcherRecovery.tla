---- MODULE WatcherRecovery ----
EXTENDS Naturals, FiniteSets

CONSTANTS Watchers, MaxCrashes

VARIABLES
    given,       \* commands a writer has given on the issue
    posts,       \* number of factory posts answering each command or event
    runs,        \* each agent run as recorded: "none", "recorded" or "done"
    runStarts,   \* how many times each agent run was started (history)
    ratPushed,   \* the issue's branch holds the ratification
    codePushed,  \* the issue's branch holds the code
    prs,         \* number of pull requests opened from the branch
    gate,        \* CI's gate on the pull request's head: "none" or "pass"
    mergeable,   \* whether GitHub can merge the pull request
    merges,      \* number of merges made
    lease,       \* the watcher holding the repository's lease, or "nobody"
    up,          \* whether each watcher is running
    inRun,       \* the agent run each watcher is in the middle of, or "none"
    crashes,     \* crashes and stalls so far
    unleased     \* watchers that ever took an effect without holding the lease (history)

vars == <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
          mergeable, merges, lease, up, inRun, crashes, unleased>>

\* Commands a writer can give: solve (a draft), ratify, a note, and a retry of a stopped build.
Cmds == {"solve", "ratify", "note", "retry"}

\* Everything the factory answers with a post: the commands, and the build and merge events.
Keys == Cmds \cup {"build", "merge"}

\* Steps that start an agent run.
RunKeys == {"solve", "build", "retry"}

NoOne == "nobody"

TypeOK ==
    /\ given \subseteq Cmds
    /\ posts \in [Keys -> Nat]
    /\ runs \in [RunKeys -> {"none", "recorded", "done"}]
    /\ runStarts \in [RunKeys -> Nat]
    /\ ratPushed \in BOOLEAN
    /\ codePushed \in BOOLEAN
    /\ prs \in Nat
    /\ gate \in {"none", "pass"}
    /\ mergeable \in BOOLEAN
    /\ merges \in Nat
    /\ lease \in Watchers \cup {NoOne}
    /\ up \in [Watchers -> BOOLEAN]
    /\ inRun \in [Watchers -> RunKeys \cup {"none"}]
    /\ crashes \in 0..MaxCrashes
    /\ unleased \subseteq Watchers

NoDoubleAnswer == \A k \in Keys : posts[k] <= 1

NoRunTwice == \A k \in RunKeys : runStarts[k] <= 1

OnePullRequest == prs <= 1

OneMerge == merges <= 1

OnlyHolderActs == unleased = {}

MergeOnlyAfterGate == merges >= 1 => gate = "pass" /\ mergeable

\* Helpers, computed only from what every watcher can see.

\* The step for k has been asked for: a command was given, the ratification was answered, or the gate passed.
Triggered(k) ==
    CASE k \in Cmds -> k \in given
      [] k = "build" -> posts["ratify"] >= 1
      [] k = "merge" -> gate = "pass"

\* A step is open until a post answers it; the post is always its last effect.
Active(k) == Triggered(k) /\ posts[k] = 0

\* Every effect of step k that comes before its post is already there.
EffectsDone(k) ==
    CASE k = "solve" -> runs["solve"] = "done"
      [] k = "ratify" -> ratPushed
      [] k \in {"build", "retry"} -> runs[k] = "done" /\ codePushed /\ prs >= 1
      [] k = "merge" -> merges >= 1 \/ ~mergeable
      [] k = "note" -> TRUE

MergeDone == merges >= 1 \/ posts["merge"] >= 1

\* Witnesses.

Merged == merges = 1 /\ posts["merge"] = 1

ReportedUnmergeable == gate = "pass" /\ ~mergeable /\ posts["merge"] = 1

StoppedRunReported == runs["build"] = "recorded" /\ posts["build"] = 1

RetryBuilt == posts["retry"] = 1 /\ prs = 1

RecoveredAfterCrashes == crashes = MaxCrashes /\ Merged

\* Properties.

CommandsAnswered == \A c \in Cmds : c \in given ~> posts[c] >= 1

GatedPullRequestsSettled == gate = "pass" ~> MergeDone

\* The model.

Init ==
    /\ given = {}
    /\ posts = [k \in Keys |-> 0]
    /\ runs = [k \in RunKeys |-> "none"]
    /\ runStarts = [k \in RunKeys |-> 0]
    /\ ratPushed = FALSE
    /\ codePushed = FALSE
    /\ prs = 0
    /\ gate = "none"
    /\ mergeable = FALSE
    /\ merges = 0
    /\ lease = NoOne
    /\ up = [w \in Watchers |-> TRUE]
    /\ inRun = [w \in Watchers |-> "none"]
    /\ crashes = 0
    /\ unleased = {}

\* A writer gives a command the issue protocol allows.
Give(c) ==
    /\ c \notin given
    /\ CASE c = "ratify" -> posts["solve"] >= 1
         [] c = "retry" -> posts["build"] >= 1 /\ prs = 0
         [] OTHER -> TRUE
    /\ given' = given \cup {c}
    /\ UNCHANGED <<posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, lease, up, inRun, crashes, unleased>>

\* CI's gate passes on the pull request's head; GitHub may or may not be able to merge it.
CIPass ==
    /\ prs >= 1
    /\ gate = "none"
    /\ gate' = "pass"
    /\ mergeable' \in BOOLEAN
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs,
                   merges, lease, up, inRun, crashes, unleased>>

Acquire(w) ==
    /\ up[w]
    /\ lease = NoOne
    /\ lease' = w
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, up, inRun, crashes, unleased>>

\* A lease whose holder is down is no longer renewed, and runs out.
Expire ==
    /\ lease # NoOne
    /\ ~up[lease]
    /\ lease' = NoOne
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, up, inRun, crashes, unleased>>

\* A watcher crashes, between effects or during an agent run; some other watcher keeps running.
Crash(w) ==
    /\ up[w]
    /\ crashes < MaxCrashes
    /\ \E v \in Watchers \ {w} : up[v]
    /\ up' = [up EXCEPT ![w] = FALSE]
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, lease, unleased>>

\* The holder stalls, say its laptop sleeps, past its lease; it wakes still running.
Stall(w) ==
    /\ up[w]
    /\ lease = w
    /\ crashes < MaxCrashes
    /\ lease' = NoOne
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, up, inRun, unleased>>

\* A crashed watcher restarts, remembering nothing.
Restart(w) ==
    /\ ~up[w]
    /\ lease # w
    /\ up' = [up EXCEPT ![w] = TRUE]
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, lease, inRun, crashes, unleased>>

\* A watcher may take an effect: it is running, holds the lease, and isn't waiting on a run.
Holding(w) == up[w] /\ lease = w /\ inRun[w] = "none"

EffectVars == <<given, gate, mergeable, lease, up, crashes, unleased>>

\* Record the agent run, then start it.
StartRun(w, k) ==
    /\ Holding(w)
    /\ k \in RunKeys
    /\ Active(k)
    /\ runs[k] = "none"
    /\ runs' = [runs EXCEPT ![k] = "recorded"]
    /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
    /\ inRun' = [inRun EXCEPT ![w] = k]
    /\ UNCHANGED <<posts, ratPushed, codePushed, prs, merges>>
    /\ UNCHANGED EffectVars

\* A run recorded and never finished stopped partway: say so, and start no other.
ReportStopped(w, k) ==
    /\ Holding(w)
    /\ k \in RunKeys
    /\ Active(k)
    /\ runs[k] = "recorded"
    /\ posts' = [posts EXCEPT ![k] = @ + 1]
    /\ UNCHANGED <<runs, runStarts, ratPushed, codePushed, prs, merges, inRun>>
    /\ UNCHANGED EffectVars

PushRatification(w, k) ==
    /\ Holding(w)
    /\ k = "ratify"
    /\ Active(k)
    /\ ~ratPushed
    /\ ratPushed' = TRUE
    /\ UNCHANGED <<posts, runs, runStarts, codePushed, prs, merges, inRun>>
    /\ UNCHANGED EffectVars

PushCode(w, k) ==
    /\ Holding(w)
    /\ k \in {"build", "retry"}
    /\ Active(k)
    /\ runs[k] = "done"
    /\ ~codePushed
    /\ codePushed' = TRUE
    /\ UNCHANGED <<posts, runs, runStarts, ratPushed, prs, merges, inRun>>
    /\ UNCHANGED EffectVars

OpenPullRequest(w, k) ==
    /\ Holding(w)
    /\ k \in {"build", "retry"}
    /\ Active(k)
    /\ runs[k] = "done"
    /\ codePushed
    /\ prs = 0
    /\ prs' = prs + 1
    /\ UNCHANGED <<posts, runs, runStarts, ratPushed, codePushed, merges, inRun>>
    /\ UNCHANGED EffectVars

MergePullRequest(w, k) ==
    /\ Holding(w)
    /\ k = "merge"
    /\ Active(k)
    /\ mergeable
    /\ merges = 0
    /\ merges' = merges + 1
    /\ UNCHANGED <<posts, runs, runStarts, ratPushed, codePushed, prs, inRun>>
    /\ UNCHANGED EffectVars

\* The post that answers step k, once no answer is there yet.
Post(w, k) ==
    /\ Holding(w)
    /\ Active(k)
    /\ EffectsDone(k)
    /\ posts' = [posts EXCEPT ![k] = @ + 1]
    /\ UNCHANGED <<runs, runStarts, ratPushed, codePushed, prs, merges, inRun>>
    /\ UNCHANGED EffectVars

Act(w) ==
    \E k \in Keys :
        \/ StartRun(w, k)
        \/ ReportStopped(w, k)
        \/ PushRatification(w, k)
        \/ PushCode(w, k)
        \/ OpenPullRequest(w, k)
        \/ MergePullRequest(w, k)
        \/ Post(w, k)

\* The agent run finishes and the holder records it.
FinishRun(w) ==
    /\ up[w]
    /\ lease = w
    /\ inRun[w] # "none"
    /\ runs' = [runs EXCEPT ![inRun[w]] = "done"]
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ UNCHANGED <<given, posts, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, lease, up, crashes, unleased>>

\* A watcher that finds it lost the lease drops its run without recording anything.
DropRun(w) ==
    /\ up[w]
    /\ lease # w
    /\ inRun[w] # "none"
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ UNCHANGED <<given, posts, runs, runStarts, ratPushed, codePushed, prs, gate,
                   mergeable, merges, lease, up, crashes, unleased>>

\* Nothing is left to answer.
Rest ==
    /\ \A k \in Keys : ~Active(k)
    /\ UNCHANGED vars

Next ==
    \/ \E c \in Cmds : Give(c)
    \/ CIPass
    \/ Expire
    \/ \E w \in Watchers :
        \/ Acquire(w) \/ Crash(w) \/ Stall(w) \/ Restart(w)
        \/ Act(w) \/ FinishRun(w) \/ DropRun(w)
    \/ Rest

\* Fairness.

FairAcquire == \A w \in Watchers : WF_vars(Acquire(w))

FairExpire == WF_vars(Expire)

FairAct == \A w \in Watchers : WF_vars(Act(w))

FairFinishRun == \A w \in Watchers : WF_vars(FinishRun(w))

FairDropRun == \A w \in Watchers : WF_vars(DropRun(w))

\* Known bugs, left out of Next.

\* A watcher posts its answer without looking for one already there.
PostWithoutLooking ==
    \E w \in Watchers, k \in Keys :
        /\ Holding(w)
        /\ Triggered(k)
        /\ EffectsDone(k)
        /\ posts[k] < 2
        /\ posts' = [posts EXCEPT ![k] = @ + 1]
        /\ UNCHANGED <<runs, runStarts, ratPushed, codePushed, prs, merges, inRun>>
        /\ UNCHANGED EffectVars

\* A watcher that finds a run that stopped partway starts it again.
RerunStoppedRun ==
    \E w \in Watchers, k \in RunKeys :
        /\ Holding(w)
        /\ Active(k)
        /\ runs[k] = "recorded"
        /\ runStarts[k] < 2
        /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
        /\ inRun' = [inRun EXCEPT ![w] = k]
        /\ UNCHANGED <<posts, runs, ratPushed, codePushed, prs, merges>>
        /\ UNCHANGED EffectVars

\* A watcher that woke after its lease ran out posts without checking the lease.
PostWithoutLease ==
    \E w \in Watchers, k \in Keys :
        /\ up[w]
        /\ lease # w
        /\ inRun[w] = "none"
        /\ Active(k)
        /\ EffectsDone(k)
        /\ posts' = [posts EXCEPT ![k] = @ + 1]
        /\ unleased' = unleased \cup {w}
        /\ UNCHANGED <<given, gate, mergeable, lease, up, crashes>>
        /\ UNCHANGED <<runs, runStarts, ratPushed, codePushed, prs, merges, inRun>>

Spec == Init /\ [][Next]_vars
====
