package verify

// #569 exercises the normal Run path, not a manually substituted Language or
// Reader. Only external commands are substituted. Fixture generation happens
// in the parent; the measured process never holds its input as a byte slice.
// At GOMAXPROCS=1/GOGC=100 and TLCParallel=1, both 256 and 512 MiB payloads
// must fit the same 192 MiB heap ceiling. This is a workload ceiling, not a
// promise about unbounded individual values, index counts, or Python receipts.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

const gate569Ceiling = 192 << 20
const gate569Padding = 8192

func init() {
	if os.Getenv("INVARIANT_569_DOCKER") == "1" {
		os.Exit(gate569Docker(os.Args[1:]))
	}
}

func gate569Write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func gate569Record(t *testing.T, path, kind string, n, padSize int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 64<<10)
	pad := strings.Repeat("p", padSize)
	state := func(i int) { fmt.Fprintf(w, `{"n":%d,"pad":"%s"}`, i, pad) }
	if kind == "traces" {
		fmt.Fprint(w, `{"traces":[[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			state(i)
		}
		fmt.Fprint(w, `]]}`)
	} else {
		// Forward references are the format emitted by the Go Try harness.
		fmt.Fprint(w, `{"attempts":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			if kind == "refused" {
				fmt.Fprintf(w, `[0,"Denied",[%d],0,{"outcome":"refused","refusal":{"type":"fixture.Refused","attributes":{"detail":"%s"}}}]`, i, pad)
			} else {
				fmt.Fprintf(w, `[%d,"Inc",[],%d]`, i, min(i+1, n-1))
			}
		}
		if kind == "refused" {
			// One accepted read-only call keeps Next enabled in the lone state;
			// all n other argument values are declared refusals.
			fmt.Fprintf(w, `,[0,"Denied",[%d],0,{"outcome":"accepted"}]`, n)
		}
		fmt.Fprint(w, `],"init":[0],"states":[`)
		if kind == "refused" {
			state(0)
		} else {
			for i := 0; i < n; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				state(i)
			}
		}
		fmt.Fprint(w, `]}`)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func gate569Fixture(t *testing.T, root, route, kind string, n int, bad bool) *project.Project {
	t.Helper()
	p := &project.Project{Dir: filepath.Join(root, "project"), Manifest: project.Manifest{Name: "stream gate fixture", Language: route, Code: "src", Module: ".invariant/specs/Counter.tla"}, Lock: project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": strconv.Itoa(n - 1)}}}
	action := fmt.Sprintf("Inc == IF n < %d THEN /\\ n' = n+1\n                              /\\ UNCHANGED pad\n                       ELSE UNCHANGED vars\nNext == Inc\n", n-1)
	if bad {
		action = strings.Replace(action, "n' = n+1", fmt.Sprintf("n' = IF n = %d THEN n+2 ELSE n+1", n-2), 1)
	}
	if kind == "refused" {
		action = fmt.Sprintf("Denied(a) == a = %d /\\ UNCHANGED vars\nNext == \\E a \\in 0..%d : Denied(a)\n", n, n)
	}
	padSize := 8
	if n >= 32768 {
		padSize = gate569Padding
	}
	model := "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLES n, pad\nvars == <<n,pad>>\nInit == n = 0 /\\ pad = " + strconv.Quote(strings.Repeat("p", padSize)) + "\n" + action + "Spec == Init /\\ [][Next]_vars\nSafe == n >= 0\n====\n"
	for _, statement := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}} {
		statement.Says = "fixture obligation"
		statement.SHA256, _ = tla.PinHash(model, statement.Name, project.Model)
		p.Lock.Statements = append(p.Lock.Statements, statement)
	}
	gate569Write(t, p.ModulePath(), model)
	switch route {
	case "go", "agreement":
		p.Manifest.Language = "go"
		code := "package counter\ntype State int\nfunc Init() State { return 0 }\nfunc Successors(s State) []State { return nil }\n"
		if route == "go" {
			code += "func Try(s State, tried func(string, []any, State)) {}\nfunc Abstract(s State) map[string]any { return nil }\n"
		}
		gate569Write(t, filepath.Join(p.Dir, "go.mod"), "module fixture\n\ngo 1.27.1\n")
		gate569Write(t, filepath.Join(p.Dir, "src/counter.go"), code)
	case "python":
		p.Manifest.Conformance = "driver.py"
		gate569Write(t, filepath.Join(p.Dir, "driver.py"), "# Command-boundary fixture.\n")
		gate569Write(t, filepath.Join(p.Dir, "src/counter.py"), "def increment(n): return n+1\n")
	default:
		p.Manifest.Language = "typescript"
		p.Manifest.Conformance = "driver.ts"
		driver := "// Command-boundary fixture.\n"
		if route == "existing" {
			p.Manifest.Existing = []string{"counter.ts"}
			driver = "import { increment } from '../counter.ts';\n"
			gate569Write(t, filepath.Join(root, "counter.ts"), "export const increment = (n:number) => n+1;\n")
			gate569Write(t, filepath.Join(root, "package.json"), `{"name":"fixture","version":"1.0.0"}`)
			gate569Write(t, filepath.Join(root, "package-lock.json"), `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"packages":{}}`)
		}
		gate569Write(t, filepath.Join(p.Dir, "driver.ts"), driver)
		gate569Write(t, filepath.Join(p.Dir, "src/counter.ts"), "export const increment = (n:number) => n+1;\n")
	}
	if kind == "refused" {
		p.Manifest.Exhaustive = true
	}
	m, _ := json.Marshal(p.Manifest)
	l, _ := json.Marshal(p.Lock)
	gate569Write(t, filepath.Join(p.Dir, ".invariant/invariant.json"), string(m))
	gate569Write(t, filepath.Join(p.Dir, ".invariant/ratified.lock"), string(l))
	return p
}

func gate569Setup(t *testing.T, route, mode, input string, n int, kind string, bad bool) (*project.Project, toolchain.Toolchain, string, string) {
	t.Helper()
	root := t.TempDir()
	events := filepath.Join(root, "events")
	tmp := filepath.Join(root, "tmp")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{events, tmp, bin} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gate569Write(t, filepath.Join(bin, "docker"), "#!/bin/sh\nINVARIANT_569_DOCKER=1 exec \"$INVARIANT_569_BINARY\" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0700); err != nil {
		t.Fatal(err)
	}
	p := gate569Fixture(t, root, route, kind, n, bad)
	for k, v := range map[string]string{"INVARIANT_569_BINARY": exe, "GATE569_EVENTS": events, "GATE569_INPUT": input, "GATE569_ROUTE": route, "GATE569_MODE": mode, "GATE569_KIND": kind, "GATE569_N": strconv.Itoa(n), "GATE569_PROJECT": p.Dir, "GATE569_BAD": strconv.FormatBool(bad), "TMPDIR": tmp, "PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH")} {
		t.Setenv(k, v)
	}
	return p, toolchain.Toolchain{GoImage: "fixture-go", GobraImage: "fixture-gobra", NodeImage: "fixture-node", PythonImage: "fixture-python", JavaImage: "fixture-java", TLCJar: filepath.Join(root, "fixture.jar")}, events, tmp
}

func gate569Event(name, body string) {
	if err := os.WriteFile(filepath.Join(os.Getenv("GATE569_EVENTS"), name), []byte(body), 0600); err != nil {
		panic(err)
	}
}
func gate569Frame(code int, body string) {
	fmt.Printf("@!@!@STARTMSG %d:0 @!@!@\n%s\n@!@!@ENDMSG %d @!@!@\n", code, body, code)
}
func gate569Pass(n int) int {
	gate569Frame(2262, "TLC fixture version")
	gate569Frame(2193, "Model checking completed. No error has been found.")
	gate569Frame(2199, fmt.Sprintf("%d states generated, %d distinct states found, 0 states left on queue.", n, n))
	gate569Frame(2194, fmt.Sprintf("The depth of the complete state graph search is %d.", n))
	return 0
}
func gate569Await(name string) {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(filepath.Join(os.Getenv("GATE569_EVENTS"), name)); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	panic("fixture handshake timed out: " + name)
}

// A producer may transfer ownership by retaining its workspace or moving the
// output. Locate the still-readable recording without prescribing that choice.
// The checker's own validation spool is deliberately excluded.
func gate569FindRecording() string {
	input, err := os.Open(os.Getenv("GATE569_INPUT"))
	if err != nil {
		return ""
	}
	defer input.Close()
	want, err := input.Stat()
	if err != nil {
		return ""
	}
	size := min(int64(512), want.Size())
	head, tail := make([]byte, size), make([]byte, size)
	if _, err := input.ReadAt(head, 0); err != nil {
		return ""
	}
	if _, err := input.ReadAt(tail, want.Size()-size); err != nil {
		return ""
	}
	found := ""
	filepath.WalkDir(os.Getenv("TMPDIR"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), "invariant-conformance-") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() != want.Size() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		first, last := make([]byte, size), make([]byte, size)
		_, e1 := file.ReadAt(first, 0)
		_, e2 := file.ReadAt(last, want.Size()-size)
		if e1 == nil && e2 == nil && bytes.Equal(first, head) && bytes.Equal(last, tail) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func gate569Docker(args []string) int {
	if len(args) == 0 {
		return 90
	}
	switch args[0] {
	case "info":
		fmt.Println(`{"NCPU":1,"MemTotal":8589934592}`)
		return 0
	case "image", "inspect", "pull":
		fmt.Println("linux/amd64")
		return 0
	case "rm":
		gate569Event("removed-container", "yes")
		return 0
	case "run":
	default:
		fmt.Fprintln(os.Stderr, "unexpected Docker call", args)
		return 91
	}
	mounts := map[string]string{}
	env := map[string]string{}
	has := func(s string) bool {
		for _, a := range args {
			if a == s {
				return true
			}
		}
		return false
	}
	for i, a := range args[:len(args)-1] {
		if a == "-v" {
			p := strings.Split(args[i+1], ":")
			if len(p) >= 2 {
				mounts[p[1]] = p[0]
			}
		}
		if a == "-e" || a == "--env" {
			k, v, _ := strings.Cut(args[i+1], "=")
			env[k] = v
		}
	}
	n, _ := strconv.Atoi(os.Getenv("GATE569_N"))
	route := os.Getenv("GATE569_ROUTE")
	mode := os.Getenv("GATE569_MODE")
	kind := os.Getenv("GATE569_KIND")
	states := n
	if kind == "refused" {
		states = 1
	}
	if has("fixture-gobra") {
		if mode == "unrelated-error" {
			gate569Await("produced")
			if err := os.Remove(filepath.Join(os.Getenv("GATE569_PROJECT"), "src/counter.go")); err != nil {
				panic(err)
			}
		}
		fmt.Println("Gobra found 0 errors")
		return 0
	}
	if !has("tlc2.TLC") {
		out := mounts["/out"]
		if out == "" || mounts["/src"] == "" || !has("none") || !has("2g") || !has("512") || env["HOME"] != "/tmp" {
			panic("lost sandbox boundary")
		}
		if route != "agreement" && env["INVARIANT_TRACES"] != "/out/traces.json" {
			panic("changed recording environment")
		}
		script := args[len(args)-1]
		if !strings.Contains(script, "exec 2>&1") {
			panic("lost ordered sandbox output")
		}
		if route == "go" || route == "agreement" {
			if env["GOTOOLCHAIN"] != "local" || env["GOFLAGS"] != "-mod=readonly" || env["CGO_ENABLED"] != "0" || mounts["/agree"] == "" || !strings.Contains(script, "-run '^TestInvariantAgreement$'") {
				panic("changed Go harness command")
			}
		} else {
			if env["INVARIANT_RUNS"] != "1000" || env["INVARIANT_STEPS"] != "40" || env["INVARIANT_SEED"] != "1" || env["DRIVER"] == "" {
				panic("changed driver controls")
			}
			if route == "python" && (!has("linux/amd64") || env["PYTHONHASHSEED"] != "0" || mounts["/runtime"] == "" || !strings.Contains(script, "/usr/local/bin/python -S -s -P /runtime/invariant_python.py")) {
				panic("changed Python startup")
			}
		}
		output := filepath.Join(out, "traces.json")
		if route != "agreement" && mode != "missing" {
			dst, err := os.Create(output)
			if err != nil {
				panic(err)
			}
			switch mode {
			case "empty":
			case "malformed":
				fmt.Fprint(dst, `{"traces":[`)
			default:
				src, err := os.Open(os.Getenv("GATE569_INPUT"))
				if err != nil {
					panic(err)
				}
				_, err = io.Copy(dst, src)
				src.Close()
				if err != nil {
					panic(err)
				}
			}
			if err := dst.Close(); err != nil {
				panic(err)
			}
		}
		gate569Event("producer-path", output)
		gate569Event("produced", "yes")
		if mode == "cancel-producer" {
			gate569Event("cancel-ready", "yes")
			time.Sleep(time.Minute)
		}
		build, driver := 0, 0
		if mode == "build-failed" {
			build = 1
		}
		if mode == "driver-failed" {
			driver = 1
		}
		switch route {
		case "go", "agreement":
			fmt.Printf("@@invariant vet=%d\n@@invariant test=0\ninvariant-agreement states=%d depth=%d\n", build, states, states)
			if driver != 0 {
				fmt.Println("driver exploded")
			}
			fmt.Printf("@@invariant agree=%d\n", driver)
		case "python":
			fmt.Printf("@@invariant compile=%d\n@@invariant test=0\n", build)
			fmt.Println("driver exploded")
			fmt.Printf("@@invariant conform=%d\n", driver)
		case "existing":
			fmt.Println("driver exploded")
			fmt.Printf("@@invariant conform=%d\n", driver)
		default:
			fmt.Printf("@@invariant test=%d\n", build)
			fmt.Println("driver exploded")
			fmt.Printf("@@invariant conform=%d\n", driver)
		}
		return 0
	}
	module := strings.TrimSuffix(filepath.Base(args[len(args)-1]), ".tla")
	work := mounts["/work"]
	if module == "Counter" {
		if mode == "cancel-wait" {
			gate569Await("produced")
			gate569Event("cancel-ready", "yes")
			time.Sleep(time.Minute)
		}
		if mode == "stage-error" {
			gate569Await("produced")
			dir := filepath.Join(os.Getenv("GATE569_PROJECT"), ".invariant/specs")
			if err := os.RemoveAll(dir); err != nil {
				panic(err)
			}
			if err := os.WriteFile(dir, []byte("injected staging failure"), 0600); err != nil {
				panic(err)
			}
		}
		return gate569Pass(states)
	}
	if strings.HasPrefix(module, "Invariant_") {
		// The owned output remains openable at consumption; no discarded
		// producer workspace can be hidden by a successful byte-based check.
		if path := gate569FindRecording(); path == "" {
			gate569Event("recording-missing", "no owned recording remains readable")
		} else {
			gate569Event("recording-live", path)
		}
		if mode == "cancel-check" {
			gate569Event("cancel-ready", "yes")
			time.Sleep(time.Minute)
		}
	}
	body, err := os.ReadFile(filepath.Join(work, module+".tla"))
	if err != nil {
		panic(err)
	}
	text := string(body)
	values := regexp.MustCompile(`\[n \|-> ([0-9]+), pad \|-> "(p*)"\]`).FindAllStringSubmatch(text, -1)
	padSize := 8
	if n >= 32768 {
		padSize = gate569Padding
	}
	for _, value := range values {
		if len(value[2]) != padSize {
			panic("recorded state payload changed")
		}
	}
	at := func(i int) int { v, _ := strconv.Atoi(values[i][1]); return v }
	count := 0
	switch module {
	case "Invariant_ConformanceInit":
		count = len(values)
		if count != 1 || at(0) != 0 {
			panic("wrong initial states")
		}
	case "Invariant_Conformance":
		_, pairs, _ := strings.Cut(text, "Invariant_Steps == {")
		pairs, _, _ = strings.Cut(pairs, "}\n")
		found := regexp.MustCompile(`<<([0-9]+), ([0-9]+)>>`).FindAllStringSubmatch(pairs, -1)
		count = len(found)
		if count == 0 || count > 2000 {
			panic("wrong Next batch size")
		}
		for _, pair := range found {
			from, _ := strconv.Atoi(pair[1])
			to, _ := strconv.Atoi(pair[2])
			from--
			to--
			if from < 0 || to < 0 || from >= len(values) || to >= len(values) || at(to) != at(from)+1 {
				panic("wrong transition transport")
			}
			if os.Getenv("GATE569_BAD") == "true" && at(from) == n-2 {
				gate569Event(module+"-"+filepath.Base(work), strconv.Itoa(count))
				gate569Event("late-bad-seen", strconv.Itoa(at(from)))
				gate569Frame(2114, "Deadlock reached.")
				gate569Frame(2217, fmt.Sprintf("1: <Initial predicate>\n/\\ n = %d\n/\\ pad = \"%s\"\n/\\ invariant_source = %d\n/\\ invariant_target = %d", at(from), values[from][2], from+1, to+1))
				return 11
			}
		}
	case "Invariant_StrictAttempts":
		count = strings.Count(text, `, "Denied", <<`)
		if kind != "refused" || count == 0 || count > 2000 || strings.Count(text, ">>, TRUE>>")+strings.Count(text, ">>, FALSE>>") != count {
			panic("declared refusal metadata lost")
		}
		for _, row := range regexp.MustCompile(`, "Denied", <<([0-9]+)>>, (TRUE|FALSE)>>`).FindAllStringSubmatch(text, -1) {
			argument, _ := strconv.Atoi(row[1])
			if argument > n || (argument < n) != (row[2] == "TRUE") {
				panic("declared outcome disagrees with the fixture action")
			}
		}
	case "Invariant_Tried":
		count = len(values)
		if kind == "refused" {
			if count != 1 {
				panic("refusal state count")
			}
			for i := 0; i <= n; i++ {
				if !strings.Contains(text, fmt.Sprintf(`<<"Denied", %d>>`, i)) {
					panic("refused argument lost")
				}
			}
		} else if count == 0 || count > 1000 || strings.Count(text, `<<"Inc">>`) < count {
			panic("tried coverage lost")
		}
	default:
		panic("unexpected model module " + module)
	}
	gate569Event(module+"-"+filepath.Base(work), strconv.Itoa(count))
	return gate569Pass(states)
}

func gate569Clean(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("gate left owned temporary storage: %v", names)
	}
}

func gate569Coverage(t *testing.T, events, route, kind string, n int, bad bool) {
	t.Helper()
	want := map[string]int{"Invariant_ConformanceInit": 1, "Invariant_Conformance": n - 1}
	if kind == "refused" {
		want["Invariant_Conformance"] = 0
		want["Invariant_StrictAttempts"] = n + 1
		want["Invariant_Tried"] = 1
	} else if route == "go" && !bad {
		want["Invariant_Tried"] = n
	}
	entries, err := os.ReadDir(events)
	if err != nil {
		t.Fatal(err)
	}
	for module, expected := range want {
		got := 0
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), module+"-") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(events, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			count, err := strconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			got += count
		}
		if got != expected {
			t.Errorf("%s checked %d entries, want %d", module, got, expected)
		}
	}
}

func gate569Result(t *testing.T, r *Report, n int, kind, route string, bad bool) {
	t.Helper()
	if r == nil || !r.Design.Passed || !r.Build.Passed || r.Passed == bad {
		t.Fatalf("wrong gate verdict: %+v", r)
	}
	c := r.Conformance
	steps, states, runs, transitions := n-1, n, 1, n-1
	if kind == "attempts" {
		steps, runs = n, 0
	}
	if kind == "refused" {
		steps, states, runs, transitions = n+1, 1, 0, 0
	}
	if c == nil || c.Passed == bad || c.Steps != steps || c.States != states || c.Runs != runs || c.Transitions != transitions || c.ModelStates != int64(states) {
		t.Fatalf("wrong conformance counts/verdict: %+v", c)
	}
	if route == "go" {
		if r.Agreement == nil || !r.Agreement.Passed || r.Agreement.States != int64(states) || r.Agreement.Depth != states {
			t.Fatalf("agreement: %+v", r.Agreement)
		}
	}
	exhaustive := route == "go" || kind == "refused"
	if c.Exhaustive != exhaustive {
		t.Fatalf("exhaustive=%v", c.Exhaustive)
	}
	if bad {
		if c.BadStep == nil || !strings.Contains(c.BadStep.From, fmt.Sprintf("n = %d,", n-2)) || !strings.Contains(c.BadStep.To, fmt.Sprintf("n = %d,", n-1)) || c.Message != "the code took a step the model doesn't allow" {
			t.Fatalf("late diagnostic: %+v", c)
		}
	} else if exhaustive && (c.Tried == nil || !c.Tried.Passed || c.Tried.Attempts != steps || c.Tried.States != states) {
		t.Fatalf("attempt verdict: %+v", c.Tried)
	}
}

func gate569Memory(t *testing.T, refused bool) {
	t.Helper()
	if os.Getenv("INVARIANT_569_MEASURE") == "1" {
		n, _ := strconv.Atoi(os.Getenv("INVARIANT_569_COUNT"))
		route := os.Getenv("INVARIANT_569_ROUTE")
		bad := os.Getenv("INVARIANT_569_BAD") == "true"
		kind := "traces"
		if route == "go" {
			kind = "attempts"
		}
		if refused {
			kind = "refused"
		}
		runtime.GOMAXPROCS(1)
		debug.SetGCPercent(100)
		debug.SetMemoryLimit(1<<63 - 1)
		p, tc, events, tmp := gate569Setup(t, route, "ok", os.Getenv("INVARIANT_569_INPUT"), n, kind, bad)
		fdCount := func() int64 {
			for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
				if entries, err := os.ReadDir(dir); err == nil {
					return int64(len(entries))
				}
			}
			return -1
		}
		fdBase := fdCount()
		var fdPeak atomic.Int64
		var ticks atomic.Uint64
		var peak atomic.Uint64
		sample := func() {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			for old := peak.Load(); m.HeapAlloc > old; old = peak.Load() {
				if peak.CompareAndSwap(old, m.HeapAlloc) {
					break
				}
			}
			if ticks.Add(1)%50 == 0 {
				n := fdCount()
				for old := fdPeak.Load(); n > old; old = fdPeak.Load() {
					if fdPeak.CompareAndSwap(old, n) {
						break
					}
				}
			}
		}
		done, joined := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(joined)
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					sample()
				}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		r, err := Run(ctx, p.Dir, "", tc, Options{TLCParallel: 1})
		sample()
		close(done)
		<-joined
		if err != nil {
			t.Fatal(err)
		}
		gate569Result(t, r, n, kind, route, bad)
		gate569Coverage(t, events, route, kind, n, bad)
		gate569Clean(t, tmp)
		if bad {
			if _, err := os.Stat(filepath.Join(events, "late-bad-seen")); err != nil {
				t.Fatal("recording tail was not checked")
			}
		}
		if got := peak.Load(); got > gate569Ceiling {
			t.Errorf("%s/%s n=%d peak=%d MiB exceeds fixed %d MiB ceiling", route, kind, n, got>>20, gate569Ceiling>>20)
		}
		if fdBase >= 0 && fdPeak.Load() > fdBase+24 {
			t.Errorf("recording descriptors accumulated: baseline=%d peak=%d", fdBase, fdPeak.Load())
		}
		t.Logf("%s/%s n=%d bad=%v peak=%d bytes", route, kind, n, bad, peak.Load())
		return
	}
	routes := []string{"go", "typescript"}
	if refused {
		routes = []string{"typescript"}
	}
	for _, route := range routes {
		for _, n := range []int{32768, 65536} {
			kind := "traces"
			if route == "go" {
				kind = "attempts"
			}
			if refused {
				kind = "refused"
			}
			path := filepath.Join(t.TempDir(), "recording.json")
			gate569Record(t, path, kind, n, gate569Padding)
			badCases := []bool{false, true}
			if refused {
				badCases = []bool{false}
			}
			for _, bad := range badCases {
				t.Run(fmt.Sprintf("%s/%d/bad=%v", route, n, bad), func(t *testing.T) {
					exe, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()
					cmd := exec.CommandContext(ctx, exe, "-test.run=^"+strings.Split(t.Name(), "/")[0]+"$", "-test.v", "-test.count=1", "-test.timeout=4m30s")
					cmd.Env = append(os.Environ(), "INVARIANT_569_MEASURE=1", "INVARIANT_569_COUNT="+strconv.Itoa(n), "INVARIANT_569_ROUTE="+route, "INVARIANT_569_BAD="+strconv.FormatBool(bad), "INVARIANT_569_INPUT="+path, "GOMAXPROCS=1", "GOGC=100", "GOMEMLIMIT=off")
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("measured gate: %v\n%s", err, out)
					}
					t.Logf("%s", out)
				})
			}
		}
	}
}

func TestGateRecordingPeakHeap(t *testing.T)        { gate569Memory(t, false) }
func TestGateRefusedRecordingPeakHeap(t *testing.T) { gate569Memory(t, true) }

func TestGateRecordingOwnershipAndCompatibility(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, route := range []string{"go", "typescript", "python", "existing"} {
		t.Run(route, func(t *testing.T) {
			kind := "traces"
			if route == "go" {
				kind = "attempts"
			}
			input := filepath.Join(t.TempDir(), "input.json")
			gate569Record(t, input, kind, 4, 8)
			for _, mode := range []string{"ok", "late-bad", "build-failed", "driver-failed", "missing", "empty", "malformed", "stage-error", "cancel-producer", "cancel-wait", "cancel-check", "unrelated-error"} {
				if mode == "unrelated-error" && route != "go" || mode == "build-failed" && route == "existing" {
					continue
				}
				t.Run(mode, func(t *testing.T) {
					p, tc, events, tmp := gate569Setup(t, route, mode, input, 4, kind, mode == "late-bad")
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					var joined chan struct{}
					if strings.HasPrefix(mode, "cancel-") {
						joined = make(chan struct{})
						go func() {
							defer close(joined)
							for {
								select {
								case <-ctx.Done():
									return
								default:
								}
								if _, err := os.Stat(filepath.Join(events, "cancel-ready")); err == nil {
									cancel()
									return
								}
								time.Sleep(time.Millisecond)
							}
						}()
					}
					r, err := Run(ctx, p.Dir, "", tc, Options{TLCParallel: 1})
					if joined != nil {
						cancel()
						<-joined
					}
					gate569Clean(t, tmp)
					if strings.HasPrefix(mode, "cancel-") {
						_, reached := os.Stat(filepath.Join(events, "cancel-ready"))
						if err == nil || reached != nil {
							t.Fatalf("cancellation: %+v %v", r, err)
						}
						return
					}
					if mode == "stage-error" || mode == "unrelated-error" {
						if err == nil {
							t.Fatalf("early operational failure disappeared: %+v", r)
						}
						return
					}
					if err != nil || r == nil {
						t.Fatalf("gate: %+v %v", r, err)
					}
					switch mode {
					case "ok", "late-bad":
						gate569Result(t, r, 4, kind, route, mode == "late-bad")
					case "build-failed":
						if r.Passed || r.Build.Passed || r.Conformance == nil || !r.Conformance.Passed {
							t.Fatalf("independent build failure: %+v", r)
						}
					case "missing", "driver-failed":
						if r.Passed {
							t.Fatal("missing/failed producer passed")
						}
						if route == "go" {
							if r.Agreement == nil || r.Agreement.Passed || r.Conformance != nil {
								t.Fatalf("missing Go agreement evidence: %+v", r)
							}
							want := "the explorer's attempts weren't written:"
							if mode == "driver-failed" {
								want = "couldn't explore the implementation"
							}
							if !strings.Contains(r.Agreement.Message, want) {
								t.Fatal(r.Agreement.Message)
							}
						} else if r.Conformance == nil || r.Conformance.Passed || r.Conformance.Message != "the conformance driver failed:\ndriver exploded" {
							t.Fatalf("driver diagnostic: %+v", r.Conformance)
						}
					case "empty", "malformed":
						if r.Passed || r.Conformance == nil || r.Conformance.Passed || r.Conformance.Message == "" || strings.Contains(r.Conformance.Message, "driver failed") {
							t.Fatalf("present invalid recording treated as absent: %+v", r)
						}
					}
					if mode == "ok" || mode == "late-bad" || mode == "build-failed" {
						if _, err := os.Stat(filepath.Join(events, "recording-live")); err != nil {
							t.Error("producer's owned recording was removed before conformance consumption")
						}
						if b, err := os.ReadFile(filepath.Join(events, "recording-missing")); err == nil {
							t.Errorf("recording unavailable: %s", b)
						}
					}
				})
			}
			t.Run("direct bytes", func(t *testing.T) {
				p, tc, _, tmp := gate569Setup(t, route, "ok", input, 4, kind, false)
				lang, err := languageFor(p, tc)
				if err != nil {
					t.Fatal(err)
				}
				b, e, err := lang.Check(context.Background(), p)
				want, _ := os.ReadFile(input)
				if err != nil || !b.Passed || string(e.Traces) != string(want) {
					t.Fatalf("direct Check lost byte evidence: build=%+v bytes=%d err=%v", b, len(e.Traces), err)
				}
				gate569Clean(t, tmp)
			})
		})
	}
	t.Run("agreement alone", func(t *testing.T) {
		p, tc, events, tmp := gate569Setup(t, "agreement", "ok", "", 4, "traces", false)
		r, err := Run(context.Background(), p.Dir, "", tc, Options{TLCParallel: 1})
		if err != nil || !r.Passed || r.Conformance != nil || r.Agreement == nil || !r.Agreement.Passed {
			t.Fatalf("agreement-only gate: %+v %v", r, err)
		}
		if _, err := os.Stat(filepath.Join(events, "recording-live")); !os.IsNotExist(err) {
			t.Fatal("agreement unexpectedly consumed recording")
		}
		gate569Clean(t, tmp)
	})
}
