/**
 * Invariant's conformance driver for this project. It explores, with
 * Invariant's harness, every state the library can reach: in each one it
 * tries every step the model's Next names, for every resource manager, and
 * lets the library refuse what the model rules out. It records every state in
 * the vocabulary of the ratified spec, and Invariant hands the runs to TLC.
 */
import { explore } from "./invariant-explore.ts";
import { Transaction, type Snapshot } from "./src/transaction.ts";

// The bounds: RM is a set of model values, so its size.
const RM = 3;

const participants = Array.from({ length: RM }, (_, i) => `r${i + 1}`);

// Values in the encoding Invariant reads: model values, sets and functions
// say what they are; plain objects are records. Sets are sorted so that equal
// states read the same.
const mv = (name: string) => ({ $mv: name });
const set = (items: unknown[]) => ({
  $set: [...items].sort((a, b) => (JSON.stringify(a) < JSON.stringify(b) ? -1 : JSON.stringify(a) > JSON.stringify(b) ? 1 : 0)),
});
const fn = (pairs: [unknown, unknown][]) => ({ $fn: pairs });

// A node is the library's state, read out of it. The environment keeps
// nothing of its own: the model has no bound on how many steps are taken.
type Node = Snapshot;

/** The abstraction: the transaction's state as the spec's variables. */
function abstract(node: Node) {
  const t = Transaction.restore(node);
  return {
    rmState: fn(t.participants.map((p) => [mv(p), t.stateOf(p)])),
    tmState: t.decided ? "done" : "init",
    tmPrepared: set(t.votes.map(mv)),
    msgs: set(
      t.messages.map((m) =>
        m.kind === "prepared" ? { type: "Prepared", rm: mv(m.from) } : { type: m.kind === "commit" ? "Commit" : "Abort" },
      ),
    ),
  };
}

// Makes the library from the node, calls the operation, and reads it back.
// A refusal leaves the library, and so the node, as it was.
const run =
  (op: (t: Transaction, p: string) => unknown) =>
  (node: Node, r?: { $mv: string }): Node => {
    const t = Transaction.restore(node);
    op(t, r?.$mv ?? "");
    return t.snapshot();
  };

const perRM = participants.map((p) => [mv(p)]);

explore<Node>({
  initial: [new Transaction(participants).snapshot()],
  abstract,
  steps: [
    { name: "TMCommit", take: run((t) => t.commit()) },
    { name: "TMAbort", take: run((t) => t.abort()) },
    { name: "TMRcvPrepared", args: perRM, take: run((t, p) => t.recordVote(p)) },
    { name: "RMPrepare", args: perRM, take: run((t, p) => t.prepare(p)) },
    { name: "RMChooseToAbort", args: perRM, take: run((t, p) => t.giveUp(p)) },
    { name: "RMRcvCommitMsg", args: perRM, take: run((t, p) => t.learnCommit(p)) },
    { name: "RMRcvAbortMsg", args: perRM, take: run((t, p) => t.learnAbort(p)) },
  ],
});
