// Explores the state machine breadth first and records one run per step.
import { writeFileSync } from "node:fs";
import { APIS, init, key, successors } from "./src/machine.ts";
import type { State } from "./src/machine.ts";

function fn<T>(f: (a: (typeof APIS)[number]) => T): unknown {
  return { $fn: APIS.map((a) => [{ $mv: a }, f(a)]) };
}

export function abstract(s: State): unknown {
  return {
    tokens: fn((a) => s.tokens[a]),
    waiting: fn((a) => ({ $seq: s.waiting[a].map((w) => ({ id: w.id, madeAt: w.madeAt })) })),
    sent: fn((a) => ({ $seq: s.sent[a].map((c) => ({ id: c.id, madeAt: c.madeAt, at: c.at })) })),
    made: fn((a) => s.made[a]),
    clock: s.clock,
  };
}

const start = init();
const parent = new Map<string, string | null>([[key(start), null]]);
const states = new Map<string, State>([[key(start), start]]);
const queue: State[] = [start];
const traces: unknown[][] = [];

function pathTo(k: string): unknown[] {
  const path: unknown[] = [];
  let cur: string | null = k;
  while (cur !== null) {
    path.push(abstract(states.get(cur)!));
    cur = parent.get(cur)!;
  }
  return path.reverse();
}

while (queue.length > 0) {
  const s = queue.shift()!;
  const k = key(s);
  const prefix = pathTo(k);
  for (const t of successors(s)) {
    traces.push([...prefix, abstract(t)]);
    const tk = key(t);
    if (!states.has(tk)) {
      states.set(tk, t);
      parent.set(tk, k);
      queue.push(t);
    }
  }
}

const out = process.env.INVARIANT_TRACES;
if (!out) throw new Error("INVARIANT_TRACES is not set");
writeFileSync(out, JSON.stringify({ traces }));
console.log(`${states.size} states, ${traces.length} runs`);
