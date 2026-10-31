package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// An application that runs as `uv run server.py` declares its environment
// in the script itself, with inline metadata (PEP 723), and uv ignores any
// requirements.txt beside it. PythonScriptDepsImage installs that environment
// instead: the block's dependencies pass through the same preflight, pinned
// pip parser, canonical form, installer and wheel evidence as root
// requirements (#320).
func PythonScriptDepsImage(ctx context.Context, root, script string) (*PythonDependencies, error) {
	canonical, env, err := preparePythonScript(ctx, root, script)
	if err != nil {
		return nil, err
	}
	return installPythonDeps(ctx, canonical, env)
}

// PythonScriptDepsRecipe reads and admits the script's inline dependencies as
// image selection does, but never builds an application image.
func PythonScriptDepsRecipe(root, script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, env, err := preparePythonScript(ctx, root, script)
	if err != nil {
		return "", err
	}
	return env.Recipe, nil
}

type pythonScriptReport struct {
	Schema         int      `json:"schema"`
	Python         string   `json:"python"`
	Block          string   `json:"block"`
	Dependencies   []string `json:"dependencies"`
	RequiresPython string   `json:"requires_python"`
	Errors         []string `json:"errors"`
}

func preparePythonScript(ctx context.Context, root, script string) (map[string][]byte, *PythonDependencies, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	full, err := pythonScriptPath(root, script)
	if err != nil {
		return nil, nil, err
	}
	source, err := os.ReadFile(full)
	if err != nil {
		return nil, nil, err
	}
	if err := ensurePythonBase(ctx); err != nil {
		return nil, nil, err
	}
	// The pinned interpreter reads the block, as the script would be read,
	// with PEP 723's reference expression, tomllib and pip's own packaging.
	cmd := Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", pythonPlatform, "--read-only", "-v", root+":/project:ro", "-w", "/tmp", PythonImage, "/usr/local/bin/python", "-I", "-B", "-c", pythonScriptReader, "/project/"+script)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	b, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s's inline metadata with the pinned Python: %w: %s", script, err, diagnostic.String())
	}
	var report pythonScriptReport
	if err := pythonCompleteJSON(b, reflect.TypeOf(report), "report"); err != nil {
		return nil, nil, fmt.Errorf("pinned inline metadata report: %w", err)
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, nil, fmt.Errorf("invalid pinned inline metadata report: %w", err)
	}
	if len(report.Errors) > 0 {
		return nil, nil, fmt.Errorf("inline metadata in %s: %s", script, report.Errors[0])
	}
	if report.Schema != 1 || report.Python == "" || report.Block == "" || len(report.Dependencies) == 0 {
		return nil, nil, fmt.Errorf("incomplete pinned inline metadata report for %s", script)
	}
	if after, err := os.ReadFile(full); err != nil || !bytes.Equal(source, after) {
		return nil, nil, fmt.Errorf("Python environment script %s changed during parsing; retry", script)
	}
	// Each dependency becomes one line of a requirements file that only the
	// installer sees, so every rule for root requirements applies to it.
	for _, d := range report.Dependencies {
		if d == "" || strings.ContainsAny(d, "\r\n\x00") {
			return nil, nil, fmt.Errorf("inline metadata in %s: unsupported dependency %q", script, d)
		}
	}
	dir, err := os.MkdirTemp("", "invariant-python-script-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte(strings.Join(report.Dependencies, "\n")+"\n"), 0o644); err != nil {
		return nil, nil, err
	}
	inputs, err := preparePythonInputs(ctx, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("inline dependencies of %s: %w", script, err)
	}
	return inputs.canonical, pythonScriptEnvironment(script, source, []byte(report.Block), inputs.canonical), nil
}

// The image's identity covers the metadata block, not the rest of the
// script, so editing the application's code reuses its environment.
func pythonScriptEnvironment(script string, source, block []byte, canonical map[string][]byte) *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonPlatform,
		Script: &PythonInput{script, pythonDigest(block)}, Inputs: []PythonInput{{script, pythonDigest(source)}}}
	h := sha256.New()
	add := func(b []byte) { fmt.Fprintf(h, "%d\n", len(b)); h.Write(b) }
	for _, s := range []string{pythonDepsDockerfile, PythonImage, pythonPlatform, "script", script} {
		add([]byte(s))
	}
	add(block)
	keys := make([]string, 0, len(canonical))
	for k := range canonical {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add([]byte(k))
		add(canonical[k])
		env.CanonicalInputs = append(env.CanonicalInputs, PythonInput{k, pythonDigest(canonical[k])})
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

// pythonScriptPath checks every component, so a link cannot redirect the
// script outside the staged application.
func pythonScriptPath(root, script string) (string, error) {
	if script == "" || path.Clean(script) != script || path.IsAbs(script) || script == ".." || strings.HasPrefix(script, "../") || strings.ContainsAny(script, "\\\x00") || !strings.HasSuffix(script, ".py") {
		return "", fmt.Errorf("Python environment script %q must be a clean contained path to a .py file", script)
	}
	full := root
	parts := strings.Split(script, "/")
	for i, part := range parts {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if err != nil {
			return "", fmt.Errorf("Python environment script %q: %w", script, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Python environment script %q must not contain symbolic links", script)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("Python environment script %q traverses a non-directory", script)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("Python environment script %q must be a regular file", script)
		}
	}
	return full, nil
}

// PEP 723's reference expression and content rule, applied as uv applies
// them. Fields an installer couldn't honor are refused, not ignored.
const pythonScriptReader = `
import json, platform, re, sys, tomllib
from pip._vendor.packaging.requirements import InvalidRequirement, Requirement
from pip._vendor.packaging.specifiers import InvalidSpecifier, SpecifierSet
from pip._vendor.packaging.version import Version

REGEX = r"(?m)^# /// (?P<type>[a-zA-Z0-9-]+)$\s(?P<content>(^#(| .*)$\s)+)^# ///$"
report = dict(schema=1, python=platform.python_version(), block="", dependencies=[], requires_python="", errors=[])
def refuse(message):
    report["errors"].append(message)
try:
    # As Python reads a script: UTF-8, an optional BOM, universal newlines.
    with open(sys.argv[1], encoding="utf-8-sig") as f:
        script = f.read()
    blocks = [m for m in re.finditer(REGEX, script) if m.group("type") == "script"]
    if len(blocks) != 1:
        refuse("found %d '# /// script' blocks; exactly one is required" % len(blocks))
    else:
        content = "".join(line[2:] if line.startswith("# ") else line[1:]
                          for line in blocks[0].group("content").splitlines(keepends=True))
        report["block"] = content
        metadata = tomllib.loads(content)
        for key in sorted(set(metadata) - {"dependencies", "requires-python", "tool"}):
            refuse("unsupported field %r" % key)
        dependencies = metadata.get("dependencies", [])
        if not isinstance(dependencies, list) or not all(isinstance(d, str) for d in dependencies):
            refuse("dependencies must be a list of strings")
        elif not dependencies:
            refuse("it declares no dependencies")
        else:
            for d in dependencies:
                try:
                    report["dependencies"].append(str(Requirement(d)))
                except InvalidRequirement as exc:
                    refuse("invalid dependency %r: %s" % (d, exc))
        if "requires-python" in metadata:
            spec = metadata["requires-python"]
            if not isinstance(spec, str):
                refuse("requires-python must be a string")
            else:
                report["requires_python"] = spec
                try:
                    if not SpecifierSet(spec).contains(Version(platform.python_version()), prereleases=True):
                        refuse("requires-python %r excludes the pinned Python %s" % (spec, platform.python_version()))
                except InvalidSpecifier as exc:
                    refuse("invalid requires-python %r: %s" % (spec, exc))
        tool = metadata.get("tool", {})
        if not isinstance(tool, dict):
            refuse("tool must be a table")
        elif "uv" in tool:
            refuse("[tool.uv] settings can change resolution, and are not supported")
except Exception as exc:
    refuse("%s: %s" % (type(exc).__name__, exc))
print(json.dumps(report, sort_keys=True))
`
