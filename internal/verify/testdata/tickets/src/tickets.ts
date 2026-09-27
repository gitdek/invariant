// A pool of tickets. Its capacity is chosen when it's made.
export class Tickets {
  readonly capacity: number;
  private holders = new Set<string>();

  constructor(capacity: number) {
    this.capacity = capacity;
  }

  // take gives client a ticket, unless it holds one or none is left.
  take(client: string): boolean {
    if (this.holders.has(client) || this.holders.size >= this.capacity) return false;
    this.holders.add(client);
    return true;
  }

  // give takes client's ticket back, if it holds one.
  give(client: string): boolean {
    return this.holders.delete(client);
  }

  held(): string[] {
    return [...this.holders].sort();
  }
}
