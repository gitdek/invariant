import { test } from "node:test";
import assert from "node:assert";
import {
  init, write, ship, shipFail, done, successors, key, CAPACITY, PRODUCERS,
} from "./machine.ts";
import type { State } from "./machine.ts";

function reachable(): Map<string, State> {
  const seen = new Map<string, State>();
  const queue = [init()];
  seen.set(key(queue[0]), queue[0]);
  while (queue.length > 0) {
    const s = queue.shift() as State;
    for (const t of successors(s)) {
      if (!seen.has(key(t))) {
        seen.set(key(t), t);
        queue.push(t);
      }
    }
  }
  return seen;
}

const same = (a: { producer: string; n: number }, b: { producer: string; n: number }) =>
  a.producer === b.producer && a.n === b.n;

test("a producer waits while the buffer is full", () => {
  let s = write(init(), "p1") as State;
  s = write(s, "p2") as State;
  assert.strictEqual(s.buf.length, CAPACITY);
  assert.strictEqual(write(s, "p1"), null);
});

test("a failed send keeps the line at the front and it is shipped once", () => {
  let s = write(init(), "p1") as State;
  s = write(s, "p1") as State;
  const failed = shipFail(s) as State;
  assert.deepStrictEqual(failed.buf, s.buf);
  assert.strictEqual(failed.retrying, true);
  assert.strictEqual(shipFail(failed), null);
  const shipped = ship(failed) as State;
  assert.deepStrictEqual(shipped.sent, [{ producer: "p1", n: 1 }]);
  assert.strictEqual(shipped.retrying, false);
});

test("actions never change their argument", () => {
  const s = write(init(), "p1") as State;
  const before = key(s);
  write(s, "p2"); ship(s); shipFail(s); done(s);
  assert.strictEqual(key(s), before);
});

test("invariants hold in every reachable state", () => {
  for (const s of reachable().values()) {
    assert.ok(s.buf.length <= CAPACITY);
    assert.ok(s.sent.length <= s.log.length);
    s.sent.forEach((l, i) => assert.ok(same(l, s.log[i])));
    assert.strictEqual(s.buf.length, s.log.length - s.sent.length);
    s.buf.forEach((l, i) => assert.ok(same(l, s.log[s.sent.length + i])));
    const ids = new Set(s.sent.map((l) => `${l.producer}.${l.n}`));
    assert.strictEqual(ids.size, s.sent.length);
    assert.ok(successors(s).length > 0, "no deadlock");
  }
});

test("everything can be shipped", () => {
  const all = [...reachable().values()].some(
    (s) => PRODUCERS.every((p) => s.written[p] === 2) && s.buf.length === 0 && s.sent.length === 4,
  );
  assert.ok(all);
});
