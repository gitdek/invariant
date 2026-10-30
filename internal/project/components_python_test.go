package project

import (
	"reflect"
	"strings"
	"testing"
)

func TestPythonComponentContractOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, source, contract string
	}{
		{"unicode before call", "def f(): '世界'; Requires('café' != '')\n", "Requires('café' != '')"},
		{"BOM", "\xef\xbb\xbfdef f(): Requires(True)\n", "Requires(True)"},
		{"carriage returns", "def f():\r    Requires(\r        True\r    )\r", "Requires(\r        True\r    )"},
		{"encoded same line", "# coding: latin-1\ndef f(): 'caf\xe9'; Requires('caf\xe9' != '')\n", "Requires('caf\xe9' != '')"},
		{"parenthesized expression", "def f(): (Requires(True)) # outside\n", "Requires(True)"},
		{"internal continuation", "def f():\n    Requires(True and \\\n        True)\n", "Requires(True and \\\n        True)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := componentInput{file: ComponentFile{Path: "core.py", SHA256: componentHash([]byte(tc.source))}, bytes: []byte(tc.source)}
			out, err := componentPython("core.py", input)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.components) != 1 || out.components[0].ContractSHA256 != componentHash([]byte(tc.contract)) || out.components[0].SourceSHA256 != input.file.SHA256 {
				t.Fatalf("wrong original-byte snapshots: %#v", out)
			}
		})
	}
}

func TestPythonComponentLexicalNamespaces(t *testing.T) {
	source := `try:
    def same():
        def local(): pass
except Exception:
    class same:
        def local(self): pass
else:
    async def same(): pass
finally:
    class Container:
        if enabled:
            def method(self): pass
        else:
            async def method(self): pass
        class method:
            def child(self): pass
`
	out, err := componentPython("core.py", componentInput{bytes: []byte(source)})
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for _, c := range out.components {
		symbols = append(symbols, c.Symbol)
	}
	want := []string{"same[1]", "same[1].<locals>.local", "same[2].local", "same[3]", "Container.method[1]", "Container.method[2]", "Container.method[3].child"}
	if !reflect.DeepEqual(symbols, want) {
		t.Fatalf("lexical namespaces = %v; want %v", symbols, want)
	}
}

func TestPythonComponentMissingRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := componentPython("core.py", componentInput{bytes: []byte("def f(): pass\n")})
	if err == nil || !strings.Contains(err.Error(), "Python 3 syntax parser") {
		t.Fatalf("missing runtime not diagnosed: %v", err)
	}
}
