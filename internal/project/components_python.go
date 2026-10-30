package project

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

//go:embed component_parser.py
var componentPythonParser string

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
