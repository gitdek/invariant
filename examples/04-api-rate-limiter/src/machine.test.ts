import { test } from "node:test";
import assert from "node:assert";
import {
  APIS, CAPACITY, MAX_CALLS, MAX_TIME, done, init, key, makeCall, successors, tick,
} from "./machine.ts";
import type { State } from "./machine.ts";

function explore(): State[] {
  const seen = new Map<string, State>([[key(init()), init()]]);
  const queue = [init()];
  while (queue.length > 0) {
    const s = queue.shift()!;
    for (const t of successors(s)) {
      if (!seen.has(key(t))) {
        seen.set(key(t), t);
        queue.push(t);
      }
    }
  }
  return [...seen.values()];
}

test("bucket starts full and a burst goes out immediately", () => {
  let s = init();
  for (let i = 0; i < CAPACITY; i++) s = makeCall(s, "a1")!;
  assert.strictEqual(s.tokens.a1, 0);
  assert.strictEqual(s.sent.a1.length, CAPACITY);
  assert.ok(s.sent.a1.every((c) => c.at === 0));
});

test("empty bucket makes calls wait, then they go out in order on ticks", () => {
  let s = init();
  for (let i = 0; i < CAPACITY + 2; i++) s = makeCall(s, "a1")!;
  assert.deepStrictEqual(s.waiting.a1.map((w) => w.id), [3, 4]);
  s = tick(s)!;
  assert.deepStrictEqual(s.sent.a1.at(-1), { id: 3, madeAt: 0, at: 1 });
  assert.strictEqual(s.tokens.a1, 0);
  assert.strictEqual(s.tokens.a2, CAPACITY);
});

test("bucket refills to full", () => {
  let s = makeCall(init(), "a2")!;
  s = tick(s)!;
  assert.strictEqual(s.tokens.a2, CAPACITY);
});

test("actions do not mutate their argument and respect guards", () => {
  const s = init();
  const before = key(s);
  makeCall(s, "a1");
  tick(s);
  assert.strictEqual(key(s), before);
  assert.strictEqual(done(s), null);
});

test("every reachable state satisfies the invariants", () => {
  const all = explore();
  assert.ok(all.length > 1);
  for (const s of all) {
    assert.ok(s.clock >= 0 && s.clock <= MAX_TIME);
    for (const a of APIS) {
      assert.ok(s.tokens[a] >= 0 && s.tokens[a] <= CAPACITY);
      assert.ok(s.made[a] <= MAX_CALLS);
      assert.strictEqual(s.made[a], s.sent[a].length + s.waiting[a].length);
      if (s.waiting[a].length > 0) assert.strictEqual(s.tokens[a], 0);
      s.sent[a].forEach((c, i) => assert.strictEqual(c.id, i + 1));
      s.waiting[a].forEach((w, i) => assert.strictEqual(w.id, s.sent[a].length + i + 1));
      const sent = s.sent[a];
      for (let i = 0; i < sent.length; i++) {
        for (let j = i; j < sent.length; j++) {
          assert.ok(j - i + 1 <= CAPACITY + (sent[j].at - sent[i].at));
        }
      }
    }
    assert.ok(successors(s).length > 0, "no deadlock");
  }
});
