/**
 * Two-phase commit among a fixed set of participants, as a small in-memory
 * library. It's ordinary code, with nothing in it written for a verifier.
 * Invariant checks it against the ratified TLA+ model by conformance testing.
 */

export type ParticipantState = "working" | "prepared" | "committed" | "aborted";

export type Message = { kind: "prepared"; from: string } | { kind: "commit" } | { kind: "abort" };

export class Transaction {
  readonly participants: readonly string[];
  #states = new Map<string, ParticipantState>();
  #votes = new Set<string>();
  #messages: Message[] = [];
  #decided = false;

  constructor(participants: readonly string[]) {
    if (participants.length === 0) {
      throw new Error("a transaction needs participants");
    }
    this.participants = [...participants];
    for (const p of participants) {
      this.#states.set(p, "working");
    }
  }

  stateOf(p: string): ParticipantState {
    const state = this.#states.get(p);
    if (state === undefined) {
      throw new Error(`unknown participant ${p}`);
    }
    return state;
  }

  /** The votes the coordinator has recorded. */
  get votes(): readonly string[] {
    return [...this.#votes];
  }

  /** Every message sent so far. Messages are never lost. */
  get messages(): readonly Message[] {
    return [...this.#messages];
  }

  /** Whether the coordinator has decided. */
  get decided(): boolean {
    return this.#decided;
  }

  /** A working participant prepares and votes to commit. */
  prepare(p: string): boolean {
    if (this.stateOf(p) !== "working") {
      return false;
    }
    this.#states.set(p, "prepared");
    this.#send({ kind: "prepared", from: p });
    return true;
  }

  /** A participant that hasn't prepared gives up on its own. */
  giveUp(p: string): boolean {
    if (this.stateOf(p) !== "working") {
      return false;
    }
    this.#states.set(p, "aborted");
    return true;
  }

  /** The coordinator records a vote it has received. */
  recordVote(p: string): boolean {
    if (this.#decided || !this.#messages.some((m) => m.kind === "prepared" && m.from === p)) {
      return false;
    }
    this.#votes.add(p);
    return true;
  }

  /** The coordinator commits, once every participant has voted. */
  commit(): boolean {
    if (this.#decided || this.#votes.size < this.participants.length) {
      return false;
    }
    this.#decided = true;
    this.#send({ kind: "commit" });
    return true;
  }

  /** The coordinator aborts. It can do that any time before it has decided. */
  abort(): boolean {
    if (this.#decided) {
      return false;
    }
    this.#decided = true;
    this.#send({ kind: "abort" });
    return true;
  }

  /** A participant learns the coordinator's decision and follows it. */
  learn(p: string): boolean {
    this.stateOf(p);
    if (this.#messages.some((m) => m.kind === "commit")) {
      this.#states.set(p, "committed");
      return true;
    }
    if (this.#messages.some((m) => m.kind === "abort")) {
      this.#states.set(p, "aborted");
      return true;
    }
    return false;
  }

  #send(m: Message): void {
    const duplicate = this.#messages.some((x) =>
      x.kind === "prepared" && m.kind === "prepared" ? x.from === m.from : x.kind === m.kind,
    );
    if (!duplicate) {
      this.#messages.push(m);
    }
  }
}
