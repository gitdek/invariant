/**
 * Invariant's conformance driver for this project. It calls the library's
 * operations at random and records every state it passes through, in the
 * vocabulary of the ratified spec. Invariant hands the runs to TLC, which
 * checks that each one starts where Init allows and moves only by Next
 * steps. Refused operations are fine: a step that changes nothing is always
 * allowed.
 */
import { writeFileSync } from "node:fs";
import { Transaction } from "./src/transaction.ts";

// Values in the encoding Invariant reads: model values, sets and functions
// say what they are; plain objects are records.
const mv = (name: string) => ({ $mv: name });
const set = (items: unknown[]) => ({ $set: items });
const fn = (pairs: [unknown, unknown][]) => ({ $fn: pairs });

/** The abstraction: the transaction's state as the spec's variables. */
function abstract(t: Transaction) {
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

// mulberry32: a small seeded generator, so a run can be reproduced.
function generator(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), a | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const runs = Number(process.env.INVARIANT_RUNS ?? 300);
const steps = Number(process.env.INVARIANT_STEPS ?? 40);
const random = generator(Number(process.env.INVARIANT_SEED ?? 1));
const participants = ["r1", "r2", "r3"];
const pick = <T>(xs: readonly T[]): T => xs[Math.floor(random() * xs.length)];

// The coordinator's abort ends a run's interesting life, so it's picked less
// often than the rest.
const operations: ((t: Transaction) => unknown)[] = [
  (t) => t.prepare(pick(participants)),
  (t) => t.prepare(pick(participants)),
  (t) => t.giveUp(pick(participants)),
  (t) => t.recordVote(pick(participants)),
  (t) => t.recordVote(pick(participants)),
  (t) => t.commit(),
  (t) => t.learn(pick(participants)),
  (t) => t.learn(pick(participants)),
  (t) => (random() < 0.6 ? t.abort() : false),
];

const traces = [];
for (let run = 0; run < runs; run++) {
  const t = new Transaction(participants);
  const trace = [abstract(t)];
  for (let step = 0; step < steps; step++) {
    pick(operations)(t);
    trace.push(abstract(t));
  }
  traces.push(trace);
}
writeFileSync(process.env.INVARIANT_TRACES ?? "traces.json", JSON.stringify({ traces }));
