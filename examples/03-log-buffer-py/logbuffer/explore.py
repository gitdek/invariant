"""The environment around the core: the bounds, the producers, and what was written and shipped."""
from collections import namedtuple

from invariant_explore import Step

from logbuffer.core import LogBuffer

Producers = 2
Capacity = 2
MaxLines = 2

PRODUCERS = ["p%d" % (i + 1) for i in range(Producers)]

# buf and retrying are the core's; sent, log and written are the environment's.
# A line <<p, n>> is the int i * MaxLines + n, where p is PRODUCERS[i].
Node = namedtuple("Node", ["buf", "retrying", "sent", "log", "written"])

INITIAL = [Node((), False, (), (), (0,) * Producers)]


def line_code(i, n):
    return i * MaxLines + n


def core(node):
    b = LogBuffer(Capacity)
    b.lines = list(node.buf)
    b.retrying = node.retrying
    return b


def take_write(node, p):
    i = PRODUCERS.index(p["$mv"])
    b = core(node)
    line = line_code(i, node.written[i] + 1)
    # A full buffer refuses the write, whatever the producer's count.
    if not b.write(line):
        return node
    if node.written[i] >= MaxLines:
        return None
    written = list(node.written)
    written[i] += 1
    return node._replace(buf=tuple(b.lines), log=node.log + (line,), written=tuple(written))


def take_ship(node):
    b = core(node)
    head = b.lines[0] if b.lines else None
    if not b.ship():
        return node
    return node._replace(buf=tuple(b.lines), retrying=b.retrying, sent=node.sent + (head,))


def take_ship_fail(node):
    b = core(node)
    if not b.ship_fail():
        return node
    return node._replace(buf=tuple(b.lines), retrying=b.retrying)


def take_done(node):
    # Done changes nothing, enabled or not.
    return node


STEPS = [
    Step("Write", take_write, [[{"$mv": p}] for p in PRODUCERS]),
    Step("Ship", take_ship),
    Step("ShipFail", take_ship_fail),
    Step("Done", take_done),
]


def line(code):
    i = (code - 1) // MaxLines
    return {"$seq": [{"$mv": PRODUCERS[i]}, code - i * MaxLines]}


def seq(codes):
    return {"$seq": [line(c) for c in codes]}


def abstract(node):
    return {
        "buf": seq(node.buf),
        "sent": seq(node.sent),
        "log": seq(node.log),
        "written": {"$fn": [[{"$mv": p}, node.written[i]] for i, p in enumerate(PRODUCERS)]},
        "retrying": node.retrying,
    }
