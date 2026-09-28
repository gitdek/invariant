// The rate limiter's environment: explores it on Invariant's harness within the
// model's bounds, trying every step Next names in every state it reaches.
import { explore } from "./invariant-explore.ts";
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

// MakeCall(a): only the bound on calls rules it out. When the queue is full,
// the code refuses the call and the environment records the refusal.
function makeCall(n: Node, a: { $mv: string }): Node | null {
  const api = a.$mv;
  if (n.env.made[api] >= MaxCalls) return null;
  const r = RateLimiter.from(n.sys);
  const env = copyEnv(n.env);
  const id = env.sent[api].length + r.waiting(api).length + 1;
  const o = r.request(api, id, env.clock);
  env.made[api] += 1;
  if (o.kind === "sent") env.sent[api].push({ id, madeAt: env.clock, at: env.clock });
  else if (o.kind === "refused") {
    env.refused[api].push({ madeAt: env.clock, tokens: o.tokens, queued: o.queued });
  }
  return { sys: r.snapshot(), env };
}

// Tick: only the bound on the clock rules it out.
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

// Done changes nothing, and before the end it can't happen yet.
function done(n: Node): Node {
  return n;
}

function tokensOf(n: Node, a: string): number {
  return n.sys.buckets[a]?.tokens ?? n.sys.capacity;
}

function waitingOf(n: Node, a: string) {
  return n.sys.buckets[a]?.waiting ?? [];
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

explore<Node>({
  initial: [initial()],
  steps: [
    { name: "MakeCall", args: apis.map((a) => [{ $mv: a }]), take: makeCall },
    { name: "Tick", take: tick },
    { name: "Done", take: done },
  ],
  abstract,
});
