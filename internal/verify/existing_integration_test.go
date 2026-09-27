//go:build integration

package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// doorPackage is a package with existing code, a door, and a project that
// checks it as it is (D-0054). edits change files by their path.
func doorPackage(t *testing.T, edits map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"package.json":      `{"name": "app", "version": "1.0.0", "private": true}`,
		"package-lock.json": `{"name": "app", "version": "1.0.0", "lockfileVersion": 3, "requires": true, "packages": {"": {"name": "app", "version": "1.0.0"}}}`,
		"src/door.ts": `export class Door {
  state: "open" | "closed" | "locked" = "closed";
  open() { if (this.state === "closed") this.state = "open"; }
  close() { if (this.state === "open") this.state = "closed"; }
  lock() { if (this.state === "closed") this.state = "locked"; }
  unlock() { if (this.state === "locked") this.state = "closed"; }
}
`,
		"invariant/door/.invariant/invariant.json": `{"name": "door", "module": ".invariant/specs/Door.tla", "code": ".", "language": "typescript", "conformance": "conformance.ts", "existing": ["src"]}`,
		"invariant/door/.invariant/ratified.lock": `{"decision": "test", "bounds": {"Doors": "{d1}"}, "statements": [
  {"name": "Spec", "kind": "spec", "says": "The door starts closed, and every step is a Next step.", "sha256": ""},
  {"name": "TypeOK", "kind": "invariant", "says": "The door is open, closed or locked.", "sha256": ""},
  {"name": "Locked", "kind": "witness", "says": "The door can be locked.", "sha256": ""}
]}`,
		"invariant/door/.invariant/specs/Door.tla": `---- MODULE Door ----
CONSTANT Doors
VARIABLE door

vars == <<door>>

TypeOK == door \in {"open", "closed", "locked"}

Locked == door = "locked"

Init == door = "closed"

Open == door = "closed" /\ door' = "open"

Close == door = "open" /\ door' = "closed"

Lock == door = "closed" /\ door' = "locked"

Unlock == door = "locked" /\ door' = "closed"

Next == Open \/ Close \/ Lock \/ Unlock

Spec == Init /\ [][Next]_vars
====
`,
		"invariant/door/conformance.ts": `import { writeFileSync } from "node:fs";
import { Door } from "../../src/door.ts";

const runs = Number(process.env.INVARIANT_RUNS), steps = Number(process.env.INVARIANT_STEPS);
let seed = Number(process.env.INVARIANT_SEED);
const rand = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
const traces: unknown[][] = [];
for (let r = 0; r < runs; r++) {
  const d = new Door();
  const run: unknown[] = [{ door: d.state }];
  for (let s = 0; s < steps; s++) {
    [() => d.open(), () => d.close(), () => d.lock(), () => d.unlock()][Math.floor(rand() * 4)]();
    run.push({ door: d.state });
  }
  traces.push(run);
}
writeFileSync(process.env.INVARIANT_TRACES!, JSON.stringify({ traces }));
`,
	}
	for name, text := range edits {
		files[name] = text
	}
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(filepath.Join(root, "invariant", "door"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return p.Dir
}

// The real existing code runs against the model, and the receipt names it.
func TestExistingCodePasses(t *testing.T) {
	r := run(t, doorPackage(t, nil))
	if !r.Passed || r.Conformance == nil || !r.Conformance.Passed {
		t.Fatalf("the honest door should pass: %s", strings.Join(Failed(r), "; "))
	}
	if len(r.Existing) != 1 || r.Existing[0].Path != "src" || r.Existing[0].Files != 1 || r.Toolchain.DepsRecipe == "" {
		t.Errorf("the receipt doesn't name the code it ran: %+v %+v", r.Existing, r.Toolchain)
	}
}

// A bug in the existing code is a step the model doesn't allow.
func TestABugInExistingCodeFails(t *testing.T) {
	dir := doorPackage(t, map[string]string{
		"src/door.ts": `export class Door {
  state: "open" | "closed" | "locked" = "closed";
  open() { if (this.state !== "open") this.state = "open"; }
  close() { if (this.state === "open") this.state = "closed"; }
  lock() { if (this.state === "closed") this.state = "locked"; }
  unlock() { if (this.state === "locked") this.state = "closed"; }
}
`,
	})
	r := run(t, dir)
	if r.Passed || r.Conformance == nil || r.Conformance.BadStep == nil {
		t.Fatalf("a door that opens while locked should fail conformance: %+v", r.Conformance)
	}
	if !strings.Contains(r.Conformance.BadStep.From, "locked") || !strings.Contains(r.Conformance.BadStep.To, "open") {
		t.Errorf("the bad step is %+v; it should open a locked door", r.Conformance.BadStep)
	}
}

// A driver that makes its runs up, without importing the code, is refused.
func TestADriverThatSkipsTheCodeFails(t *testing.T) {
	dir := doorPackage(t, map[string]string{
		"invariant/door/conformance.ts": `import { writeFileSync } from "node:fs";
writeFileSync(process.env.INVARIANT_TRACES!, JSON.stringify({ traces: [[{ door: "closed" }, { door: "open" }]] }));
`,
	})
	r := run(t, dir)
	if r.Passed || r.Build.Passed {
		t.Fatal("a driver that doesn't import the code it checks should fail")
	}
	if len(r.Build.Steps) != 1 || r.Build.Steps[0].Name != "the driver imports src" {
		t.Errorf("build steps %+v", r.Build.Steps)
	}
}
