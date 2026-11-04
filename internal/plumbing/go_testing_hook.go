package plumbing

// This host-owned helper is compiled and run inside the prepared environment.
// Only the pinned standard library testing implementation is overlaid. Neither
// candidate tests nor production/dependency sources are rewritten. Updating the
// source pin requires reviewing the hooks against that testing implementation.
const goTestingHelper = `package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}

func run() error {
	if len(os.Args) >= 4 && os.Args[1] == "execute" {
		if err := os.Chdir(os.Args[2]); err != nil { return err }
		return syscall.Exec(os.Args[3], os.Args[3:], os.Environ())
	}
	if len(os.Args) != 3 || os.Args[1] != "instrument" { return fmt.Errorf("invalid testing helper invocation") }
	source := filepath.Join(runtime.GOROOT(), "src", "testing", "testing.go")
	data, err := os.ReadFile(source)
	if err != nil { return err }
	switch hash := fmt.Sprintf("%x", sha256.Sum256(data)); hash {
	case "8d2437cfdcfb82fd8807e5063783de1d50f70e589e80cad10e17d8ef23f724d9", // Go 1.27.1
		"27c59df1832eae458ab8d2d4b6a6252bbd1ea2a3001734910d1ce2decff16f81": // Go 1.27.3
	default:
		return fmt.Errorf("unsupported pinned testing source (%s, sha256:%s)", runtime.Version(), hash)
	}
	text := string(data)
	for _, edit := range [][2]string{
		{"\tgo tRunner(t, f)", "\tinvariantEvent(&t.common, \"start\", \"\")\n\tgo tRunner(t, f)"},
		{"\t\tt.chatty.Updatef(t.name, \"=== PAUSE %s\\n\", t.name)\n\t}", "\t\tt.chatty.Updatef(t.name, \"=== PAUSE %s\\n\", t.name)\n\t}\n\tinvariantEvent(&t.common, \"pause\", \"\")"},
		{"\t\tt.chatty.Updatef(t.name, \"=== CONT  %s\\n\", t.name)\n\t}", "\t\tt.chatty.Updatef(t.name, \"=== CONT  %s\\n\", t.name)\n\t}\n\tinvariantEvent(&t.common, \"continue\", \"\")"},
		{"func (o *outputWriter) writeLine(b []byte, errBegin, errEnd bool) {", "func (o *outputWriter) writeLine(b []byte, errBegin, errEnd bool) {\n\tinvariantEvent(o.c, \"output\", string(b))"},
		{"\tdstr := fmtDuration(t.duration)", "\taction := \"pass\"\n\tif t.Failed() { action = \"fail\" } else if t.Skipped() { action = \"skip\" }\n\tinvariantEvent(&t.common, action, \"\")\n\tdstr := fmtDuration(t.duration)"},
		{"\t\tdoPanic := func(err any) {", "\t\tdoPanic := func(err any) {\n\t\tinvariantEvent(&t.common, \"output\", fmt.Sprint(err))"},
		{"\t\t\t\troot.flushToParent(root.name, \"--- FAIL: %s (%s)\\n\", root.name, fmtDuration(d))", "\t\t\t\troot.flushToParent(root.name, \"--- FAIL: %s (%s)\\n\", root.name, fmtDuration(d))\n\t\t\t\tinvariantEvent(root, \"fail\", \"\")"},
	} {
		if strings.Count(text, edit[0]) != 1 { return fmt.Errorf("testing hook anchor changed: %q", edit[0]) }
		text = strings.Replace(text, edit[0], edit[1], 1)
	}
	text += hook
	instrumented, err := format.Source([]byte(text))
	if err != nil { return err }
	filename := os.Args[2] + ".go"
	if err := os.WriteFile(filename, instrumented, 0600); err != nil { return err }
	overlay, err := json.Marshal(struct { Replace map[string]string }{map[string]string{source:filename}})
	if err != nil { return err }
	return os.WriteFile(os.Args[2], overlay, 0600)
}

const hook = ` + "`" + `

var invariantEventMu sync.Mutex
var invariantEventFile = func() *os.File {
	endpoint := os.Getenv("INVARIANT_GO_EVENTS")
	if endpoint == "" { return nil }
	f, err := os.OpenFile(endpoint, os.O_WRONLY, 0)
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(125) }
	if _, err := f.WriteString("invariant-go-events-v1\n"); err != nil { os.Exit(125) }
	return f
}()

func invariantEvent(c *common, action, text string) {
	// Synctest bubbles are not subtests. Logs belong to the enclosing real T.
	for c != nil && c.isSynctest { c = c.parent }
	if invariantEventFile == nil || c == nil || c.name == "" || c.bench { return }
	parent := c.parent
	for parent != nil && parent.isSynctest { parent = parent.parent }
	parentName := ""
	if parent != nil { parentName = parent.name }
	invariantEventMu.Lock()
	defer invariantEventMu.Unlock()
	if _, err := fmt.Fprintf(invariantEventFile, "%x\t%x\t%x\t%x\n", c.name, parentName, action, text); err != nil { os.Exit(125) }
}
` + "`" + `
`
