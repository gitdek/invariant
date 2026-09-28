// The environment for the log buffer: producers writing lines and the
// shipper's network. Explores every step LogBuffer.tla's Next names with
// Invariant's harness.

import { explore } from "./invariant-explore.ts";
import { LogBuffer } from "./src/machine.ts";
import type { Line, State } from "./src/machine.ts";

// The bounds, named after the model's constants.
const Producers = 2;
const MaxLines = 2;
// A size the code takes, not a bound of the environment (see "parameters").
const Capacity = 2;

const producers = Array.from({ length: Producers }, (_, i) => `p${i + 1}`);

// The system's part, read out of the code, and the environment's own: what
// was shipped, what was written, and how many lines each producer wrote.
type Node = {
  sys: State;
  sent: Line[];
  log: Line[];
  written: Record<string, number>;
};

const lineAbs = (l: Line) => ({ $seq: [{ $mv: l.producer }, l.n] });

function abstract(n: Node): unknown {
  return {
    buf: { $seq: n.sys.buf.map(lineAbs) },
    sent: { $seq: n.sent.map(lineAbs) },
    log: { $seq: n.log.map(lineAbs) },
    written: { $fn: producers.map((p) => [{ $mv: p }, n.written[p]]) },
    retrying: n.sys.retrying,
  };
}

const initial: Node = {
  sys: new LogBuffer(Capacity).state(),
  sent: [],
  log: [],
  written: Object.fromEntries(producers.map((p) => [p, 0])),
};

explore<Node>({
  initial: [initial],
  abstract,
  steps: [
    {
      name: "Write",
      args: producers.map((p) => [{ $mv: p }]),
      take: (n, p: { $mv: string }) => {
        const who = p.$mv;
        const b = LogBuffer.from(n.sys);
        const line = { producer: who, n: n.written[who] + 1 };
        // The code refuses a full buffer; that's tried even when the producer
        // has also written all its lines.
        if (!b.write(line)) return n;
        if (n.written[who] >= MaxLines) return null;
        return {
          sys: b.state(),
          sent: n.sent,
          log: [...n.log, line],
          written: { ...n.written, [who]: n.written[who] + 1 },
        };
      },
    },
    {
      name: "Ship",
      take: (n) => {
        const b = LogBuffer.from(n.sys);
        const line = b.ship();
        if (line === null) return n;
        return { ...n, sys: b.state(), sent: [...n.sent, line] };
      },
    },
    {
      name: "ShipFail",
      take: (n) => {
        const b = LogBuffer.from(n.sys);
        if (!b.shipFail()) return n;
        return { ...n, sys: b.state() };
      },
    },
    {
      // Every producer has written all its lines and everything has been
      // shipped; nothing changes, whether or not that's so yet.
      name: "Done",
      take: (n) => n,
    },
  ],
});
