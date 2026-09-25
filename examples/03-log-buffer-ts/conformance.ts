// Explores the state machine breadth first and records one run per step.

import { writeFileSync } from "node:fs";
import { init, successors, key, PRODUCERS } from "./src/machine.ts";
import type { State, Line } from "./src/machine.ts";

const lineAbs = (l: Line) => ({ $seq: [{ $mv: l.producer }, l.n] });

function abstract(s: State): unknown {
  return {
    buf: { $seq: s.buf.map(lineAbs) },
    sent: { $seq: s.sent.map(lineAbs) },
    log: { $seq: s.log.map(lineAbs) },
    written: { $fn: PRODUCERS.map((p) => [{ $mv: p }, s.written[p]]) },
    retrying: s.retrying,
  };
}

const start = init();
const paths = new Map<string, State[]>([[key(start), [start]]]);
const queue: State[] = [start];
const traces: unknown[][] = [];

while (queue.length > 0) {
  const s = queue.shift() as State;
  const path = paths.get(key(s)) as State[];
  for (const t of successors(s)) {
    traces.push([...path, t].map(abstract));
    const k = key(t);
    if (!paths.has(k)) {
      paths.set(k, [...path, t]);
      queue.push(t);
    }
  }
}

const out = process.env.INVARIANT_TRACES;
if (!out) throw new Error("INVARIANT_TRACES is not set");
writeFileSync(out, JSON.stringify({ traces }));
console.log(`${paths.size} states, ${traces.length} runs`);
