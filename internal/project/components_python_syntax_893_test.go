package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #893: a source's syntax is read once for its bytes and its interpreter,
// while the process lives. The stand-in is an interpreter that counts its
// starts, runs body, and then becomes the real one.
func syntax893StandIn(t *testing.T, body string) (dir, calls string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir, calls = t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := "#!" + python + " -IS\nimport os, sys\n" +
		"with open(" + strconv.Quote(calls) + ", 'a') as f:\n    f.write('start\\n')\n" + body +
		"os.execv(" + strconv.Quote(python) + ", [sys.argv[0]] + sys.argv[1:])\n"
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func syntax893Starts(t *testing.T, calls string) int {
	t.Helper()
	b, err := os.ReadFile(calls)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(b), "start\n")
}

// syntax893Kept counts the answers kept for the interpreter in dir.
func syntax893Kept(dir string) int {
	componentPythonSyntax.Lock()
	defer componentPythonSyntax.Unlock()
	n := 0
	for key := range componentPythonSyntax.kept {
		if key.interpreter == filepath.Join(dir, "python3") {
			n++
		}
	}
	return n
}

func syntax893Read(t *testing.T, source, text string) componentSource {
	t.Helper()
	out, err := componentPython(source, componentInput{file: ComponentFile{Path: source, SHA256: componentHash([]byte(text))}, bytes: []byte(text)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPythonSyntaxIsReadOnceForItsBytes893(t *testing.T) {
	dir, calls := syntax893StandIn(t, "")
	t.Setenv("PATH", dir)
	text := "def once():\n    Requires(True)\n    Exsures(ValueError, True)\n# " + t.Name() + "\n"
	first := syntax893Read(t, "core.py", text)
	if n := syntax893Starts(t, calls); n != 1 {
		t.Fatalf("the first reading started the parser %d times", n)
	}
	want := []Component{{ID: "python:core#once@core.py", Language: "python", Package: "core", Symbol: "once", Source: "core.py",
		SourceSHA256: componentHash([]byte(text)), ContractSHA256: componentHash([]byte("Requires(True)Exsures(ValueError, True)"))}}
	exsures := append([]componentExsures(nil), first.exsures["once"]...)
	if !reflect.DeepEqual(first.components, want) || len(exsures) != 1 {
		t.Fatalf("the first reading: %+v, %+v", first.components, first.exsures)
	}
	// The same bytes under another name are the same syntax, named for that
	// source.
	second := syntax893Read(t, "pkg/other.py", text)
	if n := syntax893Starts(t, calls); n != 1 {
		t.Fatalf("the same bytes started the parser again: %d starts", n)
	}
	if c := second.components; len(c) != 1 || c[0].ID != "python:pkg.other#once@pkg/other.py" || c[0].Package != "pkg.other" || c[0].ContractSHA256 != want[0].ContractSHA256 {
		t.Fatalf("the second reading isn't its own source's: %+v", c)
	}
	// What a caller does with its answer doesn't reach the next caller,
	// whether its answer was read or kept.
	for _, mine := range []componentSource{first, second} {
		mine.components[0].Symbol = "changed"
		mine.exsures["once"][0] = componentExsures{Expression: "changed"}
	}
	if again := syntax893Read(t, "core.py", text); !reflect.DeepEqual(again.components, want) || !reflect.DeepEqual(again.exsures["once"], exsures) {
		t.Fatalf("a caller's change reached a later reading: %+v, %+v", again.components, again.exsures)
	}
	if n := syntax893Starts(t, calls); n != 1 {
		t.Fatalf("a third reading started the parser: %d starts", n)
	}
	// Other bytes are read.
	if out := syntax893Read(t, "core.py", text+"def more(): pass\n"); len(out.components) != 2 {
		t.Fatalf("changed bytes kept the earlier answer: %+v", out.components)
	}
	if n := syntax893Starts(t, calls); n != 2 {
		t.Fatalf("changed bytes: %d starts, want 2", n)
	}
}

func TestPythonSyntaxAsksAnotherInterpreterAgain893(t *testing.T) {
	text := "def other(): pass\n# " + t.Name() + "\n"
	first, firstCalls := syntax893StandIn(t, "")
	second, secondCalls := syntax893StandIn(t, "")
	// The two differ only in where they are: the same size, and the same
	// modification time.
	name, other := filepath.Join(first, "python3"), filepath.Join(second, "python3")
	made := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, n := range []string{name, other} {
		if err := os.Chtimes(n, made, made); err != nil {
			t.Fatal(err)
		}
	}
	if a, err := os.Stat(name); err != nil {
		t.Fatal(err)
	} else if b, err := os.Stat(other); err != nil || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		t.Fatalf("the stand-ins differ in more than their place: %v, %v, %v", a, b, err)
	}
	t.Setenv("PATH", first)
	syntax893Read(t, "core.py", text)
	t.Setenv("PATH", second)
	syntax893Read(t, "core.py", text)
	if a, b := syntax893Starts(t, firstCalls), syntax893Starts(t, secondCalls); a != 1 || b != 1 {
		t.Fatalf("another interpreter wasn't asked: %d and %d starts", a, b)
	}
	t.Setenv("PATH", first)
	syntax893Read(t, "core.py", text)
	if a := syntax893Starts(t, firstCalls); a != 1 {
		t.Fatalf("the first interpreter was asked again: %d starts", a)
	}
	// The same path holding another program is another interpreter: by its
	// size alone, and by its modification time alone.
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, append(b, "# replaced\n"...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, made, made); err != nil {
		t.Fatal(err)
	}
	syntax893Read(t, "core.py", text)
	if a := syntax893Starts(t, firstCalls); a != 2 {
		t.Fatalf("a replaced interpreter of another size: %d starts, want 2", a)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(name, later, later); err != nil {
		t.Fatal(err)
	}
	syntax893Read(t, "core.py", text)
	if a := syntax893Starts(t, firstCalls); a != 3 {
		t.Fatalf("a replaced interpreter of the same size: %d starts, want 3", a)
	}
}

func TestPythonSyntaxKeepsNoFailure893(t *testing.T) {
	for _, tc := range []struct{ name, fail, want string }{
		{"the interpreter fails", "sys.exit(3)", "isolated Python 3 syntax parser"},
		{"the answer isn't the parser's", "print('not an inventory'); sys.exit(0)", "Python syntax parser output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			works := filepath.Join(t.TempDir(), "works")
			dir, calls := syntax893StandIn(t, "if not os.path.exists("+strconv.Quote(works)+"):\n    "+tc.fail+"\n")
			t.Setenv("PATH", dir)
			text := "def kept(): pass\n# " + t.Name() + "\n"
			input := componentInput{file: ComponentFile{Path: "core.py", SHA256: componentHash([]byte(text))}, bytes: []byte(text)}
			for i := 1; i <= 2; i++ {
				if _, err := componentPython("core.py", input); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("reading %d: want %q, got %v", i, tc.want, err)
				}
				if n := syntax893Starts(t, calls); n != i {
					t.Fatalf("a failed reading was kept: %d starts after %d readings", n, i)
				}
			}
			if n := syntax893Kept(dir); n != 0 {
				t.Fatalf("%d answers kept from readings that failed", n)
			}
			if err := os.WriteFile(works, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if out, err := componentPython("core.py", input); err != nil || len(out.components) != 1 {
					t.Fatalf("the interpreter works now: %+v, %v", out.components, err)
				}
			}
			if n := syntax893Starts(t, calls); n != 3 {
				t.Fatalf("%d starts, want two failures and one reading", n)
			}
			if n := syntax893Kept(dir); n != 1 {
				t.Fatalf("%d answers kept, want the one that was read", n)
			}
		})
	}
}

func TestPythonSyntaxKeptIsBounded893(t *testing.T) {
	dir, calls := syntax893StandIn(t, "")
	t.Setenv("PATH", dir)
	text := "def bounded(): pass\n# " + t.Name() + "\n"
	syntax893Read(t, "core.py", text)
	componentPythonSyntax.Lock()
	for i := 0; len(componentPythonSyntax.kept) < componentPythonSyntaxLimit; i++ {
		componentPythonSyntax.kept[componentPythonSyntaxKey{interpreter: t.Name(), size: int64(i)}] = []byte("[]")
	}
	componentPythonSyntax.Unlock()
	syntax893Read(t, "core.py", text+"def one_more(): pass\n")
	componentPythonSyntax.Lock()
	kept := len(componentPythonSyntax.kept)
	componentPythonSyntax.Unlock()
	if kept != 1 {
		t.Fatalf("%d answers kept past the limit of %d, want only the newest", kept, componentPythonSyntaxLimit)
	}
	syntax893Read(t, "core.py", text)
	if n := syntax893Starts(t, calls); n != 3 {
		t.Fatalf("%d starts, want the first source read again once what was kept was dropped", n)
	}
}
