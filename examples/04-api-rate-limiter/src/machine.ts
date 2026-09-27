// Token-bucket rate limiter, one bucket and one queue of waiting calls per API.
// Its capacity and how many calls may wait are chosen when it's made; the time
// is passed in with each operation. See .invariant/specs/RateLimiter.tla.

export interface Waiter {
  id: number;
  madeAt: number;
}

// A waiting call that a refilled token sent out.
export interface Released {
  api: string;
  id: number;
  madeAt: number;
  at: number;
}

// What happened to a call: it went out now, it waits, or it was refused
// with the bucket and queue it met.
export type Outcome =
  | { kind: "sent" }
  | { kind: "queued" }
  | { kind: "refused"; tokens: number; queued: number };

export interface BucketState {
  tokens: number;
  waiting: Waiter[];
}

// An API missing from buckets has a full bucket and nobody waiting.
export interface Snapshot {
  capacity: number;
  maxWaiting: number;
  buckets: Record<string, BucketState>;
}

function checkCount(name: string, n: number): void {
  if (!Number.isInteger(n) || n < 0) throw new RangeError(`${name} must be a non-negative integer`);
}

export class RateLimiter {
  readonly capacity: number;
  readonly maxWaiting: number;
  private buckets = new Map<string, BucketState>();

  constructor(capacity: number, maxWaiting: number) {
    checkCount("capacity", capacity);
    checkCount("maxWaiting", maxWaiting);
    this.capacity = capacity;
    this.maxWaiting = maxWaiting;
  }

  static from(s: Snapshot): RateLimiter {
    const r = new RateLimiter(s.capacity, s.maxWaiting);
    for (const [api, b] of Object.entries(s.buckets)) {
      r.buckets.set(api, { tokens: b.tokens, waiting: b.waiting.map((w) => ({ ...w })) });
    }
    return r;
  }

  snapshot(): Snapshot {
    const buckets: Record<string, BucketState> = {};
    for (const [api, b] of this.buckets) {
      buckets[api] = { tokens: b.tokens, waiting: b.waiting.map((w) => ({ ...w })) };
    }
    return { capacity: this.capacity, maxWaiting: this.maxWaiting, buckets };
  }

  tokens(api: string): number {
    return this.buckets.get(api)?.tokens ?? this.capacity;
  }

  waiting(api: string): Waiter[] {
    return (this.buckets.get(api)?.waiting ?? []).map((w) => ({ ...w }));
  }

  private bucket(api: string): BucketState {
    let b = this.buckets.get(api);
    if (!b) {
      b = { tokens: this.capacity, waiting: [] };
      this.buckets.set(api, b);
    }
    return b;
  }

  // A call is made: it goes out now if a token is free and nobody waits,
  // else it queues if fewer than maxWaiting wait, else it is refused.
  request(api: string, id: number, now: number): Outcome {
    const b = this.bucket(api);
    if (b.tokens > 0 && b.waiting.length === 0) {
      b.tokens -= 1;
      return { kind: "sent" };
    }
    if (b.waiting.length < this.maxWaiting) {
      b.waiting.push({ id, madeAt: now });
      return { kind: "queued" };
    }
    return { kind: "refused", tokens: b.tokens, queued: b.waiting.length };
  }

  // One refill period: every bucket refills one token, spent at once on its
  // oldest waiting call. Returns the calls that went out.
  refill(now: number): Released[] {
    const out: Released[] = [];
    for (const [api, b] of this.buckets) {
      const head = b.waiting.shift();
      if (head) {
        out.push({ api, id: head.id, madeAt: head.madeAt, at: now });
      } else if (b.tokens < this.capacity) {
        b.tokens += 1;
      }
      if (b.tokens === this.capacity && b.waiting.length === 0) this.buckets.delete(api);
    }
    return out;
  }
}
