import { test } from "node:test";
import assert from "node:assert";
import { LogBuffer } from "./machine.ts";
import type { Line, State } from "./machine.ts";

// A small environment for the tests: producers each writing a few lines.
const PRODUCERS = ["p1", "p2"];
const LINES = 2;
const CAPACITY = 2;

type World = { sys: State; sent: Line[]; log: Line[]; written: Record<string, number> };

function successors(w: World): World[] {
  const out: World[] = [];
  for (const p of PRODUCERS) {
    if (w.written[p] >= LINES) continue;
    const b = LogBuffer.from(w.sys);
    const line = { producer: p, n: w.written[p] + 1 };
    if (b.write(line)) {
      out.push({ sys: b.state(), sent: w.sent, log: [...w.log, line], written: { ...w.written, [p]: w.written[p] + 1 } });
    }
  }
  const s = LogBuffer.from(w.sys);
  const line = s.ship();
  if (line) out.push({ ...w, sys: s.state(), sent: [...w.sent, line] });
  const f = LogBuffer.from(w.sys);
  if (f.shipFail()) out.push({ ...w, sys: f.state() });
  if (PRODUCERS.every((p) => w.written[p] === LINES) && w.sys.buf.length === 0) out.push(w);
  return out;
}

function reachable(): World[] {
  const start: World = { sys: new LogBuffer(CAPACITY).state(), sent: [], log: [], written: { p1: 0, p2: 0 } };
  const seen = new Map<string, World>([[JSON.stringify(start), start]]);
  const queue = [start];
  while (queue.length > 0) {
    const w = queue.shift() as World;
    for (const t of successors(w)) {
      const k = JSON.stringify(t);
      if (!seen.has(k)) {
        seen.set(k, t);
        queue.push(t);
      }
    }
  }
  return [...seen.values()];
}

const same = (a: Line, b: Line) => a.producer === b.producer && a.n === b.n;

test("a producer waits while the buffer is full", () => {
  const b = new LogBuffer(CAPACITY);
  assert.ok(b.write({ producer: "p1", n: 1 }));
  assert.ok(b.write({ producer: "p2", n: 1 }));
  assert.strictEqual(b.state().buf.length, CAPACITY);
  assert.strictEqual(b.write({ producer: "p1", n: 2 }), false);
  assert.strictEqual(b.state().buf.length, CAPACITY);
});

test("a failed send keeps the line at the front and it is shipped once", () => {
  const b = new LogBuffer(CAPACITY);
  b.write({ producer: "p1", n: 1 });
  b.write({ producer: "p1", n: 2 });
  const before = b.state().buf;
  assert.ok(b.shipFail());
  assert.deepStrictEqual(b.state().buf, before);
  assert.strictEqual(b.state().retrying, true);
  assert.strictEqual(b.shipFail(), false);
  assert.deepStrictEqual(b.ship(), { producer: "p1", n: 1 });
  assert.strictEqual(b.state().retrying, false);
});

test("actions never change their argument", () => {
  const b = new LogBuffer(CAPACITY);
  b.write({ producer: "p1", n: 1 });
  const s = b.state();
  const before = JSON.stringify(s);
  const c = LogBuffer.from(s);
  c.write({ producer: "p2", n: 1 }); c.ship(); c.shipFail();
  assert.strictEqual(JSON.stringify(s), before);
});

test("invariants hold in every reachable state", () => {
  for (const w of reachable()) {
    const buf = w.sys.buf;
    assert.ok(buf.length <= CAPACITY);
    assert.ok(w.sent.length <= w.log.length);
    w.sent.forEach((l, i) => assert.ok(same(l, w.log[i])));
    assert.strictEqual(buf.length, w.log.length - w.sent.length);
    buf.forEach((l, i) => assert.ok(same(l, w.log[w.sent.length + i])));
    const ids = new Set(w.sent.map((l) => `${l.producer}.${l.n}`));
    assert.strictEqual(ids.size, w.sent.length);
    assert.ok(successors(w).length > 0, "no deadlock");
  }
});

test("everything can be shipped", () => {
  const all = reachable().some(
    (w) => PRODUCERS.every((p) => w.written[p] === LINES) && w.sys.buf.length === 0 && w.sent.length === 4,
  );
  assert.ok(all);
});
