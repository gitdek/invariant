package verify

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/toolchain"
)

// NaginiHeader marks a Python file for Nagini. Files without it are plain
// Python: they run, but nothing proves them, and the receipt says so.
const NaginiHeader = "# +nagini"

// naginiRuntime holds the stand-in for nagini_contracts that Python runs
// with, so a proved core runs unchanged.
//
//go:embed all:naginiruntime
var naginiRuntime embed.FS

// naginiSources splits a package's Python files into those Nagini proves and
// the rest. Tests are left out.
func naginiSources(pkg string) (proved, plain []string, err error) {
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".py") || strings.HasPrefix(n, "test_") || strings.HasSuffix(n, "_test.py") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(pkg, n))
		if err != nil {
			return nil, nil, err
		}
		if hasLine(string(b), NaginiHeader) {
			proved = append(proved, n)
		} else {
			plain = append(plain, n)
		}
	}
	sort.Strings(proved)
	sort.Strings(plain)
	return proved, plain, nil
}

func hasLine(src, want string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// runNagini proves each marked file, with no network, in the image acquire
// gives, and names the functions it proved and the ones it never saw. Nagini
// proves a file whole, so the gate first reads each one for arithmetic the
// pinned Nagini models apart from Python, by the rule an in-place proof
// applies (#359), and refuses one that holds any before Nagini runs: the
// code fails, saying where and why, and Nagini never sees the file (#380).
// The image is acquired only once a file needs Nagini.
func runNagini(ctx context.Context, acquire func(context.Context) (string, error), pkg string, proved, plain []string) (*Code, error) {
	work, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := copyTree(pkg, work); err != nil {
		return nil, err
	}
	code := &Code{Verifier: "Nagini", Passed: true}
	image := ""
	for _, f := range proved {
		// The copy holds the very bytes Nagini would read.
		src, err := os.ReadFile(filepath.Join(work, f))
		if err != nil {
			return nil, err
		}
		fns, contracts := pythonFunctions(string(src))
		refused, err := naginiRefusals(f, src)
		if err != nil {
			return nil, err
		}
		if len(refused) > 0 {
			code.Passed = false
			code.Errors = append(code.Errors, refused...)
			code.Unverified = append(code.Unverified, fns...)
			continue
		}
		if image == "" {
			if image, err = acquire(ctx); err != nil {
				return nil, err
			}
		}
		code.Functions = append(code.Functions, fns...)
		code.Contracts = append(code.Contracts, contracts...)

		var out bytes.Buffer
		cmd := toolchain.Docker(ctx, "run", "--rm", "--network", "none", "--platform", "linux/amd64",
			"-v", work+":/work", "-w", "/work", image, f)
		cmd.Stdout, cmd.Stderr = &out, &out
		exit := 0
		if err := cmd.Run(); err != nil {
			var e *exec.ExitError
			if !errors.As(err, &e) {
				return nil, err
			}
			exit = e.ExitCode()
		}
		if exit != 0 || !strings.Contains(out.String(), "Verification successful") {
			code.Passed = false
			code.Errors = append(code.Errors, naginiErrors(f, out.String())...)
		}
	}
	for _, f := range plain {
		src, err := os.ReadFile(filepath.Join(pkg, f))
		if err != nil {
			return nil, err
		}
		fns, _ := pythonFunctions(string(src))
		code.Unverified = append(code.Unverified, fns...)
	}
	return code, nil
}

// naginiRefusals reads a marked file with the host's isolated Python, which
// parses it and never runs it, and returns, as file:line: why, each place
// it holds arithmetic the pinned Nagini models apart from Python (#380): in
// any function or method, and in the module's and its classes' own
// statements, which Nagini reads too. That's the rule an in-place proof
// applies (#359), except that Acc may take a permission fraction, a
// positive int literal over one at least as large, as Acc(x, 1/2), which
// Nagini reads as a permission and never as Python's division. A file the
// host's Python can't parse is refused too, since the gate couldn't check
// it.
func naginiRefusals(file string, src []byte) ([]string, error) {
	var reply struct {
		Arithmetic []pythonProofDynamic `json:"arithmetic"`
		Unreadable string               `json:"unreadable"`
	}
	request := map[string]string{"marked": base64.StdEncoding.EncodeToString(src),
		"standin": base64.StdEncoding.EncodeToString(naginiStandInSource)}
	if err := runPythonProofAnalyzer(request, &reply); err != nil {
		return nil, err
	}
	if reply.Unreadable != "" {
		return []string{fmt.Sprintf("%s: refused before Nagini runs: the host's python3 can't parse it, so the gate can't check its arithmetic: %s", file, reply.Unreadable)}, nil
	}
	refused := make([]string, 0, len(reply.Arithmetic))
	for _, p := range reply.Arithmetic {
		refused = append(refused, fmt.Sprintf("%s:%d: refused before Nagini runs: the line %s", file, p.Line, p.What))
	}
	return refused, nil
}

// naginiError is one of Nagini's errors: the message, then where it is, as
// "(core.py@78.12--78.38)."
var naginiError = regexp.MustCompile(`^(.*?)\s*\(([\w./-]+\.py)@(\d+)\.(\d+)--\d+\.\d+\)\.?$`)

// naginiErrors reads the errors Nagini lists after "Errors:", one per line,
// as file:line:col: message. The branch conditions it adds under an error
// are left out.
func naginiErrors(file, out string) []string {
	var errs []string
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Errors:" {
			continue
		}
		branch := false
		for _, e := range lines[i+1:] {
			if strings.TrimSpace(e) == "" || strings.HasPrefix(e, "Verification took") {
				break
			}
			if strings.HasPrefix(e, "Branch conditions:") {
				branch = true
				continue
			}
			if branch && (e[0] == ' ' || e[0] == '\t') {
				continue
			}
			branch = false
			e = strings.TrimSpace(e)
			if m := naginiError.FindStringSubmatch(e); m != nil {
				e = m[2] + ":" + m[3] + ":" + m[4] + ": " + m[1]
			}
			errs = append(errs, e)
		}
	}
	if len(errs) == 0 {
		errs = []string{file + ": Nagini didn't verify it:\n" + lastLines(out, 15)}
	}
	return errs
}

var (
	pyClass = regexp.MustCompile(`^class (\w+)`)
	pyDef   = regexp.MustCompile(`^(\s*)def (\w+)\(`)
)

// pythonFunctions names a module's top-level functions and its classes'
// methods (Class.method), and those whose bodies state a contract. Functions
// nested inside others belong to them and aren't listed.
func pythonFunctions(src string) (all, withContract []string) {
	lines := strings.Split(src, "\n")
	class := ""
	for i, line := range lines {
		if m := pyClass.FindStringSubmatch(line); m != nil {
			class = m[1]
			continue
		}
		m := pyDef.FindStringSubmatch(line)
		if m == nil {
			if line != "" && line[0] != ' ' && line[0] != '#' && line[0] != '\t' {
				class = ""
			}
			continue
		}
		name := m[2]
		switch {
		case m[1] == "":
			class = ""
		case class != "" && len(m[1]) == 4:
			name = class + "." + name
		default:
			continue
		}
		all = append(all, name)
		for _, body := range lines[i+1:] {
			if pyDef.MatchString(body) || pyClass.MatchString(body) {
				break
			}
			if strings.Contains(body, "Requires(") || strings.Contains(body, "Ensures(") {
				withContract = append(withContract, name)
				break
			}
		}
	}
	return all, withContract
}

// writeNaginiRuntime writes the nagini_contracts stand-in under dir.
func writeNaginiRuntime(dir string) error {
	return fs.WalkDir(naginiRuntime, "naginiruntime", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("naginiruntime", path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := naginiRuntime.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
