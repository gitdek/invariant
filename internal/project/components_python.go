package project

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed component_parser.py
var componentPythonParser string

// PythonDeclarations reads lexical bindings without creating a production
// inventory. The source is relative to the already resolved application root.
func PythonDeclarations(root, source string) ([]Component, error) {
	name, err := componentPath(root, source, false)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	out, err := componentPython(source, componentInput{bytes: b, file: ComponentFile{Path: source, SHA256: componentHash(b)}})
	return out.components, err
}

// PythonImports resolves direct lexical imports to contained application
// sources. This binds an existing instance wrapper to the source it delegates
// to; execution must still observe that source under the selected call.
func PythonImports(root, source string) ([]string, error) {
	name, err := componentPath(root, source, false)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", `import ast,json,sys
tree=ast.parse(sys.stdin.buffer.read())
rows=[]
for node in ast.walk(tree):
    if isinstance(node,ast.Import):
        rows.extend([0,a.name] for a in node.names)
    elif isinstance(node,ast.ImportFrom):
        module=node.module or ''
        rows.append([node.level,module])
        rows.extend([node.level,('.'.join(filter(None,[module,a.name])))] for a in node.names if a.name!='*')
print(json.dumps(rows))`)
	cmd.Stdin = bytes.NewReader(b)
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("isolated Python import parser: %w", err)
	}
	var rows [][2]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		var level int
		var module string
		if err := json.Unmarshal(row[0], &level); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(row[1], &module); err != nil {
			return nil, err
		}
		base := "."
		if level > 0 {
			base = path.Dir(source)
			for i := 1; i < level; i++ {
				base = path.Dir(base)
			}
		}
		module = path.Join(base, strings.ReplaceAll(module, ".", "/"))
		for _, candidate := range []string{module + ".py", path.Join(module, "__init__.py")} {
			if _, err := componentPath(root, candidate, false); err == nil {
				seen[filepath.ToSlash(candidate)] = true
			}
		}
	}
	var out []string
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func componentPython(source string, input componentInput) (componentSource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Isolated mode ignores PYTHON* and excludes the project from sys.path.
	// -S also excludes site initialization, including installed startup hooks.
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", componentPythonParser)
	cmd.Stdin = bytes.NewReader(input.bytes)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return componentSource{}, fmt.Errorf("isolated Python 3 syntax parser: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var declarations []struct {
		Symbol    string   `json:"symbol"`
		Contracts [][2]int `json:"contracts"`
	}
	if err := json.Unmarshal(b, &declarations); err != nil {
		return componentSource{}, fmt.Errorf("Python syntax parser output: %w", err)
	}
	pkg := strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".")
	out := componentSource{file: input.file}
	for _, d := range declarations {
		var contract []byte
		for _, span := range d.Contracts {
			if span[0] < 0 || span[1] <= span[0] || span[1] > len(input.bytes) {
				return componentSource{}, fmt.Errorf("Python syntax parser: invalid contract span for %s", d.Symbol)
			}
			contract = append(contract, input.bytes[span[0]:span[1]]...)
		}
		hash := ""
		if len(contract) != 0 {
			hash = componentHash(contract)
		}
		out.components = append(out.components, Component{
			ID:       "python:" + pkg + "#" + d.Symbol + "@" + source,
			Language: "python", Package: pkg, Symbol: d.Symbol, Source: source,
			SourceSHA256: input.file.SHA256, ContractSHA256: hash,
		})
	}
	return out, nil
}
