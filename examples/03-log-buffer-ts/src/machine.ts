// Bounded log shipping buffer, the system that LogBuffer.tla models.
//
// Producers write lines into the buffer and a shipper sends them on, oldest
// first. The buffer's capacity is given when it's made; a write into a full
// buffer is refused, and the producer waits.

// A line is identified by the producer that wrote it and its per-producer number.
export type Line = { producer: string; n: number };

// The buffer's own state: its capacity, the lines it holds, oldest first, and
// whether the shipper is retrying the oldest line.
export type State = {
  capacity: number;
  buf: Line[];
  retrying: boolean;
};

export class LogBuffer {
  readonly capacity: number;
  private buf: Line[];
  private retrying: boolean;

  constructor(capacity: number, buf: Line[] = [], retrying = false) {
    if (!Number.isInteger(capacity) || capacity < 0) throw new RangeError("capacity must be a natural number");
    this.capacity = capacity;
    this.buf = buf.map((l) => ({ ...l }));
    this.retrying = retrying;
  }

  static from(s: State): LogBuffer {
    return new LogBuffer(s.capacity, s.buf, s.retrying);
  }

  state(): State {
    return { capacity: this.capacity, buf: this.buf.map((l) => ({ ...l })), retrying: this.retrying };
  }

  // A producer writes a line; it's refused (the producer waits) while the buffer is full.
  write(line: Line): boolean {
    if (this.buf.length >= this.capacity) return false;
    this.buf.push({ ...line });
    return true;
  }

  // The shipper sends the oldest line successfully and removes it. Returns
  // the line shipped, or null when there's nothing to ship.
  ship(): Line | null {
    const line = this.buf.shift();
    if (line === undefined) return null;
    this.retrying = false;
    return line;
  }

  // Sending the oldest line fails; it stays at the front to be retried.
  // Refused when there's nothing to send or a retry is already pending.
  shipFail(): boolean {
    if (this.buf.length === 0 || this.retrying) return false;
    this.retrying = true;
    return true;
  }

  isEmpty(): boolean {
    return this.buf.length === 0;
  }
}
