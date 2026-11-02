"""Container entry point, embedded by sandbox.go rather than read from /src."""

import concurrent.futures
import json
import os
import re
import subprocess
import sys
import tempfile
import threading
import time


output_lock = threading.Lock()


def write(text):
    if not text:
        return
    with output_lock:
        sys.stdout.write(text)
        if not text.endswith("\n"):
            sys.stdout.write("\n")
        sys.stdout.flush()


def record(kind, **fields):
    write("@@sandbox " + json.dumps(dict(kind=kind, **fields), separators=(",", ":")))


def failure(message):
    record("failure", message=message)


def test_record(package, name, phase, status, shard):
    record("test", package=package, name=name, phase=phase, status=status, shard=shard)


def command(args, cwd=None):
    """Stream preparation output before its marker, including on interruption."""
    with subprocess.Popen(args, cwd=cwd, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True, errors="replace") as process:
        for line in process.stdout:
            write(line)
        return process.wait()


def capture(args, cwd=None, timeout=None):
    return subprocess.run(args, cwd=cwd, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True, errors="replace", timeout=timeout)


def prepare(directories, accept, temporary):
    """Compile once, serially. No shard starts until every build is finished."""
    packages = {}
    good = True
    for index, directory in enumerate(directories):
        target = "./" + directory
        try:
            listed = capture(["go", "list", "-e", "-json", target])
        except OSError as error:
            failure("Build metadata unavailable for package " + target + ": " + str(error))
            good = False
            continue
        write(listed.stderr)
        try:
            metadata = json.loads(listed.stdout)
            package = metadata["ImportPath"]
        except (ValueError, KeyError, TypeError):
            write(listed.stdout)
            failure("Build metadata unavailable for package " + target)
            good = False
            continue
        record("package", dir=directory, package=package)
        for name in accept.get(directory, []):
            test_record(package, name, "accept", "not_run", 0)
        entry = dict(package=package, cwd=os.path.abspath(directory), binary=None, names=[])
        packages[directory] = entry
        if listed.returncode or metadata.get("Error"):
            detail = metadata.get("Error", {}).get("Err", listed.stderr.strip())
            failure("Build failed for package " + package + ": " + detail)
            good = False
            continue
        binary = os.path.join(temporary, "package-" + str(index) + ".test")
        try:
            code = command(["go", "test", "-c", "-vet=off", "-o", binary, target])
        except OSError as error:
            failure("Build failed for package " + package + ": " + str(error))
            good = False
            continue
        if code:
            failure("Build failed for package " + package + " (exit " + str(code) + ")")
            good = False
        elif os.path.isfile(binary):
            entry["binary"] = binary
        else:
            # go test -c succeeds without producing a binary for a package
            # with no test files. It has no top-level tests to assign.
            entry["empty"] = True
    return packages, good


def discover(packages, timeout, shards):
    good = True
    index = 0
    for entry in sorted(packages.values(), key=lambda entry: entry["package"]):
        if not entry["binary"]:
            continue
        try:
            listed = capture([entry["binary"], "-test.list=^(Test|Fuzz|Example)"],
                             cwd=entry["cwd"], timeout=timeout)
        except subprocess.TimeoutExpired as error:
            # Discovery runs the same TestMain as execution, so it too must
            # yield to a bound if TestMain never reaches the test harness.
            write((error.stdout or b"").decode(errors="replace"))
            write((error.stderr or b"").decode(errors="replace"))
            failure("Discovery timeout for package " + entry["package"])
            good = False
            continue
        except OSError as error:
            failure("Discovery failed for package " + entry["package"] + ": " + str(error))
            good = False
            continue
        write(listed.stderr)
        if listed.returncode:
            write(listed.stdout)
            failure("Discovery failed for package " + entry["package"] +
                    " (exit " + str(listed.returncode) + ")")
            good = False
            continue
        entry["names"] = sorted(set(line for line in listed.stdout.splitlines()
                                    if re.fullmatch(r"(Test|Fuzz|Example)[^\s/]*", line)))
        # Retain known tests before another package's TestMain can block
        # discovery. This is the same sorted order used for assignments.
        for name in entry["names"]:
            test_record(entry["package"], name, "test", "not_run", index % shards + 1)
            index += 1
    return good


def stop(process):
    if process.poll() is None:
        process.kill()
    process.wait()


def run_tests(entry, names, phase, shard, deadline=None):
    """Run a package group without letting TestMain escape a shard deadline.

    The test binary is a direct child, separate from the JSON converter. Go's
    -test.timeout does not cover all of TestMain. All children retain Docker's
    process group, so removing the container also stops their descendants.
    """
    package = entry["package"]
    identity = (("shard " + str(shard)) if phase == "test" else "acceptance")
    identity += " package " + package + " (" + ", ".join(names) + ")"
    remaining = None if deadline is None else deadline - time.monotonic()
    if remaining is not None and remaining <= 0:
        failure("Timeout before " + identity)
        return False, True
    pattern = "^(" + "|".join(re.escape(name) for name in names) + ")$"
    timeout = "0" if remaining is None else str(max(0.001, remaining)) + "s"
    process = subprocess.Popen([entry["binary"], "-test.v=test2json", "-test.run=" + pattern,
                                "-test.timeout=" + timeout], cwd=entry["cwd"],
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    try:
        converter = subprocess.Popen(["go", "tool", "test2json", "-p", package],
                                     cwd=entry["cwd"], stdin=process.stdout,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                     text=True, errors="replace")
    except BaseException:
        process.stdout.close()
        stop(process)
        raise
    process.stdout.close()
    statuses = {name: "not_run" for name in names}
    errors = []

    def consume():
        try:
            for line in converter.stdout:
                # Preserve the original JSON, so test output containing our
                # marker text cannot impersonate a runner protocol record.
                write(line)
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                name = event.get("Test")
                status = {"run": "running", "pass": "passed", "fail": "failed",
                          "skip": "skipped"}.get(event.get("Action"))
                if name and status:
                    statuses[name] = status
                    test_record(package, name, phase, status, shard)
                    if status == "failed":
                        failure("Failed " + identity.split(" (")[0] + " test " + name)
        except BaseException as error:
            errors.append(str(error))

    reader = threading.Thread(target=consume, daemon=True)
    reader.start()
    timed_out = False
    try:
        process.wait(timeout=None if deadline is None else max(0, deadline - time.monotonic()))
        converter.wait(timeout=None if deadline is None else max(0, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        timed_out = True
        stop(process)
        # go tool forwards SIGTERM to its converter child. Killing only the
        # wrapper can leave that child waiting for an inherited output pipe.
        converter.terminate()
        try:
            converter.wait(timeout=1)
        except subprocess.TimeoutExpired:
            stop(converter)
    finally:
        reader.join(timeout=1 if timed_out else None)
    if timed_out:
        failure("Timeout in " + identity)
    elif process.returncode or converter.returncode or errors:
        detail = "; ".join(errors) or "test exit " + str(process.returncode) + ", converter exit " + str(converter.returncode)
        failure("Failed " + identity + ": " + detail)
    allowed = {"passed", "skipped"} if phase == "test" else {"passed"}
    complete = all(statuses[name] in allowed for name in names)
    if not timed_out and process.returncode == 0 and converter.returncode == 0 and not complete:
        missing = [name for name in names if statuses[name] not in allowed]
        failure("Incomplete " + identity + ": " + ", ".join(missing))
    return (not timed_out and process.returncode == 0 and converter.returncode == 0
            and not errors and complete), timed_out


def run_shard(shard, groups, timeout):
    deadline = time.monotonic() + timeout
    good = True
    for entry, names in groups:
        try:
            passed, timed_out = run_tests(entry, names, "test", shard, deadline)
            good = good and passed
            if timed_out:
                break
        except Exception as error:
            failure("Shard " + str(shard) + " package " + entry["package"] + ": " + str(error))
            good = False
    return good


def main():
    record("start")
    config = json.loads(os.environ["SANDBOX_CONFIG"])
    shards = int(config["shards"])
    shard_timeout = float(config["shard_timeout"])
    accept = {os.path.normpath(directory): sorted(set(names))
              for directory, names in (config.get("accept") or {}).items()}
    directories = sorted(set(os.path.normpath(directory) for directory in
                             (config.get("packages") or [])) | set(accept))

    for name in config.get("gofmt_files") or []:
        if not os.path.isfile(name):
            continue
        formatted = capture(["gofmt", "-l", name])
        for changed in formatted.stdout.splitlines():
            write("@@gofmt " + changed)
        write(formatted.stderr)
        if formatted.returncode:
            failure("gofmt failed for " + name)
    vet = command(["go", "vet", "./..."])
    write("@@invariant vet=" + str(vet))

    with tempfile.TemporaryDirectory(prefix="invariant-tests-") as temporary:
        packages, built = prepare(directories, accept, temporary)
        discovered = discover(packages, shard_timeout, shards)
        entries = sorted((entry["package"], name, directory)
                         for directory, entry in packages.items() for name in entry["names"])
        assignments = [dict() for _ in range(shards)]
        for index, (package, name, directory) in enumerate(entries):
            shard = index % shards
            assignments[shard].setdefault(directory, []).append(name)
        with concurrent.futures.ThreadPoolExecutor(max_workers=shards) as workers:
            futures = [workers.submit(run_shard, index + 1,
                                      [(packages[directory], names) for directory, names in groups.items()],
                                      shard_timeout)
                       for index, groups in enumerate(assignments)]
            results = [future.result() for future in futures]
        write("@@invariant test=" + ("0" if built and discovered and all(results) else "1"))

        for directory, names in sorted(accept.items()):
            entry = packages.get(directory)
            passed = False
            if entry and entry["binary"]:
                try:
                    passed, _ = run_tests(entry, names, "accept", 0)
                except Exception as error:
                    failure("Acceptance package " + entry["package"] + ": " + str(error))
            else:
                failure("Acceptance package " + directory + " has no test binary")
            if entry:
                record("accept", package=entry["package"], passed=passed)
            write("@@invariant accept=" + ("0" if passed else "1"))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        failure("Sandbox runner interrupted: " + str(error))
        sys.exit(1)
