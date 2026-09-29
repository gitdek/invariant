// Package gobra runs the Gobra verifier on a Go package and reads its result.
package gobra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Result is what one Gobra run established.
type Result struct {
	Passed bool
	Errors []string // "twophase.go:97:9: Postcondition might not hold."
	// Functions lists every function in the package, and Contracts the ones
	// that carry a Gobra contract. Gobra checks all of them, including the
	// bounds of every index, whether or not they carry a contract.
	Functions []string
	Contracts []string
	// Unverified lists functions in files without the // +gobra header. Gobra
	// never sees them; the receipt says so.
	Unverified []string
	Overflow   bool // integer overflow was checked too
}

var (
	errorLine = regexp.MustCompile(`Error at: </work/([^>]+)> (.*)`)
	summary   = regexp.MustCompile(`Gobra found (\d+) errors?`)
)

// Parse reads Gobra's output.
func Parse(out string, exitCode int) Result {
	var r Result
	for _, m := range errorLine.FindAllStringSubmatch(out, -1) {
		r.Errors = append(r.Errors, m[1]+": "+strings.TrimSpace(m[2]))
	}
	s := summary.FindStringSubmatch(out)
	found := -1
	if s != nil {
		found, _ = strconv.Atoi(s[1])
	}
	r.Passed = exitCode == 0 && found == 0
	if !r.Passed && len(r.Errors) == 0 {
		r.Errors = []string{"Gobra did not report a result:\n" + lastLines(out, 15)}
	}
	return r
}

// Header marks a file for Gobra. When any file in a package carries it,
// Gobra verifies only those files; the rest are plain Go, such as the
// exploration code the agreement check runs.
const Header = "// +gobra"

// Timeout caps one Gobra run. A proof that doesn't finish by then fails,
// rather than holding up the gate, and whoever called it, for good: one
// that looped held an agent's gate call for most of an hour.
var Timeout = 20 * time.Minute

// Run verifies the package in dir with the Gobra image, which is pinned by
// digest and published for linux/amd64 only. It runs with no network, and
// for Timeout at most.
func Run(ctx context.Context, image, dir string, overflow bool) (Result, error) {
	files, skipped, err := sources(dir)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	args := []string{"run", "--rm", "--network", "none", "--platform", "linux/amd64", "-v", dir + ":/work:ro", image}
	if overflow {
		args = append(args, "--overflow")
	}
	args = append(args, "-i")
	for _, f := range files {
		args = append(args, "/work/"+f)
	}
	var out bytes.Buffer
	cmd := toolchain.Docker(ctx, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("Gobra didn't finish within %s: simplify the contracts, or split the function whose proof runs long", Timeout)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Result{}, fmt.Errorf("running Gobra: %w", err)
		}
		code = exit.ExitCode()
	}
	r := Parse(out.String(), code)
	r.Overflow = overflow
	if r.Functions, r.Contracts, err = functions(dir, files); err != nil {
		return r, err
	}
	r.Unverified, _, err = functions(dir, skipped)
	return r, err
}

// sources splits the package's non-test Go files into the ones Gobra
// verifies and the ones it skips.
func sources(dir string) (verified, skipped []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var all []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			all = append(all, n)
		}
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("no Go files in %s", dir)
	}
	sort.Strings(all)
	for _, n := range all {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, nil, err
		}
		if hasHeader(string(b)) {
			verified = append(verified, n)
		} else {
			skipped = append(skipped, n)
		}
	}
	if len(verified) == 0 {
		return all, nil, nil
	}
	return verified, skipped, nil
}

func hasHeader(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == Header {
			return true
		}
	}
	return false
}

// functions names every function declared in files, and those whose doc
// comment carries a Gobra annotation: a line starting //@, or // @ once
// gofmt has reformatted it. Gobra reads both.
func functions(dir string, files []string) (all, withContract []string, err error) {
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			all = append(all, fn.Name.Name)
			if fn.Doc != nil {
				for _, c := range fn.Doc.List {
					if strings.HasPrefix(c.Text, "//@") || strings.HasPrefix(c.Text, "// @") {
						withContract = append(withContract, fn.Name.Name)
						break
					}
				}
			}
		}
	}
	return all, withContract, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
