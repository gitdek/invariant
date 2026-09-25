// Bounded log shipping buffer, as a state machine that mirrors LogBuffer.tla.

export const PRODUCERS = ["p1", "p2"] as const;
export const CAPACITY = 2;
export const MAX_LINES = 2;

export type Producer = (typeof PRODUCERS)[number];

// A line is identified by the producer that wrote it and its per-producer number.
export type Line = { producer: Producer; n: number };

export type State = {
  buf: Line[];
  sent: Line[];
  log: Line[];
  written: Record<Producer, number>;
  retrying: boolean;
};

export function init(): State {
  return {
    buf: [],
    sent: [],
    log: [],
    written: { p1: 0, p2: 0 },
    retrying: false,
  };
}

// A producer writes its next line; it waits (is not enabled) while the buffer is full.
export function write(s: State, p: Producer): State | null {
  if (s.written[p] >= MAX_LINES) return null;
  if (s.buf.length >= CAPACITY) return null;
  const line: Line = { producer: p, n: s.written[p] + 1 };
  return {
    buf: [...s.buf, line],
    sent: [...s.sent],
    log: [...s.log, line],
    written: { ...s.written, [p]: s.written[p] + 1 },
    retrying: s.retrying,
  };
}

// The shipper sends the oldest line successfully and removes it.
export function ship(s: State): State | null {
  if (s.buf.length === 0) return null;
  return {
    buf: s.buf.slice(1),
    sent: [...s.sent, s.buf[0]],
    log: [...s.log],
    written: { ...s.written },
    retrying: false,
  };
}

// Sending the oldest line fails; it stays at the front to be retried.
export function shipFail(s: State): State | null {
  if (s.buf.length === 0) return null;
  if (s.retrying) return null;
  return {
    buf: [...s.buf],
    sent: [...s.sent],
    log: [...s.log],
    written: { ...s.written },
    retrying: true,
  };
}

// Every producer has written all its lines and everything has been shipped.
export function done(s: State): State | null {
  if (!PRODUCERS.every((p) => s.written[p] === MAX_LINES)) return null;
  if (s.buf.length !== 0) return null;
  return {
    buf: [],
    sent: [...s.sent],
    log: [...s.log],
    written: { ...s.written },
    retrying: s.retrying,
  };
}

export function successors(s: State): State[] {
  const out: (State | null)[] = [];
  for (const p of PRODUCERS) out.push(write(s, p));
  out.push(ship(s), shipFail(s), done(s));
  return out.filter((x): x is State => x !== null);
}

export function key(s: State): string {
  const line = (l: Line) => `${l.producer}.${l.n}`;
  return JSON.stringify([
    s.buf.map(line),
    s.sent.map(line),
    s.log.map(line),
    PRODUCERS.map((p) => s.written[p]),
    s.retrying,
  ]);
}
