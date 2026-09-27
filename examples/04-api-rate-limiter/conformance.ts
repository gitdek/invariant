// The rate limiter's environment: explores it breadth first within the model's
// bounds, and records one run per step (or counts states with INVARIANT_COUNT).
import { writeFileSync } from "node:fs";
import { RateLimiter } from "./src/machine.ts";
import type { Snapshot } from "./src/machine.ts";

const Apis = 2;
const Capacity = 2;
const MaxCalls = 5;
const MaxTime = 3;
const MaxWaiting = 2;

const apis = Array.from({ length: Apis }, (_, i) => `a${i + 1}`);

interface SentCall {
  id: number;
  madeAt: number;
  at: number;
}

interface RefusedCall {
  madeAt: number;
  tokens: number;
  queued: number;
}

// The environment's part: what was sent and refused, how many calls each API
// was asked for, and the clock.
interface Env {
  sent: Record<string, SentCall[]>;
  refused: Record<string, RefusedCall[]>;
  made: Record<string, number>;
  clock: number;
}

interface Node {
  sys: Snapshot;
  env: Env;
}

function copyEnv(e: Env): Env {
  const sent: Record<string, SentCall[]> = {};
  const refused: Record<string, RefusedCall[]> = {};
  for (const a of apis) {
    sent[a] = e.sent[a].slice();
    refused[a] = e.refused[a].slice();
  }
  return { sent, refused, made: { ...e.made }, clock: e.clock };
}

function initial(): Node {
  const sent: Record<string, SentCall[]> = {};
  const refused: Record<string, RefusedCall[]> = {};
  const made: Record<string, number> = {};
  for (const a of apis) {
    sent[a] = [];
    refused[a] = [];
    made[a] = 0;
  }
  return { sys: new RateLimiter(Capacity, MaxWaiting).snapshot(), env: { sent, refused, made, clock: 0 } };
}

function makeCall(n: Node, a: string): Node | null {
  if (n.env.made[a] >= MaxCalls) return null;
  const r = RateLimiter.from(n.sys);
  const env = copyEnv(n.env);
  const id = env.sent[a].length + r.waiting(a).length + 1;
  const o = r.request(a, id, env.clock);
  env.made[a] += 1;
  if (o.kind === "sent") env.sent[a].push({ id, madeAt: env.clock, at: env.clock });
  else if (o.kind === "refused") {
    env.refused[a].push({ madeAt: env.clock, tokens: o.tokens, queued: o.queued });
  }
  return { sys: r.snapshot(), env };
}

function tick(n: Node): Node | null {
  if (n.env.clock >= MaxTime) return null;
  const r = RateLimiter.from(n.sys);
  const env = copyEnv(n.env);
  env.clock += 1;
  for (const c of r.refill(env.clock)) {
    env.sent[c.api].push({ id: c.id, madeAt: c.madeAt, at: c.at });
  }
  return { sys: r.snapshot(), env };
}

function done(n: Node): Node | null {
  if (n.env.clock !== MaxTime) return null;
  for (const a of apis) if (n.env.made[a] !== MaxCalls) return null;
  return n;
}

function successors(n: Node): Node[] {
  const out: Node[] = [];
  for (const a of apis) {
    const t = makeCall(n, a);
    if (t) out.push(t);
  }
  for (const t of [tick(n), done(n)]) if (t) out.push(t);
  return out;
}

function tokensOf(n: Node, a: string): number {
  return n.sys.buckets[a]?.tokens ?? n.sys.capacity;
}

function waitingOf(n: Node, a: string) {
  return n.sys.buckets[a]?.waiting ?? [];
}

function key(n: Node): string {
  return JSON.stringify([
    apis.map((a) => tokensOf(n, a)),
    apis.map((a) => waitingOf(n, a).map((w) => [w.id, w.madeAt])),
    apis.map((a) => n.env.sent[a].map((c) => [c.id, c.madeAt, c.at])),
    apis.map((a) => n.env.refused[a].map((r) => [r.madeAt, r.tokens, r.queued])),
    apis.map((a) => n.env.made[a]),
    n.env.clock,
  ]);
}

function fn<T>(f: (a: string) => T): unknown {
  return { $fn: apis.map((a) => [{ $mv: a }, f(a)]) };
}

export function abstract(n: Node): unknown {
  return {
    tokens: fn((a) => tokensOf(n, a)),
    waiting: fn((a) => ({ $seq: waitingOf(n, a).map((w) => ({ id: w.id, madeAt: w.madeAt })) })),
    sent: fn((a) => ({ $seq: n.env.sent[a].map((c) => ({ id: c.id, madeAt: c.madeAt, at: c.at })) })),
    refused: fn((a) => ({
      $seq: n.env.refused[a].map((r) => ({ madeAt: r.madeAt, tokens: r.tokens, queued: r.queued })),
    })),
    made: fn((a) => n.env.made[a]),
    clock: n.env.clock,
  };
}

const out = process.env.INVARIANT_TRACES;
if (!out) throw new Error("INVARIANT_TRACES is not set");
const counting = !!process.env.INVARIANT_COUNT;

const start = initial();
const nodes: Node[] = [start];
const parent: number[] = [-1];
const level: number[] = [1];
const index = new Map<string, number>([[key(start), 0]]);
const traces: unknown[][] = [];
let depth = 1;

function pathTo(i: number): unknown[] {
  const path: unknown[] = [];
  for (let cur = i; cur !== -1; cur = parent[cur]) path.push(abstract(nodes[cur]));
  return path.reverse();
}

for (let i = 0; i < nodes.length; i++) {
  const n = nodes[i];
  const prefix = counting ? [] : pathTo(i);
  for (const t of successors(n)) {
    if (!counting) traces.push([...prefix, abstract(t)]);
    const tk = key(t);
    if (!index.has(tk)) {
      index.set(tk, nodes.length);
      nodes.push(t);
      parent.push(i);
      level.push(level[i] + 1);
      if (level[i] + 1 > depth) depth = level[i] + 1;
    }
  }
}

if (counting) {
  writeFileSync(out, JSON.stringify({ states: nodes.length, depth }));
  console.log(`${nodes.length} states, depth ${depth}`);
} else {
  writeFileSync(out, JSON.stringify({ traces }));
  console.log(`${nodes.length} states, ${traces.length} runs`);
}
