// invariant-explore.ts is Invariant's exploration harness for a TypeScript
// conformance driver (D-0082, D-0085). The gate writes it next to the driver
// before the driver runs, so the copy that runs is always Invariant's own.
//
// A driver gives it the nodes to start from, every step the model's Next
// names, and how to read a node as a state in the spec's vocabulary. The
// harness explores breadth first, tries every step with every argument in
// every node it reaches, and records each attempt, refusals included. The
// gate then checks that no step went untried except where only the
// environment's bounds rule it out.
import { writeFileSync } from "node:fs";

// A step the model's Next names, such as MakeCall(a).
export interface Step<N> {
  // The step's name, exactly as Next names it.
  name: string;
  // Every argument list to try it with, as Next passes them, in the spec's
  // encoding: [[{ $mv: "a1" }], [{ $mv: "a2" }]]. Leave it out for a step
  // with no arguments.
  args?: unknown[][];
  // take tries the step from node, with one argument list, and returns the
  // node it reaches: a new node when the code takes the step, a node equal to
  // the one it was given when the code refuses, and null only when the
  // environment's bounds rule the step out, such as a sixth call when five is
  // the bound. It must not change the node it's given.
  take: (node: N, ...args: any[]) => N | null;
}

export interface Exploration<N> {
  initial: N[];
  steps: Step<N>[];
  // The node's state in the spec's vocabulary: one field per variable.
  abstract: (node: N) => unknown;
  // What makes two nodes the same. By default, their states.
  key?: (node: N) => string;
}

// explore writes what it finds to $INVARIANT_TRACES. When $INVARIANT_COUNT
// is set, it explores the same way and writes only how many states it
// reached, and how many breadth-first levels its search took, to that file.
export function explore<N>(e: Exploration<N>): void {
  const counting = process.env.INVARIANT_COUNT;
  const out = counting || process.env.INVARIANT_TRACES;
  if (!out) throw new Error("neither INVARIANT_TRACES nor INVARIANT_COUNT is set");
  const key = e.key ?? ((n: N) => JSON.stringify(e.abstract(n)));
  const nodes: N[] = [];
  const levels: number[] = [];
  const states: unknown[] = [];
  const index = new Map<string, number>();
  const attempts: unknown[][] = [];
  let depth = 0;
  const add = (node: N, level: number): number => {
    const k = key(node);
    const seen = index.get(k);
    if (seen !== undefined) return seen;
    const i = nodes.length;
    index.set(k, i);
    nodes.push(node);
    levels.push(level);
    if (!counting) states.push(e.abstract(node));
    depth = Math.max(depth, level);
    return i;
  };
  const init = e.initial.map((node) => add(node, 1));
  for (let i = 0; i < nodes.length; i++) {
    for (const step of e.steps) {
      for (const args of step.args ?? [[]]) {
        const next = step.take(nodes[i], ...args);
        if (next === null) continue;
        const j = add(next, levels[i] + 1);
        if (!counting) attempts.push([i, step.name, args, j]);
      }
    }
  }
  writeFileSync(out, JSON.stringify(counting ? { states: nodes.length, depth } : { states, init, attempts }));
  console.log(counting ? `${nodes.length} states, depth ${depth}` : `${nodes.length} states, ${attempts.length} attempts`);
}
