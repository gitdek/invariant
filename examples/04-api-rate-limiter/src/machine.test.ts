import { test } from "node:test";
import assert from "node:assert";
import { RateLimiter } from "./machine.ts";

test("bucket starts full and a burst goes out immediately", () => {
  const r = new RateLimiter(2, 2);
  assert.deepStrictEqual(r.request("a1", 1, 0), { kind: "sent" });
  assert.deepStrictEqual(r.request("a1", 2, 0), { kind: "sent" });
  assert.strictEqual(r.tokens("a1"), 0);
  assert.strictEqual(r.tokens("a2"), 2);
});

test("empty bucket makes calls wait, then they go out in order on refills", () => {
  const r = new RateLimiter(2, 2);
  for (let id = 1; id <= 2; id++) r.request("a1", id, 0);
  assert.deepStrictEqual(r.request("a1", 3, 0), { kind: "queued" });
  assert.deepStrictEqual(r.request("a1", 4, 0), { kind: "queued" });
  assert.deepStrictEqual(r.waiting("a1").map((w) => w.id), [3, 4]);
  assert.deepStrictEqual(r.refill(1), [{ api: "a1", id: 3, madeAt: 0, at: 1 }]);
  assert.strictEqual(r.tokens("a1"), 0);
  assert.deepStrictEqual(r.refill(2), [{ api: "a1", id: 4, madeAt: 0, at: 2 }]);
  assert.deepStrictEqual(r.refill(3), []);
  assert.strictEqual(r.tokens("a1"), 1);
});

test("a call is refused once maxWaiting calls already wait", () => {
  const r = new RateLimiter(2, 2);
  for (let id = 1; id <= 4; id++) assert.notStrictEqual(r.request("a1", id, 0).kind, "refused");
  assert.deepStrictEqual(r.request("a1", 5, 0), { kind: "refused", tokens: 0, queued: 2 });
  assert.strictEqual(r.waiting("a1").length, 2);
  assert.deepStrictEqual(r.refill(1), [{ api: "a1", id: 3, madeAt: 0, at: 1 }]);
});

test("bucket refills to full", () => {
  const r = new RateLimiter(2, 2);
  r.request("a2", 1, 0);
  assert.strictEqual(r.tokens("a2"), 1);
  r.refill(1);
  assert.strictEqual(r.tokens("a2"), 2);
  r.refill(2);
  assert.strictEqual(r.tokens("a2"), 2);
});

test("capacity and queue length are parameters", () => {
  const r = new RateLimiter(0, 0);
  assert.deepStrictEqual(r.request("x", 1, 7), { kind: "refused", tokens: 0, queued: 0 });
  const big = new RateLimiter(100, 3);
  for (let id = 1; id <= 100; id++) assert.deepStrictEqual(big.request("x", id, 0), { kind: "sent" });
  assert.deepStrictEqual(big.request("x", 101, 0), { kind: "queued" });
  assert.throws(() => new RateLimiter(-1, 0), RangeError);
});

test("snapshot round-trips and is independent of the limiter", () => {
  const r = new RateLimiter(1, 2);
  r.request("a1", 1, 0);
  r.request("a1", 2, 0);
  const s = r.snapshot();
  const copy = RateLimiter.from(s);
  assert.deepStrictEqual(copy.snapshot(), s);
  copy.refill(1);
  assert.deepStrictEqual(RateLimiter.from(s).snapshot(), s);
  assert.deepStrictEqual(r.waiting("a1"), [{ id: 2, madeAt: 0 }]);
});
