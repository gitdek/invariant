import { test } from "node:test";
import assert from "node:assert";
import { Tickets } from "./tickets.ts";

test("a full pool refuses", () => {
  const t = new Tickets(1);
  assert.ok(t.take("a"));
  assert.ok(!t.take("b"));
  assert.ok(t.give("a"));
  assert.ok(t.take("b"));
});
