---- MODULE WatcherRecovery ----
EXTENDS Naturals, FiniteSets

CONSTANTS Watchers, MaxCrashes

VARIABLES
    given,       \* commands a writer has given on the issue
    posts,       \* number of factory posts answering each command or event
    retried,     \* a writer has given a retry, so the latest build is a retry's
    retryPosts,  \* factory posts answering the latest retry
    buildPosts,  \* factory posts answering the latest build: a pull request, or a stopped run
    runs,        \* each agent run as recorded: "none", "recorded" or "done"
    runStarts,   \* how many times each agent run was started (history)
    ratPushed,   \* the issue's branch holds the ratification
    codePushed,  \* the latest build's code is pushed
    prs,         \* pull requests opened for the latest build (history)
    pr,          \* the latest build's pull request: "none", "draft", "ready", "closed" or "merged"
    gate,        \* CI's gate on that pull request's head: "none" or "pass"
    mergeable,   \* whether GitHub can merge it
    merges,      \* number of merges made
    pred,        \* the draft the latest retry replaces: "none", "draft", "retired", "closed" or "merged"
    intent,      \* the latest retry's record that it replaces that draft is stored
    watched,     \* a writer's retry watches the latest build's failed draft again
    watchPosts,  \* factory posts answering that retry
    marked,      \* the factory marked the latest build's draft ready (history)
    lease,       \* the watcher holding the repository's lease, or "nobody"
    up,          \* whether each watcher is running
    inRun,       \* the agent run each watcher is in the middle of, or "none"
    crashes,     \* crashes and stalls so far
    unleased     \* watchers that ever took an effect without holding the lease (history)

vars == <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
          codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched, watchPosts,
          marked, lease, up, inRun, crashes, unleased>>

\* Commands a writer gives once: solve (a draft), ratify and a note. A retry
\* is apart: a writer can give one after each build that stopped or failed,
\* to build again, or to watch the build's failed draft again.
Cmds == {"solve", "ratify", "note"}

\* Everything else the factory answers with one post: the commands, and the merge.
Keys == Cmds \cup {"merge"}

\* Steps that start an agent run: the draft, and the latest build. A retry's
\* build is a new run, which leaves a watcher still in the one before stale.
RunKeys == {"solve", "build"}

NoOne == "nobody"

TypeOK ==
    /\ given \subseteq Cmds
    /\ posts \in [Keys -> Nat]
    /\ retried \in BOOLEAN
    /\ retryPosts \in Nat
    /\ buildPosts \in Nat
    /\ runs \in [RunKeys -> {"none", "recorded", "done"}]
    /\ runStarts \in [RunKeys -> Nat]
    /\ ratPushed \in BOOLEAN
    /\ codePushed \in BOOLEAN
    /\ prs \in Nat
    /\ pr \in {"none", "draft", "ready", "closed", "merged"}
    /\ gate \in {"none", "pass"}
    /\ mergeable \in BOOLEAN
    /\ merges \in Nat
    /\ pred \in {"none", "draft", "retired", "closed", "merged"}
    /\ intent \in BOOLEAN
    /\ watched \in BOOLEAN
    /\ watchPosts \in Nat
    /\ marked \in BOOLEAN
    /\ lease \in Watchers \cup {NoOne}
    /\ up \in [Watchers -> BOOLEAN]
    /\ inRun \in [Watchers -> RunKeys \cup {"none", "stale"}]
    /\ crashes \in 0..MaxCrashes
    /\ unleased \subseteq Watchers

NoDoubleAnswer ==
    /\ \A k \in Keys : posts[k] <= 1
    /\ retryPosts <= 1
    /\ watchPosts <= 1
    /\ buildPosts <= 1

NoRunTwice == \A k \in RunKeys : runStarts[k] <= 1

OnePullRequest == prs <= 1

OneOpenPullRequest == ~(pr \in {"draft", "ready"} /\ pred = "draft")

RetiredOnlyByRecord == pred = "retired" => intent

NoReplacingMerged == pred = "merged" => runs["build"] = "none" /\ prs = 0

OwnGate == gate = "pass" => prs >= 1

OwnRun == runs["build"] # "none" => runStarts["build"] >= 1

OneMerge == merges <= 1

OnlyHolderActs == unleased = {}

MergeOnlyAfterGate == merges >= 1 => gate = "pass" /\ mergeable

ReadiedOnlyWhenWatched == marked => watched /\ watchPosts >= 1

ReadiedOnlyAfterGate == marked => gate = "pass" /\ mergeable

\* Helpers, computed only from what every watcher can see.

\* The step for k has been asked for: a command was given, or the gate passed
\* on a pull request that isn't a failed draft, or on a failed draft that a
\* writer's retry watches again, once that retry is answered.
Triggered(k) ==
    CASE k \in Cmds -> k \in given
      [] k = "merge" -> gate = "pass" /\ (pr # "draft" \/ (watched /\ watchPosts >= 1))

\* A step is open until a post answers it; the post is always its last effect.
Active(k) == Triggered(k) /\ posts[k] = 0

\* Every effect of step k that comes before its post is already there.
EffectsDone(k) ==
    CASE k = "solve" -> runs["solve"] = "done"
      [] k = "ratify" -> ratPushed
      [] k = "merge" -> merges >= 1 \/ ~mergeable \/ pr \in {"closed", "merged"}
      [] k = "note" -> TRUE

\* The latest retry is still to be answered.
RetryActive == retried /\ retryPosts = 0

\* The retry that watches the latest build's failed draft again is still to
\* be answered.
WatchActive == watched /\ watchPosts = 0

\* The latest retry builds again: nothing was left to replace, or its record
\* says it replaces the draft, which is closed. A retry whose draft someone
\* merged, or closed before the record was stored, is answered without one.
Replacing == pred = "none" \/ (intent /\ pred \in {"retired", "closed"})

\* The latest build has been asked for: by the ratification's answer, or by
\* the latest retry's answer when that retry builds again.
BuildDue == IF retried THEN retryPosts >= 1 /\ Replacing ELSE posts["ratify"] >= 1

\* The latest build is asked for and no post answers it yet.
BuildActive == BuildDue /\ buildPosts = 0

MergeDone == merges >= 1 \/ posts["merge"] >= 1

\* Witnesses.

Merged == merges = 1 /\ posts["merge"] = 1

ReportedUnmergeable == gate = "pass" /\ ~mergeable /\ posts["merge"] = 1

StoppedRunReported == ~retried /\ runs["build"] = "recorded" /\ buildPosts = 1

RetryBuilt == retried /\ pred = "none" /\ prs = 1 /\ buildPosts = 1

RecoveredAfterCrashes == crashes = MaxCrashes /\ Merged

Replaced == pred = "retired" /\ prs = 1 /\ buildPosts = 1

ReplacedAndMerged == pred = "retired" /\ Merged

FailedAgain == retried /\ pr = "draft" /\ buildPosts = 1

StoppedAfterRetiring == pred = "retired" /\ runs["build"] = "recorded" /\ buildPosts = 1

MergedDraftKept == pred = "merged" /\ retryPosts = 1

ClosedDraftNoted == pred = "closed" /\ ~intent /\ retryPosts = 1

WatchedBeforeGate == watched /\ watchPosts = 1 /\ gate = "none"

WatchedAndMerged == watched /\ marked /\ Merged

WatchedButUnmergeable == watched /\ ~marked /\ gate = "pass" /\ ~mergeable /\ posts["merge"] = 1

\* Properties.

CommandsAnswered ==
    /\ \A c \in Cmds : c \in given ~> posts[c] >= 1
    /\ RetryActive ~> retryPosts >= 1
    /\ WatchActive ~> watchPosts >= 1

BuildsAnswered == BuildActive ~> buildPosts >= 1

GatedPullRequestsSettled == (gate = "pass" /\ pr # "draft") ~> MergeDone

WatchedDraftsSettled == (gate = "pass" /\ pr = "draft" /\ watched) ~> MergeDone

\* The model.

Init ==
    /\ given = {}
    /\ posts = [k \in Keys |-> 0]
    /\ retried = FALSE
    /\ retryPosts = 0
    /\ buildPosts = 0
    /\ runs = [k \in RunKeys |-> "none"]
    /\ runStarts = [k \in RunKeys |-> 0]
    /\ ratPushed = FALSE
    /\ codePushed = FALSE
    /\ prs = 0
    /\ pr = "none"
    /\ gate = "none"
    /\ mergeable = FALSE
    /\ merges = 0
    /\ pred = "none"
    /\ intent = FALSE
    /\ watched = FALSE
    /\ watchPosts = 0
    /\ marked = FALSE
    /\ lease = NoOne
    /\ up = [w \in Watchers |-> TRUE]
    /\ inRun = [w \in Watchers |-> "none"]
    /\ crashes = 0
    /\ unleased = {}

\* What the issue, its branch and its pull requests show every watcher.
Shared == <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
            codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched, watchPosts,
            marked>>

\* A writer gives a command the issue protocol allows.
Give(c) ==
    /\ c \notin given
    /\ c = "ratify" => posts["solve"] >= 1
    /\ given' = given \cup {c}
    /\ UNCHANGED <<posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
                   codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                   watchPosts, marked, lease, up, inRun, crashes, unleased>>

\* A writer's retry, once the latest build was answered and stopped or left a
\* failed draft: a new build, with its own agent run, that replaces that
\* draft. After a build that stopped, the draft an earlier retry replaced is
\* still the one this retry replaces, as that retry's record says. The
\* factory takes up an issue's commands in turn, so a retry comes once every
\* command before it is answered. A draft a writer's retry watches again is
\* never replaced.
GiveRetry ==
    /\ buildPosts >= 1
    /\ pr \in {"none", "draft"}
    /\ ~watched
    /\ merges = 0
    /\ \A k \in Keys : ~Active(k)
    /\ retried' = TRUE
    /\ retryPosts' = 0
    /\ buildPosts' = 0
    /\ pred' = IF pr = "draft" THEN "draft" ELSE pred
    /\ intent' = IF pr = "draft" THEN FALSE ELSE intent
    /\ runs' = [runs EXCEPT !["build"] = "none"]
    /\ runStarts' = [runStarts EXCEPT !["build"] = 0]
    /\ inRun' = [w \in Watchers |-> IF inRun[w] = "build" THEN "stale" ELSE inRun[w]]
    /\ codePushed' = FALSE
    /\ prs' = 0
    /\ pr' = "none"
    /\ gate' = "none"
    /\ mergeable' = FALSE
    /\ UNCHANGED <<given, posts, ratPushed, merges, watched, watchPosts, marked, lease, up,
                   crashes, unleased>>

\* A writer's retry that watches the latest build's failed draft again, as a
\* modeled project's is: it starts no build. Once the retry is answered and
\* CI's gate passes on the draft, the factory marks the draft ready and
\* merges it, or reports one it can't merge: GitHub refuses it, or it changes
\* the trusted base, which only a person merges. The factory takes up an
\* issue's commands in turn, so the retry comes once every command before it
\* is answered.
GiveWatch ==
    /\ buildPosts >= 1
    /\ pr = "draft"
    /\ ~watched
    /\ merges = 0
    /\ \A k \in Keys : ~Active(k)
    /\ watched' = TRUE
    /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts, ratPushed,
                   codePushed, prs, pr, gate, mergeable, merges, pred, intent, watchPosts,
                   marked, lease, up, inRun, crashes, unleased>>

\* CI's gate passes on the latest build's pull request; GitHub may or may not
\* be able to merge it.
CIPass ==
    /\ pr \in {"draft", "ready"}
    /\ gate = "none"
    /\ gate' = "pass"
    /\ mergeable' \in BOOLEAN
    /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                   ratPushed, codePushed, prs, pr, merges, pred, intent, watched, watchPosts,
                   marked, lease, up, inRun, crashes, unleased>>

\* Someone else closes an open pull request: the latest build's, or the draft
\* a retry replaces.
OthersClose ==
    /\ \/ /\ pr \in {"draft", "ready"}
          /\ pr' = "closed"
          /\ UNCHANGED pred
       \/ /\ pred = "draft"
          /\ pred' = "closed"
          /\ UNCHANGED pr
    /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                   ratPushed, codePushed, prs, gate, mergeable, merges, intent, watched,
                   watchPosts, marked, lease, up, inRun, crashes, unleased>>

\* Someone else merges an open pull request.
OthersMerge ==
    /\ \/ /\ pr \in {"draft", "ready"}
          /\ pr' = "merged"
          /\ UNCHANGED pred
       \/ /\ pred = "draft"
          /\ pred' = "merged"
          /\ UNCHANGED pr
    /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runs, runStarts,
                   ratPushed, codePushed, prs, gate, mergeable, merges, intent, watched,
                   watchPosts, marked, lease, up, inRun, crashes, unleased>>

Acquire(w) ==
    /\ up[w]
    /\ lease = NoOne
    /\ lease' = w
    /\ UNCHANGED <<Shared, up, inRun, crashes, unleased>>

\* A lease whose holder is down is no longer renewed, and runs out.
Expire ==
    /\ lease # NoOne
    /\ ~up[lease]
    /\ lease' = NoOne
    /\ UNCHANGED <<Shared, up, inRun, crashes, unleased>>

\* A watcher crashes, between effects or during an agent run; some other watcher keeps running.
Crash(w) ==
    /\ up[w]
    /\ crashes < MaxCrashes
    /\ \E v \in Watchers \ {w} : up[v]
    /\ up' = [up EXCEPT ![w] = FALSE]
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<Shared, lease, unleased>>

\* The holder stalls, say its laptop sleeps, past its lease; it wakes still running.
Stall(w) ==
    /\ up[w]
    /\ lease = w
    /\ crashes < MaxCrashes
    /\ lease' = NoOne
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<Shared, up, inRun, unleased>>

\* A crashed watcher restarts, remembering nothing.
Restart(w) ==
    /\ ~up[w]
    /\ lease # w
    /\ up' = [up EXCEPT ![w] = TRUE]
    /\ UNCHANGED <<Shared, lease, inRun, crashes, unleased>>

\* A watcher may take an effect: it is running, holds the lease, and isn't waiting on a run.
Holding(w) == up[w] /\ lease = w /\ inRun[w] = "none"

EffectVars == <<given, retried, gate, mergeable, watched, lease, up, crashes, unleased>>

\* Record the agent run, then start it: the draft's, or the latest build's.
StartRun(w, k) ==
    /\ Holding(w)
    /\ \/ k = "solve" /\ Active("solve")
       \/ k = "build" /\ BuildActive
    /\ runs[k] = "none"
    /\ runs' = [runs EXCEPT ![k] = "recorded"]
    /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
    /\ inRun' = [inRun EXCEPT ![w] = k]
    /\ UNCHANGED <<posts, retryPosts, buildPosts, ratPushed, codePushed, prs, pr, merges,
                   pred, intent>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* A run recorded and never finished stopped partway: say so, and start no other.
ReportStopped(w, k) ==
    /\ Holding(w)
    /\ runs[k] = "recorded"
    /\ \/ /\ k = "solve"
          /\ Active("solve")
          /\ posts' = [posts EXCEPT !["solve"] = @ + 1]
          /\ UNCHANGED buildPosts
       \/ /\ k = "build"
          /\ BuildActive
          /\ buildPosts' = buildPosts + 1
          /\ UNCHANGED posts
    /\ UNCHANGED <<retryPosts, runs, runStarts, ratPushed, codePushed, prs, pr, merges,
                   pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

PushRatification(w) ==
    /\ Holding(w)
    /\ Active("ratify")
    /\ ~ratPushed
    /\ ratPushed' = TRUE
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, codePushed, prs, pr,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Before closing the draft a retry replaces, store the retry's record that
\* it replaces it, where every watcher can read it.
RecordIntent(w) ==
    /\ Holding(w)
    /\ RetryActive
    /\ pred = "draft"
    /\ ~intent
    /\ intent' = TRUE
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                   prs, pr, merges, pred, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Close the draft the retry replaces, as its record says.
Retire(w) ==
    /\ Holding(w)
    /\ RetryActive
    /\ pred = "draft"
    /\ intent
    /\ pred' = "retired"
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                   prs, pr, merges, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

PushCode(w) ==
    /\ Holding(w)
    /\ BuildActive
    /\ runs["build"] = "done"
    /\ ~codePushed
    /\ codePushed' = TRUE
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, prs, pr,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Open the latest build's pull request: ready when its checks and review
\* passed, and a draft when they didn't.
OpenPullRequest(w, ready) ==
    /\ Holding(w)
    /\ BuildActive
    /\ runs["build"] = "done"
    /\ codePushed
    /\ prs = 0
    /\ prs' = prs + 1
    /\ pr' = IF ready THEN "ready" ELSE "draft"
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

MergePullRequest(w) ==
    /\ Holding(w)
    /\ Active("merge")
    /\ mergeable
    /\ pr = "ready"
    /\ merges = 0
    /\ merges' = merges + 1
    /\ pr' = "merged"
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                   prs, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* The post that answers step k, once no answer is there yet.
Post(w, k) ==
    /\ Holding(w)
    /\ Active(k)
    /\ EffectsDone(k)
    /\ posts' = [posts EXCEPT ![k] = @ + 1]
    /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Answer the latest retry, once the draft it replaces is closed or merged.
AnswerRetry(w) ==
    /\ Holding(w)
    /\ RetryActive
    /\ pred # "draft"
    /\ retryPosts' = retryPosts + 1
    /\ UNCHANGED <<posts, buildPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Answer the latest build with its pull request.
PostBuild(w) ==
    /\ Holding(w)
    /\ BuildActive
    /\ runs["build"] = "done"
    /\ codePushed
    /\ prs >= 1
    /\ buildPosts' = buildPosts + 1
    /\ UNCHANGED <<posts, retryPosts, runs, runStarts, ratPushed, codePushed, prs, pr,
                   merges, pred, intent, inRun>>
    /\ UNCHANGED <<watchPosts, marked>>
    /\ UNCHANGED EffectVars

\* Answer a writer's retry that watches the latest build's failed draft
\* again: the factory watches it, and says what follows once CI's gate passes.
AnswerWatch(w) ==
    /\ Holding(w)
    /\ WatchActive
    /\ watchPosts' = watchPosts + 1
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                   pr, merges, pred, intent, inRun, marked>>
    /\ UNCHANGED EffectVars

\* Mark the watched draft ready, once CI's gate passed on it and the factory
\* can merge it, so it merges as a ready pull request does.
MarkReady(w) ==
    /\ Holding(w)
    /\ Active("merge")
    /\ pr = "draft"
    /\ mergeable
    /\ pr' = "ready"
    /\ marked' = TRUE
    /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                   merges, pred, intent, inRun, watchPosts>>
    /\ UNCHANGED EffectVars

Act(w) ==
    \/ \E k \in RunKeys : StartRun(w, k) \/ ReportStopped(w, k)
    \/ PushRatification(w)
    \/ RecordIntent(w)
    \/ Retire(w)
    \/ PushCode(w)
    \/ \E ready \in BOOLEAN : OpenPullRequest(w, ready)
    \/ MergePullRequest(w)
    \/ \E k \in Keys : Post(w, k)
    \/ AnswerRetry(w)
    \/ PostBuild(w)
    \/ AnswerWatch(w)
    \/ MarkReady(w)

\* The agent run finishes and the holder records it. A run a retry made stale
\* is recorded as its own, which no longer counts for the latest build.
FinishRun(w) ==
    /\ up[w]
    /\ lease = w
    /\ inRun[w] # "none"
    /\ runs' = IF inRun[w] = "stale" THEN runs ELSE [runs EXCEPT ![inRun[w]] = "done"]
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runStarts, ratPushed,
                   codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                   watchPosts, marked, lease, up, crashes, unleased>>

\* A watcher that finds it lost the lease drops its run without recording anything.
DropRun(w) ==
    /\ up[w]
    /\ lease # w
    /\ inRun[w] # "none"
    /\ inRun' = [inRun EXCEPT ![w] = "none"]
    /\ UNCHANGED <<Shared, lease, up, crashes, unleased>>

\* Nothing is left to answer.
Rest ==
    /\ \A k \in Keys : ~Active(k)
    /\ ~RetryActive
    /\ ~WatchActive
    /\ ~BuildActive
    /\ UNCHANGED vars

Next ==
    \/ \E c \in Cmds : Give(c)
    \/ GiveRetry
    \/ GiveWatch
    \/ CIPass
    \/ OthersClose
    \/ OthersMerge
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
        /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                       pr, merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars

\* A watcher that finds a run that stopped partway starts it again.
RerunStoppedRun ==
    \E w \in Watchers, k \in RunKeys :
        /\ Holding(w)
        /\ \/ k = "solve" /\ Active("solve")
           \/ k = "build" /\ BuildActive
        /\ runs[k] = "recorded"
        /\ runStarts[k] < 2
        /\ runStarts' = [runStarts EXCEPT ![k] = @ + 1]
        /\ inRun' = [inRun EXCEPT ![w] = k]
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, ratPushed, codePushed, prs, pr,
                       merges, pred, intent>>
        /\ UNCHANGED <<watchPosts, marked>>
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
        /\ UNCHANGED <<given, retried, gate, mergeable, watched, lease, up, crashes>>
        /\ UNCHANGED <<retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed, prs,
                       pr, merges, pred, intent, inRun, watchPosts, marked>>

\* A watcher finds no open pull request for the latest build, misses the one
\* it opened that was since closed or merged, and opens another.
OpenWithoutLookingClosed ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ BuildActive
        /\ runs["build"] = "done"
        /\ codePushed
        /\ pr \in {"none", "closed", "merged"}
        /\ prs < 2
        /\ prs' = prs + 1
        /\ pr' = "draft"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars

\* A retry's build opens its pull request while the draft it replaces is
\* still open.
OpenBeforeRetiring ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ RetryActive
        /\ pred = "draft"
        /\ prs = 0
        /\ prs' = 1
        /\ pr' = "ready"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       merges, pred, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars

\* The factory closes the draft before storing the retry's record, so after a
\* crash its own close looks like someone else's.
RetireWithoutRecord ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ RetryActive
        /\ pred = "draft"
        /\ pred' = "retired"
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pr, merges, intent, inRun>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars

\* A retry whose draft someone merged builds again anyway.
ReplaceMergedDraft ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ retried
        /\ retryPosts >= 1
        /\ pred = "merged"
        /\ buildPosts = 0
        /\ runs["build"] = "none"
        /\ runs' = [runs EXCEPT !["build"] = "recorded"]
        /\ runStarts' = [runStarts EXCEPT !["build"] = @ + 1]
        /\ inRun' = [inRun EXCEPT ![w] = "build"]
        /\ UNCHANGED <<posts, retryPosts, buildPosts, ratPushed, codePushed, prs, pr, merges,
                       pred, intent>>
        /\ UNCHANGED <<watchPosts, marked>>
        /\ UNCHANGED EffectVars

\* A writer's retry keeps CI's result from the draft it replaces.
KeepGateAcrossRetry ==
    /\ buildPosts >= 1
    /\ pr \in {"none", "draft"}
    /\ ~watched
    /\ merges = 0
    /\ \A k \in Keys : ~Active(k)
    /\ retried' = TRUE
    /\ retryPosts' = 0
    /\ buildPosts' = 0
    /\ pred' = IF pr = "draft" THEN "draft" ELSE pred
    /\ intent' = IF pr = "draft" THEN FALSE ELSE intent
    /\ runs' = [runs EXCEPT !["build"] = "none"]
    /\ runStarts' = [runStarts EXCEPT !["build"] = 0]
    /\ inRun' = [w \in Watchers |-> IF inRun[w] = "build" THEN "stale" ELSE inRun[w]]
    /\ codePushed' = FALSE
    /\ prs' = 0
    /\ pr' = "none"
    /\ UNCHANGED <<given, posts, ratPushed, merges, gate, mergeable, watched, watchPosts,
                   marked, lease, up, crashes, unleased>>

\* A watcher whose agent run a retry made stale records it as the latest
\* build's.
FinishStaleRun ==
    \E w \in Watchers :
        /\ up[w]
        /\ lease = w
        /\ inRun[w] = "stale"
        /\ runs' = [runs EXCEPT !["build"] = "done"]
        /\ inRun' = [inRun EXCEPT ![w] = "none"]
        /\ UNCHANGED <<given, posts, retried, retryPosts, buildPosts, runStarts, ratPushed,
                       codePushed, prs, pr, gate, mergeable, merges, pred, intent, watched,
                       watchPosts, marked, lease, up, crashes, unleased>>

\* A watcher that woke after its lease ran out closes the draft without
\* checking the lease.
CloseWithoutLease ==
    \E w \in Watchers :
        /\ up[w]
        /\ lease # w
        /\ inRun[w] = "none"
        /\ RetryActive
        /\ pred = "draft"
        /\ intent
        /\ pred' = "retired"
        /\ unleased' = unleased \cup {w}
        /\ UNCHANGED <<given, retried, gate, mergeable, watched, lease, up, crashes>>
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pr, merges, intent, inRun, watchPosts, marked>>

\* The factory marks a failed draft ready when no writer's retry watches it
\* again.
MarkReadyWithoutWatch ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ pr = "draft"
        /\ ~watched
        /\ gate = "pass"
        /\ mergeable
        /\ pr' = "ready"
        /\ marked' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, merges, pred, intent, inRun, watchPosts>>
        /\ UNCHANGED EffectVars

\* The factory marks a watched draft ready before CI's gate passes on it.
MarkReadyBeforeGate ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ pr = "draft"
        /\ watched
        /\ watchPosts >= 1
        /\ gate = "none"
        /\ pr' = "ready"
        /\ marked' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, merges, pred, intent, inRun, watchPosts>>
        /\ UNCHANGED EffectVars

\* The factory marks ready a watched draft it can't merge, such as one that
\* changes the trusted base.
MarkUnmergeableReady ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ pr = "draft"
        /\ watched
        /\ watchPosts >= 1
        /\ gate = "pass"
        /\ ~mergeable
        /\ pr' = "ready"
        /\ marked' = TRUE
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, merges, pred, intent, inRun, watchPosts>>
        /\ UNCHANGED EffectVars

\* A watcher answers a retry that watches a failed draft again without
\* looking for an answer already there.
AnswerWatchWithoutLooking ==
    \E w \in Watchers :
        /\ Holding(w)
        /\ watched
        /\ watchPosts < 2
        /\ watchPosts' = watchPosts + 1
        /\ UNCHANGED <<posts, retryPosts, buildPosts, runs, runStarts, ratPushed, codePushed,
                       prs, pr, merges, pred, intent, inRun, marked>>
        /\ UNCHANGED EffectVars

Spec == Init /\ [][Next]_vars
====
