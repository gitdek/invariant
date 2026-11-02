package plumbing

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
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
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
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
	// Original is the checkout a build started from, which tells the tests
	// the build added from the ones it may not edit. Empty, gofmt checks
	// every test in the packages the change touches.
	Original string
	// Timeout and ShardTimeout may shorten the production limits, never
	// extend them. Zero uses the default.
	Timeout      time.Duration
	ShardTimeout time.Duration
}

const (
	SandboxShards           = 8
	SandboxTimeout          = 10 * time.Minute
	SandboxShardTimeout     = 8 * time.Minute
	SandboxStartupAllowance = time.Minute
	SandboxCleanupAllowance = 15 * time.Second
)

var sandboxDeadline = errors.New("sandbox deadline exceeded")

// TestResult identifies a test within its package and phase. Shard is 1..8
// for touched-package tests and zero for acceptance checks.
type TestResult struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	Shard   int    `json:"shard"`
}

// Result is what one run found.
type Result struct {
	Gofmt            []string        // files gofmt would change
	Vet              bool            // go vet passed
	Tests            bool            // the tests of every package the change touches passed
	Accepted         map[string]bool // each acceptance test, as file:Name, and whether it passed
	Output           string          // the whole run, for the transcript and the agent
	TestResults      []TestResult
	Failures         []string // build, shard and runner failures, retained apart from Output
	DeadlineExceeded bool
	Interrupted      bool
}

// Passed says whether everything passed.
func (r Result) Passed() bool {
	if len(r.Gofmt) > 0 || !r.Vet || !r.Tests || len(r.Failures) > 0 || r.DeadlineExceeded || r.Interrupted {
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

// script runs inside the container. Docker carries a container's stdout and
// stderr apart, and doesn't keep their order when it merges them, so the
// script starts by sending its stderr to its stdout: all of a step's output
// then comes before its marker (#124).
//
//go:embed sandbox_runner.py
var sandboxRunner string

var script = `exec 2>&1
cd /src
python3 -I -S -u - <<'INVARIANT_SANDBOX_RUNNER'
` + sandboxRunner + "\nINVARIANT_SANDBOX_RUNNER\n"

var (
	// A marker ends its line wherever it starts on it, after the last line
	// of a step whose output doesn't end with a newline.
	markerLine = regexp.MustCompile(`(?m)@@invariant (\w+)=(\d+)$`)
	gofmtLine  = regexp.MustCompile(`(?m)^@@gofmt (.+)$`)
	testLine   = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): (Test\w+)(?:\s|$)`)
	runnerLine = regexp.MustCompile(`(?m)^@@sandbox (\{[^\n]*\})$`)
)

// Run copies the checkout at root, and runs gofmt, go vet, the tests of
// every package the change touches, and the plan's acceptance tests on the
// copy.
func (s Sandbox) Run(ctx context.Context, root string, plan *Plan) (result Result, runErr error) {
	ctx, cancel := context.WithTimeoutCause(ctx, sandboxLimit(s.Timeout, SandboxTimeout), sandboxDeadline)
	defer cancel()
	defer func() {
		if ctx.Err() == nil {
			return
		}
		result.DeadlineExceeded = errors.Is(context.Cause(ctx), sandboxDeadline)
		result.Interrupted = !result.DeadlineExceeded
		if result.Accepted == nil {
			result.Accepted = map[string]bool{}
			for _, t := range plan.Tests {
				result.Accepted[t.File+":"+t.Name] = false
			}
		}
		// A deadline is a failed check, with whatever finished before it,
		// rather than a tool error which would discard the report.
		runErr = nil
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp("", "invariant-plumbing-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	if err := copyCheckout(ctx, root, src); err != nil {
		return Result{}, err
	}
	formatted, err := goFiles(ctx, s.Original, src, plan)
	if err != nil {
		return Result{}, err
	}
	accept := map[string][]string{}
	for _, t := range plan.Tests {
		dir := path.Dir(t.File)
		accept[dir] = append(accept[dir], t.Name)
	}
	pkgs, err := packages(ctx, src, plan)
	if err != nil {
		return Result{}, err
	}
	config, err := json.Marshal(struct {
		GofmtFiles   []string            `json:"gofmt_files"`
		Packages     []string            `json:"packages"`
		Accept       map[string][]string `json:"accept"`
		Shards       int                 `json:"shards"`
		ShardTimeout float64             `json:"shard_timeout"`
	}{formatted, pkgs, accept, SandboxShards, sandboxLimit(s.ShardTimeout, SandboxShardTimeout).Seconds()})
	if err != nil {
		return Result{}, err
	}
	// With no network, a container's own hostname isn't in its hosts file,
	// so git, building an identity for a commit, waited five seconds on DNS
	// for every one. The repository's tests make hundreds of commits, and a
	// run of them outlived the agent's 15-minute tool limit (#100). Named
	// localhost, the container resolves itself at once.
	args := []string{"run", "--rm", "--network", "none", "--hostname", "localhost", "--memory", "4g", "--pids-limit", "1024",
		"-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly -p=1", "-e", "GOMAXPROCS=2", "-e", "GOPROXY=off", "-e", "HOME=/tmp",
		"-e", "CGO_ENABLED=0", "-e", "GOMODCACHE=/gomod", "-e", "GOCACHE=/gocache",
		"-e", "SANDBOX_CONFIG=" + string(config),
		"-e", "GOFMT_FILES=" + strings.Join(formatted, " "), "-e", "TEST_PKGS=" + strings.Join(pkgs, " "),
		"-v", src + ":/src", "-v", s.ModCache + ":/gomod:ro"}
	// The code runs as the user who runs the sandbox, not as root, so what
	// it leaves in the build cache stays that user's to remove.
	if uid, gid := os.Getuid(), os.Getgid(); uid >= 0 && gid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}
	if s.Cache != "" {
		if err := os.MkdirAll(s.Cache, 0o777); err != nil {
			return Result{}, err
		}
		args = append(args, "-v", s.Cache+":/gocache")
	}
	args = append(args, "-w", "/src", s.Image, "sh", "-c", script)
	var buf bytes.Buffer
	cmd := toolchain.Docker(ctx, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	result = parse(buf.String(), plan)
	if err != nil {
		if ctx.Err() != nil {
			return result, nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return result, fmt.Errorf("running the sandbox: %w", err)
		}
		result.Failures = append(result.Failures, fmt.Sprintf("Sandbox exited unsuccessfully: %v", err))
	}
	return result, nil
}

func sandboxLimit(override, limit time.Duration) time.Duration {
	if override > 0 && override < limit {
		return override
	}
	return limit
}

// packages is every Go package the change touches, as ./dir: each file's
// nearest directory holding Go files in the candidate, so a change to a
// page a package embeds tests that package, and a package the change
// deletes isn't tested (#298). CI runs every test before anything merges;
// the sandbox runs these, which is quick enough to build against.
func packages(ctx context.Context, src string, plan *Plan) ([]string, error) {
	names, err := sandboxFiles(ctx, src)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, dir := range plan.packagesIn(names) {
		out = append(out, "./"+dir)
	}
	return out, nil
}

// goFiles checks named Go files, the acceptance tests and the regression
// tests the build added in the plan's packages. The staged checkout
// includes those tests without adding them to the ratified plan. A test
// the original checkout holds is one the build may not edit, so it never
// blocks a build (#298). Without the original checkout to tell them apart,
// every test in a touched package is checked.
func goFiles(ctx context.Context, original, src string, plan *Plan) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for f := range plan.Touches(0) {
		if strings.HasSuffix(f, ".go") {
			add(f)
		}
	}
	if original != "" {
		base, err := checkoutNames(ctx, original)
		if err != nil {
			return nil, err
		}
		names, err := sandboxFiles(ctx, src)
		if err != nil {
			return nil, err
		}
		// As staging admits them, against the same original inventory.
		for _, f := range plan.RegressionFiles(base, names) {
			add(f)
		}
		sort.Strings(out)
		return out, nil
	}
	pkgs, err := packages(ctx, src, plan)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, _ := filepath.Glob(filepath.Join(src, filepath.FromSlash(pkg), "*_test.go"))
		for _, name := range matches {
			info, err := os.Lstat(name)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			rel, _ := filepath.Rel(src, name)
			add(filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out, nil
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
	results := map[string]TestResult{}
	packages := map[string]string{}
	accepted := map[string]bool{}
	structured := false
	for _, m := range runnerLine.FindAllStringSubmatch(out, -1) {
		var event struct {
			TestResult
			Kind    string `json:"kind"`
			Dir     string `json:"dir"`
			Message string `json:"message"`
			Passed  bool   `json:"passed"`
		}
		if err := json.Unmarshal([]byte(m[1]), &event); err != nil {
			r.Failures = append(r.Failures, "Unreadable sandbox result: "+err.Error())
			continue
		}
		structured = true
		switch event.Kind {
		case "package":
			packages[path.Clean(event.Dir)] = event.Package
		case "test":
			key := event.Phase + "\x00" + event.Package + "\x00" + event.Name
			results[key] = event.TestResult
		case "accept":
			accepted[event.Package] = event.Passed
		case "failure":
			r.Failures = append(r.Failures, event.Message)
		}
	}
	var keys []string
	for key := range results {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r.TestResults = append(r.TestResults, results[key])
	}
	if structured {
		for _, t := range plan.Tests {
			pkg := packages[path.Dir(t.File)]
			got := results["accept\x00"+pkg+"\x00"+t.Name]
			r.Accepted[t.File+":"+t.Name] = accepted[pkg] && got.Status == "passed"
		}
		return r
	}
	// Older transcripts have only the text markers. Never use those names
	// to satisfy a structured run's package-qualified acceptance checks.
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
		if r.DeadlineExceeded {
			b.WriteString("Sandbox deadline exceeded; unfinished checks did not pass.\n")
		} else if r.Interrupted {
			b.WriteString("Sandbox interrupted; unfinished checks did not pass.\n")
		}
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
		for _, failure := range r.Failures {
			fmt.Fprintln(&b, failure)
		}
		for _, status := range []string{"passed", "failed", "skipped", "running", "not_run"} {
			var names []string
			for _, test := range r.TestResults {
				if test.Status != status {
					continue
				}
				name := test.Phase + " " + test.Package + "/" + test.Name
				if test.Shard > 0 {
					name += fmt.Sprintf(" (shard %d)", test.Shard)
				}
				names = append(names, name)
			}
			if len(names) == 0 {
				continue
			}
			label := status
			if status == "not_run" {
				label = "unstarted (not_run)"
			} else if status == "running" {
				label = "still-running"
			}
			fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(names, ", "))
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
		var err error
		names, err = sandboxFiles(ctx, root)
		if err != nil {
			return err
		}
	}
	for _, f := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		if err := copyFile(ctx, from, to, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Checkout preparation shares the run's deadline, including inventories of
// directories which are not git checkouts.
func sandboxFiles(ctx context.Context, root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
		} else if d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	return names, err
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

func copyFile(ctx context.Context, from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, sandboxReader{ctx, in}); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type sandboxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r sandboxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
