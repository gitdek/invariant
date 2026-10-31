package toolchain

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPythonScriptPathRefusals(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"serve.py": "", "app/main.py": "", "notes.txt": "", "outside/real.py": ""} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "serve.py"), filepath.Join(root, "alias.py")); err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"serve.py", "app/main.py"} {
		if _, err := pythonScriptPath(root, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "notes.txt", "missing.py", "../serve.py", "/serve.py", "./serve.py", "app//main.py", `app\main.py`, "app", "linked/real.py", "alias.py"} {
		if _, err := pythonScriptPath(root, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// The environment's identity follows its metadata block and what pip made of
// it, never the rest of the script, while its inputs bind the script as is.
func TestPythonScriptEnvironmentIdentity(t *testing.T) {
	canonical := map[string][]byte{"requirements.txt": []byte("packaging==24.2\n"), "constraints.txt": {}}
	block := []byte("dependencies = [\"packaging==24.2\"]\n")
	first := pythonScriptEnvironment("serve.py", []byte("# block\nprint(1)\n"), block, canonical)
	edited := pythonScriptEnvironment("serve.py", []byte("# block\nprint(2)\n"), block, canonical)
	if first.Recipe != edited.Recipe || reflect.DeepEqual(first.Inputs, edited.Inputs) {
		t.Fatalf("editing code outside the block must keep the recipe and change the input: %#v %#v", first, edited)
	}
	if *first.Script != (PythonInput{"serve.py", pythonDigest(block)}) || len(first.Inputs) != 1 || first.Inputs[0] != (PythonInput{"serve.py", pythonDigest([]byte("# block\nprint(1)\n"))}) {
		t.Fatalf("wrong script evidence: %#v", first)
	}
	if len(first.CanonicalInputs) != 2 || first.CanonicalInputs[0].Path != "constraints.txt" || first.CanonicalInputs[1].Path != "requirements.txt" {
		t.Fatalf("canonical inputs out of order: %#v", first.CanonicalInputs)
	}
	for name, other := range map[string]*PythonDependencies{
		"block":     pythonScriptEnvironment("serve.py", nil, []byte("dependencies = [\"packaging>=24\"]\n"), canonical),
		"script":    pythonScriptEnvironment("main.py", nil, block, canonical),
		"canonical": pythonScriptEnvironment("serve.py", nil, block, map[string][]byte{"requirements.txt": []byte("packaging==24.1\n"), "constraints.txt": {}}),
	} {
		if other.Recipe == first.Recipe {
			t.Errorf("changing the %s kept the recipe", name)
		}
	}
	// Root requirements with the same canonical lines name a different image.
	p := &pythonInputs{bytes: map[string][]byte{"requirements.txt": []byte("packaging==24.2\n")}, canonical: canonical}
	if p.environment().Recipe == first.Recipe {
		t.Fatal("inline and root-requirements environments share a recipe")
	}
}

// The reader runs in the pinned image in production. Here it runs on any
// local Python new enough to have tomllib, with pip importable.
func TestPythonScriptReader(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if exec.Command(python, "-I", "-c", "import tomllib, pip").Run() != nil {
		t.Skip("python3 lacks tomllib or pip; the reader needs Python 3.11 or newer")
	}
	version, err := exec.Command(python, "-I", "-c", "import platform; print(platform.python_version())").Output()
	if err != nil {
		t.Fatal(err)
	}
	read := func(script string) pythonScriptReport {
		t.Helper()
		f := filepath.Join(t.TempDir(), "serve.py")
		if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(python, "-I", "-B", "-c", pythonScriptReader, f).Output()
		if err != nil {
			t.Fatalf("reader failed: %v", err)
		}
		var r pythonScriptReport
		if err := pythonCompleteJSON(out, reflect.TypeOf(r), "report"); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	block := "# /// script\n# requires-python = \">=3.11\"\n# dependencies = [\n#   \"FastAPI >= 0.100\",\n#   \"uvicorn[standard]\",\n#   'tomli; python_version < \"3.11\"',\n# ]\n#\n# [tool.ruff]\n# line-length = 100\n# ///\n"
	r := read("#!/usr/bin/env python3\n" + block + "import json\n")
	want := []string{"FastAPI>=0.100", "uvicorn[standard]", `tomli; python_version < "3.11"`}
	if len(r.Errors) != 0 || !reflect.DeepEqual(r.Dependencies, want) || r.RequiresPython != ">=3.11" || r.Python != strings.TrimSpace(string(version)) || !strings.HasPrefix(r.Block, "requires-python") || !strings.Contains(r.Block, "\n\n[tool.ruff]") {
		t.Fatalf("wrong report: %#v", r)
	}
	// Python reads scripts with universal newlines and an optional BOM.
	again := read("\ufeff" + strings.ReplaceAll("#!/usr/bin/env python3\n"+block, "\n", "\r\n"))
	if len(again.Errors) != 0 || again.Block != r.Block || !reflect.DeepEqual(again.Dependencies, want) {
		t.Fatalf("CRLF and BOM changed the block: %#v", again)
	}
	// A closing line followed by more comment lines extends the block, as
	// PEP 723's reference expression gives precedence to the last one.
	extended := read("# /// script\n# dependencies = [\"six\"]\n# ///\n# [tool.other]\n# x = 1\n# ///\n")
	if len(extended.Errors) == 0 {
		t.Fatalf("a TOML-invalid extended block was accepted: %#v", extended)
	}
	for name, script := range map[string]string{
		"none":            "import json\n",
		"unclosed":        "# /// script\n# dependencies = [\"six\"]\nimport json\n",
		"two":             block + "\n" + block,
		"toml":            "# /// script\n# dependencies = [\"six\"\n# ///\n",
		"field":           "# /// script\n# dependencies = [\"six\"]\n# name = \"serve\"\n# ///\n",
		"empty":           "# /// script\n# dependencies = []\n# ///\n",
		"missing":         "# /// script\n# requires-python = \">=3.11\"\n# ///\n",
		"not strings":     "# /// script\n# dependencies = [\"six\", 1]\n# ///\n",
		"not a list":      "# /// script\n# dependencies = \"six\"\n# ///\n",
		"pep 508":         "# /// script\n# dependencies = [\"six >>= 1\"]\n# ///\n",
		"option":          "# /// script\n# dependencies = [\"--index-url https://example.invalid/simple\"]\n# ///\n",
		"python":          "# /// script\n# requires-python = \"<3\"\n# dependencies = [\"six\"]\n# ///\n",
		"python spec":     "# /// script\n# requires-python = \"3.11+\"\n# dependencies = [\"six\"]\n# ///\n",
		"python type":     "# /// script\n# requires-python = 3\n# dependencies = [\"six\"]\n# ///\n",
		"tool.uv":         "# /// script\n# dependencies = [\"six\"]\n# [tool.uv]\n# exclude-newer = \"2024-01-01T00:00:00Z\"\n# ///\n",
		"tool not table":  "# /// script\n# dependencies = [\"six\"]\n# tool = 1\n# ///\n",
		"undecodable":     "# /// script\n# dependencies = [\"six\"]\n# ///\n# \xff\n",
		"type other only": "# /// pyproject\n# dependencies = [\"six\"]\n# ///\n",
	} {
		if r := read(script); len(r.Errors) == 0 {
			t.Errorf("%s: accepted %#v", name, r)
		}
	}
}
