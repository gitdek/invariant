import { test } from "node:test";
import assert from "node:assert/strict";
import { Transaction } from "./transaction.ts";

test("everyone commits once everyone has voted", () => {
  const t = new Transaction(["a", "b"]);
  for (const p of t.participants) {
    assert.ok(t.prepare(p));
    assert.ok(t.recordVote(p));
  }
  assert.ok(t.commit());
  for (const p of t.participants) {
    assert.ok(t.learn(p));
    assert.equal(t.stateOf(p), "committed");
  }
});

test("the coordinator won't commit before every vote is in", () => {
  const t = new Transaction(["a", "b"]);
  t.prepare("a");
  t.recordVote("a");
  assert.equal(t.commit(), false);
  assert.equal(t.decided, false);
});

test("a participant can't receive a decision that wasn't sent", () => {
  const t = new Transaction(["a", "b"]);
  assert.equal(t.learnCommit("a"), false);
  assert.equal(t.learnAbort("a"), false);
  assert.ok(t.abort());
  assert.equal(t.learnCommit("a"), false);
  assert.ok(t.learnAbort("a"));
  assert.equal(t.stateOf("a"), "aborted");
});

test("a transaction made again from its snapshot is the same transaction", () => {
  const t = new Transaction(["a", "b"]);
  t.prepare("a");
  t.recordVote("a");
  t.giveUp("b");
  const u = Transaction.restore(t.snapshot());
  assert.deepEqual(u.snapshot(), t.snapshot());
  u.abort();
  assert.equal(t.decided, false);
  assert.equal(u.decided, true);
});

test("a participant that gave up can't prepare, and learns the abort", () => {
  const t = new Transaction(["a", "b"]);
  assert.ok(t.giveUp("a"));
  assert.equal(t.prepare("a"), false);
  assert.ok(t.abort());
  assert.ok(t.learn("b"));
  assert.equal(t.stateOf("b"), "aborted");
});
