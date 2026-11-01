package verify

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// naginiFakeDocker puts a docker first on PATH that stands in for the pinned
// Nagini: it logs the file it's asked to prove and every file in the
// directory it's given, as "core.py: core.py helper.py", and proves the file
// unless the file says "# fake: unproved", where it fails as Nagini does.
func naginiFakeDocker(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "nagini.log")
	script := `#!/bin/sh
[ "$1" = run ] || exit 0
work= prev= file=
for a in "$@"; do
	if [ "$prev" = -v ]; then work=${a%:/work}; fi
	prev=$a
	file=$a
done
line="$file:"
for e in "$work"/* "$work"/.[!.]*; do
	[ -e "$e" ] && line="$line ${e##*/}"
done
echo "$line" >> "$FAKE_NAGINI_LOG"
if grep -q '# fake: unproved' "$work/$file"; then
	printf 'Verification failed\nErrors:\nPostcondition of inc might not hold. Assertion Result() == n + 1 might not hold. (%s@5.12--5.29)\nVerification took 1.00 seconds.\n' "$file"
	exit 1
fi
echo 'Verification successful'
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NAGINI_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// naginiRuns reads what the fake Nagini was asked to prove, in order.
func naginiRuns(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// naginiPackageOf writes files into a package directory named twophase and
// runs the legacy path's Nagini check on it, as Python.Verify does.
func naginiPackageOf(t *testing.T, files map[string]string, acquire func(context.Context) (string, error)) *Code {
	t.Helper()
	pkg := filepath.Join(t.TempDir(), "twophase")
	for name, body := range files {
		pa310Write(t, pkg, name, body)
	}
	proved, plain, err := naginiSources(pkg)
	if err != nil {
		t.Fatal(err)
	}
	code, err := runNagini(context.Background(), acquire, pkg, proved, plain)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func naginiNeverAcquired(t *testing.T) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		t.Fatal("the gate acquired Nagini's image for files it refuses")
		return "", nil
	}
}

func naginiFakeImage(context.Context) (string, error) { return "invariant-nagini:fake", nil }

const (
	// naginiProbeDivision is the issue's first probe: a core proves
	// fdiv(7, -2) == -3, where Python gives -4, through a helper whose
	// contract divides as Viper does. The pinned Nagini verifies it.
	naginiProbeDivision = "# +nagini\nfrom nagini_contracts.contracts import *\nfrom helper import fdiv\n\n\n" +
		"def seven_by_minus_two() -> int:\n    Ensures(Result() == -3)\n    return fdiv(7, -2)\n"
	naginiDivisionHelper = "from nagini_contracts.contracts import *\n\n\n" +
		"def fdiv(a: int, b: int) -> int:\n    Requires(b != 0)\n    Ensures(Result() == a // b)\n    return a // b\n"
	// naginiProbeBody is the second: a helper whose body breaks its own
	// contract, which Nagini rejects run on its own, still lets the core
	// that imports it verify.
	naginiProbeBody = "# +nagini\nfrom nagini_contracts.contracts import *\nfrom helper import inc\n\n\n" +
		"def two() -> int:\n    Ensures(Result() == 2)\n    return inc(1)\n"
	naginiBrokenHelper = "from nagini_contracts.contracts import *\n\n\n" +
		"def inc(n: int) -> int:\n    Ensures(Result() == n + 1)\n    return n\n"
	// naginiHelper is a marked helper whose body keeps its contract.
	naginiHelper = "# +nagini\nfrom nagini_contracts.contracts import *\n\n\n" +
		"def inc(n: int) -> int:\n    Ensures(Result() == n + 1)\n    return n + 1\n"
	naginiUnmarked   = ", which isn't marked # +nagini, so Nagini would take its contracts without proving its bodies"
	naginiOwnPackage = "which the pinned Nagini can't resolve: it proves each marked file as a top-level module of its own, " +
		"so it finds another marked file only by that file's own name, as import helper finds helper.py"
)

// naginiCoreImporting is the second probe's core with imports in place of
// its import of helper, from line 3 on.
func naginiCoreImporting(imports string) string {
	return strings.Replace(naginiProbeBody, "from helper import inc\n", imports, 1)
}

// #383: the pinned Nagini reads the modules a marked file imports and
// trusts their contracts without proving their bodies. Its runs: the first
// probe's core verified fdiv(7, -2) == -3, and the second's verified, though
// Nagini, run on the helper alone, says "Postcondition of inc might not
// hold". The gate refuses each core before Nagini runs, and the receipt
// names its function as one Nagini never saw.
func TestNaginiRefusesACoreThatRestsOnAnUnprovedModule(t *testing.T) {
	for _, c := range []struct{ name, core, helper, function string }{
		{"a helper whose contract divides as Viper does", naginiProbeDivision, naginiDivisionHelper, "seven_by_minus_two"},
		{"a helper whose body breaks its contract", naginiProbeBody, naginiBrokenHelper, "two"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code := naginiPackageOf(t, map[string]string{"core.py": c.core, "helper.py": c.helper}, naginiNeverAcquired(t))
			want := []string{"core.py:3: refused before Nagini runs: the line imports helper, the package's helper.py" + naginiUnmarked}
			if code.Passed || !reflect.DeepEqual(code.Errors, want) {
				t.Fatalf("passed %v, errors %q; want %q", code.Passed, code.Errors, want)
			}
			if len(code.Functions) != 0 || len(code.Contracts) != 0 || !slices.Contains(code.Unverified, c.function) {
				t.Fatalf("code = %+v, want %s named as a function Nagini never saw", code, c.function)
			}
		})
	}
	// Marked, the helper is the gate's to prove, so the arithmetic rule
	// applies to it too: the gate refuses its floor division by a name, and
	// the core whose proof would rest on it.
	code := naginiPackageOf(t, map[string]string{"core.py": naginiProbeDivision, "helper.py": "# +nagini\n" + naginiDivisionHelper}, naginiNeverAcquired(t))
	want := []string{
		"core.py:3: refused before Nagini runs: the line imports helper, the package's helper.py, which the gate refuses, so the proof would rest on contracts nothing proves",
		"helper.py:7: refused before Nagini runs: the line holds a floor division by other than a positive integer literal",
		"helper.py:8: refused before Nagini runs: the line holds a floor division by other than a positive integer literal",
	}
	if code.Passed || len(code.Errors) != 3 || code.Errors[0] != want[0] || !strings.HasPrefix(code.Errors[1], want[1]) || !strings.HasPrefix(code.Errors[2], want[2]) {
		t.Fatalf("errors %q; want %q", code.Errors, want)
	}
	if !reflect.DeepEqual(code.Unverified, []string{"seven_by_minus_two", "fdiv"}) || len(code.Functions) != 0 {
		t.Fatalf("functions %v, unverified %v", code.Functions, code.Unverified)
	}
}

// What a marked file may import from its package, and how the gate reads
// each form: another marked file it proves in the same run, by that file's
// name, is the only module of the package a marked file may import.
func TestNaginiReadsEveryImportOfAMarkedFile(t *testing.T) {
	refused := func(line, why string) string {
		return "core.py:" + line + ": refused before Nagini runs: the line " + why
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		// errors are the refusals, by prefix, and runs what Nagini was
		// asked to prove, with what its directory held.
		errors, runs []string
	}{
		{name: "a plain module", files: map[string]string{"core.py": naginiCoreImporting("import helper\n"), "helper.py": naginiBrokenHelper},
			errors: []string{refused("3", "imports helper, the package's helper.py"+naginiUnmarked)}},
		{name: "a plain module by another name", files: map[string]string{"core.py": naginiCoreImporting("import helper as h\n"), "helper.py": naginiBrokenHelper},
			errors: []string{refused("3", "imports helper, the package's helper.py"+naginiUnmarked)}},
		{name: "an import in a function", files: map[string]string{"helper.py": naginiBrokenHelper,
			"core.py": "# +nagini\nfrom nagini_contracts.contracts import *\n\n\ndef two() -> int:\n    from helper import inc\n    return inc(1)\n"},
			errors: []string{refused("6", "imports helper, the package's helper.py"+naginiUnmarked)}},
		{name: "an import in a try", files: map[string]string{"helper.py": naginiBrokenHelper,
			"core.py": naginiCoreImporting("try:\n    import helper\nexcept ImportError:\n    pass\n")},
			errors: []string{refused("4", "imports helper, the package's helper.py"+naginiUnmarked)}},
		{name: "a test", files: map[string]string{"core.py": naginiCoreImporting("from test_helper import inc\n"), "test_helper.py": naginiHelper},
			errors: []string{refused("3", "imports test_helper, the package's test_helper.py, a test, which the gate never proves")}},
		{name: "a stub beside a marked module", files: map[string]string{"core.py": naginiCoreImporting("from helper import inc\n"), "helper.py": naginiHelper,
			"helper.pyi": "from nagini_contracts.contracts import *\n\n\ndef inc(n: int) -> int:\n    Ensures(Result() == n + 2)\n    ...\n"},
			errors: []string{refused("3", "imports helper, which Nagini would read from the package's helper.pyi, a stub the gate never proves, and take its contracts")},
			runs:   []string{"helper.py: helper.py"}},
		{name: "a package", files: map[string]string{"core.py": naginiCoreImporting("from helper.inc import inc\n"), "helper/__init__.py": "", "helper/inc.py": naginiHelper},
			errors: []string{refused("3", "imports helper.inc, which Nagini would read from the package's directory helper, whose modules the gate never proves")}},
		{name: "a stub package", files: map[string]string{"core.py": naginiCoreImporting("from helper import inc\n"), "helper.py": naginiHelper, "helper-stubs/__init__.pyi": ""},
			errors: []string{refused("3", "imports helper, which Nagini would read from the package's directory helper-stubs, a stub package the gate never proves")},
			runs:   []string{"helper.py: helper.py"}},
		{name: "a relative import", files: map[string]string{"core.py": naginiCoreImporting("from .helper import inc\n"), "helper.py": naginiHelper},
			errors: []string{refused("3", "imports from .helper, relative to its package, "+naginiOwnPackage)}, runs: []string{"helper.py: helper.py"}},
		{name: "the package itself", files: map[string]string{"core.py": naginiCoreImporting("from . import helper\n"), "helper.py": naginiHelper},
			errors: []string{refused("3", "imports from ., relative to its package, "+naginiOwnPackage)}, runs: []string{"helper.py: helper.py"}},
		{name: "the package by its own name", files: map[string]string{"core.py": naginiCoreImporting("from twophase.helper import inc\n"), "helper.py": naginiHelper},
			errors: []string{refused("3", "imports twophase.helper, its own package by name, "+naginiOwnPackage)}, runs: []string{"helper.py: helper.py"}},
		// A refusal reaches every proof that would rest on it.
		{name: "a chain", files: map[string]string{"core.py": naginiCoreImporting("from middle import inc\n"), "helper.py": naginiBrokenHelper,
			"middle.py": "# +nagini\nfrom nagini_contracts.contracts import *\nfrom helper import inc\n"},
			errors: []string{refused("3", "imports middle, the package's middle.py, which the gate refuses"),
				"middle.py:3: refused before Nagini runs: the line imports helper, the package's helper.py" + naginiUnmarked}},
		// What the package doesn't hold, or the standard library holds
		// whatever the package does, is Nagini's to read as before, and
		// its directory holds no other file of the package.
		{name: "the standard library over a module the package holds", files: map[string]string{"json.py": naginiBrokenHelper,
			"core.py": naginiCoreImporting("import json\nfrom typing import List\nimport math\nfrom nagini_contracts.contracts import Result\nimport nagini_contracts.io_builtins\n")},
			runs: []string{"core.py: core.py"}},
		{name: "a module the package doesn't hold", files: map[string]string{"core.py": naginiCoreImporting("import yaml\n"), "explore.py": "import json\n", "__init__.py": ""},
			runs: []string{"core.py: core.py"}},
		{name: "another marked core", files: map[string]string{"core.py": naginiCoreImporting("from helper import inc\n"), "helper.py": naginiHelper, "explore.py": "from helper import inc\n"},
			runs: []string{"core.py: core.py helper.py", "helper.py: helper.py"}},
		{name: "another marked core, by module", files: map[string]string{"core.py": naginiCoreImporting("import helper\n"), "helper.py": naginiHelper},
			runs: []string{"core.py: core.py helper.py", "helper.py: helper.py"}},
		{name: "marked cores that import each other", files: map[string]string{"core.py": naginiCoreImporting("from helper import inc\n"),
			"helper.py": strings.Replace(naginiHelper, "\n\n\ndef", "\nimport core\n\n\ndef", 1)},
			runs: []string{"core.py: core.py helper.py", "helper.py: core.py helper.py"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := naginiFakeDocker(t)
			code := naginiPackageOf(t, c.files, naginiFakeImage)
			if len(code.Errors) != len(c.errors) {
				t.Fatalf("errors %q, want %d starting %q", code.Errors, len(c.errors), c.errors)
			}
			for i, want := range c.errors {
				if !strings.HasPrefix(code.Errors[i], want) {
					t.Fatalf("errors %q, want %q", code.Errors, c.errors)
				}
			}
			if code.Passed != (len(c.errors) == 0) {
				t.Fatalf("passed %v with errors %q", code.Passed, code.Errors)
			}
			if runs := naginiRuns(t, log); !reflect.DeepEqual(runs, c.runs) {
				t.Fatalf("Nagini ran %q, want %q", runs, c.runs)
			}
		})
	}
}

// A core that imports another marked core proves both, and the receipt
// counts both; a proof that rests on one Nagini didn't prove doesn't stand,
// and the gate says so beside Nagini's own error.
func TestNaginiProvesAMarkedCoreAndTheMarkedCoreItImports(t *testing.T) {
	log := naginiFakeDocker(t)
	code := naginiPackageOf(t, map[string]string{"core.py": naginiProbeBody, "helper.py": naginiHelper}, naginiFakeImage)
	if !code.Passed || !reflect.DeepEqual(code.Functions, []string{"two", "inc"}) || !reflect.DeepEqual(code.Contracts, []string{"two", "inc"}) || len(code.Errors) != 0 {
		t.Fatalf("code = %+v, want both files proved", code)
	}
	if runs := naginiRuns(t, log); !reflect.DeepEqual(runs, []string{"core.py: core.py helper.py", "helper.py: helper.py"}) {
		t.Fatalf("Nagini ran %q", runs)
	}

	naginiFakeDocker(t)
	code = naginiPackageOf(t, map[string]string{"core.py": naginiProbeBody, "helper.py": "# +nagini\n# fake: unproved\n" + naginiBrokenHelper}, naginiFakeImage)
	want := []string{"helper.py:5:12: Postcondition of inc might not hold. Assertion Result() == n + 1 might not hold.",
		"core.py: its proof rests on helper.py's contracts, which Nagini didn't prove"}
	if code.Passed || !reflect.DeepEqual(code.Errors, want) {
		t.Fatalf("passed %v, errors %q; want %q", code.Passed, code.Errors, want)
	}
}

// Today's cores import nothing of their package, so each runs as before,
// in a directory that now holds only the core.
func TestNaginiRunsTodaysCoresAlone(t *testing.T) {
	cores, err := filepath.Glob("testdata/examples/*/*/core.py")
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, core := range cores {
		src, err := os.ReadFile(core)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLine(string(src), NaginiHeader) {
			continue
		}
		marked++
		t.Run(filepath.Base(filepath.Dir(filepath.Dir(core))), func(t *testing.T) {
			log := naginiFakeDocker(t)
			pkg := filepath.Join(t.TempDir(), filepath.Base(filepath.Dir(core)))
			if err := copyTree(filepath.Dir(core), pkg); err != nil {
				t.Fatal(err)
			}
			proved, plain, err := naginiSources(pkg)
			if err != nil {
				t.Fatal(err)
			}
			code, err := runNagini(context.Background(), naginiFakeImage, pkg, proved, plain)
			if err != nil {
				t.Fatal(err)
			}
			if !code.Passed || len(code.Errors) != 0 || len(code.Functions) == 0 {
				t.Fatalf("code = %+v", code)
			}
			if runs := naginiRuns(t, log); !reflect.DeepEqual(runs, []string{"core.py: core.py"}) {
				t.Fatalf("Nagini ran %q", runs)
			}
		})
	}
	if marked < 2 {
		t.Fatalf("found %d marked cores in testdata, want both two-phase commits", marked)
	}
}

// naginiAlone is a marked core that imports nothing of its package.
const naginiAlone = "# +nagini\nfrom nagini_contracts.contracts import *\n\n\ndef two() -> int:\n    Ensures(Result() == 2)\n    return 2\n"

// What Python may load in place of a marked file, and so run where the gate
// claims the file is proved: an extension, which Python's finder takes
// before the source, cached bytecode, which CPython 3.13 runs in place of
// the source when it's unchecked, a package, which it takes before the
// file, and, as D-0260 refuses where an in-place proof's module would be,
// sourceless bytecode. The gate refuses each before Nagini runs, for a core
// and for the marked file a core imports, so a core that imports one is
// refused too. A namespace directory doesn't shadow the file, and another
// module's compiled forms don't either.
func TestNaginiRefusesWhatPythonMayLoadInAMarkedFilesPlace(t *testing.T) {
	const elf = "\x7fELF\x02\x01\x01"
	shadowed := func(file, what string) string {
		return file + ": refused before Nagini runs: Python may load the package's " + what + ", in place of " + file +
			", so what runs needn't be what Nagini proves"
	}
	for _, c := range []struct {
		name         string
		files        map[string]string
		errors, runs []string
	}{
		{name: "an extension", files: map[string]string{"core.py": naginiAlone, "core.cpython-313-x86_64-linux-gnu.so": elf},
			errors: []string{shadowed("core.py", "core.cpython-313-x86_64-linux-gnu.so, a compiled module the gate can't read")}},
		{name: "a stable ABI extension", files: map[string]string{"core.py": naginiAlone, "core.abi3.so": elf},
			errors: []string{shadowed("core.py", "core.abi3.so, a compiled module the gate can't read")}},
		{name: "a bare extension", files: map[string]string{"core.py": naginiAlone, "core.so": elf},
			errors: []string{shadowed("core.py", "core.so, a compiled module the gate can't read")}},
		{name: "a Windows extension", files: map[string]string{"core.py": naginiAlone, "core.cp313-win_amd64.pyd": "MZ"},
			errors: []string{shadowed("core.py", "core.cp313-win_amd64.pyd, a compiled module the gate can't read")}},
		{name: "a bare Windows extension", files: map[string]string{"core.py": naginiAlone, "core.pyd": "MZ"},
			errors: []string{shadowed("core.py", "core.pyd, a compiled module the gate can't read")}},
		{name: "sourceless bytecode", files: map[string]string{"core.py": naginiAlone, "core.pyc": "\xf3\r\r\n"},
			errors: []string{shadowed("core.py", "core.pyc, a compiled module the gate can't read")}},
		{name: "cached bytecode", files: map[string]string{"core.py": naginiAlone, "__pycache__/core.cpython-313.pyc": "\xf3\r\r\n\x01"},
			errors: []string{shadowed("core.py", "__pycache__/core.cpython-313.pyc, bytecode the gate can't read")}},
		{name: "cached bytecode for another Python", files: map[string]string{"core.py": naginiAlone, "__pycache__/core.cpython-312.opt-1.pyc": "\xcb\r\r\n"},
			errors: []string{shadowed("core.py", "__pycache__/core.cpython-312.opt-1.pyc, bytecode the gate can't read")}},
		{name: "a package", files: map[string]string{"core.py": naginiAlone, "core/__init__.py": "TWO = 2\n"},
			errors: []string{shadowed("core.py", "directory core, a package the gate never proves")}},
		{name: "a compiled package", files: map[string]string{"core.py": naginiAlone, "core/__init__.cpython-313-x86_64-linux-gnu.so": elf},
			errors: []string{shadowed("core.py", "directory core, a package the gate never proves")}},
		// The marked file a core imports is held to the same, so the core
		// is refused with it.
		{name: "an extension beside the marked file a core imports", files: map[string]string{"core.py": naginiProbeBody, "helper.py": naginiHelper,
			"helper.cpython-313-x86_64-linux-gnu.so": elf},
			errors: []string{"core.py:3: refused before Nagini runs: the line imports helper, which Python may load from the package's helper.cpython-313-x86_64-linux-gnu.so, a compiled module the gate can't read",
				shadowed("helper.py", "helper.cpython-313-x86_64-linux-gnu.so, a compiled module the gate can't read")}},
		{name: "cached bytecode for the marked file a core imports", files: map[string]string{"core.py": naginiProbeBody, "helper.py": naginiHelper,
			"__pycache__/helper.cpython-313.pyc": "\xf3\r\r\n\x01"},
			errors: []string{"core.py:3: refused before Nagini runs: the line imports helper, the package's helper.py, which the gate refuses, so the proof would rest on contracts nothing proves",
				shadowed("helper.py", "__pycache__/helper.cpython-313.pyc, bytecode the gate can't read")}},
		{name: "a package beside the marked file a core imports", files: map[string]string{"core.py": naginiProbeBody, "helper.py": naginiHelper,
			"helper/__init__.py": "def inc(n):\n    return n\n"},
			errors: []string{"core.py:3: refused before Nagini runs: the line imports helper, which Nagini would read from the package's directory helper",
				shadowed("helper.py", "directory helper, a package the gate never proves")}},
		{name: "a module the package holds only compiled", files: map[string]string{"core.py": naginiProbeBody, "helper.cpython-313-x86_64-linux-gnu.so": elf},
			errors: []string{"core.py:3: refused before Nagini runs: the line imports helper, which Python may load from the package's helper.cpython-313-x86_64-linux-gnu.so, a compiled module the gate can't read"}},
		{name: "a module the package holds only as sourceless bytecode", files: map[string]string{"core.py": naginiProbeBody, "helper.pyc": "\xf3\r\r\n"},
			errors: []string{"core.py:3: refused before Nagini runs: the line imports helper, which Python may load from the package's helper.pyc, a compiled module the gate can't read"}},
		// What doesn't stand where a marked file loads from.
		{name: "a namespace directory", files: map[string]string{"core.py": naginiAlone, "core/data.txt": "2\n"},
			runs: []string{"core.py: core.py"}},
		{name: "another module's compiled forms", files: map[string]string{"core.py": naginiAlone, "explore.py": "import json\n",
			"explore.cpython-313-x86_64-linux-gnu.so": elf, "__pycache__/explore.cpython-313.pyc": "\xf3\r\r\n", "coreutils.so": elf, "core_test.pyc": "\xf3\r\r\n"},
			runs: []string{"core.py: core.py"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := naginiFakeDocker(t)
			code := naginiPackageOf(t, c.files, naginiFakeImage)
			if len(code.Errors) != len(c.errors) {
				t.Fatalf("errors %q, want %q", code.Errors, c.errors)
			}
			for i, want := range c.errors {
				if !strings.HasPrefix(code.Errors[i], want) {
					t.Fatalf("errors %q, want %q", code.Errors, c.errors)
				}
			}
			if code.Passed != (len(c.errors) == 0) || (len(c.errors) > 0 && len(code.Functions) != 0) {
				t.Fatalf("code = %+v", code)
			}
			if runs := naginiRuns(t, log); !reflect.DeepEqual(runs, c.runs) {
				t.Fatalf("Nagini ran %q, want %q", runs, c.runs)
			}
		})
	}
}
