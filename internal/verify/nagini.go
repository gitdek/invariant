package verify

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NaginiHeader marks a Python file for Nagini. Files without it are plain
// Python: they run, but nothing proves them, and the receipt says so.
const NaginiHeader = "# +nagini"

// naginiRuntime holds the stand-in for nagini_contracts that Python runs
// with, so a proved core runs unchanged.
//
//go:embed naginiruntime
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

// runNagini proves each marked file in image, with no network, and names the
// functions it proved and the ones it never saw.
func runNagini(ctx context.Context, image, pkg string, proved, plain []string) (*Code, error) {
	work, err := os.MkdirTemp("", "invariant-nagini-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := copyTree(pkg, work); err != nil {
		return nil, err
	}
	code := &Code{Verifier: "Nagini", Passed: true}
	for _, f := range proved {
		src, err := os.ReadFile(filepath.Join(pkg, f))
		if err != nil {
			return nil, err
		}
		fns, contracts := pythonFunctions(string(src))
		code.Functions = append(code.Functions, fns...)
		code.Contracts = append(code.Contracts, contracts...)

		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64",
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
