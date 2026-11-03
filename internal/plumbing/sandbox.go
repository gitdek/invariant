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
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

const (
	SandboxShards             = 8
	SandboxTimeout            = 12 * time.Minute
	SandboxShardTimeout       = 10 * time.Minute
	SandboxVerifyTimeout      = 25 * time.Minute
	SandboxVerifyShardTimeout = 20 * time.Minute
	SandboxStartupAllowance   = time.Minute
	SandboxCleanupAllowance   = 15 * time.Second
)

//go:embed sandbox_runner.py
var sandboxRunner string

// Sandbox runs a checkout's checks on a throwaway copy of it, in the
// pinned Go image, with no network and none of the host's environment
// (D-0014). The host's module cache is mounted read-only, so the
// repository's own dependencies build offline.
type Sandbox struct {
	Image    string // the Go image, pinned by digest
	ModCache string // the host's module cache, as go env GOMODCACHE
	// Checks sends a build's checks to its watcher's queue. Direct sandbox
	// checks, including the real-image budget checks, leave it nil.
	Checks *CheckClient
	// Cache is a build cache kept between one build's runs, so each run
	// doesn't compile everything again. Empty, each run starts cold.
	Cache string
	// Original is the checkout a build started from, which tells the tests
	// the build added from the ones it may not edit. Empty, gofmt checks
	// every test in the packages the change touches.
	Original string
	// These overrides may shorten the production budgets, never extend them.
	Timeout      time.Duration
	ShardTimeout time.Duration
}

// TestResult identifies a test independently of other packages and phases.
// Status is passed, failed, skipped, running, or not_run; Shard identifies
// one of the eight queue workers in the ordinary test phase.
type TestResult struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	Shard   int    `json:"shard,omitempty"`
	File    string `json:"file,omitempty"`
}

// Result is what one run found.
type Result struct {
	Gofmt            []string        // files gofmt would change
	Vet              bool            // go vet passed
	Tests            bool            // the tests of every package the change touches passed
	Accepted         map[string]bool // each acceptance test, as file:Name, and whether it passed
	Output           string          // the whole run, for the transcript and the agent
	DeadlineExceeded bool
	Interrupted      bool
	TimedOut         bool // a phase exhausted its budget before the whole-run deadline
	TestResults      []TestResult
	Problems         []string // phase, build and worker failures retained in summaries
}

// Passed says whether everything passed.
func (r Result) Passed() bool {
	if r.DeadlineExceeded || r.Interrupted || r.TimedOut || len(r.Problems) > 0 || len(r.Gofmt) > 0 || !r.Vet || !r.Tests {
		return false
	}
	for _, ok := range r.Accepted {
		if !ok {
			return false
		}
	}
	for _, test := range r.TestResults {
		if test.Status != "passed" && test.Status != "skipped" {
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

var (
	// A marker ends its line wherever it starts on it, after the last line
	// of a step whose output doesn't end with a newline.
	markerLine = regexp.MustCompile(`(?m)@@invariant (\w+)=(\d+)$`)
	gofmtLine  = regexp.MustCompile(`(?m)^@@gofmt (.+)$`)
	testLine   = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): (Test\w+)`)
)

// Run copies the checkout at root, and runs gofmt, go vet, the tests of
// every package the change touches, and the plan's acceptance tests on the
// copy.
func (s Sandbox) Run(ctx context.Context, root string, plan *Plan) (Result, error) {
	if s.Checks != nil {
		return s.Checks.run(ctx, root)
	}
	return s.run(ctx, root, plan, time.Now())
}

func (s Sandbox) run(ctx context.Context, root string, plan *Plan, started time.Time) (result Result, runErr error) {
	// Discovery has the ordinary limit until ownership establishes the
	// exception. Both deadlines count from the same entry time.
	end := started.Add(sandboxLimit(s.Timeout, SandboxTimeout))
	parent := ctx
	ownDeadline := true
	if parentEnd, ok := ctx.Deadline(); ok && parentEnd.Before(end) {
		ownDeadline = false
	}
	ctx, cancel := context.WithDeadline(ctx, end)
	defer cancel()
	report := func() {
		if !ownDeadline && result.DeadlineExceeded {
			result.DeadlineExceeded = false
			result.Interrupted = true
		}
		if ctx.Err() != nil {
			if result.Accepted == nil {
				result = parse(result.Output, plan)
			}
			if ownDeadline && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result.DeadlineExceeded = true
			} else {
				result.Interrupted = true
			}
			// A deadline is a failed check with partial results, so the test
			// tool can deliver the report even when staging never finished.
			runErr = nil
		}
	}
	names, err := sandboxFiles(ctx, root)
	if err != nil {
		defer report()
		return Result{}, err
	}
	dirs := plan.packagesIn(names)
	if err := ctx.Err(); err != nil {
		defer report()
		return Result{}, err
	}
	timeout, shardTimeout := sandboxBudgets(dirs)
	end = started.Add(sandboxLimit(s.Timeout, timeout))
	if parentEnd, ok := parent.Deadline(); ok {
		ownDeadline = !parentEnd.Before(end)
	}
	ctx, cancelRun := context.WithDeadline(parent, end)
	cancel()
	defer cancelRun()
	defer report()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp("", "invariant-plumbing-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	if err := copyCheckoutFiles(ctx, root, src, names); err != nil {
		return Result{}, err
	}
	formatted, err := goFiles(ctx, s.Original, src, plan)
	if err != nil {
		return Result{}, err
	}
	var pkgs []string
	for _, dir := range dirs {
		pkgs = append(pkgs, "./"+dir)
	}
	// Stage the embedded runner separately from the checkout's code. The
	// config uses arrays, not shell words, for all paths and test names.
	runnerDir, err := os.MkdirTemp(src, ".invariant-runner-")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(runnerDir, "runner.py"), []byte(sandboxRunner), 0o600); err != nil {
		return Result{}, err
	}
	deadline, _ := ctx.Deadline()
	config := struct {
		Packages     []string `json:"packages"`
		Formatted    []string `json:"formatted"`
		Acceptance   []Test   `json:"acceptance"`
		Deadline     float64  `json:"deadline"`
		ShardTimeout float64  `json:"shard_timeout"`
		Workers      int      `json:"workers"`
	}{pkgs, formatted, plan.Tests, float64(deadline.UnixNano()) / 1e9, sandboxLimit(s.ShardTimeout, shardTimeout).Seconds(), SandboxShards}
	data, err := json.Marshal(config)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(runnerDir, "config.json"), data, 0o600); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// With no network, a container's own hostname isn't in its hosts file,
	// so git, building an identity for a commit, waited five seconds on DNS
	// for every one. The repository's tests make hundreds of commits, and a
	// run of them outlived the agent's 15-minute tool limit (#100). Named
	// localhost, the container resolves itself at once.
	args := []string{"run", "--rm", "--init", "--network", "none", "--hostname", "localhost", "--memory", "4g", "--pids-limit", "1024",
		"-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly -p=1", "-e", "GOMAXPROCS=2", "-e", "GOPROXY=off", "-e", "HOME=/tmp",
		"-e", "CGO_ENABLED=0", "-e", "GOMODCACHE=/gomod", "-e", "GOCACHE=/gocache",
		"-e", "GOFMT_FILES=" + strings.Join(formatted, " "), "-e", "TEST_PKGS=" + strings.Join(pkgs, " "),
		"-v", src + ":/src", "-v", s.ModCache + ":/gomod:ro"}
	// The code runs as the user who runs the sandbox, not as root, so what
	// it leaves in the build cache stays that user's to remove.
	if uid, gid := os.Getuid(), os.Getgid(); uid >= 0 && gid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}
	cache := s.Cache
	if cache == "" {
		cache = filepath.Join(work, "cache")
	}
	if err := os.MkdirAll(cache, 0o777); err != nil {
		return Result{}, err
	}
	args = append(args, "-v", cache+":/gocache")
	// Docker merges its two output streams without preserving their order.
	// The runner also escapes child diagnostics before emitting any records.
	rel := filepath.Base(runnerDir)
	script := "exec 2>&1\ncd /src || exit 1\nexec python3 " + rel + "/runner.py " + rel + "/config.json\n"
	args = append(args, "-w", "/src", s.Image, "sh", "-c", script)
	var buf bytes.Buffer
	cmd := toolchain.Docker(ctx, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	result = parse(buf.String(), plan)
	if result.TestResults == nil && len(markerLine.FindAllStringSubmatch(result.Output, -1)) == 0 {
		result.Interrupted = true
		result.Problems = append(result.Problems, "The sandbox exited before reporting any checks.")
	}
	if result.DeadlineExceeded && ctx.Err() == nil {
		// An inner deadline can finish just before the host's cancellation.
		// Remove the container in that case too, including any descendants.
		for i, arg := range cmd.Args {
			if arg == "--name" && i+1 < len(cmd.Args) {
				cleanup, finish := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
				toolchain.Docker(cleanup, "rm", "--force", cmd.Args[i+1]).Run()
				finish()
				break
			}
		}
	}
	if err != nil {
		result.Interrupted = true
		result.Problems = append(result.Problems, "The sandbox process failed: "+err.Error())
		var exit *exec.ExitError
		if ctx.Err() == nil && !errors.As(err, &exit) {
			return result, fmt.Errorf("running the sandbox: %w", err)
		}
	}
	return result, nil
}

func sandboxBudgets(dirs []string) (time.Duration, time.Duration) {
	if slices.Contains(dirs, "internal/verify") {
		return SandboxVerifyTimeout, SandboxVerifyShardTimeout
	}
	return SandboxTimeout, SandboxShardTimeout
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
	files, err := sandboxWalk(ctx, src)
	if err != nil {
		return nil, err
	}
	var names []string
	for f := range files {
		names = append(names, f)
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
		files, err := sandboxWalk(ctx, src)
		if err != nil {
			return nil, err
		}
		var names []string
		for f := range files {
			names = append(names, f)
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
	for _, t := range plan.Tests {
		r.Accepted[t.File+":"+t.Name] = false
	}
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
	structured, done := false, false
	results := map[string]TestResult{}
	for _, line := range strings.Split(out, "\n") {
		data, ok := strings.CutPrefix(line, "@@result ")
		if !ok {
			continue
		}
		var event struct {
			TestResult
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		structured = true
		switch event.Kind {
		case "done":
			done = true
		case "deadline":
			r.DeadlineExceeded = true
			if event.Phase != "" {
				r.Problems = append(r.Problems, "Sandbox deadline exceeded during "+event.Phase+".")
			}
		case "interrupted":
			r.Interrupted = true
		case "timeout":
			r.TimedOut = true
		case "problem":
			if event.Status == "timeout" {
				r.TimedOut = true
			}
			var details []string
			for _, part := range []string{event.Phase, event.Package, event.Name} {
				if part != "" {
					details = append(details, part)
				}
			}
			if event.Shard != 0 {
				details = append(details, fmt.Sprintf("shard %d", event.Shard))
			}
			details = append(details, event.Message)
			r.Problems = append(r.Problems, strings.Join(details, ": "))
		case "test":
			if event.Package == "" || event.Name == "" || (event.Phase != "test" && event.Phase != "accept") {
				continue
			}
			switch event.Status {
			case "passed", "failed", "skipped", "running", "not_run":
				key := event.Phase + "\x00" + event.Package + "\x00" + event.Name
				results[key] = event.TestResult
			}
		}
	}
	if structured {
		// The new runner's records are separate from all child diagnostics.
		// In particular, an ordinary pass cannot satisfy an acceptance replay.
		r.TestResults = make([]TestResult, 0, len(results))
		var keys []string
		for key := range results {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t := results[key]
			r.TestResults = append(r.TestResults, t)
			if t.Phase == "accept" {
				accept := t.File + ":" + t.Name
				if _, ok := r.Accepted[accept]; ok {
					r.Accepted[accept] = t.Status == "passed"
				}
			}
		}
		if !done && !r.DeadlineExceeded {
			r.Interrupted = true
		}
		return r
	}
	// Retain the old transcript format for saved results and legacy fixtures.
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

// summaryNamed is how many passed or skipped tests a summary names when its
// run didn't pass.
const summaryNamed = 100

// Summary says what a run found, for an agent and for a person. It names
// every test that didn't pass, first. A passing run counts its passed and
// skipped tests by phase instead of naming them: a large package has
// thousands, more than an agent's tool result or a pull request holds
// (#408). A run that didn't pass names them after the rest, up to
// summaryNamed. TestResults keeps every record.
func (r Result) Summary() string {
	var b strings.Builder
	switch {
	case r.Passed():
		b.WriteString("Everything passed: gofmt, go vet, the tests of every package the change touches, and every acceptance test.\n")
	default:
		if r.DeadlineExceeded {
			b.WriteString("The sandbox deadline expired; partial results follow.\n")
		}
		if r.Interrupted {
			b.WriteString("The sandbox was interrupted before all checks finished.\n")
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
	}
	for _, problem := range r.Problems {
		fmt.Fprintln(&b, problem)
	}
	name := func(t TestResult) {
		status := t.Status
		if status == "not_run" {
			status = "unstarted (not_run)"
		}
		fmt.Fprintf(&b, "%s %s/%s: %s", t.Phase, t.Package, t.Name, status)
		if t.Shard != 0 {
			fmt.Fprintf(&b, " (shard %d)", t.Shard)
		}
		b.WriteByte('\n')
	}
	var settled []TestResult
	for _, t := range r.TestResults {
		if t.Status == "passed" || t.Status == "skipped" {
			settled = append(settled, t)
		} else {
			name(t)
		}
	}
	if r.Passed() {
		var phases []string
		passed, skipped := map[string]int{}, map[string]int{}
		for _, t := range settled {
			if passed[t.Phase]+skipped[t.Phase] == 0 {
				phases = append(phases, t.Phase)
			}
			if t.Status == "passed" {
				passed[t.Phase]++
			} else {
				skipped[t.Phase]++
			}
		}
		for _, phase := range phases {
			fmt.Fprintf(&b, "%s: %d passed, %d skipped\n", phase, passed[phase], skipped[phase])
		}
		return b.String()
	}
	for i, t := range settled {
		if i == summaryNamed {
			fmt.Fprintf(&b, "…and %d more passed or skipped\n", len(settled)-i)
			break
		}
		name(t)
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
	names, err := sandboxFiles(ctx, root)
	if err != nil {
		return err
	}
	return copyCheckoutFiles(ctx, root, dst, names)
}

// sandboxFiles is the regular-file inventory both ownership and copying use.
func sandboxFiles(ctx context.Context, root string) ([]string, error) {
	var names []string
	if exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-prefix").Run() == nil && isTop(ctx, root) {
		out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "-c", "-o", "--exclude-standard").Output()
		if err != nil {
			return nil, fmt.Errorf("listing the checkout's files: %w", err)
		}
		names = strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	} else {
		all, err := sandboxWalk(ctx, root)
		if err != nil {
			return nil, err
		}
		for f := range all {
			names = append(names, f)
		}
	}
	var regular []string
	for _, f := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			regular = append(regular, f)
		}
	}
	return regular, nil
}

func copyCheckoutFiles(ctx context.Context, root, dst string, names []string) error {
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
		if err := copyFileContext(ctx, from, to, info.Mode().Perm()); err != nil {
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
	return copyFileContext(context.Background(), from, to, perm)
}

func copyFileContext(ctx context.Context, from, to string, perm os.FileMode) error {
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
	io.Reader
}

func (r sandboxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func sandboxWalk(ctx context.Context, dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
		} else if entry.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, name)
			files[filepath.ToSlash(rel)] = name
		}
		return nil
	})
	return files, err
}
