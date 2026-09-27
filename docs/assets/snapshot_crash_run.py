"""Record the crash test, for the README's graphic of it.

    python3 docs/assets/snapshot_crash_run.py

runs TestCrashAnywhere with go test -json and writes docs/assets/crash-run.json:
each effect the watcher takes on one issue, in order, what the test stopped
just before, and whether the issue still merged with every effect done once,
restarted on the same machine and on another. generate.py draws the graphic
from that file, so it shows what the test did and nothing else (D-0015).
"""
import json
import os
import re
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
CASE = re.compile(r"^TestCrashAnywhere/(the_same_machine|another_machine)/effect_(\d+)$")
STOPPED = re.compile(r"stopped (.*)")

# Where each step starts, in the order the watcher takes its effects: the
# ratification's push, the build's record and the merge's record.
STARTS = [("Ratify", "push invariant/"), ("Build", "record build-"), ("Merge", "record merge-")]


def main():
    out = subprocess.run(["go", "test", "-count=1", "-json", "-run", "TestCrashAnywhere", "./internal/factory/"],
                         cwd=ROOT, capture_output=True, text=True).stdout
    what, result = {}, {}
    for line in out.splitlines():
        try:
            e = json.loads(line)
        except ValueError:
            continue
        m = CASE.match(e.get("Test", ""))
        if not m:
            continue
        key = (m.group(1), int(m.group(2)))
        if e.get("Action") == "output" and (s := STOPPED.search(e["Output"])):
            what[key] = s.group(1).strip()
        if e.get("Action") in ("pass", "fail"):
            result[key] = e["Action"]
    effects, step, starts = [], "Draft", list(STARTS)
    for n in range(1, max(k[1] for k in result) + 1):
        w = what.get(("the_same_machine", n)) or what.get(("another_machine", n)) or "?"
        if starts and w.startswith(starts[0][1]):
            step = starts.pop(0)[0]
        effects.append({"n": n, "stopped_before": w, "step": step,
                        "same_machine": result.get(("the_same_machine", n)), "another_machine": result.get(("another_machine", n))})
    head = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=ROOT, capture_output=True, text=True).stdout.strip()
    run = {"test": "TestCrashAnywhere", "commit": head, "effects": effects,
           "before": {"commit": "557e74e", "stops": 30, "broke": 14,
                      "note": "the same test, against the watcher before #26, stopped before each of its 15 effects then"}}
    path = os.path.join(HERE, "crash-run.json")
    with open(path, "w") as fh:
        json.dump(run, fh, indent=1)
        fh.write("\n")
    print("wrote", os.path.relpath(path, ROOT), f"({len(effects)} effects)")


if __name__ == "__main__":
    main()
