package plumbing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Sandbox runs a checkout's checks on a throwaway copy of it, in the
// pinned Go image, with no network and none of the host's environment
// (D-0014). The host's module cache is mounted read-only, so the
// repository's own dependencies build offline.
type Sandbox struct {
	Image    string // the Go image, pinned by digest
	ModCache string // the host's module cache, as go env GOMODCACHE
	// Cache is a build cache kept between one build's runs, so each run
	// doesn't compile everything again. Empty, each run starts cold.
	Cache string
}

// Result is what one run found.
type Result struct {
	Gofmt    []string        // files gofmt would change
	Vet      bool            // go vet passed
	Tests    bool            // the tests of every package the change touches passed
	Accepted map[string]bool // each acceptance test, as file:Name, and whether it passed
	Output   string          // the whole run, for the transcript and the agent
}

// Passed says whether everything passed.
func (r Result) Passed() bool {
	if len(r.Gofmt) > 0 || !r.Vet || !r.Tests {
		return false
	}
	for _, ok := range r.Accepted {
		if !ok {
			return false
		}
	}
	return true
}

// Failing is every acceptance test that didn't pass.
func (r Result) Failing() []string {
	var out []string
	for t, ok := range r.Accepted {
		if !ok {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

const script = `cd /src
for f in $GOFMT_FILES; do [ -f "$f" ] && gofmt -l "$f" | sed 's/^/@@gofmt /'; done
go vet ./... ; echo "@@invariant vet=$?"
if [ -n "$TEST_PKGS" ]; then go test -count=1 -timeout 20m $TEST_PKGS ; echo "@@invariant test=$?"; else echo "@@invariant test=0"; fi
for pkg in $ACCEPT_PKGS; do
  go test -count=1 -v -run "$ACCEPT_RUN" "./$pkg" ; echo "@@invariant accept=$?"
done
`

var (
	markerLine = regexp.MustCompile(`(?m)^@@invariant (\w+)=(\d+)$`)
	gofmtLine  = regexp.MustCompile(`(?m)^@@gofmt (.+)$`)
	testLine   = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): (Test\w+)`)
)

// Run copies the checkout at root, and runs gofmt, go vet, the tests of
// every package the change touches, and the plan's acceptance tests on the
// copy.
func (s Sandbox) Run(ctx context.Context, root string, plan *Plan) (Result, error) {
	work, err := os.MkdirTemp("", "invariant-plumbing-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	if err := copyCheckout(ctx, root, src); err != nil {
		return Result{}, err
	}
	var pkgs, names []string
	seen := map[string]bool{}
	for _, t := range plan.Tests {
		dir := path.Dir(t.File)
		if !seen[dir] {
			seen[dir] = true
			pkgs = append(pkgs, dir)
		}
		names = append(names, t.Name)
	}
	sort.Strings(pkgs)
	args := []string{"run", "--rm", "--network", "none", "--memory", "4g", "--pids-limit", "1024",
		"-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly", "-e", "GOPROXY=off", "-e", "HOME=/tmp",
		"-e", "CGO_ENABLED=0", "-e", "GOMODCACHE=/gomod", "-e", "GOCACHE=/gocache",
		"-e", "ACCEPT_PKGS=" + strings.Join(pkgs, " "), "-e", "ACCEPT_RUN=^(" + strings.Join(names, "|") + ")$",
		"-e", "GOFMT_FILES=" + strings.Join(goFiles(plan), " "), "-e", "TEST_PKGS=" + strings.Join(packages(src, plan), " "),
		"-v", src + ":/src", "-v", s.ModCache + ":/gomod:ro"}
	if s.Cache != "" {
		if err := os.MkdirAll(s.Cache, 0o777); err != nil {
			return Result{}, err
		}
		args = append(args, "-v", s.Cache+":/gocache")
	}
	args = append(args, "-w", "/src", s.Image, "sh", "-c", script)
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Result{}, fmt.Errorf("running the sandbox: %w", err)
		}
	}
	return parse(buf.String(), plan), nil
}

// packages is every Go package the change touches, as ./dir: each file's
// nearest directory holding Go files, so a change to a page a package
// embeds tests that package. CI runs every test before anything merges;
// the sandbox runs these, which is quick enough to build against.
func packages(src string, plan *Plan) []string {
	seen := map[string]bool{}
	var out []string
	for f := range plan.Touches(0) {
		for dir := path.Dir(f); ; dir = path.Dir(dir) {
			if matches, _ := filepath.Glob(filepath.Join(src, filepath.FromSlash(dir), "*.go")); len(matches) > 0 {
				if !seen[dir] {
					seen[dir] = true
					out = append(out, "./"+dir)
				}
				break
			}
			if dir == "." || dir == "/" {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// goFiles is every Go file the change touches, which gofmt checks.
func goFiles(plan *Plan) []string {
	var out []string
	for f := range plan.Touches(0) {
		if strings.HasSuffix(f, ".go") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// parse reads a run's output.
func parse(out string, plan *Plan) Result {
	r := Result{Output: out, Accepted: map[string]bool{}}
	for _, m := range gofmtLine.FindAllStringSubmatch(out, -1) {
		r.Gofmt = append(r.Gofmt, strings.TrimPrefix(m[1], "./"))
	}
	codes := map[string][]int{}
	for _, m := range markerLine.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[2])
		codes[m[1]] = append(codes[m[1]], n)
	}
	r.Vet = len(codes["vet"]) == 1 && codes["vet"][0] == 0
	r.Tests = len(codes["test"]) == 1 && codes["test"][0] == 0
	passed := map[string]string{}
	for _, m := range testLine.FindAllStringSubmatch(out, -1) {
		passed[m[2]] = m[1]
	}
	for _, t := range plan.Tests {
		// A test the run never reached, as when its package doesn't build,
		// didn't pass.
		r.Accepted[t.File+":"+t.Name] = passed[t.Name] == "PASS"
	}
	return r
}

// Summary says what a run found, for an agent and for a person.
func (r Result) Summary() string {
	var b strings.Builder
	switch {
	case r.Passed():
		b.WriteString("Everything passed: gofmt, go vet, the tests of every package the change touches, and every acceptance test.\n")
	default:
		if len(r.Gofmt) > 0 {
			fmt.Fprintf(&b, "gofmt would change: %s\n", strings.Join(r.Gofmt, ", "))
		}
		if !r.Vet {
			b.WriteString("go vet failed.\n")
		}
		if !r.Tests {
			b.WriteString("Some tests failed, or didn't build.\n")
		}
		if f := r.Failing(); len(f) > 0 {
			fmt.Fprintf(&b, "Acceptance tests that didn't pass: %s\n", strings.Join(f, ", "))
		}
	}
	return b.String()
}

// Tail is the end of a run's output, which is where failures show, at most
// n bytes.
func (r Result) Tail(n int) string {
	if len(r.Output) <= n {
		return r.Output
	}
	return "…\n" + r.Output[len(r.Output)-n:]
}

// copyCheckout copies a checkout's tracked files and new ones git doesn't
// ignore, which is everything the build could have written. A directory
// that isn't a checkout is copied whole, but for .git.
func copyCheckout(ctx context.Context, root, dst string) error {
	var names []string
	if exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-prefix").Run() == nil && isTop(ctx, root) {
		out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "-c", "-o", "--exclude-standard").Output()
		if err != nil {
			return fmt.Errorf("listing the checkout's files: %w", err)
		}
		names = strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	} else {
		all, err := walk(root)
		if err != nil {
			return err
		}
		for f := range all {
			names = append(names, f)
		}
	}
	for _, f := range names {
		if f == "" {
			continue
		}
		from := filepath.Join(root, filepath.FromSlash(f))
		info, err := os.Lstat(from)
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted in the checkout
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		to := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := copyFile(from, to, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// isTop says whether root is the top of a git checkout, rather than a
// directory inside some other one.
func isTop(ctx context.Context, root string) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	a, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	b, err2 := filepath.EvalSymlinks(root)
	return err1 == nil && err2 == nil && a == b
}

func copyFile(from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
