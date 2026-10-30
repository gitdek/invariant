package verify

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The shared canonical-staging assertion calls pyDepsAssertPipParserMatches,
// so the real parser checks each accepted fixture at its existing test site,
// including the UTF-8, traversal, canonicalization and semantic regressions.
// Recipe-only acceptance sites use pyDepsAssertAcceptedWithPip to capture the
// generated files without installing synthetic hashes or nonexistent packages.
//
// With INVARIANT_PYTHON_DEPS_REAL=1 the Docker double forwards production's
// parser invocation unchanged to real Docker and captures its actual report.
// The comparison checks that report and its followed-file closure against
// independent pinned pip observations, then compares its requirements with
// production's canonical files. It also retains matching refusal of rejected
// option-whitespace fixtures and deliberately corrupted-result controls.
// No host Python, replacement parser, resolver or installation is involved.
func TestPythonDependenciesMatchPinnedPipParser(t *testing.T) {
	firstHash, secondHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, tc := range []struct {
		name         string
		files        map[string]string
		requirements []string
		constraints  []string
	}{
		{
			name: "whitespace extras versions and markers",
			files: map[string]string{
				"requirements.txt": "# ordinary UTF-8 café comment\n\tpackaging >=24, <25  # requested\nrequests[security,socks]==2.32.3 ; python_version < '0' and (os_name == 'posix' or sys_platform == 'linux')\n",
			},
			requirements: []string{"packaging>=24,<25", "requests[security,socks]==2.32.3; python_version < '0' and (os_name == 'posix' or sys_platform == 'linux')"},
		},
		{
			name: "shared file in both roles and repeated same role",
			files: map[string]string{
				"requirements.txt":     "--requirement=deps/application.txt\n-r deps/shared.txt\n--constraint deps/shared.txt\n-cdeps/constraints.txt\n",
				"deps/application.txt": "packaging>=24,<25\n--constraint=constraints.txt\n",
				"deps/constraints.txt": "packaging==24.2\n",
				"deps/shared.txt":      "idna==3.10; python_version < '0'\n",
			},
			requirements: []string{"packaging>=24,<25", "idna==3.10; python_version < '0'"},
			constraints:  []string{"packaging==24.2", "idna==3.10; python_version < '0'"},
		},
		{
			name: "BOM CRLF hashes and false marker",
			files: map[string]string{
				"requirements.txt":     "\xef\xbb\xbf# root\r\n--requirement \\\r\n deps/application.txt\r\n-c deps/constraints.txt\r\n",
				"deps/application.txt": "\xef\xbb\xbfpackaging==24.2; os_name == 'posix' \\\r\n --hash=sha256:" + firstHash + " \\\r\n --hash=sha256:" + secondHash + "\r\nidna==3.10; python_version < '0'\r\n",
				"deps/constraints.txt": "\xef\xbb\xbfpackaging<25\r\n",
			},
			requirements: []string{"packaging==24.2; os_name == 'posix' --hash=sha256:" + firstHash + " --hash=sha256:" + secondHash, "idna==3.10; python_version < '0'"},
			constraints:  []string{"packaging<25"},
		},
		{
			name: "continued literal and coding package regression",
			files: map[string]string{
				"requirements.txt": "packaging; os_name == 'po\\\nsix'\ngeocoding==1.0\n",
			},
			requirements: []string{"packaging; os_name == 'posix'", "geocoding==1.0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pyDepsFakeDocker(t)
			root := t.TempDir()
			var inputs []string
			for filename, body := range tc.files {
				pyDepsWrite(t, root, filename, body)
				inputs = append(inputs, filename)
			}
			sort.Strings(inputs)
			before := pyDepsFiles(t, root)
			env := pyDepsBuild(t, root)
			canonical := pyDepsAssertCanonicalStaging(t, root, env, inputs)
			// Repeated includes may preserve or collapse identical entries.
			// Compare identities here, just as the real pip oracle does.
			for filename, want := range map[string][]string{"requirements.txt": tc.requirements, "constraints.txt": tc.constraints} {
				seen := map[string]bool{}
				var distinct strings.Builder
				for _, line := range strings.Split(strings.TrimSuffix(canonical[filename], "\n"), "\n") {
					identity := pyDepsComparableRequirement(t, line)
					if line != "" && !seen[identity] {
						seen[identity] = true
						distinct.WriteString(line + "\n")
					}
				}
				pyDepsAssertCanonicalLines(t, distinct.String(), want)
			}
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
				t.Fatal("canonicalization or the offline pip oracle rewrote original requirements")
			}
			if tc.name == "BOM CRLF hashes and false marker" && os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
				pyDepsPipParserNegativeControls(t, root, canonical, firstHash, secondHash)
			}
		})
	}
	t.Run("rejected option whitespace", pyDepsAssertRejectedOptionWhitespaceWithPip)
}

// #258: parsing the requirement-file options alone is insufficient: pip
// returns a TAB-delimited --hash as part of the requirement with no options.
// Its own install-requirement constructor must reject that argument. Exercise
// the same shared rejected corpus in every effective-file role, and require a
// successful Docker command that reports a known pip input error; an image,
// network, file-opening or Python failure must never count as pip refusal.
func pyDepsAssertRejectedOptionWhitespaceWithPip(t *testing.T) {
	t.Helper()
	pyDepsFakeDocker(t)
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		// Rejection-only selections still need a base for the independent
		// refusal oracle. Let a separate valid production recipe acquire it,
		// then reset the call log before asserting rejection makes no calls.
		pyDepsPreparePinnedPipOracle(t)
		pyDepsFakeDocker(t)
	}
	for _, tc := range pyDepsRejectedOptionWhitespaceFixtures() {
		for _, filename := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
			t.Run(tc.name+"/"+filename, func(t *testing.T) {
				root := pyDepsFixture(t)
				pyDepsWrite(t, root, filename, tc.raw)
				before := pyDepsFiles(t, root)
				if recipe, err := toolchain.PythonDepsRecipe(root); err == nil {
					t.Errorf("validator accepted input pinned pip must refuse in %s: recipe=%q input=%q", filename, recipe, tc.raw)
				}
				if calls := pyDepsCalls(t); len(calls) != 0 {
					t.Fatalf("rejected input reached Docker before validation: %#v", calls)
				}
				if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
					got := pyDepsRunPinnedPipRefusal(t, root)
					if !got.Refused || got.ErrorType == "" || !strings.Contains(got.Error, "Invalid requirement") || !strings.Contains(got.Error, "--hash") {
						t.Errorf("pinned pip %s did not refuse the malformed requirement/hash boundary in %s: result=%#v input=%q", got.PipVersion, filename, got, tc.raw)
					}
				}
				if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
					t.Fatal("validator or offline pip refusal oracle rewrote dependency inputs")
				}
			})
		}
	}
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("refusal oracle accepts ASCII space control", func(t *testing.T) {
			root := t.TempDir()
			pyDepsWrite(t, root, "requirements.txt", "packaging==24.2 --hash=sha256:"+strings.Repeat("a", 64)+"\n")
			got := pyDepsRunPinnedPipRefusal(t, root)
			if got.Refused || got.ErrorType != "" || got.Error != "" {
				t.Fatalf("pinned pip refusal oracle rejected its valid ASCII-space control: %#v", got)
			}
		})
	}
}

// Only called for positive inputs which otherwise exercise the recipe API or
// an intentional install/evidence failure. Keep its fake Docker and flags in
// a nested scope, so existing build-count and failure assertions are unchanged.
func pyDepsAssertAcceptedWithPip(t *testing.T, root string) {
	t.Helper()
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") != "1" {
		return
	}
	t.Run("accepted input agrees with pinned pip", func(t *testing.T) {
		for _, name := range []string{"INVARIANT_PY_DEPS_FAILURE", "INVARIANT_PY_DEPS_REPLACE_TAG", "INVARIANT_PY_DEPS_RESOLUTION"} {
			t.Setenv(name, "")
		}
		pyDepsFakeDocker(t)
		env := pyDepsBuild(t, root)
		var inputs []string
		for _, input := range env.Inputs {
			inputs = append(inputs, input.Path)
		}
		pyDepsAssertCanonicalStaging(t, root, env, inputs)
	})
}

type pyDepsPipRequirement struct {
	Name       string              `json:"name"`
	Extras     []string            `json:"extras"`
	Specifiers []string            `json:"specifiers"`
	Marker     string              `json:"marker"`
	URL        string              `json:"url"`
	Link       string              `json:"link"`
	Constraint bool                `json:"constraint"`
	Editable   bool                `json:"editable"`
	Hashes     map[string][]string `json:"hashes"`
}

type pyDepsPipComparison struct {
	PipVersion       string                   `json:"pip_version"`
	Original         []pyDepsPipRequirement   `json:"original"`
	Canonical        []pyDepsPipRequirement   `json:"canonical"`
	OriginalFiles    []pyDepsAuthorityFile    `json:"original_files"`
	OriginalIncludes []pyDepsAuthorityInclude `json:"original_includes"`
}

func pyDepsAssertPipParserMatches(t *testing.T, root string, canonical map[string]string) {
	t.Helper()
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") != "1" {
		return
	}
	got := pyDepsRunPinnedPipParser(t, root, canonical)
	if !reflect.DeepEqual(got.Original, got.Canonical) {
		original, _ := json.MarshalIndent(got.Original, "", "  ")
		generated, _ := json.MarshalIndent(got.Canonical, "", "  ")
		t.Fatalf("pinned pip %s reads different requirements, constraints or hashes from canonical files:\noriginal=%s\ncanonical=%s\ncanonical files=%#v", got.PipVersion, original, generated, canonical)
	}
	// This report comes from the production parser command forwarded to the
	// real pinned image, not from the Docker double's fixture report builder.
	var report pyDepsAuthorityReport
	found := false
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "parser" && len(call.ParserReport) != 0 {
			if err := json.Unmarshal(call.ParserReport, &report); err != nil {
				t.Fatalf("invalid production pip report: %v\n%s", err, call.ParserReport)
			}
			found = true
		}
	}
	if !found || report.Schema != 1 || report.PipVersion != got.PipVersion || report.Root != "requirements.txt" || report.Requirements == nil {
		t.Fatalf("accepted fixture lacks its actual pinned-pip production report: found=%t report=%#v oracle pip=%s", found, report, got.PipVersion)
	}
	if len(report.Errors) != 0 || len(report.Options) != 0 {
		t.Fatalf("production accepted pip errors or non-hash options: %#v", report)
	}
	var reported []pyDepsPipRequirement
	for _, requirement := range report.Requirements {
		if requirement.Path != "" || requirement.Archive || requirement.URL != "" || requirement.Link != "" || requirement.Editable || len(requirement.Options) != 0 {
			t.Fatalf("production accepted a non-PyPI form or extra option: %#v", requirement)
		}
		var specifiers []string
		if requirement.Specifier != "" {
			for _, specifier := range strings.Split(requirement.Specifier, ",") {
				specifiers = append(specifiers, strings.TrimSpace(specifier))
			}
		}
		reported = append(reported, pyDepsPipRequirement{Name: requirement.Name, Extras: requirement.Extras, Specifiers: specifiers, Marker: requirement.Marker, URL: requirement.URL, Link: requirement.Link, Constraint: requirement.Constraint, Editable: requirement.Editable, Hashes: requirement.Hashes})
	}
	if actual, want := pyDepsPipIdentities(t, reported), pyDepsPipIdentities(t, got.Canonical); !reflect.DeepEqual(actual, want) {
		t.Fatalf("canonical files disagree with production's authoritative pip report:\nreport=%q\ncanonical=%q", actual, want)
	}
	if actual, want := pyDepsJSONSet(t, report.Files), pyDepsJSONSet(t, got.OriginalFiles); !reflect.DeepEqual(actual, want) {
		t.Fatalf("production pip report lost followed files or their requirement/constraint roles:\nreport=%q\npip=%q", actual, want)
	}
	if actual, want := pyDepsJSONSet(t, report.Includes), pyDepsJSONSet(t, got.OriginalIncludes); !reflect.DeepEqual(actual, want) {
		t.Fatalf("production pip report lost includes, literal filenames or their roles:\nreport=%q\npip=%q", actual, want)
	}
}

func pyDepsPipIdentities(t *testing.T, rows []pyDepsPipRequirement) []string {
	t.Helper()
	canonicalName := func(value string) string {
		value = strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(value))
		for strings.Contains(value, "--") {
			value = strings.ReplaceAll(value, "--", "-")
		}
		return value
	}
	var normalized []pyDepsPipRequirement
	for _, row := range rows {
		row.Name = canonicalName(row.Name)
		extras := make([]string, 0, len(row.Extras))
		for _, extra := range row.Extras {
			extras = append(extras, canonicalName(extra))
		}
		sort.Strings(extras)
		row.Extras = extras
		row.Specifiers = append([]string{}, row.Specifiers...)
		sort.Strings(row.Specifiers)
		// The shared comparator accepts a whole requirement; prefix a name
		// so commas/operators inside a marker literal cannot be mistaken
		// for a requirement's reorderable specifier set.
		row.Marker = strings.TrimPrefix(pyDepsComparableRequirement(t, "placeholder;"+row.Marker), "placeholder;")
		hashes := map[string][]string{}
		for algorithm, values := range row.Hashes {
			unique := map[string]bool{}
			for _, value := range values {
				unique[value] = true
			}
			for value := range unique {
				hashes[algorithm] = append(hashes[algorithm], value)
			}
			sort.Strings(hashes[algorithm])
		}
		row.Hashes = hashes
		normalized = append(normalized, row)
	}
	return pyDepsJSONSet(t, normalized)
}

func pyDepsJSONSet[T any](t *testing.T, values []T) []string {
	t.Helper()
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !seen[string(b)] {
			result = append(result, string(b))
			seen[string(b)] = true
		}
	}
	sort.Strings(result)
	return result
}

// Resolve the actual executable before any test prepends a Docker double to
// PATH. The helper subprocess also initializes this variable, but exits in
// its existing init handler and never executes the parser.
var pyDepsParserDocker, pyDepsParserDockerErr = exec.LookPath("docker")

// Only require the executable here. The independent --pull=never,
// --platform linux/amd64 oracle run establishes that production made the
// requested platform available, including in a multi-platform image store.
// This helper must never acquire the base and hide a missing production pull.
func pyDepsRequirePinnedPipDocker(t *testing.T) {
	t.Helper()
	if pyDepsParserDockerErr != nil || !filepath.IsAbs(pyDepsParserDocker) {
		t.Fatalf("requested pinned pip acceptance requires a real Docker executable: path=%q error=%v", pyDepsParserDocker, pyDepsParserDockerErr)
	}
}

// Rejection-only and standard-library controls have no accepted dependency
// fixture of their own. Explicitly exercise the production recipe API before
// their independent oracle; never issue a Docker pull from test setup.
func pyDepsPreparePinnedPipOracle(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	pyDepsWrite(t, root, "requirements.txt", "packaging==24.2; python_version < '0'\n")
	recipe, err := toolchain.PythonDepsRecipe(root)
	if err != nil || !pyDepsDigest.MatchString(recipe) {
		t.Fatalf("valid production recipe could not prepare the independent pip oracle: recipe=%q error=%v", recipe, err)
	}
	pyDepsRequirePinnedPipDocker(t)
}

func pyDepsRunPinnedPipParser(t *testing.T, root string, canonical map[string]string) pyDepsPipComparison {
	t.Helper()
	pyDepsRequirePinnedPipDocker(t)
	if len(canonical) != 2 {
		t.Fatalf("pip parser requires exactly the generated requirements and constraints files: %#v", canonical)
	}
	canonicalDir := t.TempDir()
	for _, filename := range []string{"requirements.txt", "constraints.txt"} {
		body, ok := canonical[filename]
		if !ok {
			t.Fatalf("missing generated %s", filename)
		}
		pyDepsWrite(t, canonicalDir, filename, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, pyDepsParserDocker,
		"run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--read-only",
		"-v", root+":/original:ro", "-v", canonicalDir+":/canonical:ro", "-w", "/tmp",
		"--entrypoint", "/usr/local/bin/python", toolchain.PythonImage, "-I", "-B", "-c", pyDepsPinnedPipParser).CombinedOutput()
	if err != nil {
		t.Fatalf("pinned image's own pip could not parse accepted fixture offline: %v\n%s", err, out)
	}
	var got pyDepsPipComparison
	if err := json.Unmarshal(out, &got); err != nil || got.PipVersion == "" || got.Original == nil || got.Canonical == nil {
		t.Fatalf("invalid pinned pip parser output: error=%v\n%s", err, out)
	}
	return got
}

type pyDepsPipRefusal struct {
	PipVersion string `json:"pip_version"`
	Refused    bool   `json:"refused"`
	ErrorType  string `json:"error_type"`
	Error      string `json:"error"`
}

func pyDepsRunPinnedPipRefusal(t *testing.T, root string) pyDepsPipRefusal {
	t.Helper()
	pyDepsRequirePinnedPipDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, pyDepsParserDocker,
		"run", "--rm", "--pull=never", "--network", "none", "--platform", "linux/amd64", "--read-only",
		"-v", root+":/original:ro", "-w", "/tmp",
		"--entrypoint", "/usr/local/bin/python", toolchain.PythonImage, "-I", "-B", "-c", pyDepsPinnedPipRefusal).CombinedOutput()
	if err != nil {
		t.Fatalf("pinned pip refusal oracle failed to execute offline; this is not input rejection: %v\n%s", err, out)
	}
	var got pyDepsPipRefusal
	if err := json.Unmarshal(out, &got); err != nil || got.PipVersion == "" {
		t.Fatalf("invalid pinned pip refusal output: error=%v\n%s", err, out)
	}
	return got
}

// These controls are oracle checks: every changed canonical input must parse
// successfully, then differ. A parser exception or failed Docker command is
// a test failure, not evidence that the comparison detected semantic drift.
func pyDepsPipParserNegativeControls(t *testing.T, root string, canonical map[string]string, firstHash, secondHash string) {
	t.Helper()
	for _, change := range []string{"missing requirement", "extra requirement", "false marker dropped", "requirement became constraint", "marker space inserted", "hash changed", "second hash dropped"} {
		t.Run("oracle detects "+change, func(t *testing.T) {
			changed := map[string]string{"requirements.txt": canonical["requirements.txt"], "constraints.txt": canonical["constraints.txt"]}
			lines := strings.Split(strings.TrimSuffix(changed["requirements.txt"], "\n"), "\n")
			var retained []string
			for _, line := range lines {
				switch {
				case change == "missing requirement" && strings.HasPrefix(line, "packaging"):
					continue
				case change == "false marker dropped" && strings.HasPrefix(line, "idna"):
					continue
				case change == "requirement became constraint" && strings.HasPrefix(line, "packaging"):
					changed["constraints.txt"] += line + "\n"
					continue
				case change == "marker space inserted":
					line = strings.ReplaceAll(line, "posix", "po six")
				case change == "hash changed":
					line = strings.ReplaceAll(line, firstHash, strings.Repeat("c", 64))
				case change == "second hash dropped":
					line = strings.TrimSpace(strings.ReplaceAll(line, "--hash=sha256:"+secondHash, ""))
				}
				retained = append(retained, line)
			}
			if change == "extra requirement" {
				retained = append(retained, "six==1.17.0")
			}
			changed["requirements.txt"] = strings.Join(retained, "\n") + "\n"
			if reflect.DeepEqual(changed, canonical) {
				t.Fatal("oracle negative control did not alter canonical bytes")
			}
			got := pyDepsRunPinnedPipParser(t, root, changed)
			if reflect.DeepEqual(got.Original, got.Canonical) {
				t.Fatalf("pinned pip oracle failed to detect %s: %#v", change, got)
			}
		})
	}
}

const pyDepsPinnedPipParser = `
import json
import os
import pip
from pip._internal.network.session import PipSession
from pip._internal.req.req_file import RequirementsFileParser, parse_requirements
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._vendor.packaging.utils import canonicalize_name

def no_network(*args, **kwargs):
    raise AssertionError("pip parser attempted network access")

# Observe pip's own parsed lines before its recursive iterator consumes
# include-only rows. Merely inspecting parse_requirements' yielded entries
# misses those files and any global-option-only lines.
original_files = {}
original_includes = {}
parse_file = RequirementsFileParser._parse_file

def observe_file(self, filename, constraint):
    original = filename.startswith("/original/")
    if original:
        source = os.path.relpath(filename, "/original")
        row = {"path": source, "constraint": constraint}
        original_files[json.dumps(row, sort_keys=True)] = row
    for line in parse_file(self, filename, constraint):
        if original and line.requirement is None:
            for values, role in [(line.opts.requirements, False), (line.opts.constraints, True)]:
                for value in values or []:
                    target = os.path.relpath(os.path.normpath(os.path.join(os.path.dirname(filename), value)), "/original")
                    row = {"source": source, "target": target, "value": value, "constraint": role}
                    original_includes[json.dumps(row, sort_keys=True)] = row
        yield line

RequirementsFileParser._parse_file = observe_file

def records(sources):
    result = {}
    with PipSession() as session:
        session.request = no_network
        for filename, constraint in sources:
            for parsed in parse_requirements(filename, session=session, constraint=constraint):
                # Use pip's own constructor too: its marker splitting differs
                # from directly feeding the complete line to packaging.
                install = install_req_from_parsed_requirement(parsed, isolated=True)
                req = install.req
                if req is None:
                    raise AssertionError("accepted input is not a named PEP 508 requirement")
                row = {
                    "name": canonicalize_name(req.name),
                    "extras": sorted({canonicalize_name(extra) for extra in req.extras}),
                    "specifiers": sorted(str(specifier) for specifier in req.specifier),
                    "marker": str(install.markers) if install.markers else "",
                    "url": req.url or "",
                    "link": str(install.link) if install.link else "",
                    "constraint": install.constraint,
                    "editable": install.editable,
                    "hashes": {algorithm: sorted(set(values)) for algorithm, values in install.hash_options.items()},
                }
                # Repeating the same include in the same role, or the same
                # allowed hash, has no semantic effect. Different per-entry
                # hashes or roles stay distinct. Never evaluate markers: even
                # false-marker requirements must survive canonicalization.
                result[json.dumps(row, sort_keys=True)] = row
    return [result[key] for key in sorted(result)]

original = records([("/original/requirements.txt", False)])
canonical = records([("/canonical/requirements.txt", False), ("/canonical/constraints.txt", True)])
print(json.dumps({
    "pip_version": pip.__version__,
    "original": original,
    "canonical": canonical,
    "original_files": [original_files[key] for key in sorted(original_files)],
    "original_includes": [original_includes[key] for key in sorted(original_includes)],
}, sort_keys=True))
`

const pyDepsPinnedPipRefusal = `
import json
import pip
from pip._internal.exceptions import InstallationError
from pip._internal.network.session import PipSession
from pip._internal.req.req_file import parse_requirements
from pip._internal.req.constructors import install_req_from_parsed_requirement

def no_network(*args, **kwargs):
    raise AssertionError("pip refusal oracle attempted network access")

result = {"pip_version": pip.__version__, "refused": False, "error_type": "", "error": ""}
try:
    with PipSession() as session:
        session.request = no_network
        for parsed in parse_requirements("/original/requirements.txt", session=session):
            # req_file parsing can return the malformed --hash in its raw
            # requirement. Only the constructor establishes pip's refusal.
            install_req_from_parsed_requirement(parsed, isolated=True)
except InstallationError as error:
    if not str(error).startswith("Invalid requirement") or "--hash" not in str(error):
        raise
    result.update(refused=True, error_type=type(error).__name__, error=str(error))

# Unexpected exceptions, missing files and failed Docker invocations produce
# a nonzero process status, never a successful rejection result.
print(json.dumps(result, sort_keys=True))
`
