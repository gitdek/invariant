// The environment: clients who take and give back tickets, at the bounds.
import { writeFileSync } from "node:fs";
import { Tickets } from "./src/tickets.ts";

const Capacity = 2;
const Clients = 3;

const clients = Array.from({ length: Clients }, (_, i) => `c${i + 1}`);

// The code, made again from a state: a pool at the bounds, holding held.
function make(held: string[]): Tickets {
  const t = new Tickets(Capacity);
  for (const c of held) t.take(c);
  return t;
}

const abstract = (held: string[]) => ({ held: { $set: held.map((c) => ({ $mv: c })) } });

const start: string[] = [];
const paths = new Map<string, string[][]>([[JSON.stringify(start), [start]]]);
let frontier = [start];
let depth = 0;
const traces: unknown[][] = [];
while (frontier.length > 0) {
  depth++;
  const next: string[][] = [];
  for (const s of frontier) {
    const path = paths.get(JSON.stringify(s)) as string[][];
    for (const c of clients) {
      for (const op of ["take", "give"] as const) {
        const t = make(s);
        if (!t[op](c)) continue;
        const u = t.held();
        traces.push([...path, u].map(abstract));
        const k = JSON.stringify(u);
        if (!paths.has(k)) {
          paths.set(k, [...path, u]);
          next.push(u);
        }
      }
    }
  }
  frontier = next;
}

if (process.env.INVARIANT_COUNT) {
  writeFileSync(process.env.INVARIANT_COUNT, JSON.stringify({ states: paths.size, depth }));
} else {
  writeFileSync(process.env.INVARIANT_TRACES as string, JSON.stringify({ traces }));
}
