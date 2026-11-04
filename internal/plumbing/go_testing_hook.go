package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"strconv"
	"strings"
)

const goTestingEventEnv = "INVARIANT_GO_TESTING_EVENTS"
const goTestingProtocol = "invariant-go-testing-v1\n"

// Read inside the prepared environment: neither GOROOT nor helper paths need
// to be paths on the host of a future transport. No candidate code is loaded.
const goTestingSourceReader = `package main
import ("encoding/json"; "os"; "path/filepath"; "runtime")
func main() {
 name := filepath.Join(runtime.GOROOT(), "src", "testing", "testing.go")
 source, err := os.ReadFile(name)
 if err != nil { panic(err) }
 if err := json.NewEncoder(os.Stdout).Encode(struct{Path, Version, Source string}{name, runtime.Version(), string(source)}); err != nil { panic(err) }
}
`

// Pin the declarations the hooks depend on, independently of unrelated fixes
// elsewhere in a Go patch release. Changed lifecycle code needs review; matching
// verbose output is never a fallback.
var goTestingDeclarations = map[string]string{
	"common":                 "sha256:e0b42bc2463dc4714ee17c6e30933dbfdf749bc921bfd5012c1e6644e3d3a9b5",
	"T":                      "sha256:6ee4ea6c0a896b52169ce9b7851b2572dc6fe020acbc5ff987e7f09ed9a598a3",
	"outputWriter":           "sha256:0df8a825e9c031f47f409ad93f2dbc53e873a6e5730b106d33a373b17e48cff4",
	"outputWriter.writeLine": "sha256:7a6c7258482d3c1caa58b799fa070f412761739cd4228aba3df9d49e2024238d",
	"T.Run":                  "sha256:066fae339dc637c8b875e2a8eec9121ab5cce99a0fa892aa68f3d4e462b97f8c",
	"T.Parallel":             "sha256:93796c42c1d98832a6f577f2230170933ebc932375417994eb76092e6e92e037",
	"T.report":               "sha256:d76393532f53314eca5b3758f73ccf63177938dc8db971e1eeceebbbd3171100",
	"tRunner":                "sha256:c3f62db1cf9d01636aad25d0258c36a57a4f959aa109f4ec8f196f5589133f55",
}

func prepareGoTesting(ctx context.Context, env RepositoryProcessTransport, in RepositoryAcceptanceInvocation) (string, error) {
	private := "go-producer-" + in.Binding.InvocationID
	reader, err := env.Stage(ctx, private+"/read.go", []byte(goTestingSourceReader))
	if err != nil {
		return "", err
	}
	out, err := env.Run(ctx, []string{"go", "run", reader})
	if err := errors.Join(err, ctx.Err()); err != nil {
		return "", fmt.Errorf("read prepared testing implementation: %w: %s", err, out)
	}
	var source struct{ Path, Version, Source string }
	if err := json.Unmarshal(out, &source); err != nil {
		return "", fmt.Errorf("invalid testing source response: %w", err)
	}
	if !strings.HasPrefix(source.Version, "go1.27.") || !path.IsAbs(source.Path) || !strings.HasSuffix(source.Path, "/src/testing/testing.go") {
		return "", errors.New("unsupported prepared Go testing toolchain")
	}
	modified, err := instrumentGoTesting(ctx, source.Source)
	if err != nil {
		return "", err
	}
	file, err := env.Stage(ctx, private+"/testing.go", modified)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{source.Path: file}})
	if err != nil {
		return "", err
	}
	overlay, err := env.Stage(ctx, private+"/overlay.json", data)
	if err != nil {
		return "", err
	}
	binary, err := env.Stage(ctx, private+"/acceptance.test", nil)
	if err != nil {
		return "", err
	}
	argv := append([]string{"go", "test", "-c", "-o", binary, "-overlay=" + overlay}, in.Argv[2:]...)
	out, err = env.Run(ctx, argv)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return "", fmt.Errorf("compile instrumented Go tests: %w: %s", err, out)
	}
	return binary, nil
}

func instrumentGoTesting(ctx context.Context, source string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateGoTesting(source); err != nil {
		return nil, err
	}
	for _, hook := range []struct{ before, after string }{
		{"func (o *outputWriter) writeLine(b []byte, errBegin, errEnd bool) {", "func (o *outputWriter) writeLine(b []byte, errBegin, errEnd bool) {\n\to.c.invariantEvent(\"output\", string(b))"},
		{"\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== RUN   %s\\n\", t.name)", "\tt.common.invariantEvent(\"start\", \"\")\n\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== RUN   %s\\n\", t.name)"},
		{"\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== PAUSE %s\\n\", t.name)", "\tt.common.invariantEvent(\"pause\", \"\")\n\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== PAUSE %s\\n\", t.name)"},
		{"\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== CONT  %s\\n\", t.name)", "\tt.common.invariantEvent(\"continue\", \"\")\n\tif t.chatty != nil {\n\t\tt.chatty.Updatef(t.name, \"=== CONT  %s\\n\", t.name)"},
		{"\tdstr := fmtDuration(t.duration)", "\taction := \"pass\"\n\tif t.Failed() { action = \"fail\" } else if t.Skipped() { action = \"skip\" }\n\tt.common.invariantEvent(action, \"\")\n\tdstr := fmtDuration(t.duration)"},
		{"\t\t\t\troot.flushToParent(root.name, \"--- FAIL: %s (%s)\\n\", root.name, fmtDuration(d))", "\t\t\t\troot.invariantEvent(\"fail\", fmt.Sprint(err))\n\t\t\t\troot.flushToParent(root.name, \"--- FAIL: %s (%s)\\n\", root.name, fmtDuration(d))"},
		{"\t\t\t\t\tfmt.Fprintf(root.parent.w, \"cleanup panicked with %v\", r)", "\t\t\t\t\troot.parent.invariantEvent(\"output\", fmt.Sprintf(\"cleanup panicked with %v\", r))\n\t\t\t\t\tfmt.Fprintf(root.parent.w, \"cleanup panicked with %v\", r)"},
	} {
		if strings.Count(source, hook.before) != 1 {
			return nil, errors.New("Go testing lifecycle hook is not unique")
		}
		source = strings.Replace(source, hook.before, hook.after, 1)
	}
	source += goTestingHook
	if _, err := parser.ParseFile(token.NewFileSet(), "testing.go", source, parser.AllErrors); err != nil {
		return nil, fmt.Errorf("invalid host testing instrumentation: %w", err)
	}
	return []byte(source), ctx.Err()
}

func validateGoTesting(source string) error {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "testing.go", source, 0)
	if err != nil {
		return err
	}
	if file.Name.Name != "testing" {
		return errors.New("prepared testing source has a different package")
	}
	seen := map[string]bool{}
	check := func(name string, node ast.Node) error {
		want, ok := goTestingDeclarations[name]
		if !ok {
			return nil
		}
		text := source[positions.Position(node.Pos()).Offset:positions.Position(node.End()).Offset]
		digest := dependencyHash([]byte(text))
		// Go 1.27.1 escapes diagnostic framing even outside JSON mode.
		// The hook observes the original bytes before either reviewed variant.
		previous := name == "outputWriter.writeLine" && digest == "sha256:686f8f878585defc25237619392bd913c272fdc641e17a04924bf3fca582114d"
		if seen[name] || digest != want && !previous {
			return fmt.Errorf("unreviewed Go testing declaration %s:\n%s", name, text)
		}
		seen[name] = true
		return nil
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			name := decl.Name.Name
			if decl.Recv != nil && len(decl.Recv.List) == 1 {
				if ptr, ok := decl.Recv.List[0].Type.(*ast.StarExpr); ok {
					if typ, ok := ptr.X.(*ast.Ident); ok {
						name = typ.Name + "." + name
					}
				}
			}
			if err := check(name, decl); err != nil {
				return err
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				if typ, ok := spec.(*ast.TypeSpec); ok {
					if err := check(typ.Name.Name, decl); err != nil {
						return err
					}
				}
			}
		}
	}
	if len(seen) != len(goTestingDeclarations) {
		return errors.New("prepared Go testing source is missing lifecycle declarations")
	}
	return nil
}

// Four length-prefixed strings per record keep arbitrary log bytes intact.
// Only this testing hook writes the protocol; diagnostic framing is irrelevant.
// The endpoint is captured during testing initialization, before candidate init.
// Consume the handoff so re-executed test helpers run without producing events.
const goTestingHook = `
var invariantEventMu sync.Mutex
var invariantEventFile = func() *os.File {
 name := os.Getenv("INVARIANT_GO_TESTING_EVENTS")
 if name == "" { return nil }
 if err := os.Unsetenv("INVARIANT_GO_TESTING_EVENTS"); err != nil { panic(err) }
 f, err := os.OpenFile(name, os.O_WRONLY, 0)
 if err != nil { panic(err) }
 // OpenFile gives us a close-on-exec descriptor. Release the transport's
 // inherited descriptor too, before any test initialization can start helpers.
 if value, ok := strings.CutPrefix(name, "/dev/fd/"); ok {
  fd, err := strconv.Atoi(value)
  if err != nil || fd < 3 { panic("invalid host Go testing event descriptor") }
  if err := os.NewFile(uintptr(fd), name).Close(); err != nil { panic(err) }
 }
 if _, err := io.WriteString(f, "invariant-go-testing-v1\n"); err != nil { panic(err) }
 return f
}()

func (c *common) invariantEvent(action, text string) {
 if invariantEventFile == nil { return }
 for c.isSynctest && c.parent != nil { c = c.parent }
 if c.name == "" || c.bench { return }
 parent := ""
 if c.parent != nil { parent = c.parent.name }
 invariantEventMu.Lock()
 defer invariantEventMu.Unlock()
 record := ""
 for _, value := range []string{c.name, parent, action, text} {
  record += strconv.Itoa(len(value)) + ":" + value
 }
 if _, err := io.WriteString(invariantEventFile, record); err != nil { panic(err) }
}
`

func decodeGoTesting(data []byte) ([]GoFrameworkEvent, error) {
	if len(data) == 0 {
		return nil, nil // Initialization can exit before testing initializes.
	}
	if !bytes.HasPrefix(data, []byte(goTestingProtocol)) {
		return nil, errors.New("invalid Go testing event protocol")
	}
	data = data[len(goTestingProtocol):]
	var events []GoFrameworkEvent
	for len(data) != 0 {
		var values [4]string
		for i := range values {
			n := bytes.IndexByte(data, ':')
			if n <= 0 || n > 20 {
				return events, errors.New("truncated Go testing event length")
			}
			size, err := strconv.Atoi(string(data[:n]))
			if err != nil || size < 0 || size > len(data)-n-1 {
				return events, io.ErrUnexpectedEOF
			}
			data = data[n+1:]
			values[i], data = string(data[:size]), data[size:]
		}
		event := GoFrameworkEvent{Name: values[0], Parent: values[1], Action: values[2], Text: values[3]}
		switch event.Action {
		case "start", "pause", "continue", "output", "pass", "fail", "skip":
		default:
			return events, errors.New("unknown Go testing lifecycle action")
		}
		if event.Name == "" {
			return events, errors.New("unnamed Go testing event")
		}
		events = append(events, event)
	}
	return events, nil
}
