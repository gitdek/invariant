package statechart_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Unlike a runtime comparison on amd64, an arm64 cross-compilation can see
// implicit fusion on any host. Associate instructions with actual math.FMA
// call expressions, not comments or a hardcoded list of permitted lines.
func TestStatechartOnlyExplicitFMA792(t *testing.T) {
	type location struct {
		file string
		line int
	}
	explicit := map[location]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		mathName := ""
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "math" {
				mathName = "math"
				if imp.Name != nil {
					mathName = imp.Name.Name
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "FMA" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != mathName || pkg.Obj != nil {
				return true
			}
			for line := fset.Position(call.Pos()).Line; line <= fset.Position(call.End()).Line; line++ {
				explicit[location{name, line}] = true
			}
			return true
		})
	}
	// Go's arm64 assembler spells the double add FMADDD but the subtract
	// FMSUBD; include both signs and precisions so none slip past this check.
	instruction := regexp.MustCompile(`(?m)^\s*0x[0-9a-f]+\s+[0-9]+\s+\((.+\.go):([0-9]+)\)\s+(F(?:NMADD|NMSUB|MADD|MSUB)[DS])\s+`)
	type fused struct {
		where location
		op    string
	}
	compile := func(disabled bool) map[fused]int {
		t.Helper()
		flags := "-S"
		if disabled {
			flags += " -d=fmahash=n"
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, processor792Go(), "build", "-gcflags="+flags, ".")
		cmd.Env = processor792Env(map[string]string{
			"GOARCH": "arm64", "GOOS": "linux", "CGO_ENABLED": "0",
			"GOFLAGS": "-mod=readonly -p=1", "GOENV": "off", "GOWORK": "off",
		})
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compile production statechart for arm64 (disabled=%v): %v\n%s", disabled, err, out)
		}
		if !bytes.Contains(out, []byte("statechart")) || !bytes.Contains(out, []byte("TEXT")) {
			t.Fatal("compiler produced no statechart assembly")
		}
		counts := map[fused]int{}
		for _, m := range instruction.FindAllStringSubmatch(string(out), -1) {
			line, err := strconv.Atoi(m[2])
			if err != nil {
				t.Fatal(err)
			}
			where := location{filepath.Base(m[1]), line}
			if !explicit[where] {
				t.Errorf("arm64 %s at %s:%d has no explicit math.FMA call", m[3], where.file, where.line)
			}
			counts[fused{where, m[3]}]++
		}
		return counts
	}
	normal, disabled := compile(false), compile(true)
	if !reflect.DeepEqual(normal, disabled) {
		t.Errorf("implicit fusion changes the arm64 instruction inventory:\nnormal=%v\ndisabled=%v", normal, disabled)
	}
}
