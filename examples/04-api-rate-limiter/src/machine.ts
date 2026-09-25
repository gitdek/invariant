// Token-bucket rate limiter as a state machine mirroring .invariant/specs/RateLimiter.tla.

export const APIS = ["a1", "a2"] as const;
export type Api = (typeof APIS)[number];

export const CAPACITY = 2;
export const MAX_CALLS = 4;
export const MAX_TIME = 3;

export interface Waiter {
  id: number;
  madeAt: number;
}

export interface SentCall {
  id: number;
  madeAt: number;
  at: number;
}

export interface State {
  tokens: Record<Api, number>;
  waiting: Record<Api, Waiter[]>;
  sent: Record<Api, SentCall[]>;
  made: Record<Api, number>;
  clock: number;
}

function perApi<T>(f: (a: Api) => T): Record<Api, T> {
  const r = {} as Record<Api, T>;
  for (const a of APIS) r[a] = f(a);
  return r;
}

function clone(s: State): State {
  return {
    tokens: { ...s.tokens },
    waiting: perApi((a) => s.waiting[a].map((w) => ({ ...w }))),
    sent: perApi((a) => s.sent[a].map((c) => ({ ...c }))),
    made: { ...s.made },
    clock: s.clock,
  };
}

export function init(): State {
  return {
    tokens: perApi(() => CAPACITY),
    waiting: perApi(() => []),
    sent: perApi(() => []),
    made: perApi(() => 0),
    clock: 0,
  };
}

// A call is made: it goes out now if a token is free and nobody waits, else it queues.
export function makeCall(s: State, a: Api): State | null {
  if (s.made[a] >= MAX_CALLS) return null;
  const n = clone(s);
  const id = s.made[a] + 1;
  n.made[a] = id;
  if (s.tokens[a] > 0 && s.waiting[a].length === 0) {
    n.tokens[a] = s.tokens[a] - 1;
    n.sent[a].push({ id, madeAt: s.clock, at: s.clock });
  } else {
    n.waiting[a].push({ id, madeAt: s.clock });
  }
  return n;
}

// One tick: every bucket refills one token, spent at once on the oldest waiting call.
export function tick(s: State): State | null {
  if (s.clock >= MAX_TIME) return null;
  const n = clone(s);
  n.clock = s.clock + 1;
  for (const a of APIS) {
    if (s.waiting[a].length === 0) {
      if (s.tokens[a] < CAPACITY) n.tokens[a] = s.tokens[a] + 1;
    } else {
      const head = s.waiting[a][0];
      n.sent[a].push({ id: head.id, madeAt: head.madeAt, at: s.clock + 1 });
      n.waiting[a] = n.waiting[a].slice(1);
    }
  }
  return n;
}

// The checked run is over: time and calls are used up.
export function done(s: State): State | null {
  if (s.clock !== MAX_TIME) return null;
  for (const a of APIS) if (s.made[a] !== MAX_CALLS) return null;
  return clone(s);
}

export function successors(s: State): State[] {
  const out: State[] = [];
  for (const a of APIS) {
    const t = makeCall(s, a);
    if (t) out.push(t);
  }
  const t = tick(s);
  if (t) out.push(t);
  const d = done(s);
  if (d) out.push(d);
  return out;
}

export function key(s: State): string {
  return JSON.stringify([
    APIS.map((a) => s.tokens[a]),
    APIS.map((a) => s.waiting[a].map((w) => [w.id, w.madeAt])),
    APIS.map((a) => s.sent[a].map((c) => [c.id, c.madeAt, c.at])),
    APIS.map((a) => s.made[a]),
    s.clock,
  ]);
}
